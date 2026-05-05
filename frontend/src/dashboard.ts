/**
 * Dashboard module for CUDly
 */

import { Chart, registerables } from 'chart.js';
import * as api from './api';
import * as state from './state';
import { formatCurrency, getDateParts, escapeHtml, populateAccountFilter } from './utils';
import { renderFreshness } from './freshness';
import type { DashboardSummary, UpcomingPurchase, ServiceSavings, LocalRecommendation } from './types';
import type { SavingsDataPoint } from './api';
import { showToast } from './toast';
import { confirmDialog } from './confirmDialog';
import { groupRecsByCell, pageLevelRange, formatSavingsRange } from './recommendations';

// Register Chart.js components
Chart.register(...registerables);

// Separate Chart instance for the trend widget so renderSavingsChart's
// state.savingsChart doesn't conflict.
let savingsTrendChart: Chart | null = null;
let savingsTrendRange: '7' | '30' | '90' | 'all' = '90';

// In-memory index of the currently-rendered upcoming purchases, keyed by
// execution_id. The "View Details" affordance renders from this — the
// /api/dashboard/upcoming response already carries every field the
// dialog displays, so no extra roundtrip is needed. The Cancel button
// uses the same execution_id to call DELETE /api/purchases/planned/{id}
// so cancelling removes JUST this scheduled step (the plan template
// stays in place). Earlier iterations: PR #207 keyed by plan_id which
// caused the cancel-then-delete-the-whole-plan bug; PR #213 (this one)
// enumerates real pending executions to surface execution_id properly.
let upcomingPurchasesIndex: Map<string, UpcomingPurchase> = new Map();

/**
 * Setup dashboard event handlers
 */
