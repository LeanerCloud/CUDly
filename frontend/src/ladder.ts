/**
 * Commitment Laddering settings section (issue #1333 phase 3).
 *
 * Renders the per-account ladder config editor inside the
 * #commitment-laddering-settings placeholder div that lives inside the
 * Purchasing Settings panel. The section is flag-gated default-off:
 *
 *   - Global kill-switch: global_config.laddering_enabled (bool)
 *   - Per-account: LadderConfig.enabled (bool) per (account, provider) pair
 *
 * No laddering engine runs fire until both flags are true. This PR (phase 3,
 * schema foundation) wires the UI to read and write configs; actual engine
 * invocation is in a later phase.
 */

import * as api from './api';
import { escapeHtml, formatDate } from './utils';
import { showToast } from './toast';
import { canAccess } from './permissions';

// ==========================================
// MODULE STATE
// ==========================================

// In-flight guard for the save action.
let saveInFlight = false;

// Cached configs from the last successful load.
let cachedConfigs: api.LadderConfig[] = [];

// ==========================================
// PUBLIC API
// ==========================================

/**
 * Initialize the Commitment Laddering settings section.
 *
 * Renders the HTML form into #commitment-laddering-settings and populates
 * it with data from the API. Should be called from loadGlobalSettings after
 * the global config response is available.
 *
 * The globalEnabled parameter is the current value of
 * global_config.laddering_enabled so the kill-switch toggle reflects the
 * persisted state without a second API call.
 */
export async function initLadderingSettings(globalEnabled: boolean): Promise<void> {
  const container = document.getElementById('commitment-laddering-settings');
  if (!container) return;

  container.innerHTML = renderLadderingSection(globalEnabled);
  wireKillSwitchToggle();
  wireModalCloseButtons();

  try {
    cachedConfigs = await api.getLadderConfigs();
    renderConfigTable(cachedConfigs);
  } catch (err) {
    console.error('Failed to load ladder configs:', err);
    const tableContainer = document.getElementById('ladder-configs-table-container');
    if (tableContainer) {
      tableContainer.innerHTML = '<p class="settings-error">Failed to load per-account configurations.</p>';
    }
  }

  if (canAccess('update', 'config')) {
    document.getElementById('ladder-add-config-btn')?.addEventListener('click', () => openLadderConfigModal());
  }
}

// ==========================================
// RENDERING
// ==========================================

