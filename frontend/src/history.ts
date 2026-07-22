/**
 * History module for CUDly
 */

import * as api from './api';
import * as state from './state';
import { formatCurrency, formatDate, formatTerm, escapeHtml, escapeHtmlAttr, amortizedMonthly } from './utils';
import type { HistoryResponse, HistorySummary, HistoryPurchase } from './types';
import { switchTab } from './navigation';
import { confirmDialog } from './confirmDialog';
import { buildApprovalDetailsBody } from './approval-details';
import { showToast } from './toast';
import { getCurrentUser } from './state';
import { canAccess } from './permissions';
import { showSkeletonRows, teardownSkeleton } from './lib/skeleton';
import { getAccountName } from './recommendations';
import { applyColumnFilters } from './lib/column-filters';
import {
  openHistoryColumnPopover,
  renderHistoryFilterButton,
  closeOpenHistoryPopover,
} from './lib/history-filter-popover';
import type {
  PurchaseHistoryColumnId,
  ApprovalQueueColumnId,
} from './state';

const VALID_PROVIDERS: api.Provider[] = ['aws', 'azure', 'gcp'];

// AWS Marketplace fee/pricing constants (must stay in sync with handler_marketplace.go).
// awsMarketplaceFeePercent: transaction fee deducted from listing proceeds (published by AWS).
const AWS_MARKETPLACE_FEE_PERCENT = 12;
// awsMarketplaceNetFactor: fraction of list price the seller receives after the fee.
const AWS_MARKETPLACE_NET_FACTOR = 1 - AWS_MARKETPLACE_FEE_PERCENT / 100;
// awsMarketplaceBuyerDiscountFactor: applied to residual RI value to compute the default list price.
const AWS_MARKETPLACE_BUYER_DISCOUNT = 0.95;

type StatusFilter = 'all' | 'pending' | 'completed' | 'failed' | 'expired' | 'cancelled';

// Cache of the last-rendered purchase list so the status-chip click handler
// can re-render without re-fetching. Cleared on each loadHistory / viewPlanHistory.
let lastPurchases: HistoryPurchase[] = [];
let activeStatusFilter: StatusFilter = 'all';

// _fourEyesMode mirrors GlobalConfig.require_different_approver (issue #1005),
// refreshed on every loadHistory() call. Gates the inline Approve button so a
// creator can't approve their own pending purchase when dual-control is on.
let _fourEyesMode = false;

function normalizeStatus(p: HistoryPurchase): string {
  // Absent status → legacy DB row → counts as completed for filtering.
  return p.status || 'completed';
}

