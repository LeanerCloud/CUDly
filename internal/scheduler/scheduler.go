// Package scheduler handles scheduled recommendation collection.
package scheduler

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/LeanerCloud/CUDly/internal/config"
	"github.com/LeanerCloud/CUDly/internal/credentials"
	"github.com/LeanerCloud/CUDly/internal/email"
	"github.com/LeanerCloud/CUDly/internal/execution"
	"github.com/LeanerCloud/CUDly/internal/oidc"
	"github.com/LeanerCloud/CUDly/internal/purchase"
	"github.com/LeanerCloud/CUDly/pkg/common"
	"github.com/LeanerCloud/CUDly/pkg/logging"
	"github.com/LeanerCloud/CUDly/pkg/provider"
	azureprovider "github.com/LeanerCloud/CUDly/providers/azure"
	gcpprovider "github.com/LeanerCloud/CUDly/providers/gcp"
	"golang.org/x/sync/errgroup"
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

	// IsLambda gates the stale-while-revalidate background goroutine.
	// On Lambda, goroutines freeze between invocations — firing one from
	// a request handler would corrupt state — so we fall back to the
	// scheduled cron + manual refresh. Cloud Run / Container Apps run
	// long-lived processes where the goroutine is safe.
	IsLambda bool
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

	// isLambda gates the stale-while-revalidate background goroutine. See
	// SchedulerConfig.IsLambda for the rationale.
	isLambda bool

	// cacheTTL is the age past which opportunistic background refresh kicks
	// in on non-Lambda runtimes. Parsed from CUDLY_RECOMMENDATION_CACHE_TTL
	// at NewScheduler time; defaults to 6h.
	cacheTTL time.Duration

	// collecting is a single-flight guard so N concurrent stale reads only
	// trigger ONE background refresh.
	collecting atomic.Bool
}

// defaultCacheTTL is the fallback when CUDLY_RECOMMENDATION_CACHE_TTL is
// unset or invalid. 6h is shorter than the default 1-day cron so
// opportunistic refresh closes the gap when users are active.
const defaultCacheTTL = 6 * time.Hour

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
		isLambda:        cfg.IsLambda,
		cacheTTL:        cacheTTLFromEnv(),
	}
}