function renderLadderingSection(globalEnabled: boolean): string {
  const canEdit = canAccess('update', 'config');
  const disabledAttr = canEdit ? '' : ' disabled';

  return `
<fieldset class="settings-category" id="commitment-laddering-automation-settings">
  <legend>Commitment Laddering</legend>
  <p class="settings-help">
    Commitment Laddering automatically ramps up cloud commitments towards a
    target coverage level using a configurable ramp schedule. It is feature-gated:
    enable the global kill-switch below, then enable individual accounts via the
    per-account configuration table.
  </p>

  <div class="setting-row">
    <div class="setting-info">
      <label for="setting-laddering-enabled">Enable Commitment Laddering</label>
      <span class="setting-hint">Global kill-switch. Must be on before any per-account config can fire.</span>
    </div>
    <div class="setting-input">
      <input type="checkbox" id="setting-laddering-enabled"
             ${globalEnabled ? 'checked' : ''}${disabledAttr}>
    </div>
  </div>

  <div id="ladder-configs-section">
    <h4>Per-Account Configurations</h4>
    <div id="ladder-configs-table-container">
      <p class="settings-help">Loading&hellip;</p>
    </div>
    ${canEdit ? `
    <button type="button" id="ladder-add-config-btn" class="btn btn-secondary">
      Add Account Config
    </button>` : ''}
  </div>
</fieldset>

<!-- Per-account ladder config modal -->
<div id="ladder-config-modal" class="modal hidden" role="dialog" aria-modal="true"
     aria-labelledby="ladder-modal-title">
  <div class="modal-content">
    <div class="modal-header">
      <h3 id="ladder-modal-title">Commitment Laddering Config</h3>
      <button type="button" class="modal-close" id="ladder-modal-close-btn"
              aria-label="Close">&times;</button>
    </div>
    <div class="modal-body">
      <form id="ladder-config-form" novalidate>
        <input type="hidden" id="ladder-cfg-id">

        <div class="form-row">
          <label for="ladder-cfg-account">Cloud Account ID</label>
          <input type="text" id="ladder-cfg-account" placeholder="UUID of the cloud account"
                 required autocomplete="off">
        </div>

        <div class="form-row">
          <label for="ladder-cfg-provider">Provider</label>
          <select id="ladder-cfg-provider">
            <option value="aws">AWS</option>
            <option value="azure">Azure</option>
            <option value="gcp">GCP</option>
          </select>
        </div>

        <div class="form-row">
          <label class="form-checkbox-label">
            <input type="checkbox" id="ladder-cfg-enabled">
            Enabled for this account
          </label>
        </div>

        <div class="form-row">
          <label for="ladder-cfg-mode">Approval Mode</label>
          <select id="ladder-cfg-mode">
            <option value="email_approval">Email Approval</option>
            <option value="auto_approve">Auto Approve</option>
          </select>
        </div>

        <div class="form-row">
          <label for="ladder-cfg-cadence">Cadence</label>
          <select id="ladder-cfg-cadence">
            <option value="daily">Daily</option>
            <option value="weekly">Weekly</option>
          </select>
        </div>

        <div class="form-row">
          <label for="ladder-cfg-target-coverage">Target Coverage (%)</label>
          <input type="number" id="ladder-cfg-target-coverage"
                 value="100" min="1" max="100" step="0.01">
        </div>

        <div class="form-row">
          <label for="ladder-cfg-buffer-fraction">Buffer Fraction (0&ndash;1)</label>
          <input type="number" id="ladder-cfg-buffer-fraction"
                 value="0.10" min="0" max="0.99" step="0.01">
        </div>

        <div class="form-row">
          <label for="ladder-cfg-baseline-percentile">Baseline Percentile (1&ndash;50)</label>
          <input type="number" id="ladder-cfg-baseline-percentile"
                 value="5" min="1" max="50" step="0.1">
        </div>

        <div class="form-row">
          <label for="ladder-cfg-lookback-days">Lookback Days</label>
          <input type="number" id="ladder-cfg-lookback-days"
                 value="30" min="1" step="1">
        </div>

        <div class="form-row">
          <label for="ladder-cfg-buf-util-threshold">Buffer Utilization Threshold (%)</label>
          <input type="number" id="ladder-cfg-buf-util-threshold"
                 value="90" min="1" max="100" step="0.1">
        </div>

        <div class="form-row">
          <label for="ladder-cfg-max-hourly">Max Hourly Commit Per Run ($/hr, blank = no cap)</label>
          <input type="number" id="ladder-cfg-max-hourly"
                 placeholder="No cap" min="0.000001" step="any">
        </div>

        <div class="form-row">
          <label for="ladder-cfg-max-actions">Max Actions Per Run (1&ndash;50)</label>
          <input type="number" id="ladder-cfg-max-actions"
                 value="10" min="1" max="50" step="1">
        </div>

        <div class="form-row">
          <label for="ladder-cfg-ramp-schedule">Ramp Schedule (JSON)</label>
          <textarea id="ladder-cfg-ramp-schedule" rows="4"
                    placeholder='{"steps":[{"after_days":0,"fraction":1.0}]}'></textarea>
          <span class="form-hint">
            An array of steps. Each step has <code>after_days</code> (integer) and
            <code>fraction</code> (0&ndash;1). Fractions must sum to 1.0.
            Example: immediate &mdash; <code>{"steps":[{"after_days":0,"fraction":1.0}]}</code>
          </span>
        </div>

        <div class="modal-footer">
          <button type="button" class="btn btn-secondary" id="ladder-modal-cancel-btn">
            Cancel
          </button>
          <button type="submit" class="btn btn-primary" id="ladder-config-save-btn">
            Save Config
          </button>
        </div>
      </form>
    </div>
  </div>
</div>
`;
}

