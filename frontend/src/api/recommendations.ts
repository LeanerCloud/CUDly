/**
 * Recommendations API functions
 */

import { apiRequest } from './client';
import type { Recommendation, RecommendationFilters } from './types';

/**
 * Get recommendations
 */
export async function getRecommendations(filters: RecommendationFilters = {}): Promise<Recommendation[]> {
  const params = new URLSearchParams();
  if (filters.provider) params.set('provider', filters.provider);
  if (filters.service) params.set('service', filters.service);
  if (filters.region) params.set('region', filters.region);
  if (filters.minSavings) params.set('min_savings', String(filters.minSavings));
  if (filters.account_ids && filters.account_ids.length > 0) params.set('account_ids', filters.account_ids.join(','));

  const queryString = params.toString();
  return apiRequest<Recommendation[]>(`/recommendations${queryString ? '?' + queryString : ''}`);
}

/**
 * Refresh recommendations
 */
export async function refreshRecommendations(): Promise<{ message: string }> {
  return apiRequest<{ message: string }>('/recommendations/refresh', { method: 'POST' });
}
