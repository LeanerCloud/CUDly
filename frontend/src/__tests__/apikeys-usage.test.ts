/**
 * Tests for src/apikeys_usage.ts -- the API keys section summary card.
 *
 * The module had no direct coverage when it was introduced; these tests
 * cover the render path, the count formatting boundaries, the error path,
 * and the out-of-order-response guard.
 */
import { loadApiKeysUsageStats, renderApiKeysUsageSummary } from '../apikeys_usage';
import type { APIKeysUsageStats } from '../api/types';
import * as api from '../api';

jest.mock('../api');

const mockedApi = api as jest.Mocked<typeof api>;

function setupContainer(): HTMLElement {
  document.body.innerHTML = '<div id="apikeys-usage-summary"></div>';
  return document.getElementById('apikeys-usage-summary') as HTMLElement;
}

function stats(overrides: Partial<APIKeysUsageStats> = {}): APIKeysUsageStats {
  return {
    total_active: 2,
    total_requests_window: 42,
    total_requests_lifetime: 1234,
    top_keys: [],
    ...overrides,
  };
}

function tileValues(container: HTMLElement): string[] {
  return Array.from(container.querySelectorAll('.apikeys-usage-tile-value')).map(
    el => el.textContent ?? ''
  );
}

describe('renderApiKeysUsageSummary', () => {
  test('renders the three summary tiles', () => {
    const container = setupContainer();

    renderApiKeysUsageSummary(stats());

    expect(tileValues(container)).toEqual(['2', '42', '1.2k']);
  });

  // Regression: a lifetime total that excludes keys predating the counters is
  // a lower bound. Rendering it bare would present an undercount as exact.
  test('marks a partial lifetime total as a lower bound', () => {
    const container = setupContainer();

    renderApiKeysUsageSummary(stats({ lifetime_partial: true }));

    expect(tileValues(container)[2]).toBe('1.2k+');
  });

  test('leaves a complete lifetime total unqualified', () => {
    const container = setupContainer();

    renderApiKeysUsageSummary(stats({ lifetime_partial: false }));

    expect(tileValues(container)[2]).toBe('1.2k');
  });

  test('renders the top-keys list when keys have window activity', () => {
    const container = setupContainer();

    renderApiKeysUsageSummary(
      stats({
        top_keys: [
          { id: 'key-1', name: 'Busy', key_prefix: 'aaaa1111', request_count_window: 30 },
          { id: 'key-2', name: 'Medium', key_prefix: 'bbbb2222', request_count_window: 12 },
        ],
      })
    );

    const items = container.querySelectorAll('.apikeys-usage-top-list li');
    expect(items).toHaveLength(2);
    expect(items[0]?.querySelector('strong')?.textContent).toBe('Busy');
    expect(items[0]?.querySelector('code')?.textContent).toBe('aaaa1111...');
    expect(items[0]?.querySelector('.apikeys-usage-top-count')?.textContent).toBe('30 req');
  });

  test('omits the top-keys section when there is no window activity', () => {
    const container = setupContainer();

    renderApiKeysUsageSummary(stats({ top_keys: [] }));

    expect(container.querySelector('.apikeys-usage-top-list')).toBeNull();
    expect(container.querySelector('.apikeys-usage-top-heading')).toBeNull();
  });

  // The card is built with createElement/textContent rather than innerHTML,
  // so a key name containing markup must land as literal text.
  test('does not interpret markup in a key name as HTML', () => {
    const container = setupContainer();

    renderApiKeysUsageSummary(
      stats({
        top_keys: [
          {
            id: 'key-1',
            name: '<img src=x onerror=alert(1)>',
            key_prefix: '<script>',
            request_count_window: 1,
          },
        ],
      })
    );

    expect(container.querySelector('img')).toBeNull();
    expect(container.querySelector('script')).toBeNull();
    expect(container.querySelector('.apikeys-usage-top-list strong')?.textContent).toBe(
      '<img src=x onerror=alert(1)>'
    );
  });

  test('replaces previous content instead of appending on re-render', () => {
    const container = setupContainer();

    renderApiKeysUsageSummary(stats());
    renderApiKeysUsageSummary(stats());

    expect(container.querySelectorAll('.apikeys-usage-summary')).toHaveLength(1);
  });

  test.each([
    [0, '0'],
    [999, '999'],
    [1000, '1k'],
    [1500, '1.5k'],
    [10_000, '10k'],
    // Rounds up into the next unit rather than emitting "1000k".
    [999_999, '1.0M'],
    [1_500_000, '1.5M'],
    [10_000_000, '10M'],
    // Garbage in must not render "NaN" to the user.
    [Number.NaN, '0'],
    [-5, '0'],
  ])('formats a window total of %p as %p', (value, expected) => {
    const container = setupContainer();

    renderApiKeysUsageSummary(stats({ total_requests_window: value }));

    expect(tileValues(container)[1]).toBe(expected);
  });
});

