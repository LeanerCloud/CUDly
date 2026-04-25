/**
 * Settings module tests
 */
import {
  loadGlobalSettings,
  saveGlobalSettings,
  resetSettings,
  setupSettingsHandlers,
  copyToClipboard
} from '../settings';

// Mock the api module
jest.mock('../api', () => ({
  getConfig: jest.fn(),
  updateConfig: jest.fn(),
  updateServiceConfig: jest.fn().mockResolvedValue(undefined),
  listAccounts: jest.fn(),
  createAccount: jest.fn(),
  updateAccount: jest.fn(),
  deleteAccount: jest.fn(),
  testAccountCredentials: jest.fn(),
  saveAccountCredentials: jest.fn()
}));

// Mock federation module — loadGlobalSettings fires initFederationPanel void-style,
// and we don't want real federation code touching the test DOM.
jest.mock('../federation', () => ({
  initFederationPanel: jest.fn().mockResolvedValue(undefined),
}));

// Mock confirmDialog so tests can control confirm/cancel without driving
// the real modal UI. mockConfirmDialog default is "confirmed" (true);
// individual tests can mockResolvedValueOnce(false) to simulate cancel.
const mockConfirmDialog = jest.fn<Promise<boolean>, [unknown]>(() => Promise.resolve(true));
jest.mock('../confirmDialog', () => ({
  confirmDialog: (opts: unknown) => mockConfirmDialog(opts),
}));

// Q7: saveGlobalSettings + error paths now use showToast.
const mockShowToast = jest.fn<{ dismiss: () => void }, [unknown]>(() => ({ dismiss: jest.fn() }));
jest.mock('../toast', () => ({
  showToast: (opts: unknown) => mockShowToast(opts),
}));

import * as api from '../api';

