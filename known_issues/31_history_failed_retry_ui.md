# Purchase History — inline Retry button for failed rows

**Surfaced during:** 2026-04-23 follow-up audit after the Failed state landed (`b44283746`)
**Related commits:** `1ab739a5d` (SES rewire + email_sent/email_reason), `b44283746` (failed + expired states)
**Status:** deferred — failed rows show the reason but provide no re-submit path.

## Problem

When an approval email fails to send, `executePurchase` flips the
execution's status from `pending` to `failed` with the stored error
(`internal/api/handler_purchases.go:finalizePurchaseStatus`). The History
row renders the Failed badge + the backend reason ("send failed: Missing
domain", "FROM_EMAIL not configured", etc). But the only path back to a
working state is for the user to return to Recommendations and manually
re-select the same recommendations, which:

- is tedious when the failure was transient (SES throttle, quota reset);
- loses the audit trail linking the retry to the original failure;
- is impossible if the source recommendation has since been refreshed off
  the list.

## Proposed resolution

Add a Retry button on rows with `status = "failed"`:

- Show only for admin-authenticated sessions (same permission gate as
  execution creation).
- On click: re-submit a fresh `POST /api/purchases/execute` with the
  failed execution's Recommendations list (already stored on the
  PurchaseExecution row). Confirm via `confirmDialog` since this spends
  money on approval if the email sends this time.
- Mark the original failed row with a `retry_execution_id` so a follow-up
  UI iteration can show "Retried as …" instead of duplicating context.

A lighter-weight variant: add a single-line cURL hint to the
`status_description` when the failure reason is "FROM_EMAIL not
configured" — directs the admin at ops-side fixes (the `from_email`
tfvar / SES sandbox check) rather than hitting Retry against a
deployment that will fail again.

## Why not now

Retry semantics need product sign-off: do we store a linkage between the
original failed execution and its retry? Do we expose a "retries" count
so the UI can discourage obvious loops (e.g. FROM_EMAIL misconfig where
Retry just reproduces the same failure)? Out of scope for the
inbox-visibility milestone.
