package apihttp

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/big"
	"regexp"
	"strconv"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/smithy-go"

	"github.com/LeanerCloud/CUDly/internal/auth"
	"github.com/LeanerCloud/CUDly/internal/config"
	"github.com/LeanerCloud/CUDly/internal/credentials"
	"github.com/LeanerCloud/CUDly/pkg/exchange"
	"github.com/LeanerCloud/CUDly/pkg/logging"
	awsprovider "github.com/LeanerCloud/CUDly/providers/aws"
	"github.com/LeanerCloud/CUDly/providers/aws/recommendations"
	ec2svc "github.com/LeanerCloud/CUDly/providers/aws/services/ec2"
	azurecompute "github.com/LeanerCloud/CUDly/providers/azure/services/compute"
)

// reshapeEC2Client is the narrow slice of the EC2 client that
// getReshapeRecommendations needs. Scoped to this handler so mocks
// stay small. The concrete *ec2svc.Client returned by
// awsprovider.NewEC2ClientDirect already implements these methods
// (Go structural typing), so the nil-factory fallback path casts it
// directly.
//
// Cross-family alternatives no longer flow through here — they're
// sourced from the cached AWS Cost Explorer purchase recommendations
// in Postgres via purchaseRecLookupFromStore (see exchange_lookup.go),
// so the EC2 client only needs to enumerate convertible RIs.
type reshapeEC2Client interface {
	ListConvertibleReservedInstances(ctx context.Context) ([]ec2svc.ConvertibleRI, error)
}

// reshapeRecsClient is the narrow slice of the recommendations
// adapter that getReshapeRecommendations needs (the utilization
// fetcher injected into the cache wrapper). Scoped identically.
type reshapeRecsClient interface {
	GetRIUtilization(ctx context.Context, lookbackDays int) ([]recommendations.RIUtilization, error)
}

// buildReshapeEC2Client honours the injected factory when set, falling
// back to the direct AWS SDK constructor otherwise. Tests inject a
// stub via Handler.reshapeEC2Factory; prod leaves the field nil.
func (h *Handler) buildReshapeEC2Client(cfg aws.Config) reshapeEC2Client {
	if h.reshapeEC2Factory != nil {
		return h.reshapeEC2Factory(cfg)
	}
	return awsprovider.NewEC2ClientDirect(cfg)
}

// buildReshapeRecsClient mirrors buildReshapeEC2Client for the
// recommendations adapter.
func (h *Handler) buildReshapeRecsClient(cfg aws.Config) reshapeRecsClient {
	if h.reshapeRecsFactory != nil {
		return h.reshapeRecsFactory(cfg)
	}
	return awsprovider.NewRecommendationsClientDirect(cfg)
}

// targetOfferingsEC2Client is the narrow EC2 interface that
// listTargetOfferings needs. Scoped here so tests can inject a tiny
// stub without implementing the full ec2svc.Client surface.
type targetOfferingsEC2Client interface {
	ListConvertibleReservedInstances(ctx context.Context) ([]ec2svc.ConvertibleRI, error)
	ListTargetOfferings(ctx context.Context, params ec2svc.ListTargetOfferingsParams) ([]ec2svc.TargetOffering, error)
}

// buildTargetOfferingsEC2Client honours the injected factory when set,
// falling back to the direct AWS SDK constructor otherwise.
func (h *Handler) buildTargetOfferingsEC2Client(cfg aws.Config) targetOfferingsEC2Client {
	if h.targetOfferingsEC2Factory != nil {
		return h.targetOfferingsEC2Factory(cfg)
	}
	return awsprovider.NewEC2ClientDirect(cfg)
}

// TargetOfferingsResponse is the response for
// GET /api/ri-exchange/target-offerings.
type TargetOfferingsResponse struct {
	Offerings []ec2svc.TargetOffering `json:"offerings"`
}

// offeringIDPattern matches a standard AWS offering UUID used for
// ReservedInstancesOfferingId values. Used both as a server-side guard
// (Defect 2) and to reject any stray free-text before it reaches AWS.
var offeringIDPattern = regexp.MustCompile(
	`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`,
)

