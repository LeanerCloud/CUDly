/**
 * Purchases API functions
 */

import { apiRequest } from './client';
import type {
  Recommendation,
  PurchaseResult,
  PurchaseDetails,
  PlannedPurchasesResponse
} from './types';

/**
 * Execute purchases. capacity_percent is the user's bulk-toolbar
 * choice (1..100), recorded on the execution for audit; backend
 * math uses the already-scaled counts in the recommendations list.
 * Omit or pass 100 for "full capacity" (default).
 *
 * execute_mode controls the approval path (issue #289):
 *   - undefined / omitted: standard approval-required flow.
 *   - "direct": bypass the approval email and execute immediately.
 *     Requires the session to hold execute-any:purchases or
 *     execute-own:purchases; the backend returns 403 otherwise.
 */
export async function executePurchase(
  recommendations: Recommendation[],
  capacityPercent?: number,
  executeMode?: string,
): Promise<PurchaseResult> {
  const body: { recommendations: Recommendation[]; capacity_percent?: number; execute_mode?: string } = {
    recommendations,
  };
  if (capacityPercent !== undefined && capacityPercent !== 100) {
    body.capacity_percent = capacityPercent;
  }
  if (executeMode) {
    body.execute_mode = executeMode;
  }
  return apiRequest<PurchaseResult>('/purchases/execute', {
    method: 'POST',
    body: JSON.stringify(body)
  });
}

/**
 * Get purchase details
 */
export async function getPurchaseDetails(executionId: string): Promise<PurchaseDetails> {
  return apiRequest<PurchaseDetails>(`/purchases/${executionId}`);
}

/**
 * Cancel a scheduled purchase
 */
export async function cancelPurchase(executionId: string): Promise<void> {
  return apiRequest<void>(`/purchases/cancel/${executionId}`, { method: 'POST' });
}

/**
 * Approve a pending purchase via the session-authed dashboard route
 * (issue #286). The same backend endpoint also accepts an email-link
 * token for the legacy flow; this caller relies on the bearer-session
 * auth from `apiRequest` and intentionally does not pass a token in
 * the URL -- the backend's session-first dispatch picks the correct
 * auth path based on whether the session matches the
 * approve-{any,own} RBAC matrix.
 */
export async function approvePurchase(executionId: string): Promise<void> {
  return apiRequest<void>(`/purchases/approve/${executionId}`, { method: 'POST' });
}

/**
 * Revoke a completed purchase within the provider's free-cancel window
 * (issue #290). Only shown for Azure rows within the 7-day window; AWS
 * and GCP providers have no direct cancel API so the button is hidden
 * for those rows in the History UI.
 *
 * Returns the revocation result (status, revoked_at, revoked_via) or
 * throws on 4xx/5xx.
 */
export interface RevokePurchaseResult {
  status: string;
  revoked_at: string;
  revoked_via: string;
}

export async function revokePurchase(purchaseId: string): Promise<RevokePurchaseResult> {
  return apiRequest<RevokePurchaseResult>(`/purchases/${purchaseId}/revoke`, { method: 'POST' });
}

/**
 * Retry a failed purchase execution (issue #47).
 *
 * The session-authed endpoint creates a new execution from the failed
 * row's stored Recommendations slice, stamps the predecessor with a
 * pointer to the successor, and increments retry_attempt_n on the
 * chain. Pass `force: true` to bypass the soft-block threshold (the
 * frontend gates this behind a confirm-with-warning dialog so a user
 * can't trip it accidentally).
 *
 * Returns the API response shape — execution_id of the new row and
 * retry_attempt_n on it — so the caller can toast a meaningful link.
 */
export interface RetryPurchaseResult {
  execution_id: string;
  original_execution: string;
  status: string;
  retry_attempt_n: number;
  email_sent?: boolean;
  email_reason?: string;
  // Resolved To address that received the approval email; surfaced so
  // the post-submit toast can name the approver per CR pass on PR #294.
  // Absent when recipient resolution itself failed (no approvers configured).
  approval_recipient?: string;
}

export async function retryPurchase(
  executionId: string,
  opts?: { force?: boolean },
): Promise<RetryPurchaseResult> {
  const qs = opts?.force ? '?force=true' : '';
  return apiRequest<RetryPurchaseResult>(`/purchases/retry/${executionId}${qs}`, {
    method: 'POST',
  });
}

/**
 * Get planned purchases (scheduled from plans)
 */
export async function getPlannedPurchases(): Promise<PlannedPurchasesResponse> {
  return apiRequest<PlannedPurchasesResponse>('/purchases/planned');
}

/**
 * Pause a planned purchase
 */
export async function pausePlannedPurchase(purchaseId: string): Promise<void> {
  return apiRequest<void>(`/purchases/planned/${purchaseId}/pause`, { method: 'POST' });
}

/**
 * Resume a planned purchase
 */
export async function resumePlannedPurchase(purchaseId: string): Promise<void> {
  return apiRequest<void>(`/purchases/planned/${purchaseId}/resume`, { method: 'POST' });
}

/**
 * Run a planned purchase immediately
 */
export async function runPlannedPurchase(purchaseId: string): Promise<PurchaseResult> {
  return apiRequest<PurchaseResult>(`/purchases/planned/${purchaseId}/run`, { method: 'POST' });
}

/**
 * Delete a planned purchase
 */
export async function deletePlannedPurchase(purchaseId: string): Promise<void> {
  return apiRequest<void>(`/purchases/planned/${purchaseId}`, { method: 'DELETE' });
}

/**
 * Create planned purchases for a plan
 */
export async function createPlannedPurchases(planId: string, count: number, startDate: string): Promise<{ created: number }> {
  return apiRequest<{ created: number }>(`/plans/${planId}/purchases`, {
    method: 'POST',
    body: JSON.stringify({ count, start_date: startDate })
  });
}

// ── RI Marketplace (issue #292) ───────────────────────────────────────────────

export interface MarketplacePriceTier {
  /** Number of months remaining this price tier covers. */
  term_months: number;
  /** USD list price per unit for this tier. */
  price: number;
}

export interface MarketplaceListResult {
  listing_id: string;
  listing_state: string;
  price_schedule: MarketplacePriceTier[];
  aws_fee_percent: number;
  note?: string;
}

/**
 * Create an AWS RI Marketplace listing for a Standard Reserved Instance.
 * purchaseId is the purchase_history.purchase_id (AWS ReservedInstancesId).
 * priceSchedule is optional: when omitted the backend computes a default.
 */
export async function createMarketplaceListing(
  purchaseId: string,
  priceSchedule?: MarketplacePriceTier[],
): Promise<MarketplaceListResult> {
  const body = priceSchedule && priceSchedule.length > 0
    ? JSON.stringify({ price_schedule: priceSchedule })
    : undefined;
  return apiRequest<MarketplaceListResult>(`/purchases/${purchaseId}/marketplace-list`, {
    method: 'POST',
    ...(body ? { body } : {}),
  });
}

/**
 * Cancel an active AWS RI Marketplace listing.
 */
export async function cancelMarketplaceListing(purchaseId: string): Promise<{ listing_id: string; listing_state: string }> {
  return apiRequest<{ listing_id: string; listing_state: string }>(
    `/purchases/${purchaseId}/marketplace-cancel`,
    { method: 'POST' },
  );
}
