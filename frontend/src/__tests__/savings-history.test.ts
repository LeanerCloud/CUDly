/**
 * Savings History module tests
 */

// Mock Chart.js BEFORE importing the module
// Define mock instance that will be shared
const mockChartInstance = {
  destroy: jest.fn(),
  update: jest.fn()
};

// Mock chart.js module - must define MockChart inside the factory
jest.mock('chart.js', () => {
  const MockChart = jest.fn().mockImplementation(() => mockChartInstance) as jest.Mock & { register: jest.Mock };
  MockChart.register = jest.fn();
  return {
    Chart: MockChart,
    registerables: []
  };
});

// Mock the api module
jest.mock('../api', () => ({
  getSavingsAnalytics: jest.fn()
}));

// Mock the state module. Defaults (empty provider, no accounts) keep the
// existing loadSavingsHistory tests unchanged: with these defaults
// loadSavingsHistory adds neither `provider` nor `account_ids` to the
// request, so the `objectContaining({ interval })` assertions still hold.
jest.mock('../state', () => ({
  subscribeProvider: jest.fn(),
  subscribeAccount: jest.fn(),
  getCurrentProvider: jest.fn(() => ''),
  getCurrentAccountIDs: jest.fn(() => [])
}));

// Now import after mocking
import { loadSavingsHistory, initSavingsHistory, savingsChart, convertFromMonthly, unitSuffix, unitLabel } from '../modules/savings-history';
import { getSavingsAnalytics } from '../api';
import * as state from '../state';
import { Chart } from 'chart.js';

