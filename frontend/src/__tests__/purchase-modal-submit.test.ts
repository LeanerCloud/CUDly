/**
 * Issue #1903 / #1904: the purchase modal's Term and Payment selects must
 * re-price the row (not just relabel it), and the fan-out modal must never
 * submit a bucket it told the user would be skipped.
 *
 * These tests drive the REAL app.ts + recommendations.ts modules end to end
 * and assert on the actual request body handed to api.executePurchase — the
 * body the backend prices, records, and emails from (see
 * internal/api/handler_purchases.go: validateAndTotalRecommendations /
 * recTotalCommitment trust the submitted amounts verbatim). Asserting on an
 * intermediate helper instead of this body would not prove the fix.
 */

// ── mocks (must precede imports) ─────────────────────────────────────────────

jest.mock('../api', () => ({
  initAuth: jest.fn(),
  isAuthenticated: jest.fn(),
  getCurrentUser: jest.fn(),
  executePurchase: jest.fn(),
  getRecommendations: jest.fn(),
  getConfig: jest.fn().mockResolvedValue({ global: {} }),
  listAccountsMinimal: jest.fn().mockResolvedValue([]),
  listAccountServiceOverrides: jest.fn().mockResolvedValue([]),
}));

jest.mock('../api/recommendations', () => ({
  getRecommendationsFreshness: jest.fn().mockResolvedValue({
    last_collected_at: new Date().toISOString(),
    last_collection_error: null,
  }),
  refreshRecommendations: jest.fn().mockResolvedValue({}),
}));

jest.mock('../state', () => ({
  getCurrentProvider: jest.fn().mockReturnValue('all'),
  setCurrentProvider: jest.fn(),
  getCurrentAccountIDs: jest.fn().mockReturnValue([]),
  setCurrentAccountIDs: jest.fn(),
  getRecommendations: jest.fn().mockReturnValue([]),
  getRecommendationByID: jest.fn().mockReturnValue(undefined),
  setRecommendations: jest.fn(),
  getSelectedRecommendationIDs: jest.fn().mockReturnValue(new Set()),
  clearSelectedRecommendations: jest.fn(),
  addSelectedRecommendation: jest.fn(),
  removeSelectedRecommendation: jest.fn(),
  getRecommendationsSort: jest.fn().mockReturnValue({ column: 'savings', direction: 'desc' }),
  setRecommendationsSort: jest.fn(),
  getRecommendationsColumnFilters: jest.fn().mockReturnValue({}),
  setRecommendationsColumnFilter: jest.fn(),
  clearAllRecommendationsColumnFilters: jest.fn(),
  getVisibleRecommendations: jest.fn().mockReturnValue([]),
  setVisibleRecommendations: jest.fn(),
  getCostPeriod: jest.fn().mockReturnValue('monthly'),
  setCostPeriod: jest.fn(),
  getHiddenColumns: jest.fn().mockReturnValue(new Set()),
  setHiddenColumns: jest.fn(),
  getCurrentUser: jest.fn(),
  // setupRecommendationsHandlers (real — ../recommendations is NOT mocked in
  // this file) subscribes to both on module init via app.ts's
  // setupEventListeners().
  subscribeProvider: jest.fn().mockReturnValue(() => {}),
  subscribeAccount: jest.fn().mockReturnValue(() => {}),
}));

jest.mock('../auth', () => ({
  showLoginModal: jest.fn(),
  updateUserUI: jest.fn(),
}));

jest.mock('../dashboard', () => ({
  loadDashboard: jest.fn().mockResolvedValue(undefined),
  setupDashboardHandlers: jest.fn(),
}));

jest.mock('../navigation', () => ({
  switchTab: jest.fn(),
  applyTabFromPath: jest.fn().mockReturnValue('dashboard'),
  initRouter: jest.fn(),
  switchSettingsSubTab: jest.fn(),
  getSettingsSubTabFromPath: jest.fn().mockReturnValue('general'),
}));

// NOTE: ../recommendations is intentionally NOT mocked — this file exercises
// the real openPurchaseModal / getFanOutBuckets / loadRecommendations.

