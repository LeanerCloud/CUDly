package main

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"math"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/LeanerCloud/CUDly/pkg/common"
	"github.com/LeanerCloud/CUDly/pkg/provider"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/organizations"
	"golang.org/x/term"
)

// Constants for purchase processing
const (
	// DefaultDuplicateCheckLookbackHours is the default lookback period for checking recent purchases
	DefaultDuplicateCheckLookbackHours = 24

	// PurchaseDelaySeconds is the delay between consecutive purchases to avoid rate limiting
	PurchaseDelaySeconds = 2
)

// AppLogger is a simple logger for application output
var AppLogger = log.New(os.Stdout, "", 0)

// OrganizationsAPI interface for describing accounts
type OrganizationsAPI interface {
	DescribeAccount(ctx context.Context, params *organizations.DescribeAccountInput, optFns ...func(*organizations.Options)) (*organizations.DescribeAccountOutput, error)
}

// AccountAliasGetter is an interface for getting account aliases
type AccountAliasGetter interface {
	GetAccountAlias(ctx context.Context, accountID string) string
}

// AccountAliasCache caches account ID to alias mappings
type AccountAliasCache struct {
	mu        sync.RWMutex
	cache     map[string]string
	orgClient OrganizationsAPI
}

// NewAccountAliasCache creates a new account alias cache
func NewAccountAliasCache(cfg aws.Config) *AccountAliasCache {
	return &AccountAliasCache{
		cache:     make(map[string]string),
		orgClient: organizations.NewFromConfig(cfg),
	}
}

// NewAccountAliasCacheWithClient creates a new account alias cache with a custom client
// This is useful for testing with mocked clients
func NewAccountAliasCacheWithClient(orgClient OrganizationsAPI) *AccountAliasCache {
	return &AccountAliasCache{
		cache:     make(map[string]string),
		orgClient: orgClient,
	}
}

// GetAccountAlias returns the account alias for an account ID
func (c *AccountAliasCache) GetAccountAlias(ctx context.Context, accountID string) string {
	if accountID == "" {
		return ""
	}

	c.mu.RLock()
	if alias, ok := c.cache[accountID]; ok {
		c.mu.RUnlock()
		return alias
	}
	c.mu.RUnlock()

	// Try to fetch from Organizations
	c.mu.Lock()
	defer c.mu.Unlock()

	// Double-check after acquiring write lock
	if alias, ok := c.cache[accountID]; ok {
		return alias
	}

	// Try to describe the account
	result, err := c.orgClient.DescribeAccount(ctx, &organizations.DescribeAccountInput{
		AccountId: aws.String(accountID),
	})
	if err != nil {
		c.cache[accountID] = accountID // Use ID as fallback
		return accountID
	}

	if result.Account != nil && result.Account.Name != nil {
		c.cache[accountID] = *result.Account.Name
		return *result.Account.Name
	}

	c.cache[accountID] = accountID
	return accountID
}

// CalculateTotalInstances calculates the total instance count across recommendations
func CalculateTotalInstances(recs []common.Recommendation) int {
	total := 0
	for _, rec := range recs {
		total += rec.Count
	}
	return total
}

// ApplyCoverage applies coverage percentage to recommendations
func ApplyCoverage(recs []common.Recommendation, coverage float64) []common.Recommendation {
	if coverage >= 100 {
		return recs
	}
	if coverage <= 0 {
		return []common.Recommendation{}
	}

	// Apply coverage by reducing counts (for RIs) or hourly commitment (for Savings Plans)
	result := make([]common.Recommendation, 0, len(recs))
	for _, rec := range recs {
		adjusted := rec

		// For Savings Plans, reduce the hourly commitment instead of count.
		// If the type assertion fails (defensive — Details should always
		// be *SavingsPlanDetails for SP recs), preserve the recommendation
		// at its original values rather than silently dropping it. A
		// missing-Details record is a logged anomaly, not a reason to
		// erase coverage from the run.
		if common.IsSavingsPlan(rec.Service) {
			if details, ok := rec.Details.(*common.SavingsPlanDetails); ok {
				newDetails := *details // Copy the struct
				newDetails.HourlyCommitment = newDetails.HourlyCommitment * coverage / 100
				adjusted.Details = &newDetails
				// Also adjust the estimated savings proportionally
				adjusted.EstimatedSavings = rec.EstimatedSavings * coverage / 100
			} else {
				AppLogger.Printf("WARNING: SP recommendation for service %q has unexpected Details type %T; passing through unscaled\n", rec.Service, rec.Details)
			}
			result = append(result, adjusted)
			continue
		}

		// For RIs, reduce the count
		newCount := int(float64(rec.Count) * coverage / 100)
		if newCount > 0 {
			adjusted.Count = newCount
			result = append(result, adjusted)
		}
	}
	return result
}

