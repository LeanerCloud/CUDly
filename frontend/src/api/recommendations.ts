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
 * POST /api/recommendations/refresh response.
 *
 * The backend handler (#257) returns 202 in async (Lambda) mode and 200 in
 * synchronous-collect (HTTP/dev) mode, but the response body shape is the
 * same in both: started_at marks when MarkCollectionStarted ran, and
 * last_collected_at is the previous successful-collect timestamp (null on
 * the first ever collection). The frontend uses these to drive a polling
 * loop that waits for last_collected_at to advance past started_at before
 * declaring the refresh complete.
 */
export interface RefreshRecommendationsResult {
  // Optional in the TS view (backend always sets them) so pre-#257 fixtures
  // and mocks that omit these fields still type-check during the transition.
  started_at?: string;
  last_collected_at?: string | null;
  // Pre-#257 fields kept optional so legacy fixtures/mocks still type-check.
  // The current backend handler does not return them — present here purely
  // for transitional compatibility and removed once existing tests are
  // refreshed in a follow-up.
  recommendations?: number;
  total_savings?: number;
  successful_providers?: string[];
  failed_providers?: Record<string, string>;
}

export async function refreshRecommendations(): Promise<RefreshRecommendationsResult> {
  return apiRequest<RefreshRecommendationsResult>('/recommendations/refresh', { method: 'POST' });
}

/**
 * Recommendations cache freshness payload.
 *
 * - last_collected_at: null on a cold start (cache never populated)
 * - last_collection_error: non-null when the most recent collect attempt
 *   partially or fully failed — the frontend renders a warning banner
 *   in that case, while still showing the older cached rows.
 * - last_collection_started_at: non-null while a collection is in flight
 *   (async-invoked scheduler running). The polling loop watches this and
 *   last_collected_at to detect completion.
 */
export interface RecommendationsFreshness {
  last_collected_at: string | null;
  last_collection_error: string | null;
  // Optional in the TypeScript view for backwards compatibility with
  // pre-#257 fixtures and mocks. The backend always emits the field
  // (json:"last_collection_started_at"), but downstream readers must
  // tolerate `undefined` for legacy callsites that haven't been updated
  // and treat undefined the same as null.
  last_collection_started_at?: string | null;
}

/**
 * Get recommendations cache freshness (last successful collect timestamp
 * and last collection error, if any). Used by the freshness indicator +
 * error banner on the dashboard and recommendations pages.
 */
export async function getRecommendationsFreshness(): Promise<RecommendationsFreshness> {
  return apiRequest<RecommendationsFreshness>('/recommendations/freshness');
}

/**
 * A single sample in the per-recommendation usage time series. Mirrors
 * `api.UsagePoint` in `internal/api/types.go`. The series is always
 * ordered by `timestamp` ascending. cpu_pct / mem_pct are 0..100.
 */
export interface RecommendationUsagePoint {
  timestamp: string;
  cpu_pct: number;
  mem_pct: number;
}

/**
 * Per-id drill-down payload backing the Recommendations row-click
 * drawer. Issued by `GET /api/recommendations/:id/detail`. Mirrors
 * `api.RecommendationDetailResponse` in the backend.
 *
 * `confidence_bucket` is the server-computed confidence tier and
 * replaces the former client-side `confidenceBucketFor` heuristic.
 *
 * `provenance_note` is rendered verbatim — the backend already names
 * the collector + last-collected timestamp.
 *
 * `usage_history` is `[]` until the collector starts persisting
 * time-series utilisation per recommendation; the drawer renders a
 * "Usage history not yet available" line in that case rather than a
 * broken empty chart.
 */
export interface RecommendationDetail {
  id: string;
  usage_history: RecommendationUsagePoint[];
  confidence_bucket: 'low' | 'medium' | 'high';
  provenance_note: string;
  /**
   * Present (non-empty array) when the recommendation exists but is filtered
   * out by an account-service override (issue #214). Each element names one
   * failing dimension: "enabled=false", "engine", "region", or
   * "resource_type". Absent / undefined means the rec is fully visible.
   */
  hidden_by?: string[];
}

/**
 * Fetch the per-id detail payload for the Recommendations drawer.
 * Backend contract: `GET /api/recommendations/:id/detail`. Returns 404
 * for unknown ids (and for ids that exist but belong to accounts the
 * caller is not allowed to see — the existence-disclosure-safe path).
 *
 * The id is path-encoded so ids containing reserved URL characters
 * round-trip cleanly.
 */
export async function getRecommendationDetail(id: string): Promise<RecommendationDetail> {
  return apiRequest<RecommendationDetail>(`/recommendations/${encodeURIComponent(id)}/detail`);
}
