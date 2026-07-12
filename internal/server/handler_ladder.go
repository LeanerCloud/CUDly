package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math/big"
	"time"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/google/uuid"

	"github.com/LeanerCloud/CUDly/internal/config"
	pkgcommon "github.com/LeanerCloud/CUDly/pkg/common"
	pkgladder "github.com/LeanerCloud/CUDly/pkg/ladder"
)

// Cadence thresholds for the per-config self-gate (Q6).
// Each constant is a minimum elapsed time before the same config may run again.
// The 4-hour slack on each bound prevents skipped firings from drifting into
// the next cadence window: a daily at 23:55 still qualifies at 19:55 the next
// day even if the cron fires a few minutes early.
const (
	cadenceDailyMin  = 20 * time.Hour                // daily: 20h (24h - 4h slack)
	cadenceWeeklyMin = 6*24*time.Hour + 20*time.Hour // weekly: 6d 20h (7d - 4h slack)
)

// ladderConfigOutcome is the result of processing a single ladder_config entry
// in the handleLadderRun loop. Using a typed constant avoids bare strings on
// the outcome path.
type ladderConfigOutcome int

const (
	outcomeSkippedDisabled     ladderConfigOutcome = iota
	outcomeSkippedMultiAccount                     // iota = 1
	outcomeSkippedCadence                          // iota = 2
	outcomeErrored                                 // iota = 3
	outcomePlanned                                 // iota = 4
)

// LadderRunResult is the aggregate outcome of one ladder_run task invocation.
// Each counter increments once per ladder_config entry processed.
type LadderRunResult struct {
	Planned             int `json:"planned"`
	SkippedCadence      int `json:"skipped_cadence"`
	SkippedDisabled     int `json:"skipped_disabled"`
	SkippedMultiAccount int `json:"skipped_multi_account"`
	Errored             int `json:"errored"`
}

// record increments the counter that corresponds to the given outcome.
func (r *LadderRunResult) record(o ladderConfigOutcome) {
	switch o {
	case outcomePlanned:
		r.Planned++
	case outcomeSkippedDisabled:
		r.SkippedDisabled++
	case outcomeSkippedMultiAccount:
		r.SkippedMultiAccount++
	case outcomeSkippedCadence:
		r.SkippedCadence++
	case outcomeErrored:
		r.Errored++
	}
}

// handleLadderRun is the top-level orchestrator for the ladder_run scheduled task.
// It iterates over all ladder_configs and runs the planning engine for each
// eligible config. The result struct carries per-outcome counts so the caller
// can surface them in the Lambda log and in future status pages.
//
// plan-only: no PurchaseLayer, ReshapeBuffer, email, or approval tokens.
func (app *Application) handleLadderRun(ctx context.Context) (*LadderRunResult, error) {
	// Global kill-switch check.
	globalCfg, err := app.Config.GetGlobalConfig(ctx)
	if err != nil {
		return nil, fmt.Errorf("ladder_run: failed to load global config: %w", err)
	}
	if !globalCfg.LadderingEnabled {
		log.Println("ladder_run: laddering_enabled=false in global config, skipping all configs")
		return &LadderRunResult{}, nil
	}

	// Parse global-default term and payment option. Fail loud: money-path
	// decisions must never proceed with an invalid/unknown option.
	term, err := ladderTermFromYears(globalCfg.DefaultTerm)
	if err != nil {
		return nil, fmt.Errorf("ladder_run: invalid default_term in global config: %w", err)
	}
	paymentOpt, err := pkgladder.ParsePaymentOption(globalCfg.DefaultPayment)
	if err != nil {
		return nil, fmt.Errorf("ladder_run: invalid default_payment in global config: %w", err)
	}

	// Load all ladder_config rows. GetLadderConfigs returns ALL rows; we
	// filter Enabled=false here so the counter is visible (SkippedDisabled).
	allConfigs, err := app.Config.GetLadderConfigs(ctx)
	if err != nil {
		return nil, fmt.Errorf("ladder_run: failed to load ladder configs: %w", err)
	}
	if len(allConfigs) == 0 {
		log.Println("ladder_run: no ladder_config rows found, nothing to do")
		return &LadderRunResult{}, nil
	}

	// Q1: single-account only. Resolve the Lambda's own AWS account ID via STS
	// to match against each config's cloud account. Configs belonging to a
	// different account are skipped with a visible log line (not silently).
	awsCfg, err := awsconfig.LoadDefaultConfig(ctx)
	if err != nil {
		return nil, fmt.Errorf("ladder_run: failed to load AWS config: %w", err)
	}
	ownAccountID := resolveAccountID(ctx, awsCfg)
	region := awsCfg.Region

	now := time.Now().UTC()
	result := &LadderRunResult{}

	for i := range allConfigs {
		result.record(app.processOneLadderConfig(ctx, &allConfigs[i], ownAccountID, region, term, paymentOpt, now))
	}

	log.Printf("ladder_run done: planned=%d skipped_cadence=%d skipped_disabled=%d skipped_multi_account=%d errored=%d",
		result.Planned, result.SkippedCadence, result.SkippedDisabled, result.SkippedMultiAccount, result.Errored)
	return result, nil
}