// ApplyTargetUtilization sizes RI/SP recommendations so projected post-purchase
// utilization stays >= targetPct. See ApplyCoverage for the alternative
// coverage-based sizing; the two are dispatched via applySizing.
//
// Semantics inversion vs. intuition: higher targetPct means fewer commitments
// bought (smaller waste ceiling), not more. The math is "utilization =
// used/bought", so raising the target reduces the bought side.
//
// RIs:
//
//	n_target = floor(AverageInstancesUsedPerHour / (targetPct/100))
//	capped at rec.Count (we never exceed AWS's recommended ceiling).
//	If n_target == 0 → drop with explanatory INFO log (target unreachable).
//	If AverageInstancesUsedPerHour <= 0 → pass through (no signal); counted
//	in the per-run skip summary.
//
// SPs:
//
//	If RecommendedUtilization >= targetPct → no scaling (AWS already projects
//	above target).
//	Else ratio = RecommendedUtilization / targetPct (always < 1). Scale
//	SavingsPlanDetails.HourlyCommitment and rec.EstimatedSavings — matching
//	exactly the fields ApplyCoverage scales on the SP branch, so the two
//	sizing modes stay structurally consistent.
//	If RecommendedUtilization <= 0 → pass through; counted in skip summary.
//
// Recs of any other CommitmentType are passed through unmodified (warned
// once per type per run).
func ApplyTargetUtilization(recs []common.Recommendation, targetPct float64) []common.Recommendation {
	if targetPct <= 0 || targetPct > 100 {
		// Validation ensures we never get here in production, but be defensive
		// so a buggy caller doesn't divide by zero.
		AppLogger.Printf("WARNING: ApplyTargetUtilization called with targetPct=%.2f outside (0,100]; returning recs unchanged\n", targetPct)
		return recs
	}

	target := targetPct / 100.0
	result := make([]common.Recommendation, 0, len(recs))
	var skipped int
	unsupportedSeen := make(map[common.CommitmentType]bool)

	for _, rec := range recs {
		adjusted, kept, missingSignal := applyTargetUtilizationOne(rec, target, targetPct, unsupportedSeen)
		if missingSignal {
			skipped++
		}
		if kept {
			result = append(result, adjusted)
		}
	}

	if skipped > 0 {
		AppLogger.Printf("INFO: --target-utilization=%.1f%% skipped %d of %d recommendations with no utilization signal (passed through unchanged)\n",
			targetPct, skipped, len(recs))
	}

	return result
}

// applyTargetUtilizationOne dispatches a single recommendation through the
// appropriate branch. Returns (rec, kept, missingSignal):
//   - kept=true → caller appends `rec` (the adjusted or pass-through value).
//   - kept=false → caller drops the rec (only the RI "target unreachable"
//     branch returns this; an INFO log already fired).
//   - missingSignal=true → counted toward the end-of-run skip summary.
//
// Split out of ApplyTargetUtilization to keep that function under gocyclo's
// complexity threshold.
func applyTargetUtilizationOne(rec common.Recommendation, target, targetPct float64, unsupportedSeen map[common.CommitmentType]bool) (common.Recommendation, bool, bool) {
	switch {
	case common.IsSavingsPlan(rec.Service):
		adjusted, ok := applyTargetUtilizationSP(rec, targetPct)
		if !ok {
			// SP no-signal: pass through unchanged.
			return rec, true, true
		}
		return adjusted, true, false
	case rec.CommitmentType == common.CommitmentReservedInstance:
		adjusted, ok := applyTargetUtilizationRI(rec, target, targetPct)
		if !ok {
			// Distinguish "no signal" (pass through, count in summary) from
			// "target unreachable" (drop with already-fired INFO log).
			if rec.AverageInstancesUsedPerHour <= 0 {
				return rec, true, true
			}
			return rec, false, false
		}
		return adjusted, true, false
	default:
		if !unsupportedSeen[rec.CommitmentType] {
			AppLogger.Printf("WARNING: --target-utilization not supported for CommitmentType=%q; passing recommendations through unchanged\n", rec.CommitmentType)
			unsupportedSeen[rec.CommitmentType] = true
		}
		return rec, true, false
	}
}

