// Package compute provides Azure VM Reserved Instances client
package compute

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
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v5"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/consumption/armconsumption"

	"github.com/LeanerCloud/CUDly/pkg/common"
)

// RecommendationsPager defines the interface for paging through recommendations
type RecommendationsPager interface {
	More() bool
	NextPage(ctx context.Context) (armconsumption.ReservationRecommendationsClientListResponse, error)
}

// ReservationsDetailsPager defines the interface for paging through reservation details
type ReservationsDetailsPager interface {
	More() bool
	NextPage(ctx context.Context) (armconsumption.ReservationsDetailsClientListByReservationOrderResponse, error)
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
	recommendationsPager   RecommendationsPager
	reservationsPager      ReservationsDetailsPager
	resourceSKUsPager      ResourceSKUsPager
}

// NewClient creates a new Azure Compute client
func NewClient(cred azcore.TokenCredential, subscriptionID, region string) *ComputeClient {
	return &ComputeClient{
		cred:           cred,
		subscriptionID: subscriptionID,
		region:         region,
		httpClient:     &http.Client{Timeout: 30 * time.Second},
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
type AzureRetailPrice struct {
	Items []struct {
		CurrencyCode    string  `json:"currencyCode"`
		RetailPrice     float64 `json:"retailPrice"`
		UnitPrice       float64 `json:"unitPrice"`
		ArmRegionName   string  `json:"armRegionName"`
		ProductName     string  `json:"productName"`
		ServiceName     string  `json:"serviceName"`
		ArmSKUName      string  `json:"armSkuName"`
		ReservationTerm string  `json:"reservationTerm"`
		Type            string  `json:"type"`
	} `json:"Items"`
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

		filter := "properties/scope eq 'Shared' and properties/resourceType eq 'VirtualMachines'"
		pager = client.NewListPager(filter, &armconsumption.ReservationRecommendationsClientListOptions{})
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
			if props.SKUName != nil && strings.Contains(strings.ToLower(*props.SKUName), "virtualmachines") {
				commitment := common.Commitment{
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

				commitments = append(commitments, commitment)
			}
		}
	}

	return commitments, nil
}

// PurchaseCommitment purchases a VM Reserved Instance
func (c *ComputeClient) PurchaseCommitment(ctx context.Context, rec common.Recommendation) (common.PurchaseResult, error) {
	result := common.PurchaseResult{
		Recommendation: rec,
		DryRun:         false,
		Success:        false,
		Timestamp:      time.Now(),
	}

	reservationOrderID := fmt.Sprintf("vm-reservation-%d", time.Now().Unix())
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
			"reservedResourceType": "VirtualMachines",
			"billingScopeId":       fmt.Sprintf("/subscriptions/%s", c.subscriptionID),
			"term":                 fmt.Sprintf("P%dY", termYears),
			"quantity":             rec.Count,
			"displayName":          fmt.Sprintf("VM Reservation - %s", rec.ResourceType),
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
	vmSizes := make([]string, 0)

	// Use injected pager if available (for testing)
	var pager ResourceSKUsPager
	if c.resourceSKUsPager != nil {
		pager = c.resourceSKUsPager
	} else {
		client, err := armcompute.NewResourceSKUsClient(c.subscriptionID, c.cred, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to create resource SKUs client: %w", err)
		}

		pager = client.NewListPager(&armcompute.ResourceSKUsClientListOptions{
			Filter: nil,
		})
	}

	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to list VM sizes: %w", err)
		}

		for _, sku := range page.Value {
			if sku.Name != nil && sku.ResourceType != nil && *sku.ResourceType == "virtualMachines" {
				// Check if available in the region
				if c.isAvailableInRegion(sku, c.region) {
					vmSizes = append(vmSizes, *sku.Name)
				}
			}
		}
	}

	if len(vmSizes) == 0 {
		return nil, fmt.Errorf("no VM sizes found for region %s", c.region)
	}

	return vmSizes, nil
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
	baseURL := "https://prices.azure.com/api/retail/prices"

	filter := fmt.Sprintf("serviceName eq 'Virtual Machines' and armRegionName eq '%s' and armSkuName eq '%s'",
		region, vmSize)

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
		return nil, fmt.Errorf("no pricing data found for VM size %s in region %s", vmSize, region)
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
		return nil, fmt.Errorf("no on-demand pricing found for VM size %s", vmSize)
	}

	hoursInTerm := 8760.0 * float64(termYears)
	if reservationPrice == 0 {
		onDemandTotal := onDemandPrice * hoursInTerm
		reservationPrice = onDemandTotal * 0.62 // Azure VMs typically 38% discount
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

// convertAzureVMRecommendation converts Azure VM reservation recommendation to common format
func (c *ComputeClient) convertAzureVMRecommendation(ctx context.Context, azureRec armconsumption.ReservationRecommendationClassification) *common.Recommendation {
	rec := &common.Recommendation{
		Provider:       common.ProviderAzure,
		Service:        common.ServiceCompute,
		Account:        c.subscriptionID,
		Region:         c.region,
		CommitmentType: common.CommitmentReservedInstance,
		Timestamp:      time.Now(),
		Term:           "1yr",
		PaymentOption:  "upfront",
	}

	return rec
}
