/**
 * Freshness indicator shared between the Dashboard and Recommendations
 * pages. Both pages display the same "Data from <relative-time>" +
 * Refresh button + (optional) last-collection-error banner, driven by
 * GET /api/recommendations/freshness.
 *
 * Rendering uses textContent and DOM-construction APIs (not innerHTML)
 * so upstream strings — including last_collection_error, which comes
 * from cloud-provider error messages — can never inject HTML/JS.
 */

import {
  getRecommendationsFreshness,
  refreshRecommendations as refreshAPI,
} from './api/recommendations';
import type { RecommendationsFreshness } from './api/recommendations';
import { formatDate, formatRelativeTime } from './utils';

function buildErrorBanner(errMsg: string, absTime: string): HTMLDivElement {
  const banner = document.createElement('div');
  banner.className = 'banner banner-warning';
  banner.setAttribute('role', 'alert');
  banner.textContent = `Last collection had errors: ${errMsg}. Showing last successful snapshot from ${absTime || 'earlier'}.`;
  return banner;
}

function buildFreshnessBar(
  relTime: string,
  absTime: string,
  containerID: string,
): HTMLDivElement {
  const bar = document.createElement('div');
  bar.className = 'freshness-bar';

  const label = document.createElement('span');
  label.className = 'freshness-label';
  const prefix = document.createTextNode('Data from ');
  const timeSpan = document.createElement('span');
  timeSpan.title = absTime;
  timeSpan.textContent = relTime;
  label.appendChild(prefix);
  label.appendChild(timeSpan);

  const btn = document.createElement('button');
  btn.id = `${containerID}-refresh-btn`;
  btn.className = 'freshness-refresh';
  btn.textContent = 'Refresh';

  bar.appendChild(label);
  bar.appendChild(btn);
  return bar;
}

/**
 * Populate the freshness indicator in the given container.
 *
 * On refresh-button click: call POST /api/recommendations/refresh, then
 * invoke onRefresh so the caller can reload the data, and re-render the
 * indicator with the updated timestamp.
 */
export async function renderFreshness(
  containerID: string,
  onRefresh: () => void | Promise<void>,
): Promise<void> {
  const container = document.getElementById(containerID);
  if (!container) return;

  let freshness: RecommendationsFreshness;
  try {
    freshness = await getRecommendationsFreshness();
  } catch (err) {
    console.error('Failed to load recommendations freshness:', err);
    container.replaceChildren();
    return;
  }

  const relTime = freshness.last_collected_at
    ? formatRelativeTime(freshness.last_collected_at)
    : 'never';
  const absTime = freshness.last_collected_at ? formatDate(freshness.last_collected_at) : '';

  container.replaceChildren();
  if (freshness.last_collection_error) {
    container.appendChild(buildErrorBanner(freshness.last_collection_error, absTime));
  }
  const bar = buildFreshnessBar(relTime, absTime, containerID);
  container.appendChild(bar);

  const btn = document.getElementById(`${containerID}-refresh-btn`);
  btn?.addEventListener('click', () => {
    void (async () => {
      btn.setAttribute('disabled', 'true');
      try {
        await refreshAPI();
        await onRefresh();
        await renderFreshness(containerID, onRefresh);
      } catch (err) {
        console.error('Refresh failed:', err);
        btn.removeAttribute('disabled');
      }
    })();
  });
}
