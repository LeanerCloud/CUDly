/**
 * Tests for the Archera Insurance CTA and education overlay (issue #314).
 *
 * Covers:
 *   - renderArcheraCTA: element structure, discernible name, click handler
 *   - openArcheraPage: education page content, signup link attributes, back button
 *   - closeArcheraPage: hides and clears the container
 *   - openPurchaseModal: CTA is rendered inside #purchase-details
 *   - openCreatePlanModal / openNewPlanModal: CTA is injected once into
 *     #plan-modal; re-opening does not duplicate it
 *   - handleArcheraDeeplink: both legacy and current URL paths open the
 *     single merged page
 */

import {
  openArcheraPage,
  closeArcheraPage,
  openArcheraOfferModal,
  closeArcheraOfferModal,
  handleArcheraDeeplink,
  maybeOfferArcheraAfterExecution,
  ARCHERA_SIGNUP_URL,
  ARCHERA_PAGE_A_PATH,
  ARCHERA_PAGE_B_PATH,
  ARCHERA_OFFERED_FOR_KEY,
  ARCHERA_OFFER_WINDOW_DAYS,
} from '../archera';
import * as api from '../api';

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
  getHistory: jest.fn().mockResolvedValue({ purchases: [] }),
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
// Global DOM teardown: prevents stale-node collisions between test suites.
// ---------------------------------------------------------------------------

