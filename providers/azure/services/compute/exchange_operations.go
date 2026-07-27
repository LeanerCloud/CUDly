// This file implements the "compatible offerings" and "execute exchange"
// steps of Azure Convertible RI exchange parity with AWS EC2 (refs #473,
// closes #596).
//
// Flow:
//  1. CalculateExchange -- calls armreservations.CalculateExchangeClient.BeginPost
//     with the source reservations and caller-supplied target slots. Azure prices
//     the proposed combination and returns a session ID, the candidate offerings
//     it is willing to accept, and any policy errors -- without committing anything.
//  2. ExecuteExchange -- calls armreservations.ExchangeClient.BeginPost with the
//     session ID from a CalculateExchange call, committing the swap.
//
// Both SDK operations are async LROs; PollUntilDone blocks until Azure completes
// or ctx is canceled. Context cancellation is treated as terminal and propagated
// immediately rather than folded into a generic error (feedback_ctx_cancel_terminal).
//
// Money-path note: this file only prices and executes exactly what it is told.
// The caller (internal/api handler) is responsible for never executing a
// session ID it did not just obtain from a CalculateExchange call made against
// the caller's own guardrail-checked inputs -- see the handler's doc comment
// for the full server-re-quote design.
package compute

import (
	"context"
	"errors"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/reservations/armreservations"
)

// CompatibleOffering describes one candidate target SKU that Azure priced as
// an exchange destination for the source reservations.
type CompatibleOffering struct {
	// SKU is the VM size (e.g. "Standard_D4s_v3").
	SKU string `json:"sku"`

	// Location is the Azure region (e.g. "eastus").
	Location string `json:"location"`

	// Term is the reservation term Azure priced this offering at, in ISO
	// 8601 duration format, stringified from
	// armreservations.PossibleReservationTermValues() ("P1Y", "P3Y" or
	// "P5Y"). Consumers must not treat a term outside the one/three-year
	// pair as unsupported.
	Term string `json:"term"`

	// Quantity is the number of instances that would be purchased.
	Quantity int32 `json:"quantity"`

	// BillingCurrencyTotal is the net amount the customer would pay in their
	// billing currency for this offering. Nil when Azure did not report an
	// amount (never coerced to 0 -- absent is not the same as free).
	BillingCurrencyTotal *float64 `json:"billing_currency_total"`

	// CurrencyCode is the ISO 4217 code for BillingCurrencyTotal (e.g. "USD").
	CurrencyCode string `json:"currency_code,omitempty"`
}

// ExchangePreview holds the priced result of a CalculateExchange call: what
// the proposed exchange would cost if executed with this exact SessionID.
type ExchangePreview struct {
	// SessionID must be passed verbatim to ExecuteExchange to commit this
	// exact priced combination. It is single-use and has a short server-side
	// TTL (typically 10 minutes).
	SessionID string `json:"session_id"`

	// NetPayable is the net amount the customer would pay, in the billing
	// currency. Positive: additional charge; negative: refund. Nil when
	// Azure did not report an amount -- callers must refuse to execute
	// rather than treat a nil NetPayable as "free" (feedback_nullable_not_zero).
	NetPayable *float64 `json:"net_payable"`

	// NetPayableCurrency is the ISO 4217 code for NetPayable.
	NetPayableCurrency string `json:"net_payable_currency,omitempty"`

	// RefundsTotal is the total refund value for the returned reservations.
	RefundsTotal *float64 `json:"refunds_total"`

	// PurchasesTotal is the total cost of the acquired reservations.
	PurchasesTotal *float64 `json:"purchases_total"`

	// PolicyErrors is non-empty when Azure's exchange policy blocks this
	// combination (e.g. cross-billing-account, expired RIs). Each entry is
	// a human-readable policy violation message, rendered by
	// policyErrorMessage so that one entry here always corresponds to
	// exactly one violation Azure reported. Callers must refuse to
	// execute when this is non-empty.
	PolicyErrors []string `json:"policy_errors,omitempty"`
}

