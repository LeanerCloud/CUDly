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

import * as state from '../state';

const mockUser = (role: string | null) => {
  (state.getCurrentUser as jest.Mock).mockReturnValue(
    role === null ? null : { id: 'u', email: 'u@example.com', role },
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

  test('user role hides Purchase but keeps Create Plan', async () => {
    mockUser('user');
    await loadRecommendations();
    const purchase = document.getElementById('bulk-purchase-btn') as HTMLButtonElement;
    const plan = document.getElementById('create-plan-btn') as HTMLButtonElement;
    expect(purchase.hidden).toBe(true);
    expect(plan.hidden).toBe(false);
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

  test('the action-box capacity input stays visible for every role', async () => {
    // Readonly users still browse the bottom box for the selection summary;
    // only the mutating CTAs disappear.
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

  test('user (operator) role: select-all checkbox is present', async () => {
    mockUser('user');
    await loadRecommendations();
    expect(document.getElementById('select-all-recs')).not.toBeNull();
  });

  test('user (operator) role: per-row checkbox is present', async () => {
    mockUser('user');
    await loadRecommendations();
    const list = document.getElementById('recommendations-list');
    const rowCheckboxes = list?.querySelectorAll('input[data-rec-id]') ?? [];
    expect(rowCheckboxes.length).toBeGreaterThan(0);
  });
});
