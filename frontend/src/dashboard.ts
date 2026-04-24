/**
 * Dashboard module for CUDly
 */

import { Chart, registerables } from 'chart.js';
import * as api from './api';
import * as state from './state';
import { formatCurrency, getDateParts, escapeHtml, populateAccountFilter } from './utils';
import { renderFreshness } from './freshness';
import type { DashboardSummary, UpcomingPurchase, ServiceSavings } from './types';
import type { SavingsDataPoint } from './api';
import { showToast } from './toast';
import { confirmDialog } from './confirmDialog';

// Register Chart.js components
Chart.register(...registerables);

// Separate Chart instance for the trend widget so renderSavingsChart's
// state.savingsChart doesn't conflict.
let savingsTrendChart: Chart | null = null;
let savingsTrendRange: '7' | '30' | '90' | 'all' = '90';

/**
 * Setup dashboard event handlers
 */
export function setupDashboardHandlers(): void {
  const providerFilter = document.getElementById('dashboard-provider-filter') as HTMLSelectElement | null;
  if (providerFilter) {
    // Set initial value from state
    providerFilter.value = state.getCurrentProvider();

    providerFilter.addEventListener('change', () => {
      state.setCurrentProvider(providerFilter.value as '' | 'aws' | 'azure' | 'gcp');
      void populateAccountFilter('dashboard-account-filter', api.listAccounts, providerFilter.value);
      void loadDashboard();
    });
  }

  const accountFilter = document.getElementById('dashboard-account-filter') as HTMLSelectElement | null;
  if (accountFilter) {
    accountFilter.addEventListener('change', () => {
      const val = accountFilter.value;
      state.setCurrentAccountIDs(val ? [val] : []);
      void loadDashboard();
    });
  }

  void populateAccountFilter('dashboard-account-filter', api.listAccounts, state.getCurrentProvider());
  setupSavingsTrendHandlers();
}

/**
 * Load dashboard data
 */
export async function loadDashboard(): Promise<void> {
  try {
    const currentProvider = state.getCurrentProvider();
    const [summaryData, upcomingData] = await Promise.all([
      api.getDashboardSummary(currentProvider, state.getCurrentAccountIDs()),
      api.getUpcomingPurchases()
    ]);

    renderDashboardSummary(summaryData as DashboardSummary);
    renderSavingsChart((summaryData as DashboardSummary).by_service || {});
    renderUpcomingPurchases((upcomingData as { purchases?: UpcomingPurchase[] }).purchases || []);
    // Load the savings-over-time widget independently — failure shouldn't
    // block the rest of the dashboard (e.g. analytics not configured).
    void loadSavingsTrendChart();

    // Refresh the freshness indicator on every load so provider switches
    // + data updates both reflect the latest collection timestamp.
    void renderFreshness('dashboard-freshness', loadDashboard);
  } catch (error) {
    console.error('Failed to load dashboard:', error);
    const summary = document.getElementById('summary');
    if (summary) {
      const err = error as Error;
      summary.innerHTML = `<p class="error">Failed to load dashboard: ${escapeHtml(err.message)}</p>`;
    }
  }
}

function renderDashboardSummary(data: DashboardSummary): void {
  const summary = document.getElementById('summary');
  if (!summary) return;

  // When no recommendations and no commitments exist, "100% coverage" is
  // misleading — nothing is being tracked. Show a dash instead.
  const nothingTracked = !data.total_recommendations && !data.active_commitments;
  const coverageValue = nothingTracked ? '—' : `${data.current_coverage || 0}%`;
  const coverageDetail = nothingTracked ? 'No services tracked' : `Target: ${data.target_coverage || 80}%`;

  summary.innerHTML = `
    <div class="card">
      <h3>Potential Monthly Savings</h3>
      <p class="value savings">${formatCurrency(data.potential_monthly_savings)}</p>
      <p class="detail">${data.total_recommendations || 0} recommendations</p>
    </div>
    <div class="card">
      <h3>Active Commitments</h3>
      <p class="value">${data.active_commitments || 0}</p>
      <p class="detail">${formatCurrency(data.committed_monthly)}/mo committed</p>
    </div>
    <div class="card">
      <h3>Current Coverage</h3>
      <p class="value">${coverageValue}</p>
      <p class="detail">${coverageDetail}</p>
    </div>
    <div class="card">
      <h3>YTD Savings</h3>
      <p class="value savings">${formatCurrency(data.ytd_savings)}</p>
      <p class="detail">From commitment purchases</p>
    </div>
  `;
}

