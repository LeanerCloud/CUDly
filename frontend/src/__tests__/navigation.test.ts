/**
 * Navigation module tests
 */
import { switchTab } from '../navigation';

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
  isUnsavedChanges: jest.fn().mockReturnValue(false)
}));

import { loadDashboard } from '../dashboard';
import { loadRecommendations } from '../recommendations';
import { loadPlans } from '../plans';
import { initHistoryDateRange } from '../history';
import { loadGlobalSettings } from '../settings';

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
      <div id="settings-tab" class="tab-content"></div>
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
});
