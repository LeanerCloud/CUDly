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

// #293: mock the recommendations helpers imported by dashboard.ts so
// tests can control the range computation without loading the full
// recommendations module (which itself imports chart.js etc.).
const mockGroupRecsByCell = jest.fn((recs: unknown[]) => new Map(recs.length ? [['cell-1', recs]] : []));
const mockPageLevelRange = jest.fn((groups: Map<string, unknown[]>) => {
  if (groups.size === 0) return { savingsMin: 0, savingsMax: 0, cellCount: 0 };
  return { savingsMin: 300, savingsMax: 400, cellCount: groups.size };
});
const mockFormatSavingsRange = jest.fn((min: number, max: number) => min === max ? `$${min}` : `$${min} – $${max}`);
jest.mock('../recommendations', () => ({
  groupRecsByCell: (recs: unknown[]) => mockGroupRecsByCell(recs),
  pageLevelRange: (groups: Map<string, unknown[]>) => mockPageLevelRange(groups),
  formatSavingsRange: (min: number, max: number) => mockFormatSavingsRange(min, max),
  // Freshness-bar deletion (#284 follow-up): dashboard's loadDashboard
  // now calls triggerAutoRefreshIfStale(loadDashboard) so the 24h
  // auto-refresh + collection-error toast also fire on Home, not just
  // on Opportunities. Stub it as a resolved no-op so dashboard tests
  // don't see a real /freshness round-trip.
  triggerAutoRefreshIfStale: jest.fn(() => Promise.resolve()),
}));

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
  // Plan-level cancel — the dashboard upcoming-list targets plan
  // endpoints, not execution endpoints. PR #207 first wired this to
  // deletePlannedPurchase but that handler still operates on
  // purchase_executions; the correct endpoint is DELETE /api/plans/{id}
  // via api.deletePlan (issues #204 / #205 / #208).
  deletePlannedPurchase: jest.fn(),
  deletePlan: jest.fn(),
  listAccounts: jest.fn().mockResolvedValue([]),
  getSavingsAnalytics: jest.fn().mockResolvedValue({ data_points: [] }),
  // #293: dashboard fetches recs for per-cell savings range. Default to
  // empty list so existing tests that don't care about the savings card
  // value don't need to be updated.
  getRecommendations: jest.fn().mockResolvedValue([]),
}));
import { loadSavingsTrendChart, setupSavingsTrendHandlers, setupDashboardHandlers } from '../dashboard';

