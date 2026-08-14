package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"github.com/LeanerCloud/CUDly/internal/reporter"
	"github.com/LeanerCloud/CUDly/pkg/common"
	"github.com/LeanerCloud/CUDly/pkg/provider"
	"github.com/LeanerCloud/CUDly/pkg/scorer"
	awsprovider "github.com/LeanerCloud/CUDly/providers/aws"
	"github.com/LeanerCloud/CUDly/providers/aws/recommendations"
	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/google/uuid"
)

// fetchExistingCoverage retrieves the existing-RI coverage map from Cost
// Explorer so --target-coverage sizing can subtract what's already owned in
// each pool. Best-effort: a transient CE failure logs a warning and returns
// an empty map, which the sizing path treats as "no signal" — recs sized
// without subtracting existing commitments. Skipping the fetch entirely
// when --target-coverage is not in play avoids the per-region CE charges
// for users on the --coverage path.
//
// Coverage is fetched per-region per-account so CE's org-wide aggregate
// doesn't bleed one account's coverage into another in multi-account orgs.
// Regions come from cfg.Regions if set, otherwise from EC2 DescribeRegions.
//
// The lookback window is cfg.CoverageLookbackDays (default 30, matching the
// CE UI default). Operators reconciling against the AWS console coverage
// report should match this value to the report's own time window.
func fetchExistingCoverage(ctx context.Context, awsCfg aws.Config, recClient provider.RecommendationsClient, cfg Config) recommendations.PoolCoverageMap {
	if cfg.TargetCoverage <= 0 {
		return nil
	}
	adapter, ok := recClient.(*awsprovider.RecommendationsClientAdapter)
	if !ok {
		// Non-AWS provider: feature not wired up. Sizing degenerates to
		// the no-existing-commitments path.
		return nil
	}
	lookbackDays := cfg.CoverageLookbackDays
	if lookbackDays <= 0 {
		lookbackDays = 30
	}
	regions := cfg.Regions
	if len(regions) == 0 {
		allRegions, err := getAllAWSRegions(ctx, awsCfg)
		if err != nil {
			AppLogger.Printf("  ⚠️  Could not list AWS regions for coverage fetch (%v); skipping existing-coverage subtraction\n", err)
			return nil
		}
		regions = allRegions
	}
	AppLogger.Printf("\n🔎 Fetching existing-RI coverage from Cost Explorer per-account across %d regions (lookback %d days)...\n", len(regions), lookbackDays)
	cov, err := adapter.GetRICoverageMap(ctx, lookbackDays, regions)
	if err != nil {
		AppLogger.Printf("  ⚠️  Could not fetch existing-RI coverage (%v); sizing will assume zero existing coverage\n", err)
		return nil
	}
	AppLogger.Printf("  ✅ Fetched coverage for %d (region, instance-type, engine, account) entries\n", len(cov))
	return cov
}

// shutdownRequested is set to true when SIGINT is received during a purchase run.
var shutdownRequested atomic.Bool

// effectiveDryRun reports whether the run must stay in dry-run mode. A run is
// dry-run unless the user opts into real purchases with --purchase; that single
// flag is the only control. It defaults to false, so a bare invocation is a
// dry run and moving money is always an explicit opt-in. Both the non-CSV and
// CSV code paths use this helper so the guard is consistent and defined in one
// place.
func effectiveDryRun(cfg Config) bool {
	return !cfg.ActualPurchase
}

// runToolMultiService is the main entry point for processing multiple services.
// It runs a two-phase pipeline: (1) fetch+filter all recommendations, then
// (2) score, display, confirm, and purchase.
func runToolMultiService(ctx context.Context, cfg Config) {
	if cfg.CSVInput != "" {
		runCSVPathOrFatal(ctx, cfg)
		return
	}

	servicesToProcess := determineServicesToProcess(cfg)
	if len(servicesToProcess) == 0 {
		log.Fatalf("No valid services specified")
	}

	isDryRun := effectiveDryRun(cfg)

	// Register SIGINT handler so a running purchase loop can be interrupted cleanly.
	shutdownRequested.Store(false)
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt)
	go func() { <-sigCh; shutdownRequested.Store(true) }()
	defer signal.Stop(sigCh)

	// Verify audit log is writable before making any cloud API calls.
	if err := CheckAuditLogWritable(cfg.AuditLog); err != nil {
		log.Fatalf("Cannot write audit log: %v", err) //nolint:gocritic // exitAfterDefer: intentional startup fatal before cleanup matters
	}

	printRunMode(isDryRun)
	AppLogger.Printf("📊 Processing services: %s\n", formatServices(servicesToProcess))
	printPaymentAndTerm(cfg)

	awsCfg, err := loadAWSConfig(ctx, cfg)
	if err != nil {
		log.Fatalf("Failed to load AWS config: %v", err)
	}

	accountCache := NewAccountAliasCache(awsCfg)
	recClient := awsprovider.NewRecommendationsClient(awsCfg)
	if adapter, ok := recClient.(*awsprovider.RecommendationsClientAdapter); ok && cfg.RecLookbackPeriod != "" {
		adapter.SetRecLookbackPeriod(cfg.RecLookbackPeriod)
	}
	engineData := fetchEngineVersionData(ctx, cfg)

	// Fetch existing-RI coverage so --target-coverage can subtract what
	// the user already owns. Best-effort: a failure here logs a warning
	// and continues with an empty map, which makes sizing degenerate to
	// the no-existing-commitments path (matches behavior when no recs
	// are matched in the map).
	coverageMap := fetchExistingCoverage(ctx, awsCfg, recClient, cfg)

	// Phase 1: collect all recommendations without purchasing.
	AppLogger.Printf("\n📥 Fetching recommendations from all services...\n")
	allRecs, drops := fetchAllRecs(ctx, awsCfg, recClient, accountCache, servicesToProcess, engineData, cfg, coverageMap)

	// Phase 2: score, enforce the run-wide instance cap, and display.
	scoredResult := scoreLimitAndDisplay(allRecs, cfg, drops)
	if len(scoredResult.Passed) == 0 {
		printDropSummary(drops)
		AppLogger.Printf("\nℹ️  No recommendations passed filters. Nothing to purchase.\n")
		return
	}

	// Phases 3-4: confirm, purchase, and produce summary outputs.
	runPurchaseAndReport(ctx, awsCfg, scoredResult, isDryRun, cfg, drops)
}