describe('loadApiKeysUsageStats', () => {
  beforeEach(() => {
    jest.clearAllMocks();
  });

  test('renders the fetched summary', async () => {
    const container = setupContainer();
    mockedApi.getApiKeysUsageStats.mockResolvedValue(stats({ total_active: 7 }));

    await loadApiKeysUsageStats();

    expect(tileValues(container)[0]).toBe('7');
  });

  test('shows an inline error without throwing when the fetch fails', async () => {
    const consoleError = jest.spyOn(console, 'error').mockImplementation(() => undefined);
    const container = setupContainer();
    mockedApi.getApiKeysUsageStats.mockRejectedValue(new Error('boom'));

    await expect(loadApiKeysUsageStats()).resolves.toBeUndefined();

    expect(container.querySelector('p.error')?.textContent).toBe('Failed to load usage summary');
    consoleError.mockRestore();
  });

  // Regression: refreshes can resolve out of order. A slow earlier request
  // must not overwrite the result of a later one.
  test('ignores a stale response that resolves after a newer one', async () => {
    const container = setupContainer();

    let resolveFirst: (value: APIKeysUsageStats) => void = () => undefined;
    const first = new Promise<APIKeysUsageStats>(resolve => {
      resolveFirst = resolve;
    });
    mockedApi.getApiKeysUsageStats.mockReturnValueOnce(first);
    mockedApi.getApiKeysUsageStats.mockResolvedValueOnce(stats({ total_active: 99 }));

    const stalePending = loadApiKeysUsageStats();
    await loadApiKeysUsageStats();
    expect(tileValues(container)[0]).toBe('99');

    resolveFirst(stats({ total_active: 1 }));
    await stalePending;

    expect(tileValues(container)[0]).toBe('99');
  });

  // Same ordering guard on the failure path: a stale rejection must not
  // paint an error over a summary that loaded successfully.
  test('ignores a stale rejection that lands after a newer success', async () => {
    const consoleError = jest.spyOn(console, 'error').mockImplementation(() => undefined);
    const container = setupContainer();

    let rejectFirst: (reason: Error) => void = () => undefined;
    const first = new Promise<APIKeysUsageStats>((_, reject) => {
      rejectFirst = reject;
    });
    mockedApi.getApiKeysUsageStats.mockReturnValueOnce(first);
    mockedApi.getApiKeysUsageStats.mockResolvedValueOnce(stats({ total_active: 5 }));

    const stalePending = loadApiKeysUsageStats();
    await loadApiKeysUsageStats();

    rejectFirst(new Error('slow failure'));
    await stalePending;

    expect(container.querySelector('p.error')).toBeNull();
    expect(tileValues(container)[0]).toBe('5');
    consoleError.mockRestore();
  });

  test('is a no-op when the summary container is absent', async () => {
    document.body.innerHTML = '';

    await expect(loadApiKeysUsageStats()).resolves.toBeUndefined();
    expect(mockedApi.getApiKeysUsageStats).not.toHaveBeenCalled();
  });
});
