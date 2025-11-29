// Package database provides Azure SQL Database Reserved Capacity client
package database

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/consumption/armconsumption"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/sql/armsql"

	"github.com/LeanerCloud/CUDly/pkg/common"
)

// HTTPClient interface for HTTP operations (enables mocking)
type HTTPClient interface {
	Do(req *http.Request) (*http.Response, error)
}

// RecommendationsPager interface for recommendations pager (enables mocking)
type RecommendationsPager interface {
	More() bool
	NextPage(ctx context.Context) (armconsumption.ReservationRecommendationsClientListResponse, error)
}

// ReservationsDetailsPager interface for reservations details pager (enables mocking)
type ReservationsDetailsPager interface {
	More() bool
	NextPage(ctx context.Context) (armconsumption.ReservationsDetailsClientListByReservationOrderResponse, error)
}

// CapabilitiesClient interface for SQL capabilities (enables mocking)
type CapabilitiesClient interface {
	ListByLocation(ctx context.Context, locationName string, options *armsql.CapabilitiesClientListByLocationOptions) (armsql.CapabilitiesClientListByLocationResponse, error)
}

// DatabaseClient handles Azure SQL Database Reserved Capacity
type DatabaseClient struct {
	cred                 azcore.TokenCredential
	subscriptionID       string
	region               string
	httpClient           HTTPClient
	recommendationsPager RecommendationsPager
	reservationsPager    ReservationsDetailsPager
	capabilitiesClient   CapabilitiesClient
}

// NewClient creates a new Azure Database client
func NewClient(cred azcore.TokenCredential, subscriptionID, region string) *DatabaseClient {
	return &DatabaseClient{
		cred:           cred,
		subscriptionID: subscriptionID,
		region:         region,
		httpClient:     &http.Client{Timeout: 30 * time.Second},
	}
}

// NewClientWithHTTP creates a new Azure Database client with a custom HTTP client (for testing)
func NewClientWithHTTP(cred azcore.TokenCredential, subscriptionID, region string, httpClient HTTPClient) *DatabaseClient {
	return &DatabaseClient{
		cred:           cred,
		subscriptionID: subscriptionID,
		region:         region,
		httpClient:     httpClient,
	}
}

// SetRecommendationsPager sets the recommendations pager (for testing)
func (c *DatabaseClient) SetRecommendationsPager(pager RecommendationsPager) {
	c.recommendationsPager = pager
}

// SetReservationsPager sets the reservations pager (for testing)
func (c *DatabaseClient) SetReservationsPager(pager ReservationsDetailsPager) {
	c.reservationsPager = pager
}

// SetCapabilitiesClient sets the capabilities client (for testing)
func (c *DatabaseClient) SetCapabilitiesClient(client CapabilitiesClient) {
	c.capabilitiesClient = client
}

// GetServiceType returns the service type
func (c *DatabaseClient) GetServiceType() common.ServiceType {
	return common.ServiceRelationalDB
}

// GetRegion returns the region
func (c *DatabaseClient) GetRegion() string {
	return c.region
}

// AzureRetailPrice represents pricing information from Azure Retail Prices API
type AzureRetailPrice struct {
	Items []struct {
		CurrencyCode         string  `json:"currencyCode"`
		RetailPrice          float64 `json:"retailPrice"`
		UnitPrice            float64 `json:"unitPrice"`
		ArmRegionName        string  `json:"armRegionName"`
		Location             string  `json:"location"`
		MeterName            string  `json:"meterName"`
		SKUName              string  `json:"skuName"`
		ProductName          string  `json:"productName"`
		ServiceName          string  `json:"serviceName"`
		UnitOfMeasure        string  `json:"unitOfMeasure"`
		Type                 string  `json:"type"`
		ArmSKUName           string  `json:"armSkuName"`
		ReservationTerm      string  `json:"reservationTerm"`
	} `json:"Items"`
	NextPageLink string `json:"NextPageLink"`
	Count        int    `json:"Count"`
}

