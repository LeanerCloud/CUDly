/**
 * Settings page permission gating for issue #365, issue #870, issue #979,
 * issue #1401, and issue #1410.
 *
 * The /admin/general + /admin/purchasing sub-tabs are reachable for
 * every signed-in role (only /admin/accounts and /admin/users are
 * navigation.ts-redirected). Render the form read-only for non-admin
 * sessions: disable every control, hide Save and Reset.
 *
 * Issue #870: the Purchasing Policies panel (#purchasing-panel) is a
 * sibling <section> outside #global-settings-form. The viewer role must
 * see all its inputs as disabled and both Save/Reset buttons hidden.
 *
 * Issue #979: when GET /api/config returns 403 (permission denied), the
 * error banner must NOT be shown; the section stays hidden and the page
 * degrades gracefully. Non-permission failures (network, 5xx) still
 * surface the error banner.
 *
 * Issue #1401: after migration 000088 grants view:config to non-admin groups,
 * GET /api/config succeeds for those users. The settings form (#global-settings-
 * form) must become VISIBLE (not just the section header) after a successful
 * load, and all its controls must be read-only (disabled, Save/Reset hidden).
 *
 * Issue #1410: the Purchasing Policies panel's own inputs (term, payment,
 * target coverage) live in #purchasing-panel, not in #global-settings-form.
 * applyReadOnlySettings queries both independently; after a successful
 * getConfig call those controls must be disabled for non-admin sessions.
 */
import { loadGlobalSettings, resetConfigCache } from '../settings';
import * as api from '../api';

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

// Controls that live inside #global-settings-form (General sub-tab).
// Issue #1410: term/payment/coverage/lookback belong in #purchasing-panel in the
// real HTML; they are added there in setupDom below. Do NOT put them here to
// avoid duplicate IDs and to correctly test that applyReadOnlySettings disables
// them via the #purchasing-panel path (not the #global-settings-form path).
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
  { tag: 'input', id: 'setting-notification-days', type: 'number', value: '3' },
  { tag: 'input', id: 'setting-recs-stale-hours', type: 'number', value: '24' },
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
  // Matches the real HTML structure: term/payment/coverage/lookback live HERE
  // (in #purchasing-global-defaults and #purchasing-recommendations-lookback
  // fieldsets inside #purchasing-panel), not in #global-settings-form.
  // applyReadOnlySettings queries #purchasing-panel independently so these
  // controls are disabled via that path. Adding them here verifies issue #1410.
  const purchasingPanel = document.createElement('section');
  purchasingPanel.id = 'purchasing-panel';

  // Global Purchase Defaults (in real HTML: fieldset#purchasing-global-defaults)
  const termSel = document.createElement('select');
  termSel.id = 'setting-default-term';
  for (const v of ['1', '3']) {
    const o = document.createElement('option');
    o.value = v;
    o.textContent = v + ' Year';
    if (v === '3') o.selected = true;
    termSel.appendChild(o);
  }
  purchasingPanel.appendChild(termSel);

  const paymentSel = document.createElement('select');
  paymentSel.id = 'setting-default-payment';
  for (const v of ['no-upfront', 'all-upfront']) {
    const o = document.createElement('option');
    o.value = v;
    o.textContent = v;
    if (v === 'no-upfront') o.selected = true;
    paymentSel.appendChild(o);
  }
  purchasingPanel.appendChild(paymentSel);

  const coverageInput = document.createElement('input');
  coverageInput.id = 'setting-default-coverage';
  coverageInput.type = 'number';
  coverageInput.value = '80';
  purchasingPanel.appendChild(coverageInput);

  // Recommendations Lookback (in real HTML: fieldset#purchasing-recommendations-lookback)
  const lookbackSel = document.createElement('select');
  lookbackSel.id = 'setting-recs-lookback-days';
  for (const v of ['7', '30', '60']) {
    const o = document.createElement('option');
    o.value = v;
    o.textContent = v + ' days';
    if (v === '7') o.selected = true;
    lookbackSel.appendChild(o);
  }
  purchasingPanel.appendChild(lookbackSel);

  // Grace period inputs (unique to the purchasing panel)
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

/**
 * Issue #979: graceful degradation when GET /api/config returns a
 * permission-denied error for non-admin users on the Purchasing policies page.
 */
