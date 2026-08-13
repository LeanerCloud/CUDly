package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"
)

// riExchangeIdempotencyWindow is how long a committed RI exchange submit
// "absorbs" an identical resubmission (issue #1642).
//
// It is deliberately much longer than the purchase path's two-minute
// purchaseIdempotencyWindow. An exchange's provider call is a long-running
// operation polled to completion INSIDE the request: Azure's ExecuteExchange
// blocks on PollUntilDone, and the AWS path quotes, re-quotes and accepts in
// sequence. The double-spend this guards against is a client that gives up on
// that still-running request and retries, so the window has to outlast a full
// request lifetime plus the client's own timeout and the operator's retry, not
// merely a double-click.
//
// The cost of the window being generous is bounded and visible: a caller who
// genuinely wants to repeat the very same exchange sooner gets an explanatory
// 409 telling them to wait. The cost of it being too short is an irreversible
// second commitment, so it is sized towards the former.
const riExchangeIdempotencyWindow = 15 * time.Minute

// Provider scope literals for the idempotency fingerprint. They keep an AWS
// exchange and an Azure exchange from ever colliding on one key, whatever the
// rest of the fingerprint happens to hash to.
const (
	azureExchangeIdempotencyScope = "azure-ri-exchange"
	awsExchangeIdempotencyScope   = "aws-ri-exchange"
)

// writeLengthPrefixed feeds variable-length components into a hash as
// "<byte length>:<bytes>", so no value can be crafted to shift a field
// boundary and make two genuinely different requests fingerprint alike.
// (A plain separator-joined encoding would let a reservation ID carrying the
// separator do exactly that, silently suppressing a distinct exchange.)
func writeLengthPrefixed(w io.Writer, parts ...string) {
	for _, p := range parts {
		fmt.Fprintf(w, "%d:%s", len(p), p)
	}
}

// exchangeIdempotencyKey hashes a canonical description of what an RI exchange
// submit buys: the provider scope, the account scope the money lands in, and
// the exchange's sources and targets. Both lists are sorted, so the ordering a
// client happens to send cannot change the fingerprint, and the element counts
// are hashed too, so a source can never be read as a target.
func exchangeIdempotencyKey(scope, accountScope string, sources, targets []string) string {
	src := append([]string(nil), sources...)
	sort.Strings(src)
	tgt := append([]string(nil), targets...)
	sort.Strings(tgt)

	h := sha256.New()
	writeLengthPrefixed(h, scope, accountScope)
	fmt.Fprintf(h, "|s%d|", len(src))
	writeLengthPrefixed(h, src...)
	fmt.Fprintf(h, "|t%d|", len(tgt))
	writeLengthPrefixed(h, tgt...)
	return hex.EncodeToString(h.Sum(nil))
}

// azureExchangeIdempotencyKey fingerprints an Azure execute request.
//
// INCLUDED, and why omitting any of them would let one token stand for two
// different purchases (silently dropping the second):
//
//   - subscription_id, per the #1495 precedent. ListExchangeableReservations
//     is TENANT-wide, so a scope-blind token aliases across subscriptions.
//   - every source's reservation id AND its quantity.
//   - every target's sku, location, term AND quantity.
//
// EXCLUDED, and why including any of them would let two tokens stand for one
// purchase -- which is the double-spend this whole guard exists to stop:
//
//   - max_payment_due: a spend CAP, not what is bought. A retry that merely
//     raises the cap must not mint a fresh key and commit a second exchange.
//     Two submits differing only in cap buy exactly the same thing.
//   - currency: likewise a guardrail. It is checked against the fresh quote
//     before the claim is ever made and does not change what is bought.
//   - targets[].billing_scope_id: validated to be either absent or the
//     subscription's own derived scope, so it never changes what is charged.
//     Including it would fingerprint the omitted and the spelled-out form of
//     the SAME purchase differently.
//   - the submitting user: the money moves identically whoever sends it, and
//     two users exchanging the same reservation ids IS the double spend. The
//     claim is taken after every authorization gate, so a cross-user 409
//     discloses nothing the caller was not already entitled to see.
//
// Case and surrounding whitespace are normalized away on the ARM identifiers,
// which are case-insensitive: two spellings of one subscription, reservation
// or SKU are one purchase and must fingerprint alike. Every field read here is
// already validated non-empty by validateAzureExecuteBody, so the fingerprint
// can never collapse onto a blank field.
func azureExchangeIdempotencyKey(body AzureExecuteExchangeRequestBody) string {
	sources := make([]string, 0, len(body.Sources))
	for _, s := range body.Sources {
		id := strings.ToLower(strings.TrimSpace(s.ReservationID))
		sources = append(sources, fmt.Sprintf("%d:%s|%d", len(id), id, s.Quantity))
	}
	targets := make([]string, 0, len(body.Targets))
	for _, t := range body.Targets {
		sku := strings.ToLower(strings.TrimSpace(t.SKU))
		loc := strings.ToLower(strings.TrimSpace(t.Location))
		term := strings.ToUpper(strings.TrimSpace(t.Term))
		targets = append(targets, fmt.Sprintf("%d:%s|%d:%s|%d:%s|%d",
			len(sku), sku, len(loc), loc, len(term), term, t.Quantity))
	}
	return exchangeIdempotencyKey(
		azureExchangeIdempotencyScope,
		strings.ToLower(strings.TrimSpace(body.SubscriptionID)),
		sources, targets)
}

