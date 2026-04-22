// Package compute provides Azure VM Reserved Instances client
package compute

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v5"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/consumption/armconsumption"
	"github.com/google/uuid"

	"github.com/LeanerCloud/CUDly/pkg/common"
	"github.com/LeanerCloud/CUDly/providers/azure/internal/httpclient"
	"github.com/LeanerCloud/CUDly/providers/azure/internal/pricing"
	"github.com/LeanerCloud/CUDly/providers/azure/internal/recommendations"
)

// RecommendationsPager defines the interface for paging through recommendations
type RecommendationsPager interface {
	More() bool
	NextPage(ctx context.Context) (armconsumption.ReservationRecommendationsClientListResponse, error)
}

// ReservationsDetailsPager defines the interface for paging through reservation details
type ReservationsDetailsPager interface {
	More() bool
	NextPage(ctx context.Context) (armconsumption.ReservationsDetailsClientListResponse, error)
}

// ResourceSKUsPager defines the interface for paging through resource SKUs
type ResourceSKUsPager interface {
	More() bool
	NextPage(ctx context.Context) (armcompute.ResourceSKUsClientListResponse, error)
}

// HTTPClient defines the interface for making HTTP requests
type HTTPClient interface {
	Do(req *http.Request) (*http.Response, error)
}

// ComputeClient handles Azure VM Reserved Instances
type ComputeClient struct {
	cred           azcore.TokenCredential
	subscriptionID string
	region         string
	httpClient     HTTPClient

	// For testing - these can be set to mock implementations
	recommendationsPager RecommendationsPager
	reservationsPager    ReservationsDetailsPager
	resourceSKUsPager    ResourceSKUsPager

	// Microsoft.Capacity provider registration check (cached per client lifetime)
	capacityProviderOnce sync.Once
	capacityProviderErr  error
}

// NewClient creates a new Azure Compute client
func NewClient(cred azcore.TokenCredential, subscriptionID, region string) *ComputeClient {
	return &ComputeClient{
		cred:           cred,
		subscriptionID: subscriptionID,
		region:         region,
		httpClient:     httpclient.New(),
	}
}

// NewClientWithHTTP creates a new Azure Compute client with a custom HTTP client (for testing)
func NewClientWithHTTP(cred azcore.TokenCredential, subscriptionID, region string, httpClient HTTPClient) *ComputeClient {
	return &ComputeClient{
		cred:           cred,
		subscriptionID: subscriptionID,
		region:         region,
		httpClient:     httpClient,
	}
}

// SetRecommendationsPager sets a mock pager for recommendations (for testing)
func (c *ComputeClient) SetRecommendationsPager(pager RecommendationsPager) {
	c.recommendationsPager = pager
}

// SetReservationsPager sets a mock pager for reservations details (for testing)
func (c *ComputeClient) SetReservationsPager(pager ReservationsDetailsPager) {
	c.reservationsPager = pager
}

// SetResourceSKUsPager sets a mock pager for resource SKUs (for testing)
func (c *ComputeClient) SetResourceSKUsPager(pager ResourceSKUsPager) {
	c.resourceSKUsPager = pager
}

// GetServiceType returns the service type
func (c *ComputeClient) GetServiceType() common.ServiceType {
	return common.ServiceCompute
}

// GetRegion returns the region
func (c *ComputeClient) GetRegion() string {
	return c.region
}

// AzureRetailPrice represents pricing from Azure Retail Prices API
type AzureRetailPriceItem struct {
	CurrencyCode    string  `json:"currencyCode"`
	RetailPrice     float64 `json:"retailPrice"`
	UnitPrice       float64 `json:"unitPrice"`
	ArmRegionName   string  `json:"armRegionName"`
	ProductName     string  `json:"productName"`
	ServiceName     string  `json:"serviceName"`
	ArmSKUName      string  `json:"armSkuName"`
	ReservationTerm string  `json:"reservationTerm"`
	Type            string  `json:"type"`
}

type AzureRetailPrice struct {
	Items        []AzureRetailPriceItem `json:"Items"`
	NextPageLink string                 `json:"NextPageLink"`
}