// processOneLadderConfig applies all eligibility gates for a single
// ladder_config entry and runs the planning engine when eligible. It is
// extracted from handleLadderRun to keep both functions within the project's
// cyclomatic-complexity limit (10).
func (app *Application) processOneLadderConfig(
	ctx context.Context,
	dbCfg *config.LadderConfigDB,
	ownAccountID, region string,
	term pkgladder.Term,
	paymentOpt pkgladder.PaymentOption,
	now time.Time,
) ladderConfigOutcome {
	if !dbCfg.Enabled {
		log.Printf("ladder_run: config %s: enabled=false, skipping", dbCfg.ID)
		return outcomeSkippedDisabled
	}

	// Resolve the cloud account to get the 12-digit AWS account number (Q2).
	cloudAcct, err := app.Config.GetCloudAccount(ctx, dbCfg.CloudAccountID)
	if err != nil {
		log.Printf("ladder_run: config %s: failed to get cloud account %s: %v", dbCfg.ID, dbCfg.CloudAccountID, err)
		return outcomeErrored
	}
	if cloudAcct == nil {
		log.Printf("ladder_run: config %s: cloud account %s not found in DB", dbCfg.ID, dbCfg.CloudAccountID)
		return outcomeErrored
	}

	// Q1: skip configs that belong to a different AWS account.
	if cloudAcct.ExternalID != ownAccountID {
		log.Printf("ladder_run: config %s: cloud account external_id=%q != lambda account=%q: skipped (multi_account_unsupported)", dbCfg.ID, cloudAcct.ExternalID, ownAccountID)
		return outcomeSkippedMultiAccount
	}

	// Cadence self-gate: skip if a run already started within the window.
	if skip, reason := ladderWithinCadenceWindow(ctx, app.Config, dbCfg, now); skip {
		log.Printf("ladder_run: config %s: %s", dbCfg.ID, reason)
		return outcomeSkippedCadence
	}

	// Build the LadderCapability for this account.
	if app.LadderCapabilityFactory == nil {
		log.Printf("ladder_run: config %s: LadderCapabilityFactory is nil (not wired), erroring", dbCfg.ID)
		return outcomeErrored
	}
	cap, err := app.LadderCapabilityFactory(ctx, region, cloudAcct.ExternalID)
	if err != nil {
		log.Printf("ladder_run: config %s: failed to build ladder capability: %v", dbCfg.ID, err)
		return outcomeErrored
	}

	// Run the plan engine and persist the result.
	if err := app.executeLadderRun(ctx, dbCfg, cap, cloudAcct.ExternalID, term, paymentOpt, now); err != nil {
		log.Printf("ladder_run: config %s: planning failed: %v", dbCfg.ID, err)
		return outcomeErrored
	}
	return outcomePlanned
}

