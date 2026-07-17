/**
 * Application state management
 */

import type { AppState } from './types';
import type { Recommendation } from './api/types';

// Closed enumeration of column ids the per-column filters target.
// Typo-safety: misspellings at call sites become compile errors.
export type RecommendationsColumnId =
  | 'provider' | 'account' | 'service' | 'resource_type' | 'capacity' | 'region'
  | 'count' | 'term' | 'payment' | 'savings' | 'upfront_cost'
  | 'monthly_cost' | 'on_demand_monthly' | 'effective_savings_pct'
  | 'usage_history';

// Every visible column is sortable, so the sort column type is exactly the
// column id set. Aliasing here keeps the two in sync automatically — adding
// a future column to RecommendationsColumnId automatically makes it sortable
// without a separate edit to this type.
export type RecommendationsSortColumn = RecommendationsColumnId;
export interface RecommendationsSort {
  column: RecommendationsSortColumn;
  direction: 'asc' | 'desc';
}

export type RecommendationsColumnFilter =
  | { kind: 'set'; values: string[] }   // categorical — values always string-form
  | { kind: 'expr'; expr: string };     // numeric — parsed on apply

export type RecommendationsColumnFilters = Partial<
  Record<RecommendationsColumnId, RecommendationsColumnFilter>
>;

// Singleton state instance
export const state: AppState = {
  currentUser: null,
  currentProvider: '',
  currentAccountIDs: [],
  currentRecommendations: [],
  selectedRecommendations: new Set(),
  savingsChart: null
};

// Sort state is separate from AppState so the recommendations module can
// evolve it independently. Default is savings descending (the audit's
// most-requested view: "biggest wins first").
let recommendationsSort: RecommendationsSort = { column: 'savings', direction: 'desc' };

export function getRecommendationsSort(): RecommendationsSort {
  return { ...recommendationsSort };
}

export function setRecommendationsSort(sort: RecommendationsSort): void {
  recommendationsSort = { ...sort };
}

// State accessor functions
export function getCurrentUser() {
  return state.currentUser;
}

export function setCurrentUser(user: AppState['currentUser']) {
  state.currentUser = user;
}

// ──────────────────────────────────────────────────────────────────────────
// Filter subscription pattern (issue #344 T2).
//
// Each section (Home / Opportunities / Plans / Purchases) used to bind its
// own `change` listener to a per-section `<select>` to know when the
// provider/account filter changed. With the filter relocated to the topbar,
// every section subscribes through this module instead. Topbar-filters
// updates state via setCurrentProvider/setCurrentAccountIDs; subscribers
// then re-run their loaders.
//
// Unsubscribe is returned so tests and dev-only init paths can clean up.
// ──────────────────────────────────────────────────────────────────────────
type Listener = () => void;
const providerListeners: Set<Listener> = new Set();
const accountListeners: Set<Listener> = new Set();

export function subscribeProvider(cb: Listener): () => void {
  providerListeners.add(cb);
  return () => providerListeners.delete(cb);
}

export function subscribeAccount(cb: Listener): () => void {
  accountListeners.add(cb);
  return () => accountListeners.delete(cb);
}

export function getCurrentProvider() {
  return state.currentProvider;
}

export function setCurrentProvider(provider: AppState['currentProvider']) {
  const changed = state.currentProvider !== provider;
  state.currentProvider = provider;
  if (changed) {
    providerListeners.forEach((cb) => {
      try { cb(); } catch (err) { console.warn('subscribeProvider listener error:', err); }
    });
  }
}

export function getRecommendations(): AppState['currentRecommendations'] {
  return [...state.currentRecommendations];
}

export function setRecommendations(recs: AppState['currentRecommendations']) {
  state.currentRecommendations = recs;
}

