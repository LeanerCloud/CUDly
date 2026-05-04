/**
 * Recommendations module tests
 */
import { loadRecommendations, openPurchaseModal, getPurchaseModalRecommendations, refreshRecommendations, setupRecommendationsHandlers, clearRecommendationDetailCache, pickBestVariantPerCell, seedGlobalDefaults, effectiveMonthlySavings, effectiveSavingsPct, groupRecsByCell, cellSummary, pageLevelRange, resetExpandedCells } from '../recommendations';

// Mock the api module
jest.mock('../api', () => ({
  getRecommendations: jest.fn(),
  refreshRecommendations: jest.fn(),
  listAccounts: jest.fn().mockResolvedValue([]),
  // issue #223: getConfig is fetched on page load to resolve GlobalConfig
  // defaults (DefaultTerm + DefaultPayment). Default-empty global config so
  // pre-#223 tests retain their hardcoded-fallback behavior without extra setup.
  getConfig: jest.fn().mockResolvedValue({ global: {} }),
  // Issue #111: openFanOutModal now pre-fetches per-account service
  // overrides to seed each bucket's Payment default. Default-empty so
  // pre-#111 tests retain their toolbar-seeded behavior without any
  // mock setup; tests that specifically exercise the override seed
  // override this mock per-test.
  listAccountServiceOverrides: jest.fn().mockResolvedValue([]),
}));

// Mock the per-id detail endpoint module so the drawer-fetch tests can
// assert on call shape without going through the apiRequest layer.
// Default resolution returns a benign empty payload so tests that
// merely open + close the drawer (and don't care about the detail
// fetch) don't trip on an undefined-promise return.
jest.mock('../api/recommendations', () => ({
  getRecommendationDetail: jest.fn().mockResolvedValue({
    id: 'rec-default',
    usage_history: [],
    confidence_bucket: 'low',
    provenance_note: '',
  }),
  getRecommendationsFreshness: jest.fn().mockResolvedValue({ last_collected_at: null, last_collection_error: null }),
}));

// Mock state module
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
  // Bundle A column-filter accessors (default empty filters → applyColumnFilters is a no-op).
  getRecommendationsColumnFilters: jest.fn().mockReturnValue({}),
  setRecommendationsColumnFilter: jest.fn(),
  clearAllRecommendationsColumnFilters: jest.fn(),
  getVisibleRecommendations: jest.fn().mockReturnValue([]),
  setVisibleRecommendations: jest.fn(),
}));

// Mock utils
jest.mock('../utils', () => ({
  formatCurrency: jest.fn((val) => `$${val || 0}`),
  formatTerm: jest.fn((years) => years == null ? '' : `${years} Year${years === 1 ? '' : 's'}`),
  escapeHtml: jest.fn((str) => str || ''),
  populateAccountFilter: jest.fn(() => Promise.resolve())
}));

import * as api from '../api';
import * as state from '../state';

