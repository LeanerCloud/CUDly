/**
 * Settings module tests
 */
import {
  loadGlobalSettings,
  saveGlobalSettings,
  resetSettings,
  setupSettingsHandlers,
  showAzureCredsModal,
  closeAzureCredsModal,
  showGCPCredsModal,
  closeGCPCredsModal,
  copyToClipboard
} from '../settings';

// Mock the api module
jest.mock('../api', () => ({
  getConfig: jest.fn(),
  updateConfig: jest.fn(),
  updateServiceConfig: jest.fn().mockResolvedValue(undefined),
  saveAzureCredentials: jest.fn(),
  saveGCPCredentials: jest.fn(),
  listAccounts: jest.fn(),
  createAccount: jest.fn(),
  updateAccount: jest.fn(),
  deleteAccount: jest.fn(),
  testAccountCredentials: jest.fn(),
  saveAccountCredentials: jest.fn()
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
        <div id="collection-schedule-row" style="display: none;">
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
        <select id="aws-ec2-payment"><option value="all-upfront">All</option><option value="no-upfront">No</option></select>
        <select id="aws-rds-payment"><option value="all-upfront">All</option><option value="no-upfront">No</option></select>
        <select id="aws-elasticache-payment"><option value="all-upfront">All</option><option value="no-upfront">No</option></select>
        <select id="aws-opensearch-payment"><option value="all-upfront">All</option><option value="no-upfront">No</option></select>
        <select id="aws-redshift-payment"><option value="all-upfront">All</option><option value="no-upfront">No</option></select>
        <select id="aws-savingsplans-payment"><option value="all-upfront">All</option><option value="no-upfront">No</option></select>
        <!-- Azure term selects -->
        <select id="azure-vm-term"><option value="1">1</option><option value="3">3</option></select>
        <select id="azure-sql-term"><option value="1">1</option><option value="3">3</option></select>
        <select id="azure-cosmos-term"><option value="1">1</option><option value="3">3</option></select>
        <!-- GCP term selects -->
        <select id="gcp-compute-term"><option value="1">1</option><option value="3">3</option></select>
        <select id="gcp-sql-term"><option value="1">1</option><option value="3">3</option></select>
      </form>
      <div id="settings-error" class="hidden"></div>
      <div id="azure-creds-status"></div>
      <div id="gcp-creds-status"></div>
      <button id="azure-configure-btn"></button>
      <button id="gcp-configure-btn"></button>
      <!-- Azure Credentials Modal -->
      <div id="azure-creds-modal" class="hidden">
        <form id="azure-creds-form">
          <input type="text" id="azure-tenant-id">
          <input type="text" id="azure-client-id">
          <input type="password" id="azure-client-secret">
          <input type="text" id="azure-subscription-id">
          <div id="azure-creds-error" class="hidden"></div>
        </form>
      </div>
      <!-- GCP Credentials Modal -->
      <div id="gcp-creds-modal" class="hidden">
        <form id="gcp-creds-form">
          <textarea id="gcp-service-account-json"></textarea>
          <div id="gcp-creds-error" class="hidden"></div>
        </form>
      </div>
      <!-- Test element for copyToClipboard -->
      <code id="test-copy-element">test-value</code>
      <button class="copy-btn"><span class="copy-icon">Copy</span></button>
    `;

    jest.clearAllMocks();
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

      expect(scheduleRow?.style.display).toBe('flex');

      autoCollect.checked = false;
      autoCollect.dispatchEvent(new Event('change'));

      expect(scheduleRow?.style.display).toBe('none');
    });

    test('sets up default term to propagate to services', () => {
      setupSettingsHandlers();

      const defaultTerm = document.getElementById('setting-default-term') as HTMLSelectElement;
      defaultTerm.value = '1';
      defaultTerm.dispatchEvent(new Event('change'));

      expect((document.getElementById('aws-ec2-term') as HTMLSelectElement).value).toBe('1');
      expect((document.getElementById('aws-rds-term') as HTMLSelectElement).value).toBe('1');
      expect((document.getElementById('azure-vm-term') as HTMLSelectElement).value).toBe('1');
      expect((document.getElementById('gcp-compute-term') as HTMLSelectElement).value).toBe('1');
    });

    test('sets up default payment to propagate to AWS services', () => {
      setupSettingsHandlers();

      const defaultPayment = document.getElementById('setting-default-payment') as HTMLSelectElement;
      defaultPayment.value = 'no-upfront';
      defaultPayment.dispatchEvent(new Event('change'));

      expect((document.getElementById('aws-ec2-payment') as HTMLSelectElement).value).toBe('no-upfront');
      expect((document.getElementById('aws-rds-payment') as HTMLSelectElement).value).toBe('no-upfront');
    });

    test('sets up azure configure button', () => {
      setupSettingsHandlers();

      const azureBtn = document.getElementById('azure-configure-btn');
      const azureModal = document.getElementById('azure-creds-modal');

      azureBtn?.dispatchEvent(new Event('click'));

      expect(azureModal?.classList.contains('hidden')).toBe(false);
    });

    test('sets up gcp configure button', () => {
      setupSettingsHandlers();

      const gcpBtn = document.getElementById('gcp-configure-btn');
      const gcpModal = document.getElementById('gcp-creds-modal');

      gcpBtn?.dispatchEvent(new Event('click'));

      expect(gcpModal?.classList.contains('hidden')).toBe(false);
    });

    test('closes azure modal when clicking outside', () => {
      setupSettingsHandlers();

      const azureModal = document.getElementById('azure-creds-modal');
      azureModal?.classList.remove('hidden');

      // Simulate click on modal backdrop (the modal element itself)
      const clickEvent = new MouseEvent('click', { bubbles: true });
      Object.defineProperty(clickEvent, 'target', { value: azureModal });
      azureModal?.dispatchEvent(clickEvent);

      expect(azureModal?.classList.contains('hidden')).toBe(true);
    });

    test('closes gcp modal when clicking outside', () => {
      setupSettingsHandlers();

      const gcpModal = document.getElementById('gcp-creds-modal');
      gcpModal?.classList.remove('hidden');

      // Simulate click on modal backdrop
      const clickEvent = new MouseEvent('click', { bubbles: true });
      Object.defineProperty(clickEvent, 'target', { value: gcpModal });
      gcpModal?.dispatchEvent(clickEvent);

      expect(gcpModal?.classList.contains('hidden')).toBe(true);
    });

    test('sets up azure credentials form handler', async () => {
      (api.saveAzureCredentials as jest.Mock).mockResolvedValue({});
      setupSettingsHandlers();

      // Fill in the form
      (document.getElementById('azure-tenant-id') as HTMLInputElement).value = 'tenant-123';
      (document.getElementById('azure-client-id') as HTMLInputElement).value = 'client-456';
      (document.getElementById('azure-client-secret') as HTMLInputElement).value = 'secret-789';
      (document.getElementById('azure-subscription-id') as HTMLInputElement).value = 'sub-012';

      const form = document.getElementById('azure-creds-form');
      form?.dispatchEvent(new Event('submit'));

      // Wait for async operations
      await new Promise(resolve => setTimeout(resolve, 0));

      expect(api.saveAzureCredentials).toHaveBeenCalledWith({
        tenant_id: 'tenant-123',
        client_id: 'client-456',
        client_secret: 'secret-789',
        subscription_id: 'sub-012'
      });
    });

    test('sets up gcp credentials form handler', async () => {
      (api.saveGCPCredentials as jest.Mock).mockResolvedValue({});
      setupSettingsHandlers();

      const validGCPJson = JSON.stringify({
        type: 'service_account',
        project_id: 'my-project',
        private_key: 'test-private-key-placeholder',
        client_email: 'test@my-project.iam.gserviceaccount.com'
      });

      (document.getElementById('gcp-service-account-json') as HTMLTextAreaElement).value = validGCPJson;

      const form = document.getElementById('gcp-creds-form');
      form?.dispatchEvent(new Event('submit'));

      // Wait for async operations
      await new Promise(resolve => setTimeout(resolve, 0));

      expect(api.saveGCPCredentials).toHaveBeenCalled();
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

    test('displays credentials status', async () => {
      (api.getConfig as jest.Mock).mockResolvedValue({
        global: {
          enabled_providers: [],
          default_term: 3,
          default_payment: 'all-upfront',
          default_coverage: 80,
          notification_days_before: 3
        },
        credentials: {
          azure_configured: true,
          gcp_configured: false
        }
      });

      await loadGlobalSettings();

      const azureStatus = document.getElementById('azure-creds-status');
      const gcpStatus = document.getElementById('gcp-creds-status');

      expect(azureStatus?.textContent).toBe('Configured');
      expect(azureStatus?.classList.contains('configured')).toBe(true);
      expect(gcpStatus?.textContent).toBe('Not Configured');
      expect(gcpStatus?.classList.contains('configured')).toBe(false);
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
        notification_days_before: 3
      });
    });

    test('shows success alert on save', async () => {
      (api.updateConfig as jest.Mock).mockResolvedValue({});
      window.alert = jest.fn();

      const event = { preventDefault: jest.fn() } as unknown as Event;
      await saveGlobalSettings(event);

      expect(window.alert).toHaveBeenCalledWith('Settings saved successfully');
    });

    test('shows error alert on failure', async () => {
      (api.updateConfig as jest.Mock).mockRejectedValue(new Error('Save failed'));
      window.alert = jest.fn();

      const event = { preventDefault: jest.fn() } as unknown as Event;
      await saveGlobalSettings(event);

      expect(window.alert).toHaveBeenCalledWith('Failed to save settings: Save failed');
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

    test('calls updateServiceConfig once per service field (14 calls)', async () => {
      (api.updateConfig as jest.Mock).mockResolvedValue({});
      (api.updateServiceConfig as jest.Mock).mockResolvedValue(undefined);
      window.alert = jest.fn();

      const event = { preventDefault: jest.fn() } as unknown as Event;
      await saveGlobalSettings(event);

      expect(api.updateServiceConfig).toHaveBeenCalledTimes(14);
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

    test('does nothing if user cancels confirmation', () => {
      window.confirm = jest.fn().mockReturnValue(false);

      resetSettings();

      // Values should not change
      expect((document.getElementById('provider-azure') as HTMLInputElement).checked).toBe(true);
    });

    test('resets all fields to defaults on confirmation', () => {
      window.confirm = jest.fn().mockReturnValue(true);

      resetSettings();

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

  describe('showAzureCredsModal', () => {
    test('shows the azure credentials modal', () => {
      const modal = document.getElementById('azure-creds-modal');
      const errorEl = document.getElementById('azure-creds-error');

      showAzureCredsModal();

      expect(modal?.classList.contains('hidden')).toBe(false);
      expect(errorEl?.classList.contains('hidden')).toBe(true);
    });

    test('handles missing modal gracefully', () => {
      document.body.innerHTML = '';

      // Should not throw
      expect(() => showAzureCredsModal()).not.toThrow();
    });
  });

  describe('closeAzureCredsModal', () => {
    test('hides the azure credentials modal and resets form', () => {
      const modal = document.getElementById('azure-creds-modal');
      modal?.classList.remove('hidden');

      // Set some form values
      (document.getElementById('azure-tenant-id') as HTMLInputElement).value = 'test';

      closeAzureCredsModal();

      expect(modal?.classList.contains('hidden')).toBe(true);
      expect((document.getElementById('azure-tenant-id') as HTMLInputElement).value).toBe('');
    });

    test('handles missing modal gracefully', () => {
      document.body.innerHTML = '';

      // Should not throw
      expect(() => closeAzureCredsModal()).not.toThrow();
    });
  });

  describe('showGCPCredsModal', () => {
    test('shows the gcp credentials modal', () => {
      const modal = document.getElementById('gcp-creds-modal');
      const errorEl = document.getElementById('gcp-creds-error');

      showGCPCredsModal();

      expect(modal?.classList.contains('hidden')).toBe(false);
      expect(errorEl?.classList.contains('hidden')).toBe(true);
    });

    test('handles missing modal gracefully', () => {
      document.body.innerHTML = '';

      // Should not throw
      expect(() => showGCPCredsModal()).not.toThrow();
    });
  });

  describe('closeGCPCredsModal', () => {
    test('hides the gcp credentials modal and resets form', () => {
      const modal = document.getElementById('gcp-creds-modal');
      modal?.classList.remove('hidden');

      // Set some form values
      (document.getElementById('gcp-service-account-json') as HTMLTextAreaElement).value = '{}';

      closeGCPCredsModal();

      expect(modal?.classList.contains('hidden')).toBe(true);
      expect((document.getElementById('gcp-service-account-json') as HTMLTextAreaElement).value).toBe('');
    });

    test('handles missing modal gracefully', () => {
      document.body.innerHTML = '';

      // Should not throw
      expect(() => closeGCPCredsModal()).not.toThrow();
    });
  });

  describe('Azure Credentials Form', () => {
    beforeEach(() => {
      setupSettingsHandlers();
    });

    test('shows error when fields are empty', async () => {
      const form = document.getElementById('azure-creds-form');
      form?.dispatchEvent(new Event('submit'));

      await new Promise(resolve => setTimeout(resolve, 0));

      const errorEl = document.getElementById('azure-creds-error');
      expect(errorEl?.classList.contains('hidden')).toBe(false);
      expect(errorEl?.textContent).toBe('All fields are required');
    });

    test('shows error when some fields are missing', async () => {
      (document.getElementById('azure-tenant-id') as HTMLInputElement).value = 'tenant';
      // Leave other fields empty

      const form = document.getElementById('azure-creds-form');
      form?.dispatchEvent(new Event('submit'));

      await new Promise(resolve => setTimeout(resolve, 0));

      const errorEl = document.getElementById('azure-creds-error');
      expect(errorEl?.textContent).toBe('All fields are required');
    });

    test('updates status on successful save', async () => {
      (api.saveAzureCredentials as jest.Mock).mockResolvedValue({});
      window.alert = jest.fn();

      (document.getElementById('azure-tenant-id') as HTMLInputElement).value = 'tenant-123';
      (document.getElementById('azure-client-id') as HTMLInputElement).value = 'client-456';
      (document.getElementById('azure-client-secret') as HTMLInputElement).value = 'secret-789';
      (document.getElementById('azure-subscription-id') as HTMLInputElement).value = 'sub-012';

      const form = document.getElementById('azure-creds-form');
      form?.dispatchEvent(new Event('submit'));

      await new Promise(resolve => setTimeout(resolve, 0));

      const statusEl = document.getElementById('azure-creds-status');
      expect(statusEl?.textContent).toBe('Configured');
      expect(statusEl?.classList.contains('configured')).toBe(true);
      expect(window.alert).toHaveBeenCalledWith('Azure credentials saved successfully');
    });

    test('shows error on API failure', async () => {
      (api.saveAzureCredentials as jest.Mock).mockRejectedValue(new Error('Save failed'));

      (document.getElementById('azure-tenant-id') as HTMLInputElement).value = 'tenant-123';
      (document.getElementById('azure-client-id') as HTMLInputElement).value = 'client-456';
      (document.getElementById('azure-client-secret') as HTMLInputElement).value = 'secret-789';
      (document.getElementById('azure-subscription-id') as HTMLInputElement).value = 'sub-012';

      const form = document.getElementById('azure-creds-form');
      form?.dispatchEvent(new Event('submit'));

      await new Promise(resolve => setTimeout(resolve, 0));

      const errorEl = document.getElementById('azure-creds-error');
      expect(errorEl?.classList.contains('hidden')).toBe(false);
      expect(errorEl?.textContent).toContain('Failed to save');
    });
  });

  describe('GCP Credentials Form', () => {
    beforeEach(() => {
      setupSettingsHandlers();
    });

    test('shows error when JSON is empty', async () => {
      const form = document.getElementById('gcp-creds-form');
      form?.dispatchEvent(new Event('submit'));

      await new Promise(resolve => setTimeout(resolve, 0));

      const errorEl = document.getElementById('gcp-creds-error');
      expect(errorEl?.classList.contains('hidden')).toBe(false);
      expect(errorEl?.textContent).toBe('Service account JSON is required');
    });

    test('shows error for invalid JSON', async () => {
      (document.getElementById('gcp-service-account-json') as HTMLTextAreaElement).value = 'not valid json';

      const form = document.getElementById('gcp-creds-form');
      form?.dispatchEvent(new Event('submit'));

      await new Promise(resolve => setTimeout(resolve, 0));

      const errorEl = document.getElementById('gcp-creds-error');
      expect(errorEl?.textContent).toBe('Invalid JSON format');
    });

    test('shows error for missing required fields', async () => {
      (document.getElementById('gcp-service-account-json') as HTMLTextAreaElement).value = '{"type": "service_account"}';

      const form = document.getElementById('gcp-creds-form');
      form?.dispatchEvent(new Event('submit'));

      await new Promise(resolve => setTimeout(resolve, 0));

      const errorEl = document.getElementById('gcp-creds-error');
      expect(errorEl?.textContent).toContain('Missing required fields');
    });

    test('updates status on successful save', async () => {
      (api.saveGCPCredentials as jest.Mock).mockResolvedValue({});
      window.alert = jest.fn();

      const validJson = JSON.stringify({
        type: 'service_account',
        project_id: 'my-project',
        private_key: 'test-private-key-placeholder',
        client_email: 'test@my-project.iam.gserviceaccount.com'
      });

      (document.getElementById('gcp-service-account-json') as HTMLTextAreaElement).value = validJson;

      const form = document.getElementById('gcp-creds-form');
      form?.dispatchEvent(new Event('submit'));

      await new Promise(resolve => setTimeout(resolve, 0));

      const statusEl = document.getElementById('gcp-creds-status');
      expect(statusEl?.textContent).toBe('Configured');
      expect(statusEl?.classList.contains('configured')).toBe(true);
      expect(window.alert).toHaveBeenCalledWith('GCP credentials saved successfully');
    });

    test('shows error on API failure', async () => {
      (api.saveGCPCredentials as jest.Mock).mockRejectedValue(new Error('Save failed'));

      const validJson = JSON.stringify({
        type: 'service_account',
        project_id: 'my-project',
        private_key: 'test-private-key-placeholder',
        client_email: 'test@my-project.iam.gserviceaccount.com'
      });

      (document.getElementById('gcp-service-account-json') as HTMLTextAreaElement).value = validJson;

      const form = document.getElementById('gcp-creds-form');
      form?.dispatchEvent(new Event('submit'));

      await new Promise(resolve => setTimeout(resolve, 0));

      const errorEl = document.getElementById('gcp-creds-error');
      expect(errorEl?.classList.contains('hidden')).toBe(false);
      expect(errorEl?.textContent).toContain('Failed to save');
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
});
