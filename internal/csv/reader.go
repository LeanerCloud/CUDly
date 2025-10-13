package csv

import (
	"encoding/csv"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/LeanerCloud/rds-ri-purchase-tool/internal/common"
)

// Reader handles CSV input for recommendations
type Reader struct {
	delimiter rune
}

// NewReader creates a new CSV reader with default settings
func NewReader() *Reader {
	return &Reader{
		delimiter: ',',
	}
}

// NewReaderWithDelimiter creates a new CSV reader with a custom delimiter
func NewReaderWithDelimiter(delimiter rune) *Reader {
	return &Reader{
		delimiter: delimiter,
	}
}

// ReadRecommendations reads recommendations from a CSV file
func (r *Reader) ReadRecommendations(filename string) ([]common.Recommendation, error) {
	file, err := os.Open(filename)
	if err != nil {
		return nil, fmt.Errorf("failed to open CSV file: %w", err)
	}
	defer file.Close()

	reader := csv.NewReader(file)
	reader.Comma = r.delimiter

	// Read header
	headers, err := reader.Read()
	if err != nil {
		return nil, fmt.Errorf("failed to read CSV headers: %w", err)
	}

	// Create column index map
	columnIndex := make(map[string]int)
	for i, header := range headers {
		columnIndex[strings.TrimSpace(header)] = i
	}

	// Validate required columns
	requiredColumns := []string{
		"Region", "Engine", "Instance Type", "Payment Option",
		"Term (months)", "Instance Count",
	}
	for _, col := range requiredColumns {
		if _, ok := columnIndex[col]; !ok {
			return nil, fmt.Errorf("missing required column: %s", col)
		}
	}

	recommendations := make([]common.Recommendation, 0)

	// Read data rows
	lineNum := 1 // Start at 1 since we already read the header
	for {
		record, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("failed to read CSV row at line %d: %w", lineNum+1, err)
		}
		lineNum++

		rec, err := r.rowToRecommendation(record, columnIndex, lineNum)
		if err != nil {
			return nil, fmt.Errorf("failed to parse row %d: %w", lineNum, err)
		}

		recommendations = append(recommendations, rec)
	}

	return recommendations, nil
}

// rowToRecommendation converts a CSV row to a Recommendation
func (r *Reader) rowToRecommendation(row []string, columnIndex map[string]int, lineNum int) (common.Recommendation, error) {
	// Helper function to safely get column value
	getColumn := func(name string) string {
		if idx, ok := columnIndex[name]; ok && idx < len(row) {
			return strings.TrimSpace(row[idx])
		}
		return ""
	}

	// Helper function to parse int32
	parseInt32 := func(s string) (int32, error) {
		val, err := strconv.ParseInt(s, 10, 32)
		return int32(val), err
	}

	// Helper function to parse float64
	parseFloat := func(s string) (float64, error) {
		if s == "" || s == "N/A" {
			return 0, nil
		}
		return strconv.ParseFloat(s, 64)
	}

	// Parse required fields
	region := getColumn("Region")
	if region == "" {
		return common.Recommendation{}, fmt.Errorf("missing Region value")
	}

	engine := getColumn("Engine")
	if engine == "" {
		return common.Recommendation{}, fmt.Errorf("missing Engine value")
	}

	instanceType := getColumn("Instance Type")
	if instanceType == "" {
		return common.Recommendation{}, fmt.Errorf("missing Instance Type value")
	}

	paymentOption := getColumn("Payment Option")
	if paymentOption == "" {
		return common.Recommendation{}, fmt.Errorf("missing Payment Option value")
	}

	termStr := getColumn("Term (months)")
	if termStr == "" {
		return common.Recommendation{}, fmt.Errorf("missing Term (months) value")
	}
	term, err := parseInt32(termStr)
	if err != nil {
		return common.Recommendation{}, fmt.Errorf("invalid Term (months) value: %s", termStr)
	}

	countStr := getColumn("Instance Count")
	if countStr == "" {
		return common.Recommendation{}, fmt.Errorf("missing Instance Count value")
	}
	count, err := parseInt32(countStr)
	if err != nil {
		return common.Recommendation{}, fmt.Errorf("invalid Instance Count value: %s", countStr)
	}

	// Parse optional fields
	azConfig := getColumn("AZ Config")

	// Parse cost fields
	savingsPercentStr := getColumn("Savings Percent")
	savingsPercent, _ := parseFloat(savingsPercentStr)

	upfrontCostStr := getColumn("Total Upfront (all instances)")
	upfrontCost, _ := parseFloat(upfrontCostStr)

	riMonthlyCostStr := getColumn("RI Monthly Cost")
	riMonthlyCost, _ := parseFloat(riMonthlyCostStr)

	description := getColumn("Description")

	// Determine service type from engine or instance type
	service := determineServiceType(engine, instanceType)

	// Create service-specific details
	var serviceDetails common.ServiceDetails
	switch service {
	case common.ServiceRDS:
		serviceDetails = &common.RDSDetails{
			Engine:   engine,
			AZConfig: azConfig,
		}
	case common.ServiceElastiCache:
		serviceDetails = &common.ElastiCacheDetails{
			Engine:   engine,
			NodeType: instanceType,
		}
	case common.ServiceEC2:
		serviceDetails = &common.EC2Details{
			Platform: engine,
			Tenancy:  azConfig,
			Scope:    "region",
		}
	case common.ServiceOpenSearch, common.ServiceElasticsearch:
		serviceDetails = &common.OpenSearchDetails{
			InstanceType:  instanceType,
			InstanceCount: count,
		}
	case common.ServiceRedshift:
		serviceDetails = &common.RedshiftDetails{
			NodeType:      instanceType,
			NumberOfNodes: count,
			ClusterType:   azConfig,
		}
	case common.ServiceMemoryDB:
		serviceDetails = &common.MemoryDBDetails{
			NodeType:      instanceType,
			NumberOfNodes: count,
		}
	}

	// Calculate estimated cost (monthly savings)
	// From the CSV writer, we know that EstimatedCost is actually the monthly savings
	// We can derive it from the RI monthly cost and savings percent if available
	estimatedCost := riMonthlyCost
	if savingsPercent > 0 && riMonthlyCost > 0 {
		// If RI monthly cost = On-Demand - Savings
		// And Savings = On-Demand * (SavingsPercent / 100)
		// Then: RI = On-Demand * (1 - SavingsPercent/100)
		// So: On-Demand = RI / (1 - SavingsPercent/100)
		// And: Savings = On-Demand - RI
		onDemandCost := riMonthlyCost / (1 - (savingsPercent / 100))
		estimatedCost = onDemandCost - riMonthlyCost
	}

	// Calculate recurring monthly cost
	// For partial-upfront and no-upfront, the RI monthly cost is the recurring cost
	recurringMonthlyCost := riMonthlyCost
	if paymentOption == "all-upfront" {
		recurringMonthlyCost = 0
	}

	rec := common.Recommendation{
		Service:               service,
		Region:                region,
		InstanceType:          instanceType,
		Count:                 count,
		PaymentOption:         paymentOption,
		Term:                  int(term),
		EstimatedCost:         estimatedCost,
		SavingsPercent:        savingsPercent,
		Timestamp:             time.Now(),
		Description:           description,
		UpfrontCost:           upfrontCost,
		RecurringMonthlyCost:  recurringMonthlyCost,
		EstimatedMonthlyOnDemand: estimatedCost + riMonthlyCost,
		ServiceDetails:        serviceDetails,
	}

	return rec, nil
}