describe('Recommendations Module', () => {
  beforeEach(() => {
    // Reset DOM
    document.body.innerHTML = `
      <div id="recommendations-tab" class="tab-content active">
        <div id="recommendations-summary"></div>
        <div id="recommendations-list"></div>
      </div>
      <div id="purchase-modal" class="hidden">
        <div id="purchase-details"></div>
      </div>
    `;

    jest.clearAllMocks();
    jest.useFakeTimers();
    window.alert = jest.fn();
  });

  afterEach(() => {
    jest.useRealTimers();
  });

  describe('loadRecommendations', () => {
    test('fetches recommendations with provider/account_ids hints (Bundle B)', async () => {
      // After Bundle B, only provider + account_ids are sent to the API as
      // hints; service/region/numeric filters are pure client-side via
      // applyColumnFilters. The legacy DOM filter inputs are gone.
      (api.getRecommendations as jest.Mock).mockResolvedValue({
        summary: {},
        recommendations: [],
        regions: []
      });

      await loadRecommendations();

      expect(api.getRecommendations).toHaveBeenCalledWith({
        provider: 'all',
        account_ids: undefined,
      });
    });

    test('renders recommendations summary', async () => {
      (api.getRecommendations as jest.Mock).mockResolvedValue({
        summary: {
          total_count: 10,
          total_monthly_savings: 5000,
          total_upfront_cost: 10000,
          avg_payback_months: 2
        },
        recommendations: [],
        regions: []
      });

      await loadRecommendations();

      const summary = document.getElementById('recommendations-summary');
      expect(summary?.innerHTML).toContain('Total Recommendations');
      expect(summary?.innerHTML).toContain('Potential Monthly Savings');
      expect(summary?.innerHTML).toContain('Total Upfront Cost');
      expect(summary?.innerHTML).toContain('Payback Period');
    });

    test('Potential Monthly Savings card mirrors page-level range, not the API sum (#272)', async () => {
      // Two cells, two variants per cell. The cells share (provider, account,
      // service, resource_type, region, term, engine) within each group; the
      // variants differ by payment_option. The user can only buy one variant
      // per cell, so the achievable monthly savings is bounded by:
      //   min = sum of per-cell savingsMin = 100 + 200 = 300
      //   max = sum of per-cell savingsMax = 150 + 250 = 400
      // The API's total_monthly_savings sums all 4 variants (= 700), which
      // overstates achievable savings by ~75% on this 2-cell page. The card
      // must NOT render 700; it must render the range "$300 – $400".
      const recs = [
        { id: 'cell1-cheap',  provider: 'aws', cloud_account_id: 'a1', service: 'ec2', resource_type: 't3.medium', region: 'us-east-1', count: 1, term: 1, payment_option: 'no-upfront',     savings: 100, upfront_cost: 0 },
        { id: 'cell1-pricey', provider: 'aws', cloud_account_id: 'a1', service: 'ec2', resource_type: 't3.medium', region: 'us-east-1', count: 1, term: 1, payment_option: 'all-upfront',    savings: 150, upfront_cost: 1000 },
        { id: 'cell2-cheap',  provider: 'aws', cloud_account_id: 'a1', service: 'rds', resource_type: 'db.t3',     region: 'us-east-1', count: 1, term: 1, payment_option: 'no-upfront',     savings: 200, upfront_cost: 0 },
        { id: 'cell2-pricey', provider: 'aws', cloud_account_id: 'a1', service: 'rds', resource_type: 'db.t3',     region: 'us-east-1', count: 1, term: 1, payment_option: 'all-upfront',    savings: 250, upfront_cost: 2000 },
      ];
      (api.getRecommendations as jest.Mock).mockResolvedValue({
        summary: { total_count: 4, total_monthly_savings: 700, total_upfront_cost: 3000, avg_payback_months: 1 },
        recommendations: recs,
        regions: [],
      });

      await loadRecommendations();

      const summary = document.getElementById('recommendations-summary');
      const savingsCard = Array.from(summary?.querySelectorAll('.card') ?? [])
        .find((c) => c.querySelector('h3')?.textContent === 'Potential Monthly Savings');
      const value = savingsCard?.querySelector('.value.savings')?.textContent ?? '';
      // The page-level range is $300 – $400/mo; the API's flat 700 must not
      // appear on the card.
      expect(value).toContain('$300');
      expect(value).toContain('$400');
      expect(value).not.toContain('$700');
    });

    test('renders recommendations list', async () => {
      (api.getRecommendations as jest.Mock).mockResolvedValue({
        summary: {},
        recommendations: [
          { id: 'rec-1', provider: 'aws',
            service: 'ec2',
            resource_type: 't3.medium',
            region: 'us-east-1',
            count: 5,
            term: 1,
            savings: 100,
            upfront_cost: 500
          }
        ],
        regions: ['us-east-1', 'us-west-2']
      });

      await loadRecommendations();

      const list = document.getElementById('recommendations-list');
      expect(list?.innerHTML).toContain('table');
      expect(list?.innerHTML).toContain('ec2');
      expect(list?.innerHTML).toContain('t3.medium');
      expect(list?.innerHTML).toContain('us-east-1');
    });

    test('shows empty-state message when no recommendations', async () => {
      (api.getRecommendations as jest.Mock).mockResolvedValue({
        summary: {},
        recommendations: [],
        regions: []
      });

      await loadRecommendations();

      const list = document.getElementById('recommendations-list');
      expect(list?.innerHTML).toContain('No recommendations match');
    });

    test('stores recommendations in state', async () => {
      const mockRecs = [
        { id: 'rec-2', provider: 'aws', service: 'ec2', savings: 100 }
      ];
      (api.getRecommendations as jest.Mock).mockResolvedValue({
        summary: {},
        recommendations: mockRecs,
        regions: []
      });

      await loadRecommendations();

      expect(state.setRecommendations).toHaveBeenCalledWith(mockRecs);
    });

    test('clears selected recommendations on load', async () => {
      (api.getRecommendations as jest.Mock).mockResolvedValue({
        summary: {},
        recommendations: [],
        regions: []
      });

      await loadRecommendations();

      expect(state.clearSelectedRecommendations).toHaveBeenCalled();
    });

    test('shows error on API failure', async () => {
      (api.getRecommendations as jest.Mock).mockRejectedValue(new Error('API Error'));
      console.error = jest.fn();

      await loadRecommendations();

      const list = document.getElementById('recommendations-list');
      expect(list?.innerHTML).toContain('Failed to load recommendations');
    });

    test('renders select-all checkbox', async () => {
      (api.getRecommendations as jest.Mock).mockResolvedValue({
        summary: {},
        recommendations: [
          { id: 'rec-3', provider: 'aws', service: 'ec2', savings: 100 }
        ],
        regions: []
      });

      await loadRecommendations();

      const selectAll = document.getElementById('select-all-recs');
      expect(selectAll).toBeTruthy();
    });

    test('highlights selected recommendations', async () => {
      (api.getRecommendations as jest.Mock).mockResolvedValue({
        summary: {},
        recommendations: [
          { id: 'rec-sel', provider: 'aws', service: 'ec2', savings: 100 }
        ],
        regions: []
      });
      (state.getSelectedRecommendationIDs as jest.Mock).mockReturnValue(new Set(['rec-sel']));

      await loadRecommendations();

      const list = document.getElementById('recommendations-list');
      expect(list?.innerHTML).toContain('selected');
    });

    test('applies high-savings class for large savings', async () => {
      (api.getRecommendations as jest.Mock).mockResolvedValue({
        summary: {},
        recommendations: [
          { id: 'rec-5', provider: 'aws', service: 'ec2', savings: 2000 }
        ],
        regions: []
      });

      await loadRecommendations();

      const list = document.getElementById('recommendations-list');
      expect(list?.innerHTML).toContain('high-savings');
    });

    test('select-all checkbox selects all recommendations when checked', async () => {
      const mockRecs = [
        { id: 'rec-6', provider: 'aws', service: 'ec2', resource_type: 't3.medium', region: 'us-east-1', count: 1, term: 1, savings: 100, upfront_cost: 500 },
        { id: 'rec-7', provider: 'aws', service: 'rds', resource_type: 'db.t3.medium', region: 'us-east-1', count: 2, term: 1, savings: 200, upfront_cost: 1000 }
      ];
      (api.getRecommendations as jest.Mock).mockResolvedValue({
        summary: {},
        recommendations: mockRecs,
        regions: []
      });

      await loadRecommendations();

      const selectAll = document.getElementById('select-all-recs') as HTMLInputElement;
      selectAll.checked = true;
      selectAll.dispatchEvent(new Event('change'));

      expect(state.addSelectedRecommendation).toHaveBeenCalledWith(expect.stringMatching(/^rec-/));
      expect((state.addSelectedRecommendation as jest.Mock).mock.calls.length).toBeGreaterThanOrEqual(2);
    });

    test('select-all checkbox clears all recommendations when unchecked', async () => {
      const mockRecs = [
        { id: 'rec-8', provider: 'aws', service: 'ec2', resource_type: 't3.medium', region: 'us-east-1', count: 1, term: 1, savings: 100, upfront_cost: 500 }
      ];
      (api.getRecommendations as jest.Mock).mockResolvedValue({
        summary: {},
        recommendations: mockRecs,
        regions: []
      });

      await loadRecommendations();

      const selectAll = document.getElementById('select-all-recs') as HTMLInputElement;
      selectAll.checked = false;
      selectAll.dispatchEvent(new Event('change'));

      expect(state.clearSelectedRecommendations).toHaveBeenCalled();
    });

    test('individual row checkbox adds recommendation when checked', async () => {
      const mockRecs = [
        { id: 'rec-9', provider: 'aws', service: 'ec2', resource_type: 't3.medium', region: 'us-east-1', count: 1, term: 1, savings: 100, upfront_cost: 500 }
      ];
      (api.getRecommendations as jest.Mock).mockResolvedValue({
        summary: {},
        recommendations: mockRecs,
        regions: []
      });

      await loadRecommendations();

      const checkbox = document.querySelector('input[data-rec-id]') as HTMLInputElement;
      checkbox.checked = true;
      checkbox.dispatchEvent(new Event('change'));

      expect(state.addSelectedRecommendation).toHaveBeenCalled();
      expect((state.addSelectedRecommendation as jest.Mock).mock.calls[0][0]).toMatch(/^rec-/);
    });

    test('individual row checkbox removes recommendation when unchecked', async () => {
      const mockRecs = [
        { id: 'rec-10', provider: 'aws', service: 'ec2', resource_type: 't3.medium', region: 'us-east-1', count: 1, term: 1, savings: 100, upfront_cost: 500 }
      ];
      (api.getRecommendations as jest.Mock).mockResolvedValue({
        summary: {},
        recommendations: mockRecs,
        regions: []
      });

      await loadRecommendations();

      const checkbox = document.querySelector('input[data-rec-id]') as HTMLInputElement;
      checkbox.checked = false;
      checkbox.dispatchEvent(new Event('change'));

      expect(state.removeSelectedRecommendation).toHaveBeenCalled();
      expect((state.removeSelectedRecommendation as jest.Mock).mock.calls[0][0]).toMatch(/^rec-/);
    });

    // Issue #187: when two Azure recs share `(provider, service, region,
    // resource_type, payment)` but differ in subscription or term, they
    // each carry a distinct backend ID after the scheduler hash fix
    // (see TestScheduler_ConvertRecommendations_HashUniqueness). The
    // frontend selection toggle keys on data-rec-id, so distinct IDs →
    // independent toggles. Pin that here so a future regression on the
    // frontend rendering side surfaces immediately.
    test('toggling one row does NOT flip a sibling row with a distinct ID', async () => {
      const mockRecs = [
        { id: 'rec-azure-sub1', provider: 'azure', cloud_account_id: 'sub1', service: 'compute', resource_type: 'D2s', region: 'eastus', count: 1, term: 1, savings: 100, upfront_cost: 500 },
        { id: 'rec-azure-sub2', provider: 'azure', cloud_account_id: 'sub2', service: 'compute', resource_type: 'D2s', region: 'eastus', count: 1, term: 1, savings: 200, upfront_cost: 800 },
      ];
      (api.getRecommendations as jest.Mock).mockResolvedValue({
        summary: {},
        recommendations: mockRecs,
        regions: []
      });

      await loadRecommendations();

      const checkboxes = Array.from(
        document.querySelectorAll<HTMLInputElement>('tbody input[data-rec-id]'),
      );
      expect(checkboxes).toHaveLength(2);
      const ids = checkboxes.map((cb) => cb.dataset['recId']).sort();
      expect(ids).toEqual(['rec-azure-sub1', 'rec-azure-sub2']);

      // Tick rec-azure-sub1 specifically — the table sort order would
      // otherwise depend on savings (sub2's 200 > sub1's 100), so we
      // pick by ID, not index.
      const sub1 = checkboxes.find((cb) => cb.dataset['recId'] === 'rec-azure-sub1');
      expect(sub1).toBeDefined();
      sub1!.checked = true;
      sub1!.dispatchEvent(new Event('change'));

      // Only sub1's ID should land in addSelectedRecommendation; sub2
      // stays untouched.
      const calls = (state.addSelectedRecommendation as jest.Mock).mock.calls;
      const calledIds = calls.map((c) => c[0]);
      expect(calledIds).toContain('rec-azure-sub1');
      expect(calledIds).not.toContain('rec-azure-sub2');
    });

    // Issue #224: when a sibling variant is selected but currently hidden by a
    // column filter (e.g. user filtered to 3yr, but 1yr sibling is still
    // selected in state), checking a 3yr sibling must evict the hidden 1yr
    // selection. The fix iterates state.getRecommendations() (full loaded set)
    // rather than the filtered `recommendations` array rendered in the DOM.
    test('checking a visible variant evicts a same-cell sibling hidden by filter', async () => {
      const allRecs = [
        { id: 'rec-1yr', provider: 'aws', service: 'ec2', resource_type: 't3.medium', region: 'us-east-1', count: 1, term: 1, savings: 100, upfront_cost: 0 },
        { id: 'rec-3yr', provider: 'aws', service: 'ec2', resource_type: 't3.medium', region: 'us-east-1', count: 1, term: 3, savings: 300, upfront_cost: 0 },
      ];
      // Simulate: API returns all recs, but only the 3yr one is visible (1yr is
      // hidden by filter). state.getRecommendations() returns the full set.
      (api.getRecommendations as jest.Mock).mockResolvedValue({
        summary: {},
        recommendations: [allRecs[1]], // only 3yr visible in rendered list
        regions: []
      });
      (state.getRecommendations as jest.Mock).mockReturnValue(allRecs); // full set in state
      (state as unknown as { getRecommendationByID: jest.Mock }).getRecommendationByID.mockImplementation((id: string) => allRecs.find((r) => r.id === id));
      // Simulate: 1yr is already selected in state.
      (state.getSelectedRecommendationIDs as jest.Mock).mockReturnValue(new Set(['rec-1yr']));

      await loadRecommendations();

      // Tick the 3yr checkbox.
      const cb = document.querySelector<HTMLInputElement>('input[data-rec-id="rec-3yr"]');
      expect(cb).not.toBeNull();
      cb!.checked = true;
      cb!.dispatchEvent(new Event('change'));

      // The hidden 1yr sibling must be evicted.
      const removeCalls = (state.removeSelectedRecommendation as jest.Mock).mock.calls.map(c => c[0]);
      expect(removeCalls).toContain('rec-1yr');
      // And the 3yr rec is added.
      const addCalls = (state.addSelectedRecommendation as jest.Mock).mock.calls.map(c => c[0]);
      expect(addCalls).toContain('rec-3yr');
    });

    test('purchase button opens modal for that recommendation', async () => {
      const mockRecs = [
        { id: 'rec-11', provider: 'aws', service: 'ec2', resource_type: 't3.medium', region: 'us-east-1', count: 1, term: 1, savings: 100, upfront_cost: 500 }
      ];
      (api.getRecommendations as jest.Mock).mockResolvedValue({
        summary: {},
        recommendations: mockRecs,
        regions: []
      });
      (state.getVisibleRecommendations as jest.Mock).mockReturnValue(mockRecs);

      await loadRecommendations();

      // Bundle B: per-row Purchase buttons gone; the Purchase action lives
      // in the sticky bottom action box at #bulk-purchase-btn. The button
      // resolves its target via state.getVisibleRecommendations(), so the
      // mock needs to return the loaded recs for the click to fire.
      (state.getVisibleRecommendations as jest.Mock).mockReturnValue(mockRecs);
      const purchaseBtn = document.querySelector('#bulk-purchase-btn') as HTMLButtonElement;
      expect(purchaseBtn).not.toBeNull();
      purchaseBtn.click();
      // Issue #111 (iii): openPurchaseModal is async; flush microtasks
      // so the modal-open call lands before we assert visibility.
      await Promise.resolve(); await Promise.resolve(); await Promise.resolve();

      const modal = document.getElementById('purchase-modal');
      expect(modal?.classList.contains('hidden')).toBe(false);
    });

    test('renders engine info when present', async () => {
      const mockRecs = [
        { id: 'rec-12', provider: 'aws', service: 'rds', resource_type: 'db.t3.medium', region: 'us-east-1', count: 1, term: 1, savings: 100, upfront_cost: 500, engine: 'mysql' }
      ];
      (api.getRecommendations as jest.Mock).mockResolvedValue({
        summary: {},
        recommendations: mockRecs,
        regions: []
      });
      (state.getVisibleRecommendations as jest.Mock).mockReturnValue(mockRecs);

      await loadRecommendations();

      const list = document.getElementById('recommendations-list');
      expect(list?.innerHTML).toContain('mysql');
    });

    test('applies medium-savings class for moderate savings', async () => {
      (api.getRecommendations as jest.Mock).mockResolvedValue({
        summary: {},
        recommendations: [
          { id: 'rec-13', provider: 'aws', service: 'ec2', resource_type: 't3.medium', region: 'us-east-1', count: 1, term: 1, savings: 500, upfront_cost: 500 }
        ],
        regions: []
      });

      await loadRecommendations();

      const list = document.getElementById('recommendations-list');
      expect(list?.innerHTML).toContain('medium-savings');
    });
  });

  describe('P6: sort + bulk toolbar + detail drawer', () => {
    const twoRecs = [
      { id: 'rec-14', provider: 'aws', service: 'ec2', resource_type: 't3.large', region: 'us-east-1', count: 2, term: 1, savings: 100, upfront_cost: 500 },
      { id: 'rec-15', provider: 'aws', service: 'rds', resource_type: 'db.m5.large', region: 'us-east-1', count: 4, term: 3, savings: 1500, upfront_cost: 9000 },
    ];

    beforeEach(() => {
      (api.getRecommendations as jest.Mock).mockResolvedValue({
        recommendations: twoRecs,
        summary: {},
        available_regions: [],
      });
    });

    test('renders sortable column headers with indicators (Bundle B: 11 columns)', async () => {
      await loadRecommendations();
      const list = document.getElementById('recommendations-list');
      // Bundle B: every data column is sortable. 11 sortable data columns:
      // provider, account, service, resource_type, region, count, term,
      // savings, upfront_cost, monthly_cost, effective_savings_pct.
      // The leading checkbox column is not sortable.
      const sortables = list?.querySelectorAll('th.sortable');
      expect(sortables?.length).toBe(11);
      // The default sort is savings desc → that header shows an active ▼.
      const savingsHeader = list?.querySelector('th[data-sort="savings"]');
      expect(savingsHeader?.innerHTML).toContain('active');
    });

    test('clicking a sortable header calls setRecommendationsSort', async () => {
      await loadRecommendations();
      const header = document.querySelector<HTMLTableCellElement>('th[data-sort="upfront_cost"]');
      header?.click();
      expect(state.setRecommendationsSort).toHaveBeenCalledWith({ column: 'upfront_cost', direction: 'desc' });
    });

    test('bottom action box selection summary reflects current selection (Bundle B)', async () => {
      // Bundle B replaced the floating .recommendations-bulk-toolbar with
      // selection-summary text inside the sticky bottom action box.
      (api.getRecommendations as jest.Mock).mockResolvedValue({
        summary: {},
        recommendations: [
          { id: 'rec-bt', provider: 'aws', service: 'ec2', resource_type: 't3.medium', region: 'us-east-1', count: 1, term: 1, savings: 100, upfront_cost: 500 }
        ],
        regions: []
      });
      (state.getSelectedRecommendationIDs as jest.Mock).mockReturnValue(new Set(['rec-bt']));
      (state.getVisibleRecommendations as jest.Mock).mockReturnValue([
        { id: 'rec-bt', provider: 'aws', service: 'ec2', resource_type: 't3.medium', region: 'us-east-1', count: 1, term: 1, savings: 100, upfront_cost: 500 }
      ]);

      await loadRecommendations();

      const summary = document.getElementById('recommendations-action-summary');
      // issues #225/#226: summary text now uses "cells visible" (cell-grouped count).
      expect(summary?.textContent).toContain('1 selected of 1 cells visible');
      // Old bulk-toolbar surface is gone.
      expect(document.querySelector('.recommendations-bulk-toolbar')).toBeNull();
    });

    test('bottom action box shows "All N visible" when no row is selected (Bundle B)', async () => {
      (state.getSelectedRecommendationIDs as jest.Mock).mockReturnValue(new Set());
      (state.getVisibleRecommendations as jest.Mock).mockReturnValue(twoRecs);
      await loadRecommendations();
      const summary = document.getElementById('recommendations-action-summary');
      // issues #225/#226: summary text now uses "cells visible" (cell-grouped count).
      expect(summary?.textContent).toMatch(/All \d+ cells visible/);
      expect(document.querySelector('.recommendations-bulk-toolbar')).toBeNull();
    });

    test('clicking a row opens the detail drawer with that recommendation', async () => {
      await loadRecommendations();
      // Simulate clicking the first data row (not on a checkbox / button).
      const firstRow = document.querySelector<HTMLTableRowElement>('tr.recommendation-row');
      const cell = firstRow?.querySelectorAll('td')[3]; // Service cell — safe to click
      cell?.click();
      const drawer = document.querySelector('.detail-drawer');
      expect(drawer).not.toBeNull();
      expect(drawer?.querySelector('h3')?.textContent).toContain('AWS');
    });

    test('ESC closes the detail drawer', async () => {
      await loadRecommendations();
      const firstRow = document.querySelector<HTMLTableRowElement>('tr.recommendation-row');
      const cell = firstRow?.querySelectorAll('td')[3];
      cell?.click();
      expect(document.querySelector('.detail-drawer')).not.toBeNull();
      document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' }));
      expect(document.querySelector('.detail-drawer')).toBeNull();
    });

    describe('drawer fetches detail from /api/recommendations/:id/detail (issue #44)', () => {
      // The detail-fetch mock lives on the api/recommendations module
      // so the test can assert call shape without round-tripping
      // through apiRequest.
      // eslint-disable-next-line @typescript-eslint/no-require-imports
      const recApi = require('../api/recommendations') as { getRecommendationDetail: jest.Mock };

      beforeEach(() => {
        // Real timers — the drawer's fetch uses microtasks (Promise
        // resolution) which jest's fake timers don't auto-advance.
        jest.useRealTimers();
        clearRecommendationDetailCache();
        recApi.getRecommendationDetail.mockReset();
      });

      afterEach(() => {
        jest.useFakeTimers();
      });

      test('drawer fetches detail once per id and renders backend confidence + provenance', async () => {
        recApi.getRecommendationDetail.mockResolvedValue({
          id: 'rec-15',
          usage_history: [],
          confidence_bucket: 'high',
          provenance_note: 'AWS ec2 recommendation APIs · last collected 2026-04-24T12:00:00Z',
        });

        await loadRecommendations();
        const firstRow = document.querySelector<HTMLTableRowElement>('tr.recommendation-row');
        firstRow?.querySelectorAll('td')[3]?.click();

        // Allow the .then() handler to run.
        await Promise.resolve();
        await Promise.resolve();

        expect(recApi.getRecommendationDetail).toHaveBeenCalledTimes(1);
        // Default sort is savings desc → rec-15 ($1500) renders first.
        expect(recApi.getRecommendationDetail).toHaveBeenCalledWith('rec-15');

        const badge = document.querySelector('.detail-drawer .confidence-badge');
        expect(badge?.classList.contains('confidence-high')).toBe(true);
        expect(badge?.textContent).toBe('High');

        const provenance = document.querySelector('.detail-drawer .detail-drawer-note');
        expect(provenance?.textContent).toContain('last collected 2026-04-24T12:00:00Z');
      });

      test('empty usage_history renders the "not yet available" placeholder, not a broken chart', async () => {
        recApi.getRecommendationDetail.mockResolvedValue({
          id: 'rec-15',
          usage_history: [],
          confidence_bucket: 'medium',
          provenance_note: 'AWS ec2 recommendation APIs.',
        });

        await loadRecommendations();
        const firstRow = document.querySelector<HTMLTableRowElement>('tr.recommendation-row');
        firstRow?.querySelectorAll('td')[3]?.click();
        await Promise.resolve();
        await Promise.resolve();

        // No SVG sparkline — degraded path.
        expect(document.querySelector('.detail-drawer-sparkline')).toBeNull();
        // Placeholder note present.
        const usageNote = document.querySelector('.detail-drawer-usage .detail-drawer-note-muted');
        expect(usageNote?.textContent).toBe('Usage history not yet available.');
      });

      test('non-empty usage_history renders an inline SVG sparkline', async () => {
        recApi.getRecommendationDetail.mockResolvedValue({
          id: 'rec-15',
          usage_history: [
            { timestamp: '2026-04-23T00:00:00Z', cpu_pct: 12, mem_pct: 30 },
            { timestamp: '2026-04-23T01:00:00Z', cpu_pct: 18, mem_pct: 32 },
            { timestamp: '2026-04-23T02:00:00Z', cpu_pct: 25, mem_pct: 40 },
          ],
          confidence_bucket: 'high',
          provenance_note: 'AWS ec2 recommendation APIs.',
        });

        await loadRecommendations();
        const firstRow = document.querySelector<HTMLTableRowElement>('tr.recommendation-row');
        firstRow?.querySelectorAll('td')[3]?.click();
        await Promise.resolve();
        await Promise.resolve();

        const svg = document.querySelector('.detail-drawer-sparkline');
        expect(svg).not.toBeNull();
        // Two paths (CPU + memory).
        expect(svg?.querySelectorAll('path').length).toBe(2);
      });

      test('repeated open of same drawer reuses the cached detail (one fetch per id)', async () => {
        recApi.getRecommendationDetail.mockResolvedValue({
          id: 'rec-15',
          usage_history: [],
          confidence_bucket: 'low',
          provenance_note: 'AWS ec2 recommendation APIs.',
        });

        await loadRecommendations();
        const firstRow = document.querySelector<HTMLTableRowElement>('tr.recommendation-row');
        firstRow?.querySelectorAll('td')[3]?.click();
        await Promise.resolve();
        await Promise.resolve();

        // Close and re-open the same drawer.
        document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' }));
        firstRow?.querySelectorAll('td')[3]?.click();
        await Promise.resolve();
        await Promise.resolve();

        expect(recApi.getRecommendationDetail).toHaveBeenCalledTimes(1);
      });
    });
  });

  describe('openPurchaseModal', () => {
    // Issue #111 (iii): openPurchaseModal is now async (it pre-fetches
    // per-account service overrides to seed each row's Payment default).
    // Tests must `await` it so the DOM is populated before assertions.
    test('displays purchase modal', async () => {
      const recommendations = [
        { id: 'rec-1', provider: 'aws' as const,
          service: 'ec2',
          resource_type: 't3.medium',
          region: 'us-east-1',
          count: 5,
          term: 1,
          savings: 100,
          upfront_cost: 500
        }
      ];

      await openPurchaseModal(recommendations);

      const modal = document.getElementById('purchase-modal');
      expect(modal?.classList.contains('hidden')).toBe(false);
    });

    test('shows purchase summary', async () => {
      const recommendations = [
        { id: 'rec-2', provider: 'aws' as const, service: 'ec2', resource_type: 't3.medium', region: 'us-east-1', count: 5, term: 1, savings: 100, upfront_cost: 500 },
        { id: 'rec-3', provider: 'aws' as const, service: 'rds', resource_type: 'db.r5.large', region: 'us-east-1', count: 2, term: 1, savings: 200, upfront_cost: 1000 }
      ];

      await openPurchaseModal(recommendations);

      const details = document.getElementById('purchase-details');
      expect(details?.textContent).toContain('2'); // count of commitments
      expect(details?.textContent).toContain('Purchase Summary');
    });

    test('lists individual recommendations', async () => {
      const recommendations = [
        { id: 'rec-4', provider: 'aws' as const, service: 'ec2', resource_type: 't3.medium', region: 'us-east-1', count: 5, term: 1, savings: 100, upfront_cost: 500 }
      ];

      await openPurchaseModal(recommendations);

      const details = document.getElementById('purchase-details');
      expect(details?.textContent).toContain('ec2');
      expect(details?.textContent).toContain('t3.medium');
      expect(details?.textContent).toContain('us-east-1');
    });

    test('handles missing modal element', async () => {
      document.body.replaceChildren();

      await expect(openPurchaseModal([])).resolves.not.toThrow();
    });
  });

  describe('refreshRecommendations', () => {
    test('calls API to refresh recommendations', async () => {
      (api.refreshRecommendations as jest.Mock).mockResolvedValue({});
      (api.getRecommendations as jest.Mock).mockResolvedValue({
        summary: {},
        recommendations: [],
        regions: []
      });

      await refreshRecommendations();

      expect(api.refreshRecommendations).toHaveBeenCalled();
    });

    test('shows success alert', async () => {
      (api.refreshRecommendations as jest.Mock).mockResolvedValue({});

      await refreshRecommendations();

      expect(window.alert).toHaveBeenCalledWith('Recommendation refresh started. This may take a few minutes.');
    });

    test('schedules reload after 5 seconds', async () => {
      (api.refreshRecommendations as jest.Mock).mockResolvedValue({});
      (api.getRecommendations as jest.Mock).mockResolvedValue({
        summary: {},
        recommendations: [],
        regions: []
      });

      await refreshRecommendations();

      jest.advanceTimersByTime(5000);

      expect(api.getRecommendations).toHaveBeenCalled();
    });

    test('shows error on failure', async () => {
      (api.refreshRecommendations as jest.Mock).mockRejectedValue(new Error('API Error'));
      console.error = jest.fn();

      await refreshRecommendations();

      expect(window.alert).toHaveBeenCalledWith('Failed to start recommendation refresh');
    });
  });

  describe('setupRecommendationsHandlers', () => {
    beforeEach(() => {
      document.body.innerHTML = `
        <select id="recommendations-provider-filter">
          <option value="">All Providers</option>
          <option value="aws">AWS</option>
          <option value="azure">Azure</option>
        </select>
        <select id="service-filter">
          <optgroup label="AWS Services">
            <option value="ec2">EC2</option>
          </optgroup>
          <optgroup label="Azure Services">
            <option value="vm">Virtual Machines</option>
          </optgroup>
        </select>
        <select id="region-filter">
          <option value="">All Regions</option>
        </select>
        <input type="number" id="min-savings-filter" value="">
        <div id="recommendations-list"></div>
        <div id="recommendations-summary"></div>
      `;
      (api.getRecommendations as jest.Mock).mockResolvedValue({
        summary: {},
        recommendations: [],
        regions: []
      });
    });

    // The legacy top filter bar tests (provider-filter / service-filter /
    // region-filter / min-savings-filter change handlers + service-filter
    // visibility-toggle) are obsolete: Bundle B replaced those DOM elements
    // with per-column header-mounted popovers. See the column-filter +
    // bottom-action-box tests below for their successors.
    test('setupRecommendationsHandlers is a no-op (Bundle B)', () => {
      // Should not throw, even with the legacy filter-bar DOM absent.
      expect(() => setupRecommendationsHandlers()).not.toThrow();
    });
  });
});

