// Package analytics provides the scheduled collector for savings time-series data.
package analytics

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/LeanerCloud/CUDly/internal/config"
)

// Constants for time calculations.
const (
	// HoursPerYear is the approximate number of hours in a year (365 days).
	HoursPerYear = 365 * 24

	// MonthsPerYear amortizes an upfront commitment cost into a monthly run-rate
	// over the commitment term (term is expressed in years).
	MonthsPerYear = 12
)

// Collector aggregates savings data and writes point-in-time snapshots to
// PostgreSQL for the historical-savings analytics time-series. It runs on a
// schedule (see server.handleCollectAnalytics) across all tenants.
type Collector struct {
	store       Store
	configStore config.StoreInterface
}

// CollectorConfig holds configuration for the collector.
type CollectorConfig struct {
	Store Store
}

// NewCollector creates a new savings collector.
func NewCollector(cfg CollectorConfig, configStore config.StoreInterface) (*Collector, error) {
	if cfg.Store == nil {
		return nil, fmt.Errorf("analytics store is required")
	}
	if configStore == nil {
		return nil, fmt.Errorf("config store is required")
	}
	return &Collector{
		store:       cfg.Store,
		configStore: configStore,
	}, nil
}

// aggregateData holds aggregated savings data for one
// (cloud_account_id|account_id|service|provider|region|commitment_type) bucket.
type aggregateData struct {
	accountID      string
	cloudAccountID *string
	service        string
	provider       string
	region         string
	commitmentType string
	commitment     float64
	// usage accumulates the recurring (monthly) cost of the commitments in this
	// bucket as the covered-usage proxy. usageKnown stays false until at least
	// one contributing row carried a non-nil MonthlyCost, so a bucket made up
	// entirely of all-upfront commitments writes NULL usage rather than 0
	// (feedback_nullable_not_zero).
	usage      float64
	usageKnown bool
	savings    float64
	count      int
}

// aggKey is the bucket identity. cloudAccountID is dereferenced (or "" when
// nil) so two rows for the same provider account but differing UUID-vs-NULL
// don't merge across the tenant boundary.
func aggKey(p *config.PurchaseHistoryRecord, commitmentType string) string {
	cloud := ""
	if p.CloudAccountID != nil {
		cloud = *p.CloudAccountID
	}
	return fmt.Sprintf("%s|%s|%s|%s|%s|%s", cloud, p.AccountID, p.Service, p.Provider, p.Region, commitmentType)
}

// Collect aggregates current savings data across all tenants and writes a
// snapshot row per bucket. Intended to be called on a schedule.
func (c *Collector) Collect(ctx context.Context) error {
	log.Printf("Analytics collector: starting collection")

	now := time.Now().UTC()

	// Active-only read: the active filter is pushed into SQL so the result is
	// bounded by the number of live commitments rather than by all history ever
	// recorded. This avoids silently truncating older-but-still-active 1y/3y
	// commitments the way a single capped all-history page did.
	purchases, err := c.configStore.GetActivePurchaseHistory(ctx, now)
	if err != nil {
		return fmt.Errorf("failed to get active purchase history: %w", err)
	}

	log.Printf("Analytics collector: processing %d active purchases", len(purchases))

	serviceMap, activePurchases, skippedBadTerm, err := aggregatePurchases(ctx, purchases, now)
	if err != nil {
		return err
	}

	log.Printf("Analytics collector: %d active, %d unique buckets, %d skipped (term<=0)",
		activePurchases, len(serviceMap), skippedBadTerm)

	snapshots := buildSnapshots(serviceMap, now)
	if len(snapshots) == 0 {
		log.Printf("Analytics collector: no active purchases to snapshot")
		return nil
	}

	if err := c.store.BulkInsertSnapshots(ctx, snapshots); err != nil {
		// Surface context cancellation distinctly so the caller doesn't retry a
		// genuinely canceled run as a transient failure.
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return fmt.Errorf("collection canceled during write: %w", err)
		}
		return fmt.Errorf("failed to save snapshots: %w", err)
	}

	log.Printf("Analytics collector: saved %d snapshots", len(snapshots))
	return nil
}

