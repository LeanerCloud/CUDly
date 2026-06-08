/**
 * Utility functions for CUDly dashboard
 */

import type { CloudAccount, AccountListFilters } from './api';

/**
 * Default number of fraction digits used by formatCurrency when the caller
 * doesn't pass an explicit `digits` value. Exported so downstream code (e.g.
 * the Opportunities-table filter precision logic in recommendations.ts) can
 * stay in lock-step with the formatter rather than hard-coding `0` and
 * silently drifting if this default ever changes.
 */
export const CURRENCY_DEFAULT_DIGITS = 0;

/**
 * Format a number as currency.
 *
 * `digits` controls fraction digits (defaults to `CURRENCY_DEFAULT_DIGITS`,
 * currently 0 — the dashboard KPI format). Callers that need cents, e.g.
 * Purchase History summary cards and RI Exchange cost chips, pass `digits:
 * 2`. Having a single helper keeps "$0" / "$0.00" / "$0.00/hr" from
 * diverging across the app.
 */
export function formatCurrency(
  value: number | null | undefined,
  currency: string = '$',
  digits: number = CURRENCY_DEFAULT_DIGITS
): string {
  // Represent absent or non-finite values distinctly from a real $0 so
  // callers can see when data is missing vs actually zero (finding 11-N2).
  if (value === null || value === undefined || !Number.isFinite(value)) {
    return '--';
  }
  return `${currency}${value.toLocaleString(undefined, {
    minimumFractionDigits: digits,
    maximumFractionDigits: digits
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
 * Escape HTML to prevent XSS in text content (between tags).
 * Escapes &, <, >, ", and ' so the result is safe in both text and attribute contexts.
 */
export function escapeHtml(str: string | null | undefined): string {
  if (!str) return '';
  return str
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
    .replace(/'/g, '&#39;');
}

/**
 * Escape a value for safe interpolation inside an HTML attribute value delimited by double quotes.
 * Encodes &, <, >, ", and ' -- the quote escaping is essential here because a raw " terminates
 * the attribute and allows injecting new attributes or markup. Use escapeHtml for text nodes.
 */
export function escapeHtmlAttr(str: string | null | undefined): string {
  return escapeHtml(str);
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
 * Deep clone an object using structuredClone (finding 11-N1).
 *
 * structuredClone preserves undefined values, Date objects, Set and Map
 * instances, and circular-reference-safe trees.  The old JSON round-trip
 * silently dropped undefined, converted Date to strings, and lost Set/Map.
 *
 * For callers that explicitly need JSON-serialisation semantics (e.g. to
 * strip undefined before sending to the API) use jsonClone() instead.
 */
export function deepClone<T>(obj: T): T {
  if (obj === null || typeof obj !== 'object') return obj;
  return structuredClone(obj);
}

/**
 * Clone via JSON round-trip: drops undefined values, converts Date to strings,
 * and does not preserve Set/Map.  Use this only when JSON-serialisation
 * semantics are explicitly required; prefer deepClone for general cloning.
 */
export function jsonClone<T>(obj: T): T {
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

/** Canonical set of known cloud providers used for whitelist checks. */
const KNOWN_PROVIDERS = ['aws', 'azure', 'gcp'] as const;

/**
 * Return the whitelisted CSS class name for a provider value.
 *
 * Whitelist guards against stored XSS when provider strings are interpolated
 * into innerHTML class attributes (findings H1/L4, issues #443 / CR #253).
 * An unrecognised value produces an empty string (no badge modifier class).
 */
export function providerBadgeClass(provider: string | null | undefined): string {
  if (!provider) return '';
  const n = provider.toLowerCase();
  return (KNOWN_PROVIDERS as readonly string[]).includes(n) ? n : '';
}

/**
 * Render a `<span class="provider-badge ...">LABEL</span>` string for use in
 * innerHTML templates.  Both the CSS class and the text label are sanitised:
 * the class is whitelisted to known providers and the label is HTML-escaped.
 */
export function providerBadgeHtml(provider: string | null | undefined): string {
  const cls = providerBadgeClass(provider);
  const label = escapeHtml((provider || '').toUpperCase());
  return `<span class="provider-badge${cls ? ` ${cls}` : ''}">${label}</span>`;
}

/**
 * Compute the amortized monthly cost: the recurring monthly cost plus the
 * upfront cost spread evenly over the term.
 *
 * Used when the "Amortize upfront over term" toggle is enabled, so every
 * commitment type (No Upfront, Partial Upfront, All Upfront) is compared
 * on an apples-to-apples total-cost-per-month basis.
 *
 * Guard rules (return monthlyCost unchanged):
 *   - termYears <= 0 or not finite: cannot divide by zero / infinity.
 *   - upfrontCost is null, undefined, or not a finite number: no upfront data.
 *
 * No Upfront (upfrontCost === 0): amortized term is 0, result equals monthlyCost.
 * Partial Upfront: result is monthlyCost + upfrontCost / (termYears * 12).
 * All Upfront (monthlyCost === 0): result is just the amortized upfront slice.
 */
export function amortizedMonthly(
  monthlyCost: number,
  upfrontCost: number | null | undefined,
  termYears: number,
): number {
  if (!termYears || !isFinite(termYears) || termYears <= 0) return monthlyCost;
  if (upfrontCost == null || !isFinite(upfrontCost)) return monthlyCost;
  return monthlyCost + upfrontCost / (termYears * 12);
}
