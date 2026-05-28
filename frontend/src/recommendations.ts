/**
 * Recommendations module for CUDly
 */

import * as api from './api';
import * as state from './state';
import type { CostPeriod } from './state';
import { formatCurrency, formatTerm, escapeHtml, CURRENCY_DEFAULT_DIGITS } from './utils';
import { getRecommendationsFreshness, refreshRecommendations as refreshRecommendationsAPI } from './api/recommendations';
import { showToast } from './toast';
import {
  isPaymentSupported,
  isSavingsPlanService,
  paymentOptionsFor,
  SAVINGS_PLANS_BUCKET_KEY,
  savingsPlansBucketLabel,
  type Payment as CompatPayment,
  type Provider as CompatProvider,
} from './lib/purchase-compatibility';
import type { AccountServiceOverride } from './api/accounts';
import type { RecommendationsResponse, LocalRecommendation, RecommendationsSummary } from './types';
import { openModal } from './modal';
import { showSkeletonRows, teardownSkeleton } from './lib/skeleton';
import { canAccess } from './permissions';
import { parseNumericFilter, applyColumnFilters as applyColumnFiltersLib } from './lib/column-filters';
// Re-export the shared primitives so existing consumers that import from
// recommendations.ts keep working without import-path churn (issue #166).
export { parseNumericFilter } from './lib/column-filters';
export type { ParsedNumericFilter } from './lib/column-filters';

// Module state for current purchase modal recommendations
let currentPurchaseRecommendations: LocalRecommendation[] = [];
// Tracks which row indices in currentPurchaseRecommendations the user
// has kept included (checked). Initialised to all indices on modal open;
// toggled by per-row checkboxes and the select-all header checkbox.
// Cleared with currentPurchaseRecommendations on modal close.
let checkedPurchaseIndices: Set<number> = new Set();
// True once openPurchaseModal has been called and initialised the
// checkedPurchaseIndices. Used by getPurchaseModalRecommendations to
// distinguish "all deselected by user" from "modal never opened".
let checkedPurchaseModalInitialised = false;
// Cache of account ID → name for column display
let accountNamesCache: Map<string, string> = new Map();

/**
 * Returns the display name for a cloud account ID, falling back to the raw
 * ID when the name has not been cached yet. Exported so other modules (e.g.
 * history.ts Approval Queue) can resolve account names without importing the
 * full recommendations data model.
 */
export function getAccountName(accountId: string): string {
  return accountNamesCache.get(accountId) || accountId;
}

// issues #225 + #226: expand/collapse state for cell grouping.
// Contains the cellKey strings of cells the user has explicitly expanded.
// Cleared on page load / full refresh; survives per-column filter/sort re-renders.
const expandedCells = new Set<string>();
// Last computed group keys for the visible filtered set — used by the
// Expand-All button handler to populate expandedCells without re-computing.
let lastVisibleGroupKeys: string[] = [];

// #272 (CR follow-up): cache of the most-recent API-derived summary so
// rerenders triggered by column-filter changes can keep total_count /
// total_upfront_cost / avg_payback_months stable while the savings card
// itself is recomputed client-side from the *visible* recs (so it stays
// in sync with the per-cell banner range under the table).
let lastRecommendationsSummary: RecommendationsSummary = {};

// #284 (CR follow-up): guard against concurrent stale-load refreshes. If a
// background refresh is already in flight, any subsequent stale detection
// skips kicking off a second request (and duplicate toasts).
let autoRefreshInFlight: Promise<void> | null = null;

/**
 * Freshness budget for auto-refresh (#284). If the last successful collection
 * is older than this threshold (or there has never been one), loadRecommendations
 * fires a background refresh automatically so the user sees up-to-date data
 * without needing an explicit Refresh button.
 *
 * TODO: expose via the /api/public-info endpoint so operators can tune this
 * without a frontend redeploy.
 */
export const STALE_THRESHOLD_MS = 24 * 60 * 60 * 1000; // 24 hours

// issue #223: resolved GlobalConfig defaults, seeded on page load by
// loadRecommendations(). Initial values mirror the historical hardcoded
// defaults so the module is usable before the first API round-trip.
let cachedGlobalDefaultPayment: CompatPayment = 'all-upfront';
let cachedGlobalDefaultTerm: 1 | 3 = 1;

/**
 * Inject resolved GlobalConfig defaults into the module cache.
 * Exported for testing only — not part of the public API.
 */
export function seedGlobalDefaults(term: 1 | 3, payment: CompatPayment): void {
  cachedGlobalDefaultTerm = term;
  cachedGlobalDefaultPayment = payment;
}

/**
 * Reset cell expand/collapse state. Exported for testing only — not part of
 * the public API. Call in beforeEach to ensure tests don't share state.
 */
export function resetExpandedCells(): void {
  expandedCells.clear();
  lastVisibleGroupKeys = [];
}

/**
 * Reset the auto-refresh in-flight guard. Exported for testing only — not
 * part of the public API. Call in beforeEach so dedup tests start clean.
 */
export function resetAutoRefreshInFlight(): void {
  autoRefreshInFlight = null;
}

// populateRecommendationsAccountFilter / populateRegionFilter / the legacy
// service-filter helpers were removed in Bundle B — those DOM elements are
// gone from index.html. Provider / Account values now drive an API re-fetch
// via the column-header popovers (see openColumnPopover wiring); Service /
// Region filtering is purely client-side via applyColumnFilters.

/**
 * Get the recommendations currently loaded in the purchase modal,
 * filtered to only those rows the user has kept "included" (i.e., checked).
 * Rows the user unchecked via the per-row checkbox are excluded from the
 * returned set, so the execute-purchase code path automatically honours
 * the user's per-row skip decisions without any callers needing to change.
 *
 * Returns all recs when the modal state is pre-#320 (checkedPurchaseModalInitialised
 * is false), which can happen if a caller invokes this without opening the modal
 * first (e.g. a legacy test fixture that sets currentPurchaseRecommendations
 * directly). Once the modal opens, the filter is always authoritative.
 */
export function getPurchaseModalRecommendations(): LocalRecommendation[] {
  if (!checkedPurchaseModalInitialised) {
    // Pre-open / legacy path: no checkbox state initialised — return everything.
    return [...currentPurchaseRecommendations];
  }
  return currentPurchaseRecommendations.filter((_, idx) => checkedPurchaseIndices.has(idx));
}

/**
 * Clear purchase modal recommendations and checked-indices state
 * (called when modal closes).
 */
export function clearPurchaseModalRecommendations(): void {
  currentPurchaseRecommendations = [];
  checkedPurchaseIndices = new Set();
  checkedPurchaseModalInitialised = false;
}

/**
 * True when the Opportunities tab is the currently-visible tab. The reload-
 * on-filter-change subscriptions below skip the fetch when this is false so
 * we don't burn an API call (and a skeleton flash) for a section the user
 * isn't looking at — `switchTab('opportunities')` will run loadRecommend-
 * ations() on next entry anyway.
 */
function isOpportunitiesTabActive(): boolean {
  return document.getElementById('opportunities-tab')?.classList.contains('active') === true;
}

/**
 * Setup recommendations event handlers (issue #477).
 *
 * The legacy per-section provider/account `<select>` elements were retired
 * in issue #344 in favour of the global topbar chips. Each section reloads
 * itself by subscribing to state.subscribeProvider / state.subscribeAccount;
 * Bundle B had removed the legacy listeners without adding the new
 * subscriptions, so Opportunities only re-queried on a full route-enter
 * (issue #477 repro: change filter, list doesn't update).
 *
 * Mirrors the dashboard.ts pattern — except we guard on the active-tab
 * check so the fetch only fires when the user is actually looking at
 * Opportunities. The provider-change ordering invariant (#185 — clear
 * accounts before refetching the account list) is enforced upstream in
 * topbar-filters.ts.
 *
 * Per-column header-mounted popovers (Bundle B) continue to handle the
 * in-page filter UX; their listeners are attached per-render inside
 * renderRecommendationsList.
 */
export function setupRecommendationsHandlers(): void {
  // Coalesce duplicate reloads. The topbar provider-change handler in
  // topbar-filters.ts updates BOTH state slots in sequence (clear accounts
  // then set provider, per the #185 ordering rule), which fires the
  // account-subscriber AND the provider-subscriber from a single user
  // action. Without coalescing we'd kick off two loadRecommendations()
  // calls back-to-back — extra API load plus a stale-overwrite risk if
  // the first response lands after the second.
  //
  // Microtask scheduling: both subscriber fires are synchronous within
  // the same setCurrentProvider/setCurrentAccountIDs call chain, so a
  // microtask runs once after the chain settles. setTimeout(_, 0) would
  // also work but adds a macrotask delay the user could perceive on
  // slow machines.
  let reloadQueued = false;
  const scheduleReload = (): void => {
    if (!isOpportunitiesTabActive() || reloadQueued) return;
    reloadQueued = true;
    queueMicrotask(() => {
      reloadQueued = false;
      if (isOpportunitiesTabActive()) void loadRecommendations();
    });
  };
  state.subscribeProvider(scheduleReload);
  state.subscribeAccount(scheduleReload);
}

/**
 * Check freshness and fire a background refresh if the cache is stale or cold.
 * Fire-and-forget: does not block the table render that already happened.
 *
 * Three toast stages:
 *   1. In-flight: "Refreshing recommendations…" (sticky until resolved)
 *   2. Success:   "Recommendations refreshed"
 *   3. Failure:   "Recommendations refresh failed: <message>"
 *
 * If the freshness response itself carries a last_collection_error, that is
 * surfaced as an error toast regardless of whether a new refresh fires (the
 * freshness banner that previously showed this is being removed in #284).
 */
export async function triggerAutoRefreshIfStale(
  onReload: () => Promise<void> = loadRecommendations,
): Promise<void> {
  let freshness;
  try {
    freshness = await getRecommendationsFreshness();
  } catch (err) {
    // Network failures getting freshness are non-critical — the table is
    // already rendered with the cached data; swallow silently.
    console.error('Failed to fetch recommendations freshness:', err);
    return;
  }

  const lastCollectedMs =
    freshness.last_collected_at === null
      ? null
      : new Date(freshness.last_collected_at).getTime();

  const isStale =
    lastCollectedMs === null ||
    !Number.isFinite(lastCollectedMs) ||
    Date.now() - lastCollectedMs > STALE_THRESHOLD_MS;
  if (freshness.last_collection_error && isStale) {
    showToast({
      message: `Last recommendations collection had errors: ${freshness.last_collection_error}`,
      kind: 'error',
    });
  }

  if (!isStale) return;

  // Dedup: if a refresh is already in flight, don't start a second one.
  if (autoRefreshInFlight) return;

  const inFlight = showToast({
    message: 'Refreshing recommendations…',
    kind: 'info',
    // No auto-dismiss timeout — the .then()/.catch() paths call
    // inFlight.dismiss() explicitly, so the toast stays visible until the
    // refresh actually settles (real refreshes can take 28 s+).
    timeout: null,
  });

  autoRefreshInFlight = refreshRecommendationsAPI()
    .then(() => {
      inFlight.dismiss();
      showToast({
        message: 'Recommendations refreshed',
        kind: 'success',
        timeout: 5_000,
      });
      // Reload UI data so users see fresh content. Caller passes a
      // page-specific reload (loadDashboard from Home, loadRecommend-
      // ations from Opportunities) so we don't force a recs render on
      // a page that doesn't display them.
      return onReload();
    })
    .catch((err: unknown) => {
      inFlight.dismiss();
      const message =
        err instanceof Error
          ? err.message
          : err !== null && err !== undefined
            ? String(err)
            : 'unknown error';
      console.error('Auto-refresh of recommendations failed:', err);
      showToast({
        message: `Recommendations refresh failed: ${message}`,
        kind: 'error',
      });
    })
    .finally(() => {
      autoRefreshInFlight = null;
    });
}

// Issue #481: URL <-> sort-state sync. State stays the source of truth;
// the URL is a serialised reflection so refresh / bookmark / share keep
// the user's chosen column + direction.
//
// VALID_SORT_COLUMNS gates URL params against the closed enumeration of
// column ids; invalid params are silently ignored (no toast, no crash).
const VALID_SORT_COLUMNS: ReadonlySet<state.RecommendationsColumnId> = new Set([
  'provider', 'account', 'service', 'resource_type', 'region',
  'count', 'term', 'payment', 'savings', 'upfront_cost',
  'monthly_cost', 'on_demand_monthly', 'effective_savings_pct',
]);

/**
 * If the URL carries `?sort=<col>&dir=<asc|desc>` with valid values, seed
 * state.setRecommendationsSort with the parsed pair. Idempotent: safe to
 * call on every loadRecommendations entry. Silently ignores anything
 * malformed so a stale bookmark with an old column name doesn't crash the
 * page or look broken.
 */
function readSortFromUrl(): void {
  if (typeof window === 'undefined' || !window.location) return;
  try {
    const params = new URLSearchParams(window.location.search);
    const col = params.get('sort');
    const dir = params.get('dir');
    if (!col || !dir) return;
    if (!VALID_SORT_COLUMNS.has(col as state.RecommendationsColumnId)) return;
    if (dir !== 'asc' && dir !== 'desc') return;
    state.setRecommendationsSort({
      column: col as state.RecommendationsSortColumn,
      direction: dir,
    });
  } catch {
    // URLSearchParams parsing should never throw on well-formed input, but
    // be defensive: a malformed location.search shouldn't break the page.
  }
}

/**
 * Mirror the active sort to the URL via history.replaceState so the user
 * can refresh / bookmark / share without losing it. Uses replaceState (not
 * pushState) so the back button isn't polluted by every header click.
 */
function writeSortToUrl(sort: state.RecommendationsSort): void {
  if (typeof window === 'undefined' || !window.location || !window.history) return;
  try {
    const url = new URL(window.location.href);
    url.searchParams.set('sort', sort.column);
    url.searchParams.set('dir', sort.direction);
    window.history.replaceState(window.history.state, '', url.toString());
  } catch {
    // history.replaceState can throw on file:// URLs / iframes with a
    // different origin; failing silently is the right call (sort still
    // applies in-memory, just not persisted across this refresh).
  }
}

/**
 * Load recommendations
 */
export async function loadRecommendations(): Promise<void> {
  // Issue #481: seed sort state from the URL before the first render so a
  // refreshed / bookmarked page renders with the user's chosen sort, not
  // the module default ("savings desc"). Idempotent on subsequent reloads.
  readSortFromUrl();

  // Issue #344 T3: skeleton rows for the recommendations table so the
  // panel reads as "loading" instead of staying blank while the
  // (potentially multi-second) Promise.all resolves. 5 rows ≈ above-
  // the-fold count for the typical viewport; the column count is
  // derived from the live visible-columns set + 1 (the leading checkbox
  // column the table renders), so toggling Columns ▾ keeps the
  // skeleton row shape aligned with the eventual table.
  const listEl = document.getElementById('recommendations-list');
  if (listEl) {
    const skeletonCols = 1 + visibleColumns().length;
    showSkeletonRows(listEl, 5, skeletonCols);
  }

  try {
    // Provider + account_ids are still sent to the API as hints so the
    // backend stays bounded for big multi-cloud tenants. Service / Region /
    // numeric filters are pure client-side via applyColumnFilters.
    const accountIDs = state.getCurrentAccountIDs();
    const filters: api.RecommendationFilters = {
      provider: state.getCurrentProvider(),
      account_ids: accountIDs.length > 0 ? accountIDs : undefined,
    };

    const [data, accounts, cfgResponse] = await Promise.all([
      api.getRecommendations(filters) as unknown as RecommendationsResponse,
      api.listAccounts().catch(() => []),
      // issue #223: fetch GlobalConfig so per-cell variant selection and
      // bulk-toolbar defaults reflect the operator's configured preference.
      // Failure is siloed — a missing/unreachable config endpoint must not
      // block the recommendations load; we fall back to the module defaults.
      api.getConfig().catch(() => null),
    ]);
    // Populate the module-level GlobalConfig cache (issue #223).
    if (cfgResponse?.global) {
      const g = cfgResponse.global;
      const t = g.default_term;
      if (t === 1 || t === 3) cachedGlobalDefaultTerm = t;
      const validPayments: CompatPayment[] = ['all-upfront', 'partial-upfront', 'no-upfront', 'monthly'];
      if (g.default_payment && (validPayments as string[]).includes(g.default_payment)) {
        cachedGlobalDefaultPayment = g.default_payment as CompatPayment;
      }
      // cachedGlobalDefaultPayment is now read directly by loadBulkPurchaseState()
      // (issue #282 dropped the toolbar dropdown; no longer need to seed
      // defaultBulkPurchaseState — the module-level cache is the source of truth).
    }
    accountNamesCache = new Map(accounts.map(a => [a.id, a.name]));

    // Issue #463: gate the Opportunities list by the Settings →
    // General → Enabled Providers preference. The backend currently
    // returns all collected recs irrespective of `enabled_providers`,
    // so a user who toggled Azure off in Settings still sees Azure
    // rows on Opportunities. Apply a strict client-side filter on the
    // provider field — Settings is the source of truth.
    //
    // Permissive default: if `enabled_providers` is empty/undefined
    // (older configs or a tenant that never touched the toggles),
    // fall through with no filter rather than surfacing a blank
    // Opportunities page. The Settings load path renders the same
    // permissive default on the checkboxes (line ~2510).
    const enabledProviders = cfgResponse?.global?.enabled_providers;
    const rawRecs = (data.recommendations || []) as unknown as api.Recommendation[];
    const visibleByPreference: api.Recommendation[] = enabledProviders && enabledProviders.length > 0
      ? rawRecs.filter(r => enabledProviders.includes(r.provider as api.Provider))
      : rawRecs;

    state.setRecommendations(visibleByPreference);
    state.clearSelectedRecommendations();

    // Cache the API-derived summary so renderRecommendationsList can
    // recompute the savings card from the visible (post-filter) set on
    // every rerender — covers initial load, sort-header clicks, column-
    // filter commits, and the Clear-filters badge. Otherwise an active
    // filter would shrink the banner range under the table while leaving
    // the card pinned to the unfiltered totals — the same divergence
    // #272 was supposed to close.
    lastRecommendationsSummary = data.summary || {};
    renderRecommendationsList(visibleByPreference as unknown as LocalRecommendation[]);

    // Auto-refresh on page open (#284): check freshness and trigger an
    // async background refresh if the cache is cold or older than 24h.
    void triggerAutoRefreshIfStale();
  } catch (error) {
    console.error('Failed to load recommendations:', error);
    const list = document.getElementById('recommendations-list');
    if (list) {
      teardownSkeleton(list);
      const err = error as Error;
      list.innerHTML = `<p class="error">Failed to load recommendations: ${escapeHtml(err.message)}</p>`;
    }
  }
}

function renderRecommendationsSummary(
  _summary: RecommendationsSummary,
  recommendations: readonly LocalRecommendation[],
): void {
  const container = document.getElementById('recommendations-summary');
  if (!container) return;

  // All four summary cards (Total Recommendations, Potential Monthly
  // Savings, Total Upfront Cost, Payback Period) recompute client-side
  // from the same source on every list rerender (closes #279). The API-
  // derived `summary` is no longer read — it summed every (term, payment)
  // variant of every cell and overstated achievable totals by ~6× on a
  // typical fan-out, the same bug class #272 closed for the savings card
  // alone. Now extended to the full header.
  //
  // Selection narrowing: when the user has ticked ≥1 checkbox visible in
  // the table, the cards narrow to `selected ∩ visible`. Cleared selection
  // → cards snap back to the full visible set. The cards therefore reflect
  // exactly what a Purchase / Plan click would commit to.
  //
  // The unused `_summary` arg is retained so the call sites in
  // loadRecommendations / renderRecommendationsList don't have to change
  // shape; it can be removed when the field is also dropped from
  // RecommendationsSummary.
  const selected = state.getSelectedRecommendationIDs();
  // Narrow selection to visible rows only — if the user has ticked rows that
  // are currently hidden by a column filter, those rows must not drive the
  // summary cards (the cards should reflect what the user can actually see
  // and act on). An empty visible-intersection means no effective selection.
  const selectedVisible = recommendations.filter((r) => selected.has(r.id));
  const target: readonly LocalRecommendation[] = selectedVisible.length > 0
    ? selectedVisible
    : recommendations;
  const groups = groupRecsByCell(target);
  const plr = pageLevelRange(groups);
  const isSelectionView = selectedVisible.length > 0 && plr.cellCount > 0;

  // issue #319: scale savings by the active cost period.
  const period = state.getCostPeriod();
  const scaledSavingsMin = scaleCost(plr.savingsMin, period) ?? 0;
  const scaledSavingsMax = scaleCost(plr.savingsMax, period) ?? 0;
  // Use target.length (variant count) for the guard conditions so they
  // stay consistent with the new KPI value below (closes #748).
  const hasRecs = target.length > 0;
  const savingsText = hasRecs && plr.savingsMax > 0
    ? formatScaledRange(scaledSavingsMin, scaledSavingsMax, period)
    : formatCostForPeriod(0, period);
  const upfrontText = hasRecs && plr.upfrontMax > 0
    ? formatSavingsRange(plr.upfrontMin, plr.upfrontMax)
    : formatCurrency(0);
  const paybackText = hasRecs && plr.paybackMonthsMax > 0
    ? formatPaybackRange(plr.paybackMonthsMin, plr.paybackMonthsMax)
    : '0 months';
  // issue #748: count VARIANTS (= target.length), not cells (= plr.cellCount).
  // "Showing X of X" in the filter-status bar also counts variants, so both
  // numbers now agree on the same dataset.
  const variantCount = target.length;
  const countLabel = isSelectionView ? 'Selected Recommendations' : 'Total Recommendations';
  const savingsLabel = (() => {
    if (period === 'monthly') {
      return isSelectionView ? 'Selected Monthly Savings' : 'Potential Monthly Savings';
    }
    const sfx = periodSuffix(period);
    return isSelectionView ? `Selected Savings ${sfx}` : `Potential Savings ${sfx}`;
  })();
  const upfrontLabel = isSelectionView ? 'Selected Upfront Cost' : 'Total Upfront Cost';
  const paybackLabel = 'Payback Period';

  container.innerHTML = `
    <div class="card">
      <h3>${countLabel}</h3>
      <p class="value">${variantCount}</p>
    </div>
    <div class="card">
      <h3>${savingsLabel}</h3>
      <p class="value savings">${savingsText}</p>
    </div>
    <div class="card">
      <h3>${upfrontLabel}</h3>
      <p class="value">${upfrontText}</p>
    </div>
    <div class="card">
      <h3>${paybackLabel}</h3>
      <p class="value">${paybackText}</p>
    </div>
  `;
}