// listTargetOfferings returns valid convertible RI exchange target
// offerings for the source RI identified by ?source_ri_id=<uuid>.
//
// The handler looks up the source RI from DescribeReservedInstances,
// extracts its ProductDescription / Tenancy / Scope / Duration /
// OfferingType, and passes those to ec2svc.ListTargetOfferings which
// calls DescribeReservedInstancesOfferings with the same typed-field
// shape used by PR #690. Instance type is intentionally omitted from
// the query so AWS returns all valid target instance types -- the full
// menu of what the user can exchange into.
//
// GET /api/ri-exchange/target-offerings?source_ri_id=<uuid>&region=<region>
func (h *Handler) listTargetOfferings(ctx context.Context, req *events.LambdaFunctionURLRequest) (any, error) {
	if _, err := h.requirePermission(ctx, req, "view", "purchases"); err != nil {
		return nil, err
	}

	sourceRIID := req.QueryStringParameters["source_ri_id"]
	if sourceRIID == "" {
		return nil, NewClientError(400, "source_ri_id is required")
	}

	region := req.QueryStringParameters["region"]
	cfg, err := h.loadAWSConfigWithRegion(ctx, region)
	if err != nil {
		return nil, fmt.Errorf("failed to load AWS config: %w", err)
	}

	ec2Client := h.buildTargetOfferingsEC2Client(cfg)

	// Fetch all convertible RIs to locate the source RI's attributes.
	// DescribeReservedInstances does not support a single-ID filter
	// without the full ARN, so we enumerate and filter by ID.
	ris, err := ec2Client.ListConvertibleReservedInstances(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list convertible RIs: %w", err)
	}

	var sourceRI *ec2svc.ConvertibleRI
	for i := range ris {
		if ris[i].ReservedInstanceID == sourceRIID {
			sourceRI = &ris[i]
			break
		}
	}
	if sourceRI == nil {
		return nil, NewClientError(404, fmt.Sprintf("source RI %q not found in region %s", sourceRIID, cfg.Region))
	}

	offerings, err := ec2Client.ListTargetOfferings(ctx, ec2svc.ListTargetOfferingsParams{
		ProductDescription: sourceRI.ProductDescription,
		Tenancy:            sourceRI.InstanceTenancy,
		Scope:              sourceRI.Scope,
		Duration:           sourceRI.Duration,
		OfferingType:       sourceRI.OfferingType,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list target offerings: %w", err)
	}

	return &TargetOfferingsResponse{Offerings: offerings}, nil
}

// azureExchangeClient is the narrow interface that listExchangeableAzureRIs
// needs from the Azure compute client. Satisfied by
// *azurecompute.ComputeClient; a stub can be injected via
// Handler.azureExchangeFactory for tests.
type azureExchangeClient interface {
	ListExchangeableReservations(ctx context.Context) ([]azurecompute.ExchangeableReservation, error)
}

// buildAzureExchangeClient returns the injected factory result when one has
// been set (test path), or constructs a real Azure compute client by resolving
// per-subscription credentials via the project's credential resolver (production
// path).
//
// Credential resolution mirrors every other Azure call in the project
// (scheduler.collectAzureForAccount, purchase/execution.go resolveAzureProvider):
// look up the registered CloudAccount whose ExternalID matches subscriptionID,
// then call credentials.ResolveAzureTokenCredentialWithOpts with the Handler's
// wired OIDC signer so managed_identity / client_secret / WIF all work.
//
// Graceful empty-state rules (returns nil client, nil error):
//   - subscriptionID is empty AND no Azure accounts are registered at all:
//     Azure is not configured; the caller returns an empty reservations list.
//   - subscriptionID is provided but no matching CloudAccount is found:
//     treated as "Azure not configured for this subscription".
//
// A genuine configuration error (missing credentials, auth failure) returns
// a non-nil error that the handler maps to a 500 with a clear message.
func (h *Handler) buildAzureExchangeClient(ctx context.Context, subscriptionID string) (azureExchangeClient, error) {
	if h.azureExchangeFactory != nil {
		return h.azureExchangeFactory(subscriptionID), nil
	}

	// Look up the registered Azure CloudAccount by its subscription ID.
	// For Azure, ExternalID stores the subscription ID (see handler_accounts.go:
	// buildSelfAccountRequest sets ExternalID = si.ExternalID() = si.SubscriptionID).
	account, err := h.config.GetCloudAccountByExternalID(ctx, "azure", subscriptionID)
	if err != nil {
		return nil, fmt.Errorf("azure: look up account for subscription %q: %w", subscriptionID, err)
	}
	if account == nil {
		// No Azure account registered for this subscription.
		// Return nil client so the caller emits a graceful empty state.
		return nil, nil
	}

	cred, err := credentials.ResolveAzureTokenCredentialWithOpts(ctx, account, h.credStore, credentials.AzureResolveOptions{
		Signer:    h.signer,
		IssuerURL: h.issuerURL,
	})
	if err != nil {
		return nil, fmt.Errorf("azure: resolve credentials for subscription %q: %w", subscriptionID, err)
	}

	// Region is left empty -- ListExchangeableReservations uses the tenant-
	// wide armreservations API which is not scoped to a region.
	return azurecompute.NewClient(cred, subscriptionID, ""), nil
}

// listExchangeableAzureRIs returns all active Azure VM reservations that are
// eligible for the cross-SKU/cross-region exchange flow (InstanceFlexibility
// == On, ProvisioningState == Succeeded). Requires "view:purchases" permission.
//
// The optional ?subscription_id= query parameter scopes the credential lookup
// to the matching registered CloudAccount. When no Azure account is configured
// for the requested subscription (or no subscription is specified and none are
// registered), the handler returns an empty reservations list rather than a 500.
func (h *Handler) listExchangeableAzureRIs(ctx context.Context, req *events.LambdaFunctionURLRequest) (any, error) {
	if _, err := h.requirePermission(ctx, req, "view", "purchases"); err != nil {
		return nil, err
	}

	subscriptionID := req.QueryStringParameters["subscription_id"]

	client, err := h.buildAzureExchangeClient(ctx, subscriptionID)
	if err != nil {
		return nil, fmt.Errorf("failed to build Azure exchange client: %w", err)
	}
	if client == nil {
		// No Azure account configured for this subscription (or no Azure accounts
		// registered at all). Return an empty list rather than a 500 so the page
		// renders a "no reservations" state instead of an opaque error banner.
		return &ExchangeableAzureRIsResponse{Reservations: []azurecompute.ExchangeableReservation{}}, nil
	}

	reservations, err := client.ListExchangeableReservations(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list exchangeable Azure reservations: %w", err)
	}

	return &ExchangeableAzureRIsResponse{Reservations: reservations}, nil
}

// getBaseAWSConfig returns the cached base AWS config, loading it once via sync.Once.
func (h *Handler) getBaseAWSConfig(ctx context.Context) (aws.Config, error) {
	h.awsCfgOnce.Do(func() {
		h.awsCfg, h.awsCfgErr = awsconfig.LoadDefaultConfig(ctx)
	})
	return h.awsCfg, h.awsCfgErr
}

// loadAWSConfigWithRegion returns the cached base config, optionally overriding the region.
func (h *Handler) loadAWSConfigWithRegion(ctx context.Context, region string) (aws.Config, error) {
	cfg, err := h.getBaseAWSConfig(ctx)
	if err != nil {
		return aws.Config{}, err
	}
	if region != "" {
		cfg.Region = region
	}
	return cfg, nil
}

// reshapeCloudAccountInScope checks whether the session's allowed_accounts
// permit access to the deployment's registered AWS cloud account. It returns
// (true, nil) when the session is unrestricted or the cloud account matches,
// (false, nil) when the cloud account is outside the session's scope (caller
// should return an empty response), and (false, err) on any lookup failure.
// Used by listConvertibleRIs, getRIUtilization, and getReshapeRecommendations
// to eliminate duplicated account-scoping blocks.
func (h *Handler) reshapeCloudAccountInScope(ctx context.Context, session *Session) (bool, error) {
	allowed, aErr := h.getAllowedAccounts(ctx, session)
	if aErr != nil {
		return false, fmt.Errorf("failed to get allowed accounts: %w", aErr)
	}
	if auth.IsUnrestrictedAccess(allowed) {
		return true, nil
	}
	cloudAccountID, aErr := h.resolveReshapeCloudAccountID(ctx)
	if aErr != nil {
		return false, fmt.Errorf("failed to resolve cloud account scope: %w", aErr)
	}
	nameByID := h.resolveAccountNamesByID(ctx)
	return auth.MatchesAccount(allowed, cloudAccountID, nameByID[cloudAccountID]), nil
}

// resolveReshapeCloudAccountID returns the cloud account ID for the running
// deployment, using the test-injected reshapeAccountResolver when set and
// falling back to the production resolveAWSCloudAccountID (STS-backed).
func (h *Handler) resolveReshapeCloudAccountID(ctx context.Context) (string, error) {
	if h.reshapeAccountResolver != nil {
		return h.reshapeAccountResolver(ctx)
	}
	return h.resolveAWSCloudAccountID(ctx)
}

// checkListRIsAccountIDParam enforces the ?account_id= chip filter for
// listConvertibleRIs. Returns (true, nil) when the running AWS account matches
// the requested account (or when no account_id param is given), (false, nil)
// when it does not match (caller should return an empty list), and
// (false, err) on STS failure.
func (h *Handler) checkListRIsAccountIDParam(ctx context.Context, params map[string]string) (bool, error) {
	accountID := params["account_id"]
	if accountID == "" {
		return true, nil
	}
	resolve := h.resolveAWSAccountID
	if h.riInstancesAccountResolver != nil {
		resolve = h.riInstancesAccountResolver
	}
	runningAccountID, err := resolve(ctx)
	if err != nil {
		return false, fmt.Errorf("failed to resolve running AWS account for RI scope: %w", err)
	}
	return runningAccountID == accountID, nil
}

// listConvertibleRIs returns all active convertible Reserved Instances for
// the running AWS account.
//
// The optional ?account_id= query parameter narrows the listing to a single
// AWS account so the page honours the Main Header global account filter
// (issue #871). Convertible RIs are read from the deployment's ambient AWS
// credentials, which resolve to exactly one account number; when the chip
// selects a different account, none of these RIs belong to it, so we return
// an empty list rather than the unscoped fleet. A real STS failure fails
// closed (returns an error) instead of silently leaking the ambient account's
// RIs under another account's filter.
func (h *Handler) listConvertibleRIs(ctx context.Context, req *events.LambdaFunctionURLRequest) (any, error) {
	session, err := h.requirePermission(ctx, req, "view", "purchases")
	if err != nil {
		return nil, err
	}

	// Apply the session's allowed_accounts scope: a restricted user must
	// only see RIs for the deployment's registered AWS account.
	if inScope, sErr := h.reshapeCloudAccountInScope(ctx, session); sErr != nil {
		return nil, sErr
	} else if !inScope {
		return &ConvertibleRIsResponse{Instances: []ec2svc.ConvertibleRI{}}, nil
	}

	if inScope, sErr := h.checkListRIsAccountIDParam(ctx, req.QueryStringParameters); sErr != nil {
		return nil, sErr
	} else if !inScope {
		return &ConvertibleRIsResponse{Instances: []ec2svc.ConvertibleRI{}}, nil
	}

	region := req.QueryStringParameters["region"]
	cfg, err := h.loadAWSConfigWithRegion(ctx, region)
	if err != nil {
		return nil, fmt.Errorf("failed to load AWS config: %w", err)
	}

	ec2Client := awsprovider.NewEC2ClientDirect(cfg)
	instances, err := ec2Client.ListConvertibleReservedInstances(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list convertible RIs: %w", err)
	}

	return &ConvertibleRIsResponse{Instances: instances}, nil
}

// getRIUtilization returns per-RI utilization from Cost Explorer.
func (h *Handler) getRIUtilization(ctx context.Context, req *events.LambdaFunctionURLRequest) (any, error) {
	session, err := h.requirePermission(ctx, req, "view", "purchases")
	if err != nil {
		return nil, err
	}

	// Apply the session's allowed_accounts scope.
	if inScope, sErr := h.reshapeCloudAccountInScope(ctx, session); sErr != nil {
		return nil, sErr
	} else if !inScope {
		return &RIUtilizationResponse{Utilization: []recommendations.RIUtilization{}}, nil
	}

	lookbackDays, err := parseLookbackDaysParam(req.QueryStringParameters)
	if err != nil {
		return nil, err
	}

	region := req.QueryStringParameters["region"]
	cfg, err := h.loadAWSConfigWithRegion(ctx, region)
	if err != nil {
		return nil, fmt.Errorf("failed to load AWS config: %w", err)
	}

	recsAdapter := awsprovider.NewRecommendationsClientDirect(cfg)
	utilization, err := h.getRIUtilizationCache().getOrFetch(ctx, region, lookbackDays, riUtilizationCacheTTL, riUtilizationCacheStaleTTL, recsAdapter.GetRIUtilization)
	if err != nil {
		return nil, fmt.Errorf("failed to get RI utilization: %w", err)
	}

	return &RIUtilizationResponse{Utilization: utilization}, nil
}

// parseLookbackDaysParam parses and validates the "lookback_days" query parameter.
// Returns 30 as default when the parameter is absent.
func parseLookbackDaysParam(params map[string]string) (int, error) {
	days := params["lookback_days"]
	if days == "" {
		return 30, nil
	}
	d, err := strconv.Atoi(days)
	if err != nil || d < 1 || d > 365 {
		return 0, NewClientError(400, "lookback_days must be between 1 and 365")
	}
	return d, nil
}

// parseThresholdParam parses and validates the "threshold" query parameter.
func parseThresholdParam(params map[string]string) (float64, error) {
	t := params["threshold"]
	if t == "" {
		return 95.0, nil
	}
	f, err := strconv.ParseFloat(t, 64)
	if err != nil || math.IsNaN(f) || math.IsInf(f, 0) || f < 0 || f > 100 {
		return 0, NewClientError(400, "threshold must be a number between 0 and 100")
	}
	return f, nil
}

// monthlyCostFromConvertibleRI computes the per-instance per-month
// effective cost from an RI's pricing fields, matching the same
// formula effectiveMonthlyCost uses for offerings:
//
//	monthly = (FixedPrice / hours_per_term + UsagePrice + recurring_hourly) × 730
//
// 730 is AWS's canonical hours-per-month constant. Returns zero when
// Duration is zero (defensive — would otherwise divide by zero).
//
// Used to populate exchange.RIInfo.MonthlyCost so the cross-family
// dollar-units pre-filter can compare against per-target offering
// costs computed with the same formula.
func monthlyCostFromConvertibleRI(ri ec2svc.ConvertibleRI) float64 {
	if ri.Duration <= 0 {
		return 0
	}
	hoursPerTerm := float64(ri.Duration) / 3600
	if hoursPerTerm <= 0 {
		return 0
	}
	return ((ri.FixedPrice / hoursPerTerm) + ri.UsagePrice + ri.RecurringHourlyAmount) * 730
}

// convertToExchangeTypes converts provider-specific types to the exchange package types.
func convertToExchangeTypes(instances []ec2svc.ConvertibleRI, utilData []recommendations.RIUtilization) ([]exchange.RIInfo, []exchange.UtilizationInfo) {
	riInfos := make([]exchange.RIInfo, len(instances))
	for i, inst := range instances {
		riInfos[i] = exchange.RIInfo{
			ID:                  inst.ReservedInstanceID,
			InstanceType:        inst.InstanceType,
			InstanceCount:       inst.InstanceCount,
			OfferingClass:       "convertible",
			NormalizationFactor: inst.NormalizationFactor,
			MonthlyCost:         monthlyCostFromConvertibleRI(inst),
			CurrencyCode:        inst.CurrencyCode,
			// Plumb the AWS-reported RI duration straight through —
			// reshape's term-match guard rejects alternatives whose
			// TermSeconds differs from the source so a 3y RI never
			// surfaces as an alternative to a 1y commitment.
			TermSeconds: inst.Duration,
		}
	}

	utilInfos := make([]exchange.UtilizationInfo, len(utilData))
	for i, u := range utilData {
		utilInfos[i] = exchange.UtilizationInfo{
			RIID:               u.ReservedInstanceID,
			UtilizationPercent: u.UtilizationPercent,
		}
	}

	return riInfos, utilInfos
}

// reshapeRequestParams groups parsed query parameters for getReshapeRecommendations.
type reshapeRequestParams struct {
	threshold    float64
	lookbackDays int
	region       string
}

// parseReshapeParams parses the threshold, lookback_days, and region query
// parameters for getReshapeRecommendations in a single call, reducing the
// number of error-check branches in the handler to keep it within the
// gocyclo limit.
func parseReshapeParams(params map[string]string) (reshapeRequestParams, error) {
	threshold, err := parseThresholdParam(params)
	if err != nil {
		return reshapeRequestParams{}, err
	}
	lookbackDays, err := parseLookbackDaysParam(params)
	if err != nil {
		return reshapeRequestParams{}, err
	}
	return reshapeRequestParams{
		threshold:    threshold,
		lookbackDays: lookbackDays,
		region:       params["region"],
	}, nil
}

// getReshapeRecommendations orchestrates fetching convertible RIs + utilization
// and returns reshape recommendations.
func (h *Handler) getReshapeRecommendations(ctx context.Context, req *events.LambdaFunctionURLRequest) (any, error) {
	session, err := h.requirePermission(ctx, req, "view", "purchases")
	if err != nil {
		return nil, err
	}

	// Apply the session's allowed_accounts scope.
	if inScope, sErr := h.reshapeCloudAccountInScope(ctx, session); sErr != nil {
		return nil, sErr
	} else if !inScope {
		return &ReshapeRecommendationsResponse{Recommendations: []exchange.ReshapeRecommendation{}}, nil
	}

	p, err := parseReshapeParams(req.QueryStringParameters)
	if err != nil {
		return nil, err
	}

	cfg, err := h.loadAWSConfigWithRegion(ctx, p.region)
	if err != nil {
		return nil, fmt.Errorf("failed to load AWS config: %w", err)
	}
	// Normalize region: when the caller omits ?region=, loadAWSConfigWithRegion
	// resolves a default from the AWS SDK chain but the local string stays
	// empty — which would scope the RI utilization cache and the recs lookup
	// unscoped, leaking alternatives from other regions onto the reshape page.
	// Adopt the resolved region so every downstream consumer sees the same
	// value the AWS clients are actually talking to.
	if p.region == "" {
		p.region = cfg.Region
	}

	ec2Client := h.buildReshapeEC2Client(cfg)
	instances, err := ec2Client.ListConvertibleReservedInstances(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list convertible RIs: %w", err)
	}

	recsAdapter := h.buildReshapeRecsClient(cfg)
	utilData, err := h.getRIUtilizationCache().getOrFetch(ctx, p.region, p.lookbackDays, riUtilizationCacheTTL, riUtilizationCacheStaleTTL, recsAdapter.GetRIUtilization)
	if err != nil {
		return nil, fmt.Errorf("failed to get RI utilization: %w", err)
	}

	riInfos, utilInfos := convertToExchangeTypes(instances, utilData)
	// Cross-family alternatives are sourced from the cached AWS Cost
	// Explorer purchase recommendations table in Postgres — no per-rec
	// DescribeReservedInstancesOfferings fan-out, no hand-curated
	// peer-family allowlist. The lookup is scoped to the running AWS
	// account (when registered) so a multi-tenant deployment can't
	// surface another tenant's recs. Empty resolved account ID means
	// "no scope filter" for ambient-credentials deployments where
	// CloudAccount registration hasn't happened yet; a real ListCloudAccounts
	// error aborts the request instead of silently falling through to an
	// unscoped query that could match the wrong tenant's recs.
	cloudAccountID, err := h.resolveReshapeCloudAccountID(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve cloud account scope for reshape: %w", err)
	}
	currencyCode := firstNonEmptyCurrency(instances)
	lookup := purchaseRecLookupFromStore(h.config, cloudAccountID)
	recs := exchange.AnalyzeReshapingWithRecs(ctx, riInfos, utilInfos, p.threshold, p.region, currencyCode, lookup)

	resp := &ReshapeRecommendationsResponse{Recommendations: recs}
	h.attachReshapeStaleness(ctx, resp)
	return resp, nil
}

// attachReshapeStaleness populates the RecsStaleness and RecsCollectedAt
// fields on resp from the recommendations_state table. Non-fatal: errors
// are logged and the response ships without staleness metadata so the
// reshape table itself is unaffected by a DB read-side failure.
func (h *Handler) attachReshapeStaleness(ctx context.Context, resp *ReshapeRecommendationsResponse) {
	freshness, err := h.config.GetRecommendationsFreshness(ctx)
	if err != nil {
		logging.Warnf("getReshapeRecommendations: could not check recs freshness (banner suppressed): %v", err)
		return
	}
	resp.RecsCollectedAt = freshness.LastCollectedAt
	if freshness.LastCollectedAt == nil {
		// Cold start: cache was never populated — treat as hard-stale so the
		// banner fires on a fresh deployment rather than silently hiding it.
		resp.RecsStaleness = "hard"
	} else {
		resp.RecsStaleness = classifyRecsAge(time.Since(*freshness.LastCollectedAt))
	}
}

// firstNonEmptyCurrency returns the CurrencyCode of the first RI that
// has one set, defaulting to "USD" for legacy fixtures and the common
// case. The reshape page operates on a single AWS account at a time so
// all RIs share the same currency in practice; picking the first
// populated value is sufficient and avoids a noisy mismatch panic when
// some entries are missing the field.
func firstNonEmptyCurrency(instances []ec2svc.ConvertibleRI) string {
	for _, inst := range instances {
		if inst.CurrencyCode != "" {
			return inst.CurrencyCode
		}
	}
	return "USD"
}

// validateTargets checks each entry in targets for a non-empty, UUID-shaped
// offering_id. Extracted so both getExchangeQuote and validateExecuteExchangeBody
// share the same check without exceeding the gocyclo threshold.
func validateTargets(targets []ExchangeTargetBody) error {
	for i, t := range targets {
		if t.OfferingID == "" {
			return NewClientError(400, fmt.Sprintf("targets[%d].offering_id is required", i))
		}
		if !offeringIDPattern.MatchString(t.OfferingID) {
			return NewClientError(400, fmt.Sprintf(
				"targets[%d].offering_id %q does not look like an AWS offering UUID; "+
					"expected something like 4b2293b4-5fbc-4017-9c75-d5a9d3aa8c91 -- "+
					"did you paste an instance type by mistake?",
				i, t.OfferingID))
		}
	}
	return nil
}

// getExchangeQuote gets a quote for an RI exchange.
func (h *Handler) getExchangeQuote(ctx context.Context, req *events.LambdaFunctionURLRequest) (any, error) {
	if _, err := h.requirePermission(ctx, req, "view", "purchases"); err != nil {
		return nil, err
	}

	var body ExchangeQuoteRequestBody
	if err := json.Unmarshal([]byte(req.Body), &body); err != nil {
		return nil, NewClientError(400, "invalid request body")
	}
	if len(body.RIIDs) == 0 {
		return nil, NewClientError(400, "ri_ids is required")
	}
	if len(body.Targets) == 0 && body.TargetOfferingID == "" {
		return nil, NewClientError(400, "either targets[] or target_offering_id is required")
	}
	if err := validateTargets(body.Targets); err != nil {
		return nil, err
	}

	// Resolve region from the AWS SDK chain when the caller omits it,
	// matching getReshapeRecommendations. Hardcoding us-east-1 would
	// return an incorrect quote for deployments in other regions.
	cfg, err := h.loadAWSConfigWithRegion(ctx, body.Region)
	if err != nil {
		return nil, fmt.Errorf("failed to load AWS config: %w", err)
	}
	region := cfg.Region

	quote, err := exchange.GetExchangeQuote(ctx, exchange.ExchangeQuoteRequest{
		Region:           region,
		ReservedIDs:      body.RIIDs,
		Targets:          toExchangeTargets(body.Targets),
		TargetOfferingID: body.TargetOfferingID,
		TargetCount:      body.TargetCount,
	})
	if err != nil {
		logging.Errorf("exchange quote failed: %v", err)
		return nil, mapAWSExchangeError("exchange quote failed", err)
	}

	return quote, nil
}

// validateExecuteExchangeBody validates an unmarshalled request body
// and returns a caller-friendly 400 on the first offending field.
// Extracted from executeExchange to keep the handler below the
// cyclomatic-complexity threshold; every branch here becomes a
// separate test case so the logic stays inspectable.
func validateExecuteExchangeBody(body ExchangeExecuteRequestBody) error {
	if len(body.RIIDs) == 0 {
		return NewClientError(400, "ri_ids is required")
	}
	if len(body.Targets) == 0 && body.TargetOfferingID == "" {
		return NewClientError(400, "either targets[] or target_offering_id is required")
	}
	if err := validateTargets(body.Targets); err != nil {
		return err
	}
	if body.MaxPaymentDueUSD == "" {
		return NewClientError(400, "max_payment_due_usd is required as a safety guardrail")
	}
	// Region is required on execute: RI exchanges are region-scoped and
	// financially irreversible. Silently defaulting to us-east-1 would
	// execute the exchange in the wrong region for deployments hosted
	// elsewhere, with no way to undo the operation.
	if body.Region == "" {
		return NewClientError(400, "region is required for execute; omitting it risks exchanging RIs in the wrong region")
	}
	return nil
}

// executeExchange executes an RI exchange with a spend-cap guardrail.
// Requires execute:ri-exchange (deliberately separate from execute:purchases)
// because RI exchanges are financially irreversible once submitted to AWS.
func (h *Handler) executeExchange(ctx context.Context, req *events.LambdaFunctionURLRequest) (any, error) {
	if _, err := h.requirePermission(ctx, req, "execute", "ri-exchange"); err != nil {
		return nil, err
	}

	var body ExchangeExecuteRequestBody
	if err := json.Unmarshal([]byte(req.Body), &body); err != nil {
		return nil, NewClientError(400, "invalid request body")
	}
	if err := validateExecuteExchangeBody(body); err != nil {
		return nil, err
	}

	maxRat, err := exchange.ParseDecimalRat(body.MaxPaymentDueUSD)
	if err != nil {
		return nil, NewClientError(400, fmt.Sprintf("invalid max_payment_due_usd: %v", err))
	}

	region := body.Region

	exchangeID, quote, err := exchange.ExecuteExchange(ctx, exchange.ExchangeExecuteRequest{
		Region:           region,
		ReservedIDs:      body.RIIDs,
		Targets:          toExchangeTargets(body.Targets),
		TargetOfferingID: body.TargetOfferingID,
		TargetCount:      body.TargetCount,
		MaxPaymentDueUSD: maxRat,
	})
	if err != nil {
		logging.Errorf("exchange execution failed: %v", err)
		return nil, mapAWSExchangeError("exchange execution failed", err)
	}

	return &ExchangeExecuteResponse{
		ExchangeID: exchangeID,
		Quote:      quote,
	}, nil
}

// awsExchangeClientFaultCodes is the set of AWS error codes that are
// documented client faults for RI exchange operations. These map to
// 4xx responses so the caller receives the original AWS error message
// and understands it was their input that was wrong. All other AWS
// errors remain 5xx (transient / server-side).
var awsExchangeClientFaultCodes = map[string]bool{
	"InvalidOfferingId":                   true,
	"InvalidParameter":                    true,
	"ValidationError":                     true,
	"InvalidReservedInstancesId.NotFound": true,
	"InvalidInstanceID.NotFound":          true,
}

// mapAWSExchangeError converts an AWS SDK error from an RI exchange
// operation to a ClientError with the appropriate HTTP status code.
// AWS 4xx client-fault errors produce a 400 with the original AWS
// message preserved. Any other error produces a 500 (generic server
// failure) using the provided opMsg fallback.
func mapAWSExchangeError(opMsg string, err error) error {
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) && awsExchangeClientFaultCodes[apiErr.ErrorCode()] {
		return NewClientError(400, apiErr.ErrorMessage())
	}
	return NewClientError(500, opMsg)
}

