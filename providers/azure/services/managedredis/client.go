// Package managedredis provides Azure Managed Redis (Azure Cache for Redis) Reserved Capacity client.
// Azure Cache for Redis is the Azure equivalent of AWS MemoryDB for Redis: a fully-managed,
// in-memory caching service with reservation-based pricing.
package managedredis

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/consumption/armconsumption"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/redis/armredis/v3"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/reservations/armreservations"

	"github.com/LeanerCloud/CUDly/pkg/common"
	"github.com/LeanerCloud/CUDly/providers/azure/internal/httpclient"
	"github.com/LeanerCloud/CUDly/providers/azure/internal/pricing"
	"github.com/LeanerCloud/CUDly/providers/azure/internal/recommendations"
	"github.com/LeanerCloud/CUDly/providers/azure/services/internal/reservations"
)

// maxRecsPages caps Consumption API recommendation pagination (matches the
// sibling Azure service clients, e.g. cache/client.go).
const maxRecsPages = 10

// recommendationsListArgs builds the (scope, options) for the Consumption
// ReservationRecommendations pager. NewListPager's first argument is the
// billing scope (the subscription), NOT the ODATA filter -- passing the filter
// as the scope produces a malformed URL where every request errors (the exact
// failure mode documented in compute/client.go). The filter goes in
// options.Filter. Extracted so a unit test can assert the scope shape without a
// real Azure client (the injected mock pager bypasses NewListPager entirely).
func (c *ManagedRedisClient) recommendationsListArgs() (string, *armconsumption.ReservationRecommendationsClientListOptions) {
	scope := fmt.Sprintf("/subscriptions/%s", c.subscriptionID)
	filter := "properties/scope eq 'Shared' and properties/resourceType eq 'RedisCache'"
	return scope, &armconsumption.ReservationRecommendationsClientListOptions{Filter: &filter}
}

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

// ManagedRedisClient handles Azure Cache for Redis Reserved Capacity as Azure's MemoryDB equivalent.
// It surfaces under ServiceMemoryDB so it is treated symmetrically with AWS MemoryDB at the
// provider-dispatch level.
type ManagedRedisClient struct {
	cred                 azcore.TokenCredential
	subscriptionID       string
	region               string
	httpClient           HTTPClient
	recommendationsPager RecommendationsPager
	reservationsPager    ReservationsDetailsPager
	redisCachesPager     RedisCachesPager
}

// NewClient creates a new ManagedRedisClient.
func NewClient(cred azcore.TokenCredential, subscriptionID, region string) *ManagedRedisClient {
	return &ManagedRedisClient{
		cred:           cred,
		subscriptionID: subscriptionID,
		region:         region,
		httpClient:     httpclient.New(),
	}
}

// NewClientWithHTTP creates a new ManagedRedisClient with a custom HTTP client (for testing).
// When httpClient is nil, the SSRF-hardened httpclient.New() is used so the nil
// fallback also blocks IMDS connections.
func NewClientWithHTTP(cred azcore.TokenCredential, subscriptionID, region string, httpClient HTTPClient) *ManagedRedisClient {
	if httpClient == nil {
		httpClient = httpclient.New()
	}
	return &ManagedRedisClient{
		cred:           cred,
		subscriptionID: subscriptionID,
		region:         region,
		httpClient:     httpClient,
	}
}

// SetRecommendationsPager sets the recommendations pager (for testing).
func (c *ManagedRedisClient) SetRecommendationsPager(pager RecommendationsPager) {
	c.recommendationsPager = pager
}

// SetReservationsPager sets the reservations pager (for testing).
func (c *ManagedRedisClient) SetReservationsPager(pager ReservationsDetailsPager) {
	c.reservationsPager = pager
}

// SetRedisCachesPager sets the Redis caches pager (for testing).
func (c *ManagedRedisClient) SetRedisCachesPager(pager RedisCachesPager) {
	c.redisCachesPager = pager
}

// GetServiceType returns ServiceMemoryDB -- the cloud-agnostic label for in-memory DB services.
func (c *ManagedRedisClient) GetServiceType() common.ServiceType {
	return common.ServiceMemoryDB
}

// GetRegion returns the region.
func (c *ManagedRedisClient) GetRegion() string {
	return c.region
}

// AzureRetailPrice is the response envelope for the Azure Retail Prices API.
type AzureRetailPrice = pricing.Page[pricing.RetailPriceItem]

