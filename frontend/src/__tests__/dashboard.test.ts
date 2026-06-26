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
import { loadSavingsTrendChart, setupSavingsTrendHandlers, setupDashboardHandlers, formatTrendAxisTick } from '../dashboard';

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
  // Issue #950: the dashboard upcoming-purchase widget now gates the
  // Cancel button on creator-scope ownership (canCancelUpcomingPurchase
  // -> canAccess + getCurrentUser). Default the session to an admin so
  // the pre-#950 cancel-flow tests below keep exercising the click path;
  // the dedicated ownership tests override this per-test.
  getCurrentUser: jest.fn().mockReturnValue({
    id: 'admin-user',
    email: 'admin@example.com',
    groups: ['00000000-0000-5000-8000-000000000001'],
    effectivePermissions: [{ action: 'admin', resource: '*' }],
  }),
}));

// Mock utils
jest.mock('../utils', () => ({
  formatCurrency: jest.fn((val) => {
    const n = typeof val === 'number' ? val : Number(val);
    if (val == null || !Number.isFinite(n)) return '--';
    return `$${n}`;
  }),
  getDateParts: jest.fn(() => ({ day: 15, month: 'Jan' })),
  escapeHtml: jest.fn((str) => str || ''),
  populateAccountFilter: jest.fn(() => Promise.resolve()),
  // providerBadgeClass added for L4/D1 fix: whitelist-based CSS class helper.
  providerBadgeClass: jest.fn((p) => ['aws', 'azure', 'gcp'].includes((p || '').toLowerCase()) ? (p || '').toLowerCase() : ''),
}));

import * as api from '../api';
import * as state from '../state';