// Response types

// ConvertibleRIsResponse holds the list of convertible RIs.
type ConvertibleRIsResponse struct {
	Instances []ec2svc.ConvertibleRI `json:"instances"`
}

// ExchangeableAzureRIsResponse holds the list of Azure VM reservations that
// are eligible for the cross-SKU/cross-region exchange flow.
type ExchangeableAzureRIsResponse struct {
	Reservations []azurecompute.ExchangeableReservation `json:"reservations"`
}

// RIUtilizationResponse holds per-RI utilization data.
type RIUtilizationResponse struct {
	Utilization []recommendations.RIUtilization `json:"utilization"`
}

// ReshapeRecommendationsResponse holds reshape recommendations.
//
// RecsStaleness is empty when the underlying Cost Explorer cache is
// fresh, "soft" when it is older than reshapeSoftStaleThreshold (12 h),
// and "hard" when it is older than reshapeHardStaleThreshold (24 h).
// RecsCollectedAt carries the raw timestamp so the frontend can build
// its own relative-time label ("last collected 23h ago").
type ReshapeRecommendationsResponse struct {
	Recommendations []exchange.ReshapeRecommendation `json:"recommendations"`
	RecsStaleness   string                           `json:"recs_staleness,omitempty"`
	RecsCollectedAt *time.Time                       `json:"recs_collected_at,omitempty"`
}