describe('Savings History Module', () => {
  beforeEach(() => {
    // Reset DOM
    document.body.innerHTML = `
      <select id="savings-period">
        <option value="24h">Last 24 Hours</option>
        <option value="7d">Last 7 Days</option>
        <option value="30d">Last 30 Days</option>
        <option value="90d" selected>Last 90 Days</option>
      </select>
      <select id="savings-unit">
        <option value="hourly">Hourly</option>
        <option value="monthly" selected>Monthly</option>
        <option value="yearly">Yearly</option>
      </select>
      <button id="refresh-savings-btn">Refresh</button>
      <div id="savings-history-container">
        <canvas id="savings-history-chart"></canvas>
      </div>
      <div id="savings-history-empty" class="hidden">
        <p>No savings history data available yet.</p>
        <p class="help-text">Data will be collected hourly once you have active purchases.</p>
      </div>
      <div id="savings-stats">
        <h4 id="period-savings-label">Period Savings</h4>
        <span id="period-savings">$0</span>
        <h4 id="avg-savings-label">Avg Monthly Savings</h4>
        <span id="avg-hourly-savings">$0/mo</span>
        <span id="peak-savings">$0/mo</span>
      </div>
    `;

    jest.clearAllMocks();
    (Chart as unknown as jest.Mock).mockClear();
  });

  describe('loadSavingsHistory', () => {
    test('loads and renders savings data for default 90d period', async () => {
      const mockData = {
        start: '2024-01-01T00:00:00Z',
        end: '2024-04-01T00:00:00Z',
        interval: 'daily',
        summary: {
          total_period_savings: 500,
          total_upfront_spent: 1000,
          purchase_count: 5,
          average_savings_per_period: 10,
          peak_savings: 25
        },
        data_points: [
          { timestamp: '2024-01-01T00:00:00Z', total_savings: 10, cumulative_savings: 10, total_upfront: 100, purchase_count: 1 },
          { timestamp: '2024-01-02T00:00:00Z', total_savings: 15, cumulative_savings: 25, total_upfront: 150, purchase_count: 1 }
        ]
      };
      (getSavingsAnalytics as jest.Mock).mockResolvedValue(mockData);

      await loadSavingsHistory();

      // 90d is the new default (multi-month trend signal); switch case in
      // savings-history.ts maps it to the 'daily' interval.
      expect(getSavingsAnalytics).toHaveBeenCalledWith(expect.objectContaining({
        interval: 'daily'
      }));
      expect(Chart).toHaveBeenCalled();
    });

    test('shows empty state when no data points', async () => {
      (getSavingsAnalytics as jest.Mock).mockResolvedValue({
        data_points: []
      });

      await loadSavingsHistory();

      const emptyEl = document.getElementById('savings-history-empty');
      expect(emptyEl?.classList.contains('hidden')).toBe(false);
    });

    test('shows empty state when data_points is null', async () => {
      (getSavingsAnalytics as jest.Mock).mockResolvedValue({
        data_points: null
      });

      await loadSavingsHistory();

      const emptyEl = document.getElementById('savings-history-empty');
      expect(emptyEl?.classList.contains('hidden')).toBe(false);
    });

    test('shows empty state on API error', async () => {
      (getSavingsAnalytics as jest.Mock).mockRejectedValue(new Error('API Error'));
      console.error = jest.fn();

      await loadSavingsHistory();

      const emptyEl = document.getElementById('savings-history-empty');
      expect(emptyEl?.classList.contains('hidden')).toBe(false);
    });

    test('hides chart container when showing empty state', async () => {
      (getSavingsAnalytics as jest.Mock).mockResolvedValue({
        data_points: []
      });

      await loadSavingsHistory();

      const chartContainer = document.getElementById('savings-history-chart')?.parentElement;
      expect(chartContainer?.classList.contains('hidden')).toBe(true);
    });

    test('hides stats when showing empty state', async () => {
      (getSavingsAnalytics as jest.Mock).mockResolvedValue({
        data_points: []
      });

      await loadSavingsHistory();

      const statsEl = document.getElementById('savings-stats');
      expect(statsEl?.classList.contains('hidden')).toBe(true);
    });

    test('uses 24h period with hourly interval', async () => {
      (getSavingsAnalytics as jest.Mock).mockResolvedValue({ data_points: [] });

      const periodSelect = document.getElementById('savings-period') as HTMLSelectElement;
      periodSelect.value = '24h';

      await loadSavingsHistory();

      expect(getSavingsAnalytics).toHaveBeenCalledWith(expect.objectContaining({
        interval: 'hourly'
      }));
    });

    test('uses 30d period with daily interval', async () => {
      (getSavingsAnalytics as jest.Mock).mockResolvedValue({ data_points: [] });

      const periodSelect = document.getElementById('savings-period') as HTMLSelectElement;
      periodSelect.value = '30d';

      await loadSavingsHistory();

      expect(getSavingsAnalytics).toHaveBeenCalledWith(expect.objectContaining({
        interval: 'daily'
      }));
    });

    test('uses 90d period with daily interval', async () => {
      (getSavingsAnalytics as jest.Mock).mockResolvedValue({ data_points: [] });

      const periodSelect = document.getElementById('savings-period') as HTMLSelectElement;
      periodSelect.value = '90d';

      await loadSavingsHistory();

      expect(getSavingsAnalytics).toHaveBeenCalledWith(expect.objectContaining({
        interval: 'daily'
      }));
    });

    test('defaults to 7d period with hourly interval for unknown value', async () => {
      (getSavingsAnalytics as jest.Mock).mockResolvedValue({ data_points: [] });

      const periodSelect = document.getElementById('savings-period') as HTMLSelectElement;
      periodSelect.value = 'unknown';

      await loadSavingsHistory();

      expect(getSavingsAnalytics).toHaveBeenCalledWith(expect.objectContaining({
        interval: 'hourly'
      }));
    });

    test('renders savings stats from summary', async () => {
      const mockData = {
        summary: {
          total_period_savings: 1500,
          average_savings_per_period: 75,
          peak_savings: 200
        },
        data_points: [
          { timestamp: '2024-01-01T00:00:00Z', total_savings: 100, cumulative_savings: 100, total_upfront: 500, purchase_count: 1 }
        ]
      };
      (getSavingsAnalytics as jest.Mock).mockResolvedValue(mockData);

      await loadSavingsHistory();

      const periodSavingsEl = document.getElementById('period-savings');
      const avgSavingsEl = document.getElementById('avg-hourly-savings');
      const peakSavingsEl = document.getElementById('peak-savings');

      expect(periodSavingsEl?.textContent).toContain('$1.50K');
      expect(avgSavingsEl?.textContent).toContain('$75.00/mo');
      expect(peakSavingsEl?.textContent).toContain('$200.00/mo');
    });

    test('calculates stats from data points when summary missing', async () => {
      const mockData = {
        summary: null,
        data_points: [
          { timestamp: '2024-01-01T00:00:00Z', total_savings: 100, cumulative_savings: 100, total_upfront: 500, purchase_count: 1 },
          { timestamp: '2024-01-02T00:00:00Z', total_savings: 200, cumulative_savings: 300, total_upfront: 1000, purchase_count: 2 }
        ]
      };
      (getSavingsAnalytics as jest.Mock).mockResolvedValue(mockData);

      await loadSavingsHistory();

      const periodSavingsEl = document.getElementById('period-savings');
      expect(periodSavingsEl?.textContent).toContain('$300.00');
    });

    test('handles missing period select gracefully', async () => {
      document.getElementById('savings-period')?.remove();
      (getSavingsAnalytics as jest.Mock).mockResolvedValue({ data_points: [] });

      // Should not throw
      await expect(loadSavingsHistory()).resolves.not.toThrow();
    });

    test('destroys existing chart before creating new one', async () => {
      const mockData = {
        data_points: [
          { timestamp: '2024-01-01T00:00:00Z', total_savings: 10, cumulative_savings: 10, total_upfront: 100, purchase_count: 1 }
        ]
      };
      (getSavingsAnalytics as jest.Mock).mockResolvedValue(mockData);

      // Load twice to trigger chart recreation
      await loadSavingsHistory();
      await loadSavingsHistory();

      expect(mockChartInstance.destroy).toHaveBeenCalled();
    });

    test('formats currency values over 1000 with K suffix', async () => {
      const mockData = {
        summary: {
          total_period_savings: 5000,
          average_savings_per_period: 250,
          peak_savings: 1500
        },
        data_points: [
          { timestamp: '2024-01-01T00:00:00Z', total_savings: 100, cumulative_savings: 100, total_upfront: 500, purchase_count: 1 }
        ]
      };
      (getSavingsAnalytics as jest.Mock).mockResolvedValue(mockData);

      await loadSavingsHistory();

      const periodSavingsEl = document.getElementById('period-savings');
      expect(periodSavingsEl?.textContent).toBe('$5.00K');
    });

    test('formats currency values under 1000 without suffix', async () => {
      const mockData = {
        summary: {
          total_period_savings: 500,
          average_savings_per_period: 25,
          peak_savings: 100
        },
        data_points: [
          { timestamp: '2024-01-01T00:00:00Z', total_savings: 100, cumulative_savings: 100, total_upfront: 500, purchase_count: 1 }
        ]
      };
      (getSavingsAnalytics as jest.Mock).mockResolvedValue(mockData);

      await loadSavingsHistory();

      const periodSavingsEl = document.getElementById('period-savings');
      expect(periodSavingsEl?.textContent).toBe('$500.00');
    });

    test('shows chart container when data is available', async () => {
      const mockData = {
        data_points: [
          { timestamp: '2024-01-01T00:00:00Z', total_savings: 10, cumulative_savings: 10, total_upfront: 100, purchase_count: 1 }
        ]
      };
      (getSavingsAnalytics as jest.Mock).mockResolvedValue(mockData);

      await loadSavingsHistory();

      const chartContainer = document.getElementById('savings-history-chart')?.parentElement;
      expect(chartContainer?.classList.contains('hidden')).toBe(false);
    });

    test('hides empty state when data is available', async () => {
      const mockData = {
        data_points: [
          { timestamp: '2024-01-01T00:00:00Z', total_savings: 10, cumulative_savings: 10, total_upfront: 100, purchase_count: 1 }
        ]
      };
      (getSavingsAnalytics as jest.Mock).mockResolvedValue(mockData);

      await loadSavingsHistory();

      const emptyEl = document.getElementById('savings-history-empty');
      expect(emptyEl?.classList.contains('hidden')).toBe(true);
    });

    test('shows stats when data is available', async () => {
      const mockData = {
        data_points: [
          { timestamp: '2024-01-01T00:00:00Z', total_savings: 10, cumulative_savings: 10, total_upfront: 100, purchase_count: 1 }
        ]
      };
      (getSavingsAnalytics as jest.Mock).mockResolvedValue(mockData);

      await loadSavingsHistory();

      const statsEl = document.getElementById('savings-stats');
      expect(statsEl?.classList.contains('hidden')).toBe(false);
    });

    test('creates chart with correct configuration', async () => {
      const mockData = {
        data_points: [
          { timestamp: '2024-01-01T12:00:00Z', total_savings: 10, cumulative_savings: 10, total_upfront: 100, purchase_count: 1 },
          { timestamp: '2024-01-02T12:00:00Z', total_savings: 20, cumulative_savings: 30, total_upfront: 200, purchase_count: 1 }
        ]
      };
      (getSavingsAnalytics as jest.Mock).mockResolvedValue(mockData);

      await loadSavingsHistory();

      expect(Chart).toHaveBeenCalledWith(
        expect.any(HTMLCanvasElement),
        expect.objectContaining({
          type: 'line',
          data: expect.objectContaining({
            labels: expect.any(Array),
            datasets: expect.arrayContaining([
              expect.objectContaining({
                label: 'Period Savings'
              }),
              expect.objectContaining({
                label: 'Cumulative Savings'
              })
            ])
          }),
          options: expect.objectContaining({
            responsive: true,
            maintainAspectRatio: false
          })
        })
      );
    });

    test('handles data points with null total_savings', async () => {
      const mockData = {
        data_points: [
          { timestamp: '2024-01-01T00:00:00Z', total_savings: null, cumulative_savings: 0, total_upfront: 0, purchase_count: 0 }
        ]
      };
      (getSavingsAnalytics as jest.Mock).mockResolvedValue(mockData);

      await loadSavingsHistory();

      // Should still create chart without throwing
      expect(Chart).toHaveBeenCalled();
    });

    test('handles data points with undefined cumulative_savings', async () => {
      const mockData = {
        data_points: [
          { timestamp: '2024-01-01T00:00:00Z', total_savings: 10, cumulative_savings: undefined, total_upfront: 100, purchase_count: 1 }
        ]
      };
      (getSavingsAnalytics as jest.Mock).mockResolvedValue(mockData);

      await loadSavingsHistory();

      // Should still create chart without throwing
      expect(Chart).toHaveBeenCalled();
    });

    test('logs error on API failure', async () => {
      console.error = jest.fn();
      (getSavingsAnalytics as jest.Mock).mockRejectedValue(new Error('Network error'));

      await loadSavingsHistory();

      expect(console.error).toHaveBeenCalledWith(
        'Failed to load savings history:',
        'Network error'
      );
    });

    test('handles missing canvas element gracefully', async () => {
      document.getElementById('savings-history-chart')?.remove();
      const mockData = {
        data_points: [
          { timestamp: '2024-01-01T00:00:00Z', total_savings: 10, cumulative_savings: 10, total_upfront: 100, purchase_count: 1 }
        ]
      };
      (getSavingsAnalytics as jest.Mock).mockResolvedValue(mockData);
      console.error = jest.fn();

      await loadSavingsHistory();

      expect(console.error).toHaveBeenCalledWith('Canvas element not found: savings-history-chart');
    });
  });

  describe('initSavingsHistory', () => {
    test('adds change listener to period select', () => {
      const periodSelect = document.getElementById('savings-period') as HTMLSelectElement;
      const addEventListenerSpy = jest.spyOn(periodSelect, 'addEventListener');

      initSavingsHistory();

      expect(addEventListenerSpy).toHaveBeenCalledWith('change', expect.any(Function));
    });

    test('adds click listener to refresh button', () => {
      const refreshBtn = document.getElementById('refresh-savings-btn') as HTMLButtonElement;
      const addEventListenerSpy = jest.spyOn(refreshBtn, 'addEventListener');

      initSavingsHistory();

      expect(addEventListenerSpy).toHaveBeenCalledWith('click', expect.any(Function));
    });

    test('handles missing period select gracefully', () => {
      document.getElementById('savings-period')?.remove();

      expect(() => initSavingsHistory()).not.toThrow();
    });

    test('handles missing refresh button gracefully', () => {
      document.getElementById('refresh-savings-btn')?.remove();

      expect(() => initSavingsHistory()).not.toThrow();
    });

    test('period change triggers loadSavingsHistory', async () => {
      (getSavingsAnalytics as jest.Mock).mockResolvedValue({ data_points: [] });

      initSavingsHistory();

      const periodSelect = document.getElementById('savings-period') as HTMLSelectElement;
      periodSelect.value = '30d';
      periodSelect.dispatchEvent(new Event('change'));

      await new Promise(resolve => setTimeout(resolve, 50));

      expect(getSavingsAnalytics).toHaveBeenCalled();
    });

    test('refresh button click triggers loadSavingsHistory', async () => {
      (getSavingsAnalytics as jest.Mock).mockResolvedValue({ data_points: [] });

      initSavingsHistory();

      const refreshBtn = document.getElementById('refresh-savings-btn') as HTMLButtonElement;
      refreshBtn.click();

      await new Promise(resolve => setTimeout(resolve, 50));

      expect(getSavingsAnalytics).toHaveBeenCalled();
    });
  });

  // Issue #503: Purchases tab subscriber wiring. Chip changes must re-query
  // the Savings History chart with the new filter; inactive-tab guard and
  // microtask coalescing mirror PR #488's recommendations.ts pattern.
  describe('subscriber wiring (issue #503)', () => {
    function addPurchasesTab(active: boolean): void {
      const tab = document.createElement('div');
      tab.id = 'purchases-tab';
      if (active) tab.classList.add('active');
      document.body.appendChild(tab);
    }

    beforeEach(() => {
      (getSavingsAnalytics as jest.Mock).mockResolvedValue({ data_points: [] });
      // Reset filter getters to their defaults; individual tests override.
      (state.getCurrentProvider as jest.Mock).mockReturnValue('');
      (state.getCurrentAccountIDs as jest.Mock).mockReturnValue([]);
    });

    test('registers callbacks with state.subscribeProvider and state.subscribeAccount', () => {
      initSavingsHistory();

      expect(state.subscribeProvider).toHaveBeenCalledTimes(1);
      expect(state.subscribeAccount).toHaveBeenCalledTimes(1);
      expect(typeof (state.subscribeProvider as jest.Mock).mock.calls[0]?.[0]).toBe('function');
      expect(typeof (state.subscribeAccount as jest.Mock).mock.calls[0]?.[0]).toBe('function');
    });

    test('account chip change re-queries the chart when purchases-tab is active', async () => {
      addPurchasesTab(true);
      (state.getCurrentAccountIDs as jest.Mock).mockReturnValue(['acct-A']);

      initSavingsHistory();
      const accountCb = (state.subscribeAccount as jest.Mock).mock.calls[0]?.[0] as () => void;
      expect(typeof accountCb).toBe('function');

      (getSavingsAnalytics as jest.Mock).mockClear();
      accountCb();
      // queueMicrotask + the awaited fetch settle within a macrotask flush.
      await new Promise((r) => setTimeout(r, 0));

      expect(getSavingsAnalytics).toHaveBeenCalledTimes(1);
      expect(getSavingsAnalytics).toHaveBeenCalledWith(
        expect.objectContaining({ account_ids: ['acct-A'] })
      );
    });

    test('provider chip change re-queries the chart when purchases-tab is active', async () => {
      addPurchasesTab(true);
      (state.getCurrentProvider as jest.Mock).mockReturnValue('aws');

      initSavingsHistory();
      const providerCb = (state.subscribeProvider as jest.Mock).mock.calls[0]?.[0] as () => void;
      expect(typeof providerCb).toBe('function');

      (getSavingsAnalytics as jest.Mock).mockClear();
      providerCb();
      await new Promise((r) => setTimeout(r, 0));

      expect(getSavingsAnalytics).toHaveBeenCalledTimes(1);
      expect(getSavingsAnalytics).toHaveBeenCalledWith(
        expect.objectContaining({ provider: 'aws' })
      );
    });

    test('does NOT re-query when purchases-tab is inactive (active-tab guard)', async () => {
      // #purchases-tab present but no .active class: user is on another tab.
      addPurchasesTab(false);

      initSavingsHistory();
      const providerCb = (state.subscribeProvider as jest.Mock).mock.calls[0]?.[0] as () => void;
      const accountCb = (state.subscribeAccount as jest.Mock).mock.calls[0]?.[0] as () => void;

      (getSavingsAnalytics as jest.Mock).mockClear();
      providerCb();
      accountCb();
      await new Promise((r) => setTimeout(r, 0));

      expect(getSavingsAnalytics).not.toHaveBeenCalled();
    });

    test('coalesces back-to-back provider+account fires into one reload', async () => {
      addPurchasesTab(true);

      initSavingsHistory();
      const providerCb = (state.subscribeProvider as jest.Mock).mock.calls[0]?.[0] as () => void;
      const accountCb = (state.subscribeAccount as jest.Mock).mock.calls[0]?.[0] as () => void;

      (getSavingsAnalytics as jest.Mock).mockClear();
      // Simulate the topbar provider-change handler firing both subscribers
      // synchronously (clear accounts, then set provider, per #185).
      accountCb();
      providerCb();
      await new Promise((r) => setTimeout(r, 0));

      expect(getSavingsAnalytics).toHaveBeenCalledTimes(1);
    });

    test('two consecutive account changes produce two fetches with distinct account_ids', async () => {
      addPurchasesTab(true);

      initSavingsHistory();
      const accountCb = (state.subscribeAccount as jest.Mock).mock.calls[0]?.[0] as () => void;

      (getSavingsAnalytics as jest.Mock).mockClear();

      (state.getCurrentAccountIDs as jest.Mock).mockReturnValue(['acct-A']);
      accountCb();
      await new Promise((r) => setTimeout(r, 0));

      (state.getCurrentAccountIDs as jest.Mock).mockReturnValue(['acct-B']);
      accountCb();
      await new Promise((r) => setTimeout(r, 0));

      expect(getSavingsAnalytics).toHaveBeenCalledTimes(2);
      const firstCall = (getSavingsAnalytics as jest.Mock).mock.calls[0][0];
      const secondCall = (getSavingsAnalytics as jest.Mock).mock.calls[1][0];
      expect(firstCall.account_ids).toEqual(['acct-A']);
      expect(secondCall.account_ids).toEqual(['acct-B']);
    });
  });

  describe('chart formatting', () => {
    test('uses date labels for daily/weekly/monthly interval', async () => {
      const mockData = {
        data_points: [
          { timestamp: '2024-01-15T00:00:00Z', total_savings: 10, cumulative_savings: 10, total_upfront: 100, purchase_count: 1 }
        ]
      };
      (getSavingsAnalytics as jest.Mock).mockResolvedValue(mockData);

      const periodSelect = document.getElementById('savings-period') as HTMLSelectElement;
      periodSelect.value = '30d';

      await loadSavingsHistory();

      const chartCall = (Chart as unknown as jest.Mock).mock.calls[0];
      const labels = chartCall[1].data.labels;
      // Should format as short date (e.g., "Jan 15")
      expect(labels[0]).toMatch(/Jan \d+/);
    });

    test('uses datetime labels for hourly interval', async () => {
      const mockData = {
        data_points: [
          { timestamp: '2024-01-15T14:00:00Z', total_savings: 10, cumulative_savings: 10, total_upfront: 100, purchase_count: 1 }
        ]
      };
      (getSavingsAnalytics as jest.Mock).mockResolvedValue(mockData);

      const periodSelect = document.getElementById('savings-period') as HTMLSelectElement;
      periodSelect.value = '7d';

      await loadSavingsHistory();

      const chartCall = (Chart as unknown as jest.Mock).mock.calls[0];
      const labels = chartCall[1].data.labels;
      // Should include hour (e.g., "Jan 15, 2 PM")
      expect(labels[0]).toMatch(/Jan \d+, \d+ [AP]M/);
    });

    test('configures point radius based on data point count', async () => {
      // Create more than 50 data points
      const manyDataPoints = Array.from({ length: 60 }, (_, i) => ({
        timestamp: `2024-01-01T${i % 24}:00:00Z`,
        total_savings: 10,
        cumulative_savings: 10 * (i + 1),
        total_upfront: 100,
        purchase_count: 1
      }));

      (getSavingsAnalytics as jest.Mock).mockResolvedValue({
        data_points: manyDataPoints
      });

      await loadSavingsHistory();

      const chartCall = (Chart as unknown as jest.Mock).mock.calls[0];
      const datasets = chartCall[1].data.datasets;
      // With many data points, radius should be 0
      expect(datasets[0].pointRadius).toBe(0);
    });

    test('configures point radius for fewer data points', async () => {
      const fewDataPoints = [
        { timestamp: '2024-01-01T00:00:00Z', total_savings: 10, cumulative_savings: 10, total_upfront: 100, purchase_count: 1 },
        { timestamp: '2024-01-02T00:00:00Z', total_savings: 20, cumulative_savings: 30, total_upfront: 200, purchase_count: 1 }
      ];

      (getSavingsAnalytics as jest.Mock).mockResolvedValue({
        data_points: fewDataPoints
      });

      await loadSavingsHistory();

      const chartCall = (Chart as unknown as jest.Mock).mock.calls[0];
      const datasets = chartCall[1].data.datasets;
      // With few data points, radius should be non-zero
      expect(datasets[0].pointRadius).toBe(3);
    });
  });

  describe('savingsChart export', () => {
    test('savingsChart is exported', () => {
      // The chart is managed by the module
      expect(savingsChart).toBeDefined();
    });
  });

  describe('chart callback functions', () => {
    test('y-axis tick callback formats currency for number values', async () => {
      const mockData = {
        data_points: [
          { timestamp: '2024-01-01T00:00:00Z', total_savings: 10, cumulative_savings: 10, total_upfront: 100, purchase_count: 1 }
        ]
      };
      (getSavingsAnalytics as jest.Mock).mockResolvedValue(mockData);

      await loadSavingsHistory();

      const chartCall = (Chart as unknown as jest.Mock).mock.calls[0];
      const yAxisCallback = chartCall[1].options.scales.y.ticks.callback;

      // Test with number value
      expect(yAxisCallback(50)).toBe('$50.00');
      expect(yAxisCallback(123.456)).toBe('$123.46');
    });

    test('y-axis tick callback formats currency for string values', async () => {
      const mockData = {
        data_points: [
          { timestamp: '2024-01-01T00:00:00Z', total_savings: 10, cumulative_savings: 10, total_upfront: 100, purchase_count: 1 }
        ]
      };
      (getSavingsAnalytics as jest.Mock).mockResolvedValue(mockData);

      await loadSavingsHistory();

      const chartCall = (Chart as unknown as jest.Mock).mock.calls[0];
      const yAxisCallback = chartCall[1].options.scales.y.ticks.callback;

      // Test with string value
      expect(yAxisCallback('75.5')).toBe('$75.50');
      expect(yAxisCallback('100')).toBe('$100.00');
    });

    test('y1-axis tick callback formats large values with K suffix', async () => {
      const mockData = {
        data_points: [
          { timestamp: '2024-01-01T00:00:00Z', total_savings: 10, cumulative_savings: 10, total_upfront: 100, purchase_count: 1 }
        ]
      };
      (getSavingsAnalytics as jest.Mock).mockResolvedValue(mockData);

      await loadSavingsHistory();

      const chartCall = (Chart as unknown as jest.Mock).mock.calls[0];
      const y1AxisCallback = chartCall[1].options.scales.y1.ticks.callback;

      // Test with large number values (>= 1000)
      expect(y1AxisCallback(1000)).toBe('$1.0K');
      expect(y1AxisCallback(5000)).toBe('$5.0K');
      expect(y1AxisCallback(12500)).toBe('$12.5K');
    });

    test('y1-axis tick callback formats small values without K suffix', async () => {
      const mockData = {
        data_points: [
          { timestamp: '2024-01-01T00:00:00Z', total_savings: 10, cumulative_savings: 10, total_upfront: 100, purchase_count: 1 }
        ]
      };
      (getSavingsAnalytics as jest.Mock).mockResolvedValue(mockData);

      await loadSavingsHistory();

      const chartCall = (Chart as unknown as jest.Mock).mock.calls[0];
      const y1AxisCallback = chartCall[1].options.scales.y1.ticks.callback;

      // Test with small number values (< 1000)
      expect(y1AxisCallback(50)).toBe('$50');
      expect(y1AxisCallback(999)).toBe('$999');
      expect(y1AxisCallback(0)).toBe('$0');
    });

    test('y1-axis tick callback handles string values', async () => {
      const mockData = {
        data_points: [
          { timestamp: '2024-01-01T00:00:00Z', total_savings: 10, cumulative_savings: 10, total_upfront: 100, purchase_count: 1 }
        ]
      };
      (getSavingsAnalytics as jest.Mock).mockResolvedValue(mockData);

      await loadSavingsHistory();

      const chartCall = (Chart as unknown as jest.Mock).mock.calls[0];
      const y1AxisCallback = chartCall[1].options.scales.y1.ticks.callback;

      // Test with string values
      expect(y1AxisCallback('2500')).toBe('$2.5K');
      expect(y1AxisCallback('500')).toBe('$500');
    });

    test('tooltip label callback formats period savings with unit suffix (default monthly)', async () => {
      const mockData = {
        data_points: [
          { timestamp: '2024-01-01T00:00:00Z', total_savings: 10, cumulative_savings: 10, total_upfront: 100, purchase_count: 1 }
        ]
      };
      (getSavingsAnalytics as jest.Mock).mockResolvedValue(mockData);

      await loadSavingsHistory();

      const chartCall = (Chart as unknown as jest.Mock).mock.calls[0];
      const tooltipLabelCallback = chartCall[1].options.plugins.tooltip.callbacks.label;

      // Test period savings (datasetIndex 0)
      const periodContext = {
        raw: 25.5678,
        datasetIndex: 0,
        dataset: { label: 'Period Savings' }
      };
      expect(tooltipLabelCallback(periodContext)).toBe('Period Savings: $25.5678/mo');
    });

    test('tooltip label callback formats cumulative savings without unit suffix', async () => {
      const mockData = {
        data_points: [
          { timestamp: '2024-01-01T00:00:00Z', total_savings: 10, cumulative_savings: 10, total_upfront: 100, purchase_count: 1 }
        ]
      };
      (getSavingsAnalytics as jest.Mock).mockResolvedValue(mockData);

      await loadSavingsHistory();

      const chartCall = (Chart as unknown as jest.Mock).mock.calls[0];
      const tooltipLabelCallback = chartCall[1].options.plugins.tooltip.callbacks.label;

      // Test cumulative savings (datasetIndex 1)
      const cumulativeContext = {
        raw: 1234.56,
        datasetIndex: 1,
        dataset: { label: 'Cumulative Savings' }
      };
      expect(tooltipLabelCallback(cumulativeContext)).toBe('Cumulative Savings: $1234.56');
    });

    test('tooltip label callback handles null/undefined raw value', async () => {
      const mockData = {
        data_points: [
          { timestamp: '2024-01-01T00:00:00Z', total_savings: 10, cumulative_savings: 10, total_upfront: 100, purchase_count: 1 }
        ]
      };
      (getSavingsAnalytics as jest.Mock).mockResolvedValue(mockData);

      await loadSavingsHistory();

      const chartCall = (Chart as unknown as jest.Mock).mock.calls[0];
      const tooltipLabelCallback = chartCall[1].options.plugins.tooltip.callbacks.label;

      // Test with null/undefined raw value
      const nullContext = {
        raw: null,
        datasetIndex: 0,
        dataset: { label: 'Period Savings' }
      };
      expect(tooltipLabelCallback(nullContext)).toBe('Period Savings: $0.0000/mo');

      const undefinedContext = {
        raw: undefined,
        datasetIndex: 1,
        dataset: { label: 'Cumulative Savings' }
      };
      expect(tooltipLabelCallback(undefinedContext)).toBe('Cumulative Savings: $0.00');
    });
  });

  describe('edge cases', () => {
    test('handles empty summary object', async () => {
      const mockData = {
        summary: {},
        data_points: [
          { timestamp: '2024-01-01T00:00:00Z', total_savings: 100, cumulative_savings: 100, total_upfront: 500, purchase_count: 1 }
        ]
      };
      (getSavingsAnalytics as jest.Mock).mockResolvedValue(mockData);

      await loadSavingsHistory();

      // Should calculate from data points
      const periodSavingsEl = document.getElementById('period-savings');
      expect(periodSavingsEl?.textContent).toBe('$100.00');
    });

    test('handles missing stats elements', async () => {
      document.getElementById('period-savings')?.remove();
      document.getElementById('avg-hourly-savings')?.remove();
      document.getElementById('peak-savings')?.remove();

      const mockData = {
        summary: {
          total_period_savings: 1500,
          average_savings_per_period: 75,
          peak_savings: 200
        },
        data_points: [
          { timestamp: '2024-01-01T00:00:00Z', total_savings: 100, cumulative_savings: 100, total_upfront: 500, purchase_count: 1 }
        ]
      };
      (getSavingsAnalytics as jest.Mock).mockResolvedValue(mockData);

      // Should not throw
      await expect(loadSavingsHistory()).resolves.not.toThrow();
    });

    test('handles missing chart container parent', async () => {
      const canvas = document.getElementById('savings-history-chart');
      if (canvas && canvas.parentElement) {
        canvas.parentElement.removeChild(canvas);
        document.body.appendChild(canvas);
      }

      const mockData = {
        data_points: [
          { timestamp: '2024-01-01T00:00:00Z', total_savings: 10, cumulative_savings: 10, total_upfront: 100, purchase_count: 1 }
        ]
      };
      (getSavingsAnalytics as jest.Mock).mockResolvedValue(mockData);

      // Should not throw
      await expect(loadSavingsHistory()).resolves.not.toThrow();
    });

    test('handles zero values in data points', async () => {
      const mockData = {
        data_points: [
          { timestamp: '2024-01-01T00:00:00Z', total_savings: 0, cumulative_savings: 0, total_upfront: 0, purchase_count: 0 }
        ]
      };
      (getSavingsAnalytics as jest.Mock).mockResolvedValue(mockData);

      await loadSavingsHistory();

      expect(Chart).toHaveBeenCalled();
    });

    test('calculates peak savings from data points', async () => {
      const mockData = {
        summary: null,
        data_points: [
          { timestamp: '2024-01-01T00:00:00Z', total_savings: 50, cumulative_savings: 50, total_upfront: 500, purchase_count: 1 },
          { timestamp: '2024-01-02T00:00:00Z', total_savings: 150, cumulative_savings: 200, total_upfront: 1000, purchase_count: 2 },
          { timestamp: '2024-01-03T00:00:00Z', total_savings: 100, cumulative_savings: 300, total_upfront: 1500, purchase_count: 1 }
        ]
      };
      (getSavingsAnalytics as jest.Mock).mockResolvedValue(mockData);

      await loadSavingsHistory();

      const peakSavingsEl = document.getElementById('peak-savings');
      // Peak should be 150
      expect(peakSavingsEl?.textContent).toBe('$150.00/mo');
    });

    test('destroys chart when showing empty state', async () => {
      // First load with data to create chart
      (getSavingsAnalytics as jest.Mock).mockResolvedValue({
        data_points: [
          { timestamp: '2024-01-01T00:00:00Z', total_savings: 10, cumulative_savings: 10, total_upfront: 100, purchase_count: 1 }
        ]
      });
      await loadSavingsHistory();

      // Then load with no data
      (getSavingsAnalytics as jest.Mock).mockResolvedValue({
        data_points: []
      });
      await loadSavingsHistory();

      expect(mockChartInstance.destroy).toHaveBeenCalled();
    });
  });

  // Issue #701: when the chart returns no data because a filter is active, the
  // empty-state message must distinguish "nothing in this scope" from "no data
  // at all". Without a filter the original message applies; with a filter the
  // message should name the active filter so the user understands why the chart
  // is blank.
  describe('empty-state copy with active filter (issue #701)', () => {
    test('shows filter name in empty-state heading when provider chip is set', async () => {
      (state.getCurrentProvider as jest.Mock).mockReturnValue('aws');
      (state.getCurrentAccountIDs as jest.Mock).mockReturnValue([]);
      (getSavingsAnalytics as jest.Mock).mockResolvedValue({ data_points: [] });

      await loadSavingsHistory();

      const emptyEl = document.getElementById('savings-history-empty');
      const heading = emptyEl?.querySelector('p:first-child');
      expect(heading?.textContent).toContain('AWS');
      expect(heading?.textContent).not.toContain('available yet');
    });

    test('shows help text asking to broaden filter when a filter is active', async () => {
      (state.getCurrentProvider as jest.Mock).mockReturnValue('gcp');
      (state.getCurrentAccountIDs as jest.Mock).mockReturnValue([]);
      (getSavingsAnalytics as jest.Mock).mockResolvedValue({ data_points: [] });

      await loadSavingsHistory();

      const emptyEl = document.getElementById('savings-history-empty');
      const help = emptyEl?.querySelector('p.help-text');
      expect(help?.textContent).toMatch(/broaden/i);
    });

    test('shows original message when no filter is active', async () => {
      (state.getCurrentProvider as jest.Mock).mockReturnValue('');
      (state.getCurrentAccountIDs as jest.Mock).mockReturnValue([]);
      (getSavingsAnalytics as jest.Mock).mockResolvedValue({ data_points: [] });

      await loadSavingsHistory();

      const emptyEl = document.getElementById('savings-history-empty');
      const heading = emptyEl?.querySelector('p:first-child');
      expect(heading?.textContent).toContain('available yet');
    });

    test('shows filter name in empty-state heading when account chip is set', async () => {
      const testUUID = 'aabbccdd-1234-5678-abcd-aabbccddee00';
      (state.getCurrentProvider as jest.Mock).mockReturnValue('');
      (state.getCurrentAccountIDs as jest.Mock).mockReturnValue([testUUID]);
      (getSavingsAnalytics as jest.Mock).mockResolvedValue({ data_points: [] });

      await loadSavingsHistory();

      const emptyEl = document.getElementById('savings-history-empty');
      const heading = emptyEl?.querySelector('p:first-child');
      expect(heading?.textContent).toContain(testUUID);
    });

    test('shows provider and account in heading when both chips are set', async () => {
      const testUUID = 'aabbccdd-1234-5678-abcd-aabbccddee00';
      (state.getCurrentProvider as jest.Mock).mockReturnValue('azure');
      (state.getCurrentAccountIDs as jest.Mock).mockReturnValue([testUUID]);
      (getSavingsAnalytics as jest.Mock).mockResolvedValue({ data_points: [] });

      await loadSavingsHistory();

      const emptyEl = document.getElementById('savings-history-empty');
      const heading = emptyEl?.querySelector('p:first-child');
      expect(heading?.textContent).toContain('AZURE');
      expect(heading?.textContent).toContain(testUUID);
    });

    test('shows distinct error copy on API failure (not the filter-aware empty state)', async () => {
      // CR feedback on PR #741: a fetch failure used to fall through to the
      // "no data for the selected filter" empty state, which masked real
      // outages (network/5xx/auth). The error path must now render its own
      // copy so the user can tell "filter matched nothing" from "the fetch
      // never completed".
      (state.getCurrentProvider as jest.Mock).mockReturnValue('aws');
      (state.getCurrentAccountIDs as jest.Mock).mockReturnValue([]);
      (getSavingsAnalytics as jest.Mock).mockRejectedValue(new Error('network failure'));
      console.error = jest.fn();

      await loadSavingsHistory();

      const emptyEl = document.getElementById('savings-history-empty');
      const heading = emptyEl?.querySelector('p:first-child');
      const help = emptyEl?.querySelector('p.help-text');
      expect(heading?.textContent).toBe('Failed to load savings history.');
      expect(help?.textContent).toContain('network failure');
      // Error copy is NOT the empty-state copy — it does not name the filter.
      expect(heading?.textContent).not.toContain('AWS');
      expect(heading?.textContent).not.toContain('available yet');
    });

    test('error state hides chart container and stats', async () => {
      (getSavingsAnalytics as jest.Mock).mockRejectedValue(new Error('boom'));
      console.error = jest.fn();

      await loadSavingsHistory();

      const chartContainer = document.getElementById('savings-history-chart')?.parentElement;
      const statsEl = document.getElementById('savings-stats');
      expect(chartContainer?.classList.contains('hidden')).toBe(true);
      expect(statsEl?.classList.contains('hidden')).toBe(true);
    });

    test('error state handles non-Error rejection with "Unknown error"', async () => {
      // Generic reject value (e.g. a thrown string from a non-Error catch path)
      // must still surface a helpful message rather than `[object Object]`.
      (getSavingsAnalytics as jest.Mock).mockRejectedValue('something broke');
      console.error = jest.fn();

      await loadSavingsHistory();

      const help = document.getElementById('savings-history-empty')?.querySelector('p.help-text');
      expect(help?.textContent).toContain('Unknown error');
    });
  });

  // CR feedback on PR #741 F2: a provider chip set to the "all" sentinel
  // is the user explicitly choosing unfiltered, so buildFilterDesc must not
  // include "ALL" in the empty-state copy.
  describe('buildFilterDesc — "all" sentinel handling (PR #741 F2)', () => {
    test('treats provider === "all" as unfiltered (no provider clause in heading)', async () => {
      (state.getCurrentProvider as jest.Mock).mockReturnValue('all');
      (state.getCurrentAccountIDs as jest.Mock).mockReturnValue([]);
      (getSavingsAnalytics as jest.Mock).mockResolvedValue({ data_points: [] });

      await loadSavingsHistory();

      const heading = document.getElementById('savings-history-empty')?.querySelector('p:first-child');
      expect(heading?.textContent).toContain('available yet');
      expect(heading?.textContent).not.toMatch(/all/i);
    });

    test('treats provider === "ALL" (case-insensitive) as unfiltered', async () => {
      (state.getCurrentProvider as jest.Mock).mockReturnValue('ALL');
      (state.getCurrentAccountIDs as jest.Mock).mockReturnValue([]);
      (getSavingsAnalytics as jest.Mock).mockResolvedValue({ data_points: [] });

      await loadSavingsHistory();

      const heading = document.getElementById('savings-history-empty')?.querySelector('p:first-child');
      expect(heading?.textContent).toContain('available yet');
      expect(heading?.textContent).not.toContain('ALL');
    });

    test('still shows account clause when provider === "all" but account chip is set', async () => {
      const testUUID = 'aabbccdd-1234-5678-abcd-aabbccddee00';
      (state.getCurrentProvider as jest.Mock).mockReturnValue('all');
      (state.getCurrentAccountIDs as jest.Mock).mockReturnValue([testUUID]);
      (getSavingsAnalytics as jest.Mock).mockResolvedValue({ data_points: [] });

      await loadSavingsHistory();

      const heading = document.getElementById('savings-history-empty')?.querySelector('p:first-child');
      expect(heading?.textContent).toContain(testUUID);
      expect(heading?.textContent).not.toMatch(/all,/i);
    });
  });

  // Issue #750: unit conversion helpers + dropdown behavior
  describe('unit conversion helpers', () => {
    describe('convertFromMonthly', () => {
      test('monthly -> monthly is identity', () => {
        expect(convertFromMonthly(100, 'monthly')).toBe(100);
        expect(convertFromMonthly(0, 'monthly')).toBe(0);
        expect(convertFromMonthly(1.15, 'monthly')).toBe(1.15);
      });

      test('monthly -> hourly divides by 730', () => {
        expect(convertFromMonthly(730, 'hourly')).toBe(1);
        expect(convertFromMonthly(1.15, 'hourly')).toBeCloseTo(1.15 / 730, 6);
      });

      test('monthly -> yearly multiplies by 12', () => {
        expect(convertFromMonthly(100, 'yearly')).toBe(1200);
        expect(convertFromMonthly(1.15, 'yearly')).toBeCloseTo(1.15 * 12, 6);
      });
    });

    describe('unitSuffix', () => {
      test('returns correct suffix for each unit', () => {
        expect(unitSuffix('hourly')).toBe('/hr');
        expect(unitSuffix('monthly')).toBe('/mo');
        expect(unitSuffix('yearly')).toBe('/yr');
      });
    });

    describe('unitLabel', () => {
      test('returns correct label for each unit', () => {
        expect(unitLabel('hourly')).toBe('Hourly');
        expect(unitLabel('monthly')).toBe('Monthly');
        expect(unitLabel('yearly')).toBe('Yearly');
      });
    });
  });

  // Issue #750: unit dropdown wires + KPI display per unit
  describe('unit dropdown (issue #750)', () => {
    const mockData = {
      summary: {
        total_period_savings: 730,       // $730/mo cumulative total
        average_savings_per_period: 730, // $730/mo avg per bucket
        peak_savings: 730                // $730/mo peak bucket
      },
      data_points: [
        { timestamp: '2024-01-01T00:00:00Z', total_savings: 730, cumulative_savings: 730, total_upfront: 100, purchase_count: 1 }
      ]
    };

    test('default unit is monthly -- avg and peak show /mo suffix', async () => {
      (getSavingsAnalytics as jest.Mock).mockResolvedValue(mockData);
      await loadSavingsHistory();

      const avgEl = document.getElementById('avg-hourly-savings');
      const peakEl = document.getElementById('peak-savings');
      expect(avgEl?.textContent).toContain('/mo');
      expect(peakEl?.textContent).toContain('/mo');
    });

    test('hourly unit -- avg and peak show /hr suffix and value is monthly/730', async () => {
      const unitSelect = document.getElementById('savings-unit') as HTMLSelectElement;
      unitSelect.value = 'hourly';
      (getSavingsAnalytics as jest.Mock).mockResolvedValue(mockData);
      await loadSavingsHistory();

      const avgEl = document.getElementById('avg-hourly-savings');
      expect(avgEl?.textContent).toContain('/hr');
      // 730 / 730 = $1.00/hr
      expect(avgEl?.textContent).toContain('$1.00');
    });

    test('yearly unit -- avg and peak show /yr suffix and value is monthly*12', async () => {
      const unitSelect = document.getElementById('savings-unit') as HTMLSelectElement;
      unitSelect.value = 'yearly';
      (getSavingsAnalytics as jest.Mock).mockResolvedValue(mockData);
      await loadSavingsHistory();

      const avgEl = document.getElementById('avg-hourly-savings');
      expect(avgEl?.textContent).toContain('/yr');
      // 730 * 12 = $8760.00 -> formatted as $8.76K
      expect(avgEl?.textContent).toContain('$8.76K');
    });

    test('avg-savings-label heading changes to reflect selected unit', async () => {
      (getSavingsAnalytics as jest.Mock).mockResolvedValue(mockData);

      // Monthly (default)
      await loadSavingsHistory();
      expect(document.getElementById('avg-savings-label')?.textContent).toBe('Avg Monthly Savings');

      // Switch to hourly
      const unitSelect = document.getElementById('savings-unit') as HTMLSelectElement;
      unitSelect.value = 'hourly';
      await loadSavingsHistory();
      expect(document.getElementById('avg-savings-label')?.textContent).toBe('Avg Hourly Savings');

      // Switch to yearly
      unitSelect.value = 'yearly';
      await loadSavingsHistory();
      expect(document.getElementById('avg-savings-label')?.textContent).toBe('Avg Yearly Savings');
    });

    test('unit dropdown change triggers loadSavingsHistory via initSavingsHistory', async () => {
      (getSavingsAnalytics as jest.Mock).mockResolvedValue({ data_points: [] });
      initSavingsHistory();

      const unitSelect = document.getElementById('savings-unit') as HTMLSelectElement;
      unitSelect.value = 'hourly';
      unitSelect.dispatchEvent(new Event('change'));

      await new Promise(resolve => setTimeout(resolve, 50));
      expect(getSavingsAnalytics).toHaveBeenCalled();
    });

    test('initSavingsHistory called twice does not stack duplicate unit-change handlers', async () => {
      (getSavingsAnalytics as jest.Mock).mockResolvedValue({ data_points: [] });
      initSavingsHistory();
      initSavingsHistory();

      const unitSelect = document.getElementById('savings-unit') as HTMLSelectElement;
      unitSelect.value = 'hourly';
      (getSavingsAnalytics as jest.Mock).mockClear();
      unitSelect.dispatchEvent(new Event('change'));

      await new Promise(resolve => setTimeout(resolve, 50));
      // Should fire exactly once (no stacked duplicate listeners)
      expect(getSavingsAnalytics).toHaveBeenCalledTimes(1);
    });

    test('period savings (cumulative total) also converts with unit toggle', async () => {
      (getSavingsAnalytics as jest.Mock).mockResolvedValue(mockData);

      // Monthly: value is clean dollar total; label reflects view mode
      await loadSavingsHistory();
      const periodElMonthly = document.getElementById('period-savings');
      expect(periodElMonthly?.textContent).toBe('$730.00');
      expect(document.getElementById('period-savings-label')?.textContent).toContain('monthly');

      // Hourly: 730 / 730 = 1.00; label updates to 'hourly'
      const unitSelect = document.getElementById('savings-unit') as HTMLSelectElement;
      unitSelect.value = 'hourly';
      await loadSavingsHistory();
      expect(document.getElementById('period-savings')?.textContent).toBe('$1.00');
      expect(document.getElementById('period-savings-label')?.textContent).toContain('hourly');

      // Yearly: 730 * 12 = 8760 -> $8.76K; label updates to 'yearly'
      unitSelect.value = 'yearly';
      await loadSavingsHistory();
      expect(document.getElementById('period-savings')?.textContent).toBe('$8.76K');
      expect(document.getElementById('period-savings-label')?.textContent).toContain('yearly');
    });

    test('chart tooltip uses selected unit suffix for period savings dataset', async () => {
      const unitSelect = document.getElementById('savings-unit') as HTMLSelectElement;
      unitSelect.value = 'hourly';
      (getSavingsAnalytics as jest.Mock).mockResolvedValue(mockData);
      await loadSavingsHistory();

      const chartCall = (Chart as unknown as jest.Mock).mock.calls[0];
      const tooltipCb = chartCall[1].options.plugins.tooltip.callbacks.label;
      const ctx = { raw: 1.0, datasetIndex: 0, dataset: { label: 'Period Savings' } };
      expect(tooltipCb(ctx)).toContain('/hr');
    });
  });
});
