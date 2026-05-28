/**
 * Live range + integer-only validation on the five plan-creation numeric
 * inputs (issue #702).
 *
 * Covers wireRangeInput (via wirePlanRangeInputs) for:
 *   #plan-coverage       min=0  max=100
 *   #ramp-step-percent   min=1  max=100
 *   #ramp-interval-days  min=1  max=365  (max attr was previously missing)
 *   #plan-notify-days    min=1  max=30
 *
 * Each case fires a synthetic `input` or `blur` event and asserts on
 * aria-invalid + the sibling .field-error span. Scientific notation
 * (1e+30) must also be rejected because parseInt('1e+30') === 1
 * but the digits-only regex ^\d+$ rejects the exponent form.
 */

import { openNewPlanModal, closePlanModal, openAddPurchasesModal } from '../plans';
import type { ToastOptions } from '../toast';

jest.mock('../api', () => ({
  getPlans: jest.fn(),
  getPlannedPurchases: jest.fn(),
  getPlan: jest.fn(),
  createPlan: jest.fn(),
  updatePlan: jest.fn(),
  patchPlan: jest.fn(),
  deletePlan: jest.fn(),
  runPlannedPurchase: jest.fn(),
  pausePlannedPurchase: jest.fn(),
  resumePlannedPurchase: jest.fn(),
  deletePlannedPurchase: jest.fn(),
  createPlannedPurchases: jest.fn(),
  listPlanAccounts: jest.fn().mockResolvedValue([]),
  setPlanAccounts: jest.fn().mockResolvedValue(undefined),
  listAccounts: jest.fn().mockResolvedValue([]),
}));

jest.mock('../state', () => ({
  getRecommendations: jest.fn().mockReturnValue([]),
  getSelectedRecommendationIDs: jest.fn().mockReturnValue(new Set()),
  getVisibleRecommendations: jest.fn().mockReturnValue([]),
  setVisibleRecommendations: jest.fn(),
  getCurrentProvider: jest.fn().mockReturnValue(''),
  setCurrentProvider: jest.fn(),
  getCurrentAccountIDs: jest.fn().mockReturnValue([]),
  setCurrentAccountIDs: jest.fn(),
  subscribeProvider: jest.fn().mockReturnValue(() => {}),
  subscribeAccount: jest.fn().mockReturnValue(() => {}),
  getCurrentUser: jest.fn().mockReturnValue({ id: 'u-admin', email: 'admin@example.com', groups: ['00000000-0000-5000-8000-000000000001'] }),
  getPlansColumnFilters: jest.fn().mockReturnValue({}),
  setPlansColumnFilter: jest.fn(),
  clearAllPlansColumnFilters: jest.fn(),
}));

jest.mock('../history', () => ({ viewPlanHistory: jest.fn() }));

jest.mock('../commitmentOptions', () => ({
  populateTermSelect: jest.fn(),
  populatePaymentSelect: jest.fn(),
  isValidCombination: jest.fn().mockReturnValue(true),
  normalizePaymentValue: jest.fn((value) => value),
}));

const mockShowToast = jest.fn((_opts: ToastOptions) => ({ dismiss: jest.fn() }));
jest.mock('../toast', () => ({
  showToast: (opts: ToastOptions) => mockShowToast(opts),
}));

jest.mock('../confirmDialog', () => ({
  confirmDialog: jest.fn(() => Promise.resolve(true)),
}));

jest.mock('../utils', () => ({
  formatDate: jest.fn((v) => v ?? ''),
  formatTerm: jest.fn((y) => `${y}yr`),
  formatRampSchedule: jest.fn((v) => v ?? ''),
  getStatusBadge: jest.fn(() => ({ class: 'active', label: 'Active' })),
  escapeHtml: jest.fn((s) => s ?? ''),
  formatCurrency: jest.fn((v) => `$${v}`),
  populateAccountFilter: jest.fn(() => Promise.resolve()),
}));

// ---------------------------------------------------------------------------
// DOM helpers
// ---------------------------------------------------------------------------

function addInput(
  parent: Element,
  id: string,
  type: string,
  value: string,
  attrs: Record<string, string> = {},
): HTMLInputElement {
  const el = document.createElement('input');
  el.id = id;
  el.type = type;
  el.value = value;
  for (const [k, v] of Object.entries(attrs)) el.setAttribute(k, v);
  parent.appendChild(el);
  return el;
}

