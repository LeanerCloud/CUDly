/**
 * History module for CUDly
 */

import * as api from './api';
import { formatCurrency, formatDate, formatTerm, escapeHtml, populateAccountFilter } from './utils';
import type { HistoryResponse, HistorySummary, HistoryPurchase } from './types';
import { switchTab } from './navigation';
import { confirmDialog } from './confirmDialog';
import { showToast } from './toast';
import { getCurrentUser } from './state';

const VALID_PROVIDERS: api.Provider[] = ['aws', 'azure', 'gcp'];

type StatusFilter = 'all' | 'pending' | 'completed' | 'failed' | 'expired' | 'cancelled';

// Cache of the last-rendered purchase list so the status-chip click handler
// can re-render without re-fetching. Cleared on each loadHistory / viewPlanHistory.
let lastPurchases: HistoryPurchase[] = [];
let activeStatusFilter: StatusFilter = 'all';

function normalizeStatus(p: HistoryPurchase): string {
  // Absent status → legacy DB row → counts as completed for filtering.
  return p.status || 'completed';
}

// readDeepLinkExecutionID returns the value of the ?execution=<id>
// query parameter inside the current location hash, or '' when
// absent. The app uses hash routing ("#history"), so the "query" piece
// sits inside `window.location.hash` — `window.location.search` is
// empty. Parse it manually:
//   #history?execution=abc123 → 'abc123'
// Exported for unit-test coverage.
export function readDeepLinkExecutionID(): string {
  const hash = window.location.hash || '';
  const q = hash.split('?')[1];
  if (!q) return '';
  return new URLSearchParams(q).get('execution') || '';
}

// applyExecutionDeepLink scrolls the history table to the row matching
// the ?execution=<id> hash query, if any, and flashes a highlight
// class on it. Called after each loadHistory render so the link from
// the Recommendations badge lands on the right row. Returns true when
// a match was found + highlighted.
function applyExecutionDeepLink(): boolean {
  const execID = readDeepLinkExecutionID();
  if (!execID) return false;
  const row = document.querySelector<HTMLTableRowElement>(
    `tr[data-execution-id="${CSS.escape(execID)}"]`,
  );
  if (!row) return false;
  row.classList.add('history-row-highlight');
  row.scrollIntoView({ behavior: 'smooth', block: 'center' });
  // Fade the highlight after a few seconds so the row goes back to
  // normal styling — otherwise it stays yellow forever and a future
  // user click on a different row looks inconsistent.
  window.setTimeout(() => row.classList.remove('history-row-highlight'), 4000);
  return true;
}

function populateHistoryAccountFilter(provider?: string): Promise<void> {
  return populateAccountFilter('history-account-filter', api.listAccounts, provider);
}

/**
 * Setup history filter event handlers
 */
export function setupHistoryHandlers(): void {
  const providerFilter = document.getElementById('history-provider-filter') as HTMLSelectElement | null;
  if (providerFilter) {
    providerFilter.addEventListener('change', () => {
      void populateHistoryAccountFilter(providerFilter.value);
    });
  }
  void populateHistoryAccountFilter();
}

/**
 * Initialize history date range.
 *
 * Defaults to a 7-day window because the Purchase events table is a *log*
 * view — recent activity is what matters; older days are mostly empty and
 * mostly noise. The Savings History card on the same page covers the
 * complementary multi-month *trend* view (default 90 days, see
 * `#savings-period` in index.html), so the two controls open to their
 * own natural windows rather than fighting over one default.
 */
export function initHistoryDateRange(): void {
  const end = new Date();
  const start = new Date();
  start.setDate(start.getDate() - 7);

  const startInput = document.getElementById('history-start') as HTMLInputElement | null;
  const endInput = document.getElementById('history-end') as HTMLInputElement | null;

  if (startInput && !startInput.value) {
    startInput.value = start.toISOString().split('T')[0] || '';
  }
  if (endInput && !endInput.value) {
    endInput.value = end.toISOString().split('T')[0] || '';
  }
}

/**
 * View plan history.
 *
 * The plan-history endpoint returns the plan's *full* history regardless
 * of date range — `api.getHistory({ planId })` ignores `start`/`end`.
 * Don't seed the From/To inputs with the generic 7-day default here:
 * they would visibly disagree with the table contents (the tab-level
 * default would suggest the table only covers the last week, while in
 * fact every purchase the plan ever recorded is shown). Instead, after
 * the fetch lands, snap the inputs to the actual min/max purchase
 * timestamps so the date pickers reflect what the user is looking at.
 *
 * If the plan has no purchases yet, leave the inputs untouched — there's
 * no meaningful range to display, and clobbering them with `today`
 * would be misleading.
 */