// GetRecommendations gets Redis Cache reservation recommendations from the Azure Consumption API.
func (c *ManagedRedisClient) GetRecommendations(ctx context.Context, _ *common.RecommendationParams) ([]common.Recommendation, error) {
	recommendations := make([]common.Recommendation, 0)

	var pager RecommendationsPager
	if c.recommendationsPager != nil {
		pager = c.recommendationsPager
	} else {
		client, err := armconsumption.NewReservationRecommendationsClient(c.cred, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to create consumption client: %w", err)
		}
		scope, opts := c.recommendationsListArgs()
		pager = client.NewListPager(scope, opts)
	}

	for pageIdx := 0; pager.More(); pageIdx++ {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("context cancelled during pagination: %w", err)
		}
		if pageIdx >= maxRecsPages {
			return nil, fmt.Errorf("managedredis: GetRecommendations pagination cap (%d pages) reached", maxRecsPages)
		}
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to get Redis Cache recommendations: %w", err)
		}

		for _, rec := range page.Value {
			converted := c.convertRecommendation(ctx, rec)
			if converted != nil {
				recommendations = append(recommendations, *converted)
			}
		}
	}

	return recommendations, nil
}

// GetExistingCommitments retrieves existing Azure Cache for Redis reserved capacity.
func (c *ManagedRedisClient) GetExistingCommitments(ctx context.Context) ([]common.Commitment, error) {
	commitments := make([]common.Commitment, 0)

	pager, err := c.reservationDetailsPager()
	if err != nil {
		return nil, fmt.Errorf("failed to initialize reservations details pager: %w", err)
	}

	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to fetch reservations details page: %w", err)
		}
		for _, detail := range page.Value {
			if cm := c.reservationDetailToCommitment(detail); cm != nil {
				commitments = append(commitments, *cm)
			}
		}
	}

	return commitments, nil
}

