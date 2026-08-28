# MCP tool annotations -- decisions

This repo has no existing ADR/`docs/decisions/` convention (checked at
implementation time: no `docs/adr/` or `docs/decisions/` directory), so
this rationale lives here instead of in a numbered ADR.
Context: `docs/plans/mcp/05-store.md` Phase A and `docs/plans/mcp/00-scope.md`
§6 (not checked into this repo -- local planning notes for the MCP
store-readiness workstream).

## Why annotations at all

Anthropic's and OpenAI's MCP directory review criteria both treat tool
`title` and the `readOnlyHint`/`destructiveHint` annotations as a hard
submission gate, and annotation quality is the #1 rejection cause at OpenAI.
Independent of directory submission, the go-sdk's `mcp.ToolAnnotations` has a
trap that makes annotating explicitly non-optional for this server
specifically:

> **Nil-defaults trap**: `DestructiveHint *bool` and `OpenWorldHint *bool`
> default to `true` on the wire when left nil. A `nil` `Annotations` field
> (or a struct that leaves either pointer unset) therefore publishes
> "destructive, open-world, not read-only" for every tool that omits it --
> including the read-only search and catalog tools. Omission is not a safe
> or neutral default; it is a wrong answer for 2 of this server's 11 tools.

`ReadOnlyHint bool` and `IdempotentHint bool` have no such trap (their zero
value, `false`, is already the conservative reading), but every Descriptor
still sets them explicitly via `readOnlyAnnotations`/`purchaseAnnotations`
(`mcp/tools/registry.go`) so the intent is legible at every call site rather
than resting on an implicit zero value.

## D1 -- destructiveHint=true on every purchase tool

Considered and rejected: treating a purchase as "additive" (it only creates
a new reservation/commitment, never deletes or mutates existing state) and
setting `destructiveHint=false`.

Rejected because Anthropic's review criteria require `destructiveHint` on
any tool that modifies state, not only ones that delete or overwrite it, and
state explicitly that "destructive tools always prompt" in a client UI. A
tool that commits the caller's cloud account to a multi-thousand-dollar,
multi-year financial obligation is exactly the class of action a client
should always confirm before executing, regardless of whether the technical
effect is additive. All 9 purchase tools (`cudly_aws_ec2_ri_purchase`,
`cudly_aws_rds_ri_purchase`, `cudly_aws_elasticache_ri_purchase`,
`cudly_aws_opensearch_ri_purchase`, `cudly_aws_redshift_ri_purchase`,
`cudly_aws_memorydb_ri_purchase`, `cudly_aws_savingsplans_purchase`,
`cudly_azure_compute_ri_purchase`, `cudly_gcp_computeengine_cud_purchase`)
therefore set `DestructiveHint: boolPtr(true)`.

## A5 -- idempotentHint=false on every purchase tool, always

Every purchase tool's `IdempotentHint` is `false`, unconditionally, even
though `ExecutePurchase` (`mcp/tools/purchase.go`) already derives an
idempotency key from the request and refuses/dedupes a byte-identical retry.

Rejected alternative: `IdempotentHint=true`, on the theory that the
server-side dedupe already makes a retry safe.

Rejected because:

- `idempotency_nonce` is caller-supplied and optional. Varying that one
  string field turns an otherwise byte-identical request into a
  deliberately distinct one, so "retrying with the same arguments has no
  additional effect" is not actually true in general -- it only holds when
  the caller leaves the nonce untouched, and the hint has no way to express
  that condition.
- Provider-side dedupe (AWS `ClientToken`, and the Azure/GCP equivalents) is
  time- and scope-bounded, not a permanent guarantee independent of this
  server's own idempotency key.
- The failure asymmetry is one-sided: a wrongly-`true` hint risks an MCP
  client auto-retrying a stalled/ambiguous call and buying a real
  multi-thousand-dollar commitment twice; a wrongly-`false` hint costs
  nothing worse than an extra confirmation round in a client UI that treats
  non-idempotent tools more cautiously. Given that asymmetry, `false` is the
  only defensible default across all 9 purchase tools.

## Per-tool annotation table

