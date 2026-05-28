/**
 * Recommendations permission gating tests.
 *
 * Issue #365: bottom-action-box CTA gating.
 *   The bottom-action box stays visible for every signed-in role
 *   (read-only browsing is in scope), but the two mutating CTAs are
 *   hidden when the role lacks the underlying verb:
 *     * #bulk-purchase-btn ("Purchase" one-off) -- `execute:purchases`
 *     * #create-plan-btn   ("Create Plan")       -- `create:plans`
 *
 * Issue #869: checkbox column + row-click gating for viewer (readonly).
 *   Viewer has no purchase/plan actions so the Select-all checkbox,
 *   per-row checkboxes, and row-click selection are all hidden/inert.
 *   Admin and user (operator) roles are unchanged.
 *
 * Issue #120: per-row "Plan" button deep-links into the Create Purchase
 *   Plan modal with the row's rec pre-seeded. The button follows the
 *   same create:plans permission gate.
 */
import { loadRecommendations } from '../recommendations';
import * as api from '../api';

jest.mock('../api', () => ({
  getRecommendations: jest.fn().mockResolvedValue({ summary: {}, recommendations: [], regions: [] }),
  refreshRecommendations: jest.fn(),
  getConfig: jest.fn().mockResolvedValue({ global: {} }),
  listAccounts: jest.fn().mockResolvedValue([]),
  listAccountServiceOverrides: jest.fn().mockResolvedValue([]),
}));

jest.mock('../api/recommendations', () => ({
  getRecommendationsFreshness: jest.fn().mockResolvedValue({
    last_collected_at: new Date().toISOString(),
    last_collection_error: null,
  }),
  refreshRecommendations: jest.fn().mockResolvedValue({}),
}));

jest.mock('../state', () => ({
  getCurrentProvider: jest.fn().mockReturnValue('all'),
  setCurrentProvider: jest.fn(),
  getCurrentAccountIDs: jest.fn().mockReturnValue([]),
  setCurrentAccountIDs: jest.fn(),
  getRecommendations: jest.fn().mockReturnValue([]),
  getRecommendationByID: jest.fn().mockReturnValue(undefined),
  setRecommendations: jest.fn(),
  getSelectedRecommendationIDs: jest.fn().mockReturnValue(new Set()),
  clearSelectedRecommendations: jest.fn(),
  addSelectedRecommendation: jest.fn(),
  removeSelectedRecommendation: jest.fn(),
  getRecommendationsSort: jest.fn().mockReturnValue({ column: 'savings', direction: 'desc' }),
  setRecommendationsSort: jest.fn(),
  getRecommendationsColumnFilters: jest.fn().mockReturnValue({}),
  setRecommendationsColumnFilter: jest.fn(),
  clearAllRecommendationsColumnFilters: jest.fn(),
  getVisibleRecommendations: jest.fn().mockReturnValue([]),
  setVisibleRecommendations: jest.fn(),
  getCostPeriod: jest.fn().mockReturnValue('monthly'),
  setCostPeriod: jest.fn(),
  getHiddenColumns: jest.fn().mockReturnValue(new Set()),
  setHiddenColumns: jest.fn(),
  getCurrentUser: jest.fn(),
}));

jest.mock('../toast', () => ({
  showToast: jest.fn().mockReturnValue({ dismiss: jest.fn() }),
}));

// openCreatePlanFromBottomBox calls `await import('./plans')` at runtime.
// Mock the module so the per-row Plan button click test can assert on
// the call without rendering the full modal DOM.
const mockOpenCreatePlanModal = jest.fn();
jest.mock('../plans', () => ({
  openCreatePlanModal: (...args: unknown[]) => mockOpenCreatePlanModal(...args),
  setupPlanHandlers: jest.fn(),
  loadPlans: jest.fn().mockResolvedValue(undefined),
  closePlanModal: jest.fn(),
  closePurchaseModal: jest.fn(),
  openNewPlanModal: jest.fn(),
}));

import * as state from '../state';
import { ADMINISTRATORS_GROUP_ID } from '../permissions';

