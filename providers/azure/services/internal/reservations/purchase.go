// Package reservations provides the shared two-step calculatePrice->purchase
// flow for all Azure reservation-based service clients (compute, database,
// cache, search, cosmosdb, managedredis, synapse).
//
// Azure's Reservations API shifted away from direct-PUT for newer SKU families
// (Burstable v2 and likely others). The previous pattern:
//
//	PUT /providers/Microsoft.Capacity/reservationOrders/{client-generated-id}
//
// now returns 400 "Session timed out - Call CalculatePrice again and provide
// the new Reservation Order ID for purchase" for affected families. The fix is
// a two-step flow (issue #677):
//
//  1. POST /providers/Microsoft.Capacity/calculatePrice  -- mints a session-
//     bound reservationOrderId and a price quote.
//  2. POST /providers/Microsoft.Capacity/reservationOrders/{id}/purchase  --
//     commits the order using the Azure-minted ID.
//
// Idempotency strategy (Option B from issue #677, finished by issue #721):
// because Azure mints the order ID in step 1 we cannot use a client-supplied ID
// for deduplication (PR #653's IdempotencyGUID pattern). Instead, every
// purchase request body carries two tags:
//
//   - purchase-automation=<cudly-cli|cudly-web>   -- cosmetic attribution.
//   - cudly-idempotency-token=<deterministic hex> -- the (execution, rec)
//     identity from common.DeriveIdempotencyToken.
//
// Before invoking the two-step flow, DoIdempotentPurchaseTwoStep lists existing
// reservation orders and short-circuits when one already carries the same
// idempotency token, so a re-driven purchase of a stranded execution (issue
// #636) reuses the prior reservation rather than buying a second one. Mirrors
// the AWS EC2 findRIByIdempotencyToken pattern (providers/aws/services/ec2).
//
// This package exposes IsSessionTimeout to let callers classify errors,
// DoPurchaseTwoStep (the raw two-step flow without the dedupe guard, retained
// for callers that have no IdempotencyToken), DoIdempotentPurchaseTwoStep
// (the guarded wrapper that every service executor uses), and ApplyPurchaseTags
// (the canonical place where both tags are written into a request body).
package reservations

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/LeanerCloud/CUDly/pkg/common"
)

// ParseTermYears maps a term string to an integer year count.
// Returns an error for any value outside the explicit allowlist so that
// callers fail closed rather than silently coercing to a 1-year purchase.
// Recognised forms: "", "1", "1yr", "1y" -> 1; "3", "3yr", "3y" -> 3.
// This is the canonical parser; service clients that previously used a local
// literal comparison (rec.Term == "3yr" || rec.Term == "3") should call this
// instead to get consistent error reporting on unrecognised terms.
func ParseTermYears(term string) (int, error) {
	switch strings.ToLower(strings.TrimSpace(term)) {
	case "", "1", "1yr", "1y":
		return 1, nil
	case "3", "3yr", "3y":
		return 3, nil
	default:
		return 0, fmt.Errorf("unsupported reservation term: %s", term)
	}
}

// apiVersion is the GA api-version for the Microsoft.Capacity Reservations API.
// Pinned to 2022-11-01 — the last stable version before Azure introduced the
// calculatePrice requirement for new SKU families.
const apiVersion = "2022-11-01"

// BaseURL is the Azure Resource Manager base URL.
const BaseURL = "https://management.azure.com"

// CalculatePriceURL returns the calculatePrice endpoint URL.
func CalculatePriceURL() string {
	return BaseURL + "/providers/Microsoft.Capacity/calculatePrice?api-version=" + apiVersion
}

// PurchaseURL returns the purchase endpoint URL for a given reservationOrderId.
func PurchaseURL(reservationOrderID string) string {
	return fmt.Sprintf("%s/providers/Microsoft.Capacity/reservationOrders/%s/purchase?api-version=%s",
		BaseURL, reservationOrderID, apiVersion)
}

// ReservationOrdersListURL returns the list-reservation-orders endpoint URL.
// The endpoint is tenant-wide (no subscription prefix): the caller's bearer
// token determines visibility, and the idempotency-token tag is globally
// unique per (execution, rec), so a tenant-wide search returns the correct
// order regardless of which subscription executed the purchase.
func ReservationOrdersListURL() string {
	return BaseURL + "/providers/Microsoft.Capacity/reservationOrders?api-version=" + apiVersion
}