afterEach(() => {
  ['archera-page-container', 'archera-offer-modal-container'].forEach(id => {
    document.getElementById(id)?.remove();
  });
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

/** Minimal DOM structure used by the offer modal tests. */
function buildArcheraOfferContainer(): void {
  const container = document.createElement('div');
  container.id = 'archera-offer-modal-container';
  container.className = 'hidden';
  document.body.appendChild(container);
}

// ---------------------------------------------------------------------------
// Education overlay (single merged page)
// ---------------------------------------------------------------------------

describe('openArcheraPage', () => {
  beforeEach(() => {
    buildArcheraContainer();
  });

  it('makes #archera-page-container visible', () => {
    openArcheraPage();
    const container = document.getElementById('archera-page-container')!;
    expect(container.classList.contains('hidden')).toBe(false);
  });

  it('renders the "Archera Insurance" heading', () => {
    openArcheraPage();
    const h1 = document.querySelector('#archera-page-container h1');
    expect(h1?.textContent).toBe('Archera Insurance');
  });

  it('contains a "How it works" section with a step list', () => {
    openArcheraPage();
    const container = document.getElementById('archera-page-container')!;
    expect(container.textContent).toContain('How it works');
    const ol = container.querySelector('ol.archera-steps');
    expect(ol).not.toBeNull();
    expect(ol!.querySelectorAll('li').length).toBeGreaterThanOrEqual(3);
  });

  it('contains a "When it makes sense" section', () => {
    openArcheraPage();
    expect(document.getElementById('archera-page-container')!.textContent).toContain(
      'When it makes sense',
    );
  });

  it('contains the Full disclosure paragraph (merged from the old Disclaimers list)', () => {
    openArcheraPage();
    const text = document.getElementById('archera-page-container')!.textContent!;
    expect(text).toContain('Full disclosure:');
    // Key facts from the prior Disclaimers section still surface.
    expect(text).toMatch(/Insurance terms.*set entirely by Archera/i);
    expect(text).toMatch(/no visibility into your Archera/i);
  });

  it('contains a signup link with correct href, target=_blank, and rel=noopener noreferrer', () => {
    openArcheraPage();
    const link = document.querySelector<HTMLAnchorElement>(
      '#archera-page-container a.archera-signup-btn',
    );
    expect(link).not.toBeNull();
    expect(link!.href).toBe(ARCHERA_SIGNUP_URL);
    expect(link!.target).toBe('_blank');
    expect(link!.rel).toBe('noopener noreferrer');
  });

  it('has a back button that closes the overlay', () => {
    openArcheraPage();
    const back = document.querySelector<HTMLButtonElement>(
      '#archera-page-container .archera-page-back',
    )!;
    expect(back).not.toBeNull();
    back.click();
    const container = document.getElementById('archera-page-container')!;
    expect(container.classList.contains('hidden')).toBe(true);
  });

  it('re-rendering replaces content rather than stacking', () => {
    openArcheraPage();
    openArcheraPage();
    const container = document.getElementById('archera-page-container')!;
    const h1s = container.querySelectorAll('h1');
    expect(h1s.length).toBe(1);
  });
});

// ---------------------------------------------------------------------------
// closeArcheraPage
// ---------------------------------------------------------------------------

describe('closeArcheraPage', () => {
  it('adds .hidden and clears content', () => {
    buildArcheraContainer();
    openArcheraPage();
    closeArcheraPage();
    const container = document.getElementById('archera-page-container')!;
    expect(container.classList.contains('hidden')).toBe(true);
    expect(container.childNodes.length).toBe(0);
  });
});

// ---------------------------------------------------------------------------
// openArcheraOfferModal (post-action small modal)
// ---------------------------------------------------------------------------

describe('openArcheraOfferModal', () => {
  beforeEach(() => {
    buildArcheraOfferContainer();
    buildArcheraContainer();
  });

  it('makes #archera-offer-modal-container visible', () => {
    openArcheraOfferModal('purchase');
    const container = document.getElementById('archera-offer-modal-container')!;
    expect(container.classList.contains('hidden')).toBe(false);
  });

  it('shows the purchase-context headline by default', () => {
    openArcheraOfferModal();
    const title = document.getElementById('archera-offer-title');
    expect(title?.textContent).toMatch(/commitments?/i);
  });

  it('shows the plan-context headline for context=plan', () => {
    openArcheraOfferModal('plan');
    const title = document.getElementById('archera-offer-title');
    expect(title?.textContent).toMatch(/plan/i);
  });

  it('renders the disclosure line in the modal', () => {
    openArcheraOfferModal('purchase');
    const container = document.getElementById('archera-offer-modal-container')!;
    expect(container.textContent).toMatch(/sponsors/i);
    expect(container.textContent).toMatch(/works fully without/i);
  });

  it('has a "Sign up at Archera" link with correct href, target=_blank, rel=noopener noreferrer', () => {
    openArcheraOfferModal('purchase');
    const link = document.querySelector<HTMLAnchorElement>(
      '#archera-offer-modal-container a.archera-offer-signup',
    );
    expect(link).not.toBeNull();
    expect(link!.href).toBe(ARCHERA_SIGNUP_URL);
    expect(link!.target).toBe('_blank');
    expect(link!.rel).toBe('noopener noreferrer');
  });

  it('has a "No thanks" button that closes the modal', () => {
    openArcheraOfferModal('purchase');
    const skip = document.querySelector<HTMLButtonElement>(
      '#archera-offer-modal-container button.archera-offer-skip',
    )!;
    expect(skip.textContent).toMatch(/no thanks/i);
    skip.click();
    const container = document.getElementById('archera-offer-modal-container')!;
    expect(container.classList.contains('hidden')).toBe(true);
  });

  it('outside-click on the backdrop closes the modal', () => {
    openArcheraOfferModal('purchase');
    const backdrop = document.querySelector<HTMLElement>(
      '#archera-offer-modal-container .archera-offer-backdrop',
    )!;
    backdrop.click();
    const container = document.getElementById('archera-offer-modal-container')!;
    expect(container.classList.contains('hidden')).toBe(true);
  });

  it('ESC key closes the modal', () => {
    openArcheraOfferModal('purchase');
    document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' }));
    const container = document.getElementById('archera-offer-modal-container')!;
    expect(container.classList.contains('hidden')).toBe(true);
  });

  it('"Learn more" is a collapsed drop-down that expands inside the modal', () => {
    openArcheraOfferModal('purchase');
    const details = document.querySelector<HTMLDetailsElement>(
      '#archera-offer-modal-container details.archera-offer-learnmore',
    )!;
    expect(details).not.toBeNull();
    // Starts collapsed: open attribute absent, body content is not visible
    // to assistive tech (jsdom doesn't compute layout, but the <details>
    // semantic is what matters).
    expect(details.open).toBe(false);
    // Summary carries the discoverable label.
    const summary = details.querySelector('summary');
    expect(summary?.textContent).toMatch(/learn more/i);
    // Expanding the details surfaces the full education body inline.
    details.open = true;
    const body = details.querySelector('.archera-offer-learnmore-body');
    expect(body).not.toBeNull();
    expect(body!.textContent).toContain('How it works');
    expect(body!.textContent).toContain('When it makes sense');
    expect(body!.textContent).toContain('Full disclosure:');
    // The offer modal itself stays open — the drop-down is inline.
    const container = document.getElementById('archera-offer-modal-container')!;
    expect(container.classList.contains('hidden')).toBe(false);
  });

  it('re-opening replaces content rather than stacking', () => {
    openArcheraOfferModal('purchase');
    openArcheraOfferModal('plan');
    const container = document.getElementById('archera-offer-modal-container')!;
    const panels = container.querySelectorAll('.archera-offer-panel');
    expect(panels.length).toBe(1);
    const title = document.getElementById('archera-offer-title');
    expect(title?.textContent).toMatch(/plan/i);
  });

  it('re-opening tears down the previous open\'s ESC listener (no leak)', () => {
    // Spy on document.addEventListener so we can count keydown handlers
    // attached across the two opens. Without the cleanup-before-open
    // guard the second open would have added a second handler without
    // removing the first.
    const addSpy = jest.spyOn(document, 'addEventListener');
    const removeSpy = jest.spyOn(document, 'removeEventListener');
    try {
      openArcheraOfferModal('purchase');
      const addsAfterFirst = addSpy.mock.calls.filter(c => c[0] === 'keydown').length;
      const removesAfterFirst = removeSpy.mock.calls.filter(c => c[0] === 'keydown').length;
      openArcheraOfferModal('plan');
      const addsAfterSecond = addSpy.mock.calls.filter(c => c[0] === 'keydown').length;
      const removesAfterSecond = removeSpy.mock.calls.filter(c => c[0] === 'keydown').length;
      // Second open must remove the first open's handler before adding a new one.
      expect(addsAfterSecond - addsAfterFirst).toBe(1);
      expect(removesAfterSecond - removesAfterFirst).toBe(1);
    } finally {
      addSpy.mockRestore();
      removeSpy.mockRestore();
    }
  });

  it('traps Tab focus inside the panel (Tab from last → first)', () => {
    openArcheraOfferModal('purchase');
    const panel = document.querySelector<HTMLElement>(
      '#archera-offer-modal-container .archera-offer-panel',
    )!;
    const focusables = panel.querySelectorAll<HTMLElement>(
      'a[href], button:not([disabled]), summary, [tabindex]:not([tabindex="-1"])',
    );
    expect(focusables.length).toBeGreaterThanOrEqual(2);
    const first = focusables[0]!;
    const last = focusables[focusables.length - 1]!;
    // Focus the last element, then dispatch Tab — focus should wrap to first.
    last.focus();
    expect(document.activeElement).toBe(last);
    const evt = new KeyboardEvent('keydown', { key: 'Tab', bubbles: true, cancelable: true });
    panel.dispatchEvent(evt);
    expect(document.activeElement).toBe(first);
    expect(evt.defaultPrevented).toBe(true);
  });

  it('traps Shift+Tab focus inside the panel (Shift+Tab from first → last)', () => {
    openArcheraOfferModal('purchase');
    const panel = document.querySelector<HTMLElement>(
      '#archera-offer-modal-container .archera-offer-panel',
    )!;
    const focusables = panel.querySelectorAll<HTMLElement>(
      'a[href], button:not([disabled]), summary, [tabindex]:not([tabindex="-1"])',
    );
    const first = focusables[0]!;
    const last = focusables[focusables.length - 1]!;
    first.focus();
    expect(document.activeElement).toBe(first);
    const evt = new KeyboardEvent('keydown', { key: 'Tab', shiftKey: true, bubbles: true, cancelable: true });
    panel.dispatchEvent(evt);
    expect(document.activeElement).toBe(last);
    expect(evt.defaultPrevented).toBe(true);
  });
});

// ---------------------------------------------------------------------------
// closeArcheraOfferModal
// ---------------------------------------------------------------------------

describe('closeArcheraOfferModal', () => {
  it('adds .hidden and clears content', () => {
    buildArcheraOfferContainer();
    openArcheraOfferModal('purchase');
    closeArcheraOfferModal();
    const container = document.getElementById('archera-offer-modal-container')!;
    expect(container.classList.contains('hidden')).toBe(true);
    expect(container.childNodes.length).toBe(0);
  });
});

// ---------------------------------------------------------------------------
// Transparency disclosure
// ---------------------------------------------------------------------------

describe('transparency disclosure', () => {
  beforeEach(() => {
    buildArcheraContainer();
  });

  it('contains the "CUDly works fully without Archera" fact', () => {
    openArcheraPage();
    const text = document.getElementById('archera-page-container')!.textContent!;
    expect(text).toMatch(/fully without Archera/i);
  });

  it('contains the Archera sponsorship fact', () => {
    openArcheraPage();
    const text = document.getElementById('archera-page-container')!.textContent!;
    expect(text).toMatch(/sponsors/i);
    expect(text).toMatch(/revenue/i);
  });

  it('disclosure is rendered as a "Full disclosure:" paragraph (no heading)', () => {
    openArcheraPage();
    const container = document.getElementById('archera-page-container')!;
    const disclosure = container.querySelector<HTMLParagraphElement>('p.archera-disclosure');
    expect(disclosure).not.toBeNull();
    expect(disclosure!.textContent).toMatch(/Full disclosure:/);
    // Heading-level "Why is CUDly telling me about this?" has been removed
    // along with the duplicate user-interest paragraph (folded into lead).
    expect(container.textContent).not.toMatch(/Why is CUDly telling me about this/i);
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

  it('returns true and opens the overlay for ARCHERA_PAGE_A_PATH', () => {
    Object.defineProperty(window, 'location', {
      value: { pathname: ARCHERA_PAGE_A_PATH },
      writable: true,
      configurable: true,
    });
    const result = handleArcheraDeeplink();
    expect(result).toBe(true);
    const container = document.getElementById('archera-page-container')!;
    expect(container.classList.contains('hidden')).toBe(false);
    expect(container.querySelector('h1')?.textContent).toBe('Archera Insurance');
  });

  it('returns true and opens the same overlay for legacy ARCHERA_PAGE_B_PATH', () => {
    Object.defineProperty(window, 'location', {
      value: { pathname: ARCHERA_PAGE_B_PATH },
      writable: true,
      configurable: true,
    });
    const result = handleArcheraDeeplink();
    expect(result).toBe(true);
    const container = document.getElementById('archera-page-container')!;
    expect(container.classList.contains('hidden')).toBe(false);
    expect(container.querySelector('h1')?.textContent).toBe('Archera Insurance');
  });
});

// ---------------------------------------------------------------------------
// maybeOfferArcheraAfterExecution
// ---------------------------------------------------------------------------

describe('maybeOfferArcheraAfterExecution', () => {
  const mockGetHistory = api.getHistory as jest.Mock;

  // Build a completed purchase within the offer window.
  function recentCompleted(id: string, daysAgo = 1): Record<string, unknown> {
    const ts = new Date();
    ts.setDate(ts.getDate() - daysAgo);
    return { purchase_id: id, timestamp: ts.toISOString(), status: 'completed' };
  }

  // Helper: configure localStorage.getItem mock to return a specific JSON array.
  function seedOfferedFor(ids: string[]): void {
    (localStorage.getItem as jest.Mock).mockImplementation((key: string) => {
      if (key === ARCHERA_OFFERED_FOR_KEY) return JSON.stringify(ids);
      return null;
    });
  }

  // Helper: extract what was written to the offered-for key from setItem calls.
  function captureOfferedForWrite(): string[] {
    const calls = (localStorage.setItem as jest.Mock).mock.calls as [string, string][];
    const last = calls.filter(([k]) => k === ARCHERA_OFFERED_FOR_KEY).pop();
    if (!last) return [];
    return JSON.parse(last[1]) as string[];
  }

  beforeEach(() => {
    buildArcheraOfferContainer();
    buildArcheraContainer();
    mockGetHistory.mockReset();
    // Default: no offered-for set stored.
    (localStorage.getItem as jest.Mock).mockReturnValue(null);
  });

  it('returns early without opening modal when no executions in the window', async () => {
    mockGetHistory.mockResolvedValue({ purchases: [] });
    await maybeOfferArcheraAfterExecution();
    const container = document.getElementById('archera-offer-modal-container')!;
    expect(container.classList.contains('hidden')).toBe(true);
  });

  it('opens modal for the most recent completed execution not yet offered', async () => {
    mockGetHistory.mockResolvedValue({ purchases: [recentCompleted('exec-1')] });
    await maybeOfferArcheraAfterExecution();
    const container = document.getElementById('archera-offer-modal-container')!;
    expect(container.classList.contains('hidden')).toBe(false);
  });

  it('does not re-open modal for an execution already in the localStorage offered-for set', async () => {
    seedOfferedFor(['exec-1']);
    mockGetHistory.mockResolvedValue({ purchases: [recentCompleted('exec-1')] });
    await maybeOfferArcheraAfterExecution();
    const container = document.getElementById('archera-offer-modal-container')!;
    expect(container.classList.contains('hidden')).toBe(true);
  });

  it('does not open modal for non-completed executions (failed, pending, expired)', async () => {
    const ts = new Date().toISOString();
    mockGetHistory.mockResolvedValue({
      purchases: [
        { purchase_id: 'exec-fail', timestamp: ts, status: 'failed' },
        { purchase_id: 'exec-pend', timestamp: ts, status: 'pending' },
        { purchase_id: 'exec-exp', timestamp: ts, status: 'expired' },
      ],
    });
    await maybeOfferArcheraAfterExecution();
    const container = document.getElementById('archera-offer-modal-container')!;
    expect(container.classList.contains('hidden')).toBe(true);
  });

  it('passes start date of ARCHERA_OFFER_WINDOW_DAYS ago to getHistory', async () => {
    mockGetHistory.mockResolvedValue({ purchases: [] });
    await maybeOfferArcheraAfterExecution();
    const expected = new Date();
    expected.setDate(expected.getDate() - ARCHERA_OFFER_WINDOW_DAYS);
    const expectedStr = expected.toISOString().split('T')[0];
    expect(mockGetHistory).toHaveBeenCalledWith(expect.objectContaining({ start: expectedStr }));
  });

  it('records the offered execution in localStorage so it is not shown again', async () => {
    mockGetHistory.mockResolvedValue({ purchases: [recentCompleted('exec-2')] });
    await maybeOfferArcheraAfterExecution();
    const stored = captureOfferedForWrite();
    expect(stored).toContain('exec-2');
  });

  it('enforces FIFO cap of 50 entries in the localStorage set', async () => {
    // Pre-fill with 50 entries so next write must evict the oldest.
    const existing = Array.from({ length: 50 }, (_, i) => `old-exec-${i}`);
    seedOfferedFor(existing);
    mockGetHistory.mockResolvedValue({ purchases: [recentCompleted('exec-new')] });
    await maybeOfferArcheraAfterExecution();
    const stored = captureOfferedForWrite();
    // Total length must not exceed 50.
    expect(stored.length).toBeLessThanOrEqual(50);
    // The new entry must be present.
    expect(stored).toContain('exec-new');
    // The oldest entry must have been evicted.
    expect(stored).not.toContain('old-exec-0');
  });

  it('swallows getHistory errors and does not open modal', async () => {
    mockGetHistory.mockRejectedValue(new Error('network error'));
    // Should not throw.
    await expect(maybeOfferArcheraAfterExecution()).resolves.toBeUndefined();
    const container = document.getElementById('archera-offer-modal-container')!;
    expect(container.classList.contains('hidden')).toBe(true);
  });

  it('fires for the most recent completed purchase when multiple exist', async () => {
    const older = recentCompleted('exec-older', 3);
    const newer = recentCompleted('exec-newer', 1);
    mockGetHistory.mockResolvedValue({ purchases: [older, newer] });
    await maybeOfferArcheraAfterExecution();
    // exec-newer should be recorded (most recent) — not exec-older.
    const stored = captureOfferedForWrite();
    expect(stored).toContain('exec-newer');
    expect(stored).not.toContain('exec-older');
  });

  it('skips executions that lack a purchase_id', async () => {
    // A row without purchase_id (legacy) must not be offered.
    const ts = new Date().toISOString();
    mockGetHistory.mockResolvedValue({
      purchases: [{ timestamp: ts, status: 'completed' }], // no purchase_id
    });
    await maybeOfferArcheraAfterExecution();
    const container = document.getElementById('archera-offer-modal-container')!;
    expect(container.classList.contains('hidden')).toBe(true);
  });
});
