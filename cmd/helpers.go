package main

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/LeanerCloud/CUDly/pkg/common"
	"github.com/LeanerCloud/CUDly/pkg/provider"
	"github.com/LeanerCloud/CUDly/pkg/recfilter"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/organizations"
	"golang.org/x/term"
)

// Constants for purchase processing.
const (
	// DefaultDuplicateCheckLookbackHours is the default lookback period for checking recent purchases.
	DefaultDuplicateCheckLookbackHours = 24

	// PurchaseDelaySeconds is the delay between consecutive purchases to avoid rate limiting.
	PurchaseDelaySeconds = 2
)

// AppLogger is a simple logger for application output.
var AppLogger = log.New(os.Stdout, "", 0)

// OrganizationsAPI interface for describing accounts.
type OrganizationsAPI interface {
	DescribeAccount(ctx context.Context, params *organizations.DescribeAccountInput, optFns ...func(*organizations.Options)) (*organizations.DescribeAccountOutput, error)
}

// AccountAliasGetter is an interface for getting account aliases.
type AccountAliasGetter interface {
	GetAccountAlias(ctx context.Context, accountID string) string
}

// AccountAliasCache caches account ID to alias mappings.
type AccountAliasCache struct {
	orgClient OrganizationsAPI
	cache     map[string]string
	mu        sync.RWMutex
}

// NewAccountAliasCache creates a new account alias cache.
func NewAccountAliasCache(cfg aws.Config) *AccountAliasCache {
	return &AccountAliasCache{
		cache:     make(map[string]string),
		orgClient: organizations.NewFromConfig(cfg),
	}
}

// NewAccountAliasCacheWithClient creates a new account alias cache with a custom client
// This is useful for testing with mocked clients.
func NewAccountAliasCacheWithClient(orgClient OrganizationsAPI) *AccountAliasCache {
	return &AccountAliasCache{
		cache:     make(map[string]string),
		orgClient: orgClient,
	}
}

// GetAccountAlias returns the account alias for an account ID.
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

// CalculateTotalInstances calculates the total instance count across recommendations.
func CalculateTotalInstances(recs []common.Recommendation) int {
	total := 0
	for _rvc := range recs {
		rec := recs[_rvc]
		total += rec.Count
	}
	return total
}

// ApplyCoverage delegates to recfilter.ApplyCoverage, wiring AppLogger as the
// logging sink. Substantive documentation lives on recfilter.ApplyCoverage.
func ApplyCoverage(recs []common.Recommendation, coverage float64) []common.Recommendation {
	return recfilter.ApplyCoverage(recs, coverage, AppLogger.Printf, nil)
}

// applyCoverage delegates to recfilter.ApplyCoverage, wiring AppLogger as the
// logging sink. Substantive documentation lives on recfilter.ApplyCoverage.
func applyCoverage(recs []common.Recommendation, coverage float64, drops *common.DropSummary) []common.Recommendation {
	return recfilter.ApplyCoverage(recs, coverage, AppLogger.Printf, drops)
}

// ApplyTargetCoverage delegates to recfilter.ApplyTargetCoverage, wiring
// AppLogger as the logging sink. Substantive documentation (the RI/SP sizing
// formulas and the #338 flag-name history) lives on recfilter.ApplyTargetCoverage.
func ApplyTargetCoverage(recs []common.Recommendation, targetPct float64, drops *common.DropSummary) []common.Recommendation {
	return recfilter.ApplyTargetCoverage(recs, targetPct, AppLogger.Printf, drops)
}

// applySizing chooses target-coverage or coverage sizing.
//
// coverage is the effective % to apply when target-coverage is unset
// (the main path passes cfg.Coverage; the CSV path passes csvModeCoverage,
// which substitutes the default 80% with 100% so CSV-driven counts aren't
// silently dropped).
//
// drops accumulates per-reason drop counts for the end-of-run summary.
// Pass nil to skip tracking.
func applySizing(recs []common.Recommendation, cfg Config, coverage float64, drops *common.DropSummary) []common.Recommendation {
	if cfg.TargetCoverage > 0 {
		return ApplyTargetCoverage(recs, cfg.TargetCoverage, drops)
	}
	return applyCoverage(recs, coverage, drops)
}

