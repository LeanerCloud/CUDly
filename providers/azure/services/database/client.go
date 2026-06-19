// Package database provides Azure SQL Database Reserved Capacity client
package database

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/consumption/armconsumption"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/sql/armsql"

	"github.com/LeanerCloud/CUDly/pkg/common"
	"github.com/LeanerCloud/CUDly/pkg/logging"
	"github.com/LeanerCloud/CUDly/providers/azure/internal/httpclient"
	"github.com/LeanerCloud/CUDly/providers/azure/internal/pricing"
	azrecs "github.com/LeanerCloud/CUDly/providers/azure/internal/recommendations"
	"github.com/LeanerCloud/CUDly/providers/azure/services/internal/reservations"
)

// maxRecsPages caps Consumption API recommendation pagination.
const maxRecsPages = 10

// maxReservationsPages caps reservation-detail pagination.
const maxReservationsPages = 50

// sqlSKUEntry holds the SKU-catalogue-derived fields the converter
// wants for each Azure SQL SKU. Sourced from the
// armsql.CapabilitiesClient.ListByLocation response which embeds the
// SQL Server version in the ServerVersionCapability.Name (e.g. "12.0")
// while traversing down to ServiceLevelObjective SKUs.
type sqlSKUEntry struct {
	engineVersion string
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

// CapabilitiesClient interface for SQL capabilities (enables mocking)
type CapabilitiesClient interface {
	ListByLocation(ctx context.Context, locationName string, options *armsql.CapabilitiesClientListByLocationOptions) (armsql.CapabilitiesClientListByLocationResponse, error)
}

// SQLServersPager interface for listing SQL servers (enables mocking)
type SQLServersPager interface {
	More() bool
	NextPage(ctx context.Context) (armsql.ServersClientListResponse, error)
}

// SQLManagedInstancesPager interface for listing SQL managed instances (enables mocking)
type SQLManagedInstancesPager interface {
	More() bool
	NextPage(ctx context.Context) (armsql.ManagedInstancesClientListResponse, error)
}

// DatabaseClient handles Azure SQL Database Reserved Capacity
type DatabaseClient struct {
	cred                  azcore.TokenCredential
	subscriptionID        string
	region                string
	httpClient            HTTPClient
	recommendationsPager  RecommendationsPager
	reservationsPager     ReservationsDetailsPager
	capabilitiesClient    CapabilitiesClient
	serversPager          SQLServersPager
	managedInstancesPager SQLManagedInstancesPager

	// Lazy SKU catalogue cache. Populated once on the first
	// recommendation conversion in this client's GetRecommendations
	// call — single ListByLocation per region instead of N+1 per rec.
	// A failed fetch leaves skuCacheMap nil; converters then fall back
	// to empty EngineVersion with a one-time WARN log.
	skuCacheOnce sync.Once
	skuCacheMap  map[string]sqlSKUEntry

	// Lazy server-info cache. Populated once by fetchServerInfo, which
	// walks the managed-instances and servers pagers. Both azConfig and
	// deployment are derived in a single pass so the injected test pager
	// is consumed exactly once. Mirrors the cosmosdb cachedAPIType pattern.
	serverInfoOnce sync.Once
	azConfig       string
	deployment     string
}

// NewClient creates a new Azure Database client
func NewClient(cred azcore.TokenCredential, subscriptionID, region string) *DatabaseClient {
	return &DatabaseClient{
		cred:           cred,
		subscriptionID: subscriptionID,
		region:         region,
		httpClient:     httpclient.New(),
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

// SetServersPager sets the SQL servers pager (for testing)
func (c *DatabaseClient) SetServersPager(pager SQLServersPager) {
	c.serversPager = pager
}

// SetManagedInstancesPager sets the SQL managed instances pager (for testing)
func (c *DatabaseClient) SetManagedInstancesPager(pager SQLManagedInstancesPager) {
	c.managedInstancesPager = pager
}

// GetServiceType returns the service type
func (c *DatabaseClient) GetServiceType() common.ServiceType {
	return common.ServiceRelationalDB
}

// GetRegion returns the region
func (c *DatabaseClient) GetRegion() string {
	return c.region
}

// DatabaseRetailPriceItem is the Azure Retail Prices API item shape for
// SQL Database products. Lifted from the previous inline anonymous
// struct so it can serve as the type parameter to pricing.FetchAll.
type DatabaseRetailPriceItem struct {
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
}

// AzureRetailPrice is the service-local envelope consumers still reference.
// The shared pricing walker produces the items slice; this wrapper lets
// the rest of the package keep its `*AzureRetailPrice` idiom without a
// wider rename.
type AzureRetailPrice struct {
	Items []DatabaseRetailPriceItem `json:"Items"`
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
		// NewListPager's first argument is the billing scope, NOT the
		// filter — see the parallel comment in compute/client.go for the
		// failure mode that the wrong shape produced.
		scope := fmt.Sprintf("/subscriptions/%s", c.subscriptionID)
		filter := "properties/scope eq 'Shared' and properties/resourceType eq 'SqlDatabase'"
		pager = client.NewListPager(scope, &armconsumption.ReservationRecommendationsClientListOptions{Filter: &filter})
	}

	for pageIdx := 0; pager.More(); pageIdx++ {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("context cancelled during pagination: %w", err)
		}
		if pageIdx >= maxRecsPages {
			return nil, fmt.Errorf("database: GetRecommendations pagination cap (%d pages) reached", maxRecsPages)
		}
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to get SQL recommendations: %w", err)
		}

		for _, rec := range page.Value {
			converted := c.convertAzureSQLRecommendation(ctx, rec)
			if converted != nil {
				recommendations = append(recommendations, azrecs.ExpandPaymentVariants(*converted)...)
			}
		}
	}

	return recommendations, nil
}

