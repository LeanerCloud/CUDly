# Accounts page — merge Registrations into per-provider account timeline

**Surfaced during:** 2026-04-22 UX audit, Priority 3 ("Rework Accounts page mental model")
**Related commit:** `b66abc4d8` (Phase-1 + Phase-2 UX work)
**Status:** further partial step shipped — top-of-page **Accounts overview**
chip row now renders `[Pending N | Active N | Disabled N | Rejected N]`
counts aggregated across providers + registrations; chip click scrolls
to the relevant section. Full unification into a single per-provider
`[All | Pending | Active | Rejected]` table is still deferred (see
"What's still deferred" below).

## What shipped in Phase-1

`b66abc4d8` replaced the inline per-provider account rows with a proper
`.accounts-table` (Name / Account ID / Status / Actions) and added a
status-chip filter row `[All (n) | Active (n) | Disabled (n)]` above
each provider's table. That covers the "visible list of accounts" half
of the audit item.

## What's still deferred

The audit called for a single coherent Accounts page where Registrations
(accounts customers have requested access to but not yet confirmed) and
per-provider accounts (confirmed, live) appear in one unified list with
a 4-state status chip set `[All | Pending | Active | Rejected]`. Today
Registrations sit in their own fieldset above the per-provider tables,
so users scan two separate lists to answer "what's the state of account
X?".

Completing the rework means:

1. **Data model:** treat registrations + accounts as two points on the
   same lifecycle (`pending` → `active` / `rejected` → `disabled`). The
   backend already has this information split across two tables; a
   unified API response is the simpler move.
2. **UI:** one table per provider with a 4-state status-chip row instead
   of the current 3-state. Registration-specific actions (Approve,
   Reject) appear on pending rows; account-specific actions (Edit,
   Credentials, Overrides, Delete) appear on active/disabled rows.
3. **Transitions:** approving a registration should animate the row
   into Active without a full reload, same for disable/enable toggles.

## Why it's deferred

Phase-1 Priority 3 was scope-capped to ship the visible-accounts polish
in a reasonable sprint. The full merge touches the backend
list-accounts endpoint (response shape change), the frontend
Registrations module, and the per-provider settings panels
simultaneously, which is a multi-session refactor rather than a single
cleanup commit.

## Dependencies

None blocking — this can start whenever. The status-chip filter from
`b66abc4d8` already establishes the UI pattern; extending it from
3 chips to 4 + merging the data source is additive.