// ExchangeResult holds the outcome of a completed exchange.
type ExchangeResult struct {
	// SessionID echoes the session identifier used for the exchange.
	SessionID string `json:"session_id"`

	// NetPayable mirrors the final net payment amount. Nil when Azure did
	// not report one.
	NetPayable *float64 `json:"net_payable"`

	// NetPayableCurrency is the ISO 4217 code for NetPayable.
	NetPayableCurrency string `json:"net_payable_currency,omitempty"`

	// Status is the typed ExchangeOperationResultStatus Azure returned
	// (e.g. "Succeeded", "PendingPurchases"), stringified.
	Status string `json:"status,omitempty"`
}

// ExchangeTarget describes one reservation to acquire in an exchange.
type ExchangeTarget struct {
	// SKU is the VM size to purchase (e.g. "Standard_D4s_v3"). Required.
	SKU string

	// Location is the Azure region (e.g. "eastus"). Required.
	Location string

	// Term is the reservation term. Required: must be one of
	// armreservations.PossibleReservationTermValues(). There is no default
	// -- an unset or unrecognized term is a validation error rather than a
	// silent P1Y fallback. Which of those terms Azure actually sells for a
	// given resource type is Azure's call, surfaced as a policy error from
	// CalculateExchange rather than second-guessed here.
	Term armreservations.ReservationTerm

	// Quantity is the number of instances to reserve. Required: must be >= 1.
	Quantity int32

	// BillingScopeID is the subscription or billing account that will be
	// charged. Required by the Azure exchange API.
	BillingScopeID string

	// AppliedScopeType controls whether the discount applies to a single
	// subscription or all subscriptions ("Shared"). Optional: Azure's
	// documented default of Shared is used when nil.
	AppliedScopeType *armreservations.AppliedScopeType
}

// CalculateExchangeCallerFunc is the narrow LRO-invoker interface that
// CalculateExchange needs from the SDK client. Satisfied by wrapping
// (*armreservations.CalculateExchangeClient).BeginPost + PollUntilDone; a
// stub can be injected for tests via SetCalculateExchangeCaller.
type CalculateExchangeCallerFunc func(
	ctx context.Context,
	body armreservations.CalculateExchangeRequest,
) (armreservations.CalculateExchangeOperationResultResponse, error)

// DoExchangeCallerFunc is the narrow LRO-invoker interface for
// ExecuteExchange. Satisfied by wrapping
// (*armreservations.ExchangeClient).BeginPost + PollUntilDone; a stub can be
// injected via SetDoExchangeCaller.
type DoExchangeCallerFunc func(
	ctx context.Context,
	sessionID string,
) (armreservations.ExchangeOperationResultResponse, error)

// SetCalculateExchangeCaller injects a test-only override for the
// CalculateExchange LRO. Tests use this to avoid real Azure API calls and to
// make the LRO synchronous (no time.Sleep / real polling needed).
func (c *ComputeClient) SetCalculateExchangeCaller(fn CalculateExchangeCallerFunc) {
	c.calculateExchangeCaller = fn
}

// SetDoExchangeCaller injects a test-only override for the Exchange LRO.
func (c *ComputeClient) SetDoExchangeCaller(fn DoExchangeCallerFunc) {
	c.doExchangeCaller = fn
}

// buildCalculateExchangeCaller returns the injected test stub when set, or
// constructs a real armreservations.CalculateExchangeClient wrapper.
func (c *ComputeClient) buildCalculateExchangeCaller() (CalculateExchangeCallerFunc, error) {
	if c.calculateExchangeCaller != nil {
		return c.calculateExchangeCaller, nil
	}
	client, err := armreservations.NewCalculateExchangeClient(c.cred, nil)
	if err != nil {
		return nil, fmt.Errorf("azure: create CalculateExchange client: %w", err)
	}
	return func(ctx context.Context, body armreservations.CalculateExchangeRequest) (armreservations.CalculateExchangeOperationResultResponse, error) {
		poller, err := client.BeginPost(ctx, body, nil)
		if err != nil {
			return armreservations.CalculateExchangeOperationResultResponse{}, fmt.Errorf("azure: CalculateExchange begin: %w", err)
		}
		resp, err := poller.PollUntilDone(ctx, nil)
		if err != nil {
			return armreservations.CalculateExchangeOperationResultResponse{}, fmt.Errorf("azure: CalculateExchange poll: %w", err)
		}
		return resp.CalculateExchangeOperationResultResponse, nil
	}, nil
}

