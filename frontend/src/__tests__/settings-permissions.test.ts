/**
 * Settings page permission gating for issue #365 and issue #870.
 *
 * The /admin/general + /admin/purchasing sub-tabs are reachable for
 * every signed-in role (only /admin/accounts and /admin/users are
 * navigation.ts-redirected). Render the form read-only for non-admin
 * sessions: disable every control, hide Save and Reset.
 *
 * Issue #870: the Purchasing Policies panel (#purchasing-panel) is a
 * sibling <section> outside #global-settings-form. The viewer role must
 * see all its inputs as disabled and both Save/Reset buttons hidden.
 */
import { loadGlobalSettings } from '../settings';

jest.mock('../api', () => ({
  getConfig: jest.fn().mockResolvedValue({
    global: {
      enabled_providers: ['aws'],
      notification_email: 'ops@example.com',
      auto_collect: true,
      default_term: 3,
      default_payment: 'no-upfront',
      default_coverage: 80,
      notification_days_before: 3,
      recommendations_cache_stale_hours: 24,
      recommendations_lookback_days: 7,
    },
    services: [],
    source_cloud: 'aws',
  }),
}));

jest.mock('../federation', () => ({
  initFederationPanel: jest.fn().mockResolvedValue(undefined),
}));

jest.mock('../state', () => ({
  getCurrentUser: jest.fn(),
}));

import * as state from '../state';

const mockUser = (role: string | null) => {
  (state.getCurrentUser as jest.Mock).mockReturnValue(
    role === null ? null : { id: 'u', email: 'u@example.com', groups: role === 'admin' ? ['00000000-0000-5000-8000-000000000001'] : [] },
  );
};

interface ControlSpec {
  tag: 'input' | 'select' | 'button';
  id: string;
  type?: string;
  options?: ReadonlyArray<{ value: string; label: string; selected?: boolean }>;
  value?: string;
  buttonType?: 'button' | 'submit';
}

const FORM_CONTROLS: ReadonlyArray<ControlSpec> = [
  { tag: 'input', id: 'provider-aws', type: 'checkbox' },
  { tag: 'input', id: 'provider-azure', type: 'checkbox' },
  { tag: 'input', id: 'provider-gcp', type: 'checkbox' },
  { tag: 'input', id: 'setting-notification-email', type: 'email' },
  { tag: 'input', id: 'setting-auto-collect', type: 'checkbox' },
  {
    tag: 'select',
    id: 'setting-collection-schedule',
    options: [{ value: 'daily', label: 'Daily' }],
  },
  {
    tag: 'select',
    id: 'setting-default-term',
    options: [
      { value: '1', label: '1' },
      { value: '3', label: '3', selected: true },
    ],
  },
  {
    tag: 'select',
    id: 'setting-default-payment',
    options: [
      { value: 'no-upfront', label: 'No', selected: true },
      { value: 'all-upfront', label: 'All' },
    ],
  },
  { tag: 'input', id: 'setting-default-coverage', type: 'number', value: '80' },
  { tag: 'input', id: 'setting-notification-days', type: 'number', value: '3' },
  { tag: 'input', id: 'setting-recs-stale-hours', type: 'number', value: '24' },
  {
    tag: 'select',
    id: 'setting-recs-lookback-days',
    options: [{ value: '7', label: '7', selected: true }],
  },
  { tag: 'button', id: 'reset-settings-btn', buttonType: 'button' },
  { tag: 'button', id: 'save-settings-btn', buttonType: 'submit' },
];

const setupDom = () => {
  document.body.replaceChildren();
  const loading = document.createElement('div');
  loading.id = 'settings-loading';
  loading.className = 'hidden';

  const form = document.createElement('form');
  form.id = 'global-settings-form';
  form.className = 'hidden';

  for (const spec of FORM_CONTROLS) {
    if (spec.tag === 'input') {
      const el = document.createElement('input');
      el.id = spec.id;
      if (spec.type) el.type = spec.type;
      if (spec.value !== undefined) el.value = spec.value;
      form.appendChild(el);
    } else if (spec.tag === 'select') {
      const el = document.createElement('select');
      el.id = spec.id;
      for (const opt of spec.options ?? []) {
        const o = document.createElement('option');
        o.value = opt.value;
        o.textContent = opt.label;
        if (opt.selected) o.selected = true;
        el.appendChild(o);
      }
      form.appendChild(el);
    } else {
      const el = document.createElement('button');
      el.id = spec.id;
      if (spec.buttonType) el.type = spec.buttonType;
      form.appendChild(el);
    }
  }

  const err = document.createElement('div');
  err.id = 'settings-error';
  err.className = 'hidden';

  // Purchasing Policies panel: a sibling <section> outside #global-settings-form.
  // Only the grace-period inputs (unique to this panel) and the action buttons
  // are included here to avoid duplicate IDs with the form above. In the real
  // HTML the term/payment/coverage selects also live in this panel; that does
  // not affect the read-only gate logic since applyReadOnlySettings queries
  // #purchasing-panel independently of #global-settings-form.
  const purchasingPanel = document.createElement('section');
  purchasingPanel.id = 'purchasing-panel';
  for (const id of ['setting-grace-aws', 'setting-grace-azure', 'setting-grace-gcp']) {
    const inp = document.createElement('input');
    inp.id = id;
    inp.type = 'number';
    inp.value = '7';
    purchasingPanel.appendChild(inp);
  }
  const savePurchBtn = document.createElement('button');
  savePurchBtn.id = 'save-purchasing-btn';
  savePurchBtn.type = 'button';
  const resetPurchBtn = document.createElement('button');
  resetPurchBtn.id = 'reset-purchasing-btn';
  resetPurchBtn.type = 'button';
  purchasingPanel.append(savePurchBtn, resetPurchBtn);

  document.body.append(loading, form, err, purchasingPanel);
};

