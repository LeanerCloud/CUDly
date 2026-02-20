/**
 * History module for CUDly
 */

import * as api from './api';
import { formatCurrency, formatDate, escapeHtml } from './utils';
import type { HistoryResponse, HistorySummary, HistoryPurchase } from './types';
import { switchTab } from './navigation';

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
  }
}

/**
 * Load history with filters
 */
export async function loadHistory(): Promise<void> {
  try {
    const filters: api.HistoryFilters = {
      start: (document.getElementById('history-start') as HTMLInputElement | null)?.value,
      end: (document.getElementById('history-end') as HTMLInputElement | null)?.value,
      provider: ((document.getElementById('history-provider-filter') as HTMLSelectElement | null)?.value || undefined) as api.Provider | undefined
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

  container.innerHTML = `
    <div class="card">
      <h3>Total Purchases</h3>
      <p class="value">${summary.total_purchases || 0}</p>
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

function renderHistoryList(purchases: HistoryPurchase[]): void {
  const container = document.getElementById('history-list');
  if (!container) return;

  if (!purchases || purchases.length === 0) {
    container.innerHTML = '<p class="empty">No purchase history found for the selected period.</p>';
    return;
  }

  container.innerHTML = `
    <table>
      <thead>
        <tr>
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
        ${purchases.map(p => `
          <tr>
            <td>${formatDate(p.timestamp)}</td>
            <td><span class="provider-badge ${p.provider}">${p.provider.toUpperCase()}</span></td>
            <td>${escapeHtml(p.service)}</td>
            <td>${escapeHtml(p.resource_type)}</td>
            <td>${escapeHtml(p.region)}</td>
            <td>${p.count}</td>
            <td>${p.term} year</td>
            <td>${formatCurrency(p.upfront_cost)}</td>
            <td class="savings">${formatCurrency(p.estimated_savings)}</td>
            <td>${escapeHtml(p.plan_name || '-')}</td>
          </tr>
        `).join('')}
      </tbody>
    </table>
  `;
}