// applyTargetUtilizationRI is the RI branch of ApplyTargetUtilization. Returns
// (adjusted, true) on success, (rec, false) when the rec should be passed
// through unscaled (no signal) or dropped (target unreachable). Caller
// distinguishes the two via rec.AverageInstancesUsedPerHour.
func applyTargetUtilizationRI(rec common.Recommendation, target, targetPct float64) (common.Recommendation, bool) {
	if rec.AverageInstancesUsedPerHour <= 0 {
		// No signal — caller will pass through and count in the summary.
		return rec, false
	}

	avg := rec.AverageInstancesUsedPerHour
	uncappedTarget := int(math.Floor(avg / target))

	// Cap at rec.Count: never buy more than AWS recommended.
	nTarget := uncappedTarget
	capped := false
	if nTarget > rec.Count {
		nTarget = rec.Count
		capped = true
	}

	if nTarget == 0 {
		// Target unreachable: even buying 1 instance would over-utilize beyond
		// the user's target. Drop with explanatory log per issue #338 AC.
		AppLogger.Printf("INFO: --target-utilization=%.1f%% unreachable for %s/%s/%s (avg used %.2f/hr requires <1 instance for target) — dropped recommendation\n",
			targetPct, rec.Service, rec.Region, rec.ResourceType, avg)
		// Returning (_, false) with avg > 0 signals "drop, don't pass through".
		// applyTargetUtilizationRI's caller branches on
		// rec.AverageInstancesUsedPerHour to distinguish drop vs no-signal.
		return rec, false
	}

	if capped {
		// Target was too lenient — AWS's ceiling binds before we reach the
		// target's "buy this many" suggestion.
		AppLogger.Printf("INFO: --target-utilization=%.1f%% would have recommended %d instances for %s/%s/%s but capped at AWS ceiling of %d\n",
			targetPct, uncappedTarget, rec.Service, rec.Region, rec.ResourceType, nTarget)
	}

	adjusted := rec
	adjusted.Count = nTarget

	// Cost-scaling — mirror ApplyCoverage's RI branch (only Count is scaled
	// implicitly via the new count; ApplyCoverage doesn't scale RI cost
	// fields explicitly either, so we don't here).

	// Projection metrics.
	projUtil := avg / float64(nTarget) * 100.0
	if projUtil > 100 {
		projUtil = 100
	}
	projCov := float64(nTarget) / avg * 100.0
	if projCov > 100 {
		projCov = 100
	}
	adjusted.ProjectedUtilization = projUtil
	adjusted.ProjectedCoverage = projCov
	return adjusted, true
}

