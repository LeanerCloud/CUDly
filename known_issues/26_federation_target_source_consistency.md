# Known Issues: Federation IaC — Target/source consistency validation

> **Audit status (2026-04-22):** `1 needs triage · 0 resolved (new file)`

Surfaced during the zero-touch federation bundle work
(`fix(api): pre-fill ContactEmail from Session.Email and fail loud on empty SourceAccountID`).

## LOW: Reject target/source combinations that cannot work for the current CUDly deployment

**File**: `internal/api/handler_federation.go::getFederationIaC` (lines ~129-144)

**Description**:

A customer can request `?target=aws-cross-account&source=azure` against a CUDly
deployment that runs on Azure. The `aws-cross-account` bundle requires CUDly's
own AWS account ID (`source_account_id`) for the trust policy — but if CUDly
isn't on AWS, that ID doesn't exist. Today the bundle ships with an empty
`source_account_id` and breaks at `terraform apply` with a cryptic IAM error.

The zero-touch bundle work added a fail-loud check for the AWS case
(`if sourceCloud() == "aws"` and `SourceAccountID == ""`), but that check fires
only when `sourceCloud()` is already "aws". It doesn't catch the case where a
customer requests a bundle format that _requires_ an AWS source identity
from a non-AWS CUDly deployment.

**Concrete bad combinations**:

| Requested target | Requires CUDly source | Fails when CUDly runs on |
| --- | --- | --- |
| `aws-cross-account` | AWS account ID | Azure, GCP |
| `aws-target` (WIF) | None (customer supplies their own AWS account) | — |

For `aws-cross-account`, the correct fix is to reject with HTTP 400:

```go
if target == "aws" && source == "aws" && sourceCloud() != "aws" {
    return nil, NewClientError(400,
        fmt.Sprintf("target=aws-cross-account requires CUDly to be deployed on AWS; this deployment is on %s", sourceCloud()))
}
```

**Why deferred**: The user-reported bug was a CUDly-on-AWS case where STS failed
(now fixed with a 500). The cross-deployment mismatch is a rarer edge case with
lower customer impact. The current behaviour (bundle downloads, apply fails) is
strictly worse than a clear 400 at download time, but it's not a regression from
this commit.

**Effort**: small (~10 LOC + 3 tests). Tests call `getFederationIaC` with
`target=aws-cross-account&source=aws` while setting `CUDLY_SOURCE_CLOUD=azure`,
and assert a 400 is returned.

**Status**: not yet triaged.
