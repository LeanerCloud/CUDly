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
    // Defensive Array.isArray guard: apiRequest's catch block returns `null` when
    // response.json() fails (HTTP 2xx with empty/non-JSON body), so the settled
    // value may be null or a non-array shape even when status === 'fulfilled'.
    // #304: that non-array value reaches groupRecsByCell which iterates via
    // `for...of`, throwing "X is not iterable" and blanking the dashboard.
    const rawRecs = recsResult.status === 'fulfilled' ? recsResult.value : null;
    const recs: readonly LocalRecommendation[] = Array.isArray(rawRecs)
      ? (rawRecs as unknown as LocalRecommendation[])
      : [];

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
  // Soft-fail (#304): wrap in try/catch so an unexpected non-iterable shape
  // that slips past the Array.isArray guard in loadDashboard (e.g. if
  // groupRecsByCell is called from a path that bypasses the guard) cannot
  // blank the entire dashboard — the savings card degrades to $0 instead.
  let savingsDisplay: string;
  try {
    const groups = groupRecsByCell(recs);
    const range = pageLevelRange(groups);
    savingsDisplay = range.cellCount > 0
      ? formatSavingsRange(range.savingsMin, range.savingsMax)
      : formatCurrency(0);
  } catch (recErr) {
    console.warn('Dashboard: failed to compute savings range from recommendations:', recErr);
    savingsDisplay = formatCurrency(0);
  }

  // When no recommendations and no commitments exist, "100% coverage" is
  // misleading — nothing is being tracked. Show a dash instead.
  const nothingTracked = !data.total_recommendations && !data.active_commitments;
  const coverageValue = nothingTracked ? '—' : `${data.current_coverage || 0}%`;
  const coverageDetail = nothingTracked ? 'No services tracked' : `Target: ${data.target_coverage || 80}%`;

  // Render KPI tiles via DOM construction (textContent / appendChild)
  // rather than an innerHTML template literal, per the issue #340 plan's
  // XSS constraint. The values are all backend-sourced numbers/strings,
  // but the safe-by-default pattern is cheap and removes the question.
  while (summary.firstChild) summary.removeChild(summary.firstChild);
  const tiles: ReadonlyArray<{
    kpi: string;
    title: string;
    value: string;
    valueSavings?: boolean;
    detail: string;
  }> = [
    { kpi: 'savings',     title: 'Potential Monthly Savings', value: savingsDisplay, valueSavings: true,
      detail: `${data.total_recommendations || 0} recommendations` },
    { kpi: 'commitments', title: 'Active Commitments', value: String(data.active_commitments || 0),
      detail: `${formatCurrency(data.committed_monthly)}/mo committed` },
    { kpi: 'coverage',    title: 'Current Coverage', value: coverageValue, detail: coverageDetail },
    { kpi: 'ytd',         title: 'YTD Savings', value: formatCurrency(data.ytd_savings), valueSavings: true,
      detail: 'From commitment purchases' },
  ];
  for (const t of tiles) {
    const card = document.createElement('div');
    card.classList.add('card', 'kpi-tile');
    card.dataset['kpi'] = t.kpi;
    const h3 = document.createElement('h3');
    h3.textContent = t.title;
    const valueP = document.createElement('p');
    valueP.classList.add('value', 'kpi-tile-value');
    if (t.valueSavings) valueP.classList.add('savings');
    valueP.textContent = t.value;
    const detailP = document.createElement('p');
    detailP.classList.add('detail', 'kpi-tile-detail');
    detailP.textContent = t.detail;
    const spark = document.createElementNS('http://www.w3.org/2000/svg', 'svg');
    spark.classList.add('kpi-tile-spark', 'hidden');
    spark.dataset['sparkKey'] = t.kpi;
    spark.setAttribute('aria-hidden', 'true');
    card.appendChild(h3);
    card.appendChild(valueP);
    card.appendChild(detailP);
    card.appendChild(spark);
    summary.appendChild(card);
  }
}

/**
 * Build a small SVG polyline path string from a series of numeric values,
 * normalized into a width × height viewport. Returns the points string for
 * a <polyline points="..."> element. Pure helper — no DOM access, no I/O.
 */
function sparklinePoints(values: readonly number[], width: number, height: number): string {
  if (values.length < 2) return '';
  const min = Math.min(...values);
  const max = Math.max(...values);
  const range = max - min || 1;
  const stepX = width / (values.length - 1);
  return values.map((v, i) => {
    const x = i * stepX;
    const y = height - ((v - min) / range) * height;
    return `${x.toFixed(1)},${y.toFixed(1)}`;
  }).join(' ');
}

