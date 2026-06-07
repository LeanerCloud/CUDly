/**
 * Unit tests for utility functions
 */
import {
  formatCurrency,
  formatDate,
  formatDateTime,
  formatTerm,
  getDateParts,
  debounce,
  throttle,
  escapeHtml,
  parseQueryParams,
  buildUrl,
  deepClone,
  jsonClone,
  isValidEmail,
  formatRampSchedule,
  getStatusBadge,
  calculatePaybackMonths,
  providerBadgeClass,
  providerBadgeHtml
} from '../utils';

describe('formatCurrency', () => {
  test('formats positive numbers correctly', () => {
    expect(formatCurrency(1000)).toBe('$1,000');
    expect(formatCurrency(1234567)).toBe('$1,234,567');
    expect(formatCurrency(99.99)).toBe('$100');
  });

  test('formats zero correctly', () => {
    expect(formatCurrency(0)).toBe('$0');
  });

  test('handles null and undefined with distinct absent marker (11-N2)', () => {
    // Absent/non-finite values now render as '--' to distinguish missing data
    // from a real $0 balance (finding 11-N2, feedback_nullable_not_zero).
    expect(formatCurrency(null as unknown as number)).toBe('--');
    expect(formatCurrency(undefined as unknown as number)).toBe('--');
  });

  test('handles NaN with distinct absent marker (11-N2)', () => {
    expect(formatCurrency(NaN)).toBe('--');
  });

  test('still renders real zero as $0', () => {
    expect(formatCurrency(0)).toBe('$0');
  });

  test('supports custom currency symbol', () => {
    expect(formatCurrency(1000, '€')).toBe('€1,000');
    expect(formatCurrency(500, '£')).toBe('£500');
  });
});

describe('formatDate', () => {
  test('renders canonical "Mon DD, YYYY" form regardless of browser locale', () => {
    // 2024-03-15 → "Mar 15, 2024" in en-US short-month format. The check
    // is locale-invariant because formatDate forces en-US.
    expect(formatDate('2024-03-15')).toBe('Mar 15, 2024');
  });

  test('formats Date object into the same canonical form', () => {
    expect(formatDate(new Date('2024-03-15T00:00:00Z'))).toMatch(/^Mar \d{1,2}, 2024$/);
  });

  test('returns empty string for null/undefined', () => {
    expect(formatDate(null as unknown as string)).toBe('');
    expect(formatDate(undefined as unknown as string)).toBe('');
    expect(formatDate('')).toBe('');
  });

  test('returns empty string for invalid date', () => {
    expect(formatDate('not-a-date')).toBe('');
    expect(formatDate('2024-13-45')).toBe('');
  });
});

describe('formatDateTime', () => {
  test('renders "Mon DD, YYYY, HH:mm" with 24-hour clock', () => {
    // Construct a known UTC instant and assert the format shape. The exact
    // hour digits depend on TZ, so match the structure instead of a literal.
    const result = formatDateTime('2024-03-15T14:30:00Z');
    expect(result).toMatch(/^Mar \d{1,2}, 2024, \d{2}:\d{2}$/);
  });

  test('uses 24-hour clock (no AM/PM)', () => {
    const result = formatDateTime('2024-03-15T14:30:00Z');
    expect(result).not.toMatch(/AM|PM/i);
  });

  test('returns empty string for invalid input', () => {
    expect(formatDateTime(null as unknown as string)).toBe('');
    expect(formatDateTime('')).toBe('');
  });
});

describe('formatTerm', () => {
  test('renders "1 Year" (singular) and "3 Years" (plural)', () => {
    expect(formatTerm(1)).toBe('1 Year');
    expect(formatTerm(3)).toBe('3 Years');
  });

  test('rounds floats to nearest integer for pluralization', () => {
    expect(formatTerm(1.0)).toBe('1 Year');
    expect(formatTerm(2.9)).toBe('3 Years');
  });

  test('returns empty string for null/undefined/NaN', () => {
    expect(formatTerm(null)).toBe('');
    expect(formatTerm(undefined)).toBe('');
    expect(formatTerm(NaN)).toBe('');
  });

  test('handles arbitrary positive integers', () => {
    expect(formatTerm(5)).toBe('5 Years');
    expect(formatTerm(0)).toBe('0 Years');
  });
});

