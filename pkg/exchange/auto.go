package exchange

import (
	"context"
	"fmt"
	"math/big"
	"time"

	"github.com/LeanerCloud/CUDly/pkg/logging"
	"github.com/google/uuid"
)

// ExchangeRecord is a lightweight record type for the auto exchange logic.
// It mirrors config.RIExchangeRecord but lives in pkg/exchange to avoid
// cross-module imports (pkg/ is a separate Go module from internal/).
type ExchangeRecord struct {
	ID                 string
	AccountID          string
	ExchangeID         string
	Region             string
	SourceRIIDs        []string
	SourceInstanceType string
	SourceCount        int
	TargetOfferingID   string
	TargetInstanceType string
	TargetCount        int
	PaymentDue         string
	Status             string
	ApprovalToken      string
	Error              string
	Mode               string
	CreatedAt          time.Time
	UpdatedAt          time.Time
	CompletedAt        *time.Time
	ExpiresAt          *time.Time
}

// RIExchangeStore is the subset of store operations needed by RunAutoExchange.
type RIExchangeStore interface {
	SaveRIExchangeRecord(ctx context.Context, record *ExchangeRecord) error
	CancelAllPendingExchanges(ctx context.Context) (int64, error)
	GetStaleProcessingExchanges(ctx context.Context, olderThan time.Duration) ([]ExchangeRecord, error)
	GetRIExchangeDailySpend(ctx context.Context, date time.Time) (string, error)
	CompleteRIExchange(ctx context.Context, id string, exchangeID string) error
	FailRIExchange(ctx context.Context, id string, errorMsg string) error
}

// ExchangeClientInterface abstracts the ExchangeClient for testability.
type ExchangeClientInterface interface {
	GetQuote(ctx context.Context, req ExchangeQuoteRequest) (*ExchangeQuoteSummary, error)
	Execute(ctx context.Context, req ExchangeExecuteRequest) (string, *ExchangeQuoteSummary, error)
}

// RIExchangeConfig holds the runtime configuration for auto exchange.
type RIExchangeConfig struct {
	Mode                     string
	UtilizationThreshold     float64
	MaxPaymentPerExchangeUSD float64
	MaxPaymentDailyUSD       float64
	LookbackDays             int
}

// LookupOfferingFunc looks up a target offering ID for a given instance type and RI metadata.
type LookupOfferingFunc func(ctx context.Context, instanceType, productDesc, tenancy, scope string, duration int64) (string, error)

// RunAutoExchangeParams holds all dependencies for RunAutoExchange.
type RunAutoExchangeParams struct {
	Store          RIExchangeStore
	ExchangeClient ExchangeClientInterface
	LookupOffering LookupOfferingFunc
	RIs            []RIInfo
	Utilization    []UtilizationInfo
	Config         RIExchangeConfig
	AccountID      string
	Region         string
	DashboardURL   string

	// RIMetadata maps RI ID to its metadata (product description, tenancy, scope, duration).
	RIMetadata map[string]RIMetadataInfo
}

// RIMetadataInfo holds the offering metadata for a specific RI.
type RIMetadataInfo struct {
	ProductDescription string
	InstanceTenancy    string
	Scope              string
	Duration           int64
}

// AutoExchangeResult contains the outcome of an auto exchange run.
type AutoExchangeResult struct {
	Mode      string
	Completed []ExchangeOutcome
	Pending   []ExchangeOutcome
	Failed    []ExchangeOutcome
	Skipped   []SkippedRecommendation
}

// ExchangeOutcome captures the result of a single exchange attempt.
type ExchangeOutcome struct {
	RecordID           string
	ApprovalToken      string
	SourceRIID         string
	SourceInstanceType string
	TargetInstanceType string
	TargetOfferingID   string
	TargetCount        int32
	PaymentDue         string
	ExchangeID         string
	UtilizationPct     float64
	Error              string
}

// SkippedRecommendation captures a recommendation that was not processed.
type SkippedRecommendation struct {
	SourceRIID         string
	SourceInstanceType string
	Reason             string
}

const staleProcessingThreshold = 15 * time.Minute

