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
    // Inline Cancel button on pending/notified rows the current user is
    // permitted to cancel. Renders in the Plan column so the table
    // width stays the same; pending rows show their plan in
    // StatusDescription, not the Plan column, so this is non-conflicting.
    const planCellContent = canCancelPendingRow(p) && p.purchase_id
      ? `<button type="button" class="btn-link history-cancel-btn" data-cancel-id="${escapeHtml(p.purchase_id)}">Cancel</button>`
      : escapeHtml(p.plan_name || '-');
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

  // Scroll + flash the deep-link target if the URL hash carries a
  // ?execution=<id>. The suppression badge on the Recommendations
  // view links here so the user lands on the relevant row without
  // scrolling through the whole list.
  applyExecutionDeepLink();
}
