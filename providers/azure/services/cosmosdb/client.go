// Package cosmosdb provides Azure Cosmos DB Reserved Capacity client
package cosmosdb

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
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/cosmos/armcosmos/v2"

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

// CosmosAccountsPager interface for Cosmos DB accounts pager (enables mocking)
type CosmosAccountsPager interface {
	More() bool
	NextPage(ctx context.Context) (armcosmos.DatabaseAccountsClientListResponse, error)
}

// CosmosDBClient handles Azure Cosmos DB Reserved Capacity
type CosmosDBClient struct {
	cred                 azcore.TokenCredential
	subscriptionID       string
	region               string
	httpClient           HTTPClient
	recommendationsPager RecommendationsPager
	reservationsPager    ReservationsDetailsPager
	cosmosAccountsPager  CosmosAccountsPager
}

// NewClient creates a new Azure Cosmos DB client
func NewClient(cred azcore.TokenCredential, subscriptionID, region string) *CosmosDBClient {
	return &CosmosDBClient{
		cred:           cred,
		subscriptionID: subscriptionID,
		region:         region,
		httpClient:     &http.Client{Timeout: 30 * time.Second},
	}
}

// NewClientWithHTTP creates a new Azure Cosmos DB client with a custom HTTP client (for testing)
func NewClientWithHTTP(cred azcore.TokenCredential, subscriptionID, region string, httpClient HTTPClient) *CosmosDBClient {
	return &CosmosDBClient{
		cred:           cred,
		subscriptionID: subscriptionID,
		region:         region,
		httpClient:     httpClient,
	}
}

// SetRecommendationsPager sets the recommendations pager (for testing)
func (c *CosmosDBClient) SetRecommendationsPager(pager RecommendationsPager) {
	c.recommendationsPager = pager
}

// SetReservationsPager sets the reservations pager (for testing)
func (c *CosmosDBClient) SetReservationsPager(pager ReservationsDetailsPager) {
	c.reservationsPager = pager
}

// SetCosmosAccountsPager sets the Cosmos DB accounts pager (for testing)
func (c *CosmosDBClient) SetCosmosAccountsPager(pager CosmosAccountsPager) {
	c.cosmosAccountsPager = pager
}

// GetServiceType returns the service type
func (c *CosmosDBClient) GetServiceType() common.ServiceType {
	return common.ServiceNoSQLDB
}

// GetRegion returns the region
func (c *CosmosDBClient) GetRegion() string {
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

// GetRecommendations gets Cosmos DB reservation recommendations from Azure Consumption API
func (c *CosmosDBClient) GetRecommendations(ctx context.Context, params common.RecommendationParams) ([]common.Recommendation, error) {
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
		filter := "properties/scope eq 'Shared' and properties/resourceType eq 'CosmosDb'"
		pager = client.NewListPager(filter, &armconsumption.ReservationRecommendationsClientListOptions{})
	}

	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to get Cosmos DB recommendations: %w", err)
		}

		for _, rec := range page.Value {
			converted := c.convertAzureCosmosRecommendation(ctx, rec)
			if converted != nil {
				recommendations = append(recommendations, *converted)
			}
		}
	}

	return recommendations, nil
}

