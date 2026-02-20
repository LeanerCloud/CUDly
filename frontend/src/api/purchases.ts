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
 * Execute purchases
 */
export async function executePurchase(recommendations: Recommendation[]): Promise<PurchaseResult> {
  return apiRequest<PurchaseResult>('/purchases/execute', {
    method: 'POST',
    body: JSON.stringify({ recommendations })
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
