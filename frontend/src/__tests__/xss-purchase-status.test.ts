/**
 * Regression tests for stored XSS via purchase.status rendered as an
 * unescaped text node inside a <span> (issue #445).
 *
 * plans.ts:renderPlannedPurchaseRow used to emit:
 *   <span class="status-badge …">${purchase.status}</span>
 *
 * An attacker-controlled status value such as `<script>alert(1)</script>`
 * would be parsed as HTML and could execute arbitrary JavaScript.  The fix
 * wraps the interpolation with the existing escapeHtml() from utils.ts.
 */

import { loadPlans } from '../plans';

// Use the real escapeHtml so the DOM-based escaping is exercised, not a stub.
jest.mock('../utils', () => {
  const actual = jest.requireActual<typeof import('../utils')>('../utils');
  return {
    ...actual,
    formatDate: jest.fn((val: string) => val ? new Date(val).toLocaleDateString() : ''),
    formatTerm: jest.fn((years: number | null | undefined) =>
      years == null ? '' : `${years} Year${years === 1 ? '' : 's'}`),
    formatRampSchedule: jest.fn((val: string) => val || 'Unknown'),
    formatCurrency: jest.fn((val: number) => `$${val || 0}`),
    populateAccountFilter: jest.fn(() => Promise.resolve()),
  };
});

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

jest.mock('../history', () => ({
  viewPlanHistory: jest.fn(),
}));

jest.mock('../commitmentOptions', () => ({
  populateTermSelect: jest.fn(),
  populatePaymentSelect: jest.fn(),
  isValidCombination: jest.fn().mockReturnValue(true),
  normalizePaymentValue: jest.fn((value: unknown) => value),
}));

jest.mock('../toast', () => ({
  showToast: jest.fn(() => ({ dismiss: jest.fn() })),
}));

jest.mock('../confirmDialog', () => ({
  confirmDialog: jest.fn(() => Promise.resolve(true)),
}));

jest.mock('../modal', () => ({
  openModal: jest.fn(),
  closeModal: jest.fn(),
}));

jest.mock('../archera', () => ({
  openArcheraOfferModal: jest.fn(),
}));

jest.mock('../lib/skeleton', () => ({
  showSkeletonTiles: jest.fn(),
  showSkeletonRows: jest.fn(),
  teardownSkeleton: jest.fn(),
}));

jest.mock('../permissions', () => ({
  canAccess: jest.fn().mockReturnValue(true),
}));

import * as api from '../api';

const SCRIPT_PAYLOAD = '<script>alert(1)</script>';
const IMG_PAYLOAD = '"><img src=x onerror="alert(1)">';

function makePurchase(status: string) {
  return {
    id: 'pp-1',
    plan_id: 'plan-1',
    plan_name: 'Test Plan',
    scheduled_date: '2024-06-01',
    provider: 'aws',
    service: 'ec2',
    resource_type: 't3.medium',
    region: 'us-east-1',
    count: 1,
    term: 1,
    payment: 'no-upfront',
    estimated_savings: 50,
    upfront_cost: 0,
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    status: status as any,
    step_number: 1,
    total_steps: 1,
  };
}

describe('XSS regression: purchase.status rendered as text node (#445)', () => {
  beforeEach(() => {
    document.body.innerHTML = `
      <div id="plans-list"></div>
      <div id="planned-purchases-list"></div>
      <button id="new-plan-btn"></button>
    `;
    jest.clearAllMocks();
    (api.getPlans as jest.Mock).mockResolvedValue({ plans: [] });
  });

  test('script-tag payload in purchase.status does not create a <script> element', async () => {
    (api.getPlannedPurchases as jest.Mock).mockResolvedValue({
      purchases: [makePurchase(SCRIPT_PAYLOAD)],
    });

    await loadPlans();

    // No <script> element should have been injected.
    expect(document.querySelectorAll('script').length).toBe(0);

    const list = document.getElementById('planned-purchases-list');
    expect(list).not.toBeNull();
    // Raw tags must not appear in the rendered markup.
    expect(list!.innerHTML).not.toContain('<script>');
    // The payload must appear only as escaped text content, not as markup.
    expect(list!.textContent).toContain(SCRIPT_PAYLOAD);
  });

  test('img-onerror payload in purchase.status does not inject an img element', async () => {
    (api.getPlannedPurchases as jest.Mock).mockResolvedValue({
      purchases: [makePurchase(IMG_PAYLOAD)],
    });

    await loadPlans();

    const list = document.getElementById('planned-purchases-list');
    expect(list).not.toBeNull();
    // No <img> element should have been injected as a real DOM node.
    expect(list!.querySelectorAll('img').length).toBe(0);
    // The payload literal appears only as escaped text, not as a live attribute.
    const badge = list!.querySelector('.status-badge');
    expect(badge).not.toBeNull();
    expect(badge!.textContent).toContain('onerror');
  });

  test('valid status "pending" renders as plain text inside the status badge', async () => {
    (api.getPlannedPurchases as jest.Mock).mockResolvedValue({
      purchases: [makePurchase('pending')],
    });

    await loadPlans();

    const list = document.getElementById('planned-purchases-list');
    expect(list).not.toBeNull();
    const badge = list!.querySelector('.status-badge');
    expect(badge).not.toBeNull();
    expect(badge!.textContent).toBe('pending');
  });

  test('valid status "paused" renders as plain text inside the status badge', async () => {
    (api.getPlannedPurchases as jest.Mock).mockResolvedValue({
      purchases: [makePurchase('paused')],
    });

    await loadPlans();

    const list = document.getElementById('planned-purchases-list');
    expect(list).not.toBeNull();
    const badge = list!.querySelector('.status-badge');
    expect(badge).not.toBeNull();
    expect(badge!.textContent).toBe('paused');
  });
});
