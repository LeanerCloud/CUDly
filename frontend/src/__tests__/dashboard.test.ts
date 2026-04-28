/**
 * Dashboard module tests
 */

// Mock Chart.js - must be done before import
// Q4/Q7: dashboard now uses showToast and confirmDialog instead of
// alert/confirm. Mock both so tests can assert on calls.
const mockShowToast = jest.fn<{ dismiss: () => void }, [unknown]>(() => ({ dismiss: jest.fn() }));
jest.mock('../toast', () => ({
  showToast: (opts: unknown) => mockShowToast(opts),
}));
const mockConfirmDialog = jest.fn<Promise<boolean>, [unknown]>(() => Promise.resolve(true));
jest.mock('../confirmDialog', () => ({
  confirmDialog: (opts: unknown) => mockConfirmDialog(opts),
}));

jest.mock('chart.js', () => {
  const MockChart = jest.fn().mockImplementation(() => ({
    destroy: jest.fn()
  }));
  (MockChart as unknown as { register: jest.Mock }).register = jest.fn();
  return {
    Chart: MockChart,
    registerables: []
  };
});

import { loadDashboard } from '../dashboard';
import { Chart } from 'chart.js';

// Mock the api module. `getSavingsAnalytics` is re-exported from
// ../api/history via ../api/index, so mock it here for dashboard's
// `import * as api from './api'` usage in loadSavingsTrendChart.
jest.mock('../api', () => ({
  getDashboardSummary: jest.fn(),
  getUpcomingPurchases: jest.fn(),
  getPurchaseDetails: jest.fn(),
  cancelPurchase: jest.fn(),
  listAccounts: jest.fn().mockResolvedValue([]),
  getSavingsAnalytics: jest.fn().mockResolvedValue({ data_points: [] }),
}));
import { loadSavingsTrendChart, setupSavingsTrendHandlers } from '../dashboard';

// Mock state module
jest.mock('../state', () => ({
  getCurrentProvider: jest.fn().mockReturnValue('all'),
  setCurrentProvider: jest.fn(),
  getCurrentAccountIDs: jest.fn().mockReturnValue([]),
  setCurrentAccountIDs: jest.fn(),
  getSavingsChart: jest.fn().mockReturnValue(null),
  setSavingsChart: jest.fn()
}));

// Mock utils
jest.mock('../utils', () => ({
  formatCurrency: jest.fn((val) => `$${val || 0}`),
  getDateParts: jest.fn(() => ({ day: 15, month: 'Jan' })),
  escapeHtml: jest.fn((str) => str || ''),
  populateAccountFilter: jest.fn(() => Promise.resolve())
}));

import * as api from '../api';
import * as state from '../state';