// GetExistingCommitments retrieves existing SQL Database reserved capacity using Azure Resource Graph
func (c *DatabaseClient) GetExistingCommitments(ctx context.Context) ([]common.Commitment, error) {
	pager, err := c.createReservationsPager()
	if err != nil {
		log.Printf("WARNING: failed to create SQL reservations pager: %v", err)
		return []common.Commitment{}, nil
	}

	return c.collectSQLReservations(ctx, pager)
}

// createReservationsPager creates a pager for listing reservations
func (c *DatabaseClient) createReservationsPager() (ReservationsDetailsPager, error) {
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

// collectSQLReservations collects SQL Database reservations from the pager.
// Returns an error on first pagination failure so callers can't silently act
// on a partial list — see the compute client for the full rationale.
func (c *DatabaseClient) collectSQLReservations(ctx context.Context, pager ReservationsDetailsPager) ([]common.Commitment, error) {
	commitments := make([]common.Commitment, 0)

	for pageIdx := 0; pager.More(); pageIdx++ {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("context cancelled during pagination: %w", err)
		}
		if pageIdx >= maxReservationsPages {
			return nil, fmt.Errorf("database: GetExistingCommitments pagination cap (%d pages) reached", maxReservationsPages)
		}
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("database: list reservations: %w", err)
		}

		for _, detail := range page.Value {
			if commitment := c.convertSQLReservation(detail); commitment != nil {
				commitments = append(commitments, *commitment)
			}
		}
	}

	return commitments, nil
}

