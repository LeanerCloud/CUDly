/**
 * App module tests
 */

// Mock dependencies before imports
jest.mock('../api', () => ({
  initAuth: jest.fn(),
  isAuthenticated: jest.fn(),
  getCurrentUser: jest.fn()
}));

jest.mock('../state', () => ({
  setCurrentUser: jest.fn(),
  setCurrentProvider: jest.fn()
}));

jest.mock('../auth', () => ({
  showLoginModal: jest.fn(),
  updateUserUI: jest.fn()
}));

jest.mock('../dashboard', () => ({
  loadDashboard: jest.fn(),
  setupDashboardHandlers: jest.fn()
}));

jest.mock('../navigation', () => ({
  switchTab: jest.fn(),
  applyTabFromPath: jest.fn().mockReturnValue('dashboard'),
  initRouter: jest.fn(),
}));

jest.mock('../recommendations', () => ({
  setupRecommendationsHandlers: jest.fn()
}));

jest.mock('../plans', () => ({
  savePlan: jest.fn(),
  setupPlanHandlers: jest.fn()
}));

jest.mock('../settings', () => ({
  saveGlobalSettings: jest.fn(),
  setupSettingsHandlers: jest.fn()
}));

import { init, setupEventListeners } from '../app';
import * as api from '../api';
import * as state from '../state';
import * as auth from '../auth';
import * as dashboard from '../dashboard';
import * as navigation from '../navigation';
import * as plans from '../plans';
import * as settings from '../settings';

describe('App Module', () => {
  beforeEach(() => {
    document.body.innerHTML = '';
    jest.clearAllMocks();
  });

  describe('init', () => {
    test('calls initAuth on startup', async () => {
      (api.isAuthenticated as jest.Mock).mockReturnValue(false);

      await init();

      expect(api.initAuth).toHaveBeenCalled();
    });

    test('shows login modal when not authenticated', async () => {
      (api.isAuthenticated as jest.Mock).mockReturnValue(false);

      await init();

      expect(auth.showLoginModal).toHaveBeenCalled();
      expect(api.getCurrentUser).not.toHaveBeenCalled();
    });

    test('loads user and routes to dashboard when authenticated', async () => {
      (api.isAuthenticated as jest.Mock).mockReturnValue(true);
      (api.getCurrentUser as jest.Mock).mockResolvedValue({ id: 'user-1', email: 'test@example.com' });
      (navigation.applyTabFromPath as jest.Mock).mockReturnValue('dashboard');

      await init();

      expect(api.getCurrentUser).toHaveBeenCalled();
      expect(state.setCurrentUser).toHaveBeenCalledWith({ id: 'user-1', email: 'test@example.com' });
      expect(navigation.initRouter).toHaveBeenCalled();
      expect(navigation.applyTabFromPath).toHaveBeenCalled();
      expect(navigation.switchTab).toHaveBeenCalledWith('dashboard', { push: false });
      expect(auth.updateUserUI).toHaveBeenCalled();
    });

    test('shows login modal on 401 error', async () => {
      (api.isAuthenticated as jest.Mock).mockReturnValue(true);
      (api.getCurrentUser as jest.Mock).mockRejectedValue({ status: 401 });
      console.error = jest.fn();

      await init();

      expect(auth.showLoginModal).toHaveBeenCalled();
    });

    test('shows login modal on Unauthorized error message', async () => {
      (api.isAuthenticated as jest.Mock).mockReturnValue(true);
      (api.getCurrentUser as jest.Mock).mockRejectedValue({ message: 'Unauthorized' });
      console.error = jest.fn();

      await init();

      expect(auth.showLoginModal).toHaveBeenCalled();
    });

    test('logs error but does not show login for other errors', async () => {
      (api.isAuthenticated as jest.Mock).mockReturnValue(true);
      (api.getCurrentUser as jest.Mock).mockRejectedValue(new Error('Network error'));
      console.error = jest.fn();

      await init();

      expect(console.error).toHaveBeenCalledWith('Init error:', expect.any(Error));
      expect(auth.showLoginModal).not.toHaveBeenCalled();
    });
  });

  describe('setupEventListeners', () => {
    test('sets up tab button click handlers', () => {
      document.body.innerHTML = `
        <button class="tab-btn" data-tab="dashboard">Dashboard</button>
        <button class="tab-btn" data-tab="recommendations">Recommendations</button>
      `;

      setupEventListeners();

      const dashboardBtn = document.querySelector('[data-tab="dashboard"]') as HTMLButtonElement;
      dashboardBtn.click();

      expect(navigation.switchTab).toHaveBeenCalledWith('dashboard');
    });

    test('does not call switchTab if tab data attribute is missing', () => {
      document.body.innerHTML = `
        <button class="tab-btn">No Tab</button>
      `;

      setupEventListeners();

      const btn = document.querySelector('.tab-btn') as HTMLButtonElement;
      btn.click();

      expect(navigation.switchTab).not.toHaveBeenCalled();
    });

    test('calls setupDashboardHandlers', () => {
      setupEventListeners();
      expect(dashboard.setupDashboardHandlers).toHaveBeenCalled();
    });

    test('sets up plan form submit handler', () => {
      document.body.innerHTML = `
        <form id="plan-form">
          <button type="submit">Save</button>
        </form>
      `;

      setupEventListeners();

      const form = document.getElementById('plan-form') as HTMLFormElement;
      const submitEvent = new Event('submit', { cancelable: true });
      form.dispatchEvent(submitEvent);

      expect(plans.savePlan).toHaveBeenCalled();
    });

    test('sets up settings form submit handler', () => {
      document.body.innerHTML = `
        <form id="global-settings-form">
          <button type="submit">Save</button>
        </form>
      `;

      setupEventListeners();

      const form = document.getElementById('global-settings-form') as HTMLFormElement;
      const submitEvent = new Event('submit', { cancelable: true });
      form.dispatchEvent(submitEvent);

      expect(settings.saveGlobalSettings).toHaveBeenCalled();
    });

    test('sets up ramp schedule toggle for custom option', () => {
      document.body.innerHTML = `
        <input type="radio" name="ramp-schedule" value="standard">
        <input type="radio" name="ramp-schedule" value="custom">
        <div id="custom-ramp-config" class="hidden"></div>
      `;

      setupEventListeners();

      const customRadio = document.querySelector('input[value="custom"]') as HTMLInputElement;
      customRadio.checked = true;
      customRadio.dispatchEvent(new Event('change'));

      const customConfig = document.getElementById('custom-ramp-config');
      expect(customConfig?.classList.contains('hidden')).toBe(false);
    });

    test('hides custom ramp config for standard option', () => {
      document.body.innerHTML = `
        <input type="radio" name="ramp-schedule" value="standard">
        <input type="radio" name="ramp-schedule" value="custom">
        <div id="custom-ramp-config"></div>
      `;

      setupEventListeners();

      const standardRadio = document.querySelector('input[value="standard"]') as HTMLInputElement;
      standardRadio.checked = true;
      standardRadio.dispatchEvent(new Event('change'));

      const customConfig = document.getElementById('custom-ramp-config');
      expect(customConfig?.classList.contains('hidden')).toBe(true);
    });

    test('handles missing elements gracefully', () => {
      document.body.innerHTML = '';

      expect(() => setupEventListeners()).not.toThrow();
    });
  });
});
