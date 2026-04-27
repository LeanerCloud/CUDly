# purchase_executions retention — verify CleanupOldExecutions is scheduled

**Surfaced during:** 2026-04-23 follow-up audit — Failed + Expired rows now persist and surface in History
**Related commits:** `32f9e4ffc` (pending-in-history), `b44283746` (failed + expired states)
**Status:** ✔️ Resolved — verified 2026-04-25.

## Resolution

`internal/server/handler.go::handleCleanupExpiredRecords` (line 153) calls
`CleanupOldExecutions(ctx, 30)` with a 30-day retention. Wired as the
`TaskCleanupExpiredRecords` scheduled task type and triggered by:

- **AWS:** EventBridge rule `cleanup_schedule` in
  `terraform/modules/compute/aws/cleanup-lambda/main.tf:98`
- **Azure:** dedicated cleanup Function App
- **Container Apps:** scheduled task

The `CleanupOldExecutions` Postgres implementation in
`internal/config/store_postgres.go` runs the expected
`DELETE FROM purchase_executions WHERE …`, so the table is now bounded
on every supported deployment target.

## Original problem (historical context)

Before the merge of non-completed executions into History
(`32f9e4ffc` and `b44283746`), only `completed` purchases surfaced via
`purchase_history` — which is itself pruned in its own SQL. After those
commits, `purchase_executions` rows in `pending` / `notified` / `failed`
/ `expired` states also render on every `/api/history` request, and
those rows would have stuck around indefinitely without a cleanup
caller.

At the time this issue was filed, `grep -rn CleanupOldExecutions
internal/` showed only the interface + implementation + tests; no
caller. The concern was that, left unbounded, a high-volume tenant with
one failed purchase per day (common when FROM_EMAIL is misconfigured —
see `33pz7pom…` before `b3e17719b`) would accumulate a thousand failed
rows a year, eventually amplifying writes during `expireIfStale`
transitions on every History page load.

The wiring discovered above (handleCleanupExpiredRecords →
TaskCleanupExpiredRecords → cleanup_schedule) closes that gap. The
30-day retention is shorter than the 90-day window originally proposed
but matches what the cleanup-lambda Terraform actually configures, and
is sufficient for "what failed last week" debugging without ballooning
the table.

## Future improvements (not blocking)

- **Metrics** — emit a CloudWatch metric for the count of non-completed
  executions so we can alarm before the table becomes a performance
  issue, not after. Tracked separately if it matters; not part of this
  issue's resolution.
