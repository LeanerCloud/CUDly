// Package savingsplans provides an Azure Savings Plans service client.
package savingsplans

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/billingbenefits/armbillingbenefits"

	"github.com/LeanerCloud/CUDly/pkg/common"
)

// SavingsPlanOrderAliasAPI defines operations on SavingsPlanOrderAlias (enables mocking).
type SavingsPlanOrderAliasAPI interface {
	BeginCreate(
		ctx context.Context,
		savingsPlanOrderAliasName string,
		body armbillingbenefits.SavingsPlanOrderAliasModel,
		options *armbillingbenefits.SavingsPlanOrderAliasClientBeginCreateOptions,
	) (SavingsPlanOrderAliasPoller, error)
}

// SavingsPlanOrderAliasPoller abstracts the LRO poller returned by BeginCreate (enables mocking).
type SavingsPlanOrderAliasPoller interface {
	PollUntilDone(ctx context.Context, options *PollOptions) (armbillingbenefits.SavingsPlanOrderAliasClientCreateResponse, error)
}

// PollOptions mirrors arm/runtime.PollUntilDoneOptions to avoid a direct dependency.
type PollOptions struct {
	Frequency time.Duration
}

// SavingsPlanListAllPager abstracts the list-all pager (enables mocking).
type SavingsPlanListAllPager interface {
	More() bool
	NextPage(ctx context.Context) (armbillingbenefits.SavingsPlanClientListAllResponse, error)
}

// RPValidateAPI defines the ValidatePurchase operation (enables mocking).
type RPValidateAPI interface {
	ValidatePurchase(
		ctx context.Context,
		body armbillingbenefits.SavingsPlanPurchaseValidateRequest,
		options *armbillingbenefits.RPClientValidatePurchaseOptions,
	) (armbillingbenefits.RPClientValidatePurchaseResponse, error)
}

// HTTPClient defines the interface for HTTP calls (enables mocking).
type HTTPClient interface {
	Do(req *http.Request) (*http.Response, error)
}

// Client handles Azure Savings Plans.
type Client struct {
	cred           azcore.TokenCredential
	subscriptionID string
	region         string
	httpClient     HTTPClient

	// Injected in tests to avoid real Azure API calls.
	orderAliasClient SavingsPlanOrderAliasAPI
	listAllPager     SavingsPlanListAllPager
	rpValidateClient RPValidateAPI
}

// NewClient creates a new Azure Savings Plans client.
func NewClient(cred azcore.TokenCredential, subscriptionID, region string) *Client {
	return &Client{
		cred:           cred,
		subscriptionID: subscriptionID,
		region:         region,
		httpClient:     &http.Client{Timeout: 30 * time.Second},
	}
}

// NewClientWithHTTP creates a new Azure Savings Plans client with a custom HTTP client (for testing).
func NewClientWithHTTP(cred azcore.TokenCredential, subscriptionID, region string, httpClient HTTPClient) *Client {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}
	return &Client{
		cred:           cred,
		subscriptionID: subscriptionID,
		region:         region,
		httpClient:     httpClient,
	}
}

// SetOrderAliasClient injects a mock SavingsPlanOrderAliasAPI (for testing).
func (c *Client) SetOrderAliasClient(api SavingsPlanOrderAliasAPI) {
	c.orderAliasClient = api
}

// SetListAllPager injects a mock list-all pager (for testing).
func (c *Client) SetListAllPager(pager SavingsPlanListAllPager) {
	c.listAllPager = pager
}

// SetRPValidateClient injects a mock RPValidateAPI (for testing).
func (c *Client) SetRPValidateClient(api RPValidateAPI) {
	c.rpValidateClient = api
}

// GetServiceType returns the service type for this client.
func (c *Client) GetServiceType() common.ServiceType {
	return common.ServiceSavingsPlans
}

// GetRegion returns the region for this client.
func (c *Client) GetRegion() string {
	return c.region
}

// GetRecommendations returns an empty slice.
//
// Azure does not have a stable public API for Savings Plan purchase
// recommendations (the Benefits Recommendations API is still in preview).
// This mirrors the AWS Savings Plans client which also returns empty here and
// delegates to Cost Explorer centrally.
func (c *Client) GetRecommendations(_ context.Context, _ common.RecommendationParams) ([]common.Recommendation, error) {
	return []common.Recommendation{}, nil
}

