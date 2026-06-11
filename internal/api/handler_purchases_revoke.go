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
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	armreservations "github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/reservations/armreservations"
	"github.com/LeanerCloud/CUDly/internal/auth"
	"github.com/LeanerCloud/CUDly/internal/config"
	"github.com/LeanerCloud/CUDly/pkg/logging"
	"github.com/aws/aws-lambda-go/events"
	"github.com/jackc/pgx/v5"
)

// revokeQuoteEpsilon is the tolerance (in currency units) for the
// TOCTOU-divergence check: if Azure's actual refund on Return diverges from
// the user-consented expected_refund_amount by more than this, the revoke is
// rejected with 422 so the user can re-quote and confirm.
const revokeQuoteEpsilon = 0.01

// azureRefundSafetyMargin is subtracted from the local window-close time before
// presenting the revoke button or accepting a revoke request. Azure's 7-day
// window has a hard edge: a reservation returned in the last few minutes of the
// window occasionally fails with RefundPolicyViolated due to clock skew between
// CUDly's clock and Azure's. Shrinking the local window by 1 hour eliminates
// the tail of clock-skew failures at the edge (issue #290 Finding #3).
//
// The safety margin applies only to the local pre-flight check -- the value
// stored in purchase_history.revocation_window_closes_at is the unmodified
// Azure deadline so operators can see the true expiry.
const azureRefundSafetyMargin = 1 * time.Hour

// revokeQuoteResult is the JSON body returned by
// GET /api/purchases/revoke/calculate/{id}.
type revokeQuoteResult struct {
	// RefundAmount is the amount Azure will refund (from CalculateRefund).
	RefundAmount float64 `json:"refund_amount"`
	// RefundCurrency is the ISO-4217 currency code (e.g. "USD").
	RefundCurrency string `json:"refund_currency"`
	// QuotedAt is an RFC3339 timestamp of when this quote was generated.
	QuotedAt string `json:"quoted_at"`
}

// revokeConfirmBody is the JSON body expected on
// POST /api/purchases/{purchaseId}/revoke.
// ExpectedRefundAmount is the amount the user consented to after seeing the
// quote, used for TOCTOU-divergence detection.
type revokeConfirmBody struct {
	// ExpectedRefundAmount is the refund amount the user confirmed.
	// Required when the purchase has an Azure revocation window.
	ExpectedRefundAmount *float64 `json:"expected_refund_amount"`
}

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

// revokeReconcilePendingResult is the JSON body returned with HTTP 207
// Multi-Status when the Azure refund succeeded but the subsequent DB write
// failed after all retries. The frontend reads the "code" field and shows a
// non-retryable toast ("Refund issued. We will reconcile your audit shortly.")
// with no retry button (issue #290 Finding #6).
type revokeReconcilePendingResult struct {
	Code          string `json:"code"`
	AzureReturned bool   `json:"azure_returned"`
	Message       string `json:"message"`
}

// revokeMarkRetryBackoffs are the sleep durations between consecutive
// MarkPurchaseRevoked attempts after the first failure (1s, 3s, 9s).
var revokeMarkRetryBackoffs = []time.Duration{
	1 * time.Second,
	3 * time.Second,
	9 * time.Second,
}

