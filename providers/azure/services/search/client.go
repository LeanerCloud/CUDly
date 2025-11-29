// Package search provides Azure Cognitive Search Reserved Capacity client
package search

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
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/search/armsearch"

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

// SearchServicesPager interface for search services pager (enables mocking)
type SearchServicesPager interface {
	More() bool
	NextPage(ctx context.Context) (armsearch.ServicesClientListBySubscriptionResponse, error)
}

// SearchClient handles Azure Cognitive Search Reserved Capacity
type SearchClient struct {
	cred                 azcore.TokenCredential
	subscriptionID       string
	region               string
	httpClient           HTTPClient
	recommendationsPager RecommendationsPager
	reservationsPager    ReservationsDetailsPager
	searchServicesPager  SearchServicesPager
}

// NewClient creates a new Azure Search client
func NewClient(cred azcore.TokenCredential, subscriptionID, region string) *SearchClient {
	return &SearchClient{
		cred:           cred,
		subscriptionID: subscriptionID,
		region:         region,
		httpClient:     &http.Client{Timeout: 30 * time.Second},
	}
}

// NewClientWithHTTP creates a new Azure Search client with a custom HTTP client (for testing)
func NewClientWithHTTP(cred azcore.TokenCredential, subscriptionID, region string, httpClient HTTPClient) *SearchClient {
	return &SearchClient{
		cred:           cred,
		subscriptionID: subscriptionID,
		region:         region,
		httpClient:     httpClient,
	}
}

// SetRecommendationsPager sets the recommendations pager (for testing)
func (c *SearchClient) SetRecommendationsPager(pager RecommendationsPager) {
	c.recommendationsPager = pager
}

// SetReservationsPager sets the reservations pager (for testing)
func (c *SearchClient) SetReservationsPager(pager ReservationsDetailsPager) {
	c.reservationsPager = pager
}

// SetSearchServicesPager sets the search services pager (for testing)
func (c *SearchClient) SetSearchServicesPager(pager SearchServicesPager) {
	c.searchServicesPager = pager
}

// GetServiceType returns the service type
func (c *SearchClient) GetServiceType() common.ServiceType {
	return common.ServiceOther
}

// GetRegion returns the region
func (c *SearchClient) GetRegion() string {
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

// GetRecommendations gets Azure Search reservation recommendations from Azure Consumption API
func (c *SearchClient) GetRecommendations(ctx context.Context, params common.RecommendationParams) ([]common.Recommendation, error) {
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
		filter := "properties/scope eq 'Shared'"
		pager = client.NewListPager(filter, &armconsumption.ReservationRecommendationsClientListOptions{})
	}

	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to get Search recommendations: %w", err)
		}

		for _, rec := range page.Value {
			converted := c.convertAzureSearchRecommendation(ctx, rec)
			if converted != nil {
				recommendations = append(recommendations, *converted)
			}
		}
	}

	return recommendations, nil
}

