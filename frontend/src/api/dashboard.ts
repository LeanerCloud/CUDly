/**
 * Dashboard API functions
 */

import { apiRequest } from './client';
import type { DashboardSummary, UpcomingPurchase, Provider } from './types';

/**
 * Get dashboard summary
 */
export async function getDashboardSummary(provider: Provider | 'all' = 'all', accountIDs: string[] = []): Promise<DashboardSummary> {
  const params = new URLSearchParams({ provider });
  if (accountIDs.length > 0) params.set('account_ids', accountIDs.join(','));
  return apiRequest<DashboardSummary>(`/dashboard/summary?${params}`);
}

/**
 * Get upcoming purchases
 */
export async function getUpcomingPurchases(): Promise<UpcomingPurchase[]> {
  return apiRequest<UpcomingPurchase[]>('/dashboard/upcoming');
}
