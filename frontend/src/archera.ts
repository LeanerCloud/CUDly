/**
 * Archera Insurance integration: post-action offer modal + education page.
 *
 * This module provides:
 *   - openArcheraOfferModal(context): small modal shown AFTER a successful
 *     purchase-approval submission or plan creation. Offers "Sign up at
 *     Archera →" (opens archera.ai in a new tab) or "No thanks". Carries
 *     a "Learn more" link to the full education page below the buttons.
 *   - openArcheraPage(): the full education page rendered as a
 *     full-viewport overlay (#archera-page-container). Opened from the
 *     offer modal's "Learn more" link and from deep-linked email URLs.
 *
 * The education page is a single scrollable surface: disclosure, what it
 * is, how it works, when it makes sense, 3-step integration flow,
 * disclaimers. ARCHERA_PAGE_A_PATH and ARCHERA_PAGE_B_PATH both deep-link
 * to it (PAGE_B kept for backward compatibility with already-delivered
 * email links).
 *
 * Signup link is carried in both surfaces:
 *   https://archera.ai/signup?mode=cudly
 *
 * No backend, routing, or IaC changes (frontend-only).
 */

/** Canonical Archera signup URL with CUDly attribution. */
export const ARCHERA_SIGNUP_URL = 'https://archera.ai/signup?mode=cudly';

/**
 * Frontend URL path for the Archera education page.
 * Used as the `ArcheraEducationURL` in email templates so the link from
 * the approval / confirmation email lands directly on this page.
 * Must stay in sync with the path checked in handleArcheraDeeplink().
 */
export const ARCHERA_PAGE_A_PATH = '/archera-insurance';

/**
 * Legacy frontend URL path retained for backward compatibility. Resolves
 * to the same merged page as ARCHERA_PAGE_A_PATH. Kept so any in-flight
 * or already-delivered email that linked to the old "/how-it-works" path
 * still lands on a working overlay.
 * Must stay in sync with the path checked in handleArcheraDeeplink().
 */
export const ARCHERA_PAGE_B_PATH = '/archera-insurance/how-it-works';

/** Where the user came from: controls the offer modal's headline. */
export type ArcheraContext = 'purchase' | 'plan';

/**
 * Module-scoped reference to the element that was focused when the page
 * overlay or the offer modal was last opened. Restored on close so keyboard
 * users return to their original position. Reused across both surfaces;
 * only one is visible at a time so a single slot is sufficient.
 */
let _previouslyFocused: HTMLElement | null = null;

/** Cached cleanup for the offer modal's outside-click + ESC handlers. */
let _offerModalCleanup: (() => void) | null = null;

/**
 * Open the Archera Insurance offer modal. Shown after a successful
 * purchase-approval submission or plan creation, never blocking the
 * action itself. The modal carries two equal-weight choices:
 *
 *   - "Sign up at Archera →" opens archera.ai in a new tab.
 *   - "No thanks" dismisses the modal.
 *
 * A muted "Learn more about Archera Insurance" link below the buttons
 * opens the full education overlay (openArcheraPage) for users who
 * want context before signing up.
 *
 * Idempotent: calling twice replaces the content rather than stacking.
 *
 * TODO(@cristim): final copy review; confirm exact headline + button wording.
 */