// determineServiceType determines the AWS service type based on engine and instance type
func determineServiceType(engine, instanceType string) common.ServiceType {
	engineLower := strings.ToLower(engine)
	instanceLower := strings.ToLower(instanceType)

	// Check for ElastiCache
	if strings.Contains(engineLower, "redis") ||
	   strings.Contains(engineLower, "memcached") ||
	   strings.Contains(engineLower, "valkey") {
		return common.ServiceElastiCache
	}

	// Check for RDS engines
	if strings.Contains(engineLower, "aurora") ||
	   strings.Contains(engineLower, "mysql") ||
	   strings.Contains(engineLower, "postgres") ||
	   strings.Contains(engineLower, "mariadb") ||
	   strings.Contains(engineLower, "oracle") ||
	   strings.Contains(engineLower, "sqlserver") {
		return common.ServiceRDS
	}

	// Check for OpenSearch (before EC2 check as it may have r5 instances)
	if strings.Contains(engineLower, "opensearch") ||
	   strings.Contains(engineLower, "elasticsearch") {
		return common.ServiceOpenSearch
	}

	// Check for EC2 by instance prefix
	if strings.HasPrefix(instanceLower, "m5.") ||
	   strings.HasPrefix(instanceLower, "c5.") ||
	   strings.HasPrefix(instanceLower, "r5.") ||
	   strings.HasPrefix(instanceLower, "t3.") ||
	   !strings.Contains(instanceLower, ".") {
		return common.ServiceEC2
	}

	// Check for Redshift
	if strings.Contains(engineLower, "redshift") ||
	   strings.HasPrefix(instanceLower, "dc2.") ||
	   strings.HasPrefix(instanceLower, "ra3.") {
		return common.ServiceRedshift
	}

	// Check for MemoryDB
	if strings.Contains(engineLower, "memorydb") {
		return common.ServiceMemoryDB
	}

	// Check by instance type prefix
	if strings.HasPrefix(instanceLower, "db.") {
		return common.ServiceRDS
	}
	if strings.HasPrefix(instanceLower, "cache.") {
		return common.ServiceElastiCache
	}

	// Default to RDS if uncertain (most common case)
	return common.ServiceRDS
}
