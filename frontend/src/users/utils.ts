/**
 * User management utility functions
 */

/**
 * Format relative time. Re-exported from the shared utils module so the
 * users/ namespace keeps its existing import surface while dashboards +
 * recommendations use the same implementation.
 */
export { formatRelativeTime, formatDate } from '../utils';

/**
 * Escape HTML to prevent XSS. Re-exported from the shared utils module:
 * the previous local DOM round-trip (div.textContent -> div.innerHTML)
 * did not escape quote characters, so values interpolated inside
 * double-quoted attributes could break out of the attribute. The shared
 * implementation escapes &, <, >, " and ' and is safe in both text and
 * attribute contexts.
 */
export { escapeHtml } from '../utils';

import type { APIGroup } from '../api';
import { showToast } from '../toast';

/**
 * Validate that a proposed group-ID list does not contain a contradictory
 * combination: a view-only group (all permissions carry action === 'view')
 * paired with a write-capable group (at least one permission has a
 * non-view action such as create/update/delete/execute/approve/admin).
 *
 * Because CUDly's permission model is additive (the user gets the union of
 * all group permissions), pairing a view-only group with a write group is
 * semantically contradictory: the intent of a "Viewer" group is that the
 * user should have read-only access, but adding any write-capable group
 * silently elevates them.  The UI should surface this immediately rather
 * than letting the combination go through.
 *
 * Groups with zero permissions are skipped (they grant nothing and do not
 * participate in the conflict).
 *
 * Returns a human-readable error string when the combination is invalid,
 * or null when it is acceptable.
 *
 * @param groupIds   UUIDs of groups the user would be assigned
 * @param allGroups  Full list of loaded groups (from availableGroups)
 */
export function validateGroupCombination(
  groupIds: string[],
  allGroups: APIGroup[],
): string | null {
  const groups = groupIds
    .map(id => allGroups.find(g => g.id === id))
    .filter((g): g is APIGroup => g !== undefined);

  const viewOnlyGroups = groups.filter(
    g => Array.isArray(g.permissions) &&
         g.permissions.length > 0 &&
         g.permissions.every(p => p.action === 'view'),
  );
  const writeGroups = groups.filter(
    g => Array.isArray(g.permissions) &&
         g.permissions.some(p => p.action !== 'view'),
  );

  if (viewOnlyGroups.length === 0 || writeGroups.length === 0) return null;

  const viewNames = viewOnlyGroups.map(g => `"${g.name}"`).join(', ');
  const writeNames = writeGroups.map(g => `"${g.name}"`).join(', ');
  return (
    `Contradictory group combination: ${viewNames} ` +
    `${viewOnlyGroups.length === 1 ? 'is' : 'are'} view-only but ` +
    `${writeNames} grant${writeGroups.length === 1 ? 's' : ''} write permissions. ` +
    `Assign a view-only group or write-capable groups, not both.`
  );
}

/**
 * Show error message. Delegates to the shared toast system so callers
 * across the codebase get consistent bottom-right stacked toasts with
 * severity colours, × dismiss, and the 30s default timeout.
 */
export function showError(message: string): void {
  showToast({ message, kind: 'error' });
}

/**
 * Show success message. Uses a shorter 5s timeout because confirms are
 * transient; the 30s default is tuned for errors that users should
 * notice.
 */
export function showSuccess(message: string): void {
  showToast({ message, kind: 'success', timeout: 5_000 });
}
