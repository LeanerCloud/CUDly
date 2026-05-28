// Package savingsplans provides an Azure Savings Plans service client.
package savingsplans

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/billingbenefits/armbillingbenefits"

	"github.com/LeanerCloud/CUDly/pkg/common"
	"github.com/LeanerCloud/CUDly/providers/azure/internal/httpclient"
	"github.com/LeanerCloud/CUDly/providers/azure/internal/pricing"
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
		httpClient:     httpclient.New(),
	}
}

// NewClientWithHTTP creates a new Azure Savings Plans client with a custom HTTP client (for testing).
// When httpClient is nil, the SSRF-hardened httpclient.New() is used so the nil
// fallback also blocks IMDS connections.
func NewClientWithHTTP(cred azcore.TokenCredential, subscriptionID, region string, httpClient HTTPClient) *Client {
	if httpClient == nil {
		httpClient = httpclient.New()
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
//
// NOTE: because this stub succeeds unconditionally, it is excluded from the
// all-attempted-failed guard in the parent RecommendationsClientAdapter
// (providers/azure/recommendations.go, COR-03). When this method starts
// making real API calls, flip that call site's attempted flag back to the
// params-filter value so its failures count toward the guard.
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
//
// Idempotency: the SavingsPlanOrderAlias is created with an HTTP PUT on the
// alias name (Microsoft.BillingBenefits/savingsPlanOrderAliases/{name}), so a
// PUT with a previously-used name returns the existing order alias rather than
// creating a second savings plan. When opts.IdempotencyToken is set (the
// purchase-execution path), the alias name is derived deterministically from it
// via common.ReservationOrderID so a re-drive of a stranded execution (issue
// #636) re-PUTs the same alias instead of double-buying. When the token is empty
// (the CLI path, which has no owning execution), the prior timestamp-based name
// is retained.
func (c *Client) PurchaseCommitment(ctx context.Context, rec common.Recommendation, opts common.PurchaseOptions) (common.PurchaseResult, error) {
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

	// Derive a deterministic alias name from the idempotency token when present
	// so a re-drive PUTs the same alias (idempotent), falling back to the prior
	// timestamp-based name on the CLI path (empty token).
	aliasName := common.ReservationOrderID(opts.IdempotencyToken, fmt.Sprintf("cudly-%d", time.Now().UnixNano()))
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

// AzureRetailPrice is the response envelope for the Azure Retail Prices API.
type AzureRetailPrice = pricing.Page[pricing.RetailPriceItem]

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
// rate for the given plan type. The $filter is built with url.Values so that
// spaces and quotes are properly percent-encoded (issue #1021 H3). Pagination is
// handled by pricing.FetchAll (seen-URL guard, max-pages cap, per-page timeout).
// Returns an error when no price record is found rather than (0, nil), which would
// conflate "zero rate" with "not found" (feedback_nullable_not_zero).
func (c *Client) fetchOnDemandRate(ctx context.Context, planType string) (float64, error) {
	filter := fmt.Sprintf(
		"serviceFamily eq 'Compute' and priceType eq 'Consumption' and armSkuName eq '%s'",
		planType,
	)
	params := url.Values{}
	params.Add("$filter", filter)
	params.Add("api-version", "2023-01-01-preview")
	initialURL := "https://prices.azure.com/api/retail/prices?" + params.Encode()

	items, err := pricing.FetchAll[pricing.RetailPriceItem](ctx, c.httpClient, initialURL, pricing.DefaultPageTimeout, pricing.DefaultMaxPages)
	if err != nil {
		return 0, fmt.Errorf("failed to fetch on-demand rate for plan type %s: %w", planType, err)
	}

	for _, item := range items {
		if item.RetailPrice > 0 {
			return item.RetailPrice, nil
		}
	}

	return 0, fmt.Errorf("no on-demand pricing found for savings plan type %s", planType)
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
