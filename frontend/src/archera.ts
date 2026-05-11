/**
 * Archera Insurance integration — CTA helper and education page rendering.
 *
 * This module provides:
 *   - renderArcheraCTA() — returns a small, unobtrusive paragraph element
 *     with a link to the Archera Insurance education overlay. Used in the
 *     Purchase modal and Plan-creation modal.
 *   - openArcheraPage(pageId) — renders Page A or Page B as a full-viewport
 *     overlay panel (#archera-page-container) without touching the main
 *     navigation/routing state. The overlay has a "Back" button that
 *     closes it.
 *
 * Education pages:
 *   Page A — "What is Archera Insurance?" (pageId: 'what-is-archera')
 *   Page B — "How the CUDly ↔ Archera integration works"
 *             (pageId: 'how-it-works')
 *
 * Both pages carry the signup link:
 *   https://archera.ai/signup?mode=cudly
 *
 * No backend, routing, or IaC changes — frontend-only.
 */

/** Canonical Archera signup URL with CUDly attribution. */
export const ARCHERA_SIGNUP_URL = 'https://archera.ai/signup?mode=cudly';

/** Page identifiers for the education overlay. */
export type ArcheraPageId = 'what-is-archera' | 'how-it-works';

/**
 * Return a small CTA paragraph element:
 *   "💡 Worried about committing? Archera Insurance underwrites
 *    commitment-overuse — learn how it works →"
 *
 * Clicking the button opens Page A (the "What is Archera Insurance?" overlay).
 * The element uses the `.archera-cta` CSS class for muted, non-pushy styling.
 *
 * Exported for use in openPurchaseModal (recommendations.ts) and
 * openCreatePlanModal / openNewPlanModal (plans.ts).
 */
export function renderArcheraCTA(): HTMLParagraphElement {
  const p = document.createElement('p');
  p.className = 'archera-cta';

  const icon = document.createElement('span');
  icon.setAttribute('aria-hidden', 'true');
  icon.textContent = '💡 ';
  p.appendChild(icon);

  p.appendChild(
    document.createTextNode(
      'Worried about committing? Archera Insurance underwrites commitment-overuse — ',
    ),
  );

  const btn = document.createElement('button');
  btn.className = 'archera-cta-link';
  btn.type = 'button';
  btn.textContent = 'learn how it works →';
  btn.addEventListener('click', () => openArcheraPage('what-is-archera'));
  p.appendChild(btn);

  return p;
}

/**
 * Open the Archera education overlay, rendering either Page A or Page B.
 * Idempotent: calling twice replaces the content rather than stacking layers.
 */
export function openArcheraPage(pageId: ArcheraPageId): void {
  const container = document.getElementById('archera-page-container');
  if (!container) return;

  // Clear existing content via DOM methods (no innerHTML to avoid XSS lint).
  while (container.firstChild) container.removeChild(container.firstChild);
  container.classList.remove('hidden');

  const inner = document.createElement('div');
  inner.className = 'archera-page-inner';

  if (pageId === 'what-is-archera') {
    buildPageA(inner);
  } else {
    buildPageB(inner);
  }

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
}

// ---------------------------------------------------------------------------
// Page A — "What is Archera Insurance?"
// ---------------------------------------------------------------------------

function buildPageA(root: HTMLElement): void {
  root.appendChild(buildBackButton());

  const h1 = document.createElement('h1');
  h1.textContent = 'What is Archera Insurance?';
  root.appendChild(h1);

  // TODO(@cristim): final copy — review wording with Archera before publishing
  const intro = document.createElement('p');
  intro.className = 'archera-page-lead';
  intro.textContent =
    'Cloud commitment discounts (Reserved Instances, Savings Plans, CUDs) offer ' +
    'significant savings but require locking in capacity you may not fully use. ' +
    'Archera Insurance lets you buy that commitment coverage while protecting yourself ' +
    'against overcommitment — if your usage drops, Archera covers the gap.';
  root.appendChild(intro);

  appendSection(root, 'How it works', [
    'You purchase cloud commitments as usual through CUDly.',
    'Archera underwrites those commitments: if actual usage falls short, ' +
      'Archera pays the difference up to the insured amount.',
    // TODO(@cristim): final copy — confirm exact revenue model with Archera
    'Archera earns revenue through a sharing-of-savings arrangement — ' +
      'you keep the majority of the discount, and Archera takes a small percentage ' +
      'for providing the insurance coverage.',
    'No upfront insurance premium: the fee structure is tied to the savings achieved.',
  ]);

  appendSection(root, 'When it makes sense', [
    'Your workload is growing or changing and you are not sure whether a 3-year ' +
      'commitment will still fit in 18 months.',
    'You want the deepest discount tier (All Upfront, 3-year) but your finance team ' +
      'requires a commitment-overuse safety net.',
    'You are moving to a new service or region and historical utilisation data is thin.',
  ]);

  appendSection(root, 'What CUDly does', [
    'CUDly surfaces the Archera option at purchase time so you can decide whether ' +
      'to include insurance coverage before committing.',
    'CUDly does not generate or store any Archera credentials — signup and billing ' +
      'happen entirely on Archera\'s platform.',
    'Archera is a third-party service. CUDly has no visibility into your Archera account.',
  ]);

  // Cross-link to Page B
  const crossLink = document.createElement('p');
  crossLink.className = 'archera-page-crosslink';
  const howItWorksBtn = document.createElement('button');
  howItWorksBtn.className = 'archera-cta-link';
  howItWorksBtn.type = 'button';
  howItWorksBtn.textContent = 'See how the CUDly ↔ Archera integration works →';
  howItWorksBtn.addEventListener('click', () => openArcheraPage('how-it-works'));
  crossLink.appendChild(howItWorksBtn);
  root.appendChild(crossLink);

  root.appendChild(buildSignupBlock());
}

