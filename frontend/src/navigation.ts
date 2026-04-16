/**
 * Navigation module for CUDly
 */

import { loadDashboard } from './dashboard';
import { loadRecommendations } from './recommendations';
import { loadPlans } from './plans';
import { initHistoryDateRange } from './history';
import { loadGlobalSettings, isUnsavedChanges, loadAccountsTab } from './settings';
import { loadUsers } from './users';
import { loadApiKeys } from './apikeys';
import { loadSavingsHistory } from './modules/savings-history';
import { loadRIExchange, loadAutomationSettings } from './riexchange';
import { isAdmin } from './auth';

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

const SETTINGS_SUBTABS: Record<string, { title: string }> = {
  general:    { title: 'CUDly — Settings' },
  purchasing: { title: 'CUDly — Purchasing' },
  accounts:   { title: 'CUDly — Accounts' },
  users:      { title: 'CUDly — Users & API Keys' },
};

const SECTION_MAP: Record<string, string[]> = {
  general:    ['settings-section'],
  purchasing: ['purchasing-panel'],
  accounts:   ['accounts-section'],
  users:      ['users-section', 'apikeys-section'],
};

let currentTab: string | undefined;
let currentSettingsSubTab: string | undefined;
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
      switchSettingsSubTab(getSettingsSubTabFromPath(), { push: false });
      break;
    case 'ri-exchange':
      void loadRIExchange();
      break;
  }

  if (isSelfSwitch) return;

  if (tabName !== 'settings') {
    document.title = TABS[tabName]!.title;
  }
  currentTab = tabName;

  if (opts.push !== false) {
    historyId += 1;
    const url = tabName === 'settings'
      ? '/settings/' + (currentSettingsSubTab ?? 'general')
      : '/' + tabName;
    window.history.pushState(
      { tab: tabName, id: historyId },
      '',
      url + window.location.search + window.location.hash,
    );
  }
}

/**
 * Parse the settings sub-tab from segment[1] of the current URL.
 * Falls back to 'general' for unknown or missing segments.
 */
export function getSettingsSubTabFromPath(): string {
  const segments = window.location.pathname
    .replace(/^\/+/, '')
    .replace(/\/+$/, '')
    .split('/');
  const sub = (segments[1] ?? '').toLowerCase();
  return sub in SETTINGS_SUBTABS ? sub : 'general';
}

/**
 * Switch between settings sub-tabs (General / Accounts / Users).
 * Manages section visibility, load lifecycle, and sub-tab URL history.
 */
export function switchSettingsSubTab(subTab: string, opts: SwitchTabOptions = {}): void {
  if (!(subTab in SETTINGS_SUBTABS)) subTab = 'general';

  if ((subTab === 'accounts' || subTab === 'users') && !isAdmin()) {
    subTab = 'general';
  }

  const isSelfSwitch = subTab === currentSettingsSubTab;

  document.querySelectorAll<HTMLButtonElement>('.sub-tab-btn').forEach(btn => {
    const isActive = btn.dataset['settingsTab'] === subTab;
    btn.classList.toggle('active', isActive);
    btn.setAttribute('aria-selected', isActive ? 'true' : 'false');
  });

  for (const [tab, ids] of Object.entries(SECTION_MAP)) {
    for (const id of ids) {
      const el = document.getElementById(id);
      if (el) el.style.display = (tab === subTab) ? '' : 'none';
    }
  }

  switch (subTab) {
    case 'general':
      void loadGlobalSettings();
      break;
    case 'purchasing':
      void loadGlobalSettings();
      void loadAutomationSettings();
      break;
    case 'accounts':
      void loadAccountsTab();
      break;
    case 'users':
      void loadUsers();
      void loadApiKeys();
      break;
  }

  document.title = SETTINGS_SUBTABS[subTab]!.title;
  currentSettingsSubTab = subTab;

  if (isSelfSwitch) return;

  if (opts.push !== false) {
    historyId += 1;
    window.history.pushState(
      { tab: 'settings', subTab, id: historyId },
      '',
      '/settings/' + subTab + window.location.search + window.location.hash,
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
        if (delta !== 0) window.history.go(-delta);
        return;
      }
    }

    historyId = newId;
    switchTab(target, { push: false, skipDirtyGuard: true });
  });
}