// buildDoExchangeCaller returns the injected test stub when set, or
// constructs a real armreservations.ExchangeClient wrapper.
func (c *ComputeClient) buildDoExchangeCaller() (DoExchangeCallerFunc, error) {
	if c.doExchangeCaller != nil {
		return c.doExchangeCaller, nil
	}
	client, err := armreservations.NewExchangeClient(c.cred, nil)
	if err != nil {
		return nil, fmt.Errorf("azure: create Exchange client: %w", err)
	}
	return func(ctx context.Context, sessionID string) (armreservations.ExchangeOperationResultResponse, error) {
		poller, err := client.BeginPost(ctx, armreservations.ExchangeRequest{
			Properties: &armreservations.ExchangeRequestProperties{
				SessionID: to.Ptr(sessionID),
			},
		}, nil)
		if err != nil {
			return armreservations.ExchangeOperationResultResponse{}, fmt.Errorf("azure: Exchange begin: %w", err)
		}
		resp, err := poller.PollUntilDone(ctx, nil)
		if err != nil {
			return armreservations.ExchangeOperationResultResponse{}, fmt.Errorf("azure: Exchange poll: %w", err)
		}
		return resp.ExchangeOperationResultResponse, nil
	}, nil
}

// CalculateExchange prices a proposed exchange of sources for targets without
// committing it. Returns the priced preview (including the SessionID needed
// to execute) and the per-target compatible-offering breakdown.
//
// Every source must have a non-empty ReservationID and Quantity >= 1; every
// target must have a non-empty SKU/Location/BillingScopeID, Quantity >= 1,
// and a Term from PossibleReservationTermValues(). There is no coercion of
// invalid values (no clamping quantity to 1, no defaulting an unrecognized
// term) -- a caller mistake here is a validation error, not a silently
// different exchange than the one requested.
//
// Returns an error only when validation or the API call itself fails; a
// priced-but-policy-rejected combination is a successful call whose
// ExchangePreview.PolicyErrors is non-empty -- callers must check that
// before treating the preview as executable.
func (c *ComputeClient) CalculateExchange(
	ctx context.Context,
	sources []ExchangeableReservation,
	targets []ExchangeTarget,
) (*ExchangePreview, []CompatibleOffering, error) {
	if err := validateExchangeSources(sources); err != nil {
		return nil, nil, err
	}
	if err := validateExchangeTargets(targets); err != nil {
		return nil, nil, err
	}

	caller, err := c.buildCalculateExchangeCaller()
	if err != nil {
		return nil, nil, err
	}

	result, err := caller(ctx, buildCalculateExchangeRequest(sources, targets))
	if err != nil {
		if isTerminalCtxErr(err) {
			return nil, nil, err
		}
		return nil, nil, fmt.Errorf("azure: CalculateExchange: %w", err)
	}

	props, err := checkCalculateExchangeResult(result)
	if err != nil {
		return nil, nil, err
	}
	return extractExchangePreview(props), extractCompatibleOfferings(props), nil
}

