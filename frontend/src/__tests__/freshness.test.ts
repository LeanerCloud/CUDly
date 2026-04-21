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
  mockedRefresh.mockResolvedValue({ message: 'ok' });
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