describe('getDateParts', () => {
  test('returns day and month for valid date', () => {
    const result = getDateParts('2024-03-15');
    expect(result.day).toBe(15);
    expect(result.month).toBeTruthy();
  });

  test('returns zeros for null/undefined', () => {
    expect(getDateParts(null as unknown as string)).toEqual({ day: 0, month: '' });
    expect(getDateParts(undefined as unknown as string)).toEqual({ day: 0, month: '' });
  });

  test('returns zeros for invalid date', () => {
    expect(getDateParts('invalid')).toEqual({ day: 0, month: '' });
  });
});

describe('debounce', () => {
  beforeEach(() => {
    jest.useFakeTimers();
  });

  afterEach(() => {
    jest.useRealTimers();
  });

  test('delays function execution', () => {
    const fn = jest.fn();
    const debouncedFn = debounce(fn, 100);

    debouncedFn();
    expect(fn).not.toHaveBeenCalled();

    jest.advanceTimersByTime(100);
    expect(fn).toHaveBeenCalledTimes(1);
  });

  test('resets timer on subsequent calls', () => {
    const fn = jest.fn();
    const debouncedFn = debounce(fn, 100);

    debouncedFn();
    jest.advanceTimersByTime(50);
    debouncedFn();
    jest.advanceTimersByTime(50);
    expect(fn).not.toHaveBeenCalled();

    jest.advanceTimersByTime(50);
    expect(fn).toHaveBeenCalledTimes(1);
  });
});

describe('throttle', () => {
  beforeEach(() => {
    jest.useFakeTimers();
  });

  afterEach(() => {
    jest.useRealTimers();
  });

  test('executes immediately on first call', () => {
    const fn = jest.fn();
    const throttledFn = throttle(fn, 100);

    throttledFn();
    expect(fn).toHaveBeenCalledTimes(1);
  });

  test('limits execution rate', () => {
    const fn = jest.fn();
    const throttledFn = throttle(fn, 100);

    throttledFn();
    throttledFn();
    throttledFn();
    expect(fn).toHaveBeenCalledTimes(1);

    jest.advanceTimersByTime(100);
    throttledFn();
    expect(fn).toHaveBeenCalledTimes(2);
  });
});

describe('escapeHtml', () => {
  test('escapes HTML special characters', () => {
    expect(escapeHtml('<script>alert("xss")</script>')).toBe('&lt;script&gt;alert("xss")&lt;/script&gt;');
  });

  test('escapes ampersands', () => {
    expect(escapeHtml('A & B')).toBe('A &amp; B');
  });

  test('escapes quotes', () => {
    expect(escapeHtml('"test"')).toBe('"test"');
  });

  test('returns empty string for null/undefined', () => {
    expect(escapeHtml(null as unknown as string)).toBe('');
    expect(escapeHtml(undefined as unknown as string)).toBe('');
    expect(escapeHtml('')).toBe('');
  });

  test('passes through safe strings', () => {
    expect(escapeHtml('Hello World')).toBe('Hello World');
    expect(escapeHtml('123')).toBe('123');
  });
});

describe('parseQueryParams', () => {
  test('parses query string correctly', () => {
    const result = parseQueryParams('?foo=bar&baz=qux');
    expect(result).toEqual({ foo: 'bar', baz: 'qux' });
  });

  test('handles empty query string', () => {
    expect(parseQueryParams('')).toEqual({});
    expect(parseQueryParams('?')).toEqual({});
  });

  test('decodes URL-encoded values', () => {
    const result = parseQueryParams('?name=hello%20world');
    expect(result.name).toBe('hello world');
  });
});

describe('buildUrl', () => {
  test('builds URL with parameters', () => {
    const result = buildUrl('/api/test', { foo: 'bar', baz: 'qux' });
    expect(result).toContain('/api/test');
    expect(result).toContain('foo=bar');
    expect(result).toContain('baz=qux');
  });

  test('skips null and empty values', () => {
    const result = buildUrl('/api/test', { foo: 'bar', empty: '', nil: null });
    expect(result).toContain('foo=bar');
    expect(result).not.toContain('empty=');
    expect(result).not.toContain('nil=');
  });
});

describe('deepClone', () => {
  test('clones simple objects', () => {
    const obj = { a: 1, b: 2 };
    const clone = deepClone(obj);
    expect(clone).toEqual(obj);
    expect(clone).not.toBe(obj);
  });

  test('clones nested objects', () => {
    const obj = { a: { b: { c: 1 } } };
    const clone = deepClone(obj);
    expect(clone).toEqual(obj);
    clone.a.b.c = 2;
    expect(obj.a.b.c).toBe(1);
  });

  test('clones arrays', () => {
    const arr = [1, 2, [3, 4]] as [number, number, number[]];
    const clone = deepClone(arr);
    expect(clone).toEqual(arr);
    (clone[2] as number[])[0] = 99;
    expect((arr[2] as number[])[0]).toBe(3);
  });

  test('returns primitives as-is', () => {
    expect(deepClone(null)).toBe(null);
    expect(deepClone(42)).toBe(42);
    expect(deepClone('test')).toBe('test');
  });
});