// runPurchaseAndReport handles the confirm, execute, and report phases of
// the multi-service pipeline. It is a separate function to keep
// runToolMultiService within the cyclomatic-complexity limit.
func runPurchaseAndReport(ctx context.Context, awsCfg aws.Config, scoredResult scorer.ScoredResult, isDryRun bool, cfg Config, drops *common.DropSummary) {
	runID := uuid.New().String()
	if !isDryRun {
		totalInstances, totalSavings := sumPassedRecs(scoredResult.Passed)
		if !ConfirmPurchase(totalInstances, totalSavings, cfg.SkipConfirmation) {
			printDropSummary(drops)
			AppLogger.Printf("\n❌ Purchase canceled.\n")
			return
		}
	}

	allResults := executePurchasePipeline(ctx, awsCfg, scoredResult.Passed, isDryRun, runID, cfg)

	// Produce summary outputs.
	writeReportAndSummary(scoredResult.Passed, allResults, isDryRun, cfg, drops)
}

// writeReportAndSummary writes the CSV report and prints the final summary.
func writeReportAndSummary(passed []common.Recommendation, allResults []common.PurchaseResult, isDryRun bool, cfg Config, drops *common.DropSummary) {
	serviceStats := buildServiceStats(passed, allResults)
	finalCSVOutput := generateCSVFilename(isDryRun, cfg)
	if err := writeMultiServiceCSVReport(allResults, finalCSVOutput); err != nil {
		log.Printf("Warning: Failed to write CSV output: %v", err)
	} else {
		AppLogger.Printf("\n📋 CSV report written to: %s\n", finalCSVOutput)
	}
	printDropSummary(drops)
	printMultiServiceSummary(passed, allResults, serviceStats, isDryRun)
}

// printDropSummary writes the accumulated drop reasons at the terminal summary
// boundary. A nil or empty summary produces no output.
func printDropSummary(drops *common.DropSummary) {
	if drops == nil {
		return
	}
	if line := drops.FormatOneLine(); line != "" {
		AppLogger.Printf("\n%s\n", line)
	}
}

// loadAWSConfig builds an aws.Config from the tool config.
func loadAWSConfig(ctx context.Context, cfg Config) (aws.Config, error) {
	var opts []func(*awsconfig.LoadOptions) error
	opts = append(opts, awsconfig.WithRegion("us-east-1"))
	if cfg.Profile != "" {
		opts = append(opts, awsconfig.WithSharedConfigProfile(cfg.Profile))
	}
	return awsconfig.LoadDefaultConfig(ctx, opts...)
}

// scoreLimitAndDisplay runs the scorer on recs, enforces the run-wide
// --max-instances cap on the survivors, and prints the scored table and
// summary.
//
// The cap runs between scoring and rendering so the table, the confirmation
// prompt and the purchase loop all describe the same post-cap set, and so the
// instances that survive are the highest-savings ones run-wide (scorer.Score
// sorts Passed by savings percentage descending).
func scoreLimitAndDisplay(recs []common.Recommendation, cfg Config, drops *common.DropSummary) scorer.ScoredResult {
	scorerCfg := scorer.Config{
		MinSavingsPct:      cfg.MinSavingsPct,
		MaxBreakEvenMonths: cfg.MaxBreakEvenMonths,
		MinCount:           cfg.MinCount,
	}
	result := scorer.Score(recs, scorerCfg)
	result.Passed = applyGlobalInstanceLimit(result.Passed, cfg, rankBySavingsPercentage, drops)
	fmt.Print(reporter.RenderTable(result))
	fmt.Print(reporter.RenderExcluded(result))
	fmt.Print(reporter.RenderSummary(result))
	return result
}

