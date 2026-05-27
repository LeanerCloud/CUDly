/**
 * History module tests
 */
import { initHistoryDateRange, viewPlanHistory, loadHistory, setupHistoryHandlers } from '../history';

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

// Issue #344 T2: history.ts reads provider/account from the global
// topbar filter via state — mock the module so each test can pre-set
// the values it wants `loadHistory` to read.
const mockGetCurrentProvider = jest.fn<string, []>().mockReturnValue('all');
const mockGetCurrentAccountIDs = jest.fn<string[], []>().mockReturnValue([]);
jest.mock('../state', () => ({
  getCurrentUser: jest.fn(),
  getCurrentProvider: () => mockGetCurrentProvider(),
  setCurrentProvider: jest.fn(),
  getCurrentAccountIDs: () => mockGetCurrentAccountIDs(),
  setCurrentAccountIDs: jest.fn(),
  subscribeProvider: jest.fn().mockReturnValue(() => {}),
  subscribeAccount: jest.fn().mockReturnValue(() => {}),
}));

import * as api from '../api';
import { switchTab } from '../navigation';

describe('History Module', () => {
  beforeEach(() => {
    // Reset DOM. Issue #344 T2: provider/account filters now live in
    // the global topbar (state-driven); only the per-section controls
    // remain on the history page.
    document.body.innerHTML = `
      <input type="date" id="history-start">
      <input type="date" id="history-end">
      <div id="history-summary"></div>
      <div id="history-list"></div>
      <div id="purchases-approval-queue"></div>
    `;

    jest.clearAllMocks();
    mockGetCurrentProvider.mockReturnValue('all');
    mockGetCurrentAccountIDs.mockReturnValue([]);
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

      startInput.value = '2024-01-01';
      endInput.value = '2024-03-01';
      // Provider scope now comes from the global topbar (state.ts).
      mockGetCurrentProvider.mockReturnValue('aws');

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

    // Issue #621: an approved/running/paused execution is in-flight — its
    // synchronous AWS purchase may have been interrupted (Lambda timeout /
    // crash). It MUST render as "In Progress", never the green "Completed"
    // badge, or the user could think the purchase finished and re-approve it
    // (double-spend). Pre-fix these statuses fell through to the Completed
    // default in statusBadgeHTML.
    test('renders approved/running/paused as In Progress, not Completed', async () => {
      (api.getHistory as jest.Mock).mockResolvedValue({
        summary: {},
        purchases: [
          { purchase_id: 'appr-1', status: 'approved', provider: 'aws', region: 'us-east-1' },
          { purchase_id: 'run-1', status: 'running', provider: 'aws', region: 'us-east-1' },
          { purchase_id: 'paus-1', status: 'paused', provider: 'aws', region: 'us-east-1' },
        ],
      });

      await loadHistory();

      const list = document.getElementById('history-list');
      const html = list?.innerHTML || '';
      const inProgressBadges = (html.match(/In Progress/g) || []).length;
      expect(inProgressBadges).toBe(3);
      expect(html).not.toContain('>Completed<');
    });

    // Issue #706: partially_completed rows counted in the Completed chip bucket
    // but excluded from the chip filter. Clicking "Completed" must show ALL rows
    // that the chip counted -- including partially_completed ones.
    test('Completed chip shows partially_completed rows (issue #706)', async () => {
      (api.getHistory as jest.Mock).mockResolvedValue({
        summary: {},
        purchases: [
          { purchase_id: 'comp-1', status: 'completed', provider: 'aws', region: 'us-east-1' },
          { purchase_id: 'part-1', status: 'partially_completed', provider: 'aws', region: 'us-east-1' },
          { purchase_id: 'fail-1', status: 'failed', provider: 'aws', region: 'us-east-1' },
        ],
      });

      await loadHistory();

      const list = document.getElementById('history-list');

      // Before clicking: verify the Completed chip counts 2 (comp-1 + part-1).
      const completedChip = list?.querySelector<HTMLButtonElement>('[data-history-status="completed"]');
      expect(completedChip?.textContent).toContain('2');

      // Click the Completed chip -- triggers re-render via renderHistoryList.
      completedChip?.click();

      const html = list?.innerHTML || '';
      // Both the clean success and the partial success must be visible.
      expect(html).toContain('comp-1');
      expect(html).toContain('part-1');
      // The failed row must be hidden.
      expect(html).not.toContain('fail-1');
    });

    test('handles empty provider filter', async () => {
      (api.getHistory as jest.Mock).mockResolvedValue({
        summary: {},
        purchases: []
      });

      // 'all' is the topbar-chip "All providers" value, which loadHistory
      // translates to `provider: undefined` (no provider scope on the API
      // call). Same semantic as the old empty-string select value.
      mockGetCurrentProvider.mockReturnValue('all');

      await loadHistory();

      // Empty strings are passed when inputs have no value
      expect(api.getHistory).toHaveBeenCalledWith({
        start: '',
        end: '',
        provider: undefined
      });
    });
  });

  // Issue #701: setupHistoryHandlers must subscribe to the global topbar
  // provider/account filter chips so the Purchase History table and
  // Approval Queue reload when a chip changes. PR #716 fixed the backend
  // filter params but the frontend subscription was never registered in
  // app.ts -- adding this suite guards against a regression.
  describe('setupHistoryHandlers (issue #701)', () => {
    beforeEach(() => {
      (api.getHistory as jest.Mock).mockResolvedValue({ summary: {}, purchases: [] });
    });

    test('registers a callback with state.subscribeProvider', () => {
      setupHistoryHandlers();
      expect((require('../state').subscribeProvider as jest.Mock)).toHaveBeenCalledTimes(1);
    });

    test('registers a callback with state.subscribeAccount', () => {
      setupHistoryHandlers();
      expect((require('../state').subscribeAccount as jest.Mock)).toHaveBeenCalledTimes(1);
    });

    test('provider change triggers loadHistory', async () => {
      setupHistoryHandlers();
      const stateModule = require('../state');
      const providerCb = (stateModule.subscribeProvider as jest.Mock).mock.calls[0]?.[0] as () => void;
      expect(typeof providerCb).toBe('function');

      (api.getHistory as jest.Mock).mockClear();
      providerCb();
      await new Promise((r) => setTimeout(r, 0));

      expect(api.getHistory).toHaveBeenCalledTimes(1);
    });

    test('account change triggers loadHistory', async () => {
      setupHistoryHandlers();
      const stateModule = require('../state');
      const accountCb = (stateModule.subscribeAccount as jest.Mock).mock.calls[0]?.[0] as () => void;
      expect(typeof accountCb).toBe('function');

      (api.getHistory as jest.Mock).mockClear();
      accountCb();
      await new Promise((r) => setTimeout(r, 0));

      expect(api.getHistory).toHaveBeenCalledTimes(1);
    });
  });
});
