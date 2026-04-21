/**
 * User management utility functions
 */

/**
 * Format relative time. Re-exported from the shared utils module so the
 * users/ namespace keeps its existing import surface while dashboards +
 * recommendations use the same implementation.
 */
export { formatRelativeTime } from '../utils';

/**
 * Escape HTML to prevent XSS
 */
export function escapeHtml(text: string): string {
  const div = document.createElement('div');
  div.textContent = text;
  return div.innerHTML;
}

/**
 * Show error message
 */
export function showError(message: string): void {
  const errorDiv = document.createElement('div');
  errorDiv.className = 'toast toast-error';
  errorDiv.textContent = message;
  document.body.appendChild(errorDiv);
  setTimeout(() => errorDiv.remove(), 5000);
}

/**
 * Show success message
 */
export function showSuccess(message: string): void {
  const successDiv = document.createElement('div');
  successDiv.className = 'toast toast-success';
  successDiv.textContent = message;
  document.body.appendChild(successDiv);
  setTimeout(() => successDiv.remove(), 3000);
}