// revokePurchase is the single revocation entry point. It unifies two flows
// that both target a purchase by ID (issue #290 in-app revoke + issue #291
// email one-click revoke), dispatching on token presence and the row's state:
//
//   - GET (any path): render an HTML confirmation form so email prefetchers
//     cannot auto-trigger a mutation. The token travels in the query string for
//     the email one-click link, but nothing is mutated on GET — only the POST
//     from the rendered form (or the dashboard) actually revokes.
//
//   - The ID resolves to a purchase_execution:
//
//   - status=scheduled (Gmail-style pre-fire delay, #290): cancelled at
//     zero cost via the scheduled-execution CAS. Session-only — the
//     dashboard Revoke button drives this; there is no email one-click for
//     a not-yet-executed purchase.
//
//   - status=completed / partially_completed (#291): records a
//     "revocation_requested" status. token-OR-session dispatch, mirroring
//     approvePurchase / cancelPurchase — the email one-click link (token)
//     and the dashboard Revoke button (session) both land here.
//
//   - The ID resolves to a purchase_history row (a completed Azure reservation,
//     #290): real Azure reservation Return within the free-cancel window.
//     Session-only.
//
// Authorization is fail-closed: if the auth service is nil the request is
// rejected with 403 on every path (without it we can verify neither a session
// nor a token's contact-email binding). The session-only paths additionally
// require a valid session; the completed-execution path enforces its own
// token-or-session matrix inside revokeCompletedExecution.
func (h *Handler) revokePurchase(ctx context.Context, req *events.LambdaFunctionURLRequest, purchaseID, token string) (any, error) {
	if purchaseID == "" {
		return nil, NewClientError(400, "purchase_id is required")
	}

	// GET: render a confirmation page so email prefetchers cannot auto-trigger
	// the revocation. No mutation occurs on GET (issue #291: email prefetchers).
	if req.RequestContext.HTTP.Method == "GET" {
		return renderRevokeConfirmPage(purchaseID, token), nil
	}

	// Fail-closed: auth service nil means we can verify neither a session nor a
	// token's contact-email binding. Reject before any lookup so every path
	// (session-only and token) surfaces a uniform 403.
	if h.auth == nil {
		return nil, NewClientError(403, "authentication service not configured")
	}

	// Resolve the ID against purchase_executions first. A genuine DB error
	// surfaces as 500; (nil, nil) means the ID is not an execution (or is not
	// yet visible) and we fall through to the purchase_history lookup below.
	execution, execErr := h.config.GetExecutionByID(ctx, purchaseID)
	if execErr != nil {
		return nil, fmt.Errorf("revoke: GetExecutionByID %s: %w", purchaseID, execErr)
	}
	if execution != nil {
		return h.revokeExistingExecution(ctx, req, execution, token)
	}

	// No execution row. A token means this came from an email one-click link
	// whose target execution no longer exists (or never did): the email flow
	// only ever points at executions, never at purchase_history rows, so return
	// 404 rather than demanding a session for the unrelated Azure path.
	if token != "" {
		return nil, NewClientError(404, "execution not found")
	}

	// No token: a completed Azure reservation in purchase_history (the in-app
	// dashboard flow, #290). Session-only (real Azure Return within the
	// free-cancel window).
	session, err := h.requireSession(ctx, req)
	if err != nil {
		return nil, err
	}
	return h.loadAndRevokePurchaseHistory(ctx, req, session, purchaseID)
}

// revokeExistingExecution dispatches a revoke for an execution row that exists,
// branching on its status. Extracted from revokePurchase to keep that function
// under the cyclomatic limit; the auth/security matrix is unchanged (each branch
// enforces the same token-or-session rules it did inline).
func (h *Handler) revokeExistingExecution(ctx context.Context, req *events.LambdaFunctionURLRequest, execution *config.PurchaseExecution, token string) (any, error) {
	switch execution.Status {
	case "completed", "partially_completed":
		// Post-execution revoke (#291): token-OR-session. The email
		// one-click link carries a token; the dashboard Revoke button
		// carries a session. revokeCompletedExecution does its own auth.
		return h.revokeCompletedExecution(ctx, req, execution, token)
	case "pending", "notified":
		// Not yet scheduled or executed: there is nothing to revoke — the
		// caller wants Cancel, not Revoke. Return the friendly 409 (#291)
		// rather than letting the scheduled-CAS path produce a confusing
		// 410. No mutation, so no session is required to surface this.
		return nil, NewClientError(409, fmt.Sprintf(
			"execution %s is still pending — use the Cancel link instead of Revoke", execution.ExecutionID))
	default:
		// Scheduled (free pre-fire cancel, #290) and any other state:
		// session-only. revokeScheduledExecution's CAS (WHERE
		// status='scheduled') is the sole arbiter — it returns 410 when the
		// scheduler already fired, and a non-scheduled row simply fails the
		// CAS, so we do not pre-reject on status here (avoids a TOCTOU race).
		session, err := h.requireSession(ctx, req)
		if err != nil {
			return nil, err
		}
		return h.revokeScheduledExecution(ctx, session, execution)
	}
}

