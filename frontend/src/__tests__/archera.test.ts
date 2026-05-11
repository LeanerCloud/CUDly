/**
 * Tests for the Archera Insurance CTA and education overlay (issue #314).
 *
 * Covers:
 *   - renderArcheraCTA: element structure, discernible name, click handler
 *   - openArcheraPage('what-is-archera'): Page A content, crosslink to B,
 *     signup link attributes, back button
 *   - openArcheraPage('how-it-works'): Page B content, crosslink to A,
 *     signup link attributes, back button
 *   - closeArcheraPage: hides and clears the container
 *   - openPurchaseModal: CTA is rendered inside #purchase-details
 *   - openCreatePlanModal / openNewPlanModal: CTA is injected once into
 *     #plan-modal; re-opening does not duplicate it
 */

import {
  renderArcheraCTA,
  openArcheraPage,
  closeArcheraPage,
  handleArcheraDeeplink,
  ARCHERA_SIGNUP_URL,
  ARCHERA_PAGE_A_PATH,
  ARCHERA_PAGE_B_PATH,
} from '../archera';
import { openPurchaseModal } from '../recommendations';
import { openCreatePlanModal, openNewPlanModal } from '../plans';

// ---------------------------------------------------------------------------
// Mocks required by recommendations.ts and plans.ts
// ---------------------------------------------------------------------------

jest.mock('../api', () => ({
  getRecommendations: jest.fn(),
  refreshRecommendations: jest.fn(),
  listAccounts: jest.fn().mockResolvedValue([]),
  getConfig: jest.fn().mockResolvedValue({ global: {} }),
  listAccountServiceOverrides: jest.fn().mockResolvedValue([]),
  getPlans: jest.fn(),
  getPlannedPurchases: jest.fn().mockResolvedValue({ purchases: [] }),
  listPlanAccounts: jest.fn().mockResolvedValue([]),
  setPlanAccounts: jest.fn().mockResolvedValue(undefined),
}));

jest.mock('../api/recommendations', () => ({
  getRecommendationDetail: jest.fn().mockResolvedValue({
    id: 'rec-default',
    usage_history: [],
    confidence_bucket: 'low',
    provenance_note: '',
  }),
  getRecommendationsFreshness: jest.fn().mockResolvedValue({
    last_collected_at: new Date(Date.now() - 60 * 60 * 1000).toISOString(),
    last_collection_error: null,
  }),
  refreshRecommendations: jest.fn().mockResolvedValue({}),
}));

jest.mock('../toast', () => ({
  showToast: jest.fn(() => ({ dismiss: jest.fn() })),
}));

jest.mock('../state', () => ({
  getCurrentProvider: jest.fn().mockReturnValue('all'),
  getRecommendations: jest.fn().mockReturnValue([]),
  getSelectedRecommendationIDs: jest.fn().mockReturnValue(new Set()),
  getVisibleRecommendations: jest.fn().mockReturnValue([]),
  setVisibleRecommendations: jest.fn(),
}));

jest.mock('../history', () => ({
  viewPlanHistory: jest.fn(),
}));

jest.mock('../commitmentOptions', () => ({
  populateTermSelect: jest.fn(),
  populatePaymentSelect: jest.fn(),
  isValidCombination: jest.fn().mockReturnValue(true),
  normalizePaymentValue: jest.fn((v: string) => v),
}));

jest.mock('../confirmDialog', () => ({
  confirmDialog: jest.fn(() => Promise.resolve(true)),
}));

// ---------------------------------------------------------------------------
// Global DOM teardown — prevents stale-node collisions between test suites.
// ---------------------------------------------------------------------------

afterEach(() => {
  ['archera-page-container', 'purchase-modal', 'plan-modal'].forEach(id => {
    document.getElementById(id)?.remove();
  });
  // Also remove any loose .archera-cta elements appended directly to body.
  document.querySelectorAll('.archera-cta').forEach(el => el.remove());
});

// ---------------------------------------------------------------------------
// DOM setup helpers
// ---------------------------------------------------------------------------

