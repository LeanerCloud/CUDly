# Secret-free Workload Identity Federation

## Scope

The immediate trigger is the Azure target flow, but the same approach is used
for **all three source clouds** CUDly can be deployed on:

- CUDly on **AWS** → signs with AWS KMS asymmetric key
- CUDly on **Azure** → signs with Azure Key Vault key
- CUDly on **GCP** → signs with GCP Cloud KMS asymmetric key

The runtime `Signer` interface is the same; the HTTP `.well-known/*`
endpoints and the JWT assertion flow are cloud-agnostic. Only the signing
backend and the deployment infrastructure differ per cloud.

## Problem

The existing `workload_identity_federation` path for Azure is misnamed: it's
actually certificate-based client-assertion auth. The flow is:

1. `azure-wif-cli.sh` generates an RSA keypair + self-signed X.509 cert locally.
2. Uploads the public cert to the Azure AD App Registration via
   `az ad app credential reset --cert @...`.
3. Asks the operator to paste the private-key PEM into CUDly, which stores it
   as `azure_wif_private_key` (encrypted at rest).
4. At each Azure API call, CUDly mints a JWT signed with that stored private
   key, embeds the cert's x5t thumbprint, and presents it as `client_assertion`
   to `https://login.microsoftonline.com/{tenant}/oauth2/v2.0/token`.

The private key is a long-lived secret that CUDly owns and can leak. This is
exactly what WIF is supposed to eliminate.

## Target

True Azure federation, where CUDly stores **no** Azure-specific secret:

1. CUDly's own AWS Lambda exposes OIDC discovery + JWKS endpoints
   (`/.well-known/openid-configuration`, `/.well-known/jwks.json`) on its
   Function URL. The JWKS publishes the public half of an AWS KMS asymmetric
   signing key (RSA_2048, SIGN_VERIFY). The private half never leaves KMS.
2. The Azure AD App Registration gets a **federated identity credential**
   (`az ad app federated-credential create`) pointing at CUDly's OIDC issuer
   with `subject=cudly-controller` and `audience=api://AzureADTokenExchange`.
3. At each Azure API call, CUDly mints a JWT with
   `iss = <cudly-url>`, `sub = cudly-controller`,
   `aud = api://AzureADTokenExchange`, signs it via `kms:Sign`, and presents
   it as `client_assertion`. Azure verifies the signature against the JWKS
   it fetches from the CUDly issuer, matches it against the federated
   credential entry, and issues a short-lived access token.

No PEMs, no passwords, no symmetric secrets.

## Architecture

```text
 ┌──────────────────────────────── AWS ─────────────────────────────────┐
 │                                                                      │
 │  ┌────────────────────┐   kms:Sign    ┌────────────────────────┐     │
 │  │ CUDly Lambda       │◀─────────────▶│ KMS asymmetric         │     │
 │  │ (api handler)      │  GetPublicKey │ (RSA_2048 SIGN_VERIFY) │     │
 │  └────────────────────┘               └────────────────────────┘     │
 │          │                                                           │
 │          │  serves on Function URL:                                  │
 │          ▼    GET /.well-known/openid-configuration                  │
 │          ▼    GET /.well-known/jwks.json                             │
 │                                                                      │
 └──────────────────────────────────────────────────────────────────────┘
            ▲
            │ Azure AD fetches JWKS to verify client_assertion
            │
 ┌──────────┴───────────────────── Azure ──────────────────────────────┐
 │                                                                      │
 │  Azure AD App Registration                                           │
 │   └── Federated identity credential                                  │
 │        issuer  = https://<cudly-lambda-url>                          │
 │        subject = cudly-controller                                    │
 │        audience = api://AzureADTokenExchange                         │
 │                                                                      │
 │  Token endpoint: /oauth2/v2.0/token                                  │
 │  ← client_assertion=<JWT signed by KMS>                              │
 │  → access_token                                                      │
 │                                                                      │
 └──────────────────────────────────────────────────────────────────────┘
```

## Implementation plan

One commit per layer, each independently reviewable.

1. **`specs/azure-wif-redesign.md`** (this file).
2. **`terraform/modules/compute/aws/lambda/signing-key.tf`** — new KMS
   asymmetric key, alias, IAM policy on the Lambda role for `kms:Sign` /
   `kms:GetPublicKey`, new Lambda env var `CUDLY_SIGNING_KEY_ID`. **DONE**.
