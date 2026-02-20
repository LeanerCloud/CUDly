/**
 * Settings module for CUDly
 */

import * as api from './api';
import type { ConfigResponse } from './types';

/**
 * Set up settings event handlers
 */
export function setupSettingsHandlers(): void {
  // Provider checkbox handlers - toggle visibility of provider settings sections
  const awsCheck = document.getElementById('provider-aws') as HTMLInputElement | null;
  const azureCheck = document.getElementById('provider-azure') as HTMLInputElement | null;
  const gcpCheck = document.getElementById('provider-gcp') as HTMLInputElement | null;

  awsCheck?.addEventListener('change', () => updateProviderSettingsVisibility());
  azureCheck?.addEventListener('change', () => updateProviderSettingsVisibility());
  gcpCheck?.addEventListener('change', () => updateProviderSettingsVisibility());

  // Auto-collect checkbox - toggle schedule visibility
  const autoCollect = document.getElementById('setting-auto-collect') as HTMLInputElement | null;
  autoCollect?.addEventListener('change', () => updateCollectionScheduleVisibility());

  // Global defaults - propagate to all services when changed
  const defaultTerm = document.getElementById('setting-default-term') as HTMLSelectElement | null;
  const defaultPayment = document.getElementById('setting-default-payment') as HTMLSelectElement | null;

  defaultTerm?.addEventListener('change', () => propagateTermToServices(defaultTerm.value));
  defaultPayment?.addEventListener('change', () => propagatePaymentToServices(defaultPayment.value));

  // Credential configure button handlers
  const azureConfigBtn = document.getElementById('azure-configure-btn');
  const gcpConfigBtn = document.getElementById('gcp-configure-btn');

  azureConfigBtn?.addEventListener('click', showAzureCredsModal);
  gcpConfigBtn?.addEventListener('click', showGCPCredsModal);

  // Azure credentials form handler
  const azureCredsForm = document.getElementById('azure-creds-form');
  azureCredsForm?.addEventListener('submit', handleAzureCredsSave);

  // GCP credentials form handler
  const gcpCredsForm = document.getElementById('gcp-creds-form');
  gcpCredsForm?.addEventListener('submit', handleGCPCredsSave);

  // Close modals when clicking outside
  const azureModal = document.getElementById('azure-creds-modal');
  const gcpModal = document.getElementById('gcp-creds-modal');

  azureModal?.addEventListener('click', (e) => {
    if (e.target === azureModal) closeAzureCredsModal();
  });
  gcpModal?.addEventListener('click', (e) => {
    if (e.target === gcpModal) closeGCPCredsModal();
  });
}

/**
 * Update visibility of provider settings sections based on checkboxes
 */
function updateProviderSettingsVisibility(): void {
  const awsCheck = document.getElementById('provider-aws') as HTMLInputElement | null;
  const azureCheck = document.getElementById('provider-azure') as HTMLInputElement | null;
  const gcpCheck = document.getElementById('provider-gcp') as HTMLInputElement | null;

  const awsSettings = document.getElementById('aws-settings');
  const azureSettings = document.getElementById('azure-settings');
  const gcpSettings = document.getElementById('gcp-settings');

  if (awsSettings) awsSettings.classList.toggle('hidden', !awsCheck?.checked);
  if (azureSettings) azureSettings.classList.toggle('hidden', !azureCheck?.checked);
  if (gcpSettings) gcpSettings.classList.toggle('hidden', !gcpCheck?.checked);
}

/**
 * Update visibility of collection schedule row based on auto-collect checkbox
 */
function updateCollectionScheduleVisibility(): void {
  const autoCollect = document.getElementById('setting-auto-collect') as HTMLInputElement | null;
  const scheduleRow = document.getElementById('collection-schedule-row');

  if (scheduleRow) {
    scheduleRow.style.display = autoCollect?.checked ? 'flex' : 'none';
  }
}

/**
 * Propagate default term to all service-specific term selects
 */
