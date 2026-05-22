// Package reservations provides the shared two-step calculatePrice->purchase
// flow for all Azure reservation-based service clients (compute, database,
// cache, search, cosmosdb, managedredis).
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
// Idempotency strategy (Option B from issue #677): because Azure mints the
// order ID in step 1 we cannot use a client-supplied ID for deduplication.
// Instead, before step 1 the caller should check for an existing reservation
// that already carries the (execution, rec) identity tag. This package exposes
// IsSessionTimeout to let callers classify errors, and DoPurchaseTwoStep which
// handles the two-step HTTP calls with retry on session-timeout (re-runs
// calculatePrice from scratch on a "Session timed out" purchase 400).
package reservations

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"
)

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
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, calcURL, strings.NewReader(string(bodyBytes)))
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
	resp.Body.Close()

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

// doPurchase calls the purchase endpoint with the given body.
// Returns nil on 200/201/202; on 400 "Session timed out" returns an error
// that IsSessionTimeout recognises. All other non-2xx responses are returned
// as errors verbatim.
func doPurchase(ctx context.Context, httpClient HTTPClient, purchaseURL string, bodyBytes []byte, bearerToken string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, purchaseURL, strings.NewReader(string(bodyBytes)))
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
	resp.Body.Close()

	if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusCreated || resp.StatusCode == http.StatusAccepted {
		return nil
	}
	return fmt.Errorf("reservation purchase failed with status %d: %s", resp.StatusCode, string(body))
}