// Comparator extractors per column. Numeric columns return numbers
// (subtraction-based sort); string columns return strings (localeCompare-based
// sort). Bundle B extended this with the string columns so every visible data
// column is sortable.
//
// issue #319: `savings` and `monthly_cost` extractors are now period-aware —
// they scale by the active cost period so sort order reflects the displayed
// value. POSITIVE_INFINITY sentinel for null values is preserved; scaling
// Infinity by any finite factor still yields Infinity, so the "unknowns to
// the bottom" behaviour is unchanged across periods.
const SORTABLE_NUMERIC_COLUMNS: Record<string, (r: LocalRecommendation) => number> = {
  savings: (r) => {
    const period = state.getCostPeriod();
    return scaleCost(r.savings, period) ?? Number.POSITIVE_INFINITY;
  },
  upfront_cost: (r) => r.upfront_cost,
  // null monthly_cost means "data not provided by the provider API".
  // Use POSITIVE_INFINITY so unknown rows sort to the bottom in ascending
  // order (de-emphasised) and don't conflate with rows that have an explicit
  // $0 recurring charge (e.g. all-upfront commitments).
  monthly_cost: (r) => {
    const period = state.getCostPeriod();
    return scaleCost(r.monthly_cost, period) ?? Number.POSITIVE_INFINITY;
  },
  // onDemandMonthly returns null when the provider didn't report
  // on_demand_cost (or reported 0). POSITIVE_INFINITY is the null sentinel;
  // groupsInSortOrder keeps nullish rows last in both ascending and descending
  // sorts. The base value is monthly; scale it to the selected display period
  // so sorting matches what the user sees in the column.
  on_demand_monthly: (r) => {
    const period = state.getCostPeriod();
    return scaleCost(onDemandMonthly(r), period) ?? Number.POSITIVE_INFINITY;
  },
  // effectiveSavingsPct returns null for term=0 / on_demand=0 / null monthly_cost.
  // POSITIVE_INFINITY places null rows at the bottom in ascending order and
  // at the top in descending — the least surprising behaviour for a savings
  // column where "no data" rows should be de-emphasised.
  effective_savings_pct: (r) => effectiveSavingsPct(r) ?? Number.POSITIVE_INFINITY,
  count: (r) => r.count,
  term: (r) => r.term,
};

const SORTABLE_STRING_COLUMNS: Record<string, (r: LocalRecommendation) => string> = {
  provider: (r) => r.provider ?? '',
  account: (r) => accountNamesCache.get(r.cloud_account_id ?? '') ?? r.cloud_account_id ?? '',
  service: (r) => r.service ?? '',
  resource_type: (r) => r.resource_type ?? '',
  region: (r) => r.region ?? '',
  payment: (r) => r.payment ?? '',
};

// cellKey identifies the physical-resource cell a rec belongs to.
// After PR #195's per-(term, payment) fan-out, a single physical resource
// produces up to 6 alternative rec rows (2 terms × 3 payments). The
// `(provider, cloud_account_id, service, region, resource_type, engine)`
// prefix is the cell — recs sharing this prefix are alternatives, not
// additions. Same prefix the scheduler ID encoding uses
// (scheduler.go:856-858, PR #189) but without the (term, payment) suffix.
//
// Used by issue #224's radio enforcement: at most one variant per cell
// can be selected at any time.
function cellKey(rec: LocalRecommendation): string {
  return `${rec.provider}|${rec.cloud_account_id ?? ''}|${rec.service}|${rec.region}|${rec.resource_type}|${rec.engine ?? ''}`;
}

// issues #225 + #226: cell-grouping helpers.

/**
 * Group a flat recommendation list by physical-resource cell.
 * Returns a Map preserving insertion order of first-seen cell keys.
 * Exported for tests.
 */
export function groupRecsByCell(recs: readonly LocalRecommendation[]): Map<string, LocalRecommendation[]> {
  const groups = new Map<string, LocalRecommendation[]>();
  for (const r of recs) {
    const k = cellKey(r);
    const g = groups.get(k);
    if (g) {
      g.push(r);
    } else {
      groups.set(k, [r]);
    }
  }
  return groups;
}

/** Summary metrics for a single cell's variants. Exported for tests. */
export interface CellRangeSummary {
  savingsMin: number;
  savingsMax: number;
  upfrontMin: number;
  upfrontMax: number;
  termMin: number;
  termMax: number;
}

/**
 * Compute the min/max envelope across all variants of a cell.
 * Returns zeroed summary for an empty array (defensive; should not occur).
 * Exported for tests.
 */
export function cellSummary(variants: readonly LocalRecommendation[]): CellRangeSummary {
  if (variants.length === 0) {
    return { savingsMin: 0, savingsMax: 0, upfrontMin: 0, upfrontMax: 0, termMin: 0, termMax: 0 };
  }
  let savingsMin = variants[0]!.savings;
  let savingsMax = variants[0]!.savings;
  let upfrontMin = variants[0]!.upfront_cost;
  let upfrontMax = variants[0]!.upfront_cost;
  let termMin = variants[0]!.term;
  let termMax = variants[0]!.term;
  for (let i = 1; i < variants.length; i++) {
    const v = variants[i]!;
    if (v.savings < savingsMin) savingsMin = v.savings;
    if (v.savings > savingsMax) savingsMax = v.savings;
    if (v.upfront_cost < upfrontMin) upfrontMin = v.upfront_cost;
    if (v.upfront_cost > upfrontMax) upfrontMax = v.upfront_cost;
    if (v.term < termMin) termMin = v.term;
    if (v.term > termMax) termMax = v.term;
  }
  return { savingsMin, savingsMax, upfrontMin, upfrontMax, termMin, termMax };
}

/** Page-level totals for the summary header (closes #279). All ranges are
 * the cell-by-cell min/max sums — the user buys at most one variant per
 * cell, so the achievable totals are bounded by `sum(cell.{min,max})` not
 * by `sum(every variant)`. */
export interface PageLevelRange {
  savingsMin: number;
  savingsMax: number;
  upfrontMin: number;
  upfrontMax: number;
  /** payback months at the min-savings end of the range; clamped to 0
   * when both savings and upfront are 0 (nothing to pay back). */
  paybackMonthsMin: number;
  /** payback months at the max-savings end. */
  paybackMonthsMax: number;
  cellCount: number;
}

/**
 * Compute the page-level summary by summing per-cell min/max envelopes.
 * Used by both the summary cards and the (now-removed) banner; lives at
 * module scope so dashboard.ts can reuse it for the cross-page parity in
 * #279.
 *
 *   savingsMin       = sum of cellSummary.savingsMin
 *   savingsMax       = sum of cellSummary.savingsMax
 *   upfrontMin       = sum of cellSummary.upfrontMin (matches the variant
 *                      whose savingsMin was contributed)
 *   upfrontMax       = sum of cellSummary.upfrontMax
 *   paybackMonthsMin = sum of per-cell best-payback variant upfronts /
 *                      sum of their savings.  Each cell independently
 *                      picks the variant with the lowest upfront/savings
 *                      ratio. Using independent cross-extrema
 *                      (upfrontMin / savingsMax) is NOT attainable when a
 *                      cell's lowest-upfront variant ≠ its highest-savings
 *                      variant — it would imply buying one impossible
 *                      combination.  Full convolution (trying every
 *                      cross-cell combination) is O(variants^cells) and
 *                      impractical for pages with 30+ cells.  Per-cell
 *                      paired choice is O(cells × variants) and produces
 *                      bounds that match what a min-payback / max-payback
 *                      per-cell selection would actually commit to.
 *   paybackMonthsMax = same logic, each cell picks the worst-payback
 *                      variant.
 *
 * Exported for tests.
 */
export function pageLevelRange(groups: Map<string, LocalRecommendation[]>): PageLevelRange {
  let savingsMin = 0;
  let savingsMax = 0;
  let upfrontMin = 0;
  let upfrontMax = 0;
  // Payback accumulators: per-cell paired variant sums.
  let paybackBestUpfront = 0;
  let paybackBestSavings = 0;
  let paybackWorstUpfront = 0;
  let paybackWorstSavings = 0;
  for (const variants of groups.values()) {
    const s = cellSummary(variants);
    savingsMin += s.savingsMin;
    savingsMax += s.savingsMax;
    upfrontMin += s.upfrontMin;
    upfrontMax += s.upfrontMax;

    // For payback: pick the variant with the best (lowest) payback ratio for
    // the min-end, and the worst (highest) for the max-end. Treat savings ≤ 0
    // as Infinity payback so zero-savings variants sort to the worst end.
    let bestRatio = Infinity;
    let bestUpfront = 0;
    let bestSavings = 0;
    let worstRatio = -Infinity;
    let worstUpfront = 0;
    let worstSavings = 0;
    for (const v of variants) {
      const ratio = v.savings > 0 ? v.upfront_cost / v.savings : Infinity;
      if (ratio < bestRatio) {
        bestRatio = ratio;
        bestUpfront = v.upfront_cost;
        bestSavings = v.savings;
      }
      if (ratio > worstRatio) {
        worstRatio = ratio;
        worstUpfront = v.upfront_cost;
        worstSavings = v.savings;
      }
    }
    paybackBestUpfront += bestUpfront;
    paybackBestSavings += bestSavings;
    paybackWorstUpfront += worstUpfront;
    paybackWorstSavings += worstSavings;
  }
  // Clamp to 0 when total savings is non-positive to avoid Infinity / NaN.
  const paybackMonthsMin = paybackBestSavings > 0 ? paybackBestUpfront / paybackBestSavings : 0;
  const paybackMonthsMax = paybackWorstSavings > 0 ? paybackWorstUpfront / paybackWorstSavings : 0;
  return {
    savingsMin, savingsMax,
    upfrontMin, upfrontMax,
    paybackMonthsMin, paybackMonthsMax,
    cellCount: groups.size,
  };
}

// Fixed payment ordering for within-cell variant sort.
const PAYMENT_ORDER: Record<string, number> = {
  'no-upfront': 0,
  'partial-upfront': 1,
  'all-upfront': 2,
  monthly: 3,
};

/** Sort variants within a cell: term ASC, then payment in fixed order. */
function sortVariantsInCell(variants: LocalRecommendation[]): LocalRecommendation[] {
  return variants.slice().sort((a, b) => {
    const termDiff = a.term - b.term;
    if (termDiff !== 0) return termDiff;
    const pa = PAYMENT_ORDER[a.payment ?? ''] ?? 99;
    const pb = PAYMENT_ORDER[b.payment ?? ''] ?? 99;
    return pa - pb;
  });
}

/**
 * Per-column cell-score helper: collapse a cell's variants to ONE deterministic
 * sort key per (column, cell) pair. Closes #494 - the previous comparator used
 * `Math.max(...va.map(numericKey))` for every numeric column, which collapses
 * to the same value across all multi-variant cells after PR #195's per-(term,
 * payment) fan-out (every cell has both 1yr and 3yr variants → `Math.max` always
 * = 3 for `term`; every cell has at least one null monthly_cost or null pct →
 * `Math.max` always = POSITIVE_INFINITY) and produces apparently-random row
 * order.
 *
 * Strategy: for each affected column, pick the per-cell score that matches what
 * the user sees in the rendered summary row:
 *
 *   - `term`            → summary.termMin (then termMax as inner tiebreaker
 *                         via the returned tuple, encoded as `termMin * 100 +
 *                         termMax` since both are small ints)
 *   - `upfront_cost`    → summary.upfrontMin
 *   - `monthly_cost`    → min over non-null variants (best-case framing);
 *                         POSITIVE_INFINITY only when ALL variants are null
 *   - `effective_savings_pct` → max over non-null variants (highest pct is the
 *                               cell's best pitch); POSITIVE_INFINITY only
 *                               when ALL variants are null
 *   - `payment` (categorical) → PAYMENT_ORDER index of the variant
 *                               `sortVariantsInCell` orders first (lowest term,
 *                               then lowest PAYMENT_ORDER). Replaces the
 *                               previous alphabetic comparator which sorted
 *                               `all-upfront` before `no-upfront`, semantically
 *                               wrong.
 *
 * For all other numeric columns (savings, count, on_demand_monthly) the
 * established `Math.max` semantics are preserved: a savings table sorted by
 * "best per cell" is the established convention and #494 does not flag these.
 * For all other string columns (provider, account, service, resource_type,
 * region) the value is invariant across a cell's variants (it's part of
 * cellKey), so `va[0]` continues to work.
 *
 * Returned number/string is the score the caller should compare. The null
 * sentinel `POSITIVE_INFINITY` is preserved for numeric scores so the existing
 * "always sort all-null cells last" early-return continues to work.
 */
function cellScoreFor(
  column: string,
  variants: LocalRecommendation[],
): number | string {
  // Issue #768: do NOT vary the score by selection state. Passing selectedRecs
  // into the comparator caused rows to reorder on every checkbox toggle because
  // a selected variant's individual value differs from the cell-summary value
  // used for unselected cells. Sort order is always derived from the
  // deterministic cell-summary score below, independent of selection.

  const numericKey = SORTABLE_NUMERIC_COLUMNS[column];
  const stringKey = SORTABLE_STRING_COLUMNS[column];

  if (variants.length === 0) {
    // Defensive: empty cells should not occur post-groupRecsByCell, but if one
    // slips through return a sentinel that sorts last.
    return numericKey ? Number.POSITIVE_INFINITY : '';
  }

  // No selected variant: pick a deterministic per-cell score.
  if (column === 'term') {
    const s = cellSummary(variants);
    // Encode (termMin, termMax) into a single number for sort-key purposes:
    // small positive ints multiplied by 100 keep ordering identical to lexical
    // (termMin, termMax) tuple comparison without needing an array compare.
    return s.termMin * 100 + s.termMax;
  }
  if (column === 'upfront_cost') {
    const s = cellSummary(variants);
    // upfrontMin as the primary key; upfrontMax only matters as a much smaller
    // tiebreaker so divide by a large constant. Clamp the tiebreaker so it
    // can never overwhelm the primary key for realistic upfront ranges.
    return s.upfrontMin + s.upfrontMax / 1e12;
  }
  if (column === 'monthly_cost') {
    const period = state.getCostPeriod();
    const finite: number[] = [];
    for (const v of variants) {
      const scaled = scaleCost(v.monthly_cost, period);
      if (scaled != null) finite.push(scaled);
    }
    // Best-case framing: lowest recurring cost wins. All-null cells sink to
    // the bottom via the existing POSITIVE_INFINITY sentinel logic.
    return finite.length === 0 ? Number.POSITIVE_INFINITY : Math.min(...finite);
  }
  if (column === 'effective_savings_pct') {
    const finite: number[] = [];
    for (const v of variants) {
      const pct = effectiveSavingsPct(v);
      if (pct != null) finite.push(pct);
    }
    // Best-case framing: highest savings pct is the cell's best pitch.
    return finite.length === 0 ? Number.POSITIVE_INFINITY : Math.max(...finite);
  }
  if (column === 'payment' && stringKey) {
    // Re-categorise: sort by the same PAYMENT_ORDER index sortVariantsInCell
    // uses for within-cell ordering, applied to the first variant in that
    // canonical order. Stringified so the existing string-column comparator
    // path handles it.
    const first = sortVariantsInCell(variants)[0];
    return String(PAYMENT_ORDER[first?.payment ?? ''] ?? 99);
  }

  // All other numeric columns (savings, count, on_demand_monthly): preserve
  // the established Math.max-over-variants semantics. These columns are NOT
  // flagged by #494 and have a long history of working correctly under
  // "best per cell" framing for a savings table.
  if (numericKey) return Math.max(...variants.map(numericKey));

  // All other string columns (provider, account, service, region,
  // resource_type): the value is invariant across a cell's variants because
  // it's part of cellKey. The first variant suffices.
  if (stringKey) return stringKey(variants[0]!);

  // Unsortable column.
  return 0;
}

/**
 * Return cell keys in the active sort order.
 *
 * Closes #494. Replaces the `Math.max(...va.map(numericKey))` collapse with a
 * per-column score (see `cellScoreFor`) that produces a deterministic single
 * value per cell. Adds a stable tiebreaker on `cellKey` so genuinely-tied cells
 * render in a consistent order across renders / browsers.
 */
function groupsInSortOrder(
  groups: Map<string, LocalRecommendation[]>,
  sort: { column: string; direction: 'asc' | 'desc' },
): string[] {
  const direction = sort.direction === 'asc' ? 1 : -1;

  const keys = Array.from(groups.keys());
  return keys.slice().sort((ka, kb) => {
    const va = groups.get(ka)!;
    const vb = groups.get(kb)!;

    const scoreA = cellScoreFor(sort.column, va);
    const scoreB = cellScoreFor(sort.column, vb);

    if (typeof scoreA === 'number' && typeof scoreB === 'number') {
      // Nulls are encoded as POSITIVE_INFINITY. Always sort them last
      // regardless of direction so "no data" rows are de-emphasised in both
      // asc and desc.
      const aNullish = scoreA === Number.POSITIVE_INFINITY;
      const bNullish = scoreB === Number.POSITIVE_INFINITY;
      if (aNullish !== bNullish) return aNullish ? 1 : -1;
      // Both-nullish case: `Infinity - Infinity` would yield NaN and
      // `NaN !== 0` would short-circuit the cellKey tiebreaker below,
      // leaving two all-null cells in implementation-defined order.
      // Skip the numeric diff when both sides are sentinel-null so the
      // tiebreaker runs (matches the well-defined-order intent of #494).
      if (!aNullish) {
        const diff = (scoreA - scoreB) * direction;
        if (diff !== 0) return diff;
      }
    } else if (typeof scoreA === 'string' && typeof scoreB === 'string') {
      const diff = scoreA.localeCompare(scoreB) * direction;
      if (diff !== 0) return diff;
    }

    // Stable tiebreaker on cellKey, direction-invariant. Genuinely-tied cells
    // render in a deterministic order on every render and across browsers
    // (Array.prototype.sort stability varies historically; localeCompare on a
    // stable key sidesteps it entirely).
    return ka.localeCompare(kb);
  });
}

/** Format a savings range, collapsing "$X – $X" to "$X". Exported for use in dashboard.ts. */
export function formatSavingsRange(min: number, max: number): string {
  const lo = formatCurrency(min);
  const hi = formatCurrency(max);
  return lo === hi ? lo : `${lo} – ${hi}`;
}


/** Format a payback-months range, collapsing identical endpoints to a
 * single value and rounding to 1 decimal. Used by the Payback Period
 * summary card after #279 broadened it to a range. */
function formatPaybackRange(min: number, max: number): string {
  const lo = min.toFixed(1);
  const hi = max.toFixed(1);
  return lo === hi ? `${lo} months` : `${lo} – ${hi} months`;
}

/** Format a term range, collapsing "1yr – 1yr" to "1yr". */
function formatTermRange(min: number, max: number): string {
  const lo = formatTerm(min);
  const hi = formatTerm(max);
  return lo === hi ? lo : `${lo} – ${hi}`;
}

/**
 * Computes effective monthly savings by amortizing the upfront cost over the
 * term. Assumes rec.savings is the on-demand vs. discounted recurring delta
 * EXCLUDING upfront amortization (matches AWS Cost Explorer's
 * EstimatedMonthlySavingsAmount semantics; verify per-provider if adding
 * non-AWS sources).
 *
 * Edge case: term=0 clamps to 1yr to avoid division by zero (defensive
 * clamp consistent with the original pickBestVariantPerCell behaviour).
 */
export function effectiveMonthlySavings(r: LocalRecommendation): number {
  const monthsInTerm = Math.max(12, (r.term || 1) * 12);
  return r.savings - (r.upfront_cost / monthsInTerm);
}

/**
 * Computes effective savings as a percentage of the equivalent on-demand
 * monthly cost, amortizing the upfront cost over the term. Returns null when
 * the result would require division by zero (on_demand_monthly === 0).
 *
 * Denominator source (closes #274):
 *   1. If `r.on_demand_cost` is populated (non-null, > 0), use it directly
 *      — it's the canonical baseline straight from the cloud provider
 *      (Azure CostWithNoReservedInstances, AWS
 *      EstimatedMonthlyOnDemandCost).
 *   2. Otherwise fall back to reconstructing from
 *      `monthly_cost + savings + amortized_upfront`. This is what the
 *      frontend always did before #274 plumbed `on_demand_cost` through;
 *      it stays correct for cleanly-shaped data, but for Azure all-upfront
 *      recs where `monthly_cost = $0` the reconstructed denominator
 *      collapses to `savings + amortized` and inflates the percentage well
 *      past realistic ceilings (~30% real → 86% shown).
 *
 * Formula (numerator unchanged — assumes rec.savings excludes upfront
 * amortization, see effectiveMonthlySavings):
 *   amortized_upfront_per_month = upfront_cost / (term * 12)
 *   effective_monthly_savings   = savings - amortized_upfront_per_month
 *   effective_savings_pct       = (effective_monthly_savings / on_demand_monthly) * 100
 *
 * A negative result is valid data — it flags a rec where the upfront cost
 * outweighs the recurring savings over the term (anomaly signal).
 */
export function effectiveSavingsPct(r: LocalRecommendation): number | null {
  // Per acceptance criteria: term=0 is a data anomaly — render as em-dash.
  if (!r.term) return null;
  // monthly_cost === null means the provider API did not return a recurring
  // monthly breakdown. Without it we can only compute the formula when the
  // provider also gave us an explicit on_demand_cost; otherwise render as
  // em-dash rather than collapsing the denominator to savings alone (which
  // produces 100% / neg%).
  const hasOnDemand = r.on_demand_cost != null && r.on_demand_cost > 0;
  if (r.monthly_cost == null && !hasOnDemand) return null;
  // #323: for AWS rows the on_demand_cost field is the provider-canonical
  // denominator (EstimatedMonthlyOnDemandCost from Cost Explorer). When it
  // is absent the reconstruction formula (monthly_cost + savings + amortized)
  // diverges from the true on-demand baseline for RI/SP recs, producing
  // misleadingly high percentages. Return null so the UI renders "—" rather
  // than a silently-wrong value.
  if (r.provider === 'aws' && !hasOnDemand) return null;
  const monthsInTerm = r.term * 12;
  const amortized = r.upfront_cost / monthsInTerm;
  const effectiveSavings = r.savings - amortized;
  const onDemand = hasOnDemand
    ? (r.on_demand_cost as number)
    : (r.monthly_cost as number) + r.savings + amortized;
  if (onDemand === 0) return null;
  return (effectiveSavings / onDemand) * 100;
}

/**
 * Returns the provider-reported on-demand monthly cost (`r.on_demand_cost`)
 * directly. Issue #330 — earlier behaviour reconstructed the value from
 * `monthly_cost + savings + amortized_upfront`, which drifted from the
 * provider's billed price for rounding edge cases (Azure all-upfront RIs,
 * AWS Capacity Reservation discounts, partial-day proration). Aligning this
 * with `effectiveSavingsPct`'s `hasOnDemand` branch makes the column show
 * the same denominator the percentage column uses, so the two never disagree.
 *
 * Returns `null` when `on_demand_cost` is missing, undefined, or `0` —
 * cell renders `—` (same em-dash convention as the existing Monthly Cost
 * column for null `monthly_cost`).
 */
export function onDemandMonthly(r: LocalRecommendation): number | null {
  if (r.on_demand_cost != null && r.on_demand_cost > 0) {
    return r.on_demand_cost;
  }
  return null;
}

// ---------------------------------------------------------------------------
// Cost-period scaling (issue #319)
// ---------------------------------------------------------------------------