describe('Dashboard Module', () => {
  beforeEach(() => {
    // Reset DOM
    document.body.innerHTML = `
      <div id="summary"></div>
      <canvas id="savings-chart"></canvas>
      <div id="upcoming-list"></div>
    `;

    jest.clearAllMocks();
    window.alert = jest.fn();
    window.confirm = jest.fn().mockReturnValue(true);
    mockShowToast.mockClear();
    mockConfirmDialog.mockReset();
    mockConfirmDialog.mockImplementation(() => Promise.resolve(true));
  });

  describe('loadDashboard', () => {
    test('fetches dashboard summary and upcoming purchases', async () => {
      (api.getDashboardSummary as jest.Mock).mockResolvedValue({
        potential_monthly_savings: 1000,
        total_recommendations: 5,
        active_commitments: 3,
        committed_monthly: 500,
        current_coverage: 70,
        target_coverage: 80,
        ytd_savings: 5000,
        by_service: {}
      });
      (api.getUpcomingPurchases as jest.Mock).mockResolvedValue({
        purchases: []
      });

      await loadDashboard();

      expect(api.getDashboardSummary).toHaveBeenCalledWith('all', []);
      expect(api.getUpcomingPurchases).toHaveBeenCalled();
    });

    test('renders dashboard summary', async () => {
      (api.getDashboardSummary as jest.Mock).mockResolvedValue({
        potential_monthly_savings: 1000,
        total_recommendations: 5,
        active_commitments: 3,
        committed_monthly: 500,
        current_coverage: 70,
        target_coverage: 80,
        ytd_savings: 5000,
        by_service: {}
      });
      (api.getUpcomingPurchases as jest.Mock).mockResolvedValue({
        purchases: []
      });

      await loadDashboard();

      const summary = document.getElementById('summary');
      expect(summary?.innerHTML).toContain('Potential Monthly Savings');
      expect(summary?.innerHTML).toContain('Active Commitments');
      expect(summary?.innerHTML).toContain('Current Coverage');
      expect(summary?.innerHTML).toContain('YTD Savings');
    });

    test('renders savings chart', async () => {
      (api.getDashboardSummary as jest.Mock).mockResolvedValue({
        potential_monthly_savings: 1000,
        by_service: {
          ec2: { potential_savings: 500, current_savings: 200 },
          rds: { potential_savings: 300, current_savings: 100 }
        }
      });
      (api.getUpcomingPurchases as jest.Mock).mockResolvedValue({
        purchases: []
      });

      await loadDashboard();

      expect(Chart).toHaveBeenCalled();
      expect(state.setSavingsChart).toHaveBeenCalled();
    });

    test('destroys existing chart before creating new one', async () => {
      const mockChart = { destroy: jest.fn() };
      (state.getSavingsChart as jest.Mock).mockReturnValue(mockChart);

      (api.getDashboardSummary as jest.Mock).mockResolvedValue({
        potential_monthly_savings: 1000,
        by_service: {}
      });
      (api.getUpcomingPurchases as jest.Mock).mockResolvedValue({
        purchases: []
      });

      await loadDashboard();

      expect(mockChart.destroy).toHaveBeenCalled();
    });

    test('renders upcoming purchases', async () => {
      (api.getDashboardSummary as jest.Mock).mockResolvedValue({
        potential_monthly_savings: 1000,
        by_service: {}
      });
      (api.getUpcomingPurchases as jest.Mock).mockResolvedValue({
        purchases: [
          {
            execution_id: 'exec-1',
            plan_name: 'Test Plan',
            provider: 'aws',
            service: 'ec2',
            step_number: 1,
            total_steps: 4,
            estimated_savings: 100,
            scheduled_date: '2024-02-15'
          }
        ]
      });

      await loadDashboard();

      const upcomingList = document.getElementById('upcoming-list');
      expect(upcomingList?.innerHTML).toContain('Test Plan');
      expect(upcomingList?.innerHTML).toContain('Step 1 of 4');
    });

    test('shows empty message when no upcoming purchases', async () => {
      (api.getDashboardSummary as jest.Mock).mockResolvedValue({
        potential_monthly_savings: 1000,
        by_service: {}
      });
      (api.getUpcomingPurchases as jest.Mock).mockResolvedValue({
        purchases: []
      });

      await loadDashboard();

      const upcomingList = document.getElementById('upcoming-list');
      expect(upcomingList?.innerHTML).toContain('No upcoming scheduled purchases');
    });

    test('shows error on API failure', async () => {
      (api.getDashboardSummary as jest.Mock).mockRejectedValue(new Error('API Error'));
      console.error = jest.fn();

      await loadDashboard();

      const summary = document.getElementById('summary');
      expect(summary?.innerHTML).toContain('Failed to load dashboard');
    });

    test('uses current provider filter', async () => {
      (state.getCurrentProvider as jest.Mock).mockReturnValue('aws');
      (api.getDashboardSummary as jest.Mock).mockResolvedValue({
        potential_monthly_savings: 1000,
        by_service: {}
      });
      (api.getUpcomingPurchases as jest.Mock).mockResolvedValue({
        purchases: []
      });

      await loadDashboard();

      expect(api.getDashboardSummary).toHaveBeenCalledWith('aws', []);
    });

    test('renders view details and cancel buttons for upcoming purchases', async () => {
      (api.getDashboardSummary as jest.Mock).mockResolvedValue({
        potential_monthly_savings: 1000,
        by_service: {}
      });
      (api.getUpcomingPurchases as jest.Mock).mockResolvedValue({
        purchases: [
          {
            execution_id: 'exec-1',
            plan_name: 'Test Plan',
            provider: 'aws',
            service: 'ec2',
            step_number: 1,
            total_steps: 4,
            estimated_savings: 100,
            scheduled_date: '2024-02-15'
          }
        ]
      });

      await loadDashboard();

      const upcomingList = document.getElementById('upcoming-list');
      expect(upcomingList?.innerHTML).toContain('data-action="view-purchase"');
      expect(upcomingList?.innerHTML).toContain('data-action="cancel-purchase"');
    });

    test('handles missing canvas element gracefully', async () => {
      document.body.innerHTML = `
        <div id="summary"></div>
        <div id="upcoming-list"></div>
      `;

      (api.getDashboardSummary as jest.Mock).mockResolvedValue({
        potential_monthly_savings: 1000,
        by_service: { ec2: { potential_savings: 100, current_savings: 50 } }
      });
      (api.getUpcomingPurchases as jest.Mock).mockResolvedValue({
        purchases: []
      });

      await expect(loadDashboard()).resolves.not.toThrow();
    });

    test('view purchase button shows details', async () => {
      (api.getDashboardSummary as jest.Mock).mockResolvedValue({
        potential_monthly_savings: 1000,
        by_service: {}
      });
      (api.getUpcomingPurchases as jest.Mock).mockResolvedValue({
        purchases: [
          {
            execution_id: 'exec-123',
            plan_name: 'Test Plan',
            provider: 'aws',
            service: 'ec2',
            step_number: 1,
            total_steps: 4,
            estimated_savings: 100,
            scheduled_date: '2024-02-15'
          }
        ]
      });
      (api.getPurchaseDetails as jest.Mock).mockResolvedValue({
        execution_id: 'exec-123',
        status: 'pending'
      });

      await loadDashboard();

      const viewBtn = document.querySelector('[data-action="view-purchase"]') as HTMLButtonElement;
      viewBtn?.click();

      await new Promise(resolve => setTimeout(resolve, 50));

      expect(api.getPurchaseDetails).toHaveBeenCalledWith('exec-123');
      // Modal should be rendered in the DOM instead of alert
      const modal = document.getElementById('purchase-details-modal');
      expect(modal).toBeTruthy();
      expect(modal?.textContent).toContain('exec-123');
      expect(modal?.textContent).toContain('pending');
    });

    test('view purchase button shows error on failure', async () => {
      (api.getDashboardSummary as jest.Mock).mockResolvedValue({
        potential_monthly_savings: 1000,
        by_service: {}
      });
      (api.getUpcomingPurchases as jest.Mock).mockResolvedValue({
        purchases: [
          {
            execution_id: 'exec-123',
            plan_name: 'Test Plan',
            provider: 'aws',
            service: 'ec2',
            step_number: 1,
            total_steps: 4,
            estimated_savings: 100,
            scheduled_date: '2024-02-15'
          }
        ]
      });
      (api.getPurchaseDetails as jest.Mock).mockRejectedValue(new Error('API Error'));
      console.error = jest.fn();

      await loadDashboard();

      const viewBtn = document.querySelector('[data-action="view-purchase"]') as HTMLButtonElement;
      viewBtn?.click();

      await new Promise(resolve => setTimeout(resolve, 50));

      // Q7: alert() migrated to showToast with kind:'error'.
      expect(mockShowToast).toHaveBeenCalledWith(expect.objectContaining({
        message: 'Failed to load purchase details: API Error',
        kind: 'error',
      }));
    });

    test('cancel purchase button cancels and reloads', async () => {
      (api.getDashboardSummary as jest.Mock).mockResolvedValue({
        potential_monthly_savings: 1000,
        by_service: {}
      });
      (api.getUpcomingPurchases as jest.Mock).mockResolvedValue({
        purchases: [
          {
            execution_id: 'exec-123',
            plan_name: 'Test Plan',
            provider: 'aws',
            service: 'ec2',
            step_number: 1,
            total_steps: 4,
            estimated_savings: 100,
            scheduled_date: '2024-02-15'
          }
        ]
      });
      (api.cancelPurchase as jest.Mock).mockResolvedValue({});
      window.confirm = jest.fn().mockReturnValue(true);

      await loadDashboard();

      // Reset mocks to track the reload
      (api.getDashboardSummary as jest.Mock).mockClear();
      (api.getUpcomingPurchases as jest.Mock).mockResolvedValue({ purchases: [] });

      const cancelBtn = document.querySelector('[data-action="cancel-purchase"]') as HTMLButtonElement;
      cancelBtn?.click();

      await new Promise(resolve => setTimeout(resolve, 50));

      expect(api.cancelPurchase).toHaveBeenCalledWith('exec-123');
      expect(mockShowToast).toHaveBeenCalledWith(expect.objectContaining({
        message: 'Purchase cancelled successfully',
        kind: 'success',
      }));
    });

    test('cancel purchase does nothing if user declines confirmation', async () => {
      (api.getDashboardSummary as jest.Mock).mockResolvedValue({
        potential_monthly_savings: 1000,
        by_service: {}
      });
      (api.getUpcomingPurchases as jest.Mock).mockResolvedValue({
        purchases: [
          {
            execution_id: 'exec-123',
            plan_name: 'Test Plan',
            provider: 'aws',
            service: 'ec2',
            step_number: 1,
            total_steps: 4,
            estimated_savings: 100,
            scheduled_date: '2024-02-15'
          }
        ]
      });
      mockConfirmDialog.mockResolvedValueOnce(false);

      await loadDashboard();

      const cancelBtn = document.querySelector('[data-action="cancel-purchase"]') as HTMLButtonElement;
      cancelBtn?.click();

      await new Promise(resolve => setTimeout(resolve, 50));

      expect(api.cancelPurchase).not.toHaveBeenCalled();
    });

    test('cancel purchase shows error on failure', async () => {
      (api.getDashboardSummary as jest.Mock).mockResolvedValue({
        potential_monthly_savings: 1000,
        by_service: {}
      });
      (api.getUpcomingPurchases as jest.Mock).mockResolvedValue({
        purchases: [
          {
            execution_id: 'exec-123',
            plan_name: 'Test Plan',
            provider: 'aws',
            service: 'ec2',
            step_number: 1,
            total_steps: 4,
            estimated_savings: 100,
            scheduled_date: '2024-02-15'
          }
        ]
      });
      (api.cancelPurchase as jest.Mock).mockRejectedValue(new Error('API Error'));
      window.confirm = jest.fn().mockReturnValue(true);
      console.error = jest.fn();

      await loadDashboard();

      const cancelBtn = document.querySelector('[data-action="cancel-purchase"]') as HTMLButtonElement;
      cancelBtn?.click();

      await new Promise(resolve => setTimeout(resolve, 50));

      expect(mockShowToast).toHaveBeenCalledWith(expect.objectContaining({
        message: 'Failed to cancel purchase',
        kind: 'error',
      }));
    });
  });

  describe('savings-trend chart', () => {
    beforeEach(() => {
      const canvas = document.createElement('canvas');
      canvas.id = 'savings-trend-chart';
      const empty = document.createElement('div');
      empty.id = 'savings-trend-empty';
      empty.className = 'hidden';
      const b90 = document.createElement('button');
      b90.className = 'trend-range active';
      b90.dataset['range'] = '90';
      b90.textContent = '90d';
      const b30 = document.createElement('button');
      b30.className = 'trend-range';
      b30.dataset['range'] = '30';
      b30.textContent = '30d';
      document.body.replaceChildren(canvas, empty, b90, b30);
      (api.getSavingsAnalytics as jest.Mock).mockResolvedValue({ data_points: [] });
    });

    test('uses daily interval for the default 90-day range', async () => {
      await loadSavingsTrendChart();

      expect(api.getSavingsAnalytics).toHaveBeenCalledWith(
        expect.objectContaining({ interval: 'daily' })
      );
    });

    test('clicking a different trend-range button re-fetches with the new bucket', async () => {
      setupSavingsTrendHandlers();
      (api.getSavingsAnalytics as jest.Mock).mockClear();

      (document.querySelector('.trend-range[data-range="30"]') as HTMLButtonElement).click();
      await new Promise(r => setTimeout(r, 0));

      expect(api.getSavingsAnalytics).toHaveBeenCalledWith(
        expect.objectContaining({ interval: 'daily' })
      );
      const call = (api.getSavingsAnalytics as jest.Mock).mock.calls[0]?.[0];
      const span = new Date(call.end).getTime() - new Date(call.start).getTime();
      expect(Math.round(span / 86400_000)).toBe(30);
    });
  });

  // Issue #185: provider-change handler must clear state.currentAccountIDs
  // before loadDashboard reads it, AND must await populateAccountFilter
  // so the dropdown is in a consistent state with the post-load state.
  describe('Issue #185: provider switch clears stale account state before reload', () => {
    let setupDashboardHandlers: typeof import('../dashboard').setupDashboardHandlers;

    beforeEach(async () => {
      setupDashboardHandlers = (await import('../dashboard')).setupDashboardHandlers;
      // Build the test DOM via createElement (avoids innerHTML).
      document.body.replaceChildren();
      const providerSel = document.createElement('select');
      providerSel.id = 'dashboard-provider-filter';
      for (const [v, t] of [['', 'All'], ['aws', 'AWS'], ['azure', 'Azure']] as const) {
        const opt = document.createElement('option');
        opt.value = v; opt.textContent = t;
        providerSel.appendChild(opt);
      }
      document.body.appendChild(providerSel);
      const acctSel = document.createElement('select');
      acctSel.id = 'dashboard-account-filter';
      for (const [v, t] of [['', '(All accounts)'], ['aws-acct-1', 'aws-acct-1']] as const) {
        const opt = document.createElement('option');
        opt.value = v; opt.textContent = t;
        acctSel.appendChild(opt);
      }
      document.body.appendChild(acctSel);
      const summaryDiv = document.createElement('div');
      summaryDiv.id = 'summary';
      document.body.appendChild(summaryDiv);
      const upcomingDiv = document.createElement('div');
      upcomingDiv.id = 'upcoming-list';
      document.body.appendChild(upcomingDiv);
      const canvas = document.createElement('canvas');
      canvas.id = 'savings-chart';
      document.body.appendChild(canvas);

      // Pre-load with an AWS account selected so we can prove the
      // handler clears it.
      (state.getCurrentProvider as jest.Mock).mockReturnValue('aws');
      (state.getCurrentAccountIDs as jest.Mock).mockReturnValue(['aws-acct-1']);
      (api.getDashboardSummary as jest.Mock).mockResolvedValue({
        potential_monthly_savings: 0, total_recommendations: 0, active_commitments: 0,
        committed_monthly: 0, current_coverage: 0, target_coverage: 0, ytd_savings: 0,
        by_service: {}
      });
      (api.getUpcomingPurchases as jest.Mock).mockResolvedValue({ purchases: [] });
    });

    test('switching provider clears state.currentAccountIDs and dispatches loadDashboard', async () => {
      setupDashboardHandlers();
      const providerSel = document.getElementById('dashboard-provider-filter') as HTMLSelectElement;
      providerSel.value = 'azure';
      providerSel.dispatchEvent(new Event('change'));

      // Wait for the async handler chain to settle.
      await new Promise(r => setTimeout(r, 0));
      await new Promise(r => setTimeout(r, 0));

      expect(state.setCurrentProvider).toHaveBeenCalledWith('azure');
      // setCurrentAccountIDs([]) must be called so loadDashboard reads
      // a clean state.
      const clearCalls = (state.setCurrentAccountIDs as jest.Mock).mock.calls;
      expect(clearCalls.some((c: unknown[]) => Array.isArray(c[0]) && (c[0] as unknown[]).length === 0)).toBe(true);
      // The dashboard summary call must run AFTER the clear.
      expect(api.getDashboardSummary).toHaveBeenCalled();
    });

    test('switching provider awaits populateAccountFilter before reloading the dashboard', async () => {
      let resolvePopulate: (() => void) | undefined;
      const populateBlocker = new Promise<void>((resolve) => { resolvePopulate = resolve; });
      const utils = await import('../utils');
      // setupDashboardHandlers runs an init populateAccountFilter call
      // before any user interaction — let that one resolve immediately,
      // and only block the second call (the provider-change one).
      (utils.populateAccountFilter as jest.Mock)
        .mockImplementationOnce(() => Promise.resolve())
        .mockImplementationOnce(() => populateBlocker);

      setupDashboardHandlers();
      // Yield so the init populate + setupSavingsTrendHandlers settle
      // (they don't call getDashboardSummary, so the assertion below
      // is sound).
      await new Promise(r => setTimeout(r, 0));
      const providerSel = document.getElementById('dashboard-provider-filter') as HTMLSelectElement;
      providerSel.value = 'azure';
      providerSel.dispatchEvent(new Event('change'));

      // Yield once — populateAccountFilter is pending; getDashboardSummary
      // must NOT have fired yet.
      await new Promise(r => setTimeout(r, 0));
      const callsBefore = (api.getDashboardSummary as jest.Mock).mock.calls.length;
      expect(callsBefore).toBe(0);

      // Resolve populate; loadDashboard runs after.
      resolvePopulate!();
      await new Promise(r => setTimeout(r, 0));
      await new Promise(r => setTimeout(r, 0));

      const callsAfter = (api.getDashboardSummary as jest.Mock).mock.calls.length;
      expect(callsAfter).toBeGreaterThan(0);
    });
  });
});