describe('isValidEmail', () => {
  test('validates correct email formats', () => {
    expect(isValidEmail('test@example.com')).toBe(true);
    expect(isValidEmail('user.name@domain.co.uk')).toBe(true);
    expect(isValidEmail('user+tag@example.org')).toBe(true);
  });

  test('rejects invalid email formats', () => {
    expect(isValidEmail('notanemail')).toBe(false);
    expect(isValidEmail('missing@domain')).toBe(false);
    expect(isValidEmail('@nodomain.com')).toBe(false);
    expect(isValidEmail('spaces in@email.com')).toBe(false);
  });

  test('returns false for empty/null values', () => {
    expect(isValidEmail('')).toBe(false);
    expect(isValidEmail(null as unknown as string)).toBe(false);
    expect(isValidEmail(undefined as unknown as string)).toBe(false);
  });
});

describe('formatRampSchedule', () => {
  test('formats known schedules', () => {
    expect(formatRampSchedule('immediate')).toBe('Immediate');
    expect(formatRampSchedule('weekly-25pct')).toBe('Weekly 25%');
    expect(formatRampSchedule('monthly-10pct')).toBe('Monthly 10%');
    expect(formatRampSchedule('custom')).toBe('Custom');
  });

  test('returns input for unknown schedules', () => {
    expect(formatRampSchedule('unknown')).toBe('unknown');
  });

  test('handles null/undefined', () => {
    expect(formatRampSchedule(null as unknown as string)).toBe('Unknown');
    expect(formatRampSchedule(undefined as unknown as string)).toBe('Unknown');
  });
});

describe('getStatusBadge', () => {
  test('returns disabled for disabled items', () => {
    const result = getStatusBadge(false, true);
    expect(result.class).toBe('disabled');
    expect(result.label).toBe('Disabled');
  });

  test('returns active for enabled + auto purchase', () => {
    const result = getStatusBadge(true, true);
    expect(result.class).toBe('active');
    expect(result.label).toBe('Active');
  });

  test('returns paused for enabled without auto purchase', () => {
    const result = getStatusBadge(true, false);
    expect(result.class).toBe('paused');
    expect(result.label).toBe('Manual');
  });
});

describe('calculatePaybackMonths', () => {
  test('calculates correct payback period', () => {
    expect(calculatePaybackMonths(1200, 100)).toBe(12);
    expect(calculatePaybackMonths(600, 100)).toBe(6);
    expect(calculatePaybackMonths(150, 100)).toBe(2);
  });

  test('rounds up partial months', () => {
    expect(calculatePaybackMonths(550, 100)).toBe(6);
  });

  test('returns 0 for zero/negative savings', () => {
    expect(calculatePaybackMonths(1000, 0)).toBe(0);
    expect(calculatePaybackMonths(1000, -50)).toBe(0);
  });

  test('returns 0 for zero/negative upfront', () => {
    expect(calculatePaybackMonths(0, 100)).toBe(0);
    expect(calculatePaybackMonths(-100, 100)).toBe(0);
  });
});

// Regression tests for H1/D1/L4: providerBadgeClass whitelist.
// Pre-fix the class was interpolated raw from the API (plans.ts) or allowed any
// alphanumeric string (dashboard.ts).  Post-fix all sites go through this
// helper which enforces the aws|azure|gcp allow-list.
describe('providerBadgeClass', () => {
  test('returns lowercase provider for known aws', () => {
    expect(providerBadgeClass('aws')).toBe('aws');
    expect(providerBadgeClass('AWS')).toBe('aws');
  });

  test('returns lowercase provider for known azure', () => {
    expect(providerBadgeClass('azure')).toBe('azure');
    expect(providerBadgeClass('Azure')).toBe('azure');
  });

  test('returns lowercase provider for known gcp', () => {
    expect(providerBadgeClass('gcp')).toBe('gcp');
  });

  test('returns empty string for unknown provider (XSS payload neutralised)', () => {
    // Regression: pre-fix this value would be injected verbatim as a CSS class.
    expect(providerBadgeClass('aws"><img src=x onerror=alert(1)>')).toBe('');
    expect(providerBadgeClass('evil-class')).toBe('');
    expect(providerBadgeClass('unknown')).toBe('');
  });

  test('returns empty string for null/undefined/empty', () => {
    expect(providerBadgeClass(null)).toBe('');
    expect(providerBadgeClass(undefined)).toBe('');
    expect(providerBadgeClass('')).toBe('');
  });
});

