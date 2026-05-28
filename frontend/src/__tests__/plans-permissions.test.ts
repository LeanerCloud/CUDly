/**
 * Plans page permission-gating tests for issue #365.
 *
 * Covers both:
 *   * top-level "New Plan" button (#new-plan-btn) hidden for sessions
 *     that lack `create:plans`;
 *   * per-card action buttons (Add Purchases / Edit / Delete / toggle)
 *     hidden for sessions that lack `update:plans` / `delete:plans`;
 *   * planned-purchase row buttons (Run / Pause / Resume / Edit /
 *     Disable) hidden for sessions that lack `update:plans` /
 *     `delete:plans`.
 *
 * The role-to-default-permission map under test lives in
 * `frontend/src/permissions.ts` and mirrors backend defaults from
 * `internal/auth/types.go`. The permissions.test.ts file is the
 * canary if that mirror drifts.
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
import { ADMINISTRATORS_GROUP_ID } from '../permissions';

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

const samplePlannedPurchase = {
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
};

const mockUser = (role: string | null) => {
  (state.getCurrentUser as jest.Mock).mockReturnValue(
    role === null ? null : { id: 'u', email: 'u@example.com', groups: role === 'admin' ? [ADMINISTRATORS_GROUP_ID] : [] },
  );
};

const setupDom = () => {
  const btn = document.createElement('button');
  btn.id = 'new-plan-btn';
  btn.className = 'primary';
  btn.textContent = 'New Plan';

  const list = document.createElement('div');
  list.id = 'plans-list';

  const planned = document.createElement('div');
  planned.id = 'planned-purchases-list';

  document.body.replaceChildren(btn, list, planned);
};

describe('Plans page permission gating (issue #365)', () => {
  beforeEach(() => {
    jest.clearAllMocks();
    setupDom();
    (api.getPlans as jest.Mock).mockResolvedValue({ plans: [samplePlan] });
    (api.getPlannedPurchases as jest.Mock).mockResolvedValue({ purchases: [samplePlannedPurchase] });
  });

  describe('admin role', () => {
    beforeEach(() => mockUser('admin'));

    test('shows the top-level New Plan button', async () => {
      await loadPlans();
      const btn = document.getElementById('new-plan-btn') as HTMLButtonElement;
      expect(btn.hidden).toBe(false);
    });

    test('renders every plan-card action button', async () => {
      await loadPlans();
      const list = document.getElementById('plans-list') as HTMLElement;
      const html = list.innerHTML;
      expect(html).toContain('data-action="add-purchases"');
      expect(html).toContain('data-action="edit-plan"');
      expect(html).toContain('data-action="delete-plan"');
      expect(html).toContain('data-action="toggle-plan"');
    });

    test('renders every planned-purchase row action button', async () => {
      await loadPlans();
      const pp = document.getElementById('planned-purchases-list') as HTMLElement;
      const html = pp.innerHTML;
      expect(html).toContain('data-action="run"');
      expect(html).toContain('data-action="pause"');
      expect(html).toContain('data-action="edit"');
      expect(html).toContain('data-action="disable"');
    });
  });

  describe('non-admin user (PR #912: canAccess returns false without /me/permissions)', () => {
    beforeEach(() => mockUser('user'));

    test('hides the top-level New Plan button (no /me/permissions endpoint yet)', async () => {
      // PR #912: canAccess() is group-membership-only; non-Administrators-group
      // members get false until the /me/permissions endpoint lands.
      await loadPlans();
      const btn = document.getElementById('new-plan-btn') as HTMLButtonElement;
      expect(btn.hidden).toBe(true);
    });

    test('hides plan-card action buttons for non-admin (no /me/permissions endpoint yet)', async () => {
      await loadPlans();
      const list = document.getElementById('plans-list') as HTMLElement;
      const html = list.innerHTML;
      expect(html).not.toContain('data-action="add-purchases"');
      expect(html).not.toContain('data-action="edit-plan"');
      expect(html).not.toContain('data-action="delete-plan"');
      expect(html).not.toContain('data-action="toggle-plan"');
      // History view stays visible regardless.
      expect(html).toContain('data-action="view-history"');
    });

    test('hides planned-purchase row action buttons for non-admin (no /me/permissions endpoint yet)', async () => {
      await loadPlans();
      const pp = document.getElementById('planned-purchases-list') as HTMLElement;
      const html = pp.innerHTML;
      expect(html).not.toContain('data-action="run"');
      expect(html).not.toContain('data-action="pause"');
      expect(html).not.toContain('data-action="edit"');
      expect(html).not.toContain('data-action="disable"');
    });
  });

  describe('readonly role', () => {
    beforeEach(() => mockUser('readonly'));

    test('hides the top-level New Plan button', async () => {
      await loadPlans();
      const btn = document.getElementById('new-plan-btn') as HTMLButtonElement;
      expect(btn.hidden).toBe(true);
    });

    test('hides every plan-card action button (History stays)', async () => {
      await loadPlans();
      const list = document.getElementById('plans-list') as HTMLElement;
      const html = list.innerHTML;
      expect(html).not.toContain('data-action="add-purchases"');
      expect(html).not.toContain('data-action="edit-plan"');
      expect(html).not.toContain('data-action="delete-plan"');
      expect(html).not.toContain('data-action="toggle-plan"');
      // History view stays. Readonly users still need to inspect past activity.
      expect(html).toContain('data-action="view-history"');
    });

    test('hides every planned-purchase row action button', async () => {
      await loadPlans();
      const pp = document.getElementById('planned-purchases-list') as HTMLElement;
      const html = pp.innerHTML;
      expect(html).not.toContain('data-action="run"');
      expect(html).not.toContain('data-action="pause"');
      expect(html).not.toContain('data-action="resume"');
      expect(html).not.toContain('data-action="edit"');
      expect(html).not.toContain('data-action="disable"');
    });
  });

  describe('logged-out / null user', () => {
    beforeEach(() => mockUser(null));

    test('hides the top-level New Plan button', async () => {
      await loadPlans();
      const btn = document.getElementById('new-plan-btn') as HTMLButtonElement;
      expect(btn.hidden).toBe(true);
    });

    test('hides every mutating button on the plans list and planned-purchase rows', async () => {
      await loadPlans();
      const list = document.getElementById('plans-list') as HTMLElement;
      const pp = document.getElementById('planned-purchases-list') as HTMLElement;
      const listHtml = list.innerHTML;
      const ppHtml = pp.innerHTML;
      ['add-purchases', 'edit-plan', 'delete-plan', 'toggle-plan'].forEach((act) => {
        expect(listHtml).not.toContain(`data-action="${act}"`);
      });
      ['run', 'pause', 'resume', 'edit', 'disable'].forEach((act) => {
        expect(ppHtml).not.toContain(`data-action="${act}"`);
      });
    });
  });
});