// rankingRule names the ordering --max-instances consumes, so the cap can say
// which rule decided what survived. The two paths rank on different keys
// because they carry different data, and an operator reading the drop list has
// to know which one applied. Typed rather than a bare string so the call sites
// cannot invent a third wording that does not match any implemented ordering.
type rankingRule string

const (
	// rankBySavingsPercentage is the recommendation-driven path: scorer.Score
	// sorts on SavingsPercentage, an intensive rate, before the cap runs.
	rankBySavingsPercentage rankingRule = "highest savings-percentage recommendations first"
	// rankBySavingsPerInstance is the --input-csv path: a CSV row carries no
	// savings percentage, so sortBySavingsPerInstance derives the rate from
	// the EstimatedSavings and Count columns it does carry.
	rankBySavingsPerInstance rankingRule = "highest savings-per-instance rows first (a CSV row carries no savings percentage)"
)

// capBinds reports whether --max-instances will actually remove instances from
// recs, rather than being unset or already satisfied.
//
// applyGlobalInstanceLimit and requireRankingSignal have to agree on this
// exactly: the second refuses precisely the runs whose outcome the first would
// otherwise decide on an ordering that carries no information. If the two
// conditions drift apart, either a run is refused for a cap that changes
// nothing, or an unrankable run reaches the cap after all.
func capBinds(recs []common.Recommendation, cfg Config) bool {
	return cfg.MaxInstances > 0 && CalculateTotalInstances(recs) > int(cfg.MaxInstances)
}

// applyGlobalInstanceLimit enforces --max-instances once across the entire run.
//
// The flag is documented as a hard cap on the total number of instances
// purchased across all recommendations, so it has to see every service and
// every region together. Applying it inside the per-region fetch instead caps
// each (service, region) pair independently and multiplies the operator's cap
// by the number of pairs.
//
// passed must already be ordered best-first by rule: ApplyInstanceLimit
// consumes the slice in order and drops the tail, so the ordering decides
// which commitments survive. scorer.Score guarantees the
// rankBySavingsPercentage ordering; scoreAndLimitCSVRecs establishes the
// rankBySavingsPerInstance one.
//
// Truncation can push a recommendation under --min-count, which is a hard
// floor rather than advice, so dropTruncatedBelowMinCount removes any such
// recommendation instead of purchasing it short.
//
// Nothing is truncated silently. Every reduced or dropped recommendation is
// named on stdout, and the drops are counted into the end-of-run summary.
func applyGlobalInstanceLimit(passed []common.Recommendation, cfg Config, rule rankingRule, drops *common.DropSummary) []common.Recommendation {
	if !capBinds(passed, cfg) {
		return passed
	}

	totalBefore := CalculateTotalInstances(passed)
	limited := ApplyInstanceLimit(passed, cfg.MaxInstances)
	limited, belowMin := dropTruncatedBelowMinCount(limited, cfg.MinCount)
	reportInstanceLimit(passed, limited, len(belowMin), totalBefore, cfg.MaxInstances, rule, drops)
	reportMinCountDrops(belowMin, cfg.MinCount, drops)
	return limited
}

// dropTruncatedBelowMinCount removes recommendations that the cap truncated to
// fewer instances than --min-count allows, returning the survivors and the
// removed recommendations at their truncated counts.
//
// --min-count is a hard floor everywhere else in the codebase, never advice:
// the scorer rejects recommendations under it outright
// (scorer.filterReason, "count %d below minimum %d"), the scheduler's
// meetsMinCount drops them, and both `docs/cli/filtering.md` and
// `docs/cli/README.md` describe it as dropping recommendations below the
// number. filtering.md applies it to "the adjusted instance count (after
// coverage scaling)", so the floor is meant to gate the *sized* count, and
// truncation by --max-instances is another form of sizing.
//
// Buying a commitment smaller than the operator's stated minimum can be worse
// than buying nothing, which is the whole reason the floor exists, so a
// truncated recommendation is dropped rather than purchased short. The freed
// budget is deliberately not redistributed: the next recommendation would have
// to fit in an even smaller remainder and would fail the same floor.
//
// Removal is always from the tail. ApplyInstanceLimit reduces at most one
// recommendation (the one where the budget runs out, which is the last it
// keeps), and every earlier one still carries the full count that already
// cleared the scorer's floor. Taking only from the tail keeps the result a
// prefix of the input, which reportInstanceLimit relies on.
func dropTruncatedBelowMinCount(limited []common.Recommendation, minCount int) (kept, removed []common.Recommendation) {
	if minCount <= 0 {
		return limited, nil
	}
	kept = limited
	for len(kept) > 0 && kept[len(kept)-1].Count < minCount {
		removed = append(removed, kept[len(kept)-1])
		kept = kept[:len(kept)-1]
	}
	return kept, removed
}

