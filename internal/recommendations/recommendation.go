package recommendations

import (
	"fmt"
	"sort"
	"time"
)

// Recommendation represents an RDS Reserved Instance recommendation
type Recommendation struct {
	Region         string    `json:"region"`
	InstanceType   string    `json:"instance_type"`
	Engine         string    `json:"engine"`
	AZConfig       string    `json:"az_config"`
	PaymentOption  string    `json:"payment_option"`
	Term           int32     `json:"term"`
	Count          int32     `json:"count"`
	EstimatedCost  float64   `json:"estimated_cost"`
	SavingsPercent float64   `json:"savings_percent"`
	Description    string    `json:"description"`
	Timestamp      time.Time `json:"timestamp"`
	AccountID      string    `json:"account_id,omitempty"`
	AccountName    string    `json:"account_name,omitempty"`

	// AWS-provided cost details
	UpfrontCost               float64 `json:"upfront_cost"`
	RecurringMonthlyCost      float64 `json:"recurring_monthly_cost"`
	EstimatedMonthlyOnDemand  float64 `json:"estimated_monthly_on_demand"`
}

// RecommendationParams holds parameters for fetching recommendations
type RecommendationParams struct {
	Region             string `json:"region"`
	PaymentOption      string `json:"payment_option"`
	TermInYears        int    `json:"term_in_years"`
	LookbackPeriodDays int    `json:"lookback_period_days"`
	AccountID          string `json:"account_id,omitempty"`
}

// GenerateDescription creates a human-readable description for the recommendation
func (r *Recommendation) GenerateDescription() string {
	azConfig := "Single-AZ"
	if r.AZConfig == "multi-az" {
		azConfig = "Multi-AZ"
	}
	return fmt.Sprintf("%s %s %s", r.Engine, r.InstanceType, azConfig)
}

// Validate checks if the recommendation has all required fields
func (r *Recommendation) Validate() error {
	if r.Region == "" {
		return fmt.Errorf("region is required")
	}
	if r.InstanceType == "" {
		return fmt.Errorf("instance type is required")
	}
	if r.Engine == "" {
		return fmt.Errorf("engine is required")
	}
	if r.Count <= 0 {
		return fmt.Errorf("count must be greater than 0")
	}
	if r.AZConfig != "single-az" && r.AZConfig != "multi-az" {
		return fmt.Errorf("AZ config must be 'single-az' or 'multi-az'")
	}
	return nil
}

// GetDurationString returns the term duration as a string for AWS API
func (r *Recommendation) GetDurationString() string {
	switch r.Term {
	case 12:
		return "1yr"
	case 36:
		return "3yr"
	default:
		return "3yr"
	}
}

// GetMultiAZ returns true if the recommendation is for Multi-AZ deployment
func (r *Recommendation) GetMultiAZ() bool {
	return r.AZConfig == "multi-az"
}

// CalculateAnnualSavings calculates the estimated annual savings
func (r *Recommendation) CalculateAnnualSavings() float64 {
	return r.EstimatedCost * 12 // Monthly to annual
}

// CalculateTotalTermSavings calculates the total savings over the term
func (r *Recommendation) CalculateTotalTermSavings() float64 {
	years := float64(r.Term) / 12
	return r.CalculateAnnualSavings() * years
}

// RecommendationSummary provides aggregated information about a set of recommendations
type RecommendationSummary struct {
	TotalRecommendations int                        `json:"total_recommendations"`
	TotalInstances       int32                      `json:"total_instances"`
	TotalEstimatedCost   float64                    `json:"total_estimated_cost"`
	AverageSavings       float64                    `json:"average_savings"`
	ByEngine             map[string]EngineSummary   `json:"by_engine"`
	ByInstanceType       map[string]InstanceSummary `json:"by_instance_type"`
	ByRegion             map[string]RegionSummary   `json:"by_region"`
}

// EngineSummary provides summary information for a specific engine
type EngineSummary struct {
	Count         int32   `json:"count"`
	Instances     int32   `json:"instances"`
	EstimatedCost float64 `json:"estimated_cost"`
}

// InstanceSummary provides summary information for a specific instance type
type InstanceSummary struct {
	Count         int32   `json:"count"`
	Instances     int32   `json:"instances"`
	EstimatedCost float64 `json:"estimated_cost"`
}

// RegionSummary provides summary information for a specific region
type RegionSummary struct {
	Count         int32   `json:"count"`
	Instances     int32   `json:"instances"`
	EstimatedCost float64 `json:"estimated_cost"`
}