const mockUser = (role: string | null) => {
  (state.getCurrentUser as jest.Mock).mockReturnValue(
    role === null ? null : { id: 'u', email: 'u@example.com', groups: role === 'admin' ? [ADMINISTRATORS_GROUP_ID] : [] },
  );
};

const setupDom = () => {
  const recsTab = document.createElement('div');
  recsTab.id = 'opportunities-tab';
  recsTab.className = 'tab-content active';
  const summary = document.createElement('div');
  summary.id = 'recommendations-summary';
  const list = document.createElement('div');
  list.id = 'recommendations-list';
  recsTab.appendChild(summary);
  recsTab.appendChild(list);
  document.body.replaceChildren(recsTab);
};

// Minimal recommendation fixture for issue #869 checkbox tests.
const sampleRec = {
  id: 'r1',
  provider: 'aws',
  cloud_account_id: 'acct1',
  service: 'ec2',
  resource_type: 't3.medium',
  region: 'us-east-1',
  count: 1,
  term: 1,
  payment: 'all-upfront',
  savings: 200,
  upfront_cost: 1000,
  monthly_cost: null,
  on_demand_cost: null,
};

// Two variants sharing a cell key -- same resource_type/region/service/account but
// different term -- so buildListMarkup groups them into a summary row.
const sampleRecVariantA = { ...sampleRec, id: 'r1', term: 1, payment: 'all-upfront', savings: 200 };
const sampleRecVariantB = { ...sampleRec, id: 'r2', term: 3, payment: 'no-upfront', savings: 350 };

describe('Recommendations action-box permission gating (issue #365)', () => {
  beforeEach(() => {
    jest.clearAllMocks();
    setupDom();
  });

  test('admin sees both Purchase and Create Plan buttons', async () => {
    mockUser('admin');
    await loadRecommendations();
    const purchase = document.getElementById('bulk-purchase-btn') as HTMLButtonElement;
    const plan = document.getElementById('create-plan-btn') as HTMLButtonElement;
    expect(purchase).not.toBeNull();
    expect(plan).not.toBeNull();
    expect(purchase.hidden).toBe(false);
    expect(plan.hidden).toBe(false);
  });

  test('non-admin user hides both Purchase and Create Plan (PR #912: no /me/permissions yet)', async () => {
    // PR #912: canAccess() returns false for non-admin users until the
    // /me/permissions endpoint lands. All non-admin users see the same
    // read-only view as before.
    mockUser('user');
    await loadRecommendations();
    const purchase = document.getElementById('bulk-purchase-btn') as HTMLButtonElement;
    const plan = document.getElementById('create-plan-btn') as HTMLButtonElement;
    expect(purchase.hidden).toBe(true);
    expect(plan.hidden).toBe(true);
  });

  test('readonly role hides both Purchase and Create Plan', async () => {
    mockUser('readonly');
    await loadRecommendations();
    const purchase = document.getElementById('bulk-purchase-btn') as HTMLButtonElement;
    const plan = document.getElementById('create-plan-btn') as HTMLButtonElement;
    expect(purchase.hidden).toBe(true);
    expect(plan.hidden).toBe(true);
  });

  test('null user hides both buttons', async () => {
    mockUser(null);
    await loadRecommendations();
    const purchase = document.getElementById('bulk-purchase-btn') as HTMLButtonElement;
    const plan = document.getElementById('create-plan-btn') as HTMLButtonElement;
    expect(purchase.hidden).toBe(true);
    expect(plan.hidden).toBe(true);
  });

  test('the action-box capacity input stays visible for all sessions', async () => {
    // Non-mutating elements stay visible regardless of group membership;
    // only the action CTAs gate on permissions.
    for (const role of ['admin', 'user', 'readonly']) {
      setupDom();
      mockUser(role);
      await loadRecommendations();
      expect(document.getElementById('bulk-purchase-capacity')).not.toBeNull();
      expect(document.getElementById('recommendations-action-summary')).not.toBeNull();
    }
  });
});

