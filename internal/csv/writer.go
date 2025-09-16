package csv

import (
	"encoding/csv"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/LeanerCloud/rds-ri-purchase-tool/internal/purchase"
	"github.com/LeanerCloud/rds-ri-purchase-tool/internal/recommendations"
)

// Writer handles CSV output for purchase results and recommendations
type Writer struct {
	delimiter rune
}

// NewWriter creates a new CSV writer with default settings
func NewWriter() *Writer {
	return &Writer{
		delimiter: ',',
	}
}

// NewWriterWithDelimiter creates a new CSV writer with a custom delimiter
func NewWriterWithDelimiter(delimiter rune) *Writer {
	return &Writer{
		delimiter: delimiter,
	}
}

// WriteResults writes purchase results to a CSV file
func (w *Writer) WriteResults(results []purchase.Result, filename string) error {
	if filename == "" {
		return fmt.Errorf("filename is required - CSV output to stdout is not supported")
	}

	file, err := os.Create(filename)
	if err != nil {
		return fmt.Errorf("failed to create CSV file: %w", err)
	}
	defer file.Close()

	writer := csv.NewWriter(file)
	writer.Comma = w.delimiter
	defer writer.Flush()

	// Write header
	headers := []string{
		"Timestamp",
		"Status",
		"Region",
		"Engine",
		"Instance Type",
		"AZ Config",
		"Payment Option",
		"Term (months)",
		"Instance Count",
		"Purchase ID",
		"Reservation ID",
		"Actual Cost",
		"Estimated Cost",
		"Savings Percent",
		"Message",
		"Description",
	}

	if err := writer.Write(headers); err != nil {
		return fmt.Errorf("failed to write CSV headers: %w", err)
	}

	// Write data rows
	for _, result := range results {
		row := w.resultToRow(result)
		if err := writer.Write(row); err != nil {
			return fmt.Errorf("failed to write CSV row: %w", err)
		}
	}

	return nil
}

// WriteRecommendations writes recommendations to a CSV file
func (w *Writer) WriteRecommendations(recommendations []recommendations.Recommendation, filename string) error {
	if filename == "" {
		return fmt.Errorf("filename is required - CSV output to stdout is not supported")
	}

	file, err := os.Create(filename)
	if err != nil {
		return fmt.Errorf("failed to create CSV file: %w", err)
	}
	defer file.Close()

	writer := csv.NewWriter(file)
	writer.Comma = w.delimiter
	defer writer.Flush()

	// Write header
	headers := []string{
		"Timestamp",
		"Region",
		"Engine",
		"Instance Type",
		"AZ Config",
		"Payment Option",
		"Term (months)",
		"Recommended Count",
		"Estimated Monthly Cost",
		"Savings Percent",
		"Annual Savings",
		"Total Term Savings",
		"Description",
	}

	if err := writer.Write(headers); err != nil {
		return fmt.Errorf("failed to write CSV headers: %w", err)
	}

	// Write data rows
	for _, rec := range recommendations {
		row := w.recommendationToRow(rec)
		if err := writer.Write(row); err != nil {
			return fmt.Errorf("failed to write CSV row: %w", err)
		}
	}

	return nil
}

// WriteCostEstimates writes cost estimates to a CSV file
func (w *Writer) WriteCostEstimates(estimates []purchase.CostEstimate, filename string) error {
	if filename == "" {
		return fmt.Errorf("filename is required - CSV output to stdout is not supported")
	}

	file, err := os.Create(filename)
	if err != nil {
		return fmt.Errorf("failed to create CSV file: %w", err)
	}
	defer file.Close()

	writer := csv.NewWriter(file)
	writer.Comma = w.delimiter
	defer writer.Flush()

	// Write header
	headers := []string{
		"Region",
		"Engine",
		"Instance Type",
		"AZ Config",
		"Payment Option",
		"Term (months)",
		"Instance Count",
		"Offering ID",
		"Fixed Price Per Instance",
		"Usage Price Per Hour",
		"Total Fixed Cost",
		"Monthly Usage Cost",
		"Total Term Cost",
		"Currency",
		"Error",
	}

	if err := writer.Write(headers); err != nil {
		return fmt.Errorf("failed to write CSV headers: %w", err)
	}

	// Write data rows
	for _, estimate := range estimates {
		row := w.costEstimateToRow(estimate)
		if err := writer.Write(row); err != nil {
			return fmt.Errorf("failed to write CSV row: %w", err)
		}
	}

	return nil
}