// reportMinCountDrops names each recommendation the --min-count floor rejected
// after --max-instances truncated it. It continues the reportInstanceLimit
// listing, where these already appear as dropped, and explains why they were
// not simply purchased at the reduced count.
func reportMinCountDrops(removed []common.Recommendation, minCount int, drops *common.DropSummary) {
	for i := range removed {
		rec := removed[i]
		AppLogger.Printf("     ↳ %s %s %s: the cap left room for only %d instances, below --min-count %d, so it is dropped rather than purchased short\n",
			rec.Service, rec.Region, rec.ResourceType, rec.Count, minCount)
	}
	drops.Add(common.DropMinCountAfterCap, len(removed))
}

// reportInstanceLimit prints what --max-instances removed from the run.
// after must be the prefix of before produced by ApplyInstanceLimit, so
// after[i] and before[i] describe the same recommendation.
//
// rule is the ordering the caller sorted before by, and is printed verbatim:
// the drop list is unreadable without knowing which key decided it, and the
// two paths do not rank on the same key.
//
// belowMinCount is how many of the missing entries were removed by the
// --min-count floor rather than by the budget. They are still listed here as
// dropped (they were), but they are attributed to --min-count-after-cap by
// reportMinCountDrops, so excluding them from this tally keeps each dropped
// recommendation counted exactly once in the end-of-run summary.
func reportInstanceLimit(before, after []common.Recommendation, belowMinCount, totalBefore int, maxInstances int32, rule rankingRule, drops *common.DropSummary) {
	AppLogger.Printf("\n🔒 --max-instances=%d caps the whole run: the %d recommendations that passed scoring total %d instances.\n",
		maxInstances, len(before), totalBefore)
	AppLogger.Printf("   Keeping the %s. The following are reduced or dropped:\n", rule)

	reduced, dropped := 0, 0
	for i := range before {
		rec := before[i]
		kept := 0
		if i < len(after) {
			kept = after[i].Count
		}
		switch {
		case kept == rec.Count:
			continue
		case kept > 0:
			reduced++
			AppLogger.Printf("   • reduced: %s %s %s %d → %d instances\n", rec.Service, rec.Region, rec.ResourceType, rec.Count, kept)
		default:
			dropped++
			AppLogger.Printf("   • dropped: %s %s %s (%d instances)\n", rec.Service, rec.Region, rec.ResourceType, rec.Count)
		}
	}

	drops.Add(common.DropMaxInstances, dropped-belowMinCount)
	AppLogger.Printf("   Proceeding with %d instances across %d recommendations (%d reduced, %d dropped).\n",
		CalculateTotalInstances(after), len(after), reduced, dropped)
}

// sumPassedRecs returns total instance count and total estimated savings for passed recs.
func sumPassedRecs(recs []common.Recommendation) (total int, totalSavings float64) {
	for _rvc := range recs {
		r := recs[_rvc]
		total += r.Count
		totalSavings += r.EstimatedSavings
	}
	return
}

// executePurchasePipeline purchases each rec in the passed list (or dry-runs) and writes audit records.
func executePurchasePipeline(ctx context.Context, awsCfg aws.Config, recs []common.Recommendation, isDryRun bool, runID string, cfg Config) []common.PurchaseResult {
	results := make([]common.PurchaseResult, 0, len(recs))
	for i := range recs {
		rec := recs[i]
		if shutdownRequested.Load() {
			log.Printf("Shutdown requested — skipping %d remaining recommendations", len(recs)-i)
			break
		}
		result, status := purchaseSingleRec(ctx, awsCfg, rec, i+1, isDryRun, cfg)
		results = append(results, result)
		auditRec := common.NewAuditRecord(runID, rec, result, status, isDryRun, common.PurchaseSourceCLI)
		if err := common.WriteAuditRecord(auditRec, cfg.AuditLog); err != nil {
			log.Printf("Warning: failed to write audit record: %v", err)
		}
		if !isDryRun && i < len(recs)-1 && os.Getenv("DISABLE_PURCHASE_DELAY") != "true" {
			time.Sleep(PurchaseDelaySeconds * time.Second)
		}
	}
	return results
}

// purchaseSingleRec executes or dry-runs a single purchase and returns the result + audit status.
func purchaseSingleRec(ctx context.Context, awsCfg aws.Config, rec common.Recommendation, index int, isDryRun bool, cfg Config) (purchaseResult common.PurchaseResult, auditStatus string) {
	AppLogger.Printf("  [%d] %s %s %s (count=%d)\n", index, rec.Service, rec.Region, rec.ResourceType, rec.Count)
	if isDryRun {
		result := createDryRunResult(rec, rec.Region, index, cfg)
		AppLogger.Printf("    [dry-run] %s\n", result.CommitmentID)
		return result, "skipped"
	}

	regionalCfg := awsCfg.Copy()
	regionalCfg.Region = rec.Region
	serviceClient := createServiceClient(rec.Service, regionalCfg)
	if serviceClient == nil {
		AppLogger.Printf("    ⚠️  No service client for %s\n", rec.Service)
		return common.PurchaseResult{Success: false}, "error"
	}

	result := executePurchase(ctx, rec, rec.Region, index, serviceClient, cfg)
	status := "success"
	if !result.Success {
		status = "error"
		AppLogger.Printf("    ❌ %v\n", result.Error)
	} else {
		AppLogger.Printf("    ✅ %s\n", result.CommitmentID)
	}
	return result, status
}

