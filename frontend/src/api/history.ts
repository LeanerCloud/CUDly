/**
 * History and Analytics API functions
 */

import { apiRequest } from './client';
import type {
  PurchaseHistory,
  HistoryFilters,
  SavingsAnalyticsResponse,
  SavingsAnalyticsFilters,
  SavingsBreakdownResponse
} from './types';

/**
 * Get purchase history
 */
export async function getHistory(filters: HistoryFilters = {}): Promise<PurchaseHistory[]> {
  const params = new URLSearchParams();
  if (filters.start) params.set('start', filters.start);
  if (filters.end) params.set('end', filters.end);
  if (filters.provider) params.set('provider', filters.provider);
  if (filters.planId) params.set('plan_id', filters.planId);

  const queryString = params.toString();
  return apiRequest<PurchaseHistory[]>(`/history${queryString ? '?' + queryString : ''}`);
}

/**
 * Get savings analytics
 */
export async function getSavingsAnalytics(filters: SavingsAnalyticsFilters = {}): Promise<SavingsAnalyticsResponse> {
  const params = new URLSearchParams();
  if (filters.start) params.set('start', filters.start);
  if (filters.end) params.set('end', filters.end);
  if (filters.interval) params.set('interval', filters.interval);
  if (filters.provider) params.set('provider', filters.provider);
  if (filters.service) params.set('service', filters.service);

  const queryString = params.toString();
  return apiRequest<SavingsAnalyticsResponse>(`/history/analytics${queryString ? '?' + queryString : ''}`);
}

/**
 * Get savings breakdown
 */
export async function getSavingsBreakdown(
  dimension: 'service' | 'provider' | 'region',
  filters: { start?: string; end?: string } = {}
): Promise<SavingsBreakdownResponse> {
  const params = new URLSearchParams();
  params.set('dimension', dimension);
  if (filters.start) params.set('start', filters.start);
  if (filters.end) params.set('end', filters.end);

  return apiRequest<SavingsBreakdownResponse>(`/history/breakdown?${params.toString()}`);
}