function renderSavingsChart(byService: Record<string, ServiceSavings>): void {
  const ctx = document.getElementById('savings-chart') as HTMLCanvasElement | null;
  if (!ctx) return;

  const labels = Object.keys(byService);
  const potentialSavings = labels.map(s => byService[s]?.potential_savings || 0);
  const currentSavings = labels.map(s => byService[s]?.current_savings || 0);

  const existingChart = state.getSavingsChart();
  if (existingChart) {
    existingChart.destroy();
    state.setSavingsChart(null);
  }

  // No data → hide the canvas and render an empty-state message so the
  // chart doesn't render with a synthetic $0–$1 y-axis.
  const section = ctx.parentElement;
  let emptyState = section?.querySelector<HTMLParagraphElement>('.chart-empty');
  if (labels.length === 0) {
    ctx.style.display = 'none';
    if (section && !emptyState) {
      emptyState = document.createElement('p');
      emptyState.className = 'chart-empty empty';
      emptyState.textContent = 'No savings data yet. Add accounts and wait for recommendations.';
      section.appendChild(emptyState);
    }
    return;
  }
  // Data is back — restore the canvas and remove any stale empty state.
  ctx.style.display = '';
  emptyState?.remove();

  const chart = new Chart(ctx, {
    type: 'bar',
    data: {
      labels: labels,
      datasets: [
        {
          label: 'Potential Savings',
          data: potentialSavings,
          backgroundColor: '#fbbc04',
          borderRadius: 4
        },
        {
          label: 'Current Savings',
          data: currentSavings,
          backgroundColor: '#34a853',
          borderRadius: 4
        }
      ]
    },
    options: {
      responsive: true,
      maintainAspectRatio: false,
      scales: {
        y: {
          beginAtZero: true,
          ticks: {
            callback: (value) => '$' + value.toLocaleString()
          }
        }
      },
      plugins: {
        tooltip: {
          callbacks: {
            label: (context) => `${context.dataset.label}: $${(context.raw as number).toLocaleString()}/mo`
          }
        }
      }
    }
  });

  state.setSavingsChart(chart);
}

function renderUpcomingPurchases(purchases: UpcomingPurchase[]): void {
  const container = document.getElementById('upcoming-list');
  if (!container) return;

  if (!purchases || purchases.length === 0) {
    container.innerHTML = '<p class="empty">No upcoming scheduled purchases</p>';
    return;
  }

  container.innerHTML = purchases.map(p => {
    const dateParts = getDateParts(p.scheduled_date);
    return `
      <div class="upcoming-card">
        <div class="upcoming-info">
          <div class="upcoming-date">
            <div class="day">${dateParts.day}</div>
            <div class="month">${dateParts.month}</div>
          </div>
          <div class="upcoming-details">
            <h4>${escapeHtml(p.plan_name)}</h4>
            <p><span class="provider-badge ${p.provider}">${p.provider.toUpperCase()}</span> ${escapeHtml(p.service)} - Step ${p.step_number} of ${p.total_steps}</p>
          </div>
        </div>
        <div class="upcoming-savings">
          <div class="amount">${formatCurrency(p.estimated_savings)}</div>
          <div class="label">Est. monthly savings</div>
        </div>
        <div class="upcoming-actions">
          <button data-action="view-purchase" data-id="${p.execution_id}">View Details</button>
          <button data-action="cancel-purchase" data-id="${p.execution_id}" class="danger">Cancel</button>
        </div>
      </div>
    `;
  }).join('');

  // Add event listeners
  container.querySelectorAll<HTMLButtonElement>('[data-action="view-purchase"]').forEach(btn => {
    btn.addEventListener('click', () => void viewPurchaseDetails(btn.dataset['id'] || ''));
  });
  container.querySelectorAll<HTMLButtonElement>('[data-action="cancel-purchase"]').forEach(btn => {
    btn.addEventListener('click', () => void cancelScheduledPurchase(btn.dataset['id'] || ''));
  });
}

