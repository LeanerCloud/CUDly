package api

// handler_marketplace.go implements the Sell-on-Marketplace flow for Standard
// Reserved Instances (issue #292).
//
// Endpoints:
//   POST /api/purchases/{id}/marketplace-list    create a listing
//   POST /api/purchases/{id}/marketplace-cancel  cancel an active listing
//
// Both endpoints require the caller to hold sell-any:purchases (admin or a
// custom operator group) or sell-own:purchases for rows they purchased
// themselves. The purchase_id in the URL path is the AWS ReservedInstancesId
// stamped into purchase_history.purchase_id at completion.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/LeanerCloud/CUDly/internal/auth"
	"github.com/LeanerCloud/CUDly/internal/config"
	"github.com/LeanerCloud/CUDly/pkg/logging"
	awsprovider "github.com/LeanerCloud/CUDly/providers/aws"
	ec2svc "github.com/LeanerCloud/CUDly/providers/aws/services/ec2"
	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-sdk-go-v2/aws"
	smithy "github.com/aws/smithy-go"
	"github.com/google/uuid"
)

// marketplaceEC2Client is the narrow EC2 interface the marketplace handlers
// need. Using a minimal interface keeps test stubs small.
type marketplaceEC2Client interface {
	CreateMarketplaceListing(ctx context.Context, req ec2svc.MarketplaceListingRequest) (ec2svc.MarketplaceListingResult, error)
	DescribeMarketplaceListing(ctx context.Context, listingID string) (ec2svc.MarketplaceListingResult, error)
	CancelMarketplaceListing(ctx context.Context, listingID string) (ec2svc.MarketplaceListingResult, error)
}

// buildMarketplaceEC2Client honours the injected factory for tests, falling
// back to the direct AWS SDK constructor in production.
func (h *Handler) buildMarketplaceEC2Client(cfg aws.Config) marketplaceEC2Client {
	if h.marketplaceEC2Factory != nil {
		return h.marketplaceEC2Factory(cfg)
	}
	return awsprovider.NewEC2ClientDirect(cfg)
}

// MarketplacePriceTier is the JSON-decodable shape accepted in the request body.
type MarketplacePriceTier struct {
	// TermMonths is the remaining-months count this tier covers.
	TermMonths int64 `json:"term_months"`
	// Price is the USD list price per unit for this tier.
	Price float64 `json:"price"`
}

// MarketplaceListRequest is the request body for POST .../marketplace-list.
// PriceSchedule is optional: when absent the handler computes a default from
// the row's upfront_cost, monthly_cost, term, and a 5% discount to attract
// buyers (documented in the response body so the caller can see what was used).
type MarketplaceListRequest struct {
	PriceSchedule []MarketplacePriceTier `json:"price_schedule,omitempty"`
}

// MarketplaceListResponse is the JSON response body for a successful listing.
type MarketplaceListResponse struct {
	ListingID     string                 `json:"listing_id"`
	ListingState  string                 `json:"listing_state"`
	PriceSchedule []MarketplacePriceTier `json:"price_schedule"`
	AWSFeePercent float64                `json:"aws_fee_percent"`
	Note          string                 `json:"note,omitempty"`
}

// validateMarketplaceListRequest performs the pre-flight checks shared by the
// listing flow: session auth, UUID validation, row lookup, offering_class /
// RBAC / duplicate-listing gates, and optional body decode. It returns the
// validated history row and the decoded request body. Extracted from
// marketplaceList to keep that handler's cyclomatic complexity in check.
func (h *Handler) validateMarketplaceListRequest(ctx context.Context, req *events.LambdaFunctionURLRequest, purchaseID string) (*config.PurchaseHistoryRecord, MarketplaceListRequest, error) {
	var body MarketplaceListRequest

	session, err := h.requireSession(ctx, req)
	if err != nil {
		return nil, body, err
	}

	if err := validateUUID(purchaseID); err != nil {
		return nil, body, err
	}

	// Look up the purchase_history row to validate offering_class + get metadata.
	row, err := h.config.GetPurchaseHistoryByPurchaseID(ctx, purchaseID)
	if err != nil {
		return nil, body, fmt.Errorf("failed to look up purchase: %w", err)
	}
	if row == nil {
		return nil, body, NewClientError(404, "purchase not found")
	}

	// Only Standard RIs can be listed on the Marketplace.
	if !strings.EqualFold(row.OfferingClass, "standard") {
		return nil, body, NewClientError(400, "only Standard Reserved Instances can be listed on the AWS Marketplace; this purchase has offering_class="+row.OfferingClass)
	}

	// Enforce sell-any / sell-own RBAC.
	if err := h.authorizeSessionSell(ctx, session, row.CloudAccountID); err != nil {
		return nil, body, err
	}

	// Reject if a listing is already active to avoid duplicate listings.
	if strings.EqualFold(row.ListingState, "active") {
		return nil, body, NewClientError(409, fmt.Sprintf("an active marketplace listing %s already exists for this RI; cancel it first", row.ListingID))
	}

	// Decode optional body.
	if len(req.Body) > 0 {
		if err := json.Unmarshal([]byte(req.Body), &body); err != nil {
			return nil, body, NewClientError(400, "invalid request body: "+err.Error())
		}
	}

	return row, body, nil
}