// WritePurchaseStats writes purchase statistics to a CSV file
func (w *Writer) WritePurchaseStats(stats purchase.PurchaseStats, filename string) error {
	if filename == "" {
		return fmt.Errorf("filename is required - CSV output to stdout is not supported")
	}

	file, err := os.Create(filename)
	if err != nil {
		return fmt.Errorf("failed to create CSV file: %w", err)
	}
	defer file.Close()

	writer := csv.NewWriter(file)
	writer.Comma = w.delimiter
	defer writer.Flush()

	// Write overall stats
	if err := w.writeOverallStats(writer, stats.TotalStats); err != nil {
		return err
	}

	// Write engine stats
	if err := w.writeEngineStats(writer, stats.ByEngine); err != nil {
		return err
	}

	// Write region stats
	if err := w.writeRegionStats(writer, stats.ByRegion); err != nil {
		return err
	}

	// Write payment option stats
	if err := w.writePaymentStats(writer, stats.ByPayment); err != nil {
		return err
	}

	// Write instance type stats
	if err := w.writeInstanceStats(writer, stats.ByInstanceType); err != nil {
		return err
	}

	return nil
}

// Helper methods to convert data structures to CSV rows

func (w *Writer) resultToRow(result purchase.Result) []string {
	return []string{
		result.GetFormattedTimestamp(),
		result.GetStatusString(),
		result.Config.Region,
		result.Config.Engine,
		result.Config.InstanceType,
		result.Config.AZConfig,
		result.Config.PaymentOption,
		strconv.Itoa(int(result.Config.Term)),
		strconv.Itoa(int(result.Config.Count)),
		result.PurchaseID,
		result.ReservationID,
		result.GetCostString(),
		fmt.Sprintf("%.2f", result.Config.EstimatedCost),
		fmt.Sprintf("%.2f", result.Config.SavingsPercent),
		result.Message,
		result.Config.Description,
	}
}

func (w *Writer) recommendationToRow(rec recommendations.Recommendation) []string {
	return []string{
		rec.Timestamp.Format("2006-01-02 15:04:05"),
		rec.Region,
		rec.Engine,
		rec.InstanceType,
		rec.AZConfig,
		rec.PaymentOption,
		strconv.Itoa(int(rec.Term)),
		strconv.Itoa(int(rec.Count)),
		fmt.Sprintf("%.2f", rec.EstimatedCost),
		fmt.Sprintf("%.2f", rec.SavingsPercent),
		fmt.Sprintf("%.2f", rec.CalculateAnnualSavings()),
		fmt.Sprintf("%.2f", rec.CalculateTotalTermSavings()),
		rec.Description,
	}
}

func (w *Writer) costEstimateToRow(estimate purchase.CostEstimate) []string {
	row := []string{
		estimate.Recommendation.Region,
		estimate.Recommendation.Engine,
		estimate.Recommendation.InstanceType,
		estimate.Recommendation.AZConfig,
		estimate.Recommendation.PaymentOption,
		strconv.Itoa(int(estimate.Recommendation.Term)),
		strconv.Itoa(int(estimate.Recommendation.Count)),
	}

	if estimate.HasError() {
		// Add empty columns for offering details
		row = append(row, "", "", "", "", "", "", "")
		row = append(row, estimate.Error)
	} else {
		row = append(row,
			estimate.OfferingDetails.OfferingID,
			fmt.Sprintf("%.2f", estimate.OfferingDetails.FixedPrice),
			fmt.Sprintf("%.4f", estimate.OfferingDetails.UsagePrice),
			fmt.Sprintf("%.2f", estimate.TotalFixedCost),
			fmt.Sprintf("%.2f", estimate.MonthlyUsageCost),
			fmt.Sprintf("%.2f", estimate.TotalTermCost),
			estimate.OfferingDetails.CurrencyCode,
			"",
		)
	}

	return row
}

// Helper methods to write different types of statistics

func (w *Writer) writeOverallStats(writer *csv.Writer, stats purchase.TotalStats) error {
	// Write section header
	if err := writer.Write([]string{"OVERALL STATISTICS"}); err != nil {
		return err
	}

	headers := []string{"Metric", "Value"}
	if err := writer.Write(headers); err != nil {
		return err
	}

	rows := [][]string{
		{"Total Purchases", strconv.Itoa(stats.TotalPurchases)},
		{"Successful Purchases", strconv.Itoa(stats.SuccessfulPurchases)},
		{"Failed Purchases", strconv.Itoa(stats.FailedPurchases)},
		{"Total Instances", strconv.Itoa(int(stats.TotalInstances))},
		{"Total Cost", fmt.Sprintf("%.2f", stats.TotalCost)},
		{"Overall Success Rate", fmt.Sprintf("%.2f%%", stats.OverallSuccessRate)},
	}

	for _, row := range rows {
		if err := writer.Write(row); err != nil {
			return err
		}
	}

	// Add empty row for separation
	return writer.Write([]string{})
}

