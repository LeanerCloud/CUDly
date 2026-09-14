/**
 * Issue #1903 / #1904: the purchase modal's Term and Payment selects must
 * re-price the row (not just relabel it), and the fan-out modal must never
 * submit a bucket it told the user would be skipped.
 *
 * These tests drive app.ts and recommendations.ts and assert that the
 * displayed variant matches the body passed to api.executePurchase.
 * The backend independently resolves identity and pricing from stored
 * recommendations (internal/api/purchase_pricing.go).
 */

// Mocks must precede imports.

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

// Imports

import { handleExecutePurchase, setupEventListeners } from '../app';
import * as api from '../api';
import * as state from '../state';
import { showToast } from '../toast';
import { confirmDialog } from '../confirmDialog';
import { openModal } from '../modal';
import {
  openPurchaseModal,
  getPurchaseModalRecommendations,
  clearPurchaseModalRecommendations,
  getFanOutBuckets,
  clearFanOutBuckets,
  loadRecommendations,
  seedGlobalDefaults,
} from '../recommendations';
import { formatCurrency } from '../utils';
import { ADMINISTRATORS_GROUP_ID, PURCHASER_GROUP_ID } from '../permissions';
import type { LocalRecommendation } from '../types';

// Fixtures

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

// DOM / mock scaffolding

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

  const closeBtn = document.createElement('button');
  closeBtn.id = 'close-purchase-modal-btn';
  purchaseModal.appendChild(closeBtn);

  setupEventListeners();

  jest.clearAllMocks();
  clearPurchaseModalRecommendations();
  clearFanOutBuckets();
  seedGlobalDefaults(3, 'all-upfront');

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

// #1903: purchase modal re-prices on Term/Payment change