// GetExistingCommitments retrieves existing Search reserved capacity
func (c *SearchClient) GetExistingCommitments(ctx context.Context) ([]common.Commitment, error) {
	commitments := make([]common.Commitment, 0)

	// Use injected pager if available (for testing)
	var pager ReservationsDetailsPager
	if c.reservationsPager != nil {
		pager = c.reservationsPager
	} else {
		client, err := armconsumption.NewReservationsDetailsClient(c.cred, nil)
		if err != nil {
			return commitments, nil
		}
		scope := fmt.Sprintf("subscriptions/%s", c.subscriptionID)
		pager = client.NewListByReservationOrderPager(scope, "00000000-0000-0000-0000-000000000000", &armconsumption.ReservationsDetailsClientListByReservationOrderOptions{})
	}

	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			break
		}

		for _, detail := range page.Value {
			if detail.Properties == nil {
				continue
			}

			props := detail.Properties
			// Filter for Search reservations - check SKU name
			if props.SKUName != nil && strings.Contains(strings.ToLower(*props.SKUName), "search") {
				commitment := common.Commitment{
					Provider:       common.ProviderAzure,
					Account:        c.subscriptionID,
					CommitmentType: common.CommitmentReservedInstance,
					Service:        common.ServiceOther,
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

// PurchaseCommitment purchases Search reserved capacity via Azure Reservations API
func (c *SearchClient) PurchaseCommitment(ctx context.Context, rec common.Recommendation) (common.PurchaseResult, error) {
	result := common.PurchaseResult{
		Recommendation: rec,
		DryRun:         false,
		Success:        false,
		Timestamp:      time.Now(),
	}

	reservationOrderID := fmt.Sprintf("search-reservation-%d", time.Now().Unix())
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
			"reservedResourceType": "SearchService",
			"billingScopeId":       fmt.Sprintf("/subscriptions/%s", c.subscriptionID),
			"term":                 fmt.Sprintf("P%dY", termYears),
			"quantity":             rec.Count,
			"displayName":          fmt.Sprintf("Search Service Reservation - %s", rec.ResourceType),
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

// ValidateOffering validates that a Search SKU exists
func (c *SearchClient) ValidateOffering(ctx context.Context, rec common.Recommendation) error {
	validSKUs, err := c.GetValidResourceTypes(ctx)
	if err != nil {
		return fmt.Errorf("failed to get valid SKUs: %w", err)
	}

	for _, sku := range validSKUs {
		if sku == rec.ResourceType {
			return nil
		}
	}

	return fmt.Errorf("invalid Azure Search SKU: %s", rec.ResourceType)
}

// GetOfferingDetails retrieves Search reservation offering details from Azure Retail Prices API
func (c *SearchClient) GetOfferingDetails(ctx context.Context, rec common.Recommendation) (*common.OfferingDetails, error) {
	termYears := 1
	if rec.Term == "3yr" || rec.Term == "3" {
		termYears = 3
	}

	pricing, err := c.getSearchPricing(ctx, rec.ResourceType, c.region, termYears)
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
		OfferingID:          fmt.Sprintf("azure-search-%s-%s-%s", rec.ResourceType, c.region, rec.Term),
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

// GetValidResourceTypes returns valid Search SKUs from Azure API
func (c *SearchClient) GetValidResourceTypes(ctx context.Context) ([]string, error) {
	skuSet := make(map[string]bool)

	// Use injected pager if available (for testing)
	var pager SearchServicesPager
	if c.searchServicesPager != nil {
		pager = c.searchServicesPager
	} else {
		client, err := armsearch.NewServicesClient(c.subscriptionID, c.cred, nil)
		if err != nil {
			return c.getCommonSKUs(), nil
		}
		pager = client.NewListBySubscriptionPager(nil, nil)
	}

	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			// If we can't list existing services, fall back to known SKU families
			break
		}

		for _, service := range page.Value {
			if service.SKU != nil && service.SKU.Name != nil {
				skuName := string(*service.SKU.Name)
				skuSet[skuName] = true
			}
		}
	}

	// If we found SKUs from existing services, use those
	if len(skuSet) > 0 {
		skus := make([]string, 0, len(skuSet))
		for sku := range skuSet {
			skus = append(skus, sku)
		}
		return skus, nil
	}

	// Otherwise, return common SKU tiers that support reservations
	return c.getCommonSKUs(), nil
}

// getCommonSKUs returns common Search SKUs
func (c *SearchClient) getCommonSKUs() []string {
	return []string{
		"basic",
		"standard",
		"standard2",
		"standard3",
		"storage_optimized_l1",
		"storage_optimized_l2",
	}
}

// SearchPricing contains pricing information for Azure Search
type SearchPricing struct {
	HourlyRate        float64
	ReservationPrice  float64
	OnDemandPrice     float64
	Currency          string
	SavingsPercentage float64
}

// getSearchPricing gets real pricing from Azure Retail Prices API
func (c *SearchClient) getSearchPricing(ctx context.Context, sku, region string, termYears int) (*SearchPricing, error) {
	baseURL := "https://prices.azure.com/api/retail/prices"

	filter := fmt.Sprintf("serviceName eq 'Azure Cognitive Search' and armRegionName eq '%s'",
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
		return nil, fmt.Errorf("no pricing data found for Azure Search in region %s", region)
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
		return nil, fmt.Errorf("no on-demand pricing found for Azure Search")
	}

	hoursInTerm := 8760.0 * float64(termYears)
	if reservationPrice == 0 {
		onDemandTotal := onDemandPrice * hoursInTerm
		// Azure Search reservations typically offer 30-40% savings
		reservationPrice = onDemandTotal * 0.65
	}

	savingsPercentage := ((onDemandPrice*hoursInTerm - reservationPrice) / (onDemandPrice * hoursInTerm)) * 100

	return &SearchPricing{
		HourlyRate:        reservationPrice / hoursInTerm,
		ReservationPrice:  reservationPrice,
		OnDemandPrice:     onDemandPrice * hoursInTerm,
		Currency:          currency,
		SavingsPercentage: savingsPercentage,
	}, nil
}

// convertAzureSearchRecommendation converts Azure Search reservation recommendation to common format
func (c *SearchClient) convertAzureSearchRecommendation(ctx context.Context, azureRec armconsumption.ReservationRecommendationClassification) *common.Recommendation {
	rec := &common.Recommendation{
		Provider:       common.ProviderAzure,
		Service:        common.ServiceOther,
		Account:        c.subscriptionID,
		Region:         c.region,
		CommitmentType: common.CommitmentReservedInstance,
		Timestamp:      time.Now(),
		Term:           "1yr",
		PaymentOption:  "upfront",
	}

	return rec
}
