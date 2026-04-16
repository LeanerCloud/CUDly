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
  initHistoryDateRange: jest.fn()
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
import { initHistoryDateRange } from '../history';
import { loadGlobalSettings } from '../settings';
import { loadAutomationSettings } from '../riexchange';

describe('Navigation Module', () => {
  beforeEach(() => {
    // Setup DOM with tabs
    document.body.innerHTML = `
      <div class="tabs">
        <button class="tab-btn active" data-tab="dashboard">Dashboard</button>
        <button class="tab-btn" data-tab="recommendations">Recommendations</button>
        <button class="tab-btn" data-tab="plans">Plans</button>
        <button class="tab-btn" data-tab="history">History</button>
        <button class="tab-btn" data-tab="settings">Settings</button>
      </div>
      <div id="dashboard-tab" class="tab-content active"></div>
      <div id="recommendations-tab" class="tab-content"></div>
      <div id="plans-tab" class="tab-content"></div>
      <div id="history-tab" class="tab-content"></div>
      <div id="settings-tab" class="tab-content">
        <div class="settings-tabs">
          <button class="sub-tab-btn active" data-settings-tab="general">General</button>
          <button class="sub-tab-btn" data-settings-tab="purchasing">Purchasing</button>
          <button class="sub-tab-btn" data-settings-tab="accounts">Accounts</button>
          <button class="sub-tab-btn" data-settings-tab="users">Users</button>
        </div>
        <section id="settings-section"></section>
        <section id="purchasing-panel" style="display:none"></section>
        <section id="accounts-section" style="display:none"></section>
        <section id="users-section" style="display:none"></section>
        <section id="apikeys-section" style="display:none"></section>
      </div>
    `;

    // Clear all mocks
    jest.clearAllMocks();
  });

  describe('switchTab', () => {
    test('switches to dashboard tab and loads dashboard', () => {
      switchTab('dashboard');

      // Check button is active
      const dashboardBtn = document.querySelector('[data-tab="dashboard"]');
      expect(dashboardBtn?.classList.contains('active')).toBe(true);

      // Check other buttons are not active
      const recsBtn = document.querySelector('[data-tab="recommendations"]');
      expect(recsBtn?.classList.contains('active')).toBe(false);

      // Check content is active
      const dashboardContent = document.getElementById('dashboard-tab');
      expect(dashboardContent?.classList.contains('active')).toBe(true);

      // Check loadDashboard was called
      expect(loadDashboard).toHaveBeenCalled();
    });

    test('switches to recommendations tab and loads recommendations', () => {
      switchTab('recommendations');

      const recsBtn = document.querySelector('[data-tab="recommendations"]');
      expect(recsBtn?.classList.contains('active')).toBe(true);

      const recsContent = document.getElementById('recommendations-tab');
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

    test('switches to history tab and initializes date range', () => {
      switchTab('history');

      const historyBtn = document.querySelector('[data-tab="history"]');
      expect(historyBtn?.classList.contains('active')).toBe(true);

      const historyContent = document.getElementById('history-tab');
      expect(historyContent?.classList.contains('active')).toBe(true);

      expect(initHistoryDateRange).toHaveBeenCalled();
    });

    test('switches to settings tab and loads settings', () => {
      switchTab('settings');

      const settingsBtn = document.querySelector('[data-tab="settings"]');
      expect(settingsBtn?.classList.contains('active')).toBe(true);

      const settingsContent = document.getElementById('settings-tab');
      expect(settingsContent?.classList.contains('active')).toBe(true);

      expect(loadGlobalSettings).toHaveBeenCalled();
    });

    test('deactivates previously active tab', () => {
      // Dashboard is initially active
      const dashboardBtn = document.querySelector('[data-tab="dashboard"]');
      const dashboardContent = document.getElementById('dashboard-tab');
      expect(dashboardBtn?.classList.contains('active')).toBe(true);
      expect(dashboardContent?.classList.contains('active')).toBe(true);

      // Switch to recommendations
      switchTab('recommendations');

      // Dashboard should no longer be active
      expect(dashboardBtn?.classList.contains('active')).toBe(false);
      expect(dashboardContent?.classList.contains('active')).toBe(false);

      // Recommendations should be active
      const recsBtn = document.querySelector('[data-tab="recommendations"]');
      const recsContent = document.getElementById('recommendations-tab');
      expect(recsBtn?.classList.contains('active')).toBe(true);
      expect(recsContent?.classList.contains('active')).toBe(true);
    });

    test('falls back to dashboard for unknown tab', () => {
      // Unknown tab names are normalized to 'dashboard' rather than
      // leaving the UI in a broken all-deactivated state.
      expect(() => switchTab('unknown-tab')).not.toThrow();

      const dashboardBtn = document.querySelector('[data-tab="dashboard"]');
      expect(dashboardBtn?.classList.contains('active')).toBe(true);
      expect(loadDashboard).toHaveBeenCalled();
    });

    test('multiple tab switches work correctly', () => {
      // Switch through all tabs
      switchTab('recommendations');
      expect(loadRecommendations).toHaveBeenCalledTimes(1);

      switchTab('plans');
      expect(loadPlans).toHaveBeenCalledTimes(1);

      switchTab('history');
      expect(initHistoryDateRange).toHaveBeenCalledTimes(1);

      switchTab('settings');
      expect(loadGlobalSettings).toHaveBeenCalledTimes(1);

      switchTab('dashboard');
      expect(loadDashboard).toHaveBeenCalledTimes(1);

      // Final state should have dashboard active
      const dashboardBtn = document.querySelector('[data-tab="dashboard"]');
      expect(dashboardBtn?.classList.contains('active')).toBe(true);
    });
  });

  describe('switchSettingsSubTab', () => {
    test('shows general sections and hides others', () => {
      switchSettingsSubTab('general');

      expect(document.getElementById('settings-section')?.style.display).toBe('');
      expect(document.getElementById('purchasing-panel')?.style.display).toBe('none');
      expect(document.getElementById('accounts-section')?.style.display).toBe('none');
      expect(document.getElementById('users-section')?.style.display).toBe('none');
      expect(document.getElementById('apikeys-section')?.style.display).toBe('none');
    });

    test('shows purchasing panel and hides others', () => {
      switchSettingsSubTab('purchasing');

      expect(document.getElementById('purchasing-panel')?.style.display).toBe('');
      expect(document.getElementById('settings-section')?.style.display).toBe('none');
      expect(document.getElementById('accounts-section')?.style.display).toBe('none');
      expect(document.getElementById('users-section')?.style.display).toBe('none');
      expect(document.getElementById('apikeys-section')?.style.display).toBe('none');
    });

    test('purchasing sub-tab loads settings and automation', () => {
      switchSettingsSubTab('purchasing');

      expect(loadGlobalSettings).toHaveBeenCalled();
      expect(loadAutomationSettings).toHaveBeenCalled();
    });

    test('shows accounts section and hides others', () => {
      switchSettingsSubTab('accounts');

      expect(document.getElementById('settings-section')?.style.display).toBe('none');
      expect(document.getElementById('purchasing-panel')?.style.display).toBe('none');
      expect(document.getElementById('accounts-section')?.style.display).toBe('');
      expect(document.getElementById('users-section')?.style.display).toBe('none');
      expect(document.getElementById('apikeys-section')?.style.display).toBe('none');
    });

    test('shows users and apikeys sections for users sub-tab', () => {
      switchSettingsSubTab('users');

      expect(document.getElementById('settings-section')?.style.display).toBe('none');
      expect(document.getElementById('purchasing-panel')?.style.display).toBe('none');
      expect(document.getElementById('accounts-section')?.style.display).toBe('none');
      expect(document.getElementById('users-section')?.style.display).toBe('');
      expect(document.getElementById('apikeys-section')?.style.display).toBe('');
    });

    test('falls back to general for unknown sub-tab', () => {
      switchSettingsSubTab('foobar');

      expect(document.getElementById('settings-section')?.style.display).toBe('');
      expect(document.getElementById('purchasing-panel')?.style.display).toBe('none');
      expect(document.getElementById('accounts-section')?.style.display).toBe('none');
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

      expect(document.getElementById('settings-section')?.style.display).toBe('');
      expect(document.getElementById('accounts-section')?.style.display).toBe('none');
    });
  });

  describe('getSettingsSubTabFromPath', () => {
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
