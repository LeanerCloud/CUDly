/**
 * Sidebar nav anchor tests - issue #462.
 *
 * The left-side nav items must be real `<a href>` so the browser's
 * "Open Link in New Tab" affordance works. The SPA still intercepts
 * un-modified left clicks via preventDefault + switchTab; modifier-key
 * clicks fall through to the browser default.
 */
import fs from 'fs';
import path from 'path';

import { setupEventListeners } from '../app';

jest.mock('../navigation', () => ({
  switchTab: jest.fn(),
  switchSettingsSubTab: jest.fn(),
  applyTabFromPath: jest.fn().mockReturnValue('home'),
  getSettingsSubTabFromPath: jest.fn().mockReturnValue('general'),
  initRouter: jest.fn(),
}));
jest.mock('../auth', () => ({
  showLoginModal: jest.fn(),
  showAdminSetupModal: jest.fn(),
  showResetPasswordModal: jest.fn(),
  updateUserUI: jest.fn(),
}));
jest.mock('../dashboard', () => ({
  loadDashboard: jest.fn(),
  setupDashboardHandlers: jest.fn(),
}));
jest.mock('../recommendations', () => ({
  setupRecommendationsHandlers: jest.fn(),
  getPurchaseModalRecommendations: jest.fn(),
  clearPurchaseModalRecommendations: jest.fn(),
  getFanOutBuckets: jest.fn(),
  clearFanOutBuckets: jest.fn(),
}));
jest.mock('../plans', () => ({
  savePlan: jest.fn(),
  setupPlanHandlers: jest.fn(),
  closePlanModal: jest.fn(),
  openNewPlanModal: jest.fn(),
  closePurchaseModal: jest.fn(),
}));
jest.mock('../settings', () => ({
  saveGlobalSettings: jest.fn(),
  setupSettingsHandlers: jest.fn(),
  resetSettings: jest.fn(),
}));
jest.mock('../users', () => ({ setupUserHandlers: jest.fn() }));
jest.mock('../apikeys', () => ({ initApiKeys: jest.fn() }));
jest.mock('../history', () => ({ loadHistory: jest.fn() }));
jest.mock('../modules/savings-history', () => ({ initSavingsHistory: jest.fn() }));
jest.mock('../riexchange', () => ({
  setupRIExchangeHandlers: jest.fn(),
  saveAutomationSettings: jest.fn(),
}));
jest.mock('../toast', () => ({ showToast: jest.fn() }));
jest.mock('../confirmDialog', () => ({ confirmDialog: jest.fn() }));
jest.mock('../purchases-deeplink', () => ({ handlePurchaseDeeplink: jest.fn() }));
jest.mock('../archera', () => ({
  handleArcheraDeeplink: jest.fn(),
  openArcheraOfferModal: jest.fn(),
}));
jest.mock('../modal', () => ({ closeModal: jest.fn() }));

import { switchTab } from '../navigation';

const EXPECTED_NAV: Array<{ tab: string; href: string }> = [
  { tab: 'home',          href: '/home' },
  { tab: 'opportunities', href: '/opportunities' },
  { tab: 'plans',         href: '/plans' },
  { tab: 'purchases',     href: '/purchases' },
  { tab: 'inventory',     href: '/inventory' },
  // Admin defaults to its first sub-tab so opening it cold in a new
  // tab lands on a real route (issue #462).
  { tab: 'admin',         href: '/admin/general' },
];

describe('Sidebar nav - issue #462 anchor markup', () => {
  let html: string;

  beforeAll(() => {
    html = fs.readFileSync(
      path.resolve(__dirname, '../index.html'),
      'utf8',
    );
  });

  test.each(EXPECTED_NAV)(
    '$tab is rendered as anchor at $href with role=tab',
    ({ tab, href }) => {
      document.body.innerHTML = html;
      const el = document.querySelector(`.app-sidebar-nav [data-tab="${tab}"]`);
      expect(el).toBeTruthy();
      expect(el?.tagName).toBe('A');
      expect((el as HTMLAnchorElement).getAttribute('href')).toBe(href);
      expect(el?.getAttribute('role')).toBe('tab');
    },
  );

  test('all six sidebar entries render as anchors', () => {
    document.body.innerHTML = html;
    const items = document.querySelectorAll('.app-sidebar-nav .tab-btn');
    expect(items.length).toBe(EXPECTED_NAV.length);
    items.forEach((el) => expect(el.tagName).toBe('A'));
  });
});

describe('Sidebar nav click handling - issue #462', () => {
  beforeEach(() => {
    jest.clearAllMocks();
    const a = document.createElement('a');
    a.className = 'tab-btn';
    a.setAttribute('href', '/opportunities');
    a.setAttribute('data-tab', 'opportunities');
    a.setAttribute('role', 'tab');
    a.textContent = 'Opportunities';
    document.body.appendChild(a);
    setupEventListeners();
  });

  test('un-modified left click is intercepted by the SPA (preventDefault + switchTab)', () => {
    const link = document.querySelector('a.tab-btn') as HTMLAnchorElement;
    const evt = new MouseEvent('click', {
      bubbles: true,
      cancelable: true,
      button: 0,
    });
    link.dispatchEvent(evt);
    expect(evt.defaultPrevented).toBe(true);
    expect(switchTab).toHaveBeenCalledWith('opportunities');
  });

  test('Ctrl/Cmd-click falls through to the browser (no preventDefault, no switchTab)', () => {
    const link = document.querySelector('a.tab-btn') as HTMLAnchorElement;
    const evt = new MouseEvent('click', {
      bubbles: true,
      cancelable: true,
      button: 0,
      ctrlKey: true,
    });
    link.dispatchEvent(evt);
    expect(evt.defaultPrevented).toBe(false);
    expect(switchTab).not.toHaveBeenCalled();
  });

  test('middle-click (button=1) falls through to the browser', () => {
    const link = document.querySelector('a.tab-btn') as HTMLAnchorElement;
    const evt = new MouseEvent('click', {
      bubbles: true,
      cancelable: true,
      button: 1,
    });
    link.dispatchEvent(evt);
    expect(evt.defaultPrevented).toBe(false);
    expect(switchTab).not.toHaveBeenCalled();
  });

  test('Shift-click falls through (open in new window)', () => {
    const link = document.querySelector('a.tab-btn') as HTMLAnchorElement;
    const evt = new MouseEvent('click', {
      bubbles: true,
      cancelable: true,
      button: 0,
      shiftKey: true,
    });
    link.dispatchEvent(evt);
    expect(evt.defaultPrevented).toBe(false);
    expect(switchTab).not.toHaveBeenCalled();
  });
});