// marketplaceList handles POST /api/purchases/{id}/marketplace-list.
// The {id} must be the purchase_history.purchase_id (AWS ReservedInstancesId).
func (h *Handler) marketplaceList(ctx context.Context, req *events.LambdaFunctionURLRequest, purchaseID string) (any, error) {
	row, body, err := h.validateMarketplaceListRequest(ctx, req, purchaseID)
	if err != nil {
		return nil, err
	}

	// Compute actual remaining months from the purchase timestamp and total
	// term so the default price schedule reflects real remaining value
	// rather than the full contract term (which overprices older RIs).
	remainingMonths := computeRemainingMonths(row.Timestamp, row.Term)

	// Validate and normalise the price schedule. A nil MonthlyCost means the
	// provider recorded no recurring breakdown (issue #258 made the field
	// nullable); treat absent as zero recurring contribution to residual value.
	monthlyCost := 0.0
	if row.MonthlyCost != nil {
		monthlyCost = *row.MonthlyCost
	}
	schedule, err := resolveMarketplacePriceSchedule(body.PriceSchedule, remainingMonths, row.Term, row.UpfrontCost, monthlyCost)
	if err != nil {
		return nil, NewClientError(400, err.Error())
	}

	// Build per-provider AWS config for the region the RI lives in.
	cfg, err := h.loadAWSConfigWithRegion(ctx, row.Region)
	if err != nil {
		return nil, fmt.Errorf("failed to load AWS config: %w", err)
	}

	ec2Client := h.buildMarketplaceEC2Client(cfg)

	awsSchedule := make([]ec2svc.MarketplacePriceTier, 0, len(schedule))
	for _, t := range schedule {
		awsSchedule = append(awsSchedule, ec2svc.MarketplacePriceTier{
			Term:  t.TermMonths,
			Price: t.Price,
		})
	}

	// List every RI in the row: a row of N Standard RIs must list all N, not a
	// single unit (issue #292 multi-count fix). Floor at 1 for legacy rows that
	// somehow recorded a non-positive count so a valid Standard RI still lists.
	instanceCount := int32(row.Count)
	if instanceCount < 1 {
		instanceCount = 1
	}

	result, err := ec2Client.CreateMarketplaceListing(ctx, ec2svc.MarketplaceListingRequest{
		ReservedInstancesID: purchaseID,
		ClientToken:         uuid.New().String(),
		PriceSchedule:       awsSchedule,
		InstanceCount:       instanceCount,
	})
	if err != nil {
		logging.Warnf("marketplace: CreateReservedInstancesListing for purchase %s failed: %v", purchaseID, err)
		return nil, mapAWSMarketplaceError("AWS marketplace listing failed", err)
	}

	// Persist the listing ID and state. On DB failure, attempt a compensating
	// rollback (cancel the just-created listing) to avoid a desync where the
	// user sees success but the listing is invisible in subsequent renders.
	if dbErr := h.config.UpdatePurchaseHistoryListing(ctx, purchaseID, result.ListingID, result.State); dbErr != nil {
		logging.Errorf("marketplace: listing created (%s / %s) but DB update failed: %v — attempting rollback", result.ListingID, result.State, dbErr)
		if _, rollbackErr := ec2Client.CancelMarketplaceListing(ctx, result.ListingID); rollbackErr != nil {
			logging.Errorf("marketplace: rollback cancel for listing %s also failed: %v", result.ListingID, rollbackErr)
		} else {
			logging.Warnf("marketplace: listing %s rolled back (cancelled) after DB failure", result.ListingID)
		}
		return nil, fmt.Errorf("listing created but could not be persisted; listing has been rolled back: %w", dbErr)
	}

	return &MarketplaceListResponse{
		ListingID:     result.ListingID,
		ListingState:  result.State,
		PriceSchedule: schedule,
		AWSFeePercent: 12,
		Note:          "AWS charges a 12% transaction fee on the listing proceeds. Net proceeds = ListingPrice * 0.88.",
	}, nil
}

