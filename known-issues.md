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

- **Same-family-only recommendations**: Fully resolved for the
  allowlisted family groups — advisory *names* in the first pass
  (commit edc8d7838), real offering IDs + `EffectiveMonthlyCost`
  ranking in the follow-up (commit 0347b3111).
  `pkg/exchange.ReshapeRecommendation` now carries an
  `AlternativeTargets []OfferingOption` field (renamed from the
  earlier `AlternativeTargetInstanceTypes []string` — note for anyone
  auditing JSON payloads: the response key changed from
  `alternative_target_instance_types` to `alternative_targets`).
  `providers/aws/services/ec2/client.go`'s new
  `FindConvertibleOfferings` batches all candidate instance types
  into ONE `DescribeReservedInstancesOfferings` call per reshape
  page load (≤4 API calls for a diverse fleet; 1 for a homogeneous
  one) and ranks by monthly cost. `pkg/exchange.AnalyzeReshapingWithOfferings`
  composes the base analyzer with offering enrichment; the
  auto-exchange pipeline still uses the plain `AnalyzeReshaping`
  (no pricing needed) so automated behaviour is unchanged. Allowlist
  covers general-purpose `m5/m6i/m7g`, compute-optimised
  `c5/c6i/c7g`, memory-optimised `r5/r6i/r7g`, burstable
  `t3/t3a/t4g`. Specialty (p\*/g\*/x\*/hpc\*) and legacy-generation
  (m4/c4/r3) families are deliberately out of the allowlist — see
  the follow-up below.

  The reshape-recommendations dashboard page renders the
  alternatives as a new "Alternatives" column with per-instance
  `$X.XX/mo` cost chips (commit 97fc2597d); when the user clicks
  "Exchange" from a reshape row, the modal receives the rec's
  `alternative_targets` and shows a matching cost chip next to each
  target-offering input plus a live-updating running total
  (`sum(chip.cost × row.count)`). End-to-end coverage is exercised
  by the handler integration test at
  `internal/api/handler_ri_exchange_integration_test.go` (build-tag
  `integration`, commit da762067c) which wires a real Postgres
  through the reshape handler with mocked AWS clients via newly-
  added factory injection points on the Handler struct
  (`reshapeEC2Factory` / `reshapeRecsFactory`, both nil-safe so
  prod behaviour is unchanged).

- **Multi-target exchange**: Fully resolved — backend
  (commit 5eb274690) and frontend (commit 2ff1ebe89).
  `pkg/exchange.ExchangeQuoteRequest` and `ExchangeExecuteRequest`
  accept a `Targets []TargetConfig` slice; legacy `TargetOfferingID`
  / `TargetCount` fields are retained as a single-target alias so
  existing callers keep working. The HTTP API gains an optional
  `targets[]` array on the quote + execute bodies; when present it
  wins over the legacy singleton fields. Spend-cap semantics: AWS
  returns a single aggregated `PaymentDue` across all targets, so
  `max_payment_due_usd` naturally functions as a TOTAL cap for
  multi-target requests. Dashboard modal gained add/remove target
  rows: the modal posts the singleton shape when exactly one row is
  present (preserving existing wire format) and posts `targets[]`
  when ≥2 rows are present. With commit 97fc2597d the modal also
  shows per-row cost chips (when the caller supplies
  `alternativeTargets`) and a running total that updates live as
  the user edits offering-type / count inputs.

- **Utilization caching**: Resolved with a Postgres-backed TTL cache
  plus stale-while-revalidate on non-Lambda runtimes. Migration
  `000031_ri_utilization_cache` adds
  `ri_utilization_cache (region, lookback_days, payload, fetched_at)`.
  `internal/api/handler_ri_exchange.go` routes both `getRIUtilization`
  and `getReshapeRecommendations` through the cache wrapper
  (`internal/api/ri_utilization_cache.go`) so one Cost Explorer call
  per TTL window serves every warm and cold Lambda container.
  Two TTL knobs: `CUDLY_RI_UTILIZATION_CACHE_TTL` (default `15m`,
  soft-freshness window) and `CUDLY_RI_UTILIZATION_CACHE_STALE_TTL`
  (default `30m`, hard expiry). On non-Lambda, reads in
  `[soft, hard)` serve the stale row and kick a singleflight-guarded
  background refresh (`golang.org/x/sync/singleflight`); reads past
  `hard` force a synchronous refetch. Lambda runtimes always
  synchronously refetch on any staleness — background goroutines
  aren't safe there (containers freeze between invocations). Errors
  are never cached — a transient CE 5xx cannot lock the dashboard
  out for the full TTL. Observability: `logging.Infof` on SWR kick
  and hard-expiry paths; `logging.Debugf` on the Lambda-skip
  branch. See the Config section of
  `specs/recommendations-cache.md`. End-to-end Postgres integration
  test at `internal/api/ri_utilization_cache_integration_test.go`
  (build-tag `integration`).

