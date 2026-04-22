# purchase_executions retention — verify CleanupOldExecutions is scheduled

**Surfaced during:** 2026-04-23 follow-up audit — Failed + Expired rows now persist and surface in History
**Related commits:** `32f9e4ffc` (pending-in-history), `b44283746` (failed + expired states)
**Status:** ops unknown — need to confirm cleanup cadence before failed/expired rows pile up.

## Problem

Before the merge of non-completed executions into History (`32f9e4ffc`
and `b44283746`), only `completed` purchases surfaced via
`purchase_history` — which is itself pruned in its own SQL. Now
`purchase_executions` rows in `pending` / `notified` / `failed` /
`expired` states also render on every `/api/history` request, and those
stick around until something calls `CleanupOldExecutions`.

The store interface exposes
`CleanupOldExecutions(ctx, retentionDays) (int64, error)` and a Postgres
implementation (`internal/config/store_postgres.go`) runs the expected
`DELETE FROM purchase_executions WHERE …`. What we have not verified is
whether **any** scheduled job actually calls it. `grep -rn
CleanupOldExecutions internal/` currently shows only the interface +
implementation + tests; no caller.

If no caller exists, the table grows unbounded:

- a high-volume tenant with one failed purchase per day (common when
  FROM_EMAIL is misconfigured — see `33pz7pom…` before `b3e17719b`)
  will accumulate a thousand failed rows a year;
- History render then pulls the full non-completed set every page load
  (capped at `config.DefaultListLimit = 100`, so UX degrades gracefully,
  but the DB write amplification from `expireIfStale` transitioning
  hundreds of stale rows on a single page view is still a concern).

## Proposed resolution

1. **Audit** — confirm whether a scheduled Lambda/job calls
   `CleanupOldExecutions`. If yes, capture the cadence + retention in a
   code comment on the interface method.
2. **If no caller** — add one. Natural home is the existing
   `internal/scheduler/` package alongside the scheduled recommendation
   collector. Retention ~90 days covers "I need to see what failed last
   quarter" without ballooning the table.
3. **Metrics** — emit a CloudWatch metric for the count of non-completed
   executions so we can alarm before the table becomes a performance
   issue, not after.

## Why not now

Nothing is broken yet — the first production deployment of `b44283746`
hasn't even completed. Tagging this so the next ops review of the
scheduler catches it before the growth curve matters.
