package purchase

import (
	"fmt"
	"time"

	"github.com/LeanerCloud/rds-ri-purchase-tool/internal/recommendations"
)

// Result represents the result of a Reserved Instance purchase operation
type Result struct {
	Config        recommendations.Recommendation `json:"config"`
	Success       bool                           `json:"success"`
	PurchaseID    string                         `json:"purchase_id,omitempty"`
	ReservationID string                         `json:"reservation_id,omitempty"`
	Message       string                         `json:"message"`
	Timestamp     time.Time                      `json:"timestamp"`
	ActualCost    float64                        `json:"actual_cost,omitempty"`
	ErrorCode     string                         `json:"error_code,omitempty"`
}

// GetStatusString returns a human-readable status
func (r *Result) GetStatusString() string {
	if r.Success {
		return "SUCCESS"
	}
	return "FAILED"
}

// GetFormattedTimestamp returns a formatted timestamp string
func (r *Result) GetFormattedTimestamp() string {
	return r.Timestamp.Format("2006-01-02 15:04:05")
}

// GetCostString returns a formatted cost string
func (r *Result) GetCostString() string {
	if r.ActualCost > 0 {
		return fmt.Sprintf("$%.2f", r.ActualCost)
	}
	return "N/A"
}

// OfferingDetails contains detailed information about a Reserved Instance offering
type OfferingDetails struct {
	OfferingID    string  `json:"offering_id"`
	InstanceType  string  `json:"instance_type"`
	Engine        string  `json:"engine"`
	Duration      string  `json:"duration"`
	PaymentOption string  `json:"payment_option"`
	MultiAZ       bool    `json:"multi_az"`
	FixedPrice    float64 `json:"fixed_price"`
	UsagePrice    float64 `json:"usage_price"`
	CurrencyCode  string  `json:"currency_code"`
	OfferingType  string  `json:"offering_type"`
}

// GetAZConfigString returns the AZ configuration as a string
func (o *OfferingDetails) GetAZConfigString() string {
	if o.MultiAZ {
		return "Multi-AZ"
	}
	return "Single-AZ"
}

// GetFormattedFixedPrice returns a formatted fixed price string
func (o *OfferingDetails) GetFormattedFixedPrice() string {
	return fmt.Sprintf("%.2f %s", o.FixedPrice, o.CurrencyCode)
}

// GetFormattedUsagePrice returns a formatted usage price string
func (o *OfferingDetails) GetFormattedUsagePrice() string {
	return fmt.Sprintf("%.4f %s/hour", o.UsagePrice, o.CurrencyCode)
}

// CostEstimate represents cost estimation for a recommendation
type CostEstimate struct {
	Recommendation   recommendations.Recommendation `json:"recommendation"`
	OfferingDetails  OfferingDetails                `json:"offering_details"`
	TotalFixedCost   float64                        `json:"total_fixed_cost"`
	MonthlyUsageCost float64                        `json:"monthly_usage_cost"`
	TotalTermCost    float64                        `json:"total_term_cost"`
	Error            string                         `json:"error,omitempty"`
}

// GetFormattedTotalFixedCost returns a formatted total fixed cost
func (c *CostEstimate) GetFormattedTotalFixedCost() string {
	return fmt.Sprintf("%.2f %s", c.TotalFixedCost, c.OfferingDetails.CurrencyCode)
}

// GetFormattedMonthlyUsageCost returns a formatted monthly usage cost
func (c *CostEstimate) GetFormattedMonthlyUsageCost() string {
	return fmt.Sprintf("%.2f %s/month", c.MonthlyUsageCost, c.OfferingDetails.CurrencyCode)
}

// GetFormattedTotalTermCost returns a formatted total term cost
func (c *CostEstimate) GetFormattedTotalTermCost() string {
	return fmt.Sprintf("%.2f %s", c.TotalTermCost, c.OfferingDetails.CurrencyCode)
}

// HasError returns true if the cost estimate has an error
func (c *CostEstimate) HasError() bool {
	return c.Error != ""
}

// BatchPurchaseResult represents the result of a batch purchase operation
type BatchPurchaseResult struct {
	TotalRecommendations int           `json:"total_recommendations"`
	SuccessfulPurchases  int           `json:"successful_purchases"`
	FailedPurchases      int           `json:"failed_purchases"`
	TotalInstances       int32         `json:"total_instances"`
	TotalCost            float64       `json:"total_cost"`
	Results              []Result      `json:"results"`
	StartTime            time.Time     `json:"start_time"`
	EndTime              time.Time     `json:"end_time"`
	Duration             time.Duration `json:"duration"`
}

