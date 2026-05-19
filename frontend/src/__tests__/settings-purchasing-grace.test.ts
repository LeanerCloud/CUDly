/**
 * Settings → Purchasing: per-provider grace-period inputs.
 *
 * Covers the new AWS/Azure/GCP grace-period fields added in Commit 1 of
 * the bulk-purchase-with-grace plan:
 *   - Load renders configured values (including explicit 0) + default 7
 *     for absent keys.
 *   - Save pushes a correctly-shaped `grace_period_days` map to the API.
 *   - Out-of-range input (< 0, > 30, non-integer) is rejected with a
 *     targeted toast instead of silently succeeding or being clamped.
 *   - Reset-to-defaults restores every input to 7.
 */

import { loadGlobalSettings, saveGlobalSettings, resetSettings } from '../settings';

jest.mock('../api', () => ({
  getConfig: jest.fn(),
  updateConfig: jest.fn(),
  updateServiceConfig: jest.fn().mockResolvedValue(undefined),
}));
jest.mock('../federation', () => ({
  initFederationPanel: jest.fn().mockResolvedValue(undefined),
}));
const mockConfirmDialog = jest.fn<Promise<boolean>, [unknown]>(() => Promise.resolve(true));
jest.mock('../confirmDialog', () => ({
  confirmDialog: (opts: unknown) => mockConfirmDialog(opts),
}));
const mockShowToast = jest.fn<{ dismiss: () => void }, [unknown]>(() => ({ dismiss: jest.fn() }));
jest.mock('../toast', () => ({
  showToast: (opts: unknown) => mockShowToast(opts),
}));

import * as api from '../api';

function addInput(id: string, type: string, value: string, parent: HTMLElement, attrs: Record<string, string> = {}): HTMLInputElement {
  const el = document.createElement('input');
  el.id = id;
  el.type = type;
  el.value = value;
  for (const [k, v] of Object.entries(attrs)) el.setAttribute(k, v);
  parent.appendChild(el);
  return el;
}

function addSelect(id: string, options: string[], selected: string, parent: HTMLElement): HTMLSelectElement {
  const el = document.createElement('select');
  el.id = id;
  for (const v of options) {
    const opt = document.createElement('option');
    opt.value = v;
    opt.textContent = v;
    if (v === selected) opt.selected = true;
    el.appendChild(opt);
  }
  parent.appendChild(el);
  return el;
}

function seedDOM(): void {
  while (document.body.firstChild) document.body.removeChild(document.body.firstChild);
  const loading = document.createElement('div');
  loading.id = 'settings-loading';
  loading.className = 'hidden';
  document.body.appendChild(loading);

  const form = document.createElement('form');
  form.id = 'global-settings-form';
  form.className = 'hidden';
  document.body.appendChild(form);

  addInput('provider-aws', 'checkbox', '', form, { checked: 'checked' });
  (document.getElementById('provider-aws') as HTMLInputElement).checked = true;
  addInput('provider-azure', 'checkbox', '', form);
  addInput('provider-gcp', 'checkbox', '', form);
  addInput('setting-notification-email', 'email', '', form);
  const auto = addInput('setting-auto-collect', 'checkbox', '', form);
  auto.checked = true;
  addSelect('setting-collection-schedule', ['daily'], 'daily', form);
  addSelect('setting-default-term', ['1', '3'], '3', form);
  addSelect('setting-default-payment', ['no-upfront', 'all-upfront'], 'all-upfront', form);
  addInput('setting-default-coverage', 'number', '80', form);
  addInput('setting-notification-days', 'number', '3', form);
  addInput('setting-grace-aws', 'number', '7', form, { min: '0', max: '30' });
  addInput('setting-grace-azure', 'number', '7', form, { min: '0', max: '30' });
  addInput('setting-grace-gcp', 'number', '7', form, { min: '0', max: '30' });

  const errDiv = document.createElement('div');
  errDiv.id = 'settings-error';
  errDiv.className = 'hidden';
  document.body.appendChild(errDiv);
}

