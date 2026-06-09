/**
 * Regression tests for the Plans / Planned Purchases per-column filter
 * pipeline wired in the issue #166 follow-up to PR #570.
 *
 * The shared lib/column-filters primitives are exercised through the
 * dedicated column-filters.test.ts suite; these tests focus on the
 * Planned Purchases table integration:
 *
 *   - filter trigger button in every filterable header
 *   - popover opens / lists distinct values / commits filter state
 *   - numeric expression filter narrows rows + surfaces inline errors
 *   - categorical set filter narrows rows
 *   - stacked filters AND together
 *   - clearing a filter restores the full row set
 */
import { loadPlans } from '../plans';

jest.mock('../api', () => ({
  getPlans: jest.fn().mockResolvedValue({ plans: [] }),
  getPlannedPurchases: jest.fn(),
  runPlannedPurchase: jest.fn(),
  pausePlannedPurchase: jest.fn(),
  resumePlannedPurchase: jest.fn(),
  deletePlannedPurchase: jest.fn(),
  createPlannedPurchases: jest.fn(),
  listPlanAccounts: jest.fn().mockResolvedValue([]),
  setPlanAccounts: jest.fn().mockResolvedValue(undefined),
  listAccounts: jest.fn().mockResolvedValue([]),
  getAccount: jest.fn().mockResolvedValue(null),
  getPlan: jest.fn(),
  createPlan: jest.fn(),
  updatePlan: jest.fn(),
  patchPlan: jest.fn(),
  deletePlan: jest.fn(),
}));

// Module-scoped filter state; the real state module is too coupled to
// other slices to import directly for these tests. The mock backs
// getPlansColumnFilters / setPlansColumnFilter with a simple object
// so the popover commit path round-trips like the real store.
let plansFilters: Record<string, unknown> = {};
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
  getCurrentUser: jest.fn().mockReturnValue({ id: 'u-admin', email: 'admin@example.com', role: 'admin' }),
  getPlansColumnFilters: jest.fn(() => ({ ...plansFilters })),
  setPlansColumnFilter: jest.fn((col: string, filter: unknown) => {
    if (filter === null) {
      const next = { ...plansFilters };
      delete next[col];
      plansFilters = next;
      return;
    }
    plansFilters = { ...plansFilters, [col]: filter };
  }),
  clearAllPlansColumnFilters: jest.fn(() => { plansFilters = {}; }),
}));

jest.mock('../history', () => ({ viewPlanHistory: jest.fn() }));
jest.mock('../commitmentOptions', () => ({
  populateTermSelect: jest.fn(),
  populatePaymentSelect: jest.fn(),
  isValidCombination: jest.fn().mockReturnValue(true),
  normalizePaymentValue: jest.fn((v) => v),
}));
jest.mock('../archera', () => ({ openArcheraOfferModal: jest.fn() }));
jest.mock('../toast', () => ({ showToast: jest.fn(() => ({ dismiss: jest.fn() })) }));
jest.mock('../confirmDialog', () => ({ confirmDialog: jest.fn(() => Promise.resolve(true)) }));

import * as api from '../api';

const seedPurchases = [
  {
    id: 'pp-1', plan_id: 'plan-a', plan_name: 'Plan A', scheduled_date: '2026-06-01',
    provider: 'aws', service: 'ec2', resource_type: 't3.medium', region: 'us-east-1',
    count: 1, term: 1, payment: 'all-upfront', upfront_cost: 100, estimated_savings: 10,
    status: 'pending', step_number: 1, total_steps: 4,
  },
  {
    id: 'pp-2', plan_id: 'plan-a', plan_name: 'Plan A', scheduled_date: '2026-06-02',
    provider: 'aws', service: 'rds', resource_type: 'db.t3.large', region: 'us-east-1',
    count: 3, term: 3, payment: 'no-upfront', upfront_cost: 0, estimated_savings: 50,
    status: 'pending', step_number: 2, total_steps: 4,
  },
  {
    id: 'pp-3', plan_id: 'plan-b', plan_name: 'Plan B', scheduled_date: '2026-06-03',
    provider: 'azure', service: 'compute', resource_type: 'D2s_v3', region: 'eastus',
    count: 5, term: 3, payment: 'partial-upfront', upfront_cost: 500, estimated_savings: 200,
    status: 'paused', step_number: 1, total_steps: 1,
  },
];

