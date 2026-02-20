/**
 * API Keys Management module for CUDly
 */

import * as api from './api';
import type { APIKeyInfo, CreateAPIKeyResponse } from './types';

// State for modal management
let currentApiKeys: APIKeyInfo[] = [];

/**
 * Load and display API keys
 */
export async function loadApiKeys(): Promise<void> {
  try {
    const response = await api.getApiKeys();
    currentApiKeys = response.keys;
    renderApiKeysList();
  } catch (error) {
    console.error('Failed to load API keys:', error);
    showError('Failed to load API keys');
  }
}

/**
 * Render API keys list
 */
export function renderApiKeysList(): void {
  const container = document.getElementById('apikeys-list');
  if (!container) return;

  if (currentApiKeys.length === 0) {
    container.innerHTML = '<div class="empty">No API keys found. Create one to get started.</div>';
    return;
  }

  const table = `
    <table class="data-table">
      <thead>
        <tr>
          <th>Name</th>
          <th>Key Prefix</th>
          <th>Status</th>
          <th>Created</th>
          <th>Last Used</th>
          <th>Expires</th>
          <th>Actions</th>
        </tr>
      </thead>
      <tbody>
        ${currentApiKeys.map(key => {
          const isExpired = key.expires_at && new Date(key.expires_at) < new Date();
          const statusClass = !key.is_active ? 'badge-danger' : isExpired ? 'badge-warning' : 'badge-success';
          const statusText = !key.is_active ? 'Revoked' : isExpired ? 'Expired' : 'Active';

          return `
            <tr>
              <td><strong>${escapeHtml(key.name)}</strong></td>
              <td><code>${escapeHtml(key.key_prefix)}...</code></td>
              <td><span class="badge ${statusClass}">${statusText}</span></td>
              <td>${formatDate(key.created_at)}</td>
              <td>${key.last_used_at ? formatDate(key.last_used_at) : '<span class="text-muted">Never</span>'}</td>
              <td>${key.expires_at ? formatDate(key.expires_at) : '<span class="text-muted">Never</span>'}</td>
              <td>
                ${key.is_active && !isExpired ? `<button class="btn-small btn-warning revoke-key-btn" data-key-id="${escapeHtml(key.id)}">Revoke</button>` : ''}
                <button class="btn-small btn-danger delete-key-btn" data-key-id="${escapeHtml(key.id)}">Delete</button>
              </td>
            </tr>
          `;
        }).join('')}
      </tbody>
    </table>
  `;

  container.innerHTML = table;

  // Add event delegation after rendering
  container.querySelectorAll('.revoke-key-btn').forEach(btn => {
    btn.addEventListener('click', () => {
      const keyId = (btn as HTMLElement).dataset.keyId;
      if (keyId) void revokeApiKey(keyId);
    });
  });
  container.querySelectorAll('.delete-key-btn').forEach(btn => {
    btn.addEventListener('click', () => {
      const keyId = (btn as HTMLElement).dataset.keyId;
      if (keyId) void deleteApiKey(keyId);
    });
  });
}

/**
 * Show create API key modal
 */
export function showCreateKeyModal(): void {
  const modal = document.getElementById('create-apikey-modal');
  const form = document.getElementById('create-apikey-form') as HTMLFormElement;
  const errorEl = document.getElementById('create-apikey-error');

  if (!modal || !form) return;

  form.reset();
  if (errorEl) errorEl.classList.add('hidden');

  // Reset expiration checkbox and field visibility
  const expiresCheckbox = document.getElementById('apikey-expires') as HTMLInputElement;
  const expiresAtField = document.getElementById('apikey-expires-at-field');
  if (expiresCheckbox) expiresCheckbox.checked = false;
  if (expiresAtField) expiresAtField.classList.add('hidden');

  modal.classList.remove('hidden');
}

/**
 * Close create API key modal
 */
export function closeCreateKeyModal(): void {
  const modal = document.getElementById('create-apikey-modal');
  if (modal) modal.classList.add('hidden');
}

/**
 * Create new API key
 */
export async function createApiKey(name: string, permissions?: api.Permission[], expiresAt?: Date): Promise<CreateAPIKeyResponse> {
  try {
    const request: api.CreateAPIKeyRequest = { name };

    if (permissions && permissions.length > 0) {
      request.permissions = permissions;
    }

    if (expiresAt) {
      request.expires_at = expiresAt.toISOString();
    }

    const response = await api.createApiKey(request);
    return response;
  } catch (error) {
    console.error('Failed to create API key:', error);
    throw error;
  }
}

/**
 * Handle create API key form submission
 */
export async function handleCreateApiKey(e: Event): Promise<void> {
  e.preventDefault();

  const errorEl = document.getElementById('create-apikey-error');
  if (errorEl) errorEl.classList.add('hidden');

  const name = (document.getElementById('apikey-name') as HTMLInputElement).value.trim();
  const expiresCheckbox = (document.getElementById('apikey-expires') as HTMLInputElement).checked;
  const expiresAtInput = (document.getElementById('apikey-expires-at') as HTMLInputElement).value;

  if (!name) {
    showError('API key name is required');
    return;
  }

  let expiresAt: Date | undefined;
  if (expiresCheckbox && expiresAtInput) {
    expiresAt = new Date(expiresAtInput);
    if (expiresAt <= new Date()) {
      showError('Expiration date must be in the future');
      return;
    }
  }

  try {
    const response = await createApiKey(name, undefined, expiresAt);
    closeCreateKeyModal();
    showKeyCreatedModal(response.api_key);
    await loadApiKeys();
  } catch (error) {
    const err = error as Error;
    showError(`Failed to create API key: ${err.message}`);
  }
}

