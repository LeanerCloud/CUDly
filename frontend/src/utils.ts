/**
 * Utility functions for CUDly dashboard
 */

/**
 * Format a number as currency
 */
export function formatCurrency(value: number | null | undefined, currency: string = '$'): string {
  if (value === null || value === undefined || isNaN(value)) {
    return `${currency}0`;
  }
  return `${currency}${value.toLocaleString(undefined, {
    minimumFractionDigits: 0,
    maximumFractionDigits: 0
  })}`;
}

/**
 * Format a date for display
 */
export function formatDate(date: string | Date | null | undefined): string {
  if (!date) return '';
  const d = new Date(date);
  if (isNaN(d.getTime())) return '';
  return d.toLocaleDateString();
}

/**
 * Format a date with time
 */
export function formatDateTime(date: string | Date | null | undefined): string {
  if (!date) return '';
  const d = new Date(date);
  if (isNaN(d.getTime())) return '';
  return d.toLocaleString();
}

export interface DateParts {
  day: number;
  month: string;
}

/**
 * Get day and month from date
 */
export function getDateParts(date: string | Date | null | undefined): DateParts {
  if (!date) return { day: 0, month: '' };
  const d = new Date(date);
  if (isNaN(d.getTime())) return { day: 0, month: '' };
  return {
    day: d.getDate(),
    month: d.toLocaleString('default', { month: 'short' })
  };
}

/**
 * Debounce a function call
 */
export function debounce<T extends (...args: unknown[]) => unknown>(
  fn: T,
  delay: number
): (...args: Parameters<T>) => void {
  let timeoutId: ReturnType<typeof setTimeout>;
  return function (this: unknown, ...args: Parameters<T>): void {
    clearTimeout(timeoutId);
    timeoutId = setTimeout(() => fn.apply(this, args), delay);
  };
}

/**
 * Throttle a function call
 */
export function throttle<T extends (...args: unknown[]) => unknown>(
  fn: T,
  limit: number
): (...args: Parameters<T>) => void {
  let inThrottle = false;
  return function (this: unknown, ...args: Parameters<T>): void {
    if (!inThrottle) {
      fn.apply(this, args);
      inThrottle = true;
      setTimeout(() => (inThrottle = false), limit);
    }
  };
}

/**
 * Escape HTML to prevent XSS
 */
export function escapeHtml(str: string | null | undefined): string {
  if (!str) return '';
  const div = document.createElement('div');
  div.textContent = str;
  return div.innerHTML;
}

/**
 * Parse URL query parameters
 */
export function parseQueryParams(queryString: string): Record<string, string> {
  const params: Record<string, string> = {};
  const searchParams = new URLSearchParams(queryString);
  for (const [key, value] of searchParams) {
    params[key] = value;
  }
  return params;
}

/**
 * Build URL with query parameters
 */
export function buildUrl(baseUrl: string, params: Record<string, string | number | boolean | null | undefined>): string {
  try {
    const url = new URL(baseUrl, window.location.origin);
    Object.entries(params).forEach(([key, value]) => {
      if (value !== undefined && value !== null && value !== '') {
        url.searchParams.set(key, String(value));
      }
    });
    return url.toString();
  } catch {
    return baseUrl;
  }
}

/**
 * Deep clone an object using JSON serialization.
 * Note: This drops undefined values, converts Date to strings,
 * and does not preserve Set/Map objects. For objects containing
 * Set/Map, use structuredClone() instead.
 */
export function deepClone<T>(obj: T): T {
  if (obj === null || typeof obj !== 'object') return obj;
  return JSON.parse(JSON.stringify(obj)) as T;
}

/**
 * Validate email format
 */
export function isValidEmail(email: string | null | undefined): boolean {
  if (!email) return false;
  const emailRegex = /^[^\s@]+@[^\s@]+\.[^\s@]+$/;
  return emailRegex.test(email);
}

export type RampSchedule = 'immediate' | 'weekly-25pct' | 'monthly-10pct' | 'custom';

/**
 * Format ramp schedule for display
 */
export function formatRampSchedule(schedule: RampSchedule | string | null | undefined): string {
  switch (schedule) {
    case 'immediate':
      return 'Immediate';
    case 'weekly-25pct':
      return 'Weekly 25%';
    case 'monthly-10pct':
      return 'Monthly 10%';
    case 'custom':
      return 'Custom';
    default:
      return schedule || 'Unknown';
  }
}

export interface StatusBadge {
  class: 'active' | 'paused' | 'disabled';
  label: string;
}

/**
 * Get status badge class
 */
export function getStatusBadge(enabled: boolean, autoPurchase: boolean): StatusBadge {
  if (!enabled) {
    return { class: 'disabled', label: 'Disabled' };
  }
  if (autoPurchase) {
    return { class: 'active', label: 'Active' };
  }
  return { class: 'paused', label: 'Manual' };
}

/**
 * Calculate payback period in months.
 * Returns 0 when upfrontCost <= 0 (immediate payback) or monthlySavings <= 0 (no savings).
 */
export function calculatePaybackMonths(upfrontCost: number, monthlySavings: number): number {
  if (!monthlySavings || monthlySavings <= 0) return 0;
  if (!upfrontCost || upfrontCost <= 0) return 0;
  return Math.ceil(upfrontCost / monthlySavings);
}