// aggregatePurchases folds the active purchases into a per-bucket aggregation
// map keyed by aggKey. It skips Term<=0 rows (H1, counted as skippedBadTerm) and
// expired commitments, and treats context cancellation as terminal so a partial
// snapshot set is never written (feedback_ctx_cancel_terminal). Extracted from
// Collect to keep each function within the cyclomatic-complexity budget.
func aggregatePurchases(ctx context.Context, purchases []config.PurchaseHistoryRecord, now time.Time) (serviceMap map[string]*aggregateData, activePurchases, skippedBadTerm int, err error) {
	serviceMap = make(map[string]*aggregateData)

	for i := range purchases {
		p := &purchases[i]
		if err := ctx.Err(); err != nil {
			return nil, 0, 0, fmt.Errorf("collection canceled after %d rows: %w", activePurchases, err)
		}

		// H1: a Term <= 0 row would make the amortized-commitment division
		// (UpfrontCost / (Term*MonthsPerYear)) produce +Inf/NaN, which then
		// poisons every downstream SUM/AVG and errors at the DECIMAL bind.
		// Skip it and count it for observability rather than feeding a zero
		// denominator into the division.
		if p.Term <= 0 {
			skippedBadTerm++
			continue
		}

		// Skip expired commitments (outside their term window).
		expiryTime := p.Timestamp.Add(time.Duration(p.Term*HoursPerYear) * time.Hour)
		if now.After(expiryTime) {
			continue
		}
		activePurchases++

		commitmentType := commitmentTypeFor(p.Service)
		key := aggKey(p, commitmentType)

		agg := serviceMap[key]
		if agg == nil {
			agg = &aggregateData{
				accountID:      p.AccountID,
				cloudAccountID: p.CloudAccountID,
				service:        p.Service,
				provider:       p.Provider,
				region:         p.Region,
				commitmentType: commitmentType,
			}
			serviceMap[key] = agg
		}

		// Monthly savings run-rate (EstimatedSavings is already monthly). Stored as
		// a point-in-time run-rate, not an accrued total, so the monthly trend AVGs
		// snapshots and stays invariant to the collection schedule (daily vs hourly).
		agg.savings += p.EstimatedSavings
		// Upfront commitment amortized to a monthly run-rate over the term.
		// Term > 0 guaranteed above.
		agg.commitment += p.UpfrontCost / (float64(p.Term) * MonthsPerYear)
		// H2: real covered usage from the recurring monthly cost when present.
		// Nil MonthlyCost (e.g. AWS all-upfront) contributes nothing and leaves
		// usage unknown rather than implicitly $0.
		if p.MonthlyCost != nil {
			agg.usage += *p.MonthlyCost
			agg.usageKnown = true
		}
		agg.count++
	}

	return serviceMap, activePurchases, skippedBadTerm, nil
}

// commitmentTypeFor maps a service to its commitment_type. SavingsPlans is the
// only Savings Plan service today; everything else is a Reserved Instance.
func commitmentTypeFor(service string) string {
	if service == "SavingsPlans" {
		return "SavingsPlan"
	}
	return "RI"
}

// buildSnapshots converts the aggregation map into snapshot rows. total_usage is
// nil when no contributing row carried a recurring cost; coverage_percentage is
// nil because purchase_history carries no on-demand baseline to derive it from
// (writing a placeholder 0 would corrupt AVG, per feedback_nullable_not_zero).
func buildSnapshots(serviceMap map[string]*aggregateData, now time.Time) []SavingsSnapshot {
	snapshots := make([]SavingsSnapshot, 0, len(serviceMap))
	for _, agg := range serviceMap {
		var usage *float64
		if agg.usageKnown {
			u := agg.usage
			usage = &u
		}

		snapshots = append(snapshots, SavingsSnapshot{
			AccountID:          agg.accountID,
			CloudAccountID:     agg.cloudAccountID,
			Timestamp:          now,
			Provider:           agg.provider,
			Service:            agg.service,
			Region:             agg.region,
			CommitmentType:     agg.commitmentType,
			TotalCommitment:    agg.commitment,
			TotalUsage:         usage,
			TotalSavings:       agg.savings,
			CoveragePercentage: nil,
			Metadata: map[string]any{
				"active_purchases": agg.count,
				"collection_time":  now.Format(time.RFC3339),
			},
		})
	}
	return snapshots
}