/** Minimal DOM structure used by the education overlay tests. */
function buildArcheraContainer(): void {
  const container = document.createElement('div');
  container.id = 'archera-page-container';
  container.className = 'hidden';
  document.body.appendChild(container);
}

/** Minimal DOM for openPurchaseModal: #purchase-details + #purchase-modal. */
function buildPurchaseModalDOM(): void {
  document.body.appendChild((() => {
    const modal = document.createElement('div');
    modal.id = 'purchase-modal';
    modal.className = 'modal hidden';

    const content = document.createElement('div');
    content.className = 'modal-content modal-wide';

    const details = document.createElement('div');
    details.id = 'purchase-details';
    content.appendChild(details);

    const buttons = document.createElement('div');
    buttons.className = 'modal-buttons';
    const cancelBtn = document.createElement('button');
    cancelBtn.id = 'close-purchase-modal-btn';
    cancelBtn.textContent = 'Cancel';
    buttons.appendChild(cancelBtn);
    const execBtn = document.createElement('button');
    execBtn.id = 'execute-purchase-btn';
    execBtn.textContent = 'Send for Approval';
    buttons.appendChild(execBtn);
    content.appendChild(buttons);

    modal.appendChild(content);
    return modal;
  })());
}

/** Minimal DOM for plan modal tests. */
function buildPlanModalDOM(): void {
  const modal = document.createElement('div');
  modal.id = 'plan-modal';
  modal.className = 'modal hidden';
  modal.setAttribute('role', 'dialog');
  modal.setAttribute('aria-modal', 'true');

  const content = document.createElement('div');
  content.className = 'modal-content modal-wide';

  const h2 = document.createElement('h2');
  h2.id = 'plan-modal-title';
  h2.textContent = 'Create Purchase Plan';
  content.appendChild(h2);

  const form = document.createElement('form');
  form.id = 'plan-form';

  // Required hidden input
  const planId = document.createElement('input');
  planId.type = 'hidden';
  planId.id = 'plan-id';
  form.appendChild(planId);

  // Modal buttons (must exist so injectPlanModalCTA can find them)
  const buttons = document.createElement('div');
  buttons.className = 'modal-buttons';
  const cancelBtn = document.createElement('button');
  cancelBtn.id = 'close-plan-modal-btn';
  cancelBtn.textContent = 'Cancel';
  buttons.appendChild(cancelBtn);
  const saveBtn = document.createElement('button');
  saveBtn.type = 'submit';
  saveBtn.textContent = 'Save Plan';
  buttons.appendChild(saveBtn);
  form.appendChild(buttons);

  content.appendChild(form);
  modal.appendChild(content);
  document.body.appendChild(modal);
}

// ---------------------------------------------------------------------------
// renderArcheraCTA
// ---------------------------------------------------------------------------

describe('renderArcheraCTA', () => {
  it('returns a <p> element with class archera-cta', () => {
    const el = renderArcheraCTA();
    expect(el.tagName).toBe('P');
    expect(el.classList.contains('archera-cta')).toBe(true);
  });

  it('contains the "Insure this commitment with Archera" CTA text for default context', () => {
    const el = renderArcheraCTA();
    expect(el.textContent).toContain('Insure this commitment with Archera');
  });

  it('contains the "Insure this plan with Archera" CTA text for plan context', () => {
    const el = renderArcheraCTA('plan');
    expect(el.textContent).toContain('Insure this plan with Archera');
  });

  it('contains a button with discernible name to trigger the overlay', () => {
    const el = renderArcheraCTA();
    const btn = el.querySelector('button.archera-cta-link');
    expect(btn).not.toBeNull();
    expect(btn!.textContent).toMatch(/Archera/i);
  });

  it('opens Page A when the CTA button is clicked', () => {
    buildArcheraContainer();
    const el = renderArcheraCTA();
    document.body.appendChild(el);

    const btn = el.querySelector<HTMLButtonElement>('button.archera-cta-link')!;
    btn.click();

    const container = document.getElementById('archera-page-container')!;
    expect(container.classList.contains('hidden')).toBe(false);
    expect(container.textContent).toContain('What is Archera Insurance?');
  });
});

