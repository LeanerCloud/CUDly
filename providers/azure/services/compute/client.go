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
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v5"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/consumption/armconsumption"

	"github.com/LeanerCloud/CUDly/pkg/common"
	"github.com/LeanerCloud/CUDly/pkg/logging"
	"github.com/LeanerCloud/CUDly/providers/azure/internal/httpclient"
	"github.com/LeanerCloud/CUDly/providers/azure/internal/pricing"
	azrecs "github.com/LeanerCloud/CUDly/providers/azure/internal/recommendations"
	"github.com/LeanerCloud/CUDly/providers/azure/services/internal/reservations"
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

// vmSKUEntry holds the SKU-catalogue-derived fields the converter
// wants for each VM SKU. Sourced from
// armcompute.ResourceSKU.Capabilities (a name/value-pair list):
//   - vCPUs: Capabilities[Name=="vCPUs"].Value, parsed as int.
//   - memoryGB: Capabilities[Name=="MemoryGB"].Value, parsed as float64.
//
// Either field stays at the zero value when the capability is missing
// or unparseable; common.ComputeDetails treats 0 as "unknown" (the
// JSON tags on VCPU/MemoryGB are omitempty).

// maxRecsPages caps Consumption API recommendation pagination to avoid
// burning a Lambda deadline on a stalled or unexpectedly deep result set.
const maxRecsPages = 10

// maxReservationsPages caps reservation-detail pagination.
// Large orgs may have hundreds of reservations spread over many pages.
const maxReservationsPages = 50

// maxSKUPages caps Azure ResourceSKUs pagination.
// The SKU catalogue for a subscription can run to many pages.
const maxSKUPages = 20

type vmSKUEntry struct {
	vCPUs    int
	memoryGB float64
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

	// Lazy SKU catalogue cache. armcompute.ResourceSKUsClient.NewListPager
	// returns every SKU available to the subscription with its
	// Capabilities (vCPUs, MemoryGB). Fetched ONCE per client lifetime;
	// subsequent converter calls in the same GetRecommendations run hit
	// the in-memory map. A failed fetch leaves skuCacheMap nil and
	// converters fall back to VCPU=0/MemoryGB=0 with a WARN log — the
	// conversion itself does NOT fail (graceful-degradation contract,
	// matches cache/cosmosdb/database from PR #81).
	skuCacheOnce sync.Once
	skuCacheMap  map[string]vmSKUEntry

	// Optional injected pager for ListExchangeableReservations. When nil
	// (the production default) the method creates a real
	// armreservations.ReservationClient. Tests inject a stub to run
	// hermetically without Azure credentials.
	exchangeablePager ExchangeableReservationPager
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

// AzureRetailPrice is the paginated response envelope returned by the Azure
// Retail Prices API; NextPageLink is non-empty when additional pages exist.
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

	for pageIdx := 0; pager.More(); pageIdx++ {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("context cancelled during pagination: %w", err)
		}
		if pageIdx >= maxRecsPages {
			return nil, fmt.Errorf("compute: GetRecommendations pagination cap (%d pages) reached", maxRecsPages)
		}
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to get VM recommendations: %w", err)
		}

		for _, rec := range page.Value {
			converted := c.convertAzureVMRecommendation(ctx, rec)
			if converted != nil {
				recommendations = append(recommendations, azrecs.ExpandPaymentVariants(*converted)...)
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

	for pageIdx := 0; pager.More(); pageIdx++ {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("context cancelled during pagination: %w", err)
		}
		if pageIdx >= maxReservationsPages {
			return nil, fmt.Errorf("compute: GetExistingCommitments pagination cap (%d pages) reached", maxReservationsPages)
		}
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

	const apiVersion = "2021-04-01"
	state, err := c.fetchCapacityProviderState(ctx, token.Token, apiVersion)
	if err != nil {
		return err
	}
	if state.RegistrationState == "Registered" {
		return nil
	}
	return c.triggerCapacityProviderRegistration(ctx, token.Token, apiVersion, state.RegistrationState)
}

// fetchCapacityProviderState queries the ARM providers API and returns the
// current registration state of Microsoft.Capacity.
func (c *ComputeClient) fetchCapacityProviderState(ctx context.Context, bearerToken, apiVersion string) (providerRegistrationState, error) {
	checkURL := fmt.Sprintf(
		"https://management.azure.com/subscriptions/%s/providers/Microsoft.Capacity?api-version=%s",
		c.subscriptionID, apiVersion,
	)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, checkURL, nil)
	if err != nil {
		return providerRegistrationState{}, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+bearerToken)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return providerRegistrationState{}, fmt.Errorf("check provider: %w", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// Non-2xx from the provider check (e.g. 403 permissions, 429 throttle).
		// Log and return so the purchase attempt can still proceed; a failed
		// registration check is non-fatal.
		return providerRegistrationState{}, fmt.Errorf("check Microsoft.Capacity provider status returned HTTP %d: %s", resp.StatusCode, string(body))
	}

	var state providerRegistrationState
	if err := json.Unmarshal(body, &state); err != nil {
		return providerRegistrationState{}, fmt.Errorf("decode provider state: %w", err)
	}
	return state, nil
}