// buildServiceStats computes per-service statistics from a purchase run.
// Results are assumed to be in the same order as recs (1:1 correspondence).
func buildServiceStats(recs []common.Recommendation, results []common.PurchaseResult) map[common.ServiceType]ServiceProcessingStats {
	byService := make(map[common.ServiceType][]common.Recommendation)
	resultsByService := make(map[common.ServiceType][]common.PurchaseResult)
	for i := range recs {
		rec := recs[i]
		byService[rec.Service] = append(byService[rec.Service], rec)
		if i < len(results) {
			resultsByService[rec.Service] = append(resultsByService[rec.Service], results[i])
		}
	}
	stats := make(map[common.ServiceType]ServiceProcessingStats)
	for service, serviceRecs := range byService {
		stats[service] = calculateServiceStats(service, serviceRecs, resultsByService[service])
	}
	return stats
}

// runCSVPathOrFatal runs the CSV purchase path and exits fatally on error.
// It isolates the error-to-fatal glue from runToolMultiService so that path
// stays under the cyclomatic-complexity budget while runToolFromCSV remains
// unit-testable via its returned error.
func runCSVPathOrFatal(ctx context.Context, cfg Config) {
	if err := runToolFromCSV(ctx, cfg); err != nil {
		log.Fatalf("%v", err)
	}
}

// runToolFromCSV processes recommendations from a CSV input file.
// It returns an error instead of exiting so the orchestration glue is
// unit-testable; the caller (runCSVPathOrFatal) turns errors fatal.
func runToolFromCSV(ctx context.Context, cfg Config) error {
	isDryRun := effectiveDryRun(cfg)
	printRunMode(isDryRun)

	csvModeCoverage := determineCSVCoverage(cfg)

	AppLogger.Printf("📄 Reading recommendations from CSV: %s\n", cfg.CSVInput)

	// Read recommendations from CSV
	recs, err := loadRecommendationsFromCSV(cfg.CSVInput)
	if err != nil {
		return fmt.Errorf("failed to read CSV file: %w", err)
	}

	AppLogger.Printf("✅ Loaded %d recommendations from CSV\n", len(recs))

	// Filter and adjust recommendations
	recs, err = filterAndAdjustRecommendations(recs, csvModeCoverage, cfg)
	if err != nil {
		return err
	}

	if len(recs) == 0 {
		AppLogger.Println("⚠️  No recommendations to process after filtering")
		return nil
	}

	awsCfg, err := loadAWSConfig(ctx, cfg)
	if err != nil {
		return fmt.Errorf("failed to load AWS config: %w", err)
	}

	// Create account alias cache for lookup
	accountCache := NewAccountAliasCache(awsCfg)

	// Populate account names from account IDs
	populateAccountNames(ctx, recs, accountCache)

	// Group recommendations by service and region
	recsByServiceRegion := groupRecommendationsByServiceRegion(recs)

	// Process purchases
	allResults := make([]common.PurchaseResult, 0)
	serviceResults := make([]common.PurchaseResult, 0)
	serviceStats := make(map[common.ServiceType]ServiceProcessingStats)
	// allAdjustedRecs accumulates post-dedup recommendations so the final summary
	// reflects what was actually processed rather than the pre-dedup input slice.
	allAdjustedRecs := make([]common.Recommendation, 0)

	for service, regionRecs := range recsByServiceRegion {
		// Reset service results for each service
		serviceResults = serviceResults[:0]

		AppLogger.Printf("\n━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n")
		AppLogger.Printf("🎯 Processing %s\n", getServiceDisplayName(service))
		AppLogger.Printf("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n")

		serviceRecs := make([]common.Recommendation, 0)
		for region, recs := range regionRecs {
			AppLogger.Printf("\n  📍 Region: %s (%d recommendations)\n", region, len(recs))

			// Get service client for this region
			regionalCfg := awsCfg.Copy()
			regionalCfg.Region = region
			serviceClient := createServiceClient(service, regionalCfg)

			if serviceClient == nil {
				AppLogger.Printf("  ⚠️  Service client not yet implemented for %s\n", getServiceDisplayName(service))
				AppLogger.Printf("     (Skipping purchase phase for this service)\n")
				continue
			}

			// Check for duplicate RIs to avoid double purchasing
			adjustedRecs, err := adjustRecsForDuplicates(ctx, recs, serviceClient)
			if err != nil {
				AppLogger.Printf("  ⚠️  Warning: Could not check for existing RIs: %v\n", err)
				adjustedRecs = recs // Continue with original recommendations if check fails
			}
			// Deducting existing commitments shrinks Count, which can push a
			// row that cleared the floor in filterAndAdjustRecommendations back
			// under it (--min-count 5, a row of 6, and 5 matching recent
			// commitments would otherwise be purchased at 1). --min-count is a
			// floor on what gets bought, so it is re-applied to whatever the
			// deduction left, not only to the pre-deduction counts.
			recs = applyMinCountFloor(adjustedRecs, cfg.MinCount)

			serviceRecs = append(serviceRecs, recs...)
			allAdjustedRecs = append(allAdjustedRecs, recs...)

			// Process purchases for this region
			regionResults := processPurchaseLoop(ctx, recs, region, isDryRun, serviceClient, cfg)
			serviceResults = append(serviceResults, regionResults...)
		}

		// Add service results to overall results
		allResults = append(allResults, serviceResults...)

		// Calculate service statistics (using only this service's results)
		stats := calculateServiceStats(service, serviceRecs, serviceResults)
		serviceStats[service] = stats
		printServiceSummary(service, stats)
	}

	// Generate CSV filename and write report
	finalCSVOutput := generateCSVFilename(isDryRun, cfg)

	// Write CSV report
	if err := writeMultiServiceCSVReport(allResults, finalCSVOutput); err != nil {
		log.Printf("Warning: Failed to write CSV output: %v", err)
	} else {
		AppLogger.Printf("\n📋 CSV report written to: %s\n", finalCSVOutput)
	}

	// Print final summary using the post-dedup slice so counts match what was
	// actually processed, not the pre-dedup input passed into the outer loop.
	printMultiServiceSummary(allAdjustedRecs, allResults, serviceStats, isDryRun)
	return nil
}