async function viewPurchaseDetails(executionId: string): Promise<void> {
  try {
    const purchase = await api.getPurchaseDetails(executionId);

    // Remove any existing details modal
    document.getElementById('purchase-details-modal')?.remove();

    const modal = document.createElement('div');
    modal.id = 'purchase-details-modal';
    modal.className = 'modal';
    modal.innerHTML = `
      <div class="modal-content modal-wide">
        <h2>Purchase Details</h2>
        <div class="form-section">
          <h3>Execution Info</h3>
          <table>
            <tbody>
              <tr><td><strong>Execution ID</strong></td><td>${escapeHtml(purchase.execution_id)}</td></tr>
              <tr><td><strong>Status</strong></td><td><span class="status-badge ${purchase.status.toLowerCase().replace(/[^a-z-]/g, '')}">${escapeHtml(purchase.status)}</span></td></tr>
              <tr><td><strong>Created</strong></td><td>${escapeHtml(purchase.created_at)}</td></tr>
              ${purchase.completed_at ? `<tr><td><strong>Completed</strong></td><td>${escapeHtml(purchase.completed_at)}</td></tr>` : ''}
            </tbody>
          </table>
        </div>
        ${purchase.results && purchase.results.length > 0 ? `
        <div class="form-section">
          <h3>Results</h3>
          <table>
            <thead>
              <tr><th>Recommendation ID</th><th>Status</th><th>Confirmation ID</th><th>Error</th></tr>
            </thead>
            <tbody>
              ${purchase.results.map(r => `
                <tr>
                  <td>${escapeHtml(r.recommendation_id)}</td>
                  <td><span class="status-badge ${r.status.toLowerCase().replace(/[^a-z-]/g, '')}">${escapeHtml(r.status)}</span></td>
                  <td>${r.confirmation_id ? escapeHtml(r.confirmation_id) : '-'}</td>
                  <td>${r.error ? escapeHtml(r.error) : '-'}</td>
                </tr>
              `).join('')}
            </tbody>
          </table>
        </div>
        ` : ''}
        <div class="modal-buttons">
          ${purchase.status.toLowerCase() === 'pending' ? '<button type="button" id="cancel-purchase-detail-btn" class="danger">Cancel Purchase</button>' : ''}
          <button type="button" id="close-purchase-details-btn">Close</button>
        </div>
      </div>
    `;
    document.body.appendChild(modal);

    modal.querySelector('#close-purchase-details-btn')?.addEventListener('click', () => {
      modal.remove();
    });

    const cancelBtn = modal.querySelector('#cancel-purchase-detail-btn') as HTMLButtonElement | null;
    if (cancelBtn) {
      cancelBtn.addEventListener('click', async () => {
        const ok = await confirmDialog({
          title: 'Cancel this purchase?',
          body: 'Cancelling a scheduled purchase cannot be undone. Any upfront cost already committed will not be refunded.',
          confirmLabel: 'Cancel purchase',
          destructive: true,
        });
        if (!ok) return;
        try {
          await api.cancelPurchase(executionId);
          modal.remove();
          await loadDashboard();
          showToast({ message: 'Purchase cancelled successfully', kind: 'success', timeout: 5_000 });
        } catch (cancelError) {
          console.error('Failed to cancel purchase:', cancelError);
          showToast({ message: 'Failed to cancel purchase', kind: 'error' });
        }
      });
    }
  } catch (error) {
    console.error('Failed to load purchase details:', error);
    const err = error as Error;
    showToast({ message: `Failed to load purchase details: ${err.message}`, kind: 'error' });
  }
}

async function cancelScheduledPurchase(executionId: string): Promise<void> {
  const ok = await confirmDialog({
    title: 'Cancel this scheduled purchase?',
    body: 'Cancelling a scheduled purchase cannot be undone. Any upfront cost already committed will not be refunded.',
    confirmLabel: 'Cancel purchase',
    destructive: true,
  });
  if (!ok) return;

  try {
    await api.cancelPurchase(executionId);
    await loadDashboard();
    showToast({ message: 'Purchase cancelled successfully', kind: 'success', timeout: 5_000 });
  } catch (error) {
    console.error('Failed to cancel purchase:', error);
    showToast({ message: 'Failed to cancel purchase', kind: 'error' });
  }
}

