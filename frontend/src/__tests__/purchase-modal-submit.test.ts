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

import { handleExecutePurchase, setupEventListeners } from '../app';
import * as api from '../api';
import * as state from '../state';
import { showToast } from '../toast';
import { confirmDialog } from '../confirmDialog';
import {
  openPurchaseModal,
  getPurchaseModalRecommendations,
  clearPurchaseModalRecommendations,
  getFanOutBuckets,
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

function deferred<T>(): {
  promise: Promise<T>;
  resolve: (value: T) => void;
} {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((res) => {
    resolve = res;
  });
  return { promise, resolve };
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

  test('busy single purchase stays disabled while its request is pending', async () => {
    const request = deferred<Awaited<ReturnType<typeof api.executePurchase>>>();
    (api.executePurchase as jest.Mock).mockReturnValue(request.promise);
    const rows = buildRows();
    await openPurchaseModal([rows[0]!]);

    const executeBtn = document.getElementById('execute-purchase-btn') as HTMLButtonElement;
    executeBtn.click();
    await flush();
    expect(api.executePurchase).toHaveBeenCalledTimes(1);

    const termSelect = document.querySelector<HTMLSelectElement>('.purchase-row-term')!;
    termSelect.value = '1';
    termSelect.dispatchEvent(new Event('change'));
    const include = document.querySelector<HTMLInputElement>('.purchase-modal-row-include')!;
    include.checked = false;
    include.dispatchEvent(new Event('change'));
    include.checked = true;
    include.dispatchEvent(new Event('change'));

    expect(executeBtn.disabled).toBe(true);
    executeBtn.click();
    await flush();
    expect(api.executePurchase).toHaveBeenCalledTimes(1);

    request.resolve({
      execution_id: 'exec-single',
      status: 'pending',
      email_sent: true,
      approval_recipient: 'approver@example.com',
    });
    await flush();
    expect(executeBtn.dataset['submitting']).toBeUndefined();
  });

  test('confirmation cancel restores normal single-purchase submission', async () => {
    (confirmDialog as jest.Mock)
      .mockResolvedValueOnce(false)
      .mockResolvedValueOnce(true);
    const rows = buildRows();
    await openPurchaseModal([rows[0]!]);

    const executeBtn = document.getElementById('execute-purchase-btn') as HTMLButtonElement;
    executeBtn.click();
    await flush();

    expect(api.executePurchase).not.toHaveBeenCalled();
    expect(executeBtn.dataset['submitting']).toBeUndefined();
    expect(executeBtn.disabled).toBe(false);

    executeBtn.click();
    await flush();
    expect(api.executePurchase).toHaveBeenCalledTimes(1);
  });
});

// ── #1904: fan-out modal skips incompatible buckets ──────────────────────────

