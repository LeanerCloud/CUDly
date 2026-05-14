/**
 * Navigation module tests
 */
import { switchTab, switchSettingsSubTab, getSettingsSubTabFromPath } from '../navigation';

// Mock the dependent modules
jest.mock('../dashboard', () => ({
  loadDashboard: jest.fn().mockResolvedValue(undefined)
}));
jest.mock('../recommendations', () => ({
  loadRecommendations: jest.fn().mockResolvedValue(undefined)
}));
jest.mock('../plans', () => ({
  loadPlans: jest.fn().mockResolvedValue(undefined)
}));
jest.mock('../history', () => ({
  initHistoryDateRange: jest.fn(),
  // Issue #340 sub-task: switchTab('purchases') now auto-loads
  // history so the Approval queue card populates without waiting for
  // the "Load History" button click. Stub it so the navigation tests
  // don't make real fetch calls.
  loadHistory: jest.fn().mockResolvedValue(undefined),
}));
jest.mock('../settings', () => ({
  loadGlobalSettings: jest.fn().mockResolvedValue(undefined),
  isUnsavedChanges: jest.fn().mockReturnValue(false),
  loadAccountsTab: jest.fn().mockResolvedValue(undefined),
}));
jest.mock('../riexchange', () => ({
  loadRIExchange: jest.fn().mockResolvedValue(undefined),
  loadAutomationSettings: jest.fn().mockResolvedValue(undefined),
}));
jest.mock('../auth', () => ({
  isAdmin: jest.fn().mockReturnValue(true),
}));

import { loadDashboard } from '../dashboard';
import { loadRecommendations } from '../recommendations';
import { loadPlans } from '../plans';
import { initHistoryDateRange, loadHistory } from '../history';
import { loadGlobalSettings } from '../settings';
import { loadAutomationSettings } from '../riexchange';