// SummarizeRecommendations creates a summary of the given recommendations
func SummarizeRecommendations(recommendations []Recommendation) RecommendationSummary {
	summary := RecommendationSummary{
		TotalRecommendations: len(recommendations),
		ByEngine:             make(map[string]EngineSummary),
		ByInstanceType:       make(map[string]InstanceSummary),
		ByRegion:             make(map[string]RegionSummary),
	}

	totalSavings := 0.0
	for _, rec := range recommendations {
		summary.TotalInstances += rec.Count
		summary.TotalEstimatedCost += rec.EstimatedCost
		totalSavings += rec.SavingsPercent

		// Update engine summary
		if engineSummary, exists := summary.ByEngine[rec.Engine]; exists {
			engineSummary.Count++
			engineSummary.Instances += rec.Count
			engineSummary.EstimatedCost += rec.EstimatedCost
			summary.ByEngine[rec.Engine] = engineSummary
		} else {
			summary.ByEngine[rec.Engine] = EngineSummary{
				Count:         1,
				Instances:     rec.Count,
				EstimatedCost: rec.EstimatedCost,
			}
		}

		// Update instance type summary
		if instanceSummary, exists := summary.ByInstanceType[rec.InstanceType]; exists {
			instanceSummary.Count++
			instanceSummary.Instances += rec.Count
			instanceSummary.EstimatedCost += rec.EstimatedCost
			summary.ByInstanceType[rec.InstanceType] = instanceSummary
		} else {
			summary.ByInstanceType[rec.InstanceType] = InstanceSummary{
				Count:         1,
				Instances:     rec.Count,
				EstimatedCost: rec.EstimatedCost,
			}
		}

		// Update region summary
		if regionSummary, exists := summary.ByRegion[rec.Region]; exists {
			regionSummary.Count++
			regionSummary.Instances += rec.Count
			regionSummary.EstimatedCost += rec.EstimatedCost
			summary.ByRegion[rec.Region] = regionSummary
		} else {
			summary.ByRegion[rec.Region] = RegionSummary{
				Count:         1,
				Instances:     rec.Count,
				EstimatedCost: rec.EstimatedCost,
			}
		}
	}

	if len(recommendations) > 0 {
		summary.AverageSavings = totalSavings / float64(len(recommendations))
	}

	return summary
}

// FilterRecommendations filters recommendations based on given criteria
func FilterRecommendations(recommendations []Recommendation, filter RecommendationFilter) []Recommendation {
	filtered := make([]Recommendation, 0, len(recommendations))

	for _, rec := range recommendations {
		if matchesFilter(rec, filter) {
			filtered = append(filtered, rec)
		}
	}

	return filtered
}

// RecommendationFilter defines criteria for filtering recommendations
type RecommendationFilter struct {
	Regions       []string `json:"regions,omitempty"`
	Engines       []string `json:"engines,omitempty"`
	InstanceTypes []string `json:"instance_types,omitempty"`
	MinSavings    float64  `json:"min_savings,omitempty"`
	MaxInstances  int32    `json:"max_instances,omitempty"`
	MinInstances  int32    `json:"min_instances,omitempty"`
	MultiAZOnly   bool     `json:"multi_az_only,omitempty"`
	SingleAZOnly  bool     `json:"single_az_only,omitempty"`
}

// matchesFilter checks if a recommendation matches the given filter
func matchesFilter(rec Recommendation, filter RecommendationFilter) bool {
	// Check regions
	if len(filter.Regions) > 0 && !contains(filter.Regions, rec.Region) {
		return false
	}

	// Check engines
	if len(filter.Engines) > 0 && !contains(filter.Engines, rec.Engine) {
		return false
	}

	// Check instance types
	if len(filter.InstanceTypes) > 0 && !contains(filter.InstanceTypes, rec.InstanceType) {
		return false
	}

	// Check minimum savings
	if filter.MinSavings > 0 && rec.SavingsPercent < filter.MinSavings {
		return false
	}

	// Check instance count limits
	if filter.MaxInstances > 0 && rec.Count > filter.MaxInstances {
		return false
	}
	if filter.MinInstances > 0 && rec.Count < filter.MinInstances {
		return false
	}

	// Check AZ configuration
	if filter.MultiAZOnly && !rec.GetMultiAZ() {
		return false
	}
	if filter.SingleAZOnly && rec.GetMultiAZ() {
		return false
	}

	return true
}

// SortRecommendations sorts recommendations by different criteria
func SortRecommendations(recommendations []Recommendation, sortBy string, ascending bool) {
	sort.Slice(recommendations, func(i, j int) bool {
		var less bool
		switch sortBy {
		case "savings":
			less = recommendations[i].SavingsPercent < recommendations[j].SavingsPercent
		case "cost":
			less = recommendations[i].EstimatedCost < recommendations[j].EstimatedCost
		case "instances":
			less = recommendations[i].Count < recommendations[j].Count
		case "engine":
			less = recommendations[i].Engine < recommendations[j].Engine
		case "instance_type":
			less = recommendations[i].InstanceType < recommendations[j].InstanceType
		case "region":
			less = recommendations[i].Region < recommendations[j].Region
		default:
			// Default sort by savings (descending)
			less = recommendations[i].SavingsPercent > recommendations[j].SavingsPercent
			ascending = true // Override for default case
		}

		if ascending {
			return less
		}
		return !less
	})
}

// ApplyCoveragePercentage applies a coverage percentage to recommendations
func ApplyCoveragePercentage(recommendations []Recommendation, coverage float64) []Recommendation {
	if coverage >= 100.0 {
		return recommendations
	}

	adjusted := make([]Recommendation, 0, len(recommendations))
	for _, rec := range recommendations {
		adjustedCount := int32(float64(rec.Count) * (coverage / 100.0))
		if adjustedCount > 0 {
			rec.Count = adjustedCount
			adjusted = append(adjusted, rec)
		}
	}

	return adjusted
}

// DefaultRecommendationParams returns default parameters for fetching recommendations
func DefaultRecommendationParams() RecommendationParams {
	return RecommendationParams{
		PaymentOption:      "partial-upfront",
		TermInYears:        3,
		LookbackPeriodDays: 7,
	}
}

// Helper function to check if a slice contains a string
func contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}