// GetExistingCommitments retrieves all active Azure Savings Plans.
func (c *Client) GetExistingCommitments(ctx context.Context) ([]common.Commitment, error) {
	var pager SavingsPlanListAllPager

	if c.listAllPager != nil {
		pager = c.listAllPager
	} else {
		spClient, err := armbillingbenefits.NewSavingsPlanClient(nil, c.cred, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to create savings plan client: %w", err)
		}
		pager = spClient.NewListAllPager(nil)
	}

	commitments := make([]common.Commitment, 0)

	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to list savings plans: %w", err)
		}

		for _, sp := range page.Value {
			commitment := convertSavingsPlan(sp, c.subscriptionID)
			if commitment != nil {
				commitments = append(commitments, *commitment)
			}
		}
	}

	return commitments, nil
}

// convertSavingsPlan maps an armbillingbenefits.SavingsPlanModel to a common.Commitment.
func convertSavingsPlan(sp *armbillingbenefits.SavingsPlanModel, subscriptionID string) *common.Commitment {
	if sp == nil || sp.ID == nil {
		return nil
	}

	commitment := common.Commitment{
		Provider:       common.ProviderAzure,
		Account:        subscriptionID,
		CommitmentID:   *sp.ID,
		CommitmentType: common.CommitmentSavingsPlan,
		Service:        common.ServiceSavingsPlans,
		Count:          1,
		State:          "active",
	}

	if sp.Name != nil {
		commitment.ResourceType = *sp.Name
	}

	if sp.Properties != nil {
		props := sp.Properties

		if props.ProvisioningState != nil {
			commitment.State = string(*props.ProvisioningState)
		}

		if props.EffectiveDateTime != nil {
			commitment.StartDate = *props.EffectiveDateTime
		}

		if props.ExpiryDateTime != nil {
			commitment.EndDate = *props.ExpiryDateTime
		}

		if props.Commitment != nil && props.Commitment.Amount != nil {
			commitment.Cost = *props.Commitment.Amount
		}
	}

	return &commitment
}

// PurchaseCommitment creates an Azure Savings Plan via the OrderAlias API.
func (c *Client) PurchaseCommitment(ctx context.Context, rec common.Recommendation, _ common.PurchaseOptions) (common.PurchaseResult, error) {
	result := common.PurchaseResult{
		Recommendation: rec,
		DryRun:         false,
		Success:        false,
		Timestamp:      time.Now(),
	}

	spDetails, ok := rec.Details.(*common.SavingsPlanDetails)
	if !ok {
		result.Error = fmt.Errorf("invalid service details for Azure Savings Plans")
		return result, result.Error
	}

	term, err := toAzureTerm(rec.Term)
	if err != nil {
		result.Error = err
		return result, result.Error
	}

	grain := armbillingbenefits.CommitmentGrainHourly
	billingScopeID := fmt.Sprintf("/subscriptions/%s", c.subscriptionID)
	appliedScope := armbillingbenefits.AppliedScopeTypeShared
	hourlyAmount := spDetails.HourlyCommitment
	displayName := fmt.Sprintf("cudly-%s-%s", spDetails.PlanType, rec.Term)

	body := armbillingbenefits.SavingsPlanOrderAliasModel{
		SKU: &armbillingbenefits.SKU{Name: toPtr(spDetails.PlanType)},
		Properties: &armbillingbenefits.SavingsPlanOrderAliasProperties{
			DisplayName:      &displayName,
			BillingScopeID:   &billingScopeID,
			Term:             &term,
			AppliedScopeType: &appliedScope,
			Commitment: &armbillingbenefits.Commitment{
				Amount:       &hourlyAmount,
				CurrencyCode: toPtr("USD"),
				Grain:        &grain,
			},
		},
	}

	var aliasClient SavingsPlanOrderAliasAPI
	if c.orderAliasClient != nil {
		aliasClient = c.orderAliasClient
	} else {
		real, err := armbillingbenefits.NewSavingsPlanOrderAliasClient(c.cred, nil)
		if err != nil {
			result.Error = fmt.Errorf("failed to create order alias client: %w", err)
			return result, result.Error
		}
		aliasClient = &realOrderAliasClient{client: real}
	}

	aliasName := fmt.Sprintf("cudly-%d", time.Now().UnixNano())
	poller, err := aliasClient.BeginCreate(ctx, aliasName, body, nil)
	if err != nil {
		result.Error = fmt.Errorf("failed to begin savings plan purchase: %w", err)
		return result, result.Error
	}

	resp, err := poller.PollUntilDone(ctx, nil)
	if err != nil {
		result.Error = fmt.Errorf("savings plan purchase failed: %w", err)
		return result, result.Error
	}

	if resp.ID != nil {
		result.CommitmentID = *resp.ID
		result.Success = true
	} else {
		result.Error = fmt.Errorf("purchase response missing ID")
		return result, result.Error
	}

	return result, nil
}