// Exported for unit testing (mirrors the apikeys.ts convention of exposing
// render/action helpers so tests can drive them directly).
export function renderConfigTable(configs: api.LadderConfig[]): void {
  const container = document.getElementById('ladder-configs-table-container');
  if (!container) return;

  if (configs.length === 0) {
    container.innerHTML = '<p class="settings-help">No per-account configurations yet. Click <strong>Add Account Config</strong> to create one.</p>';
    return;
  }

  const canEdit = canAccess('update', 'config');
  const rows = configs.map(cfg => `
    <tr>
      <td>${escapeHtml(cfg.cloud_account_id)}</td>
      <td>${escapeHtml(cfg.provider.toUpperCase())}</td>
      <td>${cfg.enabled ? 'Yes' : 'No'}</td>
      <td>${escapeHtml(cfg.mode === 'email_approval' ? 'Email Approval' : 'Auto Approve')}</td>
      <td>${escapeHtml(cfg.cadence === 'daily' ? 'Daily' : 'Weekly')}</td>
      <td>${cfg.target_coverage.toFixed(1)}%</td>
      <td>${cfg.updated_at ? escapeHtml(formatDate(cfg.updated_at)) : 'N/A'}</td>
      ${canEdit
        ? `<td><button type="button" class="btn btn-sm btn-secondary ladder-edit-btn"
                       data-account-id="${escapeHtml(cfg.cloud_account_id)}"
                       data-provider="${escapeHtml(cfg.provider)}">Edit</button></td>`
        : '<td></td>'}
    </tr>
  `).join('');

  container.innerHTML = `
    <table class="data-table" aria-label="Per-account ladder configurations">
      <thead>
        <tr>
          <th>Account ID</th>
          <th>Provider</th>
          <th>Enabled</th>
          <th>Mode</th>
          <th>Cadence</th>
          <th>Target Coverage</th>
          <th>Updated</th>
          <th></th>
        </tr>
      </thead>
      <tbody>${rows}</tbody>
    </table>
  `;

  // Wire edit buttons via querySelectorAll so no user data is ever injected
  // into a JS string context (data-* attributes are HTML-attribute-context only).
  container.querySelectorAll<HTMLButtonElement>('.ladder-edit-btn').forEach(btn => {
    btn.addEventListener('click', () => {
      const accountID = btn.dataset['accountId'] ?? '';
      const provider = btn.dataset['provider'] ?? '';
      const cfg = cachedConfigs.find(c => c.cloud_account_id === accountID && c.provider === provider);
      if (cfg) openLadderConfigModal(cfg);
    });
  });
}

// ==========================================
// MODAL CLOSE WIRING
// ==========================================

// closeLadderModal hides the per-account config modal.
function closeLadderModal(): void {
  document.getElementById('ladder-config-modal')?.classList.add('hidden');
}

// wireModalCloseButtons attaches click listeners to the modal's × and Cancel
// buttons. Uses addEventListener rather than inline onclick for CSP
// consistency with the dynamically-wired edit buttons (no inline handlers
// anywhere in this module).
function wireModalCloseButtons(): void {
  document.getElementById('ladder-modal-close-btn')?.addEventListener('click', closeLadderModal);
  document.getElementById('ladder-modal-cancel-btn')?.addEventListener('click', closeLadderModal);
}

// ==========================================
// KILL-SWITCH TOGGLE
// ==========================================

function wireKillSwitchToggle(): void {
  const toggle = document.getElementById('setting-laddering-enabled') as HTMLInputElement | null;
  if (!toggle || !canAccess('update', 'config')) return;

  toggle.addEventListener('change', async () => {
    if (saveInFlight) {
      toggle.checked = !toggle.checked; // revert optimistic
      return;
    }
    saveInFlight = true;
    try {
      const globalCfg = await fetchGlobalConfigForLaddering();
      if (globalCfg === null) {
        toggle.checked = !toggle.checked;
        return;
      }
      globalCfg.laddering_enabled = toggle.checked;
      await api.updateConfig(globalCfg);
      showToast({
        message: `Commitment Laddering ${toggle.checked ? 'enabled' : 'disabled'} globally.`,
        kind: 'success',
      });
    } catch (err) {
      toggle.checked = !toggle.checked; // revert on error
      showToast({ message: 'Failed to update laddering kill-switch.', kind: 'error' });
      console.error('Failed to toggle laddering_enabled:', err);
    } finally {
      saveInFlight = false;
    }
  });
}

/**
 * Fetches the global config for the kill-switch toggle so we can merge the
 * single field change without overwriting other fields. Returns null on error
 * (error is shown via toast before returning).
 */
