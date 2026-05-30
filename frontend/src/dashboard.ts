/**
 * Dashboard module for CUDly
 */

import { Chart, registerables } from 'chart.js';
import * as api from './api';
import * as state from './state';
import { formatCurrency, getDateParts } from './utils';
import type { DashboardSummary, UpcomingPurchase, ServiceSavings, LocalRecommendation } from './types';
import type { SavingsDataPoint } from './api';
import { showToast } from './toast';
import { confirmDialog } from './confirmDialog';
import { groupRecsByCell, pageLevelRange, formatSavingsRange, triggerAutoRefreshIfStale } from './recommendations';
import { showSkeletonTiles, showSkeletonBlock, teardownSkeleton } from './lib/skeleton';

// Register Chart.js components
Chart.register(...registerables);

// Separate Chart instance for the trend widget so renderSavingsChart's
// state.savingsChart doesn't conflict.
let savingsTrendChart: Chart | null = null;
let savingsTrendRange: '7' | '30' | '90' | 'all' = '90';

// Chart instance for the per-service savings-range bar chart (issue #765).
let savingsByServiceChart: Chart | null = null;

// Maximum number of services to show in the bar chart before truncating.
const SAVINGS_BY_SERVICE_MAX = 10;

// Default palette for per-service bars. Cycles if there are more services
// than colours; alpha variant is computed inline so the array stays short.
const SERVICE_BAR_COLORS = [
  '#1a73e8', // blue
  '#34a853', // green
  '#fbbc04', // yellow
  '#ea4335', // red
  '#9c27b0', // purple
  '#00bcd4', // cyan
  '#ff5722', // deep-orange
  '#607d8b', // blue-grey
  '#795548', // brown
  '#4caf50', // light-green
];

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
 * True when the Home tab is the currently-visible tab. Used by the
 * reload-on-filter-change subscriptions below to skip the fetch (and
 * the resulting skeleton flash) when the user is on another tab —
 * switchTab('home') calls loadDashboard() on entry anyway.
 * Mirrors the isPurchasesTabActive() guard in modules/savings-history.ts.
 */
function isHomeTabActive(): boolean {
  return document.getElementById('home-tab')?.classList.contains('active') === true;
}

/**
 * Build a short human-readable description of the active topbar filter
 * for use in empty-state messages on the Home charts. Returns '' when no
 * filter is active so callers can distinguish "unfiltered empty" from
 * "filtered empty". Mirrors buildTrendFilterDesc used in loadSavingsTrendChart.
 */
export function buildFilterDesc(provider: string, accountIDs: readonly string[]): string {
  const parts: string[] = [];
  if (provider && provider.toLowerCase() !== 'all') parts.push(provider.toUpperCase());
  if (accountIDs.length > 0) parts.push(accountIDs[0] ?? '');
  return parts.join(', ');
}

/**
 * Setup dashboard event handlers
 */
export function setupDashboardHandlers(): void {
  // Filter source-of-truth lives in state.ts (mutated by the global
  // topbar chips). Subscribe to filter changes and reload the dashboard;
  // the issue #185 ordering rule (clear accounts before refetching for a
  // new provider) is enforced by topbar-filters.ts at the source so the
  // dashboard's loadDashboard() always sees consistent state.
  //
  // Coalescing: the provider-change handler in topbar-filters.ts fires
  // BOTH the account subscriber (setCurrentAccountIDs([])) AND the
  // provider subscriber (setCurrentProvider(newProv)) synchronously.
  // Without coalescing, two loadDashboard() calls race back-to-back.
  // queueMicrotask defers the actual fetch to after the current call
  // stack clears, so the two fires collapse into one reload.
  // Active-tab guard: skip the fetch when the Home tab is not visible;
  // switchTab('home') triggers loadDashboard() on entry.
  // Mirrors the scheduleReload pattern in modules/savings-history.ts.
  let dashboardReloadQueued = false;
  const scheduleDashboardReload = (): void => {
    if (!isHomeTabActive() || dashboardReloadQueued) return;
    dashboardReloadQueued = true;
    queueMicrotask(() => {
      dashboardReloadQueued = false;
      if (isHomeTabActive()) void loadDashboard();
    });
  };
  state.subscribeProvider(scheduleDashboardReload);
  state.subscribeAccount(scheduleDashboardReload);

  setupSavingsTrendHandlers();
}

