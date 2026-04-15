/**
 * Navigation module for CUDly
 */

import { loadDashboard } from './dashboard';
import { loadRecommendations } from './recommendations';
import { loadPlans } from './plans';
import { initHistoryDateRange } from './history';
import { loadGlobalSettings, isUnsavedChanges } from './settings';
import { loadUsers } from './users';
import { loadApiKeys } from './apikeys';
import { loadSavingsHistory } from './modules/savings-history';
import { loadRIExchange, loadAutomationSettings } from './riexchange';

interface TabMeta {
  title: string;
}

const TABS: Record<string, TabMeta> = {
  dashboard: { title: 'CUDly — Dashboard' },
  recommendations: { title: 'CUDly — Recommendations' },
  plans: { title: 'CUDly — Purchase Plans' },
  history: { title: 'CUDly — Purchase History' },
  'ri-exchange': { title: 'CUDly — RI Exchange' },
  settings: { title: 'CUDly — Settings' },
};

let currentTab: string | undefined;
let historyId = 0;

interface SwitchTabOptions {
  push?: boolean;
  skipDirtyGuard?: boolean;
}

/**
 * Switch between tabs
 */
export function switchTab(tabName: string, opts: SwitchTabOptions = {}): void {
  if (!(tabName in TABS)) tabName = 'dashboard';

  const isSelfSwitch = tabName === currentTab;

  if (
    !opts.skipDirtyGuard &&
    currentTab === 'settings' &&
    tabName !== 'settings' &&
    isUnsavedChanges()
  ) {
    if (!confirm('You have unsaved settings changes. Leave without saving?')) return;
  }

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
      void loadAutomationSettings();
      break;
    case 'users':
      void loadUsers();
      void loadApiKeys();
      break;
    case 'ri-exchange':
      void loadRIExchange();
      break;
  }

  if (isSelfSwitch) return;

  document.title = TABS[tabName]!.title;
  currentTab = tabName;

  if (opts.push !== false) {
    historyId += 1;
    window.history.pushState(
      { tab: tabName, id: historyId },
      '',
      '/' + tabName + window.location.search + window.location.hash,
    );
  }
}

/**
 * Resolve the current URL to a known tab name. Normalizes case, leading/
 * trailing slashes, and sub-paths (only the first segment is matched).
 * Unknown or empty paths fall back to 'dashboard'.
 */
export function applyTabFromPath(): string {
  const segment = window.location.pathname
    .replace(/^\/+/, '')
    .replace(/\/+$/, '')
    .split('/')[0]
    ?.toLowerCase() ?? '';
  if (segment === '') return 'dashboard';
  return segment in TABS ? segment : 'dashboard';
}

/**
 * Install the popstate listener that handles browser back/forward.
 * Must be called once during init() before any switchTab call.
 */
export function initRouter(): void {
  window.addEventListener('popstate', (e: PopStateEvent) => {
    const target = applyTabFromPath();
    const newId = (e.state as { id?: number } | null)?.id ?? 0;
    const delta = newId - historyId;

    if (
      currentTab === 'settings' &&
      target !== 'settings' &&
      isUnsavedChanges()
    ) {
      if (!confirm('You have unsaved settings changes. Leave without saving?')) {
        // Restore exact previous position. The induced second popstate
        // is harmless: target will equal currentTab so the self-switch
        // path inside switchTab no-ops on history.
        if (delta !== 0) window.history.go(-delta);
        return;
      }
    }

    historyId = newId;
    switchTab(target, { push: false, skipDirtyGuard: true });
  });
}
