/**
 * Recommendations module tests
 */
import { loadRecommendations, openPurchaseModal, refreshRecommendations, setupRecommendationsHandlers, clearRecommendationDetailCache } from '../recommendations';

// Mock the api module
jest.mock('../api', () => ({
  getRecommendations: jest.fn(),
  refreshRecommendations: jest.fn(),
  listAccounts: jest.fn().mockResolvedValue([])
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
      <select id="service-filter">
        <option value="">All Services</option>
        <option value="ec2">EC2</option>
      </select>
      <select id="region-filter">
        <option value="">All Regions</option>
        <option value="us-east-1">us-east-1</option>
        <option value="us-west-2">us-west-2</option>
      </select>
      <input type="number" id="min-savings-filter" value="">
      <div id="recommendations-summary"></div>
      <div id="recommendations-list"></div>
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
    test('fetches recommendations with filters', async () => {
      (api.getRecommendations as jest.Mock).mockResolvedValue({
        summary: {},
        recommendations: [],
        regions: []
      });

      const serviceFilter = document.getElementById('service-filter') as HTMLSelectElement;
      const regionFilter = document.getElementById('region-filter') as HTMLSelectElement;
      const minSavingsFilter = document.getElementById('min-savings-filter') as HTMLInputElement;

      serviceFilter.value = 'ec2';
      regionFilter.value = 'us-east-1';
      minSavingsFilter.value = '100';

      await loadRecommendations();

      expect(api.getRecommendations).toHaveBeenCalledWith({
        provider: 'all',
        service: 'ec2',
        region: 'us-east-1',
        minSavings: 100
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

    test('shows empty message when no recommendations', async () => {
      (api.getRecommendations as jest.Mock).mockResolvedValue({
        summary: {},
        recommendations: [],
        regions: []
      });

      await loadRecommendations();

      const list = document.getElementById('recommendations-list');
      expect(list?.innerHTML).toContain('No recommendations found');
    });

    test('populates region filter', async () => {
      (api.getRecommendations as jest.Mock).mockResolvedValue({
        summary: {},
        recommendations: [],
        regions: ['us-east-1', 'us-west-2', 'eu-west-1']
      });

      await loadRecommendations();

      const regionFilter = document.getElementById('region-filter') as HTMLSelectElement;
      expect(regionFilter.options.length).toBe(4); // All Regions + 3 regions
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

      // Per-row Purchase button removed in Commit 3 of bulk-purchase-
      // with-grace. Use the bulk Purchase button on the top toolbar
      // instead — a single visible rec purchases that one.
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

    test('renders sortable column headers with indicators', async () => {
      await loadRecommendations();
      const list = document.getElementById('recommendations-list');
      // Four sortable columns: count, term, savings (default), upfront_cost.
      const sortables = list?.querySelectorAll('th.sortable');
      expect(sortables?.length).toBe(4);
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

    test('bulk toolbar appears when at least one row is selected', async () => {
      (api.getRecommendations as jest.Mock).mockResolvedValue({
        summary: {},
        recommendations: [
          { id: 'rec-bt', provider: 'aws', service: 'ec2', resource_type: 't3.medium', region: 'us-east-1', count: 1, term: 1, savings: 100, upfront_cost: 500 }
        ],
        regions: []
      });
      (state.getSelectedRecommendationIDs as jest.Mock).mockReturnValue(new Set(['rec-bt']));

      await loadRecommendations();

      const toolbar = document.querySelector('.recommendations-bulk-toolbar');
      expect(toolbar).not.toBeNull();
    });

    test('bulk toolbar is absent when no row is selected', async () => {
      (state.getSelectedRecommendationIDs as jest.Mock).mockReturnValue(new Set());
      await loadRecommendations();
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

    test('sets up provider filter change handler', () => {
      setupRecommendationsHandlers();

      const providerFilter = document.getElementById('recommendations-provider-filter') as HTMLSelectElement;
      providerFilter.value = 'aws';
      providerFilter.dispatchEvent(new Event('change'));

      expect(state.setCurrentProvider).toHaveBeenCalledWith('aws');
    });

    test('provider filter updates service filter visibility', () => {
      setupRecommendationsHandlers();

      const providerFilter = document.getElementById('recommendations-provider-filter') as HTMLSelectElement;
      const serviceFilter = document.getElementById('service-filter') as HTMLSelectElement;
      const awsGroup = serviceFilter.querySelector('optgroup[label="AWS Services"]') as HTMLOptGroupElement;
      const azureGroup = serviceFilter.querySelector('optgroup[label="Azure Services"]') as HTMLOptGroupElement;

      providerFilter.value = 'aws';
      providerFilter.dispatchEvent(new Event('change'));

      expect(awsGroup.classList.contains('hidden')).toBe(false);
      expect(azureGroup.classList.contains('hidden')).toBe(true);
    });

    test('shows all service groups when "all" selected', () => {
      setupRecommendationsHandlers();

      const providerFilter = document.getElementById('recommendations-provider-filter') as HTMLSelectElement;
      const serviceFilter = document.getElementById('service-filter') as HTMLSelectElement;
      const awsGroup = serviceFilter.querySelector('optgroup[label="AWS Services"]') as HTMLOptGroupElement;
      const azureGroup = serviceFilter.querySelector('optgroup[label="Azure Services"]') as HTMLOptGroupElement;

      providerFilter.value = '';
      providerFilter.dispatchEvent(new Event('change'));

      expect(awsGroup.classList.contains('hidden')).toBe(false);
      expect(azureGroup.classList.contains('hidden')).toBe(false);
    });

    test('resets service filter when changing provider', () => {
      setupRecommendationsHandlers();

      const providerFilter = document.getElementById('recommendations-provider-filter') as HTMLSelectElement;
      const serviceFilter = document.getElementById('service-filter') as HTMLSelectElement;

      serviceFilter.value = 'ec2';
      providerFilter.value = 'azure';
      providerFilter.dispatchEvent(new Event('change'));

      expect(serviceFilter.value).toBe('');
    });

    test('sets up service filter change handler', () => {
      setupRecommendationsHandlers();

      const serviceFilter = document.getElementById('service-filter') as HTMLSelectElement;
      expect(serviceFilter).toBeTruthy();
    });

    test('sets up region filter change handler', () => {
      setupRecommendationsHandlers();

      const regionFilter = document.getElementById('region-filter') as HTMLSelectElement;
      expect(regionFilter).toBeTruthy();
    });

    test('sets up min savings filter change handler', () => {
      setupRecommendationsHandlers();

      const minSavingsFilter = document.getElementById('min-savings-filter') as HTMLInputElement;
      expect(minSavingsFilter).toBeTruthy();
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
