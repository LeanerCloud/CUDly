# Known Issues

## GCP

- **SPA routing returns 404 status**: Client-side routes like `/settings` return HTTP 404 with index.html content. The GCS bucket `not_found_page` serves the correct HTML but with 404 status code. `default_custom_error_response_policy` (which would return 200) is not supported on classic EXTERNAL scheme load balancers - would require migration to EXTERNAL_MANAGED scheme.
- **Cloud SQL allows unencrypted connections**: `require_ssl` is not enforced on the Cloud SQL instance. Should be enabled for production.
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

## Cross-Provider

- **npm install consistency**: AWS uses `npm install --production`, GCP/Azure use `npm install`. Should be consistent across providers.
- **Security headers not set at CDN level**: HSTS, X-Content-Type-Options, X-Frame-Options, Referrer-Policy, and CSP headers are served by the application but not enforced at the CDN/LB layer. Consider adding response header policies.