// ---------------------------------------------------------------------------
// Bundle A: numeric expression parser + applyColumnFilters
// ---------------------------------------------------------------------------

import { parseNumericFilter, applyColumnFilters } from '../recommendations';
import type { LocalRecommendation } from '../types';

describe('parseNumericFilter', () => {
  const accept = (expr: string, n: number): boolean => {
    const r = parseNumericFilter(expr);
    if (!r.ok) throw new Error(`unexpected parse failure for "${expr}": ${r.error}`);
    return r.predicate(n);
  };

  test('empty / whitespace expression matches everything', () => {
    expect(accept('', 0)).toBe(true);
    expect(accept('   ', 42)).toBe(true);
    expect(accept('\t\n', -7)).toBe(true);
  });

  test('plain integer matches by equality', () => {
    expect(accept('42', 42)).toBe(true);
    expect(accept('42', 41)).toBe(false);
    expect(accept('-3', -3)).toBe(true);
  });

  test('plain decimal matches by equality', () => {
    expect(accept('3.14', 3.14)).toBe(true);
    expect(accept('3.14', 3.15)).toBe(false);
  });

  test('comparator > / >= / < / <=', () => {
    expect(accept('>10', 11)).toBe(true);
    expect(accept('>10', 10)).toBe(false);
    expect(accept('>=10', 10)).toBe(true);
    expect(accept('<5', 4)).toBe(true);
    expect(accept('<5', 5)).toBe(false);
    expect(accept('<=5', 5)).toBe(true);
  });

  test('inclusive range X..Y', () => {
    expect(accept('10..20', 10)).toBe(true);
    expect(accept('10..20', 20)).toBe(true);
    expect(accept('10..20', 15)).toBe(true);
    expect(accept('10..20', 9)).toBe(false);
    expect(accept('10..20', 21)).toBe(false);
  });

  test('reversed range still works (max..min)', () => {
    expect(accept('20..10', 15)).toBe(true);
  });

  test('comma-separated terms OR together', () => {
    // `5, >100, 200..400`
    expect(accept('5, >100, 200..400', 5)).toBe(true);
    expect(accept('5, >100, 200..400', 150)).toBe(true);
    expect(accept('5, >100, 200..400', 250)).toBe(true);
    expect(accept('5, >100, 200..400', 50)).toBe(false);
    expect(accept('5, >100, 200..400', 500)).toBe(true); // matches >100
  });

  test('whitespace inside terms is tolerated', () => {
    expect(accept('  >  10  ', 11)).toBe(true);
    expect(accept('10 .. 20', 15)).toBe(true);
  });

  test('invalid expression returns ok:false with an error message', () => {
    const r1 = parseNumericFilter('>>5');
    expect(r1.ok).toBe(false);
    if (!r1.ok) expect(r1.error).toMatch(/Invalid filter term/);

    const r2 = parseNumericFilter('not a number');
    expect(r2.ok).toBe(false);

    const r3 = parseNumericFilter('1..');
    expect(r3.ok).toBe(false);
  });
});

describe('applyColumnFilters', () => {
  const rec = (
    overrides: Partial<LocalRecommendation> = {},
  ): LocalRecommendation => ({
    id: overrides.id ?? 'r1',
    provider: overrides.provider ?? 'aws',
    cloud_account_id: overrides.cloud_account_id ?? 'acct-1',
    service: overrides.service ?? 'ec2',
    resource_type: overrides.resource_type ?? 't3.medium',
    region: overrides.region ?? 'us-east-1',
    count: overrides.count ?? 1,
    term: overrides.term ?? 1,
    upfront_cost: overrides.upfront_cost ?? 100,
    monthly_cost: overrides.monthly_cost ?? 10,
    savings: overrides.savings ?? 50,
    engine: overrides.engine,
  } as unknown as LocalRecommendation);

  test('empty filters returns a clone of the input (no-op)', () => {
    const recs = [rec({ id: 'a' }), rec({ id: 'b' })];
    const out = applyColumnFilters(recs, {});
    expect(out).toEqual(recs);
    expect(out).not.toBe(recs); // defensive clone
  });

  test('categorical set filter narrows by membership', () => {
    const recs = [
      rec({ id: 'a', provider: 'aws' }),
      rec({ id: 'b', provider: 'azure' }),
      rec({ id: 'c', provider: 'gcp' }),
    ];
    const out = applyColumnFilters(recs, {
      provider: { kind: 'set', values: ['aws'] },
    });
    expect(out.map(r => r.id)).toEqual(['a']);
  });

  test('Account filter matches on cloud_account_id, not display name', () => {
    const recs = [
      rec({ id: 'a', cloud_account_id: 'acct-prod' }),
      rec({ id: 'b', cloud_account_id: 'acct-dev' }),
    ];
    const out = applyColumnFilters(recs, {
      account: { kind: 'set', values: ['acct-prod'] },
    });
    expect(out.map(r => r.id)).toEqual(['a']);
  });

  test('Term filter values are strings; row term integer stringifies', () => {
    const recs = [
      rec({ id: 'a', term: 1 }),
      rec({ id: 'b', term: 3 }),
    ];
    const out = applyColumnFilters(recs, {
      term: { kind: 'set', values: ['3'] },
    });
    expect(out.map(r => r.id)).toEqual(['b']);
  });

  test('(empty) sentinel matches null / undefined / "" cloud_account_id', () => {
    // Build raw objects — bypass the rec() factory because its `??` defaults
    // would replace null/undefined cloud_account_id with 'acct-1'.
    const recs: LocalRecommendation[] = [
      { ...rec({ id: 'a' }), cloud_account_id: '' } as LocalRecommendation,
      { ...rec({ id: 'b' }), cloud_account_id: undefined } as unknown as LocalRecommendation,
      rec({ id: 'c', cloud_account_id: 'acct-x' }),
    ];
    const out = applyColumnFilters(recs, {
      account: { kind: 'set', values: [''] },
    });
    expect(out.map(r => r.id).sort()).toEqual(['a', 'b']);
  });

  test('numeric expr filter narrows by predicate', () => {
    const recs = [
      rec({ id: 'a', savings: 25 }),
      rec({ id: 'b', savings: 150 }),
      rec({ id: 'c', savings: 1500 }),
    ];
    const out = applyColumnFilters(recs, {
      savings: { kind: 'expr', expr: '>100' },
    });
    expect(out.map(r => r.id)).toEqual(['b', 'c']);
  });

  test('invalid numeric expression is ignored (filter not applied)', () => {
    const recs = [rec({ id: 'a', savings: 1 }), rec({ id: 'b', savings: 100 })];
    const out = applyColumnFilters(recs, {
      savings: { kind: 'expr', expr: '>>5' }, // syntax error
    });
    // All rows pass — broken filter is silently inert.
    expect(out.map(r => r.id)).toEqual(['a', 'b']);
  });

  test('multiple column filters AND together', () => {
    const recs = [
      rec({ id: 'a', provider: 'aws', savings: 50 }),
      rec({ id: 'b', provider: 'aws', savings: 500 }),
      rec({ id: 'c', provider: 'azure', savings: 500 }),
    ];
    const out = applyColumnFilters(recs, {
      provider: { kind: 'set', values: ['aws'] },
      savings: { kind: 'expr', expr: '>100' },
    });
    expect(out.map(r => r.id)).toEqual(['b']);
  });
});

// ---------------------------------------------------------------------------
// Bundle A: state-accessor tests for the new column-filter / visible-recs API.
// These import the REAL state module (the recommendations.test.ts above mocks
// it; here we exercise the actual implementation in a separate require scope).
// ---------------------------------------------------------------------------

describe('state.ts column-filter accessors', () => {
  // The top-level jest.mock('../state', …) replaces the module for every
  // import. Use jest.requireActual to bypass it for these state-accessor
  // tests so we exercise the real implementation. Each test starts from
  // a clean filter state via clearAllRecommendationsColumnFilters().
  const realState = jest.requireActual<typeof import('../state')>('../state');

  beforeEach(() => {
    realState.clearAllRecommendationsColumnFilters();
    realState.setVisibleRecommendations([]);
  });

  test('default filters are empty', () => {
    expect(realState.getRecommendationsColumnFilters()).toEqual({});
  });

  test('setRecommendationsColumnFilter adds an entry', () => {
    realState.setRecommendationsColumnFilter('provider', { kind: 'set', values: ['aws'] });
    expect(realState.getRecommendationsColumnFilters()).toEqual({
      provider: { kind: 'set', values: ['aws'] },
    });
  });

  test('passing null clears that single column', () => {
    realState.setRecommendationsColumnFilter('provider', { kind: 'set', values: ['aws'] });
    realState.setRecommendationsColumnFilter('savings', { kind: 'expr', expr: '>100' });
    realState.setRecommendationsColumnFilter('provider', null);
    expect(realState.getRecommendationsColumnFilters()).toEqual({
      savings: { kind: 'expr', expr: '>100' },
    });
  });

  test('clearAllRecommendationsColumnFilters empties the record', () => {
    realState.setRecommendationsColumnFilter('provider', { kind: 'set', values: ['aws'] });
    realState.setRecommendationsColumnFilter('savings', { kind: 'expr', expr: '>100' });
    realState.clearAllRecommendationsColumnFilters();
    expect(realState.getRecommendationsColumnFilters()).toEqual({});
  });

  test('getRecommendationsColumnFilters returns a defensive shallow copy', () => {
    realState.setRecommendationsColumnFilter('provider', { kind: 'set', values: ['aws'] });
    const a = realState.getRecommendationsColumnFilters();
    delete a.provider;
    // mutation of the returned object must not affect module state
    expect(realState.getRecommendationsColumnFilters()).toEqual({
      provider: { kind: 'set', values: ['aws'] },
    });
  });

  test('setVisibleRecommendations / getVisibleRecommendations round-trip with defensive clone', () => {
    const recs = [{ id: 'r1' }, { id: 'r2' }] as unknown as Parameters<
      typeof realState.setVisibleRecommendations
    >[0];
    realState.setVisibleRecommendations(recs);
    const out = realState.getVisibleRecommendations();
    expect(out.map((r) => (r as unknown as { id: string }).id)).toEqual(['r1', 'r2']);
    // mutating returned array must not affect module state
    (out as unknown as Array<unknown>).pop();
    expect(realState.getVisibleRecommendations()).toHaveLength(2);
  });
});

// ---------------------------------------------------------------------------
// Bundle B: column-filter popover + sticky bottom action box DOM behaviour.
// These tests assert the surfaces Bundle B introduced — header filter
// triggers, the detached popover lifecycle, and the bottom action box's
// label/disabled-state transitions.
// ---------------------------------------------------------------------------

