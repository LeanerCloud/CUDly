/**
 * Tests for the modal focus-trap helper (issue #56). Covers the keyboard
 * trap, ESC-to-close, focus restoration on close, and the initialFocus
 * option. Also exercises two representative real call sites
 * (openCreatePlanModal + the account modal DOM) to confirm the trap
 * engages end-to-end through the production show/hide path.
 */

import { openModal, closeModal, FOCUSABLE_SELECTOR } from '../modal';

// ----- Mocks for the call-site integration tests -----
// plans.openCreatePlanModal touches state and a few helpers; mock them
// so the test stays focused on the focus-trap behaviour rather than
// re-validating plan business logic.
jest.mock('../state', () => ({
  getSelectedRecommendationIDs: jest.fn(() => new Set()),
  getRecommendations: jest.fn(() => []),
}));
jest.mock('../api', () => ({
  listAccounts: jest.fn(() => Promise.resolve([])),
  getConfig: jest.fn(() => Promise.resolve({})),
}));
jest.mock('../commitmentOptions', () => ({
  populateTermSelect: jest.fn(),
  populatePaymentSelect: jest.fn(),
  isValidCombination: jest.fn(() => true),
  normalizePaymentValue: jest.fn((v: string) => v),
  fetchAndPopulateCommitmentOptions: jest.fn(() => Promise.resolve()),
}));
jest.mock('../federation', () => ({ initFederationPanel: jest.fn() }));
jest.mock('../settings-subnav', () => ({ reflectDirtyState: jest.fn() }));
jest.mock('../confirmDialog', () => ({ confirmDialog: jest.fn(() => Promise.resolve(true)) }));

// ----- Test helpers -----

function dispatchKey(target: Element, key: string, opts: KeyboardEventInit = {}): KeyboardEvent {
  const event = new KeyboardEvent('keydown', { key, bubbles: true, cancelable: true, ...opts });
  target.dispatchEvent(event);
  return event;
}

/**
 * Build a minimal modal with three buttons + a focusable trigger
 * outside it.
 */
function buildModal(): { modal: HTMLDivElement; trigger: HTMLButtonElement; first: HTMLButtonElement; mid: HTMLButtonElement; last: HTMLButtonElement } {
  const trigger = document.createElement('button');
  trigger.id = 'trigger';
  trigger.textContent = 'Open';
  document.body.appendChild(trigger);

  const modal = document.createElement('div');
  modal.id = 'm';
  modal.className = 'modal hidden';
  modal.setAttribute('role', 'dialog');
  modal.setAttribute('aria-modal', 'true');
  const first = document.createElement('button');
  first.id = 'first';
  first.textContent = 'first';
  const mid = document.createElement('button');
  mid.id = 'mid';
  mid.textContent = 'mid';
  const last = document.createElement('button');
  last.id = 'last';
  last.textContent = 'last';
  modal.append(first, mid, last);
  document.body.appendChild(modal);

  return { modal, trigger, first, mid, last };
}

/**
 * Build the production-shaped plan-modal DOM (subset). We use
 * createElement rather than the innerHTML shorthand so the test file
 * stays free of XSS-flagged sinks (the security hook flags any
 * innerHTML write, even on test scaffolding).
 */