export function setupDashboardHandlers(): void {
  const providerFilter = document.getElementById('dashboard-provider-filter') as HTMLSelectElement | null;
  if (providerFilter) {
    // Set initial value from state
    providerFilter.value = state.getCurrentProvider();

    // Issue #185: previously this handler fired populateAccountFilter
    // and loadDashboard in parallel via two `void` calls. loadDashboard
    // ran first, reading stale `state.currentAccountIDs` from the
    // prior provider — so the dashboard rendered with the wrong
    // account filter. Worse, populateAccountFilter restores
    // `select.value = current` after repopulating; if the previously-
    // picked account isn't in the new provider's account list, the
    // dropdown silently goes empty (programmatic value change → no
    // `change` event fires) and state never resyncs. The fix:
    //   (a) clear the account selection in state synchronously so any
    //       in-flight read sees the cleared value,
    //   (b) await populateAccountFilter so loadDashboard sees a
    //       fully-populated dropdown,
    //   (c) explicitly reset the dropdown's display value to "(All
    //       accounts)" — no account from the previous provider can
    //       ever be valid here.
    providerFilter.addEventListener('change', () => {
      void (async (): Promise<void> => {
        const newProvider = providerFilter.value as '' | 'aws' | 'azure' | 'gcp';
        state.setCurrentProvider(newProvider);
        state.setCurrentAccountIDs([]);
        await populateAccountFilter('dashboard-account-filter', api.listAccounts, newProvider);
        const accountSel = document.getElementById('dashboard-account-filter') as HTMLSelectElement | null;
        if (accountSel) accountSel.value = '';
        await loadDashboard();
      })();
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
    const currentAccountIDs = state.getCurrentAccountIDs();

    // Fetch summary, upcoming, and recommendations concurrently.
    // Recommendations are fetched here (frontend-only approach) because
    // /api/dashboard/summary still returns a flat potential_monthly_savings
    // which overcounts by summing every variant of every cell (~6x inflation).
    // The recs endpoint is Postgres-cached and cheap; a future backend PR can
    // move the range computation server-side if needed.
    // Promise.allSettled ensures the dashboard still renders if any individual
    // fetch fails -- each card falls back gracefully.
    const [summaryResult, upcomingResult, recsResult] = await Promise.allSettled([
      api.getDashboardSummary(currentProvider, currentAccountIDs),
      api.getUpcomingPurchases(),
      api.getRecommendations({ provider: currentProvider, account_ids: currentAccountIDs }),
    ]);

    const summaryData = summaryResult.status === 'fulfilled' ? (summaryResult.value as DashboardSummary) : null;
    const upcomingData = upcomingResult.status === 'fulfilled' ? (upcomingResult.value as { purchases?: UpcomingPurchase[] }) : null;
    // api.Recommendation and LocalRecommendation are structurally identical
    // except for provider: string vs provider: Provider. The provider values
    // from the API are always the union members at runtime, so this cast is safe.
    const recs = recsResult.status === 'fulfilled'
      ? (recsResult.value as unknown as LocalRecommendation[])
      : ([] as LocalRecommendation[]);

    if (summaryResult.status === 'rejected') {
      throw summaryResult.reason as Error;
    }

    renderDashboardSummary(summaryData!, recs);
    renderSavingsChart(summaryData!.by_service || {});
    renderUpcomingPurchases(upcomingData?.purchases || []);
    // Load the savings-over-time widget independently -- failure shouldn't
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

function renderDashboardSummary(data: DashboardSummary, recs: readonly LocalRecommendation[]): void {
  const summary = document.getElementById('summary');
  if (!summary) return;

  // Compute per-cell savings range from recs. pageLevelRange sums per-cell
  // min/max savings (best and worst variant per physical resource cell),
  // avoiding the ~6x inflation of summing every variant of every cell that
  // the flat summary.potential_monthly_savings carries.
  // Falls back to formatCurrency(0) when recs is empty or fetch failed.
  const groups = groupRecsByCell(recs);
  const range = pageLevelRange(groups);
  const savingsDisplay = range.cellCount > 0
    ? formatSavingsRange(range.savingsMin, range.savingsMax)
    : formatCurrency(0);

  // When no recommendations and no commitments exist, "100% coverage" is
  // misleading — nothing is being tracked. Show a dash instead.
  const nothingTracked = !data.total_recommendations && !data.active_commitments;
  const coverageValue = nothingTracked ? '—' : `${data.current_coverage || 0}%`;
  const coverageDetail = nothingTracked ? 'No services tracked' : `Target: ${data.target_coverage || 80}%`;

  summary.innerHTML = `
    <div class="card">
      <h3>Potential Monthly Savings</h3>
      <p class="value savings">${savingsDisplay}</p>
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
    ctx.classList.add('hidden');
    if (section && !emptyState) {
      emptyState = document.createElement('p');
      emptyState.className = 'chart-empty empty';
      emptyState.textContent = 'No savings data yet. Add accounts and wait for recommendations.';
      section.appendChild(emptyState);
    }
    return;
  }
  // Data is back — restore the canvas and remove any stale empty state.
  ctx.classList.remove('hidden');
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

  // Refresh the in-memory index so viewPurchaseDetails can render its
  // dialog from local data — there is no execution row to look up yet
  // (the upcoming list shows plans whose next execution hasn't fired).
  upcomingPurchasesIndex = new Map(purchases.map(p => [p.execution_id, p]));

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
    btn.addEventListener('click', () => viewPurchaseDetails(btn.dataset['id'] || ''));
  });
  container.querySelectorAll<HTMLButtonElement>('[data-action="cancel-purchase"]').forEach(btn => {
    btn.addEventListener('click', () => void cancelScheduledPurchase(btn.dataset['id'] || ''));
  });
}

function viewPurchaseDetails(executionId: string): void {
  // The widget enumerates pending purchase_executions, so each row in the
  // upcoming list maps to a real execution row. We still render the dialog
  // from the in-memory snapshot to avoid an extra roundtrip — every field
  // the dialog displays already came down with the upcoming list.
  const purchase = upcomingPurchasesIndex.get(executionId);
  if (!purchase) {
    showToast({ message: 'Failed to load purchase details: not in current view', kind: 'error' });
    return;
  }

  // Remove any existing details modal
  document.getElementById('purchase-details-modal')?.remove();

  const modal = buildUpcomingDetailsModal(purchase, executionId);
  document.body.appendChild(modal);
}

// buildUpcomingDetailsModal constructs the dialog via DOM-API, not
// innerHTML, so user-controlled fields (plan_name, service) can never be
// interpreted as HTML — closes a class of XSS regressions that the
// previous escapeHtml-+-innerHTML pattern only avoided by convention.
function buildUpcomingDetailsModal(p: UpcomingPurchase, executionId: string): HTMLDivElement {
  const modal = document.createElement('div');
  modal.id = 'purchase-details-modal';
  modal.className = 'modal';

  const content = document.createElement('div');
  content.className = 'modal-content modal-wide';
  modal.appendChild(content);

  const h2 = document.createElement('h2');
  h2.textContent = 'Upcoming Purchase Details';
  content.appendChild(h2);

  const section = document.createElement('div');
  section.className = 'form-section';
  content.appendChild(section);

  const sectionH3 = document.createElement('h3');
  sectionH3.textContent = 'Plan Info';
  section.appendChild(sectionH3);

  const table = document.createElement('table');
  const tbody = document.createElement('tbody');
  table.appendChild(tbody);
  section.appendChild(table);

  const addRow = (label: string, value: string): void => {
    const tr = document.createElement('tr');
    const tdLabel = document.createElement('td');
    const strong = document.createElement('strong');
    strong.textContent = label;
    tdLabel.appendChild(strong);
    const tdValue = document.createElement('td');
    tdValue.textContent = value;
    tr.appendChild(tdLabel);
    tr.appendChild(tdValue);
    tbody.appendChild(tr);
  };

  addRow('Plan name', p.plan_name);
  addRow('Plan ID', p.plan_id);
  addRow('Execution ID', executionId);
  addRow('Scheduled', p.scheduled_date);
  addRow('Provider', p.provider.toUpperCase());
  addRow('Service', p.service);
  addRow('Step', `${p.step_number} of ${p.total_steps}`);
  addRow('Est. monthly savings', formatCurrency(p.estimated_savings));

  // Buttons row
  const btnRow = document.createElement('div');
  btnRow.className = 'modal-buttons';
  content.appendChild(btnRow);

  const cancelBtn = document.createElement('button');
  cancelBtn.type = 'button';
  cancelBtn.id = 'cancel-purchase-detail-btn';
  cancelBtn.className = 'danger';
  cancelBtn.textContent = 'Cancel Purchase';
  cancelBtn.addEventListener('click', async () => {
    const ok = await confirmDialog({
      title: 'Cancel this scheduled purchase?',
      body: 'Cancelling a scheduled purchase cannot be undone. Any upfront cost already committed will not be refunded.',
      confirmLabel: 'Cancel purchase',
      destructive: true,
    });
    if (!ok) return;
    try {
      await api.deletePlannedPurchase(executionId);
      modal.remove();
      await loadDashboard();
      showToast({ message: 'Purchase cancelled successfully', kind: 'success', timeout: 5_000 });
    } catch (cancelError) {
      console.error('Failed to cancel purchase:', cancelError);
      showToast({ message: 'Failed to cancel purchase', kind: 'error' });
    }
  });
  btnRow.appendChild(cancelBtn);

  const closeBtn = document.createElement('button');
  closeBtn.type = 'button';
  closeBtn.id = 'close-purchase-details-btn';
  closeBtn.textContent = 'Close';
  closeBtn.addEventListener('click', () => modal.remove());
  btnRow.appendChild(closeBtn);

  return modal;
}

async function cancelScheduledPurchase(executionId: string): Promise<void> {
  // Cancel just THIS scheduled instance via DELETE /api/purchases/planned/{id}
  // (the deletePlannedPurchase backend handler operates on
  // purchase_executions). The plan template stays intact — the next
  // scheduler tick re-creates the next instance for the plan. Earlier
  // iterations got this wrong by either:
  //   - sending plan_id to deletePlannedPurchase (404 because
  //     deletePlannedPurchase expects an execution_id), or
  //   - falling back to deletePlan (which deleted the WHOLE plan).
  // The widget now enumerates real pending executions and surfaces
  // execution_id, so neither workaround is needed.
  const ok = await confirmDialog({
    title: 'Cancel this scheduled purchase?',
    body: 'Cancelling removes this upcoming step from the plan. The plan itself stays in place; the next scheduled step (if any) is unaffected.',
    confirmLabel: 'Cancel purchase',
    destructive: true,
  });
  if (!ok) return;

  try {
    await api.deletePlannedPurchase(executionId);
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
      canvas.classList.add('hidden');
      empty?.classList.remove('hidden');
      return;
    }
    canvas.classList.remove('hidden');
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
    canvas.classList.add('hidden');
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
