package purchase

import (
	"context"
	"fmt"
	"time"

	"github.com/LeanerCloud/CUDly/internal/config"
	"github.com/LeanerCloud/CUDly/internal/email"
	"github.com/LeanerCloud/CUDly/pkg/common"
	"github.com/LeanerCloud/CUDly/pkg/logging"
	"github.com/aws/aws-sdk-go-v2/service/sts"
)

// executePurchase performs the actual purchase
func (m *Manager) executePurchase(ctx context.Context, exec *config.PurchaseExecution) error {
	logging.Infof("Executing purchase for plan %s, step %d", exec.PlanID, exec.StepNumber)

	plan, err := m.config.GetPurchasePlan(ctx, exec.PlanID)
	if err != nil {
		return fmt.Errorf("failed to get plan: %w", err)
	}
	if plan == nil {
		return fmt.Errorf("plan not found: %s", exec.PlanID)
	}

	accountID := m.getAWSAccountID(ctx)
	totalSavings, totalUpfront, purchaseErrors := m.processPurchaseRecommendations(ctx, exec, plan, accountID)

	if err := m.sendPurchaseNotification(ctx, exec, plan, totalSavings, totalUpfront); err != nil {
		logging.Errorf("Failed to send confirmation: %v", err)
	}

	if len(purchaseErrors) > 0 {
		return fmt.Errorf("some purchases failed: %v", purchaseErrors)
	}

	return nil
}

func (m *Manager) processPurchaseRecommendations(ctx context.Context, exec *config.PurchaseExecution, plan *config.PurchasePlan, accountID string) (float64, float64, []string) {
	var totalSavings, totalUpfront float64
	var purchaseErrors []string

	for i, rec := range exec.Recommendations {
		if !rec.Selected {
			continue
		}

		logging.Infof("Purchasing: %dx %s in %s", rec.Count, rec.ResourceType, rec.Region)

		purchaseResult, err := m.executeSinglePurchase(ctx, rec)
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

// executeSinglePurchase executes a single purchase using the appropriate provider
func (m *Manager) executeSinglePurchase(ctx context.Context, rec config.RecommendationRecord) (common.PurchaseResult, error) {
	// Create the provider
	cloudProvider, err := m.providerFactory.CreateAndValidateProvider(ctx, rec.Provider, nil)
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