// marketplaceCancel handles POST /api/purchases/{id}/marketplace-cancel.
func (h *Handler) marketplaceCancel(ctx context.Context, req *events.LambdaFunctionURLRequest, purchaseID string) (any, error) {
	session, err := h.requireSession(ctx, req)
	if err != nil {
		return nil, err
	}

	if err := validateUUID(purchaseID); err != nil {
		return nil, err
	}

	row, err := h.config.GetPurchaseHistoryByPurchaseID(ctx, purchaseID)
	if err != nil {
		return nil, fmt.Errorf("failed to look up purchase: %w", err)
	}
	if row == nil {
		return nil, NewClientError(404, "purchase not found")
	}

	if err := h.authorizeSessionSell(ctx, session, row.CloudAccountID); err != nil {
		return nil, err
	}

	if !strings.EqualFold(row.ListingState, "active") {
		return nil, NewClientError(409, "no active listing found for this RI; current state: "+row.ListingState)
	}

	cfg, err := h.loadAWSConfigWithRegion(ctx, row.Region)
	if err != nil {
		return nil, fmt.Errorf("failed to load AWS config: %w", err)
	}

	ec2Client := h.buildMarketplaceEC2Client(cfg)

	result, err := ec2Client.CancelMarketplaceListing(ctx, row.ListingID)
	if err != nil {
		logging.Warnf("marketplace: CancelReservedInstancesListing for listing %s failed: %v", row.ListingID, err)
		return nil, mapAWSMarketplaceError("AWS cancel listing failed", err)
	}

	// The listing is already cancelled in AWS; there is no compensating rollback
	// available if the DB write fails. Return an internal error so the caller
	// knows the state is out of sync and can retry or contact an administrator.
	if dbErr := h.config.UpdatePurchaseHistoryListing(ctx, purchaseID, result.ListingID, result.State); dbErr != nil {
		logging.Errorf("marketplace: listing cancelled in AWS (%s) but DB update failed: %v — state is out of sync", result.ListingID, dbErr)
		return nil, fmt.Errorf("listing cancelled in AWS but could not be persisted: %w", dbErr)
	}

	return map[string]string{"listing_id": result.ListingID, "listing_state": result.State}, nil
}

// authorizeSessionSell returns nil when the session is permitted to perform a
// sell/marketplace action under the sell-any / sell-own RBAC rules. The
// cloudAccountID is the cloud account that owns the RI (used for sell-own to
// confirm the session's allowed accounts cover that account). Returns a 403
// ClientError otherwise.
//
// sell-own semantics: a non-admin user can list/cancel RIs for cloud accounts
// they are permitted to access (allowed_accounts covers the account). This is
// intentionally looser than cancel-own (which checks the session UserID against
// created_by_user_id) because purchase_history rows lack a created_by_user_id.
func (h *Handler) authorizeSessionSell(ctx context.Context, session *Session, cloudAccountID *string) error {
	if h.auth == nil {
		return NewClientError(500, "authentication service not configured")
	}

	// Admins are recognised by holding the full-access admin capability
	// (auth migrated from role-based to group-membership-only, issue #907).
	isAdmin, err := h.auth.HasPermissionAPI(ctx, session.UserID, auth.ActionAdmin, auth.ResourceAll)
	if err != nil {
		return fmt.Errorf("permission check failed: %w", err)
	}
	if isAdmin {
		return nil
	}

	hasAny, err := h.auth.HasPermissionAPI(ctx, session.UserID, auth.ActionSellAny, auth.ResourcePurchases)
	if err != nil {
		return fmt.Errorf("permission check failed: %w", err)
	}
	if hasAny {
		return nil
	}

	hasOwn, err := h.auth.HasPermissionAPI(ctx, session.UserID, auth.ActionSellOwn, auth.ResourcePurchases)
	if err != nil {
		return fmt.Errorf("permission check failed: %w", err)
	}
	if !hasOwn {
		return NewClientError(403, "permission denied: requires sell-any or sell-own on purchases")
	}

	// sell-own: verify the session covers the cloud account that holds the RI.
	// When cloudAccountID is nil (ambient/legacy row), deny for non-admins.
	if cloudAccountID == nil {
		return NewClientError(403, "permission denied: cannot sell an RI from an ambiguous (non-per-account) purchase row without sell-any")
	}
	return h.authorizeAllowedAccount(ctx, session, *cloudAccountID)
}

