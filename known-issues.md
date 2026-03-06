# Known Issues

No critical issues at this time. The following are known limitations:

## Azure

- **Azure SMTP credential generation requires manual Azure Portal step**: Azure Communication Services SMTP credentials cannot be auto-generated via Terraform. See the documentation in `terraform/modules/secrets/azure/main.tf` for manual setup instructions.

## Azure AKS Module

- **Helm LoadBalancer IP may not be available on first apply**: The NGINX ingress LoadBalancer IP is provisioned asynchronously by Azure. The `load_balancer_ip` output uses `try()` to return `""` when pending. A subsequent `terraform apply` resolves the IP.

## Code Quality

- **16 Go functions exceed cyclomatic complexity 10**: The CI pipeline enforces a threshold of 10 but these pre-existing functions exceed it. They should be refactored over time. Highest offenders:
  - `(*Client).GetRIUtilization` (19) — `providers/aws/recommendations/utilization.go`
  - `(*DBRateLimiter).Allow` (16) — `internal/api/db_rate_limiter.go`
  - `(*Handler).patchPlan` (16) — `internal/api/handler_plans.go`
  - `AnalyzeReshaping` (14) — `pkg/exchange/reshape.go`
  - `serveStaticForLambda` (14) — `internal/server/static.go`
  - `(*SMTPSender).sendMailTLS` (13) — `internal/email/smtp_sender.go`
  - `(*Handler).getReshapeRecommendations` (13) — `internal/api/handler_ri_exchange.go`
  - `SanitizeReservationID` (12) — `pkg/common/identifiers.go`
  - Plus 8 more functions with complexity 11
