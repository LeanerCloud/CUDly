# Known Issues

No critical issues at this time. The following are known limitations:

## Azure

- ~~**ARM template role definition scoping + `Reservation Reader` tenant gap**~~: Previously, `arm/CUDly-CrossSubscription/template.json` built role-definition IDs scoped to the subscription (`/subscriptions/{subId}/providers/Microsoft.Authorization/roleDefinitions/...`). Azure built-in roles are GLOBAL resources — scoping the definition ID to the subscription caused ARM to look for the role inside the subscription, where it isn't registered; `Reservation Reader` (`582fc458-8989-419f-a480-75249a578f9d`) consistently failed with `RoleDefinitionDoesNotExist`, and `Reservation Reader` itself is not provisioned in every Azure tenant. **Resolved:** the template now uses the unscoped global path (`/providers/Microsoft.Authorization/roleDefinitions/{id}`) for all three surviving role definitions, and the `Reservation Reader` assignment was dropped in favour of a FOURTH `Reservation Purchaser` assignment at `/providers/Microsoft.Capacity` scope (a superset that is available in every tenant). Operators who previously applied the buggy template may need to clean up the orphaned subscription-scoped `Reservation Reader` assignment manually via `az role assignment delete --assignee <sp-object-id> --role "Reservation Reader" --scope /subscriptions/<subId>` — the next `az deployment sub create` won't delete it because ARM's resource model no longer references the obsolete assignment name.

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