// ApplyPurchaseTags stamps the two purchase-attribution tags onto an Azure
// reservation purchase request body. Either tag is omitted when its source
// value is empty. When both are empty, no tags map is added (preserving the
// untagged shape some legacy CLI test paths expect).
//
// The tags are sent verbatim in the calculatePrice + purchase request bodies
// and are persisted by Azure onto the resulting reservation order so the
// idempotency tag can be read back by FindReservationOrderByIdempotencyToken
// on a re-drive.
func ApplyPurchaseTags(body map[string]interface{}, source, idempotencyToken string) {
	if source == "" && idempotencyToken == "" {
		return
	}
	tags := map[string]string{}
	if source != "" {
		tags[common.PurchaseTagKey] = source
	}
	if idempotencyToken != "" {
		tags[common.IdempotencyTagKey] = idempotencyToken
	}
	body["tags"] = tags
}

// calculatePriceResponse is the JSON shape returned by the calculatePrice POST.
// Only the fields we need are decoded; Azure returns additional billing fields
// that are irrelevant to the purchase flow.
type calculatePriceResponse struct {
	Properties struct {
		ReservationOrderID string `json:"reservationOrderId"`
	} `json:"properties"`
}

// sessionTimeoutFragment is the substring Azure includes in the 400 error
// message when a purchase call uses a stale or client-generated order ID.
// Matching on the substring (rather than an exact message) tolerates minor
// phrasing changes in future API versions.
const sessionTimeoutFragment = "Session timed out"

// IsSessionTimeout reports whether err looks like the "Session timed out"
// 400 error from the Azure Reservations purchase endpoint. It matches the
// error message produced by DoPurchaseTwoStep so callers can distinguish
// retriable session-expiry from other 4xx errors.
func IsSessionTimeout(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), sessionTimeoutFragment)
}

// HTTPClient is the minimal interface required by DoPurchaseTwoStep, matching
// the interface used by all service clients (net/http.Client satisfies it).
type HTTPClient interface {
	Do(req *http.Request) (*http.Response, error)
}

const (
	purchaseMaxAttempts = 3
	purchaseRetryDelay  = 2 * time.Second
)

// DoPurchaseTwoStep executes the calculatePrice->purchase two-step flow.
//
// It POSTs bodyBytes to calculateURL to mint an Azure-assigned reservationOrderId,
// then POSTs the same body to the derived purchaseURL. On a "Session timed out"
// 400 from the purchase endpoint (Azure has retired the session) it re-runs
// calculatePrice from scratch (up to purchaseMaxAttempts total attempts).
// Other 4xx/5xx errors are returned immediately without retry.
//
// Returns the Azure-minted reservationOrderId on success, which the caller
// should store as the CommitmentID.
func DoPurchaseTwoStep(ctx context.Context, httpClient HTTPClient, calcURL string, bodyBytes []byte, bearerToken string) (string, error) {
	for attempt := 1; attempt <= purchaseMaxAttempts; attempt++ {
		// Step 1: calculatePrice -- mint a session-bound reservationOrderId.
		orderID, err := doCalculatePrice(ctx, httpClient, calcURL, bodyBytes, bearerToken)
		if err != nil {
			return "", fmt.Errorf("calculatePrice (attempt %d/%d): %w", attempt, purchaseMaxAttempts, err)
		}

		// Step 2: purchase -- commit the order.
		purchaseErr := doPurchase(ctx, httpClient, PurchaseURL(orderID), bodyBytes, bearerToken)
		if purchaseErr == nil {
			return orderID, nil
		}

		if IsSessionTimeout(purchaseErr) && attempt < purchaseMaxAttempts {
			log.Printf("reservation purchase session timed out (attempt %d/%d), re-running calculatePrice in %s",
				attempt, purchaseMaxAttempts, purchaseRetryDelay)
			select {
			case <-time.After(purchaseRetryDelay):
			case <-ctx.Done():
				return "", fmt.Errorf("reservation purchase canceled during retry delay: %w", ctx.Err())
			}
			continue
		}
		return "", purchaseErr
	}
	return "", fmt.Errorf("reservation purchase failed after %d attempts (session timeout)", purchaseMaxAttempts)
}

