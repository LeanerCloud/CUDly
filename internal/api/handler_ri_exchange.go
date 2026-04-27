package api

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"math"
	"math/big"
	"strconv"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"

	"github.com/LeanerCloud/CUDly/internal/auth"
	"github.com/LeanerCloud/CUDly/internal/config"
	"github.com/LeanerCloud/CUDly/pkg/exchange"
	"github.com/LeanerCloud/CUDly/pkg/logging"
	awsprovider "github.com/LeanerCloud/CUDly/providers/aws"
	"github.com/LeanerCloud/CUDly/providers/aws/recommendations"
	ec2svc "github.com/LeanerCloud/CUDly/providers/aws/services/ec2"
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

// listConvertibleRIs returns all active convertible Reserved Instances.
func (h *Handler) listConvertibleRIs(ctx context.Context, req *events.LambdaFunctionURLRequest) (any, error) {
	if _, err := h.requirePermission(ctx, req, "view", "purchases"); err != nil {
		return nil, err
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
	if _, err := h.requirePermission(ctx, req, "view", "purchases"); err != nil {
		return nil, err
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

// getReshapeRecommendations orchestrates fetching convertible RIs + utilization
// and returns reshape recommendations.
func (h *Handler) getReshapeRecommendations(ctx context.Context, req *events.LambdaFunctionURLRequest) (any, error) {
	if _, err := h.requirePermission(ctx, req, "view", "purchases"); err != nil {
		return nil, err
	}

	threshold, err := parseThresholdParam(req.QueryStringParameters)
	if err != nil {
		return nil, err
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
	// Normalize region: when the caller omits ?region=, loadAWSConfigWithRegion
	// resolves a default from the AWS SDK chain but the local string stays
	// empty — which would scope the RI utilization cache and the recs lookup
	// unscoped, leaking alternatives from other regions onto the reshape page.
	// Adopt the resolved region so every downstream consumer sees the same
	// value the AWS clients are actually talking to.
	if region == "" {
		region = cfg.Region
	}

	ec2Client := h.buildReshapeEC2Client(cfg)
	instances, err := ec2Client.ListConvertibleReservedInstances(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list convertible RIs: %w", err)
	}

	recsAdapter := h.buildReshapeRecsClient(cfg)
	utilData, err := h.getRIUtilizationCache().getOrFetch(ctx, region, lookbackDays, riUtilizationCacheTTL, riUtilizationCacheStaleTTL, recsAdapter.GetRIUtilization)
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
	// CloudAccount registration hasn't happened yet.
	cloudAccountID := h.resolveAWSCloudAccountID(ctx)
	currencyCode := firstNonEmptyCurrency(instances)
	lookup := purchaseRecLookupFromStore(h.config, cloudAccountID)
	recs := exchange.AnalyzeReshapingWithRecs(ctx, riInfos, utilInfos, threshold, region, currencyCode, lookup)

	return &ReshapeRecommendationsResponse{Recommendations: recs}, nil
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
	for i, t := range body.Targets {
		if t.OfferingID == "" {
			return nil, NewClientError(400, fmt.Sprintf("targets[%d].offering_id is required", i))
		}
	}

	region := body.Region
	if region == "" {
		region = "us-east-1"
	}

	quote, err := exchange.GetExchangeQuote(ctx, exchange.ExchangeQuoteRequest{
		Region:           region,
		ReservedIDs:      body.RIIDs,
		Targets:          toExchangeTargets(body.Targets),
		TargetOfferingID: body.TargetOfferingID,
		TargetCount:      body.TargetCount,
	})
	if err != nil {
		logging.Errorf("exchange quote failed: %v", err)
		return nil, NewClientError(500, "exchange quote failed")
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
	for i, t := range body.Targets {
		if t.OfferingID == "" {
			return NewClientError(400, fmt.Sprintf("targets[%d].offering_id is required", i))
		}
	}
	if body.MaxPaymentDueUSD == "" {
		return NewClientError(400, "max_payment_due_usd is required as a safety guardrail")
	}
	return nil
}

// executeExchange executes an RI exchange with a spend-cap guardrail.
func (h *Handler) executeExchange(ctx context.Context, req *events.LambdaFunctionURLRequest) (any, error) {
	if _, err := h.requirePermission(ctx, req, "execute", "purchases"); err != nil {
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
	if region == "" {
		region = "us-east-1"
	}

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
		return nil, NewClientError(500, "exchange execution failed")
	}

	return &ExchangeExecuteResponse{
		ExchangeID: exchangeID,
		Quote:      quote,
	}, nil
}

// Response types

// ConvertibleRIsResponse holds the list of convertible RIs.
type ConvertibleRIsResponse struct {
	Instances []ec2svc.ConvertibleRI `json:"instances"`
}

// RIUtilizationResponse holds per-RI utilization data.
type RIUtilizationResponse struct {
	Utilization []recommendations.RIUtilization `json:"utilization"`
}

// ReshapeRecommendationsResponse holds reshape recommendations.
type ReshapeRecommendationsResponse struct {
	Recommendations []exchange.ReshapeRecommendation `json:"recommendations"`
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

// approveRIExchange handles approval of a pending RI exchange via token.
func (h *Handler) approveRIExchange(ctx context.Context, id, token string) (any, error) {
	record, err := h.validateExchangeApproval(ctx, id, token)
	if err != nil {
		return nil, err
	}

	// Atomic transition: pending -> processing (checks expiry in WHERE clause)
	transitioned, err := h.config.TransitionRIExchangeStatus(ctx, id, "pending", "processing")
	if err != nil {
		return nil, fmt.Errorf("failed to transition exchange status: %w", err)
	}
	if transitioned == nil {
		return nil, NewClientError(409, "exchange already processed, expired, or was cancelled by a newer analysis run")
	}

	return h.executeApprovedExchange(ctx, id, record)
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
		region = "us-east-1"
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

	if subtle.ConstantTimeCompare([]byte(token), []byte(record.ApprovalToken)) != 1 {
		return nil, NewClientError(403, "invalid rejection token")
	}

	transitioned, err := h.config.TransitionRIExchangeStatus(ctx, id, "pending", "cancelled")
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