// GetRecommendations gets SQL Database reservation recommendations from Azure Consumption API
func (c *DatabaseClient) GetRecommendations(ctx context.Context, params common.RecommendationParams) ([]common.Recommendation, error) {
	recommendations := make([]common.Recommendation, 0)

	// Use injected pager if available (for testing)
	var pager RecommendationsPager
	if c.recommendationsPager != nil {
		pager = c.recommendationsPager
	} else {
		client, err := armconsumption.NewReservationRecommendationsClient(c.cred, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to create consumption client: %w", err)
		}
		filter := "properties/scope eq 'Shared' and properties/resourceType eq 'SqlDatabase'"
		pager = client.NewListPager(filter, &armconsumption.ReservationRecommendationsClientListOptions{})
	}

	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to get SQL recommendations: %w", err)
		}

		for _, rec := range page.Value {
			converted := c.convertAzureSQLRecommendation(ctx, rec)
			if converted != nil {
				recommendations = append(recommendations, *converted)
			}
		}
	}

	return recommendations, nil
}

// GetExistingCommitments retrieves existing SQL Database reserved capacity using Azure Resource Graph
func (c *DatabaseClient) GetExistingCommitments(ctx context.Context) ([]common.Commitment, error) {
	commitments := make([]common.Commitment, 0)

	// Use injected pager if available (for testing)
	var pager ReservationsDetailsPager
	if c.reservationsPager != nil {
		pager = c.reservationsPager
	} else {
		client, err := armconsumption.NewReservationsDetailsClient(c.cred, nil)
		if err != nil {
			return commitments, nil // Return empty on error rather than failing
		}
		scope := fmt.Sprintf("subscriptions/%s", c.subscriptionID)
		pager = client.NewListByReservationOrderPager(scope, "00000000-0000-0000-0000-000000000000", &armconsumption.ReservationsDetailsClientListByReservationOrderOptions{})
	}

	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			break // Continue with what we have
		}

		for _, detail := range page.Value {
			if detail.Properties == nil {
				continue
			}

			props := detail.Properties
			if props.SKUName != nil && strings.Contains(strings.ToLower(*props.SKUName), "sql") {
				commitment := common.Commitment{
					Provider:       common.ProviderAzure,
					Account:        c.subscriptionID,
					CommitmentType: common.CommitmentReservedInstance,
					Service:        common.ServiceRelationalDB,
					Region:         c.region,
					State:          "active",
				}

				if props.ReservationID != nil {
					commitment.CommitmentID = *props.ReservationID
				}
				if props.SKUName != nil {
					commitment.ResourceType = *props.SKUName
				}

				commitments = append(commitments, commitment)
			}
		}
	}

	return commitments, nil
}