// getSelectedRecommendationIDs returns a snapshot of currently-selected
// rec IDs. Callers intersect this with the visible (post-filter) list
// to resolve "selected AND visible" — selections outside the filter
// are silently ignored as stale.
export function getSelectedRecommendationIDs(): ReadonlySet<string> {
  return new Set(state.selectedRecommendations);
}

export function clearSelectedRecommendations() {
  state.selectedRecommendations.clear();
}

export function addSelectedRecommendation(id: string) {
  state.selectedRecommendations.add(id);
}

export function removeSelectedRecommendation(id: string) {
  state.selectedRecommendations.delete(id);
}

export function getSavingsChart() {
  // Returns chart instance by reference. Callers may need to call methods on it.
  return state.savingsChart;
}

export function setSavingsChart(chart: AppState['savingsChart']) {
  state.savingsChart = chart;
}

export function getCurrentAccountIDs(): string[] {
  return [...state.currentAccountIDs];
}

export function setCurrentAccountIDs(ids: string[]) {
  const current = state.currentAccountIDs;
  const changed =
    ids.length !== current.length ||
    ids.some((id, i) => id !== current[i]);
  state.currentAccountIDs = ids;
  if (changed) {
    accountListeners.forEach((cb) => {
      try { cb(); } catch (err) { console.warn('subscribeAccount listener error:', err); }
    });
  }
}

// Per-column filters live in their own module-scoped state, in-memory only.
// Survives tab-switch within the page; resets on full page reload. Not
// persisted to localStorage on this iteration — see the plan's "Out of
// scope" for the persistence follow-up.
let recommendationsColumnFilters: RecommendationsColumnFilters = {};

export function getRecommendationsColumnFilters(): RecommendationsColumnFilters {
  return { ...recommendationsColumnFilters };
}

export function setRecommendationsColumnFilter(
  column: RecommendationsColumnId,
  filter: RecommendationsColumnFilter | null,
): void {
  if (filter === null) {
    const next = { ...recommendationsColumnFilters };
    delete next[column];
    recommendationsColumnFilters = next;
    return;
  }
  recommendationsColumnFilters = {
    ...recommendationsColumnFilters,
    [column]: filter,
  };
}

export function clearAllRecommendationsColumnFilters(): void {
  recommendationsColumnFilters = {};
}

// Post-filter visible list, set by recommendations.ts on every render
// and read by plans.ts so plan-creation never includes filtered-out
// rows. Defensive clone on read so callers can't mutate module state.
let visibleRecommendations: readonly Recommendation[] = [];

export function getVisibleRecommendations(): readonly Recommendation[] {
  return [...visibleRecommendations];
}

export function setVisibleRecommendations(recs: readonly Recommendation[]): void {
  visibleRecommendations = [...recs];
}

// ---------------------------------------------------------------------------
// Cost-period selector (issue #319)
// Persisted in localStorage('cudly.recs.costPeriod'). In-memory fallback
// when localStorage is unavailable (private browsing, quota-exceeded).
// ---------------------------------------------------------------------------

export type CostPeriod = 'hourly' | 'daily' | 'monthly' | 'yearly';

const COST_PERIOD_LS_KEY = 'cudly.recs.costPeriod';
const VALID_PERIODS = new Set<string>(['hourly', 'daily', 'monthly', 'yearly']);

// In-memory fallback; authoritative when localStorage is unavailable.
let costPeriodMemory: CostPeriod = 'monthly';

export function getCostPeriod(): CostPeriod {
  try {
    const raw = localStorage.getItem(COST_PERIOD_LS_KEY);
    if (raw === null) {
      // No prior write — return current in-memory state (default monthly on
      // module load, last setCostPeriod() value otherwise).
      return costPeriodMemory;
    }
    if (VALID_PERIODS.has(raw)) {
      costPeriodMemory = raw as CostPeriod;
      return raw as CostPeriod;
    }
    // Corrupted/invalid value persisted — fall back to the static default
    // ('monthly') rather than whatever leaked into in-memory state.
    return 'monthly';
  } catch {
    // localStorage unavailable (private browsing, iframe sandbox) — use memory.
  }
  return costPeriodMemory;
}