jest.mock('../plans', () => ({
  savePlan: jest.fn(),
  setupPlanHandlers: jest.fn(),
  closePlanModal: jest.fn(),
  openNewPlanModal: jest.fn(),
  closePurchaseModal: jest.fn(),
}));

jest.mock('../settings', () => ({
  saveGlobalSettings: jest.fn(),
  setupSettingsHandlers: jest.fn(),
  resetSettings: jest.fn(),
}));

jest.mock('../riexchange', () => ({
  setupRIExchangeHandlers: jest.fn(),
  saveAutomationSettings: jest.fn(),
}));

jest.mock('../users', () => ({
  setupUserHandlers: jest.fn(),
}));

jest.mock('../apikeys', () => ({
  initApiKeys: jest.fn(),
}));

jest.mock('../history', () => ({
  loadHistory: jest.fn(),
  setupHistoryHandlers: jest.fn(),
}));

jest.mock('../modules/savings-history', () => ({
  initSavingsHistory: jest.fn(),
}));

jest.mock('../purchases-deeplink', () => ({
  handlePurchaseDeeplink: jest.fn(),
}));

jest.mock('../modal', () => ({
  openModal: jest.fn(),
  closeModal: jest.fn(),
}));

jest.mock('../confirmDialog', () => ({
  confirmDialog: jest.fn().mockResolvedValue(true),
}));

jest.mock('../archera', () => ({
  handleArcheraDeeplink: jest.fn(),
  openArcheraOfferModal: jest.fn(),
}));

jest.mock('../toast', () => ({
  showToast: jest.fn(),
}));

// ── imports ───────────────────────────────────────────────────────────────────

import { setupEventListeners } from '../app';
import * as api from '../api';
import * as state from '../state';
import { showToast } from '../toast';
import {
  openPurchaseModal,
  getPurchaseModalRecommendations,
  clearPurchaseModalRecommendations,
  clearFanOutBuckets,
  loadRecommendations,
} from '../recommendations';
import { formatCurrency } from '../utils';
import { ADMINISTRATORS_GROUP_ID, PURCHASER_GROUP_ID } from '../permissions';
import type { LocalRecommendation } from '../types';

// ── fixtures ──────────────────────────────────────────────────────────────────

// One AWS EC2 cell fanned out into its four (term, payment) variants — the
// same shape providers/aws/recommendations/client.go produces for a single
// physical resource. Every #1903 test reads from a fresh copy of this list
// via buildRows() so no test can leak a mutation into another.
function buildRows(): LocalRecommendation[] {
  return [
    {
      id: 'v-3-all', provider: 'aws', cloud_account_id: 'a1', service: 'ec2',
      region: 'us-east-1', resource_type: 'm5.large', count: 2, term: 3,
      payment: 'all-upfront', upfront_cost: 36000, monthly_cost: 0, savings: 900,
    },
    {
      id: 'v-3-partial', provider: 'aws', cloud_account_id: 'a1', service: 'ec2',
      region: 'us-east-1', resource_type: 'm5.large', count: 2, term: 3,
      payment: 'partial-upfront', upfront_cost: 18000, monthly_cost: 300, savings: 850,
    },
    {
      id: 'v-1-all', provider: 'aws', cloud_account_id: 'a1', service: 'ec2',
      region: 'us-east-1', resource_type: 'm5.large', count: 2, term: 1,
      payment: 'all-upfront', upfront_cost: 12000, monthly_cost: 0, savings: 700,
    },
    {
      id: 'v-1-no', provider: 'aws', cloud_account_id: 'a1', service: 'ec2',
      region: 'us-east-1', resource_type: 'm5.large', count: 1, term: 1,
      payment: 'no-upfront', upfront_cost: 0, monthly_cost: 800, savings: 500,
    },
  ];
}

/** Drains the microtask queue enough for the async handlers under test to settle. */
async function flush(): Promise<void> {
  for (let i = 0; i < 6; i++) await Promise.resolve();
}

// ── DOM / mock scaffolding ────────────────────────────────────────────────────

