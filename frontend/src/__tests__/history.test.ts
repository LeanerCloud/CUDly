/**
 * History module tests
 */
import { initHistoryDateRange, viewPlanHistory, loadHistory } from '../history';

// Mock the dependent modules
jest.mock('../api', () => ({
  getHistory: jest.fn()
}));

jest.mock('../navigation', () => ({
  switchTab: jest.fn()
}));

jest.mock('../utils', () => ({
  formatCurrency: jest.fn((val) => `$${val || 0}`),
  formatDate: jest.fn((val) => val ? new Date(val).toLocaleDateString() : ''),
  formatTerm: jest.fn((years) => years == null ? '' : `${years} Year${years === 1 ? '' : 's'}`),
  escapeHtml: jest.fn((str) => str || ''),
  populateAccountFilter: jest.fn(() => Promise.resolve())
}));

import * as api from '../api';
import { switchTab } from '../navigation';

describe('History Module', () => {
  beforeEach(() => {
    // Reset DOM
    document.body.innerHTML = `
      <input type="date" id="history-start">
      <input type="date" id="history-end">
      <select id="history-provider-filter">
        <option value="">All Providers</option>
        <option value="aws">AWS</option>
        <option value="azure">Azure</option>
        <option value="gcp">GCP</option>
      </select>
      <div id="history-summary"></div>
      <div id="history-list"></div>
    `;

    jest.clearAllMocks();
  });

  describe('initHistoryDateRange', () => {
    test('sets default date range to 7 days', () => {
      initHistoryDateRange();

      const startInput = document.getElementById('history-start') as HTMLInputElement;
      const endInput = document.getElementById('history-end') as HTMLInputElement;

      expect(startInput.value).toBeTruthy();
      expect(endInput.value).toBeTruthy();

      // End date should be today in UTC (code uses toISOString which is UTC)
      const today = new Date();
      const todayUTC = today.toISOString().split('T')[0] || '';
      expect(endInput.value).toBe(todayUTC);

      // Start date should be 7 days ago (UTC). Purchase events are a log
      // view — recent activity is what matters. Savings History (the trend
      // view) defaults to 90 days separately via #savings-period in
      // index.html.
      const expectedStart = new Date();
      expectedStart.setDate(expectedStart.getDate() - 7);
      const expectedStartUTC = expectedStart.toISOString().split('T')[0] || '';
      expect(startInput.value).toBe(expectedStartUTC);
    });

    test('does not overwrite existing values', () => {
      const startInput = document.getElementById('history-start') as HTMLInputElement;
      const endInput = document.getElementById('history-end') as HTMLInputElement;

      startInput.value = '2024-01-01';
      endInput.value = '2024-02-01';

      initHistoryDateRange();

      expect(startInput.value).toBe('2024-01-01');
      expect(endInput.value).toBe('2024-02-01');
    });

    test('handles missing elements gracefully', () => {
      document.body.innerHTML = '';

      expect(() => initHistoryDateRange()).not.toThrow();
    });
  });

  describe('viewPlanHistory', () => {
    test('switches to history tab', async () => {
      (api.getHistory as jest.Mock).mockResolvedValue({
        summary: {},
        purchases: []
      });

      await viewPlanHistory('plan-123');

      expect(switchTab).toHaveBeenCalledWith('history');
    });

    test('calls getHistory with planId filter', async () => {
      (api.getHistory as jest.Mock).mockResolvedValue({
        summary: {},
        purchases: []
      });

      await viewPlanHistory('plan-123');

      expect(api.getHistory).toHaveBeenCalledWith({ planId: 'plan-123' });
    });

    test('renders history summary', async () => {
      (api.getHistory as jest.Mock).mockResolvedValue({
        summary: {
          total_purchases: 5,
          total_upfront: 1000,
          total_monthly_savings: 200,
          total_annual_savings: 2400
        },
        purchases: []
      });

      await viewPlanHistory('plan-123');

      const summary = document.getElementById('history-summary');
      expect(summary?.innerHTML).toContain('Total Purchases');
      expect(summary?.innerHTML).toContain('5');
    });

    test('handles API errors gracefully', async () => {
      (api.getHistory as jest.Mock).mockRejectedValue(new Error('API Error'));
      console.error = jest.fn();

      await viewPlanHistory('plan-123');

      expect(console.error).toHaveBeenCalledWith('Failed to load plan history:', expect.any(Error));
    });

    test('snaps From/To inputs to min/max purchase timestamps after fetch', async () => {
      // Reproduces the scenario from PR #139's CodeRabbit nitpick: the
      // plan-history endpoint returns the full history regardless of
      // date range, so the inputs should reflect the rendered data, not
      // the tab's generic 7-day default.
      (api.getHistory as jest.Mock).mockResolvedValue({
        summary: {},
        purchases: [
          { timestamp: '2025-12-15T10:00:00Z' },
          { timestamp: '2026-02-04T10:00:00Z' },
          { timestamp: '2026-01-20T10:00:00Z' },
        ],
      });

      const startInput = document.getElementById('history-start') as HTMLInputElement;
      const endInput = document.getElementById('history-end') as HTMLInputElement;
      // Pre-seed with the tab default so we can prove the snap overwrites.
      startInput.value = '2026-04-20';
      endInput.value = '2026-04-27';

      await viewPlanHistory('plan-123');

      expect(startInput.value).toBe('2025-12-15');
      expect(endInput.value).toBe('2026-02-04');
    });

    test('leaves From/To inputs untouched when the plan has no purchases', async () => {
      // No data → no honest range to display. Clobbering the inputs with
      // `today` would lie to the user; preserve whatever was there.
      (api.getHistory as jest.Mock).mockResolvedValue({
        summary: {},
        purchases: [],
      });

      const startInput = document.getElementById('history-start') as HTMLInputElement;
      const endInput = document.getElementById('history-end') as HTMLInputElement;
      startInput.value = '2026-04-20';
      endInput.value = '2026-04-27';

      await viewPlanHistory('plan-123');

      expect(startInput.value).toBe('2026-04-20');
      expect(endInput.value).toBe('2026-04-27');
    });
  });

  describe('loadHistory', () => {
    test('loads history with filters from form', async () => {
      (api.getHistory as jest.Mock).mockResolvedValue({
        summary: {},
        purchases: []
      });

      const startInput = document.getElementById('history-start') as HTMLInputElement;
      const endInput = document.getElementById('history-end') as HTMLInputElement;
      const providerFilter = document.getElementById('history-provider-filter') as HTMLSelectElement;

      startInput.value = '2024-01-01';
      endInput.value = '2024-03-01';
      providerFilter.value = 'aws';

      await loadHistory();

      expect(api.getHistory).toHaveBeenCalledWith({
        start: '2024-01-01',
        end: '2024-03-01',
        provider: 'aws'
      });
    });

    test('renders purchase list', async () => {
      (api.getHistory as jest.Mock).mockResolvedValue({
        summary: {},
        purchases: [
          {
            purchase_date: '2024-01-15',
            provider: 'aws',
            service: 'ec2',
            resource_type: 't3.medium',
            region: 'us-east-1',
            count: 5,
            term: 1,
            upfront_cost: 500,
            monthly_savings: 100,
            plan_name: 'Test Plan'
          }
        ]
      });

      await loadHistory();

      const list = document.getElementById('history-list');
      expect(list?.innerHTML).toContain('table');
      expect(list?.innerHTML).toContain('ec2');
      expect(list?.innerHTML).toContain('us-east-1');
    });

    test('shows empty message when no purchases', async () => {
      (api.getHistory as jest.Mock).mockResolvedValue({
        summary: {},
        purchases: []
      });

      await loadHistory();

      const list = document.getElementById('history-list');
      expect(list?.innerHTML).toContain('No purchase history found');
    });

    test('shows error on API failure', async () => {
      (api.getHistory as jest.Mock).mockRejectedValue(new Error('API Error'));
      console.error = jest.fn();

      await loadHistory();

      const list = document.getElementById('history-list');
      expect(list?.innerHTML).toContain('Failed to load history');
    });

    test('handles empty provider filter', async () => {
      (api.getHistory as jest.Mock).mockResolvedValue({
        summary: {},
        purchases: []
      });

      const providerFilter = document.getElementById('history-provider-filter') as HTMLSelectElement;
      providerFilter.value = '';

      await loadHistory();

      // Empty strings are passed when inputs have no value
      expect(api.getHistory).toHaveBeenCalledWith({
        start: '',
        end: '',
        provider: undefined
      });
    });
  });

  // Issue #186 (History slice): provider+account dropdowns must call
  // loadHistory() on `change`. Previously only the date-range Apply
  // button triggered a re-query; users would change the provider, see
  // no update, and be confused.
  describe('Issue #186: provider+account change wiring triggers loadHistory', () => {
    let setupHistoryHandlers: typeof import('../history').setupHistoryHandlers;

    beforeEach(async () => {
      setupHistoryHandlers = (await import('../history')).setupHistoryHandlers;
      // Build the test DOM via createElement to avoid innerHTML.
      document.body.replaceChildren();
      const providerSel = document.createElement('select');
      providerSel.id = 'history-provider-filter';
      for (const [v, t] of [['', 'All'], ['aws', 'AWS'], ['azure', 'Azure']] as const) {
        const opt = document.createElement('option');
        opt.value = v; opt.textContent = t;
        providerSel.appendChild(opt);
      }
      document.body.appendChild(providerSel);
      const acctSel = document.createElement('select');
      acctSel.id = 'history-account-filter';
      const allOpt = document.createElement('option');
      allOpt.value = ''; allOpt.textContent = '(All accounts)';
      acctSel.appendChild(allOpt);
      const acct1 = document.createElement('option');
      acct1.value = 'acct-1'; acct1.textContent = 'acct-1';
      acctSel.appendChild(acct1);
      document.body.appendChild(acctSel);
      const startIn = document.createElement('input');
      startIn.id = 'history-start'; startIn.type = 'date';
      document.body.appendChild(startIn);
      const endIn = document.createElement('input');
      endIn.id = 'history-end'; endIn.type = 'date';
      document.body.appendChild(endIn);
      const summaryDiv = document.createElement('div');
      summaryDiv.id = 'history-summary';
      document.body.appendChild(summaryDiv);
      const listDiv = document.createElement('div');
      listDiv.id = 'history-list';
      document.body.appendChild(listDiv);

      (api.getHistory as jest.Mock).mockResolvedValue({ summary: {}, purchases: [] });
    });

    test('account dropdown change triggers loadHistory', async () => {
      setupHistoryHandlers();
      (api.getHistory as jest.Mock).mockClear();

      const acctSel = document.getElementById('history-account-filter') as HTMLSelectElement;
      acctSel.value = 'acct-1';
      acctSel.dispatchEvent(new Event('change'));
      await new Promise(r => setTimeout(r, 0));

      expect(api.getHistory).toHaveBeenCalled();
      const args = (api.getHistory as jest.Mock).mock.calls[0][0];
      expect(args.account_ids).toEqual(['acct-1']);
    });

    test('provider dropdown change awaits populate then triggers loadHistory', async () => {
      const utils = await import('../utils');
      // First call (init) resolves immediately; second (provider-change) blocks.
      let resolvePopulate: (() => void) | undefined;
      const populateBlocker = new Promise<void>((resolve) => { resolvePopulate = resolve; });
      (utils.populateAccountFilter as jest.Mock)
        .mockImplementationOnce(() => Promise.resolve())
        .mockImplementationOnce(() => populateBlocker);

      setupHistoryHandlers();
      await new Promise(r => setTimeout(r, 0));
      (api.getHistory as jest.Mock).mockClear();

      const providerSel = document.getElementById('history-provider-filter') as HTMLSelectElement;
      providerSel.value = 'azure';
      providerSel.dispatchEvent(new Event('change'));

      // Yield once — populate is pending; loadHistory must NOT have run.
      await new Promise(r => setTimeout(r, 0));
      expect((api.getHistory as jest.Mock).mock.calls.length).toBe(0);

      // Resolve populate; loadHistory runs after.
      resolvePopulate!();
      await new Promise(r => setTimeout(r, 0));
      await new Promise(r => setTimeout(r, 0));
      expect((api.getHistory as jest.Mock).mock.calls.length).toBeGreaterThan(0);
    });
  });
});
