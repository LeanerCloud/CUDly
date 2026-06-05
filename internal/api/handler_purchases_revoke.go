package api

// handler_purchases_revoke.go implements POST /api/purchases/{purchaseId}/revoke
// which lets a session-authenticated user revoke a completed purchase while it
// is still within the provider's free-cancel window (issue #290).
//
// Per-provider support:
//
//   - Azure reservations: return via armreservations.ReturnClient within a 7-day
//     window. The button is shown in the History UI for Azure rows inside the
//     window. Requires CalculateRefund first (to get the session ID) then Return.
//
//   - AWS EC2 RIs / Savings Plans: AWS does not expose a direct cancel API for
//     purchased RIs. Revocation requires an AWS Support case
//     (support:CreateCase). That flow is deferred to Phase 2 (#291). For now the
//     endpoint returns 422 and the frontend hides the button for AWS rows, per
//     the constraint: "if a provider has no cancel API, the button must be hidden".
//
//   - GCP commitments: no free-cancel window. Button hidden for GCP rows.

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	armreservations "github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/reservations/armreservations"
	"github.com/LeanerCloud/CUDly/internal/auth"
	"github.com/LeanerCloud/CUDly/internal/config"
	"github.com/LeanerCloud/CUDly/pkg/logging"
	"github.com/aws/aws-lambda-go/events"
)

// AzureRevocationWindowDays is the number of days after purchase within which
// Azure reservations are eligible for a return (refund). Per Azure docs:
// https://learn.microsoft.com/azure/cost-management-billing/reservations/exchange-and-refund-azure-reservations
// Aliases config.AzureRevocationWindowDays so the purchase-write path and this
// endpoint share a single source of truth for the window length.
const AzureRevocationWindowDays = config.AzureRevocationWindowDays

// azureReturnClient is the narrow interface over armreservations.ReturnClient
// used by the revoke handler. Extracted for test injection.
type azureReturnClient interface {
	Post(ctx context.Context, reservationOrderID string, body armreservations.RefundRequest, options *armreservations.ReturnClientPostOptions) (armreservations.ReturnClientPostResponse, error)
}

// azureCalculateRefundClient is the narrow interface over
// armreservations.CalculateRefundClient used to obtain the session ID required
// before calling ReturnClient.Post.
type azureCalculateRefundClient interface {
	Post(ctx context.Context, reservationOrderID string, body armreservations.CalculateRefundRequest, options *armreservations.CalculateRefundClientPostOptions) (armreservations.CalculateRefundClientPostResponse, error)
}

// revokePurchaseResult is the JSON body returned on a successful revocation.
type revokePurchaseResult struct {
	Status     string `json:"status"`
	RevokedAt  string `json:"revoked_at"`
	RevokedVia string `json:"revoked_via"`
}

// revokePurchase handles POST /api/purchases/{purchaseId}/revoke.
//
// Authorization: session required + revoke-own:purchases (or revoke-any for
// admins). The handler is fail-closed: if the auth service is nil the request
// is rejected with 403.
func (h *Handler) revokePurchase(ctx context.Context, req *events.LambdaFunctionURLRequest, purchaseID string) (any, error) {
	if purchaseID == "" {
		return nil, NewClientError(400, "purchase_id is required")
	}

	// Fail-closed: auth service nil means we cannot verify permissions.
	// Check before requireSession so this always surfaces as a 403 ClientError.
	if h.auth == nil {
		return nil, NewClientError(403, "authentication service not configured")
	}

	session, err := h.requireSession(ctx, req)
	if err != nil {
		return nil, err
	}

	record, err := h.config.GetPurchaseHistoryByPurchaseID(ctx, purchaseID)
	if err != nil {
		return nil, fmt.Errorf("revoke: load purchase %s: %w", purchaseID, err)
	}
	if record == nil {
		return nil, NewClientError(404, "purchase not found")
	}

	if err := h.authorizeSessionRevoke(ctx, session, record); err != nil {
		return nil, err
	}

	// Idempotency: already revoked.
	if record.RevokedAt != nil {
		return &revokePurchaseResult{
			Status:     "already_revoked",
			RevokedAt:  record.RevokedAt.Format(time.RFC3339),
			RevokedVia: record.RevokedVia,
		}, nil
	}

	return h.dispatchProviderRevoke(ctx, record)
}

// dispatchProviderRevoke routes a revocation request to the correct
// provider-specific implementation. Extracted from revokePurchase to keep
// that function's cyclomatic complexity within the project limit.
func (h *Handler) dispatchProviderRevoke(ctx context.Context, record *config.PurchaseHistoryRecord) (any, error) {
	switch record.Provider {
	case "azure":
		return h.revokeAzurePurchase(ctx, record)
	case "aws":
		// AWS does not expose a direct RI cancel API. Phase 2 (#291) adds the
		// AWS Support case path. Return 422 so the frontend hides this button.
		return nil, NewClientError(422, "AWS RIs cannot be revoked via direct API; contact AWS Support for a refund within 24h of purchase")
	case "gcp":
		return nil, NewClientError(422, "GCP commitments do not have a free-cancel window")
	default:
		return nil, NewClientError(422, fmt.Sprintf("provider %q does not support in-app revocation", record.Provider))
	}
}