function propagateTermToServices(term: string): void {
  // AWS services
  const awsTermSelects = [
    'aws-ec2-term',
    'aws-rds-term',
    'aws-elasticache-term',
    'aws-opensearch-term',
    'aws-redshift-term',
    'aws-savingsplans-term'
  ];

  // Azure services
  const azureTermSelects = [
    'azure-vm-term',
    'azure-sql-term',
    'azure-cosmos-term'
  ];

  // GCP services
  const gcpTermSelects = [
    'gcp-compute-term',
    'gcp-sql-term'
  ];

  [...awsTermSelects, ...azureTermSelects, ...gcpTermSelects].forEach(id => {
    const select = document.getElementById(id) as HTMLSelectElement | null;
    if (select) {
      select.value = term;
    }
  });
}

/**
 * Propagate default payment option to all service-specific payment selects
 * Note: Only AWS services have payment options; Azure/GCP use full upfront only
 */
function propagatePaymentToServices(payment: string): void {
  // Only AWS services have payment options
  const awsPaymentSelects = [
    'aws-ec2-payment',
    'aws-rds-payment',
    'aws-elasticache-payment',
    'aws-opensearch-payment',
    'aws-redshift-payment',
    'aws-savingsplans-payment'
  ];

  awsPaymentSelects.forEach(id => {
    const select = document.getElementById(id) as HTMLSelectElement | null;
    if (select) {
      select.value = payment;
    }
  });
}

/**
 * Load global settings
 */
export async function loadGlobalSettings(): Promise<void> {
  const loadingEl = document.getElementById('settings-loading');
  const formEl = document.getElementById('global-settings-form');
  const errorEl = document.getElementById('settings-error');

  if (loadingEl) loadingEl.classList.remove('hidden');
  if (formEl) formEl.classList.add('hidden');
  if (errorEl) errorEl.classList.add('hidden');

  try {
    const data = await api.getConfig() as unknown as ConfigResponse;

    if (data.global) {
      const providers = data.global.enabled_providers || [];
      const awsCheck = document.getElementById('provider-aws') as HTMLInputElement | null;
      const azureCheck = document.getElementById('provider-azure') as HTMLInputElement | null;
      const gcpCheck = document.getElementById('provider-gcp') as HTMLInputElement | null;

      if (awsCheck) awsCheck.checked = providers.includes('aws');
      if (azureCheck) azureCheck.checked = providers.includes('azure');
      if (gcpCheck) gcpCheck.checked = providers.includes('gcp');

      const emailInput = document.getElementById('setting-notification-email') as HTMLInputElement | null;
      if (emailInput) emailInput.value = data.global.notification_email || '';

      const autoCollect = document.getElementById('setting-auto-collect') as HTMLInputElement | null;
      if (autoCollect) autoCollect.checked = data.global.auto_collect !== false;

      const collectionSchedule = document.getElementById('setting-collection-schedule') as HTMLSelectElement | null;
      if (collectionSchedule) collectionSchedule.value = data.global.collection_schedule || 'daily';

      const termSelect = document.getElementById('setting-default-term') as HTMLSelectElement | null;
      if (termSelect) termSelect.value = String(data.global.default_term || 3);

      const paymentSelect = document.getElementById('setting-default-payment') as HTMLSelectElement | null;
      if (paymentSelect) paymentSelect.value = data.global.default_payment || 'all-upfront';

      const coverageInput = document.getElementById('setting-default-coverage') as HTMLInputElement | null;
      if (coverageInput) coverageInput.value = String(data.global.default_coverage || 80);

      const notifyDaysInput = document.getElementById('setting-notification-days') as HTMLInputElement | null;
      if (notifyDaysInput) notifyDaysInput.value = String(data.global.notification_days_before || 3);

      // Update visibility based on loaded settings
      updateProviderSettingsVisibility();
      updateCollectionScheduleVisibility();
    }

    if (data.credentials) {
      const azureStatus = document.getElementById('azure-creds-status');
      const gcpStatus = document.getElementById('gcp-creds-status');

      if (azureStatus) {
        azureStatus.textContent = data.credentials.azure_configured ? 'Configured' : 'Not Configured';
        azureStatus.classList.toggle('configured', data.credentials.azure_configured === true);
      }
      if (gcpStatus) {
        gcpStatus.textContent = data.credentials.gcp_configured ? 'Configured' : 'Not Configured';
        gcpStatus.classList.toggle('configured', data.credentials.gcp_configured === true);
      }
    }

    if (loadingEl) loadingEl.classList.add('hidden');
    if (formEl) formEl.classList.remove('hidden');
  } catch (error) {
    console.error('Failed to load settings:', error);
    if (loadingEl) loadingEl.classList.add('hidden');
    if (errorEl) {
      const err = error as Error;
      errorEl.textContent = `Failed to load settings: ${err.message}`;
      errorEl.classList.remove('hidden');
    }
  }
}

