# Rekey credentials from the zero dev-key — Azure & GCP only

## Context

Before the credential-encryption-key fix landed, Azure Container Apps and GCP
Cloud Run deployments silently used the all-zero AES-256 dev key for every
tenant credential write. The Go code read `CREDENTIAL_ENCRYPTION_KEY_SECRET_ARN`
but Terraform for those clouds wrote `_SECRET_NAME` (Azure) and `_SECRET_ID`
(GCP), so the loader never matched and fell through to the dev key.

After deploying the fix, the service loads the real key from each cloud's
secret store. Existing rows in `account_credentials` remain encrypted under
the zero key and **cannot be decrypted** by the new service until they are
re-encrypted under the real key. This runbook walks operators through that
one-shot migration.

> **AWS deployments do not need this** — the env var name matched, so AWS
> rows have always been encrypted under the real key.

## Migration window — read this first

Between PR deploy (step 1) and rekey completion (step 3), any read of an
existing tenant credential will fail with a decrypt error. The service stays
up; only credential-touching operations (recommendation listing for
onboarded accounts, scheduled purchases, etc.) on Azure/GCP are affected.
Schedule the run during a low-traffic window. For dev-stage Azure/GCP
deployments the impact is expected to be small; confirm with the operator
before proceeding.

`CREDENTIAL_ENCRYPTION_ALLOW_DEV_KEY=1` is **not** part of this migration —
it exists only for local dev. Do not set it in any deployed environment.

## Pre-flight checklist

- [ ] PR fixing `loadKey` to read the per-cloud env vars is **deployed and
      live** — confirm by tailing logs for a startup line of the form
      `credentials: loaded encryption key via CREDENTIAL_ENCRYPTION_KEY_SECRET_NAME`
      (Azure) or `…_SECRET_ID` (GCP). The env var name in that line MUST NOT
      be `CREDENTIAL_ENCRYPTION_KEY` (raw hex, misconfiguration) or
      `CREDENTIAL_ENCRYPTION_ALLOW_DEV_KEY` (dev fallback engaged).
- [ ] `/health` returns `credential_store: { status: "healthy" }` — confirms
      the real key passes the round-trip self-check.
- [ ] You have the same `DATABASE_*` and `CREDENTIAL_ENCRYPTION_KEY_SECRET_*`
      env vars the service uses, scoped to a one-off job runner inside the
      same network as the DB (see step 2).

## Steps

### 1. Deploy the fix normally

Merge the security PR and let CI deploy. No special flags. The service starts
with the real key. Existing zero-key rows remain in the DB and fail to
decrypt on read, but the service otherwise runs.

### 2. Run `cmd/rekey` as a one-off job, in-cluster

Build the binary into the same container image used for production (or run
via `go run ./cmd/rekey`). Launch as a short-lived job in the same
network/identity as the running service so it can reach Postgres and the
secret store:

- **Azure**: Container Apps job, same identity + Key Vault access as the
  main app.
- **GCP**: Cloud Run Job, same service account as the main app.
- **AWS**: not needed (was never broken).

Required env vars on the job:

```bash
CUDLY_REKEY_FROM_ZERO_KEY=1                         # safety gate
CREDENTIAL_ENCRYPTION_KEY_SECRET_NAME=<name>        # Azure  (or)
CREDENTIAL_ENCRYPTION_KEY_SECRET_ID=<id>            # GCP
SECRET_PROVIDER=azure                               # (or gcp)
DATABASE_HOST=...                                   # same as service
DATABASE_USER=...
DATABASE_NAME=...
DATABASE_PASSWORD_SECRET=...                        # if used
AZURE_KEY_VAULT_URL=...                             # Azure only
GCP_PROJECT_ID=...                                  # GCP only
```

Capture the final log line, which has the form:

```text
rekey: scanned=N re_keyed=N skippedAlreadyReal=N errored=0
```

### 3. Verify

- `errored == 0` — every row processed.
- `scanned == reKeyed + skippedAlreadyReal` — no rows were missed.
- Spot-check one onboarded tenant in the dashboard: their AWS/Azure/GCP
  account should list recommendations without an "invalid ciphertext" error.

If `errored > 0`, do not retry blindly. Inspect the logged row IDs (no
plaintext is logged) and resolve manually before re-running.

### 4. No redeploy required

The service has been running with the real key since step 1; the rekey job
just transformed at-rest data.

### 5. Post-flight verification

- `/health` still returns `credential_store: { status: "healthy" }`.
- Application logs no longer show "decrypt: ..." ERROR lines on credential
  reads (these were the symptom of pre-rekey state).

## Operational security

- The job decrypts plaintext credentials in memory for the duration of one
  row's transaction. It never writes plaintext to logs or stdout.
- Run the job in an ephemeral container that is torn down immediately after
  completion. Do not leave the job spec lying around with
  `CUDLY_REKEY_FROM_ZERO_KEY=1` set.
- The runtime may surface env var values in invocation history (Azure
  Activity log, GCP Cloud Run Jobs console, AWS CloudWatch). Only
  `CUDLY_REKEY_FROM_ZERO_KEY=1` should be visible there as an out-of-band
  flag — the secret name/ID itself is identical to what the production
  service exposes already.

## Idempotency

The job is safe to rerun. Real-key rows fail the zero-key Decrypt check
(AES-GCM authentication tag mismatch) and fall into the
`skippedAlreadyReal` bucket. The second run reports
`reKeyed=0 skippedAlreadyReal=N`.

## Troubleshooting

- **`real key is the all-zero dev key — refusing to rekey`**: the job
  loaded the zero key as the "real" key, which means the production env
  vars are still misconfigured. Re-check step 1's pre-flight log line.
- **`failed to load credential encryption key: …no credential encryption
  key configured`**: the job's env vars don't match the production service.
  Re-export `CREDENTIAL_ENCRYPTION_KEY_SECRET_*` and `SECRET_PROVIDER`.
- **Many `errored` rows**: usually a DB transient. Wait, then rerun — the
  re-keyed rows from the partial run land in `skippedAlreadyReal` next time.

## Rollback

There is no rollback. Once a row is encrypted under the real key, the zero
key cannot decrypt it. If something is wrong with the real key itself,
restore from a DB snapshot taken before the migration window.
