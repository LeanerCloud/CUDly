# Purchase History — inline Cancel button for pending rows

**Surfaced during:** 2026-04-23 follow-up audit after the Failed/Expired states landed (`b44283746`)
**Related commits:** `32f9e4ffc` (pending-in-history), `b44283746` (failed + expired states)
**Status:** deferred — pending rows surface the approver email but give no UI path to cancel.

## Problem

Once `/api/history` started merging pending executions (`32f9e4ffc`), users can
see their own in-flight approvals with the approver email rendered under the
Pending badge. What's missing is a way to **act** on them from History:

- The approval-link cancel path (`POST /api/purchases/cancel/{id}?token=…`) is
  gated by a one-time token that only lives in the email body. If the email
  never went out the token is effectively orphaned; once `b3e17719b` ships
  with the FROM_EMAIL wire-through the email will arrive, but users who
  prefer to cancel from the dashboard rather than the inbox still can't.
- The planned-purchase endpoints (`pause` / `resume` / `delete`) are session-
  authed but only accept plan-backed executions (`plan_id` populated). The
  ad-hoc "Execute Purchase" flow from Recommendations creates plan-less
  executions (`plan_id = ""`) so those endpoints reject them with
  `execution not found` or `execution cannot transition`.

## Proposed resolution

Add a session-authed cancel endpoint (or broaden the existing
`cancelPurchase` to accept either `token=` or an authenticated admin
session) and wire a Cancel button on pending rows in
`frontend/src/history.ts:renderHistoryList`. The button should:

- Show only for rows with `status = "pending"` (or `notified`).
- Require admin perms (same permission gate as Purchase
  History rendering already uses).
- On click: confirm via `confirmDialog`, then `POST` to the new endpoint,
  then reload history.

## Why not now

The token-authed cancel path is secure by design — the token in the email
is what proves intent. Adding a session-authed bypass widens the blast
radius; wants a permission-gate review (does `delete purchases` in the
RBAC table cover this, or do we add a new `cancel:own_executions` verb?).
Out of scope for the inbox-visibility milestone.
