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

/**
 * Short, stable hash for a collection-error message. Used to key the
 * sessionStorage dismissal flag so a re-render with the same error stays
 * hidden while a DIFFERENT error re-surfaces.
 */
function hashCollectionError(msg: string): string {
  // djb2 — small, non-cryptographic, deterministic across reloads.
  let h = 5381;
  for (let i = 0; i < msg.length; i++) {
    h = ((h << 5) + h + msg.charCodeAt(i)) | 0;
  }
  return (h >>> 0).toString(36);
}

function dismissalKey(errMsg: string): string {
  return `collection-error-dismissed-${hashCollectionError(errMsg)}`;
}

function buildErrorBanner(errMsg: string, absTime: string): HTMLDivElement | null {
  // Session-scoped dismiss: once the user clicks × on this particular
  // error message, suppress it until the error changes or the tab closes.
  try {
    if (sessionStorage.getItem(dismissalKey(errMsg))) return null;
  } catch {
    // sessionStorage may be unavailable (private browsing edge cases);
    // fall through and render the banner — safer than silently hiding.
  }

  const banner = document.createElement('div');
  banner.className = 'banner banner-warning collection-error-banner';
  banner.setAttribute('role', 'alert');

  // ⚠ warning glyph, aria-hidden so the banner's aria-label carries meaning.
  const icon = document.createElement('span');
  icon.className = 'collection-error-icon';
  icon.setAttribute('aria-hidden', 'true');
  icon.textContent = '\u26A0';
  banner.appendChild(icon);

  // Message body: one-line summary + an expandable details section for
  // the full error text. `<details>` is keyboard-accessible natively.
  const body = document.createElement('div');
  body.className = 'collection-error-body';

  const summaryLine = document.createElement('div');
  summaryLine.className = 'collection-error-summary';
  summaryLine.textContent = `Last collection had errors. Showing last successful snapshot from ${absTime || 'earlier'}.`;
  body.appendChild(summaryLine);

  const details = document.createElement('details');
  details.className = 'collection-error-details';
  const summary = document.createElement('summary');
  summary.textContent = 'Show details';
  details.appendChild(summary);
  const pre = document.createElement('pre');
  pre.className = 'collection-error-text';
  pre.textContent = errMsg;
  details.appendChild(pre);
  body.appendChild(details);

  banner.appendChild(body);

  const dismiss = document.createElement('button');
  dismiss.type = 'button';
  dismiss.className = 'collection-error-dismiss';
  dismiss.setAttribute('aria-label', 'Dismiss collection error');
  dismiss.textContent = '\u00D7';
  dismiss.addEventListener('click', () => {
    try {
      sessionStorage.setItem(dismissalKey(errMsg), '1');
    } catch {
      // If storage is unavailable, just hide for this render; a reload
      // will re-show, which is acceptable for the edge case.
    }
    banner.remove();
  });
  banner.appendChild(dismiss);

  return banner;
}

/**
 * Map an ISO timestamp to a staleness band used for colour-coding the
 * freshness pill. "never" (no timestamp) renders as stale so brand-new
 * tenants see the same red warning an old installation would.
 */
function freshnessBand(iso: string | null | undefined): 'fresh' | 'warn' | 'stale' {
  if (!iso) return 'stale';
  const d = new Date(iso);
  if (isNaN(d.getTime())) return 'stale';
  const ageHours = (Date.now() - d.getTime()) / 3_600_000;
  if (ageHours < 3) return 'fresh';
  if (ageHours < 12) return 'warn';
  return 'stale';
}

function buildFreshnessBar(
  relTime: string,
  absTime: string,
  containerID: string,
  band: 'fresh' | 'warn' | 'stale',
): HTMLDivElement {
  const bar = document.createElement('div');
  bar.className = 'freshness-bar';

  const pill = document.createElement('span');
  pill.className = `freshness-badge freshness-badge--${band}`;
  pill.setAttribute('title', absTime || 'No collection recorded yet');
  pill.setAttribute('aria-label', `Data freshness: ${band === 'fresh' ? 'fresh' : band === 'warn' ? 'ageing' : 'stale'}, ${relTime}`);
  const prefix = document.createTextNode('Data from ');
  const timeSpan = document.createElement('span');
  timeSpan.className = 'freshness-rel';
  timeSpan.textContent = relTime;
  pill.appendChild(prefix);
  pill.appendChild(timeSpan);

  const btn = document.createElement('button');
  btn.id = `${containerID}-refresh-btn`;
  btn.className = 'freshness-refresh';
  btn.textContent = 'Refresh';

  bar.appendChild(pill);
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
    const banner = buildErrorBanner(freshness.last_collection_error, absTime);
    if (banner) container.appendChild(banner);
  }
  const band = freshnessBand(freshness.last_collected_at);
  const bar = buildFreshnessBar(relTime, absTime, containerID, band);
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
