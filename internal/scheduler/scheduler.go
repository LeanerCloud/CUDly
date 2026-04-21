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
	"time"

	"github.com/LeanerCloud/CUDly/internal/config"
	"github.com/LeanerCloud/CUDly/internal/credentials"
	"github.com/LeanerCloud/CUDly/internal/email"
	"github.com/LeanerCloud/CUDly/internal/oidc"
	"github.com/LeanerCloud/CUDly/internal/purchase"
	"github.com/LeanerCloud/CUDly/pkg/common"
	"github.com/LeanerCloud/CUDly/pkg/logging"
	"github.com/LeanerCloud/CUDly/pkg/provider"
	azureprovider "github.com/LeanerCloud/CUDly/providers/azure"
	gcpprovider "github.com/LeanerCloud/CUDly/providers/gcp"
)

// SchedulerConfig holds configuration for the scheduler
type SchedulerConfig struct {
	ConfigStore     config.StoreInterface
	PurchaseManager ManagerInterface
	EmailSender     email.SenderInterface
	DashboardURL    string
	// Provider factory for creating cloud providers (allows injection for testing)
	ProviderFactory provider.FactoryInterface
	// Per-account credential resolution (mirrors purchase manager)
	CredentialStore credentials.CredentialStore
	OIDCSigner      oidc.Signer
	OIDCIssuerURL   string
	AssumeRoleSTS   credentials.STSClient
}

// CollectResult holds the result of collecting recommendations.
//
// SuccessfulProviders + FailedProviders track per-provider outcomes so the
// persistence layer (UpsertRecommendations) can scope the delete-stale
// clause to providers that actually ran, and the frontend banner can
// surface the specific failures.
type CollectResult struct {
	Recommendations     int               `json:"recommendations"`
	TotalSavings        float64           `json:"total_savings"`
	SuccessfulProviders []string          `json:"successful_providers,omitempty"`
	FailedProviders     map[string]string `json:"failed_providers,omitempty"`
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
	credStore       credentials.CredentialStore
	oidcSigner      oidc.Signer
	oidcIssuerURL   string
	assumeRoleSTS   credentials.STSClient
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
		credStore:       cfg.CredentialStore,
		oidcSigner:      cfg.OIDCSigner,
		oidcIssuerURL:   cfg.OIDCIssuerURL,
		assumeRoleSTS:   cfg.AssumeRoleSTS,
	}
}

// CollectRecommendations fetches recommendations from all configured cloud providers.
// Persists results to the recommendations cache so read handlers can serve
// from SQL instead of re-fetching live.
func (s *Scheduler) CollectRecommendations(ctx context.Context) (*CollectResult, error) {
	logging.Info("Collecting recommendations from cloud providers...")

	// Get global config
	globalCfg, err := s.config.GetGlobalConfig(ctx)
	if err != nil {
		return nil, err
	}

	// Collect recommendations from each enabled provider, tracking per-provider
	// outcomes so persistence can scope stale-row eviction to providers that
	// actually ran.
	var allRecommendations []config.RecommendationRecord
	var totalSavings float64
	var successfulProviders []string
	failedProviders := map[string]string{}

	for _, providerName := range globalCfg.EnabledProviders {
		logging.Infof("Collecting recommendations from %s...", providerName)

		recs, err := s.collectProviderRecommendations(ctx, providerName, globalCfg)
		if err != nil {
			logging.Errorf("Failed to collect %s recommendations: %v", providerName, err)
			failedProviders[providerName] = err.Error()
			continue
		}
		successfulProviders = append(successfulProviders, providerName)

		for _, rec := range recs {
			totalSavings += rec.Savings
		}
		allRecommendations = append(allRecommendations, recs...)
	}

	logging.Infof("Collected %d recommendations with $%.2f/month potential savings",
		len(allRecommendations), totalSavings)

	s.persistCollection(ctx, allRecommendations, successfulProviders, failedProviders)

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
		Recommendations:     len(allRecommendations),
		TotalSavings:        totalSavings,
		SuccessfulProviders: successfulProviders,
		FailedProviders:     failedProviders,
	}, nil
}