function mountPlanModalDOM(): HTMLButtonElement {
  const trigger = document.createElement('button');
  trigger.id = 'new-plan-btn';
  trigger.textContent = 'New Plan';
  document.body.appendChild(trigger);

  const modal = document.createElement('div');
  modal.id = 'plan-modal';
  modal.className = 'modal hidden';
  modal.setAttribute('role', 'dialog');
  modal.setAttribute('aria-modal', 'true');

  const content = document.createElement('div');
  content.className = 'modal-content';

  const h2 = document.createElement('h2');
  h2.id = 'plan-modal-title';
  h2.textContent = 'Create Purchase Plan';
  content.appendChild(h2);

  const form = document.createElement('form');
  form.id = 'plan-form';

  const mkInput = (id: string, type = 'text', value = ''): HTMLInputElement => {
    const i = document.createElement('input');
    i.type = type;
    i.id = id;
    if (value) i.value = value;
    return i;
  };
  const mkSelect = (id: string, optValue: string, optText: string): HTMLSelectElement => {
    const s = document.createElement('select');
    s.id = id;
    const opt = document.createElement('option');
    opt.value = optValue;
    opt.textContent = optText;
    s.appendChild(opt);
    return s;
  };
  const mkRadio = (name: string, value: string, checked = false): HTMLInputElement => {
    const r = document.createElement('input');
    r.type = 'radio';
    r.name = name;
    r.value = value;
    r.checked = checked;
    return r;
  };

  form.append(
    mkInput('plan-id', 'hidden'),
    mkInput('plan-name', 'text'),
  );
  const desc = document.createElement('textarea');
  desc.id = 'plan-description';
  form.appendChild(desc);
  form.append(
    mkSelect('plan-provider', 'aws', 'AWS'),
    mkSelect('plan-service', 'ec2', 'EC2'),
    mkSelect('plan-term', '1', '1y'),
    mkSelect('plan-payment', 'no_upfront', 'No Upfront'),
    mkInput('plan-coverage', 'number', '80'),
    mkRadio('ramp-schedule', 'immediate', true),
    mkRadio('ramp-schedule', 'custom'),
  );

  const customRamp = document.createElement('div');
  customRamp.id = 'custom-ramp-config';
  customRamp.append(
    mkInput('ramp-step-percent', 'number', '20'),
    mkInput('ramp-interval-days', 'number', '7'),
  );
  form.appendChild(customRamp);

  form.append(
    mkInput('plan-auto-purchase', 'checkbox'),
    mkInput('plan-notify-days', 'number', '3'),
    mkInput('plan-enabled', 'checkbox'),
    mkInput('plan-account-ids', 'hidden'),
    mkInput('plan-account-search', 'text'),
  );
  const suggestions = document.createElement('div');
  suggestions.id = 'plan-account-suggestions';
  suggestions.className = 'hidden';
  form.appendChild(suggestions);
  const selected = document.createElement('div');
  selected.id = 'plan-accounts-selected';
  form.appendChild(selected);

  const submitBtn = document.createElement('button');
  submitBtn.type = 'submit';
  submitBtn.textContent = 'Save';
  form.appendChild(submitBtn);

  const cancelBtn = document.createElement('button');
  cancelBtn.type = 'button';
  cancelBtn.id = 'close-plan-modal-btn';
  cancelBtn.textContent = 'Cancel';
  form.appendChild(cancelBtn);

  content.appendChild(form);
  modal.appendChild(content);
  document.body.appendChild(modal);

  return trigger;
}

function mountAccountModalDOM(): { trigger: HTMLButtonElement; modal: HTMLDivElement } {
  const trigger = document.createElement('button');
  trigger.id = 'add-account-btn';
  trigger.textContent = 'Add Account';
  document.body.appendChild(trigger);

  const modal = document.createElement('div');
  modal.id = 'account-modal';
  modal.className = 'modal hidden';
  modal.setAttribute('role', 'dialog');
  modal.setAttribute('aria-modal', 'true');

  const content = document.createElement('div');
  content.className = 'modal-content';

  const h2 = document.createElement('h2');
  h2.id = 'account-modal-title';
  h2.textContent = 'Add Account';
  content.appendChild(h2);

  const form = document.createElement('form');
  form.id = 'account-form';
  const idInput = document.createElement('input');
  idInput.type = 'hidden';
  idInput.id = 'account-id';
  const nameInput = document.createElement('input');
  nameInput.type = 'text';
  nameInput.id = 'account-name';
  const externalIdInput = document.createElement('input');
  externalIdInput.type = 'text';
  externalIdInput.id = 'account-external-id';
  const submit = document.createElement('button');
  submit.type = 'submit';
  submit.textContent = 'Save';
  const cancel = document.createElement('button');
  cancel.type = 'button';
  cancel.id = 'close-account-modal-btn';
  cancel.textContent = 'Cancel';
  form.append(idInput, nameInput, externalIdInput, submit, cancel);
  content.appendChild(form);
  modal.appendChild(content);
  document.body.appendChild(modal);

  return { trigger, modal };
}

