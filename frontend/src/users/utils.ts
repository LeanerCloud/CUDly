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
 * Escape HTML to prevent XSS
 */
export function escapeHtml(text: string): string {
  const div = document.createElement('div');
  div.textContent = text;
  return div.innerHTML;
}

import { showToast } from '../toast';

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
