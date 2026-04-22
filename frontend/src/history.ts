/**
 * History module for CUDly
 */

import * as api from './api';
import { formatCurrency, formatDate, formatTerm, escapeHtml, populateAccountFilter } from './utils';
import type { HistoryResponse, HistorySummary, HistoryPurchase } from './types';
import { switchTab } from './navigation';

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
 * Initialize history date range
 */
export function initHistoryDateRange(): void {
  const end = new Date();
  const start = new Date();
  start.setMonth(start.getMonth() - 3);

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
 * View plan history
 */
export async function viewPlanHistory(planId: string): Promise<void> {
  switchTab('history');
  initHistoryDateRange();

  try {
    const data = await api.getHistory({ planId }) as unknown as HistoryResponse;
    renderHistorySummary(data.summary || {});
    renderHistoryList(data.purchases || []);
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
    return `
      <tr>
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
        <td>${escapeHtml(p.plan_name || '-')}</td>
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
}
