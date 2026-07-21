// Package synapse provides Azure Synapse Analytics Reserved Capacity client.
// Azure Synapse Analytics (formerly SQL Data Warehouse) supports reservation-based
// commitments for Dedicated SQL Pool DWUs and Spark Compute Units (SCUs).
// Reservations are issued via the Azure Capacity / Consumption APIs — the same
// pattern used by cosmosdb and cache in this provider.
package synapse

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
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/reservations/armreservations"

	"github.com/LeanerCloud/CUDly/pkg/common"
	"github.com/LeanerCloud/CUDly/providers/azure/internal/httpclient"
	"github.com/LeanerCloud/CUDly/providers/azure/internal/pricing"
	"github.com/LeanerCloud/CUDly/providers/azure/internal/recommendations"
	"github.com/LeanerCloud/CUDly/providers/azure/services/internal/reservations"
)

// reservationResourceTypeSynapse is the canonical resourceType value for Azure
// Synapse Analytics (Dedicated SQL Pool) in the Consumption API $filter.
// Source: Azure REST API spec for Microsoft.Consumption/reservationRecommendations
// (2021-10-01 stable). The previous hand-written value "SQLDatabaseDTU" is not
// a valid enum member and caused the API to return no recommendations.
const reservationResourceTypeSynapse = "SqlDataWarehouse"

// HTTPClient interface for HTTP operations (enables mocking).
type HTTPClient interface {
	Do(req *http.Request) (*http.Response, error)
}

// RecommendationsPager interface for recommendations pager (enables mocking).
type RecommendationsPager interface {
	More() bool
	NextPage(ctx context.Context) (armconsumption.ReservationRecommendationsClientListResponse, error)
}

// ReservationsDetailsPager interface for reservations details pager (enables mocking).
type ReservationsDetailsPager interface {
	More() bool
	NextPage(ctx context.Context) (armconsumption.ReservationsDetailsClientListResponse, error)
}

// SynapseClient handles Azure Synapse Analytics Reserved Capacity.
type SynapseClient struct {
	cred                 azcore.TokenCredential
	subscriptionID       string
	region               string
	httpClient           HTTPClient
	recommendationsPager RecommendationsPager
	reservationsPager    ReservationsDetailsPager
}

// NewClient creates a new Azure Synapse Analytics client.
func NewClient(cred azcore.TokenCredential, subscriptionID, region string) *SynapseClient {
	return &SynapseClient{
		cred:           cred,
		subscriptionID: subscriptionID,
		region:         region,
		httpClient:     httpclient.New(),
	}
}

// NewClientWithHTTP creates a new Azure Synapse client with a custom HTTP client (for testing).
// When httpClient is nil, the SSRF-hardened httpclient.New() is used so the nil
// fallback also blocks IMDS connections.
func NewClientWithHTTP(cred azcore.TokenCredential, subscriptionID, region string, httpClient HTTPClient) *SynapseClient {
	if httpClient == nil {
		httpClient = httpclient.New()
	}
	return &SynapseClient{
		cred:           cred,
		subscriptionID: subscriptionID,
		region:         region,
		httpClient:     httpClient,
	}
}

// SetRecommendationsPager sets the recommendations pager (for testing).
func (c *SynapseClient) SetRecommendationsPager(pager RecommendationsPager) {
	c.recommendationsPager = pager
}

// SetReservationsPager sets the reservations pager (for testing).
func (c *SynapseClient) SetReservationsPager(pager ReservationsDetailsPager) {
	c.reservationsPager = pager
}

// GetServiceType returns the service type.
func (c *SynapseClient) GetServiceType() common.ServiceType {
	return common.ServiceDataWarehouse
}

// GetRegion returns the region.
func (c *SynapseClient) GetRegion() string {
	return c.region
}

