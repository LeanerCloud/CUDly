# CUDly MCP Server - Architecture Blueprint

> Design pass for [#1488](https://github.com/LeanerCloud/CUDly/issues/1488). No implementation in this PR - this is the plan/architecture document that the implementation PRs (PR-0 through PR-9 in §9) build against.

## 0. Scope-correcting finding (read this before the rest)

The premise "CUDly's RI/SP purchase CLI, across AWS, Azure, GCP" does not match the code as checked out. Two separate things exist:

- **`cmd/main.go`** (`ri-helper`, cobra, single command, no subcommands) is **AWS-only**. `createServiceClient` (`cmd/main.go:301-320`) switches over `common.ServiceType` and only ever instantiates AWS clients (`ec2`, `elasticache`, `memorydb`, `opensearch`, `rds`, `redshift`, `savingsplans`). There is no `azure`/`gcp` branch anywhere in `cmd/`.
- **Azure and GCP commitment-purchase code exists** (`providers/azure/services/{compute,cache,database,search,cosmosdb}/client.go`, `providers/gcp/services/{computeengine,cloudsql,cloudstorage,memorystore}/client.go`, all with a `PurchaseCommitment` method), but it is only reachable today through `internal/api` (the Lambda-based web backend), not through any CLI entrypoint.

Second, more serious finding: `git status` shows almost everything interesting (`internal/`, `cmd/multi_service_*.go`, `providers/azure/services/*`, `providers/gcp/services/*`) as **untracked** - this working tree is mid-refactor and not fully committed. Concretely, `pkg/provider/interface.go:48` declares `PurchaseCommitment(ctx, rec, opts common.PurchaseOptions)`, and it is implemented with that 3-arg, opts-aware signature only by AWS `ec2` (`client.go:107`), `redshift` (`client.go:141`), `memorydb` (`client.go:111`), `opensearch` (`client.go:139`), `savingsplans` (`client.go:169`). AWS `rds` (`client.go:115`) and `elasticache` (`client.go:112`), and **every** Azure/GCP purchase client read (Azure `compute/client.go:209`, GCP `computeengine/client.go:295`, etc.), implement the older **2-arg** `PurchaseCommitment(ctx, rec)` with no `opts` parameter - no `Source`, no `IdempotencyToken`. This was a static-read discrepancy, not a confirmed compile failure - **verify with a build before scoping any Azure/GCP/RDS/ElastiCache MCP tool**.

Practical consequence for this design: only AWS EC2, Redshift, MemoryDB, OpenSearch and Savings Plans currently have a `Source`/idempotency-aware purchase path. Everything else needs a small prerequisite PR (see §9, PR-0) before it can safely be an MCP "real purchase" tool. Also worth noting: the Azure VM reservation ID (`compute/client.go:217`, `fmt.Sprintf("vm-reservation-%d", time.Now().Unix())`) and GCP CUD name (`computeengine/client.go:325`) are timestamp-derived, not idempotency-tokened - a retried call after a network timeout can create a second real commitment. Pre-existing bug, out of scope here, flagged per CLAUDE.md.

## 1. CLI surface map

`ri-helper` has **no subcommands** - one root command, service selection via `--services`/`--all-services`, purchase toggled via `--purchase`.

| Flag | Purpose | Required? | Allowed values | Money-affecting | Cite |
|---|---|---|---|---|---|
| `--services, -s` | which resource types to act on | no (default `rds`) | `rds,elasticache,ec2,opensearch,elasticsearch,redshift,memorydb,savingsplans,sp` | yes (selects what gets bought) | `cmd/main.go:86,262-274` |
| `--all-services` | process all 7 supported services | no | bool | yes | `cmd/main.go:87,288-298` |
| `--coverage, -c` | % of recs to buy | no (80.0) | 0-100 | yes | `cmd/main.go:88,122-124` |
| `--purchase` | dry-run (false) vs real purchase (true) | no | bool | **yes - the master switch** | `cmd/main.go:89` |
| `--payment, -p` | payment option | no (`no-upfront`) | `all-upfront, partial-upfront, no-upfront` (bare strings, not a typed enum - `cmd/main.go:147-154`) | yes | `cmd/main.go:92,147-154` |
| `--term, -t` | term length | no (3) | `1` or `3` (int, validated `cmd/main.go:157-159`) | yes | `cmd/main.go:93` |
| `--profile` | AWS named profile | no | string, `~/.aws/config` names | no | `cmd/main.go:94` |
| `--yes` | skip stdin confirmation | no | bool | **do not ever pass this** (memory `feedback_no_yes_flag`) | `cmd/main.go:105` |
| `--max-instances`, `--override-count` | hard caps | no | int, capped at `MaxReasonableInstances=10000` | yes | `cmd/main.go:32,106-107` |
| `--include/exclude-{regions,instance-types,engines,accounts}` | filters | no | free strings, cross-checked for conflicts | no | `cmd/main.go:97-104` |
| `--include/exclude-sp-types` | SP filter | no | `Compute, EC2Instance, SageMaker, Database` | yes (scope) | `cmd/main.go:112-113` |
| `--input-csv`, `-o/--output` | I/O paths | no | `.csv` path | no | `cmd/main.go:90-91` |

No idempotency-key flag exists on the CLI. Idempotency is provider-side: AWS EC2 RIs are checked-then-tagged with `common.IdempotencyTagKey` (`pkg/common/types.go:266`); Savings Plans use the native `ClientToken` (`providers/aws/services/savingsplans/client.go:204`); the CLI path always leaves `PurchaseOptions.IdempotencyToken` empty (`cmd/multi_service_helpers.go:237`, doc comment `pkg/common/types.go:284-288`). `--purchase` blocks on a **stdin** confirmation prompt unless `--yes` is set (`cmd/helpers.go:160-176`, `bufio.NewReader(os.Stdin)`).

Azure/GCP: no CLI surface. Their `PurchaseCommitment` methods are the only purchase entrypoint, callable only via Go code (`internal/api` today).

## 2. SDK choice: **Go, calling internal packages directly - do not shell out**

Two hard reasons rule out wrapping the built `ri-helper` binary:

1. **It would deadlock.** `--purchase` without `--yes` blocks on `os.Stdin` (`cmd/helpers.go:168-169`). An MCP subprocess has no interactive stdin. Passing `--yes` to unblock it is explicitly forbidden by the project's own safety rule (`feedback_no_yes_flag`).
2. **It can't reach Azure/GCP.** The CLI's dispatch table is AWS-only (§0/§1).

Instead, call `pkg/provider.CreateProvider(name, cfg)` (`pkg/provider/factory.go:12`) then `Provider.GetServiceClient(ctx, service, region)` then `ServiceClient.PurchaseCommitment(ctx, rec, opts)` directly, in-process, in Go. This is exactly what `cmd/multi_service_helpers.go:231-247` already does - the MCP server becomes a second caller of the same internal API the CLI uses, with its own confirmation gate (the tool's `confirm`/`dry_run` params) replacing the stdin prompt. Recommend **`github.com/modelcontextprotocol/go-sdk`** (the official Go SDK) over `mark3labs/mcp-go`: it's the spec owner's reference implementation, keeps typed JSON Schema generation from Go structs (fits the "no bare strings" convention already enforced in this repo), and avoids a second Go MCP dependency tree competing with any future first-party tooling. Python/TypeScript would require re-implementing the provider/credential/idempotency logic outside Go, duplicating logic the memory garden explicitly warns against (`feedback_no_hardcoded_magic_values`, `feedback_sdk_enum_string_literals`).

## 3. Tool surface

One tool per (provider, product, action). Naming: `cudly_<provider>_<product>_<action>` (snake_case, provider-first so search/autocomplete groups by cloud). Every tool's params require `dry_run: bool` (default `true`) and `confirm: bool` (default `false`); real purchases require `dry_run=false AND confirm=true`. `source` is **not** a free string param - the server injects a fixed enum member (see §7); exposing it as freeform would violate `common.NormalizeSource` (`pkg/common/types.go:301-311`, allowlist of exactly `cudly-cli`/`cudly-web`).

Phase-1 tool (only one that's safe to ship today per §0):

```text
name: cudly_aws_ec2_ri_purchase
description: "Purchase AWS EC2 Reserved Instances from a Cost Explorer recommendation.
  THIS SPENDS REAL MONEY when dry_run=false and confirm=true. Always run with
  dry_run=true first to preview cost and instance count before committing."
params (JSON Schema):
  region: string (required, e.g. "us-east-1")
  instance_type: string (required, e.g. "m5.large")
  count: integer (required, >0)
  term_years: integer (required, enum: [1, 3])
  payment_option: string (required, enum: ["all-upfront","partial-upfront","no-upfront"])
  dry_run: boolean (default: true)
  confirm: boolean (default: false)
returns:
  { success: bool, dry_run: bool, commitment_id: string, cost: number,
    on_demand_cost: number, estimated_savings: number, savings_percentage: number,
    effective_date: string (RFC3339), term_years: int, error: string|null }
```

Same shape repeats for `cudly_aws_savingsplans_purchase` (adds `sp_type` enum `Compute|EC2Instance|SageMaker|Database` per `--include-sp-types`, `cmd/main.go:112`), `cudly_aws_rds_ri_purchase`, `cudly_aws_elasticache_ri_purchase`, `cudly_aws_opensearch_ri_purchase`, `cudly_aws_redshift_ri_purchase`, `cudly_aws_memorydb_ri_purchase` - each **blocked until PR-0** (§9) adds `opts` support to `rds`/`elasticache`. Azure/GCP tools (`cudly_azure_compute_ri_purchase`, `cudly_gcp_computeengine_cud_purchase`, …) are blocked the same way, plus need the retry-safety fix noted in §0.

Meta-tools:

- `cudly_list_commitment_actions` - returns the live tool catalog (name, provider, product, whether real-purchase is currently enabled) plus 2-3 example prompts per tool, generated from a single source-of-truth registry in code (§6), never hand-duplicated in docs.
- `cudly_search_recommendations` - wraps the existing `ServiceClient.GetRecommendations` / `RecommendationsClient.GetAllRecommendations` (`pkg/provider/interface.go:44,65`), the same call `cmd/multi_service.go` Phase 1 makes before purchasing. Read-only, no `confirm`/`dry_run` needed.

## 4. Config exposure

Env-vars-first, matching each provider's existing ambient-credential model - no new CUDly-specific credential file:

- **AWS**: `AWS_PROFILE` / `AWS_ACCESS_KEY_ID`+`AWS_SECRET_ACCESS_KEY` / IAM role, same as `--profile` (`cmd/main.go:94`). MCP tool params carry an optional `aws_profile` override per call, mapped to `provider.ProviderConfig.AWSProfile` (`pkg/provider/interface.go:87`).
- **Azure**: `AZURE_CLIENT_ID/SECRET/TENANT_ID` (service principal) or `az login` state, via `azidentity.NewDefaultAzureCredential` (`providers/azure/provider.go:83,149`); `AZURE_SUBSCRIPTION_ID` env var selects the subscription (`providers/azure/provider.go:234`), with a per-call `azure_subscription_id` param overriding it via `ProviderConfig.AzureSubscriptionID`.
- **GCP**: `GOOGLE_APPLICATION_CREDENTIALS` (service-account JSON) or ADC (`providers/gcp/provider.go:220-230`); per-call `gcp_project_id` param overrides via `ProviderConfig.GCPProjectID`.

The MCP server process itself takes zero CUDly-specific config beyond the provider list to register (`~/.claude/mcp.json` env block); every per-user override happens through per-tool-call params, so one running server instance serves any AWS profile/Azure subscription/GCP project the caller names in the request - it never has to be restarted to switch accounts.

## 5. Discoverability

- Naming convention `cudly_<provider>_<product>_<action>` sorts and greps predictably.
- Every description leads with the money-impact sentence and the dry-run recommendation (mandatory template, enforced in code review, not just this doc - see §6).
- `cudly_list_commitment_actions` is the anchor: a session that doesn't know the tool names starts there and gets example prompts ("buy 3-year no-upfront RIs for db.r6g.large in us-east-1").
- `cudly_search_recommendations` is the natural precursor tool - its output's `SourceRecommendation`/resource fields map 1:1 onto the purchase tools' required params, so a session naturally chains search then purchase.

## 6. Documentation plan

`mcp/README.md`: Install (`go install` or prebuilt binary) then Configure credentials (one subsection per provider, mirroring §4) then Launch (`cudly-mcp serve`) then Register in `~/.claude/mcp.json` (example block) then Worked example: `cudly_search_recommendations` then `cudly_aws_ec2_ri_purchase(dry_run=true)` then review output then re-run with `confirm=true, dry_run=false` then Troubleshooting (stuck-pending Azure/GCP polling, credential errors, rate limits).

Source of truth for per-tool docs lives in code: each tool's Go struct carries its `description` and JSON Schema field docs as struct tags/const strings next to the handler function, and `cudly_list_commitment_actions` + the README's tool table are both generated from that same registry (a `go generate` step), so the two can't drift.

## 7. Safety rails

- `dry_run` defaults `true`; server-side gate: refuse any provider call when `!(confirm && !dry_run && source != "")` - return a structured error, not a silent no-op.
- `source` is never user-supplied free text. The server hardcodes a new enum member, e.g. `PurchaseSourceMCP = "cudly-mcp"`, added to the allowlist in `pkg/common/types.go:251-254`/`NormalizeSource` (a one-line, reviewable addition - flagged as a required prerequisite change, not something to route around with a raw string).
- Never pass `--yes` - moot once we stop shelling out to the binary (§2), but the rule still applies to any test harness that does invoke the CLI.
- Missing/invalid money-affecting fields (term, payment option, region, count) return an explicit JSON-RPC error; no defaulting, per `feedback_no_silent_fallbacks`/`feedback_no_hardcoded_magic_values`.
- Every tool call's underlying SDK/HTTP failure surfaces full provider error text + a stable error code back to Claude - never swallowed.
- Rate-limited retry wraps only the outbound `PurchaseCommitment`/SDK call (per `feedback_semaphore_at_api_call`), not tool-param validation or the confirm gate.
- Each real-purchase tool call is idempotency-tokened: the server derives a token from a caller-supplied or server-generated per-call key via `common.DeriveIdempotencyToken` (`pkg/common/tokens.go:41`) and threads it as `PurchaseOptions.IdempotencyToken` - for the 5 already-opts-aware AWS clients today; blocked elsewhere until PR-0.

## 8. File layout

```text
mcp/
  server.go               # entrypoint, registers tools, wires provider registry
  registry.go             # single source-of-truth tool catalog (name, schema, desc)
  tools/
    aws_ec2_ri.go
    aws_savingsplans.go
    aws_rds_ri.go          # gated behind PR-0
    azure_compute_ri.go    # gated behind PR-0 + retry-safety fix
    gcp_computeengine_cud.go
    search_recommendations.go
    list_commitment_actions.go
  README.md
cmd/
  cudly-mcp/main.go        # thin `main` wiring mcp/server.go - kept out of the
                           # existing `ri-helper` main.go, never registered as
                           # a lambda handler
```

Keep `mcp/` and `cmd/cudly-mcp/` out of `iac/`, `terraform/`, and any Lambda packaging path (`internal/api` build target) - it's a local/desktop MCP server, not a deployed artifact.

## 9. Implementation sequence

- **PR-0 (prerequisite, blocking, not glamorous)**: Confirm with `go build ./...` whether `rds`/`elasticache`/Azure/GCP clients actually satisfy `provider.ServiceClient` as declared. If not, add the `opts common.PurchaseOptions` parameter to their `PurchaseCommitment` signatures and thread `Source`/tagging through, matching the EC2/Redshift pattern. This unblocks every non-AWS-EC2-ish tool below.
- **PR-1**: `mcp/` skeleton + `cudly_list_commitment_actions` (no real tools yet) - proves the transport and registration work.
- **PR-2**: `cudly_search_recommendations` (read-only, no money risk) - proves provider auth/config wiring (§4).
- **PR-3**: `cudly_aws_ec2_ri_purchase`, dry-run only enforced in tests, one test asserting `confirm=false` refuses execution. Gates on PR-1/2.
- **PR-4**: add `common.PurchaseSourceMCP` enum member + idempotency-token wiring (§7). Gates on PR-3.
- **PR-5**: `cudly_aws_savingsplans_purchase` (adds SP-type enum). Gates on PR-4.
- **PR-6**: remaining AWS tools (`rds`, `elasticache`, `opensearch`, `redshift`, `memorydb`). Gates on PR-0.
- **PR-7**: Azure tools, after fixing the timestamp-based reservation-ID retry-safety gap (§0). Gates on PR-0.
- **PR-8**: GCP tools, after fixing the `GENERAL_PURPOSE`/`ResourceCommitment.Type` raw-string-literal issue flagged in §0 (same bug class as the already-fixed `MEMORY_MB` incident per memory). Gates on PR-0.
- **PR-9**: `mcp/README.md` + doc-generation wiring (§6).

## 10. Open questions for the user

1. Should PR-0 open first as its own reviewable change (confirming/fixing the `ServiceClient` interface mismatch), before any MCP code lands? Recommended yes - it's a real correctness gap independent of MCP.
2. `source`: confirm the design choice to hardcode a new `cudly-mcp` enum value server-side rather than exposing `source` as a tool param at all (assumed the latter based on `NormalizeSource`'s allowlist).
3. Do you want Azure/GCP real-purchase tools gated behind the retry-safety fixes in §0, or shipped dry-run-only until those land?
4. Should `mcp/` live in this monorepo (as designed) or as a separate repo consuming CUDly's Go modules - affects whether it's covered by this repo's CI/pre-commit gates?
5. Given the untracked working-tree state (§0), does `main` (the actual git history) not yet contain `internal/`, `cmd/multi_service_*.go`, or the Azure/GCP service clients? If so, PR-0 through PR-9 need to land on top of whatever gets committed first - this changes the PR base significantly.

---

Key files cited: `cmd/main.go`, `cmd/multi_service_helpers.go`, `cmd/helpers.go`, `pkg/provider/interface.go`, `pkg/provider/factory.go`, `pkg/common/types.go`, `pkg/common/tokens.go`, `providers/aws/services/{ec2,rds,elasticache,redshift,memorydb,opensearch,savingsplans}/client.go`, `providers/aws/internal/tagging/purchase_tags.go`, `providers/azure/{provider.go,services/compute/client.go}`, `providers/gcp/{provider.go,services/computeengine/client.go}`.