// Mock state module
jest.mock('../state', () => ({
  // Issue #344 T2: AppState.currentProvider is `api.Provider | ''` —
  // '' is the topbar's "All Providers" sentinel, not the string 'all'.
  getCurrentProvider: jest.fn().mockReturnValue(''),
  setCurrentProvider: jest.fn(),
  getCurrentAccountIDs: jest.fn().mockReturnValue([]),
  setCurrentAccountIDs: jest.fn(),
  getSavingsChart: jest.fn().mockReturnValue(null),
  setSavingsChart: jest.fn(),
  // Global topbar filter subscriptions. Each section calls subscribe*
  // during setup to register its reload callback.
  subscribeProvider: jest.fn().mockReturnValue(() => {}),
  subscribeAccount: jest.fn().mockReturnValue(() => {}),
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
    // Re-initialize recommendation mocks after clearAllMocks().
    mockGroupRecsByCell.mockImplementation((recs: unknown[]) => new Map(recs.length ? [['cell-1', recs]] : []));
    mockPageLevelRange.mockImplementation((groups: Map<string, unknown[]>) => {
      if (groups.size === 0) return { savingsMin: 0, savingsMax: 0, cellCount: 0 };
      return { savingsMin: 300, savingsMax: 400, cellCount: groups.size };
    });
    mockFormatSavingsRange.mockImplementation((min: number, max: number) => min === max ? `$${min}` : `$${min} – $${max}`);
    // Default: empty recs so existing tests are unaffected by the range.
    (api.getRecommendations as jest.Mock).mockResolvedValue([]);
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

      // Issue #344 T2: AppState.currentProvider is `api.Provider | ''`,
      // not a literal 'all' — '' is the "All Providers" sentinel.
      expect(api.getDashboardSummary).toHaveBeenCalledWith('', []);
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
            execution_id: 'exec-1', plan_id: 'plan-1',
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

    // Issue #344 T3: skeleton lifecycle — skeleton renders synchronously
    // at fetch start, then is replaced by the success render (clean
    // handoff) or torn down on error so it never sits next to a stale
    // error message.
    test('error path tears down the loading skeleton', async () => {
      (api.getDashboardSummary as jest.Mock).mockRejectedValue(new Error('API Error'));
      console.error = jest.fn();

      await loadDashboard();

      const summary = document.getElementById('summary');
      expect(summary?.querySelector('.skeleton-tile')).toBeNull();
      expect(summary?.dataset['skeletonActive']).toBeUndefined();
    });

    test('success path replaces the loading skeleton with KPI tiles', async () => {
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
      (api.getUpcomingPurchases as jest.Mock).mockResolvedValue({ purchases: [] });

      await loadDashboard();

      const summary = document.getElementById('summary');
      // Real KPI tiles render — skeleton placeholders are gone.
      expect(summary?.querySelectorAll('.kpi-tile').length).toBeGreaterThan(0);
      expect(summary?.querySelector('.skeleton-tile')).toBeNull();
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
            execution_id: 'exec-1', plan_id: 'plan-1',
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
            execution_id: 'exec-123', plan_id: 'plan-123',
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

      const viewBtn = document.querySelector('[data-action="view-purchase"]') as HTMLButtonElement;
      viewBtn?.click();

      // viewPurchaseDetails is sync now — renders from the in-memory
      // upcoming-purchases index (issues #204 + #205). No API call to
      // /api/purchases/{id}, since the row identifier is a plan_id and
      // there's no execution row yet for an upcoming purchase.
      expect(api.getPurchaseDetails).not.toHaveBeenCalled();
      const modal = document.getElementById('purchase-details-modal');
      expect(modal).toBeTruthy();
      expect(modal?.textContent).toContain('Test Plan');
      expect(modal?.textContent).toContain('exec-123');
    });

    // (The previous "shows error on failure" test exercised an API
    // rejection path that no longer exists — viewPurchaseDetails is
    // synchronous after the data-flow fix. The graceful-fallback for a
    // pruned-from-index plan_id is enforced by the `if (!purchase) {
    // showToast(...) }` guard in dashboard.ts but isn't easily reached
    // through the public surface, so it's covered by code-review only.)

    test('cancel purchase button cancels and reloads', async () => {
      (api.getDashboardSummary as jest.Mock).mockResolvedValue({
        potential_monthly_savings: 1000,
        by_service: {}
      });
      (api.getUpcomingPurchases as jest.Mock).mockResolvedValue({
        purchases: [
          {
            execution_id: 'exec-123', plan_id: 'plan-123',
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
      (api.deletePlannedPurchase as jest.Mock).mockResolvedValue({});
      window.confirm = jest.fn().mockReturnValue(true);

      await loadDashboard();

      // Reset mocks to track the reload
      (api.getDashboardSummary as jest.Mock).mockClear();
      (api.getUpcomingPurchases as jest.Mock).mockResolvedValue({ purchases: [] });

      const cancelBtn = document.querySelector('[data-action="cancel-purchase"]') as HTMLButtonElement;
      cancelBtn?.click();

      await new Promise(resolve => setTimeout(resolve, 50));

      expect(api.deletePlannedPurchase).toHaveBeenCalledWith('exec-123');
      // Cancel must NOT delete the entire plan — that was the wrong fix
      // in PR #207. deletePlan should not be called from this path.
      expect(api.deletePlan).not.toHaveBeenCalled();
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
            execution_id: 'exec-123', plan_id: 'plan-123',
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

      expect(api.deletePlannedPurchase).not.toHaveBeenCalled();
    });

    test('cancel purchase shows error on failure', async () => {
      (api.getDashboardSummary as jest.Mock).mockResolvedValue({
        potential_monthly_savings: 1000,
        by_service: {}
      });
      (api.getUpcomingPurchases as jest.Mock).mockResolvedValue({
        purchases: [
          {
            execution_id: 'exec-123', plan_id: 'plan-123',
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
      (api.deletePlannedPurchase as jest.Mock).mockRejectedValue(new Error('API Error'));
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

    // #293: Potential Monthly Savings card renders per-cell range, not
    // the flat summary.potential_monthly_savings value.
    test('Potential Monthly Savings card renders per-cell range (#293)', async () => {
      // Two cells, four variants — flat sum would be 700 but per-cell range
      // is $300 – $400. The mock helpers are already configured to return
      // savingsMin=300, savingsMax=400 when recs is non-empty.
      const mockRecs = [
        { id: 'r1', provider: 'aws', service: 'ec2', region: 'us-east-1', resource_type: 't3.medium', term: 1, savings: 150, upfront_cost: 0, count: 1 },
        { id: 'r2', provider: 'aws', service: 'ec2', region: 'us-east-1', resource_type: 't3.medium', term: 3, savings: 200, upfront_cost: 100, count: 1 },
        { id: 'r3', provider: 'aws', service: 'rds', region: 'us-east-1', resource_type: 'db.t3.medium', term: 1, savings: 100, upfront_cost: 0, count: 1 },
        { id: 'r4', provider: 'aws', service: 'rds', region: 'us-east-1', resource_type: 'db.t3.medium', term: 3, savings: 250, upfront_cost: 200, count: 1 },
      ];
      (api.getRecommendations as jest.Mock).mockResolvedValue(mockRecs);
      (api.getDashboardSummary as jest.Mock).mockResolvedValue({
        potential_monthly_savings: 700, // flat (overcounted) sum — must NOT appear
        total_recommendations: 4,
        active_commitments: 0,
        committed_monthly: 0,
        current_coverage: 0,
        target_coverage: 80,
        ytd_savings: 0,
        by_service: {}
      });
      (api.getUpcomingPurchases as jest.Mock).mockResolvedValue({ purchases: [] });

      await loadDashboard();

      const savingsCard = document.querySelector('#summary .card');
      expect(savingsCard?.textContent).toContain('$300');
      expect(savingsCard?.textContent).toContain('$400');
      // The overcounted flat sum must NOT appear in the savings card.
      expect(savingsCard?.textContent).not.toContain('$700');
    });

    // #293: If getRecommendations rejects, other cards still render.
    test('failure-isolation: rec fetch failure leaves other cards rendered (#293)', async () => {
      (api.getRecommendations as jest.Mock).mockRejectedValue(new Error('Network error'));
      (api.getDashboardSummary as jest.Mock).mockResolvedValue({
        potential_monthly_savings: 500,
        total_recommendations: 2,
        active_commitments: 3,
        committed_monthly: 200,
        current_coverage: 60,
        target_coverage: 80,
        ytd_savings: 1000,
        by_service: {}
      });
      (api.getUpcomingPurchases as jest.Mock).mockResolvedValue({ purchases: [] });

      await loadDashboard();

      const summary = document.getElementById('summary');
      // Other cards must still render normally.
      expect(summary?.innerHTML).toContain('Active Commitments');
      expect(summary?.innerHTML).toContain('Current Coverage');
      expect(summary?.innerHTML).toContain('YTD Savings');
      // Savings card must fall back to $0 (not throw or go blank).
      const savingsCard = summary?.querySelector('.card');
      expect(savingsCard?.textContent).toContain('Potential Monthly Savings');
      // mockPageLevelRange returns cellCount=0 for empty groups, so formatCurrency(0) = '$0'
      expect(savingsCard?.innerHTML).toContain('$0');
    });

    // #293: summary.potential_monthly_savings is no longer the source for
    // the savings card — the range from recs is used instead.
    test('legacy summary.potential_monthly_savings is no longer read for the savings card (#293)', async () => {
      // recs give $300 – $400 range (per mock); summary says $700.
      const mockRecs = [
        { id: 'r1', provider: 'aws', service: 'ec2', region: 'us-east-1', resource_type: 't3', term: 1, savings: 300, upfront_cost: 0, count: 1 },
      ];
      (api.getRecommendations as jest.Mock).mockResolvedValue(mockRecs);
      (api.getDashboardSummary as jest.Mock).mockResolvedValue({
        potential_monthly_savings: 700,
        total_recommendations: 1,
        active_commitments: 0,
        committed_monthly: 0,
        current_coverage: 0,
        target_coverage: 80,
        ytd_savings: 0,
        by_service: {}
      });
      (api.getUpcomingPurchases as jest.Mock).mockResolvedValue({ purchases: [] });

      await loadDashboard();

      const savingsCard = document.querySelector('#summary .card');
      // The savings card must NOT show the legacy flat sum.
      expect(savingsCard?.textContent).not.toContain('$700');
      // It must show the range from recs ($300 – $400 per mock).
      expect(savingsCard?.textContent).toContain('$300');
      expect(savingsCard?.textContent).toContain('$400');
    });

    // #304: getRecommendations returns null (apiRequest catch-block fallback
    // when response.json() fails on a 2xx with empty/non-JSON body). The
    // Array.isArray guard in loadDashboard must coerce null → [] so
    // groupRecsByCell never receives a non-iterable value.
    test('#304: getRecommendations returning null does not throw "is not iterable"', async () => {
      (api.getRecommendations as jest.Mock).mockResolvedValue(null);
      (api.getDashboardSummary as jest.Mock).mockResolvedValue({
        potential_monthly_savings: 500,
        total_recommendations: 2,
        active_commitments: 1,
        committed_monthly: 100,
        current_coverage: 50,
        target_coverage: 80,
        ytd_savings: 200,
        by_service: {}
      });
      (api.getUpcomingPurchases as jest.Mock).mockResolvedValue({ purchases: [] });

      // Must not throw.
      await expect(loadDashboard()).resolves.toBeUndefined();

      // The rest of the dashboard must still render.
      const summary = document.getElementById('summary');
      expect(summary?.innerHTML).toContain('Active Commitments');
      expect(summary?.innerHTML).toContain('Current Coverage');
      expect(summary?.innerHTML).toContain('YTD Savings');

      // Savings card falls back to $0 when recs are absent.
      const savingsCard = summary?.querySelector('.card');
      expect(savingsCard?.textContent).toContain('Potential Monthly Savings');
      expect(savingsCard?.innerHTML).toContain('$0');
    });

    // #304: getRecommendations returns a non-array object (e.g. a wrapped
    // envelope or unexpected backend shape). Same coercion requirement.
    test('#304: getRecommendations returning a non-array object does not throw "is not iterable"', async () => {
      // Simulate an unexpected envelope shape from the backend.
      (api.getRecommendations as jest.Mock).mockResolvedValue({ recommendations: [], total: 0 } as unknown);
      (api.getDashboardSummary as jest.Mock).mockResolvedValue({
        potential_monthly_savings: 400,
        total_recommendations: 0,
        active_commitments: 0,
        committed_monthly: 0,
        current_coverage: 0,
        target_coverage: 80,
        ytd_savings: 0,
        by_service: {}
      });
      (api.getUpcomingPurchases as jest.Mock).mockResolvedValue({ purchases: [] });

      await expect(loadDashboard()).resolves.toBeUndefined();

      const summary = document.getElementById('summary');
      expect(summary?.innerHTML).toContain('Active Commitments');

      // Savings card falls back to $0.
      const savingsCard = summary?.querySelector('.card');
      expect(savingsCard?.innerHTML).toContain('$0');
    });

    // #304: summaryData.by_service missing entirely (null/undefined from
    // backend). renderSavingsChart receives `undefined || {}` = {} which
    // is safe; verify no throw and the error banner does not appear.
    test('#304: summaryData missing by_service field does not throw', async () => {
      (api.getDashboardSummary as jest.Mock).mockResolvedValue({
        potential_monthly_savings: 0,
        total_recommendations: 0,
        active_commitments: 0,
        committed_monthly: 0,
        current_coverage: 0,
        target_coverage: 80,
        ytd_savings: 0,
        // by_service intentionally omitted
      });
      (api.getUpcomingPurchases as jest.Mock).mockResolvedValue({ purchases: [] });

      await expect(loadDashboard()).resolves.toBeUndefined();

      const summary = document.getElementById('summary');
      // The error banner must NOT appear.
      expect(summary?.querySelector('.error')).toBeNull();
      expect(summary?.innerHTML).toContain('Active Commitments');
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

  // QA row 384 step 2.3: Home page Savings-over-time chart must honor the
  // global Account filter (issue #701). These tests verify that:
  //   1. setupDashboardHandlers subscribes to both filter chips.
  //   2. An account-chip change re-fetches when Home tab is active.
  //   3. No re-fetch fires when Home tab is inactive.
  //   4. Back-to-back provider+account fires coalesce into one reload.
  //   5. The account_id is forwarded to getSavingsAnalytics.
  //   6. Filter-aware empty-state copy is shown when a filter is active.
  describe('setupDashboardHandlers — filter chip subscriptions (QA 2.3)', () => {
    function addHomeTab(active: boolean): void {
      const div = document.createElement('div');
      div.id = 'home-tab';
      if (active) div.classList.add('active');
      document.body.appendChild(div);
    }

    beforeEach(() => {
      (state.subscribeProvider as jest.Mock).mockClear();
      (state.subscribeAccount as jest.Mock).mockClear();
      (api.getSavingsAnalytics as jest.Mock).mockClear();
      (api.getDashboardSummary as jest.Mock).mockResolvedValue({
        potential_monthly_savings: 0, total_recommendations: 0,
        active_commitments: 0, committed_monthly: 0, current_coverage: 0,
        target_coverage: 80, ytd_savings: 0, by_service: {},
      });
      (api.getUpcomingPurchases as jest.Mock).mockResolvedValue({ purchases: [] });
      (api.getRecommendations as jest.Mock).mockResolvedValue([]);
      (api.getSavingsAnalytics as jest.Mock).mockResolvedValue({ data_points: [] });

      // Ensure a canvas for savings-trend-chart exists so loadSavingsTrendChart
      // does not bail out at the early-return guard.
      const canvas = document.createElement('canvas');
      canvas.id = 'savings-trend-chart';
      const empty = document.createElement('p');
      empty.id = 'savings-trend-empty';
      empty.className = 'hidden';
      document.body.appendChild(canvas);
      document.body.appendChild(empty);
    });

    test('registers one callback each with subscribeProvider and subscribeAccount', () => {
      setupDashboardHandlers();

      expect(state.subscribeProvider).toHaveBeenCalledTimes(1);
      expect(state.subscribeAccount).toHaveBeenCalledTimes(1);
      expect(typeof (state.subscribeProvider as jest.Mock).mock.calls[0]?.[0]).toBe('function');
      expect(typeof (state.subscribeAccount as jest.Mock).mock.calls[0]?.[0]).toBe('function');
    });

    test('account chip change triggers loadDashboard (and getSavingsAnalytics) when home tab is active', async () => {
      addHomeTab(true);
      (state.getCurrentAccountIDs as jest.Mock).mockReturnValue(['uuid-acct-1']);

      setupDashboardHandlers();
      const accountCb = (state.subscribeAccount as jest.Mock).mock.calls[0]?.[0] as () => void;

      (api.getSavingsAnalytics as jest.Mock).mockClear();
      accountCb();
      await new Promise((r) => setTimeout(r, 0));

      expect(api.getSavingsAnalytics).toHaveBeenCalledTimes(1);
      expect(api.getSavingsAnalytics).toHaveBeenCalledWith(
        expect.objectContaining({ account_ids: ['uuid-acct-1'] })
      );
    });

    test('does NOT fire when home tab is inactive (active-tab guard)', async () => {
      addHomeTab(false);

      setupDashboardHandlers();
      const accountCb = (state.subscribeAccount as jest.Mock).mock.calls[0]?.[0] as () => void;
      const providerCb = (state.subscribeProvider as jest.Mock).mock.calls[0]?.[0] as () => void;

      (api.getSavingsAnalytics as jest.Mock).mockClear();
      (api.getDashboardSummary as jest.Mock).mockClear();
      accountCb();
      providerCb();
      await new Promise((r) => setTimeout(r, 0));

      expect(api.getDashboardSummary).not.toHaveBeenCalled();
    });

    test('back-to-back provider+account fires coalesce into one reload', async () => {
      addHomeTab(true);

      setupDashboardHandlers();
      const providerCb = (state.subscribeProvider as jest.Mock).mock.calls[0]?.[0] as () => void;
      const accountCb = (state.subscribeAccount as jest.Mock).mock.calls[0]?.[0] as () => void;

      (api.getDashboardSummary as jest.Mock).mockClear();
      // Simulate topbar provider-change: clears accounts then sets provider,
      // per the #185 ordering rule — both fire synchronously.
      accountCb();
      providerCb();
      await new Promise((r) => setTimeout(r, 0));

      expect(api.getDashboardSummary).toHaveBeenCalledTimes(1);
    });

    test('loadSavingsTrendChart forwards account_id to the analytics API', async () => {
      (state.getCurrentAccountIDs as jest.Mock).mockReturnValue(['uuid-acct-2']);

      await loadSavingsTrendChart();

      expect(api.getSavingsAnalytics).toHaveBeenCalledWith(
        expect.objectContaining({ account_ids: ['uuid-acct-2'] })
      );
    });

    test('empty-state shows filter name when account chip is active (QA 2.3)', async () => {
      (state.getCurrentAccountIDs as jest.Mock).mockReturnValue(['uuid-acct-3']);
      (api.getSavingsAnalytics as jest.Mock).mockResolvedValue({ data_points: [] });

      await loadSavingsTrendChart();

      const empty = document.getElementById('savings-trend-empty');
      expect(empty?.classList.contains('hidden')).toBe(false);
      expect(empty?.textContent).toContain('uuid-acct-3');
    });

    test('empty-state shows generic message when no filter is active', async () => {
      (state.getCurrentAccountIDs as jest.Mock).mockReturnValue([]);
      (state.getCurrentProvider as jest.Mock).mockReturnValue('');
      (api.getSavingsAnalytics as jest.Mock).mockResolvedValue({ data_points: [] });

      await loadSavingsTrendChart();

      const empty = document.getElementById('savings-trend-empty');
      expect(empty?.classList.contains('hidden')).toBe(false);
      expect(empty?.textContent).toContain('No purchase history yet');
    });
  });

  // Issue #185 invariant — clear account state BEFORE awaiting the
  // account-list refetch on provider change — moved from dashboard.ts
  // to topbar-filters.ts as part of issue #344 T2. The dashboard no
  // longer owns the provider-change handler; it just reloads in
  // response to state.subscribeProvider. The ordering test now lives
  // alongside the owning module in topbar-filters.test.ts. See
  // src/topbar-filters.ts::initTopbarFilters for the implementation.
  describe.skip('Issue #185: provider switch clears stale account state before reload (moved to topbar-filters.test.ts)', () => {
    test('placeholder — see topbar-filters.test.ts', () => {});
  });

  // KPI tile sparklines (issue #340 T6).
  describe('sparkline helpers', () => {
    // eslint-disable-next-line @typescript-eslint/no-var-requires
    const { __test__ } = require('../dashboard') as { __test__: {
      sparklinePoints: (values: readonly number[], w: number, h: number) => string;
      attachSparkline: (key: string, values: readonly number[]) => void;
    } };

    test('sparklinePoints normalizes values into the viewport', () => {
      const pts = __test__.sparklinePoints([0, 50, 100], 80, 24);
      const tokens = pts.split(' ');
      expect(tokens).toHaveLength(3);
      // First point: x=0, y=height (lowest value).
      expect(tokens[0]).toBe('0.0,24.0');
      // Last point: x=width, y=0 (highest value).
      expect(tokens[2]).toBe('80.0,0.0');
    });

    test('sparklinePoints returns empty string for <2 values', () => {
      expect(__test__.sparklinePoints([], 80, 24)).toBe('');
      expect(__test__.sparklinePoints([42], 80, 24)).toBe('');
    });

    test('sparklinePoints handles flat series (all values equal)', () => {
      const pts = __test__.sparklinePoints([5, 5, 5], 80, 24);
      // Range collapses to 1 so y = height for all points (no division by 0).
      expect(pts.split(' ')).toHaveLength(3);
      expect(pts).not.toContain('NaN');
    });

    test('attachSparkline draws a polyline into the matching svg', () => {
      const svg = document.createElementNS('http://www.w3.org/2000/svg', 'svg');
      svg.classList.add('kpi-tile-spark');
      svg.dataset['sparkKey'] = 'ytd';
      document.body.appendChild(svg);

      __test__.attachSparkline('ytd', [10, 20, 30]);

      const polyline = svg.querySelector('polyline');
      expect(polyline).toBeTruthy();
      expect(polyline?.getAttribute('points')).toMatch(/\d+\.\d+,\d+\.\d+/);
      expect(polyline?.getAttribute('stroke')).toBe('currentColor');

      document.body.removeChild(svg);
    });

    test('attachSparkline no-ops when placeholder is missing', () => {
      // No DOM, no throw.
      expect(() => __test__.attachSparkline('nonexistent', [1, 2, 3])).not.toThrow();
    });

    test('attachSparkline no-ops with <2 values (silent skip)', () => {
      const svg = document.createElementNS('http://www.w3.org/2000/svg', 'svg');
      svg.classList.add('kpi-tile-spark');
      svg.dataset['sparkKey'] = 'savings';
      document.body.appendChild(svg);

      __test__.attachSparkline('savings', [42]);

      expect(svg.querySelector('polyline')).toBeNull();
      document.body.removeChild(svg);
    });
  });
});