beforeEach(() => {
  document.body.replaceChildren();

  const opportunitiesTab = document.createElement('div');
  opportunitiesTab.id = 'opportunities-tab';
  opportunitiesTab.className = 'tab-content active';
  const summaryEl = document.createElement('div');
  summaryEl.id = 'recommendations-summary';
  const listEl = document.createElement('div');
  listEl.id = 'recommendations-list';
  opportunitiesTab.appendChild(summaryEl);
  opportunitiesTab.appendChild(listEl);
  document.body.appendChild(opportunitiesTab);

  const purchaseModal = document.createElement('div');
  purchaseModal.id = 'purchase-modal';
  purchaseModal.className = 'hidden';
  const purchaseDetails = document.createElement('div');
  purchaseDetails.id = 'purchase-details';
  purchaseModal.appendChild(purchaseDetails);
  document.body.appendChild(purchaseModal);

  const executeBtn = document.createElement('button');
  executeBtn.id = 'execute-purchase-btn';
  document.body.appendChild(executeBtn);

  setupEventListeners();

  jest.clearAllMocks();
  clearPurchaseModalRecommendations();
  clearFanOutBuckets();

  // loadBulkPurchaseState() (setup.ts's localStorage mock defaults getItem to
  // null) only reads cachedGlobalDefaultPayment when a raw value is present —
  // otherwise it falls back to the hardcoded 'all-upfront' default and never
  // consults GlobalConfig. Seed a truthy (capacity-only) value so the
  // bulk-purchase toolbar picks up the mocked getConfig() default_payment;
  // tests that need a specific capacity override this per-test.
  (localStorage.getItem as jest.Mock).mockReturnValue('{}');
  (api.listAccountServiceOverrides as jest.Mock).mockResolvedValue([]);
  (api.getConfig as jest.Mock).mockResolvedValue({ global: {} });
  (api.executePurchase as jest.Mock).mockResolvedValue({
    execution_id: 'exec-aaaaaaaa',
    email_sent: true,
    approval_recipient: 'approver@example.com',
  });
  (state.getCurrentUser as jest.Mock).mockReturnValue({
    id: 'u-admin', email: 'admin@example.com', groups: [ADMINISTRATORS_GROUP_ID, PURCHASER_GROUP_ID],
  });
  // Every #1903 test needs the full loaded cell so pricedCellVariant /
  // cellTermOptions / cellPaymentOptions can find the sibling rows.
  (state.getRecommendations as jest.Mock).mockReturnValue(buildRows());
});

// ── #1903: purchase modal re-prices on Term/Payment change ───────────────────

