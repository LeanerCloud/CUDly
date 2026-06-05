// Package search provides Azure Cognitive Search Reserved Capacity client
package search

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
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/search/armsearch"

	"github.com/LeanerCloud/CUDly/pkg/common"
	"github.com/LeanerCloud/CUDly/providers/azure/internal/httpclient"
	azrecs "github.com/LeanerCloud/CUDly/providers/azure/internal/recommendations"
	"github.com/LeanerCloud/CUDly/providers/azure/services/internal/reservations"
)

// maxRecsPages caps Consumption API recommendation pagination.
const maxRecsPages = 10

// maxReservationsPages caps reservation-detail pagination.
const maxReservationsPages = 50

// maxServicesPages caps Search service list pagination.
const maxServicesPages = 20

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
		httpClient:     httpclient.New(),
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
	return common.ServiceSearch
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
		// NewListPager's first argument is the billing scope, NOT the
		// filter — see the parallel comment in compute/client.go for the
		// failure mode that the wrong shape produced.
		scope := fmt.Sprintf("/subscriptions/%s", c.subscriptionID)
		filter := "properties/scope eq 'Shared'"
		pager = client.NewListPager(scope, &armconsumption.ReservationRecommendationsClientListOptions{Filter: &filter})
	}

	for pageIdx := 0; pager.More(); pageIdx++ {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("context cancelled during pagination: %w", err)
		}
		if pageIdx >= maxRecsPages {
			return nil, fmt.Errorf("search: GetRecommendations pagination cap (%d pages) reached", maxRecsPages)
		}
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to get Search recommendations: %w", err)
		}

		for _, rec := range page.Value {
			converted := c.convertAzureSearchRecommendation(ctx, rec)
			if converted != nil {
				recommendations = append(recommendations, azrecs.ExpandPaymentVariants(*converted)...)
			}
		}
	}

	return recommendations, nil
}

// GetExistingCommitments retrieves existing Search reserved capacity
func (c *SearchClient) GetExistingCommitments(ctx context.Context) ([]common.Commitment, error) {
	pager, err := c.createReservationsPager()
	if err != nil {
		log.Printf("WARNING: failed to create Search reservations pager: %v", err)
		return []common.Commitment{}, nil
	}

	return c.collectSearchReservations(ctx, pager)
}

// createReservationsPager creates a pager for listing reservations
func (c *SearchClient) createReservationsPager() (ReservationsDetailsPager, error) {
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

// collectSearchReservations collects Search reservations from the pager.
// Returns an error on first pagination failure so callers can't silently act
// on a partial list — see the compute client for the full rationale.
func (c *SearchClient) collectSearchReservations(ctx context.Context, pager ReservationsDetailsPager) ([]common.Commitment, error) {
	commitments := make([]common.Commitment, 0)

	for pageIdx := 0; pager.More(); pageIdx++ {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("context cancelled during pagination: %w", err)
		}
		if pageIdx >= maxReservationsPages {
			return nil, fmt.Errorf("search: GetExistingCommitments pagination cap (%d pages) reached", maxReservationsPages)
		}
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("search: list reservations: %w", err)
		}

		for _, detail := range page.Value {
			if commitment := c.convertSearchReservation(detail); commitment != nil {
				commitments = append(commitments, *commitment)
			}
		}
	}

	return commitments, nil
}