// isTerminalCtxErr reports whether err is a context cancellation or deadline
// expiry, which callers must propagate as-is rather than fold into a
// generic wrapped error (feedback_ctx_cancel_terminal).
func isTerminalCtxErr(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

// checkCalculateExchangeResult validates that the raw LRO result represents
// a genuinely priced exchange -- no operation-level failure, a terminal
// status of Succeeded, and a non-empty SessionID actually present -- before
// the caller extracts a preview from it. A nil-Properties or
// empty-SessionID response is an explicit error rather than a fabricated
// empty preview.
//
// The Status check is not redundant with the Error check. Azure's contract
// documents Error as "required if status == failed or status == canceled",
// but a response that violates that contract (Failed/Cancelled with a nil
// Error) would otherwise yield a preview the execute handler immediately
// commits. Status is only asserted when Azure populated it: an absent
// status leaves the SessionID check as the guard, rather than inventing a
// failure Azure never reported.
func checkCalculateExchangeResult(result armreservations.CalculateExchangeOperationResultResponse) (armreservations.CalculateExchangeResponseProperties, error) {
	if result.Error != nil {
		return armreservations.CalculateExchangeResponseProperties{}, fmt.Errorf("azure: CalculateExchange operation failed: %s", operationErrorMessage(result.Error))
	}
	if result.Status != nil && *result.Status != armreservations.CalculateExchangeOperationResultStatusSucceeded {
		return armreservations.CalculateExchangeResponseProperties{}, fmt.Errorf("azure: CalculateExchange did not succeed: terminal status %q", string(*result.Status))
	}
	if result.Properties == nil || result.Properties.SessionID == nil || *result.Properties.SessionID == "" {
		return armreservations.CalculateExchangeResponseProperties{}, fmt.Errorf("azure: CalculateExchange returned no session id")
	}
	return *result.Properties, nil
}

// ExecuteExchange commits a previously-calculated Azure RI exchange using the
// session ID returned by CalculateExchange.
//
// sessionID must be non-empty. Azure's CalculateExchange session ID is the
// idempotency mechanism for this call: replaying the same session ID after
// the exchange completes has no further effect server-side.
func (c *ComputeClient) ExecuteExchange(ctx context.Context, sessionID string) (*ExchangeResult, error) {
	if sessionID == "" {
		return nil, fmt.Errorf("azure: ExecuteExchange: session_id is required (obtain from CalculateExchange)")
	}

	caller, err := c.buildDoExchangeCaller()
	if err != nil {
		return nil, err
	}

	result, err := caller(ctx, sessionID)
	if err != nil {
		if isTerminalCtxErr(err) {
			return nil, err
		}
		return nil, fmt.Errorf("azure: ExecuteExchange: %w", err)
	}
	if result.Error != nil {
		return nil, fmt.Errorf("azure: ExecuteExchange operation failed: %s", operationErrorMessage(result.Error))
	}
	if result.Status != nil && !exchangeStatusAccepted(*result.Status) {
		return nil, fmt.Errorf(
			"azure: ExecuteExchange returned terminal status %q with no error detail; verify the reservation state in the Azure portal before retrying",
			string(*result.Status))
	}
	if result.Properties == nil {
		return nil, fmt.Errorf("azure: ExecuteExchange returned no properties")
	}

	netPayable, netPayableCurrency := extractPrice(result.Properties.NetPayable)
	res := &ExchangeResult{SessionID: sessionID, NetPayable: netPayable, NetPayableCurrency: netPayableCurrency}
	if result.Status != nil {
		res.Status = string(*result.Status)
	}
	return res, nil
}

// --- internal helpers ---

// exchangeStatusAccepted reports whether an ExchangeOperationResultStatus
// means Azure accepted and is carrying out the exchange. Succeeded is fully
// settled; PendingRefunds/PendingPurchases mean the swap was committed and
// Azure is still settling one leg, which the caller surfaces as-is.
//
// Everything else -- Failed, Cancelled, and any status a future API version
// adds that this SDK does not know -- is refused rather than reported as a
// successful exchange. An unrecognized status after a commit attempt is
// genuinely ambiguous, so the error tells the operator to check the portal
// instead of blindly retrying into a possible double exchange.
func exchangeStatusAccepted(s armreservations.ExchangeOperationResultStatus) bool {
	switch s {
	case armreservations.ExchangeOperationResultStatusSucceeded,
		armreservations.ExchangeOperationResultStatusPendingPurchases,
		armreservations.ExchangeOperationResultStatusPendingRefunds:
		return true
	default:
		return false
	}
}

// operationErrorMessage extracts a human-readable message from an Azure LRO
// error result, falling back to a generic label when Azure omits the message
// field (still an explicit error, never silently swallowed).
func operationErrorMessage(opErr *armreservations.OperationResultError) string {
	if opErr != nil && opErr.Message != nil {
		return *opErr.Message
	}
	return "no error message returned"
}

// validateExchangeSources fails loud on any source that would otherwise be
// silently coerced into something Azure did not actually ask to exchange.
func validateExchangeSources(sources []ExchangeableReservation) error {
	if len(sources) == 0 {
		return fmt.Errorf("azure: CalculateExchange: at least one source reservation is required")
	}
	for i := range sources {
		s := &sources[i]
		if s.ReservationID == "" {
			return fmt.Errorf("azure: CalculateExchange: sources[%d].reservation_id is required", i)
		}
		if s.Quantity < 1 {
			return fmt.Errorf("azure: CalculateExchange: sources[%d].quantity must be >= 1, got %d", i, s.Quantity)
		}
	}
	return nil
}

// validateExchangeTargets fails loud on any target field that would
// otherwise be silently coerced or defaulted to a different commitment
// than what the caller asked for.
func validateExchangeTargets(targets []ExchangeTarget) error {
	if len(targets) == 0 {
		return fmt.Errorf("azure: CalculateExchange: at least one target is required")
	}
	for i, t := range targets {
		if t.SKU == "" {
			return fmt.Errorf("azure: CalculateExchange: targets[%d].sku is required", i)
		}
		if t.Location == "" {
			return fmt.Errorf("azure: CalculateExchange: targets[%d].location is required", i)
		}
		if t.BillingScopeID == "" {
			return fmt.Errorf("azure: CalculateExchange: targets[%d].billing_scope_id is required", i)
		}
		if t.Quantity < 1 {
			return fmt.Errorf("azure: CalculateExchange: targets[%d].quantity must be >= 1, got %d", i, t.Quantity)
		}
		if !isValidReservationTerm(t.Term) {
			return fmt.Errorf("azure: CalculateExchange: targets[%d].term %q is not a supported reservation term", i, t.Term)
		}
	}
	return nil
}

// isValidReservationTerm reports whether term is one of the SDK's typed
// enum values, rather than accepting any string the caller happens to pass.
func isValidReservationTerm(term armreservations.ReservationTerm) bool {
	for _, t := range armreservations.PossibleReservationTermValues() {
		if t == term {
			return true
		}
	}
	return false
}

// buildCalculateExchangeRequest converts validated sources/targets into the
// SDK request shape, using typed SDK enum constants throughout
// (feedback_sdk_enum_string_literals) rather than raw strings.
func buildCalculateExchangeRequest(sources []ExchangeableReservation, targets []ExchangeTarget) armreservations.CalculateExchangeRequest {
	toReturn := make([]*armreservations.ReservationToReturn, 0, len(sources))
	for i := range sources {
		src := sources[i]
		toReturn = append(toReturn, &armreservations.ReservationToReturn{
			Quantity:      to.Ptr(src.Quantity),
			ReservationID: to.Ptr(src.ReservationID),
		})
	}

	toPurchase := make([]*armreservations.PurchaseRequest, 0, len(targets))
	for i := range targets {
		tgt := targets[i]
		scopeType := armreservations.AppliedScopeTypeShared
		if tgt.AppliedScopeType != nil {
			scopeType = *tgt.AppliedScopeType
		}
		toPurchase = append(toPurchase, &armreservations.PurchaseRequest{
			Location: to.Ptr(tgt.Location),
			SKU:      &armreservations.SKUName{Name: to.Ptr(tgt.SKU)},
			Properties: &armreservations.PurchaseRequestProperties{
				AppliedScopeType:     to.Ptr(scopeType),
				BillingPlan:          to.Ptr(armreservations.ReservationBillingPlanUpfront),
				BillingScopeID:       to.Ptr(tgt.BillingScopeID),
				Quantity:             to.Ptr(tgt.Quantity),
				Renew:                to.Ptr(false),
				ReservedResourceType: to.Ptr(armreservations.ReservedResourceTypeVirtualMachines),
				Term:                 to.Ptr(tgt.Term),
				ReservedResourceProperties: &armreservations.PurchaseRequestPropertiesReservedResourceProperties{
					InstanceFlexibility: to.Ptr(armreservations.InstanceFlexibilityOn),
				},
			},
		})
	}

	return armreservations.CalculateExchangeRequest{
		Properties: &armreservations.CalculateExchangeRequestProperties{
			ReservationsToExchange: toReturn,
			ReservationsToPurchase: toPurchase,
		},
	}
}

// extractPrice reads the optional Amount/CurrencyCode pointer fields from an
// armreservations.Price, returning a nil amount (never a fabricated 0) when
// Azure did not report one.
func extractPrice(p *armreservations.Price) (amount *float64, currency string) {
	if p == nil {
		return nil, ""
	}
	if p.Amount != nil {
		v := *p.Amount
		amount = &v
	}
	if p.CurrencyCode != nil {
		currency = *p.CurrencyCode
	}
	return amount, currency
}

func extractExchangePreview(props armreservations.CalculateExchangeResponseProperties) *ExchangePreview {
	preview := &ExchangePreview{SessionID: *props.SessionID}
	preview.NetPayable, preview.NetPayableCurrency = extractPrice(props.NetPayable)
	preview.RefundsTotal, _ = extractPrice(props.RefundsTotal)
	preview.PurchasesTotal, _ = extractPrice(props.PurchasesTotal)
	if props.PolicyResult != nil {
		for _, e := range props.PolicyResult.PolicyErrors {
			preview.PolicyErrors = append(preview.PolicyErrors, policyErrorMessage(e))
		}
	}
	return preview
}

// policyErrorMessage renders one Azure exchange policy violation as a
// non-empty string.
//
// Both fields of armreservations.ExchangePolicyError are optional pointers,
// so an entry may carry only a Code, or (in a contract violation) neither.
// Every entry must still produce a message: callers gate execution on
// len(ExchangePreview.PolicyErrors) > 0, so dropping a Message-less entry
// would empty the slice and let a policy-rejected exchange be committed.
func policyErrorMessage(e *armreservations.ExchangePolicyError) string {
	if e == nil {
		return "azure reported an unspecified exchange policy violation"
	}
	switch {
	case e.Message != nil && *e.Message != "" && e.Code != nil && *e.Code != "":
		return fmt.Sprintf("%s: %s", *e.Code, *e.Message)
	case e.Message != nil && *e.Message != "":
		return *e.Message
	case e.Code != nil && *e.Code != "":
		return *e.Code
	default:
		return "azure reported an unspecified exchange policy violation"
	}
}

func extractCompatibleOfferings(props armreservations.CalculateExchangeResponseProperties) []CompatibleOffering {
	out := make([]CompatibleOffering, 0, len(props.ReservationsToPurchase))
	for _, item := range props.ReservationsToPurchase {
		if item == nil {
			continue
		}
		o := CompatibleOffering{}
		if item.Properties != nil {
			pp := item.Properties
			if pp.Location != nil {
				o.Location = *pp.Location
			}
			if pp.SKU != nil && pp.SKU.Name != nil {
				o.SKU = *pp.SKU.Name
			}
			if pp.Properties != nil {
				if pp.Properties.Quantity != nil {
					o.Quantity = *pp.Properties.Quantity
				}
				if pp.Properties.Term != nil {
					o.Term = string(*pp.Properties.Term)
				}
			}
		}
		o.BillingCurrencyTotal, o.CurrencyCode = extractPrice(item.BillingCurrencyTotal)
		out = append(out, o)
	}
	return out
}