// loadAndRevokePurchaseHistory pulls the auth + idempotency-check + provider-dispatch
// logic for the completed-purchase path out of revokePurchase to keep that function
// under the cyclomatic limit.
func (h *Handler) loadAndRevokePurchaseHistory(ctx context.Context, req *events.LambdaFunctionURLRequest, session *Session, purchaseID string) (any, error) {
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

	// Partial-success reconciliation (issue #290 Finding #6): if the
	// revocation_in_flight flag is set but revoked_at is still NULL, Azure
	// already issued the refund but our DB write failed. Return 207 so the
	// frontend does not retry the Azure call (which would fail with "already
	// returned"). The finalize_revocations sweep will reconcile the row.
	if record.RevocationInFlight {
		return &revokeReconcilePendingResult{
			Code:          "RECONCILE_PENDING",
			AzureReturned: true,
			Message:       "Refund already issued. We will reconcile your audit record shortly. Do not retry.",
		}, nil
	}

	// Parse the optional expected_refund_amount from the request body.
	// Azure revocations require this for TOCTOU-divergence detection
	// (the two-step quote-then-confirm flow, issue #290 Finding #4).
	var body revokeConfirmBody
	if req.Body != "" {
		if jsonErr := json.Unmarshal([]byte(req.Body), &body); jsonErr != nil {
			return nil, NewClientError(400, fmt.Sprintf("invalid request body: %v", jsonErr))
		}
	}

	return h.dispatchProviderRevoke(ctx, record, body.ExpectedRefundAmount)
}

// revokeScheduledExecution cancels a Gmail-style pre-fire delayed execution
// that is still in the "scheduled" state (i.e. the cloud SDK has not been
// called yet). This is a free cancel: no provider SDK call is made.
//
// The method enforces revoke-any/revoke-own RBAC (same permissions as the
// completed-purchase revoke path), then atomically transitions the execution
// to "cancelled" and removes its purchase_suppressions.
//
// Returns 410 Gone only when the CAS observes the row already transitioned out
// of "scheduled" (the scheduler fired the SDK call between our SELECT and the
// CAS UPDATE). We do NOT pre-reject on a past ScheduledExecutionAt: a row still
// in "scheduled" is cancellable for free no matter how stale the timestamp,
// which keeps free-cancel working during scheduler lag/backpressure.
func (h *Handler) revokeScheduledExecution(ctx context.Context, session *Session, execution *config.PurchaseExecution) (any, error) {
	// No early window-expiry check on ScheduledExecutionAt: a row that is still
	// status=="scheduled" has NOT been transitioned by the scheduler, so the SDK
	// call has not fired regardless of how far the timestamp is in the past
	// (scheduler lag / backpressure). Returning 410 purely on a past timestamp
	// would break free-cancel during lag even though the CAS below can still
	// cancel it before any cloud call. Let CancelScheduledExecutionAtomic be the
	// sole arbiter: it returns cancelled=false (-> 410) only when the row has
	// actually moved out of "scheduled".
	if err := h.authorizeSessionRevokeExecution(ctx, session, execution); err != nil {
		return nil, err
	}

	// Atomically transition from scheduled -> cancelled and remove suppressions.
	var cancelledBy *string
	if session.Email != "" {
		e := session.Email
		cancelledBy = &e
	}
	var cancelled bool
	var currentStatus string
	if err := h.config.WithTx(ctx, func(tx pgx.Tx) error {
		var err error
		// The scheduled-revoke path uses its own CAS variant that flips ONLY
		// status='scheduled' -> 'cancelled'. CancelExecutionAtomic accepts
		// only ('pending','notified') and would always return zero rows on
		// a scheduled row, miscoded as "race lost" -> a misleading 410 even
		// during the happy path. Issue #290 wave-2: keep the two CAS contracts
		// distinct so 410 unambiguously means "scheduler already fired".
		cancelled, currentStatus, err = h.config.CancelScheduledExecutionAtomic(ctx, tx, execution.ExecutionID, cancelledBy)
		if err != nil {
			return err
		}
		if !cancelled {
			return nil
		}
		return h.config.DeleteSuppressionsByExecutionTx(ctx, tx, execution.ExecutionID)
	}); err != nil {
		return nil, fmt.Errorf("cancel scheduled execution %s: %w", execution.ExecutionID, err)
	}
	if !cancelled {
		// A concurrent scheduler tick transitioned the row away from "scheduled"
		// between our SELECT and the CAS UPDATE — the window closed. Return 410
		// so the client knows to switch to the completed-purchase revoke path.
		return nil, NewClientError(410, fmt.Sprintf(
			"revocation window has closed: execution %s was already transitioned to %q", execution.ExecutionID, currentStatus,
		))
	}

	logging.Infof("revokeScheduledExecution: execution_id=%s cancelled before SDK call (free cancel)", execution.ExecutionID)

	return map[string]string{
		"status":  "cancelled",
		"message": "Purchase cancelled. No cloud API call was made; no cost incurred.",
	}, nil
}