Built from `mcp/tools/registry.go`'s `readOnlyAnnotations`/
`purchaseAnnotations` helpers and each tool's `Descriptor()`. Every tool sets
`ReadOnlyHint`, `DestructiveHint`, `IdempotentHint`, and `OpenWorldHint`
explicitly; none rely on the SDK's nil-defaults-to-true behavior.

| Tool | ReadOnly | Destructive | Idempotent | OpenWorld | Title |
|---|---|---|---|---|---|
| `cudly_search_recommendations` | true | false | false | true | Search commitment recommendations |
| `cudly_list_commitment_actions` | true | false | false | **false** | List available commitment actions |
| `cudly_aws_ec2_ri_purchase` | false | true | false | true | Purchase AWS EC2 Reserved Instances |
| `cudly_aws_rds_ri_purchase` | false | true | false | true | Purchase AWS RDS Reserved Instances |
| `cudly_aws_elasticache_ri_purchase` | false | true | false | true | Purchase AWS ElastiCache Reserved Cache Nodes |
| `cudly_aws_opensearch_ri_purchase` | false | true | false | true | Purchase AWS OpenSearch Reserved Instances |
| `cudly_aws_redshift_ri_purchase` | false | true | false | true | Purchase AWS Redshift Reserved Instances |
| `cudly_aws_memorydb_ri_purchase` | false | true | false | true | Purchase AWS MemoryDB Reserved Instances |
| `cudly_aws_savingsplans_purchase` | false | true | false | true | Purchase an AWS Savings Plan |
| `cudly_azure_compute_ri_purchase` | false | true | false | true | Purchase an Azure VM Reserved Instance |
| `cudly_gcp_computeengine_cud_purchase` | false | true | false | true | Purchase a GCP Compute Engine Committed Use Discount |

`cudly_list_commitment_actions` is the one `OpenWorldHint=false` tool in the
server: it only ever reads the in-process `Descriptor` slice `NewServer`
builds at startup, never a live cloud API, unlike
`cudly_search_recommendations` (reaches Cost Explorer/Advisor/Recommender)
and every purchase tool (reaches a live provider purchase API).

## Enforcement

`Descriptor.Annotations` is a plain, optional `*mcp.ToolAnnotations` field --
nothing at the Go type level stops a new tool from leaving it nil or
building one by hand instead of calling `readOnlyAnnotations`/
`purchaseAnnotations`, so none of this is compiler-enforced. The guarantee
is carried entirely by `mcp/annotations_test.go`, run in CI on every push:

- `TestToolAnnotationsMatchDescriptor` is the drift test: it drives a real
  `ListTools` call over the in-memory MCP transport and asserts, in both
  directions, that the live `Annotations` on the wire equal each tool's
  `Descriptor().Annotations` -- tool counts must match, every live tool
  must have a matching `Descriptor`, and every `Descriptor` must have a
  matching live tool. The bidirectional check matters because a tool
  registered directly against `*gosdk.Server` outside the
  `Descriptor`/`Register` contract would never appear in the descriptor
  side of a one-directional comparison, and could ship unsafe or missing
  annotations without this test noticing.
- `TestToolAnnotationsValueAssertions` is a table-driven sweep, derived from
  `Descriptor.Action` rather than a hardcoded tool-name list (so a future
  12th purchase tool inherits the check automatically), asserting the
  concrete required value of every hint per role, not just that a value is
  present: `Annotations`/`DestructiveHint`/`OpenWorldHint` are never nil;
  every tool whose `Action` contains `"purchase"` is `ReadOnlyHint=false`,
  `DestructiveHint=true` (D1), `IdempotentHint=false` (A5), and
  `OpenWorldHint=true`; the search tool (`Action == "search"`) is
  `ReadOnlyHint=true`, `DestructiveHint=false`, `OpenWorldHint=true`; the
  catalog tool (the one remaining role, `Action == ""`) is
  `ReadOnlyHint=true`, `DestructiveHint=false`, `OpenWorldHint=false`; and
  every `Title` is non-empty, differs from the tool's snake_case `Name`, and
  contains no underscore. Asserting concrete values (not just "is set")
  matters because `TestToolAnnotationsMatchDescriptor` alone would pass a
  purchase tool that mistakenly set `IdempotentHint=true`, as long as
  `Descriptor` and the live registration agreed on that same wrong value.