// authorizeAllowedAccount returns nil when the session's allowed_accounts
// permit access to the given cloud account UUID. Returns 403 otherwise.
func (h *Handler) authorizeAllowedAccount(ctx context.Context, session *Session, cloudAccountID string) error {
	if h.auth != nil {
		// Admins are recognised by holding the full-access admin capability
		// (auth migrated from role-based to group-membership-only, issue #907).
		isAdmin, err := h.auth.HasPermissionAPI(ctx, session.UserID, auth.ActionAdmin, auth.ResourceAll)
		if err != nil {
			return fmt.Errorf("admin permission check failed: %w", err)
		}
		if isAdmin {
			return nil
		}
	}
	allowed, err := h.getAllowedAccounts(ctx, session)
	if err != nil {
		return fmt.Errorf("failed to check allowed accounts: %w", err)
	}
	// Empty list means "no restriction" (the user has access to all accounts).
	if len(allowed) == 0 {
		return nil
	}
	for _, id := range allowed {
		if id == "*" || id == cloudAccountID {
			return nil
		}
	}
	return NewClientError(403, "permission denied: purchase is in a cloud account not covered by your session's allowed accounts")
}

// computeRemainingMonths returns the number of whole months remaining on an RI
// given its purchase timestamp and total term in months. The result is floored
// at 1 so defensive callers always get a positive value.
func computeRemainingMonths(purchaseTime time.Time, termMonths int) int {
	if purchaseTime.IsZero() || termMonths <= 0 {
		return 1
	}
	elapsed := time.Since(purchaseTime)
	elapsedMonths := elapsed.Hours() / (24 * 30.4375)
	remaining := float64(termMonths) - elapsedMonths
	r := int(math.Floor(remaining))
	if r < 1 {
		return 1
	}
	return r
}

// awsMarketplaceClientFaultCodes is the set of AWS error codes that represent
// client-side faults for Marketplace listing operations. These map to 4xx
// responses so the caller receives an actionable message. Server-side AWS
// errors remain 5xx.
var awsMarketplaceClientFaultCodes = map[string]bool{
	"InvalidReservedInstancesId":            true,
	"InvalidReservedInstancesId.NotFound":   true,
	"InvalidParameterValue":                 true,
	"InvalidParameter":                      true,
	"IncorrectState":                        true,
	"InvalidReservedInstancesListingId":     true,
	"ReservedInstancesListingAlreadyExists": true,
	"SellerNotRegistered":                   true,
	"AuthFailure":                           true,
	"UnauthorizedOperation":                 true,
}

// mapAWSMarketplaceError maps an AWS SDK error to an appropriate ClientError.
// AWS client-fault errors (4xx-category codes) produce a 4xx response with the
// original AWS message so the caller gets actionable feedback. All other errors
// produce a 502 (AWS-side failure).
func mapAWSMarketplaceError(opMsg string, err error) error {
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		if awsMarketplaceClientFaultCodes[apiErr.ErrorCode()] || apiErr.ErrorFault() == smithy.FaultClient {
			return NewClientError(400, apiErr.ErrorMessage())
		}
	}
	return NewClientError(502, opMsg+": "+err.Error())
}

// resolveMarketplacePriceSchedule returns a normalised price schedule for the
// given RI. When the caller supplied an explicit schedule it is validated and
// returned unchanged. When the caller omitted the schedule (nil / empty), a
// single-tier default is computed: (upfront_remaining + future_recurring) *
// 0.95 (5% discount to attract buyers; the 12% AWS fee is applied by the
// Marketplace on top).
//
// remainingMonths must be the actual remaining months (computed via
// computeRemainingMonths from purchase timestamp and total term -- NOT the raw
// term field, which would overprice older RIs).
// originalTerm is the full contract term in months, used to prorate the
// upfront cost to its remaining value.
func resolveMarketplacePriceSchedule(supplied []MarketplacePriceTier, remainingMonths, originalTerm int, upfrontCost, monthlyCost float64) ([]MarketplacePriceTier, error) {
	if len(supplied) > 0 {
		for i, t := range supplied {
			if t.TermMonths <= 0 {
				return nil, fmt.Errorf("price_schedule[%d]: term_months must be a positive integer", i)
			}
			if t.Price < 0 {
				return nil, fmt.Errorf("price_schedule[%d]: price must be non-negative", i)
			}
		}
		return supplied, nil
	}

	// Default: spread (upfront_remaining + future_recurring) * 0.95 across
	// remaining term. The upfront cost is prorated by (remaining/original) to
	// avoid overpricing older RIs (a 12-month RI at month 6 retains only half
	// the upfront value; using the full amount would overprice by ~2x).
	if remainingMonths <= 0 {
		remainingMonths = 1 // defensive: should not happen for an active RI
	}
	upfrontRemaining := 0.0
	if originalTerm > 0 {
		upfrontRemaining = upfrontCost * (float64(remainingMonths) / float64(originalTerm))
	}
	totalValue := upfrontRemaining + (monthlyCost * float64(remainingMonths))
	listPrice := totalValue * 0.95
	if listPrice < 0 {
		listPrice = 0
	}
	return []MarketplacePriceTier{
		{TermMonths: int64(remainingMonths), Price: listPrice},
	}, nil
}