describe('Issue #1904: fan-out modal skips incompatible buckets', () => {
  function buildFanOutRows(): LocalRecommendation[] {
    return [
      {
        id: 'ec2-1', provider: 'aws', cloud_account_id: 'a1', service: 'ec2',
        region: 'us-east-1', resource_type: 'm5.large', term: 1, payment: 'no-upfront',
        count: 1, upfront_cost: 0, monthly_cost: 100, savings: 50,
      },
      {
        id: 'rds-3', provider: 'aws', cloud_account_id: 'a1', service: 'rds',
        region: 'us-east-1', resource_type: 'db.r5.large', term: 3, payment: undefined,
        count: 1, upfront_cost: 1000, savings: 200,
      },
    ];
  }

  test('T8 skipped bucket is not submitted and not totalled', async () => {
    const [ec2Rec, rdsRec] = buildFanOutRows();
    (api.getConfig as jest.Mock).mockResolvedValue({ global: { default_payment: 'no-upfront' } });
    (api.getRecommendations as jest.Mock).mockResolvedValue({
      summary: {}, recommendations: [ec2Rec, rdsRec], regions: [],
    });
    (state.getRecommendations as jest.Mock).mockReturnValue([ec2Rec, rdsRec]);
    (state.getVisibleRecommendations as jest.Mock).mockReturnValue([ec2Rec, rdsRec]);
    (state.getSelectedRecommendationIDs as jest.Mock).mockReturnValue(new Set(['ec2-1', 'rds-3']));

    await loadRecommendations();
    (document.getElementById('bulk-purchase-btn') as HTMLButtonElement).click();
    await flush();

    const errorSections = document.querySelectorAll('.fanout-bucket-error');
    expect(errorSections).toHaveLength(1);
    expect(errorSections[0]!.textContent).toContain('will be skipped');

    const summaryText = document.getElementById('fanout-summary')!.textContent ?? '';
    expect(summaryText).toContain('Will send 1 approval email');
    expect(summaryText).toContain('1 incompatible bucket will be skipped');

    const totalUpfrontLine = Array.from(document.querySelectorAll('#fanout-summary p'))
      .find((p) => p.textContent?.startsWith('Total upfront'))!;
    expect(totalUpfrontLine.querySelector('strong')!.textContent).toBe(formatCurrency(0));
    const totalCommitmentsLine = Array.from(document.querySelectorAll('#fanout-summary p'))
      .find((p) => p.textContent?.startsWith('Total commitments'))!;
    expect(totalCommitmentsLine.querySelector('strong')!.textContent).toBe('1');

    (document.getElementById('execute-purchase-btn') as HTMLButtonElement).click();
    await flush();

    expect(api.executePurchase).toHaveBeenCalledTimes(1);
    const body = (api.executePurchase as jest.Mock).mock.calls[0]![0] as Array<Record<string, unknown>>;
    for (const rec of body) {
      expect(rec['service']).toBe('ec2');
      expect(rec['id']).not.toBe('rds-3');
    }
  });

  test('T9 repairing the bucket un-skips it everywhere', async () => {
    const [ec2Rec, rdsRec] = buildFanOutRows();
    (api.getConfig as jest.Mock).mockResolvedValue({ global: { default_payment: 'no-upfront' } });
    (api.getRecommendations as jest.Mock).mockResolvedValue({
      summary: {}, recommendations: [ec2Rec, rdsRec], regions: [],
    });
    (state.getRecommendations as jest.Mock).mockReturnValue([ec2Rec, rdsRec]);
    (state.getVisibleRecommendations as jest.Mock).mockReturnValue([ec2Rec, rdsRec]);
    (state.getSelectedRecommendationIDs as jest.Mock).mockReturnValue(new Set(['ec2-1', 'rds-3']));

    await loadRecommendations();
    (document.getElementById('bulk-purchase-btn') as HTMLButtonElement).click();
    await flush();

    const rdsSection = Array.from(document.querySelectorAll<HTMLElement>('.fanout-bucket'))
      .find((s) => s.querySelector('.fanout-bucket-error') != null)!;
    const rdsPaymentSelect = rdsSection.querySelector<HTMLSelectElement>('.fanout-bucket-payment')!;
    rdsPaymentSelect.value = 'partial-upfront';
    rdsPaymentSelect.dispatchEvent(new Event('change'));

    expect(rdsSection.querySelector('.fanout-bucket-ok')).not.toBeNull();
    const summaryText = document.getElementById('fanout-summary')!.textContent ?? '';
    expect(summaryText).toContain('Will send 2 approval emails');
    expect(summaryText).not.toContain('skipped');
    const totalUpfrontLine = Array.from(document.querySelectorAll('#fanout-summary p'))
      .find((p) => p.textContent?.startsWith('Total upfront'))!;
    expect(totalUpfrontLine.querySelector('strong')!.textContent).toBe(formatCurrency(1000));

    (document.getElementById('execute-purchase-btn') as HTMLButtonElement).click();
    await flush();

    expect(api.executePurchase).toHaveBeenCalledTimes(2);
  });

  test('T10 nothing submittable disables Execute', async () => {
    const [, rdsRec] = buildFanOutRows();
    (api.getConfig as jest.Mock).mockResolvedValue({ global: { default_payment: 'no-upfront' } });
    (api.getRecommendations as jest.Mock).mockResolvedValue({
      summary: {}, recommendations: [rdsRec], regions: [],
    });
    (state.getRecommendations as jest.Mock).mockReturnValue([rdsRec]);
    (state.getVisibleRecommendations as jest.Mock).mockReturnValue([rdsRec]);
    (state.getSelectedRecommendationIDs as jest.Mock).mockReturnValue(new Set(['rds-3']));

    await loadRecommendations();
    (document.getElementById('bulk-purchase-btn') as HTMLButtonElement).click();
    await flush();

    const executeBtn = document.getElementById('execute-purchase-btn') as HTMLButtonElement;
    expect(executeBtn.disabled).toBe(true);
    expect(getFanOutBuckets()).toEqual([]);
    expect(document.getElementById('fanout-summary')!.textContent).toContain('Will send 0 approval emails');

    executeBtn.click();
    await flush();

    expect(api.executePurchase).not.toHaveBeenCalled();
  });
  // Regression for the double-scale CodeRabbit found on #2071. loadedCellVariants
  // pushes `rec` itself when the loaded list no longer holds its id, and rec is
  // already scaled, so re-scaling halved count and cost a second time. Uses a
  // count of 4 deliberately: at count 2 the second scale floors to zero units
  // and pricedCellVariant returns null, so the row is left alone and the test
  // would pass with or without the guard.
  test('T11 the fallback row is not re-scaled when the loaded list is replaced during open', async () => {
    const rec: LocalRecommendation = {
      id: 'x-1-all', provider: 'aws', cloud_account_id: 'a1', service: 'ec2',
      region: 'us-east-1', resource_type: 'm5.xlarge', count: 4, term: 1,
      payment: 'all-upfront', upfront_cost: 24000, monthly_cost: 0, savings: 1400,
    };
    (localStorage.getItem as jest.Mock).mockReturnValue(JSON.stringify({ capacity: 50 }));
    (api.getRecommendations as jest.Mock).mockResolvedValue({ summary: {}, recommendations: [rec], regions: [] });
    (state.getRecommendations as jest.Mock).mockReturnValue([rec]);
    (state.getVisibleRecommendations as jest.Mock).mockReturnValue([rec]);
    (state.getSelectedRecommendationIDs as jest.Mock).mockReturnValue(new Set(['x-1-all']));
    // A reload landing during openPurchaseModal's override fetch replaces the
    // loaded list; the override matches the rec's own payment, so the seed
    // path resolves to the fallback push, which is `rec` itself.
    (api.listAccountServiceOverrides as jest.Mock).mockImplementation(async () => {
      (state.getRecommendations as jest.Mock).mockReturnValue([]);
      return [{ id: 'ovr-1', account_id: 'a1', provider: 'aws', service: 'ec2', payment: 'all-upfront' }];
    });

    await loadRecommendations();
    (document.getElementById('bulk-purchase-btn') as HTMLButtonElement).click();
    await flush();

    expect(getPurchaseModalRecommendations()[0]).toMatchObject({
      id: 'x-1-all', count: 2, recommended_count: 4, upfront_cost: 12000,
    });

    (document.getElementById('execute-purchase-btn') as HTMLButtonElement).click();
    await flush();

    expect(api.executePurchase).toHaveBeenCalledWith(
      expect.arrayContaining([expect.objectContaining({
        id: 'x-1-all', count: 2, recommended_count: 4, upfront_cost: 12000,
      })]),
      50,
      undefined,
    );
  });

  test('busy fan-out stays disabled while its requests are pending', async () => {
    const requests = [
      deferred<Awaited<ReturnType<typeof api.executePurchase>>>(),
      deferred<Awaited<ReturnType<typeof api.executePurchase>>>(),
    ];
    (api.executePurchase as jest.Mock)
      .mockReturnValueOnce(requests[0]!.promise)
      .mockReturnValueOnce(requests[1]!.promise);
    const rows = buildFanOutRows();
    (api.getConfig as jest.Mock).mockResolvedValue({ global: { default_payment: 'partial-upfront' } });
    (api.getRecommendations as jest.Mock).mockResolvedValue({
      summary: {}, recommendations: rows, regions: [],
    });
    (state.getRecommendations as jest.Mock).mockReturnValue(rows);
    (state.getVisibleRecommendations as jest.Mock).mockReturnValue(rows);
    (state.getSelectedRecommendationIDs as jest.Mock).mockReturnValue(new Set(['ec2-1', 'rds-3']));

    await loadRecommendations();
    (document.getElementById('bulk-purchase-btn') as HTMLButtonElement).click();
    await flush();
    const executeBtn = document.getElementById('execute-purchase-btn') as HTMLButtonElement;
    executeBtn.click();
    await flush();
    expect(api.executePurchase).toHaveBeenCalledTimes(2);

    const paymentSelect = document.querySelector<HTMLSelectElement>('.fanout-bucket-payment')!;
    paymentSelect.value = 'partial-upfront';
    paymentSelect.dispatchEvent(new Event('change'));

    expect(executeBtn.disabled).toBe(true);
    executeBtn.click();
    await flush();
    expect(api.executePurchase).toHaveBeenCalledTimes(2);

    for (const [i, request] of requests.entries()) {
      request.resolve({
        execution_id: `exec-fanout-${i}`,
        status: 'pending',
        email_sent: true,
        approval_recipient: 'approver@example.com',
      });
    }
    await flush();
    expect(executeBtn.dataset['submitting']).toBeUndefined();
  });

  test('fan-out clears submitting state when result processing throws', async () => {
    const rows = buildFanOutRows();
    (api.getConfig as jest.Mock).mockResolvedValue({ global: { default_payment: 'partial-upfront' } });
    (api.getRecommendations as jest.Mock).mockResolvedValue({
      summary: {}, recommendations: rows, regions: [],
    });
    (state.getRecommendations as jest.Mock).mockReturnValue(rows);
    (state.getVisibleRecommendations as jest.Mock).mockReturnValue(rows);
    (state.getSelectedRecommendationIDs as jest.Mock).mockReturnValue(new Set(['ec2-1', 'rds-3']));
    (api.executePurchase as jest.Mock)
      .mockResolvedValueOnce(null)
      .mockResolvedValueOnce({
        execution_id: 'exec-valid',
        status: 'pending',
        email_sent: true,
      });

    await loadRecommendations();
    (document.getElementById('bulk-purchase-btn') as HTMLButtonElement).click();
    await flush();
    const executeBtn = document.getElementById('execute-purchase-btn') as HTMLButtonElement;
    await expect(handleExecutePurchase()).rejects.toThrow(TypeError);

    expect(api.executePurchase).toHaveBeenCalledTimes(2);
    expect(executeBtn.dataset['submitting']).toBeUndefined();
    expect(executeBtn.disabled).toBe(false);
  });
});
