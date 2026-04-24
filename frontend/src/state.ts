/**
 * Application state management
 */

import type { AppState } from './types';

export type RecommendationsSortColumn = 'savings' | 'upfront_cost' | 'count' | 'term' | 'payback';
export interface RecommendationsSort {
  column: RecommendationsSortColumn;
  direction: 'asc' | 'desc';
}

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