// reservationDetailsPager returns the pager to use for reservation details,
// preferring the injected mock over a real SDK pager.
// The subscription-scope NewListPager is used so that all reservation orders
// are queried rather than a single hardcoded order ID.
func (c *ManagedRedisClient) reservationDetailsPager() (ReservationsDetailsPager, error) {
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

// reservationDetailToCommitment converts a single reservation detail to a Commitment.
// Returns nil when the detail should be skipped (nil properties, non-Redis SKU).
func (c *ManagedRedisClient) reservationDetailToCommitment(detail *armconsumption.ReservationDetail) *common.Commitment {
	if detail == nil || detail.Properties == nil {
		return nil
	}
	props := detail.Properties
	if props.SKUName == nil || !strings.Contains(strings.ToLower(*props.SKUName), "redis") {
		return nil
	}
	cm := &common.Commitment{
		Provider:       common.ProviderAzure,
		Account:        c.subscriptionID,
		CommitmentType: common.CommitmentReservedInstance,
		Service:        common.ServiceMemoryDB,
		Region:         c.region,
		State:          "active",
	}
	if props.ReservationID != nil {
		cm.CommitmentID = *props.ReservationID
	}
	cm.ResourceType = *props.SKUName
	return cm
}

// PurchaseCommitment purchases Azure Cache for Redis reserved capacity using the two-step
// calculatePrice->purchase flow required by Azure's Reservations API (issue #677).
func (c *ManagedRedisClient) PurchaseCommitment(ctx context.Context, rec common.Recommendation, opts common.PurchaseOptions) (common.PurchaseResult, error) {
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
	if rec.Count <= 0 {
		result.Error = fmt.Errorf("quantity must be greater than zero, got %d", rec.Count)
		return result, result.Error
	}

	termYears, termErr := reservations.ParseTermYears(rec.Term)
	if termErr != nil {
		result.Error = termErr
		return result, result.Error
	}
	billingPlan, billingPlanErr := reservations.BillingPlanForPaymentOption(rec.PaymentOption)
	if billingPlanErr != nil {
		result.Error = billingPlanErr
		return result, result.Error
	}

	requestBody := map[string]interface{}{
		"sku": map[string]string{
			"name": rec.ResourceType,
		},
		"location": c.region,
		"properties": map[string]interface{}{
			"reservedResourceType": string(armreservations.ReservedResourceTypeRedisCache),
			"billingScopeId":       fmt.Sprintf("/subscriptions/%s", c.subscriptionID),
			"billingPlan":          string(billingPlan),
			"term":                 fmt.Sprintf("P%dY", termYears),
			"quantity":             rec.Count,
			"displayName":          fmt.Sprintf("Azure Cache for Redis Reservation - %s", rec.ResourceType),
			"appliedScopeType":     "Shared",
			"renew":                false,
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

// ValidateOffering validates that the given Redis Cache SKU is known.
func (c *ManagedRedisClient) ValidateOffering(ctx context.Context, rec common.Recommendation) error {
	validSKUs, err := c.GetValidResourceTypes(ctx)
	if err != nil {
		return fmt.Errorf("failed to get valid SKUs: %w", err)
	}

	for _, sku := range validSKUs {
		if sku == rec.ResourceType {
			return nil
		}
	}

	return fmt.Errorf("invalid Azure Cache for Redis SKU: %s", rec.ResourceType)
}

// GetOfferingDetails retrieves reservation offering details from the Azure Retail Prices API.
func (c *ManagedRedisClient) GetOfferingDetails(ctx context.Context, rec common.Recommendation) (*common.OfferingDetails, error) {
	termYears, err := reservations.ParseTermYears(rec.Term)
	if err != nil {
		return nil, err
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
		OfferingID:          fmt.Sprintf("azure-managed-redis-%s-%s-%s", rec.ResourceType, c.region, rec.Term),
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

// GetValidResourceTypes returns Redis Cache SKUs discovered from the subscription, or a
// curated fallback list when the subscription API is unreachable.
func (c *ManagedRedisClient) GetValidResourceTypes(ctx context.Context) ([]string, error) {
	pager, err := c.redisCacheListPager()
	if err != nil {
		return c.commonSKUs(), nil
	}

	skuSet, err := collectSKUsFromPager(ctx, pager)
	if err != nil {
		// Discard any partial results and fall back to the curated SKU list
		// rather than risk false validation failures for valid SKUs.
		return c.commonSKUs(), nil
	}
	if len(skuSet) > 0 {
		skus := make([]string, 0, len(skuSet))
		for sku := range skuSet {
			skus = append(skus, sku)
		}
		return skus, nil
	}
	return c.commonSKUs(), nil
}

// redisCacheListPager returns the pager to use for listing Redis caches,
// preferring the injected mock over a real SDK pager.
func (c *ManagedRedisClient) redisCacheListPager() (RedisCachesPager, error) {
	if c.redisCachesPager != nil {
		return c.redisCachesPager, nil
	}
	client, err := armredis.NewClient(c.subscriptionID, c.cred, nil)
	if err != nil {
		return nil, err
	}
	return client.NewListBySubscriptionPager(nil), nil
}

// collectSKUsFromPager iterates the pager and returns the set of full SKU
// names (e.g. "Premium_P1") built from each cache's Name/Family/Capacity.
// On a pager error the caller is expected to discard any partial set and
// fall back, so the returned set must be considered invalid when err != nil.
func collectSKUsFromPager(ctx context.Context, pager RedisCachesPager) (map[string]bool, error) {
	skuSet := make(map[string]bool)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		for _, cache := range page.Value {
			if name := extractSKUName(cache); name != "" {
				skuSet[name] = true
			}
		}
	}
	return skuSet, nil
}

// extractSKUName derives a full SKU string from a ResourceInfo entry.
// Returns "" when the entry lacks the required fields.
func extractSKUName(cache *armredis.ResourceInfo) string {
	if cache == nil || cache.Properties == nil {
		return ""
	}
	sku := cache.Properties.SKU
	if sku == nil || sku.Name == nil || sku.Family == nil || sku.Capacity == nil {
		return ""
	}
	return fmt.Sprintf("%s_%s%d", string(*sku.Name), string(*sku.Family), *sku.Capacity)
}

// commonSKUs returns a curated list of Azure Cache for Redis SKUs that support reservations.
func (c *ManagedRedisClient) commonSKUs() []string {
	return []string{
		// Basic tier
		"Basic_C0", "Basic_C1", "Basic_C2", "Basic_C3", "Basic_C4", "Basic_C5", "Basic_C6",
		// Standard tier
		"Standard_C0", "Standard_C1", "Standard_C2", "Standard_C3", "Standard_C4", "Standard_C5", "Standard_C6",
		// Premium tier (most commonly reserved)
		"Premium_P1", "Premium_P2", "Premium_P3", "Premium_P4", "Premium_P5",
	}
}

// RedisPricing holds pricing details for a given Redis Cache SKU.
type RedisPricing struct {
	HourlyRate        float64
	ReservationPrice  float64
	OnDemandPrice     float64
	Currency          string
	SavingsPercentage float64
}

// getRedisPricing fetches pricing from the Azure Retail Prices API using the
// shared pricing.FetchAll walker, which enforces a seen-URL guard, a max-pages
// cap, and a per-page timeout independent of the caller's context budget. This
// replaces the former hand-rolled NextPageLink loop (issue #1021 H2).
func (c *ManagedRedisClient) getRedisPricing(ctx context.Context, sku, region string, termYears int) (*RedisPricing, error) {
	filter := fmt.Sprintf("serviceName eq 'Azure Cache for Redis' and armRegionName eq '%s' and contains(armSkuName, '%s')",
		region, sku)

	params := url.Values{}
	params.Add("$filter", filter)
	params.Add("api-version", "2023-01-01-preview")

	initialURL := "https://prices.azure.com/api/retail/prices?" + params.Encode()

	items, err := pricing.FetchAll[pricing.RetailPriceItem](ctx, c.httpClient, initialURL, pricing.DefaultPageTimeout, pricing.DefaultMaxPages)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch Redis Cache pricing: %w", err)
	}

	if len(items) == 0 {
		return nil, fmt.Errorf("no pricing data found for Redis Cache SKU %s in region %s", sku, region)
	}

	onDemandPrice, reservationPrice, currency := parsePriceItems(items, termYears)

	if onDemandPrice == 0 {
		return nil, fmt.Errorf("no on-demand pricing found for Redis Cache SKU %s", sku)
	}

	hoursInTerm := 8760.0 * float64(termYears)
	if reservationPrice == 0 {
		return nil, fmt.Errorf("no reservation pricing found for Redis Cache SKU %s (%d year) in region %s", sku, termYears, region)
	}

	savingsPct := ((onDemandPrice*hoursInTerm - reservationPrice) / (onDemandPrice * hoursInTerm)) * 100

	return &RedisPricing{
		HourlyRate:        reservationPrice / hoursInTerm,
		ReservationPrice:  reservationPrice,
		OnDemandPrice:     onDemandPrice * hoursInTerm,
		Currency:          currency,
		SavingsPercentage: savingsPct,
	}, nil
}

// azureTermString returns the Retail Prices API ReservationTerm string for the
// given number of years. The API uses the singular form "1 Year" for one year
// and the plural form "N Years" for two or more years.
func azureTermString(termYears int) string {
	if termYears == 1 {
		return "1 Year"
	}
	return fmt.Sprintf("%d Years", termYears)
}

// parsePriceItems extracts on-demand price, reservation price, and currency
// from the flat list returned by the Azure Retail Prices API.
func parsePriceItems(items []pricing.RetailPriceItem, termYears int) (onDemand, reservation float64, currency string) {
	currency = "USD"
	termStr := azureTermString(termYears)
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
	return
}

// convertRecommendation converts an Azure Consumption API recommendation to the common format.
// Returns nil when the input is nil or cannot be parsed (e.g. an unsupported SDK Kind).
func (c *ManagedRedisClient) convertRecommendation(_ context.Context, rec armconsumption.ReservationRecommendationClassification) *common.Recommendation {
	f := recommendations.Extract(rec)
	if f == nil {
		return nil
	}
	// Pass the converter's *float64 through directly so nil ("provider API did
	// not return a monthly breakdown") stays distinct from an explicit 0.
	return &common.Recommendation{
		Provider:             common.ProviderAzure,
		Service:              common.ServiceMemoryDB,
		Account:              c.subscriptionID,
		Region:               f.Region,
		ResourceType:         f.ResourceType,
		Count:                f.Count,
		OnDemandCost:         f.OnDemandCost,
		CommitmentCost:       f.CommitmentCost,
		EstimatedSavings:     f.EstimatedSavings,
		RecurringMonthlyCost: f.RecurringMonthlyCost,
		CommitmentType:       common.CommitmentReservedInstance,
		Term:                 f.Term,
		PaymentOption:        "upfront",
		Timestamp:            time.Now(),
	}
}