function addSelect(
  parent: Element,
  id: string,
  options: Array<{ value: string; label: string }>,
): HTMLSelectElement {
  const el = document.createElement('select');
  el.id = id;
  for (const { value, label } of options) {
    const opt = document.createElement('option');
    opt.value = value;
    opt.textContent = label;
    el.appendChild(opt);
  }
  parent.appendChild(el);
  return el;
}

function buildPlanModalDOM(): void {
  // Outer modal container
  const modal = document.createElement('div');
  modal.id = 'plan-modal';

  const title = document.createElement('h3');
  title.id = 'plan-modal-title';
  title.textContent = 'New Purchase Plan';
  modal.appendChild(title);

  const form = document.createElement('form');
  form.id = 'plan-form';
  modal.appendChild(form);

  addInput(form, 'plan-id', 'hidden', '');
  addInput(form, 'plan-name', 'text', '');

  const desc = document.createElement('textarea');
  desc.id = 'plan-description';
  form.appendChild(desc);

  // Provider select with optgroups for service
  addSelect(form, 'plan-provider', [
    { value: 'aws', label: 'AWS' },
    { value: 'azure', label: 'Azure' },
    { value: 'gcp', label: 'GCP' },
  ]);

  const svcSelect = document.createElement('select');
  svcSelect.id = 'plan-service';
  const awsGroup = document.createElement('optgroup');
  awsGroup.label = 'AWS Services';
  const ec2Opt = document.createElement('option');
  ec2Opt.value = 'ec2';
  ec2Opt.textContent = 'EC2';
  awsGroup.appendChild(ec2Opt);
  svcSelect.appendChild(awsGroup);
  form.appendChild(svcSelect);

  addSelect(form, 'plan-term', [
    { value: '1', label: '1 Year' },
    { value: '3', label: '3 Years' },
  ]);
  addSelect(form, 'plan-payment', [
    { value: 'no-upfront', label: 'No Upfront' },
    { value: 'partial-upfront', label: 'Partial Upfront' },
    { value: 'all-upfront', label: 'All Upfront' },
  ]);

  // The five numeric inputs under test (min/max match HTML spec + issue #702)
  addInput(form, 'plan-coverage', 'number', '80', { min: '0', max: '100' });

  const autoPurchase = document.createElement('input');
  autoPurchase.id = 'plan-auto-purchase';
  autoPurchase.type = 'checkbox';
  form.appendChild(autoPurchase);

  addInput(form, 'plan-notify-days', 'number', '3', { min: '1', max: '30' });

  const enabledCheck = document.createElement('input');
  enabledCheck.id = 'plan-enabled';
  enabledCheck.type = 'checkbox';
  enabledCheck.checked = true;
  form.appendChild(enabledCheck);

  // Ramp radio buttons
  for (const [value, checked] of [['immediate', true], ['weekly-25pct', false], ['monthly-10pct', false], ['custom', false]] as const) {
    const r = document.createElement('input');
    r.type = 'radio';
    r.name = 'ramp-schedule';
    r.value = value;
    if (checked) r.checked = true;
    form.appendChild(r);
  }

  // Custom ramp config section
  const customConfig = document.createElement('div');
  customConfig.id = 'custom-ramp-config';
  customConfig.className = 'hidden';
  addInput(customConfig, 'ramp-step-percent', 'number', '20', { min: '1', max: '100' });
  addInput(customConfig, 'ramp-interval-days', 'number', '7', { min: '1', max: '365' });
  form.appendChild(customConfig);

  // Plans list and planned purchases (required by plans.ts loadPlans path)
  const plansList = document.createElement('div');
  plansList.id = 'plans-list';
  const ppList = document.createElement('div');
  ppList.id = 'planned-purchases-list';

  document.body.replaceChildren(modal, plansList, ppList);
}

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

function fire(el: HTMLElement, type: 'input' | 'blur'): void {
  el.dispatchEvent(new Event(type));
}