// cacheTTLFromEnv parses CUDLY_RECOMMENDATION_CACHE_TTL using
// time.ParseDuration. Falls back to defaultCacheTTL on parse error (with
// a warning) so a mis-set env doesn't take the feature offline.
func cacheTTLFromEnv() time.Duration {
	v := os.Getenv("CUDLY_RECOMMENDATION_CACHE_TTL")
	if v == "" {
		return defaultCacheTTL
	}
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		logging.Warnf("CUDLY_RECOMMENDATION_CACHE_TTL invalid (%q); using default %s", v, defaultCacheTTL)
		return defaultCacheTTL
	}
	return d
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
	// outcomes so persistence can scope stale-row eviction to (provider, account)
	// pairs that actually ran. A partial collection (e.g. one of three Azure
	// subscriptions failed) preserves the failed pairs' previous-cycle rows
	// instead of wiping the whole provider's slice.
	var allRecommendations []config.RecommendationRecord
	var totalSavings float64
	var successfulProviders []string
	var successfulCollects []config.SuccessfulCollect
	failedProviders := map[string]string{}

	for _, providerName := range globalCfg.EnabledProviders {
		logging.Infof("Collecting recommendations from %s...", providerName)

		recs, succeededAccountIDs, err := s.collectProviderRecommendations(ctx, providerName, globalCfg)
		if err != nil {
			logging.Errorf("Failed to collect %s recommendations: %v", providerName, err)
			failedProviders[providerName] = err.Error()
			continue
		}
		successfulProviders = append(successfulProviders, providerName)
		successfulCollects = append(successfulCollects, expandSuccessfulCollects(providerName, succeededAccountIDs)...)

		for _, rec := range recs {
			totalSavings += rec.Savings
		}
		allRecommendations = append(allRecommendations, recs...)
	}

	logging.Infof("Collected %d recommendations with $%.2f/month potential savings",
		len(allRecommendations), totalSavings)

	s.persistCollection(ctx, allRecommendations, successfulCollects, failedProviders)

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
// cache via UpsertRecommendations, which:
//
//   - Upserts each row by natural key so re-collected recommendations
//     reuse the same PK across runs (stable IDs for frontend).
//   - Evicts stale rows (collected_at < $now) but ONLY for the
//     (provider, account) pairs that successfully collected in this run.
//     Failed pairs' rows stay visible so the dashboard isn't blanked out
//     by transient cloud-API failures, even when one of several accounts
//     under the same provider succeeded.
//
// On any failure (including partial), the collection error is additionally
// recorded in recommendations_state so the frontend banner renders while
// the user still sees the valid rows we managed to upsert.
func (s *Scheduler) persistCollection(ctx context.Context, recs []config.RecommendationRecord, successfulCollects []config.SuccessfulCollect, failedProviders map[string]string) {
	if err := s.config.UpsertRecommendations(ctx, time.Now().UTC(), recs, successfulCollects); err != nil {
		logging.Errorf("Failed to persist recommendations: %v", err)
		if setErr := s.config.SetRecommendationsCollectionError(ctx, err.Error()); setErr != nil {
			logging.Errorf("Failed to record write error: %v", setErr)
		}
		return
	}

	if len(failedProviders) > 0 {
		if err := s.config.SetRecommendationsCollectionError(ctx, joinProviderErrors(failedProviders)); err != nil {
			logging.Errorf("Failed to record collection error: %v", err)
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

// collectProviderRecommendations collects recommendations from a specific
// provider. The succeededAccountIDs return value is the per-account roster
// that completed in this run; the scheduler threads it into
// []config.SuccessfulCollect for account-scoped eviction. For the AWS
// ambient path (no registered accounts) the slice contains a single empty
// string sentinel that expandSuccessfulCollects translates into a nil
// CloudAccountID. Provider-level errors propagate unchanged.
func (s *Scheduler) collectProviderRecommendations(ctx context.Context, providerName string, globalCfg *config.GlobalConfig) ([]config.RecommendationRecord, []string, error) {
	switch providerName {
	case "aws":
		return s.collectAWSRecommendations(ctx, globalCfg)
	case "azure":
		return s.collectAzureRecommendations(ctx, globalCfg)
	case "gcp":
		return s.collectGCPRecommendations(ctx, globalCfg)
	default:
		logging.Warnf("Unknown provider: %s", providerName)
		return nil, nil, nil
	}
}

// expandSuccessfulCollects converts the per-account ID slice returned by
// each provider collect into the typed []SuccessfulCollect the persist
// layer expects. The empty-string sentinel is translated into a nil
// CloudAccountID so the eviction's account_key join uses uuid.Nil
// (matching the AWS ambient-credential path).
func expandSuccessfulCollects(providerName string, accountIDs []string) []config.SuccessfulCollect {
	out := make([]config.SuccessfulCollect, 0, len(accountIDs))
	for _, id := range accountIDs {
		sc := config.SuccessfulCollect{Provider: providerName}
		if id != "" {
			id := id // pin
			sc.CloudAccountID = &id
		}
		out = append(out, sc)
	}
	return out
}

// collectAWSRecommendations fans out across all enabled AWS accounts.
// If no accounts are registered and CUDly runs on AWS, it falls back to
// ambient credentials (backward compatibility with single-account setups).
// Returns the merged recommendations + the IDs of accounts that succeeded
// (or [""] for the ambient path so the caller can synthesise a nil
// CloudAccountID for eviction).
func (s *Scheduler) collectAWSRecommendations(ctx context.Context, globalCfg *config.GlobalConfig) ([]config.RecommendationRecord, []string, error) {
	accounts := s.enabledAccounts(ctx, "aws")

	// Backward-compatible fallback: no registered accounts → ambient credentials
	if len(accounts) == 0 {
		recs, err := s.collectAWSAmbient(ctx, globalCfg)
		if err != nil {
			return nil, nil, err
		}
		return recs, []string{""}, nil
	}

	recs, outcome := fanOutPerAccount(ctx, "AWS", accounts, func(ctx context.Context, acct config.CloudAccount) ([]config.RecommendationRecord, error) {
		return s.collectAWSForAccount(ctx, globalCfg, acct)
	})
	if outcome.FailedCount == len(accounts) && len(accounts) > 0 {
		return nil, nil, errAllAccountsFailed("AWS", outcome)
	}
	return recs, outcome.SucceededAccountIDs, nil
}

// accountOutcome is the per-provider summary returned by
// fanOutPerAccount alongside the merged recommendations slice. The
// caller uses this to decide whether the provider's collection
// succeeded overall: if every registered account failed
// (FailedCount == len(accounts) > 0) the caller should surface an
// error so the provider lands in failedProviders + the freshness
// banner shows the failure. Partial success (some accounts succeeded)
// stays a success at the provider level — the per-account [ERROR]
// logs are the operator signal for the failed minority.
//
// SucceededAccountIDs lists the IDs of accounts whose collection
// completed; the scheduler threads these into the
// []SuccessfulCollect slice that drives UpsertRecommendations'
// account-scoped eviction. Order is not preserved — the eviction
// query treats it as a set.
type accountOutcome struct {
	SucceededCount      int
	FailedCount         int
	LastErr             string   // most-recent per-account error message, for surfacing in the banner
	SucceededAccountIDs []string // IDs of accounts that succeeded this run
}

// fanOutPerAccount runs fn concurrently across accounts, bounded by the
// shared CUDLY_MAX_ACCOUNT_PARALLELISM env var. Per-account errors are
// logged and the run continues — the stale-row preservation in the
// UpsertRecommendations write path (see commit "parallelise collection")
// keeps those accounts' older rows visible. Order is not preserved; the
// SQL read in ListStoredRecommendations re-sorts anyway.
//
// Returns the merged recommendations + an accountOutcome the caller
// uses to detect "all accounts failed" → return error → provider
// flagged as failed in CollectRecommendations.
func fanOutPerAccount(
	ctx context.Context,
	providerLabel string,
	accounts []config.CloudAccount,
	fn func(ctx context.Context, acct config.CloudAccount) ([]config.RecommendationRecord, error),
) ([]config.RecommendationRecord, accountOutcome) {
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(execution.ConcurrencyFromEnv())

	// One mutex guards both the recs accumulator AND the outcome
	// counters — the same critical section, no second mutex needed.
	var mu sync.Mutex
	var all []config.RecommendationRecord
	var outcome accountOutcome

	for _, acct := range accounts {
		acct := acct // capture
		g.Go(func() error {
			recs, err := fn(gctx, acct)
			if err != nil {
				if isAccountPermissionError(providerLabel, err) {
					// Operator-fixable misconfiguration (missing IAM role
					// on the deploy SA): log at WARN so a single
					// misconfigured account doesn't drown out other log
					// signals. The collection still counts as a per-account
					// failure — provider-success semantics are unchanged.
					logging.Warnf("%s account %s (%s) permission gap (operator action needed: grant required IAM role): %v",
						providerLabel, acct.Name, acct.ExternalID, err)
				} else {
					logging.Errorf("%s account %s (%s): %v", providerLabel, acct.Name, acct.ExternalID, err)
				}
				mu.Lock()
				outcome.FailedCount++
				outcome.LastErr = fmt.Sprintf("account %s (%s): %v", acct.Name, acct.ExternalID, err)
				mu.Unlock()
				return nil // never fail the whole group — partial is fine
			}
			mu.Lock()
			all = append(all, recs...)
			outcome.SucceededCount++
			outcome.SucceededAccountIDs = append(outcome.SucceededAccountIDs, acct.ID)
			mu.Unlock()
			return nil
		})
	}
	_ = g.Wait() // errs are always nil (swallowed above)
	return all, outcome
}

// isAccountPermissionError reports whether err from a per-account
// collection call represents an operator-fixable IAM permission gap
// (rather than a genuine collector error). Used by fanOutPerAccount to
// downgrade the log severity from ERROR to WARN — see issue #57: a
// single misconfigured GCP account otherwise emits an [ERROR] line on
// every scheduler tick (~15 min) and drowns out other log signals.
//
// Currently dispatches per provider:
//   - "GCP": gcpprovider.IsPermissionError (HTTP 403 / gRPC PermissionDenied)
//   - other providers: false (existing ERROR behaviour preserved until
//     analogous predicates are added for AWS/Azure)
func isAccountPermissionError(providerLabel string, err error) bool {
	if err == nil {
		return false
	}
	switch providerLabel {
	case "GCP":
		return gcpprovider.IsPermissionError(err)
	default:
		return false
	}
}

// errAllAccountsFailed wraps an accountOutcome's LastErr so callers
// (the per-provider collect funcs) can surface a single error to
// CollectRecommendations when every registered account failed.
func errAllAccountsFailed(providerLabel string, outcome accountOutcome) error {
	return fmt.Errorf("%s: all %d accounts failed; last error: %s",
		providerLabel, outcome.FailedCount, outcome.LastErr)
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
func (s *Scheduler) collectAzureRecommendations(ctx context.Context, _ *config.GlobalConfig) ([]config.RecommendationRecord, []string, error) {
	accounts := s.enabledAccounts(ctx, "azure")
	if len(accounts) == 0 {
		logging.Info("No enabled Azure accounts — skipping Azure recommendations")
		return nil, nil, nil
	}

	recs, outcome := fanOutPerAccount(ctx, "Azure", accounts, s.collectAzureForAccount)
	if outcome.FailedCount == len(accounts) && len(accounts) > 0 {
		return nil, nil, errAllAccountsFailed("Azure", outcome)
	}
	return recs, outcome.SucceededAccountIDs, nil
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
func (s *Scheduler) collectGCPRecommendations(ctx context.Context, _ *config.GlobalConfig) ([]config.RecommendationRecord, []string, error) {
	accounts := s.enabledAccounts(ctx, "gcp")
	if len(accounts) == 0 {
		logging.Info("No enabled GCP accounts — skipping GCP recommendations")
		return nil, nil, nil
	}

	recs, outcome := fanOutPerAccount(ctx, "GCP", accounts, s.collectGCPForAccount)
	if outcome.FailedCount == len(accounts) && len(accounts) > 0 {
		return nil, nil, errAllAccountsFailed("GCP", outcome)
	}
	return recs, outcome.SucceededAccountIDs, nil
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

// ListRecommendations reads cached recommendations from the store, applying
// the given filter as SQL WHERE clauses. Previously this did live cloud
// API calls on every request (2–10s per provider); now it's a pure DB
// read, typically under 100ms.
//
// Order of operations:
//  1. Read freshness.
//  2. Cold-start (LastCollectedAt==nil): synchronous CollectRecommendations
//     so the first caller sees real data rather than an empty table. Safe
//     on all runtimes since the call is sync.
//  3. Read from the cache.
//  4. Stale-while-revalidate: if the cache is older than cacheTTL AND we
//     aren't on Lambda, kick off a background CollectRecommendations so
//     the NEXT read sees fresh data. Lambda skips this (goroutines freeze
//     between invocations); the scheduled cron is Lambda's refresh path.
func (s *Scheduler) ListRecommendations(ctx context.Context, filter config.RecommendationFilter) ([]config.RecommendationRecord, error) {
	logging.Info("Reading recommendations from cache...")

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

	recs, err := s.config.ListStoredRecommendations(ctx, filter)
	if err != nil {
		return nil, err
	}

	recs, err = s.applySuppressions(ctx, recs)
	if err != nil {
		// A suppression read failure shouldn't block the recs list —
		// the user-facing cost of that is an empty page; we'd rather
		// over-show than under-show. Log and continue.
		logging.Errorf("failed to apply suppressions; returning un-suppressed recs: %v", err)
	}

	s.maybeKickBackgroundRefresh(freshness)
	return recs, nil
}

// suppressionKey is the 6-tuple used to match suppressions to recs.
type suppressionKey struct {
	accountID, provider, service, region, resourceType, engine string
}

// suppressionAgg aggregates all active suppression rows for a single
// 6-tuple: cumulative count, earliest expiry (drives the badge's
// "Xd remaining"), and the execution whose suppression contributed
// the most (drives the badge deep-link).
type suppressionAgg struct {
	suppressedCount         int
	earliestExpiresAt       time.Time
	primaryExecutionID      string
	primaryExecutionCreated time.Time
	primaryExecutionContrib int
}

// applySuppressions subtracts active purchase_suppressions from each
// rec's count and annotates the rec with the SuppressedCount /
// SuppressionExpiresAt / PrimarySuppressionExecutionID fields that
// drive the "recently purchased" badge on the frontend. Recs whose
// remaining count hits 0 are dropped entirely.
func (s *Scheduler) applySuppressions(ctx context.Context, recs []config.RecommendationRecord) ([]config.RecommendationRecord, error) {
	sups, err := s.config.ListActiveSuppressions(ctx)
	if err != nil {
		return recs, err
	}
	if len(sups) == 0 {
		return recs, nil
	}
	index := aggregateSuppressions(sups)
	return applySuppressionIndex(recs, index), nil
}

// aggregateSuppressions groups a raw suppression list by 6-tuple.
func aggregateSuppressions(sups []config.PurchaseSuppression) map[suppressionKey]*suppressionAgg {
	index := make(map[suppressionKey]*suppressionAgg, len(sups))
	for i := range sups {
		sup := sups[i]
		k := suppressionKey{
			accountID: sup.AccountID, provider: sup.Provider, service: sup.Service,
			region: sup.Region, resourceType: sup.ResourceType, engine: sup.Engine,
		}
		a, ok := index[k]
		if !ok {
			a = &suppressionAgg{earliestExpiresAt: sup.ExpiresAt}
			index[k] = a
		}
		a.suppressedCount += sup.SuppressedCount
		if sup.ExpiresAt.Before(a.earliestExpiresAt) {
			a.earliestExpiresAt = sup.ExpiresAt
		}
		// "Most contribution" selects the biggest single suppression
		// row; ties go to the most-recently-created row so a fresh
		// cancel of an older peer doesn't leave the badge pointing at
		// a stale execution.
		if sup.SuppressedCount > a.primaryExecutionContrib ||
			(sup.SuppressedCount == a.primaryExecutionContrib && sup.CreatedAt.After(a.primaryExecutionCreated)) {
			a.primaryExecutionID = sup.ExecutionID
			a.primaryExecutionCreated = sup.CreatedAt
			a.primaryExecutionContrib = sup.SuppressedCount
		}
	}
	return index
}

// applySuppressionIndex walks recs, subtracts the matching aggregate
// count from each rec, drops recs that go to 0 or below, and
// annotates the survivors with the badge fields.
func applySuppressionIndex(recs []config.RecommendationRecord, index map[suppressionKey]*suppressionAgg) []config.RecommendationRecord {
	out := recs[:0]
	for _, rec := range recs {
		accountID := ""
		if rec.CloudAccountID != nil {
			accountID = *rec.CloudAccountID
		}
		k := suppressionKey{
			accountID: accountID, provider: rec.Provider, service: rec.Service,
			region: rec.Region, resourceType: rec.ResourceType, engine: rec.Engine,
		}
		a, ok := index[k]
		if !ok {
			out = append(out, rec)
			continue
		}
		rec.Count -= a.suppressedCount
		if rec.Count <= 0 {
			continue // fully covered by recent purchases; hide
		}
		rec.SuppressedCount = a.suppressedCount
		expiry := a.earliestExpiresAt
		rec.SuppressionExpiresAt = &expiry
		primary := a.primaryExecutionID
		rec.PrimarySuppressionExecutionID = &primary
		out = append(out, rec)
	}
	return out
}

// maybeKickBackgroundRefresh spawns a detached CollectRecommendations
// goroutine when the cache is stale AND the runtime can safely run
// background goroutines (i.e. not Lambda). The atomic.Bool guard
// single-flights concurrent callers so only one refresh fires per TTL
// window. Recovers from panics so one bad collect can't crash the
// long-lived process.
func (s *Scheduler) maybeKickBackgroundRefresh(freshness *config.RecommendationsFreshness) {
	if s.isLambda {
		return
	}
	if freshness.LastCollectedAt == nil {
		// Just handled synchronously; nothing to backfill.
		return
	}
	if time.Since(*freshness.LastCollectedAt) < s.cacheTTL {
		return
	}
	if !s.collecting.CompareAndSwap(false, true) {
		// Another refresh is already in flight.
		return
	}

	logging.Infof("Recommendations cache is stale (age > %s); triggering background refresh", s.cacheTTL)
	bgCtx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	go func() {
		defer cancel()
		defer s.collecting.Store(false)
		defer func() {
			if r := recover(); r != nil {
				logging.Errorf("background recommendations refresh panic: %v", r)
			}
		}()
		if _, err := s.CollectRecommendations(bgCtx); err != nil {
			// CollectRecommendations already surfaces errors via
			// recommendations_state.last_collection_error, so just log
			// locally here for operator visibility.
			logging.Errorf("background refresh: %v", err)
		}
	}()
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

		// The ID hash key needs to be unique per logically-distinct rec,
		// otherwise the frontend collapses two rows into one selection (the
		// data-rec-id collision in recommendations.ts:1067-1069 / the
		// selection-set toggle in :1639-1660 — see issue #187), and any
		// downstream stage that dedupes by ID drops the second rec entirely
		// (the AWS-1yr-missing symptom in issue #188).
		//
		// Fields the hash MUST include:
		//   - providerName: separates AWS/Azure/GCP recs
		//   - rec.Account:  separates per-account/per-subscription recs
		//                   sharing the same provider+SKU+region+payment
		//   - rec.Service / rec.Region / rec.ResourceType: the cell
		//   - engine:       MySQL vs Postgres RDS at same SKU collide otherwise
		//   - term:         1yr vs 3yr at same SKU collide otherwise
		//   - rec.PaymentOption: all-upfront vs no-upfront collide otherwise
		//
		// The integer `term` is hashed (not rec.Term) so a rec with Term=""
		// or "3yr" both reduce to the same canonical value and don't drift
		// out of agreement with the persisted Term column.
		key := fmt.Sprintf("%s:%s:%s:%s:%s:%s:%d:%s",
			providerName, rec.Account, rec.Service, rec.Region,
			rec.ResourceType, engine, term, rec.PaymentOption)
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
