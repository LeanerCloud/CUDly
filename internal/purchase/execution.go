package purchase

import (
	"context"
	"fmt"
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
func (m *Manager) executePurchase(ctx context.Context, exec *config.PurchaseExecution) error {
	logging.Infof("Executing purchase for plan %s, step %d", exec.PlanID, exec.StepNumber)

	plan, err := m.config.GetPurchasePlan(ctx, exec.PlanID)
	if err != nil {
		return fmt.Errorf("failed to get plan: %w", err)
	}
	if plan == nil {
		return fmt.Errorf("plan not found: %s", exec.PlanID)
	}

	// Fan out across plan accounts when accounts are configured.
	// ADC and managed_identity modes do not require a credential store, so we cannot
	// gate fan-out on m.credStore != nil.
	// Skip fan-out if this execution is already tagged with a specific account (re-entrant call).
	if exec.CloudAccountID == nil {
		accounts, err := m.config.GetPlanAccounts(ctx, exec.PlanID)
		if err != nil {
			logging.Warnf("Failed to load plan accounts for plan %s, falling back to single-account execution: %v", exec.PlanID, err)
		}
		if len(accounts) > 0 {
			return m.executeMultiAccount(ctx, exec, plan, accounts)
		}
	}

	// Single-account (legacy) path.
	accountID := m.getAWSAccountID(ctx)
	totalSavings, totalUpfront, purchaseErrors := m.processPurchaseRecommendations(ctx, exec, plan, accountID, nil)

	if len(purchaseErrors) > 0 {
		return fmt.Errorf("some purchases failed: %v", purchaseErrors)
	}

	if err := m.sendPurchaseNotification(ctx, exec, plan, totalSavings, totalUpfront); err != nil {
		logging.Errorf("Failed to send confirmation: %v", err)
	}

	return nil
}

// executeMultiAccount fans out executePurchase across all plan accounts in parallel.
// Each account gets its own PurchaseExecution record tagged with cloud_account_id.
func (m *Manager) executeMultiAccount(ctx context.Context, baseExec *config.PurchaseExecution, plan *config.PurchasePlan, accounts []config.CloudAccount) error {
	results := execution.RunForAccounts(ctx, accounts, func(ctx context.Context, account config.CloudAccount) (struct{}, error) {
		return struct{}{}, m.executeForAccount(ctx, baseExec, plan, account)
	})

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

	provCfg := m.resolveAccountProvider(ctx, account)
	accountID := account.ExternalID // Provider-specific account identifier (AWS account ID / Azure subscription ID / GCP project ID)
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
		// The purchase succeeded on the cloud provider side but the audit record is lost.
		// This is a data integrity issue — alert strongly so operators can reconcile.
		logging.Errorf("AUDIT LOSS: failed to save execution record for account %s (purchases may have been made with no record): %v", account.ID, err)
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
// for the given account, or nil to fall back to ambient credentials.
func (m *Manager) resolveAccountProvider(ctx context.Context, account config.CloudAccount) *provider.ProviderConfig {
	switch account.Provider {
	case "aws":
		return m.resolveAWSProvider(ctx, account)
	case "azure":
		return m.resolveAzureProvider(ctx, account)
	case "gcp":
		return m.resolveGCPProvider(ctx, account)
	}
	return nil
}

func (m *Manager) resolveAWSProvider(ctx context.Context, account config.CloudAccount) *provider.ProviderConfig {
	// access_keys loads from the credential store and does not need assumeRoleSTS.
	// All other modes (role_arn, bastion, workload_identity_federation) require STS.
	if account.AWSAuthMode != "access_keys" && m.assumeRoleSTS == nil {
		return nil
	}
	awsCreds, err := credentials.ResolveAWSCredentialProvider(ctx, &account, m.credStore, m.assumeRoleSTS)
	if err != nil {
		logging.Warnf("Failed to resolve AWS credentials for account %s (%s), using ambient: %v", account.ID, account.Name, err)
		return nil
	}
	return &provider.ProviderConfig{Name: "aws", AWSCredentialsProvider: awsCreds}
}

func (m *Manager) resolveAzureProvider(ctx context.Context, account config.CloudAccount) *provider.ProviderConfig {
	if account.AzureAuthMode != "managed_identity" && m.credStore == nil {
		return nil
	}
	azCred, err := credentials.ResolveAzureTokenCredential(ctx, &account, m.credStore)
	if err != nil {
		logging.Warnf("Failed to resolve Azure credentials for account %s (%s), using ambient: %v", account.ID, account.Name, err)
		return nil
	}
	azProv, err := azureprovider.NewAzureProvider(&provider.ProviderConfig{Profile: account.AzureSubscriptionID})
	if err != nil {
		logging.Warnf("Failed to create Azure provider for account %s (%s): %v", account.ID, account.Name, err)
		return nil
	}
	azProv.SetCredential(azCred)
	return &provider.ProviderConfig{ProviderOverride: azProv}
}

func (m *Manager) resolveGCPProvider(ctx context.Context, account config.CloudAccount) *provider.ProviderConfig {
	if account.GCPAuthMode != "application_default" && m.credStore == nil {
		return nil
	}
	gcpTS, err := credentials.ResolveGCPTokenSource(ctx, &account, m.credStore)
	if err != nil {
		logging.Warnf("Failed to resolve GCP credentials for account %s (%s), using ambient: %v", account.ID, account.Name, err)
		return nil
	}
	if gcpTS == nil {
		// application_default: factory will pick up ADC automatically with nil provCfg.
		return nil
	}
	gcpProv := gcpprovider.NewProviderWithCredentials(ctx, account.GCPProjectID, gcpTS)
	return &provider.ProviderConfig{ProviderOverride: gcpProv}
}

// planProvider derives the cloud provider from the plan's Services map.
// Service keys use the "provider:service" format (e.g. "aws:ec2").
// Returns the first provider found, or empty string if no services are configured.
func planProvider(plan *config.PurchasePlan) string {
	for key := range plan.Services {
		if i := strings.Index(key, ":"); i > 0 {
			return key[:i]
		}
	}
	return ""
}

func (m *Manager) processPurchaseRecommendations(ctx context.Context, exec *config.PurchaseExecution, plan *config.PurchasePlan, accountID string, provCfg *provider.ProviderConfig) (float64, float64, []string) {
	var totalSavings, totalUpfront float64
	var purchaseErrors []string

	for i, rec := range exec.Recommendations {
		if !rec.Selected {
			continue
		}

		logging.Infof("Purchasing: %dx %s in %s", rec.Count, rec.ResourceType, rec.Region)

		purchaseResult, err := m.executeSinglePurchase(ctx, rec, provCfg)
		if err != nil {
			logging.Errorf("Failed to purchase %s: %v", rec.ResourceType, err)
			exec.Recommendations[i].Error = err.Error()
			purchaseErrors = append(purchaseErrors, fmt.Sprintf("%s: %v", rec.ResourceType, err))
			continue
		}

		exec.Recommendations[i].Purchased = true
		exec.Recommendations[i].PurchaseID = purchaseResult.CommitmentID

		totalSavings += rec.Savings
		totalUpfront += rec.UpfrontCost

		m.savePurchaseHistory(ctx, exec, plan, rec, purchaseResult, accountID)
	}

	return totalSavings, totalUpfront, purchaseErrors
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
		MonthlyCost:      rec.MonthlyCost,
		EstimatedSavings: rec.Savings,
		PlanID:           exec.PlanID,
		PlanName:         plan.Name,
		RampStep:         exec.StepNumber,
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
	data := email.NotificationData{
		DashboardURL:     m.dashboardURL,
		TotalSavings:     totalSavings,
		TotalUpfrontCost: totalUpfront,
		PlanName:         plan.Name,
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
func (m *Manager) executeSinglePurchase(ctx context.Context, rec config.RecommendationRecord, provCfg *provider.ProviderConfig) (common.PurchaseResult, error) {
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
	result, err := serviceClient.PurchaseCommitment(ctx, recommendation)
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
	case "savings-plans", "savingsplans":
		return common.ServiceSavingsPlans
	default:
		return common.ServiceType(service)
	}
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