// doCalculatePrice calls the calculatePrice endpoint and returns the
// Azure-minted reservationOrderId from the response.
func doCalculatePrice(ctx context.Context, httpClient HTTPClient, calcURL string, bodyBytes []byte, bearerToken string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, calcURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return "", fmt.Errorf("build calculatePrice request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+bearerToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("calculatePrice HTTP call: %w", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close() // #nosec G104 -- body fully drained by io.ReadAll before Close; transport close error does not affect correctness

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("calculatePrice failed with status %d: %s", resp.StatusCode, string(body))
	}

	var result calculatePriceResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return "", fmt.Errorf("decode calculatePrice response: %w", err)
	}
	if result.Properties.ReservationOrderID == "" {
		return "", fmt.Errorf("calculatePrice returned empty reservationOrderId (body: %s)", string(body))
	}
	return result.Properties.ReservationOrderID, nil
}

// reservationOrdersListResponse is the JSON shape of a single page returned by
// the list-reservation-orders endpoint. Only the fields needed for the
// idempotency-token lookup are decoded.
type reservationOrdersListResponse struct {
	Value []struct {
		// Name is the reservation order GUID; this is what Azure mints as
		// reservationOrderId during calculatePrice and what DoPurchaseTwoStep
		// returns on success.
		Name string `json:"name"`
		// Tags carries any tags stamped on the order at purchase time.
		Tags map[string]string `json:"tags"`
		// Properties.ProvisioningState lets us skip terminal-failed orders so a
		// cancelled/failed reservation with the same idempotency tag does not
		// suppress a legitimate fresh purchase. Anything outside the
		// suppressing-failure set is treated as a duplicate that should
		// short-circuit (mirrors the AWS pattern of including in-flight states,
		// not just succeeded ones).
		Properties struct {
			ProvisioningState string `json:"provisioningState"`
		} `json:"properties"`
	} `json:"value"`
	NextLink string `json:"nextLink"`
}

// reservationOrderTerminalFailedStates enumerates the provisioning states for
// which an existing reservation order MUST NOT suppress a fresh purchase.
// A cancelled/failed/expired order with a matching idempotency tag was
// either rolled back or aged out; the recommendation is still owed and the
// re-drive must be allowed through.
var reservationOrderTerminalFailedStates = map[string]struct{}{
	"Cancelled": {},
	"Failed":    {},
	"Expired":   {},
}

// FindReservationOrderByIdempotencyToken lists reservation orders visible to
// the bearer token and returns the reservation order ID (GUID) of the FIRST
// order tagged with the supplied idempotency token. Returns ("", false, nil)
// when no matching order is found and ("", false, err) on a transport / 4xx /
// decode failure.
//
// Pagination follows nextLink; the entire listing is walked because Azure has
// no server-side tag filter on this endpoint. Reservation order listings are
// small (one entry per purchase ever made on the tenant, typically tens to low
// hundreds), so the full walk is cheap and runs only on purchase paths where
// an idempotency token is supplied (i.e. never on the CLI legacy path).
//
// Terminal-failed orders (Cancelled, Failed, Expired) are skipped so they do
// not suppress a legitimate fresh purchase of the same recommendation -- this
// mirrors the EC2 dedupe guard's state filter (active + payment-pending only).
func FindReservationOrderByIdempotencyToken(ctx context.Context, httpClient HTTPClient, bearerToken, idempotencyToken string) (string, bool, error) {
	if idempotencyToken == "" {
		return "", false, nil
	}

	nextURL := ReservationOrdersListURL()
	for nextURL != "" {
		page, err := fetchReservationOrdersPage(ctx, httpClient, nextURL, bearerToken)
		if err != nil {
			return "", false, err
		}
		if orderID, found := matchReservationOrderInPage(page, idempotencyToken); found {
			return orderID, true, nil
		}
		nextURL = page.NextLink
	}

	return "", false, nil
}