/**
 * Attach an SVG sparkline to a single KPI tile, keyed by its data-spark-key.
 * Silently no-ops when the placeholder isn't in the DOM (e.g. a different
 * card layout is rendered) or when there aren't enough data points to draw
 * a meaningful line. Uses DOM methods only — no innerHTML.
 */
function attachSparkline(key: string, values: readonly number[]): void {
  const svg = document.querySelector<SVGSVGElement>(`.kpi-tile-spark[data-spark-key="${key}"]`);
  if (!svg) return;
  while (svg.firstChild) svg.removeChild(svg.firstChild);
  if (values.length < 2) {
    svg.classList.add('hidden');
    return;
  }
  svg.classList.remove('hidden');

  const width = 80;
  const height = 24;
  svg.setAttribute('viewBox', `0 0 ${width} ${height}`);
  svg.setAttribute('preserveAspectRatio', 'none');

  const points = sparklinePoints(values, width, height);
  if (!points) {
    svg.classList.add('hidden');
    return;
  }

  const polyline = document.createElementNS('http://www.w3.org/2000/svg', 'polyline');
  polyline.setAttribute('points', points);
  polyline.setAttribute('fill', 'none');
  polyline.setAttribute('stroke', 'currentColor');
  polyline.setAttribute('stroke-width', '1.5');
  polyline.setAttribute('stroke-linecap', 'round');
  polyline.setAttribute('stroke-linejoin', 'round');
  svg.appendChild(polyline);
}

export const __test__ = { sparklinePoints, attachSparkline };

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

  container.textContent = '';
  for (const p of purchases) {
    const dateParts = getDateParts(p.scheduled_date);

    const card = document.createElement('div');
    card.className = 'upcoming-card';

    // Info block
    const info = document.createElement('div');
    info.className = 'upcoming-info';

    const dateDiv = document.createElement('div');
    dateDiv.className = 'upcoming-date';
    const dayDiv = document.createElement('div');
    dayDiv.className = 'day';
    dayDiv.textContent = String(dateParts.day);
    const monthDiv = document.createElement('div');
    monthDiv.className = 'month';
    monthDiv.textContent = dateParts.month;
    dateDiv.appendChild(dayDiv);
    dateDiv.appendChild(monthDiv);

    const details = document.createElement('div');
    details.className = 'upcoming-details';
    const h4 = document.createElement('h4');
    h4.textContent = p.plan_name;
    const descP = document.createElement('p');
    const badge = document.createElement('span');
    badge.className = 'provider-badge';
    // Whitelist provider to a CSS class — only alphanumeric + hyphen allowed
    const safeProvider = /^[a-z0-9-]+$/i.test(p.provider) ? p.provider : 'unknown';
    badge.classList.add(safeProvider);
    badge.textContent = p.provider.toUpperCase();
    descP.appendChild(badge);
    descP.appendChild(document.createTextNode(` ${p.service} - Step ${p.step_number} of ${p.total_steps}`));
    details.appendChild(h4);
    details.appendChild(descP);

    info.appendChild(dateDiv);
    info.appendChild(details);

    // Savings block
    const savings = document.createElement('div');
    savings.className = 'upcoming-savings';
    const amountDiv = document.createElement('div');
    amountDiv.className = 'amount';
    amountDiv.textContent = formatCurrency(p.estimated_savings);
    const labelDiv = document.createElement('div');
    labelDiv.className = 'label';
    labelDiv.textContent = 'Est. monthly savings';
    savings.appendChild(amountDiv);
    savings.appendChild(labelDiv);

    // Actions block
    const actions = document.createElement('div');
    actions.className = 'upcoming-actions';
    const viewBtn = document.createElement('button');
    viewBtn.dataset['action'] = 'view-purchase';
    viewBtn.dataset['id'] = String(p.execution_id);
    viewBtn.textContent = 'View Details';
    const cancelBtn = document.createElement('button');
    cancelBtn.dataset['action'] = 'cancel-purchase';
    cancelBtn.dataset['id'] = String(p.execution_id);
    cancelBtn.className = 'danger';
    cancelBtn.textContent = 'Cancel';
    actions.appendChild(viewBtn);
    actions.appendChild(cancelBtn);

    card.appendChild(info);
    card.appendChild(savings);
    card.appendChild(actions);
    container.appendChild(card);
  }

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
      attachSparkline('ytd', []);
      return;
    }
    canvas.classList.remove('hidden');
    empty?.classList.add('hidden');

    // YTD Savings KPI tile sparkline (issue #340 T6) — uses the same
    // cumulative_savings series the main chart renders. Skips silently
    // when the tile isn't in the DOM (e.g. a different layout is mounted).
    const cumulativeForSpark = data.data_points.map((p: SavingsDataPoint) => p.cumulative_savings || 0);
    attachSparkline('ytd', cumulativeForSpark);

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