// GetRecommendations gets VM RI recommendations from Azure Consumption API
func (c *ComputeClient) GetRecommendations(ctx context.Context, params common.RecommendationParams) ([]common.Recommendation, error) {
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

		// NewListPager's first argument is the billing scope (the subscription
		// path), NOT the filter. Passing the filter here produced a malformed
		// URL where the ODATA filter got spliced into the URL path between
		// management.azure.com and providers/Microsoft.Consumption/... and
		// every request returned an error. The filter belongs in the
		// ClientListOptions.Filter field.
		scope := fmt.Sprintf("/subscriptions/%s", c.subscriptionID)
		filter := "properties/scope eq 'Shared' and properties/resourceType eq 'VirtualMachines'"
		pager = client.NewListPager(scope, &armconsumption.ReservationRecommendationsClientListOptions{Filter: &filter})
	}

	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to get VM recommendations: %w", err)
		}

		for _, rec := range page.Value {
			converted := c.convertAzureVMRecommendation(ctx, rec)
			if converted != nil {
				recommendations = append(recommendations, *converted)
			}
		}
	}

	return recommendations, nil
}

// GetExistingCommitments retrieves existing VM Reserved Instances
func (c *ComputeClient) GetExistingCommitments(ctx context.Context) ([]common.Commitment, error) {
	pager, err := c.createReservationsPager()
	if err != nil {
		log.Printf("WARNING: failed to create VM reservations pager: %v", err)
		return []common.Commitment{}, nil
	}

	return c.collectVMReservations(ctx, pager)
}

// createReservationsPager creates a pager for listing reservations
func (c *ComputeClient) createReservationsPager() (ReservationsDetailsPager, error) {
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

// collectVMReservations collects VM reservations from the pager.
//
// Returns an error on the first pagination failure rather than silently
// truncating the result set. A partial commitment list is unsafe for the
// purchase flow — it could trigger duplicate purchases for reservations
// that exist but weren't loaded. Callers must treat the error as fatal.
func (c *ComputeClient) collectVMReservations(ctx context.Context, pager ReservationsDetailsPager) ([]common.Commitment, error) {
	commitments := make([]common.Commitment, 0)

	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("compute: list reservations: %w", err)
		}

		for _, detail := range page.Value {
			if commitment := c.convertVMReservation(detail); commitment != nil {
				commitments = append(commitments, *commitment)
			}
		}
	}

	return commitments, nil
}

// convertVMReservation converts a reservation detail to a commitment if it's a VM reservation
func (c *ComputeClient) convertVMReservation(detail *armconsumption.ReservationDetail) *common.Commitment {
	if detail.Properties == nil {
		return nil
	}

	props := detail.Properties
	if props.SKUName == nil || !strings.Contains(strings.ToLower(*props.SKUName), "virtualmachines") {
		return nil
	}

	commitment := &common.Commitment{
		Provider:       common.ProviderAzure,
		Account:        c.subscriptionID,
		CommitmentType: common.CommitmentReservedInstance,
		Service:        common.ServiceCompute,
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

// providerRegistrationState is the JSON shape returned by the ARM providers API.
type providerRegistrationState struct {
	RegistrationState string `json:"registrationState"`
}

// ensureCapacityProviderRegistered checks that the Microsoft.Capacity resource provider
// is registered in the subscription. The check is performed once per client lifetime.
// An error is logged but does not block the purchase attempt.
func (c *ComputeClient) ensureCapacityProviderRegistered(ctx context.Context) {
	c.capacityProviderOnce.Do(func() {
		c.capacityProviderErr = c.checkAndRegisterCapacityProvider(ctx)
		if c.capacityProviderErr != nil {
			log.Printf("WARNING: Microsoft.Capacity provider registration check failed: %v", c.capacityProviderErr)
		}
	})
}

// checkAndRegisterCapacityProvider performs the actual provider registration check.
func (c *ComputeClient) checkAndRegisterCapacityProvider(ctx context.Context) error {
	if c.cred == nil {
		return nil // skip in test environments without credentials
	}

	token, err := c.cred.GetToken(ctx, policy.TokenRequestOptions{
		Scopes: []string{"https://management.azure.com/.default"},
	})
	if err != nil {
		return fmt.Errorf("get token: %w", err)
	}

	apiVersion := "2021-04-01"
	checkURL := fmt.Sprintf(
		"https://management.azure.com/subscriptions/%s/providers/Microsoft.Capacity?api-version=%s",
		c.subscriptionID, apiVersion,
	)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, checkURL, nil)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token.Token)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("check provider: %w", err)
	}
	defer resp.Body.Close()

	var state providerRegistrationState
	if err := json.NewDecoder(resp.Body).Decode(&state); err != nil {
		return fmt.Errorf("decode provider state: %w", err)
	}

	if state.RegistrationState == "Registered" {
		return nil
	}

	// Trigger registration
	registerURL := fmt.Sprintf(
		"https://management.azure.com/subscriptions/%s/providers/Microsoft.Capacity/register?api-version=%s",
		c.subscriptionID, apiVersion,
	)
	regReq, err := http.NewRequestWithContext(ctx, http.MethodPost, registerURL, nil)
	if err != nil {
		return fmt.Errorf("build register request: %w", err)
	}
	regReq.Header.Set("Authorization", "Bearer "+token.Token)
	regResp, err := c.httpClient.Do(regReq)
	if err != nil {
		return fmt.Errorf("register provider: %w", err)
	}
	regResp.Body.Close()

	log.Printf("Triggered Microsoft.Capacity provider registration (was: %q)", state.RegistrationState)
	return nil
}

