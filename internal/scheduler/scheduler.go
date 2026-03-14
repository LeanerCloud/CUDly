// Package scheduler handles scheduled recommendation collection.
package scheduler

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/LeanerCloud/CUDly/internal/config"
	"github.com/LeanerCloud/CUDly/internal/email"
	"github.com/LeanerCloud/CUDly/internal/purchase"
	"github.com/LeanerCloud/CUDly/pkg/common"
	"github.com/LeanerCloud/CUDly/pkg/logging"
	"github.com/LeanerCloud/CUDly/pkg/provider"
)

// SchedulerConfig holds configuration for the scheduler
type SchedulerConfig struct {
	ConfigStore     config.StoreInterface
	PurchaseManager ManagerInterface
	EmailSender     email.SenderInterface
	DashboardURL    string
	// Provider factory for creating cloud providers (allows injection for testing)
	ProviderFactory provider.FactoryInterface
}

// CollectResult holds the result of collecting recommendations
type CollectResult struct {
	Recommendations int     `json:"recommendations"`
	TotalSavings    float64 `json:"total_savings"`
}

// ManagerInterface defines the purchase manager methods used by scheduler
type ManagerInterface interface {
	ProcessScheduledPurchases(ctx context.Context) (*purchase.ProcessResult, error)
	SendUpcomingPurchaseNotifications(ctx context.Context) (*purchase.NotificationResult, error)
}

// Scheduler handles scheduled tasks
type Scheduler struct {
	config          config.StoreInterface
	purchase        ManagerInterface
	email           email.SenderInterface
	dashboardURL    string
	providerFactory provider.FactoryInterface
}

// NewScheduler creates a new scheduler
func NewScheduler(cfg SchedulerConfig) *Scheduler {
	factory := cfg.ProviderFactory
	if factory == nil {
		factory = &provider.DefaultFactory{}
	}

	return &Scheduler{
		config:          cfg.ConfigStore,
		purchase:        cfg.PurchaseManager,
		email:           cfg.EmailSender,
		dashboardURL:    cfg.DashboardURL,
		providerFactory: factory,
	}
}

// CollectRecommendations fetches recommendations from all configured cloud providers
func (s *Scheduler) CollectRecommendations(ctx context.Context) (*CollectResult, error) {
	logging.Info("Collecting recommendations from cloud providers...")

	// Get global config
	globalCfg, err := s.config.GetGlobalConfig(ctx)
	if err != nil {
		return nil, err
	}

	// Collect recommendations from each enabled provider
	var allRecommendations []config.RecommendationRecord
	var totalSavings float64

	for _, providerName := range globalCfg.EnabledProviders {
		logging.Infof("Collecting recommendations from %s...", providerName)

		recs, err := s.collectProviderRecommendations(ctx, providerName, globalCfg)
		if err != nil {
			logging.Errorf("Failed to collect %s recommendations: %v", providerName, err)
			continue
		}

		for _, rec := range recs {
			totalSavings += rec.Savings
		}
		allRecommendations = append(allRecommendations, recs...)
	}

	logging.Infof("Collected %d recommendations with $%.2f/month potential savings",
		len(allRecommendations), totalSavings)

	// Send notification if we have recommendations
	if len(allRecommendations) > 0 && totalSavings > 0 {
		// Sort by savings descending to show top recommendations in email
		sort.Slice(allRecommendations, func(i, j int) bool {
			return allRecommendations[i].Savings > allRecommendations[j].Savings
		})

		data := email.NotificationData{
			DashboardURL: s.dashboardURL,
			TotalSavings: totalSavings,
		}
		for _, rec := range allRecommendations {
			if len(data.Recommendations) >= 10 { // Limit to top 10 in email
				break
			}
			data.Recommendations = append(data.Recommendations, email.RecommendationSummary{
				Service:        rec.Service,
				ResourceType:   rec.ResourceType,
				Engine:         rec.Engine,
				Region:         rec.Region,
				Count:          rec.Count,
				MonthlySavings: rec.Savings,
			})
		}

		if err := s.email.SendNewRecommendationsNotification(ctx, data); err != nil {
			logging.Errorf("Failed to send notification: %v", err)
		}
	}

	return &CollectResult{
		Recommendations: len(allRecommendations),
		TotalSavings:    totalSavings,
	}, nil
}

