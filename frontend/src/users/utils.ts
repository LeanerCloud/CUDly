/**
 * User management utility functions
 */

/**
 * Format relative time
 */
export function formatRelativeTime(dateString: string): string {
  const date = new Date(dateString);
  const now = new Date();
  const diffMs = now.getTime() - date.getTime();
  const diffMins = Math.floor(diffMs / 60000);
  const diffHours = Math.floor(diffMs / 3600000);
  const diffDays = Math.floor(diffMs / 86400000);

  if (diffMins < 1) return 'Just now';
  if (diffMins < 60) return `${diffMins}m ago`;
  if (diffHours < 24) return `${diffHours}h ago`;
  if (diffDays < 7) return `${diffDays}d ago`;
  return date.toLocaleDateString();
}

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