/** Conversion factors relative to a monthly base. */
const PERIOD_FACTOR: Record<CostPeriod, number> = {
  hourly:  1 / 720,   // 24 × 30 hrs/mo
  daily:   1 / 30,
  monthly: 1,
  yearly:  12,
};

/**
 * Scale a monthly cost value to the requested display period.
 * Returns null when input is null or undefined (preserves "—" rendering).
 * Exported for tests.
 */
export function scaleCost(monthly: number | null | undefined, period: CostPeriod): number | null {
  if (monthly == null) return null;
  return monthly * PERIOD_FACTOR[period];
}

/** Number of decimal places to use when formatting a scaled cost value. */
const PERIOD_DECIMALS: Record<CostPeriod, number> = {
  hourly:  4,
  daily:   2,
  monthly: 2,
  yearly:  0,
};

/**
 * Format a monthly cost value scaled to the requested display period.
 * Returns "—" for null/undefined input.
 * Uses period-appropriate decimal precision.
 * Exported for tests.
 */
export function formatCostForPeriod(monthly: number | null | undefined, period: CostPeriod): string {
  const scaled = scaleCost(monthly, period);
  if (scaled === null) return '—';
  // For the non-monthly periods we want explicit per-period decimal
  // precision (PERIOD_DECIMALS), so we side-step formatCurrency (which
  // uses CURRENCY_DEFAULT_DIGITS for any caller that doesn't override).
  // Monthly keeps the existing formatCurrency behaviour for backward
  // compatibility (and to stay in lock-step with the rest of the
  // dashboard's monthly $ formatting).
  if (period === 'monthly') return formatCurrency(scaled);
  return `$${scaled.toFixed(PERIOD_DECIMALS[period])}`;
}

/** Human-readable suffix label for a period (used in column headers). */
export function periodSuffix(period: CostPeriod): string {
  switch (period) {
    case 'hourly':  return '/ hr';
    case 'daily':   return '/ day';
    case 'monthly': return '/ mo';
    case 'yearly':  return '/ yr';
  }
}

/**
 * Format a period-scaled savings range using period-appropriate precision.
 * Used for the summary cards and cell-level range displays (issue #319).
 * Collapses "$X – $X" to "$X".
 */
function formatScaledRange(min: number, max: number, period: CostPeriod): string {
  const lo = period === 'monthly' ? formatCurrency(min) : `$${min.toFixed(PERIOD_DECIMALS[period])}`;
  const hi = period === 'monthly' ? formatCurrency(max) : `$${max.toFixed(PERIOD_DECIMALS[period])}`;
  return lo === hi ? lo : `${lo} – ${hi}`;
}

// pickBestVariantPerCell collapses a list of recs to one rec per cell,
// preferring the variant matching resolved GlobalConfig.DefaultTerm +
// DefaultPayment, then falling back to the highest effective monthly savings.
//
// issue #223: tiebreaker is "matches resolved GlobalConfig.DefaultTerm +
// DefaultPayment". If no variant in a cell matches the configured defaults,
// fall back to the variant with the highest effective monthly savings
// (the original #224 behaviour).
//
// Effective savings (fallback metric) amortizes the upfront cost across
// the term:
//   effective = savings - (upfront_cost / (term * 12))
//
// Two `(savings, upfront_cost, term)` tuples that look identical on raw
// `savings` can score very differently on the amortized number — e.g. a
// 3yr all-upfront commitment with a large lump-sum upfront has a much
// lower effective monthly savings than a no-upfront variant with the
// same headline savings. Picking by amortization picks the variant
// that's actually best for the operator's wallet over the term.
//
// Used by issue #224's `select-all` handler. Sibling issue #223 will
// replace this tiebreaker with "matches resolved GlobalConfig.DefaultTerm
// + DefaultPayment" once that lands; until then, amortized savings is
// the right deterministic default.
// Exported so unit tests can exercise it directly.
export function pickBestVariantPerCell(recs: readonly LocalRecommendation[]): LocalRecommendation[] {
  const groups = new Map<string, LocalRecommendation[]>();
  for (const r of recs) {
    const k = cellKey(r);
    const group = groups.get(k);
    if (group) {
      group.push(r);
    } else {
      groups.set(k, [r]);
    }
  }

  const result: LocalRecommendation[] = [];
  for (const group of groups.values()) {
    // Prefer the variant matching the operator's configured defaults.
    const configMatch = group.find(
      (r) => r.term === cachedGlobalDefaultTerm && r.payment === cachedGlobalDefaultPayment,
    );
    if (configMatch) {
      result.push(configMatch);
      continue;
    }
    // No config-matching variant in this cell: fall back to highest effective savings.
    let best = group[0]!;
    for (let i = 1; i < group.length; i++) {
      if (effectiveMonthlySavings(group[i]!) > effectiveMonthlySavings(best)) best = group[i]!;
    }
    result.push(best);
  }
  return result;
}

// ---------------------------------------------------------------------------
// COLUMN_DEFS — single source of truth for the recommendations table columns.
//
// Order here matches the rendered column order (left to right), excluding the
// leading checkbox column which is always visible and never sortable/filterable.
//
// Both the table header (<th> generation) and the data rows (<td> generation)
// derive from this array so adding a new column only requires one edit here.
//
// `kind` drives the column-filter popover: 'numeric' → text input with
// comparison operators; 'categorical' → checkbox list of distinct values.
//
// `label` is the canonical (monthly-period) header. The 3 period-varying
// cost columns (`savings`, `monthly_cost`, `on_demand_monthly`) are handled
// separately by `getColumnLabel` per-period, so their entry here is only
// used as the data-attribute / fallback label, not the rendered <th>.
// ---------------------------------------------------------------------------
export interface ColumnDef {
  key: state.RecommendationsColumnId;
  label: string;
  kind: 'numeric' | 'categorical';
  // Issue #480: direction applied on the first click of a previously-
  // unsorted column. Text columns and most numerics get 'asc' (A→Z, low →
  // high) per platform convention. Two exceptions stay 'desc': `savings`
  // (savings tables open "biggest wins first") and `on_demand_monthly`
  // (per QA, current behaviour reads correctly there). Subsequent
  // clicks on the active column still toggle desc <-> asc regardless of
  // this default.
  defaultSortDirection?: 'asc' | 'desc';
}

export const COLUMN_DEFS: readonly ColumnDef[] = [
  { key: 'provider',              label: 'Provider',          kind: 'categorical' },
  { key: 'account',               label: 'Account',           kind: 'categorical' },
  { key: 'service',               label: 'Service',           kind: 'categorical' },
  { key: 'resource_type',         label: 'Resource Type',     kind: 'categorical' },
  { key: 'region',                label: 'Region',            kind: 'categorical' },
  { key: 'count',                 label: 'Count',             kind: 'numeric'     },
  { key: 'term',                  label: 'Term',              kind: 'categorical' },
  { key: 'payment',               label: 'Payment',           kind: 'categorical' },
  { key: 'savings',               label: 'Monthly Savings',   kind: 'numeric',     defaultSortDirection: 'desc' },
  { key: 'upfront_cost',          label: 'Upfront Cost',      kind: 'numeric'     },
  { key: 'monthly_cost',          label: 'Monthly Cost',      kind: 'numeric'     },
  { key: 'on_demand_monthly',     label: 'On-Demand Monthly', kind: 'numeric',     defaultSortDirection: 'desc' },
  { key: 'effective_savings_pct', label: 'Effective %',       kind: 'numeric'     },
];

// Issue #480: per-column default sort direction. Defaults to 'asc' unless
// the COLUMN_DEFS entry overrides it. Looked up by sort-header onActivate
// when transitioning from a different sort column.
function defaultSortDirectionFor(col: state.RecommendationsColumnId): 'asc' | 'desc' {
  const def = COLUMN_DEFS.find((c) => c.key === col);
  return def?.defaultSortDirection ?? 'asc';
}

// Static labels for columns whose header is period-invariant. Derived from
// COLUMN_DEFS so the source-of-truth still drives what's displayed; the
// 3 period-varying cost columns are filtered out and handled by
// getColumnLabel's switch instead.
const PERIOD_VARYING_COLUMNS = new Set<string>(['savings', 'monthly_cost', 'on_demand_monthly']);
const STATIC_COLUMN_LABELS: Record<string, string> = Object.fromEntries(
  COLUMN_DEFS
    .filter((c) => !PERIOD_VARYING_COLUMNS.has(c.key))
    .map((c) => [c.key, c.label]),
);

/**
 * Return the human-readable column header for `column` given the active
 * `period`. Cost-bearing columns (`savings`, `monthly_cost`,
 * `on_demand_monthly`) update their label to reflect the active period;
 * all other columns are period-invariant.
 */
function getColumnLabel(column: string, period: CostPeriod): string {
  switch (column) {
    case 'savings': {
      const suffixes: Record<CostPeriod, string> = {
        hourly: 'Savings / hr', daily: 'Savings / day', monthly: 'Monthly Savings', yearly: 'Savings / yr',
      };
      return suffixes[period];
    }
    case 'monthly_cost': {
      const suffixes: Record<CostPeriod, string> = {
        hourly: 'Cost / hr', daily: 'Cost / day', monthly: 'Monthly Cost', yearly: 'Cost / yr',
      };
      return suffixes[period];
    }
    case 'on_demand_monthly': {
      // The base value is the reconstructed monthly on-demand baseline (#322);
      // scale the label to the same period as the displayed cell value so the
      // header stays consistent with the column's contents.
      const suffixes: Record<CostPeriod, string> = {
        hourly: 'On-Demand / hr', daily: 'On-Demand / day', monthly: 'On-Demand Monthly', yearly: 'On-Demand / yr',
      };
      return suffixes[period];
    }
    default:
      return STATIC_COLUMN_LABELS[column] ?? column;
  }
}

// Backward-compatible alias used by popover aria-labels and filter buttons.
// These always show the current-period label.
function columnLabel(column: string): string {
  return getColumnLabel(column, state.getCostPeriod());
}

function sortIndicator(column: string, active: string, direction: 'asc' | 'desc'): string {
  if (column !== active) return '<span class="sort-indicator" aria-hidden="true">\u2195</span>';
  return direction === 'asc'
    ? '<span class="sort-indicator active" aria-hidden="true">\u25B2</span>'
    : '<span class="sort-indicator active" aria-hidden="true">\u25BC</span>';
}

// sortedRecommendations was removed in issues #225/#226: group-level sorting
// via groupsInSortOrder() supersedes the flat-list sort. The same
// SORTABLE_NUMERIC_COLUMNS / SORTABLE_STRING_COLUMNS maps are reused there.

// parseNumericFilter and ParsedNumericFilter are now in lib/column-filters.ts
// (issue #166 extraction). Imported at the top of this file and re-exported
// so existing consumers that import from recommendations.ts keep working.

// Apply the per-column filters to a rec list. Routes through the shared
// generic applyColumnFilters from lib/column-filters (issue #166/#570).
//
// ANDs all column filters together. Categorical: row passes iff its column
// value (string-form, empty/null mapped to "") is in `values`. Numeric: row
// passes iff parseNumericFilter(expr).predicate accepts the value (skipped if
// parse failed — the popover's inline error tells the user).
//
// Account uses cloud_account_id for matching; Term uses String(r.term).
// All other categorical columns compare on the underlying string field.
//
// Issue #484: numeric predicates compare against the rounded display value so
// exact-match ("123.45") works for rows whose raw value rounds to the typed
// value, and ">N" / "<N" / "N..M" all behave consistently with what the user
// sees in the cell.
export function applyColumnFilters(
  recs: readonly LocalRecommendation[],
  filters: state.RecommendationsColumnFilters,
): LocalRecommendation[] {
  const period = state.getCostPeriod();
  return applyColumnFiltersLib<LocalRecommendation, state.RecommendationsColumnId>(
    recs,
    filters,
    {
      categorical: categoricalCellValue,
      numeric: (r, col) => roundForDisplay(numericCellValue(r, col), displayPrecision(col, period)),
    },
  );
}

function categoricalCellValue(r: LocalRecommendation, col: state.RecommendationsColumnId): string {
  switch (col) {
    case 'provider':       return r.provider ?? '';
    case 'account':        return r.cloud_account_id ?? '';
    case 'service':        return r.service ?? '';
    case 'resource_type':  return r.resource_type ?? '';
    case 'region':         return r.region ?? '';
    case 'term':           return r.term == null ? '' : String(r.term);
    case 'payment':        return r.payment ?? '';
    // Numeric columns shouldn't reach this branch; return empty for type-safety.
    case 'count':
    case 'savings':
    case 'upfront_cost':
    case 'monthly_cost':
    case 'on_demand_monthly':
    case 'effective_savings_pct':   return '';
  }
}

function numericCellValue(r: LocalRecommendation, col: state.RecommendationsColumnId): number {
  // issue #319: savings and monthly_cost filter predicates operate on the
  // scaled (displayed) value so a "< $1" filter at hourly does the right thing.
  const period = state.getCostPeriod();
  switch (col) {
    case 'count':                return r.count ?? 0;
    case 'savings':              return scaleCost(r.savings, period) ?? 0;
    case 'upfront_cost':         return r.upfront_cost ?? 0;
    // Return NaN for null monthly_cost so numeric filter predicates (e.g. "= 0")
    // don't match rows where the provider simply didn't report a monthly cost.
    case 'monthly_cost':         return scaleCost(r.monthly_cost, period) ?? Number.NaN;
    // Return NaN for null on_demand_monthly (missing or zero on_demand_cost — see
    // onDemandMonthly() for the contract) so any numeric predicate returns false
    // rather than coincidentally matching 0. Scale to the active period so a
    // numeric filter targets what the user sees.
    case 'on_demand_monthly':    return scaleCost(onDemandMonthly(r), period) ?? Number.NaN;
    // Return NaN for null effective_savings_pct so any numeric predicate
    // returns false rather than coincidentally matching 0.
    case 'effective_savings_pct': return effectiveSavingsPct(r) ?? Number.NaN;
    // Categorical columns shouldn't reach this branch; return NaN so any
    // numeric predicate returns false rather than coincidentally matching 0.
    case 'provider':
    case 'account':
    case 'service':
    case 'resource_type':
    case 'region':
    case 'term':
    case 'payment':       return Number.NaN;
  }
}

// Issue #484: number of decimal places the cell renders with for the active
// period. Filter predicates compare against this rounded value so a user
// typing the value they see (e.g. "123.45") matches the row that visibly
// shows that value, regardless of how many decimals the raw backend value
// has. Mirrors the precision used by formatCostForPeriod / formatCurrency /
// pctText so display and filter logic stay in sync.
//
// For currency columns we deliberately reuse `CURRENCY_DEFAULT_DIGITS` from
// utils.ts rather than hard-coding `0`: that constant is the single source
// of truth for `formatCurrency`'s default fraction digits, so if the
// dashboard ever switches to a 2-decimal default the filter precision will
// follow automatically instead of silently diverging (the bug #484 was
// meant to close).
export function displayPrecision(col: state.RecommendationsColumnId, period: CostPeriod): number {
  switch (col) {
    case 'count':
      return 0;
    case 'effective_savings_pct':
      // Header shows pct.toFixed(1): 1 decimal place.
      return 1;
    case 'savings':
    case 'monthly_cost':
    case 'on_demand_monthly':
      // formatCostForPeriod uses formatCurrency (CURRENCY_DEFAULT_DIGITS)
      // for monthly and toFixed(PERIOD_DECIMALS[period]) otherwise.
      return period === 'monthly' ? CURRENCY_DEFAULT_DIGITS : PERIOD_DECIMALS[period];
    case 'upfront_cost':
      // Always formatted via formatCurrency with default digits.
      return CURRENCY_DEFAULT_DIGITS;
    // Categorical columns never reach the numeric filter path; default
    // is irrelevant but match formatCurrency's default-digit count for safety.
    case 'provider':
    case 'account':
    case 'service':
    case 'resource_type':
    case 'region':
    case 'term':
    case 'payment':
      return CURRENCY_DEFAULT_DIGITS;
  }
}

// Issue #484: round `n` to `precision` decimals using the same half-up
// behaviour as Number.prototype.toFixed (the rendering path used by
// formatCurrency / formatCostForPeriod / toFixed in pctText). Returns NaN
// unchanged so missing data still fails every predicate (preserves the
// "NaN-as-missing" contract from numericCellValue).
function roundForDisplay(n: number, precision: number): number {
  if (!Number.isFinite(n)) return n;
  return Number(n.toFixed(precision));
}

// ---------------------------------------------------------------------------
// Column-filter popover (portal pattern)
//
// The popover element lives appended to document.body so it survives
// renderRecommendationsList's table re-render (which does container.innerHTML
// = buildListMarkup, destroying anything inside <th>). Module-scope state
// tracks which column id is currently open; the trigger DOM node is re-found
// by `[data-column="..."]` on every render (anchor re-bind).
//
// The popover STRUCTURE is built once on open; STATE (.checked / .value) is
// re-synced on every anchor re-bind from the latest column-filter state, EXCEPT
// when the input is document.activeElement (mid-typing protection).
// ---------------------------------------------------------------------------

// Derived from COLUMN_DEFS — numeric columns get a text-input filter; categoricals
// get a checkbox-list filter.  Kept as a Set for O(1) membership tests.
const NUMERIC_COLUMNS: ReadonlySet<state.RecommendationsColumnId> = new Set(
  COLUMN_DEFS.filter((c) => c.kind === 'numeric').map((c) => c.key),
);

interface PopoverState {
  column: state.RecommendationsColumnId;
  el: HTMLDivElement;
  // The categorical-popover checkboxes are keyed by their underlying filter
  // value (cloud_account_id for Account, "1"/"3" for Term, raw string
  // otherwise). Saved here so anchor re-bind can resync .checked from state.
  checkboxes: Map<string, HTMLInputElement>;
  // Numeric-popover input + error span for re-sync.
  input: HTMLInputElement | null;
  errorEl: HTMLElement | null;
  // Trigger lives in the table; rebound by `[data-column="..."]` on each
  // render so we don't hold a stale reference.
  triggerColumn: state.RecommendationsColumnId;
}

let openPopover: PopoverState | null = null;
// Document-level click-outside listener; attached once on first open and
// torn down on close.
let outsideClickHandler: ((e: MouseEvent) => void) | null = null;
let escKeyHandler: ((e: KeyboardEvent) => void) | null = null;
let scrollCloseHandler: ((e: Event) => void) | null = null;
let resizeHandler: (() => void) | null = null;

function getColumnTriggerButton(column: state.RecommendationsColumnId): HTMLButtonElement | null {
  return document.querySelector<HTMLButtonElement>(
    `th .column-filter-btn[data-column="${column}"]`,
  );
}

function positionPopover(popover: HTMLElement, anchor: HTMLElement): void {
  const rect = anchor.getBoundingClientRect();
  // Show, then measure (popover may be display:none initially).
  popover.style.display = 'block';
  const popRect = popover.getBoundingClientRect();
  const margin = 8;

  // Vertical: prefer below, flip above on overflow.
  let top = rect.bottom + 4;
  if (top + popRect.height > window.innerHeight - margin) {
    top = Math.max(margin, rect.top - popRect.height - 4);
  }

  // Horizontal: clamp right edge.
  let left = rect.left;
  if (left + popRect.width > window.innerWidth - margin) {
    left = Math.max(margin, window.innerWidth - margin - popRect.width);
  }

  popover.style.position = 'absolute';
  popover.style.top = `${top + window.scrollY}px`;
  popover.style.left = `${left + window.scrollX}px`;
}

function distinctValuesForColumn(
  recs: readonly LocalRecommendation[],
  column: state.RecommendationsColumnId,
): string[] {
  // Numeric columns don't get a checkbox list, but we still call this for
  // categorical columns only.
  const seen = new Set<string>();
  for (const r of recs) {
    seen.add(categoricalCellValue(r, column));
  }
  return Array.from(seen).sort((a, b) => {
    if (a === '' && b !== '') return -1; // (empty) first
    if (a !== '' && b === '') return 1;
    return a.localeCompare(b);
  });
}

function categoricalDisplayLabel(
  column: state.RecommendationsColumnId,
  value: string,
): string {
  if (value === '') return '(empty)';
  if (column === 'account') {
    return accountNamesCache.get(value) || value;
  }
  if (column === 'term') {
    const n = Number(value);
    return Number.isFinite(n) ? formatTerm(n) : value;
  }
  if (column === 'provider') {
    return providerDisplayName(value);
  }
  return value;
}