// ---------------------------------------------------------------------------
// Education overlay — Page A
// ---------------------------------------------------------------------------

describe('openArcheraPage("what-is-archera") — Page A', () => {
  beforeEach(() => {
    buildArcheraContainer();
  });

  it('makes #archera-page-container visible', () => {
    openArcheraPage('what-is-archera');
    const container = document.getElementById('archera-page-container')!;
    expect(container.classList.contains('hidden')).toBe(false);
  });

  it('renders the Page A heading', () => {
    openArcheraPage('what-is-archera');
    const h1 = document.querySelector('#archera-page-container h1');
    expect(h1?.textContent).toBe('What is Archera Insurance?');
  });

  it('contains a "How it works" section', () => {
    openArcheraPage('what-is-archera');
    expect(document.getElementById('archera-page-container')!.textContent).toContain(
      'How it works',
    );
  });

  it('contains a signup link with correct href, target=_blank, and rel=noopener noreferrer', () => {
    openArcheraPage('what-is-archera');
    const link = document.querySelector<HTMLAnchorElement>(
      '#archera-page-container a.archera-signup-btn',
    );
    expect(link).not.toBeNull();
    expect(link!.href).toBe(ARCHERA_SIGNUP_URL);
    expect(link!.target).toBe('_blank');
    expect(link!.rel).toBe('noopener noreferrer');
  });

  it('has a cross-link button to Page B', () => {
    openArcheraPage('what-is-archera');
    const btns = document.querySelectorAll<HTMLButtonElement>(
      '#archera-page-container button.archera-cta-link',
    );
    const toB = Array.from(btns).find(b => b.textContent?.includes('integration works'));
    expect(toB).not.toBeUndefined();
  });

  it('clicking the cross-link navigates to Page B', () => {
    openArcheraPage('what-is-archera');
    const btns = document.querySelectorAll<HTMLButtonElement>(
      '#archera-page-container button.archera-cta-link',
    );
    const toB = Array.from(btns).find(b => b.textContent?.includes('integration works'))!;
    toB.click();
    const h1 = document.querySelector('#archera-page-container h1');
    expect(h1?.textContent).toContain('integration works');
  });

  it('has a back button that closes the overlay', () => {
    openArcheraPage('what-is-archera');
    const back = document.querySelector<HTMLButtonElement>(
      '#archera-page-container .archera-page-back',
    )!;
    expect(back).not.toBeNull();
    back.click();
    const container = document.getElementById('archera-page-container')!;
    expect(container.classList.contains('hidden')).toBe(true);
  });
});

// ---------------------------------------------------------------------------
// Education overlay — Page B
// ---------------------------------------------------------------------------

describe('openArcheraPage("how-it-works") — Page B', () => {
  beforeEach(() => {
    buildArcheraContainer();
  });

  it('renders the Page B heading', () => {
    openArcheraPage('how-it-works');
    const h1 = document.querySelector('#archera-page-container h1');
    expect(h1?.textContent).toContain('integration works');
  });

  it('contains a step list', () => {
    openArcheraPage('how-it-works');
    const ol = document.querySelector('#archera-page-container ol.archera-steps');
    expect(ol).not.toBeNull();
    expect(ol!.querySelectorAll('li').length).toBeGreaterThanOrEqual(3);
  });

  it('contains a signup link with correct href, target=_blank, and rel=noopener noreferrer', () => {
    openArcheraPage('how-it-works');
    const link = document.querySelector<HTMLAnchorElement>(
      '#archera-page-container a.archera-signup-btn',
    );
    expect(link).not.toBeNull();
    expect(link!.href).toBe(ARCHERA_SIGNUP_URL);
    expect(link!.target).toBe('_blank');
    expect(link!.rel).toBe('noopener noreferrer');
  });

  it('has a cross-link button back to Page A', () => {
    openArcheraPage('how-it-works');
    const btns = document.querySelectorAll<HTMLButtonElement>(
      '#archera-page-container button.archera-cta-link',
    );
    const toA = Array.from(btns).find(b => b.textContent?.includes('What is Archera'));
    expect(toA).not.toBeUndefined();
  });

  it('clicking the cross-link navigates back to Page A', () => {
    openArcheraPage('how-it-works');
    const btns = document.querySelectorAll<HTMLButtonElement>(
      '#archera-page-container button.archera-cta-link',
    );
    const toA = Array.from(btns).find(b => b.textContent?.includes('What is Archera'))!;
    toA.click();
    const h1 = document.querySelector('#archera-page-container h1');
    expect(h1?.textContent).toBe('What is Archera Insurance?');
  });

  it('has a back button that closes the overlay', () => {
    openArcheraPage('how-it-works');
    const back = document.querySelector<HTMLButtonElement>(
      '#archera-page-container .archera-page-back',
    )!;
    back.click();
    const container = document.getElementById('archera-page-container')!;
    expect(container.classList.contains('hidden')).toBe(true);
  });
});