// CalculateSuccessRate returns the success rate as a percentage
func (b *BatchPurchaseResult) CalculateSuccessRate() float64 {
	if b.TotalRecommendations == 0 {
		return 0
	}
	return (float64(b.SuccessfulPurchases) / float64(b.TotalRecommendations)) * 100
}

// GetFormattedDuration returns a formatted duration string
func (b *BatchPurchaseResult) GetFormattedDuration() string {
	return b.Duration.String()
}

// GetFormattedTotalCost returns a formatted total cost string
func (b *BatchPurchaseResult) GetFormattedTotalCost() string {
	return fmt.Sprintf("$%.2f", b.TotalCost)
}

// PurchaseStats provides statistics about purchase operations
type PurchaseStats struct {
	ByEngine       map[string]EngineStats   `json:"by_engine"`
	ByRegion       map[string]RegionStats   `json:"by_region"`
	ByPayment      map[string]PaymentStats  `json:"by_payment"`
	ByInstanceType map[string]InstanceStats `json:"by_instance_type"`
	TotalStats     TotalStats               `json:"total_stats"`
}

// EngineStats provides statistics for a specific engine
type EngineStats struct {
	TotalPurchases      int     `json:"total_purchases"`
	SuccessfulPurchases int     `json:"successful_purchases"`
	FailedPurchases     int     `json:"failed_purchases"`
	TotalInstances      int32   `json:"total_instances"`
	TotalCost           float64 `json:"total_cost"`
	SuccessRate         float64 `json:"success_rate"`
}

// RegionStats provides statistics for a specific region
type RegionStats struct {
	TotalPurchases      int     `json:"total_purchases"`
	SuccessfulPurchases int     `json:"successful_purchases"`
	FailedPurchases     int     `json:"failed_purchases"`
	TotalInstances      int32   `json:"total_instances"`
	TotalCost           float64 `json:"total_cost"`
	SuccessRate         float64 `json:"success_rate"`
}

// PaymentStats provides statistics for a specific payment option
type PaymentStats struct {
	TotalPurchases      int     `json:"total_purchases"`
	SuccessfulPurchases int     `json:"successful_purchases"`
	FailedPurchases     int     `json:"failed_purchases"`
	TotalInstances      int32   `json:"total_instances"`
	TotalCost           float64 `json:"total_cost"`
	SuccessRate         float64 `json:"success_rate"`
}

// InstanceStats provides statistics for a specific instance type
type InstanceStats struct {
	TotalPurchases      int     `json:"total_purchases"`
	SuccessfulPurchases int     `json:"successful_purchases"`
	FailedPurchases     int     `json:"failed_purchases"`
	TotalInstances      int32   `json:"total_instances"`
	TotalCost           float64 `json:"total_cost"`
	SuccessRate         float64 `json:"success_rate"`
}

// TotalStats provides overall statistics
type TotalStats struct {
	TotalPurchases      int     `json:"total_purchases"`
	SuccessfulPurchases int     `json:"successful_purchases"`
	FailedPurchases     int     `json:"failed_purchases"`
	TotalInstances      int32   `json:"total_instances"`
	TotalCost           float64 `json:"total_cost"`
	OverallSuccessRate  float64 `json:"overall_success_rate"`
}

// CalculateStats generates purchase statistics from results
func CalculateStats(results []Result) PurchaseStats {
	stats := PurchaseStats{
		ByEngine:       make(map[string]EngineStats),
		ByRegion:       make(map[string]RegionStats),
		ByPayment:      make(map[string]PaymentStats),
		ByInstanceType: make(map[string]InstanceStats),
	}

	for _, result := range results {
		rec := result.Config

		// Update total stats
		stats.TotalStats.TotalPurchases++
		stats.TotalStats.TotalInstances += rec.Count
		stats.TotalStats.TotalCost += result.ActualCost

		if result.Success {
			stats.TotalStats.SuccessfulPurchases++
		} else {
			stats.TotalStats.FailedPurchases++
		}

		// Update engine stats
		updateEngineStats(&stats, rec.Engine, result)

		// Update region stats
		updateRegionStats(&stats, rec.Region, result)

		// Update payment stats
		updatePaymentStats(&stats, rec.PaymentOption, result)

		// Update instance type stats
		updateInstanceStats(&stats, rec.InstanceType, result)
	}

	// Calculate success rates
	calculateSuccessRates(&stats)

	return stats
}