// reshapeSoftStaleThreshold is the age at which the reshape recs banner
// transitions to "soft" warning: data may be up to 12 h old.
const reshapeSoftStaleThreshold = 12 * time.Hour

// reshapeHardStaleThreshold is the age at which the reshape recs banner
// transitions to "hard" warning: data is more than 24 h old.
const reshapeHardStaleThreshold = 24 * time.Hour

// classifyRecsAge maps a data age to the staleness label surfaced in
// ReshapeRecommendationsResponse.RecsStaleness. The zero duration
// (cold-cache path: no LastCollectedAt) is treated as "hard" so the
// banner fires on a fresh deployment rather than silently hiding it.
func classifyRecsAge(age time.Duration) string {
	switch {
	case age >= reshapeHardStaleThreshold:
		return "hard"
	case age >= reshapeSoftStaleThreshold:
		return "soft"
	default:
		return ""
	}
}

// ExchangeTargetBody is one entry in an ExchangeQuote/Execute request's
// `targets` array. Mirrors pkg/exchange.TargetConfig but with JSON tags
// shaped for the HTTP surface.
type ExchangeTargetBody struct {
	OfferingID string `json:"offering_id"`
	Count      int32  `json:"count"`
}

// ExchangeQuoteRequestBody is the request body for the quote endpoint.
// Callers may supply either the new `targets` array (preferred) or the
// legacy `target_offering_id` + `target_count` singleton fields. When
// both are present, `targets` wins. Exactly one of them must be
// provided (or the handler returns 400).
type ExchangeQuoteRequestBody struct {
	RIIDs            []string             `json:"ri_ids"`
	Targets          []ExchangeTargetBody `json:"targets,omitempty"`
	TargetOfferingID string               `json:"target_offering_id,omitempty"`
	TargetCount      int32                `json:"target_count,omitempty"`
	Region           string               `json:"region,omitempty"`
}

