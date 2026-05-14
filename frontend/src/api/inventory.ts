/**
 * Inventory & Coverage API functions.
 *
 * Per-commitment list view for the Inventory & Coverage → Active
 * commitments sub-tab (issue #340 deferred sub-task). Reads aggregated
 * purchase-history rows from `/api/inventory/commitments` and unwraps
 * the `{commitments}` envelope so callers see a flat array.
 */

import { apiRequest } from './client';
import type { InventoryCommitment } from './types';

/**
 * List active (non-expired) commitments across the user's accessible
 * accounts. Pass `accountID` to scope the read to a single account; omit
 * for the full cross-account list. The backend always filters expired
 * rows out and sorts by soonest-expiring first.
 */
export async function listActiveCommitments(accountID?: string): Promise<InventoryCommitment[]> {
  const qs = accountID ? `?account_id=${encodeURIComponent(accountID)}` : '';
  const resp = await apiRequest<{ commitments: InventoryCommitment[] }>(`/inventory/commitments${qs}`);
  return resp.commitments ?? [];
}