describe('Bundle B: column header filter triggers', () => {
  const sampleRecs = [
    { id: 'rec-aws-1', provider: 'aws', cloud_account_id: 'a1', service: 'ec2', resource_type: 't3.medium', region: 'us-east-1', count: 1, term: 1, savings: 100, upfront_cost: 500 },
    { id: 'rec-az-1',  provider: 'azure', cloud_account_id: 'a2', service: 'vm',  resource_type: 'D2s',       region: 'eastus',    count: 2, term: 3, savings: 200, upfront_cost: 800 },
  ];

  beforeEach(() => {
    // Set up DOM (the top-level beforeEach belongs to a different describe).
    document.body.replaceChildren();
    const recsTab = document.createElement('div');
    recsTab.id = 'recommendations-tab';
    recsTab.className = 'tab-content active';
    const summary = document.createElement('div');
    summary.id = 'recommendations-summary';
    const list = document.createElement('div');
    list.id = 'recommendations-list';
    recsTab.appendChild(summary);
    recsTab.appendChild(list);
    document.body.appendChild(recsTab);
    const purchaseModal = document.createElement('div');
    purchaseModal.id = 'purchase-modal';
    purchaseModal.className = 'hidden';
    const purchaseDetails = document.createElement('div');
    purchaseDetails.id = 'purchase-details';
    purchaseModal.appendChild(purchaseDetails);
    document.body.appendChild(purchaseModal);

    jest.clearAllMocks();
    (api.getRecommendations as jest.Mock).mockResolvedValue({
      summary: {},
      recommendations: sampleRecs,
      regions: [],
    });
    (state.getRecommendations as jest.Mock).mockReturnValue(sampleRecs);
    (state.getVisibleRecommendations as jest.Mock).mockReturnValue(sampleRecs);
    (state.getRecommendationsColumnFilters as jest.Mock).mockReturnValue({});
    (state.getSelectedRecommendationIDs as jest.Mock).mockReturnValue(new Set());
  });

  test('every data column header has a filter trigger button', async () => {
    await loadRecommendations();
    const buttons = document.querySelectorAll<HTMLButtonElement>('th .column-filter-btn[data-column]');
    const cols = Array.from(buttons).map((b) => b.dataset['column']);
    expect(cols.sort()).toEqual(
      ['account', 'count', 'effective_savings_pct', 'monthly_cost', 'provider', 'region', 'resource_type', 'savings', 'service', 'term', 'upfront_cost'].sort(),
    );
  });

  test('filter button gets .active class when its column has a filter', async () => {
    (state.getRecommendationsColumnFilters as jest.Mock).mockReturnValue({
      provider: { kind: 'set', values: ['aws'] },
    });
    await loadRecommendations();
    const providerBtn = document.querySelector<HTMLButtonElement>('th .column-filter-btn[data-column="provider"]');
    expect(providerBtn?.classList.contains('active')).toBe(true);
    const serviceBtn = document.querySelector<HTMLButtonElement>('th .column-filter-btn[data-column="service"]');
    expect(serviceBtn?.classList.contains('active')).toBe(false);
  });

  test('clicking a filter trigger opens a popover detached to document.body', async () => {
    await loadRecommendations();
    const providerBtn = document.querySelector<HTMLButtonElement>('th .column-filter-btn[data-column="provider"]');
    providerBtn?.click();
    const popover = document.body.querySelector('.column-filter-popover');
    expect(popover).not.toBeNull();
    // Not a descendant of the table.
    expect(popover?.closest('table')).toBeNull();
  });

  test('clicking the same trigger toggles the popover closed', async () => {
    await loadRecommendations();
    const providerBtn = document.querySelector<HTMLButtonElement>('th .column-filter-btn[data-column="provider"]');
    providerBtn?.click();
    expect(document.querySelector('.column-filter-popover')).not.toBeNull();
    providerBtn?.click();
    expect(document.querySelector('.column-filter-popover')).toBeNull();
  });

  test('ESC closes the popover and restores focus to the trigger', async () => {
    await loadRecommendations();
    const providerBtn = document.querySelector<HTMLButtonElement>('th .column-filter-btn[data-column="provider"]');
    providerBtn?.click();
    expect(document.querySelector('.column-filter-popover')).not.toBeNull();
    document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' }));
    expect(document.querySelector('.column-filter-popover')).toBeNull();
  });

  test('categorical popover lists distinct values from the unfiltered rec set', async () => {
    await loadRecommendations();
    const providerBtn = document.querySelector<HTMLButtonElement>('th .column-filter-btn[data-column="provider"]');
    providerBtn?.click();
    const checkboxes = document.querySelectorAll<HTMLInputElement>('.column-filter-popover .column-filter-item input[type="checkbox"]');
    const values = Array.from(checkboxes).map((cb) => cb.dataset['value']);
    expect(values.sort()).toEqual(['aws', 'azure']);
  });

  test('service popover includes SageMaker when the loaded rec set has it', async () => {
    const sagemakerRecs = [
      ...sampleRecs,
      {
        id: 'rec-sm-1',
        provider: 'aws',
        cloud_account_id: 'a3',
        service: 'sagemaker',
        resource_type: 'ml.m5.xlarge',
        region: 'us-east-1',
        count: 1,
        term: 1,
        savings: 300,
        upfront_cost: 1200,
      },
    ];

    (api.getRecommendations as jest.Mock).mockResolvedValue({
      summary: {},
      recommendations: sagemakerRecs,
      regions: [],
    });
    (state.getRecommendations as jest.Mock).mockReturnValue(sagemakerRecs);
    (state.getVisibleRecommendations as jest.Mock).mockReturnValue(sagemakerRecs);

    await loadRecommendations();
    const serviceBtn = document.querySelector<HTMLButtonElement>('th .column-filter-btn[data-column="service"]');
    serviceBtn?.click();
    const values = Array.from(
      document.querySelectorAll<HTMLInputElement>('.column-filter-popover .column-filter-item input[type="checkbox"]'),
    ).map((cb) => cb.dataset['value']);
    expect(values).toContain('sagemaker');
  });

  test('Term popover labels show formatted terms; ticking commits string filter values', async () => {
    await loadRecommendations();
    const termBtn = document.querySelector<HTMLButtonElement>('th .column-filter-btn[data-column="term"]');
    termBtn?.click();
    const items = Array.from(
      document.querySelectorAll<HTMLLabelElement>('.column-filter-popover .column-filter-item'),
    );
    const labels = items.map((l) => l.querySelector('span')?.textContent);
    expect(labels.sort()).toEqual(['1 Year', '3 Years']);
    // Tick the "3 Years" checkbox
    const threeYearLabel = items.find((l) => l.textContent === '3 Years');
    const cb = threeYearLabel?.querySelector<HTMLInputElement>('input[type="checkbox"]');
    expect(cb?.dataset['value']).toBe('3');
    cb!.checked = true;
    cb!.dispatchEvent(new Event('change'));
    expect(state.setRecommendationsColumnFilter).toHaveBeenCalledWith('term', { kind: 'set', values: ['3'] });
  });

  test('numeric popover input validates expression on blur', async () => {
    await loadRecommendations();
    const savingsBtn = document.querySelector<HTMLButtonElement>('th .column-filter-btn[data-column="savings"]');
    savingsBtn?.click();
    const input = document.querySelector<HTMLInputElement>('.column-filter-popover .column-filter-numeric-input');
    expect(input).not.toBeNull();
    input!.value = '>>5';
    input!.dispatchEvent(new Event('blur'));
    const err = document.querySelector('.column-filter-popover .column-filter-error');
    expect(err?.textContent).toMatch(/Invalid filter term/);
    // No filter applied for invalid syntax.
    expect(state.setRecommendationsColumnFilter).not.toHaveBeenCalledWith(
      'savings',
      expect.objectContaining({ kind: 'expr' }),
    );
  });

  test('numeric popover commits valid expression on Enter', async () => {
    await loadRecommendations();
    const savingsBtn = document.querySelector<HTMLButtonElement>('th .column-filter-btn[data-column="savings"]');
    savingsBtn?.click();
    const input = document.querySelector<HTMLInputElement>('.column-filter-popover .column-filter-numeric-input');
    input!.value = '>100';
    input!.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true }));
    expect(state.setRecommendationsColumnFilter).toHaveBeenCalledWith('savings', { kind: 'expr', expr: '>100' });
  });

  test('Clear button drops the filter for that column', async () => {
    (state.getRecommendationsColumnFilters as jest.Mock).mockReturnValue({
      provider: { kind: 'set', values: ['aws'] },
    });
    await loadRecommendations();
    const providerBtn = document.querySelector<HTMLButtonElement>('th .column-filter-btn[data-column="provider"]');
    providerBtn?.click();
    const clearBtn = document.querySelector<HTMLButtonElement>('.column-filter-popover .column-filter-clear');
    clearBtn?.click();
    expect(state.setRecommendationsColumnFilter).toHaveBeenCalledWith('provider', null);
  });

  test('Clear-filters badge appears when at least one filter is active', async () => {
    (state.getRecommendationsColumnFilters as jest.Mock).mockReturnValue({
      provider: { kind: 'set', values: ['aws'] },
    });
    await loadRecommendations();
    const badge = document.querySelector<HTMLButtonElement>('.recommendations-filter-status .clear-filters');
    expect(badge).not.toBeNull();
    expect(badge?.textContent).toContain('Clear filters (1)');
  });

  test('aria-live region announces visible/loaded count', async () => {
    await loadRecommendations();
    const live = document.querySelector('.recommendations-filter-live');
    expect(live).not.toBeNull();
    expect(live?.getAttribute('aria-live')).toBe('polite');
    expect(live?.textContent).toMatch(/Showing \d+ of \d+/);
  });

  // Issue #137: 'All Savings Plans' affordance in the service column-filter
  // popover. PR #123 split a single 'savings-plans' service into four per-
  // plan-type slugs, so the user lost the one-click "filter to all SP recs"
  // affordance. These tests pin the new tri-state group toggle.
  describe('Issue #137: All Savings Plans tri-state in service column-filter', () => {
    const spRecs = [
      { id: 'rec-aws-1',  provider: 'aws', cloud_account_id: 'a1', service: 'ec2',                        resource_type: 't3.medium', region: 'us-east-1', count: 1, term: 1, savings: 100, upfront_cost: 500 },
      { id: 'rec-sp-c',   provider: 'aws', cloud_account_id: 'a1', service: 'savings-plans-compute',      resource_type: 'sp',        region: 'us-east-1', count: 1, term: 1, savings: 200, upfront_cost: 800 },
      { id: 'rec-sp-e',   provider: 'aws', cloud_account_id: 'a1', service: 'savings-plans-ec2instance',  resource_type: 'sp',        region: 'us-east-1', count: 1, term: 1, savings: 150, upfront_cost: 700 },
      { id: 'rec-sp-s',   provider: 'aws', cloud_account_id: 'a1', service: 'savings-plans-sagemaker',    resource_type: 'sp',        region: 'us-east-1', count: 1, term: 1, savings: 250, upfront_cost: 900 },
    ];

    beforeEach(() => {
      (api.getRecommendations as jest.Mock).mockResolvedValue({
        summary: {},
        recommendations: spRecs,
        regions: [],
      });
      (state.getRecommendations as jest.Mock).mockReturnValue(spRecs);
      (state.getVisibleRecommendations as jest.Mock).mockReturnValue(spRecs);
      // Reset the column-filters mock to a deterministic empty state so a
      // prior test in this describe block leaving a custom mockReturnValue
      // (e.g. the indeterminate test setting `service: { values: [...] }`)
      // doesn't bleed into a later test's popover-build that reads
      // getRecommendationsColumnFilters() during resync.
      (state.getRecommendationsColumnFilters as jest.Mock).mockReturnValue({});
    });

    test('service popover renders the All Savings Plans group toggle when 2+ SP slugs present', async () => {
      await loadRecommendations();
      const serviceBtn = document.querySelector<HTMLButtonElement>('th .column-filter-btn[data-column="service"]');
      serviceBtn?.click();
      const groupBox = document.querySelector<HTMLInputElement>('.column-filter-popover input[data-role="sp-group"]');
      expect(groupBox).not.toBeNull();
      const groupLabel = groupBox?.closest('label');
      expect(groupLabel?.textContent).toContain('All Savings Plans');
    });

    test('group toggle does NOT render for non-service columns', async () => {
      await loadRecommendations();
      const providerBtn = document.querySelector<HTMLButtonElement>('th .column-filter-btn[data-column="provider"]');
      providerBtn?.click();
      const groupBox = document.querySelector('.column-filter-popover input[data-role="sp-group"]');
      expect(groupBox).toBeNull();
    });

    test('group toggle does NOT render when only 0 or 1 SP slugs present', async () => {
      const oneSPRec = [
        { id: 'rec1', provider: 'aws', cloud_account_id: 'a1', service: 'ec2',                   resource_type: 't3.medium', region: 'us-east-1', count: 1, term: 1, savings: 100, upfront_cost: 500 },
        { id: 'rec2', provider: 'aws', cloud_account_id: 'a1', service: 'savings-plans-compute', resource_type: 'sp',        region: 'us-east-1', count: 1, term: 1, savings: 200, upfront_cost: 800 },
      ];
      (api.getRecommendations as jest.Mock).mockResolvedValue({ summary: {}, recommendations: oneSPRec, regions: [] });
      (state.getRecommendations as jest.Mock).mockReturnValue(oneSPRec);
      (state.getVisibleRecommendations as jest.Mock).mockReturnValue(oneSPRec);

      await loadRecommendations();
      const serviceBtn = document.querySelector<HTMLButtonElement>('th .column-filter-btn[data-column="service"]');
      serviceBtn?.click();
      const groupBox = document.querySelector('.column-filter-popover input[data-role="sp-group"]');
      expect(groupBox).toBeNull();
    });

    test('clicking group toggle commits a filter with all SP slug values', async () => {
      await loadRecommendations();
      const serviceBtn = document.querySelector<HTMLButtonElement>('th .column-filter-btn[data-column="service"]');
      serviceBtn?.click();
      const groupBox = document.querySelector<HTMLInputElement>('.column-filter-popover input[data-role="sp-group"]');
      expect(groupBox).not.toBeNull();
      // Browser flips checked → true on first click; simulate that.
      groupBox!.checked = true;
      groupBox!.dispatchEvent(new Event('change'));

      // Filter committed with the three SP values (in any order). Asserting
      // on the mock rather than cb.checked because rerenderRecommendations
      // → resyncOpenPopover resets cb state from the (mocked) filter
      // accessor — the mock call args are the canonical signal of what
      // the click handler did.
      const calls = (state.setRecommendationsColumnFilter as jest.Mock).mock.calls;
      const lastCall = calls[calls.length - 1];
      expect(lastCall[0]).toBe('service');
      expect(lastCall[1]?.kind).toBe('set');
      expect((lastCall[1]?.values as string[]).sort()).toEqual([
        'savings-plans-compute',
        'savings-plans-ec2instance',
        'savings-plans-sagemaker',
      ]);
    });

    test('clicking group toggle (off) clears the SP filter', async () => {
      // Pre-set the filter so the SP tri-state renders as checked.
      (state.getRecommendationsColumnFilters as jest.Mock).mockReturnValue({
        service: { kind: 'set', values: ['savings-plans-compute', 'savings-plans-ec2instance', 'savings-plans-sagemaker'] },
      });
      await loadRecommendations();
      const serviceBtn = document.querySelector<HTMLButtonElement>('th .column-filter-btn[data-column="service"]');
      serviceBtn?.click();
      const groupBox = document.querySelector<HTMLInputElement>('.column-filter-popover input[data-role="sp-group"]');
      expect(groupBox?.checked).toBe(true);
      // Browser flips → unchecked on click of an already-checked tri-state.
      groupBox!.checked = false;
      groupBox!.dispatchEvent(new Event('change'));

      // No SP boxes selected → commit() treats "no selections" as "no
      // narrowing" and persists null (filter cleared).
      expect(state.setRecommendationsColumnFilter).toHaveBeenLastCalledWith('service', null);
    });

    test('group toggle resyncs to indeterminate when only some SPs are filter-active', async () => {
      (state.getRecommendationsColumnFilters as jest.Mock).mockReturnValue({
        service: { kind: 'set', values: ['savings-plans-compute'] },
      });
      await loadRecommendations();
      const serviceBtn = document.querySelector<HTMLButtonElement>('th .column-filter-btn[data-column="service"]');
      serviceBtn?.click();
      const groupBox = document.querySelector<HTMLInputElement>('.column-filter-popover input[data-role="sp-group"]');
      expect(groupBox?.checked).toBe(false);
      expect(groupBox?.indeterminate).toBe(true);
    });

    test('individual SP checkbox change commits the partial-SP filter', async () => {
      await loadRecommendations();
      const serviceBtn = document.querySelector<HTMLButtonElement>('th .column-filter-btn[data-column="service"]');
      serviceBtn?.click();
      const cbCompute = document.querySelector<HTMLInputElement>('.column-filter-popover input[data-value="savings-plans-compute"]');
      cbCompute!.checked = true;
      cbCompute!.dispatchEvent(new Event('change'));
      // 1 of 4 service distinct values selected → filter committed with
      // just that slug.
      const calls = (state.setRecommendationsColumnFilter as jest.Mock).mock.calls;
      const lastCall = calls[calls.length - 1];
      expect(lastCall[0]).toBe('service');
      expect(lastCall[1]?.kind).toBe('set');
      expect(lastCall[1]?.values).toEqual(['savings-plans-compute']);
    });
  });
});

describe('Bundle B: sticky bottom action box', () => {
  const recs = [
    { id: 'r1', provider: 'aws', cloud_account_id: 'a1', service: 'ec2', resource_type: 't3.medium', region: 'us-east-1', count: 1, term: 1, savings: 100, upfront_cost: 500 },
    { id: 'r2', provider: 'aws', cloud_account_id: 'a1', service: 'ec2', resource_type: 't3.large', region: 'us-east-1', count: 2, term: 3, savings: 200, upfront_cost: 800 },
  ];

  beforeEach(() => {
    document.body.replaceChildren();
    const recsTab = document.createElement('div');
    recsTab.id = 'recommendations-tab';
    recsTab.className = 'tab-content active';
    const summary = document.createElement('div');
    summary.id = 'recommendations-summary';
    const list = document.createElement('div');
    list.id = 'recommendations-list';
    recsTab.appendChild(summary);
    recsTab.appendChild(list);
    document.body.appendChild(recsTab);

    jest.clearAllMocks();
    (api.getRecommendations as jest.Mock).mockResolvedValue({ summary: {}, recommendations: recs, regions: [] });
    (state.getRecommendations as jest.Mock).mockReturnValue(recs);
    (state.getVisibleRecommendations as jest.Mock).mockReturnValue(recs);
    (state.getRecommendationsColumnFilters as jest.Mock).mockReturnValue({});
    (state.getSelectedRecommendationIDs as jest.Mock).mockReturnValue(new Set());
  });

  test('bottom box exposes Payment / Capacity % / Purchase / Create Plan; no Term selector', async () => {
    await loadRecommendations();
    expect(document.getElementById('bulk-purchase-payment')).not.toBeNull();
    expect(document.getElementById('bulk-purchase-capacity')).not.toBeNull();
    expect(document.getElementById('bulk-purchase-btn')).not.toBeNull();
    expect(document.getElementById('create-plan-btn')).not.toBeNull();
    // Term selector is gone — each rec carries its own term through the API call.
    expect(document.getElementById('bulk-purchase-term')).toBeNull();
  });

  test('button labels reflect the action target', async () => {
    await loadRecommendations();
    let purchaseBtn = document.getElementById('bulk-purchase-btn') as HTMLButtonElement;
    expect(purchaseBtn.textContent).toBe('Purchase 2 visible');

    (state.getSelectedRecommendationIDs as jest.Mock).mockReturnValue(new Set(['r1']));
    await loadRecommendations();
    purchaseBtn = document.getElementById('bulk-purchase-btn') as HTMLButtonElement;
    expect(purchaseBtn.textContent).toBe('Purchase 1 selected');
  });

  test('buttons disabled when target is empty', async () => {
    (state.getVisibleRecommendations as jest.Mock).mockReturnValue([]);
    (api.getRecommendations as jest.Mock).mockResolvedValue({ summary: {}, recommendations: [], regions: [] });
    await loadRecommendations();
    const purchaseBtn = document.getElementById('bulk-purchase-btn') as HTMLButtonElement;
    const planBtn = document.getElementById('create-plan-btn') as HTMLButtonElement;
    expect(purchaseBtn.disabled).toBe(true);
    expect(planBtn.disabled).toBe(true);
  });

  test('Capacity input value persists across re-render (mount-once-then-update)', async () => {
    await loadRecommendations();
    const cap = document.getElementById('bulk-purchase-capacity') as HTMLInputElement;
    cap.value = '50';
    // Trigger an unrelated re-render via sort
    const header = document.querySelector<HTMLTableCellElement>('th[data-sort="service"]');
    header?.click();
    const cap2 = document.getElementById('bulk-purchase-capacity') as HTMLInputElement;
    // Same DOM node (mount-once); value preserved (no rebuild).
    expect(cap2).toBe(cap);
    expect(cap2.value).toBe('50');
  });
});

