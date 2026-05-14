/**
 * Navigation module for CUDly
 */

import { loadDashboard } from './dashboard';
import { loadRecommendations } from './recommendations';
import { loadPlans } from './plans';
import { initHistoryDateRange, loadHistory } from './history';
import { loadGlobalSettings, isUnsavedChanges, loadAccountsTab } from './settings';
import { loadUsers } from './users';
import { loadApiKeys } from './apikeys';
import { loadSavingsHistory } from './modules/savings-history';
import { loadAutomationSettings } from './riexchange';
import { loadInventory } from './inventory';
import { isAdmin } from './auth';

interface TabMeta {
  title: string;
}

const TABS: Record<string, TabMeta> = {
  home: { title: 'CUDly — Home' },
  opportunities: { title: 'CUDly — Opportunities' },
  plans: { title: 'CUDly — Plans' },
  purchases: { title: 'CUDly — Purchases' },
  inventory: { title: 'CUDly — Inventory & Coverage' },
  admin: { title: 'CUDly — Admin' },
};

/**
 * Legacy path → new tab name. Lets old bookmarks (/dashboard, /recommendations,
 * /history, /settings/..., /ri-exchange) keep resolving after the issue #340
 * IA rename + Inventory & Coverage umbrella fold-in. Applied in
 * applyTabFromPath(); we replaceState() to the canonical new URL so the
 * user's address bar reflects the current IA.
 */
const LEGACY_PATH_REDIRECTS: Record<string, string> = {
  dashboard: 'home',
  recommendations: 'opportunities',
  history: 'purchases',
  settings: 'admin',
  'ri-exchange': 'inventory',
};

const SETTINGS_SUBTABS: Record<string, { title: string }> = {
  general:    { title: 'CUDly — Admin · General' },
  purchasing: { title: 'CUDly — Admin · Purchasing policies' },
  accounts:   { title: 'CUDly — Admin · Accounts & onboarding' },
  users:      { title: 'CUDly — Admin · Users, roles & API keys' },
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
  if (!(tabName in TABS)) tabName = 'home';

  const isSelfSwitch = tabName === currentTab;

  if (
    !opts.skipDirtyGuard &&
    currentTab === 'admin' &&
    tabName !== 'admin' &&
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
    case 'home':
      void loadDashboard();
      break;
    case 'opportunities':
      void loadRecommendations();
      break;
    case 'plans':
      void loadPlans();
      break;
    case 'purchases':
      initHistoryDateRange();
      void loadSavingsHistory();
      // Auto-load history so the Approval queue card and the Purchase
      // History table populate on first visit, without requiring the
      // user to click "Load History" just to see pending approvals.
      // Matches the loadSavingsHistory pattern above. Both fetch
      // small, fast, scope-already-filtered payloads.
      void loadHistory();
      break;
    case 'admin':
      switchSettingsSubTab(getSettingsSubTabFromPath(), { push: false });
      break;
    case 'inventory':
      loadInventory();
      break;
  }

  if (isSelfSwitch) return;

  if (tabName !== 'admin') {
    document.title = TABS[tabName]!.title;
  }
  currentTab = tabName;

  if (opts.push !== false) {
    historyId += 1;
    const url = tabName === 'admin'
      ? '/admin/' + (currentSettingsSubTab ?? 'general')
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

  document.querySelectorAll<HTMLButtonElement>('#admin-tab .sub-tab-btn').forEach(btn => {
    const isActive = btn.dataset['settingsTab'] === subTab;
    btn.classList.toggle('active', isActive);
    btn.setAttribute('aria-selected', isActive ? 'true' : 'false');
  });

  for (const [tab, ids] of Object.entries(SECTION_MAP)) {
    for (const id of ids) {
      const el = document.getElementById(id);
      if (el) el.classList.toggle('hidden', tab !== subTab);
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
      { tab: 'admin', subTab, id: historyId },
      '',
      '/admin/' + subTab + window.location.search + window.location.hash,
    );
  }
}

/**
 * Resolve the current URL to a known tab name. Normalizes case, leading/
 * trailing slashes, and sub-paths (only the first segment is matched).
 * Unknown or empty paths fall back to 'home'.
 *
 * Pre-issue-#340 paths (/dashboard, /recommendations, /history, /settings)
 * are resolved through LEGACY_PATH_REDIRECTS so old bookmarks still land
 * on the intended section.
 */
export function applyTabFromPath(): string {
  const segment = window.location.pathname
    .replace(/^\/+/, '')
    .replace(/\/+$/, '')
    .split('/')[0]
    ?.toLowerCase() ?? '';
  if (segment === '') return 'home';
  if (segment in LEGACY_PATH_REDIRECTS) {
    const canonical = LEGACY_PATH_REDIRECTS[segment]!;
    window.history.replaceState(null, '', '/' + canonical + window.location.search + window.location.hash);
    return canonical;
  }
  return segment in TABS ? segment : 'home';
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
      currentTab === 'admin' &&
      target !== 'admin' &&
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