// persistCollection writes the collection results to the recommendations
// cache. On full success it calls ReplaceRecommendations (atomic wipe +
// reinsert). On partial failure it skips the write to preserve the older
// cached rows from the providers that didn't run, then surfaces the error
// through SetRecommendationsCollectionError so the frontend banner renders.
//
// Commit 6 replaces this with UpsertRecommendations + per-provider
// stale-row eviction so partial collects can safely merge fresh data
// without clobbering the older rows. Keeping the simpler Replace path
// here keeps commit 3's diff focused on persistence plumbing.
func (s *Scheduler) persistCollection(ctx context.Context, recs []config.RecommendationRecord, successfulProviders []string, failedProviders map[string]string) {
	if len(failedProviders) > 0 {
		logging.Warnf("Skipping Replace due to %d partial-collect failures; existing cached rows preserved", len(failedProviders))
		if err := s.config.SetRecommendationsCollectionError(ctx, joinProviderErrors(failedProviders)); err != nil {
			logging.Errorf("Failed to record collection error: %v", err)
		}
		return
	}

	if err := s.config.ReplaceRecommendations(ctx, time.Now().UTC(), recs); err != nil {
		logging.Errorf("Failed to persist recommendations: %v", err)
		if setErr := s.config.SetRecommendationsCollectionError(ctx, err.Error()); setErr != nil {
			logging.Errorf("Failed to record write error: %v", setErr)
		}
	}
}

// joinProviderErrors formats a "provider: err; provider: err" string for
// the state table's last_collection_error column.
func joinProviderErrors(failed map[string]string) string {
	parts := make([]string, 0, len(failed))
	for p, msg := range failed {
		parts = append(parts, fmt.Sprintf("%s: %s", p, msg))
	}
	sort.Strings(parts) // deterministic ordering for test stability
	return strings.Join(parts, "; ")
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

// collectAWSRecommendations fans out across all enabled AWS accounts.
// If no accounts are registered and CUDly runs on AWS, it falls back to
// ambient credentials (backward compatibility with single-account setups).
func (s *Scheduler) collectAWSRecommendations(ctx context.Context, globalCfg *config.GlobalConfig) ([]config.RecommendationRecord, error) {
	accounts := s.enabledAccounts(ctx, "aws")

	// Backward-compatible fallback: no registered accounts → ambient credentials
	if len(accounts) == 0 {
		return s.collectAWSAmbient(ctx, globalCfg)
	}

	var all []config.RecommendationRecord
	for _, acct := range accounts {
		recs, err := s.collectAWSForAccount(ctx, globalCfg, acct)
		if err != nil {
			logging.Errorf("AWS account %s (%s): %v", acct.Name, acct.ExternalID, err)
			continue
		}
		all = append(all, recs...)
	}
	return all, nil
}

func (s *Scheduler) collectAWSAmbient(ctx context.Context, globalCfg *config.GlobalConfig) ([]config.RecommendationRecord, error) {
	prov, err := s.providerFactory.CreateAndValidateProvider(ctx, "aws", nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create AWS provider: %w", err)
	}
	return s.fetchAndConvert(ctx, prov, "aws", nil, globalCfg)
}

func (s *Scheduler) collectAWSForAccount(ctx context.Context, globalCfg *config.GlobalConfig, acct config.CloudAccount) ([]config.RecommendationRecord, error) {
	// Self-account (role_arn with no role ARN) or ambient modes use ambient credentials
	if acct.AWSRoleARN == "" {
		prov, err := s.providerFactory.CreateAndValidateProvider(ctx, "aws", nil)
		if err != nil {
			return nil, fmt.Errorf("create ambient provider: %w", err)
		}
		return s.fetchAndConvert(ctx, prov, "aws", &acct.ID, globalCfg)
	}
	awsCreds, err := credentials.ResolveAWSCredentialProvider(ctx, &acct, s.credStore, s.assumeRoleSTS)
	if err != nil {
		return nil, fmt.Errorf("resolve credentials: %w", err)
	}
	cfg := &provider.ProviderConfig{Name: "aws", AWSCredentialsProvider: awsCreds}
	prov, err := s.providerFactory.CreateAndValidateProvider(ctx, "aws", cfg)
	if err != nil {
		return nil, fmt.Errorf("create provider: %w", err)
	}
	return s.fetchAndConvert(ctx, prov, "aws", &acct.ID, globalCfg)
}

// collectAzureRecommendations fans out across all enabled Azure accounts,
// resolving per-account federated credentials via the KMS signer.
func (s *Scheduler) collectAzureRecommendations(ctx context.Context, _ *config.GlobalConfig) ([]config.RecommendationRecord, error) {
	accounts := s.enabledAccounts(ctx, "azure")
	if len(accounts) == 0 {
		logging.Info("No enabled Azure accounts — skipping Azure recommendations")
		return nil, nil
	}

	var all []config.RecommendationRecord
	for _, acct := range accounts {
		recs, err := s.collectAzureForAccount(ctx, acct)
		if err != nil {
			logging.Errorf("Azure account %s (%s): %v", acct.Name, acct.ExternalID, err)
			continue
		}
		all = append(all, recs...)
	}
	return all, nil
}

func (s *Scheduler) collectAzureForAccount(ctx context.Context, acct config.CloudAccount) ([]config.RecommendationRecord, error) {
	azCred, err := credentials.ResolveAzureTokenCredentialWithOpts(ctx, &acct, s.credStore, credentials.AzureResolveOptions{
		Signer:    s.oidcSigner,
		IssuerURL: s.oidcIssuerURL,
	})
	if err != nil {
		return nil, fmt.Errorf("resolve credentials: %w", err)
	}
	azProv, err := azureprovider.NewAzureProvider(&provider.ProviderConfig{Profile: acct.AzureSubscriptionID})
	if err != nil {
		return nil, fmt.Errorf("create provider: %w", err)
	}
	azProv.SetCredential(azCred)

	recClient, err := azProv.GetRecommendationsClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("get recommendations client: %w", err)
	}
	recs, err := recClient.GetAllRecommendations(ctx)
	if err != nil {
		return nil, fmt.Errorf("get recommendations: %w", err)
	}
	return s.tagAccount(s.convertRecommendations(recs, "azure"), acct.ID), nil
}

