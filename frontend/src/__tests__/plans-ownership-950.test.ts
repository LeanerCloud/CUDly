/**
 * Issue #950: creator-scope ownership gating on Scheduled (Planned) Purchase
 * row action buttons.
 *
 * The pre-fix behaviour gated the Run / Pause / Resume / Edit / Disable
 * buttons purely on the plan-management verbs (update:plans / delete:plans),
 * so a Standard user with those verbs saw actionable buttons on scheduled
 * purchases created by OTHER users. The fix ANDs in
 * canManageScheduledPurchase(): a non-creator without update-any:purchases
 * sees NO action buttons.
 *
 * These tests drive the real loadPlans() render path. They set
 * effectivePermissions on the mock user so the #365 verb gate passes,
 * isolating the new ownership gate as the deciding factor.
 */
import { loadPlans } from '../plans';

jest.mock('../api', () => ({
  getPlans: jest.fn(),
  getPlannedPurchases: jest.fn(),
  listPlanAccounts: jest.fn().mockResolvedValue([]),
}));

jest.mock('../state', () => ({
  getRecommendations: jest.fn().mockReturnValue([]),
  getSelectedRecommendationIDs: jest.fn().mockReturnValue(new Set()),
  getVisibleRecommendations: jest.fn().mockReturnValue([]),
  setVisibleRecommendations: jest.fn(),
  getCurrentProvider: jest.fn().mockReturnValue(''),
  setCurrentProvider: jest.fn(),
  getCurrentAccountIDs: jest.fn().mockReturnValue([]),
  setCurrentAccountIDs: jest.fn(),
  subscribeProvider: jest.fn().mockReturnValue(() => {}),
  subscribeAccount: jest.fn().mockReturnValue(() => {}),
  getCurrentUser: jest.fn(),
  getPlansColumnFilters: jest.fn().mockReturnValue({}),
  setPlansColumnFilter: jest.fn(),
  clearAllPlansColumnFilters: jest.fn(),
}));

jest.mock('../history', () => ({ viewPlanHistory: jest.fn() }));

import * as api from '../api';
import * as state from '../state';

const CREATOR_ID = 'creator-aaaa';
const OTHER_ID = 'other-bbbb';

const samplePlan = {
  id: 'plan-1',
  name: 'Sample Plan',
  enabled: true,
  auto_purchase: true,
  services: {
    ec2: { provider: 'aws', service: 'ec2', enabled: true, term: 1, payment: 'all-upfront', coverage: 80 },
  },
  ramp_schedule: { type: 'immediate', percent_per_step: 100, step_interval_days: 0, current_step: 1, total_steps: 4 },
};

// A scheduled purchase created by CREATOR_ID.
const ownedPurchase = {
  id: 'pp-1',
  plan_id: 'plan-1',
  plan_name: 'Sample Plan',
  scheduled_date: '2026-06-01T00:00:00Z',
  provider: 'aws',
  service: 'ec2',
  resource_type: 't3.medium',
  region: 'us-east-1',
  count: 5,
  term: 1,
  payment: 'all-upfront',
  upfront_cost: 100,
  estimated_savings: 50,
  step_number: 1,
  total_steps: 4,
  status: 'pending',
  created_by_user_id: CREATOR_ID,
};

// Legacy row with no creator (pre-migration NULL).
const legacyPurchase = { ...ownedPurchase, created_by_user_id: undefined as string | undefined };

// A user holding the plan-management verbs + update:purchases (so the #365
// verb gate passes), optionally update-any:purchases. id identifies the
// session user for the ownership comparison.
const setUser = (
  id: string,
  opts: { updateAny?: boolean; cancelOwn?: boolean; cancelAny?: boolean; deletePurchases?: boolean } = {},
) => {
  const effectivePermissions: Array<{ action: string; resource: string }> = [
    { action: 'update', resource: 'plans' },
    { action: 'delete', resource: 'plans' },
    { action: 'execute', resource: 'purchases' },
    { action: 'update', resource: 'purchases' },
  ];
  // deletePurchases defaults to true to preserve existing test behaviour
  // (original setUser always included delete:purchases); callers that
  // represent Standard users (cancel-own only) must pass deletePurchases: false.
  if (opts.deletePurchases !== false) {
    effectivePermissions.push({ action: 'delete', resource: 'purchases' });
  }
  if (opts.updateAny) {
    effectivePermissions.push({ action: 'update-any', resource: 'purchases' });
  }
  if (opts.cancelOwn) {
    effectivePermissions.push({ action: 'cancel-own', resource: 'purchases' });
  }
  if (opts.cancelAny) {
    effectivePermissions.push({ action: 'cancel-any', resource: 'purchases' });
  }
  (state.getCurrentUser as jest.Mock).mockReturnValue({
    id,
    email: `${id}@example.com`,
    groups: [],
    effectivePermissions,
  });
};

const ppHtml = (): string => (document.getElementById('planned-purchases-list') as HTMLElement).innerHTML;

const setupDom = () => {
  const btn = document.createElement('button');
  btn.id = 'new-plan-btn';
  const list = document.createElement('div');
  list.id = 'plans-list';
  const planned = document.createElement('div');
  planned.id = 'planned-purchases-list';
  document.body.replaceChildren(btn, list, planned);
};

const ACTIONS = ['run', 'pause', 'resume', 'edit', 'disable'];

