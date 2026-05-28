import * as api from '../api';
import type { AccountServiceOverride } from '../api/accounts';

/**
 * Fetches AccountServiceOverride arrays for a set of account IDs in parallel.
 * Returns a Map keyed by account ID. Per-account fetch errors are silently
 * swallowed so callers can always fall back to their default seed.
 */
export async function fetchOverridesForAccounts(
  ids: Iterable<string>,
): Promise<Map<string, AccountServiceOverride[]>> {
  const out = new Map<string, AccountServiceOverride[]>();
  await Promise.all(
    Array.from(ids).map(async (id) => {
      try {
        out.set(id, await api.listAccountServiceOverrides(id));
      } catch {
        // Silent fallback -- callers handle missing entries gracefully.
      }
    }),
  );
  return out;
}