describe('Issue #1903: purchase modal re-prices on Term/Payment change', () => {
  test.each(['active', 'closed', 'executed'])('legacy payment delayed open cannot restore rows after newer modal is %s', async (action) => {
    const firstFetch = deferred<Awaited<ReturnType<typeof api.listAccountServiceOverrides>>>();
    (api.listAccountServiceOverrides as jest.Mock)
      .mockReturnValueOnce(firstFetch.promise)
      .mockResolvedValueOnce([]);
    const first = { ...buildRows()[0]!, id: 'first', resource_type: 'c5.large' };
    const second = { ...buildRows()[1]!, id: 'second', resource_type: 'm6i.large' };
    (state.getRecommendations as jest.Mock).mockReturnValue([first, second]);

    const pendingFirst = openPurchaseModal([first]);
    await openPurchaseModal([second]);
    expect(getPurchaseModalRecommendations()).toEqual([second]);
    if (action === 'closed') (document.getElementById('close-purchase-modal-btn') as HTMLButtonElement).click();
    if (action === 'executed') await handleExecutePurchase();
    const rendered = document.getElementById('purchase-details')!.innerHTML;
    const openCount = (openModal as jest.Mock).mock.calls.length;

    firstFetch.resolve([]);
    await pendingFirst;

    expect(getPurchaseModalRecommendations()).toEqual(action === 'active' ? [second] : []);
    expect(document.getElementById('purchase-details')!.innerHTML).toBe(rendered);
    expect(openModal).toHaveBeenCalledTimes(openCount);
    await handleExecutePurchase();
    if (action === 'closed') {
      expect(api.executePurchase).not.toHaveBeenCalled();
    } else {
      expect(api.executePurchase).toHaveBeenCalledTimes(1);
      expect(api.executePurchase).toHaveBeenCalledWith([expect.objectContaining(second)], 100, undefined);
    }
  });

  test.each([undefined, '', 'unrecognized'])('legacy payment %j resolves a complete priced variant before submission', async (payment) => {
    const details = { platform: 'Linux/UNIX', tenancy: 'default', scope: 'Region' };
    const legacy = { ...buildRows()[0]!, id: 'legacy', payment, upfront_cost: 17, monthly_cost: 29, details };
    const priced = { ...buildRows()[0]!, count: 4, details: { ...details, vcpu: 2 } };
    (state.getRecommendations as jest.Mock).mockReturnValue([legacy, priced]);

    await openPurchaseModal([legacy]);

    const row = document.querySelector<HTMLTableRowElement>('.purchase-modal-table tbody tr')!;
    expect(row.cells[4]!.textContent).toBe('4');
    expect(row.cells[5]!.textContent).toBe(formatCurrency(priced.upfront_cost));
    expect(row.cells[6]!.textContent).toBe(formatCurrency(priced.monthly_cost!));
    expect(getPurchaseModalRecommendations()).toEqual([expect.objectContaining(priced)]);
    await handleExecutePurchase();
    expect(api.executePurchase).toHaveBeenCalledWith([expect.objectContaining(priced)], 100, undefined);
  });

  test.each([true, false])('legacy payment uses configured preference only when priced (available: %s)', async (available) => {
    const legacy = { ...buildRows()[0]!, id: 'legacy', payment: '' };
    const all = buildRows()[0]!;
    const partial = buildRows()[1]!;
    seedGlobalDefaults(3, 'partial-upfront');
    (state.getRecommendations as jest.Mock).mockReturnValue(available ? [legacy, all, partial] : [legacy, all]);

    await openPurchaseModal([legacy]);

    const expected = available ? partial : all;
    expect(getPurchaseModalRecommendations()).toEqual([expect.objectContaining(expected)]);
    expect(document.querySelector<HTMLSelectElement>('.purchase-row-payment')!.value).toBe(expected.payment);
  });

  test.each([false, true])('legacy payment resolves with account override fetch failure: %s', async (failed) => {
    const legacy = { ...buildRows()[0]!, id: 'legacy', payment: '' };
    (state.getRecommendations as jest.Mock).mockReturnValue([legacy, ...buildRows()]);
    if (failed) {
      (api.listAccountServiceOverrides as jest.Mock).mockRejectedValue(new Error('offline'));
    } else {
      (api.listAccountServiceOverrides as jest.Mock).mockResolvedValue([
        { id: 'ovr', account_id: 'a1', provider: 'aws', service: 'ec2', payment: 'partial-upfront' },
      ]);
    }

    await openPurchaseModal([legacy]);

    expect(getPurchaseModalRecommendations()[0]).toMatchObject(buildRows()[failed ? 0 : 1]!);
    expect(document.querySelector('.purchase-row-payment-source') !== null).toBe(!failed);
  });

  test.each([false, true])('legacy payment excludes an unavailable row (zero at capacity: %s)', async (zero) => {
    const legacy = { ...buildRows()[0]!, id: 'legacy', payment: '' };
    const priced = { ...buildRows()[0]!, count: 1 };
    (state.getRecommendations as jest.Mock).mockReturnValue(zero ? [legacy, priced] : [legacy]);

    await openPurchaseModal([legacy], zero ? 50 : 100);

    const notice = document.querySelector('.purchase-modal-unavailable');
    expect(notice?.getAttribute('role')).toBe('alert');
    for (const label of ['a1', 'ec2', 'm5.large', 'us-east-1', 'excluded']) expect(notice?.textContent).toContain(label);
    expect(getPurchaseModalRecommendations()).toEqual([]);
    expect(document.querySelectorAll('.purchase-modal-table tbody tr')).toHaveLength(0);
    expect((document.getElementById('execute-purchase-btn') as HTMLButtonElement).disabled).toBe(true);
    await handleExecutePurchase();
    expect(api.executePurchase).not.toHaveBeenCalled();

    clearPurchaseModalRecommendations();
    await openPurchaseModal([priced]);
    expect(document.querySelector('.purchase-modal-unavailable')).toBeNull();
    expect(getPurchaseModalRecommendations()).toEqual([expect.objectContaining(priced)]);
  });

  test('legacy payment skips a zero-unit preferred variant and scales the viable fallback once', async () => {
    const legacy = { ...buildRows()[0]!, id: 'legacy', payment: '', count: 1, recommended_count: 2 };
    const all = { ...buildRows()[0]!, count: 1 };
    const partial = buildRows()[1]!;
    (state.getRecommendations as jest.Mock).mockReturnValue([legacy, all, partial]);

    await openPurchaseModal([legacy], 50);

    expect(getPurchaseModalRecommendations()).toEqual([expect.objectContaining({
      ...partial, count: 1, recommended_count: 2, upfront_cost: 9000, monthly_cost: 150, savings: 425,
    })]);
  });

  test('legacy payment mixed bulk selection excludes unpriced rows from totals, selection and POST', async () => {
    const legacy = { ...buildRows()[0]!, id: 'legacy', payment: '' };
    const unavailable = { ...legacy, id: 'unavailable', resource_type: 'm6i.large' };
    const priced = buildRows()[0]!;
    const rows = [legacy, unavailable, priced];
    (api.getRecommendations as jest.Mock).mockResolvedValue({ summary: {}, recommendations: rows, regions: [] });
    (state.getRecommendations as jest.Mock).mockReturnValue(rows);
    (state.getVisibleRecommendations as jest.Mock).mockReturnValue(rows);
    (state.getSelectedRecommendationIDs as jest.Mock).mockReturnValue(new Set([legacy.id, unavailable.id]));

    await loadRecommendations();
    (document.getElementById('bulk-purchase-btn') as HTMLButtonElement).click();
    await flush();

    expect(getFanOutBuckets()).toBeNull();
    expect(document.querySelector('.purchase-modal-unavailable')?.textContent).toContain('m6i.large');
    expect(document.querySelectorAll('.purchase-modal-table tbody tr')).toHaveLength(1);
    expect(document.getElementById('purchase-modal-totals-row')?.textContent).toContain(formatCurrency(priced.upfront_cost));
    const selectAll = document.getElementById('purchase-modal-select-all') as HTMLInputElement;
    selectAll.click();
    expect(getPurchaseModalRecommendations()).toEqual([]);
    selectAll.click();
    expect(getPurchaseModalRecommendations()).toEqual([expect.objectContaining(priced)]);
    (document.getElementById('execute-mode-direct') as HTMLInputElement).click();
    expect(document.querySelector('.direct-execute-warning')?.textContent).toContain('36,000.00');
    await handleExecutePurchase();
    expect(api.executePurchase).toHaveBeenCalledWith([expect.objectContaining(priced)], 100, 'direct');
  });

  test('legacy payment normalization preserves a valid Azure upfront price', async () => {
    const rec: LocalRecommendation = { ...buildRows()[0]!, provider: 'azure', service: 'compute', resource_type: 'Standard_D2s_v3', region: 'eastus', payment: 'upfront' };
    (state.getRecommendations as jest.Mock).mockReturnValue([rec]);

    await openPurchaseModal([rec]);

    expect(getPurchaseModalRecommendations()).toEqual([{ ...rec, payment: 'all-upfront' }]);
    await handleExecutePurchase();
    expect(api.executePurchase).toHaveBeenCalledWith([expect.objectContaining({ ...rec, payment: 'all-upfront' })], 100, undefined);
  });

  test.each<[string, string, string, Record<string, unknown>, unknown]>([
    ['ec2', 'platform', 'm5.large', { instance_type: 'm5.large', platform: 'Linux/UNIX', tenancy: 'default', scope: 'Region' }, 'Windows'],
    ['ec2', 'tenancy', 'm5.large', { instance_type: 'm5.large', platform: 'Linux/UNIX', tenancy: 'default', scope: 'Region' }, 'dedicated'],
    ['ec2', 'scope', 'm5.large', { instance_type: 'm5.large', platform: 'Linux/UNIX', tenancy: 'default', scope: 'Region' }, 'Availability Zone'],
    ['compute', 'platform', 'm5.large', { instance_type: 'm5.large', platform: 'Linux/UNIX', tenancy: 'default', scope: 'Region' }, 'Windows'],
    ['rds', 'az_config', 'db.r5.large', { engine: 'postgres', az_config: 'single-az' }, 'multi-az'],
    ['relational-db', 'az_config', 'db.r5.large', { engine: 'postgres', az_config: 'single-az' }, 'multi-az'],
    ['rds', 'engine', 'db.r5.large', { engine: 'postgres', az_config: 'single-az' }, 'mysql'],
    ['elasticache', 'engine', 'cache.r6g.large', { engine: 'redis', node_type: 'cache.r6g.large' }, 'memcached'],
    ['cache', 'engine', 'cache.r6g.large', { engine: 'redis', node_type: 'cache.r6g.large' }, 'memcached'],
    ['savingsplans', 'plan_type', '', { plan_type: 'Compute', hourly_commitment: 1 }, 'SageMaker'],
    ['savings-plans-ec2instance', 'instance_family', '', { plan_type: 'EC2Instance', instance_family: 'm5', region: 'us-east-1', hourly_commitment: 1 }, 'm6i'],
    ['savings-plans-ec2instance', 'region', '', { plan_type: 'EC2Instance', instance_family: 'm5', region: 'us-east-1', hourly_commitment: 1 }, 'us-west-2'],
  ])('purchase identity excludes %s variants with different %s', async (service, field, resourceType, details, otherValue) => {
    const savingsPlan = service === 'savingsplans' || service.startsWith('savings-plans');
    const original = { ...buildRows()[0]!, service, resource_type: resourceType, count: savingsPlan ? 1 : 2, region: savingsPlan ? '' : 'us-east-1', details };
    const other = { ...original, id: 'other-identity', term: 1, upfront_cost: 12000, details: { ...details, [field]: otherValue } };
    (state.getRecommendations as jest.Mock).mockReturnValue([other, original]);

    await openPurchaseModal([original]);

    const termSelect = document.querySelector<HTMLSelectElement>('.purchase-row-term')!;
    expect(Array.from(termSelect.options, (option) => option.value)).toEqual(['3']);
    expect(getPurchaseModalRecommendations()).toEqual([original]);
    (document.getElementById('execute-purchase-btn') as HTMLButtonElement).click();
    await flush();
    expect(api.executePurchase).toHaveBeenCalledTimes(1);
    expect((api.executePurchase as jest.Mock).mock.calls[0]![0]).toEqual([
      expect.objectContaining({ id: original.id, term: 3, details, upfront_cost: original.upfront_cost }),
    ]);
  });

  test.each([false, true])('purchase identity selects the matching priced EC2 variant (reverse order: %s)', async (reverse) => {
    const details = { instance_type: 'm5.large', platform: 'Linux/UNIX', tenancy: 'default', scope: 'Region' };
    const original = { ...buildRows()[0]!, details };
    const dedicated = { ...original, id: 'dedicated-1-all', term: 1, upfront_cost: 12000, details: { ...details, tenancy: 'dedicated' } };
    const matching = { ...original, id: 'default-1-no', term: 1, payment: 'no-upfront', upfront_cost: 0, monthly_cost: 800, savings: 500 };
    const loaded = [original, dedicated, matching];
    (state.getRecommendations as jest.Mock).mockReturnValue(reverse ? loaded.reverse() : loaded);

    await openPurchaseModal([original]);
    const termSelect = document.querySelector<HTMLSelectElement>('.purchase-row-term')!;
    termSelect.value = '1';
    termSelect.dispatchEvent(new Event('change'));

    const row = document.querySelector<HTMLTableRowElement>('.purchase-modal-table tbody tr')!;
    expect(row.cells[5]!.textContent).toBe(formatCurrency(0));
    expect(row.cells[6]!.textContent).toBe(formatCurrency(800));
    expect(document.querySelector<HTMLSelectElement>('.purchase-row-payment')!.value).toBe('no-upfront');
    expect(getPurchaseModalRecommendations()[0]).toMatchObject(matching);
    (document.getElementById('execute-purchase-btn') as HTMLButtonElement).click();
    await flush();
    expect(api.executePurchase).toHaveBeenCalledTimes(1);
    expect((api.executePurchase as jest.Mock).mock.calls[0]![0]).toEqual([
      expect.objectContaining(matching),
    ]);
  });

  test.each([undefined, null, [], 'invalid', { platform: 1 }, {}])('purchase identity does not match populated EC2 details to %j', async (otherDetails) => {
    const original = { ...buildRows()[0]!, details: { platform: 'Linux/UNIX', tenancy: 'default', scope: 'Region' } };
    const other = { ...original, id: 'missing-identity', term: 1, details: otherDetails };
    (state.getRecommendations as jest.Mock).mockReturnValue([other, original]);

    await openPurchaseModal([original]);

    expect(Array.from(document.querySelector<HTMLSelectElement>('.purchase-row-term')!.options, (option) => option.value))
      .toEqual(['3']);
    expect(getPurchaseModalRecommendations()[0]).toEqual(original);
  });

  test('purchase identity allows Savings Plans prices and offering IDs to change', async () => {
    const original = {
      ...buildRows()[0]!, service: 'savings-plans-ec2instance', resource_type: '', region: '', count: 1,
      details: { plan_type: 'EC2Instance', instance_family: 'm5', region: 'us-east-1', hourly_commitment: 1, offering_id: 'offering-3-all', coverage: '50.0%' },
    };
    const matching = {
      ...original, id: 'sp-1-no', term: 1, payment: 'no-upfront', upfront_cost: 0, monthly_cost: 1460, savings: 400,
      details: { ...original.details, hourly_commitment: 2, offering_id: 'offering-1-no', coverage: '40.0%' },
    };
    (state.getRecommendations as jest.Mock).mockReturnValue([original, matching]);

    await openPurchaseModal([original]);
    const termSelect = document.querySelector<HTMLSelectElement>('.purchase-row-term')!;
    termSelect.value = '1';
    termSelect.dispatchEvent(new Event('change'));

    expect(getPurchaseModalRecommendations()[0]).toMatchObject(matching);
    expect(document.querySelector<HTMLTableRowElement>('.purchase-modal-table tbody tr')!.cells[6]!.textContent)
      .toBe(formatCurrency(1460));
    (document.getElementById('execute-purchase-btn') as HTMLButtonElement).click();
    await flush();
    expect(api.executePurchase).toHaveBeenCalledTimes(1);
    expect((api.executePurchase as jest.Mock).mock.calls[0]![0]).toEqual([
      expect.objectContaining(matching),
    ]);
  });

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

  test('T3 capacity-aware payment options choose the viable alternate on term change', async () => {
    const details = { platform: 'Linux/UNIX', tenancy: 'default', scope: 'Region' };
    const rows = buildRows().filter((row) => row.id !== 'v-3-partial').map((row) => {
      if (row.id === 'v-1-all') return { ...row, details, count: 1, upfront_cost: 6000 };
      if (row.id === 'v-1-no') return {
        ...row, count: 2, recommended_count: 2, monthly_cost: 1600,
        details,
      };
      return { ...row, details };
    });
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

    const row = document.querySelector<HTMLTableRowElement>('.purchase-modal-table tbody tr')!;
    expect(row.cells[4]!.textContent).toBe('1');
    expect(row.cells[5]!.textContent).toBe(formatCurrency(0));
    expect(row.cells[6]!.textContent).toBe(formatCurrency(800));
    expect(document.querySelector<HTMLSelectElement>('.purchase-row-payment')!.value).toBe('no-upfront');
    expect(Array.from(document.querySelector<HTMLSelectElement>('.purchase-row-payment')!.options).map((o) => o.value))
      .toEqual(['no-upfront']);
    expect(getPurchaseModalRecommendations()[0]).toMatchObject({
      id: 'v-1-no', term: 1, payment: 'no-upfront', count: 1, recommended_count: 2,
      upfront_cost: 0, monthly_cost: 800,
    });
    expect(document.getElementById('purchase-modal-total-upfront')?.textContent).toContain(formatCurrency(0));
    (document.getElementById('execute-mode-direct') as HTMLInputElement).click();
    expect(document.querySelector('.direct-execute-warning')?.textContent).toContain(formatCurrency(0));

    (document.getElementById('execute-purchase-btn') as HTMLButtonElement).click();
    await flush();
    expect(api.executePurchase).toHaveBeenCalledWith(
      [expect.objectContaining({
        id: 'v-1-no', term: 1, payment: 'no-upfront', count: 1, recommended_count: 2,
        details: { platform: 'Linux/UNIX', tenancy: 'default', scope: 'Region' },
      })],
      50,
      'direct',
    );
  });

  test('T3 viable payment survives term swaps and starts from loaded count', async () => {
    const rows = [
      { ...buildRows()[0]!, id: 'v-3-no', payment: 'no-upfront' as const, count: 2, monthly_cost: 1600 },
      { ...buildRows()[2]!, count: 2 },
      { ...buildRows()[3]!, count: 2, recommended_count: 2, monthly_cost: 1600 },
    ];
    (localStorage.getItem as jest.Mock).mockReturnValue(JSON.stringify({ capacity: 50 }));
    (api.getRecommendations as jest.Mock).mockResolvedValue({ summary: {}, recommendations: rows, regions: [] });
    (state.getRecommendations as jest.Mock).mockReturnValue(rows);
    (state.getVisibleRecommendations as jest.Mock).mockReturnValue(rows);
    (state.getSelectedRecommendationIDs as jest.Mock).mockReturnValue(new Set(['v-3-no']));

    await loadRecommendations();
    (document.getElementById('bulk-purchase-btn') as HTMLButtonElement).click();
    await flush();

    const termSelect = document.querySelector<HTMLSelectElement>('.purchase-row-term')!;
    termSelect.value = '1';
    termSelect.dispatchEvent(new Event('change'));
    expect(getPurchaseModalRecommendations()[0]).toMatchObject({ id: 'v-1-no', payment: 'no-upfront', count: 1 });
    expect(Array.from(document.querySelector<HTMLSelectElement>('.purchase-row-payment')!.options).map((o) => o.value))
      .toEqual(['all-upfront', 'no-upfront']);

    const termSelectAgain = document.querySelector<HTMLSelectElement>('.purchase-row-term')!;
    termSelectAgain.value = '3';
    termSelectAgain.dispatchEvent(new Event('change'));
    expect(getPurchaseModalRecommendations()[0]).toMatchObject({ id: 'v-3-no', payment: 'no-upfront', count: 1 });
    const row = document.querySelector<HTMLTableRowElement>('.purchase-modal-table tbody tr')!;
    expect(row.cells[6]!.textContent).toBe(formatCurrency(800));
    expect(row.querySelector<HTMLSelectElement>('.purchase-row-term')!.value).toBe('3');
    expect(row.querySelector<HTMLSelectElement>('.purchase-row-payment')!.value).toBe('no-upfront');
  });

  test('T3 all-zero term restores the prior priced row', async () => {
    const rows = [
      { ...buildRows()[0]!, count: 2 },
      { ...buildRows()[2]!, count: 1, upfront_cost: 6000 },
      { ...buildRows()[3]!, count: 1, recommended_count: 1 },
    ];
    (localStorage.getItem as jest.Mock).mockReturnValue(JSON.stringify({ capacity: 50 }));
    (api.getRecommendations as jest.Mock).mockResolvedValue({ summary: {}, recommendations: rows, regions: [] });
    (state.getRecommendations as jest.Mock).mockReturnValue(rows);
    (state.getVisibleRecommendations as jest.Mock).mockReturnValue(rows);
    (state.getSelectedRecommendationIDs as jest.Mock).mockReturnValue(new Set(['v-3-all']));

    await loadRecommendations();
    (document.getElementById('bulk-purchase-btn') as HTMLButtonElement).click();
    await flush();

    const before = getPurchaseModalRecommendations()[0]!;
    const termSelect = document.querySelector<HTMLSelectElement>('.purchase-row-term')!;
    termSelect.value = '1';
    termSelect.dispatchEvent(new Event('change'));

    expect(showToast).toHaveBeenCalledWith(expect.objectContaining({ kind: 'warning' }));
    expect(termSelect.value).toBe('3');
    expect(getPurchaseModalRecommendations()[0]).toEqual(before);
    expect(document.querySelector<HTMLSelectElement>('.purchase-row-payment')!.value).toBe('all-upfront');
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

// #1904: fan-out modal skips incompatible buckets

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