// authorizeSessionRevokeExecution enforces the revoke-any / revoke-own RBAC
// matrix for scheduled executions (pre-SDK-call state). Mirrors
// authorizeSessionRevoke for completed purchases but operates on a
// PurchaseExecution (which has CreatedByUserID) rather than a
// PurchaseHistoryRecord (which has CloudAccountID).
func (h *Handler) authorizeSessionRevokeExecution(ctx context.Context, session *Session, execution *config.PurchaseExecution) error {
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

	// revoke-own: the execution must have been created by this user.
	// NULL CreatedByUserID means a non-human or legacy creator — deny rather
	// than allow an unscoped revoke (fail-closed).
	if execution.CreatedByUserID == nil || *execution.CreatedByUserID != session.UserID {
		return NewClientError(403, "permission denied: cannot revoke another user's scheduled purchase")
	}
	return nil
}

// dispatchProviderRevoke routes a revocation request to the correct
// provider-specific implementation. Extracted from revokePurchase to keep
// that function's cyclomatic complexity within the project limit.
func (h *Handler) dispatchProviderRevoke(ctx context.Context, record *config.PurchaseHistoryRecord, expectedRefundAmount *float64) (any, error) {
	switch record.Provider {
	case "azure":
		return h.revokeAzurePurchase(ctx, record, expectedRefundAmount)
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
	// elsewhere in the history view). Whether revoke-own should be tightened
	// to creator scope instead is a product decision tracked in issue #950.
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

// calculateAzureRevoke handles GET /api/purchases/revoke/calculate/{id}.
// It runs CalculateRefund against Azure and returns the quoted refund amount
// and currency so the frontend can show the user a confirmation modal before
// the destructive POST /revoke call.
//
// This is the first step of the two-step quote-then-confirm revoke UX
// (issue #290 Finding #4). No state is mutated; the result is used by the
// frontend to populate revokeConfirmBody.ExpectedRefundAmount.
func (h *Handler) calculateAzureRevoke(ctx context.Context, req *events.LambdaFunctionURLRequest, purchaseID string) (any, error) {
	_, orderID, reservationID, count, err := h.validateAzureRevokeRequest(ctx, req, purchaseID)
	if err != nil {
		return nil, err
	}

	cred, err := azidentity.NewDefaultAzureCredential(nil)
	if err != nil {
		return nil, fmt.Errorf("revoke/calculate: obtain credential: %w", err)
	}
	calcClient, err := armreservations.NewCalculateRefundClient(cred, nil)
	if err != nil {
		return nil, fmt.Errorf("revoke/calculate: create calculate-refund client: %w", err)
	}

	quantity := int32(count) //nolint:gosec
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
		return nil, fmt.Errorf("revoke/calculate: CalculateRefund failed: %w", err)
	}

	refundAmount, refundCurrency := extractAzureRefundQuote(calcResp)
	return &revokeQuoteResult{
		RefundAmount:   refundAmount,
		RefundCurrency: refundCurrency,
		QuotedAt:       time.Now().UTC().Format(time.RFC3339),
	}, nil
}

// validateAzureRevokeRequest runs the shared preflight for the Azure
// CalculateRefund endpoint: input + auth + session, load + authorize the
// purchase, enforce provider==azure and the 1h-safety-margin window check, and
// parse the reservation order/ID from the ARM path. Extracted to keep
// calculateAzureRevoke under the cyclomatic-complexity limit. Returns the loaded
// record plus the parsed orderID, reservationID, and commitment count.
func (h *Handler) validateAzureRevokeRequest(ctx context.Context, req *events.LambdaFunctionURLRequest, purchaseID string) (*config.PurchaseHistoryRecord, string, string, int, error) {
	if purchaseID == "" {
		return nil, "", "", 0, NewClientError(400, "purchase_id is required")
	}
	if h.auth == nil {
		return nil, "", "", 0, NewClientError(403, "authentication service not configured")
	}

	session, err := h.requireSession(ctx, req)
	if err != nil {
		return nil, "", "", 0, err
	}

	record, err := h.config.GetPurchaseHistoryByPurchaseID(ctx, purchaseID)
	if err != nil {
		return nil, "", "", 0, fmt.Errorf("revoke/calculate: load purchase %s: %w", purchaseID, err)
	}
	if record == nil {
		return nil, "", "", 0, NewClientError(404, "purchase not found")
	}

	if err := h.authorizeSessionRevoke(ctx, session, record); err != nil {
		return nil, "", "", 0, err
	}

	orderID, reservationID, err := azureRevokeWindowAndIDs(record)
	if err != nil {
		return nil, "", "", 0, err
	}
	return record, orderID, reservationID, record.Count, nil
}

// azureRevokeWindowAndIDs enforces provider==azure and the 1h-safety-margin
// window check, then parses the reservation order/ID from the ARM path.
// Extracted from validateAzureRevokeRequest to keep both under the cyclomatic-
// complexity limit. Returns 422 ClientErrors for every reject case.
func azureRevokeWindowAndIDs(record *config.PurchaseHistoryRecord) (string, string, error) {
	if record.Provider != "azure" {
		return "", "", NewClientError(422, fmt.Sprintf("provider %q does not support refund calculation", record.Provider))
	}

	windowClosesAt := record.Timestamp.AddDate(0, 0, AzureRevocationWindowDays)
	if record.RevocationWindowClosesAt != nil {
		windowClosesAt = *record.RevocationWindowClosesAt
	}
	// Apply the 1h safety margin so we stop offering the button before Azure's
	// hard edge (clock-skew protection, issue #290 Finding #3).
	if time.Now().UTC().After(windowClosesAt.Add(-azureRefundSafetyMargin)) {
		return "", "", NewClientError(422, fmt.Sprintf(
			"Azure reservation return window closed at %s (%d days after purchase)",
			windowClosesAt.Format(time.RFC3339), AzureRevocationWindowDays,
		))
	}

	orderID, reservationID, err := parseAzureReservationIDs(record.PurchaseID)
	if err != nil {
		return "", "", NewClientError(422, "cannot determine Azure reservation order ID from purchase record; contact Azure Support to request a refund")
	}
	if orderID == "" || reservationID == "" {
		return "", "", NewClientError(422, "cannot determine Azure reservation ID from purchase record; contact Azure Support to request a refund")
	}
	return orderID, reservationID, nil
}

// extractAzureRefundQuote pulls the refund amount and currency out of a
// CalculateRefund response, guarding every nil pointer in the chain. Returns
// zero values when the response carries no billing-refund amount.
func extractAzureRefundQuote(resp armreservations.CalculateRefundClientPostResponse) (float64, string) {
	var refundAmount float64
	var refundCurrency string
	if resp.Properties != nil && resp.Properties.BillingRefundAmount != nil {
		if resp.Properties.BillingRefundAmount.Amount != nil {
			refundAmount = *resp.Properties.BillingRefundAmount.Amount
		}
		if resp.Properties.BillingRefundAmount.CurrencyCode != nil {
			refundCurrency = *resp.Properties.BillingRefundAmount.CurrencyCode
		}
	}
	return refundAmount, refundCurrency
}

// revokeAzurePurchase handles Azure reservation returns via the Azure
// Reservations API (CalculateRefund + Return). The reservation order ID and
// reservation ID are parsed from the purchase_id ARM resource path stored at
// purchase time.
func (h *Handler) revokeAzurePurchase(ctx context.Context, record *config.PurchaseHistoryRecord, expectedRefundAmount *float64) (any, error) {
	// Prefer the window stamped on the row at purchase time (single source of
	// truth, issue #290). Fall back to recomputing from Timestamp for legacy
	// rows written before the column was populated, so they remain revocable.
	windowClosesAt := record.Timestamp.AddDate(0, 0, AzureRevocationWindowDays)
	if record.RevocationWindowClosesAt != nil {
		windowClosesAt = *record.RevocationWindowClosesAt
	}
	// Apply the 1h safety margin so we stop accepting revoke requests before
	// Azure's hard edge (clock-skew protection, issue #290 Finding #3).
	if time.Now().UTC().After(windowClosesAt.Add(-azureRefundSafetyMargin)) {
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

	return h.callAzureReturn(ctx, calcClient, returnClient, record, orderID, reservationID, expectedRefundAmount)
}

// callAzureReturn executes the two-step Azure reservation return:
// CalculateRefund (to get the session ID and quoted amount) followed by Return.
// Extracted from revokeAzurePurchase to allow test injection of the two clients.
//
// expectedRefundAmount: the amount the user consented to after the
// quote step (GET /revoke/calculate). When provided and the CalculateRefund
// response diverges by more than revokeQuoteEpsilon, the call is rejected with
// 422 so the user can re-quote and confirm the new amount.
func (h *Handler) callAzureReturn(
	ctx context.Context,
	calcClient azureCalculateRefundClient,
	returnClient azureReturnClient,
	record *config.PurchaseHistoryRecord,
	orderID, reservationID string,
	expectedRefundAmount *float64,
) (any, error) {
	// Guard against an order-only ARM path (no /reservations/{id} segment),
	// which parseAzureReservationIDs returns with an empty reservationID.
	// Submitting a Return for an empty reservation would either fail opaquely
	// or, worse, be misinterpreted by the API; reject it up front so the
	// caller gets a clear, actionable error instead.
	if orderID == "" || reservationID == "" {
		return nil, NewClientError(422, "cannot determine Azure reservation ID from purchase record; contact Azure Support to request a refund")
	}

	// Step 1: CalculateRefund -> sessionID + quoted amount (TOCTOU check).
	quantity := int32(record.Count) //nolint:gosec // Count > 0 validated at purchase
	sessionID, calcRefundAmount, calcRefundCurrency, err := h.azureCalculateRefund(ctx, calcClient, orderID, reservationID, quantity)
	if err != nil {
		return nil, err
	}

	// TOCTOU-divergence check: if the caller supplied an expected refund amount
	// (from the prior GET /revoke/calculate), verify it matches the current
	// CalculateRefund response within epsilon. A mismatch means Azure's refund
	// quote changed between the user's confirmation and the actual call (e.g.
	// partial return already submitted, time-based fee tier changed).
	if expectedRefundAmount != nil && calcRefundAmount != nil {
		if math.Abs(*expectedRefundAmount-*calcRefundAmount) > revokeQuoteEpsilon {
			return nil, NewClientError(422, fmt.Sprintf(
				"refund amount diverged: you confirmed %.2f but Azure now quotes %.2f %s; re-confirm to proceed",
				*expectedRefundAmount, *calcRefundAmount, calcRefundCurrency,
			))
		}
	}

	// Partial-success guard (issue #290 Finding #6): flip the in-flight flag
	// BEFORE calling Azure Return so that if the subsequent MarkPurchaseRevoked
	// write fails, the row is visible to the finalize_revocations sweep rather
	// than silently stuck. Best-effort: if the flip itself fails, log and
	// continue — the in-flight flag is a safety net, not a hard precondition.
	if flipErr := h.config.FlipPurchaseRevocationInFlight(ctx, record.PurchaseID); flipErr != nil {
		logging.Warnf("revoke azure: FlipPurchaseRevocationInFlight for %s failed (continuing): %v", record.PurchaseID, flipErr)
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
		return nil, h.handleAzureReturnError(ctx, record, err)
	}

	return h.persistAzureRevocation(ctx, record, calcRefundAmount, calcRefundCurrency)
}

// azureCalculateRefund runs the CalculateRefund step and parses out the session
// ID (required by Return) and the quoted refund amount/currency (for the TOCTOU
// check). Errors are classified into 400 (client) vs 500 (transient).
func (h *Handler) azureCalculateRefund(ctx context.Context, calcClient azureCalculateRefundClient, orderID, reservationID string, quantity int32) (string, *float64, string, error) {
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
			return "", nil, "", NewClientError(400, fmt.Sprintf("Azure refund calculation rejected: %v", err))
		}
		return "", nil, "", fmt.Errorf("revoke azure: CalculateRefund failed: %w", err)
	}

	var sessionID string
	var calcRefundAmount *float64
	var calcRefundCurrency string
	if calcResp.Properties != nil {
		if calcResp.Properties.SessionID != nil {
			sessionID = *calcResp.Properties.SessionID
		}
		if calcResp.Properties.BillingRefundAmount != nil {
			if calcResp.Properties.BillingRefundAmount.Amount != nil {
				v := *calcResp.Properties.BillingRefundAmount.Amount
				calcRefundAmount = &v
			}
			if calcResp.Properties.BillingRefundAmount.CurrencyCode != nil {
				calcRefundCurrency = *calcResp.Properties.BillingRefundAmount.CurrencyCode
			}
		}
	}
	return sessionID, calcRefundAmount, calcRefundCurrency, nil
}