export function openArcheraOfferModal(context: ArcheraContext = 'purchase'): void {
  const container = document.getElementById('archera-offer-modal-container');
  if (!container) return;

  const isCurrentlyHidden = container.classList.contains('hidden');
  if (isCurrentlyHidden) {
    _previouslyFocused = document.activeElement instanceof HTMLElement
      ? document.activeElement
      : null;
  }

  while (container.firstChild) container.removeChild(container.firstChild);
  container.classList.remove('hidden');

  // Backdrop. Clicking it closes the modal, matching the rest of the app's
  // modal UX. Pointer events on the panel itself stop propagation.
  const backdrop = document.createElement('div');
  backdrop.className = 'archera-offer-backdrop';
  backdrop.addEventListener('click', closeArcheraOfferModal);
  container.appendChild(backdrop);

  const panel = document.createElement('div');
  panel.className = 'archera-offer-panel';
  panel.setAttribute('role', 'dialog');
  panel.setAttribute('aria-modal', 'true');
  panel.setAttribute('aria-labelledby', 'archera-offer-title');
  panel.addEventListener('click', (e) => e.stopPropagation());
  container.appendChild(panel);

  const title = document.createElement('h2');
  title.id = 'archera-offer-title';
  title.className = 'archera-offer-title';
  title.textContent =
    context === 'plan'
      ? 'Insure this plan with Archera?'
      : 'Insure your commitments with Archera?';
  panel.appendChild(title);

  // TODO(@cristim): final copy review; verify pitch with Archera before merge.
  const lead = document.createElement('p');
  lead.className = 'archera-offer-lead';
  lead.textContent =
    'Archera Insurance covers the gap if your committed cloud capacity ' +
    'goes unused. Optional, set up on Archera’s site, and doesn’t affect ' +
    'what you just bought.';
  panel.appendChild(lead);

  // Brief disclosure: short reminder that CUDly is sponsored and that the
  // product works without Archera. The full-disclosure paragraph at the
  // bottom of the Learn-more body has the longer version.
  const disclosure = document.createElement('p');
  disclosure.className = 'archera-offer-disclosure';
  disclosure.textContent =
    'Archera sponsors CUDly’s development. CUDly works fully without it.';
  panel.appendChild(disclosure);

  // Learn-more: collapsible inline drop-down ABOVE the action buttons.
  // Starts closed; clicking the summary expands the full education body
  // within this same modal panel. Using a native <details>/<summary>
  // gives us the toggle behaviour and keyboard a11y for free. Placed
  // before the action row so a user who wants more context can read it
  // first and then act, without their eye crossing the buttons twice.
  const learnMore = document.createElement('details');
  learnMore.className = 'archera-offer-learnmore';

  const summary = document.createElement('summary');
  summary.className = 'archera-offer-learnmore-summary';
  summary.textContent = 'Learn more about Archera Insurance';
  learnMore.appendChild(summary);

  const learnMoreBody = document.createElement('div');
  learnMoreBody.className = 'archera-offer-learnmore-body archera-page-inner';
  appendArcheraEducationBody(learnMoreBody);
  learnMore.appendChild(learnMoreBody);

  panel.appendChild(learnMore);

  // Action row: No thanks (secondary) | Sign up at Archera (primary).
  // Last element in the panel so it's the final visual call to action.
  const actions = document.createElement('div');
  actions.className = 'archera-offer-actions';

  const skipBtn = document.createElement('button');
  skipBtn.type = 'button';
  skipBtn.className = 'archera-offer-skip';
  skipBtn.textContent = 'No thanks';
  skipBtn.addEventListener('click', closeArcheraOfferModal);
  actions.appendChild(skipBtn);

  const signupBtn = document.createElement('a');
  signupBtn.href = ARCHERA_SIGNUP_URL;
  signupBtn.target = '_blank';
  signupBtn.rel = 'noopener noreferrer';
  signupBtn.className = 'archera-offer-signup';
  signupBtn.textContent = 'Sign up at Archera →';
  // Close the offer once the user has decided to act on it; the new tab
  // continues in the background while CUDly returns to the underlying view.
  signupBtn.addEventListener('click', () => {
    // Defer so the browser still gets to fire the actual navigation click
    // before the surrounding overlay tears down.
    setTimeout(closeArcheraOfferModal, 0);
  });
  actions.appendChild(signupBtn);

  panel.appendChild(actions);

  // Keyboard: ESC closes. Outside-click is handled by the backdrop above.
  const escHandler = (e: KeyboardEvent): void => {
    if (e.key === 'Escape') closeArcheraOfferModal();
  };
  document.addEventListener('keydown', escHandler);
  _offerModalCleanup = () => {
    document.removeEventListener('keydown', escHandler);
    _offerModalCleanup = null;
  };

  // Focus the "Sign up" button by default: it's the primary CTA and lets
  // keyboard users confirm with Enter immediately.
  signupBtn.focus();
}

/** Close the Archera offer modal and clear its content. */
export function closeArcheraOfferModal(): void {
  const container = document.getElementById('archera-offer-modal-container');
  if (!container) return;
  container.classList.add('hidden');
  while (container.firstChild) container.removeChild(container.firstChild);

  if (_offerModalCleanup) _offerModalCleanup();

  if (
    _previouslyFocused !== null &&
    document.contains(_previouslyFocused) &&
    typeof _previouslyFocused.focus === 'function'
  ) {
    _previouslyFocused.focus();
  }
  _previouslyFocused = null;
}

/**
 * Open the Archera education overlay.
 * Idempotent: calling twice replaces the content rather than stacking layers.
 */
export function openArcheraPage(): void {
  const container = document.getElementById('archera-page-container');
  if (!container) return;

  // Capture the currently focused element only when the overlay is
  // transitioning from hidden to visible, so re-renders while open don't
  // overwrite the original opener's focus reference. closeArcheraPage
  // restores it and resets the variable.
  const isCurrentlyHidden = container.classList.contains('hidden');
  if (isCurrentlyHidden) {
    _previouslyFocused = document.activeElement instanceof HTMLElement
      ? document.activeElement
      : null;
  }

  // Clear existing content via DOM methods (no innerHTML to avoid XSS lint).
  while (container.firstChild) container.removeChild(container.firstChild);
  container.classList.remove('hidden');

  const inner = document.createElement('div');
  inner.className = 'archera-page-inner';
  buildArcheraPage(inner);
  container.appendChild(inner);

  // Focus the back button for keyboard users.
  const closeBtn = container.querySelector<HTMLElement>('.archera-page-back');
  closeBtn?.focus();
}