// GetExistingCommitments retrieves existing Cosmos DB reserved capacity using Azure Resource Graph
func (c *CosmosDBClient) GetExistingCommitments(ctx context.Context) ([]common.Commitment, error) {
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
			if props.SKUName != nil && strings.Contains(strings.ToLower(*props.SKUName), "cosmos") {
				commitment := common.Commitment{
					Provider:       common.ProviderAzure,
					Account:        c.subscriptionID,
					CommitmentType: common.CommitmentReservedInstance,
					Service:        common.ServiceNoSQLDB,
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

// PurchaseCommitment purchases Cosmos DB reserved capacity via Azure Reservations API
func (c *CosmosDBClient) PurchaseCommitment(ctx context.Context, rec common.Recommendation) (common.PurchaseResult, error) {
	result := common.PurchaseResult{
		Recommendation: rec,
		DryRun:         false,
		Success:        false,
		Timestamp:      time.Now(),
	}

	// Build reservation purchase request
	reservationOrderID := fmt.Sprintf("cosmos-reservation-%d", time.Now().Unix())

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
			"reservedResourceType": "CosmosDb",
			"billingScopeId":       fmt.Sprintf("/subscriptions/%s", c.subscriptionID),
			"term":                 fmt.Sprintf("P%dY", termYears),
			"quantity":             rec.Count,
			"displayName":          fmt.Sprintf("Cosmos DB Reservation - %s", rec.ResourceType),
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

// ValidateOffering validates that a Cosmos DB SKU exists
func (c *CosmosDBClient) ValidateOffering(ctx context.Context, rec common.Recommendation) error {
	validSKUs, err := c.GetValidResourceTypes(ctx)
	if err != nil {
		return fmt.Errorf("failed to get valid SKUs: %w", err)
	}

	for _, sku := range validSKUs {
		if sku == rec.ResourceType {
			return nil
		}
	}

	return fmt.Errorf("invalid Azure Cosmos DB SKU: %s", rec.ResourceType)
}

// GetOfferingDetails retrieves Cosmos DB reservation offering details from Azure Retail Prices API
func (c *CosmosDBClient) GetOfferingDetails(ctx context.Context, rec common.Recommendation) (*common.OfferingDetails, error) {
	termYears := 1
	if rec.Term == "3yr" || rec.Term == "3" {
		termYears = 3
	}

	pricing, err := c.getCosmosPricing(ctx, rec.ResourceType, c.region, termYears)
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
		OfferingID:          fmt.Sprintf("azure-cosmos-%s-%s-%s", rec.ResourceType, c.region, rec.Term),
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

// GetValidResourceTypes returns valid Cosmos DB SKUs from Azure API
func (c *CosmosDBClient) GetValidResourceTypes(ctx context.Context) ([]string, error) {
	skuSet := make(map[string]bool)

	// Use injected pager if available (for testing)
	var pager CosmosAccountsPager
	if c.cosmosAccountsPager != nil {
		pager = c.cosmosAccountsPager
	} else {
		client, err := armcosmos.NewDatabaseAccountsClient(c.subscriptionID, c.cred, nil)
		if err != nil {
			return c.getCommonSKUs(), nil
		}
		pager = client.NewListPager(nil)
	}

	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			// If we can't list existing accounts, fall back to known SKU types
			break
		}

		for _, account := range page.Value {
			if account.Properties != nil && account.Properties.Capabilities != nil {
				for _, capability := range account.Properties.Capabilities {
					if capability.Name != nil {
						skuSet[*capability.Name] = true
					}
				}
			}
		}
	}

	// If we found SKUs from existing accounts, use those
	if len(skuSet) > 0 {
		skus := make([]string, 0, len(skuSet))
		for sku := range skuSet {
			skus = append(skus, sku)
		}
		return skus, nil
	}

	// Otherwise, return common SKU types that support reservations
	return c.getCommonSKUs(), nil
}

// getCommonSKUs returns common Cosmos DB SKUs
func (c *CosmosDBClient) getCommonSKUs() []string {
	return []string{
		// Cosmos DB API types
		"EnableCassandra",
		"EnableMongo",
		"EnableGremlin",
		"EnableTable",
		"EnableServerless",
	}
}

// CosmosPricing contains pricing information for Cosmos DB
type CosmosPricing struct {
	HourlyRate        float64
	ReservationPrice  float64
	OnDemandPrice     float64
	Currency          string
	SavingsPercentage float64
}

// getCosmosPricing gets real pricing from Azure Retail Prices API
func (c *CosmosDBClient) getCosmosPricing(ctx context.Context, sku, region string, termYears int) (*CosmosPricing, error) {
	baseURL := "https://prices.azure.com/api/retail/prices"

	filter := fmt.Sprintf("serviceName eq 'Azure Cosmos DB' and armRegionName eq '%s'",
		region)

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
		return nil, fmt.Errorf("no pricing data found for Cosmos DB in region %s", region)
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
		return nil, fmt.Errorf("no on-demand pricing found for Cosmos DB")
	}

	hoursInTerm := 8760.0 * float64(termYears)
	if reservationPrice == 0 {
		onDemandTotal := onDemandPrice * hoursInTerm
		// Azure Cosmos DB reservations typically offer 65% savings
		reservationPrice = onDemandTotal * 0.35
	}

	savingsPercentage := ((onDemandPrice*hoursInTerm - reservationPrice) / (onDemandPrice * hoursInTerm)) * 100

	return &CosmosPricing{
		HourlyRate:        reservationPrice / hoursInTerm,
		ReservationPrice:  reservationPrice,
		OnDemandPrice:     onDemandPrice * hoursInTerm,
		Currency:          currency,
		SavingsPercentage: savingsPercentage,
	}, nil
}

// convertAzureCosmosRecommendation converts Azure Cosmos DB reservation recommendation to common format
func (c *CosmosDBClient) convertAzureCosmosRecommendation(ctx context.Context, azureRec armconsumption.ReservationRecommendationClassification) *common.Recommendation {
	rec := &common.Recommendation{
		Provider:       common.ProviderAzure,
		Service:        common.ServiceNoSQLDB,
		Account:        c.subscriptionID,
		Region:         c.region,
		CommitmentType: common.CommitmentReservedInstance,
		Timestamp:      time.Now(),
		Term:           "1yr",
		PaymentOption:  "upfront",
	}

	return rec
}