export async function viewPlanHistory(planId: string): Promise<void> {
  switchTab('history');

  try {
    const data = await api.getHistory({ planId }) as unknown as HistoryResponse;
    renderHistorySummary(data.summary || {});
    const purchases = data.purchases || [];
    renderHistoryList(purchases);
    snapDateInputsToPurchases(purchases);
  } catch (error) {
    console.error('Failed to load plan history:', error);
    const list = document.getElementById('history-list');
    if (list) {
      const err = error as Error;
      list.innerHTML = `<p class="error">Failed to load plan history: ${escapeHtml(err.message)}</p>`;
    }
  }
}

/**
 * Set the From/To inputs to bracket the purchases that just rendered.
 * No-op when the list is empty — keeping the previous values is more
 * honest than seeding a fake "today" range. Timestamps are normalised
 * to UTC YYYY-MM-DD so they slot directly into `<input type="date">`.
 */
function snapDateInputsToPurchases(purchases: HistoryPurchase[]): void {
  if (purchases.length === 0) return;
  const epochs = purchases
    .map(p => Date.parse(p.timestamp))
    .filter(n => !Number.isNaN(n));
  if (epochs.length === 0) return;
  const startInput = document.getElementById('history-start') as HTMLInputElement | null;
  const endInput = document.getElementById('history-end') as HTMLInputElement | null;
  const minDate = new Date(Math.min(...epochs)).toISOString().split('T')[0] || '';
  const maxDate = new Date(Math.max(...epochs)).toISOString().split('T')[0] || '';
  if (startInput) startInput.value = minDate;
  if (endInput) endInput.value = maxDate;
}

/**
 * Load history with filters
 */
export async function loadHistory(): Promise<void> {
  try {
    const rawProvider = (document.getElementById('history-provider-filter') as HTMLSelectElement | null)?.value || '';
    const provider: api.Provider | undefined = (VALID_PROVIDERS as string[]).includes(rawProvider)
      ? (rawProvider as api.Provider)
      : undefined;

    const rawAccountId = (document.getElementById('history-account-filter') as HTMLSelectElement | null)?.value || '';
    const accountIDs: string[] | undefined = rawAccountId ? [rawAccountId] : undefined;

    const filters: api.HistoryFilters = {
      start: (document.getElementById('history-start') as HTMLInputElement | null)?.value,
      end: (document.getElementById('history-end') as HTMLInputElement | null)?.value,
      provider,
      account_ids: accountIDs
    };
    const data = await api.getHistory(filters) as unknown as HistoryResponse;
    renderHistorySummary(data.summary || {});
    renderHistoryList(data.purchases || []);
  } catch (error) {
    console.error('Failed to load history:', error);
    const list = document.getElementById('history-list');
    if (list) {
      const err = error as Error;
      list.innerHTML = `<p class="error">Failed to load history: ${escapeHtml(err.message)}</p>`;
    }
  }
}

function renderHistorySummary(summary: HistorySummary): void {
  const container = document.getElementById('history-summary');
  if (!container) return;

  // total_completed / total_pending fall back to total_purchases so the
  // summary renders sensibly against an older API deploy that hasn't shipped
  // the new counters yet.
  const total = summary.total_purchases ?? 0;
  const completed = summary.total_completed ?? total;
  const pending = summary.total_pending ?? 0;
  const detail = pending > 0 ? `<p class="detail">${completed} completed · ${pending} pending</p>` : '';

  container.innerHTML = `
    <div class="card">
      <h3>Total Purchases</h3>
      <p class="value">${total}</p>
      ${detail}
    </div>
    <div class="card">
      <h3>Total Upfront Spent</h3>
      <p class="value">${formatCurrency(summary.total_upfront)}</p>
    </div>
    <div class="card">
      <h3>Monthly Savings</h3>
      <p class="value savings">${formatCurrency(summary.total_monthly_savings)}</p>
    </div>
    <div class="card">
      <h3>Annual Savings</h3>
      <p class="value savings">${formatCurrency(summary.total_annual_savings)}</p>
    </div>
  `;
}