// RunAutoExchange orchestrates automated RI exchanges.
func RunAutoExchange(ctx context.Context, params RunAutoExchangeParams) (*AutoExchangeResult, error) {
	result := &AutoExchangeResult{Mode: params.Config.Mode}

	// 1. Cancel all stale pending records.
	// Race condition note: if a user clicks approve at 5h59m while this new run
	// fires and cancels pending records, the TransitionRIExchangeStatus atomic
	// WHERE clause prevents the exchange from executing (record already cancelled
	// → returns nil → handler returns 409).
	cancelled, err := params.Store.CancelAllPendingExchanges(ctx)
	if err != nil {
		logging.Warnf("failed to cancel pending exchanges: %v", err)
	} else if cancelled > 0 {
		logging.Infof("cancelled %d stale pending exchange records", cancelled)
	}

	// 2. Log warning for stale processing records
	stale, err := params.Store.GetStaleProcessingExchanges(ctx, staleProcessingThreshold)
	if err != nil {
		logging.Warnf("failed to check stale processing exchanges: %v", err)
	}
	for _, s := range stale {
		logging.Warnf("stale processing exchange: record_id=%s account_id=%s source_ri_ids=%v updated_at=%s",
			s.ID, s.AccountID, s.SourceRIIDs, s.UpdatedAt.Format(time.RFC3339))
	}

	// 3. Analyze reshaping
	recs := AnalyzeReshaping(params.RIs, params.Utilization, params.Config.UtilizationThreshold)
	if len(recs) == 0 {
		logging.Info("all RIs well-utilized, nothing to do")
		return result, nil
	}

	logging.Infof("found %d reshape recommendations", len(recs))

	perExchangeCap := new(big.Rat).SetFloat64(params.Config.MaxPaymentPerExchangeUSD)

	for _, rec := range recs {
		processRecommendation(ctx, params, rec, perExchangeCap, result)
	}

	return result, nil
}

// processRecommendation handles a single reshape recommendation: validates,
// quotes, and either creates a pending record (manual) or executes (auto).
func processRecommendation(ctx context.Context, params RunAutoExchangeParams, rec ReshapeRecommendation, perExchangeCap *big.Rat, result *AutoExchangeResult) {
	// Skip idle RIs with no target
	if rec.TargetInstanceType == "" {
		result.Skipped = append(result.Skipped, SkippedRecommendation{
			SourceRIID:         rec.SourceRIID,
			SourceInstanceType: rec.SourceInstanceType,
			Reason:             "RI is idle (0% utilization) - no target instance type recommended",
		})
		return
	}

	offeringID, skip := resolveOffering(ctx, params, rec)
	if skip != nil {
		result.Skipped = append(result.Skipped, *skip)
		return
	}

	quote, skip := getValidatedQuote(ctx, params, rec, offeringID, perExchangeCap)
	if skip != nil {
		result.Skipped = append(result.Skipped, *skip)
		return
	}

	paymentDueStr := "0"
	if quote.PaymentDueUSD != nil {
		paymentDueStr = quote.PaymentDueUSD.FloatString(6)
	}

	if params.Config.Mode == "manual" {
		outcome := processManualExchange(ctx, params, rec, offeringID, paymentDueStr)
		if outcome.Error != "" {
			result.Failed = append(result.Failed, outcome)
		} else {
			result.Pending = append(result.Pending, outcome)
		}
	} else {
		outcome := processAutoExchange(ctx, params, rec, offeringID, paymentDueStr, perExchangeCap)
		if outcome.Error != "" {
			result.Failed = append(result.Failed, outcome)
		} else {
			result.Completed = append(result.Completed, outcome)
		}
	}
}

// resolveOffering looks up RI metadata and finds the target offering ID.
// Returns the offering ID on success, or a SkippedRecommendation on failure.
func resolveOffering(ctx context.Context, params RunAutoExchangeParams, rec ReshapeRecommendation) (string, *SkippedRecommendation) {
	meta, ok := params.RIMetadata[rec.SourceRIID]
	if !ok {
		return "", &SkippedRecommendation{
			SourceRIID:         rec.SourceRIID,
			SourceInstanceType: rec.SourceInstanceType,
			Reason:             "missing RI metadata",
		}
	}

	offeringID, err := params.LookupOffering(ctx, rec.TargetInstanceType, meta.ProductDescription, meta.InstanceTenancy, meta.Scope, meta.Duration)
	if err != nil {
		logging.Warnf("offering lookup failed for %s -> %s: %v", rec.SourceRIID, rec.TargetInstanceType, err)
		return "", &SkippedRecommendation{
			SourceRIID:         rec.SourceRIID,
			SourceInstanceType: rec.SourceInstanceType,
			Reason:             fmt.Sprintf("no matching offering found: %v", err),
		}
	}

	return offeringID, nil
}