describe('Settings config load: graceful degradation on permission errors (issue #979)', () => {
  beforeEach(() => {
    jest.clearAllMocks();
    setupDom();
    resetConfigCache();
  });

  function makePermissionError(status: number, message: string): Error & { status: number } {
    const err = new Error(message) as Error & { status: number };
    err.status = status;
    return err;
  }

  test('403 permission-denied error hides loading, keeps form hidden, shows no error banner', async () => {
    (api.getConfig as jest.Mock).mockRejectedValueOnce(
      makePermissionError(403, 'permission denied: requires view on config'),
    );

    await loadGlobalSettings();

    const loadingEl = document.getElementById('settings-loading');
    const formEl = document.getElementById('global-settings-form');
    const errorEl = document.getElementById('settings-error');

    // Loading spinner must be hidden (not spinning forever)
    expect(loadingEl?.classList.contains('hidden')).toBe(true);
    // Form stays hidden; no partial render
    expect(formEl?.classList.contains('hidden')).toBe(true);
    // No error banner shown
    expect(errorEl?.classList.contains('hidden')).toBe(true);
  });

  test('403 error with message not starting with "permission denied" still shows error banner', async () => {
    // A 403 for a different reason (e.g. IP block) should still surface
    (api.getConfig as jest.Mock).mockRejectedValueOnce(
      makePermissionError(403, 'access blocked by IP policy'),
    );

    await loadGlobalSettings();

    const errorEl = document.getElementById('settings-error');
    // Banner is shown for non-permission-denied 403s
    expect(errorEl?.classList.contains('hidden')).toBe(false);
    expect(errorEl?.textContent).toContain('access blocked by IP policy');
  });

  test('500 server error still shows the error banner', async () => {
    (api.getConfig as jest.Mock).mockRejectedValueOnce(
      makePermissionError(500, 'internal server error'),
    );

    await loadGlobalSettings();

    const errorEl = document.getElementById('settings-error');
    expect(errorEl?.classList.contains('hidden')).toBe(false);
    expect(errorEl?.textContent).toContain('internal server error');
  });

  test('network error still shows the error banner', async () => {
    (api.getConfig as jest.Mock).mockRejectedValueOnce(
      new Error('Failed to fetch'),
    );

    await loadGlobalSettings();

    const errorEl = document.getElementById('settings-error');
    expect(errorEl?.classList.contains('hidden')).toBe(false);
    expect(errorEl?.textContent).toContain('Failed to fetch');
  });
});

/**
 * Issue #1401: after migration 000088 grants view:config to Standard Users and
 * Read-Only Users, GET /api/config returns 200 for those groups. The settings
 * form (#global-settings-form) must be VISIBLE after a successful load — users
 * must see populated fields, not just the section header.
 *
 * Issue #1410: the Purchasing Policies panel's core inputs (Default Term,
 * Default Payment Option, Target Coverage) live in #purchasing-panel in the
 * real HTML and must be disabled (read-only) for non-admin sessions.
 * applyReadOnlySettings queries #purchasing-panel independently of
 * #global-settings-form; this test suite verifies that path.
 */
describe('Non-admin settings visibility after view:config grant (issues #1401 and #1410)', () => {
  beforeEach(() => {
    jest.clearAllMocks();
    resetConfigCache();
    setupDom();
  });

  test(
    '#1401: form (#global-settings-form) is visible (not just header) after ' +
    'successful getConfig for a standard user',
    async () => {
      mockUser('user');
      await loadGlobalSettings();

      const formEl = document.getElementById('global-settings-form');
      // The form must not carry the 'hidden' class. Before migration 000088
      // loadGlobalSettings returned early on 403, so the form stayed hidden
      // and only the section header was visible.
      expect(formEl?.classList.contains('hidden')).toBe(false);
    },
  );

  test(
    '#1401: form is visible and notification-email field is populated for ' +
    'a read-only user',
    async () => {
      mockUser('readonly');
      await loadGlobalSettings();

      const formEl = document.getElementById('global-settings-form');
      expect(formEl?.classList.contains('hidden')).toBe(false);
      // Field is populated from the mocked getConfig response.
      const emailInput = document.getElementById('setting-notification-email') as HTMLInputElement;
      expect(emailInput.value).toBe('ops@example.com');
    },
  );

  test(
    '#1410: Default Term select (in #purchasing-panel) is disabled for a ' +
    'standard user without write permission',
    async () => {
      mockUser('user');
      await loadGlobalSettings();

      // setting-default-term lives in #purchasing-panel in the real HTML.
      // applyReadOnlySettings queries the panel independently; before the fix
      // loadGlobalSettings returned early on 403 and never called
      // applyReadOnlySettings, leaving this select enabled.
      const termSel = document.getElementById('setting-default-term') as HTMLSelectElement;
      expect(termSel.disabled).toBe(true);
    },
  );

  test(
    '#1410: Default Payment select (in #purchasing-panel) is disabled for a ' +
    'read-only user without write permission',
    async () => {
      mockUser('readonly');
      await loadGlobalSettings();

      const paymentSel = document.getElementById('setting-default-payment') as HTMLSelectElement;
      expect(paymentSel.disabled).toBe(true);
    },
  );

  test(
    '#1410: Target Coverage input (in #purchasing-panel) is disabled and ' +
    'Save/Reset purchasing buttons are hidden for a standard user',
    async () => {
      mockUser('user');
      await loadGlobalSettings();

      const coverage = document.getElementById('setting-default-coverage') as HTMLInputElement;
      expect(coverage.disabled).toBe(true);

      const savePurchBtn = document.getElementById('save-purchasing-btn') as HTMLButtonElement;
      const resetPurchBtn = document.getElementById('reset-purchasing-btn') as HTMLButtonElement;
      expect(savePurchBtn.hidden).toBe(true);
      expect(resetPurchBtn.hidden).toBe(true);
    },
  );

  test(
    '#1410: all #purchasing-panel inputs and selects are disabled for a ' +
    'read-only user (full panel sweep)',
    async () => {
      mockUser('readonly');
      await loadGlobalSettings();

      const inputs = document.querySelectorAll<HTMLInputElement | HTMLSelectElement>(
        '#purchasing-panel input, #purchasing-panel select',
      );
      expect(inputs.length).toBeGreaterThan(0);
      inputs.forEach((el) =>
        expect(el.disabled).toBe(
          true,
        ),
      );
    },
  );
});
