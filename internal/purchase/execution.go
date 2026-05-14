package purchase

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/LeanerCloud/CUDly/internal/config"
	"github.com/LeanerCloud/CUDly/internal/credentials"
	"github.com/LeanerCloud/CUDly/internal/email"
	"github.com/LeanerCloud/CUDly/internal/execution"
	"github.com/LeanerCloud/CUDly/pkg/common"
	"github.com/LeanerCloud/CUDly/pkg/logging"
	"github.com/LeanerCloud/CUDly/pkg/provider"
	azureprovider "github.com/LeanerCloud/CUDly/providers/azure"
	gcpprovider "github.com/LeanerCloud/CUDly/providers/gcp"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/google/uuid"
)

// executePurchase performs the actual purchase.
// When the plan has associated cloud accounts and a credential store is configured,
// it fans out execution in parallel — one goroutine per account, each with its own
// PurchaseExecution record tagged with cloud_account_id.
// If no accounts are configured or no credential store is available, it falls back
// to single-account execution using ambient credentials.
// executePurchase runs the purchase for a single execution. Returns wasMultiAccount=true when
// fan-out was used (per-account records are already saved; caller should skip root record save).
func (m *Manager) executePurchase(ctx context.Context, exec *config.PurchaseExecution) (wasMultiAccount bool, err error) {
	logging.Infof("Executing purchase for plan %s, step %d", exec.PlanID, exec.StepNumber)

	plan, err := m.config.GetPurchasePlan(ctx, exec.PlanID)
	if err != nil {
		return false, fmt.Errorf("failed to get plan: %w", err)
	}
	if plan == nil {
		return false, fmt.Errorf("plan not found: %s", exec.PlanID)
	}

	// Fan out across plan accounts when accounts are configured.
	if exec.CloudAccountID == nil {
		accounts, err := m.config.GetPlanAccounts(ctx, exec.PlanID)
		if err != nil {
			return false, fmt.Errorf("failed to load plan accounts for plan %s: %w", exec.PlanID, err)
		}
		if len(accounts) > 0 {
			return true, m.executeMultiAccount(ctx, exec, plan, accounts)
		}
	}

	// Single-account (legacy) path.
	accountID := m.getAWSAccountID(ctx)
	totalSavings, totalUpfront, purchaseErrors := m.processPurchaseRecommendations(ctx, exec, plan, accountID, nil)

	if len(purchaseErrors) > 0 {
		return false, fmt.Errorf("some purchases failed: %v", purchaseErrors)
	}

	if err := m.sendPurchaseNotification(ctx, exec, plan, totalSavings, totalUpfront); err != nil {
		logging.Errorf("Failed to send confirmation: %v", err)
	}

	return false, nil
}

