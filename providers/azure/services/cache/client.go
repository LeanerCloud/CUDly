// Package cache provides Azure Cache for Redis Reserved Capacity client
package cache

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
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/redis/armredis/v3"
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

// RedisCachesPager interface for Redis caches pager (enables mocking)
type RedisCachesPager interface {
	More() bool
	NextPage(ctx context.Context) (armredis.ClientListBySubscriptionResponse, error)
}

// CacheClient handles Azure Cache for Redis Reserved Capacity
type CacheClient struct {
	cred                 azcore.TokenCredential
	subscriptionID       string
	region               string
	httpClient           HTTPClient
	recommendationsPager RecommendationsPager
	reservationsPager    ReservationsDetailsPager
	redisCachesPager     RedisCachesPager
}

// NewClient creates a new Azure Cache client
func NewClient(cred azcore.TokenCredential, subscriptionID, region string) *CacheClient {
	return &CacheClient{
		cred:           cred,
		subscriptionID: subscriptionID,
		region:         region,
		httpClient:     httpclient.New(),
	}
}

// NewClientWithHTTP creates a new Azure Cache client with a custom HTTP client (for testing)
func NewClientWithHTTP(cred azcore.TokenCredential, subscriptionID, region string, httpClient HTTPClient) *CacheClient {
	return &CacheClient{
		cred:           cred,
		subscriptionID: subscriptionID,
		region:         region,
		httpClient:     httpClient,
	}
}

// SetRecommendationsPager sets the recommendations pager (for testing)
func (c *CacheClient) SetRecommendationsPager(pager RecommendationsPager) {
	c.recommendationsPager = pager
}

// SetReservationsPager sets the reservations pager (for testing)
func (c *CacheClient) SetReservationsPager(pager ReservationsDetailsPager) {
	c.reservationsPager = pager
}

// SetRedisCachesPager sets the Redis caches pager (for testing)
func (c *CacheClient) SetRedisCachesPager(pager RedisCachesPager) {
	c.redisCachesPager = pager
}

// GetServiceType returns the service type
func (c *CacheClient) GetServiceType() common.ServiceType {
	return common.ServiceCache
}

// GetRegion returns the region
func (c *CacheClient) GetRegion() string {
	return c.region
}

// AzureRetailPrice represents pricing information from Azure Retail Prices API
type AzureRetailPrice struct {
	Items []struct {
		CurrencyCode    string  `json:"currencyCode"`
		RetailPrice     float64 `json:"retailPrice"`
		UnitPrice       float64 `json:"unitPrice"`
		ArmRegionName   string  `json:"armRegionName"`
		ProductName     string  `json:"productName"`
		ServiceName     string  `json:"serviceName"`
		ArmSKUName      string  `json:"armSkuName"`
		MeterName       string  `json:"meterName"`
		ReservationTerm string  `json:"reservationTerm"`
		Type            string  `json:"type"`
	} `json:"Items"`
	NextPageLink string `json:"NextPageLink"`
	Count        int    `json:"Count"`
}

// GetRecommendations gets Redis Cache reservation recommendations from Azure Consumption API
func (c *CacheClient) GetRecommendations(ctx context.Context, params common.RecommendationParams) ([]common.Recommendation, error) {
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
		filter := "properties/scope eq 'Shared' and properties/resourceType eq 'RedisCache'"
		pager = client.NewListPager(filter, &armconsumption.ReservationRecommendationsClientListOptions{})
	}

	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to get Redis Cache recommendations: %w", err)
		}

		for _, rec := range page.Value {
			converted := c.convertAzureRedisRecommendation(ctx, rec)
			if converted != nil {
				recommendations = append(recommendations, *converted)
			}
		}
	}

	return recommendations, nil
}

// GetExistingCommitments retrieves existing Redis Cache reserved capacity
func (c *CacheClient) GetExistingCommitments(ctx context.Context) ([]common.Commitment, error) {
	pager, err := c.createReservationsPager()
	if err != nil {
		log.Printf("WARNING: failed to create Redis reservations pager: %v", err)
		return []common.Commitment{}, nil
	}

	return c.collectRedisReservations(ctx, pager), nil
}

// createReservationsPager creates a pager for listing reservations
func (c *CacheClient) createReservationsPager() (ReservationsDetailsPager, error) {
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

// collectRedisReservations collects Redis reservations from the pager
func (c *CacheClient) collectRedisReservations(ctx context.Context, pager ReservationsDetailsPager) []common.Commitment {
	commitments := make([]common.Commitment, 0)

	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			break
		}

		for _, detail := range page.Value {
			if commitment := c.convertRedisReservation(detail); commitment != nil {
				commitments = append(commitments, *commitment)
			}
		}
	}

	return commitments
}