// handleAzureReturnError clears the in-flight flag (no refund was issued) and
// maps the Return error to the right status: 422 on the 7-day window edge, 400
// on other client errors, 500 otherwise.
func (h *Handler) handleAzureReturnError(ctx context.Context, record *config.PurchaseHistoryRecord, err error) error {
	// Azure Return failed. Clear the in-flight flag so the row is not left in
	// a permanently sticky state that would mislead the finalize_revocations
	// sweep into thinking Azure already issued a refund (Finding D, second-wave
	// CR). Best-effort: log and continue even if the clear fails.
	if clearErr := h.config.ClearRevocationInFlight(ctx, record.PurchaseID); clearErr != nil {
		logging.Warnf("revoke azure: ClearRevocationInFlight for %s failed after Return error (continuing): %v", record.PurchaseID, clearErr)
	}
	// Window-edge: if Azure rejects the Return with RefundPolicyViolated it
	// means our safety-margin check passed but Azure's clock disagreed (the
	// reservation crossed the 7-day boundary between our check and the API
	// call). Surface a clean 422 so the frontend can show a user-friendly
	// "window just closed" message (issue #290 Finding #3).
	if isAzureWindowEdgeError(err) {
		return NewClientError(422, "Azure reservation return window has closed; the 7-day refund period has expired")
	}
	if isAzureClientError(err) {
		return NewClientError(400, fmt.Sprintf("Azure refund rejected: %v", err))
	}
	return fmt.Errorf("revoke azure: Return failed: %w", err)
}