// ExchangeExecuteRequestBody is the request body for the execute endpoint.
// Same `targets` / legacy-alias semantics as ExchangeQuoteRequestBody.
// `max_payment_due_usd` is a TOTAL cap across all targets in the
// exchange — AWS returns a single aggregated PaymentDue so spend-cap
// checking naturally becomes a total when `targets[]` has multiple
// entries.
type ExchangeExecuteRequestBody struct {
	RIIDs            []string             `json:"ri_ids"`
	Targets          []ExchangeTargetBody `json:"targets,omitempty"`
	TargetOfferingID string               `json:"target_offering_id,omitempty"`
	TargetCount      int32                `json:"target_count,omitempty"`
	MaxPaymentDueUSD string               `json:"max_payment_due_usd"`
	Region           string               `json:"region,omitempty"`
}

// toExchangeTargets converts the HTTP-shaped targets into the
// pkg/exchange shape, preserving nil (not empty) when the caller used
// the legacy singleton fields so the pkg/exchange layer knows to fall
// back to them.
func toExchangeTargets(targets []ExchangeTargetBody) []exchange.TargetConfig {
	if len(targets) == 0 {
		return nil
	}
	out := make([]exchange.TargetConfig, 0, len(targets))
	for _, t := range targets {
		out = append(out, exchange.TargetConfig{OfferingID: t.OfferingID, Count: t.Count})
	}
	return out
}