// applyTargetUtilizationSP is the SP branch of ApplyTargetUtilization. Returns
// (adjusted, true) when the rec is kept, (rec, false) when it should be
// skipped (caller passes through unscaled and counts in the skip summary).
func applyTargetUtilizationSP(rec common.Recommendation, targetPct float64) (common.Recommendation, bool) {
	if rec.RecommendedUtilization <= 0 {
		return rec, false
	}

	adjusted := rec
	if rec.RecommendedUtilization >= targetPct {
		// AWS already projects at-or-above target — no scaling, but we do
		// surface the projected utilization in the output.
		adjusted.ProjectedUtilization = rec.RecommendedUtilization
		// ProjectedCoverage is intentionally left at zero for SPs — see
		// the package doc on the field; we don't have total-demand-$ from
		// CE to compute SP coverage cleanly.
		return adjusted, true
	}

	// AWS projects below target. Scale commitment down by ratio = recommended/target
	// so projected utilization rises to exactly target.
	ratio := rec.RecommendedUtilization / targetPct

	// Only the SP fields that ApplyCoverage scales — HourlyCommitment (via
	// the polymorphic Details copy) and EstimatedSavings. Do NOT touch
	// CommitmentCost, OnDemandCost, or SavingsPercentage; ApplyCoverage
	// doesn't either, and divergence would silently desync the two modes.
	if details, ok := rec.Details.(*common.SavingsPlanDetails); ok {
		newDetails := *details // copy
		newDetails.HourlyCommitment = newDetails.HourlyCommitment * ratio
		adjusted.Details = &newDetails
		adjusted.EstimatedSavings = rec.EstimatedSavings * ratio
	} else {
		AppLogger.Printf("WARNING: SP recommendation for service %q has unexpected Details type %T; passing through unscaled\n", rec.Service, rec.Details)
	}

	adjusted.ProjectedUtilization = targetPct
	// ProjectedCoverage left at zero for SPs (see above).
	return adjusted, true
}

// applySizing chooses target-utilization or coverage sizing.
//
// coverage is the effective % to apply when target-utilization is unset
// (the main path passes cfg.Coverage; the CSV path passes csvModeCoverage,
// which substitutes the default 80% with 100% so CSV-driven counts aren't
// silently dropped).
func applySizing(recs []common.Recommendation, cfg Config, coverage float64) []common.Recommendation {
	if cfg.TargetUtilization > 0 {
		return ApplyTargetUtilization(recs, cfg.TargetUtilization)
	}
	return ApplyCoverage(recs, coverage)
}

// ApplyCountOverride overrides the count for all recommendations
func ApplyCountOverride(recs []common.Recommendation, overrideCount int32) []common.Recommendation {
	if overrideCount <= 0 {
		return recs
	}
	result := make([]common.Recommendation, len(recs))
	for i, rec := range recs {
		result[i] = rec
		result[i].Count = int(overrideCount)
	}
	return result
}

// ApplyInstanceLimit limits the total number of instances
func ApplyInstanceLimit(recs []common.Recommendation, maxInstances int32) []common.Recommendation {
	if maxInstances <= 0 {
		return recs
	}

	result := make([]common.Recommendation, 0)
	remaining := int(maxInstances)

	for _, rec := range recs {
		if remaining <= 0 {
			break
		}
		adjusted := rec
		if rec.Count > remaining {
			adjusted.Count = remaining
		}
		result = append(result, adjusted)
		remaining -= adjusted.Count
	}
	return result
}

// ConfirmPurchase asks the user for confirmation before proceeding.
// totalSavings is the estimated annual savings from the purchase (not the purchase cost).
// Returns false without prompting if stdin is not a TTY and skipConfirmation is false.
func ConfirmPurchase(totalInstances int, totalSavings float64, skipConfirmation bool) bool {
	if skipConfirmation {
		return true
	}

	if !term.IsTerminal(int(os.Stdin.Fd())) {
		log.Printf("stdin is not a terminal and --yes was not set; skipping purchase")
		return false
	}

	fmt.Printf("\n⚠️  About to purchase %d instances with estimated annual savings: $%.2f\n", totalInstances, totalSavings)
	fmt.Print("Do you want to proceed? (yes/no): ")

	reader := bufio.NewReader(os.Stdin)
	response, err := reader.ReadString('\n')
	if err != nil {
		return false
	}

	response = strings.TrimSpace(strings.ToLower(response))
	return response == "yes" || response == "y"
}

// CheckAuditLogWritable opens the audit log file in append mode to verify it is writable.
// Returns an error if the path cannot be opened for writing.
func CheckAuditLogWritable(path string) error {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return fmt.Errorf("audit log %q not writable: %w", path, err)
	}
	return f.Close()
}