// ---------------------------------------------------------------------------
// closeArcheraPage
// ---------------------------------------------------------------------------

describe('closeArcheraPage', () => {
  it('adds .hidden and clears content', () => {
    buildArcheraContainer();
    openArcheraPage('what-is-archera');
    closeArcheraPage();
    const container = document.getElementById('archera-page-container')!;
    expect(container.classList.contains('hidden')).toBe(true);
    expect(container.childNodes.length).toBe(0);
  });
});

// ---------------------------------------------------------------------------
// openPurchaseModal — CTA in purchase-details
// ---------------------------------------------------------------------------

describe('openPurchaseModal — Archera CTA', () => {
  beforeEach(() => {
    buildPurchaseModalDOM();
    buildArcheraContainer();
  });

  it('renders .archera-cta inside #purchase-details', async () => {
    await openPurchaseModal([]);
    const details = document.getElementById('purchase-details')!;
    const cta = details.querySelector('.archera-cta');
    expect(cta).not.toBeNull();
  });

  it('CTA text mentions Archera', async () => {
    await openPurchaseModal([]);
    const cta = document.querySelector('#purchase-details .archera-cta')!;
    expect(cta.textContent).toContain('Archera');
    expect(cta.textContent).toContain('Insure this commitment');
  });

  it('CTA is re-rendered fresh on each modal open (no duplication)', async () => {
    await openPurchaseModal([]);
    await openPurchaseModal([]);
    const ctas = document.querySelectorAll('#purchase-details .archera-cta');
    expect(ctas.length).toBe(1);
  });
});

// ---------------------------------------------------------------------------
// openCreatePlanModal / openNewPlanModal — CTA in plan modal
// ---------------------------------------------------------------------------

describe('openCreatePlanModal — Archera CTA', () => {
  beforeEach(() => {
    buildPlanModalDOM();
    buildArcheraContainer();
  });

  it('injects #archera-plan-cta into #plan-modal', () => {
    openCreatePlanModal();
    const cta = document.getElementById('archera-plan-cta');
    expect(cta).not.toBeNull();
    expect(cta!.textContent).toContain('Archera');
    expect(cta!.textContent).toContain('Insure this plan');
  });

  it('CTA appears before .modal-buttons', () => {
    openCreatePlanModal();
    const form = document.getElementById('plan-form')!;
    const children = Array.from(form.children);
    const ctaIdx = children.findIndex(el => el.id === 'archera-plan-cta');
    const btnsIdx = children.findIndex(el => el.classList.contains('modal-buttons'));
    expect(ctaIdx).toBeGreaterThanOrEqual(0);
    expect(btnsIdx).toBeGreaterThanOrEqual(0);
    expect(ctaIdx).toBeLessThan(btnsIdx);
  });

  it('does not duplicate the CTA on repeated opens', () => {
    openCreatePlanModal();
    openCreatePlanModal();
    const ctas = document.querySelectorAll('#plan-modal .archera-cta');
    expect(ctas.length).toBe(1);
  });
});