// filterAndAdjustRecommendations applies filters, coverage, count override,
// the --min-count floor and the run-wide --max-instances cap to
// recommendations loaded from --input-csv.
//
// It returns an error when the cap cannot be enforced honestly; see
// requireRankingSignal.
func filterAndAdjustRecommendations(recs []common.Recommendation, csvModeCoverage float64, cfg Config) ([]common.Recommendation, error) {
	// Query running instances for engine version validation
	log.Printf("🔍 Querying running RDS instances across all regions to validate engine versions...")
	instanceVersions, err := queryRunningInstanceEngineVersions(context.Background(), cfg)
	if err != nil {
		log.Printf("⚠️  Warning: Failed to query running instances for engine version validation: %v", err)
		log.Printf("   Continuing without engine version filtering")
		instanceVersions = make(map[string][]InstanceEngineVersion)
	} else {
		log.Printf("✅ Found %d instance types with version information across all regions", len(instanceVersions))
	}

	// Query major engine versions for extended support detection
	log.Printf("🔍 Querying AWS RDS major engine versions for extended support information...")
	versionInfo, err := queryMajorEngineVersions(context.Background(), cfg)
	if err != nil {
		log.Printf("⚠️  Warning: Failed to query major engine versions: %v", err)
		log.Printf("   Continuing without extended support detection")
		versionInfo = make(map[string]MajorEngineVersionInfo)
	} else {
		log.Printf("✅ Found support information for %d major engine versions", len(versionInfo))
	}

	// Apply filters (empty currentRegion since we're processing from CSV, not iterating regions).
	// Drop tracking is skipped on the CSV path (nil drops).
	originalCount := len(recs)
	recs = applyFilters(recs, &cfg, instanceVersions, versionInfo, "", nil)
	if len(recs) < originalCount {
		AppLogger.Printf("🔍 After filters: %d recs (filtered out %d)\n", len(recs), originalCount-len(recs))
	}

	// Apply sizing — target-coverage if set, otherwise coverage.
	// Coverage 100% is a no-op (early-returned inside ApplyCoverage), but
	// --target-coverage always applies even at coverage 100%, so the
	// CSV-path short-circuit is conditional on TargetCoverage == 0.
	if cfg.TargetCoverage > 0 || csvModeCoverage < 100 {
		beforeSize := len(recs)
		recs = applySizing(recs, cfg, csvModeCoverage, nil)
		if cfg.TargetCoverage > 0 {
			AppLogger.Printf("🎯 Applying %.1f%% target-coverage: %d recs selected (from %d)\n", cfg.TargetCoverage, len(recs), beforeSize)
		} else {
			AppLogger.Printf("📈 Applying %.1f%% coverage: %d recs selected (from %d)\n", csvModeCoverage, len(recs), beforeSize)
		}
	}

	// Apply count override if specified
	if cfg.OverrideCount > 0 {
		recs = ApplyCountOverride(recs, cfg.OverrideCount)
	}

	// Enforce --min-count and the run-wide --max-instances cap.
	return scoreAndLimitCSVRecs(recs, cfg)
}

