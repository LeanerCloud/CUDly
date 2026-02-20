/**
 * Navigation module for CUDly
 */

import { loadDashboard } from './dashboard';
import { loadRecommendations } from './recommendations';
import { loadPlans } from './plans';
import { initHistoryDateRange } from './history';
import { loadGlobalSettings } from './settings';
import { loadUsers } from './users';
import { loadApiKeys } from './apikeys';
import { loadSavingsHistory } from './modules/savings-history';

/**
 * Switch between tabs
 */
export function switchTab(tabName: string): void {
  document.querySelectorAll<HTMLButtonElement>('.tab-btn').forEach(btn => {
    const isActive = btn.dataset['tab'] === tabName;
    btn.classList.toggle('active', isActive);
    btn.setAttribute('aria-selected', isActive ? 'true' : 'false');
  });

  document.querySelectorAll<HTMLElement>('.tab-content').forEach(content => {
    content.classList.toggle('active', content.id === `${tabName}-tab`);
  });

  switch (tabName) {
    case 'dashboard':
      void loadDashboard();
      break;
    case 'recommendations':
      void loadRecommendations();
      break;
    case 'plans':
      void loadPlans();
      break;
    case 'history':
      initHistoryDateRange();
      void loadSavingsHistory();
      break;
    case 'settings':
      void loadGlobalSettings();
      break;
    case 'users':
      void loadUsers();
      void loadApiKeys();
      break;
  }
}
