/**
 * Issue #950 follow-up: creator-scope ownership gating on the dashboard
 * upcoming-purchases widget's Cancel buttons.
 *
 * The pre-fix dashboard widget rendered a "Cancel" button on every
 * upcoming-purchase card (and a "Cancel Purchase" button in the
 * View Details modal) regardless of who created the underlying
 * execution. Clicking it called DELETE /api/purchases/planned/{id},
 * which the backend now (correctly) 403s for non-owners after PR #995.
 * The result was a UX hole: the operator sees the button, clicks it,
 * and gets a confusing failure toast.
 *
 * The fix gates both buttons on canCancelUpcomingPurchase(), which
 * mirrors plans.ts's canManageScheduledPurchase: admin / update-any
 * see Cancel on any row; otherwise the row's created_by_user_id must
 * match the current user (legacy NULL-creator rows are out of reach
 * for non-privileged users).
 *
 * These tests drive the real renderUpcomingPurchases pipe via
 * loadDashboard() so the production gate is what's exercised, not a
 * unit shim around the helper.
 */

// Chart.js + recommendations + freshness need to be mocked before the
// dashboard import the same way dashboard.test.ts does.
const mockShowToast = jest.fn<{ dismiss: () => void }, [unknown]>(() => ({ dismiss: jest.fn() }));
jest.mock('../toast', () => ({
  showToast: (opts: unknown) => mockShowToast(opts),
}));
jest.mock('../confirmDialog', () => ({
  confirmDialog: jest.fn(() => Promise.resolve(true)),
}));
jest.mock('chart.js', () => {
  const MockChart = jest.fn().mockImplementation(() => ({ destroy: jest.fn() }));
  (MockChart as unknown as { register: jest.Mock }).register = jest.fn();
  return { Chart: MockChart, registerables: [] };
});
jest.mock('../recommendations', () => ({
  groupRecsByCell: jest.fn(() => new Map()),
  pageLevelRange: jest.fn(() => ({ savingsMin: 0, savingsMax: 0, cellCount: 0 })),
  formatSavingsRange: jest.fn((min: number, max: number) => `$${min}-$${max}`),
  triggerAutoRefreshIfStale: jest.fn(() => Promise.resolve()),
}));

jest.mock('../api', () => ({
  getDashboardSummary: jest.fn().mockResolvedValue({ potential_monthly_savings: 0, by_service: {} }),
  getUpcomingPurchases: jest.fn(),
  getPurchaseDetails: jest.fn(),
  cancelPurchase: jest.fn(),
  deletePlannedPurchase: jest.fn().mockResolvedValue({}),
  deletePlan: jest.fn(),
  listAccounts: jest.fn().mockResolvedValue([]),
  getSavingsAnalytics: jest.fn().mockResolvedValue({ data_points: [] }),
  getRecommendations: jest.fn().mockResolvedValue([]),
}));

jest.mock('../state', () => ({
  getCurrentProvider: jest.fn().mockReturnValue(''),
  setCurrentProvider: jest.fn(),
  getCurrentAccountIDs: jest.fn().mockReturnValue([]),
  setCurrentAccountIDs: jest.fn(),
  getSavingsChart: jest.fn().mockReturnValue(null),
  setSavingsChart: jest.fn(),
  subscribeProvider: jest.fn().mockReturnValue(() => {}),
  subscribeAccount: jest.fn().mockReturnValue(() => {}),
  getCurrentUser: jest.fn(),
}));

jest.mock('../utils', () => ({
  formatCurrency: jest.fn((val) => `$${val || 0}`),
  getDateParts: jest.fn(() => ({ day: 15, month: 'Jan' })),
  escapeHtml: jest.fn((str) => str || ''),
  populateAccountFilter: jest.fn(() => Promise.resolve()),
  providerBadgeClass: jest.fn((p) => ['aws', 'azure', 'gcp'].includes((p || '').toLowerCase()) ? (p || '').toLowerCase() : ''),
}));

import { loadDashboard } from '../dashboard';
import * as api from '../api';
import * as state from '../state';

const CREATOR_ID = 'creator-aaaa';
const OTHER_ID = 'other-bbbb';
const ADMIN_GROUP = '00000000-0000-5000-8000-000000000001';

const ownedPurchase = {
  execution_id: 'exec-1',
  plan_id: 'plan-1',
  plan_name: 'Owned Plan',
  scheduled_date: '2026-06-01',
  provider: 'aws',
  service: 'ec2',
  step_number: 1,
  total_steps: 4,
  estimated_savings: 100,
  created_by_user_id: CREATOR_ID,
};

