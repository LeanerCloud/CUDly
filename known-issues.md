# Known Issues

No critical issues at this time. The following are known limitations:

## Azure

- **ARM template role definition scoping breaks `Reservation Reader` assignment**: The `arm/CUDly-CrossSubscription/template.json` constructs role definition IDs scoped to the subscription (e.g. `/subscriptions/{subId}/providers/Microsoft.Authorization/roleDefinitions/...`). Azure built-in roles are global resources — this causes ARM to look for the role definition within the subscription, where it may not be registered. `Reservation Reader` (`582fc458-8989-419f-a480-75249a578f9d`) is a billing/capacity-scope role and consistently fails with `RoleDefinitionDoesNotExist`. **Fix:** use the unscoped global path for all role definitions (e.g. `/providers/Microsoft.Authorization/roleDefinitions/{id}`). **Workaround:** assign the first three roles via `az role assignment create --scope /subscriptions/{subId}` using `arm/CUDly-CrossSubscription/assign-roles.sh`. `Reservation Reader` (`582fc458-8989-419f-a480-75249a578f9d`) is not provisioned in all Azure tenants — confirmed absent in Archera's test tenant via `az role definition list` (only `Reservation Purchaser` appears under reservation-related roles). This is a Microsoft tenant provisioning gap, not a permissions issue. Registering the `Microsoft.Capacity` resource provider (`az provider register --namespace Microsoft.Capacity`) may cause the role to appear; alternatively, the tenant may require an active Reservation purchase or EA agreement. **Workaround:** assign `Reservation Purchaser` at `/providers/Microsoft.Capacity` scope instead — it is a superset of `Reservation Reader` and is available in tenants without active reservations: `az role assignment create --assignee-object-id <sp-object-id> --assignee-principal-type ServicePrincipal --role "Reservation Purchaser" --scope /providers/Microsoft.Capacity`

- **Azure SMTP credential generation requires manual Azure Portal step**: Azure Communication Services SMTP credentials cannot be auto-generated via Terraform. See the documentation in `terraform/modules/secrets/azure/main.tf` for manual setup instructions.

## Azure AKS Module

- **Helm LoadBalancer IP may not be available on first apply**: The NGINX ingress LoadBalancer IP is provisioned asynchronously by Azure. The `load_balancer_ip` output uses `try()` to return `""` when pending. A subsequent `terraform apply` resolves the IP.

## RI Exchange

- **Same-family-only recommendations**: `AnalyzeReshaping` only suggests targets within the same instance family (e.g. m5.xlarge -> m5.large). Cross-family recommendations (e.g. m5 -> m6i) would require offering ID lookup and pricing comparison via the EC2 `DescribeReservedInstancesOfferings` API. AWS normalization-based exchange rules constrain exchanges to the same family for size-flexibility adjustments, so cross-family support is a distinct feature expansion.

- **Multi-target exchange**: The AWS exchange APIs accept multiple `TargetConfigurationRequest` entries, allowing a single exchange to produce several target RIs. The current implementation supports only one target per exchange. Supporting multi-target would require rethinking the request types, validation logic, and spend-cap guardrails.

- **Utilization caching**: `getRIUtilization` and `getReshapeRecommendations` call Cost Explorer on every request. Cost Explorer charges per API call and is rate-limited. Adding a cache would reduce costs and improve response times, but the cache design (key strategy incorporating region + lookback_days, TTL policy, invalidation on RI changes) warrants separate planning.

## Test Performance

### Test parallelization

Unit tests do not use t.Parallel(). Adding it could provide 2-4x speedup but requires:
- Auditing test files for shared state (global vars, package-level mocks)
- Ensuring test helpers (createTestUser, createTestService) are goroutine-safe
- Starting with low-risk packages (pkg/exchange, providers/aws/services/ec2) before auth

Candidate packages (low shared state risk, audit still required):
- pkg/exchange
- providers/aws/services/ec2
- internal/api (validation tests only)