// ---------------------------------------------------------------------------
// Page B — "How the CUDly ↔ Archera integration works"
// ---------------------------------------------------------------------------

function buildPageB(root: HTMLElement): void {
  root.appendChild(buildBackButton());

  const h1 = document.createElement('h1');
  h1.textContent = 'How the CUDly ↔ Archera integration works';
  root.appendChild(h1);

  // TODO(@cristim): final copy — verify step sequence with Archera team
  const intro = document.createElement('p');
  intro.className = 'archera-page-lead';
  intro.textContent =
    'The integration is lightweight: CUDly surfaces the option at purchase time; ' +
    'all Archera-specific setup happens on Archera\'s side. No credentials or ' +
    'IaC changes are required in CUDly itself.';
  root.appendChild(intro);

  const stepsH2 = document.createElement('h2');
  stepsH2.textContent = 'Step-by-step';
  root.appendChild(stepsH2);

  const steps: Array<{ title: string; body: string }> = [
    {
      title: 'Sign up at Archera',
      // TODO(@cristim): final copy — confirm exact onboarding URL and flow
      body:
        'Create an Archera account using the link below. Use the CUDly signup link ' +
        'so Archera knows you are coming from CUDly — this keeps attribution correct ' +
        'and may unlock a dedicated onboarding path.',
    },
    {
      title: 'Archera generates pre-filled IaC',
      body:
        'For AWS and GCP accounts, Archera’s onboarding flow generates pre-populated ' +
        'Terraform (or CloudFormation) that grants Archera read access to your billing ' +
        'data and usage metrics. You apply it in your account — Archera never receives ' +
        'long-lived keys.',
    },
    {
      title: 'Azure: OAuth enterprise-app consent',
      body:
        'For Azure subscriptions, Archera uses an OAuth enterprise-application consent ' +
        'flow rather than custom RBAC roles. You grant consent once per tenant through ' +
        'Archera’s onboarding UI.',
    },
    {
      title: 'Archera starts ingesting cost data',
      body:
        'Once access is granted, Archera begins ingesting your commitment utilisation ' +
        'data. The insurance policy activates and covers any overcommitment from that ' +
        'point forward.',
    },
    {
      title: 'Purchase commitments normally through CUDly',
      body:
        'Continue using CUDly for commitment recommendations and purchases. Archera ' +
        'tracks utilisation independently and pays out on any shortfall according to ' +
        'your policy terms.',
    },
  ];

  const ol = document.createElement('ol');
  ol.className = 'archera-steps';
  for (const step of steps) {
    const li = document.createElement('li');
    const strong = document.createElement('strong');
    strong.textContent = step.title + ': ';
    li.appendChild(strong);
    li.appendChild(document.createTextNode(step.body));
    ol.appendChild(li);
  }
  root.appendChild(ol);

  appendSection(root, 'Disclaimers', [
    'Archera is an independent third-party platform. CUDly does not generate or ' +
      'hold Archera credentials, billing data, or insurance policy details.',
    'The integration is referral-based: CUDly passes a source parameter to the ' +
      'Archera signup URL for attribution. No personal data is shared.',
    // TODO(@cristim): final copy — confirm disclaimer language with legal/Archera
    'Insurance terms, coverage limits, and pricing are set by Archera. Review ' +
      'Archera’s terms of service and privacy policy before signing up.',
  ]);

  // Cross-link back to Page A
  const crossLink = document.createElement('p');
  crossLink.className = 'archera-page-crosslink';
  const whatIsBtn = document.createElement('button');
  whatIsBtn.className = 'archera-cta-link';
  whatIsBtn.type = 'button';
  whatIsBtn.textContent = '← What is Archera Insurance?';
  whatIsBtn.addEventListener('click', () => openArcheraPage('what-is-archera'));
  crossLink.appendChild(whatIsBtn);
  root.appendChild(crossLink);

  root.appendChild(buildSignupBlock());
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

  // TODO(@cristim): final copy — confirm whether a more prominent disclaimer is needed
  const note = document.createElement('p');
  note.className = 'archera-signup-note';
  note.textContent =
    'Opens archera.ai in a new tab. Archera is a third-party service independent of CUDly.';
  div.appendChild(note);

  return div;
}
