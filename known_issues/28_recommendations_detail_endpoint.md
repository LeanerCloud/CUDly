# Recommendations detail drawer — backend endpoint missing

**Surfaced during:** 2026-04-22 UX audit, Priority 6 ("Recommendations: bulk
actions + sortable columns + row-click detail")
**Related commit:** `b66abc4d8` (Phase-1 + Phase-2 UX work)
**Status:** open — blocks completing Priority 6

## Problem

The Recommendations page now supports a row-click detail drawer (shipped
in `b66abc4d8`), but the drawer renders only the data already present in
the listing row — service, current type, recommended type, monthly
savings. The UX audit asked for a "why this recommendation" affordance:
usage history over the collection window, a confidence bucket (low /
medium / high), and a one-line provenance note naming the collector +
sampling period.

The frontend cannot build that view from existing API responses. The
`/api/recommendations` list endpoint omits the time-series usage data
and confidence metadata needed, and stuffing it into the list payload
would balloon the response size for the common case (no drawer open).

## Required backend change

Add a new endpoint:

```text
GET /api/recommendations/:id/detail
```

Response shape (proposed):

```text
{
  "id": "rec-abc123",
  "usage_history": [
    { "timestamp": "2026-03-20T00:00:00Z", "cpu_pct": 12.4, "mem_pct": 34.2 }
    // ...daily datapoints over the collection window
  ],
  "confidence_bucket": "high",         // "low" | "medium" | "high"
  "provenance_note": "AWS Cost Explorer · 30-day window · sampled hourly"
}
```

The detail data is already computed server-side during recommendation
generation — this is surfacing it on a per-id GET rather than a new
analytics pipeline.

## Frontend integration

Once the endpoint lands:

- `frontend/src/recommendations.ts` detail drawer calls
  `api.getRecommendationDetail(id)` on first expand and memoises the
  result for the lifetime of the drawer.
- Drawer renders a small sparkline of usage_history + the confidence
  badge + the provenance note.
- Existing row-click + drawer chrome stay untouched; only the content
  changes.

## Out of scope

- Cross-recommendation comparison UI.
- Confidence thresholds per provider (a separate tuning pass).
- Streaming detail updates as new collection runs land; re-fetch on
  drawer expand is acceptable.
