# Known Issues

No critical issues at this time. The following are known limitations:

## Azure

- **Azure SMTP credential generation requires manual Azure Portal step**: Azure Communication Services SMTP credentials cannot be auto-generated via Terraform. See the documentation in `terraform/modules/secrets/azure/main.tf` for manual setup instructions.

## Azure AKS Module

- **Helm LoadBalancer IP may not be available on first apply**: The NGINX ingress LoadBalancer IP is provisioned asynchronously by Azure. The `load_balancer_ip` output uses `try()` to return `""` when pending. A subsequent `terraform apply` resolves the IP.

## RI Exchange

- **Same-family-only recommendations**: `AnalyzeReshaping` only suggests targets within the same instance family (e.g. m5.xlarge -> m5.large). Cross-family recommendations (e.g. m5 -> m6i) would require offering ID lookup and pricing comparison via the EC2 `DescribeReservedInstancesOfferings` API. AWS normalization-based exchange rules constrain exchanges to the same family for size-flexibility adjustments, so cross-family support is a distinct feature expansion.

- **Multi-target exchange**: The AWS exchange APIs accept multiple `TargetConfigurationRequest` entries, allowing a single exchange to produce several target RIs. The current implementation supports only one target per exchange. Supporting multi-target would require rethinking the request types, validation logic, and spend-cap guardrails.

- **Utilization caching**: `getRIUtilization` and `getReshapeRecommendations` call Cost Explorer on every request. Cost Explorer charges per API call and is rate-limited. Adding a cache would reduce costs and improve response times, but the cache design (key strategy incorporating region + lookback_days, TTL policy, invalidation on RI changes) warrants separate planning.
