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

	"github.com/LeanerCloud/CUDly/internal/config"
	"github.com/LeanerCloud/CUDly/pkg/exchange"
	"github.com/LeanerCloud/CUDly/pkg/logging"
	awsprovider "github.com/LeanerCloud/CUDly/providers/aws"
	"github.com/LeanerCloud/CUDly/providers/aws/recommendations"
	ec2svc "github.com/LeanerCloud/CUDly/providers/aws/services/ec2"
)

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
	if _, err := h.requireAdmin(ctx, req); err != nil {
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
	if _, err := h.requireAdmin(ctx, req); err != nil {
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
	utilization, err := recsAdapter.GetRIUtilization(ctx, lookbackDays)
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
	if _, err := h.requireAdmin(ctx, req); err != nil {
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

	ec2Client := awsprovider.NewEC2ClientDirect(cfg)
	instances, err := ec2Client.ListConvertibleReservedInstances(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list convertible RIs: %w", err)
	}

	recsAdapter := awsprovider.NewRecommendationsClientDirect(cfg)
	utilData, err := recsAdapter.GetRIUtilization(ctx, lookbackDays)
	if err != nil {
		return nil, fmt.Errorf("failed to get RI utilization: %w", err)
	}

	riInfos, utilInfos := convertToExchangeTypes(instances, utilData)
	recs := exchange.AnalyzeReshaping(riInfos, utilInfos, threshold)

	return &ReshapeRecommendationsResponse{Recommendations: recs}, nil
}

// getExchangeQuote gets a quote for an RI exchange.
func (h *Handler) getExchangeQuote(ctx context.Context, req *events.LambdaFunctionURLRequest) (any, error) {
	if _, err := h.requireAdmin(ctx, req); err != nil {
		return nil, err
	}

	var body ExchangeQuoteRequestBody
	if err := json.Unmarshal([]byte(req.Body), &body); err != nil {
		return nil, NewClientError(400, "invalid request body")
	}
	if len(body.RIIDs) == 0 {
		return nil, NewClientError(400, "ri_ids is required")
	}
	if body.TargetOfferingID == "" {
		return nil, NewClientError(400, "target_offering_id is required")
	}

	region := body.Region
	if region == "" {
		region = "us-east-1"
	}

	quote, err := exchange.GetExchangeQuote(ctx, exchange.ExchangeQuoteRequest{
		Region:           region,
		ReservedIDs:      body.RIIDs,
		TargetOfferingID: body.TargetOfferingID,
		TargetCount:      body.TargetCount,
	})
	if err != nil {
		logging.Errorf("exchange quote failed: %v", err)
		return nil, NewClientError(500, "exchange quote failed")
	}

	return quote, nil
}

// executeExchange executes an RI exchange with a spend-cap guardrail.
func (h *Handler) executeExchange(ctx context.Context, req *events.LambdaFunctionURLRequest) (any, error) {
	if _, err := h.requireAdmin(ctx, req); err != nil {
		return nil, err
	}

	var body ExchangeExecuteRequestBody
	if err := json.Unmarshal([]byte(req.Body), &body); err != nil {
		return nil, NewClientError(400, "invalid request body")
	}
	if len(body.RIIDs) == 0 {
		return nil, NewClientError(400, "ri_ids is required")
	}
	if body.TargetOfferingID == "" {
		return nil, NewClientError(400, "target_offering_id is required")
	}
	if body.MaxPaymentDueUSD == "" {
		return nil, NewClientError(400, "max_payment_due_usd is required as a safety guardrail")
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

// ExchangeQuoteRequestBody is the request body for the quote endpoint.
type ExchangeQuoteRequestBody struct {
	RIIDs            []string `json:"ri_ids"`
	TargetOfferingID string   `json:"target_offering_id"`
	TargetCount      int32    `json:"target_count"`
	Region           string   `json:"region,omitempty"`
}

// ExchangeExecuteRequestBody is the request body for the execute endpoint.
type ExchangeExecuteRequestBody struct {
	RIIDs            []string `json:"ri_ids"`
	TargetOfferingID string   `json:"target_offering_id"`
	TargetCount      int32    `json:"target_count"`
	MaxPaymentDueUSD string   `json:"max_payment_due_usd"`
	Region           string   `json:"region,omitempty"`
}

// ExchangeExecuteResponse is the response from a successful exchange execution.
type ExchangeExecuteResponse struct {
	ExchangeID string                         `json:"exchange_id"`
	Quote      *exchange.ExchangeQuoteSummary `json:"quote"`
}

// getRIExchangeConfig returns the current RI exchange automation settings.
func (h *Handler) getRIExchangeConfig(ctx context.Context, req *events.LambdaFunctionURLRequest) (any, error) {
	if _, err := h.requireAdmin(ctx, req); err != nil {
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
	if _, err := h.requireAdmin(ctx, req); err != nil {
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
	if _, err := h.requireAdmin(ctx, req); err != nil {
		return nil, err
	}

	since := time.Now().AddDate(-1, 0, 0)
	records, err := h.config.GetRIExchangeHistory(ctx, since, 500)
	if err != nil {
		return nil, fmt.Errorf("failed to load exchange history: %w", err)
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