describe('Navigation Module', () => {
  beforeEach(() => {
    // Setup DOM with tabs
    document.body.innerHTML = `
      <div class="tabs">
        <button class="tab-btn active" data-tab="home">Dashboard</button>
        <button class="tab-btn" data-tab="opportunities">Recommendations</button>
        <button class="tab-btn" data-tab="plans">Plans</button>
        <button class="tab-btn" data-tab="purchases">History</button>
        <button class="tab-btn" data-tab="inventory">Inventory &amp; Coverage</button>
        <button class="tab-btn" data-tab="admin">Settings</button>
      </div>
      <div id="home-tab" class="tab-content active"></div>
      <div id="opportunities-tab" class="tab-content"></div>
      <div id="plans-tab" class="tab-content"></div>
      <div id="purchases-tab" class="tab-content"></div>
      <div id="inventory-tab" class="tab-content"></div>
      <div id="admin-tab" class="tab-content">
        <div class="settings-tabs">
          <button class="sub-tab-btn active" data-settings-tab="general">General</button>
          <button class="sub-tab-btn" data-settings-tab="purchasing">Purchasing</button>
          <button class="sub-tab-btn" data-settings-tab="accounts">Accounts</button>
          <button class="sub-tab-btn" data-settings-tab="users">Users</button>
        </div>
        <section id="settings-section"></section>
        <section id="purchasing-panel" class="hidden"></section>
        <section id="accounts-section" class="hidden"></section>
        <section id="users-section" class="hidden"></section>
        <section id="apikeys-section" class="hidden"></section>
      </div>
    `;

    // Clear all mocks
    jest.clearAllMocks();
  });

  describe('switchTab', () => {
    test('switches to dashboard tab and loads dashboard', () => {
      switchTab('home');

      // Check button is active
      const dashboardBtn = document.querySelector('[data-tab="home"]');
      expect(dashboardBtn?.classList.contains('active')).toBe(true);

      // Check other buttons are not active
      const recsBtn = document.querySelector('[data-tab="opportunities"]');
      expect(recsBtn?.classList.contains('active')).toBe(false);

      // Check content is active
      const dashboardContent = document.getElementById('home-tab');
      expect(dashboardContent?.classList.contains('active')).toBe(true);

      // Check loadDashboard was called
      expect(loadDashboard).toHaveBeenCalled();
    });

    test('switches to recommendations tab and loads recommendations', () => {
      switchTab('opportunities');

      const recsBtn = document.querySelector('[data-tab="opportunities"]');
      expect(recsBtn?.classList.contains('active')).toBe(true);

      const recsContent = document.getElementById('opportunities-tab');
      expect(recsContent?.classList.contains('active')).toBe(true);

      expect(loadRecommendations).toHaveBeenCalled();
    });

    test('switches to plans tab and loads plans', () => {
      switchTab('plans');

      const plansBtn = document.querySelector('[data-tab="plans"]');
      expect(plansBtn?.classList.contains('active')).toBe(true);

      const plansContent = document.getElementById('plans-tab');
      expect(plansContent?.classList.contains('active')).toBe(true);

      expect(loadPlans).toHaveBeenCalled();
    });

    test('switches to history tab and initializes date range + auto-loads history', () => {
      switchTab('purchases');

      const historyBtn = document.querySelector('[data-tab="purchases"]');
      expect(historyBtn?.classList.contains('active')).toBe(true);

      const historyContent = document.getElementById('purchases-tab');
      expect(historyContent?.classList.contains('active')).toBe(true);

      expect(initHistoryDateRange).toHaveBeenCalled();
      // Issue #340 sub-task: switchTab('purchases') auto-loads
      // history so the Approval queue card and the Purchase History
      // table populate on first visit. Guards against a regression
      // that re-introduces "user must click Load History" friction.
      expect(loadHistory).toHaveBeenCalled();
    });

    test('switches to settings tab and loads settings', () => {
      switchTab('admin');

      const settingsBtn = document.querySelector('[data-tab="admin"]');
      expect(settingsBtn?.classList.contains('active')).toBe(true);

      const settingsContent = document.getElementById('admin-tab');
      expect(settingsContent?.classList.contains('active')).toBe(true);

      expect(loadGlobalSettings).toHaveBeenCalled();
    });

    test('switches to inventory tab', () => {
      switchTab('inventory');

      const inventoryBtn = document.querySelector('[data-tab="inventory"]');
      expect(inventoryBtn?.classList.contains('active')).toBe(true);

      const inventoryContent = document.getElementById('inventory-tab');
      expect(inventoryContent?.classList.contains('active')).toBe(true);

      // Other tabs must be deactivated
      const homeBtn = document.querySelector('[data-tab="home"]');
      expect(homeBtn?.classList.contains('active')).toBe(false);
    });

    test('deactivates previously active tab', () => {
      // Dashboard is initially active
      const dashboardBtn = document.querySelector('[data-tab="home"]');
      const dashboardContent = document.getElementById('home-tab');
      expect(dashboardBtn?.classList.contains('active')).toBe(true);
      expect(dashboardContent?.classList.contains('active')).toBe(true);

      // Switch to recommendations
      switchTab('opportunities');

      // Dashboard should no longer be active
      expect(dashboardBtn?.classList.contains('active')).toBe(false);
      expect(dashboardContent?.classList.contains('active')).toBe(false);

      // Recommendations should be active
      const recsBtn = document.querySelector('[data-tab="opportunities"]');
      const recsContent = document.getElementById('opportunities-tab');
      expect(recsBtn?.classList.contains('active')).toBe(true);
      expect(recsContent?.classList.contains('active')).toBe(true);
    });

    test('falls back to home for unknown tab', () => {
      // Unknown tab names are normalized to 'home' rather than
      // leaving the UI in a broken all-deactivated state.
      expect(() => switchTab('unknown-tab')).not.toThrow();

      const homeBtn = document.querySelector('[data-tab="home"]');
      expect(homeBtn?.classList.contains('active')).toBe(true);
      expect(loadDashboard).toHaveBeenCalled();
    });

    test('multiple tab switches work correctly', () => {
      // Switch through all tabs
      switchTab('opportunities');
      expect(loadRecommendations).toHaveBeenCalledTimes(1);

      switchTab('plans');
      expect(loadPlans).toHaveBeenCalledTimes(1);

      switchTab('purchases');
      expect(initHistoryDateRange).toHaveBeenCalledTimes(1);

      switchTab('admin');
      expect(loadGlobalSettings).toHaveBeenCalledTimes(1);

      switchTab('home');
      expect(loadDashboard).toHaveBeenCalledTimes(1);

      // Final state should have home active
      const dashboardBtn = document.querySelector('[data-tab="home"]');
      expect(dashboardBtn?.classList.contains('active')).toBe(true);
    });
  });

  describe('switchSettingsSubTab', () => {
    // Sections are hidden via the `.hidden` utility class (not inline
    // style.display), so CSP's `style-src 'self'` doesn't block initial
    // parse of index.html. See issues/8.
    const isHidden = (id: string) =>
      document.getElementById(id)?.classList.contains('hidden') ?? null;

    test('shows general sections and hides others', () => {
      switchSettingsSubTab('general');

      expect(isHidden('settings-section')).toBe(false);
      expect(isHidden('purchasing-panel')).toBe(true);
      expect(isHidden('accounts-section')).toBe(true);
      expect(isHidden('users-section')).toBe(true);
      expect(isHidden('apikeys-section')).toBe(true);
    });

    test('shows purchasing panel and hides others', () => {
      switchSettingsSubTab('purchasing');

      expect(isHidden('purchasing-panel')).toBe(false);
      expect(isHidden('settings-section')).toBe(true);
      expect(isHidden('accounts-section')).toBe(true);
      expect(isHidden('users-section')).toBe(true);
      expect(isHidden('apikeys-section')).toBe(true);
    });

    test('purchasing sub-tab loads settings and automation', () => {
      switchSettingsSubTab('purchasing');

      expect(loadGlobalSettings).toHaveBeenCalled();
      expect(loadAutomationSettings).toHaveBeenCalled();
    });

    test('shows accounts section and hides others', () => {
      switchSettingsSubTab('accounts');

      expect(isHidden('settings-section')).toBe(true);
      expect(isHidden('purchasing-panel')).toBe(true);
      expect(isHidden('accounts-section')).toBe(false);
      expect(isHidden('users-section')).toBe(true);
      expect(isHidden('apikeys-section')).toBe(true);
    });

    test('shows users and apikeys sections for users sub-tab', () => {
      switchSettingsSubTab('users');

      expect(isHidden('settings-section')).toBe(true);
      expect(isHidden('purchasing-panel')).toBe(true);
      expect(isHidden('accounts-section')).toBe(true);
      expect(isHidden('users-section')).toBe(false);
      expect(isHidden('apikeys-section')).toBe(false);
    });

    test('falls back to general for unknown sub-tab', () => {
      switchSettingsSubTab('foobar');

      expect(isHidden('settings-section')).toBe(false);
      expect(isHidden('purchasing-panel')).toBe(true);
      expect(isHidden('accounts-section')).toBe(true);
    });

    test('toggles active class on sub-tab buttons', () => {
      switchSettingsSubTab('accounts');

      const generalBtn = document.querySelector('[data-settings-tab="general"]');
      const accountsBtn = document.querySelector('[data-settings-tab="accounts"]');
      expect(generalBtn?.classList.contains('active')).toBe(false);
      expect(accountsBtn?.classList.contains('active')).toBe(true);
    });

    test('redirects non-admin to general for admin-only sub-tabs', () => {
      const { isAdmin } = require('../auth');
      (isAdmin as jest.Mock).mockReturnValue(false);

      switchSettingsSubTab('accounts');

      expect(document.getElementById('settings-section')?.classList.contains('hidden')).toBe(false);
      expect(document.getElementById('accounts-section')?.classList.contains('hidden')).toBe(true);
    });
  });

  describe('getSettingsSubTabFromPath', () => {
    // Canonical /admin/* paths (issue #340 IA rename)
    test('returns general for root admin path', () => {
      delete (window as unknown as Record<string, unknown>).location;
      (window as unknown as Record<string, unknown>).location = { pathname: '/admin' } as Location;
      expect(getSettingsSubTabFromPath()).toBe('general');
    });

    test('returns accounts for /admin/accounts', () => {
      delete (window as unknown as Record<string, unknown>).location;
      (window as unknown as Record<string, unknown>).location = { pathname: '/admin/accounts' } as Location;
      expect(getSettingsSubTabFromPath()).toBe('accounts');
    });

    test('returns purchasing for /admin/purchasing', () => {
      delete (window as unknown as Record<string, unknown>).location;
      (window as unknown as Record<string, unknown>).location = { pathname: '/admin/purchasing' } as Location;
      expect(getSettingsSubTabFromPath()).toBe('purchasing');
    });

    test('returns users for /admin/users', () => {
      delete (window as unknown as Record<string, unknown>).location;
      (window as unknown as Record<string, unknown>).location = { pathname: '/admin/users' } as Location;
      expect(getSettingsSubTabFromPath()).toBe('users');
    });

    // Legacy /settings/* paths still work via LEGACY_PATH_REDIRECTS
    test('returns general for root settings path', () => {
      delete (window as unknown as Record<string, unknown>).location;
      (window as unknown as Record<string, unknown>).location = { pathname: '/settings' } as Location;
      expect(getSettingsSubTabFromPath()).toBe('general');
    });

    test('returns accounts for /settings/accounts', () => {
      delete (window as unknown as Record<string, unknown>).location;
      (window as unknown as Record<string, unknown>).location = { pathname: '/settings/accounts' } as Location;
      expect(getSettingsSubTabFromPath()).toBe('accounts');
    });

    test('returns purchasing for /settings/purchasing', () => {
      delete (window as unknown as Record<string, unknown>).location;
      (window as unknown as Record<string, unknown>).location = { pathname: '/settings/purchasing' } as Location;
      expect(getSettingsSubTabFromPath()).toBe('purchasing');
    });

    test('returns general for unknown sub-tab', () => {
      delete (window as unknown as Record<string, unknown>).location;
      (window as unknown as Record<string, unknown>).location = { pathname: '/settings/foobar' } as Location;
      expect(getSettingsSubTabFromPath()).toBe('general');
    });
  });
});
