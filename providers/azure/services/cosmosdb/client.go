// Package cosmosdb provides Azure Cosmos DB Reserved Capacity client
package cosmosdb

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/consumption/armconsumption"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/cosmos/armcosmos/v2"
	"github.com/google/uuid"

	"github.com/LeanerCloud/CUDly/pkg/common"
	"github.com/LeanerCloud/CUDly/providers/azure/internal/httpclient"
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
	NextPage(ctx context.Context) (armconsumption.ReservationsDetailsClientListResponse, error)
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
		httpClient:     httpclient.New(),
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
	return common.ServiceNoSQL
}

// GetRegion returns the region
func (c *CosmosDBClient) GetRegion() string {
	return c.region
}

// AzureRetailPrice represents pricing information from Azure Retail Prices API
type AzureRetailPrice struct {
	Items []struct {
		CurrencyCode    string  `json:"currencyCode"`
		RetailPrice     float64 `json:"retailPrice"`
		UnitPrice       float64 `json:"unitPrice"`
		ArmRegionName   string  `json:"armRegionName"`
		Location        string  `json:"location"`
		MeterName       string  `json:"meterName"`
		SKUName         string  `json:"skuName"`
		ProductName     string  `json:"productName"`
		ServiceName     string  `json:"serviceName"`
		UnitOfMeasure   string  `json:"unitOfMeasure"`
		Type            string  `json:"type"`
		ArmSKUName      string  `json:"armSkuName"`
		ReservationTerm string  `json:"reservationTerm"`
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
	pager, err := c.createReservationsPager()
	if err != nil {
		log.Printf("WARNING: failed to create Cosmos DB reservations pager: %v", err)
		return []common.Commitment{}, nil
	}

	return c.collectCosmosReservations(ctx, pager)
}

// createReservationsPager creates a pager for listing reservations
func (c *CosmosDBClient) createReservationsPager() (ReservationsDetailsPager, error) {
	// Use injected pager if available (for testing)
	if c.reservationsPager != nil {
		return c.reservationsPager, nil
	}

	client, err := armconsumption.NewReservationsDetailsClient(c.cred, nil)
	if err != nil {
		return nil, err
	}

	scope := fmt.Sprintf("subscriptions/%s", c.subscriptionID)
	return client.NewListPager(scope, &armconsumption.ReservationsDetailsClientListOptions{}), nil
}

// collectCosmosReservations collects Cosmos DB reservations from the pager.
// Returns an error on first pagination failure so callers can't silently act
// on a partial list — see the compute client for the full rationale.
func (c *CosmosDBClient) collectCosmosReservations(ctx context.Context, pager ReservationsDetailsPager) ([]common.Commitment, error) {
	commitments := make([]common.Commitment, 0)

	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("cosmosdb: list reservations: %w", err)
		}

		for _, detail := range page.Value {
			if commitment := c.convertCosmosReservation(detail); commitment != nil {
				commitments = append(commitments, *commitment)
			}
		}
	}

	return commitments, nil
}

// convertCosmosReservation converts a reservation detail to a commitment if it's a Cosmos DB reservation
func (c *CosmosDBClient) convertCosmosReservation(detail *armconsumption.ReservationDetail) *common.Commitment {
	if detail.Properties == nil {
		return nil
	}

	props := detail.Properties
	if props.SKUName == nil || !strings.Contains(strings.ToLower(*props.SKUName), "cosmos") {
		return nil
	}

	commitment := &common.Commitment{
		Provider:       common.ProviderAzure,
		Account:        c.subscriptionID,
		CommitmentType: common.CommitmentReservedInstance,
		Service:        common.ServiceNoSQL,
		Region:         c.region,
		State:          "active",
	}

	if props.ReservationID != nil {
		commitment.CommitmentID = *props.ReservationID
	}
	if props.SKUName != nil {
		commitment.ResourceType = *props.SKUName
	}

	return commitment
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
	reservationOrderID := uuid.New().String()

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
	pager, err := c.createCosmosAccountsPager()
	if err != nil {
		return c.getCommonSKUs(), nil
	}

	skuSet := c.collectCapabilitiesFromAccounts(ctx, pager)

	// If we found SKUs from existing accounts, use those
	if len(skuSet) > 0 {
		return convertCapabilitySetToSlice(skuSet), nil
	}

	// Otherwise, return common SKU types that support reservations
	return c.getCommonSKUs(), nil
}