3. **`internal/oidc/signer.go`** — cloud-agnostic `Signer` interface:
   - `Sign(ctx, digest []byte) ([]byte, error)` — raw RSA-PKCS1v15 over
     SHA-256 digest. Called by the JWT minter.
   - `PublicKey(ctx) (*rsa.PublicKey, error)` — used to build the JWK and
     to compute a stable `kid`.
   - `Algorithm() string` — currently always `RS256`.
   Backed by three implementations:
   - `AWSKMSSigner` — `aws-sdk-go-v2/service/kms`, `kms:Sign` with
     `RSASSA_PKCS1_V1_5_SHA_256`, `kms:GetPublicKey` once at startup.
   - `AzureKeyVaultSigner` — `azkeys.Client.Sign` with `RS256`,
     `GetKey` to read the public half.
   - `GCPKMSSigner` — `cloud.google.com/go/kms/apiv1`, `AsymmetricSign`
     with digest SHA-256, `GetPublicKey` once at startup.
   Plus a `LocalSigner` (in-process RSA key) used only in tests.
4. **`internal/oidc/jwt.go`** — cloud-agnostic JWT minter that takes a
   `Signer` and a `jwt.MapClaims`, builds the header (with the Signer's
   `kid`), composes `base64url(header).base64url(claims)`, hashes it,
   calls `Signer.Sign`, and returns the compact JWS.
5. **`internal/oidc/factory.go`** — `NewSignerFromEnv(ctx)` reads
   `CUDLY_SOURCE_CLOUD` and the per-cloud key env var
   (`CUDLY_SIGNING_KEY_ID` for AWS, `CUDLY_SIGNING_KEY_VAULT_URL` +
   `CUDLY_SIGNING_KEY_NAME` for Azure, `CUDLY_SIGNING_KEY_RESOURCE` for
   GCP) and returns the appropriate implementation.
6. **`internal/api/handler_oidc.go`** — two new routes, public (no auth):
   - `GET /.well-known/openid-configuration`
   - `GET /.well-known/jwks.json`
7. **`internal/credentials/azure_federated.go`** — a new
   `azcore.TokenCredential` that uses the signer to mint a client_assertion
   JWT and exchanges it at Azure's token endpoint via
   `client_assertion_type=urn:ietf:params:oauth:client-assertion-type:jwt-bearer`.
   `ResolveAzureTokenCredential` routes to this path when
   `AzureAuthMode == "workload_identity_federation"` and **no** stored
   `azure_wif_private_key` credential is present (backward-compat: legacy
   cert-based accounts keep working during the transition).
8. **`internal/iacfiles/templates/azure-wif-cli.sh.tmpl`** — drop the cert
   generation, the `az ad app credential reset`, and the certificate env-var
   prompt. Add `az ad app federated-credential create` using the CUDly
   issuer URL and `cudly-controller` as the subject.
9. **Infra for Azure and GCP CUDly deployments** — matching modules for
   `terraform/modules/compute/azure/container-apps/` (Azure Key Vault key +
   access policy) and `terraform/modules/compute/gcp/cloud-run/` (GCP KMS
   asymmetric key + IAM binding), exposing the same `CUDLY_SIGNING_KEY_*`
   env vars.
10. Tests: unit tests for each Signer impl (with fakes), handler tests for
   the two new routes, template rendering test asserting the new CLI shape.

## Subject value

`sub = "cudly-controller"` is a single global string. Azure federated
credentials are bound per-app-registration, so the `sub` doesn't need to be
unique across CUDly deployments — each target tenant's federated credential
entry already names the deployment's issuer URL (the Lambda Function URL),
which **is** unique per CUDly deployment.

A per-tenant-account subject would add complexity without meaningful
separation — the subject just has to match what the federated credential was
registered with, and a fixed constant is easier to operate.

## Key rotation

KMS handles rotation. The JWKS endpoint always publishes whatever
`GetPublicKey` returns for the configured key ARN. Azure refreshes its JWKS
cache periodically (default ~24h). Rotation flow:

1. Create a new KMS key (or use key versions if KMS supports them for
   asymmetric keys — verify; last I checked asymmetric keys do NOT support
   auto-rotation, so rotation means a new key ARN).
2. Update `CUDLY_SIGNING_KEY_ID` env var, redeploy.
3. Operators wait ~24h for Azure to refresh JWKS, then delete the old key.

If simultaneous validity is needed during rotation, the JWKS endpoint can
publish multiple keys and the signer can cycle; deferred as future work.

## Out of scope (this redesign)

- GCP source-cloud federation. GCP→Azure uses a different issuer (the GCP
  SA token endpoint) and is a separate follow-up.
- Multiple signing keys / rotation without downtime. Single key, single `kid`.
- Removing the cert-based `workload_identity_federation` path from
  `internal/credentials/resolver.go`. Kept for backward compatibility with
  existing accounts; the routing just prefers the federated path when no
  stored PEM is present.
- Database schema change. `azure_wif_private_key` credential type stays as a
  legacy value.