export function setCostPeriod(period: CostPeriod): void {
  costPeriodMemory = period;
  try {
    localStorage.setItem(COST_PERIOD_LS_KEY, period);
  } catch {
    // Non-fatal; in-memory fallback remains correct for the session.
  }
}

// ---------------------------------------------------------------------------
// RI Exchange per-column filters (issue #166 follow-up to merged #570).
//
// Scoped to the RI Exchange reshape-recommendations table. Independent of
// the recommendations slice so the two tabs don't fight over column-id
// shape — RI Exchange's columns are reshape-specific (source/target
// instance types, normalized units, utilization %).
// In-memory only; resets on page reload. Persistence is out of scope for
// this PR, same as the recommendations slice.
// ---------------------------------------------------------------------------
export type RiExchangeColumnId =
  | 'source_ri_id' | 'source_instance_type' | 'target_instance_type' | 'reason'
  | 'source_count' | 'target_count' | 'utilization_percent'
  | 'normalized_used' | 'normalized_purchased';

export type RiExchangeColumnFilter =
  | { kind: 'set'; values: string[] }   // categorical — string-form values
  | { kind: 'expr'; expr: string };     // numeric — parsed on apply

export type RiExchangeColumnFilters = Partial<
  Record<RiExchangeColumnId, RiExchangeColumnFilter>
>;

let riExchangeColumnFilters: RiExchangeColumnFilters = {};

export function getRiExchangeColumnFilters(): RiExchangeColumnFilters {
  return { ...riExchangeColumnFilters };
}

export function setRiExchangeColumnFilter(
  column: RiExchangeColumnId,
  filter: RiExchangeColumnFilter | null,
): void {
  if (filter === null) {
    const next = { ...riExchangeColumnFilters };
    delete next[column];
    riExchangeColumnFilters = next;
    return;
  }
  riExchangeColumnFilters = {
    ...riExchangeColumnFilters,
    [column]: filter,
  };
}

export function clearAllRiExchangeColumnFilters(): void {
  riExchangeColumnFilters = {};
}

// ---------------------------------------------------------------------------
// Active Convertible RIs per-column filters (issue #1414).
//
// Mirrors the reshape-recommendations filter slice above but scoped to the
// Active Convertible RIs table (ri-exchange-instances-list). Column IDs map
// directly to ConvertibleRI / RIUtilization API fields.
// ---------------------------------------------------------------------------

export type ActiveRiColumnId =
  | 'instance_type' | 'availability_zone' | 'offering_type'
  | 'instance_count' | 'utilization_pct';

export type ActiveRiColumnFilter =
  | { kind: 'set'; values: string[] }
  | { kind: 'expr'; expr: string };

export type ActiveRiColumnFilters = Partial<Record<ActiveRiColumnId, ActiveRiColumnFilter>>;

let activeRiColumnFilters: ActiveRiColumnFilters = {};

export function getActiveRiColumnFilters(): ActiveRiColumnFilters {
  return { ...activeRiColumnFilters };
}

export function setActiveRiColumnFilter(
  column: ActiveRiColumnId,
  filter: ActiveRiColumnFilter | null,
): void {
  if (filter === null) {
    const next = { ...activeRiColumnFilters };
    delete next[column];
    activeRiColumnFilters = next;
    return;
  }
  activeRiColumnFilters = { ...activeRiColumnFilters, [column]: filter };
}

export function clearAllActiveRiColumnFilters(): void {
  activeRiColumnFilters = {};
}

// ---------------------------------------------------------------------------
// Per-column visibility state (issue #318).
// A column id in this set is HIDDEN; an absent id is visible (default visible).
// In-memory only; the localStorage layer lives in recommendations.ts alongside
// the other localStorage helpers (loadBulkPurchaseState / saveBulkPurchaseState).
// ---------------------------------------------------------------------------
let hiddenColumns: Set<RecommendationsColumnId> = new Set();