// collectGCPRecommendations fans out across all enabled GCP accounts,
// resolving per-account federated credentials via the KMS signer.
func (s *Scheduler) collectGCPRecommendations(ctx context.Context, _ *config.GlobalConfig) ([]config.RecommendationRecord, error) {
	accounts := s.enabledAccounts(ctx, "gcp")
	if len(accounts) == 0 {
		logging.Info("No enabled GCP accounts — skipping GCP recommendations")
		return nil, nil
	}

	var all []config.RecommendationRecord
	for _, acct := range accounts {
		recs, err := s.collectGCPForAccount(ctx, acct)
		if err != nil {
			logging.Errorf("GCP account %s (%s): %v", acct.Name, acct.ExternalID, err)
			continue
		}
		all = append(all, recs...)
	}
	return all, nil
}

func (s *Scheduler) collectGCPForAccount(ctx context.Context, acct config.CloudAccount) ([]config.RecommendationRecord, error) {
	gcpTS, err := credentials.ResolveGCPTokenSourceWithOpts(ctx, &acct, s.credStore, credentials.GCPResolveOptions{
		Signer:    s.oidcSigner,
		IssuerURL: s.oidcIssuerURL,
	})
	if err != nil {
		return nil, fmt.Errorf("resolve credentials: %w", err)
	}

	var prov provider.Provider
	if gcpTS != nil {
		prov = gcpprovider.NewProviderWithCredentials(ctx, acct.GCPProjectID, gcpTS)
	} else {
		// ADC mode (application_default): use ambient credentials
		created, err := s.providerFactory.CreateAndValidateProvider(ctx, "gcp", nil)
		if err != nil {
			return nil, fmt.Errorf("create ambient GCP provider: %w", err)
		}
		prov = created
	}

	recClient, err := prov.GetRecommendationsClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("get recommendations client: %w", err)
	}
	recs, err := recClient.GetAllRecommendations(ctx)
	if err != nil {
		return nil, fmt.Errorf("get recommendations: %w", err)
	}
	return s.tagAccount(s.convertRecommendations(recs, "gcp"), acct.ID), nil
}