describe('Settings Module', () => {
  beforeEach(() => {
    // Reset DOM
    document.body.innerHTML = `
      <div id="settings-loading" class="hidden"></div>
      <form id="global-settings-form" class="hidden">
        <input type="checkbox" id="provider-aws">
        <input type="checkbox" id="provider-azure">
        <input type="checkbox" id="provider-gcp">
        <div id="aws-settings" class="hidden"></div>
        <div id="azure-settings" class="hidden"></div>
        <div id="gcp-settings" class="hidden"></div>
        <input type="email" id="setting-notification-email">
        <input type="checkbox" id="setting-auto-collect">
        <div id="collection-schedule-row" class="hidden">
          <select id="setting-collection-schedule">
            <option value="daily">Daily</option>
            <option value="weekly">Weekly</option>
          </select>
        </div>
        <select id="setting-default-term">
          <option value="1">1</option>
          <option value="3">3</option>
        </select>
        <select id="setting-default-payment">
          <option value="no-upfront">No Upfront</option>
          <option value="partial-upfront">Partial Upfront</option>
          <option value="all-upfront">All Upfront</option>
        </select>
        <input type="number" id="setting-default-coverage">
        <input type="number" id="setting-notification-days">
        <!-- AWS term/payment selects -->
        <select id="aws-ec2-term"><option value="1">1</option><option value="3">3</option></select>
        <select id="aws-rds-term"><option value="1">1</option><option value="3">3</option></select>
        <select id="aws-elasticache-term"><option value="1">1</option><option value="3">3</option></select>
        <select id="aws-opensearch-term"><option value="1">1</option><option value="3">3</option></select>
        <select id="aws-redshift-term"><option value="1">1</option><option value="3">3</option></select>
        <select id="aws-savingsplans-term"><option value="1">1</option><option value="3">3</option></select>
        <!-- Issue #22: SageMaker per-service card (its own SP type). Lambda
             intentionally omitted — it has no standalone SP, only Compute SP. -->
        <select id="aws-sagemaker-term"><option value="1">1</option><option value="3">3</option></select>
        <select id="aws-ec2-payment"><option value="no-upfront">No</option><option value="partial-upfront">Partial</option><option value="all-upfront">All</option></select>
        <select id="aws-rds-payment"><option value="no-upfront">No</option><option value="partial-upfront">Partial</option><option value="all-upfront">All</option></select>
        <select id="aws-elasticache-payment"><option value="no-upfront">No</option><option value="partial-upfront">Partial</option><option value="all-upfront">All</option></select>
        <select id="aws-opensearch-payment"><option value="no-upfront">No</option><option value="partial-upfront">Partial</option><option value="all-upfront">All</option></select>
        <select id="aws-redshift-payment"><option value="no-upfront">No</option><option value="partial-upfront">Partial</option><option value="all-upfront">All</option></select>
        <select id="aws-savingsplans-payment"><option value="no-upfront">No</option><option value="partial-upfront">Partial</option><option value="all-upfront">All</option></select>
        <select id="aws-sagemaker-payment"><option value="no-upfront">No</option><option value="partial-upfront">Partial</option><option value="all-upfront">All</option></select>
        <!-- Azure term selects -->
        <select id="azure-vm-term"><option value="1">1</option><option value="3">3</option></select>
        <select id="azure-sql-term"><option value="1">1</option><option value="3">3</option></select>
        <select id="azure-cosmos-term"><option value="1">1</option><option value="3">3</option></select>
        <!-- GCP term selects -->
        <select id="gcp-compute-term"><option value="1">1</option><option value="3">3</option></select>
        <select id="gcp-sql-term"><option value="1">1</option><option value="3">3</option></select>
      </form>
      <div id="settings-error" class="hidden"></div>
      <!-- Test element for copyToClipboard -->
      <code id="test-copy-element">test-value</code>
      <button class="copy-btn"><span class="copy-icon">Copy</span></button>
    `;
    // Seed the default selects to match prod's <option selected> markup so
    // cascade-confirm tests have an unambiguous baseline ("3 Years" term,
    // "all-upfront" payment) to transition away from.
    (document.getElementById('setting-default-term') as HTMLSelectElement).value = '3';
    (document.getElementById('setting-default-payment') as HTMLSelectElement).value = 'all-upfront';
    // Mirror the same baseline onto every per-service select so the cascade
    // diff only reports genuinely-changed rows.
    ['aws-ec2-term','aws-rds-term','aws-elasticache-term','aws-opensearch-term','aws-redshift-term','aws-savingsplans-term','aws-sagemaker-term','azure-vm-term','azure-sql-term','azure-cosmos-term','gcp-compute-term','gcp-sql-term'].forEach(id => {
      const el = document.getElementById(id) as HTMLSelectElement | null;
      if (el) el.value = '3';
    });
    ['aws-ec2-payment','aws-rds-payment','aws-elasticache-payment','aws-opensearch-payment','aws-redshift-payment','aws-savingsplans-payment','aws-sagemaker-payment'].forEach(id => {
      const el = document.getElementById(id) as HTMLSelectElement | null;
      if (el) el.value = 'all-upfront';
    });

    jest.clearAllMocks();
    // clearAllMocks only clears call history; flush the queued
    // mockResolvedValueOnce stack too so a test that doesn't consume a
    // queued value can't leak it into the next test.
    mockConfirmDialog.mockReset();
    mockConfirmDialog.mockImplementation(() => Promise.resolve(true));
  });

  describe('setupSettingsHandlers', () => {
    test('sets up provider checkbox event handlers', () => {
      setupSettingsHandlers();

      const awsCheck = document.getElementById('provider-aws') as HTMLInputElement;
      const awsSettings = document.getElementById('aws-settings');

      // Trigger change event
      awsCheck.checked = true;
      awsCheck.dispatchEvent(new Event('change'));

      expect(awsSettings?.classList.contains('hidden')).toBe(false);
    });

    test('sets up azure checkbox event handler', () => {
      setupSettingsHandlers();

      const azureCheck = document.getElementById('provider-azure') as HTMLInputElement;
      const azureSettings = document.getElementById('azure-settings');

      azureCheck.checked = true;
      azureCheck.dispatchEvent(new Event('change'));

      expect(azureSettings?.classList.contains('hidden')).toBe(false);
    });

    test('sets up gcp checkbox event handler', () => {
      setupSettingsHandlers();

      const gcpCheck = document.getElementById('provider-gcp') as HTMLInputElement;
      const gcpSettings = document.getElementById('gcp-settings');

      gcpCheck.checked = true;
      gcpCheck.dispatchEvent(new Event('change'));

      expect(gcpSettings?.classList.contains('hidden')).toBe(false);
    });

    test('sets up auto-collect checkbox to toggle schedule visibility', () => {
      setupSettingsHandlers();

      const autoCollect = document.getElementById('setting-auto-collect') as HTMLInputElement;
      const scheduleRow = document.getElementById('collection-schedule-row');

      autoCollect.checked = true;
      autoCollect.dispatchEvent(new Event('change'));

      expect(scheduleRow?.classList.contains('hidden')).toBe(false);

      autoCollect.checked = false;
      autoCollect.dispatchEvent(new Event('change'));

      expect(scheduleRow?.classList.contains('hidden')).toBe(true);
    });

    test('sets up default term to propagate to services (after confirm)', async () => {
      setupSettingsHandlers();
      mockConfirmDialog.mockResolvedValueOnce(true);

      // Baseline is "3" via the parent beforeEach; flip to "1" so the
      // cascade has real work to do.
      const defaultTerm = document.getElementById('setting-default-term') as HTMLSelectElement;
      defaultTerm.value = '1';
      defaultTerm.dispatchEvent(new Event('change'));
      await new Promise((r) => setTimeout(r, 0));

      expect(mockConfirmDialog).toHaveBeenCalledTimes(1);
      expect((document.getElementById('aws-ec2-term') as HTMLSelectElement).value).toBe('1');
      expect((document.getElementById('aws-rds-term') as HTMLSelectElement).value).toBe('1');
      expect((document.getElementById('azure-vm-term') as HTMLSelectElement).value).toBe('1');
      expect((document.getElementById('gcp-compute-term') as HTMLSelectElement).value).toBe('1');
    });

    test('sets up default payment to propagate to AWS services (after confirm), clamping where per-service constraints reject the value', async () => {
      setupSettingsHandlers();
      mockConfirmDialog.mockResolvedValueOnce(true);

      const defaultPayment = document.getElementById('setting-default-payment') as HTMLSelectElement;
      defaultPayment.value = 'no-upfront';
      defaultPayment.dispatchEvent(new Event('change'));
      await new Promise((r) => setTimeout(r, 0));

      expect(mockConfirmDialog).toHaveBeenCalledTimes(1);
      // EC2 accepts no-upfront at both terms — propagation lands as-is.
      expect((document.getElementById('aws-ec2-payment') as HTMLSelectElement).value).toBe('no-upfront');
      // RDS 3yr rejects no-upfront (parent beforeEach seeds all service
      // terms at "3"), so the constraint sync clamps RDS back to the
      // first valid payment option instead of persisting an invalid
      // combination the provider will refuse.
      const rdsPayment = (document.getElementById('aws-rds-payment') as HTMLSelectElement).value;
      expect(rdsPayment).not.toBe('no-upfront');
      expect(['partial-upfront', 'all-upfront']).toContain(rdsPayment);
    });

    test('cancelling the cascade restores the default term to its prior value', async () => {
      setupSettingsHandlers();
      mockConfirmDialog.mockResolvedValueOnce(false);

      const defaultTerm = document.getElementById('setting-default-term') as HTMLSelectElement;
      // Baseline: all services at "3" (seeded in beforeEach). Attempt to switch to "1".
      defaultTerm.value = '1';
      defaultTerm.dispatchEvent(new Event('change'));
      await new Promise((r) => setTimeout(r, 0));

      expect(defaultTerm.value).toBe('3');
      expect((document.getElementById('aws-ec2-term') as HTMLSelectElement).value).toBe('3');
    });

    test('cascade confirm skipped when no services would change', async () => {
      setupSettingsHandlers();
      // All services and default already at "3" (via parent beforeEach).
      // Flip default to "3" (self) and assert no prompt fires.
      const defaultTerm = document.getElementById('setting-default-term') as HTMLSelectElement;
      defaultTerm.value = '3';
      defaultTerm.dispatchEvent(new Event('change'));
      await new Promise((r) => setTimeout(r, 0));

      expect(mockConfirmDialog).not.toHaveBeenCalled();
    });

  });

  describe('loadGlobalSettings', () => {
    test('shows loading state and hides form initially', async () => {
      (api.getConfig as jest.Mock).mockResolvedValue({
        global: {
          enabled_providers: ['aws'],
          notification_email: 'test@example.com',
          auto_collect: true,
          default_term: 3,
          default_payment: 'all-upfront',
          default_coverage: 80,
          notification_days_before: 3
        },
        credentials: {}
      });

      const loadingEl = document.getElementById('settings-loading');
      const formEl = document.getElementById('global-settings-form');

      const promise = loadGlobalSettings();

      // After starting, loading should be shown
      expect(loadingEl?.classList.contains('hidden')).toBe(false);
      expect(formEl?.classList.contains('hidden')).toBe(true);

      await promise;
    });

    test('populates form with config data', async () => {
      (api.getConfig as jest.Mock).mockResolvedValue({
        global: {
          enabled_providers: ['aws', 'azure'],
          notification_email: 'admin@example.com',
          auto_collect: true,
          default_term: 1,
          default_payment: 'partial-upfront',
          default_coverage: 90,
          notification_days_before: 5
        },
        credentials: {}
      });

      await loadGlobalSettings();

      expect((document.getElementById('provider-aws') as HTMLInputElement).checked).toBe(true);
      expect((document.getElementById('provider-azure') as HTMLInputElement).checked).toBe(true);
      expect((document.getElementById('provider-gcp') as HTMLInputElement).checked).toBe(false);
      expect((document.getElementById('setting-notification-email') as HTMLInputElement).value).toBe('admin@example.com');
      expect((document.getElementById('setting-auto-collect') as HTMLInputElement).checked).toBe(true);
      expect((document.getElementById('setting-default-term') as HTMLSelectElement).value).toBe('1');
      expect((document.getElementById('setting-default-payment') as HTMLSelectElement).value).toBe('partial-upfront');
      expect((document.getElementById('setting-default-coverage') as HTMLInputElement).value).toBe('90');
      expect((document.getElementById('setting-notification-days') as HTMLInputElement).value).toBe('5');
    });

    test('populates collection schedule from config', async () => {
      (api.getConfig as jest.Mock).mockResolvedValue({
        global: {
          enabled_providers: [],
          collection_schedule: 'weekly',
          default_term: 3,
          default_payment: 'all-upfront',
          default_coverage: 80,
          notification_days_before: 3
        },
        credentials: {}
      });

      await loadGlobalSettings();

      expect((document.getElementById('setting-collection-schedule') as HTMLSelectElement).value).toBe('weekly');
    });

    test('handles auto_collect being false explicitly', async () => {
      (api.getConfig as jest.Mock).mockResolvedValue({
        global: {
          enabled_providers: [],
          auto_collect: false,
          default_term: 3,
          default_payment: 'all-upfront',
          default_coverage: 80,
          notification_days_before: 3
        },
        credentials: {}
      });

      await loadGlobalSettings();

      expect((document.getElementById('setting-auto-collect') as HTMLInputElement).checked).toBe(false);
    });

    test('shows error on API failure', async () => {
      (api.getConfig as jest.Mock).mockRejectedValue(new Error('API Error'));

      await loadGlobalSettings();

      const errorEl = document.getElementById('settings-error');
      expect(errorEl?.classList.contains('hidden')).toBe(false);
      expect(errorEl?.textContent).toContain('Failed to load settings');
    });

    test('hides loading and shows form on success', async () => {
      (api.getConfig as jest.Mock).mockResolvedValue({
        global: {
          enabled_providers: [],
          default_term: 3,
          default_payment: 'all-upfront',
          default_coverage: 80,
          notification_days_before: 3
        },
        credentials: {}
      });

      await loadGlobalSettings();

      const loadingEl = document.getElementById('settings-loading');
      const formEl = document.getElementById('global-settings-form');

      expect(loadingEl?.classList.contains('hidden')).toBe(true);
      expect(formEl?.classList.contains('hidden')).toBe(false);
    });

    test('handles missing global config gracefully', async () => {
      (api.getConfig as jest.Mock).mockResolvedValue({
        credentials: {}
      });

      await loadGlobalSettings();

      // Should not throw and form should be visible
      const formEl = document.getElementById('global-settings-form');
      expect(formEl?.classList.contains('hidden')).toBe(false);
    });

    test('handles missing credentials config gracefully', async () => {
      (api.getConfig as jest.Mock).mockResolvedValue({
        global: {
          enabled_providers: ['aws'],
          default_term: 3,
          default_payment: 'all-upfront',
          default_coverage: 80,
          notification_days_before: 3
        }
      });

      await loadGlobalSettings();

      // Should not throw
      const formEl = document.getElementById('global-settings-form');
      expect(formEl?.classList.contains('hidden')).toBe(false);
    });

    test('populates service card selects from data.services', async () => {
      (api.getConfig as jest.Mock).mockResolvedValue({
        global: {
          enabled_providers: ['aws'],
          default_term: 3,
          default_payment: 'all-upfront',
          default_coverage: 80,
          notification_days_before: 3
        },
        services: [
          { provider: 'aws', service: 'ec2', enabled: true, term: 1, payment: 'no-upfront', coverage: 70 },
          { provider: 'aws', service: 'rds', enabled: true, term: 3, payment: 'all-upfront', coverage: 80 }
        ]
      });

      await loadGlobalSettings();

      expect((document.getElementById('aws-ec2-term') as HTMLSelectElement).value).toBe('1');
      expect((document.getElementById('aws-ec2-payment') as HTMLSelectElement).value).toBe('no-upfront');
      expect((document.getElementById('aws-rds-term') as HTMLSelectElement).value).toBe('3');
    });
  });

  describe('saveGlobalSettings', () => {
    beforeEach(() => {
      // Set up form values
      (document.getElementById('provider-aws') as HTMLInputElement).checked = true;
      (document.getElementById('provider-azure') as HTMLInputElement).checked = false;
      (document.getElementById('provider-gcp') as HTMLInputElement).checked = true;
      (document.getElementById('setting-notification-email') as HTMLInputElement).value = 'test@test.com';
      (document.getElementById('setting-auto-collect') as HTMLInputElement).checked = true;
      (document.getElementById('setting-default-term') as HTMLSelectElement).value = '3';
      (document.getElementById('setting-default-payment') as HTMLSelectElement).value = 'all-upfront';
      (document.getElementById('setting-default-coverage') as HTMLInputElement).value = '80';
      (document.getElementById('setting-notification-days') as HTMLInputElement).value = '3';
    });

    test('prevents default form submission', async () => {
      (api.updateConfig as jest.Mock).mockResolvedValue({});

      const event = { preventDefault: jest.fn() } as unknown as Event;
      await saveGlobalSettings(event);

      expect(event.preventDefault).toHaveBeenCalled();
    });

    test('collects form data and calls updateConfig', async () => {
      (api.updateConfig as jest.Mock).mockResolvedValue({});
      window.alert = jest.fn();

      const event = { preventDefault: jest.fn() } as unknown as Event;
      await saveGlobalSettings(event);

      expect(api.updateConfig).toHaveBeenCalledWith({
        enabled_providers: ['aws', 'gcp'],
        notification_email: 'test@test.com',
        auto_collect: true,
        collection_schedule: 'daily',
        default_term: 3,
        default_payment: 'all-upfront',
        default_coverage: 80,
        notification_days_before: 3,
        // Grace-period inputs default to 7 per provider when the DOM
        // doesn't include the new inputs (older test harness setup).
        // The save helper reads missing elements as "empty" → default 7.
        grace_period_days: { aws: 7, azure: 7, gcp: 7 },
      });
    });

    test('shows success alert on save', async () => {
      (api.updateConfig as jest.Mock).mockResolvedValue({});
      window.alert = jest.fn();

      const event = { preventDefault: jest.fn() } as unknown as Event;
      await saveGlobalSettings(event);

      expect(mockShowToast).toHaveBeenCalledWith(expect.objectContaining({
        message: 'Settings saved successfully',
        kind: 'success',
      }));
    });

    test('shows error alert on failure', async () => {
      (api.updateConfig as jest.Mock).mockRejectedValue(new Error('Save failed'));
      window.alert = jest.fn();

      const event = { preventDefault: jest.fn() } as unknown as Event;
      await saveGlobalSettings(event);

      expect(mockShowToast).toHaveBeenCalledWith(expect.objectContaining({
        message: 'Failed to save settings: Save failed',
        kind: 'error',
      }));
    });

    test('handles missing form elements gracefully', async () => {
      // Remove all form elements
      document.body.innerHTML = '';
      (api.updateConfig as jest.Mock).mockResolvedValue({});
      window.alert = jest.fn();

      const event = { preventDefault: jest.fn() } as unknown as Event;
      await saveGlobalSettings(event);

      // Should still call updateConfig with default values
      expect(api.updateConfig).toHaveBeenCalled();
    });

    // Issue #22: SageMaker has its own SP type, so it gets a dedicated
    // purchasing-defaults card. Verify the card exists in the canonical
    // index.html (so the deployed UI actually renders it) and that the
    // save flow round-trips edits to the per-service config endpoint.
    // Lambda is intentionally absent — it has no standalone SP product.
    test('SageMaker card present in index.html with term and payment selects (issue #22)', () => {
      const fs = require('fs') as typeof import('fs');
      const path = require('path') as typeof import('path');
      const html = fs.readFileSync(path.join(__dirname, '..', 'index.html'), 'utf-8');
      expect(html).toMatch(/<h5>\s*SageMaker Savings Plans\s*<\/h5>/);
      expect(html).toMatch(/id="aws-sagemaker-term"/);
      expect(html).toMatch(/id="aws-sagemaker-payment"/);
    });

    test('no Lambda card in index.html — Lambda has no standalone SP, only Compute SP (issue #22)', () => {
      const fs = require('fs') as typeof import('fs');
      const path = require('path') as typeof import('path');
      const html = fs.readFileSync(path.join(__dirname, '..', 'index.html'), 'utf-8');
      // Guard against accidental re-introduction of a Lambda-specific card.
      expect(html).not.toMatch(/<h5>\s*Lambda Savings Plans\s*<\/h5>/);
      expect(html).not.toMatch(/id="aws-lambda-term"/);
      expect(html).not.toMatch(/id="aws-lambda-payment"/);
    });

    test('saveGlobalSettings sends per-service SageMaker term and payment (issue #22)', async () => {
      (api.updateConfig as jest.Mock).mockResolvedValue({});
      (api.updateServiceConfig as jest.Mock).mockClear().mockResolvedValue(undefined);
      window.alert = jest.fn();

      // User pins SageMaker to 1yr / no-upfront while leaving the global
      // default at 3yr / all-upfront.
      (document.getElementById('aws-sagemaker-term') as HTMLSelectElement).value = '1';
      (document.getElementById('aws-sagemaker-payment') as HTMLSelectElement).value = 'no-upfront';

      const event = { preventDefault: jest.fn() } as unknown as Event;
      await saveGlobalSettings(event);

      const call = (api.updateServiceConfig as jest.Mock).mock.calls.find(
        ([provider, service]) => provider === 'aws' && service === 'sagemaker',
      );
      expect(call).toBeDefined();
      const cfg = call![2];
      expect(cfg.term).toBe(1);
      expect(cfg.payment).toBe('no-upfront');
    });

    test('calls updateServiceConfig once per service field (16 calls)', async () => {
      (api.updateConfig as jest.Mock).mockResolvedValue({});
      (api.updateServiceConfig as jest.Mock).mockResolvedValue(undefined);
      window.alert = jest.fn();

      const event = { preventDefault: jest.fn() } as unknown as Event;
      await saveGlobalSettings(event);

      // 7 AWS (ec2, rds, elasticache, opensearch, redshift, savingsplans,
      // sagemaker — last one added per issue #22) + 5 Azure
      // (vm, sql, cosmosdb, redis, search) + 4 GCP.
      expect(api.updateServiceConfig).toHaveBeenCalledTimes(16);
    });
  }); // end saveGlobalSettings

  describe('resetSettings', () => {
    beforeEach(() => {
      // Set non-default values
      (document.getElementById('provider-aws') as HTMLInputElement).checked = false;
      (document.getElementById('provider-azure') as HTMLInputElement).checked = true;
      (document.getElementById('provider-gcp') as HTMLInputElement).checked = true;
      (document.getElementById('setting-notification-email') as HTMLInputElement).value = 'custom@example.com';
      (document.getElementById('setting-auto-collect') as HTMLInputElement).checked = false;
      (document.getElementById('setting-default-term') as HTMLSelectElement).value = '1';
      (document.getElementById('setting-default-payment') as HTMLSelectElement).value = 'no-upfront';
      (document.getElementById('setting-default-coverage') as HTMLInputElement).value = '50';
      (document.getElementById('setting-notification-days') as HTMLInputElement).value = '7';
    });

    test('does nothing if user cancels confirmation', async () => {
      mockConfirmDialog.mockResolvedValueOnce(false);

      await resetSettings();

      // Values should not change
      expect((document.getElementById('provider-azure') as HTMLInputElement).checked).toBe(true);
    });

    test('resets all fields to defaults on confirmation', async () => {
      mockConfirmDialog.mockResolvedValueOnce(true);

      await resetSettings();

      expect((document.getElementById('provider-aws') as HTMLInputElement).checked).toBe(true);
      expect((document.getElementById('provider-azure') as HTMLInputElement).checked).toBe(false);
      expect((document.getElementById('provider-gcp') as HTMLInputElement).checked).toBe(false);
      expect((document.getElementById('setting-notification-email') as HTMLInputElement).value).toBe('');
      expect((document.getElementById('setting-auto-collect') as HTMLInputElement).checked).toBe(true);
      expect((document.getElementById('setting-default-term') as HTMLSelectElement).value).toBe('3');
      expect((document.getElementById('setting-default-payment') as HTMLSelectElement).value).toBe('all-upfront');
      expect((document.getElementById('setting-default-coverage') as HTMLInputElement).value).toBe('80');
      expect((document.getElementById('setting-notification-days') as HTMLInputElement).value).toBe('3');
    });
  });

  describe('copyToClipboard', () => {
    let originalClipboard: Clipboard;

    beforeEach(() => {
      originalClipboard = navigator.clipboard;
    });

    afterEach(() => {
      Object.defineProperty(navigator, 'clipboard', {
        value: originalClipboard,
        writable: true
      });
    });

    test('copies text from element to clipboard', async () => {
      const writeTextMock = jest.fn().mockResolvedValue(undefined);
      Object.defineProperty(navigator, 'clipboard', {
        value: { writeText: writeTextMock },
        writable: true
      });

      copyToClipboard('test-copy-element');

      await new Promise(resolve => setTimeout(resolve, 0));

      expect(writeTextMock).toHaveBeenCalledWith('test-value');
    });

    test('shows copied feedback on button', async () => {
      jest.useFakeTimers();

      const writeTextMock = jest.fn().mockResolvedValue(undefined);
      Object.defineProperty(navigator, 'clipboard', {
        value: { writeText: writeTextMock },
        writable: true
      });

      copyToClipboard('test-copy-element');

      // Flush microtasks for the promise to resolve
      await Promise.resolve();
      jest.runAllTicks();

      const btn = document.querySelector('.copy-btn') as HTMLButtonElement;
      // The innerHTML will contain the actual checkmark character, not the HTML entity
      expect(btn.classList.contains('copied')).toBe(true);

      // Fast-forward timer
      jest.advanceTimersByTime(2000);

      expect(btn.innerHTML).toContain('Copy');
      expect(btn.classList.contains('copied')).toBe(false);

      jest.useRealTimers();
    });

    test('handles non-existent element gracefully', () => {
      const writeTextMock = jest.fn().mockResolvedValue(undefined);
      Object.defineProperty(navigator, 'clipboard', {
        value: { writeText: writeTextMock },
        writable: true
      });

      // Should not throw
      expect(() => copyToClipboard('non-existent-element')).not.toThrow();
      expect(writeTextMock).not.toHaveBeenCalled();
    });

    test('handles clipboard error gracefully', async () => {
      jest.useFakeTimers();

      const consoleError = jest.spyOn(console, 'error').mockImplementation(() => {});
      const writeTextMock = jest.fn().mockRejectedValue(new Error('Clipboard error'));
      Object.defineProperty(navigator, 'clipboard', {
        value: { writeText: writeTextMock },
        writable: true
      });

      copyToClipboard('test-copy-element');

      // Flush all promises
      await Promise.resolve();
      jest.runAllTicks();
      await Promise.resolve();

      expect(consoleError).toHaveBeenCalledWith('Failed to copy:', expect.any(Error));
      consoleError.mockRestore();
      jest.useRealTimers();
    });
  });

  describe('Azure payment defaults (issue #12)', () => {
    // Shared fixture covers term selects only; inject the four Azure payment
    // selects this issue introduces so loadGlobalSettings / saveGlobalSettings
    // have DOM nodes to read / write.
    const injectAzurePaymentSelects = () => {
      const form = document.getElementById('global-settings-form');
      if (!form) throw new Error('global-settings-form fixture missing');
      for (const svc of ['vm', 'sql', 'cosmosdb', 'redis']) {
        if (document.getElementById(`azure-${svc}-payment`)) continue;
        const select = document.createElement('select');
        select.id = `azure-${svc}-payment`;
        for (const [value, label] of [['all-upfront', 'Upfront'], ['no-upfront', 'Monthly']] as const) {
          const opt = document.createElement('option');
          opt.value = value;
          opt.textContent = label;
          if (value === 'all-upfront') opt.selected = true;
          select.appendChild(opt);
        }
        form.appendChild(select);
      }
    };

    test('loadGlobalSettings applies persisted azure-vm payment', async () => {
      injectAzurePaymentSelects();
      (api.getConfig as jest.Mock).mockResolvedValue({
        global: {
          enabled_providers: ['aws', 'azure'],
          default_term: 3,
          default_payment: 'all-upfront',
          default_coverage: 80,
        },
        services: [
          { provider: 'azure', service: 'vm', term: 3, payment: 'no-upfront', enabled: true, coverage: 80 },
        ],
      });

      await loadGlobalSettings();

      const vmPayment = document.getElementById('azure-vm-payment') as HTMLSelectElement;
      expect(vmPayment.value).toBe('no-upfront');
    });

    test('saveGlobalSettings sends the per-service Azure payment, not the global default', async () => {
      injectAzurePaymentSelects();
      (api.getConfig as jest.Mock).mockResolvedValue({
        global: { enabled_providers: ['aws', 'azure'], default_term: 3, default_payment: 'all-upfront', default_coverage: 80 },
        services: [],
      });
      (api.updateConfig as jest.Mock).mockResolvedValue({});
      (api.updateServiceConfig as jest.Mock).mockClear().mockResolvedValue(undefined);

      await loadGlobalSettings();
      // User flips Azure VM to Monthly while global default stays all-upfront.
      (document.getElementById('azure-vm-payment') as HTMLSelectElement).value = 'no-upfront';
      (document.getElementById('setting-default-payment') as HTMLSelectElement).value = 'all-upfront';

      await saveGlobalSettings({ preventDefault: jest.fn() } as unknown as Event);

      const azureVmCall = (api.updateServiceConfig as jest.Mock).mock.calls.find(
        ([provider, service]) => provider === 'azure' && service === 'vm',
      );
      expect(azureVmCall).toBeDefined();
      const cfg = azureVmCall![2];
      expect(cfg.payment).toBe('no-upfront');
    });

    test('help text describes the correct per-provider payment semantics', () => {
      // The help copy lives in index.html (loaded into the test fixture
      // verbatim at test startup). Guard against regressions in the
      // wording — the old copy falsely claimed Azure was always upfront.
      const fs = require('fs') as typeof import('fs');
      const path = require('path') as typeof import('path');
      const html = fs.readFileSync(path.join(__dirname, '..', 'index.html'), 'utf-8');
      expect(html).toMatch(/Azure reservations support upfront or monthly/);
      expect(html).not.toMatch(/Azure and GCP reservations are always paid upfront/);
    });
  });

  // Guard against the RDS 3yr + no-upfront regression from follow-up to
  // issue #12. The backend rejects that combination (and EC/OpenSearch/
  // Redshift 3yr no-upfront), so the Settings form must not allow it.
  // Rules live in commitmentOptions.ts; these tests exercise the wiring
  // that applies them to the per-service dropdowns.
  describe('per-service term/payment combination constraints', () => {
    const optVisible = (sel: HTMLSelectElement, value: string): boolean => {
      const opt = Array.from(sel.options).find(o => o.value === value);
      if (!opt) return false;
      return !opt.hidden && !opt.disabled;
    };

    test('RDS 3yr hides "no-upfront" and keeps partial/all upfront selectable', async () => {
      (api.getConfig as jest.Mock).mockResolvedValue({
        global: { enabled_providers: ['aws'], default_term: 3, default_payment: 'all-upfront', default_coverage: 80 },
        services: [{ provider: 'aws', service: 'rds', term: 3, payment: 'all-upfront' }],
      });
      setupSettingsHandlers();
      await loadGlobalSettings();

      const rdsPayment = document.getElementById('aws-rds-payment') as HTMLSelectElement;
      expect(optVisible(rdsPayment, 'no-upfront')).toBe(false);
      expect(optVisible(rdsPayment, 'partial-upfront')).toBe(true);
      expect(optVisible(rdsPayment, 'all-upfront')).toBe(true);
    });

    test('RDS 1yr keeps all three payment options visible', async () => {
      (api.getConfig as jest.Mock).mockResolvedValue({
        global: { enabled_providers: ['aws'], default_term: 1, default_payment: 'no-upfront', default_coverage: 80 },
        services: [{ provider: 'aws', service: 'rds', term: 1, payment: 'no-upfront' }],
      });
      setupSettingsHandlers();
      await loadGlobalSettings();

      const rdsPayment = document.getElementById('aws-rds-payment') as HTMLSelectElement;
      expect(optVisible(rdsPayment, 'no-upfront')).toBe(true);
      expect(optVisible(rdsPayment, 'partial-upfront')).toBe(true);
      expect(optVisible(rdsPayment, 'all-upfront')).toBe(true);
    });

    test('switching RDS term 1yr → 3yr while "no-upfront" is selected auto-clamps payment', async () => {
      (api.getConfig as jest.Mock).mockResolvedValue({
        global: { enabled_providers: ['aws'], default_term: 1, default_payment: 'no-upfront', default_coverage: 80 },
        services: [{ provider: 'aws', service: 'rds', term: 1, payment: 'no-upfront' }],
      });
      setupSettingsHandlers();
      await loadGlobalSettings();

      const rdsTerm = document.getElementById('aws-rds-term') as HTMLSelectElement;
      const rdsPayment = document.getElementById('aws-rds-payment') as HTMLSelectElement;
      expect(rdsPayment.value).toBe('no-upfront');

      rdsTerm.value = '3';
      rdsTerm.dispatchEvent(new Event('change'));

      // no-upfront is now invalid; payment should snap to first valid option
      expect(rdsPayment.value).not.toBe('no-upfront');
      expect(['partial-upfront', 'all-upfront']).toContain(rdsPayment.value);
      expect(optVisible(rdsPayment, 'no-upfront')).toBe(false);
    });

    test('legacy-persisted invalid combo (RDS 3yr + no-upfront) is clamped on load', async () => {
      (api.getConfig as jest.Mock).mockResolvedValue({
        global: { enabled_providers: ['aws'], default_term: 3, default_payment: 'all-upfront', default_coverage: 80 },
        // Simulate a config stored before this guardrail existed.
        services: [{ provider: 'aws', service: 'rds', term: 3, payment: 'no-upfront' }],
      });
      setupSettingsHandlers();
      await loadGlobalSettings();

      const rdsPayment = document.getElementById('aws-rds-payment') as HTMLSelectElement;
      expect(rdsPayment.value).not.toBe('no-upfront');
    });

    test('EC2 3yr keeps all three payment options visible (no service-level restriction)', async () => {
      (api.getConfig as jest.Mock).mockResolvedValue({
        global: { enabled_providers: ['aws'], default_term: 3, default_payment: 'no-upfront', default_coverage: 80 },
        services: [{ provider: 'aws', service: 'ec2', term: 3, payment: 'no-upfront' }],
      });
      setupSettingsHandlers();
      await loadGlobalSettings();

      const ec2Payment = document.getElementById('aws-ec2-payment') as HTMLSelectElement;
      expect(optVisible(ec2Payment, 'no-upfront')).toBe(true);
      expect(optVisible(ec2Payment, 'partial-upfront')).toBe(true);
      expect(optVisible(ec2Payment, 'all-upfront')).toBe(true);
      expect(ec2Payment.value).toBe('no-upfront');
    });

    test.each(['elasticache', 'opensearch', 'redshift'])(
      '%s 3yr keeps "no-upfront" visible (AWS only restricts RDS)',
      async (service) => {
        (api.getConfig as jest.Mock).mockResolvedValue({
          global: { enabled_providers: ['aws'], default_term: 3, default_payment: 'no-upfront', default_coverage: 80 },
          services: [{ provider: 'aws', service, term: 3, payment: 'no-upfront' }],
        });
        setupSettingsHandlers();
        await loadGlobalSettings();

        const payment = document.getElementById(`aws-${service}-payment`) as HTMLSelectElement;
        expect(optVisible(payment, 'no-upfront')).toBe(true);
        // And the selected value round-trips cleanly — the backend persists
        // this service with no-upfront, and the UI should not clamp it.
        expect(payment.value).toBe('no-upfront');
      },
    );

    test('SageMaker 3yr keeps "no-upfront" visible (issue #22 — no service-level restriction)', async () => {
      (api.getConfig as jest.Mock).mockResolvedValue({
        global: { enabled_providers: ['aws'], default_term: 3, default_payment: 'no-upfront', default_coverage: 80 },
        services: [{ provider: 'aws', service: 'sagemaker', term: 3, payment: 'no-upfront' }],
      });
      setupSettingsHandlers();
      await loadGlobalSettings();

      const payment = document.getElementById('aws-sagemaker-payment') as HTMLSelectElement;
      expect(optVisible(payment, 'no-upfront')).toBe(true);
      expect(optVisible(payment, 'partial-upfront')).toBe(true);
      expect(optVisible(payment, 'all-upfront')).toBe(true);
      expect(payment.value).toBe('no-upfront');
    });

    test('propagating global "no-upfront" to all services while term=3 clamps restricted services', async () => {
      (api.getConfig as jest.Mock).mockResolvedValue({
        global: { enabled_providers: ['aws'], default_term: 3, default_payment: 'all-upfront', default_coverage: 80 },
        services: [
          { provider: 'aws', service: 'ec2', term: 3, payment: 'all-upfront' },
          { provider: 'aws', service: 'rds', term: 3, payment: 'all-upfront' },
        ],
      });
      setupSettingsHandlers();
      await loadGlobalSettings();

      // User changes the global default to no-upfront and confirms the propagation.
      mockConfirmDialog.mockResolvedValue(true);
      const defaultPayment = document.getElementById('setting-default-payment') as HTMLSelectElement;
      defaultPayment.dataset['previous'] = 'all-upfront';
      defaultPayment.value = 'no-upfront';
      defaultPayment.dispatchEvent(new Event('change'));

      // Allow the async confirmDialog promise to resolve.
      await Promise.resolve();
      await Promise.resolve();

      const ec2Payment = document.getElementById('aws-ec2-payment') as HTMLSelectElement;
      const rdsPayment = document.getElementById('aws-rds-payment') as HTMLSelectElement;
      // EC2 accepts the propagated no-upfront (no restriction).
      expect(ec2Payment.value).toBe('no-upfront');
      // RDS 3yr rejects no-upfront, so it clamps back to the first valid option.
      expect(rdsPayment.value).not.toBe('no-upfront');
    });
  });
});