describe('Bundle B: term-aware bucketing in the Purchase flow', () => {
  beforeEach(() => {
    document.body.replaceChildren();
    const recsTab = document.createElement('div');
    recsTab.id = 'recommendations-tab';
    recsTab.className = 'tab-content active';
    const summary = document.createElement('div');
    summary.id = 'recommendations-summary';
    const list = document.createElement('div');
    list.id = 'recommendations-list';
    recsTab.appendChild(summary);
    recsTab.appendChild(list);
    document.body.appendChild(recsTab);
    const purchaseModal = document.createElement('div');
    purchaseModal.id = 'purchase-modal';
    purchaseModal.className = 'hidden';
    const purchaseDetails = document.createElement('div');
    purchaseDetails.id = 'purchase-details';
    purchaseModal.appendChild(purchaseDetails);
    document.body.appendChild(purchaseModal);
    jest.clearAllMocks();
  });

  test('multi-term selection produces multiple fan-out buckets', async () => {
    // Two different resource types (different cells) with different terms.
    // issues #225/#226: resolvePurchaseTarget now uses pickBestVariantPerCell
    // for the default (no-selection) target — each cell contributes one rec.
    // Use different resource_type so each rec is its own cell (no grouping),
    // ensuring both are selected as the default target and trigger fan-out.
    const mixed = [
      { id: 'a', provider: 'aws', cloud_account_id: 'a1', service: 'ec2', resource_type: 't3.medium', region: 'us-east-1', count: 1, term: 1, savings: 100, upfront_cost: 500 },
      { id: 'b', provider: 'aws', cloud_account_id: 'a1', service: 'ec2', resource_type: 'm5.large', region: 'us-east-1', count: 1, term: 3, savings: 200, upfront_cost: 800 },
    ];
    (api.getRecommendations as jest.Mock).mockResolvedValue({ summary: {}, recommendations: mixed, regions: [] });
    (state.getRecommendations as jest.Mock).mockReturnValue(mixed);
    (state.getVisibleRecommendations as jest.Mock).mockReturnValue(mixed);
    (state.getRecommendationsColumnFilters as jest.Mock).mockReturnValue({});
    (state.getSelectedRecommendationIDs as jest.Mock).mockReturnValue(new Set());

    await loadRecommendations();
    const purchaseBtn = document.getElementById('bulk-purchase-btn') as HTMLButtonElement;
    purchaseBtn.click();
    // openFanOutModal is async (issue #111: pre-fetches per-account
    // overrides). Yield twice so the awaits inside resolve before
    // we read getFanOutBuckets().
    await Promise.resolve();
    await Promise.resolve();

    const { getFanOutBuckets } = await import('../recommendations');
    const buckets = getFanOutBuckets();
    expect(buckets).not.toBeNull();
    expect(buckets!.length).toBe(2);
    expect(buckets!.map((b) => b.term).sort()).toEqual([1, 3]);
    // Each bucket carries the rec's own term, not a toolbar override.
    const b0 = buckets![0]!;
    const b1 = buckets![1]!;
    expect(b0.recs.every((r) => r.term === b0.term)).toBe(true);
    expect(b1.recs.every((r) => r.term === b1.term)).toBe(true);
  });
});

// Issue #111: per-bucket Payment dropdown in the fan-out modal,
// seeded from the per-account service override when all recs in a
// bucket share one cloud_account_id and that account has a saved
// override matching the bucket's (provider, service). Otherwise
// (multi-account, no override, override has no payment, override
// payment unsupported by the (provider, service, term) cell) the
// bucket falls back to the toolbar payment.
describe('Issue #111: per-bucket Payment seed from per-account service override', () => {
  beforeEach(() => {
    document.body.replaceChildren();
    const recsTab = document.createElement('div');
    recsTab.id = 'recommendations-tab';
    recsTab.className = 'tab-content active';
    const summary = document.createElement('div');
    summary.id = 'recommendations-summary';
    const list = document.createElement('div');
    list.id = 'recommendations-list';
    recsTab.appendChild(summary);
    recsTab.appendChild(list);
    document.body.appendChild(recsTab);
    const purchaseModal = document.createElement('div');
    purchaseModal.id = 'purchase-modal';
    purchaseModal.className = 'hidden';
    const purchaseDetails = document.createElement('div');
    purchaseDetails.id = 'purchase-details';
    purchaseModal.appendChild(purchaseDetails);
    document.body.appendChild(purchaseModal);
    jest.clearAllMocks();
    // Default: empty overrides — overridden per-test.
    (api.listAccountServiceOverrides as jest.Mock).mockResolvedValue([]);
  });

  // Force a multi-bucket fan-out by mixing terms (1yr + 3yr both
  // present); this drives openFanOutModal rather than the
  // single-bucket happy path. Each test seeds overrides differently
  // to exercise the (override / no-override / multi-account / edit)
  // matrix.
  const setupMixedTermRecs = (recs: Array<Record<string, unknown>>): void => {
    (api.getRecommendations as jest.Mock).mockResolvedValue({ summary: {}, recommendations: recs, regions: [] });
    (state.getRecommendations as jest.Mock).mockReturnValue(recs);
    (state.getVisibleRecommendations as jest.Mock).mockReturnValue(recs);
    (state.getRecommendationsColumnFilters as jest.Mock).mockReturnValue({});
    (state.getSelectedRecommendationIDs as jest.Mock).mockReturnValue(new Set());
  };

  test('(a) single-account bucket with matching override → bucket payment seeded from override', async () => {
    // issues #225/#226: resolvePurchaseTarget uses pickBestVariantPerCell for the
    // default path. Use different resource_type values so each rec is its own cell,
    // ensuring both appear in the default target and trigger fan-out.
    const recs = [
      { id: 'a', provider: 'aws', cloud_account_id: 'test-account-a', service: 'ec2', resource_type: 't3.medium', region: 'us-east-1', count: 1, term: 1, savings: 100, upfront_cost: 500 },
      { id: 'b', provider: 'aws', cloud_account_id: 'test-account-a', service: 'ec2', resource_type: 'm5.large', region: 'us-east-1', count: 1, term: 3, savings: 200, upfront_cost: 800 },
    ];
    setupMixedTermRecs(recs);
    // Override pins payment=partial-upfront for AWS EC2 on this
    // account. partial-upfront is supported for both 1yr and 3yr.
    (api.listAccountServiceOverrides as jest.Mock).mockImplementation(async (id: string) => {
      if (id === 'test-account-a') {
        return [{ id: 'ovr-1', account_id: 'test-account-a', provider: 'aws', service: 'ec2', payment: 'partial-upfront' }];
      }
      return [];
    });

    await loadRecommendations();
    (document.getElementById('bulk-purchase-btn') as HTMLButtonElement).click();
    await Promise.resolve(); await Promise.resolve(); await Promise.resolve();

    const { getFanOutBuckets } = await import('../recommendations');
    const buckets = getFanOutBuckets();
    expect(buckets).not.toBeNull();
    expect(buckets!.length).toBe(2);
    for (const b of buckets!) {
      expect(b.payment).toBe('partial-upfront');
      expect(b.paymentSource).toBe('override');
    }
    // Source-note span renders.
    const noteSpans = document.querySelectorAll('.fanout-bucket-payment-source');
    expect(noteSpans.length).toBe(2);
    expect((noteSpans[0] as HTMLElement).textContent).toContain('account override');
    // Dropdown's selected value matches.
    const selects = document.querySelectorAll<HTMLSelectElement>('.fanout-bucket-payment');
    expect(selects.length).toBe(2);
    expect(selects[0]!.value).toBe('partial-upfront');
  });

  test('(b) single-account bucket with NO matching override → bucket payment seeded from toolbar', async () => {
    // issues #225/#226: use different resource_type so each rec is its own cell.
    const recs = [
      { id: 'a', provider: 'aws', cloud_account_id: 'test-account-a', service: 'ec2', resource_type: 't3.medium', region: 'us-east-1', count: 1, term: 1, savings: 100, upfront_cost: 500 },
      { id: 'b', provider: 'aws', cloud_account_id: 'test-account-a', service: 'ec2', resource_type: 'm5.large', region: 'us-east-1', count: 1, term: 3, savings: 200, upfront_cost: 800 },
    ];
    setupMixedTermRecs(recs);
    // Account exists but has overrides for a DIFFERENT service — the
    // ec2 lookup should miss and fall back.
    (api.listAccountServiceOverrides as jest.Mock).mockImplementation(async (id: string) => {
      if (id === 'test-account-a') {
        return [{ id: 'ovr-1', account_id: 'test-account-a', provider: 'aws', service: 'rds', payment: 'no-upfront' }];
      }
      return [];
    });

    await loadRecommendations();
    (document.getElementById('bulk-purchase-btn') as HTMLButtonElement).click();
    await Promise.resolve(); await Promise.resolve(); await Promise.resolve();

    const { getFanOutBuckets } = await import('../recommendations');
    const buckets = getFanOutBuckets();
    expect(buckets).not.toBeNull();
    for (const b of buckets!) {
      // Toolbar default is 'all-upfront' (loadBulkPurchaseState's
      // defaultBulkPurchaseState).
      expect(b.payment).toBe('all-upfront');
      expect(b.paymentSource).toBe('toolbar');
    }
    // No source-note rendered.
    expect(document.querySelectorAll('.fanout-bucket-payment-source').length).toBe(0);
  });

  test('(c) multi-account bucket → bucket payment seeded from toolbar regardless of any override', async () => {
    // Two recs, same (provider, service, term) — bucket-key match —
    // but different cloud_account_ids. resolveBucketPaymentSeed must
    // return toolbar (the documented multi-account fallback).
    // Pair with a third 3yr rec to force multi-bucket fan-out.
    //
    // issues #225/#226: resolvePurchaseTarget uses pickBestVariantPerCell so
    // each rec must be in its own cell. Recs 'a' and 'b' differ by account
    // (already distinct cells). Rec 'c' gets a different resource_type so it
    // doesn't collapse into the same cell as rec 'a' (same account-a).
    const recs = [
      { id: 'a', provider: 'aws', cloud_account_id: 'test-account-a', service: 'ec2', resource_type: 't3.medium', region: 'us-east-1', count: 1, term: 1, savings: 100, upfront_cost: 500 },
      { id: 'b', provider: 'aws', cloud_account_id: 'test-account-b', service: 'ec2', resource_type: 't3.medium', region: 'us-east-1', count: 1, term: 1, savings: 150, upfront_cost: 600 },
      { id: 'c', provider: 'aws', cloud_account_id: 'test-account-a', service: 'ec2', resource_type: 'r5.large', region: 'us-east-1', count: 1, term: 3, savings: 200, upfront_cost: 800 },
    ];
    setupMixedTermRecs(recs);
    // Both accounts have ec2 overrides — the multi-account bucket
    // must NOT pick either; only the single-account 3yr bucket may
    // honour the override.
    (api.listAccountServiceOverrides as jest.Mock).mockImplementation(async (id: string) => {
      if (id === 'test-account-a') return [{ id: 'ovr-a', account_id: 'test-account-a', provider: 'aws', service: 'ec2', payment: 'partial-upfront' }];
      if (id === 'test-account-b') return [{ id: 'ovr-b', account_id: 'test-account-b', provider: 'aws', service: 'ec2', payment: 'no-upfront' }];
      return [];
    });

    await loadRecommendations();
    (document.getElementById('bulk-purchase-btn') as HTMLButtonElement).click();
    await Promise.resolve(); await Promise.resolve(); await Promise.resolve();

    const { getFanOutBuckets } = await import('../recommendations');
    const buckets = getFanOutBuckets();
    expect(buckets).not.toBeNull();
    expect(buckets!.length).toBe(2);
    const bucket1yr = buckets!.find((b) => b.term === 1)!;
    const bucket3yr = buckets!.find((b) => b.term === 3)!;
    // 1yr bucket: 2 recs, 2 distinct accounts → toolbar.
    expect(bucket1yr.recs.length).toBe(2);
    expect(bucket1yr.payment).toBe('all-upfront');
    expect(bucket1yr.paymentSource).toBe('toolbar');
    // 3yr bucket: 1 rec, single account a → override honoured.
    expect(bucket3yr.recs.length).toBe(1);
    expect(bucket3yr.payment).toBe('partial-upfront');
    expect(bucket3yr.paymentSource).toBe('override');
  });

  test('(d) user-edited Payment dropdown is reflected in module state', async () => {
    // issues #225/#226: use different resource_type so each rec is its own cell.
    const recs = [
      { id: 'a', provider: 'aws', cloud_account_id: 'test-account-a', service: 'ec2', resource_type: 't3.medium', region: 'us-east-1', count: 1, term: 1, savings: 100, upfront_cost: 500 },
      { id: 'b', provider: 'aws', cloud_account_id: 'test-account-a', service: 'ec2', resource_type: 'm5.large', region: 'us-east-1', count: 1, term: 3, savings: 200, upfront_cost: 800 },
    ];
    setupMixedTermRecs(recs);

    await loadRecommendations();
    (document.getElementById('bulk-purchase-btn') as HTMLButtonElement).click();
    await Promise.resolve(); await Promise.resolve(); await Promise.resolve();

    const { getFanOutBuckets } = await import('../recommendations');
    const before = getFanOutBuckets();
    expect(before).not.toBeNull();
    expect(before![0]!.payment).toBe('all-upfront');

    // Pick the dropdown for the 1yr bucket and switch to no-upfront
    // (a supported value for AWS EC2 1yr).
    const selects = Array.from(document.querySelectorAll<HTMLSelectElement>('.fanout-bucket-payment'));
    expect(selects.length).toBe(2);
    const firstSel = selects[0]!;
    firstSel.value = 'no-upfront';
    firstSel.dispatchEvent(new Event('change'));

    const after = getFanOutBuckets();
    expect(after).not.toBeNull();
    // The bucket whose dropdown we changed now reports 'no-upfront'.
    // We don't rely on bucket[0] vs bucket[1] order; pick by recs
    // identity to find which bucket the first select belonged to.
    const firstBucketIdx = before!.findIndex((b) => b.recs[0]?.id === recs[0]!.id || b.recs[0]?.id === recs[1]!.id);
    expect(firstBucketIdx).toBeGreaterThanOrEqual(0);
    // At least one bucket payment must now be 'no-upfront'.
    expect(after!.some((b) => b.payment === 'no-upfront')).toBe(true);
  });
});