function statusBadgeHTML(status: string): string {
  const normalized = (status || 'completed').toLowerCase();
  switch (normalized) {
    case 'pending':
    case 'notified':
      return '<span class="badge badge-warning">Pending</span>';
    case 'cancelled':
      return '<span class="badge badge-muted">Cancelled</span>';
    case 'failed':
      return '<span class="badge badge-danger">Failed</span>';
    case 'expired':
      return '<span class="badge badge-muted">Expired</span>';
    default:
      return '<span class="badge badge-success">Completed</span>';
  }
}

function buildStatusChipRowHTML(purchases: HistoryPurchase[], active: StatusFilter): string {
  const counts: Record<StatusFilter, number> = {
    all: purchases.length,
    pending: 0,
    completed: 0,
    failed: 0,
    expired: 0,
    cancelled: 0,
  };
  for (const p of purchases) {
    const s = normalizeStatus(p).toLowerCase();
    if (s === 'pending' || s === 'notified') counts.pending++;
    else if (s === 'cancelled') counts.cancelled++;
    else if (s === 'failed') counts.failed++;
    else if (s === 'expired') counts.expired++;
    else counts.completed++;
  }
  // Only render Failed / Expired / Cancelled chips when there's something in
  // them — keeps the row uncluttered on healthy deployments that have never
  // seen one. All / Pending / Completed always render so the user has a
  // consistent filter affordance even on an empty or fresh dataset.
  const allChips: Array<{ key: StatusFilter; label: string }> = [
    { key: 'all', label: 'All' },
    { key: 'pending', label: 'Pending' },
    { key: 'completed', label: 'Completed' },
    { key: 'failed', label: 'Failed' },
    { key: 'expired', label: 'Expired' },
    { key: 'cancelled', label: 'Cancelled' },
  ];
  const chips = allChips.filter(c => c.key === 'all' || c.key === 'pending' || c.key === 'completed' || counts[c.key] > 0);
  return `
    <div class="status-chip-row" role="tablist" aria-label="Filter history by status">
      ${chips.map(c => `
        <button type="button"
          class="status-chip${active === c.key ? ' active' : ''}"
          data-history-status="${c.key}"
          role="tab"
          aria-selected="${active === c.key}"
        >${c.label} (${counts[c.key]})</button>
      `).join('')}
    </div>
  `;
}

function providerCell(p: HistoryPurchase): string {
  if (!p.provider || p.provider === 'multiple') return '<span class="provider-badge">Multiple</span>';
  return `<span class="provider-badge ${p.provider}">${p.provider.toUpperCase()}</span>`;
}

// canCancelPendingRow returns true when the current session is permitted
// to cancel the given pending/notified history row via the session-authed
// Cancel button (issue #46). UX gate only — the backend
// authorizeSessionCancel in internal/api/handler_purchases.go remains the
// security boundary; if this helper is wrong-positive the API surfaces
// 403 and the click handler turns that into a "Failed to cancel" toast.
//
// Heuristic:
//   * admin → always yes;
//   * non-admin matching the row's created_by_user_id → yes (cancel-own);
//   * anyone else → no.
//
// Caveat: a non-admin role explicitly granted cancel-any:purchases (no
// such role exists by default; the verb is reserved for future operator
// roles) WILL be allowed by the backend but hidden by this helper. We
// don't surface that case because the frontend doesn't currently fetch
// the user's permission list, and adding a /me/permissions round-trip
// just to enable a button for a role nobody has is wasteful. If/when an
// operator role lands, extend User to carry permissions and broaden this
// check accordingly.
function canCancelPendingRow(p: HistoryPurchase): boolean {
  const status = (p.status || '').toLowerCase();
  if (status !== 'pending' && status !== 'notified') return false;
  const user = getCurrentUser();
  if (!user) return false;
  if (user.role === 'admin') return true;
  // Non-admin: only the original creator. Legacy rows with no
  // created_by_user_id can't be cancelled via this UI; the email-token
  // path remains the escape hatch.
  if (!p.created_by_user_id) return false;
  return p.created_by_user_id === user.id;
}

// canRetryFailedRow returns true when the current session is permitted
// to retry the given failed history row via the inline Retry button
// (issue #47). UX gate only — the backend authorizeSessionRetry in
// internal/api/handler_purchases.go remains the security boundary.
//
// Heuristic mirrors canCancelPendingRow:
//   * status must be "failed";
//   * row must NOT carry an ops_hint (persistent failure → no retry,
//     show the hint instead);
//   * row must NOT already have a retry_execution_id (we don't allow
//     retrying the same failure twice — the user should retry the
//     latest descendant in the chain);
//   * admin → always yes;
//   * non-admin matching the row's created_by_user_id → yes (retry-own).
function canRetryFailedRow(p: HistoryPurchase): boolean {
  const status = (p.status || '').toLowerCase();
  if (status !== 'failed') return false;
  if (p.ops_hint) return false;
  if (p.retry_execution_id) return false; // already retried — user should act on the descendant
  const user = getCurrentUser();
  if (!user) return false;
  if (user.role === 'admin') return true;
  if (!p.created_by_user_id) return false;
  return p.created_by_user_id === user.id;
}