// scoreAndLimitCSVRecs enforces --min-count and the run-wide --max-instances
// cap on recommendations loaded from --input-csv, so both spend guards behave
// on the CSV path as they do on the recommendation-driven path. Both are
// documented in docs/cli/filtering.md as flags of the tool, not of a mode.
//
// Previously the CSV path handed the load-ordered slice straight to
// ApplyInstanceLimit, which consumes its input in slice order and drops the
// tail: whichever rows appeared first in the file spent the budget, and
// --min-count was never consulted, so a row the cap truncated below the floor
// was purchased short.
//
// Only MinCount is enforced here. A CSV row carries no savings percentage and
// no break-even figure (writeMultiServiceCSVReport emits neither column and
// parseCSVRecord reads neither), so both fields load as zero and gating on
// them would reject every row of every file. --min-savings-pct and
// --max-break-even-months are therefore refused up front by
// validateCSVModeFilterFlags rather than accepted and ignored here; #1819
// tracks teaching the CSV format to carry those columns.
//
// scorer.Score's own ordering is useless here: SavingsPercentage is uniformly
// zero on loaded rows, so it resolves on EstimatedSavings, a whole-row dollar
// total, while --max-instances is a budget in instances. Ranking a total
// against a per-instance budget spends the whole budget on whichever row is
// merely biggest, not on the rows that return the most per instance bought.
// sortBySavingsPerInstance therefore re-orders the survivors on the rate
// derived from the two columns a CSV does carry, which is the greedy the flag
// implies, and applyGlobalInstanceLimit consumes that order: the cap keeps the
// best-value rows, names every row it reduces or drops, and drops rather than
// shortens anything truncated below --min-count.
func scoreAndLimitCSVRecs(recs []common.Recommendation, cfg Config) ([]common.Recommendation, error) {
	passed := applyMinCountFloor(recs, cfg.MinCount)
	if err := requireRankingSignal(passed, cfg); err != nil {
		return nil, err
	}
	sortBySavingsPerInstance(passed)
	return applyGlobalInstanceLimit(passed, cfg, rankBySavingsPerInstance, nil), nil
}

// applyMinCountFloor drops recommendations below the --min-count floor and
// names each drop on stdout. A floor of 0 disables the flag, and returns recs
// untouched rather than re-ordering them for nothing.
//
// The floor itself is scorer.Score's, so the CSV path and the
// recommendation-driven path reject on the identical predicate and reason
// string. MinCount is the only scorer.Config field this helper ever sets, so
// the "--min-count dropped" prefix cannot come to describe some other filter.
func applyMinCountFloor(recs []common.Recommendation, minCount int) []common.Recommendation {
	if minCount <= 0 {
		return recs
	}
	scored := scorer.Score(recs, scorer.Config{MinCount: minCount})
	for i := range scored.Filtered {
		f := scored.Filtered[i]
		AppLogger.Printf("🔒 --min-count dropped %s %s %s: %s\n",
			f.Recommendation.Service, f.Recommendation.Region, f.Recommendation.ResourceType, f.FilterReason)
	}
	return scored.Passed
}

// savingsPerInstance is the ranking key for CSV rows: the row's monthly
// savings divided by the instances it would buy. --max-instances is a budget
// in instances, so the rows worth keeping are the ones returning the most per
// instance, not the ones whose total happens to be largest.
//
// A non-positive Count buys nothing and has no rate, so it ranks last rather
// than dividing by zero. ApplyInstanceLimit already refuses to credit budget
// back for such a row.
func savingsPerInstance(rec common.Recommendation) float64 {
	if rec.Count <= 0 {
		return 0
	}
	return rec.EstimatedSavings / float64(rec.Count)
}

// sortBySavingsPerInstance orders recs best-value-first, in place.
//
// Rows with equal rates fall back to the same Service|Region|ResourceType key
// scorer.Score tie-breaks on, so the selection is deterministic whatever order
// the file listed them in. The tie-break is spelled out here rather than
// inherited from an upstream sort because --min-count 0 skips the scorer
// entirely, which would otherwise leave file order deciding between equals on
// exactly the path #1741 is about.
//
// It must run before the cap, never after: ApplyInstanceLimit truncates Count
// without rescaling EstimatedSavings (#1830), so a post-cap row's rate is
// inflated by exactly the amount the cap removed.
func sortBySavingsPerInstance(recs []common.Recommendation) {
	sort.SliceStable(recs, func(i, j int) bool {
		a, b := recs[i], recs[j]
		if rateA, rateB := savingsPerInstance(a), savingsPerInstance(b); rateA != rateB {
			return rateA > rateB
		}
		keyA := string(a.Service) + "|" + a.Region + "|" + a.ResourceType
		keyB := string(b.Service) + "|" + b.Region + "|" + b.ResourceType
		return keyA < keyB
	})
}

// maxNamedUnrankableRows bounds how many offending rows requireRankingSignal
// names before summarising the rest, so a large file produces a readable error.
const maxNamedUnrankableRows = 5

