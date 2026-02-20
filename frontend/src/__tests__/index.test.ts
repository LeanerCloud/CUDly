/**
 * Index module tests - entry point
 */

// Mock all modules before importing index
jest.mock('../app', () => ({
  init: jest.fn()
}));

jest.mock('../plans', () => ({
  closePlanModal: jest.fn(),
  openCreatePlanModal: jest.fn(),
  openNewPlanModal: jest.fn(),
  closePurchaseModal: jest.fn()
}));

jest.mock('../recommendations', () => ({
  refreshRecommendations: jest.fn()
}));

jest.mock('../history', () => ({
  loadHistory: jest.fn()
}));

jest.mock('../settings', () => ({
  resetSettings: jest.fn()
}));

jest.mock('../auth', () => ({
  logout: jest.fn()
}));

import * as app from '../app';
import * as plans from '../plans';
import * as recommendations from '../recommendations';
import * as history from '../history';
import * as settings from '../settings';
import * as auth from '../auth';

describe('Index Module', () => {
  beforeEach(() => {
    jest.clearAllMocks();
  });

  test('sets up global window functions', () => {
    // Import index to trigger setup
    require('../index');

    expect(window.refreshRecommendations).toBeDefined();
    expect(window.openCreatePlanModal).toBeDefined();
    expect(window.openNewPlanModal).toBeDefined();
    expect(window.closePlanModal).toBeDefined();
    expect(window.closePurchaseModal).toBeDefined();
    expect(window.resetSettings).toBeDefined();
    expect(window.loadHistory).toBeDefined();
    expect(window.logout).toBeDefined();
  });

  test('window.refreshRecommendations calls module function', () => {
    require('../index');
    window.refreshRecommendations();
    expect(recommendations.refreshRecommendations).toHaveBeenCalled();
  });

  test('window.openCreatePlanModal calls module function', () => {
    require('../index');
    window.openCreatePlanModal();
    expect(plans.openCreatePlanModal).toHaveBeenCalled();
  });

  test('window.openNewPlanModal calls module function', () => {
    require('../index');
    window.openNewPlanModal();
    expect(plans.openNewPlanModal).toHaveBeenCalled();
  });

  test('window.closePlanModal calls module function', () => {
    require('../index');
    window.closePlanModal();
    expect(plans.closePlanModal).toHaveBeenCalled();
  });

  test('window.closePurchaseModal calls module function', () => {
    require('../index');
    window.closePurchaseModal();
    expect(plans.closePurchaseModal).toHaveBeenCalled();
  });

  test('window.resetSettings calls module function', () => {
    require('../index');
    window.resetSettings();
    expect(settings.resetSettings).toHaveBeenCalled();
  });

  test('window.loadHistory calls module function', () => {
    require('../index');
    window.loadHistory();
    expect(history.loadHistory).toHaveBeenCalled();
  });

  test('window.logout calls module function', () => {
    require('../index');
    window.logout();
    expect(auth.logout).toHaveBeenCalled();
  });

  test('calls init on DOMContentLoaded', () => {
    require('../index');

    // Trigger DOMContentLoaded event
    const event = new Event('DOMContentLoaded');
    document.dispatchEvent(event);

    // Wait for async init
    return new Promise(resolve => setTimeout(() => {
      expect(app.init).toHaveBeenCalled();
      resolve(undefined);
    }, 10));
  });
});