// ExchangeExecuteResponse is the response from a successful exchange execution.
type ExchangeExecuteResponse struct {
	ExchangeID string                         `json:"exchange_id"`
	Quote      *exchange.ExchangeQuoteSummary `json:"quote"`
}

// getRIExchangeConfig returns the current RI exchange automation settings.
func (h *Handler) getRIExchangeConfig(ctx context.Context, req *events.LambdaFunctionURLRequest) (any, error) {
	if _, err := h.requirePermission(ctx, req, "view", "config"); err != nil {
		return nil, err
	}

	globalCfg, err := h.config.GetGlobalConfig(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to load config: %w", err)
	}

	return &RIExchangeConfigResponse{
		AutoExchangeEnabled:      globalCfg.RIExchangeEnabled,
		Mode:                     globalCfg.RIExchangeMode,
		UtilizationThreshold:     globalCfg.RIExchangeUtilizationThreshold,
		MaxPaymentPerExchangeUSD: globalCfg.RIExchangeMaxPerExchangeUSD,
		MaxPaymentDailyUSD:       globalCfg.RIExchangeMaxDailyUSD,
		LookbackDays:             globalCfg.RIExchangeLookbackDays,
	}, nil
}

// updateRIExchangeConfig updates the RI exchange automation settings.
func (h *Handler) updateRIExchangeConfig(ctx context.Context, req *events.LambdaFunctionURLRequest) (any, error) {
	if _, err := h.requirePermission(ctx, req, "update", "config"); err != nil {
		return nil, err
	}

	var body RIExchangeConfigUpdateRequest
	if err := json.Unmarshal([]byte(req.Body), &body); err != nil {
		return nil, NewClientError(400, "invalid request body")
	}

	if err := body.validate(); err != nil {
		return nil, err
	}

	globalCfg, err := h.config.GetGlobalConfig(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to load config: %w", err)
	}

	globalCfg.RIExchangeEnabled = body.AutoExchangeEnabled
	globalCfg.RIExchangeMode = body.Mode
	globalCfg.RIExchangeUtilizationThreshold = body.UtilizationThreshold
	globalCfg.RIExchangeMaxPerExchangeUSD = body.MaxPaymentPerExchangeUSD
	globalCfg.RIExchangeMaxDailyUSD = body.MaxPaymentDailyUSD
	globalCfg.RIExchangeLookbackDays = body.LookbackDays

	if err := h.config.SaveGlobalConfig(ctx, globalCfg); err != nil {
		return nil, fmt.Errorf("failed to save config: %w", err)
	}

	return &StatusResponse{Status: "updated"}, nil
}

// getRIExchangeHistory returns RI exchange records from the last 12 months.
func (h *Handler) getRIExchangeHistory(ctx context.Context, req *events.LambdaFunctionURLRequest) (any, error) {
	session, err := h.requirePermission(ctx, req, "view", "purchases")
	if err != nil {
		return nil, err
	}

	since := time.Now().AddDate(-1, 0, 0)
	records, err := h.config.GetRIExchangeHistory(ctx, since, 500)
	if err != nil {
		return nil, fmt.Errorf("failed to load exchange history: %w", err)
	}

	// Filter records by the session's allowed_accounts against the record's
	// AccountID. Scoped users don't see history for accounts outside their
	// scope. Admin / unrestricted sessions pass through unchanged.
	allowed, err := h.getAllowedAccounts(ctx, session)
	if err != nil {
		return nil, fmt.Errorf("failed to get allowed accounts: %w", err)
	}
	if !auth.IsUnrestrictedAccess(allowed) {
		nameByID := h.resolveAccountNamesByID(ctx)
		filtered := records[:0]
		for _, r := range records {
			if auth.MatchesAccount(allowed, r.AccountID, nameByID[r.AccountID]) {
				filtered = append(filtered, r)
			}
		}
		records = filtered
	}

	// Strip approval tokens — single-use secrets must not be included in
	// a read-only response that could be cached, logged, or screen-shared.
	for i := range records {
		records[i].ApprovalToken = ""
	}

	return &RIExchangeHistoryResponse{Records: records}, nil
}

// approveRIExchange handles approval of a pending RI exchange.
//
// Three-mode dispatch mirroring approvePurchase (issue #286, issue #300):
//
//  1. Session present AND RBAC-authorized (admin / approve-any / approve-own
//     match) -> session-authed approve, regardless of whether a token is also
//     in the URL. Closes issue #300.
//  2. token != "" -> legacy email-link flow. validateExchangeApproval enforces
//     the token-equality check; the permission-denied fall-through ensures a
//     logged-in user without approve-* can still use an email link they hold.
//  3. token == "" AND no qualifying session -> 403 via
//     approveRIExchangeViaSession's requireSession gate.
func (h *Handler) approveRIExchange(ctx context.Context, req *events.LambdaFunctionURLRequest, id, token string) (any, error) {
	if session := h.tryGetSession(ctx, req); session != nil {
		// Quick RBAC pre-check (no record fetch needed): does this session hold
		// ANY approve right? If yes, hand off to approveRIExchangeViaSession
		// which will re-check ownership with the actual record. If 403, fall
		// through to the token branch so email-link holders can still approve.
		switch err := h.sessionHasApproveRight(ctx, session); {
		case err == nil:
			result, sessErr := h.approveRIExchangeViaSession(ctx, req, id, session)
			if sessErr == nil {
				return result, nil
			}
			// Record-level RBAC denied (e.g. approve-own user is not the creator).
			// If a token is present, preserve legacy token flow; otherwise surface the error.
			if !(token != "" && isPermissionDenied(sessErr)) {
				return nil, sessErr
			}
		case isPermissionDenied(err):
			// Logged-in user without approve-* may still hold a valid email token.
		default:
			return nil, err
		}
	}

	if token != "" {
		return h.approveRIExchangeViaToken(ctx, id, token)
	}

	return h.approveRIExchangeViaSession(ctx, req, id, nil)
}