describe('Settings → Purchasing grace-period inputs', () => {
  beforeEach(() => {
    jest.clearAllMocks();
    seedDOM();
    (api.updateConfig as jest.Mock).mockResolvedValue(undefined);
  });

  it('loads configured grace-period values (explicit 0 preserved)', async () => {
    (api.getConfig as jest.Mock).mockResolvedValue({
      global: {
        enabled_providers: ['aws'],
        default_term: 3,
        default_payment: 'all-upfront',
        default_coverage: 80,
        notification_days_before: 3,
        grace_period_days: { aws: 14, azure: 0, gcp: 7 },
      },
      services: [],
    });

    await loadGlobalSettings();

    expect((document.getElementById('setting-grace-aws') as HTMLInputElement).value).toBe('14');
    expect((document.getElementById('setting-grace-azure') as HTMLInputElement).value).toBe('0');
    expect((document.getElementById('setting-grace-gcp') as HTMLInputElement).value).toBe('7');
  });

  it('loads default 7 for absent keys', async () => {
    (api.getConfig as jest.Mock).mockResolvedValue({
      global: {
        enabled_providers: ['aws'],
        default_term: 3,
        default_payment: 'all-upfront',
        default_coverage: 80,
        notification_days_before: 3,
      },
      services: [],
    });

    await loadGlobalSettings();

    expect((document.getElementById('setting-grace-aws') as HTMLInputElement).value).toBe('7');
    expect((document.getElementById('setting-grace-azure') as HTMLInputElement).value).toBe('7');
    expect((document.getElementById('setting-grace-gcp') as HTMLInputElement).value).toBe('7');
  });

  it('save includes grace_period_days in the updateConfig payload', async () => {
    (document.getElementById('setting-grace-aws') as HTMLInputElement).value = '10';
    (document.getElementById('setting-grace-azure') as HTMLInputElement).value = '0';
    (document.getElementById('setting-grace-gcp') as HTMLInputElement).value = '21';

    await saveGlobalSettings(new Event("submit"));

    expect(api.updateConfig).toHaveBeenCalledTimes(1);
    const payload = (api.updateConfig as jest.Mock).mock.calls[0]![0];
    expect(payload.grace_period_days).toEqual({ aws: 10, azure: 0, gcp: 21 });
  });

  it('rejects out-of-range input (> 30) with a targeted toast', async () => {
    (document.getElementById('setting-grace-aws') as HTMLInputElement).value = '31';

    await saveGlobalSettings(new Event("submit"));

    expect(api.updateConfig).not.toHaveBeenCalled();
    expect(mockShowToast).toHaveBeenCalled();
    const toastArg = mockShowToast.mock.calls[0]![0] as { message: string; kind: string };
    // Issue #478: validation message follows the aggregated format
    // and names every affected provider. Single-provider failure still
    // includes the provider name in parentheses.
    expect(toastArg.message).toMatch(/between 0 and 30/i);
    expect(toastArg.message).toMatch(/\(AWS\)/);
    expect(toastArg.kind).toBe('error');
  });

  it('rejects non-integer input with a targeted toast', async () => {
    (document.getElementById('setting-grace-azure') as HTMLInputElement).value = '7.5';

    await saveGlobalSettings(new Event("submit"));

    expect(api.updateConfig).not.toHaveBeenCalled();
    expect(mockShowToast).toHaveBeenCalled();
    const toastArg = mockShowToast.mock.calls[0]![0] as { message: string };
    expect(toastArg.message).toMatch(/whole number/i);
    expect(toastArg.message).toMatch(/\(AZURE\)/);
  });

  it('rejects negative input with a targeted toast', async () => {
    (document.getElementById('setting-grace-gcp') as HTMLInputElement).value = '-1';

    await saveGlobalSettings(new Event("submit"));

    expect(api.updateConfig).not.toHaveBeenCalled();
    expect(mockShowToast).toHaveBeenCalled();
    const toastArg = mockShowToast.mock.calls[0]![0] as { message: string };
    expect(toastArg.message).toMatch(/\(GCP\)/);
  });

  // Issue #478: when multiple providers have out-of-range values,
  // surface a single toast that names every affected provider rather
  // than bailing on the first failure.
  it('aggregates every out-of-range provider in a single toast (#478)', async () => {
    (document.getElementById('setting-grace-aws') as HTMLInputElement).value = '-1';
    (document.getElementById('setting-grace-azure') as HTMLInputElement).value = '99';
    (document.getElementById('setting-grace-gcp') as HTMLInputElement).value = '7.5';

    await saveGlobalSettings(new Event("submit"));

    expect(api.updateConfig).not.toHaveBeenCalled();
    // Exactly one toast for the validation failure — not three.
    const errorToasts = mockShowToast.mock.calls.filter(c => {
      const arg = c[0] as { kind?: string };
      return arg.kind === 'error';
    });
    expect(errorToasts).toHaveLength(1);
    const toastArg = errorToasts[0]![0] as { message: string };
    // All three providers named, in input order.
    expect(toastArg.message).toMatch(/AWS, AZURE, GCP/);
    expect(toastArg.message).toMatch(/whole number between 0 and 30/i);
  });

  it('aggregates two affected providers (AWS + GCP, Azure valid) (#478)', async () => {
    (document.getElementById('setting-grace-aws') as HTMLInputElement).value = '-5';
    (document.getElementById('setting-grace-azure') as HTMLInputElement).value = '7';
    (document.getElementById('setting-grace-gcp') as HTMLInputElement).value = '50';

    await saveGlobalSettings(new Event("submit"));

    expect(api.updateConfig).not.toHaveBeenCalled();
    const errorToasts = mockShowToast.mock.calls.filter(c => {
      const arg = c[0] as { kind?: string };
      return arg.kind === 'error';
    });
    expect(errorToasts).toHaveLength(1);
    const toastArg = errorToasts[0]![0] as { message: string };
    // AWS and GCP listed, Azure absent.
    expect(toastArg.message).toMatch(/AWS, GCP/);
    expect(toastArg.message).not.toMatch(/AZURE/);
  });

  it('empty input defaults to 7 in the save payload', async () => {
    (document.getElementById('setting-grace-aws') as HTMLInputElement).value = '';
    (document.getElementById('setting-grace-azure') as HTMLInputElement).value = '0';
    (document.getElementById('setting-grace-gcp') as HTMLInputElement).value = '5';

    await saveGlobalSettings(new Event("submit"));

    expect(api.updateConfig).toHaveBeenCalledTimes(1);
    const payload = (api.updateConfig as jest.Mock).mock.calls[0]![0];
    expect(payload.grace_period_days).toEqual({ aws: 7, azure: 0, gcp: 5 });
  });

  it('resetSettings restores every grace input to 7', async () => {
    (document.getElementById('setting-grace-aws') as HTMLInputElement).value = '14';
    (document.getElementById('setting-grace-azure') as HTMLInputElement).value = '0';
    (document.getElementById('setting-grace-gcp') as HTMLInputElement).value = '30';

    mockConfirmDialog.mockResolvedValueOnce(true);
    await resetSettings();

    expect((document.getElementById('setting-grace-aws') as HTMLInputElement).value).toBe('7');
    expect((document.getElementById('setting-grace-azure') as HTMLInputElement).value).toBe('7');
    expect((document.getElementById('setting-grace-gcp') as HTMLInputElement).value).toBe('7');
  });
});
