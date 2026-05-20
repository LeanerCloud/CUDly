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

import { loadGlobalSettings, saveGlobalSettings, resetSettings, setupSettingsHandlers } from '../settings';

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

/**
 * Settings → Purchasing: per-provider grace inputs flow through
 * wireInlineRangeValidation (follow-up to #471's "Out of scope" carve-out).
 *
 * Verifies the same inline contract the three top-level numeric inputs
 * now use: aria-invalid + a sibling .field-error appears as the user
 * types an out-of-range or fractional value, and clears when the value
 * comes back in range or the field is emptied. Save remains blocked
 * for invalid input but uses showToast (not alert/window.alert), so
 * the inline indicator stays the primary feedback channel.
 */
describe('Settings → Purchasing grace inputs: inline range validation', () => {
  let alertSpy: jest.SpyInstance;

  beforeEach(() => {
    jest.clearAllMocks();
    seedDOM();
    setupSettingsHandlers();
    alertSpy = jest.spyOn(window, 'alert').mockImplementation(() => undefined);
  });

  afterEach(() => {
    alertSpy.mockRestore();
  });

  function fire(el: HTMLInputElement, type: 'input' | 'blur'): void {
    el.dispatchEvent(new Event(type));
  }

  function errorEl(inputId: string): HTMLElement | null {
    return document.getElementById(`${inputId}-range-error`);
  }

  it.each([
    ['setting-grace-aws', '31'],
    ['setting-grace-azure', '31'],
    ['setting-grace-gcp', '-1'],
  ])('typing out-of-range value into %s shows inline error', (inputId, value) => {
    const input = document.getElementById(inputId) as HTMLInputElement;
    input.value = value;
    fire(input, 'input');

    expect(input.getAttribute('aria-invalid')).toBe('true');
    const err = errorEl(inputId);
    expect(err).not.toBeNull();
    expect(err!.classList.contains('hidden')).toBe(false);
    expect(err!.textContent).toBe('Must be a whole number between 0 and 30');
  });

  it('typing a fractional value (7.5) is rejected inline (requireInteger)', () => {
    const input = document.getElementById('setting-grace-azure') as HTMLInputElement;
    input.value = '7.5';
    fire(input, 'input');

    expect(input.getAttribute('aria-invalid')).toBe('true');
    expect(errorEl('setting-grace-azure')!.textContent).toBe('Must be a whole number between 0 and 30');
  });

  it('clearing the field removes aria-invalid and hides the inline error', () => {
    const input = document.getElementById('setting-grace-aws') as HTMLInputElement;
    input.value = '99';
    fire(input, 'input');
    expect(input.getAttribute('aria-invalid')).toBe('true');

    input.value = '';
    fire(input, 'input');
    expect(input.getAttribute('aria-invalid')).toBeNull();
    expect(errorEl('setting-grace-aws')!.classList.contains('hidden')).toBe(true);
  });

  it('typing a valid in-range value removes aria-invalid', () => {
    const input = document.getElementById('setting-grace-gcp') as HTMLInputElement;
    input.value = '99';
    fire(input, 'input');
    expect(input.getAttribute('aria-invalid')).toBe('true');

    input.value = '15';
    fire(input, 'input');
    expect(input.getAttribute('aria-invalid')).toBeNull();
    expect(errorEl('setting-grace-gcp')!.classList.contains('hidden')).toBe(true);
  });

  it('save with a grace input still invalid is blocked and never calls window.alert', async () => {
    (document.getElementById('setting-grace-aws') as HTMLInputElement).value = '31';

    await saveGlobalSettings(new Event('submit'));

    expect(api.updateConfig).not.toHaveBeenCalled();
    expect(alertSpy).not.toHaveBeenCalled();
    // The save-time toast aggregates failed providers per #478 (one toast
    // naming every affected provider in input order, AWS/Azure/GCP). The
    // inline indicator still carries the per-input error separately.
    expect(mockShowToast).toHaveBeenCalled();
    const toastArg = mockShowToast.mock.calls[0]![0] as { message: string; kind: string };
    expect(toastArg.message).toBe('Grace period must be a whole number between 0 and 30 days (AWS).');
    expect(toastArg.kind).toBe('error');
  });
});