/** Close the education overlay and clear its content. */
export function closeArcheraPage(): void {
  const container = document.getElementById('archera-page-container');
  if (!container) return;
  container.classList.add('hidden');
  while (container.firstChild) container.removeChild(container.firstChild);

  // Restore focus to the element that was active before the overlay opened,
  // provided it is still in the document and is focusable.
  if (
    _previouslyFocused !== null &&
    document.contains(_previouslyFocused) &&
    typeof _previouslyFocused.focus === 'function'
  ) {
    _previouslyFocused.focus();
  }
  _previouslyFocused = null;
}

/**
 * Deep-link handler for Archera education page URLs.
 *
 * Called from app.ts `init()` after authentication, before normal tab
 * routing. If the current URL path is `/archera-insurance` or the legacy
 * `/archera-insurance/how-it-works`, opens the education overlay. The
 * normal tab routing then runs underneath (dashboard becomes active),
 * so the user has a fully functional app behind the overlay.
 *
 * Returns true if the path matched and the overlay was opened, false
 * if no match (caller can continue with normal routing as-is).
 *
 * The path constants ARCHERA_PAGE_A_PATH / ARCHERA_PAGE_B_PATH are the
 * source of truth for the URLs used in email templates (ArcheraEducationURL).
 */
export function handleArcheraDeeplink(): boolean {
  const path = window.location.pathname.replace(/\/+$/, '');
  if (
    path === ARCHERA_PAGE_A_PATH ||
    path === ARCHERA_PAGE_B_PATH ||
    path.startsWith(ARCHERA_PAGE_A_PATH + '/')
  ) {
    openArcheraPage();
    return true;
  }
  return false;
}

// ---------------------------------------------------------------------------
// The Archera Insurance education page (single merged page)
// ---------------------------------------------------------------------------

function buildArcheraPage(root: HTMLElement): void {
  root.appendChild(buildBackButton());

  const h1 = document.createElement('h1');
  h1.textContent = 'Archera Insurance';
  root.appendChild(h1);

  // Page-only orientation paragraph. The offer modal's own top lead +
  // disclosure plays this role inside the modal, so the shared
  // education body skips it; deep-linked email URLs land here without
  // that context, so the page supplies its own one-line orientation.
  // TODO(@cristim): final copy review.
  const pageLead = document.createElement('p');
  pageLead.className = 'archera-page-lead';
  pageLead.textContent =
    'Archera Insurance covers the gap if your committed cloud capacity ' +
    'goes unused. Optional, third-party coverage that doesn’t affect the ' +
    'commitments you buy through CUDly.';
  root.appendChild(pageLead);

  appendArcheraEducationBody(root);

  root.appendChild(buildSignupBlock());
}

/**
 * Append the reusable body of the Archera education content to `root`.
 *
 * Use cases, mechanics, premium note, and a closing full-disclosure
 * paragraph. Used by buildArcheraPage (under back button + h1 + a short
 * page-only lead + signup block) and by the offer modal's collapsible
 * "Learn more" section (without the surrounding chrome: the modal's
 * own lead + disclosure cover the elevator-pitch level).
 *
 * Opens directly with "When it makes sense" rather than a fresh lead:
 * both callers provide their own orientation above (the modal title +
 * lead, or the page h1 + page-only lead), so a second lead here would
 * duplicate the pitch the user just read.
 *
 * TODO(@cristim): final copy review across all paragraphs before merge.
 */