// ValidateOffering checks whether the Savings Plan configuration is valid.
func (c *Client) ValidateOffering(ctx context.Context, rec common.Recommendation) error {
	validateBody, err := c.buildValidateBody(rec)
	if err != nil {
		return err
	}

	rpClient, err := c.getRPValidateClient()
	if err != nil {
		return err
	}

	resp, err := rpClient.ValidatePurchase(ctx, validateBody, nil)
	if err != nil {
		return fmt.Errorf("failed to validate savings plan: %w", err)
	}

	return checkValidateResponse(resp)
}

// buildValidateBody constructs the SavingsPlanPurchaseValidateRequest from a recommendation.
func (c *Client) buildValidateBody(rec common.Recommendation) (armbillingbenefits.SavingsPlanPurchaseValidateRequest, error) {
	spDetails, ok := rec.Details.(*common.SavingsPlanDetails)
	if !ok {
		return armbillingbenefits.SavingsPlanPurchaseValidateRequest{}, fmt.Errorf("invalid service details for Azure Savings Plans")
	}

	term, err := toAzureTerm(rec.Term)
	if err != nil {
		return armbillingbenefits.SavingsPlanPurchaseValidateRequest{}, err
	}

	grain := armbillingbenefits.CommitmentGrainHourly
	billingScopeID := fmt.Sprintf("/subscriptions/%s", c.subscriptionID)
	appliedScope := armbillingbenefits.AppliedScopeTypeShared
	hourlyAmount := spDetails.HourlyCommitment
	displayName := "cudly-validate"

	return armbillingbenefits.SavingsPlanPurchaseValidateRequest{
		Benefits: []*armbillingbenefits.SavingsPlanOrderAliasModel{
			{
				SKU: &armbillingbenefits.SKU{Name: toPtr(spDetails.PlanType)},
				Properties: &armbillingbenefits.SavingsPlanOrderAliasProperties{
					DisplayName:      &displayName,
					BillingScopeID:   &billingScopeID,
					Term:             &term,
					AppliedScopeType: &appliedScope,
					Commitment: &armbillingbenefits.Commitment{
						Amount:       &hourlyAmount,
						CurrencyCode: toPtr("USD"),
						Grain:        &grain,
					},
				},
			},
		},
	}, nil
}

// getRPValidateClient returns the injected mock or creates a real RPClient.
func (c *Client) getRPValidateClient() (RPValidateAPI, error) {
	if c.rpValidateClient != nil {
		return c.rpValidateClient, nil
	}
	real, err := armbillingbenefits.NewRPClient(c.cred, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create RP client: %w", err)
	}
	return real, nil
}

// checkValidateResponse returns an error if any benefit in the response is invalid.
func checkValidateResponse(resp armbillingbenefits.RPClientValidatePurchaseResponse) error {
	for _, b := range resp.Benefits {
		if b == nil || b.Valid == nil || *b.Valid {
			continue
		}
		reason := ""
		if b.Reason != nil {
			reason = *b.Reason
		}
		return fmt.Errorf("savings plan offering invalid: %s", reason)
	}
	return nil
}

// AzureRetailPrice represents a pricing record from the Azure Retail Prices API.
//
// NOTE: this struct is also defined in services/compute and services/search.
// Consolidating it into a shared internal package is tracked as a follow-up.
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

// GetOfferingDetails retrieves pricing details for an Azure Savings Plan offering.
func (c *Client) GetOfferingDetails(ctx context.Context, rec common.Recommendation) (*common.OfferingDetails, error) {
	spDetails, ok := rec.Details.(*common.SavingsPlanDetails)
	if !ok {
		return nil, fmt.Errorf("invalid service details for Azure Savings Plans")
	}

	// Calculate term hours.
	azTerm, err := toAzureTerm(rec.Term)
	if err != nil {
		return nil, err
	}

	var hoursInTerm float64
	var termStr string
	switch azTerm {
	case armbillingbenefits.TermP1Y:
		hoursInTerm, termStr = 8760.0, "1yr"
	case armbillingbenefits.TermP3Y:
		hoursInTerm, termStr = 26280.0, "3yr"
	case armbillingbenefits.TermP5Y:
		hoursInTerm, termStr = 43800.0, "5yr"
	default:
		return nil, fmt.Errorf("unsupported savings plan term: %s", rec.Term)
	}

	totalCost := spDetails.HourlyCommitment * hoursInTerm

	var upfrontCost, recurringCost float64
	switch rec.PaymentOption {
	case "All Upfront", "all-upfront":
		upfrontCost = totalCost
		recurringCost = 0
	case "Partial Upfront", "partial-upfront":
		upfrontCost = totalCost * 0.5
		recurringCost = (totalCost * 0.5) / hoursInTerm
	case "No Upfront", "no-upfront":
		upfrontCost = 0
		recurringCost = totalCost / hoursInTerm
	default:
		return nil, fmt.Errorf("unsupported payment option: %s", rec.PaymentOption)
	}

	return &common.OfferingDetails{
		ResourceType:        spDetails.PlanType,
		Term:                termStr,
		PaymentOption:       rec.PaymentOption,
		UpfrontCost:         upfrontCost,
		RecurringCost:       recurringCost,
		TotalCost:           totalCost,
		EffectiveHourlyRate: spDetails.HourlyCommitment,
		Currency:            "USD",
	}, nil
}