/**
 * Load dashboard data
 */
export async function loadDashboard(): Promise<void> {
  // Issue #344 T3: render skeletons synchronously before kicking off the
  // fetch so the panels show "loading" intent instead of staying blank.
  // The success render replaces children for a clean handoff; the catch
  // block calls teardownSkeleton before rendering the error.
  const summaryEl = document.getElementById('summary');
  if (summaryEl) showSkeletonTiles(summaryEl, 4);
  const upcomingEl = document.getElementById('upcoming-list');
  if (upcomingEl) showSkeletonBlock(upcomingEl, '100%', '6rem');

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
    // Defensive extraction: the backend always returns the envelope shape
    //   { recommendations: [...], summary: {...}, regions: [...] }
    // so the real runtime value is never a flat array. We unwrap it here to
    // match what the Opportunities page does (cast to RecommendationsResponse
    // and read .recommendations). A flat-array result is also accepted so
    // test fixtures that resolve with a plain array continue to work.
    // #304: apiRequest's catch block returns `null` on a 2xx with empty/non-JSON
    // body; guard against null / unexpected shapes to avoid "X is not iterable".
    const rawRecs = recsResult.status === 'fulfilled' ? recsResult.value : null;
    const recsArray = Array.isArray(rawRecs)
      ? rawRecs
      : (rawRecs != null && typeof rawRecs === 'object' && Array.isArray((rawRecs as { recommendations?: unknown }).recommendations))
        ? (rawRecs as { recommendations: unknown[] }).recommendations
        : [];
    const recs: readonly LocalRecommendation[] = recsArray as unknown as LocalRecommendation[];

    if (summaryResult.status === 'rejected') {
      throw summaryResult.reason as Error;
    }

    // Build a human-readable filter description for filter-aware empty states
    // on both Home charts. Mirrors the pattern from loadSavingsTrendChart (#747).
    const filterDesc = buildFilterDesc(currentProvider, currentAccountIDs);

    renderDashboardSummary(summaryData!, recs);
    renderSavingsChart(summaryData!.by_service || {}, filterDesc);
    renderSavingsByService(recs, filterDesc);
    renderUpcomingPurchases(upcomingData?.purchases || []);
    // Load the savings-over-time widget independently -- failure shouldn't
    // block the rest of the dashboard (e.g. analytics not configured).
    void loadSavingsTrendChart();

    // Auto-refresh the recommendations cache if the last collection
    // is older than 24h (or never ran). The dashboard's KPI tiles +
    // savings card derive from recs; without this, a user who only
    // opens Home would never trigger the refresh cycle that the
    // Opportunities page used to own exclusively. The helper surfaces
    // collection errors as toasts and dedups concurrent refreshes; on
    // success it calls loadDashboard() to repaint this page with the
    // fresh data instead of forcing a recs render.
    void triggerAutoRefreshIfStale(loadDashboard);
  } catch (error) {
    console.error('Failed to load dashboard:', error);
    const summary = document.getElementById('summary');
    if (summary) {
      teardownSkeleton(summary);
      const err = error as Error;
      while (summary.firstChild) summary.removeChild(summary.firstChild);
      const p = document.createElement('p');
      p.classList.add('error');
      p.textContent = `Failed to load dashboard: ${err.message}`;
      summary.appendChild(p);
    }
    // Clear the upcoming-list skeleton too — shimmer next to a dashboard
    // error reads as a fresh fetch in-flight, which is misleading.
    const upcoming = document.getElementById('upcoming-list');
    if (upcoming) teardownSkeleton(upcoming);
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

export const __test__ = { sparklinePoints, attachSparkline, computeServiceStats };

function renderSavingsChart(byService: Record<string, ServiceSavings>, filterDesc = ''): void {
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
  // When a filter is active, mention it so the user understands why the
  // chart is blank (mirrors the savings-trend empty-state pattern from #747).
  const section = ctx.parentElement;
  let emptyState = section?.querySelector<HTMLParagraphElement>('.chart-empty');
  if (labels.length === 0) {
    ctx.classList.add('hidden');
    const emptyText = filterDesc
      ? `No savings data for the selected filter (${filterDesc}).`
      : 'No savings data yet. Add accounts and wait for recommendations.';
    if (section && !emptyState) {
      emptyState = document.createElement('p');
      emptyState.className = 'chart-empty empty';
      section.appendChild(emptyState);
    }
    if (emptyState) emptyState.textContent = emptyText;
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
 * Per-service savings statistics derived from a window of data points or
 * from a recommendations list.
 * Exported for unit testing only.
 */
export interface ServiceSavingsStats {
  min: number;
  max: number;
  sum: number;
  count: number;
  samples: number[];
  /** Label of the recommendation option that produced the minimum savings (e.g. "1yr no-upfront"). */
  minLabel?: string;
  /** Label of the recommendation option that produced the maximum savings (e.g. "3yr all-upfront"). */
  maxLabel?: string;
}

/**
 * Compute per-service savings stats from an array of data points. Each
 * data point carries a by_service map of { serviceName: savingsValue }.
 * Points with no by_service entry (omitempty from backend) are skipped.
 * Exported for unit testing.
 *
 * @deprecated Used only by historical-data tests. New production code uses
 * computeServiceStatsFromRecs for forward-looking potential savings.
 */
export function computeServiceStats(
  dataPoints: readonly SavingsDataPoint[],
): Map<string, ServiceSavingsStats> {
  const stats = new Map<string, ServiceSavingsStats>();
  for (const dp of dataPoints) {
    if (!dp.by_service) continue;
    for (const [svc, val] of Object.entries(dp.by_service)) {
      if (typeof val !== 'number') continue;
      const existing = stats.get(svc);
      if (existing) {
        existing.min = Math.min(existing.min, val);
        existing.max = Math.max(existing.max, val);
        existing.sum += val;
        existing.count += 1;
        existing.samples.push(val);
      } else {
        stats.set(svc, { min: val, max: val, sum: val, count: 1, samples: [val] });
      }
    }
  }
  return stats;
}

/**
 * Compute per-service potential-savings stats from a recommendations list.
 *
 * For each service, collects every recommendation row and treats each row's
 * `savings` value as one "option". The bar's:
 *   - floor  = min(savings) across all rows for that service
 *   - upside = max(savings) - min(savings)
 *
 * When a service has only one recommendation row, the bar collapses to a
 * single point (floor only, zero upside) — that is the correct visual.
 *
 * NOTE: The current recommendations response carries a single `savings` field
 * per row rather than per-variant breakdowns (e.g. 1yr/3yr × no-upfront/
 * all-upfront columns). When per-variant rows are shipped, min/max will
 * automatically reflect the full option range; no code change is required.
 * See #769 for context.
 *
 * Exported for unit testing.
 */
export function computeServiceStatsFromRecs(
  recs: readonly LocalRecommendation[],
): Map<string, ServiceSavingsStats> {
  const stats = new Map<string, ServiceSavingsStats>();
  for (const rec of recs) {
    const svc = rec.service;
    const val = typeof rec.savings === 'number' ? rec.savings : 0;
    const paymentLabel = rec.payment && rec.payment.trim().length > 0 ? rec.payment : 'unspecified';
    const label = `${rec.term}yr ${paymentLabel}`;
    const existing = stats.get(svc);
    if (existing) {
      if (val < existing.min) {
        existing.min = val;
        existing.minLabel = label;
      }
      if (val > existing.max) {
        existing.max = val;
        existing.maxLabel = label;
      }
      existing.sum += val;
      existing.count += 1;
      existing.samples.push(val);
    } else {
      stats.set(svc, { min: val, max: val, sum: val, count: 1, samples: [val], minLabel: label, maxLabel: label });
    }
  }
  return stats;
}


/**
 * Render the per-service potential-savings range stacked bar chart (issue #769).
 * Accepts the recommendations array from loadDashboard.
 *
 * Each bar is split into two stacked datasets:
 *   - "Min potential"  (solid): height = min potential savings across recommendation rows for that service.
 *   - "Upside"  (translucent 35% opacity): height = max - min (the additional potential).
 *
 * Services are sorted by max potential savings descending; the top SAVINGS_BY_SERVICE_MAX
 * are shown. When truncated, the section heading notes "+N more".
 *
 * Empty state: when no recommendations are available, the canvas is hidden and
 * the empty-state paragraph is shown.
 */
export function renderSavingsByService(recs: readonly LocalRecommendation[], filterDesc = ''): void {
  const canvas = document.getElementById('savings-by-service-chart') as HTMLCanvasElement | null;
  const emptyEl = document.getElementById('savings-by-service-empty');
  const section = document.getElementById('savings-by-service-section');
  if (!canvas) return;

  // Destroy existing chart before rebuilding.
  if (savingsByServiceChart) {
    savingsByServiceChart.destroy();
    savingsByServiceChart = null;
  }

  const stats = computeServiceStatsFromRecs(recs);
  const heading = section?.querySelector('h3');

  // Filter to services with positive savings, then sort by max desc.
  const positive = Array.from(stats.entries()).filter(([, s]) => s.max > 0);
  positive.sort((a, b) => b[1].max - a[1].max);

  if (positive.length === 0) {
    if (heading) heading.textContent = 'Potential savings range per service';
    canvas.classList.add('hidden');
    if (emptyEl) {
      // When a filter is active and excluded all results, surface it so the
      // user understands why the chart is blank (mirrors #747 pattern).
      emptyEl.textContent = filterDesc
        ? `No recommendations for the selected filter (${filterDesc}).`
        : 'No positive potential savings found for current recommendations.';
      emptyEl.classList.remove('hidden');
    }
    return;
  }

  canvas.classList.remove('hidden');
  emptyEl?.classList.add('hidden');

  // Cap at maximum and update heading with "+N more" if truncated.
  const truncated = positive.length > SAVINGS_BY_SERVICE_MAX;
  const visible = positive.slice(0, SAVINGS_BY_SERVICE_MAX);
  if (heading) {
    heading.textContent = truncated
      ? `Potential savings range per service (+${positive.length - SAVINGS_BY_SERVICE_MAX} more)`
      : 'Potential savings range per service';
  }

  const labels = visible.map(([svc]) => svc);
  const floorData = visible.map(([, s]) => s.min);
  const rangeData = visible.map(([, s]) => s.max - s.min);
  const totalSavings = Array.from(stats.values()).reduce((acc, s) => acc + s.max, 0);

  // Assign a colour per service (cycles if more than palette length).
  const bgColors = visible.map((_, i) => SERVICE_BAR_COLORS[i % SERVICE_BAR_COLORS.length] ?? '#1a73e8');
  const rangeColors = bgColors.map(c => {
    // Convert solid hex to rgba at 0.35 opacity for the range segment.
    const hex = c.replace('#', '');
    const r = parseInt(hex.substring(0, 2), 16);
    const g = parseInt(hex.substring(2, 4), 16);
    const b = parseInt(hex.substring(4, 6), 16);
    return `rgba(${r},${g},${b},0.35)`;
  });

  savingsByServiceChart = new Chart(canvas, {
    type: 'bar',
    data: {
      labels,
      datasets: [
        {
          label: 'Min potential',
          data: floorData,
          backgroundColor: bgColors,
          borderRadius: { topLeft: 0, topRight: 0, bottomLeft: 4, bottomRight: 4 },
          stack: 'savings',
        },
        {
          label: 'Upside',
          data: rangeData,
          backgroundColor: rangeColors,
          borderRadius: { topLeft: 4, topRight: 4, bottomLeft: 0, bottomRight: 0 },
          stack: 'savings',
        },
      ],
    },
    options: {
      responsive: true,
      maintainAspectRatio: false,
      plugins: {
        legend: {
          display: true,
          position: 'top',
          labels: { boxWidth: 12 },
        },
        tooltip: {
          callbacks: {
            label: (ctx) => {
              const svc = ctx.label ?? '';
              const s = stats.get(svc);
              if (!s) return '';
              const pct = totalSavings > 0 ? ((s.max / totalSavings) * 100).toFixed(1) : '0.0';
              const lines = [
                `Service: ${svc}`,
                `Min potential: $${s.min.toLocaleString()}`,
                `Max potential: $${s.max.toLocaleString()}`,
                `Options: ${s.count}`,
                `% of total: ${pct}%`,
              ];
              if (s.minLabel) lines.push(`Min option: ${s.minLabel}`);
              if (s.maxLabel) lines.push(`Max option: ${s.maxLabel}`);
              return lines;
            },
          },
        },
      },
      scales: {
        x: {
          stacked: true,
        },
        y: {
          stacked: true,
          beginAtZero: true,
          ticks: { callback: (v) => '$' + (v as number).toLocaleString() },
        },
      },
    },
  });
}

/**
 * Format a millisecond timestamp for the savings-trend x-axis tick label.
 * Exported for unit testing.
 */
export function formatTrendAxisTick(tsMs: number, intervalHint: 'hourly' | 'daily' | 'weekly'): string {
  const d = new Date(tsMs);
  if (intervalHint === 'hourly') {
    return d.toLocaleString('en-US', { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit', hour12: false });
  }
  return d.toLocaleDateString('en-US', { month: 'short', day: 'numeric' });
}

/**
 * Load the savings-over-time trend chart for the dashboard. Fetches the
 * history analytics endpoint with the currently-selected range and
 * renders a line chart of cumulative savings spanning the full selected
 * window on the x-axis (QA row 405, step 3.1). Empty windows render
 * labelled axes rather than a "no data" stub; only fetch failures use
 * the error stub.
 */
export async function loadSavingsTrendChart(): Promise<void> {
  const canvas = document.getElementById('savings-trend-chart') as HTMLCanvasElement | null;
  const empty = document.getElementById('savings-trend-empty');
  if (!canvas) return;

  const now = new Date();
  const nowMs = now.getTime();
  const isAllRange = savingsTrendRange === 'all';
  // For 'all', pass the Unix epoch as the start sentinel so the backend
  // returns every data point it holds. parseDateRange on the backend
  // defaults a missing start to (end - 7d), so we must send an explicit
  // floor rather than omitting the param — epoch is the lowest valid
  // RFC3339 value and has no practical upper bound on history length.
  // A client-side 3650-day ceiling would silently truncate accounts with
  // purchase history older than ~10 years.
  const epochStart = '1970-01-01T00:00:00Z';
  const days = isAllRange ? null : parseInt(savingsTrendRange, 10);
  // windowStart is the left edge of the axis; for 'all' it is overridden
  // below to the earliest purchase timestamp (or now-365d if no purchases).
  const windowStartMs = isAllRange ? nowMs - 365 * 86400_000 : nowMs - (days as number) * 86400_000;
  const intervalDays = isAllRange ? 3650 : (days as number);
  const interval: 'hourly' | 'daily' | 'weekly' = intervalDays <= 7 ? 'hourly' : intervalDays <= 90 ? 'daily' : 'weekly';

  try {
    // Always forward account_ids to the chart so its data scope matches the
    // KPI tiles above it. The backend /history/analytics handler accepts a
    // single `account_id`; api.getSavingsAnalytics also sets the singular
    // param for single-account requests (see api/history.ts).
    // provider is not forwarded: the analytics backend does not yet support
    // provider-scoped queries (handler_analytics.go has no provider param).
    const accountIDs = state.getCurrentAccountIDs();
    const data = await api.getSavingsAnalytics({
      // For 'all': send the epoch sentinel so the backend returns unbounded
      // history. Omitting start would cause parseDateRange to default to
      // (end - 7d), silently clipping the chart (see handler_analytics.go).
      start: isAllRange ? epochStart : new Date(windowStartMs).toISOString(),
      end: now.toISOString(),
      interval,
      ...(accountIDs.length > 0 ? { account_ids: accountIDs } : {}),
    });

    const points = data.data_points ?? [];

    // YTD Savings KPI tile sparkline (issue #340 T6) — uses the same
    // cumulative_savings series the main chart renders. Skips silently
    // when the tile isn't in the DOM (e.g. a different layout is mounted).
    attachSparkline('ytd', points.map((p: SavingsDataPoint) => p.cumulative_savings || 0));

    // Determine the left edge of the x-axis.
    // For 'all': anchor to the earliest purchase so the line fills the
    // chart rather than clustering at the right end. Fall back to now-365d
    // when there are no purchases.
    let axisMinMs = windowStartMs;
    if (savingsTrendRange === 'all' && points.length > 0) {
      const earliest = points.reduce(
        (min: number, p: SavingsDataPoint) => Math.min(min, new Date(p.timestamp).getTime()),
        Infinity,
      );
      axisMinMs = earliest;
    }

    // Map each data point to {x: timestamp_ms, y: cumulative_savings} so
    // Chart.js positions it by its real date, not by label index.
    const chartData = points.map((p: SavingsDataPoint) => ({
      x: new Date(p.timestamp).getTime(),
      y: p.cumulative_savings || 0,
    }));

    // Policy (QA 2.3, supersedes QA 3.1 empty-axis approach):
    // - No data + active account filter: show empty-state with the filter
    //   name so the user understands why the chart is blank.
    // - No data + no filter active: show a generic "No purchase history yet"
    //   message rather than blank axes.
    // - Data present: always show the chart (hide empty-state).
    // The stub is also shown on fetch errors (catch block below).
    if (points.length === 0) {
      canvas.classList.add('hidden');
      if (empty) {
        if (accountIDs.length > 0) {
          // Account IDs are forwarded to the backend (see call above), so
          // mentioning them in the empty-state is accurate.
          empty.textContent = `No savings history for ${accountIDs.join(', ')}.`;
        } else {
          // Provider is intentionally NOT mentioned here: the analytics
          // endpoint does not accept a provider param yet (tracked in #764),
          // so the query always returns all-provider data regardless of the
          // topbar provider filter. Claiming provider scope would be
          // misleading — drop it until #764 lands.
          empty.textContent = 'No purchase history yet.';
        }
        empty.classList.remove('hidden');
      }
      if (savingsTrendChart) { savingsTrendChart.destroy(); savingsTrendChart = null; }
      attachSparkline('ytd', []);
      return;
    }
    canvas.classList.remove('hidden');
    empty?.classList.add('hidden');

    if (savingsTrendChart) savingsTrendChart.destroy();

    savingsTrendChart = new Chart(canvas, {
      type: 'line',
      data: {
        datasets: [{
          label: 'Cumulative savings',
          data: chartData,
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
              label: (ctx) => `Cumulative savings: $${((ctx.raw as { x: number; y: number }).y).toLocaleString()}`,
            },
          },
        },
        scales: {
          x: {
            type: 'linear',
            min: axisMinMs,
            max: nowMs,
            ticks: {
              maxTicksLimit: 6,
              callback: (value) => formatTrendAxisTick(value as number, interval),
            },
          },
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
    attachSparkline('ytd', []);
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