// Issue #111 (iii): per-row Payment seed in openPurchaseModal — the
// single-bucket / single-rec purchase modal renders editable Term and
// Payment dropdowns. Each row's defaults walk the precedence
// override → rec.payment → paymentOptionsFor[0]. Edits mutate
// currentPurchaseRecommendations[idx] in place; getPurchaseModalRecommendations()
// returns the user's choices; app.ts::handleExecutePurchase posts
// `r.payment` per rec (no longer hardcoded 'all-upfront').
describe('Issue #111 (iii): per-row Payment seed in openPurchaseModal', () => {
  beforeEach(() => {
    document.body.replaceChildren();
    const purchaseModal = document.createElement('div');
    purchaseModal.id = 'purchase-modal';
    purchaseModal.className = 'hidden';
    const purchaseDetails = document.createElement('div');
    purchaseDetails.id = 'purchase-details';
    purchaseModal.appendChild(purchaseDetails);
    document.body.appendChild(purchaseModal);
    jest.clearAllMocks();
    (api.listAccountServiceOverrides as jest.Mock).mockResolvedValue([]);
  });

  test('(a) single rec with matching override → row Payment seeded from override; source-note rendered', async () => {
    (api.listAccountServiceOverrides as jest.Mock).mockImplementation(async (id: string) => {
      if (id === 'test-account-a') {
        return [{ id: 'ovr-1', account_id: 'test-account-a', provider: 'aws', service: 'ec2', payment: 'partial-upfront' }];
      }
      return [];
    });

    const rec = {
      id: 'rec-1', provider: 'aws' as const, cloud_account_id: 'test-account-a',
      service: 'ec2', resource_type: 't3.medium', region: 'us-east-1',
      count: 5, term: 1, payment: 'all-upfront', savings: 100, upfront_cost: 500,
    };

    await openPurchaseModal([rec]);

    const live = getPurchaseModalRecommendations();
    expect(live).toHaveLength(1);
    expect(live[0]!.payment).toBe('partial-upfront');

    const select = document.querySelector<HTMLSelectElement>('.purchase-row-payment');
    expect(select).not.toBeNull();
    expect(select!.value).toBe('partial-upfront');

    const note = document.querySelector<HTMLElement>('.purchase-row-payment-source');
    expect(note).not.toBeNull();
    expect(note!.textContent).toContain('account override');
  });

  test('(b) single rec with NO matching override → row Payment seeded from rec.payment; no source-note', async () => {
    // Override exists but for a different service — the lookup misses
    // and the rec's own payment ('partial-upfront') wins.
    (api.listAccountServiceOverrides as jest.Mock).mockImplementation(async (id: string) => {
      if (id === 'test-account-a') {
        return [{ id: 'ovr-1', account_id: 'test-account-a', provider: 'aws', service: 'rds', payment: 'no-upfront' }];
      }
      return [];
    });

    const rec = {
      id: 'rec-2', provider: 'aws' as const, cloud_account_id: 'test-account-a',
      service: 'ec2', resource_type: 't3.medium', region: 'us-east-1',
      count: 5, term: 1, payment: 'partial-upfront', savings: 100, upfront_cost: 500,
    };

    await openPurchaseModal([rec]);

    const live = getPurchaseModalRecommendations();
    expect(live[0]!.payment).toBe('partial-upfront');

    const select = document.querySelector<HTMLSelectElement>('.purchase-row-payment');
    expect(select!.value).toBe('partial-upfront');

    const note = document.querySelector('.purchase-row-payment-source');
    expect(note).toBeNull();
  });

  test('(c) override has unsupported payment for the (provider,service,term) cell → falls back to rec.payment', async () => {
    // AWS RDS 3yr does NOT support no-upfront (per
    // isPaymentSupported / cmd/validators.go:warnRDS3YearNoUpfront).
    // The override pins no-upfront → must be ignored; rec.payment wins.
    (api.listAccountServiceOverrides as jest.Mock).mockImplementation(async (id: string) => {
      if (id === 'test-account-a') {
        return [{ id: 'ovr-1', account_id: 'test-account-a', provider: 'aws', service: 'rds', payment: 'no-upfront' }];
      }
      return [];
    });

    const rec = {
      id: 'rec-3', provider: 'aws' as const, cloud_account_id: 'test-account-a',
      service: 'rds', resource_type: 'db.r5.large', region: 'us-east-1',
      count: 1, term: 3, payment: 'all-upfront', savings: 200, upfront_cost: 1000,
    };

    await openPurchaseModal([rec]);

    const live = getPurchaseModalRecommendations();
    expect(live[0]!.payment).toBe('all-upfront');

    const note = document.querySelector('.purchase-row-payment-source');
    expect(note).toBeNull();
  });

  test('(d) user changes Term 1→3 → row Payment options rebuilt; live state still consistent', async () => {
    const rec = {
      id: 'rec-4', provider: 'aws' as const, cloud_account_id: 'test-account-a',
      service: 'ec2', resource_type: 't3.medium', region: 'us-east-1',
      count: 5, term: 1, payment: 'all-upfront', savings: 100, upfront_cost: 500,
    };

    await openPurchaseModal([rec]);

    const termSelect = document.querySelector<HTMLSelectElement>('.purchase-row-term');
    const paymentSelect = document.querySelector<HTMLSelectElement>('.purchase-row-payment');
    expect(termSelect).not.toBeNull();
    expect(paymentSelect).not.toBeNull();

    // Switch term to 3yr.
    termSelect!.value = '3';
    termSelect!.dispatchEvent(new Event('change'));

    const live = getPurchaseModalRecommendations();
    expect(live[0]!.term).toBe(3);
    // Payment is set to a value supported for AWS EC2 3yr — the
    // exact value depends on whether 'all-upfront' (the prior value)
    // remained valid for the new term; we only require that:
    //   (i) live.payment is non-empty
    //   (ii) live.payment matches the dropdown's current value
    //   (iii) the dropdown's options are now the 3yr-supported set.
    expect(live[0]!.payment).toBeTruthy();
    expect(paymentSelect!.value).toBe(live[0]!.payment);
    const options = Array.from(paymentSelect!.options).map((o) => o.value);
    expect(options.length).toBeGreaterThan(0);
  });

  test('(e) user changes Payment dropdown → live state reflects new value (and would round-trip via handleExecutePurchase)', async () => {
    const rec = {
      id: 'rec-5', provider: 'aws' as const, cloud_account_id: 'test-account-a',
      service: 'ec2', resource_type: 't3.medium', region: 'us-east-1',
      count: 5, term: 1, payment: 'all-upfront', savings: 100, upfront_cost: 500,
    };

    await openPurchaseModal([rec]);

    const paymentSelect = document.querySelector<HTMLSelectElement>('.purchase-row-payment');
    expect(paymentSelect).not.toBeNull();

    // Change to 'no-upfront' (always supported for AWS EC2 1yr).
    paymentSelect!.value = 'no-upfront';
    paymentSelect!.dispatchEvent(new Event('change'));

    const live = getPurchaseModalRecommendations();
    expect(live[0]!.payment).toBe('no-upfront');

    // The mapping in app.ts::handleExecutePurchase reads this value
    // verbatim (`payment: r.payment ?? 'all-upfront'`), so a downstream
    // assertion that the API call carries 'no-upfront' is implicit in
    // (a) the live state above and (b) the source-of-truth read at
    // app.ts:289-303. No separate mock-call assertion needed here —
    // the integration of that mapping is exercised by app.ts'
    // existing handleExecutePurchase tests.
  });
});

// Issue #132: pre-PR-#123 a 'savings-plans' Compute SP rec and a
// 'savings-plans' SageMaker SP rec landed in the same bulk-buy
// bucket. PR #123 split them into per-plan-type service slugs, which
// silently fanned out into N separate buckets and N separate approval
// emails. This restores the one-bucket experience.
describe('Issue #132: bulk-buy collapses SP plan types into one bucket', () => {
  beforeEach(async () => {
    document.body.replaceChildren();
    const recsTab = document.createElement('div');
    recsTab.id = 'recommendations-tab';
    recsTab.className = 'tab-content active';
    const summary = document.createElement('div');
    summary.id = 'recommendations-summary';
    const list = document.createElement('div');
    list.id = 'recommendations-list';
    recsTab.appendChild(summary);
    recsTab.appendChild(list);
    document.body.appendChild(recsTab);
    const purchaseModal = document.createElement('div');
    purchaseModal.id = 'purchase-modal';
    purchaseModal.className = 'hidden';
    const purchaseDetails = document.createElement('div');
    purchaseDetails.id = 'purchase-details';
    purchaseModal.appendChild(purchaseDetails);
    document.body.appendChild(purchaseModal);
    jest.clearAllMocks();
    // Default: empty overrides — issue #132's bucket-key collapse is
    // independent of the issue #111 override-seed path, but
    // openFanOutModal still calls listAccountServiceOverrides per
    // single-account bucket, so we keep the mock primed.
    (api.listAccountServiceOverrides as jest.Mock).mockResolvedValue([]);
    // Module-level FanOut state survives test isolation; reset it so
    // a previous test's openFanOutModal call doesn't leak into a
    // single-bucket-happy-path assertion here.
    const { clearFanOutBuckets, clearPurchaseModalRecommendations } = await import('../recommendations');
    clearFanOutBuckets();
    clearPurchaseModalRecommendations();
  });

  test('compute + sagemaker SPs at term=1 share a single bucket (happy path)', async () => {
    const recs = [
      { id: 's1', provider: 'aws', cloud_account_id: 'a1', service: 'savings-plans-compute',   resource_type: 'sp', region: 'us-east-1', count: 1, term: 1, savings: 100, upfront_cost: 500 },
      { id: 's2', provider: 'aws', cloud_account_id: 'a1', service: 'savings-plans-sagemaker', resource_type: 'sp', region: 'us-east-1', count: 1, term: 1, savings: 200, upfront_cost: 800 },
    ];
    (api.getRecommendations as jest.Mock).mockResolvedValue({ summary: {}, recommendations: recs, regions: [] });
    (state.getRecommendations as jest.Mock).mockReturnValue(recs);
    (state.getVisibleRecommendations as jest.Mock).mockReturnValue(recs);
    (state.getRecommendationsColumnFilters as jest.Mock).mockReturnValue({});
    (state.getSelectedRecommendationIDs as jest.Mock).mockReturnValue(new Set());

    await loadRecommendations();
    (document.getElementById('bulk-purchase-btn') as HTMLButtonElement).click();

    const { getFanOutBuckets, getPurchaseModalRecommendations } = await import('../recommendations');
    // 1 collapsed bucket → openPurchaseModal happy path, no fan-out.
    expect(getFanOutBuckets()).toBeNull();
    // The single-bucket modal carries BOTH SPs (proves they collapsed).
    const modalRecs = getPurchaseModalRecommendations();
    expect(modalRecs.map((r) => r.id).sort()).toEqual(['s1', 's2']);
    expect(modalRecs.map((r) => r.service).sort()).toEqual([
      'savings-plans-compute',
      'savings-plans-sagemaker',
    ]);
  });

  test('SP plan types + a non-SP rec produce one SP bucket + one EC2 bucket', async () => {
    const recs = [
      { id: 's1', provider: 'aws', cloud_account_id: 'a1', service: 'savings-plans-compute',     resource_type: 'sp', region: 'us-east-1', count: 1, term: 1, savings: 100, upfront_cost: 500 },
      { id: 's2', provider: 'aws', cloud_account_id: 'a1', service: 'savings-plans-sagemaker',   resource_type: 'sp', region: 'us-east-1', count: 1, term: 1, savings: 150, upfront_cost: 600 },
      { id: 's3', provider: 'aws', cloud_account_id: 'a1', service: 'savings-plans-ec2instance', resource_type: 'sp', region: 'us-east-1', count: 1, term: 1, savings: 200, upfront_cost: 800 },
      { id: 'e1', provider: 'aws', cloud_account_id: 'a1', service: 'ec2',                       resource_type: 't3.medium', region: 'us-east-1', count: 1, term: 1, savings:  50, upfront_cost: 300 },
    ];
    (api.getRecommendations as jest.Mock).mockResolvedValue({ summary: {}, recommendations: recs, regions: [] });
    (state.getRecommendations as jest.Mock).mockReturnValue(recs);
    (state.getVisibleRecommendations as jest.Mock).mockReturnValue(recs);
    (state.getRecommendationsColumnFilters as jest.Mock).mockReturnValue({});
    (state.getSelectedRecommendationIDs as jest.Mock).mockReturnValue(new Set());

    await loadRecommendations();
    (document.getElementById('bulk-purchase-btn') as HTMLButtonElement).click();
    // openFanOutModal is async (issue #111 prefetch); wait a tick.
    await Promise.resolve(); await Promise.resolve(); await Promise.resolve();

    const { getFanOutBuckets } = await import('../recommendations');
    const buckets = getFanOutBuckets();
    expect(buckets).not.toBeNull();
    // Expect 2 buckets total: 1 collapsed SP bucket (3 recs) + 1 EC2 bucket (1 rec).
    expect(buckets!.length).toBe(2);

    const spBucket = buckets!.find((b) => b.service === 'savings-plans');
    expect(spBucket).toBeDefined();
    expect(spBucket!.recs).toHaveLength(3);
    // Per-rec services are preserved — backend audit/suppression keeps
    // the real plan type per rec.
    expect(spBucket!.recs.map((r) => r.service).sort()).toEqual([
      'savings-plans-compute',
      'savings-plans-ec2instance',
      'savings-plans-sagemaker',
    ]);

    const ec2Bucket = buckets!.find((b) => b.service === 'ec2');
    expect(ec2Bucket).toBeDefined();
    expect(ec2Bucket!.recs).toHaveLength(1);
  });

  test('SP recs at different terms still split by term', async () => {
    const recs = [
      { id: 's1', provider: 'aws', cloud_account_id: 'a1', service: 'savings-plans-compute',   resource_type: 'sp', region: 'us-east-1', count: 1, term: 1, savings: 100, upfront_cost: 500 },
      { id: 's2', provider: 'aws', cloud_account_id: 'a1', service: 'savings-plans-sagemaker', resource_type: 'sp', region: 'us-east-1', count: 1, term: 3, savings: 200, upfront_cost: 800 },
    ];
    (api.getRecommendations as jest.Mock).mockResolvedValue({ summary: {}, recommendations: recs, regions: [] });
    (state.getRecommendations as jest.Mock).mockReturnValue(recs);
    (state.getVisibleRecommendations as jest.Mock).mockReturnValue(recs);
    (state.getRecommendationsColumnFilters as jest.Mock).mockReturnValue({});
    (state.getSelectedRecommendationIDs as jest.Mock).mockReturnValue(new Set());

    await loadRecommendations();
    (document.getElementById('bulk-purchase-btn') as HTMLButtonElement).click();
    await Promise.resolve(); await Promise.resolve(); await Promise.resolve();

    const { getFanOutBuckets } = await import('../recommendations');
    const buckets = getFanOutBuckets();
    expect(buckets).not.toBeNull();
    // Different terms must still fan out — SP collapsing only joins
    // recs that share (provider, term).
    expect(buckets!.length).toBe(2);
    expect(buckets!.every((b) => b.service === 'savings-plans')).toBe(true);
    expect(buckets!.map((b) => b.term).sort()).toEqual([1, 3]);
  });

  test('mixed-SP bucket renders combined plan-type label', async () => {
    const recs = [
      { id: 's1', provider: 'aws', cloud_account_id: 'a1', service: 'savings-plans-compute',   resource_type: 'sp', region: 'us-east-1', count: 1, term: 1, savings: 100, upfront_cost: 500 },
      { id: 's2', provider: 'aws', cloud_account_id: 'a1', service: 'savings-plans-sagemaker', resource_type: 'sp', region: 'us-east-1', count: 1, term: 1, savings: 200, upfront_cost: 800 },
      { id: 'e1', provider: 'aws', cloud_account_id: 'a1', service: 'ec2',                     resource_type: 't3.medium', region: 'us-east-1', count: 1, term: 1, savings:  50, upfront_cost: 300 },
    ];
    (api.getRecommendations as jest.Mock).mockResolvedValue({ summary: {}, recommendations: recs, regions: [] });
    (state.getRecommendations as jest.Mock).mockReturnValue(recs);
    (state.getVisibleRecommendations as jest.Mock).mockReturnValue(recs);
    (state.getRecommendationsColumnFilters as jest.Mock).mockReturnValue({});
    (state.getSelectedRecommendationIDs as jest.Mock).mockReturnValue(new Set());

    await loadRecommendations();
    (document.getElementById('bulk-purchase-btn') as HTMLButtonElement).click();
    await Promise.resolve(); await Promise.resolve(); await Promise.resolve();

    // The fan-out modal renders one section per bucket; the SP section
    // title carries the combined plan-type label.
    const sectionTitles = Array.from(
      document.querySelectorAll('.fanout-bucket h4'),
    ).map((el) => el.textContent || '');
    expect(sectionTitles.some((t) => t.includes('Savings Plans (Compute + SageMaker)'))).toBe(true);
    // Non-SP bucket title still uses the raw service slug.
    expect(sectionTitles.some((t) => t.includes('AWS / ec2'))).toBe(true);
  });
});