// DuplicateChecker checks for existing commitments to avoid duplicates
type DuplicateChecker struct {
	LookbackHours int // How many hours to look back for recent purchases
}

// NewDuplicateChecker creates a new duplicate checker. Pass 0 to use the default lookback period.
func NewDuplicateChecker(hours int) *DuplicateChecker {
	if hours <= 0 {
		hours = DefaultDuplicateCheckLookbackHours
	}
	return &DuplicateChecker{
		LookbackHours: hours,
	}
}

// AdjustRecommendationsForExisting adjusts recommendations based on existing commitments
// This checks for recently purchased RIs (within LookbackHours) to avoid duplicate purchases.
// Note: This is designed to prevent re-purchasing something you just bought, not to prevent
// purchasing RIs in other accounts that happen to have the same characteristics.
func (d *DuplicateChecker) AdjustRecommendationsForExisting(ctx context.Context, recs []common.Recommendation, client provider.ServiceClient) ([]common.Recommendation, []common.Recommendation, error) {
	existing, err := client.GetExistingCommitments(ctx)
	if err != nil {
		return recs, nil, err
	}

	log.Printf("    [DuplicateChecker] Found %d total existing commitments", len(existing))

	recentExisting := d.filterRecentCommitments(existing)
	log.Printf("    [DuplicateChecker] Found %d recent commitments (purchased in last %d hours)", len(recentExisting), d.LookbackHours)

	if len(recentExisting) == 0 {
		return recs, nil, nil
	}

	existingMap := buildExistingCommitmentsMap(recentExisting)
	log.Printf("    [DuplicateChecker] Existing map has %d unique keys", len(existingMap))

	passed, filtered := adjustRecommendationsAgainstExisting(recs, existingMap)

	if len(filtered) > 0 {
		log.Printf("    [DuplicateChecker] Result: %d recommendations kept out of %d (avoided %d duplicates)",
			len(passed), len(recs), len(filtered))
	}
	return passed, filtered, nil
}

// filterRecentCommitments filters commitments to only recent purchases within the lookback window
func (d *DuplicateChecker) filterRecentCommitments(existing []common.Commitment) []common.Commitment {
	cutoffTime := time.Now().Add(-time.Duration(d.LookbackHours) * time.Hour)
	recentExisting := make([]common.Commitment, 0)

	for _, c := range existing {
		if isRecentActiveCommitment(c, cutoffTime) {
			recentExisting = append(recentExisting, c)
		}
	}

	return recentExisting
}

// isRecentActiveCommitment checks if a commitment is active and purchased after the cutoff time
func isRecentActiveCommitment(c common.Commitment, cutoffTime time.Time) bool {
	return (c.State == "active" || c.State == "payment-pending") && c.StartDate.After(cutoffTime)
}

// buildExistingCommitmentsMap builds a map of commitments by resource type, region, and engine
func buildExistingCommitmentsMap(commitments []common.Commitment) map[string]int {
	existingMap := make(map[string]int)

	for _, c := range commitments {
		normalizedEngine := normalizeEngineName(c.Engine)
		key := fmt.Sprintf("%s|%s|%s", c.ResourceType, c.Region, normalizedEngine)
		existingMap[key] += c.Count
		log.Printf("    [DuplicateChecker] Recent RI: key=%s count=%d startDate=%s (raw engine=%s)",
			key, c.Count, c.StartDate.Format("2006-01-02 15:04:05"), c.Engine)
	}

	return existingMap
}

// adjustRecommendationsAgainstExisting adjusts recommendations based on existing commitments.
// Returns (passed, filtered) where filtered contains recs whose count was reduced to zero.
func adjustRecommendationsAgainstExisting(recs []common.Recommendation, existingMap map[string]int) ([]common.Recommendation, []common.Recommendation) {
	passed := make([]common.Recommendation, 0, len(recs))
	filtered := make([]common.Recommendation, 0)

	for _, rec := range recs {
		adjusted := adjustSingleRecommendation(rec, existingMap)
		if adjusted.Count > 0 {
			passed = append(passed, adjusted)
		} else {
			filtered = append(filtered, rec)
		}
	}

	return passed, filtered
}