// Regression tests for H1: providerBadgeHtml XSS in plans.ts.
// Pre-fix: plans.ts interpolated provider raw into class + textContent via innerHTML.
// Post-fix: class is whitelisted; text is HTML-escaped.
describe('providerBadgeHtml', () => {
  test('renders known provider with correct class and uppercased label', () => {
    const html = providerBadgeHtml('aws');
    expect(html).toContain('class="provider-badge aws"');
    expect(html).toContain('>AWS<');
  });

  test('renders azure with correct class', () => {
    const html = providerBadgeHtml('azure');
    expect(html).toContain('class="provider-badge azure"');
    expect(html).toContain('>AZURE<');
  });

  test('neutralises XSS payload in class attribute position', () => {
    // The raw value would previously be injected as: class="provider-badge <payload>"
    const payload = 'aws"><img src=x onerror=alert(document.cookie)>';
    const html = providerBadgeHtml(payload);
    // Class must not contain the raw payload.
    expect(html).not.toContain(payload);
    // No <img> or onerror in the output.
    expect(html).not.toContain('<img');
    expect(html).not.toContain('onerror');
    // Class falls back to just provider-badge (no extra class token).
    expect(html).toMatch(/class="provider-badge"/);
  });

  test('HTML-escapes text content of unknown provider', () => {
    const html = providerBadgeHtml('<script>alert(1)</script>');
    expect(html).not.toContain('<script>');
    expect(html).toContain('&lt;SCRIPT&gt;');
  });

  test('handles null/undefined gracefully', () => {
    expect(providerBadgeHtml(null)).toContain('provider-badge');
    expect(providerBadgeHtml(undefined)).toContain('provider-badge');
  });
});

// ---------------------------------------------------------------------------
// Regression tests for finding 11-N1: deepClone defaults to structuredClone.
// Note: the jsdom test environment polyfills structuredClone with a JSON
// round-trip (see setup.ts) because jsdom does not expose Node's built-in
// structuredClone. As a result, undefined preservation can only be asserted
// when the native structuredClone is available (not in jsdom).
// ---------------------------------------------------------------------------
describe('deepClone (11-N1: structuredClone default)', () => {
  test('deep copy: mutation of clone does not affect source', () => {
    const obj = { nested: { x: 1 } };
    const clone = deepClone(obj);
    clone.nested.x = 99;
    expect(obj.nested.x).toBe(1);
  });

  test('deep copy: clones arrays independently', () => {
    const arr = [1, [2, 3]] as [number, number[]];
    const clone = deepClone(arr);
    (clone[1] as number[])[0] = 99;
    expect((arr[1] as number[])[0]).toBe(2);
  });

  test('handles primitives and null without throwing', () => {
    expect(deepClone(null)).toBe(null);
    expect(deepClone(42)).toBe(42);
    expect(deepClone('hello')).toBe('hello');
  });
});

describe('jsonClone (explicit JSON-serialisation variant)', () => {
  test('drops undefined values (expected JSON behaviour)', () => {
    const obj = { a: 1, b: undefined };
    const clone = jsonClone(obj);
    // JSON round-trip removes keys whose value is undefined.
    expect('b' in clone).toBe(false);
  });
});

// ---------------------------------------------------------------------------
// Regression tests for finding 11-N2: formatCurrency distinguishes absent from $0.
// ---------------------------------------------------------------------------
describe('formatCurrency (11-N2: absent vs real zero)', () => {
  test('null returns -- not $0', () => {
    expect(formatCurrency(null as unknown as number)).toBe('--');
  });

  test('undefined returns -- not $0', () => {
    expect(formatCurrency(undefined as unknown as number)).toBe('--');
  });

  test('NaN returns -- not $0', () => {
    expect(formatCurrency(NaN)).toBe('--');
  });

  test('Infinity returns -- not $Infinity', () => {
    expect(formatCurrency(Infinity)).toBe('--');
  });

  test('real zero still renders as $0', () => {
    expect(formatCurrency(0)).toBe('$0');
  });
});