// executeLadderRun runs the planning engine for a single ladder_config and
// persists the result as a ladder_runs row (+ ladder_tranches audit rows).
//
// It is split from handleLadderRun so tests can inject a hermetic
// LadderCapability (fake) and a mock store without touching AWS or PostgreSQL.
func (app *Application) executeLadderRun(
	ctx context.Context,
	dbCfg *config.LadderConfigDB,
	cap pkgladder.LadderCapability,
	accountID string,
	term pkgladder.Term,
	paymentOpt pkgladder.PaymentOption,
	now time.Time,
) error {
	// Convert the DB config row to the engine's typed LadderConfig.
	engineCfg, err := ladderConfigToEngine(dbCfg, accountID)
	if err != nil {
		return fmt.Errorf("config conversion: %w", err)
	}
	scope := engineCfg.Scope

	// Collect read-side data. All three calls are independent; errors are
	// fail-loud (not silently defaulted to empty).
	baseline, err := cap.GetUsageBaseline(ctx, scope, engineCfg.LookbackDays, engineCfg.BaselinePercentile)
	if err != nil {
		return fmt.Errorf("GetUsageBaseline: %w", err)
	}

	layerStates, err := cap.GetLayerStates(ctx, scope)
	if err != nil {
		return fmt.Errorf("GetLayerStates: %w", err)
	}

	supportedLayers := cap.SupportedLayers()

	// Run Allocate.
	allocResult, err := pkgladder.Allocate(&pkgladder.AllocationInput{
		Now:         now,
		Baseline:    baseline,
		LayerStates: layerStates,
		Layers:      supportedLayers,
		Config:      engineCfg,
		DataSources: []string{"aws-ce"},
	})
	if err != nil {
		return fmt.Errorf("Allocate: %w", err)
	}

	// Run BuildTranches to produce the ramp schedule.
	runID := uuid.New().String()
	trancheResult, err := pkgladder.BuildTranches(&pkgladder.TrancheInput{
		Config:        &engineCfg,
		RunID:         runID,
		Term:          term,
		PaymentOption: paymentOpt,
		NewID:         func() string { return uuid.New().String() },
		Now:           now,
		Allocations:   allocResult.Allocations,
	})
	if err != nil {
		return fmt.Errorf("BuildTranches: %w", err)
	}

	// Assemble LadderPlan (Q5: interleaves purchases AND reshapes in one JSON).
	plan := assembleLadderPlan(scope, now, allocResult, layerStates, baseline, term, paymentOpt)
	if err := plan.Validate(); err != nil {
		return fmt.Errorf("LadderPlan.Validate: %w", err)
	}

	planJSON, err := marshalLadderPlan(plan)
	if err != nil {
		return fmt.Errorf("marshal plan: %w", err)
	}

	// Map *big.Rat monetary snapshot to *float64 (nil stays nil).
	// These are stored in the run row for dashboards; the authoritative
	// detail is in plan_json.
	cfgID := dbCfg.ID
	modeStr := dbCfg.Mode
	cadenceStr := dbCfg.Cadence
	runRow := &config.LadderRunDB{
		ID:                runID,
		ConfigID:          &cfgID,
		StartedAt:         now,
		Status:            pkgladder.RunStatusPlanned,
		Mode:              &modeStr,
		Cadence:           &cadenceStr,
		Plan:              planJSON,
		BaselineUSDHr:     baseline.LowWaterUSDPerHour,
		TargetUSDHr:       ratToFloat64Ptr(plan.TargetUSDPerHour),
		ExistingUSDHr:     ratToFloat64Ptr(plan.ExistingUSDPerHour),
		GapUSDHr:          ratToFloat64Ptr(plan.GapUSDPerHour),
		TotalHourlyCommit: allocTotalHourlyCommit(allocResult.Allocations),
	}

	// Persist the run row first; tranches reference the run ID.
	savedRun, err := app.Config.SaveLadderRun(ctx, runRow)
	if err != nil {
		return fmt.Errorf("SaveLadderRun: %w", err)
	}

	// Persist the tranche audit rows (status=scheduled; no firing in PR-2).
	trancheRows := buildTrancheDBRows(trancheResult.Tranches, savedRun.ID, &cfgID)
	if err := app.Config.SaveLadderTranches(ctx, trancheRows); err != nil {
		return fmt.Errorf("SaveLadderTranches: %w", err)
	}

	log.Printf("ladder_run: config %s: persisted run %s (status=%s, allocations=%d, tranches=%d, holds=%d)",
		dbCfg.ID, savedRun.ID, savedRun.Status,
		len(allocResult.Allocations), len(trancheResult.Tranches), len(allocResult.Holds))
	return nil
}