// adjustSingleRecommendation adjusts a single recommendation based on existing commitments
func adjustSingleRecommendation(rec common.Recommendation, existingMap map[string]int) common.Recommendation {
	engine := getEngineFromRecommendation(rec)
	key := fmt.Sprintf("%s|%s|%s", rec.ResourceType, rec.Region, engine)
	existingCount := existingMap[key]

	if existingCount >= rec.Count {
		// All of this recommendation is covered by recent RIs.
		// Return a zero-value Recommendation (Count=0) as a sentinel; the caller
		// (adjustRecommendationsAgainstExisting) filters out recommendations with Count <= 0.
		log.Printf("    [DuplicateChecker] SKIP %s: recent %d >= recommended %d", key, existingCount, rec.Count)
		existingMap[key] -= rec.Count
		return common.Recommendation{Count: 0}
	}

	// Partial or no coverage by recent RIs
	adjusted := rec
	if existingCount > 0 {
		adjusted.Count = rec.Count - existingCount
		existingMap[key] = 0
		log.Printf("    [DuplicateChecker] PARTIAL %s: adjusted count from %d to %d", key, rec.Count, adjusted.Count)
	}

	return adjusted
}

// getEngineFromRecommendation extracts the engine from recommendation details
func getEngineFromRecommendation(rec common.Recommendation) string {
	if rec.Details == nil {
		return ""
	}
	var engine string
	switch details := rec.Details.(type) {
	case common.DatabaseDetails:
		engine = details.Engine
	case *common.DatabaseDetails:
		engine = details.Engine
	case common.CacheDetails:
		engine = details.Engine
	case *common.CacheDetails:
		engine = details.Engine
	default:
		return ""
	}
	return normalizeEngineName(engine)
}

// engineNameMap maps database engine names to a consistent normalized format.
// AWS RIs use: "aurora-postgresql", "aurora-mysql", "mysql", "postgres"
// Cost Explorer uses: "Aurora PostgreSQL", "Aurora MySQL", "MySQL", "PostgreSQL"
var engineNameMap = map[string]string{
	// Cost Explorer format -> normalized
	"Aurora PostgreSQL": "aurora-postgresql",
	"Aurora MySQL":      "aurora-mysql",
	"MySQL":             "mysql",
	"PostgreSQL":        "postgresql",
	"MariaDB":           "mariadb",
	"Oracle":            "oracle",
	"SQL Server":        "sqlserver",
	// Already normalized (from AWS RIs)
	"aurora-postgresql": "aurora-postgresql",
	"aurora-mysql":      "aurora-mysql",
	"mysql":             "mysql",
	"postgresql":        "postgresql",
	"postgres":          "postgresql",
	"mariadb":           "mariadb",
	"oracle-se":         "oracle",
	"oracle-se1":        "oracle",
	"oracle-se2":        "oracle",
	"oracle-ee":         "oracle",
	"sqlserver-se":      "sqlserver",
	"sqlserver-ee":      "sqlserver",
	"sqlserver-ex":      "sqlserver",
	"sqlserver-web":     "sqlserver",
}

// normalizeEngineName normalizes database engine names to a consistent format
func normalizeEngineName(engine string) string {
	if normalized, ok := engineNameMap[engine]; ok {
		return normalized
	}
	// Return lowercase as fallback
	return strings.ToLower(engine)
}

// AdjustRecommendationsForExistingRIs is an alias for AdjustRecommendationsForExisting
func (d *DuplicateChecker) AdjustRecommendationsForExistingRIs(ctx context.Context, recs []common.Recommendation, client provider.ServiceClient) ([]common.Recommendation, []common.Recommendation, error) {
	return d.AdjustRecommendationsForExisting(ctx, recs, client)
}

// GetRecommendationDescription returns a human-readable description
func GetRecommendationDescription(rec common.Recommendation) string {
	desc := fmt.Sprintf("%s %s", rec.Service, rec.ResourceType)
	if rec.Details != nil {
		desc += " " + rec.Details.GetDetailDescription()
	}
	return desc
}
