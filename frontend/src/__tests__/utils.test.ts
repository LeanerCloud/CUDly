/**
 * Unit tests for utility functions
 */
import {
  formatCurrency,
  formatDate,
  formatDateTime,
  getDateParts,
  debounce,
  throttle,
  escapeHtml,
  parseQueryParams,
  buildUrl,
  deepClone,
  isValidEmail,
  formatRampSchedule,
  getStatusBadge,
  calculatePaybackMonths
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

  test('handles null and undefined', () => {
    expect(formatCurrency(null as unknown as number)).toBe('$0');
    expect(formatCurrency(undefined as unknown as number)).toBe('$0');
  });

  test('handles NaN', () => {
    expect(formatCurrency(NaN)).toBe('$0');
  });

  test('supports custom currency symbol', () => {
    expect(formatCurrency(1000, '€')).toBe('€1,000');
    expect(formatCurrency(500, '£')).toBe('£500');
  });
});

describe('formatDate', () => {
  test('formats valid date string', () => {
    const date = '2024-03-15';
    const result = formatDate(date);
    expect(result).toBeTruthy();
    expect(result).toMatch(/\d{1,2}\/\d{1,2}\/\d{4}|\d{4}-\d{2}-\d{2}|March|Mar/i);
  });

  test('formats Date object', () => {
    const date = new Date('2024-03-15');
    const result = formatDate(date);
    expect(result).toBeTruthy();
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
  test('formats valid datetime', () => {
    const date = '2024-03-15T14:30:00';
    const result = formatDateTime(date);
    expect(result).toBeTruthy();
  });

  test('returns empty string for invalid input', () => {
    expect(formatDateTime(null as unknown as string)).toBe('');
    expect(formatDateTime('')).toBe('');
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
