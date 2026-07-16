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
	// FetchOfferingClass calls AWS DescribeReservedInstances to determine
	// whether a given Reserved Instance is 'standard' or 'convertible'. Used
	// when the purchase_history row has no offering_class stored (pre-migration
	// 000084 rows and externally-created Standard RIs).
	FetchOfferingClass(ctx context.Context, reservedInstancesID string) (string, error)
}

// buildMarketplaceEC2Client honors the injected factory for tests, falling
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

	// Reject explicitly non-standard classes before the RBAC call (fast path).
	// Empty offering_class is allowed here: the listing handler populates it
	// lazily from AWS before performing the definitive check.
	if isKnownNonStandardOfferingClass(row.OfferingClass) {
		return nil, body, NewClientError(400, "only Standard Reserved Instances can be listed on the AWS Marketplace; this purchase has offering_class="+row.OfferingClass)
	}

	// Enforce sell-any / sell-own RBAC.
	if err := h.authorizeSessionSell(ctx, session, row.CloudAccountID); err != nil {
		return nil, body, err
	}

	// Reject if a listing is already active to avoid duplicate listings.
	if strings.EqualFold(row.ListingState, config.ListingStateActive) {
		return nil, body, NewClientError(409, fmt.Sprintf("an active marketplace listing %s already exists for this RI; cancel it first", row.ListingID))
	}

	// Decode optional body.
	if req.Body != "" {
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

	// Populate offering_class from AWS when the DB row has none. This covers
	// two cases: (1) pre-migration 000084 rows purchased by CUDly before this
	// column existed, and (2) externally-created Standard RIs whose
	// offering_class was never stamped. After population the value is persisted
	// so subsequent requests do not require an extra AWS API call.
	offeringClass := row.OfferingClass
	if offeringClass == "" {
		offeringClass, err = h.populateOfferingClass(ctx, purchaseID, ec2Client)
		if err != nil {
			return nil, err
		}
	}
	// Definitive offering_class gate: only Standard RIs can be listed.
	if !strings.EqualFold(offeringClass, "standard") {
		return nil, NewClientError(400, "only Standard Reserved Instances can be listed on the AWS Marketplace; this purchase has offering_class="+offeringClass)
	}

	awsSchedule := make([]ec2svc.MarketplacePriceTier, 0, len(schedule))
	for _, t := range schedule {
		awsSchedule = append(awsSchedule, ec2svc.MarketplacePriceTier{
			Term:  t.TermMonths,
			Price: t.Price,
		})
	}

	result, err := h.reserveAndCreateListing(ctx, purchaseID, row, ec2Client, awsSchedule)
	if err != nil {
		return nil, err
	}

	return &MarketplaceListResponse{
		ListingID:     result.ListingID,
		ListingState:  result.State,
		PriceSchedule: schedule,
		AWSFeePercent: awsMarketplaceFeePercent,
		Note:          fmt.Sprintf("AWS charges a %d%% transaction fee on the listing proceeds. Net proceeds = ListingPrice * %.2f.", awsMarketplaceFeePercent, awsMarketplaceNetFactor),
	}, nil
}

