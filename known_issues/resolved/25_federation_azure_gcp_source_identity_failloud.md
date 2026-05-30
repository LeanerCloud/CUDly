# Known Issues: Federation IaC — Azure/GCP source-identity fail-loud guards

> **Audit status (2026-04-22):** `1 needs triage · 0 resolved (new file)`

Surfaced during the zero-touch federation bundle work
(`fix(api): pre-fill ContactEmail from Session.Email and fail loud on empty SourceAccountID`).
That commit added a fail-loud HTTP 500 for `sourceCloud() == "aws"` when
`resolveSourceAccountID()` returns empty. The analogous check for Azure-deployed
and GCP-deployed CUDly is missing.

## MEDIUM: Extend fail-loud source-identity guard to Azure and GCP deployments

**Files**:

- `internal/api/handler_federation.go::getFederationIaC` (lines ~142-155)
- `internal/api/handler.go::resolveSourceIdentity` (lines ~438-454)

**Description**:

Today `handler.go::resolveSourceIdentity` reads Azure identity fields
(`AZURE_CLIENT_ID`, `AZURE_SUBSCRIPTION_ID`, `AZURE_TENANT_ID`) and the GCP
project ID (`GCP_PROJECT_ID`) from environment variables. If any are unset, the
rendered tfvars/scripts have empty identity fields. Customers' deployments then
break at apply time with confusing errors (e.g., blank `client_id` in a trust
policy).

The AWS-source fix added:

```go
if sourceCloud() == "aws" {
    data.SourceAccountID = h.resolveSourceAccountID(ctx)
    if data.SourceAccountID == "" {
        return nil, fmt.Errorf("federation iac: CUDly failed to resolve its own AWS account ID; ...")
    }
}
```

The same pattern should extend to:

- `sourceCloud() == "azure"`: check `data.SubscriptionID == "" || data.TenantID == "" || data.ClientID == ""`
  — any empty field means the Azure identity is incomplete.
- `sourceCloud() == "gcp"`: check `data.ProjectID == ""`
  — empty GCP project breaks every trust binding.

Return HTTP 500 with a clear operator-facing message (naming the missing env var)
instead of shipping a broken bundle.

**Why deferred**: The Azure and GCP identity fields are read from env vars rather
than a live cloud API, so the failure mode is "env var missing on Lambda cold
start" rather than "API call failed." Lower-urgency than the AWS STS case but the
customer impact is identical: broken trust policy, confusing apply error.

**Fix**:

```go
if sourceCloud() == "azure" {
    id := h.resolveSourceIdentity(ctx)
    if id.SubscriptionID == "" || id.TenantID == "" {
        return nil, fmt.Errorf("federation iac: AZURE_SUBSCRIPTION_ID or AZURE_TENANT_ID is not set; check the Lambda environment variables")
    }
}
if sourceCloud() == "gcp" {
    id := h.resolveSourceIdentity(ctx)
    if id.ProjectID == "" {
        return nil, fmt.Errorf("federation iac: GCP_PROJECT_ID is not set; check the Lambda environment variables")
    }
}
```

**Effort**: small (~5 LOC + 4 tests). Tests mock `resolveSourceIdentity` to return
empty fields and assert a non-client-error 500 is returned.

**Status**: not yet triaged.
