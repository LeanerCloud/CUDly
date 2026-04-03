/**
 * Dashboard API functions
 */

import { apiRequest } from './client';
import type { DashboardSummary, UpcomingPurchase, Provider } from './types';

/**
 * Get dashboard summary
 */
export async function getDashboardSummary(provider: Provider | 'all' = 'all', accountIDs: string[] = []): Promise<DashboardSummary> {
  let url = `/dashboard/summary?provider=${provider}`;
  if (accountIDs.length > 0) url += `&account_ids=${accountIDs.join(',')}`;
  return apiRequest<DashboardSummary>(url);
}

/**
 * Get upcoming purchases
 */
export async function getUpcomingPurchases(): Promise<UpcomingPurchase[]> {
  return apiRequest<UpcomingPurchase[]>('/dashboard/upcoming');
}