describe('Recommendations checkbox + row-click gating for viewer role (issue #869)', () => {
  beforeEach(() => {
    jest.clearAllMocks();
    setupDom();
    // Seed one recommendation so the table actually renders rows.
    (api.getRecommendations as jest.Mock).mockResolvedValue({
      summary: {},
      recommendations: [sampleRec],
      regions: [],
    });
    (state.getVisibleRecommendations as jest.Mock).mockReturnValue([sampleRec]);
  });

  test('readonly role: no select-all checkbox in table header', async () => {
    mockUser('readonly');
    await loadRecommendations();
    expect(document.getElementById('select-all-recs')).toBeNull();
  });

  test('readonly role: no per-row checkboxes in table body', async () => {
    mockUser('readonly');
    await loadRecommendations();
    const list = document.getElementById('recommendations-list');
    const rowCheckboxes = list?.querySelectorAll('input[data-rec-id]') ?? [];
    expect(rowCheckboxes.length).toBe(0);
  });

  test('readonly role: row click does not trigger selection state change', async () => {
    mockUser('readonly');
    await loadRecommendations();
    const list = document.getElementById('recommendations-list');
    const row = list?.querySelector<HTMLTableRowElement>('tr.recommendation-row');
    expect(row).not.toBeNull();
    // Simulate a click on the row body (not on a checkbox, which is absent).
    row!.click();
    expect(state.addSelectedRecommendation).not.toHaveBeenCalled();
  });

  test('admin role: select-all checkbox is present', async () => {
    mockUser('admin');
    await loadRecommendations();
    expect(document.getElementById('select-all-recs')).not.toBeNull();
  });

  test('admin role: per-row checkbox is present', async () => {
    mockUser('admin');
    await loadRecommendations();
    const list = document.getElementById('recommendations-list');
    const rowCheckboxes = list?.querySelectorAll('input[data-rec-id]') ?? [];
    expect(rowCheckboxes.length).toBeGreaterThan(0);
  });

  test('non-admin user: no select-all checkbox (PR #912: canAccess returns false without /me/permissions)', async () => {
    // PR #912: only Administrators-group members get checkboxes.
    // The /me/permissions endpoint that would restore Standard Users'
    // checkbox access is deferred to a follow-up.
    mockUser('user');
    await loadRecommendations();
    expect(document.getElementById('select-all-recs')).toBeNull();
  });

  test('non-admin user: no per-row checkboxes (PR #912: canAccess returns false without /me/permissions)', async () => {
    mockUser('user');
    await loadRecommendations();
    const list = document.getElementById('recommendations-list');
    const rowCheckboxes = list?.querySelectorAll('input[data-rec-id]') ?? [];
    expect(rowCheckboxes.length).toBe(0);
  });

  test('readonly role: grouped-row summary has no checkbox-col and column span is aligned', async () => {
    // Two variants share the same cell key -- buildListMarkup renders a summary row.
    (api.getRecommendations as jest.Mock).mockResolvedValue({
      summary: {},
      recommendations: [sampleRecVariantA, sampleRecVariantB],
      regions: [],
    });
    (state.getVisibleRecommendations as jest.Mock).mockReturnValue([sampleRecVariantA, sampleRecVariantB]);
    mockUser('readonly');
    await loadRecommendations();

    const list = document.getElementById('recommendations-list');
    const table = list?.querySelector('table');
    expect(table).not.toBeNull();

    const summaryRow = table!.querySelector('tr.rec-cell-summary-row');
    expect(summaryRow).not.toBeNull();

    // Header must not contain a checkbox-col th.
    const headerCheckboxCols = table!.querySelectorAll('thead tr th.checkbox-col');
    expect(headerCheckboxCols.length).toBe(0);

    // Summary row must not contain a checkbox-col td (the bug this PR fixes).
    const summaryCheckboxCols = summaryRow!.querySelectorAll('td.checkbox-col');
    expect(summaryCheckboxCols.length).toBe(0);

    // Effective column count: sum of colspan values in each row must match header th count.
    const headerColCount = table!.querySelectorAll('thead tr th').length;
    const summaryEffectiveCols = Array.from(summaryRow!.querySelectorAll('td'))
      .reduce((sum, td) => sum + (td.colSpan || 1), 0);
    expect(summaryEffectiveCols).toBe(headerColCount);
  });

  test('admin role: grouped-row summary retains checkbox-col and column span is aligned', async () => {
    (api.getRecommendations as jest.Mock).mockResolvedValue({
      summary: {},
      recommendations: [sampleRecVariantA, sampleRecVariantB],
      regions: [],
    });
    (state.getVisibleRecommendations as jest.Mock).mockReturnValue([sampleRecVariantA, sampleRecVariantB]);
    mockUser('admin');
    await loadRecommendations();

    const list = document.getElementById('recommendations-list');
    const table = list?.querySelector('table');
    expect(table).not.toBeNull();

    const summaryRow = table!.querySelector('tr.rec-cell-summary-row');
    expect(summaryRow).not.toBeNull();

    // Header must have a checkbox-col th for admin.
    const headerCheckboxCols = table!.querySelectorAll('thead tr th.checkbox-col');
    expect(headerCheckboxCols.length).toBe(1);

    // Summary row must have a checkbox-col td for admin.
    const summaryCheckboxCols = summaryRow!.querySelectorAll('td.checkbox-col');
    expect(summaryCheckboxCols.length).toBe(1);

    // Effective column count must match.
    const headerColCount = table!.querySelectorAll('thead tr th').length;
    const summaryEffectiveCols = Array.from(summaryRow!.querySelectorAll('td'))
      .reduce((sum, td) => sum + (td.colSpan || 1), 0);
    expect(summaryEffectiveCols).toBe(headerColCount);
  });
});

