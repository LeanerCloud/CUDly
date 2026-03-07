package api

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strconv"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"

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