// executeMultiAccount fans out executePurchase across all plan accounts in parallel.
// Each account gets its own PurchaseExecution record tagged with cloud_account_id.
func (m *Manager) executeMultiAccount(ctx context.Context, baseExec *config.PurchaseExecution, plan *config.PurchasePlan, accounts []config.CloudAccount) error {
	results := execution.RunForAccountsWithConcurrency(ctx, accounts, func(ctx context.Context, account config.CloudAccount) (struct{}, error) {
		return struct{}{}, m.executeForAccount(ctx, baseExec, plan, account)
	}, getMaxAccountParallelism())

	var errs []string
	for _, r := range results {
		if r.Err != nil {
			errs = append(errs, fmt.Sprintf("account %s: %v", r.AccountID, r.Err))
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("multi-account execution: %s", strings.Join(errs, "; "))
	}
	return nil
}

// executeForAccount runs a single plan execution for one cloud account.
// It creates a new PurchaseExecution record tagged with cloud_account_id, resolves
// per-account credentials, executes purchases, and saves the result.
func (m *Manager) executeForAccount(ctx context.Context, baseExec *config.PurchaseExecution, plan *config.PurchasePlan, account config.CloudAccount) error {
	// Create a per-account copy of the execution record with an independent
	// Recommendations slice so concurrent goroutines don't race on writes.
	acctID := account.ID
	acctExec := *baseExec
	acctExec.ExecutionID = uuid.New().String()
	acctExec.CloudAccountID = &acctID
	recs := make([]config.RecommendationRecord, len(baseExec.Recommendations))
	copy(recs, baseExec.Recommendations)
	acctExec.Recommendations = recs

	provCfg, err := m.resolveAccountProvider(ctx, account)
	if err != nil {
		acctExec.Status = "failed"
		acctExec.Error = err.Error()
		_ = m.config.SavePurchaseExecution(ctx, &acctExec)
		return fmt.Errorf("credential resolution failed for account %s: %w", account.ID, err)
	}

	accountID := account.ExternalID
	totalSavings, totalUpfront, purchaseErrors := m.processPurchaseRecommendations(ctx, &acctExec, plan, accountID, provCfg)

	if len(purchaseErrors) > 0 {
		acctExec.Status = "failed"
		acctExec.Error = strings.Join(purchaseErrors, "; ")
	} else {
		now := time.Now()
		acctExec.Status = "completed"
		acctExec.CompletedAt = &now
	}

	if err := m.config.SavePurchaseExecution(ctx, &acctExec); err != nil {
		return fmt.Errorf("AUDIT LOSS: failed to save execution record for account %s: %w", account.ID, err)
	}

	if len(purchaseErrors) > 0 {
		return fmt.Errorf("some purchases failed: %v", purchaseErrors)
	}

	if err := m.sendPurchaseNotification(ctx, &acctExec, plan, totalSavings, totalUpfront); err != nil {
		logging.Errorf("Failed to send confirmation for account %s: %v", account.ID, err)
	}
	return nil
}

// resolveAccountProvider returns a *ProviderConfig with a pre-authenticated provider
// for the given account. Returns an error if credential resolution fails — callers
// must NOT fall back to ambient credentials on error.
func (m *Manager) resolveAccountProvider(ctx context.Context, account config.CloudAccount) (*provider.ProviderConfig, error) {
	switch account.Provider {
	case "aws":
		return m.resolveAWSProvider(ctx, account)
	case "azure":
		return m.resolveAzureProvider(ctx, account)
	case "gcp":
		return m.resolveGCPProvider(ctx, account)
	default:
		return nil, fmt.Errorf("credentials: unknown cloud provider %q for account %s", account.Provider, account.ID)
	}
}

func (m *Manager) resolveAWSProvider(ctx context.Context, account config.CloudAccount) (*provider.ProviderConfig, error) {
	if account.AWSAuthMode != "access_keys" && m.assumeRoleSTS == nil {
		return nil, fmt.Errorf("credentials: STS client not configured for non-access_keys mode (account %s)", account.ID)
	}
	awsCreds, err := credentials.ResolveAWSCredentialProvider(ctx, &account, m.credStore, m.assumeRoleSTS)
	if err != nil {
		return nil, fmt.Errorf("credentials: resolve AWS for account %s (%s): %w", account.ID, account.Name, err)
	}
	return &provider.ProviderConfig{Name: "aws", AWSCredentialsProvider: awsCreds}, nil
}

func (m *Manager) resolveAzureProvider(ctx context.Context, account config.CloudAccount) (*provider.ProviderConfig, error) {
	if account.AzureAuthMode != "managed_identity" && m.credStore == nil {
		return nil, fmt.Errorf("credentials: credential store required for non-managed_identity Azure account %s", account.ID)
	}
	azCred, err := credentials.ResolveAzureTokenCredentialWithOpts(ctx, &account, m.credStore, credentials.AzureResolveOptions{
		Signer:    m.oidcSigner,
		IssuerURL: m.oidcIssuerURL,
	})
	if err != nil {
		return nil, fmt.Errorf("credentials: resolve Azure for account %s (%s): %w", account.ID, account.Name, err)
	}
	azProv, err := azureprovider.NewAzureProvider(&provider.ProviderConfig{Profile: account.AzureSubscriptionID})
	if err != nil {
		return nil, fmt.Errorf("credentials: create Azure provider for account %s (%s): %w", account.ID, account.Name, err)
	}
	azProv.SetCredential(azCred)
	return &provider.ProviderConfig{ProviderOverride: azProv}, nil
}

func (m *Manager) resolveGCPProvider(ctx context.Context, account config.CloudAccount) (*provider.ProviderConfig, error) {
	if account.GCPAuthMode != "application_default" && m.credStore == nil {
		return nil, fmt.Errorf("credentials: credential store required for non-ADC GCP account %s", account.ID)
	}
	gcpTS, err := credentials.ResolveGCPTokenSourceWithOpts(ctx, &account, m.credStore, credentials.GCPResolveOptions{
		Signer:    m.oidcSigner,
		IssuerURL: m.oidcIssuerURL,
	})
	if err != nil {
		return nil, fmt.Errorf("credentials: resolve GCP for account %s (%s): %w", account.ID, account.Name, err)
	}
	if gcpTS == nil {
		// ADC mode: no explicit token source — use ambient credentials
		return nil, nil
	}
	gcpProv := gcpprovider.NewProviderWithCredentials(ctx, account.GCPProjectID, gcpTS)
	return &provider.ProviderConfig{ProviderOverride: gcpProv}, nil
}

// getMaxAccountParallelism is a thin alias over the shared
// execution.ConcurrencyFromEnv so the purchase manager and the scheduler
// both honour the same CUDLY_MAX_ACCOUNT_PARALLELISM override.
func getMaxAccountParallelism() int {
	return execution.ConcurrencyFromEnv()
}

// recPurchaseOutcome carries the result of a single fan-out unit so the
// aggregator can write back into exec.Recommendations and call
// savePurchaseHistory from a single goroutine (no concurrent map / slice
// mutation). The index field is the position in exec.Recommendations.
type recPurchaseOutcome struct {
	index    int
	purchase common.PurchaseResult
	err      error
}

// processPurchaseRecommendations issues a cloud-provider purchase for
// every Selected recommendation in exec.Recommendations. Recs are
// fanned out across goroutines (cross-provider and cross-service calls
// run in parallel) and the per-rec outcomes are aggregated serially by
// aggregatePurchaseOutcomes so writes to exec.Recommendations[i],
// purchaseErrors, and savePurchaseHistory are race-free.
//
// Returns (totalSavings, totalUpfront, purchaseErrors). An empty
// purchaseErrors slice means every Selected rec succeeded; a non-empty
// slice carries one entry per failed rec (the caller flips the
// execution to "failed" on any non-empty errors).
func (m *Manager) processPurchaseRecommendations(ctx context.Context, exec *config.PurchaseExecution, plan *config.PurchasePlan, accountID string, provCfg *provider.ProviderConfig) (float64, float64, []string) {
	opts := common.PurchaseOptions{Source: m.normalizePurchaseSource(exec)}

	// Build the list of selected indices once so the fan-out closure only
	// has to look up rec[i] (no second pass over the full slice).
	selected := selectedIndices(exec.Recommendations)
	if len(selected) == 0 {
		return 0, 0, nil
	}

	// Each rec runs in its own goroutine so a multi-rec execution that
	// spans providers (AWS RI + Azure reservation + GCP CUD) or services
	// (EC2 + RDS + ElastiCache + OpenSearch within AWS) makes its cloud
	// API calls in parallel rather than blocking on the slowest one.
	// Provider client construction inside executeSinglePurchase is
	// independent per call, so cross-provider parallelism is safe.
	// Concurrency is capped at getMaxAccountParallelism so the same
	// operator-level CUDLY_MAX_ACCOUNT_PARALLELISM knob covers both the
	// outer account fan-out and this inner rec fan-out.
	results := execution.FanOutWithConcurrency(ctx, indexKeys(selected),
		func(ctx context.Context, key string) (recPurchaseOutcome, error) {
			i, _ := strconv.Atoi(key)
			rec := exec.Recommendations[i]
			logging.Infof("Purchasing: %dx %s in %s (%s/%s)", rec.Count, rec.ResourceType, rec.Region, rec.Provider, rec.Service)
			purchaseResult, err := m.executeSinglePurchase(ctx, rec, provCfg, opts)
			return recPurchaseOutcome{index: i, purchase: purchaseResult, err: err}, nil
		}, getMaxAccountParallelism())

	return m.aggregatePurchaseOutcomes(ctx, exec, plan, accountID, results)
}

// aggregatePurchaseOutcomes walks the fan-out results serially and writes
// each rec's outcome back to exec.Recommendations + savePurchaseHistory.
// Extracted so processPurchaseRecommendations stays under gocyclo:10 and
// so the aggregation logic is single-threaded — no concurrent writes to
// totals, purchaseErrors, or exec.Recommendations[i] regardless of how
// many recs ran in parallel.
func (m *Manager) aggregatePurchaseOutcomes(ctx context.Context, exec *config.PurchaseExecution, plan *config.PurchasePlan, accountID string, results []execution.Result[recPurchaseOutcome]) (float64, float64, []string) {
	var totalSavings, totalUpfront float64
	var purchaseErrors []string
	for _, r := range results {
		v := r.Value
		i := v.index
		rec := exec.Recommendations[i]
		if v.err != nil {
			logging.Errorf("Failed to purchase %s: %v", rec.ResourceType, v.err)
			exec.Recommendations[i].Error = v.err.Error()
			purchaseErrors = append(purchaseErrors, fmt.Sprintf("%s: %v", rec.ResourceType, v.err))
			continue
		}
		exec.Recommendations[i].Purchased = true
		exec.Recommendations[i].PurchaseID = v.purchase.CommitmentID
		totalSavings += rec.Savings
		totalUpfront += rec.UpfrontCost
		m.savePurchaseHistory(ctx, exec, plan, rec, v.purchase, accountID)
	}
	return totalSavings, totalUpfront, purchaseErrors
}

// normalizePurchaseSource canonicalizes exec.Source for downstream tag
// stamping. Defence-in-depth: NormalizeSource rejects anything outside
// the allowed whitelist; an unexpected value (DB tampering, future
// code path) is dropped to "" rather than fed onto a cloud commitment
// where it would be expensive to retract.
func (m *Manager) normalizePurchaseSource(exec *config.PurchaseExecution) string {
	source := exec.Source
	if source == "" {
		return ""
	}
	normalized, err := common.NormalizeSource(source)
	if err != nil {
		logging.Warnf("Invalid purchase source %q on execution %s: %v, proceeding untagged", source, exec.ExecutionID, err)
		return ""
	}
	return normalized
}

// selectedIndices returns the positions of recs with Selected=true,
// preserving slice order so the aggregator's pass over the fan-out
// results writes back to exec.Recommendations deterministically.
func selectedIndices(recs []config.RecommendationRecord) []int {
	out := make([]int, 0, len(recs))
	for i, rec := range recs {
		if rec.Selected {
			out = append(out, i)
		}
	}
	return out
}

// indexKeys formats a slice of indices as string keys for FanOut.
func indexKeys(idx []int) []string {
	out := make([]string, len(idx))
	for i, n := range idx {
		out[i] = strconv.Itoa(n)
	}
	return out
}

func (m *Manager) savePurchaseHistory(ctx context.Context, exec *config.PurchaseExecution, plan *config.PurchasePlan, rec config.RecommendationRecord, result common.PurchaseResult, accountID string) {
	historyRecord := &config.PurchaseHistoryRecord{
		AccountID:        accountID,
		PurchaseID:       result.CommitmentID,
		Timestamp:        time.Now(),
		Provider:         rec.Provider,
		Service:          rec.Service,
		Region:           rec.Region,
		ResourceType:     rec.ResourceType,
		Count:            rec.Count,
		Term:             rec.Term,
		Payment:          rec.Payment,
		UpfrontCost:      result.Cost,
		MonthlyCost:      derefFloat64(rec.MonthlyCost),
		EstimatedSavings: rec.Savings,
		PlanID:           exec.PlanID,
		PlanName:         plan.Name,
		RampStep:         exec.StepNumber,
		CloudAccountID:   exec.CloudAccountID,
		Source:           exec.Source,
	}
	if err := m.config.SavePurchaseHistory(ctx, historyRecord); err != nil {
		logging.Errorf("Failed to save history: %v", err)
	}
}

func (m *Manager) sendPurchaseNotification(ctx context.Context, exec *config.PurchaseExecution, plan *config.PurchasePlan, totalSavings, totalUpfront float64) error {
	data := m.buildPurchaseConfirmationData(exec, plan, totalSavings, totalUpfront)
	return m.email.SendPurchaseConfirmation(ctx, data)
}

func (m *Manager) buildPurchaseConfirmationData(exec *config.PurchaseExecution, plan *config.PurchasePlan, totalSavings, totalUpfront float64) email.NotificationData {
	dashboardBase := strings.TrimRight(m.dashboardURL, "/")
	data := email.NotificationData{
		DashboardURL:     dashboardBase,
		TotalSavings:     totalSavings,
		TotalUpfrontCost: totalUpfront,
		PlanName:         plan.Name,
	}
	if dashboardBase != "" {
		data.ArcheraEducationURL = dashboardBase + "/archera-insurance"
	}

	for _, rec := range exec.Recommendations {
		if rec.Purchased {
			data.Recommendations = append(data.Recommendations, email.RecommendationSummary{
				Service:        rec.Service,
				ResourceType:   rec.ResourceType,
				Engine:         rec.Engine,
				Region:         rec.Region,
				Count:          rec.Count,
				MonthlySavings: rec.Savings,
			})
		}
	}

	return data
}

// executeSinglePurchase executes a single purchase using the appropriate provider.
// provCfg carries optional per-account credentials; pass nil to use ambient credentials.
// opts carries execution-level metadata (the source surface) that providers stamp
// onto the commitment they create.
func (m *Manager) executeSinglePurchase(ctx context.Context, rec config.RecommendationRecord, provCfg *provider.ProviderConfig, opts common.PurchaseOptions) (common.PurchaseResult, error) {
	// Create the provider
	cloudProvider, err := m.providerFactory.CreateAndValidateProvider(ctx, rec.Provider, provCfg)
	if err != nil {
		return common.PurchaseResult{}, fmt.Errorf("failed to create %s provider: %w", rec.Provider, err)
	}

	// Map service string to ServiceType
	serviceType := m.mapServiceType(rec.Service)

	// Get the service client for this region
	serviceClient, err := cloudProvider.GetServiceClient(ctx, serviceType, rec.Region)
	if err != nil {
		return common.PurchaseResult{}, fmt.Errorf("failed to get service client: %w", err)
	}

	// Build the recommendation in common format
	recommendation := common.Recommendation{
		Provider:       common.ProviderType(rec.Provider),
		Service:        serviceType,
		Region:         rec.Region,
		ResourceType:   rec.ResourceType,
		Count:          rec.Count,
		Term:           fmt.Sprintf("%dyr", rec.Term),
		PaymentOption:  rec.Payment,
		CommitmentCost: rec.UpfrontCost,
	}

	// Add service-specific details
	if rec.Engine != "" {
		recommendation.Details = common.DatabaseDetails{
			Engine: rec.Engine,
		}
	}

	// Execute the purchase
	result, err := serviceClient.PurchaseCommitment(ctx, recommendation, opts)
	if err != nil {
		return result, fmt.Errorf("purchase failed: %w", err)
	}

	if !result.Success {
		if result.Error != nil {
			return result, result.Error
		}
		return result, fmt.Errorf("purchase was not successful")
	}

	logging.Infof("Successfully purchased %s: %s", rec.ResourceType, result.CommitmentID)
	return result, nil
}

// mapServiceType maps a service string to common.ServiceType
func (m *Manager) mapServiceType(service string) common.ServiceType {
	if svc, ok := mapSavingsPlansSlug(service); ok {
		return svc
	}
	switch service {
	case "ec2", "compute":
		return common.ServiceEC2
	case "rds", "relational-db":
		return common.ServiceRDS
	case "elasticache", "cache":
		return common.ServiceElastiCache
	case "opensearch", "search":
		return common.ServiceOpenSearch
	case "redshift", "data-warehouse":
		return common.ServiceRedshift
	case "memorydb":
		return common.ServiceMemoryDB
	default:
		return common.ServiceType(service)
	}
}

// mapSavingsPlansSlug normalises both the canonical hyphenated SP slugs and
// the dash-free spellings the frontend has historically sent into the
// matching common.ServiceType. The map covers the legacy umbrella plus the
// four per-plan-type slugs in both spellings — pulled out of mapServiceType
// to keep that switch under the gocyclo budget.
//
// TODO(#85): once purchase_executions JSONB rows persisted before the
// "savingsplans" rename (~6-month retention window) have aged out, the
// "savings-plans"-spelled aliases below can be removed and only the
// dash-free spellings ("savingsplans", "savingsplans-compute", etc.)
// need be matched here. The umbrella rename happened in PR #94; the
// per-plan-type slugs were always dash-form on the wire so their
// "savingsplans-*" aliases are forward-compat for any future
// frontend-canonical normalisation.
func mapSavingsPlansSlug(service string) (common.ServiceType, bool) {
	slugs := map[string]common.ServiceType{
		"savings-plans":             common.ServiceSavingsPlans,
		"savingsplans":              common.ServiceSavingsPlans,
		"savings-plans-compute":     common.ServiceSavingsPlansCompute,
		"savingsplans-compute":      common.ServiceSavingsPlansCompute,
		"savings-plans-ec2instance": common.ServiceSavingsPlansEC2Instance,
		"savingsplans-ec2instance":  common.ServiceSavingsPlansEC2Instance,
		"savings-plans-sagemaker":   common.ServiceSavingsPlansSageMaker,
		"savingsplans-sagemaker":    common.ServiceSavingsPlansSageMaker,
		"savings-plans-database":    common.ServiceSavingsPlansDatabase,
		"savingsplans-database":     common.ServiceSavingsPlansDatabase,
	}
	svc, ok := slugs[service]
	return svc, ok
}

// updatePlanProgress advances the ramp schedule after a purchase
func (m *Manager) updatePlanProgress(ctx context.Context, planID string) error {
	plan, err := m.config.GetPurchasePlan(ctx, planID)
	if err != nil {
		return err
	}
	if plan == nil {
		return nil
	}

	// Advance ramp schedule only if not complete
	if !plan.RampSchedule.IsComplete() {
		plan.RampSchedule.CurrentStep++
	}

	// Calculate next execution date
	if !plan.RampSchedule.IsComplete() {
		nextDate := plan.RampSchedule.GetNextPurchaseDate()
		plan.NextExecutionDate = &nextDate
	} else {
		plan.NextExecutionDate = nil
	}

	now := time.Now()
	plan.LastExecutionDate = &now

	return m.config.UpdatePurchasePlan(ctx, plan)
}

// getAWSAccountID retrieves the current AWS account ID using STS
func (m *Manager) getAWSAccountID(ctx context.Context) string {
	if m.stsClient == nil {
		logging.Debug("STS client not configured, using 'unknown' as account ID")
		return "unknown"
	}

	result, err := m.stsClient.GetCallerIdentity(ctx, &sts.GetCallerIdentityInput{})
	if err != nil {
		logging.Warnf("Failed to get AWS account ID: %v", err)
		return "unknown"
	}

	if result.Account != nil {
		return *result.Account
	}

	return "unknown"
}

// derefFloat64 safely dereferences a *float64, returning 0 for nil.
// Used when copying from RecommendationRecord.MonthlyCost (*float64)
// into PurchaseHistoryRecord.MonthlyCost (float64), where nil means
// "not provided" and maps to 0 in the history table.
func derefFloat64(p *float64) float64 {
	if p == nil {
		return 0
	}
	return *p
}