// getValidatedQuote fetches and validates an exchange quote.
// Returns the quote on success, or a SkippedRecommendation on failure.
func getValidatedQuote(ctx context.Context, params RunAutoExchangeParams, rec ReshapeRecommendation, offeringID string, perExchangeCap *big.Rat) (*ExchangeQuoteSummary, *SkippedRecommendation) {
	quote, err := params.ExchangeClient.GetQuote(ctx, ExchangeQuoteRequest{
		Region:           params.Region,
		ReservedIDs:      []string{rec.SourceRIID},
		TargetOfferingID: offeringID,
		TargetCount:      rec.TargetCount,
	})
	if err != nil {
		logging.Warnf("quote failed for %s: %v", rec.SourceRIID, err)
		return nil, &SkippedRecommendation{
			SourceRIID:         rec.SourceRIID,
			SourceInstanceType: rec.SourceInstanceType,
			Reason:             fmt.Sprintf("quote failed: %v", err),
		}
	}

	if !quote.IsValidExchange {
		return nil, &SkippedRecommendation{
			SourceRIID:         rec.SourceRIID,
			SourceInstanceType: rec.SourceInstanceType,
			Reason:             fmt.Sprintf("invalid exchange: %s", quote.ValidationFailureReason),
		}
	}

	if quote.PaymentDueUSD != nil && quote.PaymentDueUSD.Cmp(perExchangeCap) > 0 {
		return nil, &SkippedRecommendation{
			SourceRIID:         rec.SourceRIID,
			SourceInstanceType: rec.SourceInstanceType,
			Reason: fmt.Sprintf("exceeds per-exchange cap: payment $%s > cap $%.2f",
				quote.PaymentDueUSD.FloatString(2), params.Config.MaxPaymentPerExchangeUSD),
		}
	}

	return quote, nil
}

func processManualExchange(ctx context.Context, params RunAutoExchangeParams, rec ReshapeRecommendation, offeringID, paymentDueStr string) ExchangeOutcome {
	token := uuid.New().String()
	// 24h is a safety net; the email says "approve within 6 hours" because
	// CancelAllPendingExchanges at the next run start (every 6h) will cancel
	// this record. The 24h expiry catches edge cases where the scheduled run
	// is delayed or disabled.
	expiresAt := time.Now().Add(24 * time.Hour)

	record := &ExchangeRecord{
		AccountID:          params.AccountID,
		Region:             params.Region,
		SourceRIIDs:        []string{rec.SourceRIID},
		SourceInstanceType: rec.SourceInstanceType,
		SourceCount:        int(rec.SourceCount),
		TargetOfferingID:   offeringID,
		TargetInstanceType: rec.TargetInstanceType,
		TargetCount:        int(rec.TargetCount),
		PaymentDue:         paymentDueStr,
		Status:             "pending",
		ApprovalToken:      token,
		Mode:               "manual",
		ExpiresAt:          &expiresAt,
	}

	if err := params.Store.SaveRIExchangeRecord(ctx, record); err != nil {
		logging.Errorf("failed to save pending exchange record for %s: %v", rec.SourceRIID, err)
		return ExchangeOutcome{
			SourceRIID:         rec.SourceRIID,
			SourceInstanceType: rec.SourceInstanceType,
			TargetInstanceType: rec.TargetInstanceType,
			TargetOfferingID:   offeringID,
			TargetCount:        rec.TargetCount,
			PaymentDue:         paymentDueStr,
			UtilizationPct:     rec.UtilizationPercent,
			Error:              fmt.Sprintf("failed to save record: %v", err),
		}
	}

	return ExchangeOutcome{
		RecordID:           record.ID,
		ApprovalToken:      token,
		SourceRIID:         rec.SourceRIID,
		SourceInstanceType: rec.SourceInstanceType,
		TargetInstanceType: rec.TargetInstanceType,
		TargetOfferingID:   offeringID,
		TargetCount:        rec.TargetCount,
		PaymentDue:         paymentDueStr,
		UtilizationPct:     rec.UtilizationPercent,
	}
}

