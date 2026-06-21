package exchange

import (
	"context"
	"fmt"
	"math/big"
	"time"

	"github.com/LeanerCloud/CUDly/pkg/common"
	"github.com/LeanerCloud/CUDly/pkg/logging"
)

// Record is a lightweight record type for the auto exchange logic.
// It mirrors config.RIRecord but lives in pkg/exchange to avoid
// cross-module imports (pkg/ is a separate Go module from internal/).
type Record struct {
	CompletedAt        *time.Time
	ExpiresAt          *time.Time
	CreatedAt          time.Time
	UpdatedAt          time.Time
	ID                 string
	AccountID          string
	ExchangeID         string
	Region             string
	SourceInstanceType string
	TargetOfferingID   string
	TargetInstanceType string
	PaymentDue         string
	Status             string
	ApprovalToken      string
	Error              string
	Mode               string
	SourceRIIDs        []string
	SourceCount        int
	TargetCount        int
}

// RIExchangeStore is the subset of store operations needed by RunAutoExchange.
type RIExchangeStore interface {
	SaveRIRecord(ctx context.Context, record *Record) error
	CancelAllPendingExchanges(ctx context.Context) (int64, error)
	GetStaleProcessingExchanges(ctx context.Context, olderThan time.Duration) ([]Record, error)
	GetRIExchangeDailySpend(ctx context.Context, date time.Time) (string, error)
	CompleteRIExchange(ctx context.Context, id string, exchangeID string) error
	FailRIExchange(ctx context.Context, id string, errorMsg string) error
}

// ClientInterface abstracts the Client for testability.
type ClientInterface interface {
	GetQuote(ctx context.Context, req *QuoteRequest) (*QuoteSummary, error)
	Execute(ctx context.Context, req *ExecuteRequest) (string, *QuoteSummary, error)
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
	Client         ClientInterface
	LookupOffering LookupOfferingFunc
	RIs            []RIInfo
	Utilization    []UtilizationInfo
	AccountID      string
	Region         string
	DashboardURL   string
	// RIMetadata maps RI ID to its metadata (product description, tenancy, scope, duration).
	RIMetadata map[string]RIMetadataInfo
	Config     RIExchangeConfig
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
	Completed []Outcome
	Pending   []Outcome
	Failed    []Outcome
	Skipped   []SkippedRecommendation
}

// Outcome captures the result of a single exchange attempt.
type Outcome struct {
	RecordID           string
	ApprovalToken      string
	SourceRIID         string
	SourceInstanceType string
	TargetInstanceType string
	TargetOfferingID   string
	PaymentDue         string
	ExchangeID         string
	Error              string
	UtilizationPct     float64
	TargetCount        int32
}

// SkippedRecommendation captures a recommendation that was not processed.
type SkippedRecommendation struct {
	SourceRIID         string
	SourceInstanceType string
	Reason             string
}

const staleProcessingThreshold = 15 * time.Minute

// RunAutoExchange orchestrates automated RI exchanges.
func RunAutoExchange(ctx context.Context, params *RunAutoExchangeParams) (*AutoExchangeResult, error) {
	result := &AutoExchangeResult{Mode: params.Config.Mode}

	// 1. Cancel all stale pending records.
	// Race condition note: if a user clicks approve at 5h59m while this new run
	// fires and cancels pending records, the TransitionRIExchangeStatus atomic
	// WHERE clause prevents the exchange from executing (record already canceled
	// -> returns nil -> handler returns 409).
	canceled, err := params.Store.CancelAllPendingExchanges(ctx)
	if err != nil {
		logging.Warnf("failed to cancel pending exchanges: %v", err)
	} else if canceled > 0 {
		logging.Infof("canceled %d stale pending exchange records", canceled)
	}

	// 2. Log warning for stale processing records
	stale, err := params.Store.GetStaleProcessingExchanges(ctx, staleProcessingThreshold)
	if err != nil {
		logging.Warnf("failed to check stale processing exchanges: %v", err)
	}
	for i := range stale {
		logging.Warnf("stale processing exchange: record_id=%s account_id=%s source_ri_ids=%v updated_at=%s",
			stale[i].ID, stale[i].AccountID, stale[i].SourceRIIDs, stale[i].UpdatedAt.Format(time.RFC3339))
	}

	// 3. Analyze reshaping
	recs := AnalyzeReshaping(params.RIs, params.Utilization, params.Config.UtilizationThreshold)
	if len(recs) == 0 {
		logging.Info("all RIs well-utilized, nothing to do")
		return result, nil
	}

	logging.Infof("found %d reshape recommendations", len(recs))

	perExchangeCap := new(big.Rat).SetFloat64(params.Config.MaxPaymentPerExchangeUSD)

	for i := range recs {
		processRecommendation(ctx, params, &recs[i], perExchangeCap, result)
	}

	return result, nil
}