// Build the popover DOM for a given column. Categorical: checkbox list with
// (All) tri-state + Clear footer. Numeric: free-text expression input with
// inline error and Clear footer.
function buildPopoverContent(
  column: state.RecommendationsColumnId,
  recs: readonly LocalRecommendation[],
): { el: HTMLDivElement; checkboxes: Map<string, HTMLInputElement>; input: HTMLInputElement | null; errorEl: HTMLElement | null } {
  const popover = document.createElement('div');
  popover.className = 'column-filter-popover';
  popover.setAttribute('role', 'dialog');
  popover.setAttribute('aria-modal', 'false');

  const headingId = `column-filter-heading-${column}`;
  popover.setAttribute('aria-labelledby', headingId);

  const heading = document.createElement('h3');
  heading.id = headingId;
  heading.className = 'column-filter-heading';
  heading.textContent = `Filter ${columnLabel(column)}`;
  popover.appendChild(heading);

  const checkboxes = new Map<string, HTMLInputElement>();
  let input: HTMLInputElement | null = null;
  let errorEl: HTMLElement | null = null;
  // commitAllRef is set inside the categorical else-branch so the Clear
  // button handler (which lives outside that branch) can call commitAll()
  // and thereby invoke updateAllTriState() — fixing issue #700.
  let commitAllRef: ((target: boolean) => void) | null = null;

  if (NUMERIC_COLUMNS.has(column)) {
    const label = document.createElement('label');
    label.className = 'column-filter-numeric-label';
    label.textContent = 'Expression';
    input = document.createElement('input');
    input.type = 'text';
    input.className = 'column-filter-numeric-input';
    input.placeholder = 'e.g. >100, 50..200, 5';
    input.setAttribute('aria-describedby', `column-filter-error-${column}`);
    label.appendChild(input);
    popover.appendChild(label);

    errorEl = document.createElement('div');
    errorEl.id = `column-filter-error-${column}`;
    errorEl.className = 'column-filter-error';
    errorEl.setAttribute('role', 'status');
    popover.appendChild(errorEl);

    const commit = (): void => {
      const expr = input!.value.trim();
      if (expr === '') {
        state.setRecommendationsColumnFilter(column, null);
        errorEl!.textContent = '';
        rerenderRecommendations();
        return;
      }
      const parsed = parseNumericFilter(expr);
      if (!parsed.ok) {
        errorEl!.textContent = parsed.error;
        return;
      }
      errorEl!.textContent = '';
      state.setRecommendationsColumnFilter(column, { kind: 'expr', expr });
      rerenderRecommendations();
    };
    input.addEventListener('blur', commit);
    input.addEventListener('keydown', (e) => {
      if (e.key === 'Enter') {
        e.preventDefault();
        commit();
      }
    });
  } else {
    const distinct = distinctValuesForColumn(recs, column);

    // (All) tri-state checkbox at the top.
    const allLabel = document.createElement('label');
    allLabel.className = 'column-filter-all';
    const allBox = document.createElement('input');
    allBox.type = 'checkbox';
    allBox.dataset['role'] = 'all';
    allLabel.appendChild(allBox);
    const allText = document.createElement('span');
    allText.textContent = '(All)';
    allLabel.appendChild(allText);
    popover.appendChild(allLabel);

    // Issue #137: when the service column popover lists 2+ SP plan-type
    // slugs, render a second tri-state row "All Savings Plans" between
    // (All) and the individual list. PR #123 split the single
    // 'savings-plans' option into per-plan-type slugs, removing the
    // one-click "filter to all SP recommendations" affordance — this
    // restores it as a group-level toggle scoped to SP slugs only.
    // Per-plan-type checkboxes remain individually selectable for
    // narrowing.
    const spSlugs: string[] = column === 'service'
      ? distinct.filter((v) => isSavingsPlanService(v))
      : [];
    let spBox: HTMLInputElement | null = null;
    if (spSlugs.length >= 2) {
      const spLabel = document.createElement('label');
      spLabel.className = 'column-filter-group';
      spBox = document.createElement('input');
      spBox.type = 'checkbox';
      spBox.dataset['role'] = 'sp-group';
      spLabel.appendChild(spBox);
      const spText = document.createElement('span');
      spText.textContent = 'All Savings Plans';
      spLabel.appendChild(spText);
      popover.appendChild(spLabel);
    }

    const list = document.createElement('div');
    list.className = 'column-filter-list';
    for (const value of distinct) {
      const itemLabel = document.createElement('label');
      itemLabel.className = 'column-filter-item';
      const cb = document.createElement('input');
      cb.type = 'checkbox';
      cb.dataset['value'] = value;
      itemLabel.appendChild(cb);
      const text = document.createElement('span');
      text.textContent = categoricalDisplayLabel(column, value);
      itemLabel.appendChild(text);
      list.appendChild(itemLabel);
      checkboxes.set(value, cb);
    }
    popover.appendChild(list);

    const updateAllTriState = (): void => {
      const total = checkboxes.size;
      let checked = 0;
      checkboxes.forEach((cb) => { if (cb.checked) checked++; });
      allBox.indeterminate = checked > 0 && checked < total;
      allBox.checked = checked === total && total > 0;
    };

    // SP-group tri-state mirrors updateAllTriState but is scoped to the
    // savings-plans-* boxes only. checked = every SP box ticked,
    // indeterminate = some-but-not-all, unchecked = none.
    const updateSPTriState = (): void => {
      if (!spBox) return;
      let checked = 0;
      for (const slug of spSlugs) {
        const cb = checkboxes.get(slug);
        if (cb && cb.checked) checked++;
      }
      spBox.indeterminate = checked > 0 && checked < spSlugs.length;
      spBox.checked = checked === spSlugs.length && spSlugs.length > 0;
    };

    // Individual-checkbox commit (issue #482):
    //   - N=size selected → null (no narrowing applied; all values pass)
    //   - 0<=N<size → {set, selected}; N=0 explicitly stores an empty
    //     allow-list so unchecking the last value reaches the same
    //     zero-row state as the (All) checkbox and the Clear button,
    //     rather than snapping back to "all checked".
    const commit = (): void => {
      const selected: string[] = [];
      checkboxes.forEach((cb, value) => { if (cb.checked) selected.push(value); });
      if (selected.length === checkboxes.size) {
        state.setRecommendationsColumnFilter(column, null);
      } else {
        state.setRecommendationsColumnFilter(column, { kind: 'set', values: selected });
      }
      updateAllTriState();
      updateSPTriState();
      rerenderRecommendations();
    };

    // (All) and Clear use commitAll(target) directly:
    //   - target=true → no narrowing (null); resync renders all individual
    //     boxes checked. Matches issue #482's "All selects every value".
    //   - target=false → explicit empty allow-list ({set, []}), table shows
    //     0 rows. Matches issue #482's requirement that Clear/uncheck-All is
    //     distinct from no-filter.
    const commitAll = (target: boolean): void => {
      checkboxes.forEach((cb) => { cb.checked = target; });
      if (target) {
        state.setRecommendationsColumnFilter(column, null);
      } else {
        state.setRecommendationsColumnFilter(column, { kind: 'set', values: [] });
      }
      updateAllTriState();
      updateSPTriState();
      rerenderRecommendations();
    };
    // Expose commitAll to the Clear button handler outside this else-branch.
    commitAllRef = commitAll;

    checkboxes.forEach((cb) => {
      cb.addEventListener('change', commit);
    });
    allBox.addEventListener('change', () => {
      // Browser resolves indeterminate to checked on click, so allBox.checked
      // after the click is the desired target.
      commitAll(allBox.checked);
    });
    if (spBox) {
      spBox.addEventListener('change', () => {
        // Clicking SP tri-state ticks ALL SP boxes when transitioning
        // from unchecked/indeterminate → checked, and unticks all SP
        // boxes when transitioning checked → unchecked. The browser
        // resolves indeterminate to checked on click, so spBox.checked
        // after the click is the desired target state for SP boxes.
        const target = spBox!.checked;
        for (const slug of spSlugs) {
          const cb = checkboxes.get(slug);
          if (cb) cb.checked = target;
        }
        commit();
      });
    }
  }

  // Footer with Clear button.
  const footer = document.createElement('div');
  footer.className = 'column-filter-footer';
  const clearBtn = document.createElement('button');
  clearBtn.type = 'button';
  clearBtn.className = 'column-filter-clear';
  clearBtn.textContent = 'Clear';
  clearBtn.addEventListener('click', () => {
    if (input) {
      // Numeric column: Clear drops the expression entirely (no filter).
      state.setRecommendationsColumnFilter(column, null);
      input.value = '';
      if (errorEl) errorEl.textContent = '';
      rerenderRecommendations();
    } else {
      // Issue #482: Clear on a categorical filter sets an explicit empty
      // allow-list rather than null, so it's distinguishable from "no
      // filter applied" (which renders as all-checked). The popover's
      // checkboxes flip unchecked; the table renders 0 rows.
      // Issue #700: call commitAllRef(false) (the same as commitAll(false)
      // inside the categorical branch) so updateAllTriState() is invoked and
      // the (All) checkbox reflects the cleared state. commitAllRef also
      // calls rerenderRecommendations() internally.
      commitAllRef?.(false);
    }
  });
  footer.appendChild(clearBtn);
  popover.appendChild(footer);

  return { el: popover, checkboxes, input, errorEl };
}

// Re-sync popover .checked / .value from current filter state, except when
// the active element is the numeric input (mid-typing protection).
function resyncOpenPopover(): void {
  if (!openPopover) return;
  const f = state.getRecommendationsColumnFilters()[openPopover.column];
  if (openPopover.input) {
    if (document.activeElement !== openPopover.input) {
      const expr = f && f.kind === 'expr' ? f.expr : '';
      openPopover.input.value = expr;
      if (openPopover.errorEl) openPopover.errorEl.textContent = '';
    }
    return;
  }
  // Categorical: tick checkboxes whose value is in the active filter.
  // Issue #482: when no filter is set (f == null), render every checkbox
  // as checked and the (All) box as checked. This reflects the user
  // mental model that "no narrowing applied" means "every value is
  // included", distinct from an explicit empty allow-list ({set, []})
  // which renders every box as unchecked.
  if (f == null) {
    openPopover.checkboxes.forEach((cb) => { cb.checked = true; });
  } else {
    const values: ReadonlySet<string> = f.kind === 'set' ? new Set(f.values) : new Set();
    openPopover.checkboxes.forEach((cb, value) => {
      cb.checked = values.has(value);
    });
  }
  // Update (All) tri-state.
  const allBox = openPopover.el.querySelector<HTMLInputElement>('input[data-role="all"]');
  if (allBox) {
    const total = openPopover.checkboxes.size;
    let checked = 0;
    openPopover.checkboxes.forEach((cb) => { if (cb.checked) checked++; });
    allBox.indeterminate = checked > 0 && checked < total;
    allBox.checked = checked === total && total > 0;
  }
  // Issue #137: also resync the "All Savings Plans" tri-state when the
  // service column has the SP-group affordance rendered.
  const spBox = openPopover.el.querySelector<HTMLInputElement>('input[data-role="sp-group"]');
  if (spBox) {
    let spTotal = 0;
    let spChecked = 0;
    openPopover.checkboxes.forEach((cb, value) => {
      if (isSavingsPlanService(value)) {
        spTotal++;
        if (cb.checked) spChecked++;
      }
    });
    spBox.indeterminate = spChecked > 0 && spChecked < spTotal;
    spBox.checked = spChecked === spTotal && spTotal > 0;
  }
}

function attachPopoverGlobalListeners(): void {
  if (outsideClickHandler) return;
  outsideClickHandler = (e: MouseEvent): void => {
    if (!openPopover) return;
    const target = e.target as Node | null;
    if (!target) return;
    if (openPopover.el.contains(target)) return;
    // Any column-filter trigger button click is handled by the trigger's own
    // handler; don't double-close.
    if (target instanceof Element && target.closest('.column-filter-btn')) return;
    closePopover();
  };
  escKeyHandler = (e: KeyboardEvent): void => {
    if (!openPopover) return;
    if (e.key === 'Escape') {
      e.preventDefault();
      closePopover(true);
    }
  };
  scrollCloseHandler = (e: Event): void => {
    if (!openPopover) return;
    // Issue #483: ignore scrolls that originate inside the popover itself
    // (the outer popover scroll container or the inner column-filter-list).
    // Capture-phase scroll events from inner scrollables bubble up to the
    // window listener; without this guard, scrolling the popover's own
    // contents dismisses it before the user can reach values below the fold.
    const target = e.target as Node | null;
    if (target && openPopover.el.contains(target)) return;
    closePopover();
  };
  resizeHandler = (): void => {
    if (!openPopover) return;
    const trigger = getColumnTriggerButton(openPopover.column);
    if (!trigger) {
      closePopover();
      return;
    }
    positionPopover(openPopover.el, trigger);
  };
  document.addEventListener('mousedown', outsideClickHandler);
  document.addEventListener('keydown', escKeyHandler);
  // Use capture for scroll so we catch all scroll containers; passive for perf.
  window.addEventListener('scroll', scrollCloseHandler, { capture: true, passive: true });
  window.addEventListener('resize', resizeHandler);
}

function detachPopoverGlobalListeners(): void {
  if (outsideClickHandler) document.removeEventListener('mousedown', outsideClickHandler);
  if (escKeyHandler) document.removeEventListener('keydown', escKeyHandler);
  if (scrollCloseHandler) window.removeEventListener('scroll', scrollCloseHandler, { capture: true } as EventListenerOptions);
  if (resizeHandler) window.removeEventListener('resize', resizeHandler);
  outsideClickHandler = null;
  escKeyHandler = null;
  scrollCloseHandler = null;
  resizeHandler = null;
}

function openColumnPopover(column: state.RecommendationsColumnId, anchor: HTMLElement): void {
  // Defensive: if the popover element is no longer in the document (jest
  // DOM reset between tests, or an external script removed it), drop the
  // stale ref before applying the toggle/swap logic below.
  if (openPopover && !openPopover.el.isConnected) {
    detachPopoverGlobalListeners();
    openPopover = null;
  }
  // Toggle: clicking same trigger closes.
  if (openPopover && openPopover.column === column) {
    closePopover(true);
    return;
  }
  if (openPopover) closePopover();

  const recs = state.getRecommendations() as unknown as LocalRecommendation[];
  const built = buildPopoverContent(column, recs);
  document.body.appendChild(built.el);
  openPopover = {
    column,
    el: built.el,
    checkboxes: built.checkboxes,
    input: built.input,
    errorEl: built.errorEl,
    triggerColumn: column,
  };
  resyncOpenPopover();
  positionPopover(built.el, anchor);

  // ARIA wiring on the trigger.
  anchor.setAttribute('aria-expanded', 'true');

  attachPopoverGlobalListeners();

  // Move focus into the popover for keyboard users.
  const firstFocusable = built.input
    ?? built.el.querySelector<HTMLInputElement>('input[type="checkbox"]');
  firstFocusable?.focus();
}

function closePopover(restoreFocus = false): void {
  if (!openPopover) return;
  const { column, el } = openPopover;
  el.remove();
  openPopover = null;
  detachPopoverGlobalListeners();
  // ARIA cleanup on the trigger (if it still exists in the DOM).
  const trigger = getColumnTriggerButton(column);
  if (trigger) {
    trigger.setAttribute('aria-expanded', 'false');
    if (restoreFocus) trigger.focus();
  }
}

// Called by renderRecommendationsList after the table is rebuilt to re-anchor
// any open popover to the freshly-rendered trigger button. If the column was
// removed from the table somehow, close gracefully.
function rebindOpenPopoverAnchor(): void {
  if (!openPopover) return;
  const trigger = getColumnTriggerButton(openPopover.column);
  if (!trigger) {
    closePopover();
    return;
  }
  trigger.setAttribute('aria-expanded', 'true');
  positionPopover(openPopover.el, trigger);
  resyncOpenPopover();
}

// rerenderRecommendations triggers a full re-render from the latest loaded
// state. Used by popover commits and the Clear-filters badge so the table
// reflects new column-filter state immediately.
//
// #272 (CR follow-up): the summary card is recomputed inside
// renderRecommendationsList itself (against the post-filter visible set),
// so this helper doesn't need to call renderRecommendationsSummary
// separately — every entry to renderRecommendationsList covers it.
function rerenderRecommendations(): void {
  const loaded = state.getRecommendations() as unknown as LocalRecommendation[];
  renderRecommendationsList(loaded);
}

// Close the popover when the Opportunities tab loses .active, so the
// detached popover doesn't float over other tabs' content. Wired via a
// MutationObserver on the opportunities-tab element.
let recommendationsTabObserver: MutationObserver | null = null;
function ensureRecommendationsTabObserver(): void {
  if (recommendationsTabObserver) return;
  const tab = document.getElementById('opportunities-tab');
  if (!tab) return;
  recommendationsTabObserver = new MutationObserver(() => {
    if (tab.classList.contains('active')) return;
    if (openPopover) {
      closePopover();
    }
    closeVisibilityPopover();
  });
  recommendationsTabObserver.observe(tab, { attributes: true, attributeFilter: ['class'] });
}

// ---------------------------------------------------------------------------
// Column-visibility popover (issue #318)
//
// Separate state from the column-filter popover (openPopover / outsideClickHandler
// etc.) to avoid conflating the two interactions.  Shares the positionPopover()
// helper for positioning.
// ---------------------------------------------------------------------------

interface VisibilityPopoverState {
  el: HTMLDivElement;
  checkboxes: Map<state.RecommendationsColumnId, HTMLInputElement>;
  trigger: HTMLElement;
}

let openVisibilityPopover: VisibilityPopoverState | null = null;
let visOutsideClickHandler: ((e: MouseEvent) => void) | null = null;
let visEscKeyHandler: ((e: KeyboardEvent) => void) | null = null;

function closeVisibilityPopover(): void {
  if (!openVisibilityPopover) return;
  const { el, trigger } = openVisibilityPopover;
  el.remove();
  trigger.setAttribute('aria-expanded', 'false');
  openVisibilityPopover = null;
  if (visOutsideClickHandler) {
    document.removeEventListener('mousedown', visOutsideClickHandler);
    visOutsideClickHandler = null;
  }
  if (visEscKeyHandler) {
    document.removeEventListener('keydown', visEscKeyHandler);
    visEscKeyHandler = null;
  }
}

function openVisibilityPopover_(anchor: HTMLElement): void {
  // Close any open filter popover when visibility popover opens (only one popover
  // at a time keeps the UI from getting cluttered).
  closePopover();

  if (openVisibilityPopover) {
    closeVisibilityPopover();
    return; // toggle: second click on the button closes the popover
  }

  const popover = document.createElement('div');
  popover.className = 'column-visibility-popover';
  popover.setAttribute('role', 'dialog');
  popover.setAttribute('aria-label', 'Show/hide columns');
  popover.style.display = 'none';
  document.body.appendChild(popover);

  const title = document.createElement('p');
  title.className = 'column-visibility-title';
  title.textContent = 'Show/hide columns';
  popover.appendChild(title);

  const hidden = state.getHiddenColumns();
  const checkboxes = new Map<state.RecommendationsColumnId, HTMLInputElement>();

  for (const col of TOGGLEABLE_COLUMNS) {
    const row = document.createElement('label');
    row.className = 'column-visibility-row';

    const cb = document.createElement('input');
    cb.type = 'checkbox';
    cb.checked = !hidden.has(col.key);
    cb.setAttribute('aria-label', col.label);
    checkboxes.set(col.key, cb);

    cb.addEventListener('change', () => {
      const currentHidden = new Set(state.getHiddenColumns());
      if (cb.checked) {
        currentHidden.delete(col.key);
      } else {
        currentHidden.add(col.key);
      }
      state.setHiddenColumns(currentHidden);
      saveColumnVisibility(currentHidden);
      rerenderRecommendations();
    });

    row.appendChild(cb);
    row.appendChild(document.createTextNode(' ' + col.label));
    popover.appendChild(row);
  }

  openVisibilityPopover = { el: popover, checkboxes, trigger: anchor };
  anchor.setAttribute('aria-expanded', 'true');
  positionPopover(popover, anchor);

  // Keyboard: Escape closes.
  visEscKeyHandler = (e: KeyboardEvent): void => {
    if (e.key === 'Escape') {
      closeVisibilityPopover();
      anchor.focus();
    }
  };
  document.addEventListener('keydown', visEscKeyHandler);

  // Click-outside closes.
  visOutsideClickHandler = (e: MouseEvent): void => {
    const target = e.target as Node;
    if (!popover.contains(target) && !anchor.contains(target)) {
      closeVisibilityPopover();
    }
  };
  // Defer one tick so the current click event (which opened the popover) doesn't
  // immediately close it via the click-outside handler.
  const handler = visOutsideClickHandler;
  setTimeout(() => {
    if (handler && openVisibilityPopover && visOutsideClickHandler === handler) {
      document.addEventListener('mousedown', handler);
    }
  }, 0);
}

// mountColumnsButton mounts the "Columns ▾" button in the filter-status bar once
// and updates its label on subsequent calls to reflect hidden-column count.
function mountColumnsButton(bar: HTMLElement): void {
  let btn = bar.querySelector<HTMLButtonElement>('.column-visibility-btn');
  if (!btn) {
    btn = document.createElement('button');
    btn.type = 'button';
    btn.className = 'column-visibility-btn';
    btn.setAttribute('aria-haspopup', 'dialog');
    btn.setAttribute('aria-expanded', 'false');
    bar.appendChild(btn);
    btn.addEventListener('click', () => {
      openVisibilityPopover_(btn!);
    });
    btn.addEventListener('keydown', (e) => {
      if (e.key === 'Enter' || e.key === ' ') {
        e.preventDefault();
        openVisibilityPopover_(btn!);
      }
    });
  }
  // Update label to reflect hidden-column count.
  const hiddenCount = state.getHiddenColumns().size;
  btn.textContent = hiddenCount > 0 ? `Columns ▾ (${hiddenCount} hidden)` : 'Columns ▾';
  btn.setAttribute('aria-pressed', hiddenCount > 0 ? 'true' : 'false');

  // Re-sync checkboxes if the popover is open (in case a filter popover commit
  // changed visible state while the popover was open — rare but possible).
  if (openVisibilityPopover) {
    const currentHidden = state.getHiddenColumns();
    for (const [key, cb] of openVisibilityPopover.checkboxes) {
      cb.checked = !currentHidden.has(key);
    }
  }
}

// ---------------------------------------------------------------------------

// Render (or update) the filter-status bar: a "Clear filters (N)" button
// when at least one column filter is active, plus an aria-live region
// announcing visible vs loaded counts. Mounted above the table; survives
// container.innerHTML rewrites because it lives outside #recommendations-list.
function renderFilterStatusBar(loadedCount: number, visibleCount: number): void {
  const recsTab = document.getElementById('opportunities-tab');
  const list = document.getElementById('recommendations-list');
  if (!recsTab || !list) return;

  let bar = document.getElementById('recommendations-filter-status');
  if (!bar) {
    bar = document.createElement('div');
    bar.id = 'recommendations-filter-status';
    bar.className = 'recommendations-filter-status';
    list.parentNode?.insertBefore(bar, list);
  }

  // Build content fresh on every call. We set textContent on the live
  // region directly so screen readers fire announcements on actual changes.
  let badge = bar.querySelector<HTMLButtonElement>('.clear-filters');
  let live = bar.querySelector<HTMLElement>('.recommendations-filter-live');

  if (!live) {
    live = document.createElement('span');
    live.className = 'recommendations-filter-live';
    live.setAttribute('aria-live', 'polite');
    live.setAttribute('aria-atomic', 'true');
    bar.appendChild(live);
  }
  // Always update the live region (even when no filters active) so the
  // spoken count reflects state after every render. #279: when the user
  // has ticked ≥1 visible row, the line surfaces the selection count too —
  // same narrowing source-of-truth as the summary cards. Selections that
  // are filtered out of view are NOT counted here, matching the card
  // narrowing behaviour (only selected ∩ visible drives the UI).
  const selectedIDs = state.getSelectedRecommendationIDs();
  const visibleRecs = state.getVisibleRecommendations();
  const selectedVisibleCount = visibleRecs.filter((r) => selectedIDs.has(r.id)).length;
  live.textContent = selectedVisibleCount > 0
    ? `${selectedVisibleCount} selected · Showing ${visibleCount} of ${loadedCount} recommendations`
    : `Showing ${visibleCount} of ${loadedCount} recommendations`;

  const filters = state.getRecommendationsColumnFilters();
  const activeCount = Object.keys(filters).length;
  if (activeCount === 0) {
    if (badge) badge.remove();
  } else {
    if (!badge) {
      badge = document.createElement('button');
      badge.type = 'button';
      badge.className = 'clear-filters';
      badge.addEventListener('click', () => {
        state.clearAllRecommendationsColumnFilters();
        rerenderRecommendations();
      });
      bar.insertBefore(badge, live);
    }
    badge.textContent = `Clear filters (${activeCount})`;
  }

  // issues #225 + #226: Expand-All toggle button. Only render when there
  // are multi-variant cells to expand (lastVisibleGroupKeys has entries).
  // The button label flips between "Expand all" and "Collapse all"
  // depending on whether all known groups are currently expanded.
  // issues #225 + #226: Expand-All toggle. Only shown when multi-variant cells exist.
  let expandAllBtn = bar.querySelector<HTMLButtonElement>('.expand-all-toggle');
  const multiVariantGroups = lastVisibleGroupKeys.length > 0;
  if (!multiVariantGroups) {
    if (expandAllBtn) expandAllBtn.remove();
  } else {
    if (!expandAllBtn) {
      expandAllBtn = document.createElement('button');
      expandAllBtn.type = 'button';
      expandAllBtn.className = 'expand-all-toggle';
      bar.appendChild(expandAllBtn);
      expandAllBtn.addEventListener('click', () => {
        const allExpanded = lastVisibleGroupKeys.every((k) => expandedCells.has(k));
        if (allExpanded) {
          // Collapse all: clear every key that belongs to the current visible set.
          for (const k of lastVisibleGroupKeys) expandedCells.delete(k);
        } else {
          // Expand all: add every visible group key.
          for (const k of lastVisibleGroupKeys) expandedCells.add(k);
        }
        rerenderRecommendations();
      });
    }
    // Update label to reflect current state.
    const allExpanded = lastVisibleGroupKeys.every((k) => expandedCells.has(k));
    expandAllBtn.textContent = allExpanded ? 'Collapse all' : 'Expand all';
  }

  // issue #319: cost-period selector. Always mount regardless of group state.
  mountCostPeriodSelector(bar);

  // issue #318: "Columns ▾" button — mount-once, always visible.
  // Shows the column-visibility popover; label updates on re-render to
  // reflect any active hidden-column count.
  mountColumnsButton(bar);

  // Sort-hidden indicator: show a note when the active sort column is hidden
  // so users aren't confused by inexplicable row ordering (#318).
  let sortHiddenNote = bar.querySelector<HTMLSpanElement>('.sort-hidden-note');
  const sortCol = state.getRecommendationsSort().column;
  const hidden = state.getHiddenColumns();
  if (hidden.has(sortCol)) {
    if (!sortHiddenNote) {
      sortHiddenNote = document.createElement('span');
      sortHiddenNote.className = 'sort-hidden-note';
      bar.appendChild(sortHiddenNote);
    }
    // Use the period-aware label so the note mirrors the active period.
    const hiddenLabel = getColumnLabel(sortCol, state.getCostPeriod());
    sortHiddenNote.textContent = `(sorted by hidden column: ${hiddenLabel})`;
  } else {
    if (sortHiddenNote) sortHiddenNote.remove();
  }
}