### Database Migrations

- **Migration 000027 non-idempotent on fresh DBs**: Resolved.
  `internal/database/postgres/migrations/000027_savings_snapshots_pk.up.sql`
  now runs `ALTER TABLE savings_snapshots DROP CONSTRAINT IF EXISTS
  savings_snapshots_pkey;` before the existing DELETE CTE + ADD
  CONSTRAINT sequence. The guard makes the migration safe on fresh
  containers (where 000018 already added the PK) without changing
  behaviour on production DBs where 000027 was the first to add
  the PK. The `internal/api/ri_utilization_cache_integration_test.go`
  bootstrap now uses the standard `migrations.RunMigrations` path
  instead of the earlier table-create workaround.

### Test Performance

- **t.Parallel() adoption (partial)**: Resolved for three audit-safe
  packages — `pkg/exchange/{auto,exchange,reshape}_test.go`,
  `providers/aws/services/ec2/client_test.go`, and
  `internal/api/validation_test.go`. Remaining packages haven't been
  audited per-file and keep their sequential execution — see the
  follow-up below.

## Outstanding follow-ups

- ~~**Cross-family RI recommendations for specialty + legacy families**~~ **— RESOLVED.** Extended `peerFamilyGroups` in `pkg/exchange/reshape.go` with specialty (`p3/p4d/p5`, `g4dn/g5`, `hpc6a/hpc6id/hpc7g`) and legacy-generation (`m4/m5`, `c4/c5`, `r3/r4/r5`) groups. Added a local `passesDollarUnitsCheck(srcNF, srcMonthlyCost, srcCurrency, target)` pre-filter applied in `fillAlternativesFromOfferings`: a target survives only if `target.NF × target.EffectiveMonthlyCost >= src.NF × src.MonthlyCost` (with an explicit currency-equality guard that's a no-op when either side is empty). The check approximates AWS's runtime two-parallel-≥-checks rule using the already-computed `EffectiveMonthlyCost` (which folds upfront amortisation + recurring + usage), so no per-pair `GetReservedInstancesExchangeQuote` API calls are needed — false positives are caught by the existing `auto.go` `IsValidExchange=false` skip path at execution time. `OfferingOption` gained `NormalizationFactor` + `CurrencyCode` fields populated by `FindConvertibleOfferings`; `ConvertibleRI` gained `CurrencyCode` + `RecurringHourlyAmount` populated by `ListConvertibleReservedInstances`; `RIInfo` gained `MonthlyCost` + `CurrencyCode` populated by both API and server handlers via a new `monthlyCostFromConvertibleRI` helper using AWS's canonical `(FixedPrice/hours_per_term + UsagePrice + recurring_hourly) × 730` formula. **Follow-up:** make the family allowlist obsolete by sourcing cross-family candidates from CUDly's already-cached Cost Explorer RI purchase recommendations (data we already collect) instead of a hardcoded family list or a per-rec offering API enumeration — see `known_issues/24_exchange_offering_cache.md` for the full design.

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

- **Migration 000027 non-idempotent on fresh DBs**: Integration
  tests that spin up a fresh Postgres via `testcontainers-go` can't
  run the full migration set — migration 000027 (`savings_snapshots_pk`)
  tries to `ADD PRIMARY KEY` that migration 000018 already added,
  failing with "multiple primary keys for table". Production DBs
  aren't affected because they were already in the "duplicate rows
  needing dedup" state that 000027 was written to fix. Fix: make
  the ADD CONSTRAINT idempotent (e.g. DROP CONSTRAINT IF EXISTS
  first, or wrap in a conditional PL/pgSQL block) without changing
  the behaviour on already-migrated databases. Tracked separately
  because it requires careful review against real prod migration
  history. Commit `2d8f1e2ba` works around it by bypassing
  migrations entirely for the cache integration test (creates only
  the `ri_utilization_cache` table directly).

- **GCP account `serene-bazaar-666` deploy SA missing `compute.regions.list`**:
  Visible in production Lambda logs (`2026-04-21T16:28:22Z` and onward):

      [ERROR] GCP account GCP serene-bazaar-666 (serene-bazaar-666):
      get recommendations: failed to get regions: failed to list regions:
      googleapi: Error 403: Required 'compute.regions.list' permission
      for 'projects/serene-bazaar-666'

  The deploy service account that CUDly impersonates for that project
  doesn't have `roles/compute.viewer` (or a custom role that includes
  `compute.regions.list`). Two paths to fix:

  - **Operator action (preferred):** grant the GCP service account
    `roles/compute.viewer` on the project (or a narrower custom role
    containing `compute.regions.list` + `compute.zones.list` if least-
    privilege matters).
  - **Code-side mitigation:** the GCP region-fetch already short-circuits
    on errors but every fetch attempt logs as `[ERROR]`. The collector
    could downgrade to `[WARN]` for permission errors specifically (so
    the operator notices once but the noise stops) — tracked as a
    follow-up in `known_issues/22_scheduler.md` under the silent-
    failure entry.

  The collector's account-failure-swallow bug masks this entirely: the
  GCP provider is reported as successful even when this account fails,
  so the operator only sees the issue if they tail logs.