const (
	purchaseMaxAttempts = 3
	purchaseRetryDelay  = 2 * time.Second
)

// buildReservationBody builds the JSON body for a reservation PUT request.
// When source is non-empty, a top-level tags map carrying purchase-automation
// is attached — Azure's Microsoft.Capacity/reservationOrders PUT body accepts
// tags at creation, so no follow-up call is needed.
func (c *ComputeClient) buildReservationBody(rec common.Recommendation, source string) ([]byte, error) {
	termYears := 1
	if rec.Term == "3yr" || rec.Term == "3" {
		termYears = 3
	}
	requestBody := map[string]interface{}{
		"sku":      map[string]string{"name": rec.ResourceType},
		"location": c.region,
		"properties": map[string]interface{}{
			"reservedResourceType": "VirtualMachines",
			"billingScopeId":       fmt.Sprintf("/subscriptions/%s", c.subscriptionID),
			"term":                 fmt.Sprintf("P%dY", termYears),
			"quantity":             rec.Count,
			"displayName":          fmt.Sprintf("VM Reservation - %s", rec.ResourceType),
			"appliedScopeType":     "Shared",
			"renew":                false,
		},
	}
	if source != "" {
		requestBody["tags"] = map[string]string{common.PurchaseTagKey: source}
	}
	return json.Marshal(requestBody)
}

// isSuccessStatus reports whether the HTTP status code is a successful purchase response.
func isSuccessStatus(code int) bool {
	return code == http.StatusOK || code == http.StatusCreated || code == http.StatusAccepted
}