const legacyPurchase = {
  ...ownedPurchase,
  execution_id: 'exec-legacy',
  created_by_user_id: undefined as string | undefined,
};

type StubUserOpts = { updateAny?: boolean; admin?: boolean; deletePurchases?: boolean };
const setUser = (id: string, opts: StubUserOpts = {}) => {
  const effectivePermissions: Array<{ action: string; resource: string }> = [];
  if (opts.admin) {
    effectivePermissions.push({ action: 'admin', resource: '*' });
  } else if (opts.deletePurchases !== false) {
    // Standard user holds delete:purchases (PR #660 default).
    effectivePermissions.push({ action: 'delete', resource: 'purchases' });
  }
  if (opts.updateAny) {
    effectivePermissions.push({ action: 'update-any', resource: 'purchases' });
  }
  (state.getCurrentUser as jest.Mock).mockReturnValue({
    id,
    email: `${id}@example.com`,
    groups: opts.admin ? [ADMIN_GROUP] : [],
    effectivePermissions,
  });
};

const setupDom = () => {
  document.body.innerHTML = `
    <div id="summary"></div>
    <section id="savings-by-service-section">
      <canvas id="savings-by-service-chart"></canvas>
      <p id="savings-by-service-empty" class="empty hidden"></p>
    </section>
    <div id="upcoming-list"></div>
  `;
};

const cancelBtns = () =>
  document.querySelectorAll<HTMLButtonElement>('[data-action="cancel-purchase"]');

describe('Dashboard upcoming-purchase ownership gating (issue #950)', () => {
  beforeEach(() => {
    jest.clearAllMocks();
    setupDom();
  });

  test('creator sees the Cancel button on their own scheduled purchase', async () => {
    setUser(CREATOR_ID);
    (api.getUpcomingPurchases as jest.Mock).mockResolvedValue({ purchases: [ownedPurchase] });
    await loadDashboard();
    expect(cancelBtns()).toHaveLength(1);
  });

  test('non-creator with the same verbs sees NO Cancel button (the bug)', async () => {
    setUser(OTHER_ID);
    (api.getUpcomingPurchases as jest.Mock).mockResolvedValue({ purchases: [ownedPurchase] });
    await loadDashboard();
    expect(cancelBtns()).toHaveLength(0);
    // The card still renders -- the operator sees the row and can click
    // "View Details", which is intentionally unrestricted.
    const viewBtns = document.querySelectorAll<HTMLButtonElement>('[data-action="view-purchase"]');
    expect(viewBtns).toHaveLength(1);
  });

  test('update-any holder sees Cancel on another user\'s scheduled purchase', async () => {
    setUser(OTHER_ID, { updateAny: true });
    (api.getUpcomingPurchases as jest.Mock).mockResolvedValue({ purchases: [ownedPurchase] });
    await loadDashboard();
    expect(cancelBtns()).toHaveLength(1);
  });

  test('admin sees Cancel on every row (admin:* covers delete:purchases)', async () => {
    setUser(OTHER_ID, { admin: true });
    (api.getUpcomingPurchases as jest.Mock).mockResolvedValue({
      purchases: [ownedPurchase, { ...ownedPurchase, execution_id: 'exec-2' }],
    });
    await loadDashboard();
    expect(cancelBtns()).toHaveLength(2);
  });

  test('legacy NULL-creator row shows no Cancel for a non-update-any user', async () => {
    setUser(CREATOR_ID);
    (api.getUpcomingPurchases as jest.Mock).mockResolvedValue({ purchases: [legacyPurchase] });
    await loadDashboard();
    expect(cancelBtns()).toHaveLength(0);
  });

  test('user without delete:purchases sees no Cancel even on their own row', async () => {
    // Read-only style: holds neither delete:purchases nor admin nor
    // update-any. Even on their own row the button must stay hidden
    // because the backend would reject the click on verb grounds.
    (state.getCurrentUser as jest.Mock).mockReturnValue({
      id: CREATOR_ID,
      email: 'ro@example.com',
      groups: [],
      effectivePermissions: [{ action: 'view', resource: 'purchases' }],
    });
    (api.getUpcomingPurchases as jest.Mock).mockResolvedValue({ purchases: [ownedPurchase] });
    await loadDashboard();
    expect(cancelBtns()).toHaveLength(0);
  });
});
