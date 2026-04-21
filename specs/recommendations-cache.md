# Recommendations cache

The dashboard and Recommendations pages read cost-optimisation
recommendations from Postgres rather than re-fetching live from AWS /
Azure / GCP on every request. Provider-switch clicks go from 2–10 s
(live API round-trip per provider × per account) to sub-100 ms (single
SQL read).

## Tables

Migration `000030_recommendations_cache.{up,down}.sql` creates:

- `recommendations` — one row per (account, provider, service, region,
  resource type) with the full `RecommendationRecord` JSON in `payload`
  and denormalised `upfront_cost` / `monthly_savings` columns for SQL
  aggregation. The `account_key` generated column collapses NULL
  `cloud_account_id` to the zero UUID so the natural-key UNIQUE index
  catches duplicates for account-less (global) recommendations.
- `recommendations_state` — singleton row (`CHECK id = 1`) tracking
  `last_collected_at` and `last_collection_error` so the frontend can
  render a freshness indicator + partial-failure banner.

## Write path

`scheduler.CollectRecommendations` calls `UpsertRecommendations` at the
end. Natural-key upsert keeps PKs stable across runs. Stale-row
eviction (`DELETE ... WHERE collected_at < $now AND provider = ANY(...)`)
is scoped to providers that actually succeeded in the current run, so
rows from a provider whose collect failed stay visible instead of being
blanked out.

Per-account cloud-API calls within each provider are fanned out via
`errgroup.SetLimit`, bounded by `CUDLY_MAX_ACCOUNT_PARALLELISM`
(default 20). Partial failures are logged and the run continues.

## Read path

`scheduler.ListRecommendations(ctx, filter)` is the handler-facing
entry point. Flow:

1. Read `recommendations_state` freshness.
2. If `last_collected_at IS NULL` (cold start), run
   `CollectRecommendations` synchronously — the first request sees real
   data rather than an empty table. Safe on all runtimes.
3. Read `recommendations` with the filter pushed into the SQL `WHERE`
   clause.
4. Non-Lambda only: if `last_collected_at` is older than
   `CUDLY_RECOMMENDATION_CACHE_TTL`, kick off a background
   `CollectRecommendations` via a goroutine guarded by an `atomic.Bool`
   single-flight flag so the *next* read is fresh. Lambda skips this —
   goroutines freeze between invocations.

## Refresh triggers

- **Scheduled cron** (AWS EventBridge / GCP Cloud Scheduler / Azure
  Logic App) — runs on all runtimes at `var.recommendation_schedule`,
  default `rate(1 day)`.
- **Manual `POST /api/recommendations/refresh`** — user-driven via the
  Refresh button on the dashboard. All runtimes.
- **In-request stale-while-revalidate** — GCP Cloud Run and Azure
  Container Apps only. Fires when cache age exceeds
  `CUDLY_RECOMMENDATION_CACHE_TTL` during a read. Lambda skips this —
  goroutines freeze between invocations.

Lambda deploys rely on scheduled + manual refresh only.

## Configuration

- `CUDLY_RECOMMENDATION_CACHE_TTL` (default `6h`) — parsed by
  `time.ParseDuration`. Invalid values fall back to the default with a
  warning. Controls when in-request stale-while-revalidate fires.
- `CUDLY_MAX_ACCOUNT_PARALLELISM` (default `20`) — caps per-account
  concurrency inside each provider phase. Shared with the purchase
  manager so both honour the same operator override.

Terraform scheduled-task knobs (per-cloud `enable_scheduled_tasks` +
`recommendation_schedule`) are unchanged by this feature.

## API

- `GET /api/recommendations` — query with `provider`, `service`,
  `region`, `min_savings`, `account_ids`. Auth: `AuthAdmin` +
  `view:recommendations`.
- `GET /api/recommendations/freshness` — returns
  `{last_collected_at, last_collection_error}`. Auth: `AuthAdmin` +
  `view:recommendations`.
- `POST /api/recommendations/refresh` — synchronous refresh. Calls
  `CollectRecommendations`, which persists on success. Auth:
  `AuthAdmin` + `create:recommendations`.

## Rollback

`000030_recommendations_cache.down.sql` drops both tables. Running
handlers gracefully degrade: the read path returns an empty list and
logs the error; the cron continues to collect but can't persist.
