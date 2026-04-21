# Known Issues

The seven limitations previously listed here have been resolved or
scoped-down with explicit follow-ups. This file tracks what's still
outstanding so future work has a clear starting point.

## Resolved

### Azure

- **ARM template role-definition scoping + `Reservation Reader` tenant
  gap**: Resolved. `arm/CUDly-CrossSubscription/template.json` now uses
  unscoped global role-definition paths
  (`/providers/Microsoft.Authorization/roleDefinitions/{id}`) and drops
  the fragile `Reservation Reader` assignment in favour of a
  `Reservation Purchaser` assignment at `/providers/Microsoft.Capacity`
  scope (a superset available in every tenant). Operators who previously
  applied the buggy template may need to clean up the orphaned
  subscription-scoped `Reservation Reader` assignment manually with
  `az role assignment delete --assignee <sp-object-id> --role "Reservation Reader" --scope /subscriptions/<subId>`.

- **Azure ACS SMTP credential generation requires manual portal step**:
  Microsoft's API gap remains (no REST endpoint generates ACS SMTP
  credentials; the Portal is the only supported path). The ergonomic
  gap is closed: `scripts/azure-smtp-setup.sh` prints a pre-filled
  checklist with the direct Azure Portal URL plus the exact `az keyvault
  secret set` commands for this deployment. The `smtp_setup_instructions`
  Terraform output surfaces the command to run at the end of
  `terraform apply`. See `specs/azure-smtp-setup.md` for the runbook and
  troubleshooting.

### Azure AKS Module

- **Helm LoadBalancer IP may not be available on first apply**:
  Resolved. `terraform/modules/compute/azure/aks/main.tf` now emits a
  `time_sleep.wait_for_lb_ip` (5-minute create_duration) between the
  `helm_release.nginx_ingress` and the `kubernetes_service` data source
  read, covering Azure's typical 2–5 minute LB provisioning window.
  First-apply no longer requires a follow-up run in the common case;
  the `try()` fallback on the output still handles the rare
  beyond-budget provisioning tail.

### RI Exchange

- **Same-family-only recommendations**: Resolved with scoped cross-family
  advisory suggestions. `pkg/exchange.ReshapeRecommendation` now carries
  an advisory `AlternativeTargetInstanceTypes []string` field populated
  from a peer-family allowlist (general-purpose `m5/m6i/m7g`,
  compute-optimised `c5/c6i/c7g`, memory-optimised `r5/r6i/r7g`,
  burstable `t3/t3a/t4g`). The UI can surface these as alternatives
  alongside the primary target; the auto-exchange pipeline still acts
  on the primary target only so existing automated behaviour is
  unchanged. Specialty (p\*/g\*/x\*/hpc\*) and legacy-generation
  (m4/c4/r3) families are deliberately out of the allowlist — see the
  follow-up below.

- **Multi-target exchange**: Resolved. `pkg/exchange.ExchangeQuoteRequest`
  and `ExchangeExecuteRequest` accept a `Targets []TargetConfig` slice;
  legacy `TargetOfferingID` / `TargetCount` fields are retained as a
  single-target alias so existing callers keep working. The HTTP API
  gains an optional `targets[]` array on the quote + execute bodies;
  when present it wins over the legacy singleton fields. Spend-cap
  semantics: AWS returns a single aggregated `PaymentDue` across all
  targets, so `max_payment_due_usd` naturally functions as a TOTAL cap
  for multi-target requests. Frontend UI for exposing multi-target is a
  separate follow-up.

- **Utilization caching**: Resolved with a Postgres-backed TTL cache.
  Migration `000031_ri_utilization_cache` adds
  `ri_utilization_cache (region, lookback_days, payload, fetched_at)`.
  `internal/api/handler_ri_exchange.go` routes both
  `getRIUtilization` and `getReshapeRecommendations` through the cache
  wrapper (`internal/api/ri_utilization_cache.go`) so one Cost Explorer
  call per TTL window serves every warm and cold Lambda container. TTL
  configurable via `CUDLY_RI_UTILIZATION_CACHE_TTL` (default `15m`,
  matches CE's hourly upstream refresh cadence). Errors are never
  cached — a transient CE 5xx cannot lock the dashboard out for the
  full TTL. See the Config section of `specs/recommendations-cache.md`.

### Test Performance

- **t.Parallel() adoption (partial)**: Resolved for three audit-safe
  packages — `pkg/exchange/{auto,exchange,reshape}_test.go`,
  `providers/aws/services/ec2/client_test.go`, and
  `internal/api/validation_test.go`. Remaining packages haven't been
  audited per-file and keep their sequential execution — see the
  follow-up below.

## Outstanding follow-ups

- **Cross-family RI recommendations for specialty + legacy families**:
  `pkg/exchange/reshape.go`'s `peerFamilyGroups` allowlist covers
  general/compute/memory/burstable mainstream families only. Specialty
  families (`p*`, `g*`, `x*`, `hpc*`) and legacy generations (`m4`,
  `c4`, `r3`) return no cross-family alternatives because AWS's
  `$`-units check routinely rejects exchanges for these shapes — adding
  them would hurt user trust. A proper fix requires live offering
  enumeration via `DescribeReservedInstancesOfferings` + pricing
  comparison, which is a multi-day feature scoped separately.

- **t.Parallel() adoption for remaining packages**: Adoption is complete
  only for `pkg/exchange/`, `providers/aws/services/ec2/`, and
  `internal/api/validation_test.go`. Other packages need a per-test-file
  audit for shared state before parallelizing:

  - `internal/api/` (other test files besides `validation_test.go`) use
    handler fixtures and shared mocks; not race-safe without review.
  - `internal/config/*_test.go` integration tests share a Postgres
    container and cannot naively parallelize.
  - `internal/server/app_test.go` uses package-level vars
    (`runMigrations`, `migrationsTimeout`) that are not race-safe.
  - Any test file using `os.Setenv`/`t.Setenv` for process-wide state
    needs verification that the variable scope is per-test.

  Expected incremental speedup is meaningful but each package needs its
  own small audit commit; scheduled as ad-hoc cleanup rather than a
  single sweeping change.

- **Frontend UI for multi-target RI exchange**: The backend accepts
  `targets[]`, but the dashboard still posts the single-target shape
  (`target_offering_id` + `target_count`). Exposing multi-target in the
  UI is the natural next step once users have a mental model for
  redistributing one source RI into multiple shapes atomically. Out of
  scope for the current round because it's a UX question more than a
  backend question.