// awsExchangeIdempotencyKey fingerprints an AWS execute request. The same
// inclusion/exclusion reasoning as azureExchangeIdempotencyKey applies:
// max_payment_due_usd is excluded as a cap rather than a purchase attribute.
//
// cloudAccountID is the account the exchange's RIs live in, as already
// resolved by the caller (unattributedAccountConstraint when the deployment
// maps to no registered account -- never empty), and region is part of the
// account scope because RI exchanges are region-scoped.
//
// The target list is canonicalized with exactly the precedence
// pkg/exchange.buildTargetConfigs applies when it builds the actual AWS
// request: `targets[]` when non-empty, otherwise the single legacy
// target_offering_id/target_count pair. Diverging from it would let the two
// request spellings of one purchase fingerprint differently and double-spend.
func awsExchangeIdempotencyKey(cloudAccountID string, body ExchangeExecuteRequestBody) string {
	sources := make([]string, 0, len(body.RIIDs))
	for _, raw := range body.RIIDs {
		id := strings.ToLower(strings.TrimSpace(raw))
		sources = append(sources, fmt.Sprintf("%d:%s", len(id), id))
	}

	targetTuple := func(offeringID string, count int32) string {
		id := strings.ToLower(strings.TrimSpace(offeringID))
		return fmt.Sprintf("%d:%s|%d", len(id), id, count)
	}
	var targets []string
	if len(body.Targets) > 0 {
		targets = make([]string, 0, len(body.Targets))
		for _, t := range body.Targets {
			targets = append(targets, targetTuple(t.OfferingID, t.Count))
		}
	} else {
		targets = []string{targetTuple(body.TargetOfferingID, body.TargetCount)}
	}

	region := strings.ToLower(strings.TrimSpace(body.Region))
	accountScope := fmt.Sprintf("%d:%s|%d:%s", len(cloudAccountID), cloudAccountID, len(region), region)
	return exchangeIdempotencyKey(awsExchangeIdempotencyScope, accountScope, sources, targets)
}

// claimExchangeSubmit takes the submit-time idempotency claim for key, and is
// called immediately before the irreversible provider commit -- after every
// authorization gate and every money guardrail have passed.
//
// Claiming last is what makes a release path unnecessary: no request that
// failed a gate ever holds a claim, so no legitimate retry is turned away with
// a spurious 409. Conversely the claim is deliberately NOT released when the
// commit itself fails, because past that point the provider may already have
// accepted the exchange; holding the claim for the rest of the window is the
// fail-closed choice.
//
// A store failure refuses the exchange rather than proceeding unguarded: an
// idempotency guard that cannot be evaluated is not a guard.
func (h *Handler) claimExchangeSubmit(ctx context.Context, key string) error {
	claimed, err := h.config.ClaimRIExchangeIdempotencyKey(ctx, key, riExchangeIdempotencyWindow)
	if err != nil {
		return fmt.Errorf("failed to claim the RI exchange idempotency key: %w", err)
	}
	if !claimed {
		return NewClientError(409, fmt.Sprintf(
			"an identical RI exchange was submitted within the last %s and may still be settling; "+
				"check the exchange's outcome before resubmitting rather than committing it twice",
			riExchangeIdempotencyWindow))
	}
	return nil
}