function errorEl(inputId: string): HTMLElement | null {
  return document.getElementById(`${inputId}-range-error`);
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

describe('plan numeric inputs: live range validation (#702)', () => {
  beforeEach(() => {
    jest.clearAllMocks();
    buildPlanModalDOM();
    // openNewPlanModal calls wirePlanRangeInputs which wires all five inputs.
    openNewPlanModal();
  });

  // --- out-of-range values show inline error --------------------------------

  it.each([
    ['plan-coverage', '101', 'Must be a whole number between 0 and 100'],
    ['plan-coverage', '-1', 'Must be a whole number between 0 and 100'],
    ['ramp-step-percent', '0', 'Must be a whole number between 1 and 100'],
    ['ramp-step-percent', '101', 'Must be a whole number between 1 and 100'],
    ['ramp-interval-days', '0', 'Must be a whole number between 1 and 365'],
    ['ramp-interval-days', '366', 'Must be a whole number between 1 and 365'],
    ['plan-notify-days', '0', 'Must be a whole number between 1 and 30'],
    ['plan-notify-days', '31', 'Must be a whole number between 1 and 30'],
  ])('typing %s=%s shows inline error "%s"', (inputId, value, expectedMsg) => {
    const input = document.getElementById(inputId) as HTMLInputElement;
    input.value = value;
    fire(input, 'input');

    expect(input.getAttribute('aria-invalid')).toBe('true');
    const err = errorEl(inputId);
    expect(err).not.toBeNull();
    expect(err!.classList.contains('hidden')).toBe(false);
    expect(err!.textContent).toBe(expectedMsg);
  });

  // --- scientific notation rejected ----------------------------------------

  it('rejects scientific notation (1e+30) in ramp-interval-days', () => {
    const input = document.getElementById('ramp-interval-days') as HTMLInputElement;
    input.value = '1e+30';
    fire(input, 'input');

    expect(input.getAttribute('aria-invalid')).toBe('true');
    const err = errorEl('ramp-interval-days');
    expect(err).not.toBeNull();
    expect(err!.classList.contains('hidden')).toBe(false);
  });

  it('rejects scientific notation (1e+30) in plan-coverage', () => {
    const input = document.getElementById('plan-coverage') as HTMLInputElement;
    input.value = '1e+30';
    fire(input, 'input');

    expect(input.getAttribute('aria-invalid')).toBe('true');
  });

  it('rejects scientific notation (2e2) in ramp-step-percent', () => {
    const input = document.getElementById('ramp-step-percent') as HTMLInputElement;
    input.value = '2e2';
    fire(input, 'input');

    expect(input.getAttribute('aria-invalid')).toBe('true');
  });

  // --- valid values clear the error ----------------------------------------

  it.each([
    ['plan-coverage', '80'],
    ['plan-coverage', '0'],
    ['plan-coverage', '100'],
    ['ramp-step-percent', '25'],
    ['ramp-interval-days', '7'],
    ['ramp-interval-days', '365'],
    ['ramp-interval-days', '1'],
    ['plan-notify-days', '3'],
    ['plan-notify-days', '1'],
    ['plan-notify-days', '30'],
  ])('valid value %s=%s clears aria-invalid and hides the error span', (inputId, value) => {
    const input = document.getElementById(inputId) as HTMLInputElement;
    // First trigger an error state.
    input.value = '9999';
    fire(input, 'input');
    expect(input.getAttribute('aria-invalid')).toBe('true');

    // Then type a valid value.
    input.value = value;
    fire(input, 'input');
    expect(input.getAttribute('aria-invalid')).toBeNull();
    const err = errorEl(inputId);
    expect(err).not.toBeNull();
    expect(err!.classList.contains('hidden')).toBe(true);
  });

  // --- empty field clears the error ----------------------------------------

  it('clearing the field removes aria-invalid and hides the error', () => {
    const input = document.getElementById('plan-notify-days') as HTMLInputElement;
    input.value = '999';
    fire(input, 'input');
    expect(input.getAttribute('aria-invalid')).toBe('true');

    input.value = '';
    fire(input, 'input');
    expect(input.getAttribute('aria-invalid')).toBeNull();
    expect(errorEl('plan-notify-days')!.classList.contains('hidden')).toBe(true);
  });

  // --- blur clamps the value to [min, max] ---------------------------------

  it('out-of-range value is clamped to max on blur', () => {
    const input = document.getElementById('plan-coverage') as HTMLInputElement;
    input.value = '150';
    fire(input, 'blur');

    expect(input.value).toBe('100');
    expect(input.getAttribute('aria-invalid')).toBeNull();
    expect(errorEl('plan-coverage')!.classList.contains('hidden')).toBe(true);
  });

  it('below-min value is clamped to min on blur', () => {
    const input = document.getElementById('plan-notify-days') as HTMLInputElement;
    input.value = '0';
    fire(input, 'blur');

    expect(input.value).toBe('1');
    expect(input.getAttribute('aria-invalid')).toBeNull();
  });

  it('non-integer value is NOT clamped on blur (error stays visible)', () => {
    const input = document.getElementById('plan-coverage') as HTMLInputElement;
    input.value = '1e+30';
    fire(input, 'input');
    expect(input.getAttribute('aria-invalid')).toBe('true');

    fire(input, 'blur');
    // Non-integer cannot be clamped; error persists so the user sees feedback.
    expect(input.value).toBe('1e+30');
    expect(input.getAttribute('aria-invalid')).toBe('true');
  });

  // --- idempotency: stale error UI is reconciled on reopen -----------------

  it('re-opening the modal reconciles stale error UI for valid default values', () => {
    // Trigger an error.
    const coverage = document.getElementById('plan-coverage') as HTMLInputElement;
    coverage.value = '999';
    fire(coverage, 'input');
    expect(coverage.getAttribute('aria-invalid')).toBe('true');

    // Reset the field to a valid value (simulating form reset) then reopen.
    coverage.value = '80';
    closePlanModal();
    openNewPlanModal();

    // The reopen re-dispatches 'input', which clears the stale error.
    expect(coverage.getAttribute('aria-invalid')).toBeNull();
    expect(errorEl('plan-coverage')!.classList.contains('hidden')).toBe(true);
  });

  // --- idempotency: re-opening does not stack duplicate error spans ---------

  it('re-opening the modal does not create duplicate error spans', () => {
    // Trigger validation to create the error spans.
    const coverage = document.getElementById('plan-coverage') as HTMLInputElement;
    coverage.value = '999';
    fire(coverage, 'input');

    // Close and re-open.
    closePlanModal();
    openNewPlanModal();

    // Exactly one error span per input — no duplicates.
    expect(document.querySelectorAll('#plan-coverage-range-error')).toHaveLength(1);
    expect(document.querySelectorAll('#ramp-interval-days-range-error')).toHaveLength(1);
    expect(document.querySelectorAll('#plan-notify-days-range-error')).toHaveLength(1);
    expect(document.querySelectorAll('#ramp-step-percent-range-error')).toHaveLength(1);
  });

  // --- aria attributes are set correctly ------------------------------------

  it('error span has role=status and aria-live=polite', () => {
    const input = document.getElementById('plan-coverage') as HTMLInputElement;
    input.value = '999';
    fire(input, 'input');

    const err = errorEl('plan-coverage');
    expect(err).not.toBeNull();
    expect(err!.getAttribute('role')).toBe('status');
    expect(err!.getAttribute('aria-live')).toBe('polite');
  });

  it('input aria-describedby references the error span id', () => {
    const input = document.getElementById('plan-coverage') as HTMLInputElement;
    input.value = '999';
    fire(input, 'input');

    const describedBy = input.getAttribute('aria-describedby') ?? '';
    expect(describedBy).toContain('plan-coverage-range-error');
  });
});

// ---------------------------------------------------------------------------
// Add Purchases modal: inline range validation on Number of Purchases (#771)
// ---------------------------------------------------------------------------

/**
 * Wire live range validation on the "Number of Purchases" field
 * in the Add Purchases modal (issue #771).
 *
 * The field has min=1 max=52 (weekly cadence cap). Mirrors the same
 * wireRangeInput pattern as the plan-creation modal (#702/#714):
 *   - aria-invalid + sibling .field-error on out-of-range / non-integer
 *     input / blur
 *   - Submit button is disabled while the field is invalid
 *   - Save-time guard uses Number() + Number.isInteger() (not parseInt)
 *     so fractional values like 2.5 are rejected before reaching the API
 */
describe('Add Purchases modal: inline range validation (#771)', () => {
  beforeEach(async () => {
    jest.clearAllMocks();
    // Provide a minimal body with the plans-list container that
    // openAddPurchasesModal's DOM-removal path expects.
    const plansList = document.createElement('div');
    plansList.id = 'plans-list';
    const ppList = document.createElement('div');
    ppList.id = 'planned-purchases-list';
    document.body.replaceChildren(plansList, ppList);

    await openAddPurchasesModal('plan-xyz', 'My Test Plan');
  });

  function countInput(): HTMLInputElement {
    return document.getElementById('add-purchases-count') as HTMLInputElement;
  }

  function submitBtn(): HTMLButtonElement {
    return document.querySelector<HTMLButtonElement>('#add-purchases-modal button[type="submit"]') as HTMLButtonElement;
  }

  function errorEl(): HTMLElement | null {
    return document.getElementById('add-purchases-count-range-error');
  }

  // --- out-of-range values show inline error --------------------------------

  it('typing 0 (below min) sets aria-invalid and shows the error span', () => {
    const input = countInput();
    input.value = '0';
    input.dispatchEvent(new Event('input'));

    expect(input.getAttribute('aria-invalid')).toBe('true');
    const err = errorEl();
    expect(err).not.toBeNull();
    expect(err!.classList.contains('hidden')).toBe(false);
    expect(err!.textContent).toBe('Must be a whole number between 1 and 52');
  });

  it('typing 53 (above max) sets aria-invalid and shows the error span', () => {
    const input = countInput();
    input.value = '53';
    input.dispatchEvent(new Event('input'));

    expect(input.getAttribute('aria-invalid')).toBe('true');
    const err = errorEl();
    expect(err).not.toBeNull();
    expect(err!.classList.contains('hidden')).toBe(false);
    expect(err!.textContent).toBe('Must be a whole number between 1 and 52');
  });

  // --- submit button disabled while field is invalid -----------------------

  it('submit button is disabled when value is 0', () => {
    const input = countInput();
    input.value = '0';
    input.dispatchEvent(new Event('input'));

    expect(submitBtn().disabled).toBe(true);
  });

  it('submit button is disabled when value is 53', () => {
    const input = countInput();
    input.value = '53';
    input.dispatchEvent(new Event('input'));

    expect(submitBtn().disabled).toBe(true);
  });

  it('submit button is re-enabled when value comes back in range', () => {
    const input = countInput();
    input.value = '0';
    input.dispatchEvent(new Event('input'));
    expect(submitBtn().disabled).toBe(true);

    input.value = '4';
    input.dispatchEvent(new Event('input'));
    expect(submitBtn().disabled).toBe(false);
  });

  // --- valid boundary values clear the error --------------------------------

  it.each([['1'], ['26'], ['52']])(
    'valid value %s clears aria-invalid and hides the error span',
    (value) => {
      const input = countInput();
      // First trigger an error.
      input.value = '0';
      input.dispatchEvent(new Event('input'));
      expect(input.getAttribute('aria-invalid')).toBe('true');

      input.value = value;
      input.dispatchEvent(new Event('input'));
      expect(input.getAttribute('aria-invalid')).toBeNull();
      const err = errorEl();
      expect(err).not.toBeNull();
      expect(err!.classList.contains('hidden')).toBe(true);
    },
  );

  // --- fractional input rejected (feedback_strict_int_parse) ---------------

  it('fractional value 2.5 is rejected as non-integer', () => {
    const input = countInput();
    input.value = '2.5';
    input.dispatchEvent(new Event('input'));

    expect(input.getAttribute('aria-invalid')).toBe('true');
    expect(errorEl()!.classList.contains('hidden')).toBe(false);
  });

  // --- error span a11y attributes ------------------------------------------

  it('error span has role=status and aria-live=polite', () => {
    const input = countInput();
    input.value = '0';
    input.dispatchEvent(new Event('input'));

    const err = errorEl();
    expect(err).not.toBeNull();
    expect(err!.getAttribute('role')).toBe('status');
    expect(err!.getAttribute('aria-live')).toBe('polite');
  });

  it('input aria-describedby references the error span id', () => {
    const input = countInput();
    input.value = '0';
    input.dispatchEvent(new Event('input'));

    expect(input.getAttribute('aria-describedby')).toContain('add-purchases-count-range-error');
  });

  // --- save-time guard prevents API call on out-of-range value -------------

  it('submitting count=0 is blocked by save-time guard without calling the API', async () => {
    (require('../api').createPlannedPurchases as jest.Mock).mockResolvedValue({});

    const input = countInput();
    input.value = '0';
    input.dispatchEvent(new Event('input'));

    const form = document.getElementById('add-purchases-form') as HTMLFormElement;
    form.dispatchEvent(new Event('submit'));

    await new Promise(resolve => setTimeout(resolve, 20));

    expect(require('../api').createPlannedPurchases).not.toHaveBeenCalled();
    const errDiv = document.getElementById('add-purchases-error');
    expect(errDiv?.classList.contains('hidden')).toBe(false);
    expect(errDiv?.textContent).toMatch(/whole number between 1 and 52/i);
  });

  it('submitting count=53 is blocked by save-time guard without calling the API', async () => {
    (require('../api').createPlannedPurchases as jest.Mock).mockResolvedValue({});

    const input = countInput();
    input.value = '53';
    input.dispatchEvent(new Event('input'));

    const form = document.getElementById('add-purchases-form') as HTMLFormElement;
    form.dispatchEvent(new Event('submit'));

    await new Promise(resolve => setTimeout(resolve, 20));

    expect(require('../api').createPlannedPurchases).not.toHaveBeenCalled();
  });
});