- **Per-plan-type SP split: caveats exposed in plans/recommendations
  views**: The migration to four per-plan-type Savings Plans cards
  (Compute / EC2 Instance / SageMaker / Database) replaces the umbrella
  `(aws, savings-plans)` ServiceConfig row with four per-plan-type
  rows and rewrites `purchase_plans.services` JSONB keys atomically
  (migration 000040). Two pre-existing UX limitations are now visible
  with the split and are tracked here as follow-ups, not blockers:

  - **Multi-SP purchase-plan summary shows only one plan type.**
    `frontend/src/plans.ts:231` renders a plan summary by reading the
    FIRST entry from `plan.services` (a JSONB-derived map). A purchase
    plan that targets multiple SP plan types (e.g., Compute +
    SageMaker) will list only one — whichever sorts first — in its
    summary card. Pre-split this was hidden because the single
    `aws:savings-plans` key always rendered as "Savings Plans"; post-
    split the same plan now has four keys and only one displays. Fix
    is plans.ts-only: render a comma-separated list or a count badge
    when multiple SP plan types are present in the same plan. Out of
    scope for the issue #22 follow-up PR.

  - **Bulk-buy-from-Recommendations no longer sees "all SP types" rows.**
    The bulk-buy modal in recommendations.ts groups recommendations by
    `(provider, service)`. Pre-split, every SP recommendation shared
    `service: "savings-plans"`, so a Compute SP rec and a SageMaker SP
    rec landed in the same bucket and could be bought in one click.
    Post-split, each plan type is its own service, so an operator who
    used to bulk-buy SP must now bulk-buy four times (once per plan
    type). Fix is a UI-side aggregator that groups by
    `IsSavingsPlan(rec.service)` for the bulk-buy view only, leaving
    the underlying service distinction intact for the per-card save
    path. Out of scope for the issue #22 follow-up PR.

- **GCP memorystore + cloudsql mock-service tests fail on HEAD**:
  `providers/gcp/services/memorystore/client_test.go::TestMemorystoreClient_GetExistingCommitments_WithMockService`
  (3 sub-tests) and `providers/gcp/services/cloudsql/client_test.go::TestCloudSQLClient_ValidateOffering_NoCredentials`
  fail consistently. The failures predate the April 2026 purchase-automation
  work (reproducible at `022ea3be5^`, the commit before
  `refactor(purchase): widen PurchaseCommitment with PurchaseOptions`),
  so they are pre-existing infrastructure issues from the GCP mock
  service introduced in `51db1b9b0` (2025-11-29). Symptoms: memorystore
  asserts `mockService.closeCalled == true` but Close() is never called by
  the production code path under test; cloudsql expects an error from
  `ValidateOffering` when no credentials are present but the call returns
  nil. CI tolerates the failures because the pre-commit hook runs
  `-short` and these tests are reachable (so they're being skipped or
  the CI runner is not running the GCP module tests). Fix needs a
  small audit of the GCP mock harness wiring vs the production
  `GetExistingCommitments` / `ValidateOffering` code paths — likely
  the mock's `Close()` was renamed or the production code was
  refactored to use a different client lifecycle.

- **OpenSearch RI tagging: best-effort, may be rejected by AWS**:
  Implemented in `providers/aws/services/opensearch/client.go`. The
  client now resolves the caller's AWS account ID via STS (cached on
  first tag call), constructs an ARN
  (`arn:aws:es:<region>:<account>:reserved-instance/<id>`), and calls
  `opensearch:AddTags` post-purchase. AWS documentation only explicitly
  supports AddTags on domain/data-source/application ARNs, so the call
  MAY be rejected with a `ValidationException`. When that happens,
  `retry.ErrPermanent` short-circuits the retry budget and the failure
  is logged at WARN — the purchase still succeeds. If AWS extends
  AddTags to cover reserved-instance ARNs (or CUDly switches to
  ResourceGroupsTaggingAPI if that ever adds the resource type), the
  code will start working with no change. Source is also persisted in
  `purchase_history.source` for DB-side reconciliation.