// approveRIExchangeViaToken is the legacy email-link branch of approveRIExchange.
// It validates the approval token, transitions the exchange to processing, and
// executes it. Extracted to keep approveRIExchange within cyclomatic-complexity limits.
func (h *Handler) approveRIExchangeViaToken(ctx context.Context, id, token string) (any, error) {
	record, err := h.validateExchangeApproval(ctx, id, token)
	if err != nil {
		return nil, err
	}

	// Token-based approval: no session user, so transitioned_by = NULL.
	transitioned, err := h.config.TransitionRIExchangeStatus(ctx, id, "pending", "processing", nil)
	if err != nil {
		return nil, fmt.Errorf("failed to transition exchange status: %w", err)
	}
	if transitioned == nil {
		return nil, NewClientError(409, "exchange already processed, expired, or was cancelled by a newer analysis run")
	}

	return h.executeApprovedExchange(ctx, id, record)
}

// approveRIExchangeViaSession is the session-authed branch of approveRIExchange
// (issue #300). Enforces the approve-any/approve-own RBAC matrix, then atomically
// transitions pending -> processing and executes the exchange. Stamps
// session.Email onto the approved_by column as an audit trail.
//
// The session parameter may be non-nil (already validated by the caller) or nil
// (requireSession will validate it and return 401 if absent).
func (h *Handler) approveRIExchangeViaSession(ctx context.Context, req *events.LambdaFunctionURLRequest, id string, session *Session) (any, error) {
	var err error
	if session == nil {
		session, err = h.requireSession(ctx, req)
		if err != nil {
			return nil, err
		}
	}

	record, err := h.fetchAndAuthorizeRIExchange(ctx, session, id)
	if err != nil {
		return nil, err
	}

	// Session-authed approval: stamp the session user as the actor.
	transitioned, err := h.config.TransitionRIExchangeStatus(ctx, id, "pending", "processing", resolveCreatorUserID(session))
	if err != nil {
		return nil, fmt.Errorf("failed to transition exchange status: %w", err)
	}
	if transitioned == nil {
		return nil, NewClientError(409, "exchange already processed, expired, or was cancelled by a newer analysis run")
	}

	result, execErr := h.executeApprovedExchange(ctx, id, record)

	// Stamp approver attribution (best-effort: the exchange itself already
	// executed, so a stamp failure is logged but not surfaced to the caller).
	if execErr == nil {
		if stampErr := h.config.StampRIExchangeApprovedBy(ctx, id, session.Email); stampErr != nil {
			logging.Errorf("failed to stamp approved_by on exchange %s: %v", id, stampErr)
		}
	}

	return result, execErr
}

// fetchAndAuthorizeRIExchange looks up the pending exchange record by id, checks
// that it is in "pending" state, and then verifies that session is authorised to
// approve it. Extracted from approveRIExchangeViaSession to keep that function
// under the cyclomatic-complexity limit.
func (h *Handler) fetchAndAuthorizeRIExchange(ctx context.Context, session *Session, id string) (*config.RIExchangeRecord, error) {
	if err := validateUUID(id); err != nil {
		return nil, err
	}

	record, err := h.config.GetRIExchangeRecord(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("failed to look up exchange record: %w", err)
	}
	if record == nil {
		return nil, NewClientError(404, "exchange record not found")
	}

	if record.Status != "pending" {
		return nil, NewClientError(409, fmt.Sprintf("exchange %s cannot be approved (status=%s)", id, record.Status))
	}

	if err := h.authorizeSessionApproveRIExchange(ctx, session, record); err != nil {
		return nil, err
	}

	return record, nil
}

// sessionHasApproveRight returns nil when the session holds ANY approve right
// on purchases (admin / approve-any / approve-own) without checking ownership.
// Used by the three-mode dispatch in approveRIExchange to decide whether to route
// to approveRIExchangeViaSession before fetching the record.
func (h *Handler) sessionHasApproveRight(ctx context.Context, session *Session) error {
	// Stateless admin API key: full access, no user row. Administrators-group
	// users pass via the approve-any HasPermissionAPI check below.
	if session.UserID == apiKeyAdminUserID {
		return nil
	}
	if h.auth == nil {
		return NewClientError(500, "authentication service not configured")
	}
	hasAny, err := h.auth.HasPermissionAPI(ctx, session.UserID, auth.ActionApproveAny, auth.ResourcePurchases)
	if err != nil {
		return fmt.Errorf("permission check failed: %w", err)
	}
	if hasAny {
		return nil
	}
	hasOwn, err := h.auth.HasPermissionAPI(ctx, session.UserID, auth.ActionApproveOwn, auth.ResourcePurchases)
	if err != nil {
		return fmt.Errorf("permission check failed: %w", err)
	}
	if hasOwn {
		return nil
	}
	return NewClientError(403, "permission denied: requires approve-any or approve-own on purchases")
}

// authorizeSessionApproveRIExchange returns nil when the session is permitted to
// approve the given RI exchange record under the approve-any / approve-own RBAC rules
// (issue #300). Mirrors authorizeSessionApprove from handler_purchases.go.
//
// The RI exchange shares ResourcePurchases because approval is conceptually
// "approving a purchase action on a different resource type" per the issue spec,
// which prefers reusing the existing verbs to keep the matrix small.
func (h *Handler) authorizeSessionApproveRIExchange(ctx context.Context, session *Session, record *config.RIExchangeRecord) error {
	// Stateless admin API key: full access, no user row. Administrators-group
	// users pass via the approve-any HasPermissionAPI check below.
	if session.UserID == apiKeyAdminUserID {
		return nil
	}
	if h.auth == nil {
		return NewClientError(500, "authentication service not configured")
	}

	hasAny, err := h.auth.HasPermissionAPI(ctx, session.UserID, auth.ActionApproveAny, auth.ResourcePurchases)
	if err != nil {
		return fmt.Errorf("permission check failed: %w", err)
	}
	if hasAny {
		return nil
	}

	hasOwn, err := h.auth.HasPermissionAPI(ctx, session.UserID, auth.ActionApproveOwn, auth.ResourcePurchases)
	if err != nil {
		return fmt.Errorf("permission check failed: %w", err)
	}
	if !hasOwn {
		return NewClientError(403, "permission denied: requires approve-any or approve-own on purchases")
	}

	// approve-own: only allow if the session user created this exchange.
	if record.CreatedByUserID == nil || *record.CreatedByUserID != session.UserID {
		return NewClientError(403, "permission denied: cannot approve another user's pending exchange")
	}

	return nil
}

// validateExchangeApproval validates ID, token, and record state for an exchange approval.
func (h *Handler) validateExchangeApproval(ctx context.Context, id, token string) (*config.RIExchangeRecord, error) {
	if err := validateUUID(id); err != nil {
		return nil, err
	}
	if token == "" {
		return nil, NewClientError(400, "approval token is required")
	}

	record, err := h.config.GetRIExchangeRecord(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("failed to look up exchange record: %w", err)
	}
	if record == nil {
		return nil, NewClientError(404, "exchange record not found")
	}

	if record.ApprovalToken == "" {
		return nil, NewClientError(403, "this exchange record does not support approval")
	}

	if subtle.ConstantTimeCompare([]byte(token), []byte(record.ApprovalToken)) != 1 {
		return nil, NewClientError(403, "invalid approval token")
	}

	return record, nil
}

// failExchange marks an exchange as failed, logging if the DB write also fails.
func (h *Handler) failExchange(ctx context.Context, id, reason string) (any, error) {
	if failErr := h.config.FailRIExchange(ctx, id, reason); failErr != nil {
		logging.Errorf("failed to mark exchange %s as failed (DB may be unavailable): %v", id, failErr)
	}
	return map[string]any{"status": "failed", "reason": reason}, nil
}