// authorizeSessionRevoke enforces the revoke-any / revoke-own RBAC matrix.
// Mirror of authorizeSessionCancel / authorizeSessionApprove patterns.
func (h *Handler) authorizeSessionRevoke(ctx context.Context, session *Session, record *config.PurchaseHistoryRecord) error {
	// The stateless admin API key has full access and no user row to resolve
	// permissions from. Administrators-group users fall through and pass via
	// the revoke-any HasPermissionAPI check below, since {admin, *} matches
	// any requested permission.
	if session.UserID == apiKeyAdminUserID {
		return nil
	}

	hasAny, err := h.auth.HasPermissionAPI(ctx, session.UserID, auth.ActionRevokeAny, auth.ResourcePurchases)
	if err != nil {
		return fmt.Errorf("permission check failed: %w", err)
	}
	if hasAny {
		return nil
	}

	hasOwn, err := h.auth.HasPermissionAPI(ctx, session.UserID, auth.ActionRevokeOwn, auth.ResourcePurchases)
	if err != nil {
		return fmt.Errorf("permission check failed: %w", err)
	}
	if !hasOwn {
		return NewClientError(403, "permission denied: requires revoke-any or revoke-own on purchases")
	}

	return h.checkRevokeOwnAccountAccess(ctx, session.UserID, record)
}

// checkRevokeOwnAccountAccess enforces the account-scope ownership constraint
// for revoke-own: the purchase must be in a cloud account the session user
// is allowed to access. Extracted from authorizeSessionRevoke to keep that
// function's cyclomatic complexity within the project limit.
func (h *Handler) checkRevokeOwnAccountAccess(ctx context.Context, userID string, record *config.PurchaseHistoryRecord) error {
	// Purchase history rows pre-date created_by_user_id; ownership is via
	// account access (same model as the per-account-perms middleware used
	// elsewhere in the history view).
	// Fail closed for revoke-own: if the purchase has no account association
	// we cannot verify ownership, so deny rather than allow an unscoped revoke.
	if record.CloudAccountID == nil || *record.CloudAccountID == "" {
		return NewClientError(403, "permission denied: cannot verify ownership for this purchase")
	}
	allowed, err := h.auth.GetAllowedAccountsAPI(ctx, userID)
	if err != nil {
		return fmt.Errorf("account access check failed: %w", err)
	}
	if len(allowed) > 0 && !stringInSlice(*record.CloudAccountID, allowed) {
		return NewClientError(403, "permission denied: purchase is in an account you do not have access to")
	}
	return nil
}

// revokeAzurePurchase handles Azure reservation returns via the Azure
// Reservations API (CalculateRefund + Return). The reservation order ID and
// reservation ID are parsed from the purchase_id ARM resource path stored at
// purchase time.
func (h *Handler) revokeAzurePurchase(ctx context.Context, record *config.PurchaseHistoryRecord) (any, error) {
	// Prefer the window stamped on the row at purchase time (single source of
	// truth, issue #290). Fall back to recomputing from Timestamp for legacy
	// rows written before the column was populated, so they remain revocable.
	windowClosesAt := record.Timestamp.AddDate(0, 0, AzureRevocationWindowDays)
	if record.RevocationWindowClosesAt != nil {
		windowClosesAt = *record.RevocationWindowClosesAt
	}
	if time.Now().UTC().After(windowClosesAt) {
		return nil, NewClientError(422, fmt.Sprintf(
			"Azure reservation return window closed at %s (%d days after purchase)",
			windowClosesAt.Format(time.RFC3339), AzureRevocationWindowDays,
		))
	}

	orderID, reservationID, err := parseAzureReservationIDs(record.PurchaseID)
	if err != nil {
		logging.Warnf("revoke azure: cannot parse reservation IDs from purchase_id %q: %v", record.PurchaseID, err)
		return nil, NewClientError(422, "cannot determine Azure reservation order ID from purchase record; contact Azure Support to request a refund")
	}

	cred, err := azidentity.NewDefaultAzureCredential(nil)
	if err != nil {
		return nil, fmt.Errorf("revoke azure: obtain credential: %w", err)
	}

	calcClient, err := armreservations.NewCalculateRefundClient(cred, nil)
	if err != nil {
		return nil, fmt.Errorf("revoke azure: create calculate-refund client: %w", err)
	}

	returnClient, err := armreservations.NewReturnClient(cred, nil)
	if err != nil {
		return nil, fmt.Errorf("revoke azure: create return client: %w", err)
	}

	return h.callAzureReturn(ctx, calcClient, returnClient, record, orderID, reservationID)
}