/**
 * Load the savings-over-time trend chart for the dashboard. Fetches the
 * history analytics endpoint with the currently-selected range and
 * renders a line chart of cumulative savings. Failure modes (analytics
 * not configured, empty data) degrade gracefully to an empty-state note.
 */
export async function loadSavingsTrendChart(): Promise<void> {
  const canvas = document.getElementById('savings-trend-chart') as HTMLCanvasElement | null;
  const empty = document.getElementById('savings-trend-empty');
  if (!canvas) return;
  const end = new Date();
  const days = savingsTrendRange === 'all' ? 365 : parseInt(savingsTrendRange, 10);
  const start = new Date(end.getTime() - days * 86400_000);
  const interval = days <= 7 ? 'hourly' : days <= 90 ? 'daily' : 'weekly';

  try {
    // Q5: honour the account-filter dropdown. Backend's /history/analytics
    // takes a single account_id (see handler_analytics.go). The dashboard
    // filter is single-select so we pass the only selected ID or omit to
    // query all accessible accounts.
    const accountIDs = state.getCurrentAccountIDs();
    const data = await api.getSavingsAnalytics({
      start: start.toISOString(),
      end: end.toISOString(),
      interval,
      ...(accountIDs.length === 1 ? { account_ids: accountIDs } : {}),
    });
    if (!data.data_points || data.data_points.length === 0) {
      if (savingsTrendChart) { savingsTrendChart.destroy(); savingsTrendChart = null; }
      canvas.style.display = 'none';
      empty?.classList.remove('hidden');
      return;
    }
    canvas.style.display = '';
    empty?.classList.add('hidden');

    if (savingsTrendChart) savingsTrendChart.destroy();
    const labels = data.data_points.map((p: SavingsDataPoint) => {
      const d = new Date(p.timestamp);
      return interval === 'hourly'
        ? d.toLocaleString('en-US', { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit', hour12: false })
        : d.toLocaleDateString('en-US', { month: 'short', day: 'numeric' });
    });
    const cumulative = data.data_points.map((p: SavingsDataPoint) => p.cumulative_savings || 0);

    savingsTrendChart = new Chart(canvas, {
      type: 'line',
      data: {
        labels,
        datasets: [{
          label: 'Cumulative savings',
          data: cumulative,
          borderColor: '#1a73e8',
          backgroundColor: 'rgba(26, 115, 232, 0.1)',
          fill: true,
          tension: 0.25,
          pointRadius: 2,
        }],
      },
      options: {
        responsive: true,
        maintainAspectRatio: false,
        plugins: {
          legend: { display: false },
          tooltip: {
            callbacks: {
              label: (ctx) => `Cumulative savings: $${(ctx.raw as number).toLocaleString()}`,
            },
          },
        },
        scales: {
          y: {
            beginAtZero: true,
            ticks: { callback: (v) => '$' + (v as number).toLocaleString() },
          },
        },
      },
    });
  } catch (err) {
    // Analytics is Postgres-backed now, but the endpoint still guards with
    // a 503 when the analytics client isn't wired. Don't break the rest of
    // the dashboard — hide the widget and fall back to a neutral message.
    console.warn('Savings trend chart unavailable:', err);
    if (savingsTrendChart) { savingsTrendChart.destroy(); savingsTrendChart = null; }
    canvas.style.display = 'none';
    if (empty) {
      empty.textContent = 'Savings history is not available yet.';
      empty.classList.remove('hidden');
    }
  }
}

// Wire up the range-toggle buttons. Called once during dashboard setup.
export function setupSavingsTrendHandlers(): void {
  document.querySelectorAll<HTMLButtonElement>('.trend-range').forEach((btn) => {
    btn.addEventListener('click', () => {
      const range = btn.dataset['range'];
      if (range !== '7' && range !== '30' && range !== '90' && range !== 'all') return;
      savingsTrendRange = range;
      document.querySelectorAll('.trend-range').forEach((b) => b.classList.remove('active'));
      btn.classList.add('active');
      void loadSavingsTrendChart();
    });
  });
}