func (w *Writer) writeEngineStats(writer *csv.Writer, engineStats map[string]purchase.EngineStats) error {
	// Write section header
	if err := writer.Write([]string{"STATISTICS BY ENGINE"}); err != nil {
		return err
	}

	headers := []string{"Engine", "Total Purchases", "Successful", "Failed", "Total Instances", "Total Cost", "Success Rate"}
	if err := writer.Write(headers); err != nil {
		return err
	}

	for engine, stats := range engineStats {
		row := []string{
			engine,
			strconv.Itoa(stats.TotalPurchases),
			strconv.Itoa(stats.SuccessfulPurchases),
			strconv.Itoa(stats.FailedPurchases),
			strconv.Itoa(int(stats.TotalInstances)),
			fmt.Sprintf("%.2f", stats.TotalCost),
			fmt.Sprintf("%.2f%%", stats.SuccessRate),
		}
		if err := writer.Write(row); err != nil {
			return err
		}
	}

	// Add empty row for separation
	return writer.Write([]string{})
}

func (w *Writer) writeRegionStats(writer *csv.Writer, regionStats map[string]purchase.RegionStats) error {
	// Write section header
	if err := writer.Write([]string{"STATISTICS BY REGION"}); err != nil {
		return err
	}

	headers := []string{"Region", "Total Purchases", "Successful", "Failed", "Total Instances", "Total Cost", "Success Rate"}
	if err := writer.Write(headers); err != nil {
		return err
	}

	for region, stats := range regionStats {
		row := []string{
			region,
			strconv.Itoa(stats.TotalPurchases),
			strconv.Itoa(stats.SuccessfulPurchases),
			strconv.Itoa(stats.FailedPurchases),
			strconv.Itoa(int(stats.TotalInstances)),
			fmt.Sprintf("%.2f", stats.TotalCost),
			fmt.Sprintf("%.2f%%", stats.SuccessRate),
		}
		if err := writer.Write(row); err != nil {
			return err
		}
	}

	// Add empty row for separation
	return writer.Write([]string{})
}

func (w *Writer) writePaymentStats(writer *csv.Writer, paymentStats map[string]purchase.PaymentStats) error {
	// Write section header
	if err := writer.Write([]string{"STATISTICS BY PAYMENT OPTION"}); err != nil {
		return err
	}

	headers := []string{"Payment Option", "Total Purchases", "Successful", "Failed", "Total Instances", "Total Cost", "Success Rate"}
	if err := writer.Write(headers); err != nil {
		return err
	}

	for payment, stats := range paymentStats {
		row := []string{
			payment,
			strconv.Itoa(stats.TotalPurchases),
			strconv.Itoa(stats.SuccessfulPurchases),
			strconv.Itoa(stats.FailedPurchases),
			strconv.Itoa(int(stats.TotalInstances)),
			fmt.Sprintf("%.2f", stats.TotalCost),
			fmt.Sprintf("%.2f%%", stats.SuccessRate),
		}
		if err := writer.Write(row); err != nil {
			return err
		}
	}

	// Add empty row for separation
	return writer.Write([]string{})
}

func (w *Writer) writeInstanceStats(writer *csv.Writer, instanceStats map[string]purchase.InstanceStats) error {
	// Write section header
	if err := writer.Write([]string{"STATISTICS BY INSTANCE TYPE"}); err != nil {
		return err
	}

	headers := []string{"Instance Type", "Total Purchases", "Successful", "Failed", "Total Instances", "Total Cost", "Success Rate"}
	if err := writer.Write(headers); err != nil {
		return err
	}

	for instanceType, stats := range instanceStats {
		row := []string{
			instanceType,
			strconv.Itoa(stats.TotalPurchases),
			strconv.Itoa(stats.SuccessfulPurchases),
			strconv.Itoa(stats.FailedPurchases),
			strconv.Itoa(int(stats.TotalInstances)),
			fmt.Sprintf("%.2f", stats.TotalCost),
			fmt.Sprintf("%.2f%%", stats.SuccessRate),
		}
		if err := writer.Write(row); err != nil {
			return err
		}
	}

	return nil
}

// GenerateFilename generates a timestamped filename for CSV output
func GenerateFilename(prefix string) string {
	timestamp := time.Now().Format("20060102-150405")
	return fmt.Sprintf("%s_%s.csv", prefix, timestamp)
}

// ValidateCSVPath checks if the given path is valid for CSV output
func ValidateCSVPath(path string) error {
	if path == "" {
		return fmt.Errorf("file path cannot be empty")
	}

	// Check if the path ends with .csv
	if !strings.HasSuffix(strings.ToLower(path), ".csv") {
		return fmt.Errorf("file path must end with .csv extension")
	}

	// Check if we can create the file (this will also validate the directory exists)
	file, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("cannot create file at path %s: %w", path, err)
	}

	// Clean up the test file
	file.Close()
	os.Remove(path)

	return nil
}