async function fetchGlobalConfigForLaddering(): Promise<api.Config | null> {
  try {
    const resp = await api.getConfig();
    const g = resp.global;
    if (!g) {
      showToast({ message: 'Could not load global config.', kind: 'error' });
      return null;
    }
    // Build a Config-shaped payload from GlobalConfig fields that are safe to
    // round-trip (the union of GlobalConfig and Config). We pass only the
    // fields the backend's updateConfig handler accepts.
    //
    // IMPORTANT: include every field the backend stores, not just the one we
    // are changing. GlobalConfig.NotificationEmail is a *string in Go: omitting
    // it from the PUT body decodes as nil and zeroes it out in the DB.
    return {
      enabled_providers: (g.enabled_providers ?? []) as api.Provider[],
      notification_email: g.notification_email,
      auto_collect: g.auto_collect ?? true,
      collection_schedule: g.collection_schedule ?? 'daily',
      notification_days_before: g.notification_days_before ?? 3,
      default_term: g.default_term ?? 3,
      default_payment: (g.default_payment ?? 'all-upfront') as api.PaymentOption,
      default_coverage: g.default_coverage ?? 80,
      grace_period_days: g.grace_period_days,
      recommendations_cache_stale_hours: g.recommendations_cache_stale_hours,
      recommendations_lookback_days: g.recommendations_lookback_days,
      laddering_enabled: g.laddering_enabled ?? false,
    };
  } catch (err) {
    showToast({ message: 'Failed to load global config.', kind: 'error' });
    console.error('fetchGlobalConfigForLaddering:', err);
    return null;
  }
}

// ==========================================
// PER-ACCOUNT CONFIG MODAL
// ==========================================

function openLadderConfigModal(existing?: api.LadderConfig): void {
  const modal = document.getElementById('ladder-config-modal');
  if (!modal) return;

  // Populate modal fields.
  setValue('ladder-cfg-id', existing?.id ?? '');
  setValue('ladder-cfg-account', existing?.cloud_account_id ?? '');
  setSelectValue('ladder-cfg-provider', existing?.provider ?? 'aws');
  setChecked('ladder-cfg-enabled', existing?.enabled ?? false);
  setSelectValue('ladder-cfg-mode', existing?.mode ?? 'email_approval');
  setSelectValue('ladder-cfg-cadence', existing?.cadence ?? 'daily');
  setValue('ladder-cfg-target-coverage', String(existing?.target_coverage ?? 100));
  setValue('ladder-cfg-buffer-fraction', String(existing?.buffer_fraction ?? 0.10));
  setValue('ladder-cfg-baseline-percentile', String(existing?.baseline_percentile ?? 5.0));
  setValue('ladder-cfg-lookback-days', String(existing?.lookback_days ?? 30));
  setValue('ladder-cfg-buf-util-threshold', String(existing?.buffer_utilization_threshold ?? 90));
  setValue('ladder-cfg-max-hourly', existing?.max_hourly_commit_per_run != null
    ? String(existing.max_hourly_commit_per_run)
    : '');
  setValue('ladder-cfg-max-actions', String(existing?.max_actions_per_run ?? 10));

  const defaultRamp = JSON.stringify({ steps: [{ after_days: 0, fraction: 1.0 }] }, null, 2);
  setValue('ladder-cfg-ramp-schedule',
    existing?.ramp_schedule ? JSON.stringify(existing.ramp_schedule, null, 2) : defaultRamp);

  // Disable account/provider fields when editing an existing row (they form
  // the upsert key and cannot be changed without a delete+re-create).
  const accountInput = document.getElementById('ladder-cfg-account') as HTMLInputElement | null;
  const providerSelect = document.getElementById('ladder-cfg-provider') as HTMLSelectElement | null;
  if (accountInput) accountInput.readOnly = !!existing;
  if (providerSelect) providerSelect.disabled = !!existing;

  // Wire the save button (remove previous listener by replacing the element
  // clone so duplicate-listener accumulation cannot occur).
  const form = document.getElementById('ladder-config-form');
  if (form) {
    const newForm = form.cloneNode(true) as HTMLElement;
    form.replaceWith(newForm);
    newForm.addEventListener('submit', (e) => {
      e.preventDefault();
      saveLadderConfig();
    });
    // The Cancel button lives inside the form, so cloneNode drops its
    // listener (cloneNode does not copy event handlers). Re-wire it on the
    // fresh node. The × close button sits outside the form and keeps the
    // listener attached once in wireModalCloseButtons.
    newForm.querySelector('#ladder-modal-cancel-btn')
      ?.addEventListener('click', closeLadderModal);
  }

  modal.classList.remove('hidden');
}

