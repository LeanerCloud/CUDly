# Test coverage gap — History `expireIfStale` lazy transition

**Surfaced during:** 2026-04-23 review of `b44283746` (failed + expired states)
**Related commit:** `b44283746`
**Status:** low-severity test gap — happy paths covered, stale-approval path not.

## Problem

`Handler.fetchExecutionsAsHistory` calls `expireIfStale` for every row it
projects, and that helper transitions `pending`/`notified` rows older
than the 7-day `approvalExpiryWindow` to `expired` via
`TransitionExecutionStatus`. The existing tests:

- `TestHandler_getHistory_IncludesPending` sets `ScheduledDate =
  time.Now()` to keep the transition from firing mid-assert (the comment
  calls this out explicitly and defers the stale path to "its own test
  below");
- `TestHandler_executePurchase_Success` covers the failed-on-send path.

Neither test asserts the transition itself: given a
`pending`/`notified` execution with `ScheduledDate > 7 days ago`,
`GetExecutionsByStatuses` returns it, `TransitionExecutionStatus` is
called with `["pending","notified"] → "expired"`, and the returned row's
Status is `expired` with `StatusDescription = "approval link expired …"`.

## Proposed resolution

One table-driven test in
`internal/api/handler_history_test.go` that:

1. Seeds two executions — one fresh, one with `ScheduledDate =
   time.Now().Add(-8 * 24 * time.Hour)`.
2. Mocks `GetExecutionsByStatuses` to return both.
3. Mocks `TransitionExecutionStatus` to return the stale row with
   `Status = "expired"`.
4. Asserts the mock was called exactly once, with the stale row's ID,
   and the rendered history row's `Status == "expired"` +
   `StatusDescription` non-empty.
5. Plus a negative case where `TransitionExecutionStatus` returns an
   error — `expireIfStale` must fall back to rendering the original
   pending row without panicking (already the intent per the inline
   comment).

## Why not now

The logic is 12 lines; a missed test here wouldn't cause data loss
(the worst case is a pending row mislabeled as pending forever, which
is exactly the pre-`b44283746` behaviour). Parking it here so we don't
lose sight of it on the next touch to that file.
