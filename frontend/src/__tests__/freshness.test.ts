/**
 * Tests for the shared freshness indicator module. Focuses on the
 * cross-cutting behaviours: render on mount, refresh-button click wires
 * through to POST /recommendations/refresh + reloads data, error banner
 * appears when last_collection_error is non-null, and — critically —
 * error text from upstream cloud providers is rendered as text, not HTML
 * (the XSS regression guard).
 */

import { renderFreshness } from '../freshness';

jest.mock('../api/recommendations', () => ({
  getRecommendationsFreshness: jest.fn(),
  refreshRecommendations: jest.fn(),
}));

// Use the real utils implementations so escapeHtml + formatDate run for
// real — the XSS assertion depends on the actual escape behaviour, not
// a mock.
import {
  getRecommendationsFreshness,
  refreshRecommendations,
} from '../api/recommendations';
import type { RefreshRecommendationsResult } from '../api/recommendations';

const mockedGet = getRecommendationsFreshness as jest.MockedFunction<typeof getRecommendationsFreshness>;
const mockedRefresh = refreshRecommendations as jest.MockedFunction<typeof refreshRecommendations>;

beforeEach(() => {
  jest.clearAllMocks();
  // Use a fresh DOM subtree for each test via createElement, avoiding
  // innerHTML on document.body.
  document.body.replaceChildren();
  const container = document.createElement('div');
  container.id = 'fresh';
  document.body.appendChild(container);
});

test('renders freshness indicator with relative time on mount', async () => {
  const fiveMinAgo = new Date(Date.now() - 5 * 60_000).toISOString();
  mockedGet.mockResolvedValue({
    last_collected_at: fiveMinAgo,
    last_collection_error: null,
  });

  await renderFreshness('fresh', jest.fn());

  const container = document.getElementById('fresh')!;
  expect(container.textContent).toContain('Data from');
  expect(container.textContent).toMatch(/\d+m ago/);
  expect(container.querySelector('button')).not.toBeNull();
});

test('renders "never" label when the cache has never been populated', async () => {
  mockedGet.mockResolvedValue({
    last_collected_at: null,
    last_collection_error: null,
  });

  await renderFreshness('fresh', jest.fn());

  const container = document.getElementById('fresh')!;
  expect(container.textContent).toContain('never');
});

test('renders warning banner with last_collection_error', async () => {
  mockedGet.mockResolvedValue({
    last_collected_at: new Date().toISOString(),
    last_collection_error: 'aws: access denied',
  });

  await renderFreshness('fresh', jest.fn());

  const banner = document.querySelector('#fresh .banner-warning');
  expect(banner).not.toBeNull();
  expect(banner!.textContent).toContain('aws: access denied');
});

test('error text from cloud providers is rendered as text, not HTML (XSS guard)', async () => {
  const xssPayload = '<script>alert("pwned")</script><img src=x onerror=alert(1)>';
  mockedGet.mockResolvedValue({
    last_collected_at: new Date().toISOString(),
    last_collection_error: xssPayload,
  });

  await renderFreshness('fresh', jest.fn());

  const banner = document.querySelector('#fresh .banner-warning')!;
  // The banner's textContent should contain the literal payload text...
  expect(banner.textContent).toContain('<script>');
  // ...but NO actual <script> or <img> child elements should have been
  // parsed into the DOM — if escaping were broken they would exist.
  expect(banner.querySelector('script')).toBeNull();
  expect(banner.querySelector('img')).toBeNull();
});

test('refresh button triggers POST /recommendations/refresh + onRefresh callback', async () => {
  mockedGet.mockResolvedValue({
    last_collected_at: new Date().toISOString(),
    last_collection_error: null,
  });
  mockedRefresh.mockResolvedValue({
    recommendations: 0,
    total_savings: 0,
    successful_providers: [],
    failed_providers: {},
  });
  const onRefresh = jest.fn().mockResolvedValue(undefined);

  await renderFreshness('fresh', onRefresh);

  const btn = document.querySelector('#fresh-refresh-btn') as HTMLButtonElement;
  expect(btn).not.toBeNull();

  btn.click();
  // click handler is async — wait for the microtask queue to drain.
  await new Promise((r) => setTimeout(r, 0));
  await new Promise((r) => setTimeout(r, 0));

  expect(mockedRefresh).toHaveBeenCalledTimes(1);
  expect(onRefresh).toHaveBeenCalledTimes(1);
});