export function getHiddenColumns(): ReadonlySet<RecommendationsColumnId> {
  return new Set(hiddenColumns);
}

export function setHiddenColumns(hidden: ReadonlySet<RecommendationsColumnId>): void {
  // Filter out fixed anchor columns that must always remain visible
  const fixedColumns: ReadonlySet<RecommendationsColumnId> = new Set(['provider', 'account', 'service', 'resource_type']);
  const filtered = Array.from(hidden).filter((col) => !fixedColumns.has(col));
  hiddenColumns = new Set(filtered);
}

// ---------------------------------------------------------------------------
// Plans / Planned Purchases per-column filters (issue #166 follow-up to #570).
//
// Mirrors RecommendationsColumnFilters but is scoped to the Plans tab's
// Planned Purchases table. Kept as a separate slice (and column-id type) so
// the Plans, History, and RI Exchange follow-up PRs can land in parallel
// without contending on the same state shape.
// ---------------------------------------------------------------------------

export type PlansColumnId =
  | 'provider' | 'service' | 'resource_type' | 'term' | 'payment' | 'status'
  | 'count' | 'upfront_cost' | 'estimated_savings';

export type PlansColumnFilter =
  | { kind: 'set'; values: string[] }
  | { kind: 'expr'; expr: string };

export type PlansColumnFilters = Partial<Record<PlansColumnId, PlansColumnFilter>>;

// In-memory only; survives tab switches within the SPA, resets on full reload.
// Matches the recommendationsColumnFilters lifecycle.
let plansColumnFilters: PlansColumnFilters = {};

export function getPlansColumnFilters(): PlansColumnFilters {
  return { ...plansColumnFilters };
}

export function setPlansColumnFilter(
  column: PlansColumnId,
  filter: PlansColumnFilter | null,
): void {
  if (filter === null) {
    const next = { ...plansColumnFilters };
    delete next[column];
    plansColumnFilters = next;
    return;
  }
  plansColumnFilters = {
    ...plansColumnFilters,
    [column]: filter,
  };
}

export function clearAllPlansColumnFilters(): void {
  plansColumnFilters = {};
}

// ---------------------------------------------------------------------------
// Amortize-upfront toggle (issue #1112).
// Persisted in localStorage('cudly.amortizeUpfront'). In-memory fallback
// when localStorage is unavailable (private browsing, quota-exceeded).
// Subscribers are notified on every change so all views re-render in sync.
// ---------------------------------------------------------------------------

const AMORTIZE_UPFRONT_LS_KEY = 'cudly.amortizeUpfront';

let amortizeUpfrontMemory = false;

export function getAmortizeUpfront(): boolean {
  try {
    const raw = localStorage.getItem(AMORTIZE_UPFRONT_LS_KEY);
    if (raw === null) return amortizeUpfrontMemory;
    amortizeUpfrontMemory = raw === 'true';
    return amortizeUpfrontMemory;
  } catch {
    // localStorage unavailable (private browsing, iframe sandbox) -- use memory.
  }
  return amortizeUpfrontMemory;
}

export function setAmortizeUpfront(value: boolean): void {
  amortizeUpfrontMemory = value;
  try {
    localStorage.setItem(AMORTIZE_UPFRONT_LS_KEY, String(value));
  } catch {
    // Non-fatal; in-memory fallback remains correct for the session.
  }
  amortizeListeners.forEach((cb) => {
    try { cb(); } catch (err) { console.warn('subscribeAmortizeUpfront listener error:', err); }
  });
}

const amortizeListeners: Set<() => void> = new Set();

export function subscribeAmortizeUpfront(cb: () => void): () => void {
  amortizeListeners.add(cb);
  return () => amortizeListeners.delete(cb);
}