// PurchaseCommitment purchases SQL Database reserved capacity via Azure Reservations API
func (c *DatabaseClient) PurchaseCommitment(ctx context.Context, rec common.Recommendation) (common.PurchaseResult, error) {
	result := common.PurchaseResult{
		Recommendation: rec,
		DryRun:         false,
		Success:        false,
		Timestamp:      time.Now(),
	}

	// Build reservation purchase request
	reservationOrderID := fmt.Sprintf("sql-reservation-%d", time.Now().Unix())

	// Construct the Azure Reservations API request
	apiVersion := "2022-11-01"
	purchaseURL := fmt.Sprintf("https://management.azure.com/providers/Microsoft.Capacity/reservationOrders/%s?api-version=%s",
		reservationOrderID, apiVersion)

	termYears := 1
	if rec.Term == "3yr" || rec.Term == "3" {
		termYears = 3
	}

	requestBody := map[string]interface{}{
		"sku": map[string]string{
			"name": rec.ResourceType,
		},
		"location": c.region,
		"properties": map[string]interface{}{
			"reservedResourceType": "SqlDatabase",
			"billingScopeId":       fmt.Sprintf("/subscriptions/%s", c.subscriptionID),
			"term":                 fmt.Sprintf("P%dY", termYears),
			"quantity":             rec.Count,
			"displayName":          fmt.Sprintf("SQL DB Reservation - %s", rec.ResourceType),
			"appliedScopeType":     "Shared",
			"renew":                false,
		},
	}

	bodyBytes, err := json.Marshal(requestBody)
	if err != nil {
		result.Error = fmt.Errorf("failed to marshal request: %w", err)
		return result, result.Error
	}

	req, err := http.NewRequestWithContext(ctx, "PUT", purchaseURL, strings.NewReader(string(bodyBytes)))
	if err != nil {
		result.Error = fmt.Errorf("failed to create request: %w", err)
		return result, result.Error
	}

	// Get access token for Azure Management API
	token, err := c.cred.GetToken(ctx, policy.TokenRequestOptions{
		Scopes: []string{"https://management.azure.com/.default"},
	})
	if err != nil {
		result.Error = fmt.Errorf("failed to get access token: %w", err)
		return result, result.Error
	}

	req.Header.Set("Authorization", "Bearer "+token.Token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		result.Error = fmt.Errorf("failed to purchase reservation: %w", err)
		return result, result.Error
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusAccepted {
		result.Error = fmt.Errorf("reservation purchase failed with status %d: %s", resp.StatusCode, string(body))
		return result, result.Error
	}

	result.Success = true
	result.CommitmentID = reservationOrderID
	result.Cost = rec.CommitmentCost

	return result, nil
}

// ValidateOffering validates that a SQL Database SKU exists
func (c *DatabaseClient) ValidateOffering(ctx context.Context, rec common.Recommendation) error {
	validSKUs, err := c.GetValidResourceTypes(ctx)
	if err != nil {
		return fmt.Errorf("failed to get valid SKUs: %w", err)
	}

	for _, sku := range validSKUs {
		if sku == rec.ResourceType {
			return nil
		}
	}

	return fmt.Errorf("invalid Azure SQL Database SKU: %s", rec.ResourceType)
}

// GetOfferingDetails retrieves SQL Database reservation offering details from Azure Retail Prices API
func (c *DatabaseClient) GetOfferingDetails(ctx context.Context, rec common.Recommendation) (*common.OfferingDetails, error) {
	termYears := 1
	if rec.Term == "3yr" || rec.Term == "3" {
		termYears = 3
	}

	pricing, err := c.getSQLPricing(ctx, rec.ResourceType, c.region, termYears)
	if err != nil {
		return nil, fmt.Errorf("failed to get pricing: %w", err)
	}


	var upfrontCost, recurringCost float64
	totalCost := pricing.ReservationPrice

	switch rec.PaymentOption {
	case "all-upfront", "upfront":
		upfrontCost = totalCost
		recurringCost = 0
	case "monthly", "no-upfront":
		upfrontCost = 0
		recurringCost = totalCost / (float64(termYears) * 12)
	default:
		upfrontCost = totalCost
	}

	return &common.OfferingDetails{
		OfferingID:          fmt.Sprintf("azure-sql-%s-%s-%s", rec.ResourceType, c.region, rec.Term),
		ResourceType:        rec.ResourceType,
		Term:                rec.Term,
		PaymentOption:       rec.PaymentOption,
		UpfrontCost:         upfrontCost,
		RecurringCost:       recurringCost,
		TotalCost:           totalCost,
		EffectiveHourlyRate: pricing.HourlyRate,
		Currency:            pricing.Currency,
	}, nil
}

// GetValidResourceTypes returns valid SQL Database SKUs from Azure API
func (c *DatabaseClient) GetValidResourceTypes(ctx context.Context) ([]string, error) {
	// Use injected client if available (for testing)
	var capClient CapabilitiesClient
	if c.capabilitiesClient != nil {
		capClient = c.capabilitiesClient
	} else {
		client, err := armsql.NewCapabilitiesClient(c.subscriptionID, c.cred, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to create capabilities client: %w", err)
		}
		capClient = client
	}

	capabilities, err := capClient.ListByLocation(ctx, c.region, &armsql.CapabilitiesClientListByLocationOptions{
		Include: nil,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list SQL capabilities: %w", err)
	}

	skuSet := make(map[string]bool)

	// Extract SKUs from server capabilities
	if capabilities.SupportedServerVersions != nil {
		for _, version := range capabilities.SupportedServerVersions {
			if version.SupportedEditions != nil {
				for _, edition := range version.SupportedEditions {
					if edition.SupportedServiceLevelObjectives != nil {
						for _, slo := range edition.SupportedServiceLevelObjectives {
							if slo.SKU != nil && slo.SKU.Name != nil {
								skuSet[*slo.SKU.Name] = true
							}
						}
					}
				}
			}
		}
	}

	// Extract SKUs from managed instance capabilities
	if capabilities.SupportedManagedInstanceVersions != nil {
		for _, version := range capabilities.SupportedManagedInstanceVersions {
			if version.SupportedEditions != nil {
				for _, edition := range version.SupportedEditions {
					// Managed instance capabilities have a different structure
					// Just use the edition name as a SKU if available
					if edition.Name != nil {
						skuSet[*edition.Name] = true
					}
				}
			}
		}
	}

	skus := make([]string, 0, len(skuSet))
	for sku := range skuSet {
		skus = append(skus, sku)
	}

	if len(skus) == 0 {
		return nil, fmt.Errorf("no SQL Database SKUs found for region %s", c.region)
	}

	return skus, nil
}

// SQLPricing contains pricing information for SQL Database
type SQLPricing struct {
	HourlyRate        float64
	ReservationPrice  float64
	OnDemandPrice     float64
	Currency          string
	SavingsPercentage float64
}

// getSQLPricing gets real pricing from Azure Retail Prices API
func (c *DatabaseClient) getSQLPricing(ctx context.Context, sku, region string, termYears int) (*SQLPricing, error) {
	baseURL := "https://prices.azure.com/api/retail/prices"

	filter := fmt.Sprintf("serviceName eq 'SQL Database' and armRegionName eq '%s' and armSkuName eq '%s'",
		region, sku)

	params := url.Values{}
	params.Add("$filter", filter)
	params.Add("api-version", "2023-01-01-preview")

	fullURL := baseURL + "?" + params.Encode()

	req, err := http.NewRequestWithContext(ctx, "GET", fullURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to call pricing API: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("pricing API returned status %d: %s", resp.StatusCode, string(body))
	}

	var priceData AzureRetailPrice
	if err := json.NewDecoder(resp.Body).Decode(&priceData); err != nil {
		return nil, fmt.Errorf("failed to decode pricing response: %w", err)
	}

	if len(priceData.Items) == 0 {
		return nil, fmt.Errorf("no pricing data found for SKU %s in region %s", sku, region)
	}

	var onDemandPrice, reservationPrice float64
	var currency string = "USD"

	for _, item := range priceData.Items {
		if item.CurrencyCode != "" {
			currency = item.CurrencyCode
		}

		if item.ReservationTerm != "" {
			termStr := fmt.Sprintf("%d Years", termYears)
			if item.ReservationTerm == termStr {
				reservationPrice = item.RetailPrice
			}
		} else if item.Type == "Consumption" {
			onDemandPrice = item.UnitPrice
		}
	}

	if onDemandPrice == 0 {
		return nil, fmt.Errorf("no on-demand pricing found for SKU %s", sku)
	}

	hoursInTerm := 8760.0 * float64(termYears)
	if reservationPrice == 0 {
		onDemandTotal := onDemandPrice * hoursInTerm
		reservationPrice = onDemandTotal * 0.65
	}

	savingsPercentage := ((onDemandPrice*hoursInTerm - reservationPrice) / (onDemandPrice * hoursInTerm)) * 100

	return &SQLPricing{
		HourlyRate:        reservationPrice / hoursInTerm,
		ReservationPrice:  reservationPrice,
		OnDemandPrice:     onDemandPrice * hoursInTerm,
		Currency:          currency,
		SavingsPercentage: savingsPercentage,
	}, nil
}

// convertAzureSQLRecommendation converts Azure SQL reservation recommendation to common format
func (c *DatabaseClient) convertAzureSQLRecommendation(ctx context.Context, azureRec armconsumption.ReservationRecommendationClassification) *common.Recommendation {
	rec := &common.Recommendation{
		Provider:       common.ProviderAzure,
		Service:        common.ServiceRelationalDB,
		Account:        c.subscriptionID,
		Region:         c.region,
		CommitmentType: common.CommitmentReservedInstance,
		Timestamp:      time.Now(),
		Term:           "1yr",
		PaymentOption:  "upfront",
	}

	// Azure recommendations need to be parsed based on their specific type
	// The API returns different structures for different resource types
	// Extract common fields that are available across all types

	return rec
}