// fetchOnDemandRate queries the Azure Retail Prices API for the on-demand hourly
// rate for the given plan type. Returns 0 and an error if unavailable.
func (c *Client) fetchOnDemandRate(ctx context.Context, planType string) (float64, error) {
	filter := fmt.Sprintf(
		"serviceFamily eq 'Compute' and priceType eq 'Consumption' and armSkuName eq '%s'",
		url.QueryEscape(planType),
	)
	apiURL := fmt.Sprintf(
		"https://prices.azure.com/api/retail/prices?$filter=%s",
		filter,
	)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return 0, err
	}

	// Attach Bearer token if credential is available.
	if c.cred != nil {
		tokenOpts := policy.TokenRequestOptions{Scopes: []string{"https://management.azure.com/.default"}}
		token, err := c.cred.GetToken(ctx, tokenOpts)
		if err == nil {
			req.Header.Set("Authorization", "Bearer "+token.Token)
		}
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, err
	}

	var pricing AzureRetailPrice
	if err := json.Unmarshal(body, &pricing); err != nil {
		return 0, err
	}

	for _, item := range pricing.Items {
		if item.RetailPrice > 0 {
			return item.RetailPrice, nil
		}
	}

	return 0, nil
}

// GetValidResourceTypes returns the Azure Savings Plan types.
func (c *Client) GetValidResourceTypes(_ context.Context) ([]string, error) {
	return []string{
		"Compute",
		"MachineLearning",
	}, nil
}

// toAzureTerm converts a CUDly term string to an armbillingbenefits.Term.
func toAzureTerm(term string) (armbillingbenefits.Term, error) {
	switch term {
	case "1yr", "1", "P1Y", "":
		return armbillingbenefits.TermP1Y, nil
	case "3yr", "3", "P3Y":
		return armbillingbenefits.TermP3Y, nil
	case "5yr", "5", "P5Y":
		return armbillingbenefits.TermP5Y, nil
	default:
		return "", fmt.Errorf("unsupported savings plan term: %s", term)
	}
}

// toPtr returns a pointer to v.
func toPtr[T any](v T) *T {
	return &v
}

// realOrderAliasClient wraps armbillingbenefits.SavingsPlanOrderAliasClient to
// satisfy SavingsPlanOrderAliasAPI.
type realOrderAliasClient struct {
	client *armbillingbenefits.SavingsPlanOrderAliasClient
}

func (r *realOrderAliasClient) BeginCreate(
	ctx context.Context,
	savingsPlanOrderAliasName string,
	body armbillingbenefits.SavingsPlanOrderAliasModel,
	options *armbillingbenefits.SavingsPlanOrderAliasClientBeginCreateOptions,
) (SavingsPlanOrderAliasPoller, error) {
	poller, err := r.client.BeginCreate(ctx, savingsPlanOrderAliasName, body, options)
	if err != nil {
		return nil, err
	}
	return &realPoller{poller: poller}, nil
}

// realPoller wraps the SDK LRO poller to satisfy SavingsPlanOrderAliasPoller.
type realPoller struct {
	poller *runtime.Poller[armbillingbenefits.SavingsPlanOrderAliasClientCreateResponse]
}

func (r *realPoller) PollUntilDone(ctx context.Context, _ *PollOptions) (armbillingbenefits.SavingsPlanOrderAliasClientCreateResponse, error) {
	return r.poller.PollUntilDone(ctx, nil)
}

// SavingsPlanOrderAliasProperties mirrors armbillingbenefits for use in BeginCreate body.
// Re-exporting via an alias for clarity.
type SavingsPlanOrderAliasProperties = armbillingbenefits.SavingsPlanOrderAliasProperties
