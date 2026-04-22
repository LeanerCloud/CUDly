/**
 * Utility functions for CUDly dashboard
 */

import type { CloudAccount, AccountListFilters } from './api';

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
 * Canonical date-only format used everywhere in the UI: "Apr 17, 2026".
 * The en-US locale + `month: 'short'` removes ambiguity from pure numeric
 * forms ("3/25/2026" vs "25.3.2026") and stays compact enough for table
 * cells while being readable for non-technical users.
 */
export function formatDate(date: string | Date | null | undefined): string {
  if (!date) return '';
  const d = new Date(date);
  if (isNaN(d.getTime())) return '';
  return d.toLocaleDateString('en-US', { year: 'numeric', month: 'short', day: 'numeric' });
}

/**
 * Canonical date+time format: "Apr 17, 2026, 00:06". 24-hour clock chosen
 * over 12-hour "12:06 AM" because timestamps frequently compare relative
 * durations (purchase at T, next at T+N) and 24h is unambiguous.
 */
export function formatDateTime(date: string | Date | null | undefined): string {
  if (!date) return '';
  const d = new Date(date);
  if (isNaN(d.getTime())) return '';
  return d.toLocaleString('en-US', {
    year: 'numeric', month: 'short', day: 'numeric',
    hour: '2-digit', minute: '2-digit', hour12: false,
  });
}

/**
 * Format a date as a relative time ("5m ago", "2h ago", …). Used by the
 * recommendations freshness indicator and user-management row timestamps.
 * Returns an empty string for null/undefined/invalid input so call sites
 * don't have to guard.
 */
export function formatRelativeTime(date: string | Date | null | undefined): string {
  if (!date) return '';
  const d = new Date(date);
  if (isNaN(d.getTime())) return '';
  const diffMs = Date.now() - d.getTime();
  const diffMins = Math.floor(diffMs / 60000);
  const diffHours = Math.floor(diffMs / 3600000);
  const diffDays = Math.floor(diffMs / 86400000);
  if (diffMins < 1) return 'Just now';
  if (diffMins < 60) return `${diffMins}m ago`;
  if (diffHours < 24) return `${diffHours}h ago`;
  if (diffDays < 7) return `${diffDays}d ago`;
  // Fall back to the canonical formatDate so the Last-Login-style columns
  // don't mix "18h ago" with a raw-locale "3/25/2026" once the entry ages
  // past 7 days.
  return formatDate(d);
}

/**
 * Canonical term rendering: "1 Year" / "3 Years". Matches the case used
 * by the Default Term dropdown in Settings → Purchasing (commitmentOptions)
 * so tables and selectors agree.
 */
export function formatTerm(years: number | null | undefined): string {
  if (years === null || years === undefined || isNaN(years)) return '';
  const n = Math.round(years);
  return `${n} Year${n === 1 ? '' : 's'}`;
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

/**
 * Populate a `<select>` element with cloud accounts, filtering by provider.
 * Preserves the current selection. Non-critical — silently ignores API errors.
 */
export async function populateAccountFilter(
  selectId: string,
  listAccountsFn: (filter?: AccountListFilters) => Promise<CloudAccount[]>,
  provider?: string
): Promise<void> {
  const select = document.getElementById(selectId) as HTMLSelectElement | null;
  if (!select) return;
  try {
    const filter: AccountListFilters | undefined =
      provider && provider !== 'all' && provider !== ''
        ? { provider: provider as AccountListFilters['provider'] }
        : undefined;
    const accounts = await listAccountsFn(filter);
    const current = select.value;
    while (select.options.length > 1) select.remove(1);
    accounts.forEach(a => {
      const opt = document.createElement('option');
      opt.value = a.id;
      opt.textContent = `${a.name} (${a.external_id})`;
      select.appendChild(opt);
    });
    select.value = current;
  } catch {
    // Non-critical — filter just won't be populated
  }
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