// ladderWithinCadenceWindow returns (true, reason) when a new run should be
// skipped because a recent run already covers the cadence window. It queries
// LatestLadderRunStartedAt for the config; on DB error it returns (false, "")
// so the run proceeds (fail-open on measurement failure is safer than
// silently skipping a scheduled run forever).
func ladderWithinCadenceWindow(ctx context.Context, store config.StoreInterface, dbCfg *config.LadderConfigDB, now time.Time) (bool, string) {
	latest, err := store.LatestLadderRunStartedAt(ctx, dbCfg.ID)
	if err != nil {
		log.Printf("ladder_run: config %s: LatestLadderRunStartedAt error (proceeding): %v", dbCfg.ID, err)
		return false, ""
	}
	if latest == nil {
		return false, "" // no previous run, always eligible
	}
	elapsed := now.Sub(*latest)
	switch dbCfg.Cadence {
	case string(pkgladder.CadenceDaily):
		if elapsed < cadenceDailyMin {
			return true, fmt.Sprintf("cadence=daily: last run %v ago (threshold %v), skipping", elapsed.Round(time.Minute), cadenceDailyMin)
		}
	case string(pkgladder.CadenceWeekly):
		if elapsed < cadenceWeeklyMin {
			return true, fmt.Sprintf("cadence=weekly: last run %v ago (threshold %v), skipping", elapsed.Round(time.Minute), cadenceWeeklyMin)
		}
	default:
		// Unknown cadence: log but do not block the run. The LadderConfig
		// validator will surface this as an error during Allocate.
		log.Printf("ladder_run: config %s: unknown cadence=%q, proceeding without cadence gate", dbCfg.ID, dbCfg.Cadence)
	}
	return false, ""
}

// ladderConfigToEngine converts a LadderConfigDB row to a pkg/ladder LadderConfig.
// It parses typed enums at the boundary and fails loud on any unknown value.
func ladderConfigToEngine(dbCfg *config.LadderConfigDB, accountID string) (pkgladder.LadderConfig, error) {
	mode, err := pkgladder.ParseLadderMode(dbCfg.Mode)
	if err != nil {
		return pkgladder.LadderConfig{}, fmt.Errorf("mode: %w", err)
	}
	cadence, err := pkgladder.ParseLadderCadence(dbCfg.Cadence)
	if err != nil {
		return pkgladder.LadderConfig{}, fmt.Errorf("cadence: %w", err)
	}

	var ramp pkgladder.RampSchedule
	if err := json.Unmarshal(dbCfg.RampSchedule, &ramp); err != nil {
		return pkgladder.LadderConfig{}, fmt.Errorf("ramp_schedule: %w", err)
	}

	return pkgladder.LadderConfig{
		Scope: pkgladder.Scope{
			Provider:  pkgcommon.ProviderAWS,
			AccountID: accountID,
		},
		Mode:                          mode,
		Cadence:                       cadence,
		Ramp:                          ramp,
		TargetCoveragePct:             dbCfg.TargetCoverage,
		BufferFraction:                dbCfg.BufferFraction,
		BaselinePercentile:            dbCfg.BaselinePercentile,
		LookbackDays:                  dbCfg.LookbackDays,
		MaxActionsPerRun:              dbCfg.MaxActionsPerRun,
		BufferUtilizationThresholdPct: dbCfg.BufferUtilizationThreshold,
		MaxHourlyCommitPerRun:         dbCfg.MaxHourlyCommitPerRun,
	}, nil
}