/**
 * Mount (or re-sync) the cost-period dropdown inside the filter-status bar.
 * The dropdown is mounted once then kept in sync with state.getCostPeriod()
 * on subsequent calls. Changing the dropdown triggers a rerenderRecommendations
 * and persists the choice in localStorage.
 */
function mountCostPeriodSelector(bar: HTMLElement): void {
  let wrapper = bar.querySelector<HTMLElement>('.cost-period-selector-wrapper');
  if (!wrapper) {
    wrapper = document.createElement('span');
    wrapper.className = 'cost-period-selector-wrapper';

    const label = document.createElement('label');
    label.className = 'cost-period-selector-label';
    label.htmlFor = 'cost-period-select';
    label.textContent = 'Show costs: ';
    wrapper.appendChild(label);

    const select = document.createElement('select');
    select.id = 'cost-period-select';
    select.className = 'cost-period-select';
    select.setAttribute('aria-label', 'Cost display period');

    const options: Array<[CostPeriod, string]> = [
      ['hourly', 'Hourly'],
      ['daily', 'Daily'],
      ['monthly', 'Monthly'],
      ['yearly', 'Yearly'],
    ];
    for (const [value, text] of options) {
      const opt = document.createElement('option');
      opt.value = value;
      opt.textContent = text;
      select.appendChild(opt);
    }

    select.addEventListener('change', () => {
      const newPeriod = select.value as CostPeriod;
      state.setCostPeriod(newPeriod);
      rerenderRecommendations();
    });

    wrapper.appendChild(select);
    bar.appendChild(wrapper);
  }

  // Sync the selected option to the current period state (handles reload from localStorage).
  const select = wrapper.querySelector<HTMLSelectElement>('.cost-period-select');
  if (select) {
    select.value = state.getCostPeriod();
  }
}

// renderBulkToolbar was the old "N selected / Add to plan / Clear" pill that
// rendered above the table whenever any rows were selected. Bundle B folded
// that surface into the sticky bottom action box: the selection-summary text
// is in updateBottomActionBox, and the Create-Plan button in the bottom box
// supersedes the old "Add to plan". Function removed; tests for its DOM
// (bulk-count etc.) are gone too.
//
// For "Clear selection" affordance, see the bottom box's selection-summary
// text — when a selection exists the summary is followed by the row-checkbox
// columns (per-row deselect) and the all-selectAll checkbox in the table
// header (selectAll's third state clears).

// (Bundle B) selectedRecsFromVisible was inlined into resolvePurchaseTarget
// inside the bottom action box. The old helper had only one caller; folding
// keeps the action-target logic centralised in one place.


function providerDisplayName(provider: string): string {
  switch (provider.toLowerCase()) {
    case 'aws': return 'AWS';
    case 'azure': return 'Azure';
    case 'gcp': return 'GCP';
    default: return provider;
  }
}

// CR #253 finding: whitelist provider for CSS class injection.
// rec.provider comes from the API and is injected into class attributes via
// template literals. A non-whitelisted value falls back to '' (no badge class).
function providerBadgeClass(provider: string): string {
  const n = provider.toLowerCase();
  return n === 'aws' || n === 'azure' || n === 'gcp' ? n : '';
}


// Map raw payment_option values to display labels for the Payment column.
const PAYMENT_DISPLAY_LABELS: Record<string, string> = {
  'all-upfront':     'All Upfront',
  'partial-upfront': 'Partial Upfront',
  'no-upfront':      'No Upfront',
  'upfront':         'Upfront',
  'monthly':         'Monthly',
};

function formatPayment(payment: string | undefined): string {
  if (!payment) return '\u2014';
  return PAYMENT_DISPLAY_LABELS[payment] ?? payment;
}

// renderColumnCell renders a single <td> for the given column key.
// All column cell rendering is centralised here so buildVariantRowMarkup
// can iterate over COLUMN_DEFS (or a visibility-filtered subset in
// Commit 2) without knowing each column's HTML shape.
function renderColumnCell(key: state.RecommendationsColumnId, rec: LocalRecommendation, ctx: {
  accountName: string;
  badge: string;
  pct: number | null;
  pctClass: string;
  pctText: string;
  period: CostPeriod;
}): string {
  switch (key) {
    case 'provider':
      return `<td><span class="provider-badge ${providerBadgeClass(rec.provider)}">${escapeHtml(providerDisplayName(rec.provider))}</span></td>`;
    case 'account':
      return `<td>${escapeHtml(ctx.accountName)}</td>`;
    case 'service':
      return `<td><span class="service-badge">${escapeHtml(rec.service)}</span></td>`;
    case 'resource_type':
      return `<td title="${escapeHtml(rec.resource_type)}">${escapeHtml(rec.resource_type)}${rec.engine ? ` (${escapeHtml(rec.engine)})` : ''}${ctx.badge}</td>`;
    case 'region':
      return `<td>${escapeHtml(rec.region)}</td>`;
    case 'count':
      return `<td>${rec.count}</td>`;
    case 'term':
      return `<td>${formatTerm(rec.term)}</td>`;
    case 'payment':
      return `<td>${formatPayment(rec.payment)}</td>`;
    case 'savings':
      // issue #319: savings display value scales with the active cost period.
      return `<td class="savings">${formatCostForPeriod(rec.savings, ctx.period)}</td>`;
    case 'upfront_cost':
      // upfront_cost is one-time, not recurring \u2014 period-invariant.
      return `<td>${formatCurrency(rec.upfront_cost)}</td>`;
    case 'monthly_cost':
      // issue #319: monthly_cost display scales with period; null still renders as em-dash.
      return `<td>${formatCostForPeriod(rec.monthly_cost, ctx.period)}</td>`;
    case 'on_demand_monthly':
      // issue #319: on-demand baseline scales with period to stay consistent
      // with the savings + monthly_cost columns.
      return `<td>${formatCostForPeriod(onDemandMonthly(rec), ctx.period)}</td>`;
    case 'effective_savings_pct':
      return `<td${ctx.pctClass}>${ctx.pctText}</td>`;
  }
}

// Helper: render one variant row (a single LocalRecommendation) with optional
// indentation styling for multi-variant cells.
//
// `cols` defaults to all COLUMN_DEFS; Commit 2 (column visibility) passes a
// visibility-filtered subset so hidden columns are absent from the DOM.
function buildVariantRowMarkup(
  rec: LocalRecommendation,
  selectedRecs: ReadonlySet<string>,
  isNested: boolean,
  cols: readonly ColumnDef[] = COLUMN_DEFS,
): string {
  // issue #319: cost-bearing cells scale with the active period; resolved
  // once per row and threaded through ctx so renderColumnCell doesn't
  // re-read state on every cell.
  const period = state.getCostPeriod();
  const savingsClass = rec.savings > 1000 ? 'high-savings' : rec.savings > 100 ? 'medium-savings' : '';
  const isSelected = selectedRecs.has(rec.id);
  const accountName = rec.cloud_account_id ? (accountNamesCache.get(rec.cloud_account_id) || rec.cloud_account_id) : '\u2014';
  const badge = renderSuppressionBadge(rec);
  const recId = escapeHtml(rec.id);
  const pct = effectiveSavingsPct(rec);
  const pctClass = pct !== null && pct < 0 ? ' class="effective-pct-negative"' : '';
  const pctText = pct === null ? '\u2014' : pct.toFixed(1) + '%';
  const nestedClass = isNested ? ' rec-variant-row' : '';
  const cellCtx = { accountName, badge, pct, pctClass, pctText, period };
  return `
  <tr class="recommendation-row${nestedClass} ${savingsClass} ${isSelected ? 'selected' : ''}" data-rec-id="${recId}">
    <td class="checkbox-col">
      <input type="checkbox" data-rec-id="${recId}" ${isSelected ? 'checked' : ''} aria-label="Select recommendation">
    </td>
    ${cols.map((c) => renderColumnCell(c.key, rec, cellCtx)).join('')}
  </tr>`;
}


function buildListMarkup(
  recommendations: LocalRecommendation[],
  selectedRecs: ReadonlySet<string>,
  visibleCols: readonly ColumnDef[] = COLUMN_DEFS,
): string {
  const sort = state.getRecommendationsSort();
  const filters = state.getRecommendationsColumnFilters();
  const period = state.getCostPeriod();
  const filterBtn = (column: state.RecommendationsColumnId): string => {
    const active = filters[column] ? ' active' : '';
    const lbl = getColumnLabel(column, period);
    const label = filters[column] ? `Filter ${lbl} \u2014 currently active` : `Filter ${lbl}`;
    return `<button type="button" class="column-filter-btn${active}" data-column="${column}" aria-haspopup="dialog" aria-expanded="false" aria-label="${label}" title="${label}">\u26db</button>`;
  };
  const sortHeader = (column: state.RecommendationsColumnId): string => {
    const lbl = getColumnLabel(column, period);
    return `<th class="sortable" data-sort="${column}" tabindex="0" role="button" aria-label="Sort by ${lbl}"><span>${lbl}</span>${sortIndicator(column, sort.column, sort.direction)}${filterBtn(column)}</th>`;
  };

  // issues #225 + #226: group by cell, sort groups, then render.
  const groups = groupRecsByCell(recommendations);
  // Issue #768: sort order is selection-independent; selectedRecs is used only
  // for per-row rendering (checked state, CSS class) and the summary cards.
  const sortedKeys = groupsInSortOrder(groups, sort);

  // Update module-level group key cache for the Expand-All button.
  // Only multi-variant cells count — single-variant cells render flat with no chevron.
  lastVisibleGroupKeys = sortedKeys.filter((k) => (groups.get(k)?.length ?? 0) > 1);

  // The "Recommended range" banner that used to live here was redundant
  // with the Potential Monthly Savings card after #272 / #279 brought
  // the same range to the summary header. Removed (closes #278).

  // Summary row colspan: the big cell covers resource_type (always shown) + all
  // visible toggleable columns. The 3 fixed identity cells (provider, account,
  // service) always render separately, so this colspan = 1 + visible toggleable count.
  const visibleToggleableCols = visibleCols.filter((c) => TOGGLEABLE_COLUMN_KEYS.has(c.key));
  const summaryColspan = 1 + visibleToggleableCols.length;
  const visibleKeys = new Set(visibleCols.map((c) => c.key));

  // Build tbody rows: grouped for multi-variant cells, flat for single-variant.
  const rows: string[] = [];
  for (const key of sortedKeys) {
    const variants = groups.get(key)!;
    if (variants.length === 1) {
      // Single-variant: render flat, no group header, no indent.
      rows.push(buildVariantRowMarkup(variants[0]!, selectedRecs, false, visibleCols));
      continue;
    }

    // Multi-variant cell.
    const isExpanded = expandedCells.has(key);
    const summary = cellSummary(variants);
    const rep = variants[0]!;
    const accountName = rep.cloud_account_id ? (accountNamesCache.get(rep.cloud_account_id) || rep.cloud_account_id) : '\u2014';

    // Selected variant in this cell (if any) — used to show selected values on summary row.
    const selectedVariant = variants.find((r) => selectedRecs.has(r.id));

    // Summary row savings display: selected variant value if one is selected;
    // otherwise the range across all variants.
    // issue #319: scale savings by the active period.
    const sfxLabel = periodSuffix(period);
    const scaledSavingsMin = scaleCost(summary.savingsMin, period) ?? summary.savingsMin;
    const scaledSavingsMax = scaleCost(summary.savingsMax, period) ?? summary.savingsMax;
    const savingsDisplay = selectedVariant
      ? `${formatCostForPeriod(selectedVariant.savings, period)}${sfxLabel} <span class="rec-variants-count">(+${variants.length - 1} variants)</span>`
      : `${formatScaledRange(scaledSavingsMin, scaledSavingsMax, period)}${sfxLabel}`;

    const upfrontDisplay = selectedVariant
      ? formatCurrency(selectedVariant.upfront_cost)
      : formatSavingsRange(summary.upfrontMin, summary.upfrontMax);

    const termDisplay = selectedVariant
      ? formatTerm(selectedVariant.term)
      : formatTermRange(summary.termMin, summary.termMax);

    const chevron = isExpanded ? '\u25bc' : '\u25b6';
    const identityParts = [
      `${escapeHtml(rep.resource_type)}${rep.engine ? ` (${escapeHtml(rep.engine)})` : ''}`,
    ];
    if (visibleKeys.has('region')) {
      identityParts.push(escapeHtml(rep.region));
    }
    identityParts.push(`${variants.length} variants`);

    const rangeParts: string[] = [];
    if (visibleKeys.has('savings')) {
      rangeParts.push(savingsDisplay);
    }
    if (visibleKeys.has('upfront_cost')) {
      rangeParts.push(`upfront: ${upfrontDisplay}`);
    }
    if (visibleKeys.has('term')) {
      rangeParts.push(`term: ${termDisplay}`);
    }

    rows.push(`
  <tr class="rec-cell-summary-row" data-cell-key="${escapeHtml(key)}">
    <td class="checkbox-col">
      <button type="button" class="rec-cell-chevron" data-cell-key="${escapeHtml(key)}" aria-expanded="${isExpanded}" aria-label="${isExpanded ? 'Collapse' : 'Expand'} cell variants">
        ${chevron}
      </button>
    </td>
    <td><span class="provider-badge ${providerBadgeClass(rep.provider)}">${escapeHtml(providerDisplayName(rep.provider))}</span></td>
    <td>${escapeHtml(accountName)}</td>
    <td><span class="service-badge">${escapeHtml(rep.service)}</span></td>
    <td colspan="${summaryColspan}" class="rec-cell-summary-content">
      <span class="rec-cell-identity">${identityParts.join(' &mdash; ')}</span>
      ${rangeParts.length > 0 ? `<span class="rec-cell-range">${rangeParts.join(' &middot; ')}</span>` : ''}
    </td>
  </tr>`);

    if (isExpanded) {
      const sortedVariants = sortVariantsInCell(variants);
      for (const v of sortedVariants) {
        rows.push(buildVariantRowMarkup(v, selectedRecs, true, visibleCols));
      }
    }
  }

  // Issue #479: select-all header checkbox renders as a proper tri-state
  // reflecting current selection vs. the set of best-variant-per-cell
  // recommendations (the set that selectAll's onChange actually populates,
  // see openColumnPopover wiring + #224). Indeterminate is set via JS in
  // renderRecommendationsList's post-render hook because HTML attributes
  // can't express the indeterminate state.
  const bestVariants = pickBestVariantPerCell(recommendations);
  const bestVariantIds = new Set(bestVariants.map((r) => r.id));
  let selectedBestCount = 0;
  selectedRecs.forEach((id) => { if (bestVariantIds.has(id)) selectedBestCount++; });
  const allSelected = bestVariants.length > 0 && selectedBestCount === bestVariants.length;
  const selectAllCheckedAttr = allSelected ? ' checked' : '';
  // Threaded to the renderer via a data attribute so the post-render hook
  // can flip the .indeterminate property without re-deriving the counts.
  const selectAllIndeterminate = selectedBestCount > 0 && selectedBestCount < bestVariants.length;
  const selectAllDataIndeterminate = ` data-indeterminate="${selectAllIndeterminate ? 'true' : 'false'}"`;

  return `
    <table>
      <thead>
        <tr>
          <th class="checkbox-col">
            <input type="checkbox" id="select-all-recs" aria-label="Select all recommendations"${selectAllCheckedAttr}${selectAllDataIndeterminate}>
          </th>
          ${visibleCols.map((c) => sortHeader(c.key)).join('')}
        </tr>
      </thead>
      <tbody>
        ${rows.join('')}
      </tbody>
    </table>
  `;
}

// renderSuppressionBadge returns HTML for the "recently purchased"
// indicator shown on recs the scheduler has annotated with an active
// purchase_suppression. Deep-links to Purchase History filtered to
// the execution whose suppression contributed the most. Returns ''
// when no suppression applies or the suppression has expired
// (defence-in-depth \u2014 the scheduler should have dropped such recs).
//
//   {suppressed} = rec.suppressed_count
//   {original}   = rec.count + rec.suppressed_count (rec.count is
//                  already post-subtraction)
//   {days}       = ceil((expires_at - now) / 24h), so 23h59m renders
//                  as "1d remaining" rather than "0d".
function renderSuppressionBadge(rec: LocalRecommendation): string {
  const suppressed = rec.suppressed_count ?? 0;
  if (suppressed <= 0) return '';
  const original = rec.count + suppressed;
  const expiresRaw = rec.suppression_expires_at;
  if (!expiresRaw) return '';
  const diffMs = new Date(expiresRaw).getTime() - Date.now();
  if (diffMs <= 0) return '';
  const days = Math.ceil(diffMs / (24 * 60 * 60 * 1000));
  const execID = rec.primary_suppression_execution_id;
  const href = execID ? `#history?execution=${encodeURIComponent(execID)}` : '#history';
  return ` <a class="rec-suppression-badge" href="${href}" title="Capacity from recent purchase; not re-proposed until it expires.">recently purchased ${suppressed}/${original} \u2014 ${days}d remaining</a>`;
}

// BULK_PURCHASE_LS_KEY holds the persisted Term/Payment/Capacity values
// so the toolbar remembers the user's last choice across page reloads.
// Versioned so we can migrate the shape in future without blowing up on
// a stale cached blob.
const BULK_PURCHASE_LS_KEY = 'cudly.recommendations.bulkPurchase.v1';

// BulkPurchaseToolbarState used to carry a `term` field that overrode each
// row's recommended term at API-call time. Bundle B drops it: each rec is
// purchased with its own per-row term (see term-aware bucketing in
// handleBulkPurchaseClick). Issue #282 drops the global Payment dropdown
// from the toolbar: the `payment` field is kept internally (seeded from
// GlobalConfig or 'all-upfront') so the fan-out modal's override/fallback
// logic continues to work, but it is no longer exposed in the UI or
// persisted to localStorage. loadBulkPurchaseState explicitly picks known
// fields so any legacy `term` or `payment` from older localStorage values
// is silently ignored on read — no migration shim needed.
type BulkPurchasePayment = 'all-upfront' | 'partial-upfront' | 'no-upfront' | 'monthly';

// Normalize payment synonyms that upstream rows may carry (e.g. Azure's
// 'upfront') to the canonical BulkPurchasePayment forms used throughout the
// bucket-key and toolbar machinery.  Returns null for unknown or absent values
// so callers can fall back safely.
//
// Mappings:
//   'upfront'       -> 'all-upfront'  (Azure canonical synonym)
//   'all-upfront'   -> 'all-upfront'  (AWS / pass-through)
//   'partial-upfront' -> 'partial-upfront' (pass-through)
//   'no-upfront'    -> 'no-upfront'   (pass-through)
//   'monthly'       -> 'monthly'      (GCP canonical; kept as-is)
//   anything else / undefined -> null
function normalizeBulkPayment(payment: string | undefined): BulkPurchasePayment | null {
  switch (payment) {
    case 'upfront':
    case 'all-upfront':
      return 'all-upfront';
    case 'partial-upfront':
      return 'partial-upfront';
    case 'no-upfront':
      return 'no-upfront';
    case 'monthly':
      return 'monthly';
    default:
      return null;
  }
}

// Centralized bucket-level payment compatibility check. A bucket is
// compatible iff EVERY rec in it has a supported (provider, service,
// term, payment) combination. Used by the bulk-buy fan-out path to
// flag buckets the user has built but won't be allowed to submit.
function isBucketPaymentCompatible(
  recs: readonly LocalRecommendation[],
  payment: BulkPurchasePayment,
): boolean {
  return recs.every((r) =>
    isPaymentSupported(r.provider as CompatProvider, r.service, r.term as 1 | 3, payment),
  );
}

interface BulkPurchaseToolbarState {
  payment: BulkPurchasePayment;
  capacity: number; // 1..100
}

const defaultBulkPurchaseState: BulkPurchaseToolbarState = {
  payment: 'all-upfront',
  capacity: 100,
};

function loadBulkPurchaseState(): BulkPurchaseToolbarState {
  try {
    const raw = localStorage.getItem(BULK_PURCHASE_LS_KEY);
    if (!raw) return { ...defaultBulkPurchaseState };
    const parsed = JSON.parse(raw) as Partial<BulkPurchaseToolbarState> & { term?: unknown };
    // Explicit field-pick rather than spread-and-omit — avoids leaking a
    // legacy `term` or `payment` from older localStorage values at runtime.
    // Payment is seeded from GlobalConfig only (issue #282 drops the toolbar
    // dropdown; the field is internal-only, not persisted).
    return {
      payment: cachedGlobalDefaultPayment as BulkPurchasePayment,
      capacity: Math.max(1, Math.min(100, Number(parsed.capacity) || 100)),
    };
  } catch {
    return { ...defaultBulkPurchaseState };
  }
}

function saveBulkPurchaseState(s: BulkPurchaseToolbarState): void {
  try {
    // Only persist capacity — payment is dropped from the toolbar (issue #282)
    // and is now session-only, seeded from GlobalConfig.
    localStorage.setItem(BULK_PURCHASE_LS_KEY, JSON.stringify({ capacity: s.capacity }));
  } catch {
    // Private-browsing / quota-exceeded — non-fatal, just lose the
    // sticky choice. The bottom box still works in-session.
  }
}

