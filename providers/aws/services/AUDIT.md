# findOfferingID Quarterly Audit Checklist

This document is the authoritative checklist for the periodic audit of all seven
AWS service clients' `findOfferingID` implementations (or `lookupOfferingID` for
SavingsPlans). The audit was introduced in issue #515 following the PR #690
rework. Run this checklist whenever:

- A quarterly reminder fires.
- AWS announces a new offering attribute for any of the services below.
- A `findOfferingID` function is modified on a PR.

---

## Contract Items (all 7 services must satisfy every item)

### C1 -- Typed first-class fields, not Filter[]-heavy queries

Use SDK-typed request fields (e.g. `InstanceType`, `ProductDescription`,
`OfferingType`) to narrow the result set server-side where the AWS API supports
them. Relying solely on client-side filtering inside `Filters[]` caused AWS to
return empty pages with a NextToken, walking until the Lambda budget expired
(issue #688).

**Exception**: OpenSearch and Redshift have no server-side filters for instance
type or payment option; all matching must be done client-side. This is
intentional and documented in those clients' function comments.

### C2 -- ctx.Err() check at the top of each pagination iteration

```go
for {
    if err := ctx.Err(); err != nil {
        return "", err
    }
    // ... page fetch ...
}
```

Context cancellation is terminal (see `feedback_ctx_cancel_terminal.md`). The
check must be the first statement inside the loop body, before any API call.
This is regression-tested by `TestFindOfferingID_CtxCancelledBeforePage` in
each service's `client_test.go`.

### C3 -- Page cap (maxOfferingPages) with explicit diagnostic error

```go
const maxOfferingPages = 5

if page > maxOfferingPages {
    return "", fmt.Errorf("pagination cap reached after %d pages ... (issue #688)", ...)
}
```

The cap must reference issue #688 in the error string to aid log triage. It
must fire before the (cap+1)th API call. Regression-tested by
`TestFindOfferingID_PaginationCapFires` / `TestLookupOfferingID_PaginationCapFires`.

### C4 -- Per-page log line with timing

```go
log.Printf("purchase[%s]: <Service> findOfferingID page %d: %d offerings in %s",
    tag, page, len(results), time.Since(pageStart))
```

Both the page number and the elapsed time since page start must appear. The tag
(`execID` or `"no-exec"`) provides log correlation across concurrent purchases.

### C5 -- Soft-skip of mismatched variants

When a returned offering does not match the requested payment option (or offering
type), the behaviour depends on whether the API accepts a payment-option filter:

- **Filter-capable APIs** (RDS, ElastiCache, MemoryDB): API filter is set, so a
  mismatch indicates an API bug, not a routine variant. Return an error with
  `"API filter mismatch"` in the message.
- **Client-side-only APIs** (EC2, OpenSearch): skip the mismatched offering (log
  it) and continue to the next one. Do not fail the entire search.
- **Redshift**: the AWS offering type is `"Regular"` or `"Upgradable"`, which is
  unrelated to payment option. Payment option is not a server-side or client-side
  filter dimension. Any value outside those two is surfaced as an error.
- **SavingsPlans**: `PaymentOptions` filter is passed on the request; the first
  result is returned without a secondary check.

### C6 -- Pagination terminator handles BOTH nil AND empty-string token

```go
// Pattern for *string token:
if token == nil || aws.ToString(token) == "" {
    break
}

// Pattern via helper (EC2, MemoryDB):
func isLastXxxPage(token *string) bool {
    return token == nil || aws.ToString(token) == ""
}
```

The AWS SDK may return either `nil` or a pointer to `""` for the last page.
Both must end pagination to avoid an extra redundant API call. Regression-tested
by `TestFindOfferingID_EmptyStringTokenEndsPagination` /
`TestLookupOfferingID_EmptyStringTokenEndsPagination`.

---

## Service-by-Service Status (last audited 2026-Q2, issue #515)

| Service      | C1 typed fields | C2 ctx.Err() | C3 page cap | C4 per-page log | C5 variant skip | C6 nil+empty token |
|--------------|:---------:|:---------:|:---------:|:---------:|:---------:|:---------:|
| EC2          | PASS      | PASS      | PASS      | PASS      | PASS (soft-skip) | PASS (`isLastEC2Page`) |
| RDS          | PASS      | PASS      | PASS      | PASS      | PASS (hard-error on mismatch) | PASS (inline) |
| ElastiCache  | PASS      | PASS      | PASS      | PASS      | PASS (hard-error on mismatch) | PASS (inline) |
| MemoryDB     | PASS      | PASS      | PASS      | PASS      | PASS (hard-error on mismatch) | PASS (`isLastMemoryDBPage`) |
| OpenSearch   | PASS*     | PASS      | PASS      | PASS      | PASS (error only when type+duration match but payment differs) | PASS (inline) |
| Redshift     | PASS*     | PASS      | PASS      | PASS      | PASS (error on unknown offering type) | PASS (inline) |
| SavingsPlans | PASS      | PASS      | PASS      | PASS      | PASS (filter on request) | PASS (inline) |

\* OpenSearch and Redshift have no server-side filters; client-side matching is
the only option. Documented in function comments.

---

## Watchlist Items (no code change needed today, monitor each quarter)

### MemoryDB -- Valkey engine dimension

AWS added Valkey alongside Redis for MemoryDB in 2024.
`DescribeReservedNodesOfferings` does not currently expose engine as a separate
offering attribute (a Valkey RI is still matched by node type). If AWS ever
splits the offering schema on engine, `findOfferingID` would silently pick the
first match for the given node type, potentially buying a Redis offering for a
Valkey recommendation.

**Check each quarter**: call `aws memorydb describe-reserved-nodes-offerings`
and inspect whether any new field like `Engine` or `EngineVersion` appears on
the `ReservedNodesOffering` struct. If it does, update `findOfferingID` to
filter on it and add `Engine` to `common.MemoryDBDetails` if needed.

### OpenSearch / Redshift -- Graviton architecture dimension

If AWS introduces an ARM-vs-x86 dimension on
`DescribeReservedInstanceOfferings` (OpenSearch) or
`DescribeReservedNodeOfferings` (Redshift), the current first-match-by-type loop
would become ambiguous between Graviton and non-Graviton offerings for the same
node type.

**Check each quarter**: inspect the SDK types for any new field on
`ReservedInstanceOffering` (OpenSearch) or `ReservedNodeOffering` (Redshift).
If a new dimension appears, extend `common.SearchDetails` /
`common.RedshiftDetails` accordingly and add a client-side filter in the
respective `scan*OfferingPage` function.

---

## How the Contract is Enforced in CI

Each of the seven `client_test.go` files contains three tests that pin the
contract at the code level:

| Test name | Catches regression on |
|-----------|----------------------|
| `TestFindOfferingID_PaginationCapFires` | C3 -- page cap |
| `TestFindOfferingID_CtxCancelledBeforePage` | C2 -- ctx.Err() |
| `TestFindOfferingID_EmptyStringTokenEndsPagination` | C6 -- nil + empty token |
| `TestFindOfferingID_WrongVariantRejected` | C5 -- variant mismatch |
| `TestFindOfferingID_HappyPath` | overall happy path |

SavingsPlans names the equivalent tests `TestLookupOfferingID_*` because
pagination lives in `lookupOfferingID`.

Running `go test ./providers/aws/services/...` covers all seven services.

---

## Performing the Quarterly Audit

1. Run `go test ./providers/aws/services/...` -- all tests must pass.
2. For each service, open `providers/aws/services/<service>/client.go` and walk
   the checklist items C1-C6 against the current implementation.
3. Grep for the watchlist dimensions described above.
4. Update the "last audited" date and the status table in this file.
5. If any item regresses, open a new issue referencing #515 and fix it in the
   same PR.