describe('Issue #1903: purchase modal re-prices on Term/Payment change', () => {
  test('T1 term change re-prices the submitted body', async () => {
    const rows = buildRows();
    const v3all = rows.find((r) => r.id === 'v-3-all')!;

    await openPurchaseModal([v3all]);

    const termSelect = document.querySelector<HTMLSelectElement>('.purchase-row-term')!;
    termSelect.value = '1';
    termSelect.dispatchEvent(new Event('change'));

    const tr = document.querySelector<HTMLTableRowElement>('.purchase-modal-table tbody tr')!;
    expect(tr.cells[5]!.textContent).toBe(formatCurrency(12000));
    expect(tr.cells[6]!.textContent).toBe(formatCurrency(0));
    expect(tr.cells[7]!.textContent).toBe(formatCurrency(700));
    expect(document.getElementById('purchase-modal-total-upfront')?.textContent).toContain(formatCurrency(12000));

    (document.getElementById('execute-purchase-btn') as HTMLButtonElement).click();
    await flush();

    expect(api.executePurchase).toHaveBeenCalledTimes(1);
    const body = (api.executePurchase as jest.Mock).mock.calls[0]![0] as Array<Record<string, unknown>>;
    expect(body[0]).toMatchObject({
      id: 'v-1-all', term: 1, payment: 'all-upfront', upfront_cost: 12000, monthly_cost: 0, savings: 700,
    });
  });

  test('T2 payment change re-prices the submitted body', async () => {
    const rows = buildRows();
    const v1all = rows.find((r) => r.id === 'v-1-all')!;

    await openPurchaseModal([v1all]);

    const paymentSelect = document.querySelector<HTMLSelectElement>('.purchase-row-payment')!;
    paymentSelect.value = 'no-upfront';
    paymentSelect.dispatchEvent(new Event('change'));

    (document.getElementById('execute-purchase-btn') as HTMLButtonElement).click();
    await flush();

    expect(api.executePurchase).toHaveBeenCalledTimes(1);
    const body = (api.executePurchase as jest.Mock).mock.calls[0]![0] as Array<Record<string, unknown>>;
    expect(body[0]).toMatchObject({
      id: 'v-1-no', term: 1, payment: 'no-upfront', upfront_cost: 0, monthly_cost: 800, count: 1,
    });
  });

  test('T3 options are only priced variants', async () => {
    const rows = buildRows();
    const v3all = rows.find((r) => r.id === 'v-3-all')!;

    await openPurchaseModal([v3all]);

    const termSelect = document.querySelector<HTMLSelectElement>('.purchase-row-term')!;
    const paymentSelect = document.querySelector<HTMLSelectElement>('.purchase-row-payment')!;
    expect(Array.from(termSelect.options).map((o) => o.value)).toEqual(['1', '3']);
    expect(Array.from(paymentSelect.options).map((o) => o.value)).toEqual(['all-upfront', 'partial-upfront']);

    // Azure cell with a single loaded variant.
    const azureRec: LocalRecommendation = {
      id: 'az-1', provider: 'azure', cloud_account_id: 'a2', service: 'compute',
      region: 'eastus', resource_type: 'Standard_D2s_v3', count: 1, term: 3,
      payment: 'upfront', upfront_cost: 500, savings: 100,
    };
    (state.getRecommendations as jest.Mock).mockReturnValue([azureRec]);
    clearPurchaseModalRecommendations();

    await openPurchaseModal([azureRec]);

    const termSelect2 = document.querySelector<HTMLSelectElement>('.purchase-row-term')!;
    const paymentSelect2 = document.querySelector<HTMLSelectElement>('.purchase-row-payment')!;
    expect(Array.from(termSelect2.options).map((o) => o.value)).toEqual(['3']);
    expect(Array.from(paymentSelect2.options).map((o) => o.value)).toEqual(['all-upfront']);
  });

  test('T4 capacity scaling survives a swap (bulk path)', async () => {
    const rows = buildRows();
    (localStorage.getItem as jest.Mock).mockReturnValue(JSON.stringify({ capacity: 50 }));
    (api.getRecommendations as jest.Mock).mockResolvedValue({ summary: {}, recommendations: rows, regions: [] });
    (state.getRecommendations as jest.Mock).mockReturnValue(rows);
    (state.getVisibleRecommendations as jest.Mock).mockReturnValue(rows);
    (state.getSelectedRecommendationIDs as jest.Mock).mockReturnValue(new Set(['v-3-all']));

    await loadRecommendations();
    (document.getElementById('bulk-purchase-btn') as HTMLButtonElement).click();
    await flush();

    const tr = document.querySelector<HTMLTableRowElement>('.purchase-modal-table tbody tr')!;
    expect(tr.cells[4]!.textContent).toBe('1');
    expect(tr.cells[5]!.textContent).toBe(formatCurrency(18000));

    const termSelect = document.querySelector<HTMLSelectElement>('.purchase-row-term')!;
    termSelect.value = '1';
    termSelect.dispatchEvent(new Event('change'));

    const tr2 = document.querySelector<HTMLTableRowElement>('.purchase-modal-table tbody tr')!;
    expect(tr2.cells[5]!.textContent).toBe(formatCurrency(6000));

    (document.getElementById('execute-purchase-btn') as HTMLButtonElement).click();
    await flush();

    expect(api.executePurchase).toHaveBeenCalledWith(
      expect.arrayContaining([expect.objectContaining({ id: 'v-1-all', count: 1, recommended_count: 2, upfront_cost: 6000 })]),
      50,
      undefined,
    );
  });

  test('T5 zero-unit variant is refused and the row keeps its price', async () => {
    const rows = buildRows();
    (localStorage.getItem as jest.Mock).mockReturnValue(JSON.stringify({ capacity: 50 }));
    (api.getRecommendations as jest.Mock).mockResolvedValue({ summary: {}, recommendations: rows, regions: [] });
    (state.getRecommendations as jest.Mock).mockReturnValue(rows);
    (state.getVisibleRecommendations as jest.Mock).mockReturnValue(rows);
    (state.getSelectedRecommendationIDs as jest.Mock).mockReturnValue(new Set(['v-3-all']));

    await loadRecommendations();
    (document.getElementById('bulk-purchase-btn') as HTMLButtonElement).click();
    await flush();

    const termSelect = document.querySelector<HTMLSelectElement>('.purchase-row-term')!;
    termSelect.value = '1';
    termSelect.dispatchEvent(new Event('change'));

    // The term-change re-render replaced the row — re-query the fresh Payment select.
    const paymentSelect = document.querySelector<HTMLSelectElement>('.purchase-row-payment')!;
    paymentSelect.value = 'no-upfront';
    paymentSelect.dispatchEvent(new Event('change'));

    expect(showToast).toHaveBeenCalledWith(expect.objectContaining({ kind: 'warning' }));
    expect(paymentSelect.value).toBe('all-upfront');

    (document.getElementById('execute-purchase-btn') as HTMLButtonElement).click();
    await flush();

    const body = (api.executePurchase as jest.Mock).mock.calls[0]![0] as Array<Record<string, unknown>>;
    expect(body[0]).toMatchObject({ id: 'v-1-all', upfront_cost: 6000 });
  });

  test('T6 account override re-prices at open', async () => {
    const rows = buildRows();
    const v3all = rows.find((r) => r.id === 'v-3-all')!;

    (api.listAccountServiceOverrides as jest.Mock).mockResolvedValue([
      { id: 'ovr-1', account_id: 'a1', provider: 'aws', service: 'ec2', payment: 'partial-upfront' },
    ]);
    await openPurchaseModal([v3all]);

    const live = getPurchaseModalRecommendations();
    expect(live[0]).toMatchObject({ id: 'v-3-partial', payment: 'partial-upfront', upfront_cost: 18000 });
    expect(document.querySelector('.purchase-row-payment-source')).not.toBeNull();
    expect(document.querySelector<HTMLTableRowElement>('.purchase-modal-table tbody tr')!.cells[5]!.textContent)
      .toBe(formatCurrency(18000));

    clearPurchaseModalRecommendations();
    (api.listAccountServiceOverrides as jest.Mock).mockResolvedValue([
      { id: 'ovr-2', account_id: 'a1', provider: 'aws', service: 'ec2', payment: 'no-upfront' },
    ]);
    await openPurchaseModal([v3all]);

    const live2 = getPurchaseModalRecommendations();
    expect(live2[0]).toMatchObject({ id: 'v-3-all', payment: 'all-upfront', upfront_cost: 36000 });
    expect(document.querySelector('.purchase-row-payment-source')).toBeNull();
  });

  test('T7 direct-execute warning follows a term change', async () => {
    const rows = buildRows();
    const v3all = rows.find((r) => r.id === 'v-3-all')!;

    await openPurchaseModal([v3all]);

    const directRadio = document.getElementById('execute-mode-direct') as HTMLInputElement;
    expect(directRadio).not.toBeNull();
    directRadio.click();
    directRadio.dispatchEvent(new Event('change', { bubbles: true }));

    expect(document.querySelector('.direct-execute-warning')?.textContent).toContain('36,000.00');

    const termSelect = document.querySelector<HTMLSelectElement>('.purchase-row-term')!;
    termSelect.value = '1';
    termSelect.dispatchEvent(new Event('change'));

    expect(document.querySelector('.direct-execute-warning')?.textContent).toContain('12,000.00');
  });
});