// ---------------------------------------------------------------------------
// Column visibility — localStorage persistence (issue #318)
// ---------------------------------------------------------------------------

// TOGGLEABLE_COLUMNS — the subset of COLUMN_DEFS whose visibility can be toggled.
// Provider, Account, Service, and Resource Type are "cell identity anchors" on
// multi-variant summary rows and are always visible in v1.
export const TOGGLEABLE_COLUMNS: readonly ColumnDef[] = COLUMN_DEFS.filter(
  (c) => !(['provider', 'account', 'service', 'resource_type'] as string[]).includes(c.key),
);

const TOGGLEABLE_COLUMN_KEYS = new Set<state.RecommendationsColumnId>(
  TOGGLEABLE_COLUMNS.map((c) => c.key),
);

const COLUMN_VISIBILITY_LS_KEY = 'cudly.recs.columnVisibility.v1';
const COLUMN_VISIBILITY_SCHEMA_VERSION = 1;

interface ColumnVisibilitySchema {
  schemaVersion: number;
  hidden: string[];
}

/** Load hidden columns from localStorage. Returns empty set on any error. Exported for tests. */
export function loadColumnVisibility(): Set<state.RecommendationsColumnId> {
  try {
    const raw = localStorage.getItem(COLUMN_VISIBILITY_LS_KEY);
    if (!raw) return new Set();
    const parsed = JSON.parse(raw) as Partial<ColumnVisibilitySchema>;
    if (parsed.schemaVersion !== COLUMN_VISIBILITY_SCHEMA_VERSION) return new Set();
    if (!Array.isArray(parsed.hidden)) return new Set();
    // Whitelist: only accept known toggleable column keys so stale/unknown
    // values from future versions are silently dropped.
    const valid = parsed.hidden.filter(
      (k): k is state.RecommendationsColumnId => TOGGLEABLE_COLUMN_KEYS.has(k as state.RecommendationsColumnId),
    );
    return new Set(valid);
  } catch {
    return new Set();
  }
}

/** Persist hidden columns to localStorage. Exported for tests. */
export function saveColumnVisibility(hidden: ReadonlySet<state.RecommendationsColumnId>): void {
  try {
    const payload: ColumnVisibilitySchema = {
      schemaVersion: COLUMN_VISIBILITY_SCHEMA_VERSION,
      hidden: Array.from(hidden),
    };
    localStorage.setItem(COLUMN_VISIBILITY_LS_KEY, JSON.stringify(payload));
  } catch {
    // Private-browsing / quota-exceeded — non-fatal.
  }
}

// Seed flag: set to true once column visibility is loaded from localStorage
// on first render so subsequent renders don't overwrite in-session toggles.
let columnVisibilitySeeded = false;

/**
 * Reset column-visibility seeded state. Exported for tests only — not part of
 * the public API. Call in beforeEach to ensure tests don't share seeding state.
 */
export function resetColumnVisibilityState(): void {
  columnVisibilitySeeded = false;
  state.setHiddenColumns(new Set());
}

/** Returns the subset of COLUMN_DEFS that are currently visible. */
function visibleColumns(): readonly ColumnDef[] {
  const hidden = state.getHiddenColumns();
  if (hidden.size === 0) return COLUMN_DEFS;
  return COLUMN_DEFS.filter((c) => !hidden.has(c.key));
}

// ---------------------------------------------------------------------------
// Mount-once-then-update lifecycle for the sticky bottom action box.
// mountBottomActionBox builds the DOM (input/select/button identities) and
// wires listeners exactly once. updateBottomActionBox refreshes only the
// mutable surface — button labels, .disabled, the selection-summary text —
// leaving the input/select elements (and their in-progress values) alone.
//
// IDs preserved for backward compatibility:
//   #bulk-purchase-capacity (Capacity % input — read by app.ts:307)
//   #bulk-purchase-btn      (Purchase one-off button)
//   #create-plan-btn        (Create Purchase Plan button — relocated from
//                            the old top filter bar)
//
// Issue #282: the bulk Payment dropdown (#bulk-purchase-payment) is removed.
// Each rec carries its own payment_option from the API fan-out; the per-cell
// radio enforcement caps purchase to one variant per cell. A global override
// was misleading and is redundant.
function mountBottomActionBox(): HTMLElement | null {
  const recsTab = document.getElementById('opportunities-tab');
  if (!recsTab) return null;

  let box = document.getElementById('recommendations-action-box');
  if (box) return box;

  box = document.createElement('div');
  box.id = 'recommendations-action-box';
  box.className = 'recommendations-action-box';
  box.setAttribute('role', 'toolbar');
  box.setAttribute('aria-label', 'Recommendations actions');

  const tbState = loadBulkPurchaseState();

  // Selection summary text (e.g. "(3 selected of 19 visible)" or "(All 19 visible)")
  const summary = document.createElement('span');
  summary.id = 'recommendations-action-summary';
  summary.className = 'recommendations-action-summary';
  box.appendChild(summary);

  // Capacity % input — preserved ID (app.ts:307 reads this)
  const capacityLabel = document.createElement('label');
  capacityLabel.textContent = 'Capacity % ';
  const capacityInput = document.createElement('input');
  capacityInput.id = 'bulk-purchase-capacity';
  capacityInput.type = 'number';
  capacityInput.min = '1';
  capacityInput.max = '100';
  capacityInput.step = '1';
  capacityInput.value = String(tbState.capacity);
  capacityLabel.appendChild(capacityInput);
  box.appendChild(capacityLabel);

  // Purchase one-off (preserved ID). Issue #365: hide for sessions
  // that lack `execute:purchases` (admin only by default; user and
  // readonly never see the button). The element stays in the DOM so
  // the click handler stays wired and the existing `updateBottomAction
  // Box` updates still flow through; `.hidden` toggles via the HTML
  // hidden attribute which renders as `display: none`.
  const purchaseBtn = document.createElement('button');
  purchaseBtn.type = 'button';
  purchaseBtn.className = 'btn btn-primary';
  purchaseBtn.id = 'bulk-purchase-btn';
  purchaseBtn.textContent = 'Purchase';
  purchaseBtn.title = 'Buy these reservations now (one-off, processed immediately)';
  purchaseBtn.hidden = !canAccess('execute', 'purchases');
  box.appendChild(purchaseBtn);

  // Create Purchase Plan (relocated from old top bar). Issue #365:
  // hide for sessions that lack `create:plans` (readonly loses it;
  // admin + user keep it).
  const planBtn = document.createElement('button');
  planBtn.type = 'button';
  planBtn.className = 'btn btn-secondary';
  planBtn.id = 'create-plan-btn';
  planBtn.textContent = 'Create Plan';
  planBtn.title = 'Schedule a recurring plan that will purchase these recommendations on a defined cadence';
  planBtn.hidden = !canAccess('create', 'plans');
  box.appendChild(planBtn);

  // a11y hint for the disabled-button state (#273 CR follow-up).
  // Disabled <button> elements are non-focusable per HTML spec and
  // browsers don't reliably show their `title` tooltips, so a sibling
  // hint with aria-describedby is the discoverable channel for both
  // mouse and keyboard users. The element starts hidden; updateBottom-
  // ActionBox toggles its visibility and links the buttons via
  // aria-describedby when they're disabled.
  const disabledHint = document.createElement('span');
  disabledHint.id = 'recommendations-action-disabled-hint';
  disabledHint.className = 'recommendations-action-disabled-hint';
  disabledHint.setAttribute('role', 'status');
  disabledHint.setAttribute('aria-live', 'polite');
  disabledHint.hidden = true;
  box.appendChild(disabledHint);

  const persist = (): void => {
    saveBulkPurchaseState({
      payment: tbState.payment,
      capacity: Math.max(1, Math.min(100, parseInt(capacityInput.value, 10) || 100)),
    });
  };
  capacityInput.addEventListener('change', persist);

  purchaseBtn.addEventListener('click', () => {
    const target = resolvePurchaseTarget();
    if (target.length === 0) return;
    handleBulkPurchaseClick(target);
  });

  planBtn.addEventListener('click', () => {
    const target = resolvePurchaseTarget();
    if (target.length === 0) return;
    // Pass the resolved target through to the plan modal as a snapshot
    // (#273 CR follow-up). Without this, savePlan would re-derive the
    // target from state.getVisibleRecommendations() / getSelectedRecommendation
    // IDs() at Save time — racing Refresh, filter changes, and
    // deselections that happen while the modal is open. The Purchase
    // path already captures the target at click time via handleBulkPurchase
    // Click(target); the Plan path now mirrors that.
    void openCreatePlanFromBottomBox(target);
  });

  recsTab.appendChild(box);
  return box;
}

// Resolve the action target: selected ∩ visible. Returns an empty slice when
// no rows are selected — the action buttons are disabled in that state by
// updateBottomActionBox (closes #273), so callers should never reach this
// helper without a selection. The empty-return is defence-in-depth: if a
// caller bypasses the disabled UI (programmatic invocation, future code path,
// regression on the gating), no purchase happens.
//
// Historical context: prior to #273 this fell back to
// pickBestVariantPerCell(visible) when no rows were selected, so misclicking
// a "Purchase visible" button could trigger an irreversible bulk purchase.
// The fallback was removed because Refresh and filter changes silently
// mutate the visible set, making the no-selection path structurally unsafe.
function resolvePurchaseTarget(): LocalRecommendation[] {
  const visible = state.getVisibleRecommendations() as unknown as LocalRecommendation[];
  const selected = state.getSelectedRecommendationIDs();
  return visible.filter((r) => selected.has(r.id));
}

// isHomogeneousSelection returns true iff every recommendation in the slice
// shares the same (provider, service, term, payment). A single-item slice
// always passes. An empty slice returns true (vacuously homogeneous).
// Plans require a homogeneous selection because the plan's scheduling
// parameters (provider, service, term, payment) must be unambiguous.
// Exported so unit tests can cover it directly without a full DOM setup.
export function isHomogeneousSelection(recs: readonly LocalRecommendation[]): boolean {
  if (recs.length <= 1) return true;
  // recs is non-empty here; the non-null assertion is safe.
  // eslint-disable-next-line @typescript-eslint/no-non-null-assertion
  const first = recs[0]!;
  const { provider, service, term, payment } = first;
  return recs.every(
    (r) => r.provider === provider && r.service === service && r.term === term && r.payment === payment,
  );
}

// updateBottomActionBox refreshes labels and disabled state on every
// renderRecommendationsList call without rebuilding the input/select DOM,
// preserving any in-progress typing in the Capacity input.
function updateBottomActionBox(visibleCount: number, loadedCount: number): void {
  const box = document.getElementById('recommendations-action-box');
  if (!box) return;

  const selected = state.getSelectedRecommendationIDs();
  // Count only selections that are currently visible.
  const visible = state.getVisibleRecommendations() as unknown as LocalRecommendation[];
  const selectedVisibleCount = visible.reduce(
    (n, r) => n + (selected.has(r.id) ? 1 : 0),
    0,
  );

  // Action-box summary line surfaces the *financial* impact of the
  // current action target, not just selection counts (closes #281). The
  // selected-vs-visible count is the least useful info at this point —
  // the user can already see selection state from row checkboxes — and
  // the action box is prime real estate for the dollar figures the user
  // is about to authorise. Source-of-truth matches the summary cards
  // above: selection ∩ visible if ≥1 selected, else the visible set.
  const target: readonly LocalRecommendation[] = selectedVisibleCount > 0
    ? visible.filter((r) => selected.has(r.id))
    : visible;
  const targetGroups = groupRecsByCell(target);
  const targetRange = pageLevelRange(targetGroups);

  const summary = document.getElementById('recommendations-action-summary');
  if (summary) {
    if (loadedCount === 0) {
      summary.textContent = '(No recommendations loaded)';
    } else if (visibleCount === 0) {
      summary.textContent = '(0 visible — adjust filters)';
    } else if (selectedVisibleCount === 0) {
      summary.textContent = '(Select cells to act on)';
    } else if (targetRange.cellCount > 0) {
      const savingsText = targetRange.savingsMax > 0
        ? formatSavingsRange(targetRange.savingsMin, targetRange.savingsMax)
        : formatCurrency(0);
      const upfrontText = targetRange.upfrontMax > 0
        ? formatSavingsRange(targetRange.upfrontMin, targetRange.upfrontMax)
        : formatCurrency(0);
      const cellWord = targetRange.cellCount === 1 ? 'cell' : 'cells';
      summary.textContent = `(${savingsText}/mo · ${upfrontText} upfront across ${targetRange.cellCount} ${cellWord})`;
    } else {
      // Shouldn't happen given the gating above, but defensive.
      summary.textContent = `(${selectedVisibleCount} selected)`;
    }
  }

  const purchaseBtn = document.getElementById('bulk-purchase-btn') as HTMLButtonElement | null;
  const planBtn = document.getElementById('create-plan-btn') as HTMLButtonElement | null;
  const disabledHint = document.getElementById('recommendations-action-disabled-hint');
  const hasSelection = selectedVisibleCount > 0;
  const disabledMessage = loadedCount === 0
    ? 'No recommendations loaded'
    : visibleCount === 0
      ? 'No rows visible — adjust filters'
      : 'Select at least one cell to enable';

  // Compute the selected-visible slice once; both the plan-button gating and
  // the hint span need it.
  const selectedVisible = visible.filter((r) => selected.has(r.id));
  const planHomogeneous = isHomogeneousSelection(selectedVisible);
  const planEnabled = hasSelection && planHomogeneous;

  if (purchaseBtn) {
    purchaseBtn.disabled = !hasSelection;
    purchaseBtn.textContent = hasSelection
      ? `Purchase ${selectedVisibleCount} selected`
      : 'Purchase';
    if (hasSelection) {
      purchaseBtn.title = 'Buy these reservations now (one-off, processed immediately)';
      purchaseBtn.removeAttribute('aria-describedby');
    } else {
      // Drop the title — title on disabled buttons is unreliable; the
      // sibling hint carries the message.
      purchaseBtn.removeAttribute('title');
      purchaseBtn.setAttribute('aria-describedby', 'recommendations-action-disabled-hint');
    }
  }
  if (planBtn) {
    planBtn.disabled = !planEnabled;
    planBtn.textContent = hasSelection
      ? `Plan from ${selectedVisibleCount} selected`
      : 'Create Plan';
    if (planEnabled) {
      planBtn.title = 'Schedule a recurring plan that will purchase these recommendations on a defined cadence';
      planBtn.removeAttribute('aria-describedby');
    } else {
      planBtn.removeAttribute('title');
      planBtn.setAttribute('aria-describedby', 'recommendations-action-disabled-hint');
    }
  }

  // a11y: the disabled-state explanation lives on a sibling hint span, not on
  // the buttons' `title` attribute. Disabled <button> elements are non-focusable
  // per HTML spec and don't reliably surface `title` tooltips across browsers, so
  // keyboard users would never see the hint and mouse users only sometimes would.
  // The sibling element + aria-describedby pattern works for both (#273 CR follow-up).
  // The hint also carries the heterogeneous-selection explanation for the plan
  // button (#769): when a selection spans multiple providers/services/terms/payment
  // options the plan button is disabled and the hint explains why.
  if (disabledHint) {
    const heterogeneousPlanBlock = hasSelection && planBtn != null && !planBtn.hidden && !planHomogeneous;
    if (!hasSelection) {
      disabledHint.hidden = false;
      disabledHint.textContent = disabledMessage;
    } else if (heterogeneousPlanBlock) {
      disabledHint.hidden = false;
      disabledHint.textContent =
        'Plans require one provider, service, term, and payment. Refine your selection.';
    } else {
      disabledHint.hidden = true;
      disabledHint.textContent = '';
    }
  }
}

// openCreatePlanFromBottomBox opens the plan-creation modal. plans.ts'
// savePlan reads state.getVisibleRecommendations() (Bundle B's plumbing
// addition in Step 8c) so the plan only includes selected ∩ visible (or
// all visible if no selection).
async function openCreatePlanFromBottomBox(snapshot: LocalRecommendation[]): Promise<void> {
  const { openCreatePlanModal } = await import('./plans');
  // Cast: api.Recommendation and LocalRecommendation share the persisted
  // wire shape; the modal stores a copy and savePlan submits it as
  // api.Recommendation[]. The snapshot was already passed through
  // resolvePurchaseTarget() / Set membership, both of which treat the
  // shape as opaque.
  openCreatePlanModal(snapshot as unknown as readonly api.Recommendation[]);
}

function handleBulkPurchaseClick(recommendations: LocalRecommendation[]): void {
  const tb = loadBulkPurchaseState();
  if (recommendations.length === 0) {
    showToast({ message: 'No recommendations to purchase.', kind: 'warning' });
    return;
  }

  // Scale by capacity %; drop rows whose scaled count floors to 0.
  const scaled: LocalRecommendation[] = [];
  for (const r of recommendations) {
    const newCount = Math.floor((r.count * tb.capacity) / 100);
    if (newCount <= 0) continue;
    const ratio = r.count > 0 ? newCount / r.count : 1;
    scaled.push({
      ...r,
      count: newCount,
      // Carry the pre-scaling count so the backend can verify the
      // capacity_percent it records against the scaled count (#647).
      recommended_count: r.count,
      upfront_cost: r.upfront_cost * ratio,
      monthly_cost: r.monthly_cost != null ? r.monthly_cost * ratio : null,
      savings: r.savings * ratio,
    });
  }
  if (scaled.length === 0) {
    showToast({
      message: `Capacity ${tb.capacity}% produces no whole-number purchases from the current selection. Try a higher %.`,
      kind: 'warning',
    });
    return;
  }

  // Bucket by (provider, service, term, payment). Bundle B added `term` to
  // the key so multi-term selections fan out into separate buckets. Issue
  // #699 adds `payment` for the same reason: recs with identical
  // (provider, service, term) but different per-rec payment values must
  // also land in separate buckets so each bucket is payment-uniform and
  // resolveBucketPaymentSeed can seed from recs[0].payment rather than
  // falling back to the toolbar default ('all-upfront').
  //
  // Issue #132: SP recs (savings-plans-{compute,ec2instance,sagemaker,
  // database}) collapse into a single bucket per (provider, term) so an
  // operator who used to bulk-buy SP pre-PR-#123 (when there was one
  // 'savings-plans' service) keeps the one-click experience. Each rec
  // retains its real per-plan-type service slug — only the bucket key
  // is canonicalized via SAVINGS_PLANS_BUCKET_KEY. The backend
  // executePurchase loops per rec and uses rec.service for the
  // suppression and audit records, so a mixed-SP POST behaves
  // identically to four separate POSTs except that there's only one
  // approval token / email.
  const buckets = new Map<string, LocalRecommendation[]>();
  for (const r of scaled) {
    const bucketService = isSavingsPlanService(r.service) ? SAVINGS_PLANS_BUCKET_KEY : r.service;
    const key = `${r.provider}|${bucketService}|${r.term}|${normalizeBulkPayment(r.payment) ?? ''}`;
    const existing = buckets.get(key);
    if (existing) existing.push(r);
    else buckets.set(key, [r]);
  }
  const bucketEntries = Array.from(buckets.entries());

  // Per-bucket compatibility check using the bucket's own term. For a
  // mixed-SP bucket we check every rec's service — if ANY rec's
  // (provider, service, term, payment) is unsupported, the whole bucket
  // is flagged incompatible. SP plan types share the same compatibility
  // rules today (no SP variant rejects no-upfront the way RDS 3yr does),
  // so this is a defensive belt-and-suspenders check rather than a
  // common case.
  const incompatible = bucketEntries.filter(([_key, recs]) => !isBucketPaymentCompatible(recs, tb.payment));

  if (bucketEntries.length > 1 || incompatible.length > 0) {
    // Multi-bucket / incompatible path: open the fan-out modal so the
    // user can review per-bucket details before submitting one
    // executePurchase call per bucket in parallel.
    // openFanOutModal is async (issue #111: it pre-fetches per-account
    // service overrides to seed each bucket's Payment default); the
    // returned promise is fire-and-forget — the modal is the surface
    // the user interacts with.
    void openFanOutModal(bucketEntries, tb);
    return;
  }

  // Single-bucket happy path: open the preview modal + submit via the
  // existing approval-request flow. The modal's "Send for Approval" button
  // (wired in app.ts) picks up the recs via getPurchaseModalRecommendations.
  // openPurchaseModal is async (issue #111 (iii): per-rec override
  // prefetch); fire-and-forget — the modal is the user's surface.
  void openPurchaseModal(scaled);
}

// FanOutBucket groups one batch of recs under a single (provider,
// service, term, payment, capacity) choice. A multi-bucket Purchase
// fires one executePurchase POST per bucket.
//
// `service` is the canonical bucket slug — equal to the rec's service
// for non-SP buckets, or `SAVINGS_PLANS_BUCKET_KEY` for any bucket
// containing one or more savings-plans-* recs (issue #132). Per-rec
// service slugs are preserved on `recs[].service` and round-trip into
// the executePurchase POST body — the backend uses each rec's own
// service for suppression and audit records.
//
// `paymentSource` (issue #111) records WHERE this bucket's payment
// default came from:
//   - 'override': all recs share one cloud_account_id, that account
//     has an AccountServiceOverride matching the bucket's
//     (provider, service), and the override's payment is supported by
//     the (provider, service, term) cell. The bucket section renders
//     a small "(from account override)" note next to the Payment
//     dropdown.
//   - 'toolbar': fallback — multi-account bucket, no override, override
//     has no payment, or the override's payment is unsupported for
//     this cell. Bucket inherits the bulk-toolbar Payment, today's
//     pre-#111 behavior.
//
// The user can change the per-bucket Payment via the dropdown
// rendered in the modal; the `change` handler updates `payment` (and
// keeps `paymentSource` so the source note doesn't lie about origin).
export interface FanOutBucket {
  provider: CompatProvider;
  service: string;
  term: 1 | 3;
  payment: 'all-upfront' | 'partial-upfront' | 'no-upfront' | 'monthly';
  capacityPercent: number;
  recs: LocalRecommendation[]; // scaled by capacityPercent
  paymentSource: 'override' | 'toolbar';
}

// Fan-out modal state. app.ts's Send-for-Approval click reads these
// via getFanOutBuckets() to fire one POST per bucket. Cleared when
// the modal closes.
let currentFanOutBuckets: FanOutBucket[] | null = null;

export function getFanOutBuckets(): FanOutBucket[] | null {
  return currentFanOutBuckets ? currentFanOutBuckets.map((b) => ({ ...b })) : null;
}

export function clearFanOutBuckets(): void {
  currentFanOutBuckets = null;
}