// reserveAndCreateListing atomically claims the marketplace-listing slot for the
// row, creates the AWS listing, and persists it, releasing the claim on every
// failure path so a failed attempt never leaves the row stuck in the transient
// pending state. Extracted from marketplaceList to keep that handler under the
// gocyclo budget. Returns the persisted listing result on success.
func (h *Handler) reserveAndCreateListing(ctx context.Context, purchaseID string, row *config.PurchaseHistoryRecord, ec2Client marketplaceEC2Client, awsSchedule []ec2svc.MarketplacePriceTier) (ec2svc.MarketplaceListingResult, error) {
	// List every RI in the row: a row of N Standard RIs must list all N, not a
	// single unit (issue #292 multi-count fix). Floor at 1 for legacy rows that
	// somehow recorded a non-positive count so a valid Standard RI still lists,
	// and reject an implausibly large count rather than silently truncating it
	// into int32 (which could list the wrong number of RIs on the money path).
	instanceCount := int32(1)
	switch {
	case row.Count > math.MaxInt32:
		return ec2svc.MarketplaceListingResult{}, NewClientError(500, fmt.Sprintf("purchase count %d exceeds the marketplace listing limit", row.Count))
	case row.Count > 1:
		instanceCount = int32(row.Count)
	}

	// Atomically reserve the listing slot before calling AWS. The read-only
	// listing_state check in validateMarketplaceListRequest is not enough on its
	// own: two concurrent requests can both pass it, and AWS assigns a fresh
	// ClientToken per call so the provider will not dedup them, leaving two live
	// listings for one RI. The claim serializes concurrent creates for the same
	// purchase_id (issue #292 CR concurrent-create guard).
	claimed, err := h.config.ClaimMarketplaceListingSlot(ctx, purchaseID)
	if err != nil {
		return ec2svc.MarketplaceListingResult{}, fmt.Errorf("failed to reserve marketplace listing slot: %w", err)
	}
	if !claimed {
		return ec2svc.MarketplaceListingResult{}, NewClientError(409, "a marketplace listing is already active or in progress for this RI; cancel it first")
	}

	result, err := ec2Client.CreateMarketplaceListing(ctx, ec2svc.MarketplaceListingRequest{
		ReservedInstancesID: purchaseID,
		ClientToken:         uuid.New().String(),
		PriceSchedule:       awsSchedule,
		InstanceCount:       instanceCount,
	})
	if err != nil {
		// AWS created nothing: release the claim so a retry is not blocked.
		h.releaseMarketplaceClaim(ctx, purchaseID, row)
		logging.Warnf("marketplace: CreateReservedInstancesListing for purchase %s failed: %v", purchaseID, err)
		return ec2svc.MarketplaceListingResult{}, mapAWSMarketplaceError("AWS marketplace listing failed", err)
	}

	// Persist the listing ID and state. On DB failure, attempt a compensating
	// rollback (cancel the just-created listing) to avoid a desync where the
	// user sees success but the listing is invisible in subsequent renders, then
	// release the claim so the row does not stay stuck in the pending state.
	if dbErr := h.config.UpdatePurchaseHistoryListing(ctx, purchaseID, result.ListingID, result.State); dbErr != nil {
		logging.Errorf("marketplace: listing created (%s / %s) but DB update failed: %v; attempting rollback", result.ListingID, result.State, dbErr)
		if _, rollbackErr := ec2Client.CancelMarketplaceListing(ctx, result.ListingID); rollbackErr != nil {
			logging.Errorf("marketplace: rollback cancel for listing %s also failed: %v", result.ListingID, rollbackErr)
		} else {
			logging.Warnf("marketplace: listing %s rolled back (canceled) after DB failure", result.ListingID)
		}
		h.releaseMarketplaceClaim(ctx, purchaseID, row)
		return ec2svc.MarketplaceListingResult{}, fmt.Errorf("listing created but could not be persisted; listing has been rolled back: %w", dbErr)
	}

	return result, nil
}

// populateOfferingClass calls AWS DescribeReservedInstances to determine the
// offering class for an RI whose purchase_history row lacks one, then persists
// the result so subsequent requests are served from the DB. Returns the class
// string on success; returns an error if AWS reports no such RI.
//
// The DB stamp is best-effort: a write failure is logged but does not block the
// listing request because the in-memory class value is authoritative for this
// request.
func (h *Handler) populateOfferingClass(ctx context.Context, purchaseID string, ec2Client marketplaceEC2Client) (string, error) {
	class, err := ec2Client.FetchOfferingClass(ctx, purchaseID)
	if err != nil {
		return "", fmt.Errorf("offering_class not set and could not fetch from AWS: %w", err)
	}
	if stampErr := h.config.StampOfferingClass(ctx, purchaseID, class); stampErr != nil {
		logging.Errorf("marketplace: failed to persist offering_class %q for purchase %s: %v", class, purchaseID, stampErr)
	}
	return class, nil
}