// Exported for unit testing (see renderConfigTable note).
export async function saveLadderConfig(): Promise<void> {
  if (saveInFlight) return;

  const maxHourlyRaw = (document.getElementById('ladder-cfg-max-hourly') as HTMLInputElement | null)?.value?.trim() ?? '';
  const maxHourly: number | null = maxHourlyRaw === '' ? null : Number(maxHourlyRaw);
  if (maxHourlyRaw !== '' && (!Number.isFinite(maxHourly) || (maxHourly as number) <= 0)) {
    showToast({ message: 'Max hourly commit must be a positive number (or blank for no cap).', kind: 'error' });
    return;
  }

  const rampRaw = (document.getElementById('ladder-cfg-ramp-schedule') as HTMLTextAreaElement | null)?.value?.trim() ?? '';
  let rampSchedule: api.LadderConfig['ramp_schedule'];
  try {
    rampSchedule = JSON.parse(rampRaw);
  } catch {
    showToast({ message: 'Ramp schedule is not valid JSON.', kind: 'error' });
    return;
  }

  const cfg: api.LadderConfig = {
    id: (document.getElementById('ladder-cfg-id') as HTMLInputElement | null)?.value || undefined,
    cloud_account_id: (document.getElementById('ladder-cfg-account') as HTMLInputElement | null)?.value?.trim() ?? '',
    provider: (document.getElementById('ladder-cfg-provider') as HTMLSelectElement | null)?.value ?? 'aws',
    enabled: (document.getElementById('ladder-cfg-enabled') as HTMLInputElement | null)?.checked ?? false,
    mode: ((document.getElementById('ladder-cfg-mode') as HTMLSelectElement | null)?.value ?? 'email_approval') as api.LadderConfig['mode'],
    cadence: ((document.getElementById('ladder-cfg-cadence') as HTMLSelectElement | null)?.value ?? 'daily') as api.LadderConfig['cadence'],
    target_coverage: Number((document.getElementById('ladder-cfg-target-coverage') as HTMLInputElement | null)?.value ?? '100'),
    buffer_fraction: Number((document.getElementById('ladder-cfg-buffer-fraction') as HTMLInputElement | null)?.value ?? '0.10'),
    baseline_percentile: Number((document.getElementById('ladder-cfg-baseline-percentile') as HTMLInputElement | null)?.value ?? '5'),
    lookback_days: Number((document.getElementById('ladder-cfg-lookback-days') as HTMLInputElement | null)?.value ?? '30'),
    buffer_utilization_threshold: Number((document.getElementById('ladder-cfg-buf-util-threshold') as HTMLInputElement | null)?.value ?? '90'),
    max_hourly_commit_per_run: maxHourly,
    max_actions_per_run: Number((document.getElementById('ladder-cfg-max-actions') as HTMLInputElement | null)?.value ?? '10'),
    ramp_schedule: rampSchedule,
  };

  if (!cfg.cloud_account_id) {
    showToast({ message: 'Cloud Account ID is required.', kind: 'error' });
    return;
  }

  saveInFlight = true;
  const saveBtn = document.getElementById('ladder-config-save-btn') as HTMLButtonElement | null;
  if (saveBtn) saveBtn.disabled = true;

  try {
    const saved = await api.upsertLadderConfig(cfg);

    // Update the in-memory cache and re-render the table.
    const idx = cachedConfigs.findIndex(
      c => c.cloud_account_id === saved.cloud_account_id && c.provider === saved.provider
    );
    if (idx >= 0) {
      cachedConfigs[idx] = saved;
    } else {
      cachedConfigs.push(saved);
    }
    renderConfigTable(cachedConfigs);

    closeLadderModal();
    showToast({ message: 'Ladder config saved.', kind: 'success' });
  } catch (err) {
    showToast({ message: 'Failed to save ladder config. Check inputs and try again.', kind: 'error' });
    console.error('saveLadderConfig:', err);
  } finally {
    saveInFlight = false;
    if (saveBtn) saveBtn.disabled = false;
  }
}

// ==========================================
// DOM HELPERS
// ==========================================

function setValue(id: string, value: string): void {
  const el = document.getElementById(id) as HTMLInputElement | HTMLTextAreaElement | null;
  if (el) el.value = value;
}

function setSelectValue(id: string, value: string): void {
  const el = document.getElementById(id) as HTMLSelectElement | null;
  if (el) el.value = value;
}

function setChecked(id: string, checked: boolean): void {
  const el = document.getElementById(id) as HTMLInputElement | null;
  if (el) el.checked = checked;
}
