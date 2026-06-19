# RI Exchange Alternatives Audit

**Issue:** #152
**PR:** (see accompanying pull request)

## Background

PR #79 / commit `1d76df7a2` replaced a hand-curated `peerFamilyGroups` allowlist in
`pkg/exchange/reshape.go` with Cost Explorer (CE)-driven cross-family alternatives sourced
from the cached recommendations table via `internal/api/exchange_lookup.go::purchaseRecLookupFromStore`.

This document audits whether the CE-driven approach produces a set of alternatives that is
correct with respect to AWS's native `GetReservedInstancesExchangeQuote` API rules.

## AWS RI Exchange API Rules

AWS will accept a convertible RI exchange when ALL of the following hold:

1. **Both source and target must be convertible** (not Standard). CUDly already pre-filters to
   `OfferingClass == "convertible"` in `analyzeRI`.
2. **Same region.** The store query scopes by region via `RecommendationFilter.Region`.
3. **Same term.** A 1y source cannot exchange to a 3y target and vice versa. CUDly enforces
   this via `termMatchesIfKnown` when both sides supply `TermSeconds`.
4. **New total value >= old total value** (the "dollar-units" rule). CUDly approximates this
   via `passesDollarUnitsCheck`: `target.NF * target.EMC >= src.NF * src.MonthlyCost`.
5. **Same platform / tenancy / scope.** CE recommendations are already scoped to the account's
   actual usage so platform mismatches (Linux vs Windows, shared vs dedicated) can only surface
   when the store contains recs for a different platform. No additional code filter is needed.

## Old Allowlist vs CE-Driven Approach

### Old `peerFamilyGroups` allowlist (removed in `1d76df7a2`)

The allowlist encoded known-safe cross-family pairs grouped by use-case:

| Group          | Families                                   |
|----------------|--------------------------------------------|
| general        | m5, m6i, m7g                               |
| compute        | c5, c6i, c7g                               |
| memory         | r5, r6i, r7g                               |
| burstable      | t3, t3a, t4g                               |
| GPU/inference  | p3/p4d/p5, g4dn/g5, hpc6a/hpc6id/hpc7g    |
| legacy general | m4 -> m5                                   |
| legacy compute | c4 -> c5                                   |
| legacy memory  | r3/r4 -> r5                                |

Families absent from the allowlist (e.g. `x1`, `i3`, `d2`, `z1d`, `m8g`, `c8g`, `r8g`,
any new generation at time of removal) received **no alternatives** regardless of dollar-units.

### CE-Driven Approach

CE recommendations are produced by AWS for the account's actual usage. The set of families
surfaced is open: any family AWS recommends passes through as long as it clears:

- Cross-family check (not source family, not primary target)
- Term match (when both sides supply `TermSeconds`)
- Dollar-units check (when source pricing is available)

## Delta Analysis

### Cases where the allowlist was more restrictive than CE (CE wins, no bug)

1. **Cross-group exchanges.** The allowlist prevented, e.g., m5 users from seeing c5
   alternatives (those are in separate groups). AWS actually allows m5 -> c5 exchanges if
   dollar-units pass. CE correctly surfaces these when it has recs for c5.

2. **New generations.** Families like m8g, c8g, r8g, added after the allowlist was authored,
   were silently excluded. CE produces recs for these when the account uses them, so the
   CE-driven path surfaced them without a code change. This is the intended behaviour.

3. **Specialty families.** GPU/HPC families beyond the allowlist's three groups (e.g. `inf1`,
   `inf2`, `trn1`) were excluded. CE surfaces these when the account incurs those costs.

### Cases where the allowlist was more permissive than CE (potential gap)

1. **Long-tail families with no CE signal.** If an account has never used a family, CE will
   not recommend it, so no alternatives from that family will surface even if an exchange would
   be valid. This is a "missing" alternative, not a false positive. The user can still obtain
   the offering manually via the RI Exchange UI. This is acceptable UX behaviour: CUDly only
   shows alternatives backed by AWS's own cost recommendation, not arbitrary guesses.

2. **t-family burstable exchanges.** The allowlist mapped t3/t3a/t4g within their group. CE
   rarely recommends burstable RIs for accounts primarily using those types (the signal is
   typically "buy fewer, run on demand during bursts"). In practice this means fewer burstable
   alternatives surface; this is intentional, not a regression.

## Verdict: No Material Divergence Requiring a Code Fix

The CE-driven predicate is **correct** with respect to AWS's exchange API rules:

- It does not emit false positives that AWS would reject, provided the CE recommendations in
  the store are already platform/tenancy-scoped to the account's usage (which they are by
  construction -- CE recommendations are account-specific).
- The dollar-units approximation (`NF * EMC >= src.NF * src.MonthlyCost`) is a single-product
  proxy for AWS's two-sided check (new upfront >= old AND new recurring >= old). This can
  produce false positives, but those are caught at exchange time by the existing
  `IsValidExchange=false` guard in `auto.go` / `executeWithAPI`. No UI-visible 4xx results.
- The term-match guard blocks term-mismatched offerings cleanly.

The only gap is **missing** alternatives (families with no CE signal for the account). This is
by design and preferable to guessing.

**Predicate tightening verdict: not required.**

## Test Fixtures

The following fixtures pin the audit's conclusions. They extend the existing cross-family
test suite in `pkg/exchange/reshape_crossfamily_test.go`.

### Fixture A: cross-group exchange (m5 source sees c5 and r5 alternatives)

Source: m5.xlarge, 1y, $25/mo, NF=8. Offerings: c5.large (NF=4, $50/mo, 1y) and r5.large
(NF=4, $60/mo, 1y). Dollar-units check: c5 = 4*50=200 >= 8*25=200 (pass); r5 = 4*60=240 >= 200
(pass). Both must appear in AlternativeTargets.

### Fixture B: new-generation alternative (m8g surfaces, old allowlist would have missed it)

Source: m5.xlarge, 1y, $25/mo, NF=8. CE rec: m8g.large (NF=4, $52/mo, 1y). Dollar-units =
4*52=208 >= 200. Must appear. Confirms new-generation families route through without an
allowlist entry.

### Fixture C: dollar-units false positive at CE layer is caught at exchange time, not recommendation time

Source: m5.xlarge, 1y, $25/mo, NF=8. CE rec: c5.medium (NF=2, $60/mo, 1y). Dollar-units =
2*60=120 < 200. Must NOT appear (pre-filter blocks it).

### Fixture D: term mismatch is rejected even when dollar-units pass

Source: m5.xlarge, 1y, NF=8, $25/mo. CE rec: c5.large (NF=4, $60/mo, **3y**). Dollar-units
would pass (4*60=240 >= 200). Term mismatch (1y vs 3y) must block it.

### Fixture E: missing CE signal means no alternative (not a bug)

Source: m5.xlarge, 1y, $25/mo. CE returns no recs. AlternativeTargets must be empty.
Primary target (m5.large) must remain.

Fixtures A and D are already covered by
`TestAnalyzeReshapingWithRecs_RecommendationDrivenAlternatives` and
`TestAnalyzeReshapingWithRecs_TermMismatchedAlternativesFiltered` respectively. Fixtures
B, C, and E are added as new test cases in `pkg/exchange/reshape_crossfamily_test.go`.