// createCosmosAccountsPager creates a pager for listing Cosmos DB accounts
func (c *CosmosDBClient) createCosmosAccountsPager() (CosmosAccountsPager, error) {
	// Use injected pager if available (for testing)
	if c.cosmosAccountsPager != nil {
		return c.cosmosAccountsPager, nil
	}

	client, err := armcosmos.NewDatabaseAccountsClient(c.subscriptionID, c.cred, nil)
	if err != nil {
		return nil, err
	}

	return client.NewListPager(nil), nil
}

// collectCapabilitiesFromAccounts collects capabilities from existing Cosmos DB accounts
func (c *CosmosDBClient) collectCapabilitiesFromAccounts(ctx context.Context, pager CosmosAccountsPager) map[string]bool {
	skuSet := make(map[string]bool)

	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			// If we can't list existing accounts, fall back to known SKU types
			break
		}

		for _, account := range page.Value {
			capabilities := extractCapabilitiesFromAccount(account)
			for _, capability := range capabilities {
				skuSet[capability] = true
			}
		}
	}

	return skuSet
}

// extractCapabilitiesFromAccount extracts capability names from a Cosmos DB account
func extractCapabilitiesFromAccount(account *armcosmos.DatabaseAccountGetResults) []string {
	if account.Properties == nil || account.Properties.Capabilities == nil {
		return nil
	}

	capabilities := make([]string, 0, len(account.Properties.Capabilities))
	for _, capability := range account.Properties.Capabilities {
		if capability.Name != nil {
			capabilities = append(capabilities, *capability.Name)
		}
	}

	return capabilities
}

// convertCapabilitySetToSlice converts a map of capabilities to a slice
func convertCapabilitySetToSlice(skuSet map[string]bool) []string {
	skus := make([]string, 0, len(skuSet))
	for sku := range skuSet {
		skus = append(skus, sku)
	}
	return skus
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
	filter := fmt.Sprintf("serviceName eq 'Azure Cosmos DB' and armRegionName eq '%s'", region)

	priceData, err := c.fetchAzurePricing(ctx, filter)
	if err != nil {
		return nil, err
	}

	if len(priceData.Items) == 0 {
		return nil, fmt.Errorf("no pricing data found for Cosmos DB in region %s", region)
	}

	onDemandPrice, reservationPrice, currency := extractCosmosPricing(priceData.Items, termYears)
	if onDemandPrice == 0 {
		return nil, fmt.Errorf("no on-demand pricing found for Cosmos DB")
	}

	hoursInTerm := 8760.0 * float64(termYears)
	if reservationPrice == 0 {
		reservationPrice = estimateCosmosReservationPrice(onDemandPrice, hoursInTerm)
	}

	savingsPercentage := calculateCosmosSavingsPercentage(onDemandPrice, hoursInTerm, reservationPrice)

	return &CosmosPricing{
		HourlyRate:        reservationPrice / hoursInTerm,
		ReservationPrice:  reservationPrice,
		OnDemandPrice:     onDemandPrice * hoursInTerm,
		Currency:          currency,
		SavingsPercentage: savingsPercentage,
	}, nil
}

// retailPricesMaxPages caps fetchAzurePricing's NextPageLink loop. The
// Azure Retail Prices API paginates at 100 items per page, so 50 pages
// covers 5000 items — more than any realistic SKU/region/term query.
const retailPricesMaxPages = 50