// SynapseRetailPriceItem is the Azure Retail Prices API item shape for
// Synapse Analytics. Used as the type parameter to pricing.FetchAll.
type SynapseRetailPriceItem struct {
	CurrencyCode    string  `json:"currencyCode"`
	RetailPrice     float64 `json:"retailPrice"`
	UnitPrice       float64 `json:"unitPrice"`
	ArmRegionName   string  `json:"armRegionName"`
	ProductName     string  `json:"productName"`
	ServiceName     string  `json:"serviceName"`
	ArmSKUName      string  `json:"armSkuName"`
	MeterName       string  `json:"meterName"`
	SKUName         string  `json:"skuName"`
	ReservationTerm string  `json:"reservationTerm"`
	Type            string  `json:"type"`
}

// GetRecommendations retrieves Synapse reservation recommendations from the
// Azure Consumption API.
func (c *SynapseClient) GetRecommendations(ctx context.Context, _ *common.RecommendationParams) ([]common.Recommendation, error) {
	recs := make([]common.Recommendation, 0)

	var pager RecommendationsPager
	if c.recommendationsPager != nil {
		pager = c.recommendationsPager
	} else {
		client, err := armconsumption.NewReservationRecommendationsClient(c.cred, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to create consumption client: %w", err)
		}
		scope := fmt.Sprintf("/subscriptions/%s", c.subscriptionID)
		filter := "properties/scope eq 'Shared' and properties/resourceType eq '" + reservationResourceTypeSynapse + "'"
		pager = client.NewListPager(scope, &armconsumption.ReservationRecommendationsClientListOptions{Filter: &filter})
	}

	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to get Synapse recommendations: %w", err)
		}

		for _, rec := range page.Value {
			converted := c.convertSynapseRecommendation(rec)
			if converted == nil {
				continue
			}
			if c.region != "" && !strings.EqualFold(converted.Region, c.region) {
				continue
			}
			recs = append(recs, *converted)
		}
	}

	return recs, nil
}

// GetExistingCommitments retrieves existing Synapse reserved capacity
// commitments from the Azure Consumption API.
func (c *SynapseClient) GetExistingCommitments(ctx context.Context) ([]common.Commitment, error) {
	pager, err := c.createReservationsPager()
	if err != nil {
		return nil, fmt.Errorf("synapse: create reservations pager: %w", err)
	}

	return c.collectSynapseReservations(ctx, pager)
}

func (c *SynapseClient) createReservationsPager() (ReservationsDetailsPager, error) {
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

func (c *SynapseClient) collectSynapseReservations(ctx context.Context, pager ReservationsDetailsPager) ([]common.Commitment, error) {
	commitments := make([]common.Commitment, 0)

	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("synapse: list reservations: %w", err)
		}
		for _, detail := range page.Value {
			if commitment := c.convertSynapseReservation(detail); commitment != nil {
				commitments = append(commitments, *commitment)
			}
		}
	}

	return commitments, nil
}

// convertSynapseReservation converts a reservation detail to a Commitment if
// it is a Synapse SQL Pool or Spark reservation. Identification relies on the
// SKU name containing a Synapse-specific prefix ("DW" for Dedicated SQL Pools
// or "SCU" for Spark Compute Units).
func (c *SynapseClient) convertSynapseReservation(detail *armconsumption.ReservationDetail) *common.Commitment {
	if detail == nil || detail.Properties == nil {
		return nil
	}
	props := detail.Properties
	if props.SKUName == nil {
		return nil
	}
	skuLower := strings.ToLower(*props.SKUName)
	if !strings.HasPrefix(skuLower, "dw") &&
		!strings.HasPrefix(skuLower, "scu") &&
		!strings.Contains(skuLower, "synapse") {
		return nil
	}

	commitment := &common.Commitment{
		Provider:       common.ProviderAzure,
		Account:        c.subscriptionID,
		CommitmentType: common.CommitmentReservedInstance,
		Service:        common.ServiceDataWarehouse,
		Region:         c.region,
		State:          "active",
	}
	if props.ReservationID != nil {
		commitment.CommitmentID = *props.ReservationID
	}
	commitment.ResourceType = *props.SKUName
	return commitment
}