// requireRankingSignal refuses a run whose --max-instances cap has to choose
// between rows it cannot rank.
//
// parseCSVFloat leaves EstimatedSavings at zero for a blank cell, and
// getCSVField returns "" for a column that is not in the header at all, so a
// CSV written without an EstimatedSavings column loads every row at zero.
// Nothing downstream can tell that apart from a row genuinely worth $0: the
// value is absent, not zero. With every rate equal, sortBySavingsPerInstance
// and scorer.Score both fall through to the Service|Region|ResourceType
// tie-break, and the cap silently buys by instance-type name while stdout and
// docs/cli/filtering.md both promise it is buying by savings.
//
// Ranking only decides anything when the cap actually binds, so that is the
// only case this refuses; a file with no savings column still runs uncapped,
// and so does one whose total already fits. Money paths in this project fail
// loud rather than picking a defensible-looking default, and #1741's own
// framing is that silent partial enforcement of a spend guard is worse than
// not offering the guard.
func requireRankingSignal(recs []common.Recommendation, cfg Config) error {
	if !capBinds(recs, cfg) {
		return nil
	}

	unrankable := make([]string, 0)
	for i := range recs {
		if recs[i].EstimatedSavings <= 0 {
			unrankable = append(unrankable, fmt.Sprintf("%s %s %s",
				recs[i].Service, recs[i].Region, recs[i].ResourceType))
		}
	}
	if len(unrankable) == 0 {
		return nil
	}

	named := unrankable
	suffix := ""
	if len(named) > maxNamedUnrankableRows {
		named = named[:maxNamedUnrankableRows]
		suffix = fmt.Sprintf(" (and %d more)", len(unrankable)-maxNamedUnrankableRows)
	}
	return fmt.Errorf(
		"--max-instances=%d has to choose which of %d recommendations to buy, but %d row(s) of %s carry no usable EstimatedSavings value: %s%s. "+
			"A blank or missing EstimatedSavings cell is indistinguishable from $0 of savings, so capping on it would pick by instance-type name rather than by value. "+
			"Populate EstimatedSavings for every row, or drop --max-instances and cap the file itself",
		cfg.MaxInstances, len(recs), len(unrankable), cfg.CSVInput, strings.Join(named, ", "), suffix)
}

// processService processes a single service and returns recommendations and results.
// Used by legacy callers; new code should use fetchAllRecs + executePurchasePipeline.
func processService(ctx context.Context, awsCfg aws.Config, recClient provider.RecommendationsClient, accountCache *AccountAliasCache, service common.ServiceType, isDryRun bool, cfg Config, engineData engineVersionData) ([]common.Recommendation, []common.PurchaseResult) { //nolint:unparam // engineData always nil at current callsites but param is part of the API
	regionsToProcess, err := determineRegionsForService(ctx, awsCfg, recClient, service, cfg.Regions)
	if err != nil {
		log.Printf("❌ Failed to determine regions: %v", err)
		return nil, nil
	}

	serviceRecs := make([]common.Recommendation, 0)
	serviceResults := make([]common.PurchaseResult, 0)

	for i, region := range regionsToProcess {
		// Legacy single-service entry point — no coverage map is fetched here,
		// so sizing falls back to the no-existing-commitments formula. The new
		// path (runToolMultiService) fetches coverage once and threads it through.
		regionResult := processRegionRecommendations(
			ctx, awsCfg, recClient, accountCache,
			service, region, i+1, len(regionsToProcess),
			engineData, isDryRun, cfg, nil,
		)
		serviceRecs = append(serviceRecs, regionResult.recommendations...)
		serviceResults = append(serviceResults, regionResult.results...)
	}

	return serviceRecs, serviceResults
}

// processPurchaseLoop processes purchases for a single region (used by CSV mode).
func processPurchaseLoop(ctx context.Context, recs []common.Recommendation, region string, isDryRun bool, serviceClient provider.ServiceClient, cfg Config) []common.PurchaseResult {
	results := make([]common.PurchaseResult, 0, len(recs))

	for j := range recs {
		rec := recs[j]
		AppLogger.Printf("    [%d/%d] Processing: %s %s\n", j+1, len(recs), rec.Service, rec.ResourceType)
		AppLogger.Printf("    💳 Purchasing %d instances\n", rec.Count)

		var result common.PurchaseResult
		if isDryRun {
			result = createDryRunResult(rec, region, j+1, cfg)
		} else {
			// Ask for confirmation before proceeding with purchases (only on first item)
			if j == 0 {
				totalInstances := CalculateTotalInstances(recs)
				totalSavings := 0.0
				for _rvc := range recs {
					r := recs[_rvc]
					totalSavings += r.EstimatedSavings
				}

				if !ConfirmPurchase(totalInstances, totalSavings, cfg.SkipConfirmation) {
					// User canceled - return canceled results for all
					return createCancelledResults(recs, region, cfg)
				}
			}

			// Execute actual purchase
			result = executePurchase(ctx, rec, region, j+1, serviceClient, cfg)

			// Add delay between purchases to avoid rate limiting
			if j < len(recs)-1 && os.Getenv("DISABLE_PURCHASE_DELAY") != "true" {
				time.Sleep(PurchaseDelaySeconds * time.Second)
			}
		}

		results = append(results, result)

		if result.Success {
			AppLogger.Printf("    ✅ Success: %s\n", result.CommitmentID)
		} else {
			errMsg := "unknown error"
			if result.Error != nil {
				errMsg = result.Error.Error()
			}
			AppLogger.Printf("    ❌ Failed: %s\n", errMsg)
		}
	}

	return results
}
