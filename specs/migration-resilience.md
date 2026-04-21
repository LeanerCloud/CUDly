# Migration resilience — operator runbook

## What changed

Database migration failures are no longer fatal. `internal/server/app.go:ensureDB`
runs `migrations.RunMigrations` in a goroutine bounded by `migrationsTimeout`.
On failure, timeout, or panic the error is recorded on the `Application`
struct, logged at error level, and surfaced via `/health`'s new
`migrations` check — but `ensureDB` returns `nil` so the Lambda / Cloud
Run / Container App keeps serving every request that doesn't need the
missing schema.

This prevents the previous failure mode where a dirty `schema_migrations`
row (e.g. the "Dirty database version 27" outage) 502'd every endpoint
until an operator intervened.

## `/health` states

`checks.migrations.status` is one of:

- **disabled** — `dbConfig` is nil (non-PostgreSQL mode) or
  `DB_AUTO_MIGRATE=false`. Migrations happen elsewhere (CI, ops-run).
  **Does not flip overall status.**
- **pending** — `DB_AUTO_MIGRATE=true` but `ensureDB` hasn't completed
  its first run yet. Only visible during cold-start. **Flips overall
  status to `degraded`.**
- **failed** — last migration attempt returned an error or timed out.
  `message` carries the underlying error string. **Flips overall status
  to `degraded`.**
- **healthy** — last attempt completed without error. **Does not flip
  overall status.**

The endpoint still returns HTTP 200 regardless — liveness probes pass.

## Alerting guidance

If your monitoring alerts on `status != "healthy"` you will get a new
category of pages:

- **`pending` during cold-start** is expected and transient. The first
  request after a container starts moves state to `healthy` within
  ~seconds. If you alert on `degraded`, consider either (a) waiting 60s
  before paging so `pending` has cleared, or (b) filtering out alerts
  whose only failing check is `migrations` with status `pending`.

- **`failed` is genuinely actionable.** It means the latest migration
  didn't succeed. The app is still up but handlers that touch new
  schema will return 500s at query time. Follow the "Recovery" section
  below.

## Recovery from a failed migration

1. **Inspect logs to find the specific error.**

   ```bash
   aws logs tail /aws/lambda/<function-name> --since 10m --format short | grep -E "Migration|migration"
   ```

   Look for the line starting `⚠️ Migration failed — app continuing with
   existing schema:` and the underlying error message.

2. **Diagnose the failure.** Common causes:
   - `Dirty database version N. Fix and force version.` — migration N
     was interrupted mid-run (Lambda timeout, ENI drop, etc.) and
     `schema_migrations.dirty = true`.
   - `migration timed out after 20s` — migration N ran longer than
     `CUDLY_MIGRATION_TIMEOUT`. Either the migration is genuinely long
     (DDL on large table) or the DB is slow. Tune `CUDLY_MIGRATION_TIMEOUT`
     upwards and redeploy.
   - SQL errors (constraint violations, missing tables, etc.) — the
     migration file itself is broken. Fix the file, redeploy.

3. **Clear dirty state** if that's the cause. Use
   `CUDLY_FORCE_MIGRATION_VERSION` — see `internal/database/postgres/migrations/migrate.go`
   for the full operator flow. Short version:
   - If migration N's SQL landed on-disk (check the schema), set
     `CUDLY_FORCE_MIGRATION_VERSION=N`. Next cold start marks clean and
     resumes from N+1.
   - If it didn't land, set `CUDLY_FORCE_MIGRATION_VERSION=N-1` to
     retry N.
   - Remove the env var after the deploy reports `healthy`.

4. **Verify recovery.** `/health` should return `migrations.status =
   "healthy"` after the next cold start completes successfully.

## Configuration

- `CUDLY_MIGRATION_TIMEOUT` (default `20s`) — parsed by
  `time.ParseDuration`. Invalid values fall back to the default with a
  log warning. Shorter than the default Lambda 30s timeout so a runaway
  migration gets cancelled cleanly inside `ensureDB` (preventing the
  exact dirty-flag scenario) rather than by Lambda mid-invocation.

- `CUDLY_FORCE_MIGRATION_VERSION` (unset by default) — one-shot
  operator recovery. When set to a non-negative integer, calls
  `migrate.Force(N)` before `m.Up()`. Rejected with a loud error on
  non-numeric input — typos should surface immediately, not corrupt
  state.

- `DB_AUTO_MIGRATE` (default `false`, set to `true` in deployments that
  want lazy-init migrations) — when off, the `/health` migrations check
  reports `disabled`.

## Not covered here

- Moving migrations to a dedicated CI deploy step (the longer-term
  fix — migrations run once per deploy with clear failure visibility,
  not lazily per cold-start). Separate plan.
- Per-migration retry / auto-heal logic — if a migration fails, an
  operator is still the right recovery mechanism. This feature just
  stops the failure from nuking the user-facing app.

## Related

- `specs/recommendations-cache.md` — the recommendations cache also
  uses `CUDLY_MAX_ACCOUNT_PARALLELISM` for parallel cloud API calls
  and `CUDLY_RECOMMENDATION_CACHE_TTL` for stale-while-revalidate.
- Commit that introduced this change: `refactor(server): run migrations
  in a bounded goroutine, continue on failure` (branch
  `feat/multicloud-web-frontend`).