// Issue #224: at most one (term, payment) variant per physical-resource
// cell can be selected at any time. After PR #195's per-cell fan-out (2
// terms × 3 payments per cell), naive selection produces wrong purchase
// intent — manual checking lets the user accumulate sibling commitments,
// and `select-all` over-commits 6×. Cell = `(provider, account, service,
// region, resource_type, engine)`. The fix lives in two places: the
// per-row checkbox change handler (deselect any in-cell sibling on check)
// and the select-all handler (group by cell, pick highest-effective per).
describe('Issue #224: one-variant-per-cell radio selection', () => {
  beforeEach(() => {
    document.body.replaceChildren();
    const recsTab = document.createElement('div');
    recsTab.id = 'recommendations-tab';
    recsTab.className = 'tab-content active';
    const summary = document.createElement('div');
    summary.id = 'recommendations-summary';
    const list = document.createElement('div');
    list.id = 'recommendations-list';
    recsTab.appendChild(summary);
    recsTab.appendChild(list);
    document.body.appendChild(recsTab);
    const purchaseModal = document.createElement('div');
    purchaseModal.id = 'purchase-modal';
    purchaseModal.className = 'hidden';
    document.body.appendChild(purchaseModal);
    jest.clearAllMocks();
    (state.getRecommendationsColumnFilters as jest.Mock).mockReturnValue({});
    (state.getSelectedRecommendationIDs as jest.Mock).mockReturnValue(new Set());
  });

  // (a) Manual toggle: two variants of the same cell. Checking variant B
  // when variant A is already selected must remove A first, leaving only B.
  test('(a) checking variant B in same cell deselects sibling variant A', async () => {
    const recs = [
      { id: 'cellA-1y-allup',  provider: 'aws', cloud_account_id: 'acct-1', service: 'ec2', resource_type: 't3.medium', region: 'us-east-1', engine: '', count: 1, term: 1, savings: 100, upfront_cost: 500 },
      { id: 'cellA-3y-noup',   provider: 'aws', cloud_account_id: 'acct-1', service: 'ec2', resource_type: 't3.medium', region: 'us-east-1', engine: '', count: 1, term: 3, savings: 200, upfront_cost: 0 },
    ];
    (api.getRecommendations as jest.Mock).mockResolvedValue({ summary: {}, recommendations: recs, regions: [] });
    // state.getRecommendations() is the full loaded set (used by the sibling-eviction loop).
    (state.getRecommendations as jest.Mock).mockReturnValue(recs);
    // Pretend variant A is already selected (the "user previously checked it" state).
    (state.getSelectedRecommendationIDs as jest.Mock).mockReturnValue(new Set(['cellA-1y-allup']));
    await loadRecommendations();

    // issues #225/#226: multi-variant cells are collapsed by default.
    // Expand the cell first so the variant checkboxes are rendered.
    const chevron = document.querySelector<HTMLButtonElement>('.rec-cell-chevron');
    expect(chevron).not.toBeNull();
    chevron!.click();

    // Tick variant B in the same cell.
    const cbs = Array.from(document.querySelectorAll<HTMLInputElement>('tbody input[data-rec-id]'));
    const variantB = cbs.find((cb) => cb.dataset['recId'] === 'cellA-3y-noup');
    expect(variantB).toBeDefined();
    variantB!.checked = true;
    variantB!.dispatchEvent(new Event('change'));

    // The handler must have removed sibling A AND added B.
    const removed = (state.removeSelectedRecommendation as jest.Mock).mock.calls.map((c) => c[0]);
    const added = (state.addSelectedRecommendation as jest.Mock).mock.calls.map((c) => c[0]);
    expect(removed).toContain('cellA-1y-allup');
    expect(added).toContain('cellA-3y-noup');
    // Sanity: B was not also removed, A was not also added.
    expect(removed).not.toContain('cellA-3y-noup');
    expect(added).not.toContain('cellA-1y-allup');
  });

  // (b) Cross-cell independence: selecting a rec in cell X must not
  // affect cell Y's selection state. The radio enforcement is per-cell,
  // not global.
  test('(b) selecting in cell X does not affect cell Y selections', async () => {
    const recs = [
      { id: 'cellX-1y',  provider: 'aws', cloud_account_id: 'acct-1', service: 'ec2', resource_type: 't3.medium', region: 'us-east-1', engine: '', count: 1, term: 1, savings: 100, upfront_cost: 500 },
      { id: 'cellY-1y',  provider: 'aws', cloud_account_id: 'acct-1', service: 'rds', resource_type: 'db.r5.large', region: 'us-east-1', engine: 'mysql', count: 1, term: 1, savings: 200, upfront_cost: 1000 },
    ];
    (api.getRecommendations as jest.Mock).mockResolvedValue({ summary: {}, recommendations: recs, regions: [] });
    (state.getRecommendations as jest.Mock).mockReturnValue(recs);
    // Pretend cellY is already selected.
    (state.getSelectedRecommendationIDs as jest.Mock).mockReturnValue(new Set(['cellY-1y']));
    await loadRecommendations();

    // Tick cellX.
    const cbs = Array.from(document.querySelectorAll<HTMLInputElement>('tbody input[data-rec-id]'));
    const cellX = cbs.find((cb) => cb.dataset['recId'] === 'cellX-1y');
    expect(cellX).toBeDefined();
    cellX!.checked = true;
    cellX!.dispatchEvent(new Event('change'));

    // cellY must NOT have been removed — cells are independent.
    const removed = (state.removeSelectedRecommendation as jest.Mock).mock.calls.map((c) => c[0]);
    expect(removed).not.toContain('cellY-1y');
    const added = (state.addSelectedRecommendation as jest.Mock).mock.calls.map((c) => c[0]);
    expect(added).toContain('cellX-1y');
  });

  // (c) Select-all collapses to one-per-cell. Three distinct cells × six
  // variants each = 18 recs. After select-all, exactly 3 should be added,
  // not 18 (one per cell). This is the headline money-impact fix.
  test('(c) select-all picks exactly one variant per cell (3 cells × 6 variants → 3 selected)', async () => {
    const cells = [
      { account: 'acct-1', service: 'ec2', resource_type: 't3.medium', region: 'us-east-1', engine: '' },
      { account: 'acct-1', service: 'ec2', resource_type: 'm5.large',  region: 'us-east-1', engine: '' },
      { account: 'acct-1', service: 'rds', resource_type: 'db.r5.large', region: 'eu-west-1', engine: 'mysql' },
    ];
    const variants = [
      { term: 1, payment: 'all-upfront' },
      { term: 1, payment: 'partial-upfront' },
      { term: 1, payment: 'no-upfront' },
      { term: 3, payment: 'all-upfront' },
      { term: 3, payment: 'partial-upfront' },
      { term: 3, payment: 'no-upfront' },
    ];
    const recs: Array<Record<string, unknown>> = [];
    let i = 0;
    for (const c of cells) {
      for (const v of variants) {
        recs.push({
          id: `c${i++}`,
          provider: 'aws', cloud_account_id: c.account, service: c.service,
          resource_type: c.resource_type, region: c.region, engine: c.engine,
          count: 1, term: v.term, savings: 100 + i, upfront_cost: 500,
        });
      }
    }
    expect(recs).toHaveLength(18);

    (api.getRecommendations as jest.Mock).mockResolvedValue({ summary: {}, recommendations: recs, regions: [] });
    (state.getVisibleRecommendations as jest.Mock).mockReturnValue(recs);
    await loadRecommendations();

    const selectAll = document.getElementById('select-all-recs') as HTMLInputElement;
    expect(selectAll).not.toBeNull();
    selectAll.checked = true;
    selectAll.dispatchEvent(new Event('change'));

    // Exactly 3 add calls — one per cell.
    expect((state.addSelectedRecommendation as jest.Mock).mock.calls).toHaveLength(3);
    // And clearSelectedRecommendations was called first to drop any stale state.
    expect(state.clearSelectedRecommendations).toHaveBeenCalled();
  });

  // (d) Tiebreaker: when multiple variants share a cell, select-all picks
  // the variant with the highest EFFECTIVE monthly savings (amortizing
  // upfront over term * 12 months) — NOT the highest raw `savings`.
  // Concrete example: a 3yr/all-upfront with $36000 upfront + $1200/mo
  // headline savings has effective = 1200 - 36000/36 = $200. A 1yr/no-upfront
  // with $0 upfront + $300/mo headline savings has effective = $300. The
  // 1yr/no-upfront wins despite the 3yr's higher raw `savings`.
  test('(d) select-all picks highest-effective-savings (amortized) per cell', async () => {
    const recs = [
      // 3yr/all-upfront — high raw savings ($1200/mo) but huge upfront drags effective to $200/mo.
      { id: 'big-upfront', provider: 'aws', cloud_account_id: 'acct-1', service: 'ec2', resource_type: 't3.medium', region: 'us-east-1', engine: '', count: 1, term: 3, savings: 1200, upfront_cost: 36000 },
      // 1yr/no-upfront — lower raw ($300/mo) but $0 upfront → effective stays at $300/mo.
      { id: 'no-upfront',  provider: 'aws', cloud_account_id: 'acct-1', service: 'ec2', resource_type: 't3.medium', region: 'us-east-1', engine: '', count: 1, term: 1, savings: 300,  upfront_cost: 0 },
      // 3yr/partial-upfront — middle of the road ($600/mo savings, $7200 upfront → effective = 600 - 7200/36 = $400).
      { id: 'middle',      provider: 'aws', cloud_account_id: 'acct-1', service: 'ec2', resource_type: 't3.medium', region: 'us-east-1', engine: '', count: 1, term: 3, savings: 600,  upfront_cost: 7200 },
    ];
    (api.getRecommendations as jest.Mock).mockResolvedValue({ summary: {}, recommendations: recs, regions: [] });
    (state.getVisibleRecommendations as jest.Mock).mockReturnValue(recs);
    await loadRecommendations();

    const selectAll = document.getElementById('select-all-recs') as HTMLInputElement;
    selectAll.checked = true;
    selectAll.dispatchEvent(new Event('change'));

    // Exactly one add call (single cell, one variant picked).
    const addCalls = (state.addSelectedRecommendation as jest.Mock).mock.calls;
    expect(addCalls).toHaveLength(1);
    // The "middle" variant has the highest effective ($400/mo > $300 > $200) — picked.
    expect(addCalls[0]![0]).toBe('middle');
  });
});

// ---------------------------------------------------------------------------
// issue #223: default-seed from GlobalConfig across all 3 surfaces.
// These tests exercise the pickBestVariantPerCell config-match tiebreaker
// and the seedGlobalDefaults hook that injects resolved GlobalConfig values.
// ---------------------------------------------------------------------------

describe('issue #223: pickBestVariantPerCell config-match tiebreaker', () => {
  const rec = (
    id: string,
    term: 1 | 3,
    payment: string,
    savings = 100,
    upfront_cost = 0,
  ) => ({
    id,
    provider: 'aws' as const,
    cloud_account_id: 'acct-1',
    service: 'ec2',
    resource_type: 't3.medium',
    region: 'us-east-1',
    engine: '',
    count: 1,
    term,
    payment,
    savings,
    upfront_cost,
  } as unknown as LocalRecommendation);

  afterEach(() => {
    // Reset module cache to initial defaults so tests don't bleed into each other.
    seedGlobalDefaults(1, 'all-upfront');
  });

  test('prefers variant matching configured (term, payment) over highest-effective', () => {
    // Two variants in one cell: 1yr/all-upfront (configured default) vs
    // 3yr/no-upfront (higher effective savings).
    const recs = [
      rec('want-this', 1, 'all-upfront', 300, 0), // effective = $300/mo
      rec('skip-this', 3, 'no-upfront', 400, 0), // effective = $400/mo (higher)
    ];
    seedGlobalDefaults(1, 'all-upfront');
    const result = pickBestVariantPerCell(recs);
    expect(result).toHaveLength(1);
    expect(result[0]!.id).toBe('want-this');
  });

  test('falls back to highest-effective when no variant matches configured defaults', () => {
    // Neither variant matches term=1/all-upfront; fallback picks highest effective.
    const recs = [
      rec('low-effective', 3, 'all-upfront', 1200, 36000), // effective = 1200 - 1000 = $200
      rec('high-effective', 3, 'no-upfront', 400, 0), // effective = $400
    ];
    seedGlobalDefaults(1, 'all-upfront'); // neither matches
    const result = pickBestVariantPerCell(recs);
    expect(result).toHaveLength(1);
    expect(result[0]!.id).toBe('high-effective');
  });

  test('handles multiple independent cells and picks config-match in each', () => {
    // Two distinct cells; each has a variant matching configured defaults.
    const recs = [
      rec('cell-a-match', 1, 'all-upfront', 100, 0),
      rec('cell-a-other', 3, 'no-upfront', 400, 0), // higher effective but different cell
      { ...rec('cell-b-match', 1, 'all-upfront', 200, 0), region: 'eu-west-1', id: 'cell-b-match' },
      { ...rec('cell-b-other', 3, 'partial-upfront', 600, 0), region: 'eu-west-1', id: 'cell-b-other' },
    ];
    seedGlobalDefaults(1, 'all-upfront');
    const result = pickBestVariantPerCell(recs);
    const ids = result.map((r) => r.id).sort();
    expect(ids).toEqual(['cell-a-match', 'cell-b-match']);
  });

  test('config-match with 3yr/partial-upfront as configured defaults', () => {
    const recs = [
      rec('wrong-1', 1, 'all-upfront', 100, 0),
      rec('want-3yr', 3, 'partial-upfront', 100, 0),
    ];
    seedGlobalDefaults(3, 'partial-upfront');
    const result = pickBestVariantPerCell(recs);
    expect(result).toHaveLength(1);
    expect(result[0]!.id).toBe('want-3yr');
  });
});

// ---------------------------------------------------------------------------
// Issue #220 / #221: effectiveMonthlySavings + effectiveSavingsPct helpers
// + Monthly Cost and Effective % column rendering
// ---------------------------------------------------------------------------

describe('effectiveMonthlySavings', () => {
  const mk = (overrides: Partial<LocalRecommendation>): LocalRecommendation => ({
    id: 'r',
    provider: 'aws',
    service: 'ec2',
    resource_type: 't3.medium',
    region: 'us-east-1',
    count: 1,
    term: 1,
    savings: 100,
    upfront_cost: 0,
    monthly_cost: 50,
    ...overrides,
  } as unknown as LocalRecommendation);

  test('no-upfront: effective equals raw savings when upfront=0', () => {
    expect(effectiveMonthlySavings(mk({ savings: 100, upfront_cost: 0, term: 1 }))).toBeCloseTo(100);
  });

  test('all-upfront: effective = savings - upfront / (term * 12)', () => {
    expect(effectiveMonthlySavings(mk({ savings: 50, upfront_cost: 600, term: 1 }))).toBeCloseTo(0);
    expect(effectiveMonthlySavings(mk({ savings: 1200, upfront_cost: 36000, term: 3 }))).toBeCloseTo(200);
  });

  test('partial-upfront: intermediate amortization', () => {
    expect(effectiveMonthlySavings(mk({ savings: 600, upfront_cost: 7200, term: 3 }))).toBeCloseTo(400);
  });

  test('term=0 clamps to 1yr (12 months) to avoid division by zero', () => {
    expect(effectiveMonthlySavings(mk({ savings: 60, upfront_cost: 120, term: 0 }))).toBeCloseTo(50);
  });

  test('can return negative when upfront dominates (data anomaly signal)', () => {
    expect(effectiveMonthlySavings(mk({ savings: 10, upfront_cost: 1200, term: 1 }))).toBeCloseTo(-90);
  });
});