// callAzureReturn executes the two-step Azure reservation return:
// CalculateRefund (to get the session ID) followed by Return. Extracted from
// revokeAzurePurchase to allow test injection of the two clients.
func (h *Handler) callAzureReturn(
	ctx context.Context,
	calcClient azureCalculateRefundClient,
	returnClient azureReturnClient,
	record *config.PurchaseHistoryRecord,
	orderID, reservationID string,
) (any, error) {
	// Guard against an order-only ARM path (no /reservations/{id} segment),
	// which parseAzureReservationIDs returns with an empty reservationID.
	// Submitting a Return for an empty reservation would either fail opaquely
	// or, worse, be misinterpreted by the API; reject it up front so the
	// caller gets a clear, actionable error instead.
	if orderID == "" || reservationID == "" {
		return nil, NewClientError(422, "cannot determine Azure reservation ID from purchase record; contact Azure Support to request a refund")
	}

	// Step 1: CalculateRefund to obtain a sessionId required by the Return API.
	quantity := int32(record.Count) //nolint:gosec // Count > 0 validated at purchase
	calcResp, err := calcClient.Post(ctx, orderID, armreservations.CalculateRefundRequest{
		Properties: &armreservations.CalculateRefundRequestProperties{
			ReservationToReturn: &armreservations.ReservationToReturn{
				ReservationID: &reservationID,
				Quantity:      &quantity,
			},
			Scope: toPtr("Reservation"),
		},
	}, nil)
	if err != nil {
		if isAzureClientError(err) {
			return nil, NewClientError(400, fmt.Sprintf("Azure refund calculation rejected: %v", err))
		}
		return nil, fmt.Errorf("revoke azure: CalculateRefund failed: %w", err)
	}

	var sessionID string
	if calcResp.Properties != nil && calcResp.Properties.SessionID != nil {
		sessionID = *calcResp.Properties.SessionID
	}

	// Step 2: Return (post the actual refund request).
	_, err = returnClient.Post(ctx, orderID, armreservations.RefundRequest{
		Properties: &armreservations.RefundRequestProperties{
			ReservationToReturn: &armreservations.ReservationToReturn{
				ReservationID: &reservationID,
				Quantity:      &quantity,
			},
			SessionID:    &sessionID,
			ReturnReason: toPtr("Revoked via CUDly within free-cancel window"),
			Scope:        toPtr("Reservation"),
		},
	}, nil)
	if err != nil {
		if isAzureClientError(err) {
			return nil, NewClientError(400, fmt.Sprintf("Azure refund rejected: %v", err))
		}
		return nil, fmt.Errorf("revoke azure: Return failed: %w", err)
	}

	now := time.Now().UTC()
	if markErr := h.config.MarkPurchaseRevoked(ctx, record.PurchaseID, now, "direct-api", ""); markErr != nil {
		logging.Errorf("revoke azure: MarkPurchaseRevoked failed for %s after successful Azure return: %v", record.PurchaseID, markErr)
		return nil, fmt.Errorf("revoke azure: refund submitted but failed to persist revocation state: %w", markErr)
	}

	// PII policy: log execution and account IDs only, not user identifiers.
	logging.Infof("revoke azure: purchase_id=%s account_id=%s revoked_via=direct-api", record.PurchaseID, record.AccountID)

	return &revokePurchaseResult{
		Status:     "revoked",
		RevokedAt:  now.Format(time.RFC3339),
		RevokedVia: "direct-api",
	}, nil
}

// parseAzureReservationIDs extracts the reservation order ID and reservation ID
// from an Azure ARM resource path. The purchase_id is stored as the ARM
// resource ID at purchase time.
//
// Accepted formats (case-insensitive path segments):
//
//	/subscriptions/{sub}/providers/Microsoft.Capacity/reservationOrders/{orderID}/reservations/{resID}
//	/providers/Microsoft.Capacity/reservationOrders/{orderID}/reservations/{resID}
//	/providers/Microsoft.Capacity/reservationOrders/{orderID}
func parseAzureReservationIDs(purchaseID string) (orderID, reservationID string, err error) {
	lower := strings.ToLower(purchaseID)
	const orderKey = "reservationorders/"

	orderIdx := strings.Index(lower, orderKey)
	if orderIdx < 0 {
		return "", "", fmt.Errorf("no reservationOrders segment in %q", purchaseID)
	}
	afterOrder := purchaseID[orderIdx+len(orderKey):]

	resIdx := strings.Index(strings.ToLower(afterOrder), "/reservations/")
	if resIdx < 0 {
		// Order-only path.
		return strings.TrimRight(afterOrder, "/"), "", nil
	}
	orderID = afterOrder[:resIdx]
	reservationID = afterOrder[resIdx+len("/reservations/"):]
	if sl := strings.Index(reservationID, "/"); sl >= 0 {
		reservationID = reservationID[:sl]
	}
	return orderID, reservationID, nil
}

// isAzureClientError returns true when the error message contains indicators
// of a 4xx (client-side) Azure API rejection. Used to map Azure errors onto
// the correct HTTP status for the frontend.
func isAzureClientError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	for _, indicator := range []string{
		"400", "409", "422",
		"refundpolicyviolated", "refund not allowed", "returnpolicyviolated",
	} {
		if strings.Contains(msg, indicator) {
			return true
		}
	}
	return false
}

// toPtr returns a pointer to its argument. Generic helper used by the Azure
// revocation call-site to construct ARM struct fields without temp variables.
func toPtr[T any](v T) *T { return &v }