// doPurchaseWithRetry executes the reservation PUT with 409-retry logic.
// Returns the successful response body (for future use) or an error.
func (c *ComputeClient) doPurchaseWithRetry(ctx context.Context, purchaseURL string, bodyBytes []byte, bearerToken string) error {
	for attempt := 1; attempt <= purchaseMaxAttempts; attempt++ {
		req, err := http.NewRequestWithContext(ctx, "PUT", purchaseURL, strings.NewReader(string(bodyBytes)))
		if err != nil {
			return fmt.Errorf("failed to create request: %w", err)
		}
		req.Header.Set("Authorization", "Bearer "+bearerToken)
		req.Header.Set("Content-Type", "application/json")

		resp, err := c.httpClient.Do(req)
		if err != nil {
			return fmt.Errorf("failed to purchase reservation: %w", err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		if resp.StatusCode == http.StatusConflict && attempt < purchaseMaxAttempts {
			log.Printf("reservation purchase returned 409 (attempt %d/%d), retrying in %s", attempt, purchaseMaxAttempts, purchaseRetryDelay)
			time.Sleep(purchaseRetryDelay)
			continue
		}
		if !isSuccessStatus(resp.StatusCode) {
			return fmt.Errorf("reservation purchase failed with status %d: %s", resp.StatusCode, string(body))
		}
		return nil
	}
	return fmt.Errorf("reservation purchase failed after %d attempts (409 Conflict)", purchaseMaxAttempts)
}

// PurchaseCommitment purchases a VM Reserved Instance
func (c *ComputeClient) PurchaseCommitment(ctx context.Context, rec common.Recommendation, opts common.PurchaseOptions) (common.PurchaseResult, error) {
	result := common.PurchaseResult{
		Recommendation: rec,
		DryRun:         false,
		Success:        false,
		Timestamp:      time.Now(),
	}

	// Ensure Microsoft.Capacity provider is registered (cached after first call).
	c.ensureCapacityProviderRegistered(ctx)

	bodyBytes, err := c.buildReservationBody(rec, opts.Source)
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

	reservationOrderID := uuid.New().String()
	purchaseURL := fmt.Sprintf("https://management.azure.com/providers/Microsoft.Capacity/reservationOrders/%s?api-version=2022-11-01",
		reservationOrderID)

	if err := c.doPurchaseWithRetry(ctx, purchaseURL, bodyBytes, token.Token); err != nil {
		result.Error = err
		return result, result.Error
	}

	result.Success = true
	result.CommitmentID = reservationOrderID
	result.Cost = rec.CommitmentCost
	return result, nil
}

// ValidateOffering validates that a VM SKU exists
func (c *ComputeClient) ValidateOffering(ctx context.Context, rec common.Recommendation) error {
	validSKUs, err := c.GetValidResourceTypes(ctx)
	if err != nil {
		return fmt.Errorf("failed to get valid SKUs: %w", err)
	}

	for _, sku := range validSKUs {
		if sku == rec.ResourceType {
			return nil
		}
	}

	return fmt.Errorf("invalid Azure VM SKU: %s", rec.ResourceType)
}

// GetOfferingDetails retrieves VM RI offering details from Azure Retail Prices API
func (c *ComputeClient) GetOfferingDetails(ctx context.Context, rec common.Recommendation) (*common.OfferingDetails, error) {
	termYears := 1
	if rec.Term == "3yr" || rec.Term == "3" {
		termYears = 3
	}

	pricing, err := c.getVMPricing(ctx, rec.ResourceType, c.region, termYears)
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
		OfferingID:          fmt.Sprintf("azure-vm-%s-%s-%s", rec.ResourceType, c.region, rec.Term),
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

// GetValidResourceTypes returns valid VM sizes from Azure Compute API
func (c *ComputeClient) GetValidResourceTypes(ctx context.Context) ([]string, error) {
	pager, err := c.createResourceSKUsPager()
	if err != nil {
		return nil, err
	}

	vmSizes, err := c.collectVMSizesFromSKUs(ctx, pager)
	if err != nil {
		return nil, err
	}

	if len(vmSizes) == 0 {
		return nil, fmt.Errorf("no VM sizes found for region %s", c.region)
	}

	return vmSizes, nil
}

// createResourceSKUsPager creates a pager for listing resource SKUs
func (c *ComputeClient) createResourceSKUsPager() (ResourceSKUsPager, error) {
	// Use injected pager if available (for testing)
	if c.resourceSKUsPager != nil {
		return c.resourceSKUsPager, nil
	}

	client, err := armcompute.NewResourceSKUsClient(c.subscriptionID, c.cred, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create resource SKUs client: %w", err)
	}

	return client.NewListPager(&armcompute.ResourceSKUsClientListOptions{Filter: nil}), nil
}

// collectVMSizesFromSKUs collects VM sizes from the resource SKUs pager
func (c *ComputeClient) collectVMSizesFromSKUs(ctx context.Context, pager ResourceSKUsPager) ([]string, error) {
	vmSizes := make([]string, 0)

	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to list VM sizes: %w", err)
		}

		for _, sku := range page.Value {
			if vmSize := c.extractVMSizeIfValid(sku); vmSize != "" {
				vmSizes = append(vmSizes, vmSize)
			}
		}
	}

	return vmSizes, nil
}

// extractVMSizeIfValid extracts the VM size name if it's a valid VM in the region
func (c *ComputeClient) extractVMSizeIfValid(sku *armcompute.ResourceSKU) string {
	if sku.Name == nil || sku.ResourceType == nil || *sku.ResourceType != "virtualMachines" {
		return ""
	}

	if !c.isAvailableInRegion(sku, c.region) {
		return ""
	}

	return *sku.Name
}

// isAvailableInRegion checks if a SKU is available in the specified region
func (c *ComputeClient) isAvailableInRegion(sku *armcompute.ResourceSKU, region string) bool {
	if sku.Locations == nil {
		return false
	}

	for _, location := range sku.Locations {
		if location != nil && strings.EqualFold(*location, region) {
			return true
		}
	}

	return false
}

// VMPricing contains VM pricing information
type VMPricing struct {
	HourlyRate        float64
	ReservationPrice  float64
	OnDemandPrice     float64
	Currency          string
	SavingsPercentage float64
}

// getVMPricing gets real VM pricing from Azure Retail Prices API
func (c *ComputeClient) getVMPricing(ctx context.Context, vmSize, region string, termYears int) (*VMPricing, error) {
	filter := fmt.Sprintf("serviceName eq 'Virtual Machines' and armRegionName eq '%s' and armSkuName eq '%s'",
		region, vmSize)

	priceData, err := c.fetchAzurePricing(ctx, filter)
	if err != nil {
		return nil, err
	}

	if len(priceData.Items) == 0 {
		return nil, fmt.Errorf("no pricing data found for VM size %s in region %s", vmSize, region)
	}

	onDemandPrice, reservationPrice, currency := extractVMPricing(priceData.Items, termYears)
	if onDemandPrice == 0 {
		return nil, fmt.Errorf("no on-demand pricing found for VM size %s", vmSize)
	}

	hoursInTerm := 8760.0 * float64(termYears)
	if reservationPrice == 0 {
		reservationPrice = onDemandPrice * hoursInTerm * 0.62 // Azure VMs typically 38% discount
	}

	savingsPercentage := ((onDemandPrice*hoursInTerm - reservationPrice) / (onDemandPrice * hoursInTerm)) * 100

	return &VMPricing{
		HourlyRate:        reservationPrice / hoursInTerm,
		ReservationPrice:  reservationPrice,
		OnDemandPrice:     onDemandPrice * hoursInTerm,
		Currency:          currency,
		SavingsPercentage: savingsPercentage,
	}, nil
}

// fetchAzurePricing fetches pricing data from Azure Retail Prices API,
// following NextPageLink until exhausted (or the shared safety cap is
// hit). Delegates the pagination walk to pricing.FetchAll so every
// service client shares the same per-page timeout, seen-URL guard, and
// max-pages cap — see providers/azure/internal/pricing for those
// invariants.
//
// The earlier version issued a single GET and decoded just the first
// page, so any SKU/term/region combination that landed on page 2+
// produced a "no on-demand pricing found" error or a wrong price
// estimate.
func (c *ComputeClient) fetchAzurePricing(ctx context.Context, filter string) (*AzureRetailPrice, error) {
	params := url.Values{}
	params.Add("$filter", filter)
	params.Add("api-version", "2023-01-01-preview")

	initialURL := "https://prices.azure.com/api/retail/prices?" + params.Encode()
	items, err := pricing.FetchAll[AzureRetailPriceItem](ctx, c.httpClient, initialURL, pricing.DefaultPageTimeout, pricing.DefaultMaxPages)
	if err != nil {
		return nil, err
	}
	return &AzureRetailPrice{Items: items}, nil
}

// extractVMPricing extracts on-demand and reservation pricing from price items
func extractVMPricing(items []AzureRetailPriceItem, termYears int) (onDemand, reservation float64, currency string) {
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

// convertAzureVMRecommendation converts Azure VM reservation recommendation to common format.
//
// Returns nil when the SDK payload is unusable (nil, wrong concrete type,
// or missing Properties) so the caller can filter it out rather than
// append an empty recommendation. The field extraction lives in
// providers/azure/internal/recommendations so the four Azure service
// converters share the same type-assertion + nil-guard ladder.
//
// Details populated from the reservation recommendation payload only —
// Platform / Tenancy / Scope (and the vCPU / memory counts a UI would
// want) require an ARM SKU-catalogue lookup; populating them here would
// trigger an N+1 armcompute.ResourceSKUsClient.ListByLocation per
// recommendation. That batched-enrichment is tracked as a follow-up in
// known_issues/10_azure_provider.md.
func (c *ComputeClient) convertAzureVMRecommendation(_ context.Context, azureRec armconsumption.ReservationRecommendationClassification) *common.Recommendation {
	f := recommendations.Extract(azureRec)
	if f == nil {
		return nil
	}
	return &common.Recommendation{
		Provider:         common.ProviderAzure,
		Service:          common.ServiceCompute,
		Account:          c.subscriptionID,
		Region:           f.Region,
		ResourceType:     f.ResourceType,
		Count:            f.Count,
		OnDemandCost:     f.OnDemandCost,
		CommitmentCost:   f.CommitmentCost,
		EstimatedSavings: f.EstimatedSavings,
		CommitmentType:   common.CommitmentReservedInstance,
		Term:             f.Term,
		PaymentOption:    "upfront",
		Timestamp:        time.Now(),
		// Only InstanceType is safely derivable from the payload. Platform,
		// Tenancy, Scope are armcompute.ResourceSKUsClient territory and
		// are deferred to the batched-enrichment follow-up.
		Details: common.ComputeDetails{
			InstanceType: f.ResourceType,
		},
	}
}
