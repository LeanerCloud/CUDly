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
  escapeHtmlAttr,
  parseQueryParams,
  buildUrl,
  deepClone,
  jsonClone,
  isValidEmail,
  formatRampSchedule,
  getStatusBadge,
  calculatePaybackMonths,
  providerBadgeClass,
  providerBadgeHtml,
  amortizedMonthly,
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
    // 2024-03-15 → "Mar 15, 2024" in en-US short-month format. The check is
    // locale-invariant (formatDate forces en-US) and timezone-invariant: a
    // bare "YYYY-MM-DD" is parsed as local midnight, so the displayed day
    // stays 15 even in browsers west of UTC (previously rendered "Mar 14").
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
    // Digit-shaped but out-of-range: must not silently roll over (month 13,
    // day 45) into a valid date — parseDateInput round-trips and rejects it.
    expect(formatDate('2024-13-45')).toBe('');
  });

  test('does not shift a bare calendar day across the day boundary', () => {
    // A date-only string is the intended calendar day; it must render the
    // same day regardless of the runner's timezone (regression for the
    // UTC-midnight off-by-one that showed "Mar 14" west of UTC).
    expect(formatDate('2024-01-01')).toBe('Jan 1, 2024');
    expect(formatDate('2024-12-31')).toBe('Dec 31, 2024');
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
    // Out-of-range digits must be rejected, not rolled over.
    expect(getDateParts('2024-13-45')).toEqual({ day: 0, month: '' });
  });

  test('keeps a bare calendar day stable across timezones', () => {
    // Regression: a date-only string parsed as UTC midnight previously
    // reported the previous day's date west of UTC.
    expect(getDateParts('2024-03-15').day).toBe(15);
    expect(getDateParts('2024-01-01').day).toBe(1);
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
    expect(escapeHtml('<script>alert("xss")</script>')).toBe('&lt;script&gt;alert(&quot;xss&quot;)&lt;/script&gt;');
  });

  test('escapes ampersands', () => {
    expect(escapeHtml('A & B')).toBe('A &amp; B');
  });

  test('escapes quotes', () => {
    expect(escapeHtml('"test"')).toBe('&quot;test&quot;');
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

describe('escapeHtmlAttr', () => {
  test('encodes double-quote to &quot; (attribute boundary safety)', () => {
    // A raw " would terminate the surrounding attribute and allow markup injection.
    expect(escapeHtmlAttr('"')).toBe('&quot;');
    expect(escapeHtmlAttr('a" onmouseover="alert(1)')).toBe('a&quot; onmouseover=&quot;alert(1)');
  });

  test('encodes single-quote to &#39;', () => {
    expect(escapeHtmlAttr("'")).toBe('&#39;');
    expect(escapeHtmlAttr("it's")).toBe('it&#39;s');
  });

  test('still encodes & < > (inherits from escapeHtml)', () => {
    expect(escapeHtmlAttr('&')).toBe('&amp;');
    expect(escapeHtmlAttr('<img>')).toBe('&lt;img&gt;');
    expect(escapeHtmlAttr('x"><img src=x onerror=alert(1)>')).toBe('x&quot;&gt;&lt;img src=x onerror=alert(1)&gt;');
  });

  test('returns empty string for null/undefined', () => {
    expect(escapeHtmlAttr(null as unknown as string)).toBe('');
    expect(escapeHtmlAttr(undefined as unknown as string)).toBe('');
    expect(escapeHtmlAttr('')).toBe('');
  });

  test('passes through safe strings unchanged', () => {
    expect(escapeHtmlAttr('abc-123_OK')).toBe('abc-123_OK');
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
// The jsdom test environment polyfills structuredClone via Node's
// MessageChannel serializer (see setup.ts), which implements the real HTML
// structured clone algorithm, so full browser semantics are asserted here.
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

// ---------------------------------------------------------------------------
// Regression tests for TEST-07: the test-environment structuredClone must
// implement real structured clone semantics, not a JSON round-trip. Each test
// below fails against the previous JSON.parse(JSON.stringify(...)) polyfill
// and passes against the browser/Node algorithm.
// ---------------------------------------------------------------------------
describe('deepClone (TEST-07: real structured clone semantics)', () => {
  test('preserves undefined-valued properties (JSON round-trip drops them)', () => {
    const obj: { a: number; b: number | undefined } = { a: 1, b: undefined };
    const clone = deepClone(obj);
    expect('b' in clone).toBe(true);
    expect(clone.b).toBeUndefined();
  });

  test('preserves Date instances (JSON round-trip stringifies them)', () => {
    const obj = { ts: new Date('2026-01-02T03:04:05.678Z') };
    const clone = deepClone(obj);
    expect(clone.ts).toBeInstanceOf(Date);
    expect(clone.ts.getTime()).toBe(obj.ts.getTime());
    expect(clone.ts).not.toBe(obj.ts);
  });

  test('preserves Map and Set (JSON round-trip degrades them to {})', () => {
    const obj = { m: new Map([['k', 1]]), s: new Set([1, 2]) };
    const clone = deepClone(obj);
    expect(clone.m).toBeInstanceOf(Map);
    expect(clone.m.get('k')).toBe(1);
    expect(clone.m).not.toBe(obj.m);
    expect(clone.s).toBeInstanceOf(Set);
    expect(clone.s.has(2)).toBe(true);
    expect(clone.s).not.toBe(obj.s);
  });

  test('handles cyclic references (JSON round-trip throws)', () => {
    interface Cyclic {
      name: string;
      self?: Cyclic;
    }
    const obj: Cyclic = { name: 'loop' };
    obj.self = obj;
    const clone = deepClone(obj);
    expect(clone).not.toBe(obj);
    expect(clone.self).toBe(clone);
  });

  test('throws on functions like the browser implementation', () => {
    // JSON round-trip silently drops functions; structured clone must throw.
    expect(() => deepClone({ fn: (): number => 1 })).toThrow();
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

describe('amortizedMonthly', () => {
  test('All Upfront: zero recurring cost produces positive amortized value', () => {
    // $0/mo recurring + $1200 upfront over 1 year = $100/mo amortized
    expect(amortizedMonthly(0, 1200, 1)).toBeCloseTo(100, 5);
  });

  test('All Upfront: 3-year term spreads upfront over 36 months', () => {
    // $0/mo recurring + $3600 upfront over 3 years = $100/mo amortized
    expect(amortizedMonthly(0, 3600, 3)).toBeCloseTo(100, 5);
  });

  test('Partial Upfront: recurring + amortized-upfront slice', () => {
    // $50/mo recurring + $600 upfront over 1 year = $50 + $50 = $100/mo
    expect(amortizedMonthly(50, 600, 1)).toBeCloseTo(100, 5);
  });

  test('No Upfront (upfront === 0): result equals monthlyCost unchanged', () => {
    expect(amortizedMonthly(80, 0, 1)).toBeCloseTo(80, 5);
    expect(amortizedMonthly(80, 0, 3)).toBeCloseTo(80, 5);
  });

  test('term <= 0: returns monthlyCost unchanged (guard against divide-by-zero)', () => {
    expect(amortizedMonthly(50, 600, 0)).toBe(50);
    expect(amortizedMonthly(50, 600, -1)).toBe(50);
  });

  test('non-finite term: returns monthlyCost unchanged', () => {
    expect(amortizedMonthly(50, 600, Infinity)).toBe(50);
    expect(amortizedMonthly(50, 600, NaN)).toBe(50);
  });

  test('null upfrontCost: returns monthlyCost unchanged', () => {
    expect(amortizedMonthly(80, null, 1)).toBe(80);
  });

  test('undefined upfrontCost: returns monthlyCost unchanged', () => {
    expect(amortizedMonthly(80, undefined, 1)).toBe(80);
  });

  test('non-finite upfrontCost: returns monthlyCost unchanged', () => {
    expect(amortizedMonthly(80, Infinity, 1)).toBe(80);
    expect(amortizedMonthly(80, NaN, 1)).toBe(80);
  });
});