// ApplyInstanceLimit truncates recs so their total Count does not exceed
// maxInstances. It is a single-shot cap over whatever slice it is handed: the
// caller is responsible for handing it the complete run-wide set, because
// applying it to a subset (one service, one region) caps that subset only and
// multiplies the effective cap by the number of subsets. See
// applyGlobalInstanceLimit in multi_service.go for the run-wide call site.
//
// Recommendations are consumed in slice order, so the caller controls which
// ones survive by ordering the slice (the main path caps the scorer's
// savings-sorted output, keeping the highest-value commitments).
//
// A truncated recommendation has its extensive money fields scaled by the
// discrete count ratio, like every other sizing path (see
// common.ScaleRecommendationCosts). EstimatedSavings and friends are
// whole-row totals for the count the provider proposed, so cutting Count
// alone leaves the row claiming the savings of instances the run will not
// buy. The run summary and the purchase report then overstate the benefit
// of a capped run, which is the wrong direction to be wrong in on a money
// path (#1830).
func ApplyInstanceLimit(recs []common.Recommendation, maxInstances int32) []common.Recommendation {
	if maxInstances <= 0 {
		return recs
	}

	result := make([]common.Recommendation, 0)
	remaining := int(maxInstances)

	for _rvc := range recs {
		rec := recs[_rvc]
		if remaining <= 0 {
			break
		}
		adjusted := rec
		// rec.Count > remaining and remaining >= 1 together imply rec.Count
		// >= 2, so the denominator is always positive here. A non-positive
		// Count can never enter this branch and so is never rescaled: it
		// buys nothing, there is nothing to scale down to, and a zero or
		// negative denominator would produce NaN or a sign flip.
		if rec.Count > remaining {
			adjusted = common.ScaleRecommendationCosts(rec, float64(remaining)/float64(rec.Count))
			adjusted.Count = remaining
		}
		result = append(result, adjusted)
		// Only a positive Count consumes budget. Subtracting a non-positive
		// Count would credit budget back and let later recommendations push
		// the run past the cap.
		if adjusted.Count > 0 {
			remaining -= adjusted.Count
		}
	}
	return result
}

// ConfirmPurchase asks the user for confirmation before proceeding.
// totalSavings is the estimated monthly savings from the purchase (not the purchase cost),
// matching the EstimatedSavings column and the "Estimated monthly savings" summary.
// Returns false without prompting if stdin is not a TTY and skipConfirmation is false.
func ConfirmPurchase(totalInstances int, totalSavings float64, skipConfirmation bool) bool {
	if skipConfirmation {
		return true
	}

	if !term.IsTerminal(int(os.Stdin.Fd())) { //nolint:gosec // G115: uintptr->int for file descriptor; FD values are always small positive integers
		log.Printf("stdin is not a terminal and --yes was not set; skipping purchase")
		return false
	}

	fmt.Printf("\n⚠️  About to purchase %d instances with estimated monthly savings: $%.2f\n", totalInstances, totalSavings)
	fmt.Print("Do you want to proceed? (yes/no): ")

	reader := bufio.NewReader(os.Stdin)
	response, err := reader.ReadString('\n')
	if err != nil {
		return false
	}

	response = strings.TrimSpace(strings.ToLower(response))
	return response == "yes" || response == "y"
}

// CheckAuditLogWritable reports whether the audit log at path can be appended to.
// Thin wrapper over common.CheckAuditLogWritable; kept so cmd's existing call sites
// and tests are unchanged.
func CheckAuditLogWritable(path string) error { return common.CheckAuditLogWritable(path) }