// assembleLadderPlan builds a LadderPlan from the Allocate result and the
// collected read-side data. Q5: Actions interleaves both purchases
// (Allocations) and Reshapes in one authoritative plan JSON. Term and
// PaymentOption are stamped on every ActionPurchase entry; they are required
// by PlannedAction.Validate for purchase actions.
func assembleLadderPlan(
	scope pkgladder.Scope,
	now time.Time,
	allocResult *pkgladder.AllocateResult,
	layerStates map[pkgladder.LayerType]pkgladder.LayerState,
	baseline pkgladder.UsageBaseline,
	term pkgladder.Term,
	paymentOpt pkgladder.PaymentOption,
) *pkgladder.LadderPlan {
	var actions []pkgladder.PlannedAction
	// Purchase allocations come first so approval-email ordering is stable.
	for _, alloc := range allocResult.Allocations {
		actions = append(actions, pkgladder.PlannedAction{
			Action:           pkgladder.ActionPurchase,
			Layer:            alloc.Layer,
			AmountUSDPerHour: alloc.GapUSDPerHour,
			Term:             term,
			PaymentOption:    paymentOpt,
			Rationale:        alloc.Rationale,
			DataSources:      alloc.DataSources,
		})
	}
	// Reshapes follow; they carry no amount (AmountUSDPerHour must be nil).
	actions = append(actions, allocResult.Reshapes...)
	// Holds are informational; append last.
	actions = append(actions, allocResult.Holds...)

	// Compute aggregate monetary fields from layer states.
	var existing, target, gap *big.Rat
	var existingTotal float64
	for _, ls := range layerStates {
		if ls.ExistingUSDPerHour != nil {
			existingTotal += *ls.ExistingUSDPerHour
		}
	}
	existing = ratFromFloat(existingTotal)

	if baseline.LowWaterUSDPerHour != nil {
		low := ratFromFloat(*baseline.LowWaterUSDPerHour)
		target = low // LadderConfig.TargetCoveragePct scaling is applied inside Allocate
		if existing != nil {
			gap = new(big.Rat).Sub(target, existing)
		}
	}

	return &pkgladder.LadderPlan{
		Scope:              scope,
		GeneratedAt:        now,
		TargetUSDPerHour:   target,
		ExistingUSDPerHour: existing,
		GapUSDPerHour:      gap,
		Actions:            actions,
		Baseline:           baseline,
	}
}

// ratFromFloat converts a float64 to *big.Rat. It returns nil when
// big.Rat.SetFloat64 returns nil (non-finite inputs like NaN or Inf).
// This mirrors the project-wide convention for converting float64 to exact
// rational arithmetic at the boundary.
func ratFromFloat(f float64) *big.Rat {
	r := new(big.Rat)
	if r.SetFloat64(f) == nil {
		return nil
	}
	return r
}

// ratToFloat64Ptr converts a *big.Rat to a *float64 suitable for storage in
// a nullable DB column. nil stays nil (never 0-coerced).
func ratToFloat64Ptr(r *big.Rat) *float64 {
	if r == nil {
		return nil
	}
	f, _ := r.Float64()
	return &f
}

// allocTotalHourlyCommit sums the GapUSDPerHour across all allocations,
// converting to float64. This is the non-nullable total_hourly_commit stored
// in the run row; it starts at 0 for a no-op (Hold-only) plan, which is the
// correct semantic (zero new commitment, not "not computed").
func allocTotalHourlyCommit(allocs []pkgladder.Allocation) float64 {
	sum := new(big.Rat)
	for _, a := range allocs {
		if a.GapUSDPerHour != nil {
			sum.Add(sum, a.GapUSDPerHour)
		}
	}
	f, _ := sum.Float64()
	return f
}