// isInFlight reports whether a status is an approved-but-not-yet-finalised
// execution (issue #621): the synchronous AWS purchase is mid-execution or got
// interrupted (Lambda timeout / crash). These rows MUST NOT render as the green
// "Completed" badge — doing so would tell the user a purchase finished when it
// may not have, tempting a re-approval / double-spend. They are grouped under
// the "Pending" filter chip (not "Completed") for the same reason.
function isInFlightStatus(s: string): boolean {
  return s === 'approved' || s === 'running' || s === 'paused';
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
// class on it. Called after each loadHistory render so links from
// the Recommendations badge AND the scheduled-purchase email's
// Review & Edit button land on the right row. Returns true when a
// match was found + highlighted.
//
// Falsy paths:
//   - No execID in URL: return false silently (the common case — no
//     deeplink was requested).
//   - execID present but no matching row in the rendered list (e.g.
//     the user's date filter excludes the execution, or the row hasn't
//     been ingested yet): surface a non-blocking toast so the user
//     understands why the page didn't jump anywhere, and clear the
//     hash so a follow-up loadHistory() doesn't re-toast the same
//     miss on every re-render.
//
// Exported for unit-test coverage.
export function applyExecutionDeepLink(): boolean {
  const execID = readDeepLinkExecutionID();
  if (!execID) return false;
  const row = document.querySelector<HTMLTableRowElement>(
    `tr[data-execution-id="${CSS.escape(execID)}"]`,
  );
  if (!row) {
    // Short-prefix the ID so the toast is readable but the user can
    // still cross-reference against the email if needed.
    const shortID = execID.length > 8 ? `${execID.slice(0, 8)}…` : execID;
    showToast({
      message: `Execution ${shortID} isn't in the current view — clear filters or widen the date range to find it.`,
      kind: 'info',
      timeout: 8_000,
    });
    // Drop the ?execution= from the hash so the next loadHistory()
    // (e.g. user changes a filter) doesn't fire this toast again.
    if (window.location.hash) {
      const baseHash = window.location.hash.split('?')[0] ?? '';
      window.history.replaceState({}, '', window.location.pathname + window.location.search + baseHash);
    }
    return false;
  }
  row.classList.add('history-row-highlight');
  row.scrollIntoView({ behavior: 'smooth', block: 'center' });
  // Fade the highlight after a few seconds so the row goes back to
  // normal styling — otherwise it stays yellow forever and a future
  // user click on a different row looks inconsistent.
  window.setTimeout(() => row.classList.remove('history-row-highlight'), 4000);
  return true;
}

/**
 * Setup history filter event handlers.
 *
 * Provider/account filters are global (sourced from state.ts via the
 * topbar chips). The history-section's own controls are just date range
 * + Load History button — those stay here.
 */
export function setupHistoryHandlers(): void {
  state.subscribeProvider(() => void loadHistory());
  state.subscribeAccount(() => void loadHistory());
  // Re-render both tables when the amortize toggle flips (issue #1112).
  state.subscribeAmortizeUpfront(() => {
    renderHistoryList(lastPurchases);
    renderApprovalQueue(lastPurchases);
    syncAmortizeCheckbox('history-amortize-checkbox');
    syncAmortizeCheckbox('approval-queue-amortize-checkbox');
  });
}

/**
 * Mount the "Amortize upfront over term" checkbox into a container element
 * (idempotent -- safe to call on every loadHistory).
 *
 * The checkbox is wired to setAmortizeUpfront so a change here is reflected
 * in all other views via the shared localStorage key + subscriber pattern.
 */
function mountAmortizeCheckbox(containerId: string, checkboxId: string): void {
  const container = document.getElementById(containerId);
  if (!container) return;
  if (document.getElementById(checkboxId)) return; // already mounted

  const wrapper = document.createElement('label');
  wrapper.className = 'amortize-toggle-label';
  wrapper.htmlFor = checkboxId;

  const cb = document.createElement('input');
  cb.type = 'checkbox';
  cb.id = checkboxId;
  cb.checked = state.getAmortizeUpfront();
  cb.addEventListener('change', () => {
    state.setAmortizeUpfront(cb.checked);
  });

  wrapper.appendChild(cb);
  wrapper.appendChild(document.createTextNode(' Amortize upfront over term'));
  container.appendChild(wrapper);
}

/** Keep an already-mounted checkbox in sync when state changes externally. */
function syncAmortizeCheckbox(checkboxId: string): void {
  const cb = document.getElementById(checkboxId) as HTMLInputElement | null;
  if (cb) cb.checked = state.getAmortizeUpfront();
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
    renderHistorySummary(data.summary ?? null);
    const purchases = data.purchases || [];
    renderApprovalQueue(purchases);
    renderHistoryList(purchases);
    snapDateInputsToPurchases(purchases);
  } catch (error) {
    console.error('Failed to load plan history:', error);
    const err = error as Error;
    const list = document.getElementById('history-list');
    if (list) {
      list.innerHTML = `<p class="error">Failed to load plan history: ${escapeHtml(err.message)}</p>`;
    }
    // Mirror the loadHistory catch: clear the approval queue on error
    // so stale pending rows from a previous render don't sit on screen
    // alongside the failure message and tempt clicks on outdated state.
    const queue = document.getElementById('purchases-approval-queue');
    if (queue) {
      queue.innerHTML = `<p class="error">Failed to load approval queue: ${escapeHtml(err.message)}</p>`;
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
  // Issue #344 T3: skeleton rows for the purchase-history table. 8
  // rows matches the typical first-page row count so the skeleton
  // doesn't shrink dramatically when real data arrives. Column count
  // (12) mirrors the rendered table headers in renderHistoryList:
  // Status / Date / Provider / Service / Type / Region / Count /
  // Term / Upfront Cost / Monthly Cost / Monthly Savings / Plan.
  const listEl = document.getElementById('history-list');
  if (listEl) showSkeletonRows(listEl, 8, 12);
  // Pending-approval queue card (issue #340 sub-task): 3 rows x 12
  // cols matches the queue table shape (Date / Account / Provider /
  // Service / Count / Term / Payment / Monthly Cost / Upfront Cost /
  // Monthly Savings / Created by / Actions). The queue is typically
  // much shorter than the full history list, so 3 rows is a sensible
  // skeleton size.
  const queueEl = document.getElementById('purchases-approval-queue');
  if (queueEl) showSkeletonRows(queueEl, 3, 12);

  // Close any open History column-filter popover before re-rendering so the
  // popover doesn't sit anchored to a stale <th> button after the table is
  // innerHTML-rewritten. The next render with active filters rebinds fresh
  // triggers; the popover stays opt-in (user clicks again to re-open).
  closeOpenHistoryPopover();

  try {
    // Provider/account filters live in state.ts now (mutated by topbar chips).
    const rawProvider = state.getCurrentProvider();
    const provider: api.Provider | undefined = (VALID_PROVIDERS as string[]).includes(rawProvider)
      ? (rawProvider as api.Provider)
      : undefined;

    const stateAccountIDs = state.getCurrentAccountIDs();
    const accountIDs: string[] | undefined = stateAccountIDs.length > 0 ? stateAccountIDs : undefined;

    const filters: api.HistoryFilters = {
      start: (document.getElementById('history-start') as HTMLInputElement | null)?.value,
      end: (document.getElementById('history-end') as HTMLInputElement | null)?.value,
      provider,
      account_ids: accountIDs
    };
    const [data, cfgResponse] = await Promise.all([
      api.getHistory(filters) as unknown as Promise<HistoryResponse>,
      // 4-eyes mode (issue #1005) lives on GlobalConfig; a failed fetch must
      // not block the history render, so this leg fails closed to "no config"
      // rather than throwing, and _fourEyesMode falls back to its last value.
      api.getConfig().catch(() => null),
    ]);
    if (cfgResponse?.global) {
      _fourEyesMode = cfgResponse.global.require_different_approver === true;
    }
    const banner = document.getElementById('four-eyes-banner');
    if (banner) banner.classList.toggle('hidden', !_fourEyesMode);
    renderHistorySummary(data.summary ?? null);
    const purchases = data.purchases || [];
    renderApprovalQueue(purchases);
    renderHistoryList(purchases);
  } catch (error) {
    console.error('Failed to load history:', error);
    const err = error as Error;
    const list = document.getElementById('history-list');
    if (list) {
      teardownSkeleton(list);
      list.innerHTML = `<p class="error">Failed to load history: ${escapeHtml(err.message)}</p>`;
    }
    const queue = document.getElementById('purchases-approval-queue');
    if (queue) {
      teardownSkeleton(queue);
      queue.innerHTML = `<p class="error">Failed to load approval queue: ${escapeHtml(err.message)}</p>`;
    }
  }
}

function renderHistorySummary(summary: HistorySummary | null): void {
  const container = document.getElementById('history-summary');
  if (!container) return;

  // When the API omits the summary field entirely (older deploy, partial
  // response, or an error absorbed upstream), render an explicit unknown
  // state on each card rather than fabricating all-zero values that look
  // like real financial aggregates.
  if (summary === null || summary === undefined) {
    container.innerHTML = `
      <div class="card">
        <h3>Total Purchases</h3>
        <p class="value">--</p>
      </div>
      <div class="card">
        <h3>Total Upfront Spent</h3>
        <p class="value">--</p>
      </div>
      <div class="card">
        <h3>Monthly Savings</h3>
        <p class="value savings">--</p>
      </div>
      <div class="card">
        <h3>Annual Savings</h3>
        <p class="value savings">--</p>
      </div>
    `;
    return;
  }

  // total_completed / total_pending fall back to total_purchases so the
  // summary renders sensibly against an older API deploy that hasn't shipped
  // the new counters yet.
  const total = summary.total_purchases ?? null;
  const totalDisplay = total !== null ? String(total) : '--';
  const completed = summary.total_completed ?? total;
  const pending = summary.total_pending ?? 0;
  const detail = (total !== null && pending > 0)
    ? `<p class="detail">${completed} completed · ${pending} pending</p>`
    : '';

  container.innerHTML = `
    <div class="card">
      <h3>Total Purchases</h3>
      <p class="value">${totalDisplay}</p>
      ${detail}
    </div>
    <div class="card">
      <h3>Total Upfront Spent</h3>
      <p class="value">${formatCurrency(summary.total_upfront ?? null)}</p>
    </div>
    <div class="card">
      <h3>Monthly Savings</h3>
      <p class="value savings">${formatCurrency(summary.total_monthly_savings ?? null)}</p>
    </div>
    <div class="card">
      <h3>Annual Savings</h3>
      <p class="value savings">${formatCurrency(summary.total_annual_savings ?? null)}</p>
    </div>
  `;
}

function statusBadgeHTML(status: string): string {
  const normalized = (status || 'completed').toLowerCase();
  switch (normalized) {
    case 'pending':
    case 'notified':
      return '<span class="badge badge-warning">Pending</span>';
    case 'approved':
    case 'running':
    case 'paused':
      // In-flight (issue #621): not finished — never show the green Completed
      // badge for these, or the user may think the purchase is done.
      return '<span class="badge badge-warning">In Progress</span>';
    case 'canceled':
    case 'cancelled':
      // Migration 000089 (expand-contract rename): the backend may return
      // either the new US spelling ('canceled') or the legacy British
      // spelling ('cancelled') during the rolling deploy window. Match both
      // so a row written by EITHER old or new code renders the muted Cancelled
      // badge instead of falling through to the green Completed default.
      // The CONTRACT migration (#1278) will normalize the data once the deploy
      // is stable; the British branch can be removed then.
      return '<span class="badge badge-muted">Cancelled</span>';
    case 'partially_completed':
      // #642: some commitments succeeded, some failed. Not a clean success
      // and never "failed" (real commitments exist) — a distinct warning badge
      // so the user knows to read the description and reconcile the failures.
      return '<span class="badge badge-warning">Partial</span>';
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
    if (s === 'pending' || s === 'notified' || isInFlightStatus(s)) counts.pending++;
    // Migration 000089 (expand-contract rename): the backend may return
    // either spelling during the rolling deploy window. Counting only the
    // British spelling would silently bucket new 'canceled' rows into the
    // Completed total, hiding them from the user.
    else if (s === 'canceled' || s === 'cancelled') counts.cancelled++;
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
  // Whitelist provider to prevent stored XSS via class attribute injection (#443).
  const safeProvider = (VALID_PROVIDERS as string[]).includes(p.provider) ? p.provider : 'unknown';
  return `<span class="provider-badge ${safeProvider}">${safeProvider.toUpperCase()}</span>`;
}

// canCancelPendingRow returns true when the current session is permitted
// to cancel the given pending/notified history row via the session-authed
// Cancel button (issue #46). UX gate only — the backend
// authorizeSessionCancel in internal/api/handler_purchases.go remains the
// security boundary; if this helper is wrong-positive the API surfaces
// 403 and the click handler turns that into a "Failed to cancel" toast.
//
// Heuristic:
//   * admin → always yes (canAccess('admin', '*'));
//   * cancel-any:purchases → yes (operator roles, issue #158);
//   * cancel-own:purchases + matching created_by_user_id → yes;
//   * anyone else, or legacy row with no created_by_user_id → no.
function canCancelPendingRow(p: HistoryPurchase): boolean {
  const status = (p.status || '').toLowerCase();
  if (status !== 'pending' && status !== 'notified') return false;
  const user = getCurrentUser();
  if (!user) return false;
  if (canAccess('admin', '*') || canAccess('cancel-any', 'purchases')) return true;
  // cancel-own: only the original creator. Legacy rows with no
  // created_by_user_id can't be cancelled via this UI; the email-token
  // path remains the escape hatch.
  if (!p.created_by_user_id) return false;
  return canAccess('cancel-own', 'purchases') && p.created_by_user_id === user.id;
}

// canApproveUnder4Eyes returns true when the 4-eyes dual-control policy
// (issue #1005, GlobalConfig.require_different_approver) allows sessionUserId
// to approve a row created by row.created_by_user_id. Mirrors the backend's
// requireDifferentApprover in internal/api/handler_purchases.go: mode off →
// always allowed; mode on → allowed only when the row has a recorded,
// different creator (a NULL/legacy creator is denied, matching the backend's
// fail-closed 403 for rows that predate dual-control).
function canApproveUnder4Eyes(row: HistoryPurchase, sessionUserId: string): boolean {
  return _fourEyesMode === false || (row.created_by_user_id != null && row.created_by_user_id !== sessionUserId);
}

// rbacAllowsApprove is the approve-permission decision (issue #286 /
// #1407) WITHOUT the 4-eyes overlay, so renderPendingActionButtons can
// distinguish "no permission at all" (no button, no badge) from
// "permission would allow it but 4-eyes blocks it" (badge instead of button).
function rbacAllowsApprove(p: HistoryPurchase, user: { id: string }): boolean {
  const status = (p.status || '').toLowerCase();
  if (status !== 'pending' && status !== 'notified') return false;
  // approve-any:purchases is carved out of admin:* (issue #923) and is
  // granted by the seeded Purchaser group OR any custom group that
  // explicitly lists the verb in effectivePermissions. Gate on the
  // verb directly so a non-seeded role with the same grant still
  // approves rows the backend would also let through.
  if (canAccess('approve-any', 'purchases')) return true;
  // Four-eyes RBAC (issue #1407): the session must hold an explicit
  // approve-own grant before ownership is consulted. Ownership alone never
  // grants approve.
  if (!canAccess('approve-own', 'purchases')) return false;
  if (!p.created_by_user_id) return false;
  return p.created_by_user_id === user.id;
}

// canApprovePendingRow returns true when the current session is permitted
// to approve the given pending history row via the inline Approve button
// (issue #286). UX gate only — the backend authorizeSessionApprove in
// internal/api/handler_purchases.go remains the security boundary; a
// false-positive here surfaces as a 403 toast on click rather than a
// successful approve.
//
// Heuristic (four-eyes RBAC — issue #1407; 4-eyes dual-control — issue #1005):
//   * status must be "pending" or "notified";
//   * any session with approve-any:purchases (carved-out admin verb,
//     seeded on Purchaser group; can also come from a custom group via
//     effectivePermissions) → approve-any; shows Approve on every pending row;
//   * session must also hold approve-own:purchases before ownership is
//     even evaluated (four-eyes: ownership alone does NOT grant approve);
//   * only then: the row's created_by_user_id must match the current user;
//   * legacy rows with NULL created_by_user_id → no (the email-token
//     path remains the escape hatch);
//   * finally, canApproveUnder4Eyes must allow it: when dual-control mode is
//     on, the session cannot approve a row it created itself.
function canApprovePendingRow(p: HistoryPurchase): boolean {
  const user = getCurrentUser();
  if (!user) return false;
  if (!rbacAllowsApprove(p, user)) return false;
  return canApproveUnder4Eyes(p, user.id);
}

// canRetryFailedRow returns true when the current session is permitted
// to retry the given failed history row via the inline Retry button
// (issue #47). UX gate only — the backend authorizeSessionRetry in
// internal/api/handler_purchases.go remains the security boundary.
//
// Heuristic:
//   * status must be "failed";
//   * row must NOT carry an ops_hint (persistent failure → no retry,
//     show the hint instead);
//   * row must NOT already have a retry_execution_id (we don't allow
//     retrying the same failure twice — the user should retry the
//     latest descendant in the chain);
//   * any session with retry-any:purchases (carved-out admin verb,
//     seeded on Purchaser group; can also come from a custom group via
//     effectivePermissions) → retry-any;
//   * otherwise the row's created_by_user_id must match the current
//     user (retry-own).
function canRetryFailedRow(p: HistoryPurchase): boolean {
  const status = (p.status || '').toLowerCase();
  if (status !== 'failed') return false;
  if (p.ops_hint) return false;
  if (p.retry_execution_id) return false; // already retried — user should act on the descendant
  const user = getCurrentUser();
  if (!user) return false;
  // retry-any:purchases is carved out of admin:* (issue #923) and is
  // granted by the seeded Purchaser group OR any custom group that
  // explicitly lists the verb in effectivePermissions. Gate on the
  // verb directly so a non-seeded role with the same grant still
  // retries rows the backend would also let through.
  if (canAccess('retry-any', 'purchases')) return true;
  // Mirror canCancelPendingRow / canApprovePendingRow (issue #1418): require
  // the retry-own permission explicitly before checking creator match.
  // Without this gate a role that holds NO retry permission at all (e.g. a
  // Plan Authors group with only plan verbs) would still see the Retry button
  // on rows they created, because the creator check alone was a sufficient
  // condition. The backend authorizeSessionRetry is the real security boundary;
  // this closes the UX gap.
  if (!canAccess('retry-own', 'purchases')) return false;
  if (!p.created_by_user_id) return false;
  return p.created_by_user_id === user.id;
}

// canRevokeCompletedRow returns true when the current session may revoke the
// given purchase row via the inline Revoke button (issue #290).
// UX gate only -- the backend authorizeSessionRevoke remains the real
// security boundary.
//
// Conditions:
//   * status must be "completed", "" (legacy blank), or "scheduled"
//     (pre-fire delay: the cloud SDK has not been called yet -- free cancel);
//   * provider must be "azure" (AWS and GCP have no direct cancel API);
//   * revocation_window_closes_at must be in the future;
//     for "scheduled" rows this field is populated with scheduled_execution_at
//     by the backend (issue #290, second-wave CR Finding E);
//   * row must not already be revoked (revoked_at absent);
//   * session must have revoke-any:purchases or revoke-own:purchases. Without
//     this the button rendered for every signed-in user and the backend just
//     403d, replicating the same UX-vs-RBAC drift PR #995 caught for the
//     approve / delete paths. Mirror the peer predicates (canCancelPendingRow,
//     canApprovePendingRow, canRetryFailedRow) which all check canAccess.
function canRevokeCompletedRow(p: HistoryPurchase): boolean {
  const status = (p.status || '').toLowerCase();
  if (status !== 'completed' && status !== '' && status !== 'scheduled') return false;
  if ((p.provider || '').toLowerCase() !== 'azure') return false;
  if (p.revoked_at) return false; // already revoked
  if (!p.revocation_window_closes_at) return false;
  if (new Date(p.revocation_window_closes_at) <= new Date()) return false;
  const user = getCurrentUser();
  if (!user) return false;
  // RBAC: admin or revoke-any always; otherwise revoke-own (account-scope
  // ownership is enforced server-side, the same model as the backend handler).
  if (canAccess('admin', '*') || canAccess('revoke-any', 'purchases')) return true;
  return canAccess('revoke-own', 'purchases');
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

// canSellOnMarketplace returns true when the current session is permitted
// to list the given completed history row on the AWS RI Marketplace
// (issue #292). UX gate only -- the backend authorizeSessionSell in
// internal/api/handler_marketplace.go remains the security boundary; a
// false-positive here surfaces as a 403 toast on click.
//
// Conditions:
//   * row must be a completed purchase (status "completed" or absent);
//   * row must be an AWS EC2 RI (marketplace is EC2-only);
//   * offering_class must be "standard" OR still unknown (empty). CUDly stamps
//     "convertible" on its own EC2 purchases, but externally-created Standard
//     RIs (and pre-migration rows) have an empty offering_class until the
//     backend lazily populates it from AWS on the list call. The backend
//     definitively gates -- it 400s a fetched "convertible" -- so we show the
//     button for unknown-class EC2 rows and let the backend decide, otherwise
//     the feature is unreachable end-to-end for the very case it targets;
//   * no active listing already (listing_state != "active"); and
//   * admin, or non-admin user (sell-own covers their own accounts --
//     we can't efficiently check per-account ownership client-side, so
//     we show the button for all non-admin users and let the backend 403
//     when the account is out of scope).
function canSellOnMarketplace(p: HistoryPurchase): boolean {
  const status = normalizeStatus(p).toLowerCase();
  if (status !== 'completed') return false;
  // Marketplace listing is AWS EC2 Standard-RI only. Gate on provider/service
  // so unknown-class rows for non-EC2 providers never show the Sell button.
  if ((p.provider || '').toLowerCase() !== 'aws') return false;
  if ((p.service || '').toLowerCase() !== 'ec2') return false;
  const offeringClass = (p.offering_class || '').toLowerCase();
  if (offeringClass !== 'standard' && offeringClass !== '') return false;
  if ((p.listing_state || '').toLowerCase() === 'active') return false;
  const user = getCurrentUser();
  if (!user) return false;
  // Gate on the sell verbs, not bare sign-in, so we don't show Sell to a
  // role that lacks sell-own/sell-any and avoid frontend/backend auth drift
  // (the backend authorizeSessionSell would 403 anyway). admin:* satisfies
  // sell-own here since it is not carved out of admin.
  if (
    !canAccess('admin', '*') &&
    !canAccess('sell-any', 'purchases') &&
    !canAccess('sell-own', 'purchases')
  ) {
    return false;
  }
  // Guard against listing a matured RI: compute remaining months from the
  // purchase timestamp and the total term. purchase_history.term is stored in
  // YEARS (1 or 3), so convert to months before comparing against elapsed
  // months (mirrors the row.Term * 12 conversion in handler_marketplace.go).
  // Without the conversion a 3-year RI was treated as 3 months and the Sell
  // button vanished after ~3 months. We require at least 1 full month remaining.
  const termYears = typeof p.term === 'number' ? p.term : Number(p.term) || 0;
  if (termYears <= 0) return false;
  const termMonths = termYears * 12;
  const purchaseMs = new Date(p.timestamp).getTime();
  if (!Number.isFinite(purchaseMs)) return false;
  const elapsedMonths = (Date.now() - purchaseMs) / (1000 * 60 * 60 * 24 * 30.4375);
  const remainingMonths = termMonths - elapsedMonths;
  if (remainingMonths < 1) return false;
  return true;
}

// canCancelMarketplaceListing returns true when there is an active listing
// that the current session can cancel.
function canCancelMarketplaceListing(p: HistoryPurchase): boolean {
  if ((p.listing_state || '').toLowerCase() !== 'active') return false;
  const user = getCurrentUser();
  if (!user) return false;
  // Same sell-verb gate as canSellOnMarketplace: cancelling a listing is a
  // marketplace write, so require sell-own/sell-any (or admin) rather than
  // bare sign-in to keep the UX gate aligned with authorizeSessionSell.
  if (
    !canAccess('admin', '*') &&
    !canAccess('sell-any', 'purchases') &&
    !canAccess('sell-own', 'purchases')
  ) {
    return false;
  }
  return true;
}

// shortExecID renders the first 8 chars of a UUID so inline lineage
// links ("Retried as #abc12345") stay readable in the table cell. The
// full ID is preserved in the data-history-status attribute so the
// click handler can deep-link without truncation surprises.
function shortExecID(id: string): string {
  return (id || '').replace(/^urn:.*?:/, '').slice(0, 8);
}

// sameRowActions returns the set of row-action buttons (Approve, Cancel,
// future siblings) in the same table cell as `btn`. Used by the Approve
// and Cancel click handlers to disable BOTH actions for the in-flight
// row while the API request is pending — issue #286 + CR pass on
// PR #299: clicking Approve disabled only Approve, leaving the
// adjacent Cancel button live; a quick double-click could fire
// conflicting requests on the same row before the reload completes.
//
// Falls back to `[btn]` when the button has no parent <td> (test
// fixtures may render buttons without a wrapping cell).
function sameRowActions(btn: HTMLButtonElement): HTMLButtonElement[] {
  const cell = btn.closest('td') || btn.parentElement;
  if (!cell) return [btn];
  return Array.from(
    cell.querySelectorAll<HTMLButtonElement>(
      '.history-approve-btn, .history-cancel-btn, .history-revoke-btn, .history-marketplace-sell-btn, .history-marketplace-cancel-btn',
    ),
  );
}

// renderPendingActionButtons returns the inline Approve / Cancel
// button HTML for a pending|notified row, or "" when neither verb is
// available to the current session. Extracted from renderActionCell
// so the approval-queue card can emit identical buttons without
// duplicating the predicate logic or the DOM contract. Each predicate
// is checked independently so a custom role with only one of the
// verbs renders just that button; Approve sits to the left as the
// affirmative action.
function renderPendingActionButtons(p: HistoryPurchase): string {
  if (!p.purchase_id) return '';
  const buttons: string[] = [];
  const user = getCurrentUser();
  if (canApprovePendingRow(p)) {
    buttons.push(`<button type="button" class="btn-link history-approve-btn" data-approve-id="${escapeHtmlAttr(p.purchase_id)}">Approve</button>`);
  } else if (user && rbacAllowsApprove(p, user) && !canApproveUnder4Eyes(p, user.id)) {
    // RBAC would allow Approve, but 4-eyes dual-control (issue #1005) blocks
    // this session from approving its own row. Surface the reason inline
    // instead of silently hiding the action.
    buttons.push('<span class="badge badge-muted">Awaiting different approver</span>');
  }
  if (canCancelPendingRow(p)) {
    buttons.push(`<button type="button" class="btn-link history-cancel-btn" data-cancel-id="${escapeHtmlAttr(p.purchase_id)}">Cancel</button>`);
  }
  return buttons.join(' ');
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
  // Pending → render Approve + Cancel side-by-side when the session
  // qualifies for both (the typical case after issue #286 — the same
  // approve-own / cancel-own grant lives in DefaultUserPermissions).
  const pendingButtons = renderPendingActionButtons(p);
  if (pendingButtons) {
    return pendingButtons;
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
      return `<button type="button" class="${cls}" data-retry-id="${escapeHtmlAttr(p.purchase_id)}">${label}</button>`;
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
  // Build the trailing action buttons so they compose with lineage links
  // — a retry-descendant can also have an active listing that needs a
  // Cancel button, or be an Azure row still inside its revoke window.
  const trailingActions: string[] = [];
  if (p.purchase_id) {
    // Completed Azure row within revocation window: Revoke button (issue
    // #290). Only Azure supports direct in-app revocation; AWS and GCP have
    // no cancel API so the button is suppressed for those providers.
    if (canRevokeCompletedRow(p)) {
      trailingActions.push(`<button type="button" class="btn-link history-revoke-btn" data-revoke-id="${escapeHtml(p.purchase_id)}">Revoke</button>`);
    }
    // Completed Standard RI rows (AWS): Cancel listing / Sell on Marketplace
    // (issue #292). Mutually exclusive with revoke in practice (revoke is
    // Azure-only, marketplace is AWS Standard-RI-only).
    if (canCancelMarketplaceListing(p)) {
      trailingActions.push(`<button type="button" class="btn-link history-marketplace-cancel-btn" data-marketplace-cancel-id="${escapeHtml(p.purchase_id)}">Cancel listing ${escapeHtml(p.listing_id || '')}</button>`);
    } else if (canSellOnMarketplace(p)) {
      trailingActions.push(`<button type="button" class="btn-link history-marketplace-sell-btn" data-marketplace-sell-id="${escapeHtml(p.purchase_id)}">Sell on Marketplace</button>`);
    }
  }

  if (lineage.length > 0 || trailingActions.length > 0) {
    return [...lineage, ...trailingActions].join(' ');
  }

  return escapeHtml(p.plan_name || '-');
}

// ---------------------------------------------------------------------------
// Per-column filter wiring for the Purchase History table.
//
// The Status chip-row above stays as-is — it's an enum-driven filter with no
// natural fit for the generic set/expr column-filter shape, and the chip-row
// is the more discoverable affordance for status anyway. Column filters here
// cover the row attributes (provider/service/type/region/term/count/upfront/
// savings) — Status is intentionally excluded.
//
// Numeric extractors round to 0 decimal places to match formatCurrency's
// default (CURRENCY_DEFAULT_DIGITS), so a user typing the visible "$123"
// matches the row that displays that exact value (issue #484 contract on
// the recommendations table).
// ---------------------------------------------------------------------------

const PURCHASE_HISTORY_NUMERIC_COLUMNS: ReadonlySet<PurchaseHistoryColumnId> = new Set([
  'count', 'upfront_cost', 'savings',
]);

function purchaseHistoryCategoricalCellValue(
  p: HistoryPurchase,
  col: PurchaseHistoryColumnId,
): string {
  switch (col) {
    case 'provider':       return p.provider ?? '';
    case 'service':        return p.service ?? '';
    case 'resource_type':  return p.resource_type ?? '';
    case 'region':         return p.region ?? '';
    case 'term':           return p.term == null ? '' : String(p.term);
    case 'count':
    case 'upfront_cost':
    case 'savings':        return '';
  }
}

function purchaseHistoryNumericCellValue(
  p: HistoryPurchase,
  col: PurchaseHistoryColumnId,
): number {
  switch (col) {
    case 'count':         return p.count ?? 0;
    case 'upfront_cost':  return p.upfront_cost ?? 0;
    case 'savings':       return p.estimated_savings ?? 0;
    case 'provider':
    case 'service':
    case 'resource_type':
    case 'region':
    case 'term':          return Number.NaN;
  }
}

// Round to display precision so typed values match the rendered cell value
// (formatCurrency default of 0 fraction digits, formatTerm renders the
// integer term unchanged).
function roundForDisplay(n: number): number {
  if (!Number.isFinite(n)) return n;
  return Number(n.toFixed(0));
}

export function applyPurchaseHistoryColumnFilters(
  purchases: readonly HistoryPurchase[],
  filters: state.PurchaseHistoryColumnFilters,
): HistoryPurchase[] {
  return applyColumnFilters<HistoryPurchase, PurchaseHistoryColumnId>(
    purchases,
    filters,
    {
      categorical: purchaseHistoryCategoricalCellValue,
      numeric: (p, col) => roundForDisplay(purchaseHistoryNumericCellValue(p, col)),
    },
  );
}

const PURCHASE_HISTORY_LABELS: Record<PurchaseHistoryColumnId, string> = {
  provider: 'Provider',
  service: 'Service',
  resource_type: 'Type',
  region: 'Region',
  term: 'Term',
  count: 'Count',
  upfront_cost: 'Upfront Cost',
  savings: 'Monthly Savings',
};

function purchaseHistoryDistinctValues(
  purchases: readonly HistoryPurchase[],
  column: PurchaseHistoryColumnId,
): string[] {
  const seen = new Set<string>();
  for (const p of purchases) {
    seen.add(purchaseHistoryCategoricalCellValue(p, column));
  }
  return Array.from(seen).sort((a, b) => {
    if (a === '' && b !== '') return -1;
    if (a !== '' && b === '') return 1;
    return a.localeCompare(b);
  });
}

function purchaseHistoryDisplayLabel(
  column: PurchaseHistoryColumnId,
  value: string,
): string {
  if (value === '') return '(empty)';
  if (column === 'term') {
    const n = Number(value);
    return Number.isFinite(n) ? formatTerm(n) : value;
  }
  return value;
}

function wirePurchaseHistoryFilterButtons(
  container: HTMLElement,
  // The pre-column-filter slice — popover lists distinct values from
  // every row that survived the status chip, NOT the further-narrowed
  // visible slice (otherwise the popover would lose values the user just
  // unchecked).
  sourceRows: readonly HistoryPurchase[],
): void {
  container.querySelectorAll<HTMLButtonElement>('.history-column-filter-btn').forEach((btn) => {
    const column = btn.dataset['column'] as PurchaseHistoryColumnId | undefined;
    if (!column) return;
    btn.addEventListener('click', (e) => {
      e.stopPropagation();
      const isNumeric = PURCHASE_HISTORY_NUMERIC_COLUMNS.has(column);
      const filters = state.getPurchaseHistoryColumnFilters();
      openHistoryColumnPopover<PurchaseHistoryColumnId>({
        column,
        anchor: btn,
        currentFilter: filters[column],
        headerLabel: PURCHASE_HISTORY_LABELS[column],
        kind: isNumeric ? 'numeric' : 'categorical',
        distinctValues: isNumeric ? undefined : purchaseHistoryDistinctValues(sourceRows, column),
        displayLabel: (v) => purchaseHistoryDisplayLabel(column, v),
        onCommit: (filter) => {
          state.setPurchaseHistoryColumnFilter(column, filter);
          renderHistoryList(lastPurchases);
        },
      });
    });
  });
}

function renderHistoryList(purchases: HistoryPurchase[]): void {
  const container = document.getElementById('history-list');
  if (!container) return;

  lastPurchases = purchases;

  // Issue #923: inject a read-only notice for sessions that lack the
  // carved-out spending verbs the buttons in this table need. Approve /
  // Retry are gated by canApprovePendingRow / canRetryFailedRow which
  // call canAccess('approve-any','purchases') and
  // canAccess('retry-any','purchases'); use the same predicate here so
  // the banner stays in lockstep with the visible buttons (a user who
  // holds either verb via a custom group sees no contradictory
  // notice).
  const canApproveAny = canAccess('approve-any', 'purchases');
  const canRetryAny = canAccess('retry-any', 'purchases');
  const hasAnyCarvedOut = canApproveAny || canRetryAny;
  const existingBanner = document.getElementById('history-no-purchaser-banner');
  if (!existingBanner && !hasAnyCarvedOut) {
    const banner = document.createElement('div');
    banner.id = 'history-no-purchaser-banner';
    banner.className = 'info-banner';
    banner.setAttribute('role', 'note');
    banner.textContent =
      'You can view and plan, but not execute purchases directly. ' +
      'Ask an admin to add you to the Purchaser group (Admin → Users) to execute purchases.';
    container.parentElement?.insertBefore(banner, container);
  } else if (existingBanner && hasAnyCarvedOut) {
    existingBanner.remove();
  }

  // Reset the filter when the dataset changes so the user isn't stuck on an
  // empty "Cancelled" slice after reloading with a fresh query.
  if (activeStatusFilter !== 'all' && !purchases.some(p => {
    const s = normalizeStatus(p).toLowerCase();
    if (activeStatusFilter === 'pending') return s === 'pending' || s === 'notified' || isInFlightStatus(s);
    if (activeStatusFilter === 'completed') return s === 'completed' || s === 'partially_completed' || !p.status;
    // Migration 000089: the Cancelled chip key is 'cancelled' (British, kept
    // stable for URL/state compatibility) but it must surface BOTH spellings
    // during the expand-contract deploy window so new 'canceled' rows aren't
    // hidden from the filter.
    if (activeStatusFilter === 'cancelled') return s === 'cancelled' || s === 'canceled';
    return s === activeStatusFilter;
  })) {
    activeStatusFilter = 'all';
  }

  if (!purchases || purchases.length === 0) {
    container.innerHTML = '<p class="empty">No purchase history found for the selected period.</p>';
    return;
  }

  const statusFiltered = purchases.filter(p => {
    if (activeStatusFilter === 'all') return true;
    const s = normalizeStatus(p).toLowerCase();
    if (activeStatusFilter === 'pending') return s === 'pending' || s === 'notified' || isInFlightStatus(s);
    if (activeStatusFilter === 'completed') return s === 'completed' || s === 'partially_completed' || !p.status;
    // Migration 000089: surface BOTH spellings under the Cancelled chip during
    // the expand-contract deploy window (see the equivalent guard above).
    if (activeStatusFilter === 'cancelled') return s === 'cancelled' || s === 'canceled';
    return s === activeStatusFilter;
  });

  // Apply the per-column filters AFTER the status chip filter so the
  // categorical popover lists only values present in the active status
  // slice (e.g. filtering by "Failed" only shows providers/services that
  // have failed rows).
  const colFilters = state.getPurchaseHistoryColumnFilters();
  const visible = applyPurchaseHistoryColumnFilters(statusFiltered, colFilters);

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
    const execIdAttr = p.purchase_id ? ` data-execution-id="${escapeHtmlAttr(p.purchase_id)}"` : '';
    const planCellContent = renderActionCell(p);
    const amortize = state.getAmortizeUpfront();
    const rawMonthly = p.monthly_cost != null ? p.monthly_cost : null;
    const displayMonthly = (rawMonthly != null && amortize)
      ? amortizedMonthly(rawMonthly, p.upfront_cost, p.term)
      : rawMonthly;
    const monthlyCostCell = displayMonthly != null
      ? formatCurrency(displayMonthly)
      : '<span class="muted">-</span>';
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
        <td>${monthlyCostCell}</td>
        <td class="savings">${formatCurrency(p.estimated_savings)}</td>
        <td>${planCellContent}</td>
      </tr>
    `;
  }).join('');

  const amortize = state.getAmortizeUpfront();
  const monthlyColHeader = amortize ? 'Monthly Cost (amortized)' : 'Monthly Cost';
  const fbtn = (col: PurchaseHistoryColumnId): string => renderHistoryFilterButton(
    col, PURCHASE_HISTORY_LABELS[col], colFilters[col] != null,
  );
  const markup = `
    ${buildStatusChipRowHTML(purchases, activeStatusFilter)}
    <table>
      <thead>
        <tr>
          <th>Status</th>
          <th>Date</th>
          <th><span>Provider</span>${fbtn('provider')}</th>
          <th><span>Service</span>${fbtn('service')}</th>
          <th><span>Type</span>${fbtn('resource_type')}</th>
          <th><span>Region</span>${fbtn('region')}</th>
          <th><span>Count</span>${fbtn('count')}</th>
          <th><span>Term</span>${fbtn('term')}</th>
          <th><span>Upfront Cost</span>${fbtn('upfront_cost')}</th>
          <th>${escapeHtml(monthlyColHeader)}</th>
          <th><span>Monthly Savings</span>${fbtn('savings')}</th>
          <th>Plan</th>
        </tr>
      </thead>
      <tbody>
        ${tableRows}
      </tbody>
    </table>
  `;
  container.innerHTML = markup;

  // Mount the amortize checkbox into the controls area (idempotent).
  mountAmortizeCheckbox('history-controls', 'history-amortize-checkbox');
  wirePurchaseHistoryFilterButtons(container, statusFiltered);

  container.querySelectorAll<HTMLButtonElement>('.status-chip[data-history-status]').forEach(btn => {
    btn.addEventListener('click', () => {
      const next = btn.dataset['historyStatus'] as StatusFilter | undefined;
      if (!next || next === activeStatusFilter) return;
      activeStatusFilter = next;
      renderHistoryList(lastPurchases);
    });
  });

  wireRowActionHandlers(container);

  // Scroll + flash the deep-link target if the URL hash carries a
  // ?execution=<id>. The suppression badge on the Recommendations
  // view links here so the user lands on the relevant row without
  // scrolling through the whole list.
  applyExecutionDeepLink();
}

// wireRowActionHandlers binds the Approve / Cancel / Retry click handlers
// against the buttons inside `container`. Scoped to the container (NOT
// the document) so multiple mounted lists (Purchase History table +
// Approval queue card) don't cross-fire: clicking the queue's Approve
// button binds and dispatches against the queue's button instance only.
//
// All three handlers terminate with a `loadHistory()` reload on success,
// which re-renders BOTH the history table and the queue card from the
// same fetched dataset. That means a successful approve from the queue
// card removes the row from BOTH views in one shot.
function wireRowActionHandlers(container: HTMLElement): void {
  // Wire the inline Approve button on pending rows the current session
  // may approve (issue #286). One flow: confirmDialog → POST /approve
  // (no token — bearer-session auth on apiRequest) → reload + toast.
  // Backend may still 409 on a status race (concurrent cancel landed
  // first); the catch surfaces the structured detail.
  container.querySelectorAll<HTMLButtonElement>('.history-approve-btn[data-approve-id]').forEach(btn => {
    btn.addEventListener('click', async () => {
      const id = btn.dataset['approveId'];
      if (!id) return;
      // Issue #374: show the per-rec details (service / engine /
      // resource / region / count / term + payment / costs) in the
      // modal so the user has informed consent before authorising a
      // financial commitment. buildApprovalDetailsBody falls back to
      // the legacy text sentence if the GET fails.
      const detailsBody = await buildApprovalDetailsBody(id);
      const ok = await confirmDialog({
        title: 'Approve this pending purchase?',
        body: detailsBody,
        confirmLabel: 'Approve purchase',
        destructive: false,
      });
      if (!ok) return;
      // Issue #286 + CR pass: Approve and Cancel can render together on
      // the same row, so disabling only the clicked button leaves the
      // sibling clickable while we await the API. Disable BOTH on
      // either click and re-enable both on failure — a successful
      // approve triggers a full history reload that re-renders the
      // row, so the row-action sibling state doesn't matter on the
      // happy path.
      const rowActions = sameRowActions(btn);
      rowActions.forEach((b) => { b.disabled = true; });
      try {
        await api.approvePurchase(id);
      } catch (approveError) {
        console.error('Failed to approve pending purchase:', approveError);
        const err = approveError as Error;
        showToast({ message: `Failed to approve: ${err.message || 'unknown error'}`, kind: 'error' });
        rowActions.forEach((b) => { b.disabled = false; });
        return;
      }
      showToast({ message: 'Purchase approved', kind: 'success', timeout: 5_000 });
      try {
        await loadHistory();
      } catch (reloadError) {
        console.error('Failed to reload history after approve:', reloadError);
      }
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
      // Symmetric with the Approve handler above: disable both row
      // actions while the API is in flight (CR pass on PR #299).
      const rowActions = sameRowActions(btn);
      rowActions.forEach((b) => { b.disabled = true; });
      try {
        await api.cancelPurchase(id);
      } catch (cancelError) {
        console.error('Failed to cancel pending purchase:', cancelError);
        const err = cancelError as Error;
        showToast({ message: `Failed to cancel: ${err.message || 'unknown error'}`, kind: 'error' });
        rowActions.forEach((b) => { b.disabled = false; });
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
      let retryResult: Awaited<ReturnType<typeof api.retryPurchase>>;
      try {
        retryResult = await api.retryPurchase(id, overThreshold ? { force: true } : undefined);
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
      // Gate the toast on the approval email outcome reported by the backend.
      // email_sent===false is an explicit failure signal that overrides any
      // status-based inference; show a warning even when status==='pending'.
      // email_sent===true or a pending/notified status (with email_sent absent)
      // means the approval request is in the queue.
      const emailExplicitlyFailed = retryResult.email_sent === false;
      const emailOk = !emailExplicitlyFailed && (
        retryResult.email_sent === true
        || retryResult.status === 'pending'
        || retryResult.status === 'notified'
      );
      if (emailOk) {
        showToast({ message: 'Purchase request sent for approval', kind: 'success', timeout: 5_000 });
      } else {
        showToast({ message: 'Retry created but approval email failed - check your notification settings', kind: 'warning', timeout: 8_000 });
      }
      try {
        await loadHistory();
      } catch (reloadError) {
        console.error('Failed to reload history after retry:', reloadError);
      }
    });
  });

  // Wire the inline Revoke button on completed Azure rows within the
  // free-cancel window (issue #290). confirmDialog -> POST -> reload.
  // The backend is the security boundary; canRevokeCompletedRow is a
  // UX gate that hides the button when the call would fail, but a stale
  // cache can still surface a 4xx -- handle it like any other failure.
  container.querySelectorAll<HTMLButtonElement>('.history-revoke-btn[data-revoke-id]').forEach(btn => {
    btn.addEventListener('click', async () => {
      const id = btn.dataset['revokeId'];
      if (!id) return;
      const ok = await confirmDialog({
        title: 'Revoke this purchase within the free-cancel window?',
        body: 'This will request an Azure reservation return. The charge will be refunded if the request is within the 7-day window. This action cannot be undone.',
        confirmLabel: 'Revoke purchase',
        destructive: true,
      });
      if (!ok) return;
      const rowActions = sameRowActions(btn);
      rowActions.forEach((b) => { b.disabled = true; });
      try {
        await api.revokePurchase(id);
      } catch (revokeError) {
        console.error('Failed to revoke purchase:', revokeError);
        const err = revokeError as Error;
        showToast({ message: `Failed to revoke: ${err.message || 'unknown error'}`, kind: 'error' });
        rowActions.forEach((b) => { b.disabled = false; });
        return;
      }
      showToast({ message: 'Purchase revocation submitted', kind: 'success', timeout: 5_000 });
      try {
        await loadHistory();
      } catch (reloadError) {
        console.error('Failed to reload history after revoke:', reloadError);
      }
    });
  });

  // Wire Sell on Marketplace button (issue #292).
  // Flow: pricing/schedule modal (RI summary + default price + 12% fee) →
  // user confirms → createMarketplaceListing. We never skip the pricing
  // modal (CR finding: going straight from confirmDialog to the API call
  // denies the user informed consent about the price and fee).
  container.querySelectorAll<HTMLButtonElement>('.history-marketplace-sell-btn[data-marketplace-sell-id]').forEach(btn => {
    btn.addEventListener('click', async () => {
      const id = btn.dataset['marketplaceSellId'];
      if (!id) return;

      // Look up the purchase record so we can show a meaningful price summary.
      const purchase = lastPurchases.find(p => p.purchase_id === id);

      // Build a pricing modal body with RI summary and fee breakdown.
      const bodyEl = document.createElement('div');
      bodyEl.className = 'marketplace-pricing-modal-body';

      if (purchase) {
        // purchase_history.term is stored in YEARS (1 or 3); convert to months
        // before computing the remaining term and residual so the price summary
        // shown to the user reflects real remaining value rather than ~1/3 of it
        // (a 3-year RI was previously treated as 3 months). Mirrors the
        // row.Term * 12 conversion in internal/api/handler_marketplace.go.
        const termYears = typeof purchase.term === 'number' ? purchase.term : Number(purchase.term) || 0;
        const termMonths = termYears > 0 ? termYears * 12 : 0;
        const purchaseMs = new Date(purchase.timestamp).getTime();
        const elapsedMonths = Number.isFinite(purchaseMs)
          ? (Date.now() - purchaseMs) / (1000 * 60 * 60 * 24 * 30.4375)
          : 0;
        const remainingMonths = Math.max(0, Math.round(termMonths - elapsedMonths));
        const upfront = purchase.upfront_cost ?? 0;
        const count = purchase.count > 0 ? purchase.count : 1;
        // Mirror marketplaceResidualPerUnit + resolveMarketplacePriceSchedule's
        // default branch in internal/api/handler_marketplace.go EXACTLY, so
        // this preview can never diverge from what the backend actually lists:
        //   - upfront-only: recurring (monthly) cost is deliberately excluded
        //     because the buyer assumes the recurring obligation post-transfer;
        //   - per instance: upfront_cost is the row total for `count` instances,
        //     but the AWS Marketplace price is per instance, so divide by count;
        //   - prorated: the upfront residual is scaled by remaining/original
        //     term (a 36-month RI at month 6 retains only 30/36 of its value);
        //   - zero when unpriceable: a no-upfront RI (upfront <= 0) or an
        //     unknown term (termMonths <= 0) has no residual to prorate, which
        //     is exactly when the backend now rejects the default schedule
        //     with an error instead of silently listing at $0.
        const perUnitResidual = termMonths > 0 && upfront > 0
          ? (upfront * (remainingMonths / termMonths)) / count
          : 0;
        const listPricePerUnit = perUnitResidual * AWS_MARKETPLACE_BUYER_DISCOUNT;
        const listPriceTotal = listPricePerUnit * count;
        const netProceedsTotal = listPriceTotal * AWS_MARKETPLACE_NET_FACTOR;

        const summaryEl = document.createElement('dl');
        summaryEl.className = 'marketplace-pricing-summary';
        const addRow = (label: string, value: string): void => {
          const dt = document.createElement('dt');
          dt.textContent = label;
          const dd = document.createElement('dd');
          dd.textContent = value;
          summaryEl.appendChild(dt);
          summaryEl.appendChild(dd);
        };
        addRow('RI ID', id);
        addRow('Region', purchase.region || '-');
        addRow('Resource type', purchase.resource_type || '-');
        addRow('Remaining term', remainingMonths === 1 ? '1 month' : `${remainingMonths} months`);
        if (listPricePerUnit > 0) {
          addRow('Default list price', count > 1
            ? `${formatCurrency(listPricePerUnit)}/unit (${formatCurrency(listPriceTotal)} total for ${count} units)`
            : formatCurrency(listPriceTotal));
          addRow(`AWS fee (${AWS_MARKETPLACE_FEE_PERCENT}%)`, formatCurrency(listPriceTotal * (AWS_MARKETPLACE_FEE_PERCENT / 100)));
          addRow('Estimated net proceeds', formatCurrency(netProceedsTotal));
        } else {
          // No default price can be computed (no upfront cost or unknown
          // term) -- listing will be rejected server-side unless a custom
          // price_schedule is supplied. Say so instead of showing a
          // misleading $0 or fabricated price.
          addRow('Default list price', 'unavailable (no upfront cost or unknown term)');
        }
        bodyEl.appendChild(summaryEl);
      }

      const noteEl = document.createElement('p');
      noteEl.className = 'marketplace-pricing-note';
      noteEl.textContent = `AWS charges a ${AWS_MARKETPLACE_FEE_PERCENT}% transaction fee on proceeds. The default schedule prices the listing at ${(1 - AWS_MARKETPLACE_BUYER_DISCOUNT) * 100}% below remaining value. You can adjust pricing by contacting your administrator or modifying the schedule via the API. This action cannot be undone without cancelling the listing.`;
      bodyEl.appendChild(noteEl);

      const ok = await confirmDialog({
        title: 'List this RI on the AWS Marketplace?',
        body: bodyEl,
        confirmLabel: 'Confirm listing',
        destructive: false,
      });
      if (!ok) return;

      const rowActions = sameRowActions(btn);
      rowActions.forEach(b => { b.disabled = true; });
      try {
        await api.createMarketplaceListing(id);
      } catch (sellError) {
        console.error('Failed to list RI on Marketplace:', sellError);
        const err = sellError as Error;
        showToast({ message: `Failed to list on Marketplace: ${err.message || 'unknown error'}`, kind: 'error' });
        rowActions.forEach(b => { b.disabled = false; });
        return;
      }
      showToast({ message: 'RI listed on Marketplace successfully', kind: 'success', timeout: 5_000 });
      try {
        await loadHistory();
      } catch (reloadError) {
        console.error('Failed to reload history after Marketplace listing:', reloadError);
      }
    });
  });

  // Wire Cancel listing button (issue #292)
  container.querySelectorAll<HTMLButtonElement>('.history-marketplace-cancel-btn[data-marketplace-cancel-id]').forEach(btn => {
    btn.addEventListener('click', async () => {
      const id = btn.dataset['marketplaceCancelId'];
      if (!id) return;
      const ok = await confirmDialog({
        title: 'Cancel this Marketplace listing?',
        body: 'This will remove the listing from the AWS Marketplace. Any existing buyer negotiations will be cancelled. You can relist the RI at any time.',
        confirmLabel: 'Cancel listing',
        destructive: true,
      });
      if (!ok) return;
      const rowActions = sameRowActions(btn);
      rowActions.forEach(b => { b.disabled = true; });
      try {
        await api.cancelMarketplaceListing(id);
      } catch (cancelError) {
        console.error('Failed to cancel Marketplace listing:', cancelError);
        const err = cancelError as Error;
        showToast({ message: `Failed to cancel listing: ${err.message || 'unknown error'}`, kind: 'error' });
        rowActions.forEach(b => { b.disabled = false; });
        return;
      }
      showToast({ message: 'Marketplace listing cancelled', kind: 'success', timeout: 5_000 });
      try {
        await loadHistory();
      } catch (reloadError) {
        console.error('Failed to reload history after Marketplace cancel:', reloadError);
      }
    });
  });
}

// isPendingRow returns true when a history row represents a purchase
// awaiting approval. The Approval queue card filters on this predicate.
// Mirrors the badge / button-eligibility logic elsewhere in this file:
// "pending" is the freshly-created state, "notified" is the post-SES
// state; both render the same Approve / Cancel affordances.
function isPendingRow(p: HistoryPurchase): boolean {
  const s = (p.status || '').toLowerCase();
  return s === 'pending' || s === 'notified';
}

// renderApprovalQueue paints the pending-approval card at the top of the
// Purchases tab (issue #340 sub-task). Filters the full history slice
// down to pending|notified rows and renders a compact action-focused
// table. When the filtered slice is empty, the card shows a friendly
// "No pending approvals" message so the section stays visible (stable
// layout, screen-reader-discoverable) without rendering an empty table.
//
// The row-action buttons reuse renderPendingActionButtons and the click
// handlers are wired by the shared wireRowActionHandlers helper, so
// approving from the queue card runs the exact same flow as approving
// from the history table (confirmDialog → API → toast → reload). The
// reload re-renders both views from one fetch, which removes the
// approved row from BOTH lists in one shot.
// ---------------------------------------------------------------------------
// Per-column filter wiring for the Approval Queue table.
//
// The queue scope is already narrow (pending|notified rows only); column
// filters add inline narrowing on the queue's own columns. As with the
// Purchase History wiring, Status is excluded because the queue's row set
// is status-defined and the parent loadHistory loop is the authoritative
// status source.
//
// Numeric extractors round to 0 decimal places (CURRENCY_DEFAULT_DIGITS)
// so a "$X" filter targets the same value the cell renders.
// ---------------------------------------------------------------------------

const APPROVAL_QUEUE_NUMERIC_COLUMNS: ReadonlySet<ApprovalQueueColumnId> = new Set([
  'count', 'monthly_cost', 'upfront_cost', 'savings',
]);

const APPROVAL_QUEUE_LABELS: Record<ApprovalQueueColumnId, string> = {
  provider: 'Provider',
  account: 'Account',
  service: 'Service',
  term: 'Term',
  payment: 'Payment',
  created_by: 'Created by',
  count: 'Count',
  monthly_cost: 'Monthly Cost',
  upfront_cost: 'Upfront Cost',
  savings: 'Monthly Savings',
};

function approvalQueueCategoricalCellValue(
  p: HistoryPurchase,
  col: ApprovalQueueColumnId,
): string {
  switch (col) {
    case 'provider':    return p.provider ?? '';
    case 'account':     return p.account_id ?? '';
    case 'service':     return p.service ?? '';
    case 'term':        return p.term == null ? '' : String(p.term);
    case 'payment':     return p.payment ?? '';
    case 'created_by':  return p.created_by_user_email ?? p.created_by_user_id ?? '';
    case 'count':
    case 'monthly_cost':
    case 'upfront_cost':
    case 'savings':     return '';
  }
}

function approvalQueueNumericCellValue(
  p: HistoryPurchase,
  col: ApprovalQueueColumnId,
): number {
  switch (col) {
    case 'count':        return p.count ?? 0;
    // Return NaN for null monthly_cost so numeric predicates (e.g. "= 0")
    // don't match rows where the provider didn't report a monthly cost.
    case 'monthly_cost': return p.monthly_cost == null ? Number.NaN : p.monthly_cost;
    case 'upfront_cost': return p.upfront_cost ?? 0;
    case 'savings':      return p.estimated_savings ?? 0;
    case 'provider':
    case 'account':
    case 'service':
    case 'term':
    case 'payment':
    case 'created_by':   return Number.NaN;
  }
}

export function applyApprovalQueueColumnFilters(
  purchases: readonly HistoryPurchase[],
  filters: state.ApprovalQueueColumnFilters,
): HistoryPurchase[] {
  return applyColumnFilters<HistoryPurchase, ApprovalQueueColumnId>(
    purchases,
    filters,
    {
      categorical: approvalQueueCategoricalCellValue,
      numeric: (p, col) => roundForDisplay(approvalQueueNumericCellValue(p, col)),
    },
  );
}

function approvalQueueDistinctValues(
  purchases: readonly HistoryPurchase[],
  column: ApprovalQueueColumnId,
): string[] {
  const seen = new Set<string>();
  for (const p of purchases) {
    seen.add(approvalQueueCategoricalCellValue(p, column));
  }
  return Array.from(seen).sort((a, b) => {
    if (a === '' && b !== '') return -1;
    if (a !== '' && b === '') return 1;
    return a.localeCompare(b);
  });
}

function approvalQueueDisplayLabel(
  column: ApprovalQueueColumnId,
  value: string,
): string {
  if (value === '') return '(empty)';
  if (column === 'term') {
    const n = Number(value);
    return Number.isFinite(n) ? formatTerm(n) : value;
  }
  if (column === 'account') {
    // Account cells render via getAccountName() — mirror the same display.
    return getAccountName(value);
  }
  return value;
}

function wireApprovalQueueFilterButtons(
  container: HTMLElement,
  // Source for the popover's distinct-values list — the pre-column-filter
  // pending slice. Same reasoning as Purchase History: the popover must
  // list every value that exists in the broader (un-narrowed) set so
  // the user can re-check a value after unchecking it.
  sourceRows: readonly HistoryPurchase[],
): void {
  container.querySelectorAll<HTMLButtonElement>('.history-column-filter-btn').forEach((btn) => {
    const column = btn.dataset['column'] as ApprovalQueueColumnId | undefined;
    if (!column) return;
    btn.addEventListener('click', (e) => {
      e.stopPropagation();
      const isNumeric = APPROVAL_QUEUE_NUMERIC_COLUMNS.has(column);
      const filters = state.getApprovalQueueColumnFilters();
      openHistoryColumnPopover<ApprovalQueueColumnId>({
        column,
        anchor: btn,
        currentFilter: filters[column],
        headerLabel: APPROVAL_QUEUE_LABELS[column],
        kind: isNumeric ? 'numeric' : 'categorical',
        distinctValues: isNumeric ? undefined : approvalQueueDistinctValues(sourceRows, column),
        displayLabel: (v) => approvalQueueDisplayLabel(column, v),
        onCommit: (filter) => {
          state.setApprovalQueueColumnFilter(column, filter);
          renderApprovalQueue(lastPendingForQueue);
        },
      });
    });
  });
}

// Cache of the last pre-column-filter pending list so the popover-driven
// re-render path can rebuild the table without re-fetching.
let lastPendingForQueue: HistoryPurchase[] = [];

export function renderApprovalQueue(purchases: HistoryPurchase[]): void {
  const container = document.getElementById('purchases-approval-queue');
  if (!container) return;

  const pending = (purchases || []).filter(isPendingRow);
  lastPendingForQueue = pending;

  if (pending.length === 0) {
    container.innerHTML = '<p class="empty">No pending approvals.</p>';
    return;
  }

  const colFilters = state.getApprovalQueueColumnFilters();
  const visible = applyApprovalQueueColumnFilters(pending, colFilters);

  const rows = visible.map(p => {
    const actions = renderPendingActionButtons(p);
    const actionsCell = actions || '<span class="muted">-</span>';
    // Show email when resolved; fall back to UUID so the cancel-own gate still
    // has something human-readable to show. Fall back to "-" for scheduler rows.
    const createdBy = p.created_by_user_email
      ? escapeHtml(p.created_by_user_email)
      : p.created_by_user_id
        ? escapeHtml(p.created_by_user_id)
        : '<span class="muted">-</span>';
    const accountCell = p.account_id
      ? escapeHtml(getAccountName(p.account_id))
      : '<span class="muted">-</span>';
    const termCell = p.term ? escapeHtml(formatTerm(p.term)) : '<span class="muted">-</span>';
    const paymentCell = p.payment ? escapeHtml(p.payment) : '<span class="muted">-</span>';
    const amortize = state.getAmortizeUpfront();
    const rawMonthly = p.monthly_cost != null ? p.monthly_cost : null;
    const displayMonthly = (rawMonthly != null && amortize)
      ? amortizedMonthly(rawMonthly, p.upfront_cost, p.term)
      : rawMonthly;
    const monthlyCostCell = displayMonthly != null
      ? formatCurrency(displayMonthly)
      : '<span class="muted">-</span>';
    const execIdAttr = p.purchase_id ? ` data-execution-id="${escapeHtmlAttr(p.purchase_id)}"` : '';
    return `
      <tr${execIdAttr}>
        <td>${formatDate(p.timestamp)}</td>
        <td>${accountCell}</td>
        <td>${providerCell(p)}</td>
        <td>${escapeHtml(p.service)}</td>
        <td>${p.count}</td>
        <td>${termCell}</td>
        <td>${paymentCell}</td>
        <td>${monthlyCostCell}</td>
        <td>${formatCurrency(p.upfront_cost)}</td>
        <td class="savings">${formatCurrency(p.estimated_savings)}</td>
        <td>${createdBy}</td>
        <td>${actionsCell}</td>
      </tr>
    `;
  }).join('');

  const amortize = state.getAmortizeUpfront();
  const monthlyColHeader = amortize ? 'Monthly Cost (amortized)' : 'Monthly Cost';
  // monthlyColHeader is a hardcoded constant string (no user data), so
  // interpolating it directly into the template is safe.
  const fbtn = (col: ApprovalQueueColumnId): string => renderHistoryFilterButton(
    col, APPROVAL_QUEUE_LABELS[col], colFilters[col] != null,
  );
  container.innerHTML = `
    <table class="approval-queue-table">
      <thead>
        <tr>
          <th>Date</th>
          <th><span>Account</span>${fbtn('account')}</th>
          <th><span>Provider</span>${fbtn('provider')}</th>
          <th><span>Service</span>${fbtn('service')}</th>
          <th><span>Count</span>${fbtn('count')}</th>
          <th><span>Term</span>${fbtn('term')}</th>
          <th><span>Payment</span>${fbtn('payment')}</th>
          <th><span>${monthlyColHeader}</span>${fbtn('monthly_cost')}</th>
          <th><span>Upfront Cost</span>${fbtn('upfront_cost')}</th>
          <th><span>Monthly Savings</span>${fbtn('savings')}</th>
          <th><span>Created by</span>${fbtn('created_by')}</th>
          <th>Actions</th>
        </tr>
      </thead>
      <tbody>
        ${rows}
      </tbody>
    </table>
  `;

  // Mount the amortize checkbox into the approval queue section (idempotent).
  mountAmortizeCheckbox('purchases-approval-queue-section', 'approval-queue-amortize-checkbox');

  wireRowActionHandlers(container);
  wireApprovalQueueFilterButtons(container, pending);
}