// retryThresholdReached returns true when the row has hit the soft-
// block threshold (5 attempts). The frontend shows a confirm-with-
// warning dialog and forwards force=true on confirmation.
//
// Kept in sync with retryThreshold in internal/api/handler_purchases.go;
// the backend remains authoritative — if this client predicate disagrees
// with the server, the API surfaces a structured 409 with retry_attempt_n
// + threshold and the toast falls back to the server message.
const RETRY_THRESHOLD = 5;
function retryThresholdReached(p: HistoryPurchase): boolean {
  return (p.retry_attempt_n ?? 0) >= RETRY_THRESHOLD;
}

// shortExecID renders the first 8 chars of a UUID so inline lineage
// links ("Retried as #abc12345") stay readable in the table cell. The
// full ID is preserved in the data-history-status attribute so the
// click handler can deep-link without truncation surprises.
function shortExecID(id: string): string {
  return (id || '').replace(/^urn:.*?:/, '').slice(0, 8);
}

// renderActionCell returns the HTML for the Plan / action column on a
// single history row. The column doubles as the per-row action surface
// because pending / failed rows rarely have a meaningful plan_name (the
// pending plan info already shows in StatusDescription) and reusing the
// existing column keeps table width unchanged.
//
// Decision tree (status-driven, mutually exclusive):
//   * pending|notified + canCancel  → Cancel button (issue #46)
//   * failed + ops_hint set         → ⚠ ops-hint badge (issue #47, Q3 — no retry)
//   * failed + threshold reached    → "Retried 5× — confirm to override" Retry button (Q2)
//   * failed + canRetry             → standard ↻ Retry button
//   * any row with retry lineage    → inline ↻ Retried as / ↻ Retry of link
//   * else                          → plan_name or "-"
function renderActionCell(p: HistoryPurchase): string {
  // Pending → Cancel takes precedence; we never show Retry on a non-
  // failed row.
  if (canCancelPendingRow(p) && p.purchase_id) {
    return `<button type="button" class="btn-link history-cancel-btn" data-cancel-id="${escapeHtml(p.purchase_id)}">Cancel</button>`;
  }

  // Failed → either ops-hint (no retry possible), Retry (with optional
  // threshold-confirm flag), plus lineage link to the successor if it
  // exists. We never show ops-hint AND Retry on the same row — the
  // hint replaces the action entirely because retrying a persistent
  // misconfig is guaranteed to fail again.
  if ((p.status || '').toLowerCase() === 'failed') {
    if (p.ops_hint) {
      return `<span class="history-ops-hint" title="Operator action required — Retry will not help until this is fixed">⚠ ${escapeHtml(p.ops_hint)}</span>`;
    }
    if (canRetryFailedRow(p) && p.purchase_id) {
      const overThreshold = retryThresholdReached(p);
      const label = overThreshold ? `⚠ Retried ${p.retry_attempt_n ?? 0}× — click to override` : '↻ Retry';
      const cls = overThreshold ? 'btn-link history-retry-btn history-retry-over-threshold' : 'btn-link history-retry-btn';
      return `<button type="button" class="${cls}" data-retry-id="${escapeHtml(p.purchase_id)}">${label}</button>`;
    }
  }

  // Lineage links: a row that was retried (retry_execution_id set) or
  // is itself a retry (retry_attempt_n > 0) gets an inline cross-link
  // to the other end of the chain so the user can navigate without
  // scrolling. Both can be true simultaneously on a middle-of-chain
  // row (failed_v2 was retried into v3 AND is itself a retry of v1).
  const lineage: string[] = [];
  if (p.retry_execution_id) {
    lineage.push(`<a href="#history?execution=${encodeURIComponent(p.retry_execution_id)}" class="history-retry-link" title="Jump to the retry execution">↻ Retried as #${escapeHtml(shortExecID(p.retry_execution_id))}</a>`);
  }
  if ((p.retry_attempt_n ?? 0) > 0) {
    // We don't carry a back-pointer field — a future enhancement
    // could surface the predecessor's exec ID via the API. For now
    // we render a static badge (no link target) so users at least
    // see "this is a retry" provenance.
    lineage.push(`<span class="history-retry-link history-retry-of" title="This row is retry #${p.retry_attempt_n} in its chain">↻ Retry #${p.retry_attempt_n}</span>`);
  }
  if (lineage.length > 0) {
    return lineage.join(' ');
  }

  return escapeHtml(p.plan_name || '-');
}