describe('Planned Purchases column filters (issue #166 follow-up)', () => {
  beforeEach(() => {
    plansFilters = {};
    document.body.innerHTML = `
      <div id="plans-list"></div>
      <div id="planned-purchases-list"></div>
    `;
    jest.clearAllMocks();
    (api.getPlans as jest.Mock).mockResolvedValue({ plans: [] });
    (api.getPlannedPurchases as jest.Mock).mockResolvedValue({ purchases: seedPurchases });
  });

  afterEach(() => {
    // Detached popover lives on document.body; clean up between tests so
    // a leftover from one assertion doesn't bleed into the next.
    document.body.querySelectorAll('.column-filter-popover').forEach((n) => n.remove());
  });

  function countRowsByStatus(): Record<string, number> {
    const rows = document.querySelectorAll<HTMLTableRowElement>(
      '#planned-purchases-list tbody tr.planned-purchase-row',
    );
    const out: Record<string, number> = {};
    rows.forEach((r) => {
      const status = r.className.split(/\s+/).find((c) => c.startsWith('status-')) ?? 'unknown';
      out[status] = (out[status] ?? 0) + 1;
    });
    return out;
  }

  function countRows(): number {
    return document.querySelectorAll(
      '#planned-purchases-list tbody tr.planned-purchase-row',
    ).length;
  }

  test('every filterable column header has a trigger button', async () => {
    await loadPlans();
    const buttons = document.querySelectorAll<HTMLButtonElement>(
      '#planned-purchases-list th .column-filter-btn[data-column]',
    );
    const cols = Array.from(buttons).map((b) => b.dataset['column']);
    expect(cols.sort()).toEqual(
      ['count', 'estimated_savings', 'payment', 'provider', 'resource_type', 'service', 'status', 'term', 'upfront_cost'].sort(),
    );
  });

  test('clicking a trigger opens a popover detached to document.body', async () => {
    await loadPlans();
    const providerBtn = document.querySelector<HTMLButtonElement>(
      '#planned-purchases-list th .column-filter-btn[data-column="provider"]',
    );
    providerBtn?.click();
    const popover = document.body.querySelector('.column-filter-popover');
    expect(popover).not.toBeNull();
    expect(popover?.closest('table')).toBeNull();
  });

  test('categorical set filter narrows rows (provider=aws)', async () => {
    await loadPlans();
    expect(countRows()).toBe(3);

    // Open the provider popover, uncheck "azure" to narrow to aws-only.
    const providerBtn = document.querySelector<HTMLButtonElement>(
      '#planned-purchases-list th .column-filter-btn[data-column="provider"]',
    );
    providerBtn?.click();
    const items = Array.from(document.querySelectorAll<HTMLLabelElement>(
      '.column-filter-popover .column-filter-item',
    ));
    const azureItem = items.find((l) => l.querySelector('input')?.dataset['value'] === 'azure');
    const azureCb = azureItem?.querySelector<HTMLInputElement>('input[type="checkbox"]');
    expect(azureCb).not.toBeNull();
    expect(azureCb!.checked).toBe(true); // no filter → all checked
    azureCb!.checked = false;
    azureCb!.dispatchEvent(new Event('change'));

    expect(countRows()).toBe(2);
    document.querySelectorAll<HTMLTableRowElement>(
      '#planned-purchases-list tbody tr.planned-purchase-row',
    ).forEach((r) => {
      expect(r.innerHTML.toLowerCase()).toContain('aws');
    });
  });

  test('numeric expr filter narrows rows (count >= 2)', async () => {
    await loadPlans();
    expect(countRows()).toBe(3);

    const countBtn = document.querySelector<HTMLButtonElement>(
      '#planned-purchases-list th .column-filter-btn[data-column="count"]',
    );
    countBtn?.click();
    const input = document.querySelector<HTMLInputElement>(
      '.column-filter-popover .column-filter-numeric-input',
    );
    expect(input).not.toBeNull();
    input!.value = '>=2';
    input!.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true }));

    // pp-1 has count=1 (filtered out); pp-2 count=3 and pp-3 count=5 remain.
    expect(countRows()).toBe(2);
  });

  test('stacked filters AND together (provider=aws AND count >= 2)', async () => {
    await loadPlans();

    // First filter: provider=aws (untick azure).
    const providerBtn = document.querySelector<HTMLButtonElement>(
      '#planned-purchases-list th .column-filter-btn[data-column="provider"]',
    );
    providerBtn?.click();
    const azureCb = document.querySelector<HTMLInputElement>(
      '.column-filter-popover .column-filter-item input[data-value="azure"]',
    );
    azureCb!.checked = false;
    azureCb!.dispatchEvent(new Event('change'));
    expect(countRows()).toBe(2);

    // Second filter: count >= 2.
    const countBtn = document.querySelector<HTMLButtonElement>(
      '#planned-purchases-list th .column-filter-btn[data-column="count"]',
    );
    countBtn?.click();
    const input = document.querySelector<HTMLInputElement>(
      '.column-filter-popover .column-filter-numeric-input',
    );
    input!.value = '>=2';
    input!.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true }));

    // Only pp-2 (aws + count=3) survives both filters; pp-3 fails on provider, pp-1 on count.
    expect(countRows()).toBe(1);
    const survivor = document.querySelector(
      '#planned-purchases-list tbody tr.planned-purchase-row',
    );
    expect(survivor?.innerHTML).toContain('rds');
  });

  test('invalid numeric expression surfaces the lib error inline (no filter applied)', async () => {
    await loadPlans();
    const countBtn = document.querySelector<HTMLButtonElement>(
      '#planned-purchases-list th .column-filter-btn[data-column="count"]',
    );
    countBtn?.click();
    const input = document.querySelector<HTMLInputElement>(
      '.column-filter-popover .column-filter-numeric-input',
    );
    input!.value = '>=abc';
    input!.dispatchEvent(new Event('blur'));

    const err = document.querySelector('.column-filter-popover .column-filter-error');
    expect(err?.textContent).toMatch(/Invalid filter term/);
    // No filter applied → all 3 rows still rendered.
    expect(countRows()).toBe(3);
  });

  test('clearing a filter restores the full row set', async () => {
    await loadPlans();

    // Apply: status=pending (uncheck paused).
    const statusBtn = document.querySelector<HTMLButtonElement>(
      '#planned-purchases-list th .column-filter-btn[data-column="status"]',
    );
    statusBtn?.click();
    const pausedCb = document.querySelector<HTMLInputElement>(
      '.column-filter-popover .column-filter-item input[data-value="paused"]',
    );
    pausedCb!.checked = false;
    pausedCb!.dispatchEvent(new Event('change'));
    expect(countRows()).toBe(2);
    expect(countRowsByStatus()['status-paused']).toBeUndefined();

    // Popover is still open after the checkbox commit. Use the (All)
    // tri-state to restore "no narrowing" → all 3 rows.
    const allBox = document.querySelector<HTMLInputElement>(
      '.column-filter-popover input[data-role="all"]',
    );
    expect(allBox).not.toBeNull();
    allBox!.checked = true;
    allBox!.dispatchEvent(new Event('change'));

    expect(countRows()).toBe(3);
  });
});
