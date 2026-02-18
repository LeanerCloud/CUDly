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
  escapeHtml: jest.fn((str) => str || '')
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
    test('sets default date range to 3 months', () => {
      initHistoryDateRange();

      const startInput = document.getElementById('history-start') as HTMLInputElement;
      const endInput = document.getElementById('history-end') as HTMLInputElement;

      expect(startInput.value).toBeTruthy();
      expect(endInput.value).toBeTruthy();

      // End date should be today in UTC (code uses toISOString which is UTC)
      const today = new Date();
      const todayUTC = today.toISOString().split('T')[0] || '';
      expect(endInput.value).toBe(todayUTC);

      // Start date should be about 3 months ago (UTC)
      const expectedStart = new Date();
      expectedStart.setMonth(expectedStart.getMonth() - 3);
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
});