function renderHistoryList(purchases: HistoryPurchase[]): void {
  const container = document.getElementById('history-list');
  if (!container) return;

  lastPurchases = purchases;

  // Reset the filter when the dataset changes so the user isn't stuck on an
  // empty "Cancelled" slice after reloading with a fresh query.
  if (activeStatusFilter !== 'all' && !purchases.some(p => {
    const s = normalizeStatus(p).toLowerCase();
    if (activeStatusFilter === 'pending') return s === 'pending' || s === 'notified';
    if (activeStatusFilter === 'completed') return s === 'completed' || !p.status;
    return s === activeStatusFilter;
  })) {
    activeStatusFilter = 'all';
  }

  if (!purchases || purchases.length === 0) {
    container.innerHTML = '<p class="empty">No purchase history found for the selected period.</p>';
    return;
  }

  const visible = purchases.filter(p => {
    if (activeStatusFilter === 'all') return true;
    const s = normalizeStatus(p).toLowerCase();
    if (activeStatusFilter === 'pending') return s === 'pending' || s === 'notified';
    if (activeStatusFilter === 'completed') return s === 'completed' || !p.status;
    return s === activeStatusFilter;
  });

  const tableRows = visible.map(p => {
    const statusCell = (() => {
      const badge = statusBadgeHTML(normalizeStatus(p));
      const s = normalizeStatus(p).toLowerCase();
      if ((s === 'pending' || s === 'notified') && p.approver) {
        return `${badge}<div class="history-approver">awaiting approval from <strong>${escapeHtml(p.approver)}</strong></div>`;
      }
      if (p.status_description) {
        return `${badge}<div class="history-approver">${escapeHtml(p.status_description)}</div>`;
      }
      return badge;
    })();
    const execIdAttr = p.purchase_id ? ` data-execution-id="${escapeHtml(p.purchase_id)}"` : '';
    const planCellContent = renderActionCell(p);
    return `
      <tr${execIdAttr}>
        <td>${statusCell}</td>
        <td>${formatDate(p.timestamp)}</td>
        <td>${providerCell(p)}</td>
        <td>${escapeHtml(p.service)}</td>
        <td>${escapeHtml(p.resource_type)}</td>
        <td>${escapeHtml(p.region)}</td>
        <td>${p.count}</td>
        <td>${formatTerm(p.term)}</td>
        <td>${formatCurrency(p.upfront_cost)}</td>
        <td class="savings">${formatCurrency(p.estimated_savings)}</td>
        <td>${planCellContent}</td>
      </tr>
    `;
  }).join('');

  const markup = `
    ${buildStatusChipRowHTML(purchases, activeStatusFilter)}
    <table>
      <thead>
        <tr>
          <th>Status</th>
          <th>Date</th>
          <th>Provider</th>
          <th>Service</th>
          <th>Type</th>
          <th>Region</th>
          <th>Count</th>
          <th>Term</th>
          <th>Upfront Cost</th>
          <th>Monthly Savings</th>
          <th>Plan</th>
        </tr>
      </thead>
      <tbody>
        ${tableRows}
      </tbody>
    </table>
  `;
  container.innerHTML = markup;

  container.querySelectorAll<HTMLButtonElement>('.status-chip[data-history-status]').forEach(btn => {
    btn.addEventListener('click', () => {
      const next = btn.dataset['historyStatus'] as StatusFilter | undefined;
      if (!next || next === activeStatusFilter) return;
      activeStatusFilter = next;
      renderHistoryList(lastPurchases);
    });
  });

  // Wire the inline Cancel button on pending/notified rows the current
  // session may cancel (issue #46). confirmDialog → POST → reload. The
  // backend remains the security boundary; the canCancelPendingRow
  // helper above is a UX gate that hides the button when the call would
  // 403, but a stale cache could still surface a 403 — handle it the
  // same way as any other failure.
  //
  // The cancel POST and the follow-up reload are split into separate
  // try/catch blocks: a successful cancel + failed reload must not show
  // a "Failed to cancel" toast (the purchase IS cancelled), and the
  // user should see success-toast first so they don't think their
  // click was lost while we re-fetch the table.
  container.querySelectorAll<HTMLButtonElement>('.history-cancel-btn[data-cancel-id]').forEach(btn => {
    btn.addEventListener('click', async () => {
      const id = btn.dataset['cancelId'];
      if (!id) return;
      const ok = await confirmDialog({
        title: 'Cancel this pending purchase?',
        body: 'This will permanently abort the approval flow. The pending email approval link will stop working. This action cannot be undone.',
        confirmLabel: 'Cancel purchase',
        destructive: true,
      });
      if (!ok) return;
      btn.disabled = true;
      try {
        await api.cancelPurchase(id);
      } catch (cancelError) {
        console.error('Failed to cancel pending purchase:', cancelError);
        const err = cancelError as Error;
        showToast({ message: `Failed to cancel: ${err.message || 'unknown error'}`, kind: 'error' });
        btn.disabled = false;
        return;
      }
      // Cancel succeeded — surface success regardless of whether the
      // refresh works. A reload failure leaves the row in its previous
      // pending state on screen (stale-but-correct: the next manual
      // reload corrects it).
      showToast({ message: 'Purchase cancelled', kind: 'success', timeout: 5_000 });
      try {
        await loadHistory();
      } catch (reloadError) {
        console.error('Failed to reload history after cancel:', reloadError);
        // Don't downgrade the success toast; loadHistory's own catch
        // already paints an error message into the list area.
      }
    });
  });

  // Wire the inline Retry button on failed rows the current session
  // may retry (issue #47). Two flows:
  //   * normal:    confirmDialog → POST /retry → reload + toast.
  //   * over-threshold: confirmDialog with stronger warning → POST
  //     with ?force=true → reload + toast.
  // The backend may still 409 with an ops_hint or threshold response;
  // the catch block surfaces the structured detail when present.
  container.querySelectorAll<HTMLButtonElement>('.history-retry-btn[data-retry-id]').forEach(btn => {
    btn.addEventListener('click', async () => {
      const id = btn.dataset['retryId'];
      if (!id) return;
      const overThreshold = btn.classList.contains('history-retry-over-threshold');
      const ok = await confirmDialog({
        title: overThreshold ? 'Retry past threshold?' : 'Retry this failed purchase?',
        body: overThreshold
          ? 'The same recommendations have already failed multiple times. Are you sure you want to retry again? This may not succeed.'
          : 'This will create a new purchase execution from the same recommendations. The original failed row will be linked to the new attempt.',
        confirmLabel: overThreshold ? 'Retry anyway' : 'Retry purchase',
        destructive: false,
      });
      if (!ok) return;
      btn.disabled = true;
      try {
        await api.retryPurchase(id, overThreshold ? { force: true } : undefined);
      } catch (retryError) {
        console.error('Failed to retry purchase:', retryError);
        // Surface structured retry hints from the backend (issue #47):
        //   * ops_hint — operator-actionable reason; takes priority
        //   * retry_attempt_n + threshold — soft-block message
        //   * else — fall back to the raw error message
        const err = retryError as Error & { details?: Record<string, unknown> };
        const opsHint = typeof err.details?.['ops_hint'] === 'string' ? err.details['ops_hint'] : '';
        const retryAttemptN = typeof err.details?.['retry_attempt_n'] === 'number' ? err.details['retry_attempt_n'] : undefined;
        const threshold = typeof err.details?.['threshold'] === 'number' ? err.details['threshold'] : undefined;
        let detailMessage = '';
        if (opsHint) {
          detailMessage = opsHint;
        } else if (retryAttemptN != null && threshold != null) {
          detailMessage = `already retried ${retryAttemptN} times (threshold ${threshold}) — confirm the override prompt to force`;
        }
        const finalMessage = detailMessage || err.message || 'unknown error';
        showToast({ message: `Failed to retry: ${finalMessage}`, kind: 'error' });
        btn.disabled = false;
        return;
      }
      // Retry POST succeeded — surface success regardless of whether
      // the refresh works. The reload error path mirrors the cancel
      // flow above.
      showToast({ message: 'Retry execution created', kind: 'success', timeout: 5_000 });
      try {
        await loadHistory();
      } catch (reloadError) {
        console.error('Failed to reload history after retry:', reloadError);
      }
    });
  });

  // Scroll + flash the deep-link target if the URL hash carries a
  // ?execution=<id>. The suppression badge on the Recommendations
  // view links here so the user lands on the relevant row without
  // scrolling through the whole list.
  applyExecutionDeepLink();
}