// executeApprovedExchange checks caps and executes the exchange after approval.
func (h *Handler) executeApprovedExchange(ctx context.Context, id string, record *config.RIExchangeRecord) (any, error) {
	dailySpendStr, err := h.config.GetRIExchangeDailySpend(ctx, time.Now())
	if err != nil {
		return h.failExchange(ctx, id, "daily spending cap check failed")
	}

	globalCfg, err := h.config.GetGlobalConfig(ctx)
	if err != nil {
		return h.failExchange(ctx, id, "config load failed")
	}

	if globalCfg.RIExchangeMaxDailyUSD == 0 {
		return h.failExchange(ctx, id, "daily spending cap is not configured (RIExchangeMaxDailyUSD is 0)")
	}
	if reason := checkDailyCap(dailySpendStr, record.PaymentDue, globalCfg.RIExchangeMaxDailyUSD); reason != "" {
		return h.failExchange(ctx, id, reason)
	}

	region := record.Region
	if region == "" {
		// Region is a required field captured at record-creation time.
		// Defaulting to us-east-1 here would execute a financial mutation
		// in the wrong region for records created without a region stamp.
		return h.failExchange(ctx, id, "exchange record has no region; cannot execute safely")
	}

	if globalCfg.RIExchangeMaxPerExchangeUSD == 0 {
		return h.failExchange(ctx, id, "per-exchange spending cap is not configured (RIExchangeMaxPerExchangeUSD is 0)")
	}

	perExchangeCap := new(big.Rat).SetFloat64(globalCfg.RIExchangeMaxPerExchangeUSD)
	exchangeID, _, execErr := exchange.ExecuteExchange(ctx, exchange.ExchangeExecuteRequest{
		Region:           region,
		ReservedIDs:      record.SourceRIIDs,
		TargetOfferingID: record.TargetOfferingID,
		TargetCount:      int32(record.TargetCount),
		MaxPaymentDueUSD: perExchangeCap,
	})
	if execErr != nil {
		return h.failExchange(ctx, id, execErr.Error())
	}

	if err := h.config.CompleteRIExchange(ctx, id, exchangeID); err != nil {
		logging.Errorf("failed to mark exchange %s as completed: %v", id, err)
	}

	return map[string]any{"status": "completed", "exchange_id": exchangeID}, nil
}

// checkDailyCap verifies the exchange payment won't exceed the daily spending cap.
// Returns an empty string if within cap, or a reason string if exceeded.
func checkDailyCap(dailySpendStr, paymentDueStr string, maxDailyUSD float64) string {
	dailyCap := new(big.Rat).SetFloat64(maxDailyUSD)
	dailySpent, err := exchange.ParseDecimalRat(dailySpendStr)
	if err != nil || dailySpent == nil {
		// A parse failure means we cannot determine today's spend; treat as a cap
		// check failure to avoid under-counting spend (fail-safe).
		logging.Warnf("checkDailyCap: failed to parse daily spend string %q: %v; blocking exchange to avoid exceeding cap", dailySpendStr, err)
		return fmt.Sprintf("daily spend check failed: could not parse today's spend value %q", dailySpendStr)
	}
	paymentDue, err := exchange.ParseDecimalRat(paymentDueStr)
	if err != nil || paymentDue == nil {
		logging.Warnf("checkDailyCap: failed to parse payment due string %q: %v; treating as $0", paymentDueStr, err)
		paymentDue = new(big.Rat)
	}

	newTotal := new(big.Rat).Add(dailySpent, paymentDue)
	if newTotal.Cmp(dailyCap) > 0 {
		return fmt.Sprintf("daily cap exceeded: spent $%s + payment $%s > cap $%.2f",
			dailySpent.FloatString(2), paymentDue.FloatString(2), maxDailyUSD)
	}
	return ""
}

// rejectRIExchange handles rejection of a pending RI exchange via token.
func (h *Handler) rejectRIExchange(ctx context.Context, id, token string) (any, error) {
	if err := validateUUID(id); err != nil {
		return nil, err
	}
	if token == "" {
		return nil, NewClientError(400, "rejection token is required")
	}

	record, err := h.config.GetRIExchangeRecord(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("failed to look up exchange record: %w", err)
	}
	if record == nil {
		return nil, NewClientError(404, "exchange record not found")
	}

	if record.ApprovalToken == "" {
		return nil, NewClientError(403, "this exchange record does not support rejection")
	}

	if subtle.ConstantTimeCompare([]byte(token), []byte(record.ApprovalToken)) != 1 {
		return nil, NewClientError(403, "invalid rejection token")
	}

	// Token-based rejection: no session user, so transitioned_by = NULL.
	transitioned, err := h.config.TransitionRIExchangeStatus(ctx, id, "pending", "cancelled", nil)
	if err != nil {
		return nil, fmt.Errorf("failed to transition exchange status: %w", err)
	}
	if transitioned == nil {
		return nil, NewClientError(409, "exchange already processed, expired, or was cancelled")
	}

	return map[string]string{"status": "cancelled"}, nil
}

// RIExchangeConfigResponse is the response for GET /api/ri-exchange/config.
type RIExchangeConfigResponse struct {
	AutoExchangeEnabled      bool    `json:"auto_exchange_enabled"`
	Mode                     string  `json:"mode"`
	UtilizationThreshold     float64 `json:"utilization_threshold"`
	MaxPaymentPerExchangeUSD float64 `json:"max_payment_per_exchange_usd"`
	MaxPaymentDailyUSD       float64 `json:"max_payment_daily_usd"`
	LookbackDays             int     `json:"lookback_days"`
}

// RIExchangeConfigUpdateRequest is the request body for PUT /api/ri-exchange/config.
type RIExchangeConfigUpdateRequest struct {
	AutoExchangeEnabled      bool    `json:"auto_exchange_enabled"`
	Mode                     string  `json:"mode"`
	UtilizationThreshold     float64 `json:"utilization_threshold"`
	MaxPaymentPerExchangeUSD float64 `json:"max_payment_per_exchange_usd"`
	MaxPaymentDailyUSD       float64 `json:"max_payment_daily_usd"`
	LookbackDays             int     `json:"lookback_days"`
}

func (r *RIExchangeConfigUpdateRequest) validate() error {
	if r.Mode != "manual" && r.Mode != "auto" {
		return NewClientError(400, "mode must be 'manual' or 'auto'")
	}
	if r.UtilizationThreshold < 0 || r.UtilizationThreshold > 100 {
		return NewClientError(400, "utilization_threshold must be between 0 and 100")
	}
	if r.LookbackDays < 1 || r.LookbackDays > 365 {
		return NewClientError(400, "lookback_days must be between 1 and 365")
	}
	if r.MaxPaymentPerExchangeUSD < 0 {
		return NewClientError(400, "max_payment_per_exchange_usd must be >= 0")
	}
	if r.MaxPaymentDailyUSD < 0 {
		return NewClientError(400, "max_payment_daily_usd must be >= 0")
	}
	return nil
}

// RIExchangeHistoryResponse is the response for GET /api/ri-exchange/history.
type RIExchangeHistoryResponse struct {
	Records []config.RIExchangeRecord `json:"records"`
}