/**
 * Save global settings
 */
export async function saveGlobalSettings(e: Event): Promise<void> {
  e.preventDefault();

  const enabledProviders: api.Provider[] = [];
  if ((document.getElementById('provider-aws') as HTMLInputElement | null)?.checked) enabledProviders.push('aws');
  if ((document.getElementById('provider-azure') as HTMLInputElement | null)?.checked) enabledProviders.push('azure');
  if ((document.getElementById('provider-gcp') as HTMLInputElement | null)?.checked) enabledProviders.push('gcp');

  const settings: api.Config = {
    enabled_providers: enabledProviders,
    notification_email: (document.getElementById('setting-notification-email') as HTMLInputElement | null)?.value || '',
    auto_collect: (document.getElementById('setting-auto-collect') as HTMLInputElement | null)?.checked ?? true,
    default_term: parseInt((document.getElementById('setting-default-term') as HTMLSelectElement | null)?.value || '3', 10),
    default_payment: ((document.getElementById('setting-default-payment') as HTMLSelectElement | null)?.value || 'all-upfront') as api.PaymentOption,
    default_coverage: parseInt((document.getElementById('setting-default-coverage') as HTMLInputElement | null)?.value || '80', 10),
    notification_days: parseInt((document.getElementById('setting-notification-days') as HTMLInputElement | null)?.value || '3', 10)
  };

  try {
    await api.updateConfig(settings);
    alert('Settings saved successfully');
  } catch (error) {
    console.error('Failed to save settings:', error);
    const err = error as Error;
    alert(`Failed to save settings: ${err.message}`);
  }
}

/**
 * Reset settings to defaults
 */
export function resetSettings(): void {
  if (!confirm('Are you sure you want to reset all settings to defaults?')) return;

  const awsCheck = document.getElementById('provider-aws') as HTMLInputElement | null;
  const azureCheck = document.getElementById('provider-azure') as HTMLInputElement | null;
  const gcpCheck = document.getElementById('provider-gcp') as HTMLInputElement | null;
  if (awsCheck) awsCheck.checked = true;
  if (azureCheck) azureCheck.checked = false;
  if (gcpCheck) gcpCheck.checked = false;

  const emailInput = document.getElementById('setting-notification-email') as HTMLInputElement | null;
  if (emailInput) emailInput.value = '';

  const autoCollect = document.getElementById('setting-auto-collect') as HTMLInputElement | null;
  if (autoCollect) autoCollect.checked = true;

  const termSelect = document.getElementById('setting-default-term') as HTMLSelectElement | null;
  if (termSelect) termSelect.value = '3';

  const paymentSelect = document.getElementById('setting-default-payment') as HTMLSelectElement | null;
  if (paymentSelect) paymentSelect.value = 'all-upfront';

  const coverageInput = document.getElementById('setting-default-coverage') as HTMLInputElement | null;
  if (coverageInput) coverageInput.value = '80';

  const notifyDaysInput = document.getElementById('setting-notification-days') as HTMLInputElement | null;
  if (notifyDaysInput) notifyDaysInput.value = '3';
}

/**
 * Show Azure credentials modal
 */
export function showAzureCredsModal(): void {
  const modal = document.getElementById('azure-creds-modal');
  const errorEl = document.getElementById('azure-creds-error');
  if (modal) modal.classList.remove('hidden');
  if (errorEl) errorEl.classList.add('hidden');
}

/**
 * Close Azure credentials modal
 */
export function closeAzureCredsModal(): void {
  const modal = document.getElementById('azure-creds-modal');
  if (modal) modal.classList.add('hidden');
  // Clear form
  const form = document.getElementById('azure-creds-form') as HTMLFormElement | null;
  form?.reset();
}

/**
 * Show GCP credentials modal
 */
export function showGCPCredsModal(): void {
  const modal = document.getElementById('gcp-creds-modal');
  const errorEl = document.getElementById('gcp-creds-error');
  if (modal) modal.classList.remove('hidden');
  if (errorEl) errorEl.classList.add('hidden');
}

