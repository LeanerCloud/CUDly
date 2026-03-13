// Package analytics provides the hourly collector for savings data.
package analytics

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/LeanerCloud/CUDly/internal/config"
)

// Constants for time calculations
const (
	// HoursPerYear is the approximate number of hours in a year (365 days)
	HoursPerYear = 365 * 24

	// HoursPerMonth is the approximate number of hours in a month (30 days)
	HoursPerMonth = 30 * 24
)

// Collector aggregates savings data and writes it to PostgreSQL for analytics.
type Collector struct {
	store       AnalyticsStore
	configStore config.StoreInterface
	accountID   string
}

// CollectorConfig holds configuration for the collector.
type CollectorConfig struct {
	AnalyticsStore AnalyticsStore
	AccountID      string
}

// NewCollector creates a new savings collector.
func NewCollector(cfg CollectorConfig, configStore config.StoreInterface) (*Collector, error) {
	if cfg.AnalyticsStore == nil {
		return nil, fmt.Errorf("analytics store is required")
	}
	if configStore == nil {
		return nil, fmt.Errorf("config store is required")
	}

	return &Collector{
		store:       cfg.AnalyticsStore,
		configStore: configStore,
		accountID:   cfg.AccountID,
	}, nil
}

// aggregateData holds aggregated savings data for a service/provider/region combination
type aggregateData struct {
	service    string
	provider   string
	region     string
	commitment float64
	usage      float64
	savings    float64
	count      int
}

// Collect aggregates current savings data and writes it to PostgreSQL.
// This should be called hourly by EventBridge scheduled rule.
func (c *Collector) Collect(ctx context.Context) error {
	log.Printf("Analytics collector: Starting hourly collection for account %s", c.accountID)

	// Get recent purchase history to calculate current savings
	purchases, err := c.configStore.GetPurchaseHistory(ctx, c.accountID, 1000)
	if err != nil {
		return fmt.Errorf("failed to get purchase history: %w", err)
	}

	log.Printf("Analytics collector: Processing %d purchases", len(purchases))

	// Calculate savings from purchases
	now := time.Now().UTC()

	// Aggregate savings by service, provider, region
	serviceMap := make(map[string]*aggregateData)

	// Process each purchase to calculate active savings
	activePurchases := 0
	for _, p := range purchases {
		// Check if purchase is still active (within term)
		purchaseTime := p.Timestamp
		termDuration := time.Duration(p.Term*HoursPerYear) * time.Hour
		expiryTime := purchaseTime.Add(termDuration)

		if now.After(expiryTime) {
			continue // Skip expired purchases
		}

		activePurchases++

		// Create unique key for this combination (service|provider|region)
		key := fmt.Sprintf("%s|%s|%s", p.Service, p.Provider, p.Region)

		if serviceMap[key] == nil {
			serviceMap[key] = &aggregateData{
				service:  p.Service,
				provider: p.Provider,
				region:   p.Region,
			}
		}

		agg := serviceMap[key]

		// Calculate hourly savings rate for this purchase
		// EstimatedSavings is typically monthly, convert to hourly
		hourlySavings := p.EstimatedSavings / HoursPerMonth

		agg.savings += hourlySavings
		agg.commitment += p.UpfrontCost / (float64(p.Term) * HoursPerYear) // Amortized hourly
		agg.count++
	}

	log.Printf("Analytics collector: Found %d active purchases, %d unique combinations", activePurchases, len(serviceMap))

	// Build snapshots for all active combinations and bulk-insert in one round-trip.
	snapshots := make([]SavingsSnapshot, 0, len(serviceMap))
	for _, agg := range serviceMap {
		commitmentType := "RI"
		if agg.service == "SavingsPlans" {
			commitmentType = "SavingsPlan"
		}

		snapshots = append(snapshots, SavingsSnapshot{
			AccountID:          c.accountID,
			Timestamp:          now,
			Provider:           agg.provider,
			Service:            agg.service,
			Region:             agg.region,
			CommitmentType:     commitmentType,
			TotalCommitment:    agg.commitment,
			TotalUsage:         0, // TODO: Can be calculated from CloudWatch if needed
			TotalSavings:       agg.savings,
			CoveragePercentage: 0, // TODO: Calculate from usage data if needed
			Metadata: map[string]any{
				"active_purchases": agg.count,
				"collection_time":  now.Format(time.RFC3339),
			},
		})
	}

	if len(snapshots) == 0 {
		log.Printf("Analytics collector: No active purchases to snapshot")
		return nil
	}

	if err := c.store.BulkInsertSnapshots(ctx, snapshots); err != nil {
		return fmt.Errorf("failed to save snapshots: %w", err)
	}

	log.Printf("Analytics collector: Successfully saved %d snapshots", len(snapshots))
	return nil
}