/**
 * Show key created modal with one-time display
 */
export function showKeyCreatedModal(apiKey: string): void {
  // Remove any existing modal to prevent duplicates
  document.getElementById('apikey-created-modal')?.remove();

  const modal = document.createElement('div');
  modal.id = 'apikey-created-modal';
  modal.className = 'modal';
  modal.innerHTML = `
    <div class="modal-content">
      <h2>API Key Created Successfully</h2>
      <div class="warning-box">
        <strong>Important:</strong> This is the only time you'll see this API key.
        Please copy it now and store it securely.
      </div>
      <div class="apikey-display">
        <label>Your API Key:</label>
        <div class="apikey-value-container">
          <code id="apikey-value" class="apikey-value">${escapeHtml(apiKey)}</code>
          <button type="button" id="copy-apikey-btn" class="btn-small primary">Copy</button>
        </div>
      </div>
      <div class="modal-info">
        <p>Use this key in the <code>X-API-Key</code> header when making API requests:</p>
        <pre class="code-example">curl -H "X-API-Key: ${escapeHtml(apiKey)}" https://your-api-endpoint.com/api/...</pre>
      </div>
      <div class="modal-buttons">
        <button type="button" id="close-apikey-created-btn" class="primary">I've Copied the Key</button>
      </div>
    </div>
  `;

  document.body.appendChild(modal);
  modal.classList.remove('hidden');

  // Setup copy button
  const copyBtn = document.getElementById('copy-apikey-btn');
  if (copyBtn) {
    copyBtn.addEventListener('click', () => {
      navigator.clipboard.writeText(apiKey).then(() => {
        copyBtn.textContent = 'Copied!';
        copyBtn.classList.add('copied');
        setTimeout(() => {
          copyBtn.textContent = 'Copy';
          copyBtn.classList.remove('copied');
        }, 2000);
      }).catch(err => {
        console.error('Failed to copy:', err);
        alert('Failed to copy to clipboard. Please copy manually.');
      });
    });
  }

  // Setup close button
  const closeBtn = document.getElementById('close-apikey-created-btn');
  if (closeBtn) {
    closeBtn.addEventListener('click', () => {
      modal.remove();
    });
  }
}

/**
 * Revoke an API key
 */
export async function revokeApiKey(keyId: string): Promise<void> {
  const key = currentApiKeys.find(k => k.id === keyId);
  if (!key) return;

  if (!confirm(`Are you sure you want to revoke the API key "${key.name}"? This action cannot be undone and the key will immediately stop working.`)) {
    return;
  }

  try {
    await api.revokeApiKey(keyId);
    await loadApiKeys();
  } catch (error) {
    console.error('Failed to revoke API key:', error);
    showError('Failed to revoke API key');
  }
}

/**
 * Delete an API key
 */
export async function deleteApiKey(keyId: string): Promise<void> {
  const key = currentApiKeys.find(k => k.id === keyId);
  if (!key) return;

  if (!confirm(`Are you sure you want to delete the API key "${key.name}"? This action cannot be undone.`)) {
    return;
  }

  try {
    await api.deleteApiKey(keyId);
    await loadApiKeys();
  } catch (error) {
    console.error('Failed to delete API key:', error);
    showError('Failed to delete API key');
  }
}

/**
 * Initialize API keys management
 */
export function initApiKeys(): void {
  // Setup create key button
  const createKeyBtn = document.getElementById('create-apikey-btn');
  if (createKeyBtn) {
    createKeyBtn.addEventListener('click', () => showCreateKeyModal());
  }

  // Setup close modal button
  const closeModalBtn = document.getElementById('close-create-apikey-modal-btn');
  if (closeModalBtn) {
    closeModalBtn.addEventListener('click', () => closeCreateKeyModal());
  }

  // Setup form submission
  const form = document.getElementById('create-apikey-form');
  if (form) {
    form.addEventListener('submit', (e) => void handleCreateApiKey(e));
  }

  // Setup expires checkbox toggle
  const expiresCheckbox = document.getElementById('apikey-expires') as HTMLInputElement;
  const expiresAtField = document.getElementById('apikey-expires-at-field');
  if (expiresCheckbox && expiresAtField) {
    expiresCheckbox.addEventListener('change', () => {
      expiresAtField.classList.toggle('hidden', !expiresCheckbox.checked);
      const expiresAtInput = document.getElementById('apikey-expires-at') as HTMLInputElement;
      if (expiresAtInput) {
        expiresAtInput.required = expiresCheckbox.checked;
        // Set default to 90 days from now
        if (expiresCheckbox.checked && !expiresAtInput.value) {
          const defaultDate = new Date();
          defaultDate.setDate(defaultDate.getDate() + 90);
          expiresAtInput.value = defaultDate.toISOString().split('T')[0] || "";
        }
      }
    });
  }

  // Close modal when clicking outside
  const modal = document.getElementById('create-apikey-modal');
  if (modal) {
    modal.addEventListener('click', (e) => {
      if (e.target === modal) closeCreateKeyModal();
    });
  }
}

/**
 * Show error message
 */
function showError(message: string): void {
  const errorEl = document.getElementById('create-apikey-error');
  if (errorEl) {
    errorEl.textContent = message;
    errorEl.classList.remove('hidden');
  } else {
    alert(message);
  }
}

/**
 * Format date for display
 */
function formatDate(dateString: string): string {
  const date = new Date(dateString);
  return date.toLocaleDateString() + ' ' + date.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });
}

/**
 * Escape HTML to prevent XSS
 */
function escapeHtml(text: string): string {
  const div = document.createElement('div');
  div.textContent = text;
  return div.innerHTML;
}
