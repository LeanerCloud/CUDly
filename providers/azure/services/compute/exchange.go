// Package compute provides Azure VM Reserved Instances client.
// This file implements the "list exchangeable reservations" half of Azure
// Convertible RI exchange parity with AWS EC2 (refs #473).
//
// Azure VM reservations are exchangeable when ALL of the following hold:
//  1. ReservedResourceType == VirtualMachines
//  2. ProvisioningState == Succeeded
//  3. InstanceFlexibility == On  (nil or Off means the reservation is NOT
//     eligible for the cross-SKU/cross-region exchange path)
//
// Listings use armreservations.ReservationClient.NewListAllPager which
// enumerates all reservations across the tenant (not scoped to a single
// subscription), matching what the Azure portal shows on the
// "Reservations" blade.
package compute

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/reservations/armreservations"
)

// ExchangeableReservation represents an Azure VM reservation that is
// eligible for exchange.
type ExchangeableReservation struct {
	ExpiryDate          time.Time `json:"expiry_date,omitempty"`
	ReservationOrderID  string    `json:"reservation_order_id"`
	ReservationID       string    `json:"reservation_id"`
	SKU                 string    `json:"sku"`
	Region              string    `json:"region,omitempty"`
	Term                string    `json:"term,omitempty"`
	InstanceFlexibility string    `json:"instance_flexibility"`
	DisplayName         string    `json:"display_name,omitempty"`
	Quantity            int32     `json:"quantity"`
}

// ExchangeableReservationPager defines the paging contract for listing
// reservations. Satisfied by the pager returned from
// armreservations.ReservationClient.NewListAllPager; a stub can be
// injected for tests via SetExchangeablePager.
//
// Each page carries a ListResult whose Value slice holds
// *armreservations.ReservationResponse items.
type ExchangeableReservationPager interface {
	More() bool
	NextPage(ctx context.Context) (armreservations.ReservationClientListAllResponse, error)
}

// SetExchangeablePager injects a mock pager for unit tests. Tests call
// this instead of providing real Azure credentials.
func (c *ComputeClient) SetExchangeablePager(p ExchangeableReservationPager) {
	c.exchangeablePager = p
}

// ListExchangeableReservations returns all active VM reservations in the
// tenant that are eligible for exchange.
//
// Eligibility requires ProvisioningState == Succeeded AND
// InstanceFlexibility == On. Reservations with nil InstanceFlexibility
// or InstanceFlexibility == Off are excluded.
//
// The listing is tenant-wide (not scoped to c.subscriptionID) because
// the Azure Capacity exchange API operates on reservation order IDs
// which span subscriptions.
//
// Returns an empty non-nil slice when no eligible reservations are found.
func (c *ComputeClient) ListExchangeableReservations(ctx context.Context) ([]ExchangeableReservation, error) {
	pager, err := c.createExchangeablePager()
	if err != nil {
		return nil, fmt.Errorf("compute: list exchangeable reservations: create pager: %w", err)
	}
	return c.collectExchangeableReservations(ctx, pager)
}

// createExchangeablePager returns an injected mock pager when one has
// been set via SetExchangeablePager, or constructs a real
// armreservations.ReservationClient pager otherwise.
func (c *ComputeClient) createExchangeablePager() (ExchangeableReservationPager, error) {
	if c.exchangeablePager != nil {
		return c.exchangeablePager, nil
	}
	client, err := armreservations.NewReservationClient(c.cred, nil)
	if err != nil {
		return nil, fmt.Errorf("create armreservations client: %w", err)
	}
	return client.NewListAllPager(nil), nil
}

// collectExchangeableReservations iterates the pager and applies the
// eligibility filter. Any pagination error is returned immediately
// (partial results are unsafe -- a missing reservation could lead to a
// duplicate exchange attempt upstream).
func (c *ComputeClient) collectExchangeableReservations(ctx context.Context, pager ExchangeableReservationPager) ([]ExchangeableReservation, error) {
	result := make([]ExchangeableReservation, 0)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("compute: list exchangeable reservations: page: %w", err)
		}
		for _, item := range page.Value {
			r := convertToExchangeableReservation(item)
			if r != nil {
				result = append(result, *r)
			}
		}
	}
	return result, nil
}

