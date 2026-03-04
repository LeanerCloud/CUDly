# Known Issues

## GCP

- DNS zone outputs reference resources that may not exist if subdomain zone not created

## GCP GKE Module (not blocking Cloud Run deployment)

- No scheduled tasks implementation (Cloud Scheduler / CronJob)
- Health checks hardcoded (not configurable via variables)

## Azure

- **Classic CDN API routing uses redirect**: The CDN `url_rewrite_action` issues HTTP 302 redirects to the Container App instead of proxying requests. Only affects classic CDN path (not Front Door). Front Door now has proper API proxy routing.
- **Classic CDN SPA routing limited to file extensions**: The CDN delivery rule only catches requests matching `/[^.]+$` (no dots in path). Nested routes with dots may not get SPA fallback. Only affects classic CDN path.
- DNS zone outputs may reference non-existent resources if subdomain zone not created
- Azure SMTP credential generation requires manual Azure Portal step

## Azure AKS Module (not blocking Container Apps deployment)

- Kubernetes/Helm providers not configured in module - would fail on `terraform apply`
- Helm release data source race condition (LoadBalancer IP may not be ready)
- Network policy enabled but not configured
- No resource quotas or pod disruption budgets

## API / Auth

- **`requireAdmin` blocks API-key auth**: All admin endpoints only accept Bearer token auth. API-key-authenticated requests always get 401. (`internal/api/middleware.go:182-202`)

## AWS Provider

- **`getOfferingClass` conflates payment option with offering class**: Maps `all-upfront` to `convertible`, everything else to `standard`. These are orthogonal dimensions. (`providers/aws/services/ec2/client.go:338-345`)
- **No DI for `pkg/exchange` functions**: `GetExchangeQuote` and `ExecuteExchange` create AWS clients internally, making them untestable without real credentials.

## Frontend

- **Password not base64-encoded in `saveProfile()` and `resetPassword()`**: All other password-sending endpoints use `base64Encode()` but these two don't. (`frontend/src/auth.ts:740-747`, `frontend/src/api/auth.ts:89`)
- **Duplicate logout event handler**: `setupButtonHandlers()` and `updateUserUI()` both add click listeners to the logout button. (`frontend/src/auth.ts:608`)

## Scripts

- **entrypoint.sh migration exit code handling**: `$?` inside `else` block is always 1, silently swallowing migration failures. (`scripts/entrypoint.sh:66-77`)
- **tf-deploy.sh references `$AWS_PROFILE` without `${:-}` under `set -u`**: Script crashes with unbound variable error if neither `AWS_PROFILE` nor `TF_VAR_aws_profile` is set. (`scripts/tf-deploy.sh:90-96`)

## Cross-Provider

- **Security headers not set at CDN level**: HSTS, X-Content-Type-Options, X-Frame-Options, Referrer-Policy, and CSP headers are served by the application but not enforced at the CDN/LB layer. Consider adding response header policies.
- **ADMIN_PASSWORD plaintext in container env vars**: Stored as plain environment variable instead of using cloud secret stores. Affects all compute modules (Fargate, Lambda, Cloud Run, Container Apps, GKE, AKS).
- **Fargate EventBridge task references container `"main"` but definition names it `"app"`**: All scheduled tasks silently fail. (`terraform/modules/compute/aws/fargate/main.tf:658`)
