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
import { loadRecommendations, resetExpandedCells } from '../recommendations';
import * as api from '../api';

jest.mock('../api', () => ({
  getRecommendations: jest.fn().mockResolvedValue({ summary: {}, recommendations: [], regions: [] }),
  refreshRecommendations: jest.fn(),
  getConfig: jest.fn().mockResolvedValue({ global: {} }),
  listAccounts: jest.fn().mockResolvedValue([]),
  listAccountsMinimal: jest.fn().mockResolvedValue([]),
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
import { ADMINISTRATORS_GROUP_ID, PURCHASER_GROUP_ID } from '../permissions';

const mockUser = (role: string | null) => {
  // 'admin' represents a fully-capable admin: Administrators + Purchaser
  // (mirrors the auto-migration that adds existing admins to Purchaser on
  // first deploy of issue #923). Tests that want to assert admin-alone
  // behaviour (no spending access) should call mockUserWithGroups directly.
  (state.getCurrentUser as jest.Mock).mockReturnValue(
    role === null
      ? null
      : { id: 'u', email: 'u@example.com', groups: role === 'admin' ? [ADMINISTRATORS_GROUP_ID, PURCHASER_GROUP_ID] : [] },
  );
};

// Direct group-set mocking for tests that need to assert specific
// group combinations (e.g. admin WITHOUT Purchaser for the carve-out
// regression checks per CR #924 F5).
const mockUserWithGroups = (groups: string[], effectivePermissions?: { action: string; resource: string }[]) => {
  (state.getCurrentUser as jest.Mock).mockReturnValue(
    { id: 'u', email: 'u@example.com', groups, effectivePermissions },
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

  test('admin WITHOUT Purchaser hides Purchase, keeps Create Plan, shows no-Purchaser notice (CR #924 F3)', async () => {
    // Issue #923 + CR #924 F3: execute:purchases is carved out of
    // admin:*. A bare admin (no Purchaser group, no effectivePermissions
    // yet) sees the Create Plan button (create:plans is NOT carved out)
    // but NOT the Purchase one-off button. The no-Purchaser banner must
    // appear, driven by canAccess('execute', 'purchases') so it stays
    // in lockstep with the Purchase CTA.
    mockUserWithGroups([ADMINISTRATORS_GROUP_ID]);
    await loadRecommendations();
    const purchase = document.getElementById('bulk-purchase-btn') as HTMLButtonElement;
    const plan = document.getElementById('create-plan-btn') as HTMLButtonElement;
    expect(purchase).not.toBeNull();
    expect(plan).not.toBeNull();
    expect(purchase.hidden).toBe(true);
    expect(plan.hidden).toBe(false);
    // No-Purchaser banner present.
    const actionBox = document.getElementById('recommendations-action-box')!;
    const banners = actionBox.querySelectorAll('.info-banner');
    expect(banners.length).toBe(1);
    expect(banners[0]?.textContent).toContain('not execute purchases directly');
    // Row 550: must not imply the user can't plan, and must use the real nav
    // name ("Admin", not "Settings"), and must not claim non-admins can
    // self-serve in Admin → Users.
    expect(banners[0]?.textContent).toContain('Admin → Users');
    expect(banners[0]?.textContent).not.toContain('Settings → Users');
    expect(banners[0]?.textContent).toContain('view and plan');
  });

  test('custom (non-seeded) group with explicit execute:purchases shows Purchase + no banner (CR #924 F3/F4)', async () => {
    // CR #924 F3 + F4: the banner must NOT appear when a custom group
    // (not PURCHASER_GROUP_ID) carries execute:purchases via
    // effectivePermissions. The previous isPurchaser()-only predicate
    // would have shown the contradictory "you can view but not execute"
    // notice alongside a live Purchase CTA.
    const customGid = '00000000-0000-5000-8000-00000000abcd';
    mockUserWithGroups([customGid], [
      { action: 'execute', resource: 'purchases' },
      { action: 'create', resource: 'plans' },
      { action: 'view', resource: 'recommendations' },
    ]);
    await loadRecommendations();
    const purchase = document.getElementById('bulk-purchase-btn') as HTMLButtonElement;
    expect(purchase.hidden).toBe(false);
    // No banner -- the predicate matches the live CTA.
    const actionBox = document.getElementById('recommendations-action-box')!;
    const banners = actionBox.querySelectorAll('.info-banner');
    expect(banners.length).toBe(0);
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

  test('readonly role: grouped-row summary has chevron in leading checkbox-col and column span is aligned (closes #1006)', async () => {
    // Two variants share the same cell key -- buildListMarkup renders a summary row.
    // Issue #1006: the expand chevron must sit in td.checkbox-col at the far-left
    // (before Provider), not inline inside the content cell.
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

    // Header must have a leading th.checkbox-col (empty for viewers, aligns the chevron column).
    const headerCheckboxCols = table!.querySelectorAll('thead tr th.checkbox-col');
    expect(headerCheckboxCols.length).toBe(1);
    // The leading checkbox-col must be the FIRST header cell (position check).
    const firstHeaderTh = table!.querySelector('thead tr th:first-child');
    expect(firstHeaderTh?.classList.contains('checkbox-col')).toBe(true);

    // Summary row must have a td.checkbox-col at the far-left containing the chevron button.
    const summaryCheckboxCols = summaryRow!.querySelectorAll('td.checkbox-col');
    expect(summaryCheckboxCols.length).toBe(1);
    // The leading checkbox-col must be the FIRST summary cell (position check).
    const firstSummaryTd = summaryRow!.querySelector('td:first-child');
    expect(firstSummaryTd?.classList.contains('checkbox-col')).toBe(true);

    // The chevron button lives inside the leading td.checkbox-col, not inline in the content.
    const chevron = summaryCheckboxCols[0]!.querySelector<HTMLButtonElement>('.rec-cell-chevron');
    expect(chevron).not.toBeNull();
    expect(summaryRow!.querySelector('.rec-cell-summary-content .rec-cell-chevron')).toBeNull();

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

// Issue #135 + #869: SP plan-type group child rows must honor showCheckboxes.
// Two SP recs of different plan types in the same (provider, account, region)
// scope group under one SP parent row. When expanded, each per-plan-type child
// cell renders a variant row via buildVariantRowMarkup. Those nested calls must
// forward showCheckboxes so readonly sessions get no checkbox cells, matching
// the non-SP path and the column header (which omits the select-all checkbox).
describe('Recommendations SP-group child-row checkbox gating (issue #135 + #869)', () => {
  // Two AWS savings-plans recs with distinct plan-type slugs but the same
  // scope -> two cell keys -> one SP group with 2 plan types.
  const spRecCompute = {
    ...sampleRec,
    id: 'sp1',
    service: 'savings-plans-compute',
    resource_type: 'compute',
    savings: 300,
  };
  const spRecEc2 = {
    ...sampleRec,
    id: 'sp2',
    service: 'savings-plans-ec2instance',
    resource_type: 'ec2instance',
    savings: 250,
  };

  beforeEach(() => {
    jest.clearAllMocks();
    // expandedSpGroups is module-level state; reset so the SP group starts
    // collapsed in every test and the chevron click reliably expands it.
    resetExpandedCells();
    setupDom();
    (api.getRecommendations as jest.Mock).mockResolvedValue({
      summary: {},
      recommendations: [spRecCompute, spRecEc2],
      regions: [],
    });
    (state.getVisibleRecommendations as jest.Mock).mockReturnValue([spRecCompute, spRecEc2]);
  });

  const expandSpGroup = (): HTMLTableElement => {
    const list = document.getElementById('recommendations-list');
    const table = list?.querySelector('table') as HTMLTableElement | null;
    expect(table).not.toBeNull();
    const chevron = table!.querySelector<HTMLButtonElement>('.rec-sp-group-chevron');
    expect(chevron).not.toBeNull();
    chevron!.click();
    return list!.querySelector('table') as HTMLTableElement;
  };

  test('readonly role: expanded SP-group child variant rows have an empty leading checkbox-col but no checkbox input', async () => {
    mockUser('readonly');
    await loadRecommendations();
    const table = expandSpGroup();

    // The SP group expanded into per-plan-type child variant rows.
    const childRows = table.querySelectorAll('tr.rec-variant-row');
    expect(childRows.length).toBeGreaterThan(0);

    // Issue #1006: variant rows carry an empty td.checkbox-col so columns
    // stay aligned with the leading cell in the summary/header rows.  No
    // checkbox input or action buttons appear for readonly sessions.
    for (const row of Array.from(childRows)) {
      expect(row.querySelectorAll('td.checkbox-col').length).toBe(1);
      // The checkbox-col must be the FIRST cell and contain no text (position + empty-content check).
      const firstTd = row.querySelector('td:first-child');
      expect(firstTd?.classList.contains('checkbox-col')).toBe(true);
      expect(firstTd?.textContent?.trim()).toBe('');
      expect(row.querySelectorAll('input[data-rec-id]').length).toBe(0);
    }
  });

  test('admin role: expanded SP-group child variant rows retain checkbox-col', async () => {
    mockUser('admin');
    await loadRecommendations();
    const table = expandSpGroup();

    const childRows = table.querySelectorAll('tr.rec-variant-row');
    expect(childRows.length).toBeGreaterThan(0);

    for (const row of Array.from(childRows)) {
      expect(row.querySelectorAll('td.checkbox-col').length).toBe(1);
    }
  });
});