// resolveBucketPaymentSeed picks the default Payment value for a
// bucket per issue #111 sub-option (ii):
//   - When all recs in the bucket share one non-empty cloud_account_id
//     AND that account has a saved AccountServiceOverride matching
//     `(provider, recs[0].service)` AND the override's `payment` is a
//     non-empty value supported by the (provider, service, term) cell:
//     seed from override.
//   - Otherwise: seed from the toolbar payment (today's behavior).
//
// IMPORTANT: the override-lookup uses `recs[0].service` (the per-rec
// service slug), NOT `bucket.service`. That's a future-proofing choice
// for the post-#132 SP-canonical-bucket-key landing in PR #180 — when
// `bucket.service` becomes the canonical `'savings-plans'` for a
// mixed-plan-type SP bucket, the override is still keyed on the
// per-plan-type slug (`savings-plans-compute`, etc.), so this lookup
// stays correct under either bucket-key encoding.
//
// Multi-account fallback: if recs span 2+ distinct cloud_account_id
// values, no single account's override applies cleanly. Falling back
// to toolbar avoids surprising the user. TODO(#111-followup): consider
// per-rec seeding inside a multi-account bucket — would need either
// per-rec dropdowns or a "split this bucket by account" UX.
function resolveBucketPaymentSeed(
  recs: LocalRecommendation[],
  toolbar: BulkPurchaseToolbarState,
  overridesByAccount: Map<string, AccountServiceOverride[]>,
): { payment: BulkPurchaseToolbarState['payment']; source: 'override' | 'toolbar' } {
  if (recs.length === 0) return { payment: toolbar.payment, source: 'toolbar' };
  const r0 = recs[0]!;
  const provider = r0.provider as CompatProvider;
  const term = r0.term as 1 | 3;

  // Single-account check: every rec must carry the same non-empty cloud_account_id.
  const accountIDs = new Set<string>();
  for (const r of recs) {
    if (!r.cloud_account_id) {
      // Any rec missing an account id skips the override lookup; fall
      // through to the rec.payment seed below.
      accountIDs.clear();
      break;
    }
    accountIDs.add(r.cloud_account_id);
  }
  if (accountIDs.size === 1) {
    const accountID = recs[0]!.cloud_account_id!;
    const overrides = overridesByAccount.get(accountID);
    if (overrides) {
      // Match on the per-rec service (NOT bucket.service) — see the comment
      // above for the SP-canonical-bucket-key future-proofing rationale.
      const match = overrides.find(
        (o) => o.provider === provider && o.service === r0.service,
      );
      const overridePayment = normalizeBulkPayment(match?.payment);
      if (
        overridePayment
        // Defensive: only honour the override when the (provider, service,
        // term, payment) combo is actually supported. A stale or hand-saved
        // override pointing at an unsupported payment for this term shouldn't
        // poison the dropdown — fall through to rec.payment seed.
        && isPaymentSupported(provider, r0.service, term, overridePayment)
      ) {
        return {
          payment: overridePayment,
          source: 'override',
        };
      }
    }
  }

  // Issue #699: since `payment` is now part of the bucket key, every bucket
  // is payment-uniform (all recs share the same rec.payment). Seed from
  // recs[0].payment when it's a supported value for this (provider, service,
  // term) cell instead of blindly falling back to toolbar.payment
  // ('all-upfront'). Multi-account buckets, missing-payment recs, and
  // unsupported payment values still fall through to toolbar.
  //
  // Normalize first so upstream synonym forms ('upfront', 'monthly') map to
  // the canonical BulkPurchasePayment values before the support check.
  const recPayment = normalizeBulkPayment(r0.payment);
  if (
    recPayment
    && isPaymentSupported(provider, r0.service, term, recPayment as CompatPayment)
  ) {
    return { payment: recPayment, source: 'toolbar' };
  }

  return { payment: toolbar.payment, source: 'toolbar' };
}

async function openFanOutModal(
  bucketEntries: Array<[string, LocalRecommendation[]]>,
  toolbar: BulkPurchaseToolbarState,
): Promise<void> {
  // Pre-fetch service-overrides for every account that's the SOLE
  // account in any bucket — these are the only buckets eligible for
  // the override seed (multi-account buckets always fall back to
  // toolbar). One fetch per distinct accountID; cached for the
  // lifetime of this openFanOutModal call. Errors are swallowed: the
  // toolbar-seed fallback always works, so a transient API failure
  // shouldn't block the modal.
  const eligibleAccountIDs = new Set<string>();
  for (const [, recs] of bucketEntries) {
    if (recs.length === 0) continue;
    const ids = new Set<string>();
    for (const r of recs) {
      if (r.cloud_account_id) ids.add(r.cloud_account_id);
    }
    if (ids.size === 1) {
      const only = recs[0]?.cloud_account_id;
      if (only) eligibleAccountIDs.add(only);
    }
  }
  const overridesByAccount = new Map<string, AccountServiceOverride[]>();
  await Promise.all(
    Array.from(eligibleAccountIDs).map(async (id) => {
      try {
        const list = await api.listAccountServiceOverrides(id);
        overridesByAccount.set(id, list);
      } catch {
        // Silent fallback to toolbar seed — a network blip shouldn't
        // block the user from purchasing.
      }
    }),
  );

  const buckets: FanOutBucket[] = bucketEntries
    .filter(([_key, recs]) => recs.length > 0)
    .map(([_key, recs]) => {
      const r = recs[0]!;
      const seed = resolveBucketPaymentSeed(recs, toolbar, overridesByAccount);
      // SP buckets carry the canonical bucket key as `service` so the
      // section header can render the mixed-plan-type label; the per-
      // rec slugs on recs[].service are what the backend sees.
      const bucketService = isSavingsPlanService(r.service) ? SAVINGS_PLANS_BUCKET_KEY : r.service;
      return {
        provider: r.provider as CompatProvider,
        service: bucketService,
        // Each bucket is now term-uniform (key includes term), so we read
        // the term from the bucket itself rather than from the dropped
        // toolbar override.
        term: r.term as 1 | 3,
        payment: seed.payment,
        paymentSource: seed.source,
        capacityPercent: toolbar.capacity,
        recs,
      };
    });
  currentFanOutBuckets = buckets;

  const container = document.getElementById('purchase-details');
  const modal = document.getElementById('purchase-modal');
  if (!container || !modal) return;

  // Build the modal body via createElement so the innerHTML hook
  // doesn't flag any template-literal HTML.
  while (container.firstChild) container.removeChild(container.firstChild);

  const summary = document.createElement('div');
  summary.className = 'form-section fanout-summary';
  const summaryTitle = document.createElement('h3');
  summaryTitle.textContent = `Bulk purchase — ${buckets.length} bucket${buckets.length === 1 ? '' : 's'}`;
  summary.appendChild(summaryTitle);

  const emailNote = document.createElement('p');
  emailNote.className = 'fanout-email-note';
  emailNote.textContent = `Will send ${buckets.length} approval email${buckets.length === 1 ? '' : 's'} — one per bucket.`;
  summary.appendChild(emailNote);

  const totals = computeFanOutTotals(buckets);
  const totalLine = (label: string, value: string, cls = ''): HTMLParagraphElement => {
    const p = document.createElement('p');
    const strong = document.createElement('strong');
    if (cls) strong.className = cls;
    strong.textContent = value;
    p.appendChild(document.createTextNode(label + ': '));
    p.appendChild(strong);
    return p;
  };
  // issue #319: scale total savings by the active cost period.
  const fanOutPeriod = state.getCostPeriod();
  summary.appendChild(totalLine('Total commitments', String(totals.totalCount)));
  summary.appendChild(totalLine('Total upfront', formatCurrency(totals.totalUpfront)));
  summary.appendChild(totalLine(`Total savings ${periodSuffix(fanOutPeriod)}`, formatCostForPeriod(totals.totalSavings, fanOutPeriod), 'savings'));
  container.appendChild(summary);

  for (const b of buckets) {
    container.appendChild(renderFanOutBucketSection(b));
  }

  openModal(modal);
}

function computeFanOutTotals(buckets: FanOutBucket[]): { totalCount: number; totalUpfront: number; totalSavings: number } {
  let totalCount = 0;
  let totalUpfront = 0;
  let totalSavings = 0;
  for (const b of buckets) {
    for (const r of b.recs) {
      totalCount += r.count;
      totalUpfront += r.upfront_cost;
      totalSavings += r.savings;
    }
  }
  return { totalCount, totalUpfront, totalSavings };
}

function renderFanOutBucketSection(b: FanOutBucket): HTMLElement {
  const section = document.createElement('section');
  section.className = 'fanout-bucket form-section';

  // Service label: SP bucket → "Savings Plans (Compute + SageMaker)"
  // listing only the plan types actually present in this bucket;
  // non-SP bucket → the raw service slug (e.g. "ec2", "rds"). Order
  // follows the recs' insertion order so it tracks the table.
  const isSPBucket = b.service === SAVINGS_PLANS_BUCKET_KEY;
  const serviceLabel = isSPBucket
    ? savingsPlansBucketLabel(b.recs.map((r) => r.service))
    : b.service;

  const title = document.createElement('h4');
  title.textContent = `${b.provider.toUpperCase()} / ${serviceLabel} — ${b.recs.length} commitment${b.recs.length === 1 ? '' : 's'}`;
  section.appendChild(title);

  const status = document.createElement('p');
  const renderStatus = (): void => {
    // For mixed-SP buckets check compatibility per rec — every rec must
    // be supported. For non-SP buckets every rec shares b.service so a
    // single check is equivalent. The shared helper keeps this in sync
    // with the same check at handleBulkPurchaseClick.
    const compat = isBucketPaymentCompatible(b.recs, b.payment);
    status.className = compat ? 'fanout-bucket-ok' : 'fanout-bucket-error';
    status.textContent = compat
      ? `${b.capacityPercent}% capacity · ${b.term}yr · ${b.payment}`
      : `Invalid combo: ${b.provider} / ${serviceLabel} doesn't support ${b.term}yr + ${b.payment}. This bucket will be skipped.`;
  };
  renderStatus();
  section.appendChild(status);

  // Issue #111: Per-bucket Payment dropdown. Default-selected =
  // bucket.payment (seeded by resolveBucketPaymentSeed: override →
  // toolbar). Options come from paymentOptionsFor, which already
  // filters to the supported (provider, service, term) Payment values
  // — so the user can never pick an unsupported combo here. The
  // change handler updates the bucket's payment in module state
  // (`currentFanOutBuckets`) so getFanOutBuckets() returns the
  // user's choice and handleFanOutExecute (in app.ts) reads the
  // right value per POST.
  const paymentRow = document.createElement('div');
  paymentRow.className = 'fanout-bucket-payment-row';
  const paymentLabel = document.createElement('label');
  paymentLabel.className = 'fanout-bucket-payment-label';
  paymentLabel.appendChild(document.createTextNode('Payment: '));
  const paymentSelect = document.createElement('select');
  paymentSelect.className = 'fanout-bucket-payment';
  for (const opt of paymentOptionsFor(b.provider, b.service, b.term)) {
    const option = document.createElement('option');
    option.value = opt;
    option.textContent = opt;
    if (opt === b.payment) option.selected = true;
    paymentSelect.appendChild(option);
  }
  paymentSelect.addEventListener('change', () => {
    const next = paymentSelect.value as FanOutBucket['payment'];
    // Find this bucket in module state by reference equality on the
    // recs array (the recs array is preserved across the b ↔
    // currentFanOutBuckets[i] mapping; identity comparison is safe).
    if (currentFanOutBuckets) {
      const idx = currentFanOutBuckets.findIndex((cb) => cb.recs === b.recs);
      if (idx >= 0) {
        currentFanOutBuckets[idx]!.payment = next;
      }
    }
    b.payment = next;
    renderStatus();
  });
  paymentLabel.appendChild(paymentSelect);
  paymentRow.appendChild(paymentLabel);
  if (b.paymentSource === 'override') {
    const sourceNote = document.createElement('span');
    sourceNote.className = 'fanout-bucket-payment-source';
    sourceNote.textContent = ' (from account override)';
    paymentRow.appendChild(sourceNote);
  }
  section.appendChild(paymentRow);

  const bucketTotal = b.recs.reduce(
    (acc, r) => ({
      count: acc.count + r.count,
      upfront: acc.upfront + r.upfront_cost,
      savings: acc.savings + r.savings,
    }),
    { count: 0, upfront: 0, savings: 0 },
  );
  // issue #319: scale bucket savings by the active cost period.
  const bPeriod = state.getCostPeriod();
  const totals = document.createElement('p');
  totals.className = 'fanout-bucket-totals';
  totals.textContent = `${bucketTotal.count} commitments · ${formatCurrency(bucketTotal.upfront)} upfront · ${formatCostForPeriod(bucketTotal.savings, bPeriod)} savings ${periodSuffix(bPeriod)}`;
  section.appendChild(totals);

  return section;
}

function renderRecommendationsList(loadedRecs: LocalRecommendation[]): void {
  const container = document.getElementById('recommendations-list');
  if (!container) return;

  // Seed column visibility from localStorage on the first render.
  // columnVisibilitySeeded stays true for the rest of the session so
  // in-session toggles (via the "Columns ▾" popover) are not overwritten.
  if (!columnVisibilitySeeded) {
    state.setHiddenColumns(loadColumnVisibility());
    columnVisibilitySeeded = true;
  }

  // Pipeline:
  //   loaded -> applyColumnFilters -> visible
  //   state.setVisibleRecommendations(visible)   (read by plans.ts:savePlan)
  // When the column-filters record is empty, applyColumnFilters returns a
  // clone of the input.
  const recommendations = applyColumnFilters(
    loadedRecs ?? [],
    state.getRecommendationsColumnFilters(),
  );
  state.setVisibleRecommendations(recommendations as unknown as readonly api.Recommendation[]);

  // Keep the savings card in sync with the visible (post-filter) set on
  // every list rerender so the card and the per-cell banner under the
  // table never diverge (#272 CR follow-up). The cached
  // lastRecommendationsSummary holds the API-derived counts (total_count,
  // total_upfront_cost, avg_payback_months); the savings figure itself is
  // recomputed from `recommendations` inside renderRecommendationsSummary
  // via the same pageLevelRange the banner uses.
  renderRecommendationsSummary(lastRecommendationsSummary, recommendations);

  // Mount once; update is per-render below.
  mountBottomActionBox();

  const emptyResult = !recommendations || recommendations.length === 0;
  if (emptyResult) {
    lastVisibleGroupKeys = [];
  }

  // Compute the visible column set once per render — passed to buildListMarkup
  // so header and row rendering use the same snapshot of column visibility state.
  const visibleCols = visibleColumns();

  const selectedIDs = state.getSelectedRecommendationIDs();
  // Dynamic table markup: every caller-provided value passes through
  // escapeHtml or is a number. The string is built in buildListMarkup.
  // NOTE: buildListMarkup also populates lastVisibleGroupKeys, so it MUST
  // run before renderFilterStatusBar (which reads it for the Expand-All button).
  container.innerHTML = buildListMarkup(recommendations ?? [], selectedIDs, visibleCols);

  // Issue #700: when the filter yields zero rows, preserve the <thead> by
  // injecting a hint row into the empty <tbody> rather than replacing the
  // entire table with a <p>. The column headers remain visible so the user
  // can see which columns are active while they adjust filters.
  if (emptyResult) {
    const tbody = container.querySelector('tbody');
    if (tbody) {
      // colspan = 1 (checkbox col) + all visible data columns.
      const colspan = 1 + visibleCols.length;
      const tr = document.createElement('tr');
      const td = document.createElement('td');
      td.setAttribute('colspan', String(colspan));
      td.className = 'empty';
      td.textContent = 'No rows match these filters.';
      tr.appendChild(td);
      tbody.appendChild(tr);
    }
  }

  const visibleCount = recommendations?.length ?? 0;

  // Filter status: Clear-filters badge + aria-live count + Expand-All toggle.
  // Rendered AFTER buildListMarkup so lastVisibleGroupKeys is populated.
  // Mounted as a sibling above the table so it survives the container's
  // innerHTML rewrite without losing aria-live announcements.
  renderFilterStatusBar(loadedRecs?.length ?? 0, visibleCount);

  updateBottomActionBox(visibleCount, loadedRecs?.length ?? visibleCount);


  // Per-column filter button: trigger opens the popover anchored to the
  // button. e.stopPropagation prevents the surrounding <th>'s sort handler
  // from also firing.
  container.querySelectorAll<HTMLButtonElement>('.column-filter-btn').forEach((btn) => {
    const column = btn.dataset['column'] as state.RecommendationsColumnId | undefined;
    if (!column) return;
    btn.addEventListener('click', (e) => {
      e.stopPropagation();
      openColumnPopover(column, btn);
    });
  });

  // After the table is rebuilt, re-anchor any open popover to the new
  // trigger DOM node and re-sync .checked / .value from current state.
  rebindOpenPopoverAnchor();

  // Watch the recommendations tab's class so we can close the popover if
  // the user switches away to another tab.
  ensureRecommendationsTabObserver();

  // Sortable column headers. Toggle ascending/descending on repeat click.
  container.querySelectorAll<HTMLTableCellElement>('th.sortable').forEach((th) => {
    const onActivate = (): void => {
      const col = th.dataset['sort'];
      if (!col) return;
      const prev = state.getRecommendationsSort();
      // Issue #480: first click on a different column uses that column's
      // per-config default direction (asc for text/most numerics; desc for
      // `savings` and `on_demand_monthly`). Subsequent clicks on the active
      // column toggle desc <-> asc.
      const direction: 'asc' | 'desc' =
        prev.column === col && prev.direction === 'desc' ? 'asc'
          : prev.column === col && prev.direction === 'asc' ? 'desc'
          : defaultSortDirectionFor(col as state.RecommendationsColumnId);
      state.setRecommendationsSort({ column: col as state.RecommendationsSortColumn, direction });
      // Issue #481: persist sort to the URL so refreshes / bookmarks /
      // shareable links restore the user's column + direction. Catches the
      // common UX surprise of "I sorted by Account, refreshed, now I'm back
      // at Savings desc".
      writeSortToUrl({ column: col as state.RecommendationsSortColumn, direction });
      renderRecommendationsList(recommendations);
    };
    th.addEventListener('click', onActivate);
    th.addEventListener('keydown', (e) => {
      if (e.key === 'Enter' || e.key === ' ') {
        e.preventDefault();
        onActivate();
      }
    });
  });

  // Add event listeners
  const selectAllCheckbox = document.getElementById('select-all-recs') as HTMLInputElement | null;
  if (selectAllCheckbox) {
    // Issue #479: indeterminate is a DOM property only, so apply it from
    // the data attribute the renderer threaded through. Without this, a
    // partial selection shows no visual cue and clicking the header
    // repeatedly becomes a no-op because the checkbox's .checked never
    // flips.
    selectAllCheckbox.indeterminate = selectAllCheckbox.dataset['indeterminate'] === 'true';
    selectAllCheckbox.addEventListener('change', () => {
      if (selectAllCheckbox.checked) {
        // Issue #224: select-all picks ONE variant per cell (highest-effective-
        // savings) rather than every visible row. After PR #195's per-(term,
        // payment) fan-out, naive "select every row" produces 6× the intended
        // commitments per resource — wrong purchase intent. Clear current
        // selection first so a stale choice from a different filter context
        // doesn't bleed through.
        state.clearSelectedRecommendations();
        for (const r of pickBestVariantPerCell(recommendations)) {
          state.addSelectedRecommendation(r.id);
        }
      } else {
        state.clearSelectedRecommendations();
      }
      renderRecommendationsList(recommendations);
    });
  }

  // ID-keyed selection toggles. data-rec-id persists across filter
  // changes so a stale selection from a previous filter is a no-op
  // once the user narrows, rather than pointing at whichever rec
  // happens to occupy the old index position.
  //
  // Issue #224: enforce one-variant-per-cell radio behaviour on check.
  // When the user checks a variant, deselect any other variant of the
  // same cell that's already selected — a single physical resource
  // can only carry one (term, payment) commitment at a time.
  container.querySelectorAll<HTMLInputElement>('input[data-rec-id]').forEach(cb => {
    cb.addEventListener('change', () => {
      const id = cb.dataset['recId'] || '';
      if (!id) return;
      if (cb.checked) {
        const newRec = recommendations.find((r) => r.id === id);
        if (newRec) {
          const newCell = cellKey(newRec);
          const selected = state.getSelectedRecommendationIDs();
          // Scan the full loaded set (not just the filtered view) so that
          // hidden siblings (e.g. filtered-out term variants) are also
          // deselected, preserving the one-variant-per-cell contract.
          const allLoaded = state.getRecommendations() as unknown as LocalRecommendation[];
          for (const r of allLoaded) {
            if (r.id !== id && selected.has(r.id) && cellKey(r) === newCell) {
              state.removeSelectedRecommendation(r.id);
            }
          }
        }
        state.addSelectedRecommendation(id);
      } else {
        state.removeSelectedRecommendation(id);
      }
      renderRecommendationsList(recommendations);
    });
  });

  // issues #225 + #226: chevron click toggles expand/collapse for a cell group.
  container.querySelectorAll<HTMLButtonElement>('.rec-cell-chevron').forEach((btn) => {
    btn.addEventListener('click', (e) => {
      e.stopPropagation();
      const key = btn.dataset['cellKey'] ?? '';
      if (!key) return;
      if (expandedCells.has(key)) {
        expandedCells.delete(key);
      } else {
        expandedCells.add(key);
      }
      renderRecommendationsList(loadedRecs);
    });
  });

  // Row-click toggles selection (issue #344 T4'). Clicking anywhere on
  // the row's body now toggles the row's checkbox + dispatches the
  // existing change handler — which already enforces the
  // one-variant-per-cell radio behaviour (issue #224) and rerenders.
  // Skip clicks on the checkbox itself (its native click already
  // toggles) and on any interactive child (button / a / input / label /
  // select / [data-action]) so per-row controls keep their own
  // semantics. The previous row-click → openDetailDrawer behaviour was
  // dropped: see plan.md §T4 (the detail drawer's payload duplicated
  // the table, with backend-deferred fields the only differentiators).
  container.querySelectorAll<HTMLTableRowElement>('tr.recommendation-row').forEach((tr) => {
    tr.addEventListener('click', (e) => {
      if (!(e.target instanceof Element)) return;
      const target = e.target as HTMLElement;
      if (target.closest('input, button, a, label, select, [data-action]')) return;
      const cb = tr.querySelector<HTMLInputElement>('input[type="checkbox"][data-rec-id]');
      if (!cb) return;
      cb.checked = !cb.checked;
      cb.dispatchEvent(new Event('change', { bubbles: true }));
    });
  });
}