// processRecommendation handles a single reshape recommendation: validates,
// quotes, and either creates a pending record (manual) or executes (auto).
func processRecommendation(ctx context.Context, params *RunAutoExchangeParams, rec *ReshapeRecommendation, perExchangeCap *big.Rat, result *AutoExchangeResult) {
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
func resolveOffering(ctx context.Context, params *RunAutoExchangeParams, rec *ReshapeRecommendation) (string, *SkippedRecommendation) {
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
func getValidatedQuote(ctx context.Context, params *RunAutoExchangeParams, rec *ReshapeRecommendation, offeringID string, perExchangeCap *big.Rat) (*QuoteSummary, *SkippedRecommendation) {
	quote, err := params.Client.GetQuote(ctx, &QuoteRequest{
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

func processManualExchange(ctx context.Context, params *RunAutoExchangeParams, rec *ReshapeRecommendation, offeringID, paymentDueStr string) Outcome {
	token, err := common.GenerateApprovalToken()
	if err != nil {
		logging.Errorf("failed to generate approval token for %s: %v", rec.SourceRIID, err)
		errMsg := fmt.Sprintf("failed to generate approval token: %v", err)
		// Persist a failed record so an operator auditing the DB sees this
		// failure, mirroring the auto-mode failure paths in
		// processAutoExchange. crypto/rand failures are rare in practice
		// but still merit an audit trail.
		saveFailedRecord(ctx, params, rec, offeringID, paymentDueStr, errMsg, ModeManual)
		return Outcome{
			SourceRIID:         rec.SourceRIID,
			SourceInstanceType: rec.SourceInstanceType,
			TargetInstanceType: rec.TargetInstanceType,
			TargetOfferingID:   offeringID,
			TargetCount:        rec.TargetCount,
			PaymentDue:         paymentDueStr,
			UtilizationPct:     rec.UtilizationPercent,
			Error:              errMsg,
		}
	}
	// 24h is a safety net; the email says "approve within 6 hours" because
	// CancelAllPendingExchanges at the next run start (every 6h) will cancel
	// this record. The 24h expiry catches edge cases where the scheduled run
	// is delayed or disabled.
	expiresAt := time.Now().Add(24 * time.Hour)

	record := &Record{
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
		Mode:               string(ModeManual),
		ExpiresAt:          &expiresAt,
	}

	if err := params.Store.SaveRIRecord(ctx, record); err != nil {
		logging.Errorf("failed to save pending exchange record for %s: %v", rec.SourceRIID, err)
		return Outcome{
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

	return Outcome{
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
func processAutoExchange(ctx context.Context, params *RunAutoExchangeParams, rec *ReshapeRecommendation, offeringID, paymentDueStr string, perExchangeCap *big.Rat) Outcome {
	outcome := Outcome{
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
		saveFailedRecord(ctx, params, rec, offeringID, paymentDueStr, outcome.Error, ModeAuto)
		return outcome
	}

	dailyCap := new(big.Rat).SetFloat64(params.Config.MaxPaymentDailyUSD)
	dailySpent, err := ParseDecimalRat(dailySpendStr)
	if err != nil {
		logging.Errorf("failed to parse daily spend %q: %v", dailySpendStr, err)
		outcome.Error = fmt.Sprintf("failed to parse daily spend: %v", err)
		saveFailedRecord(ctx, params, rec, offeringID, paymentDueStr, outcome.Error, ModeAuto)
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
		saveFailedRecord(ctx, params, rec, offeringID, paymentDueStr, reason, ModeAuto)
		return outcome
	}

	// Execute the exchange
	exchangeID, _, execErr := params.Client.Execute(ctx, &ExecuteRequest{
		Region:           params.Region,
		ReservedIDs:      []string{rec.SourceRIID},
		TargetOfferingID: offeringID,
		TargetCount:      rec.TargetCount,
		MaxPaymentDueUSD: perExchangeCap,
	})

	if execErr != nil {
		logging.Errorf("exchange execution failed for %s: %v", rec.SourceRIID, execErr)
		outcome.Error = execErr.Error()
		saveFailedRecord(ctx, params, rec, offeringID, paymentDueStr, outcome.Error, ModeAuto)
		return outcome
	}

	// Save completed record
	now := time.Now()
	record := &Record{
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
		Mode:               string(ModeAuto),
		CompletedAt:        &now,
	}

	if err := params.Store.SaveRIRecord(ctx, record); err != nil {
		logging.Errorf("failed to save completed exchange record for %s: %v", rec.SourceRIID, err)
	}

	outcome.RecordID = record.ID
	outcome.ExchangeID = exchangeID
	return outcome
}

// Mode constrains the originating code path of an exchange record so
// `saveFailedRecord` (and any future caller) can't silently leak a typo into
// `Record.Mode`. The storage field stays `string` for serialization
// stability — this is a call-site discipline, not a schema change.
type Mode string

const (
	ModeAuto   Mode = "auto"
	ModeManual Mode = "manual"
)

// saveFailedRecord persists a failed exchange attempt for DB audit.
// `mode` distinguishes auto-mode failures from manual-mode failures so
// downstream filters/UI can split the two.
func saveFailedRecord(ctx context.Context, params *RunAutoExchangeParams, rec *ReshapeRecommendation, offeringID, paymentDueStr, errMsg string, mode Mode) {
	record := &Record{
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
		Mode:               string(mode),
	}
	if err := params.Store.SaveRIRecord(ctx, record); err != nil {
		logging.Errorf("failed to save failed exchange record for %s: %v", rec.SourceRIID, err)
	}
}
