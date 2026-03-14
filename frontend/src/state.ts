/**
 * Application state management
 */

import type { AppState } from './types';

// Singleton state instance
export const state: AppState = {
  currentUser: null,
  currentProvider: 'all',
  currentRecommendations: [],
  selectedRecommendations: new Set(),
  savingsChart: null
};

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

export function getSelectedRecommendations(): ReadonlySet<number> {
  return new Set(state.selectedRecommendations);
}

export function clearSelectedRecommendations() {
  state.selectedRecommendations.clear();
}

export function addSelectedRecommendation(index: number) {
  state.selectedRecommendations.add(index);
}

export function removeSelectedRecommendation(index: number) {
  state.selectedRecommendations.delete(index);
}

export function getSavingsChart() {
  // Returns chart instance by reference. Callers may need to call methods on it.
  return state.savingsChart;
}

export function setSavingsChart(chart: AppState['savingsChart']) {
  state.savingsChart = chart;
}