// convertSQLReservation converts a reservation detail to a commitment if it's a SQL reservation
func (c *DatabaseClient) convertSQLReservation(detail *armconsumption.ReservationDetail) *common.Commitment {
	if detail.Properties == nil {
		return nil
	}

	props := detail.Properties
	if props.SKUName == nil || !strings.Contains(strings.ToLower(*props.SKUName), "sql") {
		return nil
	}

	commitment := &common.Commitment{
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

	return commitment
}

// PurchaseCommitment purchases SQL Database reserved capacity using the two-step
// calculatePrice->purchase flow required by Azure's Reservations API (issue #677).
func (c *DatabaseClient) PurchaseCommitment(ctx context.Context, rec common.Recommendation, opts common.PurchaseOptions) (common.PurchaseResult, error) {
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
			"displayName": reservations.BuildDisplayName(reservations.DisplayNameFields{
				Service:      "sql",
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

// ValidateOffering validates that a SQL Database SKU exists
func (c *DatabaseClient) ValidateOffering(ctx context.Context, rec common.Recommendation) error {
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

	return fmt.Errorf("invalid Azure SQL Database SKU: %s", rec.ResourceType)
}

// GetOfferingDetails retrieves SQL Database reservation offering details from Azure Retail Prices API
func (c *DatabaseClient) GetOfferingDetails(ctx context.Context, rec common.Recommendation) (*common.OfferingDetails, error) {
	termYears, err := reservations.ParseTermYears(rec.Term)
	if err != nil {
		return nil, fmt.Errorf("invalid term: %w", err)
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
		// Fail loud on an unrecognised payment option rather than silently
		// billing it as all-upfront (owner policy: no silent fallbacks on
		// money-affecting fields).
		return nil, fmt.Errorf("unsupported payment option for Azure SQL Database offering details: %q", rec.PaymentOption)
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
	capClient, err := c.getOrCreateCapabilitiesClient()
	if err != nil {
		return nil, err
	}

	capabilities, err := capClient.ListByLocation(ctx, c.region, &armsql.CapabilitiesClientListByLocationOptions{
		Include: nil,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list SQL capabilities: %w", err)
	}

	skuSet := make(map[string]bool)

	// Extract SKUs from server capabilities
	c.extractServerSKUs(capabilities.LocationCapabilities, skuSet)

	// Extract SKUs from managed instance capabilities
	c.extractManagedInstanceSKUs(capabilities.LocationCapabilities, skuSet)

	skus := make([]string, 0, len(skuSet))
	for sku := range skuSet {
		skus = append(skus, sku)
	}

	if len(skus) == 0 {
		return nil, fmt.Errorf("no SQL Database SKUs found for region %s", c.region)
	}

	return skus, nil
}

// getOrCreateCapabilitiesClient returns the injected client or creates a new one
func (c *DatabaseClient) getOrCreateCapabilitiesClient() (CapabilitiesClient, error) {
	if c.capabilitiesClient != nil {
		return c.capabilitiesClient, nil
	}

	client, err := armsql.NewCapabilitiesClient(c.subscriptionID, c.cred, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create capabilities client: %w", err)
	}

	return client, nil
}

// extractServerSKUs extracts SKUs from server version capabilities
func (c *DatabaseClient) extractServerSKUs(capabilities armsql.LocationCapabilities, skuSet map[string]bool) {
	if capabilities.SupportedServerVersions == nil {
		return
	}

	for _, version := range capabilities.SupportedServerVersions {
		if version.SupportedEditions == nil {
			continue
		}

		for _, edition := range version.SupportedEditions {
			if edition.SupportedServiceLevelObjectives == nil {
				continue
			}

			for _, slo := range edition.SupportedServiceLevelObjectives {
				if slo.SKU != nil && slo.SKU.Name != nil {
					skuSet[*slo.SKU.Name] = true
				}
			}
		}
	}
}

// extractManagedInstanceSKUs extracts SKUs from managed instance capabilities
func (c *DatabaseClient) extractManagedInstanceSKUs(capabilities armsql.LocationCapabilities, skuSet map[string]bool) {
	if capabilities.SupportedManagedInstanceVersions == nil {
		return
	}

	for _, version := range capabilities.SupportedManagedInstanceVersions {
		if version.SupportedEditions == nil {
			continue
		}

		for _, edition := range version.SupportedEditions {
			if edition.Name != nil {
				skuSet[*edition.Name] = true
			}
		}
	}
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
	filter := fmt.Sprintf("serviceName eq 'SQL Database' and armRegionName eq '%s' and armSkuName eq '%s'",
		region, sku)

	priceData, err := c.fetchAzurePricing(ctx, filter)
	if err != nil {
		return nil, err
	}

	if len(priceData.Items) == 0 {
		return nil, fmt.Errorf("no pricing data found for SKU %s in region %s", sku, region)
	}

	onDemandPrice, reservationPrice, currency := extractSQLPricing(priceData.Items, termYears)
	if onDemandPrice == 0 {
		return nil, fmt.Errorf("no on-demand pricing found for SKU %s", sku)
	}

	hoursInTerm := 8760.0 * float64(termYears)
	// Return an error rather than fabricating a reservation price from a
	// hardcoded discount multiplier (issue #1020 H4). Presenting an
	// estimated figure as a real TotalCost/SavingsPercentage is misleading
	// and can justify uneconomical purchases. managedredis already uses
	// this pattern as the model.
	if reservationPrice == 0 {
		return nil, fmt.Errorf("no reservation pricing found for SQL Database SKU %s (%d year) in region %s", sku, termYears, region)
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

// fetchAzurePricing fetches pricing data from Azure Retail Prices API,
// following NextPageLink until exhausted or the shared safety cap fires.
// Delegates pagination to pricing.FetchAll — see
// providers/azure/internal/pricing for the per-page timeout, max-pages
// cap, and seen-URL guard invariants.
func (c *DatabaseClient) fetchAzurePricing(ctx context.Context, filter string) (*AzureRetailPrice, error) {
	params := url.Values{}
	params.Add("$filter", filter)
	params.Add("api-version", "2023-01-01-preview")

	initialURL := "https://prices.azure.com/api/retail/prices?" + params.Encode()
	items, err := pricing.FetchAll[DatabaseRetailPriceItem](ctx, c.httpClient, initialURL, pricing.DefaultPageTimeout, pricing.DefaultMaxPages)
	if err != nil {
		return nil, err
	}
	return &AzureRetailPrice{Items: items}, nil
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

// extractSQLPricing extracts on-demand and reservation pricing from price items
func extractSQLPricing(items []DatabaseRetailPriceItem, termYears int) (onDemand, reservation float64, currency string) {
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

	return onDemand, reservation, currency
}

// convertAzureSQLRecommendation converts Azure SQL reservation recommendation to common format.
// See providers/azure/internal/recommendations.Extract for the shared
// SDK-to-struct ladder. Returns nil when the SDK payload is unusable so
// the caller can filter it out.
//
// Details: Engine="sqlserver" + InstanceClass=ResourceType (always
// populated). EngineVersion enriched from the lazily-cached
// armsql.CapabilitiesClient catalogue when the recommendation's SKU
// string matches a ServiceLevelObjective in the location capabilities;
// otherwise stays empty. AZConfig and Deployment are enriched from the
// lazily-cached subscription-wide server/managed-instance lists;
// both stay empty when the fetch fails or the subscription is ambiguous.
func (c *DatabaseClient) convertAzureSQLRecommendation(ctx context.Context, azureRec armconsumption.ReservationRecommendationClassification) *common.Recommendation {
	f := azrecs.Extract(azureRec)
	if f == nil {
		return nil
	}
	details := detailsFromSQLSKU(f.ResourceType)
	if entry, ok := c.cachedSKULookup(ctx, f.ResourceType); ok && entry.engineVersion != "" {
		details.EngineVersion = entry.engineVersion
	}
	if az := c.cachedDominantAZConfig(ctx); az != "" {
		details.AZConfig = az
	}
	if dep := c.cachedDominantDeployment(ctx); dep != "" {
		details.Deployment = dep
	}
	return &common.Recommendation{
		Provider:             common.ProviderAzure,
		Service:              common.ServiceRelationalDB,
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
		Details:              details,
	}
}

// cachedSKULookup returns the SKU catalogue entry for skuName, fetching
// the catalogue lazily on first call. The catalogue is fetched ONCE per
// client lifetime via armsql.CapabilitiesClient.ListByLocation;
// subsequent calls are O(1) map lookups. ok=false on cache miss OR
// catalogue-fetch failure — the caller falls back to empty
// EngineVersion rather than failing the whole conversion.
func (c *DatabaseClient) cachedSKULookup(ctx context.Context, skuName string) (sqlSKUEntry, bool) {
	c.skuCacheOnce.Do(func() {
		c.skuCacheMap = c.fetchSKUCatalogue(ctx)
	})
	if c.skuCacheMap == nil {
		return sqlSKUEntry{}, false
	}
	entry, ok := c.skuCacheMap[skuName]
	return entry, ok
}

// fetchSKUCatalogue performs the single ListByLocation call and reduces
// the response into a name->sqlSKUEntry map keyed by ServiceLevelObjective
// SKU.Name (matches the recommendation engine's ResourceType). Returns
// nil on error so the sync.Once-gated cache field stays nil.
func (c *DatabaseClient) fetchSKUCatalogue(ctx context.Context) map[string]sqlSKUEntry {
	capClient, err := c.getOrCreateCapabilitiesClient()
	if err != nil {
		logging.Warnf("azure database: SKU catalogue capabilities client create failed for region %s: %v — Details.EngineVersion left empty", c.region, err)
		return nil
	}
	resp, err := capClient.ListByLocation(ctx, c.region, &armsql.CapabilitiesClientListByLocationOptions{Include: nil})
	if err != nil {
		logging.Warnf("azure database: SKU catalogue ListByLocation failed for region %s: %v — Details.EngineVersion left empty", c.region, err)
		return nil
	}
	out := make(map[string]sqlSKUEntry)
	for _, version := range resp.LocationCapabilities.SupportedServerVersions {
		if version == nil || version.Name == nil {
			continue
		}
		populateSQLSKUMapFromVersion(out, *version.Name, version.SupportedEditions)
	}
	return out
}

// populateSQLSKUMapFromVersion writes one sqlSKUEntry per ServiceLevelObjective
// SKU.Name found under the given server version's editions. Extracted out of
// fetchSKUCatalogue to keep that function under the cyclomatic-complexity
// threshold enforced by the pre-commit hook. First-write-wins semantics: if
// the same SKU name appears under multiple server versions (rare in
// practice), the first one wins. Order is deterministic per ListByLocation
// response; downstream consumers don't switch behaviour on the
// engine-version delta within a single region.
func populateSQLSKUMapFromVersion(out map[string]sqlSKUEntry, engineVersion string, editions []*armsql.EditionCapability) {
	for _, edition := range editions {
		if edition == nil {
			continue
		}
		for _, slo := range edition.SupportedServiceLevelObjectives {
			if slo == nil || slo.SKU == nil || slo.SKU.Name == nil {
				continue
			}
			skuName := *slo.SKU.Name
			if _, exists := out[skuName]; !exists {
				out[skuName] = sqlSKUEntry{engineVersion: engineVersion}
			}
		}
	}
}

// cachedDominantAZConfig returns the single dominant AZConfig observed
// across all managed instances in the subscription, or "" when the
// answer is ambiguous or unavailable. Values: "zoneRedundant" when all
// instances have ZoneRedundant=true; "none" when all have
// ZoneRedundant=false; "" when the mix is ambiguous, zero instances
// exist, or the fetch fails.
//
// Both AZConfig and Deployment are populated by a single
// fetchServerInfo call gated by serverInfoOnce; there is no double walk.
// This ensures the injected test pager is consumed exactly once.
func (c *DatabaseClient) cachedDominantAZConfig(ctx context.Context) string {
	c.serverInfoOnce.Do(func() {
		c.azConfig, c.deployment = c.fetchServerInfo(ctx)
	})
	return c.azConfig
}

// cachedDominantDeployment returns the dominant Deployment observed
// across the subscription's SQL resources, or "" when ambiguous or
// unavailable. Values: "managed" / "single". Shares the single
// fetchServerInfo walk with cachedDominantAZConfig.
func (c *DatabaseClient) cachedDominantDeployment(ctx context.Context) string {
	c.serverInfoOnce.Do(func() {
		c.azConfig, c.deployment = c.fetchServerInfo(ctx)
	})
	return c.deployment
}

// fetchServerInfo performs a single walk of each pager (managed
// instances and regular servers) to compute both AZConfig and
// Deployment. This ensures the injected test pager for managed instances
// is consumed exactly once, even though the results feed two separate
// cached fields.
//
// AZConfig: "zoneRedundant" / "none" / "" (ambiguous or no signal).
// Deployment: "managed" / "single" / "" (mixed or no signal).
func (c *DatabaseClient) fetchServerInfo(ctx context.Context) (azConfig, deployment string) {
	zoneRedundantCount, nonZoneRedundantCount, managedCount := c.walkManagedInstances(ctx)
	hasServers := c.hasRegularServers(ctx)

	// Derive AZConfig from zone-redundancy counts.
	total := zoneRedundantCount + nonZoneRedundantCount
	switch {
	case total == 0:
		azConfig = ""
	case zoneRedundantCount == total:
		azConfig = "zoneRedundant"
	case nonZoneRedundantCount == total:
		azConfig = "none"
	default:
		azConfig = ""
	}

	// Derive Deployment from presence of managed vs regular servers.
	hasManaged := managedCount > 0
	switch {
	case hasManaged && !hasServers:
		deployment = "managed"
	case hasServers && !hasManaged:
		deployment = "single"
	default:
		deployment = ""
	}

	return azConfig, deployment
}

// walkManagedInstances walks the managed-instances pager once and
// returns the count of zone-redundant, non-zone-redundant, and total
// managed instances observed. Stops immediately on context cancellation
// or unrecoverable page error.
func (c *DatabaseClient) walkManagedInstances(ctx context.Context) (zoneRedundant, nonZoneRedundant, total int) {
	pager, err := c.createManagedInstancesPager()
	if err != nil {
		logging.Warnf("azure database: managed instances pager create failed: %v; AZConfig/Deployment signal unavailable", err)
		return 0, 0, 0
	}
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return zoneRedundant, nonZoneRedundant, total
			}
			logging.Warnf("azure database: managed instances page fetch failed: %v; AZConfig/Deployment signal unavailable", err)
			return zoneRedundant, nonZoneRedundant, total
		}
		for _, mi := range page.Value {
			total++
			if mi == nil || mi.Properties == nil || mi.Properties.ZoneRedundant == nil {
				continue
			}
			if *mi.Properties.ZoneRedundant {
				zoneRedundant++
			} else {
				nonZoneRedundant++
			}
		}
	}
	return zoneRedundant, nonZoneRedundant, total
}

// createManagedInstancesPager returns the injected pager or creates a real one.
func (c *DatabaseClient) createManagedInstancesPager() (SQLManagedInstancesPager, error) {
	if c.managedInstancesPager != nil {
		return c.managedInstancesPager, nil
	}
	client, err := armsql.NewManagedInstancesClient(c.subscriptionID, c.cred, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create managed instances client: %w", err)
	}
	return client.NewListPager(nil), nil
}

// hasRegularServers returns true when at least one SQL server exists in
// the subscription. Stops after the first non-empty page.
func (c *DatabaseClient) hasRegularServers(ctx context.Context) bool {
	pager, err := c.createServersPager()
	if err != nil {
		logging.Warnf("azure database: servers pager create failed: %v; Deployment signal unavailable", err)
		return false
	}
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return false
			}
			logging.Warnf("azure database: servers page fetch failed: %v; Deployment signal unavailable", err)
			return false
		}
		if len(page.Value) > 0 {
			return true
		}
	}
	return false
}

// createServersPager returns the injected pager or creates a real one.
func (c *DatabaseClient) createServersPager() (SQLServersPager, error) {
	if c.serversPager != nil {
		return c.serversPager, nil
	}
	client, err := armsql.NewServersClient(c.subscriptionID, c.cred, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create servers client: %w", err)
	}
	return client.NewListPager(nil), nil
}

// detailsFromSQLSKU parses an Azure SQL SKU string into a
// common.DatabaseDetails value. The Azure Reservation Recommendations
// API returns SKU strings like "GeneralPurpose_Gen5_2" (edition, compute
// generation, vcore count) or "BusinessCritical_Gen5_4". The parser is
// permissive: unknown formats populate InstanceClass and leave the rest
// blank — converters must never return an error on unexpected SKU
// strings because the API can add new SKUs without breaking consumers.
//
// Engine is always "sqlserver". EngineVersion, AZConfig, and Deployment
// are enriched by the lazy subscription-wide cached lookups in
// convertAzureSQLRecommendation; they are left empty here.
func detailsFromSQLSKU(sku string) common.DatabaseDetails {
	// Engine is always SQL Server for an Azure SQL Database reservation.
	d := common.DatabaseDetails{
		Engine:        "sqlserver",
		InstanceClass: sku,
	}
	return d
}