// Each test mounts its own DOM via buildModal/mountPlanModalDOM/
// mountAccountModalDOM, all of which append to document.body. Reset
// between tests so a failure mid-test doesn't leak elements (or the
// active focus they own) into the next one. replaceChildren() rather
// than innerHTML to keep the file free of XSS-flagged sinks (the
// project's security hook flags any innerHTML write, even on test
// scaffolding — see the mountPlanModalDOM comment above).
afterEach(() => {
  document.body.replaceChildren();
});

describe('modal focus-trap helper', () => {
  describe('openModal', () => {
    test('removes hidden class', () => {
      const { modal } = buildModal();
      openModal(modal);
      expect(modal.classList.contains('hidden')).toBe(false);
    });

    test('focuses first focusable when no initialFocus is given', () => {
      const { modal, first } = buildModal();
      openModal(modal);
      expect(document.activeElement).toBe(first);
    });

    test('honours initialFocus option (HTMLElement)', () => {
      const { modal, mid } = buildModal();
      openModal(modal, { initialFocus: mid });
      expect(document.activeElement).toBe(mid);
    });

    test('honours initialFocus option (selector string)', () => {
      const { modal, last } = buildModal();
      openModal(modal, { initialFocus: '#last' });
      expect(document.activeElement).toBe(last);
    });
  });

  describe('focus trap', () => {
    test('Tab from last focusable wraps to first', () => {
      const { modal, first, last } = buildModal();
      openModal(modal);
      last.focus();
      expect(document.activeElement).toBe(last);

      const evt = dispatchKey(last, 'Tab');
      expect(evt.defaultPrevented).toBe(true);
      expect(document.activeElement).toBe(first);
    });

    test('Shift+Tab from first focusable wraps to last', () => {
      const { modal, first, last } = buildModal();
      openModal(modal);
      first.focus();
      expect(document.activeElement).toBe(first);

      const evt = dispatchKey(first, 'Tab', { shiftKey: true });
      expect(evt.defaultPrevented).toBe(true);
      expect(document.activeElement).toBe(last);
    });

    test('Tab in the middle is allowed through (browser handles native focus advance)', () => {
      const { modal, first } = buildModal();
      openModal(modal);
      first.focus();

      const evt = dispatchKey(first, 'Tab');
      // We only intercept at the edges; mid-stream Tab is the browser's
      // job, so the helper must not preventDefault here.
      expect(evt.defaultPrevented).toBe(false);
    });

    test('focus that escaped the modal subtree is pulled back on Tab', () => {
      const { modal, first, trigger } = buildModal();
      openModal(modal);
      // Simulate focus escaping (e.g. user clicked outside the modal).
      trigger.focus();
      expect(document.activeElement).toBe(trigger);

      // The keydown still has to arrive on the modal for the trap to
      // fire — that's the point of attaching the listener to the modal
      // root, so dispatch the event on the modal here.
      const evt = dispatchKey(modal, 'Tab');
      expect(evt.defaultPrevented).toBe(true);
      expect(document.activeElement).toBe(first);
    });
  });

  describe('Escape', () => {
    test('Escape calls closeModal (modal becomes hidden)', () => {
      const { modal } = buildModal();
      openModal(modal);
      expect(modal.classList.contains('hidden')).toBe(false);

      const evt = dispatchKey(modal, 'Escape');
      expect(evt.defaultPrevented).toBe(true);
      expect(modal.classList.contains('hidden')).toBe(true);
    });

    test('non-Escape, non-Tab keys are ignored', () => {
      const { modal, first } = buildModal();
      openModal(modal);
      first.focus();

      const evt = dispatchKey(first, 'a');
      expect(evt.defaultPrevented).toBe(false);
      expect(modal.classList.contains('hidden')).toBe(false);
    });
  });

  describe('focus restoration', () => {
    test('focus returns to the trigger on close', () => {
      const { modal, trigger } = buildModal();
      trigger.focus();
      expect(document.activeElement).toBe(trigger);

      openModal(modal);
      expect(document.activeElement).not.toBe(trigger);

      closeModal(modal);
      expect(document.activeElement).toBe(trigger);
    });

    test('skips restoration when the trigger has been removed from the DOM', () => {
      const { modal, trigger } = buildModal();
      trigger.focus();
      openModal(modal);
      // Simulate the trigger being unmounted while the modal was open.
      trigger.remove();
      // No throw, no jump anywhere weird — focus stays on whatever the
      // browser fell back to (jsdom: body).
      expect(() => closeModal(modal)).not.toThrow();
      expect(modal.classList.contains('hidden')).toBe(true);
    });
  });

  describe('teardown', () => {
    test('keydown handler is removed on close — Escape after close is a no-op', () => {
      const { modal } = buildModal();
      openModal(modal);
      closeModal(modal);
      expect(modal.classList.contains('hidden')).toBe(true);

      // Re-add to a clean state so we can observe whether the listener
      // is still wired.
      modal.classList.remove('hidden');
      const evt = dispatchKey(modal, 'Escape');
      expect(evt.defaultPrevented).toBe(false);
      // The class stays as we left it — the trap didn't re-hide it.
      expect(modal.classList.contains('hidden')).toBe(false);
    });

    test('opening twice on the same element does not stack listeners', () => {
      const { modal, trigger } = buildModal();
      trigger.focus();
      openModal(modal);
      // Snapshot whatever ended up focused after the first open — that
      // becomes the trigger recorded by the second open() call.
      const intermediate = document.activeElement as HTMLElement;
      openModal(modal);
      closeModal(modal);
      expect(document.activeElement).toBe(intermediate);
    });
  });
});