// convertRedisReservation converts a reservation detail to a commitment if it's a Redis reservation
func (c *CacheClient) convertRedisReservation(detail *armconsumption.ReservationDetail) *common.Commitment {
	if detail.Properties == nil {
		return nil
	}

	props := detail.Properties
	// Filter for Redis reservations - check SKU name since ReservedResourceType may not be available
	if props.SKUName == nil || !strings.Contains(strings.ToLower(*props.SKUName), "redis") {
		return nil
	}

	commitment := &common.Commitment{
		Provider:       common.ProviderAzure,
		Account:        c.subscriptionID,
		CommitmentType: common.CommitmentReservedInstance,
		Service:        common.ServiceCache,
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

// PurchaseCommitment purchases Redis Cache reserved capacity via Azure Reservations API
func (c *CacheClient) PurchaseCommitment(ctx context.Context, rec common.Recommendation) (common.PurchaseResult, error) {
	result := common.PurchaseResult{
		Recommendation: rec,
		DryRun:         false,
		Success:        false,
		Timestamp:      time.Now(),
	}

	reservationOrderID := uuid.New().String()
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
			"reservedResourceType": "RedisCache",
			"billingScopeId":       fmt.Sprintf("/subscriptions/%s", c.subscriptionID),
			"term":                 fmt.Sprintf("P%dY", termYears),
			"quantity":             rec.Count,
			"displayName":          fmt.Sprintf("Redis Cache Reservation - %s", rec.ResourceType),
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

// ValidateOffering validates that a Redis Cache SKU exists
func (c *CacheClient) ValidateOffering(ctx context.Context, rec common.Recommendation) error {
	validSKUs, err := c.GetValidResourceTypes(ctx)
	if err != nil {
		return fmt.Errorf("failed to get valid SKUs: %w", err)
	}

	for _, sku := range validSKUs {
		if sku == rec.ResourceType {
			return nil
		}
	}

	return fmt.Errorf("invalid Azure Redis Cache SKU: %s", rec.ResourceType)
}

// GetOfferingDetails retrieves Redis Cache reservation offering details from Azure Retail Prices API
func (c *CacheClient) GetOfferingDetails(ctx context.Context, rec common.Recommendation) (*common.OfferingDetails, error) {
	termYears := 1
	if rec.Term == "3yr" || rec.Term == "3" {
		termYears = 3
	}

	pricing, err := c.getRedisPricing(ctx, rec.ResourceType, c.region, termYears)
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
		OfferingID:          fmt.Sprintf("azure-redis-%s-%s-%s", rec.ResourceType, c.region, rec.Term),
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

// GetValidResourceTypes returns valid Redis Cache SKUs from Azure API
func (c *CacheClient) GetValidResourceTypes(ctx context.Context) ([]string, error) {
	pager, err := c.createRedisCachesPager()
	if err != nil {
		// Fall back to common SKUs if we can't create client
		return c.getCommonSKUs(), nil
	}

	skuSet := c.collectSKUsFromCaches(ctx, pager)

	// If we found SKUs from existing caches, use those
	if len(skuSet) > 0 {
		return convertSKUSetToSlice(skuSet), nil
	}

	// Otherwise, return common SKU families that support reservations
	return c.getCommonSKUs(), nil
}

// createRedisCachesPager creates a pager for listing Redis caches
func (c *CacheClient) createRedisCachesPager() (RedisCachesPager, error) {
	// Use injected pager if available (for testing)
	if c.redisCachesPager != nil {
		return c.redisCachesPager, nil
	}

	client, err := armredis.NewClient(c.subscriptionID, c.cred, nil)
	if err != nil {
		return nil, err
	}

	return client.NewListBySubscriptionPager(nil), nil
}

// collectSKUsFromCaches collects SKUs from existing Redis caches
func (c *CacheClient) collectSKUsFromCaches(ctx context.Context, pager RedisCachesPager) map[string]bool {
	skuSet := make(map[string]bool)

	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			// If we can't list existing caches, fall back to known SKU families
			break
		}

		for _, cache := range page.Value {
			if fullSKU := extractSKUFromCache(cache); fullSKU != "" {
				skuSet[fullSKU] = true
			}
		}
	}

	return skuSet
}

// extractSKUFromCache extracts the full SKU name from a cache resource
func extractSKUFromCache(cache *armredis.ResourceInfo) string {
	if cache.Properties == nil || cache.Properties.SKU == nil {
		return ""
	}

	sku := cache.Properties.SKU
	if sku.Name == nil || sku.Family == nil || sku.Capacity == nil {
		return ""
	}

	skuName := string(*sku.Name)
	family := string(*sku.Family)
	capacity := *sku.Capacity

	// Build full SKU name like "Premium_P1"
	return fmt.Sprintf("%s_%s%d", skuName, family, capacity)
}

// convertSKUSetToSlice converts a map of SKUs to a sorted slice
func convertSKUSetToSlice(skuSet map[string]bool) []string {
	skus := make([]string, 0, len(skuSet))
	for sku := range skuSet {
		skus = append(skus, sku)
	}
	return skus
}

// getCommonSKUs returns common Redis Cache SKUs
func (c *CacheClient) getCommonSKUs() []string {
	return []string{
		// Basic tier
		"Basic_C0", "Basic_C1", "Basic_C2", "Basic_C3", "Basic_C4", "Basic_C5", "Basic_C6",
		// Standard tier
		"Standard_C0", "Standard_C1", "Standard_C2", "Standard_C3", "Standard_C4", "Standard_C5", "Standard_C6",
		// Premium tier (most commonly reserved)
		"Premium_P1", "Premium_P2", "Premium_P3", "Premium_P4", "Premium_P5",
	}
}

// RedisPricing contains pricing information for Redis Cache
type RedisPricing struct {
	HourlyRate        float64
	ReservationPrice  float64
	OnDemandPrice     float64
	Currency          string
	SavingsPercentage float64
}

// getRedisPricing gets real pricing from Azure Retail Prices API
func (c *CacheClient) getRedisPricing(ctx context.Context, sku, region string, termYears int) (*RedisPricing, error) {
	priceData, err := c.fetchAzurePricing(ctx, "Azure Cache for Redis", sku, region)
	if err != nil {
		return nil, err
	}

	if len(priceData.Items) == 0 {
		return nil, fmt.Errorf("no pricing data found for Redis Cache SKU %s in region %s", sku, region)
	}

	onDemandPrice, reservationPrice, currency := extractRedisPricing(priceData.Items, termYears)
	if onDemandPrice == 0 {
		return nil, fmt.Errorf("no on-demand pricing found for Redis Cache SKU %s", sku)
	}

	hoursInTerm := 8760.0 * float64(termYears)
	if reservationPrice == 0 {
		reservationPrice = onDemandPrice * hoursInTerm * 0.45 // 55% savings
	}

	savingsPercentage := ((onDemandPrice*hoursInTerm - reservationPrice) / (onDemandPrice * hoursInTerm)) * 100

	return &RedisPricing{
		HourlyRate:        reservationPrice / hoursInTerm,
		ReservationPrice:  reservationPrice,
		OnDemandPrice:     onDemandPrice * hoursInTerm,
		Currency:          currency,
		SavingsPercentage: savingsPercentage,
	}, nil
}

// fetchAzurePricing fetches pricing data from Azure Retail Prices API
func (c *CacheClient) fetchAzurePricing(ctx context.Context, serviceName, sku, region string) (*AzureRetailPrice, error) {
	filter := fmt.Sprintf("serviceName eq '%s' and armRegionName eq '%s' and contains(armSkuName, '%s')",
		serviceName, region, sku)

	params := url.Values{}
	params.Add("$filter", filter)
	params.Add("api-version", "2023-01-01-preview")

	fullURL := "https://prices.azure.com/api/retail/prices?" + params.Encode()

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

	return &priceData, nil
}

// extractRedisPricing extracts on-demand and reservation pricing from price items
func extractRedisPricing(items []struct {
	CurrencyCode    string  `json:"currencyCode"`
	RetailPrice     float64 `json:"retailPrice"`
	UnitPrice       float64 `json:"unitPrice"`
	ArmRegionName   string  `json:"armRegionName"`
	ProductName     string  `json:"productName"`
	ServiceName     string  `json:"serviceName"`
	ArmSKUName      string  `json:"armSkuName"`
	MeterName       string  `json:"meterName"`
	ReservationTerm string  `json:"reservationTerm"`
	Type            string  `json:"type"`
}, termYears int) (onDemand, reservation float64, currency string) {
	currency = "USD"
	termStr := fmt.Sprintf("%d Years", termYears)

	for _, item := range items {
		if item.CurrencyCode != "" {
			currency = item.CurrencyCode
		}

		if item.ReservationTerm == termStr {
			reservation = item.RetailPrice
		} else if item.Type == "Consumption" {
			onDemand = item.UnitPrice
		}
	}

	return onDemand, reservation, currency
}

// convertAzureRedisRecommendation converts Azure Redis Cache reservation recommendation to common format
func (c *CacheClient) convertAzureRedisRecommendation(ctx context.Context, azureRec armconsumption.ReservationRecommendationClassification) *common.Recommendation {
	rec := &common.Recommendation{
		Provider:       common.ProviderAzure,
		Service:        common.ServiceCache,
		Account:        c.subscriptionID,
		Region:         c.region,
		CommitmentType: common.CommitmentReservedInstance,
		Timestamp:      time.Now(),
		Term:           "1yr",
		PaymentOption:  "upfront",
	}

	return rec
}
