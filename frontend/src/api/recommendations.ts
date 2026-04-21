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

/**
 * Recommendations cache freshness payload. `last_collected_at` is null
 * on a cold start (cache never populated). `last_collection_error` is
 * non-null when the most recent collect attempt partially or fully
 * failed — the frontend renders a warning banner in that case, while
 * still showing the older cached rows.
 */
export interface RecommendationsFreshness {
  last_collected_at: string | null;
  last_collection_error: string | null;
}

/**
 * Get recommendations cache freshness (last successful collect timestamp
 * and last collection error, if any). Used by the freshness indicator +
 * error banner on the dashboard and recommendations pages.
 */
export async function getRecommendationsFreshness(): Promise<RecommendationsFreshness> {
  return apiRequest<RecommendationsFreshness>('/recommendations/freshness');
}