// Helper functions for updating statistics

func updateEngineStats(stats *PurchaseStats, engine string, result Result) {
	engineStats := stats.ByEngine[engine]
	engineStats.TotalPurchases++
	engineStats.TotalInstances += result.Config.Count
	engineStats.TotalCost += result.ActualCost

	if result.Success {
		engineStats.SuccessfulPurchases++
	} else {
		engineStats.FailedPurchases++
	}

	stats.ByEngine[engine] = engineStats
}

func updateRegionStats(stats *PurchaseStats, region string, result Result) {
	regionStats := stats.ByRegion[region]
	regionStats.TotalPurchases++
	regionStats.TotalInstances += result.Config.Count
	regionStats.TotalCost += result.ActualCost

	if result.Success {
		regionStats.SuccessfulPurchases++
	} else {
		regionStats.FailedPurchases++
	}

	stats.ByRegion[region] = regionStats
}

func updatePaymentStats(stats *PurchaseStats, paymentOption string, result Result) {
	paymentStats := stats.ByPayment[paymentOption]
	paymentStats.TotalPurchases++
	paymentStats.TotalInstances += result.Config.Count
	paymentStats.TotalCost += result.ActualCost

	if result.Success {
		paymentStats.SuccessfulPurchases++
	} else {
		paymentStats.FailedPurchases++
	}

	stats.ByPayment[paymentOption] = paymentStats
}

func updateInstanceStats(stats *PurchaseStats, instanceType string, result Result) {
	instanceStats := stats.ByInstanceType[instanceType]
	instanceStats.TotalPurchases++
	instanceStats.TotalInstances += result.Config.Count
	instanceStats.TotalCost += result.ActualCost

	if result.Success {
		instanceStats.SuccessfulPurchases++
	} else {
		instanceStats.FailedPurchases++
	}

	stats.ByInstanceType[instanceType] = instanceStats
}

func calculateSuccessRates(stats *PurchaseStats) {
	// Calculate overall success rate
	if stats.TotalStats.TotalPurchases > 0 {
		stats.TotalStats.OverallSuccessRate = (float64(stats.TotalStats.SuccessfulPurchases) / float64(stats.TotalStats.TotalPurchases)) * 100
	}

	// Calculate engine success rates
	for engine, engineStats := range stats.ByEngine {
		if engineStats.TotalPurchases > 0 {
			engineStats.SuccessRate = (float64(engineStats.SuccessfulPurchases) / float64(engineStats.TotalPurchases)) * 100
			stats.ByEngine[engine] = engineStats
		}
	}

	// Calculate region success rates
	for region, regionStats := range stats.ByRegion {
		if regionStats.TotalPurchases > 0 {
			regionStats.SuccessRate = (float64(regionStats.SuccessfulPurchases) / float64(regionStats.TotalPurchases)) * 100
			stats.ByRegion[region] = regionStats
		}
	}

	// Calculate payment success rates
	for payment, paymentStats := range stats.ByPayment {
		if paymentStats.TotalPurchases > 0 {
			paymentStats.SuccessRate = (float64(paymentStats.SuccessfulPurchases) / float64(paymentStats.TotalPurchases)) * 100
			stats.ByPayment[payment] = paymentStats
		}
	}

	// Calculate instance type success rates
	for instanceType, instanceStats := range stats.ByInstanceType {
		if instanceStats.TotalPurchases > 0 {
			instanceStats.SuccessRate = (float64(instanceStats.SuccessfulPurchases) / float64(instanceStats.TotalPurchases)) * 100
			stats.ByInstanceType[instanceType] = instanceStats
		}
	}
}

// Common error types for purchase operations
var (
	ErrOfferingNotFound    = fmt.Errorf("offering not found")
	ErrInsufficientQuota   = fmt.Errorf("insufficient quota")
	ErrInvalidPayment      = fmt.Errorf("invalid payment option")
	ErrRegionUnavailable   = fmt.Errorf("region unavailable")
	ErrInstanceUnavailable = fmt.Errorf("instance type unavailable")
)