// collectProviderRecommendations collects recommendations from a specific provider
func (s *Scheduler) collectProviderRecommendations(ctx context.Context, providerName string, globalCfg *config.GlobalConfig) ([]config.RecommendationRecord, error) {
	switch providerName {
	case "aws":
		return s.collectAWSRecommendations(ctx, globalCfg)
	case "azure":
		return s.collectAzureRecommendations(ctx, globalCfg)
	case "gcp":
		return s.collectGCPRecommendations(ctx, globalCfg)
	default:
		logging.Warnf("Unknown provider: %s", providerName)
		return nil, nil
	}
}

// collectAWSRecommendations fetches recommendations from AWS Cost Explorer
func (s *Scheduler) collectAWSRecommendations(ctx context.Context, globalCfg *config.GlobalConfig) ([]config.RecommendationRecord, error) {
	logging.Info("Collecting AWS recommendations...")

	// Create AWS provider
	awsProvider, err := s.providerFactory.CreateAndValidateProvider(ctx, "aws", nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create AWS provider: %w", err)
	}

	// Get recommendations client
	recClient, err := awsProvider.GetRecommendationsClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get AWS recommendations client: %w", err)
	}

	// Build recommendation params from global config.
	// Note: These params are only used in the fallback path (when GetAllRecommendations
	// returns empty). The primary GetAllRecommendations call returns all recommendations
	// and filtering by term/payment is handled later in convertRecommendations
	// with a hardcoded default of term=3.
	params := common.RecommendationParams{
		Term:           fmt.Sprintf("%dyr", globalCfg.DefaultTerm),
		PaymentOption:  globalCfg.DefaultPayment,
		LookbackPeriod: "7d",
	}

	// Get all recommendations
	recommendations, err := recClient.GetAllRecommendations(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get AWS recommendations: %w", err)
	}

	// Also try with params for any specific filtering.
	// Note: This fallback is intentionally silent on error - if both calls
	// fail, we proceed with an empty list and log a warning.
	// This ensures the scheduler continues even if recommendations are unavailable.
	if len(recommendations) == 0 {
		recommendations, err = recClient.GetRecommendations(ctx, params)
		if err != nil {
			logging.Warnf("Failed to get filtered AWS recommendations: %v", err)
		}
	}

	// Convert to internal format
	return s.convertRecommendations(recommendations, "aws"), nil
}

// collectAzureRecommendations fetches recommendations from Azure Advisor
func (s *Scheduler) collectAzureRecommendations(ctx context.Context, globalCfg *config.GlobalConfig) ([]config.RecommendationRecord, error) {
	logging.Info("Collecting Azure recommendations...")

	// Create Azure provider
	azureProvider, err := s.providerFactory.CreateAndValidateProvider(ctx, "azure", nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create Azure provider: %w", err)
	}

	// Get recommendations client
	recClient, err := azureProvider.GetRecommendationsClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get Azure recommendations client: %w", err)
	}

	// Get all recommendations
	recommendations, err := recClient.GetAllRecommendations(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get Azure recommendations: %w", err)
	}

	// Convert to internal format
	return s.convertRecommendations(recommendations, "azure"), nil
}

// collectGCPRecommendations fetches recommendations from GCP Recommender
func (s *Scheduler) collectGCPRecommendations(ctx context.Context, globalCfg *config.GlobalConfig) ([]config.RecommendationRecord, error) {
	logging.Info("Collecting GCP recommendations...")

	// Create GCP provider
	gcpProvider, err := s.providerFactory.CreateAndValidateProvider(ctx, "gcp", nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create GCP provider: %w", err)
	}

	// Get recommendations client
	recClient, err := gcpProvider.GetRecommendationsClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get GCP recommendations client: %w", err)
	}

	// Get all recommendations
	recommendations, err := recClient.GetAllRecommendations(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get GCP recommendations: %w", err)
	}

	// Convert to internal format
	return s.convertRecommendations(recommendations, "gcp"), nil
}

// RecommendationQueryParams holds query parameters for filtering recommendations
type RecommendationQueryParams struct {
	Provider string
	Service  string
	Region   string
}

