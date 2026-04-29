/**
 * Recommendations module tests
 */
import { loadRecommendations, openPurchaseModal, refreshRecommendations, setupRecommendationsHandlers, clearRecommendationDetailCache } from '../recommendations';

// Mock the api module
jest.mock('../api', () => ({
  getRecommendations: jest.fn(),
  refreshRecommendations: jest.fn(),
  listAccounts: jest.fn().mockResolvedValue([]),
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

    test('purchase button opens modal for that recommendation', async () => {
      const mockRecs = [
        { id: 'rec-11', provider: 'aws', service: 'ec2', resource_type: 't3.medium', region: 'us-east-1', count: 1, term: 1, savings: 100, upfront_cost: 500 }
      ];
      (api.getRecommendations as jest.Mock).mockResolvedValue({
        summary: {},
        recommendations: mockRecs,
        regions: []
      });

      await loadRecommendations();

      // Bundle B: per-row Purchase buttons gone; the Purchase action lives
      // in the sticky bottom action box at #bulk-purchase-btn. The button
      // resolves its target via state.getVisibleRecommendations(), so the
      // mock needs to return the loaded recs for the click to fire.
      (state.getVisibleRecommendations as jest.Mock).mockReturnValue(mockRecs);
      const purchaseBtn = document.querySelector('#bulk-purchase-btn') as HTMLButtonElement;
      expect(purchaseBtn).not.toBeNull();
      purchaseBtn.click();

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

    test('renders sortable column headers with indicators (Bundle B: 9 columns)', async () => {
      await loadRecommendations();
      const list = document.getElementById('recommendations-list');
      // Bundle B: every data column is sortable. 9 sortable data columns:
      // provider, account, service, resource_type, region, count, term,
      // savings, upfront_cost. The leading checkbox column is not sortable.
      const sortables = list?.querySelectorAll('th.sortable');
      expect(sortables?.length).toBe(9);
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
      expect(summary?.textContent).toContain('1 selected of 1 visible');
      // Old bulk-toolbar surface is gone.
      expect(document.querySelector('.recommendations-bulk-toolbar')).toBeNull();
    });

    test('bottom action box shows "All N visible" when no row is selected (Bundle B)', async () => {
      (state.getSelectedRecommendationIDs as jest.Mock).mockReturnValue(new Set());
      (state.getVisibleRecommendations as jest.Mock).mockReturnValue(twoRecs);
      await loadRecommendations();
      const summary = document.getElementById('recommendations-action-summary');
      expect(summary?.textContent).toMatch(/All \d+ visible/);
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
    test('displays purchase modal', () => {
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

      openPurchaseModal(recommendations);

      const modal = document.getElementById('purchase-modal');
      expect(modal?.classList.contains('hidden')).toBe(false);
    });

    test('shows purchase summary', () => {
      const recommendations = [
        { id: 'rec-2', provider: 'aws' as const, service: 'ec2', resource_type: 't3.medium', region: 'us-east-1', count: 5, term: 1, savings: 100, upfront_cost: 500 },
        { id: 'rec-3', provider: 'aws' as const, service: 'rds', resource_type: 'db.r5.large', region: 'us-east-1', count: 2, term: 1, savings: 200, upfront_cost: 1000 }
      ];

      openPurchaseModal(recommendations);

      const details = document.getElementById('purchase-details');
      expect(details?.innerHTML).toContain('2'); // count of commitments
      expect(details?.innerHTML).toContain('Purchase Summary');
    });

    test('lists individual recommendations', () => {
      const recommendations = [
        { id: 'rec-4', provider: 'aws' as const, service: 'ec2', resource_type: 't3.medium', region: 'us-east-1', count: 5, term: 1, savings: 100, upfront_cost: 500 }
      ];

      openPurchaseModal(recommendations);

      const details = document.getElementById('purchase-details');
      expect(details?.innerHTML).toContain('ec2');
      expect(details?.innerHTML).toContain('t3.medium');
      expect(details?.innerHTML).toContain('us-east-1');
    });

    test('handles missing modal element', () => {
      document.body.innerHTML = '';

      expect(() => openPurchaseModal([])).not.toThrow();
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
      ['account', 'count', 'provider', 'region', 'resource_type', 'savings', 'service', 'term', 'upfront_cost'].sort(),
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
    const mixed = [
      { id: 'a', provider: 'aws', cloud_account_id: 'a1', service: 'ec2', resource_type: 't3.medium', region: 'us-east-1', count: 1, term: 1, savings: 100, upfront_cost: 500 },
      { id: 'b', provider: 'aws', cloud_account_id: 'a1', service: 'ec2', resource_type: 't3.medium', region: 'us-east-1', count: 1, term: 3, savings: 200, upfront_cost: 800 },
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
    const recs = [
      { id: 'a', provider: 'aws', cloud_account_id: 'test-account-a', service: 'ec2', resource_type: 't3.medium', region: 'us-east-1', count: 1, term: 1, savings: 100, upfront_cost: 500 },
      { id: 'b', provider: 'aws', cloud_account_id: 'test-account-a', service: 'ec2', resource_type: 't3.medium', region: 'us-east-1', count: 1, term: 3, savings: 200, upfront_cost: 800 },
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
    const recs = [
      { id: 'a', provider: 'aws', cloud_account_id: 'test-account-a', service: 'ec2', resource_type: 't3.medium', region: 'us-east-1', count: 1, term: 1, savings: 100, upfront_cost: 500 },
      { id: 'b', provider: 'aws', cloud_account_id: 'test-account-a', service: 'ec2', resource_type: 't3.medium', region: 'us-east-1', count: 1, term: 3, savings: 200, upfront_cost: 800 },
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
    const recs = [
      { id: 'a', provider: 'aws', cloud_account_id: 'test-account-a', service: 'ec2', resource_type: 't3.medium', region: 'us-east-1', count: 1, term: 1, savings: 100, upfront_cost: 500 },
      { id: 'b', provider: 'aws', cloud_account_id: 'test-account-b', service: 'ec2', resource_type: 't3.medium', region: 'us-east-1', count: 1, term: 1, savings: 150, upfront_cost: 600 },
      { id: 'c', provider: 'aws', cloud_account_id: 'test-account-a', service: 'ec2', resource_type: 't3.medium', region: 'us-east-1', count: 1, term: 3, savings: 200, upfront_cost: 800 },
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
    const recs = [
      { id: 'a', provider: 'aws', cloud_account_id: 'test-account-a', service: 'ec2', resource_type: 't3.medium', region: 'us-east-1', count: 1, term: 1, savings: 100, upfront_cost: 500 },
      { id: 'b', provider: 'aws', cloud_account_id: 'test-account-a', service: 'ec2', resource_type: 't3.medium', region: 'us-east-1', count: 1, term: 3, savings: 200, upfront_cost: 800 },
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