// persistAzureRevocation records the successful revocation with exponential-
// backoff retries. If every attempt fails, Azure has already refunded but the
// DB write could not land, so it returns a 207 RECONCILE_PENDING result (no
// retry) for the finalize_revocations sweep to reconcile, rather than a 500.
func (h *Handler) persistAzureRevocation(ctx context.Context, record *config.PurchaseHistoryRecord, calcRefundAmount *float64, calcRefundCurrency string) (any, error) {
	now := time.Now().UTC()
	markErr := h.config.MarkPurchaseRevoked(ctx, record.PurchaseID, now, "direct-api", "", calcRefundAmount, calcRefundCurrency)
	for attempt, backoff := range revokeMarkRetryBackoffs {
		if markErr == nil {
			break
		}
		logging.Warnf("revoke azure: MarkPurchaseRevoked attempt %d failed for %s: %v (retrying in %s)",
			attempt+1, record.PurchaseID, markErr, backoff)
		time.Sleep(backoff)
		markErr = h.config.MarkPurchaseRevoked(ctx, record.PurchaseID, now, "direct-api", "", calcRefundAmount, calcRefundCurrency)
	}
	if markErr != nil {
		// All retries failed. Azure has already refunded but we cannot persist
		// the revocation state. Return 207 Multi-Status so the frontend knows
		// the refund issued but not to retry — the finalize_revocations sweep
		// will reconcile the DB write on its next tick.
		logging.Errorf("revoke azure: MarkPurchaseRevoked failed for %s after %d attempts (Azure already returned): %v",
			record.PurchaseID, len(revokeMarkRetryBackoffs)+1, markErr)
		return &revokeReconcilePendingResult{
			Code:          "RECONCILE_PENDING",
			AzureReturned: true,
			Message:       "Refund issued. We will reconcile your audit record shortly. Do not retry.",
		}, nil
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

// isAzureClientError reports whether err represents a 4xx (client-side) Azure
// API rejection that the frontend should see as a user-actionable error rather
// than an internal server error.
//
// The check uses typed error inspection (errors.As to *azcore.ResponseError)
// rather than substring matching on err.Error(). The substring approach had two
// failure modes:
//  1. False positives: a network timeout whose message happens to contain "400"
//     or "404" would be misclassified as a client error, hiding transient infra
//     problems from the operator.
//  2. False negatives: Azure may return refund-policy errors with HTTP status
//     codes we did not enumerate as string literals (e.g. 403, 405).
//
// The typed approach classifies exactly the HTTP status codes Azure uses for
// policy violations and bad requests; all other errors (transport errors,
// 5xx, unknown error types) correctly classify as server-side.
func isAzureClientError(err error) bool {
	if err == nil {
		return false
	}
	var respErr *azcore.ResponseError
	if errors.As(err, &respErr) {
		switch respErr.StatusCode {
		case 400, 403, 404, 405, 409, 422:
			return true
		}
	}
	return false
}

// isAzureWindowEdgeError reports whether err is an Azure RefundPolicyViolated
// rejection from the Return API. This specific error code is returned when the
// reservation's 7-day return window has closed (either because the request
// arrived just after expiry due to clock skew, or because a partial return
// was already submitted). It is distinct from general client errors because
// the appropriate HTTP response is 422 with code AZURE_WINDOW_EDGE rather
// than the generic 400 "Azure refund rejected" path.
func isAzureWindowEdgeError(err error) bool {
	if err == nil {
		return false
	}
	var respErr *azcore.ResponseError
	if errors.As(err, &respErr) {
		return respErr.ErrorCode == "RefundPolicyViolated"
	}
	return false
}

// toPtr returns a pointer to its argument. Generic helper used by the Azure
// revocation call-site to construct ARM struct fields without temp variables.
func toPtr[T any](v T) *T { return &v }