// isExchangeEligible reports whether the reservation passes the Azure
// exchange eligibility criteria:
//
//   - ReservedResourceType == VirtualMachines
//   - ProvisioningState == Succeeded
//   - InstanceFlexibility == On  (nil is treated as Off)
func isExchangeEligible(item *armreservations.ReservationResponse) bool {
	if item == nil || item.Properties == nil {
		return false
	}
	props := item.Properties
	if props.ReservedResourceType == nil || *props.ReservedResourceType != armreservations.ReservedResourceTypeVirtualMachines {
		return false
	}
	if props.ProvisioningState == nil || *props.ProvisioningState != armreservations.ProvisioningStateSucceeded {
		return false
	}
	return props.InstanceFlexibility != nil && *props.InstanceFlexibility == armreservations.InstanceFlexibilityOn
}

// extractReservationFields reads the optional pointer fields from item and its
// Properties, returning safe zero values for any nil pointers.
//
//nolint:gocritic // tooManyResultsChecker: 7 returns map 1:1 to ExchangeableReservation fields; grouping into a struct would add indirection with no clarity gain
func extractReservationFields(item *armreservations.ReservationResponse) (id, sku, region, term, displayName string, quantity int32, expiryDate time.Time) {
	props := item.Properties // guaranteed non-nil by isExchangeEligible
	if item.ID != nil {
		id = *item.ID
	}
	if item.SKU != nil && item.SKU.Name != nil {
		sku = *item.SKU.Name
	}
	if item.Location != nil {
		region = *item.Location
	}
	if props.Quantity != nil {
		quantity = *props.Quantity
	}
	if props.Term != nil {
		term = string(*props.Term)
	}
	if props.ExpiryDate != nil {
		expiryDate = *props.ExpiryDate
	}
	if props.DisplayName != nil {
		displayName = *props.DisplayName
	}
	return
}

// convertToExchangeableReservation converts a single armreservations item
// to the CUDly type, returning nil when the item fails the eligibility
// criteria.
func convertToExchangeableReservation(item *armreservations.ReservationResponse) *ExchangeableReservation {
	if !isExchangeEligible(item) {
		return nil
	}
	id, sku, region, term, displayName, quantity, expiryDate := extractReservationFields(item)
	orderID := parseReservationOrderID(id)
	// parseReservationOrderID returns "" for IDs that do not contain the expected
	// "/reservationOrders/" segment (malformed or unexpected format). Reservations
	// with an empty order ID are still returned here so the caller can include them
	// in the inventory view, but callers MUST filter out empty-order-ID entries
	// before initiating an exchange operation -- the Azure exchange API requires a
	// non-empty reservationOrderId.
	return &ExchangeableReservation{
		ReservationOrderID:  orderID,
		ReservationID:       id,
		SKU:                 sku,
		Quantity:            quantity,
		Region:              region,
		Term:                term,
		ExpiryDate:          expiryDate,
		InstanceFlexibility: string(armreservations.InstanceFlexibilityOn),
		DisplayName:         displayName,
	}
}

// parseReservationOrderID extracts the reservation order GUID from a
// full ARM resource ID of the form:
//
//	/providers/Microsoft.Capacity/reservationOrders/{orderID}/reservations/{resID}
//
// Returns an empty string when the ID is blank or does not match the
// expected format, rather than returning an error (graceful degradation --
// the reservation is still returned with an empty order ID and the caller
// can skip it for exchange operations that require the order ID).
func parseReservationOrderID(resourceID string) string {
	if resourceID == "" {
		return ""
	}
	// Normalise to lower-case for case-insensitive segment matching.
	lower := strings.ToLower(resourceID)
	const marker = "/reservationorders/"
	idx := strings.Index(lower, marker)
	if idx < 0 {
		return ""
	}
	rest := resourceID[idx+len(marker):]
	// The next path segment is the order GUID; subsequent segments follow
	// a "/" separator.
	end := strings.IndexByte(rest, '/')
	if end < 0 {
		// Trailing segment -- the whole rest is the order ID.
		return rest
	}
	return rest[:end]
}
