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

// Now import after mocking
import { loadSavingsHistory, initSavingsHistory, savingsChart } from '../modules/savings-history';
import { getSavingsAnalytics } from '../api';
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
      <button id="refresh-savings-btn">Refresh</button>
      <div id="savings-history-container">
        <canvas id="savings-history-chart"></canvas>
      </div>
      <div id="savings-history-empty" class="hidden">No data</div>
      <div id="savings-stats">
        <span id="period-savings">$0</span>
        <span id="avg-hourly-savings">$0/hr</span>
        <span id="peak-savings">$0/hr</span>
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
      expect(avgSavingsEl?.textContent).toContain('$75.00/hr');
      expect(peakSavingsEl?.textContent).toContain('$200.00/hr');
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

    test('tooltip label callback formats period savings with /hr suffix', async () => {
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
      expect(tooltipLabelCallback(periodContext)).toBe('Period Savings: $25.5678/hr');
    });

    test('tooltip label callback formats cumulative savings without /hr suffix', async () => {
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
      expect(tooltipLabelCallback(nullContext)).toBe('Period Savings: $0.0000/hr');

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
      expect(peakSavingsEl?.textContent).toBe('$150.00/hr');
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
});