describe('openNewPlanModal — Archera CTA', () => {
  beforeEach(() => {
    buildPlanModalDOM();
    buildArcheraContainer();
  });

  it('injects #archera-plan-cta into #plan-modal', () => {
    openNewPlanModal();
    const cta = document.getElementById('archera-plan-cta');
    expect(cta).not.toBeNull();
    expect(cta!.textContent).toContain('Archera');
    expect(cta!.textContent).toContain('Insure this plan');
  });

  it('does not duplicate the CTA if opened multiple times', () => {
    openNewPlanModal();
    openNewPlanModal();
    const ctas = document.querySelectorAll('#plan-modal .archera-cta');
    expect(ctas.length).toBe(1);
  });
});

// ---------------------------------------------------------------------------
// Transparency disclosures — Page A and Page B
// ---------------------------------------------------------------------------

describe('transparency disclosures', () => {
  beforeEach(() => {
    buildArcheraContainer();
  });

  it('Page A contains "CUDly works fully without Archera" fact', () => {
    openArcheraPage('what-is-archera');
    const text = document.getElementById('archera-page-container')!.textContent!;
    expect(text).toMatch(/fully without Archera/i);
  });

  it('Page A contains the Archera sponsorship fact', () => {
    openArcheraPage('what-is-archera');
    const text = document.getElementById('archera-page-container')!.textContent!;
    expect(text).toMatch(/sponsors/i);
    expect(text).toMatch(/revenue/i);
  });

  it('Page A disclosure heading reads "Why is CUDly telling me about this?"', () => {
    openArcheraPage('what-is-archera');
    const container = document.getElementById('archera-page-container')!;
    const disclosure = container.querySelector('.archera-disclosure h2');
    expect(disclosure?.textContent).toMatch(/Why is CUDly telling me about this/i);
  });

  it('Page B contains the "CUDly works fully without Archera" fact', () => {
    openArcheraPage('how-it-works');
    const text = document.getElementById('archera-page-container')!.textContent!;
    expect(text).toMatch(/fully without Archera/i);
  });

  it('Page B contains the Archera sponsorship fact', () => {
    openArcheraPage('how-it-works');
    const text = document.getElementById('archera-page-container')!.textContent!;
    expect(text).toMatch(/sponsors/i);
  });

  it('Page B disclosure heading reads "Disclosure"', () => {
    openArcheraPage('how-it-works');
    const container = document.getElementById('archera-page-container')!;
    const disclosure = container.querySelector('.archera-disclosure h2');
    expect(disclosure?.textContent).toBe('Disclosure');
  });
});

// ---------------------------------------------------------------------------
// handleArcheraDeeplink
// ---------------------------------------------------------------------------

describe('handleArcheraDeeplink', () => {
  beforeEach(() => {
    buildArcheraContainer();
  });

  it('returns false and does not open overlay for a non-Archera path', () => {
    Object.defineProperty(window, 'location', {
      value: { pathname: '/dashboard' },
      writable: true,
      configurable: true,
    });
    const result = handleArcheraDeeplink();
    expect(result).toBe(false);
    const container = document.getElementById('archera-page-container')!;
    expect(container.classList.contains('hidden')).toBe(true);
  });

  it('returns true and opens Page A for ARCHERA_PAGE_A_PATH', () => {
    Object.defineProperty(window, 'location', {
      value: { pathname: ARCHERA_PAGE_A_PATH },
      writable: true,
      configurable: true,
    });
    const result = handleArcheraDeeplink();
    expect(result).toBe(true);
    const container = document.getElementById('archera-page-container')!;
    expect(container.classList.contains('hidden')).toBe(false);
    expect(container.querySelector('h1')?.textContent).toBe('What is Archera Insurance?');
  });

  it('returns true and opens Page B for ARCHERA_PAGE_B_PATH', () => {
    Object.defineProperty(window, 'location', {
      value: { pathname: ARCHERA_PAGE_B_PATH },
      writable: true,
      configurable: true,
    });
    const result = handleArcheraDeeplink();
    expect(result).toBe(true);
    const container = document.getElementById('archera-page-container')!;
    expect(container.classList.contains('hidden')).toBe(false);
    expect(container.querySelector('h1')?.textContent).toContain('integration works');
  });
});
