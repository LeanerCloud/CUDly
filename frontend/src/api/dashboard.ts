/**
 * Dashboard API functions
 */

import { apiRequest } from './client';
import type { DashboardSummary, UpcomingPurchase, Provider } from './types';

/**
 * Get dashboard summary
 */
export async function getDashboardSummary(provider: Provider | 'all' = 'all'): Promise<DashboardSummary> {
  return apiRequest<DashboardSummary>(`/dashboard/summary?provider=${provider}`);
}

/**
 * Get upcoming purchases
 */
export async function getUpcomingPurchases(): Promise<UpcomingPurchase[]> {
  return apiRequest<UpcomingPurchase[]>('/dashboard/upcoming');
}