// DuplicateChecker checks for existing commitments to avoid duplicates.
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
func (d *DuplicateChecker) AdjustRecommendationsForExisting(ctx context.Context, recs []common.Recommendation, client provider.ServiceClient) (passed, filtered []common.Recommendation, err error) {
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

	passed, filtered = adjustRecommendationsAgainstExisting(recs, existingMap)

	if len(filtered) > 0 {
		log.Printf("    [DuplicateChecker] Result: %d recommendations kept out of %d (avoided %d duplicates)",
			len(passed), len(recs), len(filtered))
	}
	return passed, filtered, nil
}

// filterRecentCommitments filters commitments to only recent purchases within the lookback window.
func (d *DuplicateChecker) filterRecentCommitments(existing []common.Commitment) []common.Commitment {
	cutoffTime := time.Now().Add(-time.Duration(d.LookbackHours) * time.Hour)
	recentExisting := make([]common.Commitment, 0)

	for _rvc := range existing {
		c := existing[_rvc]
		if isRecentActiveCommitment(c, cutoffTime) {
			recentExisting = append(recentExisting, c)
		}
	}

	return recentExisting
}

// isRecentActiveCommitment checks if a commitment is active and purchased after the cutoff time.
func isRecentActiveCommitment(c common.Commitment, cutoffTime time.Time) bool {
	return (c.State == "active" || c.State == "payment-pending") && c.StartDate.After(cutoffTime)
}

// buildExistingCommitmentsMap builds a map of commitments by resource type, region, and engine.
func buildExistingCommitmentsMap(commitments []common.Commitment) map[string]int {
	existingMap := make(map[string]int)

	for _rvc := range commitments {
		c := commitments[_rvc]
		normalizedEngine := common.NormalizeEngineName(c.Engine)
		key := fmt.Sprintf("%s|%s|%s", c.ResourceType, c.Region, normalizedEngine)
		existingMap[key] += c.Count
		log.Printf("    [DuplicateChecker] Recent RI: key=%s count=%d startDate=%s (raw engine=%s)",
			key, c.Count, c.StartDate.Format("2006-01-02 15:04:05"), c.Engine)
	}

	return existingMap
}

// adjustRecommendationsAgainstExisting adjusts recommendations based on existing commitments.
// Returns (passed, filtered) where filtered contains recs whose count was reduced to zero.
func adjustRecommendationsAgainstExisting(recs []common.Recommendation, existingMap map[string]int) (passed, filtered []common.Recommendation) {
	passed = make([]common.Recommendation, 0, len(recs))
	filtered = make([]common.Recommendation, 0)

	for _rvc := range recs {
		rec := recs[_rvc]
		adjusted := adjustSingleRecommendation(rec, existingMap)
		if adjusted.Count > 0 {
			passed = append(passed, adjusted)
		} else {
			filtered = append(filtered, rec)
		}
	}

	return passed, filtered
}

// adjustSingleRecommendation adjusts a single recommendation based on existing commitments.
func adjustSingleRecommendation(rec common.Recommendation, existingMap map[string]int) common.Recommendation {
	engine := common.EngineFromDetails(rec.Details)
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

// AdjustRecommendationsForExistingRIs is an alias for AdjustRecommendationsForExisting.
func (d *DuplicateChecker) AdjustRecommendationsForExistingRIs(ctx context.Context, recs []common.Recommendation, client provider.ServiceClient) (passed, filtered []common.Recommendation, err error) {
	return d.AdjustRecommendationsForExisting(ctx, recs, client)
}

// GetRecommendationDescription returns a human-readable description.
func GetRecommendationDescription(rec common.Recommendation) string {
	desc := fmt.Sprintf("%s %s", rec.Service, rec.ResourceType)
	if rec.Details != nil {
		desc += " " + rec.Details.GetDetailDescription()
	}
	return desc
}