test('refresh button renames to "Refreshing..." while in flight, then emits a success toast', async () => {
  const initial = new Date(Date.now() - 60 * 60_000).toISOString();
  const refreshed = new Date().toISOString();
  mockedGet
    .mockResolvedValueOnce({ last_collected_at: initial, last_collection_error: null })
    .mockResolvedValueOnce({ last_collected_at: refreshed, last_collection_error: null });

  // Hold the refresh API open so we can observe the in-flight state
  // before resolving.
  let resolveRefresh: ((v: RefreshRecommendationsResult) => void) | null = null;
  mockedRefresh.mockImplementation(
    () => new Promise((r) => { resolveRefresh = r; }),
  );
  const onRefresh = jest.fn().mockResolvedValue(undefined);

  await renderFreshness('fresh', onRefresh);

  const btn = document.querySelector('#fresh-refresh-btn') as HTMLButtonElement;
  btn.click();
  // let the click handler run up to the awaited refreshAPI() call
  await new Promise((r) => setTimeout(r, 0));

  expect(btn.textContent).toBe('Refreshing...');
  expect(btn.disabled).toBe(true);

  const inFlightToast = document.querySelector('#toast-container .toast-message');
  expect(inFlightToast?.textContent).toBe('Refreshing recommendations…');

  resolveRefresh!({ recommendations: 0, total_savings: 0 });
  // drain the rest of the handler: refreshAPI → onRefresh → renderFreshness → toasts
  for (let i = 0; i < 5; i++) await new Promise((r) => setTimeout(r, 0));

  const toastMessages = Array.from(
    document.querySelectorAll('#toast-container .toast-message'),
  ).map((n) => n.textContent);
  expect(toastMessages).toContain('Recommendations refreshed');

  // Bar was re-rendered with the newer timestamp.
  expect(document.querySelector('#fresh')!.textContent).toContain('Data from');
});

test('refresh button restores original text and surfaces an error toast on failure', async () => {
  mockedGet.mockResolvedValue({
    last_collected_at: new Date().toISOString(),
    last_collection_error: null,
  });
  mockedRefresh.mockRejectedValue(new Error('boom'));
  const onRefresh = jest.fn();

  await renderFreshness('fresh', onRefresh);
  const btn = document.querySelector('#fresh-refresh-btn') as HTMLButtonElement;
  // Swallow the expected console.error from the handler so the test output stays clean.
  const spy = jest.spyOn(console, 'error').mockImplementation(() => {});

  btn.click();
  for (let i = 0; i < 5; i++) await new Promise((r) => setTimeout(r, 0));

  expect(btn.textContent).toBe('Refresh');
  expect(btn.disabled).toBe(false);
  const toastMessages = Array.from(
    document.querySelectorAll('#toast-container .toast-message'),
  ).map((n) => n.textContent);
  expect(toastMessages.some((m) => m?.includes('Refresh failed'))).toBe(true);
  expect(onRefresh).not.toHaveBeenCalled();

  spy.mockRestore();
});

test('silently no-ops when the container element is missing', async () => {
  document.body.replaceChildren(); // container gone
  await expect(renderFreshness('nonexistent', jest.fn())).resolves.toBeUndefined();
  expect(mockedGet).not.toHaveBeenCalled();
});

test('clears the container when the freshness fetch fails', async () => {
  const container = document.getElementById('fresh')!;
  const stale = document.createElement('span');
  stale.textContent = 'stale-content';
  container.appendChild(stale);

  mockedGet.mockRejectedValue(new Error('network down'));

  await renderFreshness('fresh', jest.fn());

  expect(document.getElementById('fresh')!.children).toHaveLength(0);
});

// P5a: colour-coded staleness bands.
test('renders --fresh badge for data younger than 3 hours', async () => {
  const oneHourAgo = new Date(Date.now() - 60 * 60 * 1000).toISOString();
  mockedGet.mockResolvedValue({ last_collected_at: oneHourAgo, last_collection_error: null });
  await renderFreshness('fresh', jest.fn());
  const pill = document.querySelector('.freshness-badge');
  expect(pill?.classList.contains('freshness-badge--fresh')).toBe(true);
});

test('renders --warn badge for data 3-12 hours old', async () => {
  const sixHoursAgo = new Date(Date.now() - 6 * 60 * 60 * 1000).toISOString();
  mockedGet.mockResolvedValue({ last_collected_at: sixHoursAgo, last_collection_error: null });
  await renderFreshness('fresh', jest.fn());
  const pill = document.querySelector('.freshness-badge');
  expect(pill?.classList.contains('freshness-badge--warn')).toBe(true);
});