describe('Settings page permission gating (issue #365)', () => {
  beforeEach(() => {
    jest.clearAllMocks();
    setupDom();
  });

  test('admin keeps Save and Reset visible and form enabled', async () => {
    mockUser('admin');
    await loadGlobalSettings();
    const save = document.getElementById('save-settings-btn') as HTMLButtonElement;
    const reset = document.getElementById('reset-settings-btn') as HTMLButtonElement;
    expect(save.hidden).toBe(false);
    expect(reset.hidden).toBe(false);
    const emailInput = document.getElementById('setting-notification-email') as HTMLInputElement;
    expect(emailInput.disabled).toBe(false);
  });

  test('user role hides Save and Reset and disables every form control', async () => {
    mockUser('user');
    await loadGlobalSettings();
    const save = document.getElementById('save-settings-btn') as HTMLButtonElement;
    const reset = document.getElementById('reset-settings-btn') as HTMLButtonElement;
    expect(save.hidden).toBe(true);
    expect(reset.hidden).toBe(true);
    const emailInput = document.getElementById('setting-notification-email') as HTMLInputElement;
    const termSelect = document.getElementById('setting-default-term') as HTMLSelectElement;
    const autoCollect = document.getElementById('setting-auto-collect') as HTMLInputElement;
    expect(emailInput.disabled).toBe(true);
    expect(termSelect.disabled).toBe(true);
    expect(autoCollect.disabled).toBe(true);
  });

  test('readonly role hides Save and Reset and disables every form control', async () => {
    mockUser('readonly');
    await loadGlobalSettings();
    const save = document.getElementById('save-settings-btn') as HTMLButtonElement;
    const reset = document.getElementById('reset-settings-btn') as HTMLButtonElement;
    expect(save.hidden).toBe(true);
    expect(reset.hidden).toBe(true);
    const inputs = document.querySelectorAll<HTMLInputElement | HTMLSelectElement>(
      '#global-settings-form input, #global-settings-form select',
    );
    inputs.forEach((i) => expect(i.disabled).toBe(true));
  });

  test('null user hides Save and Reset and disables the form', async () => {
    mockUser(null);
    await loadGlobalSettings();
    const save = document.getElementById('save-settings-btn') as HTMLButtonElement;
    expect(save.hidden).toBe(true);
    const emailInput = document.getElementById('setting-notification-email') as HTMLInputElement;
    expect(emailInput.disabled).toBe(true);
  });
});

describe('Purchasing Policies panel permission gating (issue #870)', () => {
  beforeEach(() => {
    jest.clearAllMocks();
    setupDom();
  });

  test('admin: purchasing inputs enabled and Save/Reset buttons visible', async () => {
    mockUser('admin');
    await loadGlobalSettings();
    const save = document.getElementById('save-purchasing-btn') as HTMLButtonElement;
    const reset = document.getElementById('reset-purchasing-btn') as HTMLButtonElement;
    expect(save.hidden).toBe(false);
    expect(reset.hidden).toBe(false);
    const inputs = document.querySelectorAll<HTMLInputElement | HTMLSelectElement>(
      '#purchasing-panel input, #purchasing-panel select',
    );
    expect(inputs.length).toBeGreaterThan(0);
    inputs.forEach((el) => expect(el.disabled).toBe(false));
  });

  test('readonly role: purchasing inputs disabled and Save/Reset buttons hidden', async () => {
    mockUser('readonly');
    await loadGlobalSettings();
    const save = document.getElementById('save-purchasing-btn') as HTMLButtonElement;
    const reset = document.getElementById('reset-purchasing-btn') as HTMLButtonElement;
    expect(save.hidden).toBe(true);
    expect(reset.hidden).toBe(true);
    const inputs = document.querySelectorAll<HTMLInputElement | HTMLSelectElement>(
      '#purchasing-panel input, #purchasing-panel select',
    );
    expect(inputs.length).toBeGreaterThan(0);
    inputs.forEach((el) => expect(el.disabled).toBe(true));
  });

  test('user role: purchasing inputs disabled and Save/Reset buttons hidden', async () => {
    mockUser('user');
    await loadGlobalSettings();
    const save = document.getElementById('save-purchasing-btn') as HTMLButtonElement;
    const reset = document.getElementById('reset-purchasing-btn') as HTMLButtonElement;
    expect(save.hidden).toBe(true);
    expect(reset.hidden).toBe(true);
    const graceInput = document.getElementById('setting-grace-aws') as HTMLInputElement;
    expect(graceInput.disabled).toBe(true);
  });

  test('null user: purchasing inputs disabled and Save/Reset buttons hidden', async () => {
    mockUser(null);
    await loadGlobalSettings();
    const save = document.getElementById('save-purchasing-btn') as HTMLButtonElement;
    expect(save.hidden).toBe(true);
    const graceGcp = document.getElementById('setting-grace-gcp') as HTMLInputElement;
    expect(graceGcp.disabled).toBe(true);
  });
});