function appendArcheraEducationBody(root: HTMLElement): void {
  // "When it makes sense" runs FIRST: a reader who isn't sure the product
  // applies to them gets the use-case framing first; only readers who
  // self-identify with one of those cases need to read the mechanics.
  appendSection(root, 'When it makes sense', [
    'You want the deepest discount tier (3-year) but aren\'t sure the ' +
      'workload will still fit in 18 months, or you want to be covered in ' +
      'case your usage drops.',
    'You are moving to a new service or region and historical utilisation data is thin.',
  ]);

  // Combined "How it works": non-gating fact, 3-step onboarding flow, then
  // premium note. Folded from the prior split "How it works" + "How the
  // integration works" sections.
  // TODO(@cristim): final copy; verify step sequence + premium wording with
  // Archera before publishing
  const howH2 = document.createElement('h2');
  howH2.textContent = 'How it works';
  root.appendChild(howH2);

  const nonGating = document.createElement('p');
  nonGating.textContent =
    'CUDly works fully without Archera Insurance. Every feature ' +
    '(recommendations, scheduling, plans, the dashboard) operates identically ' +
    'whether or not you enroll. If you do enroll, you have 7 days from each ' +
    'purchase to sign up at Archera. The flow is lightweight:';
  root.appendChild(nonGating);

  const steps: Array<{ title: string; body: string }> = [
    {
      title: 'Sign up at Archera',
      // TODO(@cristim): final copy; confirm exact onboarding URL and flow
      body:
        'create an account using the link below. The CUDly signup link tells ' +
        'Archera you came from us; CUDly is compensated for the referral, and ' +
        'the link unlocks a dedicated onboarding path.',
    },
    {
      title: 'Archera starts ingesting cost data',
      body:
        'once access is granted, the insurance policy activates and covers any ' +
        'overcommitment from that point forward.',
    },
    {
      title: 'Purchase commitments normally through CUDly',
      body:
        'Archera tracks utilisation independently and pays out on shortfalls ' +
        'per your policy.',
    },
  ];

  const ol = document.createElement('ol');
  ol.className = 'archera-steps';
  for (const step of steps) {
    const li = document.createElement('li');
    const stepStrong = document.createElement('strong');
    stepStrong.textContent = step.title + ': ';
    li.appendChild(stepStrong);
    li.appendChild(document.createTextNode(step.body));
    ol.appendChild(li);
  }
  root.appendChild(ol);

  // Premium note: pricing is a question readers will have after they
  // understand the flow.
  // TODO(@cristim): final copy; confirm exact premium structure with Archera
  const premium = document.createElement('p');
  premium.textContent =
    'Archera charges an insurance premium for the coverage you select, a ' +
    'separate fee paid to Archera. The cloud commitment you bought through ' +
    'CUDly is unaffected: same price, same billing.';
  root.appendChild(premium);

  // Combined Full Disclosure paragraph: sponsorship + the legal/scope facts
  // that previously lived in a separate "Disclaimers" bullet list. Folded
  // together because the bullets restated points that were already in the
  // modal's top lead ("set up on Archera's site"), in step 1 ("CUDly is
  // compensated for the referral"), or implicit in the sponsorship line.
  // TODO(@cristim): final copy; confirm disclosure + legal language
  const disclosure = document.createElement('p');
  disclosure.className = 'archera-disclosure';
  const strong = document.createElement('strong');
  strong.textContent = 'Full disclosure:';
  disclosure.appendChild(strong);
  disclosure.appendChild(
    document.createTextNode(
      ' Archera sponsors CUDly\'s development with a share of their insurance ' +
        'revenue; we surface the option because we think it\'s useful, but you ' +
        'should know about the financial relationship. Insurance terms, coverage, ' +
        'and pricing are set entirely by Archera. CUDly has no visibility into ' +
        'your Archera account or policy. Review Archera\'s terms of service and ' +
        'privacy policy before signing up.',
    ),
  );
  root.appendChild(disclosure);
}

// ---------------------------------------------------------------------------
// Shared helpers
// ---------------------------------------------------------------------------

function buildBackButton(): HTMLButtonElement {
  const btn = document.createElement('button');
  btn.className = 'archera-page-back';
  btn.type = 'button';
  btn.textContent = '← Back';
  btn.addEventListener('click', closeArcheraPage);
  return btn;
}

function appendSection(root: HTMLElement, title: string, items: string[]): void {
  const section = document.createElement('section');

  const h2 = document.createElement('h2');
  h2.textContent = title;
  section.appendChild(h2);

  const ul = document.createElement('ul');
  for (const item of items) {
    const li = document.createElement('li');
    li.textContent = item;
    ul.appendChild(li);
  }
  section.appendChild(ul);

  root.appendChild(section);
}

function buildSignupBlock(): HTMLElement {
  const div = document.createElement('div');
  div.className = 'archera-signup-block';

  const a = document.createElement('a');
  a.href = ARCHERA_SIGNUP_URL;
  a.target = '_blank';
  a.rel = 'noopener noreferrer';
  a.className = 'archera-signup-btn';
  a.textContent = 'Sign up at Archera →';
  div.appendChild(a);

  // TODO(@cristim): final copy; confirm whether a more prominent disclaimer is needed
  const note = document.createElement('p');
  note.className = 'archera-signup-note';
  note.textContent =
    'Opens archera.ai in a new tab. Archera is a third-party service independent of CUDly.';
  div.appendChild(note);

  return div;
}