describe('Scheduled-purchase ownership gating (issue #950)', () => {
  beforeEach(() => {
    jest.clearAllMocks();
    setupDom();
    (api.getPlans as jest.Mock).mockResolvedValue({ plans: [samplePlan] });
  });

  test("creator sees action buttons on their own scheduled purchase", async () => {
    setUser(CREATOR_ID);
    (api.getPlannedPurchases as jest.Mock).mockResolvedValue({ purchases: [ownedPurchase] });
    await loadPlans();
    const html = ppHtml();
    // run/pause are status-dependent (pending -> run+pause shown).
    expect(html).toContain('data-action="run"');
    expect(html).toContain('data-action="pause"');
    expect(html).toContain('data-action="edit"');
    expect(html).toContain('data-action="disable"');
  });

  test("non-creator with the same verbs sees NO action buttons (the bug)", async () => {
    // The deciding factor is ownership: this user holds update:plans /
    // delete:plans / update:purchases but did NOT create the row.
    setUser(OTHER_ID);
    (api.getPlannedPurchases as jest.Mock).mockResolvedValue({ purchases: [ownedPurchase] });
    await loadPlans();
    const html = ppHtml();
    ACTIONS.forEach((act) => expect(html).not.toContain(`data-action="${act}"`));
    // The row itself still renders (status badge visible), just no buttons.
    expect(html).toContain('Sample Plan');
  });

  test("update-any holder sees buttons on another user's scheduled purchase", async () => {
    setUser(OTHER_ID, { updateAny: true });
    (api.getPlannedPurchases as jest.Mock).mockResolvedValue({ purchases: [ownedPurchase] });
    await loadPlans();
    const html = ppHtml();
    expect(html).toContain('data-action="run"');
    expect(html).toContain('data-action="pause"');
    expect(html).toContain('data-action="edit"');
    expect(html).toContain('data-action="disable"');
  });

  test("legacy NULL-creator row shows no buttons for a non-update-any user", async () => {
    setUser(CREATOR_ID);
    (api.getPlannedPurchases as jest.Mock).mockResolvedValue({ purchases: [legacyPurchase] });
    await loadPlans();
    const html = ppHtml();
    ACTIONS.forEach((act) => expect(html).not.toContain(`data-action="${act}"`));
  });
});

describe('Plans-page Disable-button cancel-own gating (issue #1442)', () => {
  // Standard users hold cancel-own:purchases (not delete:purchases) in
  // DefaultUserPermissions. Before this fix, the canDisablePlan gate required
  // delete:purchases so the Disable button was hidden even for the creator of
  // the scheduled purchase. The fix accepts cancel-own:purchases and
  // cancel-any:purchases in addition to delete:purchases, mirroring the backend
  // requireDeleteOrCancelPurchasePermission gate introduced by PR #1421.
  beforeEach(() => {
    jest.clearAllMocks();
    setupDom();
    (api.getPlans as jest.Mock).mockResolvedValue({ plans: [samplePlan] });
  });

  test('Standard user (cancel-own, no delete:purchases) sees Disable on their own purchase', async () => {
    // Represents: Plan Author / Standard User role with cancel-own:purchases
    // but NOT delete:purchases. Creator must see the Disable button.
    setUser(CREATOR_ID, { deletePurchases: false, cancelOwn: true });
    (api.getPlannedPurchases as jest.Mock).mockResolvedValue({ purchases: [ownedPurchase] });
    await loadPlans();
    expect(ppHtml()).toContain('data-action="disable"');
  });

  test('Standard user (cancel-own) does NOT see Disable on another user\'s purchase', async () => {
    // Ownership gate (canManageScheduledPurchase) must block cancel-own users
    // from managing rows they did not create.
    setUser(OTHER_ID, { deletePurchases: false, cancelOwn: true });
    (api.getPlannedPurchases as jest.Mock).mockResolvedValue({ purchases: [ownedPurchase] });
    await loadPlans();
    const html = ppHtml();
    expect(html).not.toContain('data-action="disable"');
    // The row still renders; only the action button is suppressed.
    expect(html).toContain('Sample Plan');
  });

  test('Standard user (cancel-own) sees NO Disable on a legacy NULL-creator row', async () => {
    // NULL created_by_user_id means ownership cannot be determined.
    // The button must stay hidden (matches backend: NULL-creator rows are
    // out of reach for non-full-scope users).
    setUser(CREATOR_ID, { deletePurchases: false, cancelOwn: true });
    (api.getPlannedPurchases as jest.Mock).mockResolvedValue({ purchases: [legacyPurchase] });
    await loadPlans();
    expect(ppHtml()).not.toContain('data-action="disable"');
  });

  test('cancel-any holder sees Disable on their own row (full-scope via canManagePurchase)', async () => {
    // cancel-any:purchases grants operator-scope cancel on the backend.
    // The UX gate shows Disable when the user is the creator AND holds
    // cancel-any (canManagePurchase true via creator-match, canDisablePlan
    // true via cancel-any verb). Full-scope visibility for non-creators
    // requires update-any, tracked separately.
    setUser(CREATOR_ID, { deletePurchases: false, cancelAny: true });
    (api.getPlannedPurchases as jest.Mock).mockResolvedValue({ purchases: [ownedPurchase] });
    await loadPlans();
    expect(ppHtml()).toContain('data-action="disable"');
  });
});