// triggerCapacityProviderRegistration POSTs to the ARM register endpoint to
// initiate Microsoft.Capacity provider registration.
func (c *ComputeClient) triggerCapacityProviderRegistration(ctx context.Context, bearerToken, apiVersion, currentState string) error {
	registerURL := fmt.Sprintf(
		"https://management.azure.com/subscriptions/%s/providers/Microsoft.Capacity/register?api-version=%s",
		c.subscriptionID, apiVersion,
	)
	regReq, err := http.NewRequestWithContext(ctx, http.MethodPost, registerURL, nil)
	if err != nil {
		return fmt.Errorf("build register request: %w", err)
	}
	regReq.Header.Set("Authorization", "Bearer "+bearerToken)

	regResp, err := c.httpClient.Do(regReq)
	if err != nil {
		return fmt.Errorf("register provider: %w", err)
	}
	regBody, _ := io.ReadAll(regResp.Body)
	regResp.Body.Close()
	if regResp.StatusCode < 200 || regResp.StatusCode >= 300 {
		return fmt.Errorf("register Microsoft.Capacity provider returned HTTP %d: %s", regResp.StatusCode, string(regBody))
	}

	log.Printf("Triggered Microsoft.Capacity provider registration (was: %q)", currentState)
	return nil
}

// buildReservationBody builds the JSON body for a reservation purchase request.
// The same body is sent to both calculatePrice and purchase endpoints (issue #677).
// The purchase-automation and cudly-idempotency-token tags are attached via
// reservations.ApplyPurchaseTags so the resulting reservation is identifiable
// in the portal AND a re-driven purchase can find it via tag lookup before
// buying a duplicate (issue #721).
func (c *ComputeClient) buildReservationBody(rec common.Recommendation, source, idempotencyToken string) ([]byte, error) {
	termYears, err := reservations.ParseTermYears(rec.Term)
	if err != nil {
		return nil, err
	}
	requestBody := map[string]interface{}{
		"sku":      map[string]string{"name": rec.ResourceType},
		"location": c.region,
		"properties": map[string]interface{}{
			"reservedResourceType": "VirtualMachines",
			"billingScopeId":       fmt.Sprintf("/subscriptions/%s", c.subscriptionID),
			"term":                 fmt.Sprintf("P%dY", termYears),
			"quantity":             rec.Count,
			"displayName": reservations.BuildDisplayName(reservations.DisplayNameFields{
				Service:      "vm",
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
	reservations.ApplyPurchaseTags(requestBody, source, idempotencyToken)
	return json.Marshal(requestBody)
}

// PurchaseCommitment purchases a VM Reserved Instance using the two-step
// calculatePrice->purchase flow required by Azure's Reservations API (issue #677).
//
// Azure shifted newer SKU families (Burstable v2 and others) to require a
// calculatePrice call before purchase. The previous direct-PUT pattern returns
// 400 "Session timed out" for these families. The two-step flow:
//  1. POST calculatePrice -- Azure mints a session-bound reservationOrderId.
//  2. POST reservationOrders/{id}/purchase -- commits the order.
//
// Idempotency: Azure mints the order ID in step 1, so client-supplied IDs are
// no longer used. Re-drives are idempotent via tag-based deduplication
// performed inside reservations.DoIdempotentPurchaseTwoStep: every purchase
// body carries the cudly-idempotency-token tag derived from opts.IdempotencyToken,
// and a re-drive lists existing reservation orders and short-circuits when an
// order already carries the same tag (issue #721).
func (c *ComputeClient) PurchaseCommitment(ctx context.Context, rec common.Recommendation, opts common.PurchaseOptions) (common.PurchaseResult, error) {
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

	// Ensure Microsoft.Capacity provider is registered (cached after first call).
	c.ensureCapacityProviderRegistered(ctx)

	bodyBytes, err := c.buildReservationBody(rec, opts.Source, opts.IdempotencyToken)
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

// ValidateOffering validates that a VM SKU exists
func (c *ComputeClient) ValidateOffering(ctx context.Context, rec common.Recommendation) error {
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

	return fmt.Errorf("invalid Azure VM SKU: %s", rec.ResourceType)
}

// GetOfferingDetails retrieves VM RI offering details from Azure Retail Prices API
func (c *ComputeClient) GetOfferingDetails(ctx context.Context, rec common.Recommendation) (*common.OfferingDetails, error) {
	termYears, err := reservations.ParseTermYears(rec.Term)
	if err != nil {
		return nil, fmt.Errorf("invalid term: %w", err)
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
		// Fail loud on an unrecognised payment option rather than silently
		// billing it as all-upfront (owner policy: no silent fallbacks on
		// money-affecting fields).
		return nil, fmt.Errorf("unsupported payment option for Azure VM offering details: %q", rec.PaymentOption)
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

	for pageIdx := 0; pager.More(); pageIdx++ {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("context cancelled during pagination: %w", err)
		}
		if pageIdx >= maxSKUPages {
			return nil, fmt.Errorf("compute: GetValidResourceTypes pagination cap (%d pages) reached", maxSKUPages)
		}
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
	// Return an error rather than fabricating a reservation price from a
	// hardcoded discount multiplier (issue #1020 H4). Presenting an
	// estimated figure as a real TotalCost/SavingsPercentage is misleading
	// and can justify uneconomical purchases. managedredis already uses
	// this pattern as the model.
	if reservationPrice == 0 {
		return nil, fmt.Errorf("no reservation pricing found for VM size %s (%d year) in region %s", vmSize, termYears, region)
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

// azureTermString returns the Retail Prices API ReservationTerm string for the
// given number of years. The API uses the singular form "1 Year" for one year
// and the plural form "N Years" for two or more years.
func azureTermString(termYears int) string {
	if termYears == 1 {
		return "1 Year"
	}
	return fmt.Sprintf("%d Years", termYears)
}

// extractVMPricing extracts on-demand and reservation pricing from price items
func extractVMPricing(items []AzureRetailPriceItem, termYears int) (onDemand, reservation float64, currency string) {
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

// convertAzureVMRecommendation converts Azure VM reservation recommendation to common format.
//
// Returns nil when the SDK payload is unusable (nil, wrong concrete type,
// or missing Properties) so the caller can filter it out rather than
// append an empty recommendation. The field extraction lives in
// providers/azure/internal/recommendations so the four Azure service
// converters share the same type-assertion + nil-guard ladder.
//
// Details.VCPU and Details.MemoryGB are enriched from a lazily-cached
// armcompute.ResourceSKUsClient catalogue (cachedSKULookup). The
// catalogue is fetched ONCE per client lifetime; converter calls in
// the same GetRecommendations run share the in-memory map (the N+1
// invariant pinned by TestComputeClient_CachedSKULookup_FetchedOnce).
// On catalogue-fetch failure or cache miss, both fields stay at 0
// (the omitempty JSON tags hide them from API payloads) and the
// conversion still succeeds — matches the graceful-degradation
// contract from cache/cosmosdb/database in PR #81.
//
// Platform / Tenancy / Scope still require additional Azure data
// sources (consumption usage records, dedicated-host inventory) and
// remain unpopulated — out of scope for this issue.
func (c *ComputeClient) convertAzureVMRecommendation(ctx context.Context, azureRec armconsumption.ReservationRecommendationClassification) *common.Recommendation {
	f := azrecs.Extract(azureRec)
	if f == nil {
		return nil
	}
	details := common.ComputeDetails{
		InstanceType: f.ResourceType,
	}
	if entry, ok := c.cachedSKULookup(ctx, f.ResourceType); ok {
		if entry.vCPUs > 0 {
			details.VCPU = entry.vCPUs
		}
		if entry.memoryGB > 0 {
			details.MemoryGB = entry.memoryGB
		}
	}
	return &common.Recommendation{
		Provider:             common.ProviderAzure,
		Service:              common.ServiceCompute,
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
// client lifetime via armcompute.ResourceSKUsClient.NewListPager;
// subsequent calls are O(1) map lookups. ok=false on cache miss OR
// catalogue-fetch failure — the caller falls back to VCPU=0 / MemoryGB=0
// rather than failing the whole conversion.
func (c *ComputeClient) cachedSKULookup(ctx context.Context, skuName string) (vmSKUEntry, bool) {
	c.skuCacheOnce.Do(func() {
		c.skuCacheMap = c.fetchSKUCatalogue(ctx)
	})
	if c.skuCacheMap == nil {
		return vmSKUEntry{}, false
	}
	entry, ok := c.skuCacheMap[skuName]
	return entry, ok
}

// fetchSKUCatalogue performs the single ResourceSKUsClient.NewListPager
// walk and reduces the response into a name->vmSKUEntry map keyed by
// SKU.Name (matches the recommendation engine's ResourceType output).
//
// Filters out non-VM resource types and SKUs not available in c.region
// (mirrors the existing extractVMSizeIfValid filter used by
// GetValidResourceTypes — a SKU listed for a different region is not
// safe to attribute to a recommendation in this client's region).
//
// Returns nil on pager-create, page-fetch error, or context cancellation
// so the sync.Once-gated cache field stays nil and cachedSKULookup falls
// back to the empty-fields path. Errors and cancellation are logged WARN
// once; context.Canceled/DeadlineExceeded are treated as terminal
// (feedback_ctx_cancel_terminal.md).
func (c *ComputeClient) fetchSKUCatalogue(ctx context.Context) map[string]vmSKUEntry {
	pager, err := c.createResourceSKUsPager()
	if err != nil {
		logging.Warnf("azure compute: SKU catalogue pager create failed for region %s: %v — Details.VCPU/MemoryGB left at 0", c.region, err)
		return nil
	}
	out := make(map[string]vmSKUEntry)
	for pageIdx := 0; pager.More(); pageIdx++ {
		if err := ctx.Err(); err != nil {
			logging.Warnf("azure compute: SKU catalogue fetch cancelled for region %s after %d pages: %v; partial cache discarded", c.region, pageIdx, err)
			return nil
		}
		if pageIdx >= maxSKUPages {
			logging.Warnf("azure compute: SKU catalogue pagination cap (%d pages) reached for region %s; partial cache (%d entries) used", maxSKUPages, c.region, len(out))
			break
		}
		page, err := pager.NextPage(ctx)
		if err != nil {
			logging.Warnf("azure compute: SKU catalogue page fetch failed for region %s: %v; partial cache (%d entries) discarded, Details.VCPU/MemoryGB left at 0", c.region, err, len(out))
			return nil
		}
		c.populateVMSKUMapFromPage(out, page.Value)
	}
	return out
}

// populateVMSKUMapFromPage writes one vmSKUEntry per qualifying VM SKU
// found in the page into out. Filters non-VM resource types and SKUs
// not available in c.region. First-write-wins on duplicate SKU names —
// ResourceSKUs returns each SKU once per subscription, but defending
// against duplicates keeps the cache deterministic regardless of pager
// order. Extracted out of fetchSKUCatalogue to keep that function
// under the cyclomatic-complexity threshold enforced by the
// pre-commit hook (matches the cache/database extraction pattern from
// PR #81).
func (c *ComputeClient) populateVMSKUMapFromPage(out map[string]vmSKUEntry, skus []*armcompute.ResourceSKU) {
	for _, sku := range skus {
		if sku == nil || sku.Name == nil {
			continue
		}
		if sku.ResourceType == nil || *sku.ResourceType != "virtualMachines" {
			continue
		}
		if !c.isAvailableInRegion(sku, c.region) {
			continue
		}
		if _, exists := out[*sku.Name]; exists {
			continue
		}
		vCPUs, memoryGB := extractVMSKUCapabilities(sku)
		out[*sku.Name] = vmSKUEntry{vCPUs: vCPUs, memoryGB: memoryGB}
	}
}

// extractVMSKUCapabilities pulls the vCPUs and MemoryGB capabilities
// out of an armcompute.ResourceSKU's Capabilities name/value list.
// Returns (0, 0) when the capability is missing or unparseable —
// callers treat the zero value as "unknown" (omitempty JSON tag on
// common.ComputeDetails).
//
// Extracted out of fetchSKUCatalogue to keep that function under the
// cyclomatic-complexity threshold enforced by the pre-commit hook
// (matches the cache/database extraction pattern from PR #81).
func extractVMSKUCapabilities(sku *armcompute.ResourceSKU) (int, float64) {
	var vCPUs int
	var memoryGB float64
	for _, cb := range sku.Capabilities {
		if cb == nil || cb.Name == nil || cb.Value == nil {
			continue
		}
		switch *cb.Name {
		case "vCPUs":
			if v, err := strconv.Atoi(*cb.Value); err == nil {
				vCPUs = v
			}
		case "MemoryGB":
			if v, err := strconv.ParseFloat(*cb.Value, 64); err == nil {
				memoryGB = v
			}
		}
	}
	return vCPUs, memoryGB
}