// resolvePerRecPaymentSeed picks the default Payment value for one rec
// in the per-row purchase modal (issue #111 sub-option (iii)). The
// precedence:
//   1. Account override: rec carries a non-empty cloud_account_id, that
//      account has an AccountServiceOverride matching
//      `(rec.provider, rec.service)`, the override's `payment` is
//      non-empty, AND `(provider, service, term, payment)` is supported
//      by isPaymentSupported. → seed from override; the row's source-
//      note span renders "(from account override)".
//   2. Rec's own payment: the API stamps payment at collection time;
//      use it if non-empty AND supported for `(provider, service, term)`.
//   3. paymentOptionsFor(provider, service, term)[0]: defensive fallback
//      for malformed test fixtures or pre-#111 cached responses where
//      the rec lacks a payment. paymentOptionsFor returns at least one
//      option for every provider/service the recommendations engine
//      generates rows for.
//
// NOTE: this helper duplicates the override-fetch shape from
// resolveBucketPaymentSeed (per-bucket, used by the fan-out modal). The
// two are kept separate by deliberate scope discipline; a follow-up
// issue will consolidate them into a single
// `frontend/src/lib/overrides.ts` helper once both surfaces have shipped.
function resolvePerRecPaymentSeed(
  rec: LocalRecommendation,
  overridesByAccount: Map<string, AccountServiceOverride[]>,
): { payment: CompatPayment; source: 'override' | 'rec' | 'fallback' } {
  const provider = rec.provider as CompatProvider;
  const term = rec.term as 1 | 3;

  if (rec.cloud_account_id) {
    const overrides = overridesByAccount.get(rec.cloud_account_id);
    if (overrides) {
      const match = overrides.find(
        (o) => o.provider === provider && o.service === rec.service,
      );
      if (
        match
        && match.payment
        && isPaymentSupported(provider, rec.service, term, match.payment as CompatPayment)
      ) {
        return { payment: match.payment as CompatPayment, source: 'override' };
      }
    }
  }

  if (rec.payment && isPaymentSupported(provider, rec.service, term, rec.payment as CompatPayment)) {
    return { payment: rec.payment as CompatPayment, source: 'rec' };
  }

  // Defensive fallback: rec is missing/has-unsupported payment AND no
  // matching override. paymentOptionsFor always returns at least one
  // option for the (provider, service, term) cells the engine emits.
  // issue #223: prefer GlobalConfig.DefaultPayment over the first option
  // so the fallback is consistent with the operator's configured preference.
  const options = paymentOptionsFor(provider, rec.service, term);
  const preferred = (options as string[]).includes(cachedGlobalDefaultPayment)
    ? cachedGlobalDefaultPayment
    : (options[0] ?? 'all-upfront') as CompatPayment;
  return { payment: preferred, source: 'fallback' };
}

/**
 * Open the single-bucket purchase modal with editable per-row Term and
 * Payment dropdowns (issue #111 sub-option (iii)).
 *
 * Expanded by issue #320 to also show:
 *   - Per-row "Include" checkboxes (all checked by default) with a
 *     select-all/deselect-all header checkbox.
 *   - Account, Upfront, Monthly Cost, Effective Savings, and Effective %
 *     columns so the user can verify the full breakdown before committing.
 *   - A live totals row that updates as checkboxes toggle.
 *   - Execute Purchase disabled when no rows are checked.
 *
 * Only checked rows are submitted: `getPurchaseModalRecommendations()`
 * filters by `checkedPurchaseIndices` so the existing execute-purchase
 * code path in app.ts needs no changes.
 *
 * Modal shows monthly totals regardless of the page's cost-period selector
 * — by design (the modal is a commit-decision context where monthly is
 * canonical).
 *
 * Defaults are seeded by resolvePerRecPaymentSeed:
 *   override → rec's own payment → paymentOptionsFor[0] fallback.
 *
 * On change, handlers mutate `currentPurchaseRecommendations[idx]` in
 * place so `getPurchaseModalRecommendations()` returns the user's
 * edits, and `app.ts::handleExecutePurchase` posts those values per row
 * (replacing the historical hardcoded `'all-upfront'` on that path).
 *
 * Async because it pre-fetches per-account overrides — same pattern as
 * `openFanOutModal`. Errors swallowed: the rec-payment fallback always
 * works, so a transient API blip shouldn't block the modal.
 */
export async function openPurchaseModal(recommendations: LocalRecommendation[]): Promise<void> {
  currentPurchaseRecommendations = [...recommendations];
  // Initialise all indices as checked (issue #320: all selected by default).
  checkedPurchaseIndices = new Set(currentPurchaseRecommendations.map((_, i) => i));
  checkedPurchaseModalInitialised = true;

  const container = document.getElementById('purchase-details');
  if (!container) return;

  // Pre-fetch overrides for every distinct non-empty cloud_account_id
  // in the input set. One fetch per account, parallel via Promise.all,
  // cached in a per-call Map.
  const accountIDs = new Set<string>();
  for (const r of currentPurchaseRecommendations) {
    if (r.cloud_account_id) accountIDs.add(r.cloud_account_id);
  }
  const overridesByAccount = new Map<string, AccountServiceOverride[]>();
  await Promise.all(
    Array.from(accountIDs).map(async (id) => {
      try {
        const list = await api.listAccountServiceOverrides(id);
        overridesByAccount.set(id, list);
      } catch {
        // Silent fallback to rec-own / paymentOptionsFor[0] seed.
      }
    }),
  );

  // Compute seed per rec and mutate currentPurchaseRecommendations in
  // place so the in-flight modal state matches what the dropdowns
  // render. The 'rec' source case is a no-op write (same value), but
  // keeping the assignment uniform avoids "did the user edit this?"
  // ambiguity downstream — every rec carries an explicit payment by
  // the time the modal opens.
  const seeds = currentPurchaseRecommendations.map((r) => resolvePerRecPaymentSeed(r, overridesByAccount));
  for (let i = 0; i < currentPurchaseRecommendations.length; i++) {
    currentPurchaseRecommendations[i]!.payment = seeds[i]!.payment;
  }

  while (container.firstChild) container.removeChild(container.firstChild);

  // Approval-required note: clicking the modal's primary button does NOT
  // execute the purchase — it sends an approval-request email to the
  // configured approver(s). The actual upfront charges fire only when an
  // approver clicks the link in that email. Issue #288 closed the
  // earlier "Execute Purchase" button-label gap that implied immediate
  // execution; #289 will introduce a session-permission branch where
  // holders of `execute-any:purchases` can opt into direct execution and
  // this note will become conditional on the resolved auth path.
  const approvalNote = document.createElement('p');
  approvalNote.className = 'approval-required-note';
  approvalNote.textContent =
    'Submitting will email an approval request to the configured approver — commitments are charged only after the approver clicks the link in that email.';
  container.appendChild(approvalNote);

  // Commitments table with per-row Include checkboxes, Term, and Payment selects.
  const commitsSection = document.createElement('div');
  commitsSection.className = 'form-section purchase-modal-commits';

  const table = document.createElement('table');
  table.className = 'purchase-modal-table';

  // Table header: select-all checkbox + per-column labels.
  const thead = document.createElement('thead');
  const headRow = document.createElement('tr');

  // Select-all checkbox in the header (issue #320).
  const selectAllTh = document.createElement('th');
  const selectAllCb = document.createElement('input');
  selectAllCb.type = 'checkbox';
  selectAllCb.id = 'purchase-modal-select-all';
  selectAllCb.checked = true; // all selected by default
  selectAllCb.setAttribute('aria-label', 'Select all purchases');
  selectAllCb.title = 'Select / deselect all';
  selectAllTh.appendChild(selectAllCb);

  const includeLabel = document.createElement('span');
  includeLabel.className = 'purchase-modal-include-label';
  includeLabel.textContent = 'Include';
  selectAllTh.appendChild(includeLabel);
  headRow.appendChild(selectAllTh);

  for (const label of ['Account', 'Service / Type', 'Region', 'Count', 'Upfront', 'Monthly Cost', 'Eff. Savings', 'Eff. %', 'Term', 'Payment']) {
    const th = document.createElement('th');
    th.textContent = label;
    headRow.appendChild(th);
  }
  thead.appendChild(headRow);
  table.appendChild(thead);

  const tbody = document.createElement('tbody');
  for (let i = 0; i < currentPurchaseRecommendations.length; i++) {
    tbody.appendChild(renderPurchaseModalRow(i, seeds[i]!.source));
  }
  table.appendChild(tbody);

  // Totals row in tfoot (issue #320: live totals updated on checkbox toggle).
  const tfoot = document.createElement('tfoot');
  const totalsRow = document.createElement('tr');
  totalsRow.id = 'purchase-modal-totals-row';
  totalsRow.className = 'purchase-modal-totals';
  table.appendChild(tfoot);
  tfoot.appendChild(totalsRow);

  commitsSection.appendChild(table);
  container.appendChild(commitsSection);

  // Wire the select-all checkbox (must happen after tbody rows are in DOM
  // so per-row checkboxes can be queried).
  selectAllCb.addEventListener('change', () => {
    const allIndices = currentPurchaseRecommendations.map((_, i) => i);
    if (selectAllCb.checked) {
      checkedPurchaseIndices = new Set(allIndices);
    } else {
      checkedPurchaseIndices = new Set();
    }
    // Sync each row's checkbox to the new state.
    const rowCheckboxes = container.querySelectorAll<HTMLInputElement>('.purchase-modal-row-include');
    rowCheckboxes.forEach((cb) => {
      cb.checked = selectAllCb.checked;
    });
    updatePurchaseModalTotals(selectAllCb);
  });

  // Initial totals render and Execute button state.
  updatePurchaseModalTotals(selectAllCb);

  const purchaseModal = document.getElementById('purchase-modal');
  if (purchaseModal) openModal(purchaseModal);
}

/**
 * Updates the totals row and Execute button disabled state based on the
 * current `checkedPurchaseIndices` set. Also syncs the select-all
 * checkbox's indeterminate/checked/unchecked state.
 *
 * Called on modal open and on every per-row or select-all checkbox change.
 *
 * The `selectAllCb` parameter is the select-all header checkbox element,
 * passed in so the live-update handler doesn't need to re-query it.
 */
function updatePurchaseModalTotals(selectAllCb: HTMLInputElement): void {
  const totalsRow = document.getElementById('purchase-modal-totals-row');

  // Compute totals over checked indices only.
  let totalCount = 0;
  let totalUpfront = 0;
  let totalMonthlyCost = 0;
  let totalEffSavings = 0;
  // Weighted-average effective %: sum effective savings / sum on-demand monthly.
  let weightedEffSavingsNum = 0; // numerator: sum of effectiveMonthlySavings per checked rec
  let weightedEffSavingsDen = 0; // denominator: sum of on-demand monthly per checked rec
  let hasMonthlyCostData = false;

  for (const idx of checkedPurchaseIndices) {
    const rec = currentPurchaseRecommendations[idx];
    if (!rec) continue;
    totalCount += rec.count;
    totalUpfront += rec.upfront_cost;
    const monthlyCost = rec.monthly_cost;
    if (monthlyCost != null) {
      totalMonthlyCost += monthlyCost;
      hasMonthlyCostData = true;
    }
    // Weighted-average effective % denominator should match effectiveSavingsPct:
    // prefer provider-supplied on_demand_cost when present, otherwise fall back
    // to the reconstructed monthly_cost + savings + amortized baseline.
    // Include rows that only have on_demand_cost so they are not skipped.
    if (rec.term) {
      const amortized = rec.upfront_cost / (rec.term * 12);
      const effSav = effectiveMonthlySavings(rec);
      const hasOnDemand = rec.on_demand_cost != null && rec.on_demand_cost > 0;
      const onDemand = hasOnDemand
        ? rec.on_demand_cost
        : monthlyCost != null
          ? monthlyCost + rec.savings + amortized
          : null;
      if (onDemand != null && onDemand > 0) {
        weightedEffSavingsNum += effSav;
        weightedEffSavingsDen += onDemand;
      }
    }
    totalEffSavings += effectiveMonthlySavings(rec);
  }

  // Rebuild the totals row DOM. Clear first.
  if (totalsRow) {
    while (totalsRow.firstChild) totalsRow.removeChild(totalsRow.firstChild);

    // Placeholder for the Include checkbox column.
    const tdBlank = document.createElement('td');
    tdBlank.textContent = '';
    totalsRow.appendChild(tdBlank);

    // Account column placeholder.
    const tdTotalLabel = document.createElement('td');
    const strong = document.createElement('strong');
    strong.textContent = 'Totals';
    tdTotalLabel.appendChild(strong);
    totalsRow.appendChild(tdTotalLabel);

    // Service / Type placeholder.
    const tdBlank2 = document.createElement('td');
    totalsRow.appendChild(tdBlank2);

    // Region placeholder.
    const tdBlank3 = document.createElement('td');
    totalsRow.appendChild(tdBlank3);

    // Count.
    const tdCount = document.createElement('td');
    tdCount.id = 'purchase-modal-total-count';
    const countStrong = document.createElement('strong');
    countStrong.textContent = String(totalCount);
    tdCount.appendChild(countStrong);
    totalsRow.appendChild(tdCount);

    // Upfront.
    const tdUpfront = document.createElement('td');
    tdUpfront.id = 'purchase-modal-total-upfront';
    const upfrontStrong = document.createElement('strong');
    upfrontStrong.textContent = formatCurrency(totalUpfront);
    tdUpfront.appendChild(upfrontStrong);
    totalsRow.appendChild(tdUpfront);

    // Monthly cost.
    const tdMonthly = document.createElement('td');
    tdMonthly.id = 'purchase-modal-total-monthly';
    const monthlyStrong = document.createElement('strong');
    monthlyStrong.textContent = hasMonthlyCostData ? formatCurrency(totalMonthlyCost) : '—';
    tdMonthly.appendChild(monthlyStrong);
    totalsRow.appendChild(tdMonthly);

    // Effective savings.
    const tdEffSav = document.createElement('td');
    tdEffSav.id = 'purchase-modal-total-eff-savings';
    tdEffSav.className = 'savings';
    const effSavStrong = document.createElement('strong');
    effSavStrong.textContent = formatCurrency(totalEffSavings);
    tdEffSav.appendChild(effSavStrong);
    totalsRow.appendChild(tdEffSav);

    // Effective % (weighted average over on-demand monthly).
    const tdEffPct = document.createElement('td');
    tdEffPct.id = 'purchase-modal-total-eff-pct';
    const effPctStrong = document.createElement('strong');
    if (weightedEffSavingsDen > 0) {
      const avgPct = (weightedEffSavingsNum / weightedEffSavingsDen) * 100;
      effPctStrong.textContent = avgPct.toFixed(1) + '%';
      if (avgPct < 0) tdEffPct.className = 'effective-pct-negative';
    } else {
      effPctStrong.textContent = '—';
    }
    tdEffPct.appendChild(effPctStrong);
    totalsRow.appendChild(tdEffPct);

    // Term and Payment column placeholders (editable per row, no aggregate).
    const tdBlankTerm = document.createElement('td');
    totalsRow.appendChild(tdBlankTerm);
    const tdBlankPayment = document.createElement('td');
    totalsRow.appendChild(tdBlankPayment);
  }

  // Update Execute button disabled state.
  const executeBtn = document.getElementById('execute-purchase-btn') as HTMLButtonElement | null;
  if (executeBtn) {
    const noneSelected = checkedPurchaseIndices.size === 0;
    executeBtn.disabled = noneSelected;
    executeBtn.title = noneSelected ? 'Select at least one purchase' : '';
  }

  // Sync select-all checkbox indeterminate/checked/unchecked state.
  const total = currentPurchaseRecommendations.length;
  const checked = checkedPurchaseIndices.size;
  if (checked === 0) {
    selectAllCb.checked = false;
    selectAllCb.indeterminate = false;
  } else if (checked === total) {
    selectAllCb.checked = true;
    selectAllCb.indeterminate = false;
  } else {
    selectAllCb.checked = false;
    selectAllCb.indeterminate = true;
  }
}

// renderPurchaseModalRow builds one editable <tr> for the per-row
// purchase modal. Idx is the index into currentPurchaseRecommendations
// — change handlers locate the live rec via that index so subsequent
// edits to other rows don't stale-close over an outdated array
// reference. The modal does NOT re-render mid-edit; only the row's own
// Payment <select> options are rebuilt on a Term change.
//
// Issue #320: adds Include checkbox (col 0), Account (col 1), and
// Upfront/Monthly Cost/Eff. Savings/Eff. % columns before Term/Payment.
// The checkbox change handler updates checkedPurchaseIndices and calls
// updatePurchaseModalTotals so the live totals row and Execute button
// state stay in sync.
function renderPurchaseModalRow(idx: number, paymentSource: 'override' | 'rec' | 'fallback'): HTMLTableRowElement {
  const rec = currentPurchaseRecommendations[idx]!;
  const tr = document.createElement('tr');
  tr.dataset['recIdx'] = String(idx);

  // Issue #320: Include checkbox (col 0).
  const includeTd = document.createElement('td');
  const includeCb = document.createElement('input');
  includeCb.type = 'checkbox';
  includeCb.className = 'purchase-modal-row-include';
  includeCb.checked = checkedPurchaseIndices.has(idx);
  includeCb.setAttribute('aria-label', `Include row ${idx + 1}`);
  includeTd.appendChild(includeCb);
  tr.appendChild(includeTd);

  // Account (col 1): display name from accountNamesCache, fallback to ID.
  const accountTd = document.createElement('td');
  const accountName = rec.cloud_account_id
    ? (accountNamesCache.get(rec.cloud_account_id) || rec.cloud_account_id)
    : '—';
  accountTd.appendChild(document.createTextNode(accountName));
  tr.appendChild(accountTd);

  // Service / Type (col 2): service + resource_type combined.
  const serviceTypeTd = document.createElement('td');
  serviceTypeTd.appendChild(document.createTextNode(rec.service));
  if (rec.resource_type) {
    const typeSpan = document.createElement('span');
    typeSpan.className = 'purchase-modal-resource-type';
    typeSpan.appendChild(document.createTextNode(' / ' + rec.resource_type));
    serviceTypeTd.appendChild(typeSpan);
  }
  tr.appendChild(serviceTypeTd);

  // Region (col 3).
  const regionCell = document.createElement('td');
  regionCell.appendChild(document.createTextNode(rec.region));
  tr.appendChild(regionCell);

  // Count (col 4).
  const countCell = document.createElement('td');
  countCell.appendChild(document.createTextNode(String(rec.count)));
  tr.appendChild(countCell);

  // Upfront (col 5): upfront_cost is already the total for all count units.
  const upfrontTd = document.createElement('td');
  upfrontTd.appendChild(document.createTextNode(formatCurrency(rec.upfront_cost)));
  tr.appendChild(upfrontTd);

  // Monthly cost (col 6): null when provider API didn't return it.
  const monthlyCostTd = document.createElement('td');
  monthlyCostTd.appendChild(
    document.createTextNode(rec.monthly_cost != null ? formatCurrency(rec.monthly_cost) : '—'),
  );
  tr.appendChild(monthlyCostTd);

  // Effective monthly savings (col 7): reuses effectiveMonthlySavings helper.
  // savings and upfront_cost are already total-for-rec values, so the helper
  // returns the aggregate effective savings for all count units.
  const effSavTd = document.createElement('td');
  effSavTd.className = 'savings';
  effSavTd.appendChild(document.createTextNode(formatCurrency(effectiveMonthlySavings(rec))));
  tr.appendChild(effSavTd);

  // Effective % (col 8): reuses effectiveSavingsPct helper.
  const effPctTd = document.createElement('td');
  const pct = effectiveSavingsPct(rec);
  if (pct !== null && pct < 0) effPctTd.className = 'effective-pct-negative';
  effPctTd.appendChild(document.createTextNode(pct !== null ? pct.toFixed(1) + '%' : '—'));
  tr.appendChild(effPctTd);

  // Term select (col 9). AWS/Azure/GCP commitments universally support 1y and 3y;
  // on change we rederive Payment options for the new term and pick a
  // still-valid value if the current one becomes unsupported.
  const termCell = document.createElement('td');
  const termSelect = document.createElement('select');
  termSelect.className = 'purchase-row-term';
  for (const t of [1, 3]) {
    const opt = document.createElement('option');
    opt.value = String(t);
    opt.textContent = `${t}yr`;
    if (t === rec.term) opt.selected = true;
    termSelect.appendChild(opt);
  }
  termCell.appendChild(termSelect);
  tr.appendChild(termCell);

  // Payment select (col 10). Options come from paymentOptionsFor (already
  // filtered to supported values for this provider/service/term cell),
  // so the user can never pick an unsupported combo through the UI.
  const paymentCell = document.createElement('td');
  const paymentSelect = document.createElement('select');
  paymentSelect.className = 'purchase-row-payment';
  rebuildPaymentOptions(paymentSelect, rec.provider as CompatProvider, rec.service, rec.term as 1 | 3, (rec.payment ?? '') as CompatPayment);
  paymentCell.appendChild(paymentSelect);
  if (paymentSource === 'override') {
    const sourceNote = document.createElement('span');
    sourceNote.className = 'purchase-row-payment-source';
    sourceNote.textContent = ' (from account override)';
    paymentCell.appendChild(sourceNote);
  }
  tr.appendChild(paymentCell);

  // Issue #320: Include checkbox change handler.
  // Must query the select-all checkbox from the DOM each time because
  // the handler outlives the initial render call.
  includeCb.addEventListener('change', () => {
    if (includeCb.checked) {
      checkedPurchaseIndices.add(idx);
    } else {
      checkedPurchaseIndices.delete(idx);
    }
    const selectAllCb = document.getElementById('purchase-modal-select-all') as HTMLInputElement | null;
    if (selectAllCb) updatePurchaseModalTotals(selectAllCb);
  });

  termSelect.addEventListener('change', () => {
    const live = currentPurchaseRecommendations[idx];
    if (!live) return;
    const newTerm = parseInt(termSelect.value, 10) === 3 ? 3 : 1;
    live.term = newTerm;
    // Rebuild this row's payment options for the new term; if current
    // payment is no longer supported, pick the first valid option and
    // mirror back to live state.
    rebuildPaymentOptions(
      paymentSelect,
      live.provider as CompatProvider,
      live.service,
      newTerm,
      (live.payment ?? '') as CompatPayment,
    );
    live.payment = paymentSelect.value;
  });

  paymentSelect.addEventListener('change', () => {
    const live = currentPurchaseRecommendations[idx];
    if (!live) return;
    live.payment = paymentSelect.value;
  });

  return tr;
}

// rebuildPaymentOptions clears and re-populates a <select> with the
// supported Payment options for a (provider, service, term) cell.
// If `desired` is in the new option set, it stays selected; otherwise
// the first option wins and the select's `.value` reflects that.
function rebuildPaymentOptions(
  select: HTMLSelectElement,
  provider: CompatProvider,
  service: string,
  term: 1 | 3,
  desired: CompatPayment | '',
): void {
  while (select.firstChild) select.removeChild(select.firstChild);
  const options = paymentOptionsFor(provider, service, term);
  let matched = false;
  for (const opt of options) {
    const o = document.createElement('option');
    o.value = opt;
    o.textContent = opt;
    if (opt === desired) {
      o.selected = true;
      matched = true;
    }
    select.appendChild(o);
  }
  if (!matched && options.length > 0) {
    select.value = options[0]!;
  }
}

/**
 * Refresh recommendations from API
 */
export async function refreshRecommendations(): Promise<void> {
  try {
    await api.refreshRecommendations();
    alert('Recommendation refresh started. This may take a few minutes.');
    setTimeout(() => void loadRecommendations(), 5000);
  } catch (error) {
    console.error('Failed to refresh recommendations:', error);
    alert('Failed to start recommendation refresh');
  }
}