// ----- Call-site integration tests -----

describe('focus trap engages on real call sites', () => {
  test('openNewPlanModal — opens modal, traps focus, ESC closes', async () => {
    const trigger = mountPlanModalDOM();
    trigger.focus();

    // Lazy-import so the jest.mock setup at top-of-file is in effect.
    const plans = await import('../plans');
    plans.openNewPlanModal();

    const modal = document.getElementById('plan-modal') as HTMLDivElement;
    expect(modal.classList.contains('hidden')).toBe(false);

    // Focus should be inside the modal.
    expect(modal.contains(document.activeElement)).toBe(true);

    // Tab from the last focusable wraps to first. Reuses the canonical
    // selector exported from modal.ts so it can't drift from the impl.
    const focusables = modal.querySelectorAll<HTMLElement>(FOCUSABLE_SELECTOR);
    expect(focusables.length).toBeGreaterThan(1);
    const last = focusables[focusables.length - 1]!;
    const first = focusables[0]!;
    last.focus();
    dispatchKey(last, 'Tab');
    expect(document.activeElement).toBe(first);

    // ESC closes and restores focus to the trigger.
    dispatchKey(modal, 'Escape');
    expect(modal.classList.contains('hidden')).toBe(true);
    expect(document.activeElement).toBe(trigger);
  });

  test('account modal — openModal directly engages trap on the same DOM shape', () => {
    const { trigger, modal } = mountAccountModalDOM();
    trigger.focus();

    // The settings.openCreateAccountModal flow does provider-specific
    // population we don't need to re-execute here — the contract under
    // test is "openModal is what the call site uses", which we
    // exercise by calling it the same way settings.ts now does.
    openModal(modal);
    expect(modal.classList.contains('hidden')).toBe(false);
    expect(modal.contains(document.activeElement)).toBe(true);

    // ESC closes and restores focus.
    dispatchKey(modal, 'Escape');
    expect(modal.classList.contains('hidden')).toBe(true);
    expect(document.activeElement).toBe(trigger);
  });
});