test('renders --stale badge for data older than 12 hours', async () => {
  const oneDayAgo = new Date(Date.now() - 24 * 60 * 60 * 1000).toISOString();
  mockedGet.mockResolvedValue({ last_collected_at: oneDayAgo, last_collection_error: null });
  await renderFreshness('fresh', jest.fn());
  const pill = document.querySelector('.freshness-badge');
  expect(pill?.classList.contains('freshness-badge--stale')).toBe(true);
});

test('renders --stale badge when the cache has never been populated', async () => {
  mockedGet.mockResolvedValue({ last_collected_at: null, last_collection_error: null });
  await renderFreshness('fresh', jest.fn());
  const pill = document.querySelector('.freshness-badge');
  expect(pill?.classList.contains('freshness-badge--stale')).toBe(true);
});

// Q6: richer collection-error banner. jest setup.ts installs a stateless
// jest.fn() sessionStorage mock; swap in a real Map-backed one per-test so
// we can verify the hash-dismissal round-trip.
describe('collection-error banner', () => {
  let store: Map<string, string>;
  beforeEach(() => {
    store = new Map();
    Object.defineProperty(global, 'sessionStorage', {
      configurable: true,
      value: {
        getItem: (k: string) => store.get(k) ?? null,
        setItem: (k: string, v: string) => { store.set(k, v); },
        removeItem: (k: string) => { store.delete(k); },
        clear: () => store.clear(),
        key: (i: number) => Array.from(store.keys())[i] ?? null,
        get length() { return store.size; },
      },
    });
  });

  test('renders icon, summary, expandable details, and dismiss button', async () => {
    mockedGet.mockResolvedValue({
      last_collected_at: new Date().toISOString(),
      last_collection_error: 'aws: access denied (retry)',
    });
    await renderFreshness('fresh', jest.fn());

    const banner = document.querySelector('#fresh .collection-error-banner');
    expect(banner).not.toBeNull();
    expect(banner?.querySelector('.collection-error-icon')?.textContent).toBe('\u26A0');
    expect(banner?.querySelector('.collection-error-summary')?.textContent).toContain('Last collection had errors');
    expect(banner?.querySelector('.collection-error-details summary')?.textContent).toBe('Show details');
    expect(banner?.querySelector('.collection-error-text')?.textContent).toBe('aws: access denied (retry)');
    expect(banner?.querySelector('.collection-error-dismiss')).not.toBeNull();
  });

  test('clicking × dismisses the banner and records the hash in sessionStorage', async () => {
    mockedGet.mockResolvedValue({
      last_collected_at: new Date().toISOString(),
      last_collection_error: 'aws: access denied',
    });
    await renderFreshness('fresh', jest.fn());

    const dismiss = document.querySelector<HTMLButtonElement>('.collection-error-dismiss');
    expect(dismiss).not.toBeNull();
    dismiss!.click();
    expect(document.querySelector('.collection-error-banner')).toBeNull();
    // sessionStorage key name is opaque; just assert at least one key exists.
    expect(sessionStorage.length).toBeGreaterThan(0);
  });

  test('once dismissed, the SAME error stays hidden on re-render', async () => {
    mockedGet.mockResolvedValue({
      last_collected_at: new Date().toISOString(),
      last_collection_error: 'aws: access denied',
    });
    await renderFreshness('fresh', jest.fn());
    document.querySelector<HTMLButtonElement>('.collection-error-dismiss')!.click();

    await renderFreshness('fresh', jest.fn());
    expect(document.querySelector('.collection-error-banner')).toBeNull();
  });

  test('a DIFFERENT error message re-appears after dismissal of the first', async () => {
    mockedGet.mockResolvedValue({
      last_collected_at: new Date().toISOString(),
      last_collection_error: 'aws: access denied',
    });
    await renderFreshness('fresh', jest.fn());
    document.querySelector<HTMLButtonElement>('.collection-error-dismiss')!.click();

    mockedGet.mockResolvedValue({
      last_collected_at: new Date().toISOString(),
      last_collection_error: 'azure: rate limit exceeded',
    });
    await renderFreshness('fresh', jest.fn());
    expect(document.querySelector('.collection-error-banner')).not.toBeNull();
    expect(document.querySelector('.collection-error-text')?.textContent).toBe('azure: rate limit exceeded');
  });
});