// fetchReservationOrdersPage GETs a single page of the list-reservation-orders
// endpoint and decodes the response. Extracted from
// FindReservationOrderByIdempotencyToken so the outer pagination loop stays
// under the gocyclo:10 threshold.
func fetchReservationOrdersPage(ctx context.Context, httpClient HTTPClient, pageURL, bearerToken string) (*reservationOrdersListResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, pageURL, nil)
	if err != nil {
		return nil, fmt.Errorf("build list reservation orders request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+bearerToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("list reservation orders HTTP call: %w", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close() // #nosec G104 -- body fully drained by io.ReadAll before Close; transport close error does not affect correctness

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("list reservation orders failed with status %d: %s", resp.StatusCode, string(body))
	}

	var page reservationOrdersListResponse
	if err := json.Unmarshal(body, &page); err != nil {
		return nil, fmt.Errorf("decode list reservation orders response: %w", err)
	}
	return &page, nil
}

// matchReservationOrderInPage scans one decoded page for an order tagged with
// the idempotency token. Terminal-failed (Cancelled/Failed/Expired) orders
// are skipped so they do not suppress a legitimate fresh purchase. Extracted
// from FindReservationOrderByIdempotencyToken to keep the function under the
// gocyclo:10 threshold enforced by the pre-commit hook.
func matchReservationOrderInPage(page *reservationOrdersListResponse, idempotencyToken string) (string, bool) {
	for _, order := range page.Value {
		if order.Tags[common.IdempotencyTagKey] != idempotencyToken {
			continue
		}
		if _, terminalFailed := reservationOrderTerminalFailedStates[order.Properties.ProvisioningState]; terminalFailed {
			continue
		}
		if order.Name == "" {
			continue
		}
		return order.Name, true
	}
	return "", false
}

// DoIdempotentPurchaseTwoStep is the dedupe-guarded wrapper around
// DoPurchaseTwoStep that every Azure service executor should use. Flow:
//
//  1. If idempotencyToken is empty, fall straight through to DoPurchaseTwoStep
//     (preserves the CLI path's pre-issue-721 behaviour, which has no owning
//     execution and so no token to dedupe on).
//  2. Otherwise, look for an existing reservation order already tagged with
//     the token. If found, short-circuit and return its order ID -- this is a
//     re-drive of an execution that already created the reservation; buying
//     again would double-charge the customer (issues #641, #721).
//  3. Otherwise, call DoPurchaseTwoStep. The caller is responsible for having
//     stamped the idempotency tag into bodyBytes via ApplyPurchaseTags so the
//     resulting order is tagged and the NEXT re-drive will short-circuit at
//     step 2.
//
// A failed lookup must NOT fall through to a purchase: doing so would defeat
// the guard and risk a double-buy on a re-drive. The lookup error is returned
// verbatim, and the recovery sweep treats the recommendation as not-yet-
// purchased and retries the whole guarded path (mirroring the EC2 EC2
// findRIByIdempotencyToken safety contract in providers/aws/services/ec2).
func DoIdempotentPurchaseTwoStep(ctx context.Context, httpClient HTTPClient, calcURL string, bodyBytes []byte, bearerToken, idempotencyToken string) (string, error) {
	if idempotencyToken != "" {
		existingID, found, err := FindReservationOrderByIdempotencyToken(ctx, httpClient, bearerToken, idempotencyToken)
		if err != nil {
			return "", fmt.Errorf("idempotency lookup failed before Azure reservation purchase (refusing to purchase to avoid a possible double-buy): %w", err)
		}
		if found {
			log.Printf("Azure reservation order for idempotency token %s already exists (%s); skipping purchase (issue #721 re-drive)", common.MaskToken(idempotencyToken), existingID)
			return existingID, nil
		}
	}
	return DoPurchaseTwoStep(ctx, httpClient, calcURL, bodyBytes, bearerToken)
}

// doPurchase calls the purchase endpoint with the given body.
// Returns nil on 200/201/202; on 400 "Session timed out" returns an error
// that IsSessionTimeout recognises. All other non-2xx responses are returned
// as errors verbatim.
func doPurchase(ctx context.Context, httpClient HTTPClient, purchaseURL string, bodyBytes []byte, bearerToken string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, purchaseURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return fmt.Errorf("build purchase request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+bearerToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to purchase reservation: %w", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close() // #nosec G104 -- body fully drained by io.ReadAll before Close; transport close error does not affect correctness

	if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusCreated || resp.StatusCode == http.StatusAccepted {
		return nil
	}
	return fmt.Errorf("reservation purchase failed with status %d: %s", resp.StatusCode, string(body))
}