// releaseMarketplaceClaim restores a purchase_history row's listing fields to
// the state captured before ClaimMarketplaceListingSlot reserved the slot. It
// runs on every failure path after a successful claim so a failed listing
// attempt does not leave the row stuck in the transient pending state (which
// would block future list attempts). Best-effort: on error it logs loudly
// because the row may stay pending until the #292 status poller reconciles it.
func (h *Handler) releaseMarketplaceClaim(ctx context.Context, purchaseID string, row *config.PurchaseHistoryRecord) {
	if err := h.config.UpdatePurchaseHistoryListing(ctx, purchaseID, row.ListingID, row.ListingState); err != nil {
		logging.Errorf("marketplace: failed to release listing claim for purchase %s (row may be stuck in %q): %v", purchaseID, config.ListingStatePending, err)
	}
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

	if !strings.EqualFold(row.ListingState, config.ListingStateActive) {
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

	// The listing is already canceled in AWS; there is no compensating rollback
	// available if the DB write fails. Return an internal error so the caller
	// knows the state is out of sync and can retry or contact an administrator.
	if dbErr := h.config.UpdatePurchaseHistoryListing(ctx, purchaseID, result.ListingID, result.State); dbErr != nil {
		logging.Errorf("marketplace: listing canceled in AWS (%s) but DB update failed: %v; state is out of sync", result.ListingID, dbErr)
		return nil, fmt.Errorf("listing canceled in AWS but could not be persisted: %w", dbErr)
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

	// Admins are recognized by holding the full-access admin capability
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
		// Admins are recognized by holding the full-access admin capability
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

// awsMarketplaceFeePercent is the AWS Marketplace transaction fee percentage
// charged against listing proceeds. Published at
// https://aws.amazon.com/marketplace/ri-marketplace/faq/
const awsMarketplaceFeePercent = 12

// awsMarketplaceNetFactor is the fraction of list price the seller keeps
// after the AWS Marketplace fee: 1 - awsMarketplaceFeePercent/100.
// Derived from awsMarketplaceFeePercent so the two stay in sync automatically.
const awsMarketplaceNetFactor = 1 - awsMarketplaceFeePercent/100.0

// awsMarketplaceBuyerDiscountFactor is the discount applied to the computed
// residual RI value when building the default listing price. A 5% discount
// makes the listing attractive to buyers while still recovering most of the
// seller's remaining cost basis. Applied before AWS deducts its fee.
const awsMarketplaceBuyerDiscountFactor = 0.95

// awsMarketplaceMinPriceFloorFraction is the minimum ratio of prorated
// residual value that a caller-supplied price schedule must total across its
// tiers. At 5%, a schedule totalling less than 1/20 of what the default
// schedule would offer is rejected with a 400 so a user cannot accidentally
// (or maliciously) list an RI at $0 or a nominal amount. The default schedule
// uses awsMarketplaceBuyerDiscountFactor (95%) of residual; this floor is
// intentionally far below that to give sellers flexibility while preventing
// zero-price listings.
const awsMarketplaceMinPriceFloorFraction = 0.05

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
// awsMarketplaceBuyerDiscountFactor (5% discount to attract buyers; the
// awsMarketplaceFeePercent% AWS fee is applied by the Marketplace on top).
//
// remainingMonths must be the actual remaining months (computed via
// computeRemainingMonths from purchase timestamp and total term -- NOT the raw
// term field, which would overprice older RIs).
// originalTerm is the full contract term in months, used to prorate the
// upfront cost to its remaining value.
// isKnownNonStandardOfferingClass reports true when offering_class is
// explicitly set to a non-standard value (e.g. "convertible"). An empty string
// returns false because the class is not yet known and will be resolved lazily.
func isKnownNonStandardOfferingClass(class string) bool {
	return class != "" && !strings.EqualFold(class, "standard")
}

// checkSuppliedScheduleFloor returns a non-nil error when the total value of
// a caller-supplied price schedule falls below awsMarketplaceMinPriceFloorFraction
// of the computed residual value. Extracted from resolveMarketplacePriceSchedule
// to keep that function within the gocyclo budget. The check is skipped when
// residual is zero (fully-elapsed term or $0 RI) to avoid spurious rejections.
func checkSuppliedScheduleFloor(tiers []MarketplacePriceTier, remainingMonths, originalTerm int, upfrontCost, monthlyCost float64) error {
	if remainingMonths <= 0 {
		return nil
	}
	upfrontRemaining := 0.0
	if originalTerm > 0 {
		upfrontRemaining = upfrontCost * (float64(remainingMonths) / float64(originalTerm))
	}
	residual := upfrontRemaining + monthlyCost*float64(remainingMonths)
	if residual <= 0 {
		return nil
	}
	floor := residual * awsMarketplaceMinPriceFloorFraction
	var total float64
	for _, t := range tiers {
		total += t.Price * float64(t.TermMonths)
	}
	if total < floor {
		return fmt.Errorf(
			"total listing price (%.2f) is below the minimum floor (%.2f, %.0f%% of residual value %.2f); raise your price schedule or omit it to use the default",
			total, floor, awsMarketplaceMinPriceFloorFraction*100, residual)
	}
	return nil
}

func resolveMarketplacePriceSchedule(supplied []MarketplacePriceTier, remainingMonths, originalTerm int, upfrontCost, monthlyCost float64) ([]MarketplacePriceTier, error) {
	if len(supplied) > 0 {
		for i, t := range supplied {
			if t.TermMonths <= 0 {
				return nil, fmt.Errorf("price_schedule[%d]: term_months must be a positive integer", i)
			}
			if t.Price <= 0 {
				return nil, fmt.Errorf("price_schedule[%d]: price must be positive (received %.4f); use a non-zero listing price", i, t.Price)
			}
		}
		if err := checkSuppliedScheduleFloor(supplied, remainingMonths, originalTerm, upfrontCost, monthlyCost); err != nil {
			return nil, err
		}
		return supplied, nil
	}

	// Default: spread (upfront_remaining + future_recurring) * awsMarketplaceBuyerDiscountFactor
	// across remaining term. The upfront cost is prorated by (remaining/original) to
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
	listPrice := totalValue * awsMarketplaceBuyerDiscountFactor
	if listPrice < 0 {
		listPrice = 0
	}
	return []MarketplacePriceTier{
		{TermMonths: int64(remainingMonths), Price: listPrice},
	}, nil
}
