/**
 * Application state management
 */

import type { AppState } from './types';
import type { Recommendation } from './api/types';

// Closed enumeration of column ids the per-column filters target.
// Typo-safety: misspellings at call sites become compile errors.
export type RecommendationsColumnId =
  | 'provider' | 'account' | 'service' | 'resource_type' | 'region'
  | 'count' | 'term' | 'payment' | 'savings' | 'upfront_cost'
  | 'monthly_cost' | 'on_demand_monthly' | 'effective_savings_pct';

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

export function getCurrentProvider() {
  return state.currentProvider;
}

export function setCurrentProvider(provider: AppState['currentProvider']) {
  state.currentProvider = provider;
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
  state.currentAccountIDs = ids;
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
