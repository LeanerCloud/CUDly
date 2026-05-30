/**
 * Inventory & Coverage API functions.
 *
 * Per-commitment list view for the Inventory & Coverage → Active
 * commitments sub-tab (issue #340 deferred sub-task). Reads aggregated
 * purchase-history rows from `/api/inventory/commitments` and unwraps
 * the `{commitments}` envelope so callers see a flat array.
 */

import { apiRequest } from './client';
import type { CoverageBreakdownResponse, InventoryCommitment } from './types';

/** Shared filter params for the two Inventory endpoints. */
export interface InventoryFilter {
  provider?: string;
  accountID?: string;
}

/**
 * List active (non-expired) commitments across the user's accessible
 * accounts. Both `provider` and `accountID` are forwarded as query params
 * so the global topbar chips propagate into the result set (issue #866).
 * The backend filters expired rows and sorts by soonest-expiring first.
 */
export async function listActiveCommitments(filter: InventoryFilter = {}): Promise<InventoryCommitment[]> {
  const parts: string[] = [];
  if (filter.accountID) parts.push(`account_id=${encodeURIComponent(filter.accountID)}`);
  if (filter.provider) parts.push(`provider=${encodeURIComponent(filter.provider)}`);
  const qs = parts.length > 0 ? `?${parts.join('&')}` : '';
  const resp = await apiRequest<{ commitments: InventoryCommitment[] }>(`/inventory/commitments${qs}`);
  return resp.commitments ?? [];
}

/**
 * Fetch per-provider, per-service coverage breakdowns.
 * Returns one section per known provider (aws, azure, gcp). A provider
 * with no usage data has services=null and overall_coverage_pct=null.
 * Both `provider` and `accountID` are forwarded as query params so the
 * global topbar chips propagate into coverage data (issue #866).
 */
export async function getCoverageBreakdown(filter: InventoryFilter = {}): Promise<CoverageBreakdownResponse> {
  const parts: string[] = [];
  if (filter.accountID) parts.push(`account_id=${encodeURIComponent(filter.accountID)}`);
  if (filter.provider) parts.push(`provider=${encodeURIComponent(filter.provider)}`);
  const qs = parts.length > 0 ? `?${parts.join('&')}` : '';
  return apiRequest<CoverageBreakdownResponse>(`/inventory/coverage${qs}`);
}
