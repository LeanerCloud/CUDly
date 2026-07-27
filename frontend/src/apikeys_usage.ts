/**
 * API Keys usage-stats summary module for CUDly.
 *
 * Split out of apikeys.ts (CodeRabbit finding on PR #1523 -- apikeys.ts
 * exceeded the 500-line limit once the usage-summary rendering was added).
 * This module owns the section-level summary card above the API keys
 * table: loading, rendering, and formatting for total-active / requests
 * (window + lifetime) / top-3-most-active.
 */

import * as api from './api';
import type { APIKeysUsageStats } from './api/types';
import { showSkeletonBlock, teardownSkeleton } from './lib/skeleton';

/**
 * Load and render the section-level usage summary (totals + top keys).
 * Fails closed: on error the summary slot shows an inline message and
 * the rest of the section still works.
 */
export async function loadApiKeysUsageStats(): Promise<void> {
  const container = document.getElementById('apikeys-usage-summary');
  if (!container) return;
  showSkeletonBlock(container, '100%', '4rem');
  try {
    const stats = await api.getApiKeysUsageStats();
    renderApiKeysUsageSummary(stats);
  } catch (error) {
    console.error('Failed to load API keys usage stats:', error);
    teardownSkeleton(container);
    const p = document.createElement('p');
    p.className = 'error';
    p.textContent = 'Failed to load usage summary';
    container.appendChild(p);
  }
}

/**
 * Render the section-level summary card: total active keys, total
 * requests (current window + lifetime), and a top-3 most-active row.
 * Built with createElement only (no innerHTML) to match the codebase
 * XSS posture.
 *
 * "Requests (window)" reflects request_count_window, a FIXED/TUMBLING
 * window count (not a true rolling 24h total) -- see
 * APIKeysUsageStats.total_requests_window.
 */
export function renderApiKeysUsageSummary(stats: APIKeysUsageStats): void {
  const container = document.getElementById('apikeys-usage-summary');
  if (!container) return;
  container.replaceChildren();
  delete container.dataset['skeletonActive'];

  const card = document.createElement('div');
  card.className = 'apikeys-usage-summary card';

  const tiles = document.createElement('div');
  tiles.className = 'apikeys-usage-tiles';
  tiles.appendChild(buildSummaryTile('Active keys', String(stats.total_active)));
  tiles.appendChild(buildSummaryTile('Requests (window)', formatCount(stats.total_requests_window)));
  tiles.appendChild(buildSummaryTile('Requests (lifetime)', formatCount(stats.total_requests_lifetime)));
  card.appendChild(tiles);

  if (stats.top_keys && stats.top_keys.length > 0) {
    const heading = document.createElement('h5');
    heading.className = 'apikeys-usage-top-heading';
    heading.textContent = 'Most active (window)';
    card.appendChild(heading);

    const list = document.createElement('ul');
    list.className = 'apikeys-usage-top-list';
    for (const top of stats.top_keys) {
      const li = document.createElement('li');
      const name = document.createElement('strong');
      name.textContent = top.name;
      const code = document.createElement('code');
      code.textContent = `${top.key_prefix}...`;
      const count = document.createElement('span');
      count.className = 'apikeys-usage-top-count';
      count.textContent = `${formatCount(top.request_count_window)} req`;
      li.appendChild(name);
      li.appendChild(document.createTextNode(' '));
      li.appendChild(code);
      li.appendChild(document.createTextNode(' — '));
      li.appendChild(count);
      list.appendChild(li);
    }
    card.appendChild(list);
  }

  container.appendChild(card);
}

function buildSummaryTile(label: string, value: string): HTMLElement {
  const tile = document.createElement('div');
  tile.className = 'apikeys-usage-tile';
  const labelEl = document.createElement('div');
  labelEl.className = 'apikeys-usage-tile-label';
  labelEl.textContent = label;
  const valueEl = document.createElement('div');
  valueEl.className = 'apikeys-usage-tile-value';
  valueEl.textContent = value;
  tile.appendChild(labelEl);
  tile.appendChild(valueEl);
  return tile;
}

/**
 * Format a request count for display in the summary tiles. Uses the
 * standard "k" / "M" abbreviation for large values so the tile doesn't
 * have to fit "1,234,567" -- the per-row table cells still show the
 * exact number via apikeys.ts's formatRequestCount.
 */
function formatCount(n: number): string {
  if (!Number.isFinite(n) || n < 0) return '0';
  if (n < 1000) return String(Math.trunc(n));
  if (n < 1_000_000) {
    // Round to the chosen precision FIRST so we can detect the
    // 999,500..999,999 band that rounds up to 1000k and promote
    // it to the M branch instead of emitting "1000k".
    const k = Number((n / 1000).toFixed(n < 10_000 ? 1 : 0));
    if (k < 1000) return `${k}k`;
  }
  return `${(n / 1_000_000).toFixed(n < 10_000_000 ? 1 : 0)}M`;
}