// processAutoExchange executes a single exchange in auto mode.
// If overlapping scheduled runs attempt to exchange the same RI, the first
// succeeds and the second fails because AWS replaces the source RI atomically.
// No DB-level mutex is needed — AWS itself guarantees idempotency (an RI can
// only be exchanged once). The failed attempt is recorded with status=failed.
func processAutoExchange(ctx context.Context, params RunAutoExchangeParams, rec ReshapeRecommendation, offeringID, paymentDueStr string, perExchangeCap *big.Rat) ExchangeOutcome {
	outcome := ExchangeOutcome{
		SourceRIID:         rec.SourceRIID,
		SourceInstanceType: rec.SourceInstanceType,
		TargetInstanceType: rec.TargetInstanceType,
		TargetOfferingID:   offeringID,
		TargetCount:        rec.TargetCount,
		PaymentDue:         paymentDueStr,
		UtilizationPct:     rec.UtilizationPercent,
	}

	// Check daily cap
	dailySpendStr, err := params.Store.GetRIExchangeDailySpend(ctx, time.Now())
	if err != nil {
		logging.Errorf("daily cap check failed for %s: %v", rec.SourceRIID, err)
		outcome.Error = fmt.Sprintf("daily cap check failed: %v", err)
		saveFailedRecord(ctx, params, rec, offeringID, paymentDueStr, outcome.Error)
		return outcome
	}

	dailyCap := new(big.Rat).SetFloat64(params.Config.MaxPaymentDailyUSD)
	dailySpent, err := ParseDecimalRat(dailySpendStr)
	if err != nil {
		logging.Errorf("failed to parse daily spend %q: %v", dailySpendStr, err)
		outcome.Error = fmt.Sprintf("failed to parse daily spend: %v", err)
		saveFailedRecord(ctx, params, rec, offeringID, paymentDueStr, outcome.Error)
		return outcome
	}

	paymentDue, err := ParseDecimalRat(paymentDueStr)
	if err != nil {
		logging.Warnf("failed to parse payment due %q: %v, using zero", paymentDueStr, err)
	}
	if paymentDue == nil {
		paymentDue = new(big.Rat)
	}

	newTotal := new(big.Rat).Add(dailySpent, paymentDue)
	if newTotal.Cmp(dailyCap) > 0 {
		reason := fmt.Sprintf("daily cap exceeded: spent $%s + payment $%s > cap $%.2f",
			dailySpent.FloatString(2), paymentDue.FloatString(2), params.Config.MaxPaymentDailyUSD)
		logging.Warnf("skipping exchange for %s: %s", rec.SourceRIID, reason)
		outcome.Error = reason
		saveFailedRecord(ctx, params, rec, offeringID, paymentDueStr, reason)
		return outcome
	}

	// Execute the exchange
	exchangeID, _, execErr := params.ExchangeClient.Execute(ctx, ExchangeExecuteRequest{
		Region:           params.Region,
		ReservedIDs:      []string{rec.SourceRIID},
		TargetOfferingID: offeringID,
		TargetCount:      rec.TargetCount,
		MaxPaymentDueUSD: perExchangeCap,
	})

	if execErr != nil {
		logging.Errorf("exchange execution failed for %s: %v", rec.SourceRIID, execErr)
		outcome.Error = execErr.Error()
		saveFailedRecord(ctx, params, rec, offeringID, paymentDueStr, outcome.Error)
		return outcome
	}

	// Save completed record
	now := time.Now()
	record := &ExchangeRecord{
		AccountID:          params.AccountID,
		Region:             params.Region,
		ExchangeID:         exchangeID,
		SourceRIIDs:        []string{rec.SourceRIID},
		SourceInstanceType: rec.SourceInstanceType,
		SourceCount:        int(rec.SourceCount),
		TargetOfferingID:   offeringID,
		TargetInstanceType: rec.TargetInstanceType,
		TargetCount:        int(rec.TargetCount),
		PaymentDue:         paymentDueStr,
		Status:             "completed",
		Mode:               "auto",
		CompletedAt:        &now,
	}

	if err := params.Store.SaveRIExchangeRecord(ctx, record); err != nil {
		logging.Errorf("failed to save completed exchange record for %s: %v", rec.SourceRIID, err)
	}

	outcome.RecordID = record.ID
	outcome.ExchangeID = exchangeID
	return outcome
}

func saveFailedRecord(ctx context.Context, params RunAutoExchangeParams, rec ReshapeRecommendation, offeringID, paymentDueStr, errMsg string) {
	record := &ExchangeRecord{
		AccountID:          params.AccountID,
		Region:             params.Region,
		SourceRIIDs:        []string{rec.SourceRIID},
		SourceInstanceType: rec.SourceInstanceType,
		SourceCount:        int(rec.SourceCount),
		TargetOfferingID:   offeringID,
		TargetInstanceType: rec.TargetInstanceType,
		TargetCount:        int(rec.TargetCount),
		PaymentDue:         paymentDueStr,
		Status:             "failed",
		Error:              errMsg,
		Mode:               "auto",
	}
	if err := params.Store.SaveRIExchangeRecord(ctx, record); err != nil {
		logging.Errorf("failed to save failed exchange record for %s: %v", rec.SourceRIID, err)
	}
}