// convertSearchReservation converts a reservation detail to a commitment if it's a Search reservation
func (c *SearchClient) convertSearchReservation(detail *armconsumption.ReservationDetail) *common.Commitment {
	if detail.Properties == nil {
		return nil
	}

	props := detail.Properties
	if props.SKUName == nil || !strings.Contains(strings.ToLower(*props.SKUName), "search") {
		return nil
	}

	commitment := &common.Commitment{
		Provider:       common.ProviderAzure,
		Account:        c.subscriptionID,
		CommitmentType: common.CommitmentReservedInstance,
		Service:        common.ServiceSearch,
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

// PurchaseCommitment purchases Search reserved capacity using the two-step
// calculatePrice->purchase flow required by Azure's Reservations API (issue #677).
func (c *SearchClient) PurchaseCommitment(ctx context.Context, rec common.Recommendation, opts common.PurchaseOptions) (common.PurchaseResult, error) {
	result := common.PurchaseResult{
		Recommendation: rec,
		DryRun:         false,
		Success:        false,
		Timestamp:      time.Now(),
	}

	// Source is required so the resulting reservation is attributable to CUDly
	// in the portal via the purchase-automation tag. The dedupe key for
	// idempotent re-drives is now opts.IdempotencyToken (issue #721, applied
	// in reservations.DoIdempotentPurchaseTwoStep); source remains mandatory
	// for attribution.
	if opts.Source == "" {
		result.Error = fmt.Errorf("purchase source is required for Azure reservation purchases")
		return result, result.Error
	}

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
			"displayName": reservations.BuildDisplayName(reservations.DisplayNameFields{
				Service:      "search",
				Region:       c.region,
				ResourceType: rec.ResourceType,
				Count:        rec.Count,
				Term:         rec.Term,
				Payment:      rec.PaymentOption,
				Now:          time.Now(),
			}),
			"appliedScopeType": "Shared",
			"renew":            false,
		},
	}
	reservations.ApplyPurchaseTags(requestBody, opts.Source, opts.IdempotencyToken)

	bodyBytes, err := json.Marshal(requestBody)
	if err != nil {
		result.Error = fmt.Errorf("failed to marshal request: %w", err)
		return result, result.Error
	}

	token, err := c.cred.GetToken(ctx, policy.TokenRequestOptions{
		Scopes: []string{"https://management.azure.com/.default"},
	})
	if err != nil {
		result.Error = fmt.Errorf("failed to get access token: %w", err)
		return result, result.Error
	}

	reservationOrderID, err := reservations.DoIdempotentPurchaseTwoStep(ctx, c.httpClient, reservations.CalculatePriceURL(), bodyBytes, token.Token, opts.IdempotencyToken)
	if err != nil {
		result.Error = err
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

	resourceType := strings.TrimSpace(rec.ResourceType)
	for _, sku := range validSKUs {
		if strings.EqualFold(sku, resourceType) {
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
	pager, ok := c.resolveServicesPager()
	if !ok {
		return c.getCommonSKUs(), nil
	}

	skuSet, err := c.collectSKUsFromPager(ctx, pager)
	if err != nil {
		return nil, err
	}

	if len(skuSet) > 0 {
		skus := make([]string, 0, len(skuSet))
		for sku := range skuSet {
			skus = append(skus, sku)
		}
		return skus, nil
	}

	return c.getCommonSKUs(), nil
}

func (c *SearchClient) resolveServicesPager() (SearchServicesPager, bool) {
	if c.searchServicesPager != nil {
		return c.searchServicesPager, true
	}
	client, err := armsearch.NewServicesClient(c.subscriptionID, c.cred, nil)
	if err != nil {
		return nil, false
	}
	return client.NewListBySubscriptionPager(nil, nil), true
}

func (c *SearchClient) collectSKUsFromPager(ctx context.Context, pager SearchServicesPager) (map[string]bool, error) {
	skuSet := make(map[string]bool)
	for pageIdx := 0; pager.More(); pageIdx++ {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("search: GetValidResourceTypes context cancelled after %d pages: %w", pageIdx, err)
		}
		if pageIdx >= maxServicesPages {
			log.Printf("WARNING: search: GetValidResourceTypes pagination cap (%d pages) reached", maxServicesPages)
			break
		}
		page, err := pager.NextPage(ctx)
		if err != nil {
			break
		}
		for _, service := range page.Value {
			if service.SKU != nil && service.SKU.Name != nil {
				skuSet[string(*service.SKU.Name)] = true
			}
		}
	}
	return skuSet, nil
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
	filter := fmt.Sprintf("serviceName eq 'Azure Cognitive Search' and armRegionName eq '%s'", region)

	priceData, err := c.fetchAzurePricing(ctx, filter)
	if err != nil {
		return nil, err
	}

	if len(priceData.Items) == 0 {
		return nil, fmt.Errorf("no pricing data found for Azure Search in region %s", region)
	}

	onDemandPrice, reservationPrice, currency := extractSearchPricing(priceData.Items, termYears)
	if onDemandPrice == 0 {
		return nil, fmt.Errorf("no on-demand pricing found for Azure Search")
	}

	hoursInTerm := 8760.0 * float64(termYears)
	if reservationPrice == 0 {
		reservationPrice = estimateSearchReservationPrice(onDemandPrice, hoursInTerm)
	}

	savingsPercentage := calculateSearchSavingsPercentage(onDemandPrice, hoursInTerm, reservationPrice)

	return &SearchPricing{
		HourlyRate:        reservationPrice / hoursInTerm,
		ReservationPrice:  reservationPrice,
		OnDemandPrice:     onDemandPrice * hoursInTerm,
		Currency:          currency,
		SavingsPercentage: savingsPercentage,
	}, nil
}

// fetchAzurePricing fetches pricing data from Azure Retail Prices API
func (c *SearchClient) fetchAzurePricing(ctx context.Context, filter string) (*AzureRetailPrice, error) {
	baseURL := "https://prices.azure.com/api/retail/prices"
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

	return &priceData, nil
}

// extractSearchPricing extracts on-demand and reservation pricing from price items
func extractSearchPricing(items []struct {
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

		if item.ReservationTerm != "" && item.ReservationTerm == termStr {
			reservation = item.RetailPrice
		} else if item.Type == "Consumption" {
			onDemand = item.UnitPrice
		}
	}

	return onDemand, reservation, currency
}

// estimateSearchReservationPrice estimates reservation price when not available
func estimateSearchReservationPrice(onDemandPrice, hoursInTerm float64) float64 {
	onDemandTotal := onDemandPrice * hoursInTerm
	// Azure Search reservations typically offer 30-40% savings
	return onDemandTotal * 0.65
}

// calculateSearchSavingsPercentage calculates the savings percentage
func calculateSearchSavingsPercentage(onDemandPrice, hoursInTerm, reservationPrice float64) float64 {
	onDemandTotal := onDemandPrice * hoursInTerm
	return ((onDemandTotal - reservationPrice) / onDemandTotal) * 100
}

// convertAzureSearchRecommendation converts Azure Search reservation recommendation to common format
func (c *SearchClient) convertAzureSearchRecommendation(ctx context.Context, azureRec armconsumption.ReservationRecommendationClassification) *common.Recommendation {
	// Extract fields from Azure recommendation using the shared converter
	extracted := azrecs.Extract(azureRec)
	if extracted == nil {
		return nil
	}

	rec := &common.Recommendation{
		Provider:       common.ProviderAzure,
		Service:        common.ServiceSearch,
		Account:        c.subscriptionID,
		CommitmentType: common.CommitmentReservedInstance,
		Timestamp:      time.Now(),
		// Populate fields from Azure API response
		Region:               extracted.Region,
		ResourceType:         extracted.ResourceType,
		Count:                extracted.Count,
		OnDemandCost:         extracted.OnDemandCost,
		CommitmentCost:       extracted.CommitmentCost,
		EstimatedSavings:     extracted.EstimatedSavings,
		Term:                 extracted.Term,
		RecurringMonthlyCost: extracted.RecurringMonthlyCost,
		PaymentOption:        "upfront", // Default, will be expanded by ExpandPaymentVariants
	}

	// Override region with client region if extraction didn't find one
	if rec.Region == "" {
		rec.Region = c.region
	}

	return rec
}