// buildTrancheDBRows converts pkg/ladder Tranche rows to config.LadderTrancheDB
// rows ready for SaveLadderTranches.
func buildTrancheDBRows(tranches []pkgladder.Tranche, runID string, configID *string) []config.LadderTrancheDB {
	rows := make([]config.LadderTrancheDB, 0, len(tranches))
	for _, tr := range tranches {
		// AmountUSDHr: parse the RatString back to float64. Fail-loud:
		// an empty or malformed RatString means BuildTranches produced a
		// broken tranche; that should never happen after Validate() passes.
		var amountUSDHr float64
		if r, ok := new(big.Rat).SetString(tr.AmountUSDPerHour); ok && r != nil {
			amountUSDHr, _ = r.Float64()
		}
		runIDCopy := runID
		rows = append(rows, config.LadderTrancheDB{
			ID:            tr.ID,
			ConfigID:      configID,
			RunID:         &runIDCopy,
			LayerType:     tr.Layer,
			Term:          tr.Term,
			PaymentOption: tr.PaymentOption,
			Status:        pkgladder.TrancheStatusScheduled,
			AmountUSDHr:   amountUSDHr,
			ScheduledDate: tr.FireAfter,
		})
	}
	return rows
}

// ladderPlanJSONAction is a JSON-serializable form of pkgladder.PlannedAction.
// PlannedAction.AmountUSDPerHour is *big.Rat (not JSON-friendly); this DTO
// converts it to *float64 at the boundary.
type ladderPlanJSONAction struct {
	Action        string   `json:"action"`
	Layer         string   `json:"layer"`
	AmountUSDHr   *float64 `json:"amount_usd_hr,omitempty"` // nil for Hold/Reshape
	Term          string   `json:"term,omitempty"`
	PaymentOption string   `json:"payment_option,omitempty"`
	Rationale     string   `json:"rationale"`
	DataSources   []string `json:"data_sources,omitempty"`
}

// ladderPlanJSONDTO is a JSON-serializable form of pkgladder.LadderPlan.
// The *big.Rat monetary fields are converted to *float64 so they round-trip
// cleanly through JSONB without binary encoding.
type ladderPlanJSONDTO struct {
	Scope         pkgladder.Scope         `json:"scope"`
	GeneratedAt   time.Time               `json:"generated_at"`
	TargetUSDHr   *float64                `json:"target_usd_hr,omitempty"`
	ExistingUSDHr *float64                `json:"existing_usd_hr,omitempty"`
	GapUSDHr      *float64                `json:"gap_usd_hr,omitempty"`
	Actions       []ladderPlanJSONAction  `json:"actions"`
	Baseline      pkgladder.UsageBaseline `json:"baseline"`
}

// marshalLadderPlan serializes a LadderPlan to JSON for storage in the
// plan JSONB column. *big.Rat fields are converted to *float64 at this
// boundary; nil stays nil.
func marshalLadderPlan(plan *pkgladder.LadderPlan) (json.RawMessage, error) {
	actions := make([]ladderPlanJSONAction, 0, len(plan.Actions))
	for _, a := range plan.Actions {
		action := ladderPlanJSONAction{
			Action:        string(a.Action),
			Layer:         string(a.Layer),
			AmountUSDHr:   ratToFloat64Ptr(a.AmountUSDPerHour),
			Term:          string(a.Term),
			PaymentOption: string(a.PaymentOption),
			Rationale:     a.Rationale,
			DataSources:   a.DataSources,
		}
		actions = append(actions, action)
	}
	dto := ladderPlanJSONDTO{
		Scope:         plan.Scope,
		GeneratedAt:   plan.GeneratedAt,
		TargetUSDHr:   ratToFloat64Ptr(plan.TargetUSDPerHour),
		ExistingUSDHr: ratToFloat64Ptr(plan.ExistingUSDPerHour),
		GapUSDHr:      ratToFloat64Ptr(plan.GapUSDPerHour),
		Actions:       actions,
		Baseline:      plan.Baseline,
	}
	b, err := json.Marshal(dto)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(b), nil
}

// ladderTermFromYears converts a GlobalConfig.DefaultTerm (integer years) to
// a typed pkgladder.Term. Fails loud on any unrecognized value so money-path
// decisions never proceed with an unknown commitment term.
func ladderTermFromYears(years int) (pkgladder.Term, error) {
	switch years {
	case 1:
		return pkgladder.Term1Year, nil
	case 3:
		return pkgladder.Term3Year, nil
	default:
		return "", fmt.Errorf("unsupported default_term=%d years (allowed: 1, 3)", years)
	}
}