// Shared rec fixture used by the per-row Plan button tests below.
const sampleRec120 = {
  id: 'rec-120',
  provider: 'aws',
  cloud_account_id: 'acc-1',
  service: 'ec2',
  resource_type: 't3.small',
  region: 'us-east-1',
  count: 2,
  term: 1,
  payment: 'all-upfront',
  savings: 150,
  upfront_cost: 500,
};

// Helper: seed the api + state mocks with one rec so the table renders a row.
const seedOneRec = () => {
  (api.getRecommendations as jest.Mock).mockResolvedValue({
    summary: {},
    recommendations: [sampleRec120],
    regions: [],
  });
  (state.getRecommendations as jest.Mock).mockReturnValue([sampleRec120]);
  (state.getVisibleRecommendations as jest.Mock).mockReturnValue([sampleRec120]);
  (state.getSelectedRecommendationIDs as jest.Mock).mockReturnValue(new Set());
};

describe('Per-row Plan button deep-link (issue #120)', () => {
  beforeEach(() => {
    jest.clearAllMocks();
    mockOpenCreatePlanModal.mockReset();
    setupDom();
  });

  test('admin sees a per-row Plan button when rows are rendered', async () => {
    mockUser('admin');
    seedOneRec();
    await loadRecommendations();
    const planBtns = document.querySelectorAll<HTMLButtonElement>('button.rec-plan-btn');
    expect(planBtns.length).toBe(1);
    expect(planBtns[0]!.dataset['recId']).toBe('rec-120');
  });

  test('readonly role has no per-row Plan buttons (create:plans gated)', async () => {
    mockUser('readonly');
    seedOneRec();
    await loadRecommendations();
    const planBtns = document.querySelectorAll<HTMLButtonElement>('button.rec-plan-btn');
    expect(planBtns.length).toBe(0);
  });

  test('clicking per-row Plan button calls openCreatePlanModal with the rec pre-seeded', async () => {
    mockUser('admin');
    seedOneRec();
    await loadRecommendations();
    const planBtn = document.querySelector<HTMLButtonElement>('button.rec-plan-btn[data-rec-id="rec-120"]');
    expect(planBtn).not.toBeNull();
    planBtn!.click();
    // openCreatePlanFromBottomBox does `await import('./plans')` which in
    // CommonJS (ts-jest) compiles to Promise.resolve().then(() => require(...)).
    // Flush the microtask queue with a few awaits so the dynamic import
    // resolves and openCreatePlanModal is called before we assert.
    for (let i = 0; i < 5; i++) await Promise.resolve();
    expect(mockOpenCreatePlanModal).toHaveBeenCalledTimes(1);
    const calledWith = mockOpenCreatePlanModal.mock.calls[0]![0] as unknown[];
    expect(Array.isArray(calledWith)).toBe(true);
    expect((calledWith as { id: string }[])[0]!.id).toBe('rec-120');
  });
});