// enabledAccounts returns all enabled cloud accounts for the given provider.
func (s *Scheduler) enabledAccounts(ctx context.Context, providerName string) []config.CloudAccount {
	enabled := true
	accounts, err := s.config.ListCloudAccounts(ctx, config.CloudAccountFilter{
		Provider: &providerName,
		Enabled:  &enabled,
	})
	if err != nil {
		logging.Errorf("Failed to list %s accounts: %v", providerName, err)
		return nil
	}
	return accounts
}

// fetchAndConvert is a convenience for the AWS ambient path.
func (s *Scheduler) fetchAndConvert(ctx context.Context, prov provider.Provider, providerName string, accountID *string, globalCfg *config.GlobalConfig) ([]config.RecommendationRecord, error) {
	recClient, err := prov.GetRecommendationsClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get %s recommendations client: %w", providerName, err)
	}
	recs, err := recClient.GetAllRecommendations(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get %s recommendations: %w", providerName, err)
	}
	if len(recs) == 0 && globalCfg != nil {
		params := common.RecommendationParams{
			Term:           fmt.Sprintf("%dyr", globalCfg.DefaultTerm),
			PaymentOption:  globalCfg.DefaultPayment,
			LookbackPeriod: "7d",
		}
		recs, _ = recClient.GetRecommendations(ctx, params)
	}
	result := s.convertRecommendations(recs, providerName)
	if accountID != nil {
		result = s.tagAccount(result, *accountID)
	}
	return result, nil
}

// tagAccount sets CloudAccountID on each recommendation record.
func (s *Scheduler) tagAccount(recs []config.RecommendationRecord, accountID string) []config.RecommendationRecord {
	for i := range recs {
		recs[i].CloudAccountID = &accountID
	}
	return recs
}

// RecommendationQueryParams holds query parameters for filtering recommendations
type RecommendationQueryParams struct {
	Provider   string
	Service    string
	Region     string
	AccountIDs []string // filter by cloud account UUIDs; empty = all accounts
}

// GetRecommendations reads cached recommendations from the store, applying
// the given filters as SQL WHERE clauses. Previously this did live cloud
// API calls on every request (2–10s per provider); now it's a pure DB
// read, typically under 100ms.
//
// The upcoming commit 7 wraps this with stale-while-revalidate; for now
// the wrapper is a thin passthrough to config.ListStoredRecommendations.
// On Lambda-safe cold start (empty cache), CollectRecommendations is run
// synchronously first so the user sees data rather than an empty table.
func (s *Scheduler) GetRecommendations(ctx context.Context, params RecommendationQueryParams) ([]config.RecommendationRecord, error) {
	logging.Info("Reading recommendations from cache...")

	// Cold-start fallback: if the cache has never been populated, collect
	// synchronously so the first caller sees real data. Subsequent requests
	// read from the DB. Safe on all runtimes since the call is sync.
	freshness, err := s.config.GetRecommendationsFreshness(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to read recommendations freshness: %w", err)
	}
	if freshness.LastCollectedAt == nil {
		logging.Info("Recommendations cache is empty; performing synchronous cold-start collect")
		if _, err := s.CollectRecommendations(ctx); err != nil {
			return nil, fmt.Errorf("cold-start collect failed: %w", err)
		}
	}

	return s.config.ListStoredRecommendations(ctx, recommendationFilterFromQueryParams(params))
}

// recommendationFilterFromQueryParams translates the API-facing
// RecommendationQueryParams into the DB-facing config.RecommendationFilter.
func recommendationFilterFromQueryParams(params RecommendationQueryParams) config.RecommendationFilter {
	return config.RecommendationFilter{
		Provider:   params.Provider,
		Service:    params.Service,
		Region:     params.Region,
		AccountIDs: params.AccountIDs,
	}
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