// fetchAzurePricing fetches pricing data from Azure Retail Prices API,
// following NextPageLink until exhausted or the safety cap fires.
func (c *CosmosDBClient) fetchAzurePricing(ctx context.Context, filter string) (*AzureRetailPrice, error) {
	baseURL := "https://prices.azure.com/api/retail/prices"
	params := url.Values{}
	params.Add("$filter", filter)
	params.Add("api-version", "2023-01-01-preview")

	combined := &AzureRetailPrice{}
	nextURL := baseURL + "?" + params.Encode()
	seen := map[string]struct{}{}

	for pageIdx := 0; pageIdx < retailPricesMaxPages && nextURL != ""; pageIdx++ {
		if _, ok := seen[nextURL]; ok {
			return nil, fmt.Errorf("pricing API returned a self-referential NextPageLink (page %d)", pageIdx)
		}
		seen[nextURL] = struct{}{}

		req, err := http.NewRequestWithContext(ctx, "GET", nextURL, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to create request (page %d): %w", pageIdx, err)
		}
		resp, err := c.httpClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("failed to call pricing API (page %d): %w", pageIdx, err)
		}
		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			return nil, fmt.Errorf("pricing API returned status %d (page %d): %s", resp.StatusCode, pageIdx, string(body))
		}

		var page AzureRetailPrice
		if err := json.NewDecoder(resp.Body).Decode(&page); err != nil {
			resp.Body.Close()
			return nil, fmt.Errorf("failed to decode pricing response (page %d): %w", pageIdx, err)
		}
		resp.Body.Close()

		combined.Items = append(combined.Items, page.Items...)
		nextURL = page.NextPageLink
	}

	return combined, nil
}

// extractCosmosPricing extracts on-demand and reservation pricing from price items
func extractCosmosPricing(items []struct {
	CurrencyCode    string  `json:"currencyCode"`
	RetailPrice     float64 `json:"retailPrice"`
	UnitPrice       float64 `json:"unitPrice"`
	ArmRegionName   string  `json:"armRegionName"`
	Location        string  `json:"location"`
	MeterName       string  `json:"meterName"`
	SKUName         string  `json:"skuName"`
	ProductName     string  `json:"productName"`
	ServiceName     string  `json:"serviceName"`
	UnitOfMeasure   string  `json:"unitOfMeasure"`
	Type            string  `json:"type"`
	ArmSKUName      string  `json:"armSkuName"`
	ReservationTerm string  `json:"reservationTerm"`
}, termYears int) (onDemand, reservation float64, currency string) {
	currency = "USD"
	termStr := fmt.Sprintf("%d Years", termYears)

	for _, item := range items {
		if item.CurrencyCode != "" {
			currency = item.CurrencyCode
		}

		if item.ReservationTerm != "" && item.ReservationTerm == termStr {
			reservation = item.RetailPrice
		} else if item.Type == "Consumption" {
			onDemand = item.UnitPrice
		}
	}

	return onDemand, reservation, currency
}

// estimateCosmosReservationPrice estimates reservation price when not available
func estimateCosmosReservationPrice(onDemandPrice, hoursInTerm float64) float64 {
	onDemandTotal := onDemandPrice * hoursInTerm
	// Azure Cosmos DB reservations typically offer 65% savings
	return onDemandTotal * 0.35
}

// calculateCosmosSavingsPercentage calculates the savings percentage
func calculateCosmosSavingsPercentage(onDemandPrice, hoursInTerm, reservationPrice float64) float64 {
	onDemandTotal := onDemandPrice * hoursInTerm
	return ((onDemandTotal - reservationPrice) / onDemandTotal) * 100
}

// convertAzureCosmosRecommendation converts Azure Cosmos DB reservation recommendation to common format
func (c *CosmosDBClient) convertAzureCosmosRecommendation(ctx context.Context, azureRec armconsumption.ReservationRecommendationClassification) *common.Recommendation {
	rec := &common.Recommendation{
		Provider:       common.ProviderAzure,
		Service:        common.ServiceNoSQL,
		Account:        c.subscriptionID,
		Region:         c.region,
		CommitmentType: common.CommitmentReservedInstance,
		Timestamp:      time.Now(),
		Term:           "1yr",
		PaymentOption:  "upfront",
	}

	return rec
}