/**
 * Close GCP credentials modal
 */
export function closeGCPCredsModal(): void {
  const modal = document.getElementById('gcp-creds-modal');
  if (modal) modal.classList.add('hidden');
  // Clear form
  const form = document.getElementById('gcp-creds-form') as HTMLFormElement | null;
  form?.reset();
}

/**
 * Handle Azure credentials save
 */
async function handleAzureCredsSave(e: Event): Promise<void> {
  e.preventDefault();
  const errorEl = document.getElementById('azure-creds-error');

  const tenantId = (document.getElementById('azure-tenant-id') as HTMLInputElement)?.value.trim();
  const clientId = (document.getElementById('azure-client-id') as HTMLInputElement)?.value.trim();
  const clientSecret = (document.getElementById('azure-client-secret') as HTMLInputElement)?.value;
  const subscriptionId = (document.getElementById('azure-subscription-id') as HTMLInputElement)?.value.trim();

  if (!tenantId || !clientId || !clientSecret || !subscriptionId) {
    if (errorEl) {
      errorEl.textContent = 'All fields are required';
      errorEl.classList.remove('hidden');
    }
    return;
  }

  try {
    await api.saveAzureCredentials({
      tenant_id: tenantId,
      client_id: clientId,
      client_secret: clientSecret,
      subscription_id: subscriptionId
    });

    // Update status
    const statusEl = document.getElementById('azure-creds-status');
    if (statusEl) {
      statusEl.textContent = 'Configured';
      statusEl.classList.add('configured');
    }

    closeAzureCredsModal();
    alert('Azure credentials saved successfully');
  } catch (error) {
    console.error('Failed to save Azure credentials:', error);
    if (errorEl) {
      const err = error as Error;
      errorEl.textContent = `Failed to save: ${err.message}`;
      errorEl.classList.remove('hidden');
    }
  }
}

/**
 * Handle GCP credentials save
 */
async function handleGCPCredsSave(e: Event): Promise<void> {
  e.preventDefault();
  const errorEl = document.getElementById('gcp-creds-error');

  const jsonText = (document.getElementById('gcp-service-account-json') as HTMLTextAreaElement)?.value.trim();

  if (!jsonText) {
    if (errorEl) {
      errorEl.textContent = 'Service account JSON is required';
      errorEl.classList.remove('hidden');
    }
    return;
  }

  // Parse and validate JSON
  let credentials: api.GCPCredentials;
  try {
    credentials = JSON.parse(jsonText) as api.GCPCredentials;
  } catch {
    if (errorEl) {
      errorEl.textContent = 'Invalid JSON format';
      errorEl.classList.remove('hidden');
    }
    return;
  }

  // Validate required fields
  if (!credentials.type || !credentials.project_id || !credentials.private_key || !credentials.client_email) {
    if (errorEl) {
      errorEl.textContent = 'Missing required fields: type, project_id, private_key, client_email';
      errorEl.classList.remove('hidden');
    }
    return;
  }

  try {
    await api.saveGCPCredentials(credentials);

    // Update status
    const statusEl = document.getElementById('gcp-creds-status');
    if (statusEl) {
      statusEl.textContent = 'Configured';
      statusEl.classList.add('configured');
    }

    closeGCPCredsModal();
    alert('GCP credentials saved successfully');
  } catch (error) {
    console.error('Failed to save GCP credentials:', error);
    if (errorEl) {
      const err = error as Error;
      errorEl.textContent = `Failed to save: ${err.message}`;
      errorEl.classList.remove('hidden');
    }
  }
}

/**
 * Copy text from an element to clipboard
 */
export function copyToClipboard(elementId: string): void {
  const element = document.getElementById(elementId);
  if (!element) return;

  const text = element.textContent || '';
  navigator.clipboard.writeText(text).then(() => {
    // Show feedback
    const btn = element.nextElementSibling as HTMLButtonElement;
    if (btn) {
      const originalContent = btn.innerHTML;
      btn.innerHTML = '<span class="copy-icon">&#10003;</span>';
      btn.classList.add('copied');
      setTimeout(() => {
        btn.innerHTML = originalContent;
        btn.classList.remove('copied');
      }, 2000);
    }
  }).catch(err => {
    console.error('Failed to copy:', err);
  });
}