describe('effectiveSavingsPct', () => {
  const mk = (overrides: Partial<LocalRecommendation>): LocalRecommendation => ({
    id: 'r',
    provider: 'aws',
    service: 'ec2',
    resource_type: 't3.medium',
    region: 'us-east-1',
    count: 1,
    term: 1,
    savings: 100,
    upfront_cost: 0,
    monthly_cost: 50,
    ...overrides,
  } as unknown as LocalRecommendation);

  test('no-upfront: pct = savings / (monthly_cost + savings) * 100', () => {
    const pct = effectiveSavingsPct(mk({ savings: 100, upfront_cost: 0, monthly_cost: 50, term: 1 }));
    expect(pct).not.toBeNull();
    expect(pct!).toBeCloseTo(66.67, 1);
  });

  test('all-upfront with monthly_cost=0: pct uses amortized upfront in onDemand', () => {
    const pct = effectiveSavingsPct(mk({ savings: 50, upfront_cost: 600, monthly_cost: 0, term: 1 }));
    expect(pct).not.toBeNull();
    expect(pct!).toBeCloseTo(0, 1);
  });

  test('partial-upfront: standard case', () => {
    const pct = effectiveSavingsPct(mk({ savings: 600, upfront_cost: 7200, monthly_cost: 200, term: 3 }));
    expect(pct).not.toBeNull();
    expect(pct!).toBeCloseTo(40, 1);
  });

  test('on_demand_monthly=0 returns null (no division by zero)', () => {
    const pct = effectiveSavingsPct(mk({ savings: 0, upfront_cost: 0, monthly_cost: 0, term: 1 }));
    expect(pct).toBeNull();
  });

  test('term=0 clamps to 12 months (no explosion)', () => {
    const pct = effectiveSavingsPct(mk({ savings: 60, upfront_cost: 120, monthly_cost: 40, term: 0 }));
    expect(pct).toBeNull();
  });

  test('negative effective savings returns a negative percentage', () => {
    const pct = effectiveSavingsPct(mk({ savings: 10, upfront_cost: 1200, monthly_cost: 400, term: 1 }));
    expect(pct).not.toBeNull();
    expect(pct!).toBeLessThan(0);
    expect(pct!).toBeCloseTo(-17.65, 1);
  });

  test('undefined/null monthly_cost returns null (data not provided — cannot compute effective %)', () => {
    // monthly_cost null/undefined means the provider API did not return a monthly
    // recurring breakdown. Without it we cannot reconstruct on_demand_monthly,
    // so effectiveSavingsPct must return null rather than collapsing the
    // denominator to savings alone (which produced the misleading 100% rows in #252).
    expect(effectiveSavingsPct(mk({ savings: 100, upfront_cost: 0, monthly_cost: undefined, term: 1 }))).toBeNull();
    expect(effectiveSavingsPct(mk({ savings: 100, upfront_cost: 0, monthly_cost: null, term: 1 }))).toBeNull();
  });

  test('monthly_cost=0 (real all-upfront) is treated as known data, not missing', () => {
    // A literal 0 means the backend explicitly reported zero recurring cost
    // (e.g. an all-upfront commitment). effectiveSavingsPct SHOULD compute a
    // result in this case, because on_demand_monthly = 0 + savings + amortized.
    const pct = effectiveSavingsPct(mk({ savings: 100, upfront_cost: 0, monthly_cost: 0, term: 1 }));
    // onDemand = 0 + 100 + 0 = 100; effective = 100/100 * 100 = 100%
    expect(pct).not.toBeNull();
    expect(pct!).toBeCloseTo(100, 1);
  });
});

describe('Monthly Cost + Effective % column rendering', () => {
  beforeEach(() => {
    document.body.innerHTML = [
      '<div id="recommendations-tab" class="tab-content active">',
      '<div id="recommendations-summary"></div>',
      '<div id="recommendations-list"></div>',
      '</div>',
      '<div id="purchase-modal" class="hidden">',
      '<div id="purchase-details"></div>',
      '</div>',
    ].join('');
    jest.clearAllMocks();
    jest.useFakeTimers();
    window.alert = jest.fn();
  });

  afterEach(() => {
    jest.useRealTimers();
  });

  const baseRec = (overrides: Partial<LocalRecommendation> = {}): LocalRecommendation => ({
    id: 'test-rec',
    provider: 'aws' as const,
    service: 'ec2',
    resource_type: 't3.medium',
    region: 'us-east-1',
    count: 1,
    term: 1,
    savings: 100,
    upfront_cost: 0,
    monthly_cost: 50,
    ...overrides,
  } as unknown as LocalRecommendation);

  test('table header includes "Monthly Cost" and "Effective %" columns', async () => {
    (api.getRecommendations as jest.Mock).mockResolvedValue({
      summary: {},
      recommendations: [baseRec()],
      regions: [],
    });
    (state.getRecommendations as jest.Mock).mockReturnValue([baseRec()]);
    await loadRecommendations();

    const headers = Array.from(document.querySelectorAll('th')).map((th) => th.textContent ?? '');
    expect(headers.some((h) => h.includes('Monthly Cost'))).toBe(true);
    expect(headers.some((h) => h.includes('Effective %'))).toBe(true);
  });

  test('no-upfront row: Monthly Cost shows rec.monthly_cost, Effective % is positive', async () => {
    const rec = baseRec({ savings: 100, upfront_cost: 0, monthly_cost: 50, term: 1 });
    (api.getRecommendations as jest.Mock).mockResolvedValue({
      summary: {},
      recommendations: [rec],
      regions: [],
    });
    (state.getRecommendations as jest.Mock).mockReturnValue([rec]);
    await loadRecommendations();

    const cells = Array.from(document.querySelectorAll('tbody td')).map((td) => td.textContent ?? '');
    expect(cells.some((c) => c === '$50')).toBe(true);
    expect(cells.some((c) => c.includes('%') && !c.includes('em'))).toBe(true);
  });

  test('all-upfront row: Monthly Cost shows $0, Effective % accounts for amortization', async () => {
    const rec = baseRec({ savings: 50, upfront_cost: 600, monthly_cost: 0, term: 1 });
    (api.getRecommendations as jest.Mock).mockResolvedValue({
      summary: {},
      recommendations: [rec],
      regions: [],
    });
    (state.getRecommendations as jest.Mock).mockReturnValue([rec]);
    await loadRecommendations();

    const cells = Array.from(document.querySelectorAll('tbody td')).map((td) => td.textContent ?? '');
    expect(cells.some((c) => c === '$0')).toBe(true);
    expect(cells.some((c) => c === '0.0%')).toBe(true);
  });

  test('on_demand_monthly=0 row: Effective % renders as em-dash', async () => {
    const rec = baseRec({ savings: 0, upfront_cost: 0, monthly_cost: 0, term: 1 });
    (api.getRecommendations as jest.Mock).mockResolvedValue({
      summary: {},
      recommendations: [rec],
      regions: [],
    });
    (state.getRecommendations as jest.Mock).mockReturnValue([rec]);
    await loadRecommendations();

    const cells = Array.from(document.querySelectorAll('tbody td')).map((td) => td.textContent ?? '');
    expect(cells.some((c) => c === '—')).toBe(true);
  });

  test('negative-effective row: Effective % cell has effective-pct-negative class', async () => {
    const rec = baseRec({ savings: 10, upfront_cost: 1200, monthly_cost: 400, term: 1 });
    (api.getRecommendations as jest.Mock).mockResolvedValue({
      summary: {},
      recommendations: [rec],
      regions: [],
    });
    (state.getRecommendations as jest.Mock).mockReturnValue([rec]);
    await loadRecommendations();

    const negativeCells = document.querySelectorAll('tbody td.effective-pct-negative');
    expect(negativeCells.length).toBeGreaterThan(0);
  });

  test('sort header for monthly_cost is wired - clicking sets sort to monthly_cost', async () => {
    (api.getRecommendations as jest.Mock).mockResolvedValue({
      summary: {},
      recommendations: [baseRec()],
      regions: [],
    });
    (state.getRecommendations as jest.Mock).mockReturnValue([baseRec()]);
    await loadRecommendations();

    const header = document.querySelector<HTMLTableCellElement>('th[data-sort="monthly_cost"]');
    expect(header).not.toBeNull();
    header!.click();
    expect(state.setRecommendationsSort).toHaveBeenCalledWith(
      expect.objectContaining({ column: 'monthly_cost' }),
    );
  });

  test('sort header for effective_savings_pct is wired - clicking sets sort', async () => {
    (api.getRecommendations as jest.Mock).mockResolvedValue({
      summary: {},
      recommendations: [baseRec()],
      regions: [],
    });
    (state.getRecommendations as jest.Mock).mockReturnValue([baseRec()]);
    await loadRecommendations();

    const header = document.querySelector<HTMLTableCellElement>('th[data-sort="effective_savings_pct"]');
    expect(header).not.toBeNull();
    header!.click();
    expect(state.setRecommendationsSort).toHaveBeenCalledWith(
      expect.objectContaining({ column: 'effective_savings_pct' }),
    );
  });
});

// ---------------------------------------------------------------------------
// Issues #225 + #226: cell grouping with savings range and collapse/expand
// ---------------------------------------------------------------------------

/** Helper to build a minimal LocalRecommendation fixture. */
const mkRec = (overrides: Partial<LocalRecommendation> = {}): LocalRecommendation => ({
  id: 'rec-' + Math.random().toString(36).slice(2),
  provider: 'aws',
  service: 'ec2',
  resource_type: 'm5.large',
  region: 'us-east-1',
  count: 3,
  term: 1,
  payment: 'no-upfront',
  savings: 100,
  upfront_cost: 0,
  monthly_cost: 50,
  ...overrides,
} as unknown as LocalRecommendation);

/** Two recs sharing the same cell key (same provider/account/service/region/resource_type/engine). */
const sameCell = (savingsA: number, savingsB: number): LocalRecommendation[] => [
  mkRec({ id: 'a1', savings: savingsA, term: 1, payment: 'no-upfront', upfront_cost: 0 }),
  mkRec({ id: 'a2', savings: savingsB, term: 3, payment: 'all-upfront', upfront_cost: 1200 }),
];

describe('Issues #225 + #226: cell grouping with savings range and collapse/expand', () => {
  describe('groupRecsByCell (pure helper)', () => {
    test('groups two recs with the same cell key into one entry', () => {
      const recs = sameCell(80, 120);
      const groups = groupRecsByCell(recs);
      expect(groups.size).toBe(1);
      expect(groups.values().next().value).toHaveLength(2);
    });

    test('groups recs with different resource_type into separate cells', () => {
      const rec1 = mkRec({ resource_type: 'm5.large' });
      const rec2 = mkRec({ resource_type: 't3.medium' });
      const groups = groupRecsByCell([rec1, rec2]);
      expect(groups.size).toBe(2);
    });
  });

  describe('cellSummary (pure helper)', () => {
    test('computes min and max savings across variants', () => {
      const recs = sameCell(80, 120);
      const s = cellSummary(recs);
      expect(s.savingsMin).toBe(80);
      expect(s.savingsMax).toBe(120);
    });

    test('collapses to same value for a single-variant cell', () => {
      const rec = mkRec({ savings: 55, upfront_cost: 0, term: 1 });
      const s = cellSummary([rec]);
      expect(s.savingsMin).toBe(55);
      expect(s.savingsMax).toBe(55);
      expect(s.termMin).toBe(1);
      expect(s.termMax).toBe(1);
    });

    test('returns zeroed summary for empty input (defensive)', () => {
      const s = cellSummary([]);
      expect(s.savingsMin).toBe(0);
      expect(s.savingsMax).toBe(0);
    });
  });

  describe('pageLevelRange (pure helper)', () => {
    test('sums per-cell min and max across two cells', () => {
      const cell1 = sameCell(50, 100);  // min=50, max=100
      const cell2 = [
        mkRec({ id: 'b1', resource_type: 't3.small', savings: 30, term: 1, payment: 'no-upfront', upfront_cost: 0 }),
        mkRec({ id: 'b2', resource_type: 't3.small', savings: 70, term: 3, payment: 'all-upfront', upfront_cost: 600 }),
      ];
      const groups = groupRecsByCell([...cell1, ...cell2]);
      const plr = pageLevelRange(groups);
      expect(plr.cellCount).toBe(2);
      expect(plr.savingsMin).toBe(80);   // 50 + 30
      expect(plr.savingsMax).toBe(170);  // 100 + 70
    });

    test('returns cellCount=0 and savings=0 for empty groups', () => {
      const plr = pageLevelRange(new Map());
      expect(plr.cellCount).toBe(0);
      expect(plr.savingsMin).toBe(0);
      expect(plr.savingsMax).toBe(0);
    });
  });

  describe('DOM rendering (cell grouping integrated)', () => {
    beforeEach(() => {
      document.body.innerHTML = [
        '<div id="recommendations-tab" class="tab-content active">',
        '<div id="recommendations-summary"></div>',
        '<div id="recommendations-list"></div>',
        '</div>',
        '<div id="purchase-modal" class="hidden">',
        '<div id="purchase-details"></div>',
        '</div>',
      ].join('');
      jest.clearAllMocks();
      jest.useFakeTimers();
      window.alert = jest.fn();
      // Reset cell expand state so tests don't share module-level expandedCells.
      resetExpandedCells();
    });

    afterEach(() => {
      jest.useRealTimers();
    });

    const multiVariantRecs = (): LocalRecommendation[] => [
      mkRec({ id: 'mv-1', savings: 80,  term: 1, payment: 'no-upfront',   upfront_cost: 0 }),
      mkRec({ id: 'mv-2', savings: 120, term: 3, payment: 'all-upfront',  upfront_cost: 1200 }),
    ];

    test('multi-variant cell renders a summary row, not two flat variant rows by default', async () => {
      const recs = multiVariantRecs();
      (api.getRecommendations as jest.Mock).mockResolvedValue({ summary: {}, recommendations: recs });
      (state.getRecommendations as jest.Mock).mockReturnValue(recs);
      await loadRecommendations();

      // Summary row should be present
      const summaryRow = document.querySelector('.rec-cell-summary-row');
      expect(summaryRow).not.toBeNull();

      // Variant rows should NOT be rendered (collapsed by default)
      const variantRows = document.querySelectorAll('.rec-variant-row');
      expect(variantRows.length).toBe(0);
    });

    test('clicking chevron expands the cell and shows variant rows', async () => {
      const recs = multiVariantRecs();
      (api.getRecommendations as jest.Mock).mockResolvedValue({ summary: {}, recommendations: recs });
      (state.getRecommendations as jest.Mock).mockReturnValue(recs);
      await loadRecommendations();

      const chevron = document.querySelector<HTMLButtonElement>('.rec-cell-chevron');
      expect(chevron).not.toBeNull();
      chevron!.click();

      // After expand: variant rows should appear
      const variantRows = document.querySelectorAll('.rec-variant-row');
      expect(variantRows.length).toBe(2);
    });

    test('page-level range banner appears when multi-variant cells exist', async () => {
      const recs = multiVariantRecs();
      (api.getRecommendations as jest.Mock).mockResolvedValue({ summary: {}, recommendations: recs });
      (state.getRecommendations as jest.Mock).mockReturnValue(recs);
      await loadRecommendations();

      const banner = document.querySelector('.rec-range-banner');
      expect(banner).not.toBeNull();
      expect(banner!.textContent).toMatch(/Recommended range/);
    });

    test('single-variant cell renders a flat row, no summary row', async () => {
      const rec = mkRec({ id: 'solo', savings: 60 });
      (api.getRecommendations as jest.Mock).mockResolvedValue({ summary: {}, recommendations: [rec] });
      (state.getRecommendations as jest.Mock).mockReturnValue([rec]);
      await loadRecommendations();

      const summaryRow = document.querySelector('.rec-cell-summary-row');
      expect(summaryRow).toBeNull();

      // The flat row should exist as a regular recommendation-row
      const rows = document.querySelectorAll('tr.recommendation-row');
      expect(rows.length).toBe(1);
    });

    test('Expand all button appears in filter-status bar for multi-variant cells', async () => {
      const recs = multiVariantRecs();
      (api.getRecommendations as jest.Mock).mockResolvedValue({ summary: {}, recommendations: recs });
      (state.getRecommendations as jest.Mock).mockReturnValue(recs);
      await loadRecommendations();

      const expandAllBtn = document.querySelector('.expand-all-toggle');
      expect(expandAllBtn).not.toBeNull();
      expect(expandAllBtn!.textContent).toMatch(/Expand all/);
    });

    test('Expand all expands all multi-variant cells', async () => {
      const recs = multiVariantRecs();
      (api.getRecommendations as jest.Mock).mockResolvedValue({ summary: {}, recommendations: recs });
      (state.getRecommendations as jest.Mock).mockReturnValue(recs);
      await loadRecommendations();

      const expandAllBtn = document.querySelector<HTMLButtonElement>('.expand-all-toggle');
      expect(expandAllBtn).not.toBeNull();
      expandAllBtn!.click();

      // After expand all: variant rows should be visible
      const variantRows = document.querySelectorAll('.rec-variant-row');
      expect(variantRows.length).toBe(2);
    });
  });
});
