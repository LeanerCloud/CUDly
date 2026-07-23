# CUDly MCP Server

`cudly-mcp` exposes CUDly's reserved-capacity search and purchase tools (AWS EC2/RDS/ElastiCache/OpenSearch/Redshift/MemoryDB/Savings Plans, Azure VM Reservations, GCP Compute Engine CUDs) to any MCP client -- Claude Code, Claude Desktop, or another MCP-speaking agent -- as a local process. It is a thin wrapper around the same in-process Go packages (`pkg/provider`, `pkg/common`) the `ri-helper` CLI uses; it never shells out to `ri-helper`, and it is not deployed anywhere (see [Deployment model](#deployment-model)).

Every purchase tool is dry-run by default (`dry_run=true`) and requires an explicit `confirm=true` alongside `dry_run=false` before it spends money. See [Safety model](#safety-model).

## Install

From the repository root:

```bash
go install ./cmd/cudly-mcp
```

This installs the `cudly-mcp` binary to `$(go env GOBIN)` (or `$(go env GOPATH)/bin` if `GOBIN` is unset); make sure that directory is on your `PATH` so `cudly-mcp` resolves without a full path.

Or run directly without a separate install step:

```bash
go run ./cmd/cudly-mcp
```

There is no separate module or release artifact for `cudly-mcp` yet -- install it from a checkout of this repository.

## Configure credentials

The server takes no CUDly-specific configuration of its own. Each provider tool authenticates the same way the corresponding CUDly CLI path does, and every tool call also accepts a per-call override (`aws_profile`, `azure_subscription_id`, `gcp_project_id`) so one running server instance can serve requests against different accounts/subscriptions/projects without a restart.

### AWS

One of, in the usual SDK precedence order:

- `AWS_PROFILE` (matches a profile in `~/.aws/config` / `~/.aws/credentials`)
- `AWS_ACCESS_KEY_ID` + `AWS_SECRET_ACCESS_KEY` (+ optional `AWS_SESSION_TOKEN`)
- An IAM role (EC2 instance profile, ECS task role, etc.)

Per-call override: pass `aws_profile` on any AWS tool call to use a specific named profile for that call only.

### Azure

One of:

- A service principal via `AZURE_CLIENT_ID`, `AZURE_CLIENT_SECRET`, `AZURE_TENANT_ID`
- Local `az login` state (picked up by `azidentity.NewDefaultAzureCredential`)

Set `AZURE_SUBSCRIPTION_ID` to select the subscription, or pass `azure_subscription_id` on a per-call basis to override it.

### GCP

One of:

- `GOOGLE_APPLICATION_CREDENTIALS` pointing at a service-account JSON key file
- Application Default Credentials (`gcloud auth application-default login`)

Pass `gcp_project_id` on a per-call basis to override the ambient project.

## Launch

```bash
cudly-mcp
```

The server speaks MCP over stdio and logs diagnostics to stderr; it does not print anything to stdout other than protocol traffic, so it is safe to launch directly from an MCP client's process-spawning config (below) rather than through a wrapper script.

## Register with an MCP client

Add an entry to your client's MCP server config. For Claude Code, this is `~/.claude/mcp.json`. If the client does not inherit your shell's `PATH`, use the absolute path `go install` reported: `$(go env GOBIN)/cudly-mcp` if `GOBIN` is set, otherwise `$(go env GOPATH)/bin/cudly-mcp` (`$(go env GOBIN)` expands to an empty string when `GOBIN` is unset, so that path alone is not a valid binary location):

```json
{
  "mcpServers": {
    "cudly": {
      "command": "/absolute/path/to/cudly-mcp",
      "env": {
        "AWS_PROFILE": "my-aws-profile",
        "AZURE_SUBSCRIPTION_ID": "00000000-0000-0000-0000-000000000000"
      }
    }
  }
}
```

`env` is optional -- omit it entirely to rely on whatever ambient credentials are already active in the shell that launches the client, or set only the provider(s) you actually use.

## Worked example

A typical session searches for a recommendation, previews the purchase, then executes it:

1. **Search**: call `cudly_search_recommendations` with `provider="aws"`, `service="ec2"`, `region="us-east-1"` to see what AWS Cost Explorer currently recommends reserving.
2. **Preview**: take a result's `region`/`resource_type`/`count` and call `cudly_aws_ec2_ri_purchase` with those values and `term_years`/`payment_option` of your choice. Leave `dry_run` at its default (`true`) -- the response validates your parameters without contacting AWS or spending anything, and reports `cost`/`on_demand_cost`/`estimated_savings`/`savings_percentage` only when a real figure is actually known (omitted otherwise, never a fabricated `0`).
3. **Execute**: once the preview looks right, call the same tool again with `dry_run=false, confirm=true`. This is the only combination that performs a real purchase; any other combination either previews or returns an explicit refusal error (see [Safety model](#safety-model)).

Every other provider's purchase tool (`cudly_aws_savingsplans_purchase`, `cudly_aws_rds_ri_purchase`, `cudly_azure_compute_ri_purchase`, `cudly_gcp_computeengine_cud_purchase`, ...) follows the identical dry_run-then-confirm pattern. Call `cudly_list_commitment_actions` at any point for the full, always-current list of tools, which ones can spend real money today, and 2-3 example prompts per tool.

## Safety model

- `dry_run` defaults to `true` on every purchase tool. A dry-run call never contacts the cloud provider and never spends money -- it only validates your parameters. It reports pricing (`cost`/`on_demand_cost`/`estimated_savings`/`savings_percentage`) only when a real figure is genuinely known; those fields are omitted, not zeroed, when it isn't.
- A real purchase requires **both** `dry_run=false` **and** `confirm=true`. `dry_run=false` with `confirm=false` (or vice versa) is refused with a structured error, not silently downgraded to a preview or silently ignored.
- Every money-affecting parameter (region, resource type, count, term, payment option, and any provider-specific dimension such as RDS's `az_config`) is validated against an explicit enum or non-empty check before anything is built or sent. There is no silent default for a value that materially changes what gets purchased.
- Every real purchase is tagged with a source identifying it came from this MCP server (never a user-suppliable string) and a deterministic idempotency token derived from the request's own parameters. By default, retrying an identical tool call -- however long after the original, and regardless of any clock boundary -- always derives the same token, so the provider dedupes the retry instead of buying twice; this is a fail-safe default, since the worst case of a false dedupe is a skipped intentional repeat, never a double purchase. To deliberately make a second, otherwise-identical purchase (e.g. "buy 3 RIs now" and "buy 3 more next week"), pass a fresh `idempotency_nonce` value on the second call; passing the same nonce on a retry of that same call still dedupes correctly.
- Provider/SDK failures surface their full error text back to the caller; nothing is swallowed.

## Caveats and known gaps

These are pre-existing behaviours in the underlying purchase clients, not something introduced by or specific to the MCP server -- flagged here so you know what to expect:

- **Azure VM Reservations have no partial-upfront billing plan.** Azure honors exactly two billing plans, all-upfront and no-upfront (billed monthly, same total price -- Azure charges no premium for spreading payments), and `cudly_azure_compute_ri_purchase` defaults `payment_option` to no-upfront when omitted. `payment_option=partial-upfront` has no Azure equivalent and is rejected with an explicit error rather than silently purchased under all-upfront or no-upfront instead.
- **GCP Compute Engine CUDs commit resources, not instances.** `cudly_gcp_computeengine_cud_purchase` takes `vcpu_count` and `memory_gb` directly (a CUD is a vCPU+memory commitment), not an instance count -- there is no implicit vCPU-per-instance conversion.

## Deployment model

`cudly-mcp` is a local/desktop process, not a deployed service: it is intentionally kept out of `iac/`, `terraform/`, and the `internal/api` Lambda packaging path. Run it on the same machine as your MCP client.

## Troubleshooting

- **"provider ... is not configured" / credential errors**: confirm the relevant environment variable(s) from [Configure credentials](#configure-credentials) are set in the shell (or the client's `env` block) that launches `cudly-mcp`, or pass the matching per-call override (`aws_profile` / `azure_subscription_id` / `gcp_project_id`).
- **Azure/GCP purchase calls appear to hang**: Azure Reservations and GCP Compute Commitments both provision asynchronously after the purchase call returns; a `success=true` response means the purchase request was accepted, not necessarily that the resource is already active in the portal/console. Re-run `cudly_search_recommendations` or check the provider console if you need to confirm activation state.
- **Rate limits / throttling from the cloud provider**: retry the same tool call with the same parameters -- the idempotency token guarantees a retry cannot double-purchase no matter how long you wait before retrying (see [Safety model](#safety-model)). If you genuinely want a second, separate purchase with the same parameters instead of a retry, pass a fresh `idempotency_nonce` value.
- **"invalid ... must be one of ..." errors**: every enum-typed parameter (term, payment option, engine, az_config, sp_type, scope, tenancy, platform) is validated against an explicit allow-list; call `cudly_list_commitment_actions` or re-check this README's per-tool schema for the exact accepted values.
