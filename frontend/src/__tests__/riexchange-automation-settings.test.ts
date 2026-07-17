/**
 * loadAutomationSettings permission-gating tests (issue #1413).
 *
 * GET /api/ri-exchange/config requires view:config. Before migration 000088
 * granted view:config to non-admin groups, the handler returned 403 and
 * loadAutomationSettings showed an error paragraph ("Failed to load settings:
 * permission denied: requires view on config") at the bottom of the Purchasing
 * Policies panel. The page should degrade gracefully instead: clear the
 * container and return without showing any error.
 *
 * After the migration non-admin users have view:config, so the 403 no longer
 * occurs in normal operation. The graceful-degradation path remains as defense
 * in depth for custom roles that do not include view:config.
 */

jest.mock('../api', () => ({
  getRIExchangeConfig: jest.fn(),
}));

jest.mock('../settings', () => ({
  applyReadOnlySettings: jest.fn(),
  isPermissionDeniedError: jest.fn(),
}));

jest.mock('../state', () => ({
  getCurrentUser: jest.fn(),
}));

import * as api from '../api';
import * as settings from '../settings';
import { loadAutomationSettings } from '../riexchange';

function makePermissionError(status: number, message: string): Error & { status: number } {
  const err = new Error(message) as Error & { status: number };
  err.status = status;
  return err;
}

const setupContainer = () => {
  document.body.replaceChildren();
  const container = document.createElement('div');
  container.id = 'ri-exchange-automation-settings';
  // Simulate the loading paragraph loadAutomationSettings prepends.
  const loading = document.createElement('p');
  loading.className = 'loading';
  loading.textContent = 'Loading settings...';
  container.appendChild(loading);
  document.body.appendChild(container);
  return container;
};

describe('loadAutomationSettings: graceful degradation on permission errors (issue #1413)', () => {
  beforeEach(() => {
    jest.clearAllMocks();
  });

  test(
    '403 permission-denied: container is cleared, no error paragraph shown',
    async () => {
      setupContainer();
      const permErr = makePermissionError(403, 'permission denied: requires view on config');
      (api.getRIExchangeConfig as jest.Mock).mockRejectedValueOnce(permErr);
      // isPermissionDeniedError is imported from settings and must return true
      // for the graceful-degradation branch to fire.
      (settings.isPermissionDeniedError as jest.Mock).mockReturnValue(true);

      await loadAutomationSettings();

      const container = document.getElementById('ri-exchange-automation-settings');
      // No error paragraph — the section should be empty (silently cleared).
      const errorP = container?.querySelector('.error');
      expect(errorP).toBeNull();
      // The container itself must be present in the DOM (not removed).
      expect(container).not.toBeNull();
      // No child nodes: both the loading paragraph and any error content cleared.
      expect(container?.childNodes.length).toBe(0);
    },
  );

  test(
    '403 permission-denied: no retry button is shown',
    async () => {
      setupContainer();
      const permErr = makePermissionError(403, 'permission denied: requires view on config');
      (api.getRIExchangeConfig as jest.Mock).mockRejectedValueOnce(permErr);
      (settings.isPermissionDeniedError as jest.Mock).mockReturnValue(true);

      await loadAutomationSettings();

      const container = document.getElementById('ri-exchange-automation-settings');
      const retryBtn = container?.querySelector('button');
      expect(retryBtn).toBeNull();
    },
  );

  test(
    'non-permission 403 (e.g. IP block): error paragraph IS shown',
    async () => {
      setupContainer();
      const nonPermErr = makePermissionError(403, 'access blocked by IP policy');
      (api.getRIExchangeConfig as jest.Mock).mockRejectedValueOnce(nonPermErr);
      // isPermissionDeniedError returns false for non-permission 403s.
      (settings.isPermissionDeniedError as jest.Mock).mockReturnValue(false);

      await loadAutomationSettings();

      const container = document.getElementById('ri-exchange-automation-settings');
      const errorP = container?.querySelector('.error');
      expect(errorP).not.toBeNull();
      expect(errorP?.textContent).toContain('access blocked by IP policy');
    },
  );

  test(
    'network error: error paragraph IS shown with message',
    async () => {
      setupContainer();
      (api.getRIExchangeConfig as jest.Mock).mockRejectedValueOnce(
        new Error('Failed to fetch'),
      );
      (settings.isPermissionDeniedError as jest.Mock).mockReturnValue(false);

      await loadAutomationSettings();

      const container = document.getElementById('ri-exchange-automation-settings');
      const errorP = container?.querySelector('.error');
      expect(errorP).not.toBeNull();
      expect(errorP?.textContent).toContain('Failed to fetch');
    },
  );

  test(
    'network error: retry button IS shown',
    async () => {
      setupContainer();
      (api.getRIExchangeConfig as jest.Mock).mockRejectedValueOnce(
        new Error('Failed to fetch'),
      );
      (settings.isPermissionDeniedError as jest.Mock).mockReturnValue(false);

      await loadAutomationSettings();

      const container = document.getElementById('ri-exchange-automation-settings');
      const retryBtn = container?.querySelector('button');
      expect(retryBtn).not.toBeNull();
      expect(retryBtn?.textContent).toBe('Retry');
    },
  );
});
