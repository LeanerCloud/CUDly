/**
 * Dashboard module for CUDly
 */

import { Chart, registerables } from 'chart.js';
import * as api from './api';
import * as state from './state';
import { formatCurrency, getDateParts, escapeHtml, populateAccountFilter } from './utils';
import type { DashboardSummary, UpcomingPurchase, ServiceSavings } from './types';

// Register Chart.js components
Chart.register(...registerables);

/**
 * Setup dashboard event handlers
 */
export function setupDashboardHandlers(): void {
  const providerFilter = document.getElementById('dashboard-provider-filter') as HTMLSelectElement | null;
  if (providerFilter) {
    // Set initial value from state
    providerFilter.value = state.getCurrentProvider();

    providerFilter.addEventListener('change', () => {
      state.setCurrentProvider(providerFilter.value as 'all' | 'aws' | 'azure' | 'gcp');
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
      <p class="value">${data.current_coverage || 0}%</p>
      <p class="detail">Target: ${data.target_coverage || 80}%</p>
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
  }

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
        if (!confirm('Are you sure you want to cancel this purchase?')) return;
        try {
          await api.cancelPurchase(executionId);
          modal.remove();
          await loadDashboard();
        } catch (cancelError) {
          console.error('Failed to cancel purchase:', cancelError);
          alert('Failed to cancel purchase');
        }
      });
    }
  } catch (error) {
    console.error('Failed to load purchase details:', error);
    const err = error as Error;
    alert(`Failed to load purchase details: ${err.message}`);
  }
}

async function cancelScheduledPurchase(executionId: string): Promise<void> {
  if (!confirm('Are you sure you want to cancel this scheduled purchase?')) {
    return;
  }

  try {
    await api.cancelPurchase(executionId);
    await loadDashboard();
    alert('Purchase cancelled successfully');
  } catch (error) {
    console.error('Failed to cancel purchase:', error);
    alert('Failed to cancel purchase');
  }
}