// PurchaseCommitment purchases Synapse reserved capacity via the Azure
// Reservations API two-step flow (calculatePrice -> purchase). The reserved
// resource type is "SqlDW" which covers Dedicated SQL Pool DWU reservations.
func (c *SynapseClient) PurchaseCommitment(ctx context.Context, rec common.Recommendation, opts common.PurchaseOptions) (common.PurchaseResult, error) {
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

	if strings.TrimSpace(rec.ResourceType) == "" {
		result.Error = fmt.Errorf("resource type is required")
		return result, result.Error
	}
	if rec.Count <= 0 {
		result.Error = fmt.Errorf("quantity must be greater than zero")
		return result, result.Error
	}

	termYears, err := reservations.ParseTermYears(rec.Term)
	if err != nil {
		result.Error = err
		return result, result.Error
	}

	requestBody := map[string]interface{}{
		"sku": map[string]string{
			"name": rec.ResourceType,
		},
		"location": c.region,
		"properties": map[string]interface{}{
			"reservedResourceType": string(armreservations.ReservedResourceTypeSQLDataWarehouse),
			"billingScopeId":       fmt.Sprintf("/subscriptions/%s", c.subscriptionID),
			"term":                 fmt.Sprintf("P%dY", termYears),
			"quantity":             rec.Count,
			"displayName":          fmt.Sprintf("Synapse SQL Pool Reservation - %s", rec.ResourceType),
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

// ValidateOffering validates that a Synapse SKU is in the known set.
func (c *SynapseClient) ValidateOffering(ctx context.Context, rec common.Recommendation) error {
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
	return fmt.Errorf("invalid Azure Synapse SKU: %s", rec.ResourceType)
}

// GetOfferingDetails retrieves Synapse reservation offering details from the
// Azure Retail Prices API.
func (c *SynapseClient) GetOfferingDetails(ctx context.Context, rec common.Recommendation) (*common.OfferingDetails, error) {
	termYears, err := reservations.ParseTermYears(rec.Term)
	if err != nil {
		return nil, err
	}

	p, err := c.getSynapsePricing(ctx, rec.ResourceType, c.region, termYears)
	if err != nil {
		return nil, fmt.Errorf("failed to get pricing: %w", err)
	}

	var upfrontCost, recurringCost float64
	totalCost := p.ReservationPrice

	switch rec.PaymentOption {
	case "all-upfront", "upfront":
		upfrontCost = totalCost
		recurringCost = 0
	case "monthly", "no-upfront":
		upfrontCost = 0
		recurringCost = totalCost / (float64(termYears) * 12)
	default:
		// Fail loud on an unrecognized payment option rather than silently
		// billing it as all-upfront (owner policy: no silent fallbacks on
		// money-affecting fields).
		return nil, fmt.Errorf("unsupported payment option for Azure Synapse offering details: %q", rec.PaymentOption)
	}

	return &common.OfferingDetails{
		OfferingID:          fmt.Sprintf("azure-synapse-%s-%s-%s", rec.ResourceType, c.region, rec.Term),
		ResourceType:        rec.ResourceType,
		Term:                rec.Term,
		PaymentOption:       rec.PaymentOption,
		UpfrontCost:         upfrontCost,
		RecurringCost:       recurringCost,
		TotalCost:           totalCost,
		EffectiveHourlyRate: p.HourlyRate,
		Currency:            p.Currency,
	}, nil
}

// GetValidResourceTypes returns the known Synapse Dedicated SQL Pool DWU SKUs
// that support reservations. Azure Synapse reservations are available for
// DW100c through DW30000c performance levels.
func (c *SynapseClient) GetValidResourceTypes(_ context.Context) ([]string, error) {
	return []string{
		// Dedicated SQL Pool DWU levels (cDWU generation)
		"DW100c",
		"DW200c",
		"DW300c",
		"DW400c",
		"DW500c",
		"DW1000c",
		"DW1500c",
		"DW2000c",
		"DW2500c",
		"DW3000c",
		"DW5000c",
		"DW6000c",
		"DW7500c",
		"DW10000c",
		"DW15000c",
		"DW30000c",
	}, nil
}

// SynapsePricing holds pricing information for Synapse Analytics.
type SynapsePricing struct {
	HourlyRate        float64
	ReservationPrice  float64
	OnDemandPrice     float64
	Currency          string
	SavingsPercentage float64
}

// getSynapsePricing fetches pricing from the Azure Retail Prices API.
func (c *SynapseClient) getSynapsePricing(ctx context.Context, sku, region string, termYears int) (*SynapsePricing, error) {
	filter := fmt.Sprintf("serviceName eq 'Azure Synapse Analytics' and armRegionName eq '%s' and skuName eq '%s'",
		region, sku)

	params := url.Values{}
	params.Add("$filter", filter)
	params.Add("api-version", "2023-01-01-preview")

	initialURL := "https://prices.azure.com/api/retail/prices?" + params.Encode()
	items, err := pricing.FetchAll[SynapseRetailPriceItem](ctx, c.httpClient, initialURL, pricing.DefaultPageTimeout, pricing.DefaultMaxPages)
	if err != nil {
		return nil, err
	}

	if len(items) == 0 {
		return nil, fmt.Errorf("no pricing data found for Synapse SKU %s in region %s", sku, region)
	}

	onDemandPrice, reservationPrice, currency := extractSynapsePricing(items, termYears)
	if onDemandPrice == 0 {
		return nil, fmt.Errorf("no on-demand pricing found for Synapse SKU %s", sku)
	}

	hoursInTerm := 8760.0 * float64(termYears)
	if reservationPrice == 0 {
		return nil, fmt.Errorf("pricing data unavailable for Synapse SKU %s in region %s: no reservation price returned by API", sku, region)
	}

	savingsPercentage := ((onDemandPrice*hoursInTerm - reservationPrice) / (onDemandPrice * hoursInTerm)) * 100

	return &SynapsePricing{
		HourlyRate:        reservationPrice / hoursInTerm,
		ReservationPrice:  reservationPrice,
		OnDemandPrice:     onDemandPrice * hoursInTerm,
		Currency:          currency,
		SavingsPercentage: savingsPercentage,
	}, nil
}

// extractSynapsePricing extracts on-demand and reservation pricing from price items.
func extractSynapsePricing(items []SynapseRetailPriceItem, termYears int) (onDemand, reservation float64, currency string) {
	currency = "USD"
	termStr := fmt.Sprintf("%d Year", termYears)
	if termYears > 1 {
		termStr = fmt.Sprintf("%d Years", termYears)
	}

	for _, item := range items {
		if item.CurrencyCode != "" {
			currency = item.CurrencyCode
		}
		switch {
		case strings.Contains(item.ReservationTerm, termStr):
			if item.RetailPrice > 0 {
				reservation = item.RetailPrice
			}
		case item.Type == "Consumption" && item.RetailPrice > 0:
			onDemand = item.RetailPrice
		}
	}
	return onDemand, reservation, currency
}

// convertSynapseRecommendation converts an Azure reservation recommendation
// to the common Recommendation format.
func (c *SynapseClient) convertSynapseRecommendation(azureRec armconsumption.ReservationRecommendationClassification) *common.Recommendation {
	f := recommendations.Extract(azureRec)
	if f == nil {
		return nil
	}
	details := common.DataWarehouseDetails{
		NodeType:    f.ResourceType,
		ClusterType: "dedicated-sql-pool",
	}
	return &common.Recommendation{
		Provider:             common.ProviderAzure,
		Service:              common.ServiceDataWarehouse,
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