// GetRecommendations fetches recommendations from all configured providers with optional filtering
func (s *Scheduler) GetRecommendations(ctx context.Context, params RecommendationQueryParams) ([]config.RecommendationRecord, error) {
	logging.Info("Getting recommendations from cloud providers...")

	globalCfg, err := s.config.GetGlobalConfig(ctx)
	if err != nil {
		return nil, err
	}

	return s.collectAndFilterRecommendations(ctx, globalCfg, params)
}

// collectAndFilterRecommendations collects recommendations from all providers and applies filters
func (s *Scheduler) collectAndFilterRecommendations(ctx context.Context, globalCfg *config.GlobalConfig, params RecommendationQueryParams) ([]config.RecommendationRecord, error) {
	var allRecommendations []config.RecommendationRecord

	for _, providerName := range globalCfg.EnabledProviders {
		if !shouldIncludeProvider(providerName, params.Provider) {
			continue
		}

		recs, err := s.collectProviderRecommendations(ctx, providerName, globalCfg)
		if err != nil {
			logging.Errorf("Failed to collect %s recommendations: %v", providerName, err)
			continue
		}

		filtered := filterRecommendations(recs, params)
		allRecommendations = append(allRecommendations, filtered...)
	}

	return allRecommendations, nil
}

// shouldIncludeProvider checks if a provider should be included based on filter
func shouldIncludeProvider(providerName, filter string) bool {
	return filter == "" || filter == providerName
}

// filterRecommendations filters recommendations by service and region
func filterRecommendations(recs []config.RecommendationRecord, params RecommendationQueryParams) []config.RecommendationRecord {
	if params.Service == "" && params.Region == "" {
		return recs
	}

	filtered := make([]config.RecommendationRecord, 0, len(recs))
	for _, rec := range recs {
		if shouldIncludeRecommendation(rec, params) {
			filtered = append(filtered, rec)
		}
	}

	return filtered
}

// shouldIncludeRecommendation checks if a recommendation matches the filters
func shouldIncludeRecommendation(rec config.RecommendationRecord, params RecommendationQueryParams) bool {
	if params.Service != "" && rec.Service != params.Service {
		return false
	}
	if params.Region != "" && rec.Region != params.Region {
		return false
	}
	return true
}

// convertRecommendations converts common.Recommendation slice to config.RecommendationRecord slice
func (s *Scheduler) convertRecommendations(recs []common.Recommendation, providerName string) []config.RecommendationRecord {
	records := make([]config.RecommendationRecord, 0, len(recs))

	for _, rec := range recs {
		// Extract engine from service details if available
		engine := ""
		if rec.Details != nil {
			switch d := rec.Details.(type) {
			case common.DatabaseDetails:
				engine = d.Engine
			case *common.DatabaseDetails:
				engine = d.Engine
			case common.CacheDetails:
				engine = d.Engine
			case *common.CacheDetails:
				engine = d.Engine
			}
		}

		// Parse term to integer (e.g., "3yr" -> 3)
		term := 3
		if rec.Term != "" {
			termStr := strings.TrimSuffix(rec.Term, "yr")
			if parsed, err := strconv.Atoi(termStr); err == nil && parsed > 0 {
				term = parsed
			} else {
				logging.Warnf("Invalid term value %q, defaulting to 3 years", rec.Term)
			}
		}

		key := fmt.Sprintf("%s:%s:%s:%s:%s", providerName, rec.Service, rec.Region, rec.ResourceType, rec.PaymentOption)
		hash := sha256.Sum256([]byte(key))
		recordID := hex.EncodeToString(hash[:])[:16]

		records = append(records, config.RecommendationRecord{
			ID:           recordID,
			Provider:     providerName,
			Service:      string(rec.Service),
			Region:       rec.Region,
			ResourceType: rec.ResourceType,
			Engine:       engine,
			Count:        rec.Count,
			Term:         term,
			Payment:      rec.PaymentOption,
			UpfrontCost:  rec.CommitmentCost,
			MonthlyCost:  0, // Cost Explorer doesn't always provide monthly breakdown
			Savings:      rec.EstimatedSavings,
			Selected:     true, // Default to selected
			Purchased:    false,
		})
	}

	return records
}
