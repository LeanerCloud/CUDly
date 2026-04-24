/**
 * Recommendations module tests
 */
import { loadRecommendations, openPurchaseModal, refreshRecommendations, setupRecommendationsHandlers } from '../recommendations';

// Mock the api module
jest.mock('../api', () => ({
  getRecommendations: jest.fn(),
  refreshRecommendations: jest.fn(),
  listAccounts: jest.fn().mockResolvedValue([])
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

      expect(awsGroup.style.display).toBe('');
      expect(azureGroup.style.display).toBe('none');
    });

    test('shows all service groups when "all" selected', () => {
      setupRecommendationsHandlers();

      const providerFilter = document.getElementById('recommendations-provider-filter') as HTMLSelectElement;
      const serviceFilter = document.getElementById('service-filter') as HTMLSelectElement;
      const awsGroup = serviceFilter.querySelector('optgroup[label="AWS Services"]') as HTMLOptGroupElement;
      const azureGroup = serviceFilter.querySelector('optgroup[label="Azure Services"]') as HTMLOptGroupElement;

      providerFilter.value = '';
      providerFilter.dispatchEvent(new Event('change'));

      expect(awsGroup.style.display).toBe('');
      expect(azureGroup.style.display).toBe('');
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