describe('Dashboard Module', () => {
  beforeEach(() => {
    // Reset DOM
    document.body.innerHTML = `
      <div id="summary"></div>
      <section id="savings-by-service-section"><h3>Potential savings range per service</h3>
        <canvas id="savings-by-service-chart"></canvas>
        <p id="savings-by-service-empty" class="empty hidden"></p>
      </section>
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

    test('formats fractional current_coverage with one decimal (not raw float)', async () => {
      (api.getDashboardSummary as jest.Mock).mockResolvedValue({
        potential_monthly_savings: 0,
        total_recommendations: 1,
        active_commitments: 1,
        committed_monthly: 100,
        current_coverage: 0.14648736342042862,
        target_coverage: 80,
        ytd_savings: 0,
        by_service: {}
      });
      (api.getUpcomingPurchases as jest.Mock).mockResolvedValue({ purchases: [] });

      await loadDashboard();

      const summary = document.getElementById('summary');
      expect(summary?.innerHTML).toContain('0.1%');
      expect(summary?.innerHTML).not.toContain('0.14648736342042862');
    });

    test('renders the merged per-service savings chart', async () => {
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
      // Range chart is driven by recs; supply two ec2 options + one rds.
      (api.getRecommendations as jest.Mock).mockResolvedValue([
        { service: 'ec2', savings: 400, term: 1, payment: 'no-upfront' },
        { service: 'ec2', savings: 600, term: 3, payment: 'all-upfront' },
        { service: 'rds', savings: 300, term: 1, payment: 'no-upfront' },
      ]);

      await loadDashboard();

      expect(Chart).toHaveBeenCalled();
    });

    test('destroys existing merged chart before creating new one', async () => {
      (api.getDashboardSummary as jest.Mock).mockResolvedValue({
        potential_monthly_savings: 1000,
        by_service: { ec2: { potential_savings: 500, current_savings: 200 } }
      });
      (api.getUpcomingPurchases as jest.Mock).mockResolvedValue({
        purchases: []
      });
      (api.getRecommendations as jest.Mock).mockResolvedValue([
        { service: 'ec2', savings: 500, term: 1, payment: 'no-upfront' },
      ]);

      // First render builds a chart; second render must destroy it.
      await loadDashboard();
      const results = (Chart as unknown as jest.Mock).mock.results;
      const firstChart = results[results.length - 1]?.value as { destroy: jest.Mock };
      await loadDashboard();

      expect(firstChart.destroy).toHaveBeenCalled();
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

    // #978: Current Coverage KPI must render with exactly 1 decimal place so it
    // cannot overflow the tile box at narrower screen widths.
    test('#978: Current Coverage KPI rounds to 1 decimal place', async () => {
      (api.getDashboardSummary as jest.Mock).mockResolvedValue({
        potential_monthly_savings: 0,
        total_recommendations: 3,
        active_commitments: 2,
        committed_monthly: 100,
        // A long-fractional value that previously overflowed the tile box.
        current_coverage: 72.3456789012,
        target_coverage: 80,
        ytd_savings: 0,
        by_service: {}
      });
      (api.getUpcomingPurchases as jest.Mock).mockResolvedValue({ purchases: [] });

      await loadDashboard();

      const coverageTile = document.querySelector('[data-kpi="coverage"]');
      const valueEl = coverageTile?.querySelector('.kpi-tile-value');
      // Must display exactly 1 decimal place (e.g. "72.3%"), not the raw float.
      expect(valueEl?.textContent).toBe('72.3%');
      // The raw long-decimal form must not appear.
      expect(valueEl?.textContent).not.toContain('72.3456');
    });

    // Integer coverage values must still format cleanly with one decimal place.
    test('#978: integer Current Coverage KPI renders with one decimal place', async () => {
      (api.getDashboardSummary as jest.Mock).mockResolvedValue({
        potential_monthly_savings: 0,
        total_recommendations: 1,
        active_commitments: 1,
        committed_monthly: 50,
        current_coverage: 70,
        target_coverage: 80,
        ytd_savings: 0,
        by_service: {}
      });
      (api.getUpcomingPurchases as jest.Mock).mockResolvedValue({ purchases: [] });

      await loadDashboard();

      const coverageTile = document.querySelector('[data-kpi="coverage"]');
      const valueEl = coverageTile?.querySelector('.kpi-tile-value');
      expect(valueEl?.textContent).toBe('70.0%');
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
      // Savings card must fall back to '--' (not throw, go blank, or fabricate $0).
      const savingsCard = summary?.querySelector('.card');
      expect(savingsCard?.textContent).toContain('Potential Monthly Savings');
      // When recs fail cellCount=0, the sentinel '--' is shown instead of fabricated $0.
      expect(savingsCard?.innerHTML).toContain('--');
      expect(savingsCard?.innerHTML).not.toContain('$0');
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

      // Savings card shows '--' when recs are absent (no fabricated $0).
      const savingsCard = summary?.querySelector('.card');
      expect(savingsCard?.textContent).toContain('Potential Monthly Savings');
      expect(savingsCard?.innerHTML).toContain('--');
      expect(savingsCard?.innerHTML).not.toContain('$0');
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

      // Savings card shows '--' (no fabricated $0 when recs are absent).
      const savingsCard = summary?.querySelector('.card');
      expect(savingsCard?.innerHTML).toContain('--');
      expect(savingsCard?.innerHTML).not.toContain('$0');
    });

    // #749: the real backend always returns the envelope shape
    // { recommendations: [...], summary: {...}, regions: [...] }, not a flat
    // array. The dashboard must unwrap .recommendations so the savings range
    // is computed from the actual recs rather than falling back to $0.
    test('#749: getRecommendations returning envelope shape populates savings card', async () => {
      const mockRecs = [
        { id: 'r1', provider: 'aws', service: 'ec2', region: 'us-east-1', resource_type: 't3.medium', term: 1, savings: 150, upfront_cost: 0, count: 1 },
        { id: 'r2', provider: 'aws', service: 'rds', region: 'us-east-1', resource_type: 'db.t3.medium', term: 1, savings: 62, upfront_cost: 0, count: 1 },
      ];
      // Simulate the real API response shape (envelope, not flat array).
      (api.getRecommendations as jest.Mock).mockResolvedValue({
        recommendations: mockRecs,
        summary: { total_count: 2, total_monthly_savings: 212, total_upfront_cost: 0, avg_payback_months: 0 },
        regions: ['us-east-1'],
      } as unknown);
      (api.getDashboardSummary as jest.Mock).mockResolvedValue({
        potential_monthly_savings: 0, // would be $0 if the flat-sum path were used
        total_recommendations: 2,
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
      // mockGroupRecsByCell / mockPageLevelRange return savingsMin=300,
      // savingsMax=400 for any non-empty recs array. The card must NOT be $0.
      expect(savingsCard?.textContent).toContain('$300');
      expect(savingsCard?.textContent).toContain('$400');
      expect(savingsCard?.innerHTML).not.toContain('$0');
    });

    // #304: summaryData.by_service missing entirely (null/undefined from
    // backend). renderSavingsByService receives `undefined || {}` = {} which
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
      // Reset the topbar filter mocks to the documented default (no filter)
      // so each test starts isolated. clearAllMocks() resets call history but
      // not mockReturnValue implementations, so a provider/account value set by
      // an earlier test would otherwise leak in (now that the chart reads the
      // provider chip, issue #498).
      (state.getCurrentProvider as jest.Mock).mockReturnValue('');
      (state.getCurrentAccountIDs as jest.Mock).mockReturnValue([]);
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

    // QA 3.1: 'all' range must NOT send the epoch sentinel (1970-01-01) as
    // the start param. The backend caps the date range at 366 days and returns
    // HTTP 400 for a 1970 start, which the catch block rendered as an empty-
    // state error. The fix uses a rolling ~365-day window that fits the cap.
    test('All range sends a start within ~365 days of now, not the epoch sentinel (QA 3.1)', async () => {
      // Add an 'all' button and make it active.
      const bAll = document.createElement('button');
      bAll.className = 'trend-range';
      bAll.dataset['range'] = 'all';
      bAll.textContent = 'All';
      document.body.appendChild(bAll);
      setupSavingsTrendHandlers();
      // Override the beforeEach default of empty data_points so the chart
      // actually renders -- the test asserts the empty-state stays hidden.
      (api.getSavingsAnalytics as jest.Mock).mockResolvedValue({
        data_points: [{ timestamp: new Date().toISOString(), cumulative_savings: 500, total_savings: 50, total_upfront: 0, purchase_count: 1 }],
      });
      (api.getSavingsAnalytics as jest.Mock).mockClear();

      bAll.click();
      await new Promise(r => setTimeout(r, 0));

      const call = (api.getSavingsAnalytics as jest.Mock).mock.calls[0]?.[0];
      // Must NOT be the epoch sentinel -- that causes HTTP 400 from the backend.
      expect(call.start).not.toBe('1970-01-01T00:00:00Z');
      // Start must be within ~365 days of now (within 366 days to fit the cap).
      const startMs = new Date(call.start as string).getTime();
      const nowMs = Date.now();
      const ageMs = nowMs - startMs;
      expect(ageMs).toBeGreaterThan(0);
      expect(ageMs).toBeLessThanOrEqual(366 * 86400_000 + 5_000); // 5s clock tolerance
      // The chart must render (not show empty-state) when the API returns data.
      const empty = document.getElementById('savings-trend-empty');
      expect(empty?.classList.contains('hidden')).toBe(true);
    });

    // QA row 405, step 3.1 — x-axis windowing behaviour.
    // Policy (aligned with QA 2.3 tests below): a successful-but-empty
    // response shows the empty-state banner, not blank axes. The original
    // QA 3.1 intent was to avoid showing a broken chart widget; QA 2.3
    // superseded that with an explicit "show a friendly message" policy
    // (see tests 'empty-state shows filter name' and 'empty-state shows
    // generic message' at the end of this describe block).

    test('shows empty-state (not canvas) when there are no data points and no filter is active (QA 3.1 / QA 2.3 policy)', async () => {
      // No active account or provider filter (default mock state: [] and '').
      // Expect the empty-state banner with generic text, canvas hidden.
      (api.getSavingsAnalytics as jest.Mock).mockResolvedValue({ data_points: [] });

      await loadSavingsTrendChart();

      const canvas = document.getElementById('savings-trend-chart');
      const empty = document.getElementById('savings-trend-empty');
      expect(canvas?.classList.contains('hidden')).toBe(true);
      expect(empty?.classList.contains('hidden')).toBe(false);
      expect(empty?.textContent).toContain('No purchase history yet');
    });

    test('x-axis min/max spans the selected window regardless of data point dates (QA 3.1)', async () => {
      // Add a 7d button before wiring handlers so setupSavingsTrendHandlers
      // attaches a click listener to it. Drive it to '7' for a deterministic
      // window size independent of prior test state.
      const b7 = document.createElement('button');
      b7.className = 'trend-range';
      b7.dataset['range'] = '7';
      b7.textContent = '7d';
      document.body.appendChild(b7);
      setupSavingsTrendHandlers();

      // Single purchase at the very start of the 7-day window.
      const now = Date.now();
      const purchaseTs = new Date(now - 6 * 86400_000).toISOString();
      (api.getSavingsAnalytics as jest.Mock).mockResolvedValue({
        data_points: [{ timestamp: purchaseTs, cumulative_savings: 100, total_savings: 5, total_upfront: 200, purchase_count: 1 }],
      });

      (Chart as unknown as jest.Mock).mockClear();
      b7.click();
      await new Promise(r => setTimeout(r, 0));

      const chartCalls = (Chart as unknown as jest.Mock).mock.calls;
      expect(chartCalls.length).toBeGreaterThan(0);
      const chartCall = chartCalls[chartCalls.length - 1];
      const xScale = chartCall[1].options.scales.x;
      // min must be ~7 days before max; allow 60-second clock skew in tests.
      expect(xScale.max - xScale.min).toBeGreaterThanOrEqual(6 * 86400_000);
      expect(xScale.max - xScale.min).toBeLessThanOrEqual(8 * 86400_000);
    });

    test('data points use {x: timestamp_ms, y: value} so they are positioned by real date (QA 3.1)', async () => {
      const purchaseTs = '2024-06-15T12:00:00Z';
      (api.getSavingsAnalytics as jest.Mock).mockResolvedValue({
        data_points: [{ timestamp: purchaseTs, cumulative_savings: 250, total_savings: 10, total_upfront: 500, purchase_count: 1 }],
      });

      await loadSavingsTrendChart();

      const chartCall = (Chart as unknown as jest.Mock).mock.calls[0];
      const dataset = chartCall[1].data.datasets[0];
      expect(dataset.data[0]).toMatchObject({ x: new Date(purchaseTs).getTime(), y: 250 });
    });

    test('fetch error shows error stub and hides canvas (not the empty-axes path) (QA 3.1)', async () => {
      (api.getSavingsAnalytics as jest.Mock).mockRejectedValue(new Error('503'));

      await loadSavingsTrendChart();

      const canvas = document.getElementById('savings-trend-chart');
      const empty = document.getElementById('savings-trend-empty');
      expect(canvas?.classList.contains('hidden')).toBe(true);
      expect(empty?.classList.contains('hidden')).toBe(false);
    });

    // QA 3.2: tooltip title callback must return a formatted date string, not
    // the raw 13-digit millisecond timestamp that Chart.js renders by default
    // when no title callback is configured.
    test('tooltip title callback returns a formatted date string, not a raw ms timestamp (QA 3.2)', async () => {
      const purchaseTs = '2024-06-15T12:00:00Z';
      (api.getSavingsAnalytics as jest.Mock).mockResolvedValue({
        data_points: [{ timestamp: purchaseTs, cumulative_savings: 250, total_savings: 10, total_upfront: 500, purchase_count: 1 }],
      });

      await loadSavingsTrendChart();

      const chartCall = (Chart as unknown as jest.Mock).mock.calls[0];
      const titleCb = chartCall[1].options.plugins.tooltip.callbacks.title as
        (items: Array<{ raw: { x: number; y: number } }>) => string;

      const xMs = new Date(purchaseTs).getTime();
      const result = titleCb([{ raw: { x: xMs, y: 250 } }]);

      // Must be a non-empty string.
      expect(typeof result).toBe('string');
      expect(result.length).toBeGreaterThan(0);
      // Must NOT be the raw numeric millisecond value.
      expect(result).not.toBe(String(xMs));
      expect(result).not.toMatch(/^\d{13}$/);
      // Must contain recognizable date text (month abbreviation).
      expect(result).toMatch(/Jun\s+\d+/);
    });
  });

  describe('formatTrendAxisTick (QA 3.1)', () => {
    test('formats hourly ticks with date + time', () => {
      // Use a fixed UTC timestamp: 2024-03-15 14:30 UTC.
      const ts = new Date('2024-03-15T14:30:00Z').getTime();
      const label = formatTrendAxisTick(ts, 'hourly');
      // Expect something like "Mar 15, 14:30" — locale-dependent but must contain the date.
      expect(label).toMatch(/Mar\s+\d+/);
    });

    test('formats daily ticks with short date only', () => {
      const ts = new Date('2024-03-15T00:00:00Z').getTime();
      const label = formatTrendAxisTick(ts, 'daily');
      expect(label).toMatch(/Mar\s+\d+/);
      // No colon (no time component).
      expect(label).not.toMatch(/:/);
    });

    test('formats weekly ticks with short date only', () => {
      const ts = new Date('2024-03-15T00:00:00Z').getTime();
      const label = formatTrendAxisTick(ts, 'weekly');
      expect(label).not.toMatch(/:/);
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

    test('loadSavingsTrendChart forwards account_ids for multi-account filter (filter parity with KPI tiles)', async () => {
      // Regression: previously the chart omitted account_ids when length > 1,
      // causing the chart data to diverge from the KPI tiles above it which
      // always forward all selected accounts. The fix passes account_ids
      // unconditionally when any accounts are selected.
      (state.getCurrentAccountIDs as jest.Mock).mockReturnValue(['uuid-a', 'uuid-b']);
      (api.getSavingsAnalytics as jest.Mock).mockResolvedValue({
        data_points: [{ timestamp: new Date().toISOString(), cumulative_savings: 50, total_savings: 5, total_upfront: 100, purchase_count: 1 }],
      });

      await loadSavingsTrendChart();

      expect(api.getSavingsAnalytics).toHaveBeenCalledWith(
        expect.objectContaining({ account_ids: ['uuid-a', 'uuid-b'] })
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

    test('empty-state names the provider when a provider filter is active (issue #498, QA 2.3)', async () => {
      // The analytics endpoint now applies the provider param as a WHERE
      // filter, so the query IS scoped to the selected provider — naming it
      // in the empty-state is accurate. Mirrors the account-filter empty-state.
      (state.getCurrentAccountIDs as jest.Mock).mockReturnValue([]);
      (state.getCurrentProvider as jest.Mock).mockReturnValue('aws');
      (api.getSavingsAnalytics as jest.Mock).mockResolvedValue({ data_points: [] });

      await loadSavingsTrendChart();

      const empty = document.getElementById('savings-trend-empty');
      expect(empty?.classList.contains('hidden')).toBe(false);
      // Provider name (uppercased by buildFilterDesc) appears in the message.
      expect(empty?.textContent).toContain('AWS');
      expect(empty?.textContent).toContain('No savings history for');
    });

    test('loadSavingsTrendChart forwards the provider chip to the analytics API (issue #498, QA 2.3)', async () => {
      // Regression: previously the chart dropped the provider param, so the
      // savings-history series showed the same all-provider data regardless of
      // the selected provider. The fix forwards provider to scope the query.
      (state.getCurrentAccountIDs as jest.Mock).mockReturnValue([]);
      (state.getCurrentProvider as jest.Mock).mockReturnValue('aws');
      (api.getSavingsAnalytics as jest.Mock).mockResolvedValue({ data_points: [] });

      await loadSavingsTrendChart();

      expect(api.getSavingsAnalytics).toHaveBeenCalledWith(
        expect.objectContaining({ provider: 'aws' })
      );
    });

    test('loadSavingsTrendChart omits provider when no provider filter is active', async () => {
      (state.getCurrentAccountIDs as jest.Mock).mockReturnValue([]);
      (state.getCurrentProvider as jest.Mock).mockReturnValue('');
      (api.getSavingsAnalytics as jest.Mock).mockResolvedValue({ data_points: [] });

      await loadSavingsTrendChart();

      const call = (api.getSavingsAnalytics as jest.Mock).mock.calls[0]?.[0];
      expect(call).not.toHaveProperty('provider');
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

  // Issue #765: per-service savings-range bar chart.
  describe('renderSavingsByService (issue #769)', () => {
    // Import the public helpers from dashboard. The module is already
    // loaded above via the jest.mock chain, so we can import directly.
    // eslint-disable-next-line @typescript-eslint/no-var-requires
    const { renderSavingsByService, computeServiceStats, computeServiceStatsFromRecs, darkenHexColor, parseHexColor } = require('../dashboard') as {
      renderSavingsByService: (recs: unknown[], byService?: Record<string, { potential_savings: number; current_savings: number }>, filterDesc?: string) => void;
      computeServiceStats: (dataPoints: unknown[]) => Map<string, { min: number; max: number; sum: number; count: number; samples: number[] }>;
      computeServiceStatsFromRecs: (recs: unknown[]) => Map<string, { min: number; max: number; sum: number; count: number; samples: number[]; minLabel?: string; maxLabel?: string }>;
      darkenHexColor: (hex: string, factor?: number) => string;
      parseHexColor: (hex: string) => { r: number; g: number; b: number };
    };

    function buildDOM(): void {
      const canvas = document.createElement('canvas');
      canvas.id = 'savings-by-service-chart';
      const empty = document.createElement('p');
      empty.id = 'savings-by-service-empty';
      empty.className = 'empty hidden';
      const section = document.createElement('section');
      section.id = 'savings-by-service-section';
      const h3 = document.createElement('h3');
      h3.textContent = 'Potential savings range per service';
      section.appendChild(h3);
      section.appendChild(canvas);
      section.appendChild(empty);
      document.body.appendChild(section);
    }

    /** Build a minimal recommendation fixture. */
    function rec(service: string, savings: number, term = 1, payment = 'no_upfront'): unknown {
      return {
        id: `${service}-${savings}`,
        provider: 'aws',
        service,
        region: 'us-east-1',
        resource_type: 'Standard',
        count: 1,
        term,
        payment,
        upfront_cost: 0,
        monthly_cost: 0,
        savings,
        selected: false,
        purchased: false,
      };
    }

    beforeEach(() => {
      document.body.innerHTML = '';
      jest.clearAllMocks();
      // Re-apply the recommendation mock resets from the outer beforeEach.
      mockGroupRecsByCell.mockImplementation((recs: unknown[]) => new Map(recs.length ? [['cell-1', recs]] : []));
      mockPageLevelRange.mockImplementation((groups: Map<string, unknown[]>) => {
        if (groups.size === 0) return { savingsMin: 0, savingsMax: 0, cellCount: 0 };
        return { savingsMin: 300, savingsMax: 400, cellCount: groups.size };
      });
      mockFormatSavingsRange.mockImplementation((min: number, max: number) => min === max ? `$${min}` : `$${min} – $${max}`);
      (api.getRecommendations as jest.Mock).mockResolvedValue([]);
    });

    // computeServiceStats unit tests (retained: function still exported for legacy compatibility).
    describe('computeServiceStats (legacy data-points path)', () => {
      test('returns empty map for empty data points', () => {
        const result = computeServiceStats([]);
        expect(result.size).toBe(0);
      });

      test('returns empty map when all data points have no by_service', () => {
        const result = computeServiceStats([
          { timestamp: 't1', total_savings: 100, total_upfront: 0, purchase_count: 1, cumulative_savings: 100 },
        ]);
        expect(result.size).toBe(0);
      });

      test('accumulates min/max/sum/count correctly for a single service', () => {
        const points = [
          { timestamp: 't1', total_savings: 0, total_upfront: 0, purchase_count: 0, cumulative_savings: 0, by_service: { ec2: 50 } },
          { timestamp: 't2', total_savings: 0, total_upfront: 0, purchase_count: 0, cumulative_savings: 0, by_service: { ec2: 200 } },
          { timestamp: 't3', total_savings: 0, total_upfront: 0, purchase_count: 0, cumulative_savings: 0, by_service: { ec2: 100 } },
        ];
        const result = computeServiceStats(points);
        expect(result.size).toBe(1);
        const ec2 = result.get('ec2');
        expect(ec2?.min).toBe(50);
        expect(ec2?.max).toBe(200);
        expect(ec2?.sum).toBe(350);
        expect(ec2?.count).toBe(3);
      });

      test('accumulates stats for multiple services independently', () => {
        const points = [
          { timestamp: 't1', total_savings: 0, total_upfront: 0, purchase_count: 0, cumulative_savings: 0, by_service: { ec2: 100, rds: 50 } },
          { timestamp: 't2', total_savings: 0, total_upfront: 0, purchase_count: 0, cumulative_savings: 0, by_service: { ec2: 300, rds: 80 } },
        ];
        const result = computeServiceStats(points);
        expect(result.size).toBe(2);
        expect(result.get('ec2')?.min).toBe(100);
        expect(result.get('ec2')?.max).toBe(300);
        expect(result.get('rds')?.min).toBe(50);
        expect(result.get('rds')?.max).toBe(80);
      });

      test('skips data points with missing by_service (omitempty)', () => {
        const points = [
          { timestamp: 't1', total_savings: 0, total_upfront: 0, purchase_count: 0, cumulative_savings: 0 },
          { timestamp: 't2', total_savings: 0, total_upfront: 0, purchase_count: 0, cumulative_savings: 0, by_service: { ec2: 200 } },
        ];
        const result = computeServiceStats(points);
        expect(result.get('ec2')?.count).toBe(1);
      });

      test('stores raw sample values for median computation', () => {
        const points = [
          { timestamp: 't1', total_savings: 0, total_upfront: 0, purchase_count: 0, cumulative_savings: 0, by_service: { ec2: 50 } },
          { timestamp: 't2', total_savings: 0, total_upfront: 0, purchase_count: 0, cumulative_savings: 0, by_service: { ec2: 200 } },
          { timestamp: 't3', total_savings: 0, total_upfront: 0, purchase_count: 0, cumulative_savings: 0, by_service: { ec2: 100 } },
        ];
        const result = computeServiceStats(points);
        const ec2 = result.get('ec2');
        expect(ec2?.samples).toHaveLength(3);
        expect(ec2?.samples).toContain(50);
        expect(ec2?.samples).toContain(100);
        expect(ec2?.samples).toContain(200);
      });
    });

    // computeServiceStatsFromRecs unit tests.
    describe('computeServiceStatsFromRecs', () => {
      test('returns empty map for empty recommendations', () => {
        expect(computeServiceStatsFromRecs([]).size).toBe(0);
      });

      test('single rec produces min === max (zero upside)', () => {
        const result = computeServiceStatsFromRecs([rec('ec2', 100)]);
        const ec2 = result.get('ec2');
        expect(ec2?.min).toBe(100);
        expect(ec2?.max).toBe(100);
        expect(ec2?.count).toBe(1);
      });

      test('two recs for same service: min is lower value, max is higher value', () => {
        const result = computeServiceStatsFromRecs([
          rec('ec2', 200, 1, 'no_upfront'),
          rec('ec2', 500, 3, 'all_upfront'),
        ]);
        const ec2 = result.get('ec2');
        expect(ec2?.min).toBe(200);
        expect(ec2?.max).toBe(500);
        expect(ec2?.count).toBe(2);
      });

      test('tracks minLabel and maxLabel from term/payment option', () => {
        const result = computeServiceStatsFromRecs([
          rec('ec2', 200, 1, 'no_upfront'),
          rec('ec2', 500, 3, 'all_upfront'),
        ]);
        const ec2 = result.get('ec2');
        expect(ec2?.minLabel).toBe('1yr no_upfront');
        expect(ec2?.maxLabel).toBe('3yr all_upfront');
      });

      test('uses "unspecified" label when payment is undefined or empty string', () => {
        const recNoPayment = { ...rec('ec2', 100) as Record<string, unknown> };
        delete recNoPayment['payment'];
        const recEmptyPayment = { ...rec('ec2', 200) as Record<string, unknown>, payment: '' };
        const result = computeServiceStatsFromRecs([recNoPayment, recEmptyPayment] as unknown as Parameters<typeof computeServiceStatsFromRecs>[0]);
        const ec2 = result.get('ec2');
        expect(ec2?.minLabel).toContain('unspecified');
        expect(ec2?.maxLabel).toContain('unspecified');
        expect(ec2?.minLabel).not.toContain('undefined');
        expect(ec2?.maxLabel).not.toContain('undefined');
      });

      test('accumulates stats for multiple services independently', () => {
        const result = computeServiceStatsFromRecs([
          rec('ec2', 100, 1, 'no_upfront'),
          rec('ec2', 400, 3, 'all_upfront'),
          rec('rds', 50, 1, 'no_upfront'),
          rec('rds', 80, 3, 'all_upfront'),
        ]);
        expect(result.size).toBe(2);
        expect(result.get('ec2')?.min).toBe(100);
        expect(result.get('ec2')?.max).toBe(400);
        expect(result.get('rds')?.min).toBe(50);
        expect(result.get('rds')?.max).toBe(80);
      });

      test('stores raw sample values', () => {
        const result = computeServiceStatsFromRecs([
          rec('ec2', 100),
          rec('ec2', 300),
          rec('ec2', 200),
        ]);
        const ec2 = result.get('ec2');
        expect(ec2?.samples).toHaveLength(3);
        expect(ec2?.samples).toContain(100);
        expect(ec2?.samples).toContain(200);
        expect(ec2?.samples).toContain(300);
      });
    });

    // renderSavingsByService DOM behaviour tests (now driven by recommendation fixtures).
    describe('DOM behaviour', () => {
      test('shows empty state and hides canvas when no recommendations', () => {
        buildDOM();
        renderSavingsByService([]);
        const canvas = document.getElementById('savings-by-service-chart');
        const empty = document.getElementById('savings-by-service-empty');
        expect(canvas?.classList.contains('hidden')).toBe(true);
        expect(empty?.classList.contains('hidden')).toBe(false);
      });

      test('shows empty state when all recommendations have zero savings', () => {
        buildDOM();
        renderSavingsByService([rec('ec2', 0)]);
        expect(document.getElementById('savings-by-service-chart')?.classList.contains('hidden')).toBe(true);
        expect(document.getElementById('savings-by-service-empty')?.classList.contains('hidden')).toBe(false);
      });

      test('resets heading text to default when dataset becomes empty after a truncated render', () => {
        buildDOM();
        renderSavingsByService([rec('ec2', 100), rec('rds', 50)]);
        const h3 = document.querySelector('#savings-by-service-section h3') as HTMLElement;
        h3.textContent = 'Potential savings range per service (+3 more)'; // simulate stale suffix
        // Second render with empty data -- heading must be reset.
        renderSavingsByService([]);
        expect(h3.textContent).toBe('Potential savings range per service');
      });

      test('renders chart with exactly two services when two services have positive savings', () => {
        buildDOM();
        renderSavingsByService([
          rec('ec2', 100, 1, 'no_upfront'),
          rec('ec2', 300, 3, 'all_upfront'),
          rec('rds', 50, 1, 'no_upfront'),
          rec('rds', 80, 3, 'all_upfront'),
        ]);
        // Chart must be constructed (canvas visible, empty hidden).
        expect(document.getElementById('savings-by-service-chart')?.classList.contains('hidden')).toBe(false);
        expect(document.getElementById('savings-by-service-empty')?.classList.contains('hidden')).toBe(true);
        // Chart.js was called with both services as labels.
        const chartCtor = Chart as unknown as jest.Mock;
        const lastCall = chartCtor.mock.calls[chartCtor.mock.calls.length - 1];
        const chartData = lastCall?.[1] as { data: { labels: string[] } };
        expect(chartData.data.labels).toHaveLength(2);
        expect(chartData.data.labels).toContain('ec2');
        expect(chartData.data.labels).toContain('rds');
      });

      test('three stacked datasets: Current/Committed (bottom), Lowest option, Upside (top)', () => {
        buildDOM();
        // ec2: min rec=100, max rec=400, current=0 -> lowestOption=100, upside=300.
        renderSavingsByService([
          rec('ec2', 100, 1, 'no_upfront'),
          rec('ec2', 400, 3, 'all_upfront'),
        ]);
        const chartCtor = Chart as unknown as jest.Mock;
        const lastCall = chartCtor.mock.calls[chartCtor.mock.calls.length - 1];
        const datasets = (lastCall?.[1] as { data: { datasets: { label: string; data: number[]; stack: string }[] } }).data.datasets;
        const currentDs = datasets.find((d) => d.label === 'Current / Committed');
        const lowestDs = datasets.find((d) => d.label === 'Lowest option');
        const upsideDs = datasets.find((d) => d.label === 'Upside');
        // All three datasets must be present and in the same stack.
        expect(currentDs).toBeDefined();
        expect(lowestDs).toBeDefined();
        expect(upsideDs).toBeDefined();
        expect(currentDs?.stack).toBe('savings');
        expect(lowestDs?.stack).toBe('savings');
        expect(upsideDs?.stack).toBe('savings');
        // Values: current=0 (no byService supplied), lowestOption=100-0=100, upside=400-100=300.
        expect(currentDs?.data[0]).toBe(0);
        expect(lowestDs?.data[0]).toBe(100);
        expect(upsideDs?.data[0]).toBe(300);
        // Tooltip Total must equal the sum of the three visible bar layers (0+100+300=400).
        const tooltipLabel = (lastCall?.[1] as {
          options: { plugins: { tooltip: { callbacks: { label: (ctx: { label: string }) => string[] } } } };
        }).options.plugins.tooltip.callbacks.label({ label: 'ec2' });
        const totalLine = tooltipLabel.find((l) => l.startsWith('Total:'));
        expect(totalLine).toMatch(/\$400/);
      });

      // Issue #908: merged chart draws a current-savings bottom layer per service.
      test('renders Current / Committed layer from byService.current_savings', () => {
        buildDOM();
        // current=250, min rec=100, max rec=400
        // lowestOption = max(0, 100-250) = 0 (committed already exceeds min rec)
        // upside = max(0, 400-100) = 300
        renderSavingsByService(
          [rec('ec2', 100, 1, 'no_upfront'), rec('ec2', 400, 3, 'all_upfront')],
          { ec2: { potential_savings: 400, current_savings: 250 } },
        );
        const chartCtor = Chart as unknown as jest.Mock;
        const lastCall = chartCtor.mock.calls[chartCtor.mock.calls.length - 1];
        const datasets = (lastCall?.[1] as {
          data: { datasets: { label: string; data: number[]; stack: string }[] };
        }).data.datasets;
        const currentDs = datasets.find((d) => d.label === 'Current / Committed');
        expect(currentDs).toBeDefined();
        expect(currentDs?.data[0]).toBe(250);
        // All three datasets share one stack so Current renders below the
        // potential range (dataset order = bottom-to-top in Chart.js stacked bars).
        expect(currentDs?.stack).toBe('savings');
      });

      test('Lowest option clamps to 0 when committed savings exceed min rec', () => {
        buildDOM();
        // current=300, min rec=100 -> lowestOption = max(0, 100-300) = 0
        renderSavingsByService(
          [rec('ec2', 100, 1, 'no_upfront'), rec('ec2', 400, 3, 'all_upfront')],
          { ec2: { potential_savings: 400, current_savings: 300 } },
        );
        const chartCtor = Chart as unknown as jest.Mock;
        const lastCall = chartCtor.mock.calls[chartCtor.mock.calls.length - 1];
        const datasets = (lastCall?.[1] as {
          data: { datasets: { label: string; data: number[] }[] };
        }).data.datasets;
        const lowestDs = datasets.find((d) => d.label === 'Lowest option');
        expect(lowestDs?.data[0]).toBe(0);
      });

      test('current underlay uses a DARKER variant of each service base hue', () => {
        buildDOM();
        renderSavingsByService(
          [rec('ec2', 500)],
          { ec2: { potential_savings: 500, current_savings: 200 } },
        );
        const chartCtor = Chart as unknown as jest.Mock;
        const lastCall = chartCtor.mock.calls[chartCtor.mock.calls.length - 1];
        const datasets = (lastCall?.[1] as {
          data: { datasets: { label: string; backgroundColor: string[] }[] };
        }).data.datasets;
        const lowestOptionColor = datasets.find((d) => d.label === 'Lowest option')?.backgroundColor[0] as string;
        const currentColor = datasets.find((d) => d.label === 'Current / Committed')?.backgroundColor[0] as string;
        // The current colour is the darkened form of the base (lowest-option) colour.
        expect(currentColor).toBe(darkenHexColor(lowestOptionColor));
        // And it is genuinely darker: each channel sum is lower.
        const sum = (hex: string): number => { const c = parseHexColor(hex); return c.r + c.g + c.b; };
        expect(sum(currentColor)).toBeLessThan(sum(lowestOptionColor));
      });

      test('current layer defaults to 0 for a service absent from byService', () => {
        buildDOM();
        renderSavingsByService([rec('rds', 300)], {}); // no rds entry
        const chartCtor = Chart as unknown as jest.Mock;
        const lastCall = chartCtor.mock.calls[chartCtor.mock.calls.length - 1];
        const datasets = (lastCall?.[1] as {
          data: { datasets: { label: string; data: number[] }[] };
        }).data.datasets;
        const currentDs = datasets.find((d) => d.label === 'Current / Committed');
        expect(currentDs?.data[0]).toBe(0);
      });

      // Issue #769 follow-up (2x height + stacking): all three datasets must
      // share one stack so Current renders visually BELOW the potential range.
      test('all datasets share one stack so Current renders under Potential', () => {
        buildDOM();
        renderSavingsByService(
          [rec('ec2', 100), rec('ec2', 400)],
          { ec2: { potential_savings: 400, current_savings: 150 } },
        );
        const chartCtor = Chart as unknown as jest.Mock;
        const lastCall = chartCtor.mock.calls[chartCtor.mock.calls.length - 1];
        const config = lastCall?.[1] as {
          data: { datasets: { label: string; stack: string }[] };
          options: { scales: { x: { stacked: boolean }; y: { stacked: boolean } } };
        };
        const datasets = config.data.datasets;
        // Every dataset must share the same stack name.
        const stacks = new Set(datasets.map((d) => d.stack));
        expect(stacks.size).toBe(1);
        // Both axes must have stacked: true.
        expect(config.options.scales.x.stacked).toBe(true);
        expect(config.options.scales.y.stacked).toBe(true);
      });

      test('dataset order: Current / Committed is first so it renders at the base', () => {
        buildDOM();
        renderSavingsByService(
          [rec('ec2', 200)],
          { ec2: { potential_savings: 200, current_savings: 80 } },
        );
        const chartCtor = Chart as unknown as jest.Mock;
        const lastCall = chartCtor.mock.calls[chartCtor.mock.calls.length - 1];
        const datasets = (lastCall?.[1] as {
          data: { datasets: { label: string }[] };
        }).data.datasets;
        // Use find to avoid direct index access (TS strict mode).
        const labels = datasets.map((d) => d.label);
        expect(labels[0]).toBe('Current / Committed');
        expect(labels[1]).toBe('Lowest option');
        expect(labels[2]).toBe('Upside');
      });

      test('service present in byService but absent from recs renders Current-only bar', () => {
        buildDOM();
        // No recs for 'lambda'; only byService entry -> single Current band, no Lowest/Upside.
        renderSavingsByService(
          [],
          { lambda: { potential_savings: 0, current_savings: 120 } },
        );
        const canvas = document.getElementById('savings-by-service-chart');
        expect(canvas?.classList.contains('hidden')).toBe(false);
        const chartCtor = Chart as unknown as jest.Mock;
        const lastCall = chartCtor.mock.calls[chartCtor.mock.calls.length - 1];
        const labels = (lastCall?.[1] as { data: { labels: string[] } }).data.labels;
        expect(labels).toContain('lambda');
        const datasets = (lastCall?.[1] as {
          data: { datasets: { label: string; data: number[] }[] };
        }).data.datasets;
        const currentDs = datasets.find((d) => d.label === 'Current / Committed');
        const lowestDs = datasets.find((d) => d.label === 'Lowest option');
        const upsideDs = datasets.find((d) => d.label === 'Upside');
        expect(currentDs?.data[0]).toBe(120);
        expect(lowestDs?.data[0]).toBe(0);
        expect(upsideDs?.data[0]).toBe(0);
      });

      test('services are sorted by visible total (current + lowestOption + upside) descending', () => {
        buildDOM();
        // ec2: current=0, minRec=200, maxRec=200 -> lowestOption=200, upside=0, visibleTotal=200
        // rds: current=150, minRec=500, maxRec=500 -> lowestOption=350, upside=0, visibleTotal=500
        // lambda: current=0, minRec=50, maxRec=50 -> lowestOption=50, upside=0, visibleTotal=50
        // Expected order: rds (500), ec2 (200), lambda (50).
        renderSavingsByService(
          [rec('ec2', 200), rec('rds', 500), rec('lambda', 50)],
          { rds: { potential_savings: 500, current_savings: 150 } },
        );
        const chartCtor = Chart as unknown as jest.Mock;
        const lastCall = chartCtor.mock.calls[chartCtor.mock.calls.length - 1];
        const labels = (lastCall?.[1] as { data: { labels: string[] } }).data.labels;
        expect(labels[0]).toBe('rds');
        expect(labels[1]).toBe('ec2');
        expect(labels[2]).toBe('lambda');
      });

      test('destroys existing chart instance before re-rendering', () => {
        buildDOM();
        const mockDestroyA = jest.fn();
        (Chart as unknown as jest.Mock).mockImplementationOnce(() => ({ destroy: mockDestroyA }));

        const recs = [rec('ec2', 100)];
        renderSavingsByService(recs);
        // Second call must destroy the first chart.
        renderSavingsByService(recs);
        expect(mockDestroyA).toHaveBeenCalled();
      });

      test('no-ops gracefully when canvas is missing from DOM', () => {
        // No buildDOM() call — canvas absent.
        expect(() => renderSavingsByService([rec('ec2', 100)])).not.toThrow();
      });

      // Issue #867: filter-aware empty state.
      test('empty state shows generic text when no filter is active', () => {
        buildDOM();
        renderSavingsByService([], {}, '');
        const empty = document.getElementById('savings-by-service-empty');
        expect(empty?.classList.contains('hidden')).toBe(false);
        expect(empty?.textContent).toBe('No positive potential savings found for current recommendations.');
      });

      test('empty state mentions filter when provider chip is active and result is empty', () => {
        buildDOM();
        renderSavingsByService([], {}, 'AWS');
        const empty = document.getElementById('savings-by-service-empty');
        expect(empty?.classList.contains('hidden')).toBe(false);
        expect(empty?.textContent).toContain('AWS');
        expect(empty?.textContent).toContain('selected filter');
      });

      test('empty state mentions filter when account chip is active and result is empty', () => {
        buildDOM();
        renderSavingsByService([], {}, 'uuid-acct-1');
        const empty = document.getElementById('savings-by-service-empty');
        expect(empty?.classList.contains('hidden')).toBe(false);
        expect(empty?.textContent).toContain('uuid-acct-1');
      });

      test('empty state text updates when filter changes between renders', () => {
        buildDOM();
        // First render with data -- chart shown, empty hidden.
        renderSavingsByService([rec('ec2', 100)]);
        // Second render with filter-narrowed empty result.
        renderSavingsByService([], {}, 'AWS, uuid-acct-2');
        const empty = document.getElementById('savings-by-service-empty');
        expect(empty?.classList.contains('hidden')).toBe(false);
        expect(empty?.textContent).toContain('AWS, uuid-acct-2');
      });

      test('loadDashboard wires range bars to recommendations, not trend data', async () => {
        // The range bar chart must receive the recs from getRecommendations,
        // not from getSavingsAnalytics. Verify by providing recs but no analytics.
        buildDOM();
        const summaryEl = document.createElement('section');
        summaryEl.id = 'summary';
        const upcomingEl = document.createElement('div');
        upcomingEl.id = 'upcoming-list';
        document.body.appendChild(summaryEl);
        document.body.appendChild(upcomingEl);

        (api.getDashboardSummary as jest.Mock).mockResolvedValue({
          potential_monthly_savings: 0,
          total_recommendations: 1, active_commitments: 0, committed_monthly: 0,
          target_coverage: 80, ytd_savings: 0,
          by_service: {},
        });
        (api.getUpcomingPurchases as jest.Mock).mockResolvedValue({ purchases: [] });
        // Provide a rec so the bar chart should render (not show empty state).
        (api.getRecommendations as jest.Mock).mockResolvedValue([rec('ec2', 150)]);
        // Analytics deliberately absent/empty -- bar chart should still render from recs.
        (api.getSavingsAnalytics as jest.Mock).mockResolvedValue({ data_points: [] });

        await loadDashboard();

        expect(document.getElementById('savings-by-service-chart')?.classList.contains('hidden')).toBe(false);
        expect(document.getElementById('savings-by-service-empty')?.classList.contains('hidden')).toBe(true);
      });

      // Issue #908: the merged chart must keep honoring the topbar chips.
      // loadDashboard re-runs on every chip change (via the state subscribers
      // wired in setupDashboardHandlers), so a second load with new filter
      // results must re-render the chart with the new data.
      test('re-renders the merged chart when the filter changes between loads', async () => {
        buildDOM();
        const summaryEl = document.createElement('section');
        summaryEl.id = 'summary';
        const upcomingEl = document.createElement('div');
        upcomingEl.id = 'upcoming-list';
        document.body.appendChild(summaryEl);
        document.body.appendChild(upcomingEl);

        (api.getDashboardSummary as jest.Mock).mockResolvedValue({
          potential_monthly_savings: 0, total_recommendations: 1,
          active_commitments: 0, committed_monthly: 0, target_coverage: 80,
          ytd_savings: 0, by_service: { ec2: { potential_savings: 150, current_savings: 90 } },
        });
        (api.getUpcomingPurchases as jest.Mock).mockResolvedValue({ purchases: [] });
        (api.getSavingsAnalytics as jest.Mock).mockResolvedValue({ data_points: [] });

        // First load: AWS/ec2 result.
        (api.getRecommendations as jest.Mock).mockResolvedValue([rec('ec2', 150)]);
        await loadDashboard();
        const chartCtor = Chart as unknown as jest.Mock;
        const lastChartData = (): { labels: string[] } =>
          (chartCtor.mock.calls[chartCtor.mock.calls.length - 1]?.[1] as { data: { labels: string[] } }).data;
        expect(lastChartData().labels).toEqual(['ec2']);

        // Filter changes -> new recs -> second load must re-render. rds appears
        // from the new recs (total=220); ec2 still appears from byService
        // current_savings=90 even without a rec in this load. rds sorts first.
        (api.getRecommendations as jest.Mock).mockResolvedValue([rec('rds', 220)]);
        await loadDashboard();
        expect(lastChartData().labels).toContain('rds');
        expect(lastChartData().labels[0]).toBe('rds');
      });
    });

    // Issue #908: colour-derivation helpers for the current-savings underlay.
    describe('colour helpers (issue #908)', () => {
      test('parseHexColor parses #rrggbb', () => {
        expect(parseHexColor('#1a73e8')).toEqual({ r: 26, g: 115, b: 232 });
      });

      test('parseHexColor tolerates a missing leading hash', () => {
        expect(parseHexColor('34a853')).toEqual({ r: 52, g: 168, b: 83 });
      });

      test('parseHexColor falls back to the default blue for malformed input', () => {
        expect(parseHexColor('not-a-color')).toEqual({ r: 26, g: 115, b: 232 });
      });

      test('darkenHexColor returns a strictly darker same-hue colour', () => {
        const base = '#34a853';
        const darker = darkenHexColor(base);
        expect(darker).toMatch(/^#[0-9a-f]{6}$/);
        const sum = (hex: string): number => { const c = parseHexColor(hex); return c.r + c.g + c.b; };
        expect(sum(darker)).toBeLessThan(sum(base));
        // 30% reduction by default (factor 0.7): green channel 168 -> ~118.
        expect(parseHexColor(darker).g).toBe(Math.round(168 * 0.7));
      });

      test('darkenHexColor honours an explicit factor', () => {
        // factor 0.5 halves each channel.
        expect(darkenHexColor('#646464', 0.5)).toBe('#323232');
      });
    });
  });

  // H-3 regression: absent API coverage/count fields must render '--', not '$0'/'80%'/'0'.
  // Pre-fix: `data.current_coverage || 0` fabricated '0%'; `data.target_coverage || 80`
  // fabricated '80%'; `data.active_commitments || 0` fabricated '0'; the savings catch
  // block fell back to formatCurrency(0). These tests must FAIL on pre-fix code.
  describe('H-3: absent dashboard KPI fields render -- sentinel, not fabricated values', () => {
    test('absent target_coverage shows -- not hardcoded 80%', async () => {
      (api.getDashboardSummary as jest.Mock).mockResolvedValue({
        // target_coverage deliberately absent
        total_recommendations: 1,
        active_commitments: 1,
        committed_monthly: 100,
        current_coverage: 60,
        ytd_savings: 0,
        by_service: {}
      });
      (api.getUpcomingPurchases as jest.Mock).mockResolvedValue({ purchases: [] });

      await loadDashboard();

      const summary = document.getElementById('summary');
      // Must not show the hardcoded 80% fallback
      expect(summary?.textContent).not.toContain('Target: 80.0%');
      expect(summary?.textContent).not.toContain('Target: 80%');
      // Must show the '--' sentinel for absent target
      expect(summary?.textContent).toContain('Target: --');
    });

    test('absent current_coverage shows -- not fabricated 0%', async () => {
      (api.getDashboardSummary as jest.Mock).mockResolvedValue({
        // current_coverage deliberately absent
        total_recommendations: 2,
        active_commitments: 1,
        committed_monthly: 100,
        target_coverage: 80,
        ytd_savings: 0,
        by_service: {}
      });
      (api.getUpcomingPurchases as jest.Mock).mockResolvedValue({ purchases: [] });

      await loadDashboard();

      const summaryEl = document.getElementById('summary');
      // Find the Coverage tile specifically to check its value
      const tiles = summaryEl?.querySelectorAll('.kpi-tile');
      const coverageTile = Array.from(tiles ?? []).find(t =>
        t.textContent?.includes('Current Coverage')
      );
      expect(coverageTile).toBeDefined();
      const valueEl = coverageTile?.querySelector('.kpi-tile-value');
      // Must show '--' (not a fabricated '0.0%') when current_coverage is absent
      expect(valueEl?.textContent).toBe('--');
    });

    test('absent active_commitments shows -- not fabricated 0', async () => {
      (api.getDashboardSummary as jest.Mock).mockResolvedValue({
        total_recommendations: 1,
        // active_commitments deliberately absent
        committed_monthly: 0,
        current_coverage: 70,
        target_coverage: 80,
        ytd_savings: 0,
        by_service: {}
      });
      (api.getUpcomingPurchases as jest.Mock).mockResolvedValue({ purchases: [] });

      await loadDashboard();

      const summaryEl = document.getElementById('summary');
      // Find the Active Commitments tile specifically
      const tiles = summaryEl?.querySelectorAll('.kpi-tile');
      const commitmentsTile = Array.from(tiles ?? []).find(t =>
        t.textContent?.includes('Active Commitments')
      );
      expect(commitmentsTile).toBeDefined();
      // Must not show fabricated '0'; must show '--'
      const valueEl = commitmentsTile?.querySelector('.kpi-tile-value');
      expect(valueEl?.textContent).toBe('--');
    });

    test('savings range catch block shows -- not $0 when computation throws', async () => {
      // Force the groupRecsByCell mock to throw so the catch block fires.
      mockGroupRecsByCell.mockImplementationOnce(() => { throw new Error('compute error'); });
      (api.getDashboardSummary as jest.Mock).mockResolvedValue({
        total_recommendations: 1,
        active_commitments: 1,
        committed_monthly: 100,
        current_coverage: 70,
        target_coverage: 80,
        ytd_savings: 0,
        by_service: {}
      });
      (api.getUpcomingPurchases as jest.Mock).mockResolvedValue({ purchases: [] });

      await loadDashboard();

      const summaryEl = document.getElementById('summary');
      const tiles = summaryEl?.querySelectorAll('.kpi-tile');
      const savingsTile = Array.from(tiles ?? []).find(t =>
        t.textContent?.includes('Potential Monthly Savings')
      );
      expect(savingsTile).toBeDefined();
      const valueEl = savingsTile?.querySelector('.kpi-tile-value');
      // Must show '--' (not '$0') when the savings computation fails
      expect(valueEl?.textContent).toBe('--');
    });
  });
});
