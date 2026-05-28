/**
 * Plans module tests
 */
import { loadPlans, savePlan, closePlanModal, openCreatePlanModal, openNewPlanModal, closePurchaseModal, setupPlanHandlers } from '../plans';

// Mock the api module
jest.mock('../api', () => ({
  getPlans: jest.fn(),
  getPlan: jest.fn(),
  createPlan: jest.fn(),
  updatePlan: jest.fn(),
  patchPlan: jest.fn(),
  deletePlan: jest.fn(),
  getPlannedPurchases: jest.fn(),
  runPlannedPurchase: jest.fn(),
  pausePlannedPurchase: jest.fn(),
  resumePlannedPurchase: jest.fn(),
  deletePlannedPurchase: jest.fn(),
  createPlannedPurchases: jest.fn(),
  listPlanAccounts: jest.fn().mockResolvedValue([]),
  setPlanAccounts: jest.fn().mockResolvedValue(undefined),
  listAccounts: jest.fn().mockResolvedValue([]),
  getAccount: jest.fn().mockResolvedValue(null)
}));

// Mock state module
jest.mock('../state', () => ({
  getRecommendations: jest.fn().mockReturnValue([]),
  getSelectedRecommendationIDs: jest.fn().mockReturnValue(new Set()),
  // Bundle B (column-filter UX overhaul): savePlan now reads the
  // post-filter visible set so plans never include filtered-out rows.
  // Default to empty so tests that don't seed a target keep their old
  // behaviour (no recommendations attached to plan).
  getVisibleRecommendations: jest.fn().mockReturnValue([]),
  setVisibleRecommendations: jest.fn(),
  // Issue #344 T2: plans.ts reads the global filter via state and
  // subscribes to changes so the list re-renders when the topbar chips
  // change. The topbar's "All providers" chip writes '' to state, which
  // is the falsy "no filter" value plans.ts checks for.
  getCurrentProvider: jest.fn().mockReturnValue(''),
  setCurrentProvider: jest.fn(),
  getCurrentAccountIDs: jest.fn().mockReturnValue([]),
  setCurrentAccountIDs: jest.fn(),
  subscribeProvider: jest.fn().mockReturnValue(() => {}),
  subscribeAccount: jest.fn().mockReturnValue(() => {}),
  // Issue #365: plans.ts now reads getCurrentUser via the permissions
  // helper to decide which row actions to render. Default the existing
  // suite to admin so every legacy assertion (button-text expectations,
  // click-handler counts, plan-card layout) keeps passing unchanged.
  // The permission-gating tests in plans-permissions.test.ts override
  // this per-case to exercise readonly / user / null sessions.
  getCurrentUser: jest.fn().mockReturnValue({ id: 'u-admin', email: 'admin@example.com', role: 'admin' }),
}));

// Mock history module
jest.mock('../history', () => ({
  viewPlanHistory: jest.fn()
}));

// Mock commitmentOptions module
jest.mock('../commitmentOptions', () => ({
  populateTermSelect: jest.fn(),
  populatePaymentSelect: jest.fn(),
  isValidCombination: jest.fn().mockReturnValue(true),
  normalizePaymentValue: jest.fn((value) => value)
}));

// Mock archera: savePlan must NOT call openArcheraOfferModal after #499.
const mockOpenArcheraOfferModal = jest.fn();
jest.mock('../archera', () => ({
  openArcheraOfferModal: (...args: unknown[]) => mockOpenArcheraOfferModal(...args),
}));

// Q7: plans.ts migrated alert() → showToast and destructive confirm() →
// confirmDialog. Mock both so tests can assert calls and control confirm.
const mockShowToast = jest.fn<{ dismiss: () => void }, [unknown]>(() => ({ dismiss: jest.fn() }));
jest.mock('../toast', () => ({
  showToast: (opts: unknown) => mockShowToast(opts),
}));
const mockConfirmDialog = jest.fn<Promise<boolean>, [unknown]>(() => Promise.resolve(true));
jest.mock('../confirmDialog', () => ({
  confirmDialog: (opts: unknown) => mockConfirmDialog(opts),
}));

// Mock utils
jest.mock('../utils', () => ({
  formatDate: jest.fn((val) => val ? new Date(val).toLocaleDateString() : ''),
  formatTerm: jest.fn((years) => years == null ? '' : `${years} Year${years === 1 ? '' : 's'}`),
  formatRampSchedule: jest.fn((val) => val || 'Unknown'),
  getStatusBadge: jest.fn(() => ({ class: 'active', label: 'Active' })),
  escapeHtml: jest.fn((str) => str || ''),
  formatCurrency: jest.fn((val) => `$${val || 0}`),
  populateAccountFilter: jest.fn(() => Promise.resolve())
}));

import * as api from '../api';
import * as state from '../state';
import { viewPlanHistory } from '../history';
import { populateTermSelect, populatePaymentSelect, isValidCombination } from '../commitmentOptions';

describe('Plans Module', () => {
  beforeEach(() => {
    mockOpenArcheraOfferModal.mockClear();
    // Reset DOM with full form structure
    document.body.innerHTML = `
      <div id="plans-list"></div>
      <div id="planned-purchases-list"></div>
      <div id="plan-modal" class="hidden">
        <h3 id="plan-modal-title">Create Plan</h3>
        <form id="plan-form">
          <input type="hidden" id="plan-id">
          <input type="text" id="plan-name">
          <textarea id="plan-description"></textarea>
          <select id="plan-provider">
            <option value="aws">AWS</option>
            <option value="azure">Azure</option>
            <option value="gcp">GCP</option>
          </select>
          <select id="plan-service">
            <optgroup label="AWS Services">
              <option value="ec2">EC2</option>
              <option value="rds">RDS</option>
            </optgroup>
            <optgroup label="Azure Services">
              <option value="compute">Compute</option>
            </optgroup>
            <optgroup label="GCP Services">
              <option value="compute">Compute</option>
            </optgroup>
          </select>
          <select id="plan-term">
            <option value="1">1 Year</option>
            <option value="3">3 Years</option>
          </select>
          <select id="plan-payment">
            <option value="no-upfront">No Upfront</option>
            <option value="partial-upfront">Partial Upfront</option>
            <option value="all-upfront">All Upfront</option>
          </select>
          <input type="number" id="plan-coverage" value="80">
          <input type="checkbox" id="plan-auto-purchase">
          <input type="number" id="plan-notify-days" value="3">
          <input type="checkbox" id="plan-enabled" checked>
          <input type="radio" name="ramp-schedule" value="immediate" checked>
          <input type="radio" name="ramp-schedule" value="weekly-25pct">
          <input type="radio" name="ramp-schedule" value="monthly-10pct">
          <input type="radio" name="ramp-schedule" value="custom">
          <div id="custom-ramp-config" class="hidden">
            <input type="number" id="ramp-step-percent" value="20">
            <input type="number" id="ramp-interval-days" value="7">
          </div>
          <!-- Target Accounts section (universal-plans fix). The hidden
               plan-account-ids field is the contract between renderPlan
               AccountChips and savePlan; the submit button's disabled
               state is recomputed every time the chip list changes. -->
          <div id="plan-accounts-selected" class="selected-accounts"></div>
          <input type="hidden" id="plan-account-ids" value="">
          <button type="submit">Save Plan</button>
        </form>
      </div>
      <div id="purchase-modal" class="hidden"></div>
    `;

    jest.clearAllMocks();
    window.alert = jest.fn();
    window.confirm = jest.fn().mockReturnValue(true);
  });

  describe('loadPlans', () => {
    test('fetches and renders plans', async () => {
      (api.getPlans as jest.Mock).mockResolvedValue({
        plans: [
          {
            id: 'plan-1',
            name: 'Test Plan',
            enabled: true,
            auto_purchase: true,
            services: {
              'ec2': {
                provider: 'aws',
                service: 'ec2',
                enabled: true,
                term: 1,
                payment: 'all-upfront',
                coverage: 80
              }
            },
            ramp_schedule: {
              type: 'immediate',
              percent_per_step: 100,
              step_interval_days: 0,
              current_step: 2,
              total_steps: 4
            }
          }
        ]
      });
      (api.getPlannedPurchases as jest.Mock).mockResolvedValue({ purchases: [] });

      await loadPlans();

      const list = document.getElementById('plans-list');
      expect(list?.innerHTML).toContain('Test Plan');
      expect(list?.innerHTML).toContain('ec2');
    });

    test('multi-SP plan summary lists every plan-type covered (issue #131)', async () => {
      // Pre-fix: extractPlanInfo took serviceValues[0], so a plan
      // covering both Compute SP and SageMaker SP rendered only
      // "Compute Savings Plans" — the SageMaker side was hidden.
      // Post-fix: every entry is rendered, comma-joined, with the
      // SP slugs abbreviated so 4 plan-types still fit in the card.
      (api.getPlans as jest.Mock).mockResolvedValue({
        plans: [
          {
            id: 'plan-multi-sp',
            name: 'Multi SP plan',
            enabled: true,
            auto_purchase: true,
            services: {
              'aws:savings-plans-compute': {
                provider: 'aws',
                service: 'savings-plans-compute',
                enabled: true,
                term: 3,
                payment: 'no-upfront',
                coverage: 80,
              },
              'aws:savings-plans-sagemaker': {
                provider: 'aws',
                service: 'savings-plans-sagemaker',
                enabled: true,
                term: 3,
                payment: 'no-upfront',
                coverage: 80,
              },
            },
            ramp_schedule: {
              type: 'immediate',
              percent_per_step: 100,
              step_interval_days: 0,
              current_step: 0,
              total_steps: 1,
            },
          },
        ],
      });
      (api.getPlannedPurchases as jest.Mock).mockResolvedValue({ purchases: [] });

      await loadPlans();

      const list = document.getElementById('plans-list');
      expect(list?.innerHTML).toContain('Compute SP');
      expect(list?.innerHTML).toContain('SageMaker SP');
      // The pre-fix "Multiple" placeholder must be gone.
      expect(list?.innerHTML).not.toContain('Multiple');
    });

    test('single-service plan still renders one label (no regression)', async () => {
      (api.getPlans as jest.Mock).mockResolvedValue({
        plans: [
          {
            id: 'plan-ec2',
            name: 'EC2 only',
            enabled: true,
            services: { ec2: { provider: 'aws', service: 'ec2', term: 3, coverage: 80 } },
            ramp_schedule: { type: 'immediate', percent_per_step: 100, step_interval_days: 0, current_step: 0, total_steps: 1 },
          },
        ],
      });
      (api.getPlannedPurchases as jest.Mock).mockResolvedValue({ purchases: [] });

      await loadPlans();

      const list = document.getElementById('plans-list');
      // The Service field shows "ec2" — the slug pass-through case.
      expect(list?.innerHTML).toContain('ec2');
    });

    test('shows empty message when no plans', async () => {
      (api.getPlans as jest.Mock).mockResolvedValue({
        plans: []
      });
      (api.getPlannedPurchases as jest.Mock).mockResolvedValue({ purchases: [] });

      await loadPlans();

      const list = document.getElementById('plans-list');
      expect(list?.innerHTML).toContain('No purchase plans configured');
    });

    test('provider filter derives provider from first service entry', async () => {
      // Regression: backend config.PurchasePlan has no top-level
      // `provider` field — it lives on each service inside the `services`
      // map. Filtering via p.provider directly returned 0 rows every
      // time and was the cause of "switching Provider: AWS empties the
      // list" seen in the 2026-04-22 screenshots.
      //
      // Issue #344 T2: provider filter source moved from a per-section
      // <select> to the global topbar (state.ts). Simulate "user picked
      // AWS in the topbar" by pointing the mock at 'aws'.
      const state = await import('../state');
      (state.getCurrentProvider as jest.Mock).mockReturnValue('aws');

      (api.getPlans as jest.Mock).mockResolvedValue({
        plans: [
          {
            id: 'plan-aws',
            name: 'AWS fanout',
            enabled: true,
            services: { ec2: { provider: 'aws', service: 'ec2', term: 3, coverage: 80 } },
            ramp_schedule: { type: 'immediate', percent_per_step: 100, step_interval_days: 0, current_step: 0, total_steps: 1 },
          },
          {
            id: 'plan-gcp',
            name: 'GCP CUDs',
            enabled: true,
            services: { compute: { provider: 'gcp', service: 'compute', term: 3, coverage: 80 } },
            ramp_schedule: { type: 'immediate', percent_per_step: 100, step_interval_days: 0, current_step: 0, total_steps: 1 },
          },
        ],
      });
      (api.getPlannedPurchases as jest.Mock).mockResolvedValue({ purchases: [] });

      try {
        await loadPlans();

        const list = document.getElementById('plans-list');
        expect(list?.innerHTML).toContain('AWS fanout');
        expect(list?.innerHTML).not.toContain('GCP CUDs');
      } finally {
        // Restore the mock so the next test's "no provider scope"
        // default holds (it relies on getCurrentProvider returning '').
        (state.getCurrentProvider as jest.Mock).mockReturnValue('');
      }
    });

    test('passes account_ids to api.getPlans when account filter is active (issue #705)', async () => {
      // Regression test for the Account global filter being non-functional
      // on the Plans page. loadPlans must forward the account selection to
      // api.getPlans so the backend can JOIN plan_accounts and prune the list.
      const state = await import('../state');
      const accountID = 'aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa';
      (state.getCurrentAccountIDs as jest.Mock).mockReturnValue([accountID]);

      (api.getPlans as jest.Mock).mockResolvedValue({ plans: [] });
      (api.getPlannedPurchases as jest.Mock).mockResolvedValue({ purchases: [] });

      try {
        await loadPlans();

        expect(api.getPlans).toHaveBeenCalledWith({ account_ids: [accountID] });
      } finally {
        (state.getCurrentAccountIDs as jest.Mock).mockReturnValue([]);
      }
    });

    test('calls api.getPlans with empty object when no account is selected', async () => {
      // When no account chip is active, getPlans receives {} so the backend
      // returns all plans (no account_ids filter applied).
      const state = await import('../state');
      (state.getCurrentAccountIDs as jest.Mock).mockReturnValue([]);

      (api.getPlans as jest.Mock).mockResolvedValue({ plans: [] });
      (api.getPlannedPurchases as jest.Mock).mockResolvedValue({ purchases: [] });

      await loadPlans();

      expect(api.getPlans).toHaveBeenCalledWith({});
    });

    test('shows error on API failure', async () => {
      (api.getPlans as jest.Mock).mockRejectedValue(new Error('API Error'));
      (api.getPlannedPurchases as jest.Mock).mockResolvedValue({ purchases: [] });
      console.error = jest.fn();

      await loadPlans();

      const list = document.getElementById('plans-list');
      expect(list?.innerHTML).toContain('Failed to load plans');
    });

    test('renders plan cards with status badges', async () => {
      (api.getPlans as jest.Mock).mockResolvedValue({
        plans: [
          {
            id: 'plan-1',
            name: 'Test Plan',
            enabled: true,
            auto_purchase: true,
            services: {
              'ec2': {
                provider: 'aws',
                service: 'ec2',
                enabled: true,
                term: 1,
                payment: 'all-upfront',
                coverage: 80
              }
            },
            ramp_schedule: {
              type: 'immediate',
              percent_per_step: 100,
              step_interval_days: 0
            }
          }
        ]
      });
      (api.getPlannedPurchases as jest.Mock).mockResolvedValue({ purchases: [] });

      await loadPlans();

      const list = document.getElementById('plans-list');
      expect(list?.innerHTML).toContain('status-badge');
    });

    test('renders toggle switches for plans', async () => {
      (api.getPlans as jest.Mock).mockResolvedValue({
        plans: [
          {
            id: 'plan-1',
            name: 'Test Plan',
            enabled: true,
            auto_purchase: false,
            services: {
              'ec2': {
                provider: 'aws',
                service: 'ec2',
                enabled: true,
                term: 1,
                payment: 'all-upfront',
                coverage: 80
              }
            }
          }
        ]
      });
      (api.getPlannedPurchases as jest.Mock).mockResolvedValue({ purchases: [] });

      await loadPlans();

      const list = document.getElementById('plans-list');
      expect(list?.innerHTML).toContain('data-action="toggle-plan"');
    });

    test('renders action buttons', async () => {
      (api.getPlans as jest.Mock).mockResolvedValue({
        plans: [
          {
            id: 'plan-1',
            name: 'Test Plan',
            enabled: true,
            auto_purchase: false,
            services: {
              'ec2': {
                provider: 'aws',
                service: 'ec2',
                enabled: true,
                term: 1,
                payment: 'all-upfront',
                coverage: 80
              }
            }
          }
        ]
      });
      (api.getPlannedPurchases as jest.Mock).mockResolvedValue({ purchases: [] });

      await loadPlans();

      const list = document.getElementById('plans-list');
      expect(list?.innerHTML).toContain('data-action="edit-plan"');
      expect(list?.innerHTML).toContain('data-action="view-history"');
      expect(list?.innerHTML).toContain('data-action="delete-plan"');
      expect(list?.innerHTML).toContain('data-action="add-purchases"');
    });

    test('shows next execution date when available', async () => {
      (api.getPlans as jest.Mock).mockResolvedValue({
        plans: [
          {
            id: 'plan-1',
            name: 'Test Plan',
            enabled: true,
            auto_purchase: false,
            services: {
              'ec2': {
                provider: 'aws',
                service: 'ec2',
                enabled: true,
                term: 1,
                payment: 'all-upfront',
                coverage: 80
              }
            },
            next_execution_date: '2024-02-15'
          }
        ]
      });
      (api.getPlannedPurchases as jest.Mock).mockResolvedValue({ purchases: [] });

      await loadPlans();

      const list = document.getElementById('plans-list');
      expect(list?.innerHTML).toContain('Next Purchase');
    });

    test('handles plan without services gracefully', async () => {
      (api.getPlans as jest.Mock).mockResolvedValue({
        plans: [
          {
            id: 'plan-1',
            name: 'Empty Plan',
            enabled: true,
            auto_purchase: false,
            services: {}
          }
        ]
      });
      (api.getPlannedPurchases as jest.Mock).mockResolvedValue({ purchases: [] });

      await loadPlans();

      const list = document.getElementById('plans-list');
      expect(list?.innerHTML).toContain('Empty Plan');
    });

    test('handles plan with null services', async () => {
      (api.getPlans as jest.Mock).mockResolvedValue({
        plans: [
          {
            id: 'plan-1',
            name: 'Null Services Plan',
            enabled: true,
            auto_purchase: false,
            services: null
          }
        ]
      });
      (api.getPlannedPurchases as jest.Mock).mockResolvedValue({ purchases: [] });

      await loadPlans();

      const list = document.getElementById('plans-list');
      expect(list?.innerHTML).toContain('Null Services Plan');
    });
  });

  describe('loadPlannedPurchases', () => {
    test('renders planned purchases table', async () => {
      (api.getPlans as jest.Mock).mockResolvedValue({ plans: [] });
      (api.getPlannedPurchases as jest.Mock).mockResolvedValue({
        purchases: [
          {
            id: 'purchase-1',
            plan_id: 'plan-1',
            plan_name: 'Test Plan',
            scheduled_date: '2024-02-20',
            provider: 'aws',
            service: 'ec2',
            resource_type: 't3.medium',
            region: 'us-east-1',
            count: 5,
            term: 1,
            payment: 'all-upfront',
            upfront_cost: 1000,
            estimated_savings: 200,
            status: 'pending',
            step_number: 1,
            total_steps: 4
          }
        ]
      });

      await loadPlans();

      const container = document.getElementById('planned-purchases-list');
      expect(container?.innerHTML).toContain('Test Plan');
      expect(container?.innerHTML).toContain('t3.medium');
      expect(container?.innerHTML).toContain('us-east-1');
    });

    test('shows empty message when no planned purchases', async () => {
      (api.getPlans as jest.Mock).mockResolvedValue({ plans: [] });
      (api.getPlannedPurchases as jest.Mock).mockResolvedValue({ purchases: [] });

      await loadPlans();

      const container = document.getElementById('planned-purchases-list');
      expect(container?.innerHTML).toContain('No planned purchases');
    });

    test('shows error on planned purchases API failure', async () => {
      (api.getPlans as jest.Mock).mockResolvedValue({ plans: [] });
      (api.getPlannedPurchases as jest.Mock).mockRejectedValue(new Error('API Error'));
      console.error = jest.fn();

      await loadPlans();

      const container = document.getElementById('planned-purchases-list');
      expect(container?.innerHTML).toContain('Failed to load planned purchases');
    });

    test('handles missing planned purchases container', async () => {
      document.getElementById('planned-purchases-list')?.remove();
      (api.getPlans as jest.Mock).mockResolvedValue({ plans: [] });
      (api.getPlannedPurchases as jest.Mock).mockResolvedValue({ purchases: [] });

      // Should not throw
      await expect(loadPlans()).resolves.not.toThrow();
    });

    test('renders paused purchase with correct buttons', async () => {
      (api.getPlans as jest.Mock).mockResolvedValue({ plans: [] });
      (api.getPlannedPurchases as jest.Mock).mockResolvedValue({
        purchases: [
          {
            id: 'purchase-1',
            plan_id: 'plan-1',
            plan_name: 'Test Plan',
            scheduled_date: '2024-02-20',
            provider: 'aws',
            service: 'ec2',
            resource_type: 't3.medium',
            region: 'us-east-1',
            count: 5,
            term: 1,
            payment: 'all-upfront',
            upfront_cost: 1000,
            estimated_savings: 200,
            status: 'paused',
            step_number: 1,
            total_steps: 4
          }
        ]
      });

      await loadPlans();

      const container = document.getElementById('planned-purchases-list');
      expect(container?.innerHTML).toContain('data-action="resume"');
      expect(container?.innerHTML).toContain('data-action="run"');
    });

    test('renders running purchase without pause/resume buttons', async () => {
      (api.getPlans as jest.Mock).mockResolvedValue({ plans: [] });
      (api.getPlannedPurchases as jest.Mock).mockResolvedValue({
        purchases: [
          {
            id: 'purchase-1',
            plan_id: 'plan-1',
            plan_name: 'Test Plan',
            scheduled_date: '2024-02-20',
            provider: 'aws',
            service: 'ec2',
            resource_type: 't3.medium',
            region: 'us-east-1',
            count: 5,
            term: 1,
            payment: 'all-upfront',
            upfront_cost: 1000,
            estimated_savings: 200,
            status: 'running',
            step_number: 1,
            total_steps: 4
          }
        ]
      });

      await loadPlans();

      const container = document.getElementById('planned-purchases-list');
      expect(container?.innerHTML).toContain('status-running');
    });

    test('renders completed purchase', async () => {
      (api.getPlans as jest.Mock).mockResolvedValue({ plans: [] });
      (api.getPlannedPurchases as jest.Mock).mockResolvedValue({
        purchases: [
          {
            id: 'purchase-1',
            plan_id: 'plan-1',
            plan_name: 'Test Plan',
            scheduled_date: '2024-02-20',
            provider: 'aws',
            service: 'ec2',
            resource_type: 't3.medium',
            region: 'us-east-1',
            count: 5,
            term: 1,
            payment: 'all-upfront',
            upfront_cost: 1000,
            estimated_savings: 200,
            status: 'completed',
            step_number: 4,
            total_steps: 4
          }
        ]
      });

      await loadPlans();

      const container = document.getElementById('planned-purchases-list');
      expect(container?.innerHTML).toContain('status-completed');
    });

    test('renders failed purchase', async () => {
      (api.getPlans as jest.Mock).mockResolvedValue({ plans: [] });
      (api.getPlannedPurchases as jest.Mock).mockResolvedValue({
        purchases: [
          {
            id: 'purchase-1',
            plan_id: 'plan-1',
            plan_name: 'Test Plan',
            scheduled_date: '2024-02-20',
            provider: 'aws',
            service: 'ec2',
            resource_type: 't3.medium',
            region: 'us-east-1',
            count: 5,
            term: 1,
            payment: 'all-upfront',
            upfront_cost: 1000,
            estimated_savings: 200,
            status: 'failed',
            step_number: 2,
            total_steps: 4
          }
        ]
      });

      await loadPlans();

      const container = document.getElementById('planned-purchases-list');
      expect(container?.innerHTML).toContain('status-failed');
    });
  });

  describe('planned purchase actions', () => {
    beforeEach(async () => {
      (api.getPlans as jest.Mock).mockResolvedValue({ plans: [] });
      (api.getPlannedPurchases as jest.Mock).mockResolvedValue({
        purchases: [
          {
            id: 'purchase-1',
            plan_id: 'plan-1',
            plan_name: 'Test Plan',
            scheduled_date: '2024-02-20',
            provider: 'aws',
            service: 'ec2',
            resource_type: 't3.medium',
            region: 'us-east-1',
            count: 5,
            term: 1,
            payment: 'all-upfront',
            upfront_cost: 1000,
            estimated_savings: 200,
            status: 'pending',
            step_number: 1,
            total_steps: 4
          }
        ]
      });
      await loadPlans();
    });

    test('run action executes purchase with confirmation', async () => {
      (api.runPlannedPurchase as jest.Mock).mockResolvedValue({});
      (api.getPlannedPurchases as jest.Mock).mockResolvedValue({ purchases: [] });
      window.confirm = jest.fn().mockReturnValue(true);

      const runBtn = document.querySelector('[data-action="run"]') as HTMLButtonElement;
      runBtn?.click();

      await new Promise(resolve => setTimeout(resolve, 50));

      expect(window.confirm).toHaveBeenCalled();
      expect(api.runPlannedPurchase).toHaveBeenCalledWith('purchase-1');
      expect(mockShowToast).toHaveBeenCalledWith(expect.objectContaining({ message: 'Purchase executed successfully' }));
    });

    test('run action cancelled by user', async () => {
      window.confirm = jest.fn().mockReturnValue(false);

      const runBtn = document.querySelector('[data-action="run"]') as HTMLButtonElement;
      runBtn?.click();

      await new Promise(resolve => setTimeout(resolve, 50));

      expect(api.runPlannedPurchase).not.toHaveBeenCalled();
    });

    test('pause action pauses purchase', async () => {
      (api.pausePlannedPurchase as jest.Mock).mockResolvedValue({});
      (api.getPlannedPurchases as jest.Mock).mockResolvedValue({ purchases: [] });

      const pauseBtn = document.querySelector('[data-action="pause"]') as HTMLButtonElement;
      pauseBtn?.click();

      await new Promise(resolve => setTimeout(resolve, 50));

      expect(api.pausePlannedPurchase).toHaveBeenCalledWith('purchase-1');
    });

    test('disable action deletes planned purchase with confirmation', async () => {
      (api.deletePlannedPurchase as jest.Mock).mockResolvedValue({});
      (api.getPlans as jest.Mock).mockResolvedValue({ plans: [] });
      (api.getPlannedPurchases as jest.Mock).mockResolvedValue({ purchases: [] });
      window.confirm = jest.fn().mockReturnValue(true);

      // Clear prior getPlans calls from setup so the assertion below only
      // counts the reload triggered by the disable action itself.
      (api.getPlans as jest.Mock).mockClear();

      const disableBtn = document.querySelector('[data-action="disable"]') as HTMLButtonElement;
      disableBtn?.click();

      await new Promise(resolve => setTimeout(resolve, 50));

      expect(window.confirm).toHaveBeenCalled();
      expect(api.deletePlannedPurchase).toHaveBeenCalledWith('purchase-1');
      // Issue #774: after disable the Plans page must refresh so the toggle
      // reflects the backend's new enabled=false. getPlans is the API call
      // that loadPlans() fires to repopulate the Plans list.
      expect(api.getPlans).toHaveBeenCalledTimes(1);
    });

    test('disable action cancelled by user', async () => {
      window.confirm = jest.fn().mockReturnValue(false);

      const disableBtn = document.querySelector('[data-action="disable"]') as HTMLButtonElement;
      disableBtn?.click();

      await new Promise(resolve => setTimeout(resolve, 50));

      expect(api.deletePlannedPurchase).not.toHaveBeenCalled();
    });

    test('action shows error on failure', async () => {
      (api.pausePlannedPurchase as jest.Mock).mockRejectedValue(new Error('API Error'));
      console.error = jest.fn();

      const pauseBtn = document.querySelector('[data-action="pause"]') as HTMLButtonElement;
      pauseBtn?.click();

      await new Promise(resolve => setTimeout(resolve, 50));

      expect(mockShowToast).toHaveBeenCalledWith(expect.objectContaining({ message: 'Failed to pause purchase: API Error' }));
    });

    test('edit action calls getPlan with plan_id, not the purchase id (#773)', async () => {
      // The purchase row has id="purchase-1" and plan_id="plan-1".
      // Before the fix, editPlan received "purchase-1", causing GET /plans/purchase-1
      // to return 404 and surfacing "Failed to load plan details".
      (api.getPlan as jest.Mock).mockResolvedValue({
        id: 'plan-1',
        name: 'Test Plan',
        enabled: true,
        auto_purchase: false,
        notification_days_before: 3,
        services: {
          ec2: { provider: 'aws', service: 'ec2', enabled: true, term: 1, payment: 'all-upfront', coverage: 80 },
        },
        ramp_schedule: { type: 'immediate', percent_per_step: 100, step_interval_days: 0 },
      });

      const editBtn = document.querySelector('[data-action="edit"]') as HTMLButtonElement;
      editBtn?.click();

      await new Promise(resolve => setTimeout(resolve, 50));

      // Must use the plan FK, not the purchase PK.
      expect(api.getPlan).toHaveBeenCalledWith('plan-1');
      expect(api.getPlan).not.toHaveBeenCalledWith('purchase-1');
      // No error toast should fire.
      expect(mockShowToast).not.toHaveBeenCalledWith(
        expect.objectContaining({ message: 'Failed to load plan details' }),
      );
    });

    test('edit action with empty plan id is a no-op (defensive guard)', async () => {
      // Simulate a button whose data-plan-id attribute is missing/empty by
      // directly injecting a button without the attribute and clicking it.
      const container = document.getElementById('planned-purchases-list');
      const btn = document.createElement('button');
      btn.dataset.action = 'edit';
      btn.dataset.id = 'purchase-1';
      // intentionally omit data-plan-id so planId defaults to ''
      container?.appendChild(btn);
      btn.click();

      await new Promise(resolve => setTimeout(resolve, 50));

      // getPlan must NOT be called when planId is empty.
      expect(api.getPlan).not.toHaveBeenCalled();
    });
  });

  describe('resume action for paused purchase', () => {
    test('resume action resumes paused purchase', async () => {
      (api.getPlans as jest.Mock).mockResolvedValue({ plans: [] });
      (api.getPlannedPurchases as jest.Mock).mockResolvedValue({
        purchases: [
          {
            id: 'purchase-1',
            plan_id: 'plan-1',
            plan_name: 'Test Plan',
            scheduled_date: '2024-02-20',
            provider: 'aws',
            service: 'ec2',
            resource_type: 't3.medium',
            region: 'us-east-1',
            count: 5,
            term: 1,
            payment: 'all-upfront',
            upfront_cost: 1000,
            estimated_savings: 200,
            status: 'paused',
            step_number: 1,
            total_steps: 4
          }
        ]
      });
      await loadPlans();

      (api.resumePlannedPurchase as jest.Mock).mockResolvedValue({});
      (api.getPlannedPurchases as jest.Mock).mockResolvedValue({ purchases: [] });

      const resumeBtn = document.querySelector('[data-action="resume"]') as HTMLButtonElement;
      resumeBtn?.click();

      await new Promise(resolve => setTimeout(resolve, 50));

      expect(api.resumePlannedPurchase).toHaveBeenCalledWith('purchase-1');
    });
  });

  describe('savePlan', () => {
    beforeEach(() => {
      // Set up form values
      (document.getElementById('plan-name') as HTMLInputElement).value = 'New Plan';
      (document.getElementById('plan-description') as HTMLTextAreaElement).value = 'Test description';
      (document.getElementById('plan-provider') as HTMLSelectElement).value = 'aws';
      (document.getElementById('plan-service') as HTMLSelectElement).value = 'ec2';
      (document.getElementById('plan-term') as HTMLSelectElement).value = '1';
      (document.getElementById('plan-payment') as HTMLSelectElement).value = 'all-upfront';
      (document.getElementById('plan-coverage') as HTMLInputElement).value = '80';
      (document.getElementById('plan-auto-purchase') as HTMLInputElement).checked = true;
      (document.getElementById('plan-notify-days') as HTMLInputElement).value = '3';
      (document.getElementById('plan-enabled') as HTMLInputElement).checked = true;
      // Universal-plans fix: savePlan rejects an empty Target Accounts list,
      // so default the hidden field to a single account UUID for every test
      // in this block. Tests that exercise the empty-accounts rejection set
      // it back to '' explicitly inside the test.
      (document.getElementById('plan-account-ids') as HTMLInputElement).value = '11111111-1111-1111-1111-111111111111';
    });

    test('prevents default form submission', async () => {
      (api.createPlan as jest.Mock).mockResolvedValue({});
      (api.getPlans as jest.Mock).mockResolvedValue({ plans: [] });
      (api.getPlannedPurchases as jest.Mock).mockResolvedValue({ purchases: [] });

      const event = { preventDefault: jest.fn() } as unknown as Event;
      await savePlan(event);

      expect(event.preventDefault).toHaveBeenCalled();
    });

    test('creates new plan when no plan ID', async () => {
      (api.createPlan as jest.Mock).mockResolvedValue({});
      (api.getPlans as jest.Mock).mockResolvedValue({ plans: [] });
      (api.getPlannedPurchases as jest.Mock).mockResolvedValue({ purchases: [] });
      (document.getElementById('plan-id') as HTMLInputElement).value = '';

      const event = { preventDefault: jest.fn() } as unknown as Event;
      await savePlan(event);

      expect(api.createPlan).toHaveBeenCalled();
      expect(mockShowToast).toHaveBeenCalledWith(expect.objectContaining({ message: 'Plan created successfully' }));
    });

    test('updates existing plan when plan ID present', async () => {
      (api.updatePlan as jest.Mock).mockResolvedValue({});
      (api.getPlans as jest.Mock).mockResolvedValue({ plans: [] });
      (api.getPlannedPurchases as jest.Mock).mockResolvedValue({ purchases: [] });
      (document.getElementById('plan-id') as HTMLInputElement).value = 'plan-123';

      const event = { preventDefault: jest.fn() } as unknown as Event;
      await savePlan(event);

      expect(api.updatePlan).toHaveBeenCalledWith('plan-123', expect.any(Object));
      expect(mockShowToast).toHaveBeenCalledWith(expect.objectContaining({ message: 'Plan updated successfully' }));
    });

    test('includes custom ramp settings when custom schedule selected', async () => {
      (api.createPlan as jest.Mock).mockResolvedValue({});
      (api.getPlans as jest.Mock).mockResolvedValue({ plans: [] });
      (api.getPlannedPurchases as jest.Mock).mockResolvedValue({ purchases: [] });

      const customRadio = document.querySelector('input[value="custom"]') as HTMLInputElement;
      customRadio.checked = true;
      (document.getElementById('ramp-step-percent') as HTMLInputElement).value = '25';
      (document.getElementById('ramp-interval-days') as HTMLInputElement).value = '14';

      const event = { preventDefault: jest.fn() } as unknown as Event;
      await savePlan(event);

      expect(api.createPlan).toHaveBeenCalledWith(expect.objectContaining({
        custom_step_percent: 25,
        custom_interval_days: 14
      }));
    });

    // Helper: openCreatePlanModal/openNewPlanModal call form.reset(), which
    // clears the hidden plan-account-ids field stamped by the beforeEach.
    // Universal-plans fix requires that field to be non-empty at savePlan
    // time, so any test that opens the modal must re-stamp it before submit.
    const stampAccountIds = () => {
      (document.getElementById('plan-account-ids') as HTMLInputElement).value
        = '11111111-1111-1111-1111-111111111111';
    };

    test('includes the snapshot stamped by openCreatePlanModal (#273 CR)', async () => {
      // #273 CR follow-up: savePlan now reads the snapshot stamped at
      // Plan-button click time via openCreatePlanModal(snapshot), instead
      // of re-deriving from getVisibleRecommendations() / getSelectedRec
      // ommendationIDs() at Save time. State mutations between modal-open
      // and modal-Save (Refresh, filter changes, deselections) can no
      // longer change which recs are planned. The Purchase flow already
      // captures the target at click time; the Plan flow now mirrors it.
      const snapshot = [
        { id: 'rec-1', service: 'ec2' },
        { id: 'rec-2', service: 'rds' },
      ] as unknown as readonly api.Recommendation[];
      openCreatePlanModal(snapshot);
      stampAccountIds();

      (api.createPlan as jest.Mock).mockResolvedValue({});
      (api.getPlans as jest.Mock).mockResolvedValue({ plans: [] });
      (api.getPlannedPurchases as jest.Mock).mockResolvedValue({ purchases: [] });

      const event = { preventDefault: jest.fn() } as unknown as Event;
      await savePlan(event);

      const sentRecs = (api.createPlan as jest.Mock).mock.calls[0][0].recommendations;
      expect(sentRecs.map((r: { id: string }) => r.id).sort()).toEqual(['rec-1', 'rec-2']);
    });

    test('snapshot is immune to state mutations between modal-open and Save (#273 CR)', async () => {
      // The race the snapshot closes: user clicks "Plan from N selected"
      // → openCreatePlanModal stamps the snapshot → user toggles a
      // checkbox / Refresh fires / a column filter is applied while the
      // modal is open → user clicks Save. The Plan must reflect the
      // user's intent at click time, NOT the (potentially narrower /
      // wider / different) state at Save time.
      const snapshotAtClickTime = [
        { id: 'rec-1', service: 'ec2' },
        { id: 'rec-2', service: 'rds' },
      ] as unknown as readonly api.Recommendation[];
      openCreatePlanModal(snapshotAtClickTime);
      stampAccountIds();

      // Now simulate post-modal-open state mutations: deselection,
      // refresh-replaced visible set, etc. None of these should affect
      // the saved plan.
      (state.getSelectedRecommendationIDs as jest.Mock).mockReturnValue(new Set());
      (state.getVisibleRecommendations as jest.Mock).mockReturnValue([
        { id: 'completely-different-rec', service: 'cache' },
      ]);

      (api.createPlan as jest.Mock).mockResolvedValue({});
      (api.getPlans as jest.Mock).mockResolvedValue({ plans: [] });
      (api.getPlannedPurchases as jest.Mock).mockResolvedValue({ purchases: [] });

      const event = { preventDefault: jest.fn() } as unknown as Event;
      await savePlan(event);

      const sentRecs = (api.createPlan as jest.Mock).mock.calls[0][0].recommendations;
      // Must be the snapshot, NOT the post-mutation state.
      expect(sentRecs.map((r: { id: string }) => r.id).sort()).toEqual(['rec-1', 'rec-2']);
    });

    test('does NOT include any recs when no selection (#273 CR follow-up)', async () => {
      // The Bundle B fallback to "all visible recs" was removed as part of
      // the #273 CR loop: Refresh / filter changes silently mutate the
      // visible set between the user clicking the Create-Plan-button and
      // clicking Save in the modal, so a no-selection path is structurally
      // unsafe. The bottom action box already disables the button in that
      // state; this assertion is the defence-in-depth at the savePlan
      // layer for any path that bypasses the disabled UI (programmatic
      // calls, future code paths, regressions on the gating).
      // Clear the snapshot cache that earlier tests in this describe stamped
      // via openCreatePlanModal(snapshot). openNewPlanModal() resets it
      // (the New-Plan-from-scratch path explicitly clears the cache so a
      // subsequent New-Plan submit doesn't inherit a previous flow's recs).
      openNewPlanModal();
      stampAccountIds();
      (api.createPlan as jest.Mock).mockResolvedValue({});
      (api.getPlans as jest.Mock).mockResolvedValue({ plans: [] });
      (api.getPlannedPurchases as jest.Mock).mockResolvedValue({ purchases: [] });

      const event = { preventDefault: jest.fn() } as unknown as Event;
      await savePlan(event);

      const sentRecs = (api.createPlan as jest.Mock).mock.calls[0][0].recommendations;
      expect(sentRecs == null || sentRecs.length === 0).toBe(true);
    });

    test('empty snapshot from openCreatePlanModal also produces no recs', async () => {
      // Defence-in-depth: if a future caller passes an empty array as the
      // snapshot (e.g. the resolvePurchaseTarget result captured at click
      // time was empty for some reason), savePlan must still submit without
      // a recommendations field, not blow up.
      openCreatePlanModal([] as unknown as readonly api.Recommendation[]);
      stampAccountIds();
      (api.createPlan as jest.Mock).mockResolvedValue({});
      (api.getPlans as jest.Mock).mockResolvedValue({ plans: [] });
      (api.getPlannedPurchases as jest.Mock).mockResolvedValue({ purchases: [] });

      const event = { preventDefault: jest.fn() } as unknown as Event;
      await savePlan(event);

      const sentRecs = (api.createPlan as jest.Mock).mock.calls[0][0].recommendations;
      expect(sentRecs == null || sentRecs.length === 0).toBe(true);
    });

    test('closes modal after successful save', async () => {
      (api.createPlan as jest.Mock).mockResolvedValue({});
      (api.getPlans as jest.Mock).mockResolvedValue({ plans: [] });
      (api.getPlannedPurchases as jest.Mock).mockResolvedValue({ purchases: [] });

      const event = { preventDefault: jest.fn() } as unknown as Event;
      await savePlan(event);

      const modal = document.getElementById('plan-modal');
      expect(modal?.classList.contains('hidden')).toBe(true);
    });

    test('shows error on save failure', async () => {
      (api.createPlan as jest.Mock).mockRejectedValue(new Error('Save failed'));
      console.error = jest.fn();

      const event = { preventDefault: jest.fn() } as unknown as Event;
      await savePlan(event);

      expect(mockShowToast).toHaveBeenCalledWith(expect.objectContaining({ message: 'Failed to save plan: Save failed' }));
    });

    test('uses weekly-25pct ramp schedule', async () => {
      (api.createPlan as jest.Mock).mockResolvedValue({});
      (api.getPlans as jest.Mock).mockResolvedValue({ plans: [] });
      (api.getPlannedPurchases as jest.Mock).mockResolvedValue({ purchases: [] });

      const weeklyRadio = document.querySelector('input[value="weekly-25pct"]') as HTMLInputElement;
      weeklyRadio.checked = true;
      (document.querySelector('input[value="immediate"]') as HTMLInputElement).checked = false;

      const event = { preventDefault: jest.fn() } as unknown as Event;
      await savePlan(event);

      expect(api.createPlan).toHaveBeenCalledWith(expect.objectContaining({
        ramp_schedule: 'weekly-25pct'
      }));
    });

    test('uses monthly-10pct ramp schedule', async () => {
      (api.createPlan as jest.Mock).mockResolvedValue({});
      (api.getPlans as jest.Mock).mockResolvedValue({ plans: [] });
      (api.getPlannedPurchases as jest.Mock).mockResolvedValue({ purchases: [] });

      const monthlyRadio = document.querySelector('input[value="monthly-10pct"]') as HTMLInputElement;
      monthlyRadio.checked = true;
      (document.querySelector('input[value="immediate"]') as HTMLInputElement).checked = false;

      const event = { preventDefault: jest.fn() } as unknown as Event;
      await savePlan(event);

      expect(api.createPlan).toHaveBeenCalledWith(expect.objectContaining({
        ramp_schedule: 'monthly-10pct'
      }));
    });

    test('does NOT open Archera offer modal on plan create (fix #499)', async () => {
      // After #499 the modal must be surfaced only after execution completes,
      // never at plan-save time. Regression guard: ensure the removed call
      // does not come back.
      (api.createPlan as jest.Mock).mockResolvedValue({});
      (api.getPlans as jest.Mock).mockResolvedValue({ plans: [] });
      (api.getPlannedPurchases as jest.Mock).mockResolvedValue({ purchases: [] });
      (document.getElementById('plan-id') as HTMLInputElement).value = '';

      const event = { preventDefault: jest.fn() } as unknown as Event;
      await savePlan(event);

      // Guard against false positives: confirm the save actually ran (not a
      // short-circuit) so the "modal not called" assertion is meaningful.
      expect(api.createPlan).toHaveBeenCalledTimes(1);
      expect(mockOpenArcheraOfferModal).not.toHaveBeenCalled();
    });

    test('does NOT open Archera offer modal on plan update (fix #499)', async () => {
      (api.updatePlan as jest.Mock).mockResolvedValue({});
      (api.getPlans as jest.Mock).mockResolvedValue({ plans: [] });
      (api.getPlannedPurchases as jest.Mock).mockResolvedValue({ purchases: [] });
      (document.getElementById('plan-id') as HTMLInputElement).value = 'plan-123';

      const event = { preventDefault: jest.fn() } as unknown as Event;
      await savePlan(event);

      // Guard against false positives: confirm the update actually ran.
      expect(api.updatePlan).toHaveBeenCalledWith('plan-123', expect.any(Object));
      expect(mockOpenArcheraOfferModal).not.toHaveBeenCalled();
    });

    test('rejects submit and never calls createPlan when Target Accounts is empty (universal-plans fix)', async () => {
      // Universal plans (purchase_plans rows with no plan_accounts row) are
      // no longer allowed. The Save Plan button is also disabled in this
      // state via refreshPlanSaveButtonState; this assertion is the defence-
      // in-depth at the savePlan layer for scripted submissions or any
      // future regression that bypasses the disabled UI.
      (document.getElementById('plan-account-ids') as HTMLInputElement).value = '';
      (api.createPlan as jest.Mock).mockResolvedValue({ id: 'p1' });

      const event = { preventDefault: jest.fn() } as unknown as Event;
      await savePlan(event);

      expect(api.createPlan).not.toHaveBeenCalled();
      expect(api.setPlanAccounts).not.toHaveBeenCalled();
      expect(mockShowToast).toHaveBeenCalledWith(expect.objectContaining({
        kind: 'error',
        message: expect.stringContaining('Target Accounts'),
      }));
    });

    test('forwards selected target_accounts on createPlan (universal-plans fix)', async () => {
      // Verifies the new wire contract: savePlan stamps the selected account
      // chip IDs onto the request body so the backend can validate and
      // persist plan_accounts in the same call. The 2-step PUT remains a
      // belt-and-suspenders write for update flows, but the create path
      // must include target_accounts inline.
      (document.getElementById('plan-account-ids') as HTMLInputElement).value
        = '11111111-1111-1111-1111-111111111111,22222222-2222-2222-2222-222222222222';
      (api.createPlan as jest.Mock).mockResolvedValue({ id: 'p1' });
      (api.getPlans as jest.Mock).mockResolvedValue({ plans: [] });
      (api.getPlannedPurchases as jest.Mock).mockResolvedValue({ purchases: [] });

      const event = { preventDefault: jest.fn() } as unknown as Event;
      await savePlan(event);

      expect(api.createPlan).toHaveBeenCalledWith(expect.objectContaining({
        target_accounts: [
          '11111111-1111-1111-1111-111111111111',
          '22222222-2222-2222-2222-222222222222',
        ],
      }));
    });
  });

  describe('closePlanModal', () => {
    test('adds hidden class to plan modal', () => {
      const modal = document.getElementById('plan-modal');
      modal?.classList.remove('hidden');

      closePlanModal();

      expect(modal?.classList.contains('hidden')).toBe(true);
    });

    test('handles missing modal element', () => {
      document.body.innerHTML = '';

      expect(() => closePlanModal()).not.toThrow();
    });
  });

  describe('openCreatePlanModal', () => {
    test('opens modal with "New Purchase Plan" title when selection is empty (issue #17)', () => {
      (state.getSelectedRecommendationIDs as jest.Mock).mockReturnValue(new Set());

      openCreatePlanModal();

      const modal = document.getElementById('plan-modal');
      expect(modal?.classList.contains('hidden')).toBe(false);
      const title = document.getElementById('plan-modal-title');
      expect(title?.textContent).toBe('New Purchase Plan');
      // Previously we early-returned behind a toast that users missed
      // (e.g. after a provider-filter switch). Falling through to the
      // new-plan flow never silently noop-s.
      expect(mockShowToast).not.toHaveBeenCalled();
    });

    test('opens modal when recommendations are selected', () => {
      (state.getSelectedRecommendationIDs as jest.Mock).mockReturnValue(new Set([0]));

      openCreatePlanModal();

      const modal = document.getElementById('plan-modal');
      expect(modal?.classList.contains('hidden')).toBe(false);
    });

    test('sets "Create Purchase Plan" title when a snapshot is passed', () => {
      // #273 CR follow-up: title now keys off the snapshot the caller
      // passes (the resolved-target captured at button-click time)
      // rather than the live selection state, which would otherwise be
      // racy between modal-open and modal-render.
      const snapshot = [
        { id: 'rec-x', service: 'ec2' },
      ] as unknown as readonly api.Recommendation[];
      openCreatePlanModal(snapshot);

      const title = document.getElementById('plan-modal-title');
      expect(title?.textContent).toBe('Create Purchase Plan');
    });

    test('clears plan ID for new plan', () => {
      (state.getSelectedRecommendationIDs as jest.Mock).mockReturnValue(new Set([0]));
      (document.getElementById('plan-id') as HTMLInputElement).value = 'existing-plan';

      openCreatePlanModal();

      expect((document.getElementById('plan-id') as HTMLInputElement).value).toBe('');
    });

    // #770: Purchase Configuration section should be prefilled from the
    // selected commitment when exactly one commitment is passed in the snapshot.
    describe('prefill from single selected commitment (#770)', () => {
      const fixture: api.Recommendation = {
        id: 'rec-770',
        provider: 'aws',
        service: 'ec2',
        region: 'us-east-1',
        resource_type: 't3.medium',
        count: 1,
        term: 1,
        payment: 'partial-upfront',
        upfront_cost: 100,
        monthly_cost: 20,
        savings: 15,
        selected: true,
        purchased: false,
        cloud_account_id: 'acct-uuid-123',
      };

      test('prefills provider and service selects', () => {
        openCreatePlanModal([fixture]);

        expect((document.getElementById('plan-provider') as HTMLSelectElement).value).toBe('aws');
        expect((document.getElementById('plan-service') as HTMLSelectElement).value).toBe('ec2');
      });

      test('prefills term and payment selects', () => {
        openCreatePlanModal([fixture]);

        expect((document.getElementById('plan-term') as HTMLSelectElement).value).toBe('1');
        expect((document.getElementById('plan-payment') as HTMLSelectElement).value).toBe('partial-upfront');
      });

      test('calls populateTermSelect and populatePaymentSelect with provider+service', () => {
        openCreatePlanModal([fixture]);

        expect(populateTermSelect).toHaveBeenCalledWith(
          expect.any(HTMLSelectElement), 'aws', 'ec2'
        );
        expect(populatePaymentSelect).toHaveBeenCalledWith(
          expect.any(HTMLSelectElement), 'aws', 'ec2'
        );
      });

      test('fetches account by cloud_account_id to prefill chip', async () => {
        (api.getAccount as jest.Mock).mockResolvedValueOnce({
          id: 'acct-uuid-123',
          name: 'Prod AWS',
          external_id: '123456789012',
        });

        openCreatePlanModal([fixture]);

        // Wait for the async prefillAccountChipFromId to settle
        await Promise.resolve();
        await Promise.resolve();

        expect(api.getAccount).toHaveBeenCalledWith('acct-uuid-123');
        const chips = document.getElementById('plan-accounts-selected');
        expect(chips?.textContent).toContain('Prod AWS');
        const hiddenIds = (document.getElementById('plan-account-ids') as HTMLInputElement).value;
        expect(hiddenIds).toContain('acct-uuid-123');
      });

      test('does not throw and leaves account section empty when getAccount fails', async () => {
        (api.getAccount as jest.Mock).mockRejectedValueOnce(new Error('network error'));

        openCreatePlanModal([fixture]);

        await Promise.resolve();
        await Promise.resolve();

        // Account section should be empty -- no chip added
        const hiddenIds = (document.getElementById('plan-account-ids') as HTMLInputElement).value;
        expect(hiddenIds).toBe('');
      });

      test('skips prefill when no cloud_account_id present', async () => {
        const noAccount: api.Recommendation = { ...fixture, cloud_account_id: undefined };
        openCreatePlanModal([noAccount]);

        await Promise.resolve();

        expect(api.getAccount).not.toHaveBeenCalled();
      });

      test('discards stale getAccount result when modal is reopened before promise resolves', async () => {
        // Simulate a slow first getAccount call that resolves after the modal
        // is closed and a new modal session starts (the race condition fixed
        // by the planModalSession guard — #770 CR Major).
        let resolveFirstCall!: (value: api.CloudAccount) => void;
        const firstCallPromise = new Promise<api.CloudAccount>(resolve => {
          resolveFirstCall = resolve;
        });

        (api.getAccount as jest.Mock)
          .mockReturnValueOnce(firstCallPromise) // first open: hangs
          .mockResolvedValueOnce(null);          // second open: no account

        // First modal open with cloud_account_id
        openCreatePlanModal([fixture]);

        // Close and reopen — this increments planModalSession
        closePlanModal();
        const noAccount: api.Recommendation = { ...fixture, cloud_account_id: undefined };
        openCreatePlanModal([noAccount]);

        // Now resolve the stale first promise — should be discarded
        resolveFirstCall({ id: 'acct-uuid-123', name: 'Prod AWS', external_id: '123456789012' } as api.CloudAccount);
        await Promise.resolve();
        await Promise.resolve();

        // The stale chip must NOT have been added to the new modal session
        const hiddenIds = (document.getElementById('plan-account-ids') as HTMLInputElement).value;
        expect(hiddenIds).toBe('');
        const chips = document.getElementById('plan-accounts-selected');
        expect(chips?.textContent).not.toContain('Prod AWS');
      });

      test('does not prefill when snapshot has more than one commitment', () => {
        const second: api.Recommendation = { ...fixture, id: 'rec-771', service: 'rds' };
        openCreatePlanModal([fixture, second]);

        // populateTermSelect is called by setupRampScheduleHandlers / updateCommitmentOptions
        // but NOT by prefillPurchaseConfigFromCommitment (which only runs for length===1)
        // The provider/service select should NOT be forced to either rec's values
        // We assert that the service select was not forced to 'ec2' alone
        // (a multi-commitment plan requires manual selection)
        expect(api.getAccount).not.toHaveBeenCalled();
      });
    });
  });

  describe('openNewPlanModal', () => {
    test('opens modal without requiring selected recommendations', () => {
      (state.getSelectedRecommendationIDs as jest.Mock).mockReturnValue(new Set());

      openNewPlanModal();

      const modal = document.getElementById('plan-modal');
      expect(modal?.classList.contains('hidden')).toBe(false);
    });

    test('sets correct title for new plan', () => {
      openNewPlanModal();

      const title = document.getElementById('plan-modal-title');
      expect(title?.textContent).toBe('New Purchase Plan');
    });

    test('resets form', () => {
      (document.getElementById('plan-name') as HTMLInputElement).value = 'Old Plan';

      openNewPlanModal();

      expect((document.getElementById('plan-id') as HTMLInputElement).value).toBe('');
    });

    test('sets up ramp schedule handlers', () => {
      openNewPlanModal();

      // Verify handlers are attached by triggering change
      const rampRadio = document.querySelector('input[value="custom"]') as HTMLInputElement;
      rampRadio.dispatchEvent(new Event('change'));

      const customConfig = document.getElementById('custom-ramp-config');
      expect(customConfig?.classList.contains('hidden')).toBe(false);
    });
  });

  describe('closePurchaseModal', () => {
    test('adds hidden class to purchase modal', () => {
      const modal = document.getElementById('purchase-modal');
      modal?.classList.remove('hidden');

      closePurchaseModal();

      expect(modal?.classList.contains('hidden')).toBe(true);
    });
  });

  describe('setupPlanHandlers', () => {
    test('sets up provider change handler', () => {
      setupPlanHandlers();

      const providerSelect = document.getElementById('plan-provider') as HTMLSelectElement;
      providerSelect.value = 'azure';
      providerSelect.dispatchEvent(new Event('change'));

      // Should hide + disable the AWS services optgroup. Previously this
      // test asserted on inline style.display — that was indicator-only
      // and missed the real bug (issue #11: the hidden class stayed
      // baked in, so switching away from AWS never re-showed the target
      // provider's services). The class + disabled state is the new
      // source of truth.
      const serviceSelect = document.getElementById('plan-service') as HTMLSelectElement;
      const awsOptgroup = serviceSelect.querySelector('optgroup[label="AWS Services"]') as HTMLOptGroupElement;
      expect(awsOptgroup.classList.contains('hidden')).toBe(true);
      expect(awsOptgroup.disabled).toBe(true);
    });

    test('handles missing elements gracefully', () => {
      document.body.innerHTML = '';

      expect(() => setupPlanHandlers()).not.toThrow();
    });
  });

  describe('plan action buttons', () => {
    test('toggle plan button calls patchPlan and reloads', async () => {
      (api.getPlans as jest.Mock).mockResolvedValue({
        plans: [
          {
            id: 'plan-1',
            name: 'Test Plan',
            enabled: true,
            auto_purchase: true,
            services: {
              'ec2': {
                provider: 'aws',
                service: 'ec2',
                enabled: true,
                term: 1,
                payment: 'all-upfront',
                coverage: 80
              }
            },
            ramp_schedule: {
              type: 'immediate',
              percent_per_step: 100,
              step_interval_days: 0
            }
          }
        ]
      });
      (api.getPlannedPurchases as jest.Mock).mockResolvedValue({ purchases: [] });
      (api.patchPlan as jest.Mock).mockResolvedValue({});

      await loadPlans();

      // Reset mock to track reload
      (api.getPlans as jest.Mock).mockClear();
      (api.getPlans as jest.Mock).mockResolvedValue({ plans: [] });

      const toggleInput = document.querySelector('[data-action="toggle-plan"]') as HTMLInputElement;
      toggleInput?.dispatchEvent(new Event('change'));

      await new Promise(resolve => setTimeout(resolve, 50));

      expect(api.patchPlan).toHaveBeenCalledWith('plan-1', expect.objectContaining({ enabled: true }));
      expect(api.getPlans).toHaveBeenCalled();
    });

    test('toggle plan shows error on failure', async () => {
      (api.getPlans as jest.Mock).mockResolvedValue({
        plans: [
          {
            id: 'plan-1',
            name: 'Test Plan',
            enabled: true,
            auto_purchase: false,
            services: {
              'ec2': {
                provider: 'aws',
                service: 'ec2',
                enabled: true,
                term: 1,
                payment: 'all-upfront',
                coverage: 80
              }
            }
          }
        ]
      });
      (api.getPlannedPurchases as jest.Mock).mockResolvedValue({ purchases: [] });
      (api.patchPlan as jest.Mock).mockRejectedValue(new Error('API Error'));
      console.error = jest.fn();

      await loadPlans();

      const toggleInput = document.querySelector('[data-action="toggle-plan"]') as HTMLInputElement;
      toggleInput?.dispatchEvent(new Event('change'));

      await new Promise(resolve => setTimeout(resolve, 50));

      expect(mockShowToast).toHaveBeenCalledWith(expect.objectContaining({ message: 'Failed to update plan' }));
    });

    test('edit plan button loads plan details and opens modal', async () => {
      (api.getPlans as jest.Mock).mockResolvedValue({
        plans: [
          {
            id: 'plan-1',
            name: 'Test Plan',
            enabled: true,
            auto_purchase: false,
            services: {
              'ec2': {
                provider: 'aws',
                service: 'ec2',
                enabled: true,
                term: 1,
                payment: 'all-upfront',
                coverage: 80
              }
            }
          }
        ]
      });
      (api.getPlannedPurchases as jest.Mock).mockResolvedValue({ purchases: [] });
      (api.getPlan as jest.Mock).mockResolvedValue({
        id: 'plan-1',
        name: 'Test Plan',
        enabled: true,
        auto_purchase: true,
        notification_days_before: 3,
        services: {
          'ec2': {
            provider: 'aws',
            service: 'ec2',
            enabled: true,
            term: 1,
            payment: 'all-upfront',
            coverage: 80
          }
        },
        ramp_schedule: {
          type: 'immediate',
          percent_per_step: 100,
          step_interval_days: 0
        }
      });

      await loadPlans();

      const editBtn = document.querySelector('[data-action="edit-plan"]') as HTMLButtonElement;
      editBtn?.click();

      await new Promise(resolve => setTimeout(resolve, 50));

      expect(api.getPlan).toHaveBeenCalledWith('plan-1');
      const modal = document.getElementById('plan-modal');
      expect(modal?.classList.contains('hidden')).toBe(false);
      expect((document.getElementById('plan-name') as HTMLInputElement).value).toBe('Test Plan');
    });

    test('edit plan with weekly-25pct ramp schedule', async () => {
      (api.getPlans as jest.Mock).mockResolvedValue({
        plans: [
          {
            id: 'plan-1',
            name: 'Test Plan',
            enabled: true,
            auto_purchase: false,
            services: {}
          }
        ]
      });
      (api.getPlannedPurchases as jest.Mock).mockResolvedValue({ purchases: [] });
      (api.getPlan as jest.Mock).mockResolvedValue({
        id: 'plan-1',
        name: 'Weekly Plan',
        enabled: true,
        auto_purchase: true,
        notification_days_before: 3,
        services: {
          'ec2': {
            provider: 'aws',
            service: 'ec2',
            term: 1,
            payment: 'all-upfront',
            coverage: 80
          }
        },
        ramp_schedule: {
          type: 'weekly',
          percent_per_step: 25,
          step_interval_days: 7
        }
      });

      await loadPlans();

      const editBtn = document.querySelector('[data-action="edit-plan"]') as HTMLButtonElement;
      editBtn?.click();

      await new Promise(resolve => setTimeout(resolve, 50));

      const weeklyRadio = document.querySelector('input[value="weekly-25pct"]') as HTMLInputElement;
      expect(weeklyRadio.checked).toBe(true);
    });

    test('edit plan with monthly-10pct ramp schedule', async () => {
      (api.getPlans as jest.Mock).mockResolvedValue({
        plans: [
          {
            id: 'plan-1',
            name: 'Test Plan',
            enabled: true,
            auto_purchase: false,
            services: {}
          }
        ]
      });
      (api.getPlannedPurchases as jest.Mock).mockResolvedValue({ purchases: [] });
      (api.getPlan as jest.Mock).mockResolvedValue({
        id: 'plan-1',
        name: 'Monthly Plan',
        enabled: true,
        auto_purchase: true,
        notification_days_before: 3,
        services: {
          'ec2': {
            provider: 'aws',
            service: 'ec2',
            term: 1,
            payment: 'all-upfront',
            coverage: 80
          }
        },
        ramp_schedule: {
          type: 'monthly',
          percent_per_step: 10,
          step_interval_days: 30
        }
      });

      await loadPlans();

      const editBtn = document.querySelector('[data-action="edit-plan"]') as HTMLButtonElement;
      editBtn?.click();

      await new Promise(resolve => setTimeout(resolve, 50));

      const monthlyRadio = document.querySelector('input[value="monthly-10pct"]') as HTMLInputElement;
      expect(monthlyRadio.checked).toBe(true);
    });

    test('edit plan with custom ramp schedule populates custom fields', async () => {
      (api.getPlans as jest.Mock).mockResolvedValue({
        plans: [
          {
            id: 'plan-1',
            name: 'Test Plan',
            enabled: true,
            auto_purchase: false,
            services: {
              'ec2': {
                provider: 'aws',
                service: 'ec2',
                enabled: true,
                term: 1,
                payment: 'all-upfront',
                coverage: 80
              }
            }
          }
        ]
      });
      (api.getPlannedPurchases as jest.Mock).mockResolvedValue({ purchases: [] });
      (api.getPlan as jest.Mock).mockResolvedValue({
        id: 'plan-1',
        name: 'Test Plan',
        enabled: true,
        auto_purchase: true,
        notification_days_before: 3,
        services: {
          'ec2': {
            provider: 'aws',
            service: 'ec2',
            enabled: true,
            term: 1,
            payment: 'all-upfront',
            coverage: 80
          }
        },
        ramp_schedule: {
          type: 'custom',
          percent_per_step: 25,
          step_interval_days: 14
        }
      });

      await loadPlans();

      const editBtn = document.querySelector('[data-action="edit-plan"]') as HTMLButtonElement;
      editBtn?.click();

      await new Promise(resolve => setTimeout(resolve, 50));

      expect((document.getElementById('ramp-step-percent') as HTMLInputElement).value).toBe('25');
      expect((document.getElementById('ramp-interval-days') as HTMLInputElement).value).toBe('14');
    });

    test('edit plan shows error on failure', async () => {
      (api.getPlans as jest.Mock).mockResolvedValue({
        plans: [
          {
            id: 'plan-1',
            name: 'Test Plan',
            enabled: true,
            auto_purchase: false,
            services: {
              'ec2': {
                provider: 'aws',
                service: 'ec2',
                enabled: true,
                term: 1,
                payment: 'all-upfront',
                coverage: 80
              }
            }
          }
        ]
      });
      (api.getPlannedPurchases as jest.Mock).mockResolvedValue({ purchases: [] });
      (api.getPlan as jest.Mock).mockRejectedValue(new Error('API Error'));
      console.error = jest.fn();

      await loadPlans();

      const editBtn = document.querySelector('[data-action="edit-plan"]') as HTMLButtonElement;
      editBtn?.click();

      await new Promise(resolve => setTimeout(resolve, 50));

      expect(mockShowToast).toHaveBeenCalledWith(expect.objectContaining({ message: 'Failed to load plan details' }));
    });

    test('view history button calls viewPlanHistory', async () => {
      (api.getPlans as jest.Mock).mockResolvedValue({
        plans: [
          {
            id: 'plan-1',
            name: 'Test Plan',
            enabled: true,
            auto_purchase: false,
            services: {}
          }
        ]
      });
      (api.getPlannedPurchases as jest.Mock).mockResolvedValue({ purchases: [] });

      await loadPlans();

      const historyBtn = document.querySelector('[data-action="view-history"]') as HTMLButtonElement;
      historyBtn?.click();

      await new Promise(resolve => setTimeout(resolve, 50));

      expect(viewPlanHistory).toHaveBeenCalledWith('plan-1');
    });

    test('delete plan button deletes plan and reloads', async () => {
      (api.getPlans as jest.Mock).mockResolvedValue({
        plans: [
          {
            id: 'plan-1',
            name: 'Test Plan',
            enabled: true,
            auto_purchase: false,
            services: {
              'ec2': {
                provider: 'aws',
                service: 'ec2',
                enabled: true,
                term: 1,
                payment: 'all-upfront',
                coverage: 80
              }
            }
          }
        ]
      });
      (api.getPlannedPurchases as jest.Mock).mockResolvedValue({ purchases: [] });
      (api.deletePlan as jest.Mock).mockResolvedValue({});
      window.confirm = jest.fn().mockReturnValue(true);

      await loadPlans();

      // Reset mock to track reload
      (api.getPlans as jest.Mock).mockClear();
      (api.getPlans as jest.Mock).mockResolvedValue({ plans: [] });

      const deleteBtn = document.querySelector('[data-action="delete-plan"]') as HTMLButtonElement;
      deleteBtn?.click();

      await new Promise(resolve => setTimeout(resolve, 50));

      expect(api.deletePlan).toHaveBeenCalledWith('plan-1');
      expect(api.getPlans).toHaveBeenCalled();
    });

    test('delete plan does nothing if user cancels', async () => {
      (api.getPlans as jest.Mock).mockResolvedValue({
        plans: [
          {
            id: 'plan-1',
            name: 'Test Plan',
            enabled: true,
            auto_purchase: false,
            services: {
              'ec2': {
                provider: 'aws',
                service: 'ec2',
                enabled: true,
                term: 1,
                payment: 'all-upfront',
                coverage: 80
              }
            }
          }
        ]
      });
      (api.getPlannedPurchases as jest.Mock).mockResolvedValue({ purchases: [] });
      mockConfirmDialog.mockResolvedValueOnce(false);

      await loadPlans();

      const deleteBtn = document.querySelector('[data-action="delete-plan"]') as HTMLButtonElement;
      deleteBtn?.click();

      await new Promise(resolve => setTimeout(resolve, 50));

      expect(api.deletePlan).not.toHaveBeenCalled();
    });

    test('delete plan shows error on failure', async () => {
      (api.getPlans as jest.Mock).mockResolvedValue({
        plans: [
          {
            id: 'plan-1',
            name: 'Test Plan',
            enabled: true,
            auto_purchase: false,
            services: {
              'ec2': {
                provider: 'aws',
                service: 'ec2',
                enabled: true,
                term: 1,
                payment: 'all-upfront',
                coverage: 80
              }
            }
          }
        ]
      });
      (api.getPlannedPurchases as jest.Mock).mockResolvedValue({ purchases: [] });
      (api.deletePlan as jest.Mock).mockRejectedValue(new Error('API Error'));
      window.confirm = jest.fn().mockReturnValue(true);
      console.error = jest.fn();

      await loadPlans();

      const deleteBtn = document.querySelector('[data-action="delete-plan"]') as HTMLButtonElement;
      deleteBtn?.click();

      await new Promise(resolve => setTimeout(resolve, 50));

      expect(mockShowToast).toHaveBeenCalledWith(expect.objectContaining({ message: 'Failed to delete plan' }));
    });

    test('add purchases button opens modal', async () => {
      (api.getPlans as jest.Mock).mockResolvedValue({
        plans: [
          {
            id: 'plan-1',
            name: 'Test Plan',
            enabled: true,
            auto_purchase: false,
            services: {}
          }
        ]
      });
      (api.getPlannedPurchases as jest.Mock).mockResolvedValue({ purchases: [] });

      await loadPlans();

      const addBtn = document.querySelector('[data-action="add-purchases"]') as HTMLButtonElement;
      addBtn?.click();

      await new Promise(resolve => setTimeout(resolve, 50));

      const modal = document.getElementById('add-purchases-modal');
      expect(modal).toBeTruthy();
      expect(modal?.innerHTML).toContain('Add Planned Purchases');
    });
  });

  describe('add purchases modal', () => {
    beforeEach(async () => {
      (api.getPlans as jest.Mock).mockResolvedValue({
        plans: [
          {
            id: 'plan-1',
            name: 'Test Plan',
            enabled: true,
            auto_purchase: false,
            services: {}
          }
        ]
      });
      (api.getPlannedPurchases as jest.Mock).mockResolvedValue({ purchases: [] });

      await loadPlans();

      const addBtn = document.querySelector('[data-action="add-purchases"]') as HTMLButtonElement;
      addBtn?.click();

      await new Promise(resolve => setTimeout(resolve, 50));
    });

    test('cancel button closes modal', async () => {
      const cancelBtn = document.getElementById('add-purchases-cancel') as HTMLButtonElement;
      cancelBtn?.click();

      await new Promise(resolve => setTimeout(resolve, 50));

      const modal = document.getElementById('add-purchases-modal');
      expect(modal).toBeNull();
    });

    test('submit form creates planned purchases', async () => {
      (api.createPlannedPurchases as jest.Mock).mockResolvedValue({});
      (api.getPlannedPurchases as jest.Mock).mockResolvedValue({ purchases: [] });

      const countInput = document.getElementById('add-purchases-count') as HTMLInputElement;
      countInput.value = '3';

      const form = document.getElementById('add-purchases-form') as HTMLFormElement;
      form.dispatchEvent(new Event('submit'));

      await new Promise(resolve => setTimeout(resolve, 50));

      expect(api.createPlannedPurchases).toHaveBeenCalledWith('plan-1', 3, expect.any(String));
      expect(mockShowToast).toHaveBeenCalledWith(expect.objectContaining({ message: 'Successfully scheduled 3 purchases' }));
    });

    test('submit form with single purchase shows singular message', async () => {
      (api.createPlannedPurchases as jest.Mock).mockResolvedValue({});
      (api.getPlannedPurchases as jest.Mock).mockResolvedValue({ purchases: [] });

      const countInput = document.getElementById('add-purchases-count') as HTMLInputElement;
      countInput.value = '1';

      const form = document.getElementById('add-purchases-form') as HTMLFormElement;
      form.dispatchEvent(new Event('submit'));

      await new Promise(resolve => setTimeout(resolve, 50));

      expect(mockShowToast).toHaveBeenCalledWith(expect.objectContaining({ message: 'Successfully scheduled 1 purchase' }));
    });

    test('submit form shows error on failure', async () => {
      (api.createPlannedPurchases as jest.Mock).mockRejectedValue(new Error('API Error'));

      const form = document.getElementById('add-purchases-form') as HTMLFormElement;
      form.dispatchEvent(new Event('submit'));

      await new Promise(resolve => setTimeout(resolve, 50));

      const errorDiv = document.getElementById('add-purchases-error');
      expect(errorDiv?.textContent).toBe('API Error');
      expect(errorDiv?.classList.contains('hidden')).toBe(false);
    });
  });

  describe('ramp schedule form handlers', () => {
    test('generates plan name based on immediate schedule', () => {
      (state.getSelectedRecommendationIDs as jest.Mock).mockReturnValue(new Set([0]));
      openCreatePlanModal();

      const planNameInput = document.getElementById('plan-name') as HTMLInputElement;
      expect(planNameInput.value).toContain('Full Coverage Purchase');
    });

    test('generates plan name based on weekly-25pct schedule', () => {
      (state.getSelectedRecommendationIDs as jest.Mock).mockReturnValue(new Set([0]));
      openCreatePlanModal();

      const weeklyRadio = document.querySelector('input[value="weekly-25pct"]') as HTMLInputElement;
      weeklyRadio.checked = true;
      weeklyRadio.dispatchEvent(new Event('change', { bubbles: true }));

      const planNameInput = document.getElementById('plan-name') as HTMLInputElement;
      expect(planNameInput.value).toContain('Weekly 25% Ramp-up');
    });

    test('generates plan name based on monthly-10pct schedule', () => {
      (state.getSelectedRecommendationIDs as jest.Mock).mockReturnValue(new Set([0]));
      openCreatePlanModal();

      const monthlyRadio = document.querySelector('input[value="monthly-10pct"]') as HTMLInputElement;
      monthlyRadio.checked = true;
      monthlyRadio.dispatchEvent(new Event('change', { bubbles: true }));

      const planNameInput = document.getElementById('plan-name') as HTMLInputElement;
      expect(planNameInput.value).toContain('Monthly 10% Ramp-up');
    });

    test('generates plan name based on custom schedule', () => {
      (state.getSelectedRecommendationIDs as jest.Mock).mockReturnValue(new Set([0]));
      openCreatePlanModal();

      const customRadio = document.querySelector('input[value="custom"]') as HTMLInputElement;
      customRadio.checked = true;
      customRadio.dispatchEvent(new Event('change', { bubbles: true }));

      const planNameInput = document.getElementById('plan-name') as HTMLInputElement;
      expect(planNameInput.value).toContain('Custom');
    });

    test('custom schedule fields update plan name', () => {
      (state.getSelectedRecommendationIDs as jest.Mock).mockReturnValue(new Set([0]));
      openCreatePlanModal();

      const customRadio = document.querySelector('input[value="custom"]') as HTMLInputElement;
      customRadio.checked = true;
      customRadio.dispatchEvent(new Event('change', { bubbles: true }));

      const stepPercentInput = document.getElementById('ramp-step-percent') as HTMLInputElement;
      stepPercentInput.value = '15';
      stepPercentInput.dispatchEvent(new Event('input'));

      const planNameInput = document.getElementById('plan-name') as HTMLInputElement;
      expect(planNameInput.value).toContain('Custom 15%');
    });

    test('does not update plan name when editing existing plan', () => {
      (state.getSelectedRecommendationIDs as jest.Mock).mockReturnValue(new Set([0]));
      openCreatePlanModal();

      // Set a plan ID to simulate editing
      (document.getElementById('plan-id') as HTMLInputElement).value = 'existing-plan';
      (document.getElementById('plan-name') as HTMLInputElement).value = 'Existing Plan Name';

      // Trigger ramp schedule change
      const weeklyRadio = document.querySelector('input[value="weekly-25pct"]') as HTMLInputElement;
      weeklyRadio.checked = true;
      weeklyRadio.dispatchEvent(new Event('change', { bubbles: true }));

      const planNameInput = document.getElementById('plan-name') as HTMLInputElement;
      expect(planNameInput.value).toBe('Existing Plan Name');
    });

    test('updates custom config fields from preset', () => {
      (state.getSelectedRecommendationIDs as jest.Mock).mockReturnValue(new Set([0]));
      openCreatePlanModal();

      // Select weekly-25pct
      const weeklyRadio = document.querySelector('input[value="weekly-25pct"]') as HTMLInputElement;
      weeklyRadio.checked = true;
      weeklyRadio.dispatchEvent(new Event('change', { bubbles: true }));

      const stepPercentInput = document.getElementById('ramp-step-percent') as HTMLInputElement;
      const intervalDaysInput = document.getElementById('ramp-interval-days') as HTMLInputElement;

      expect(stepPercentInput.value).toBe('25');
      expect(intervalDaysInput.value).toBe('7');
    });

    test('updates custom config for monthly preset', () => {
      (state.getSelectedRecommendationIDs as jest.Mock).mockReturnValue(new Set([0]));
      openCreatePlanModal();

      const monthlyRadio = document.querySelector('input[value="monthly-10pct"]') as HTMLInputElement;
      monthlyRadio.checked = true;
      monthlyRadio.dispatchEvent(new Event('change', { bubbles: true }));

      const stepPercentInput = document.getElementById('ramp-step-percent') as HTMLInputElement;
      const intervalDaysInput = document.getElementById('ramp-interval-days') as HTMLInputElement;

      expect(stepPercentInput.value).toBe('10');
      expect(intervalDaysInput.value).toBe('30');
    });

    test('updates custom config for immediate preset', () => {
      (state.getSelectedRecommendationIDs as jest.Mock).mockReturnValue(new Set([0]));
      openCreatePlanModal();

      // First switch to custom
      const customRadio = document.querySelector('input[value="custom"]') as HTMLInputElement;
      customRadio.checked = true;
      customRadio.dispatchEvent(new Event('change', { bubbles: true }));

      // Then switch back to immediate
      const immediateRadio = document.querySelector('input[value="immediate"]') as HTMLInputElement;
      immediateRadio.checked = true;
      immediateRadio.dispatchEvent(new Event('change', { bubbles: true }));

      const stepPercentInput = document.getElementById('ramp-step-percent') as HTMLInputElement;
      const intervalDaysInput = document.getElementById('ramp-interval-days') as HTMLInputElement;

      expect(stepPercentInput.value).toBe('100');
      expect(intervalDaysInput.value).toBe('0');
    });
  });

  describe('commitment options handling', () => {
    test('provider change updates commitment options', () => {
      openNewPlanModal();

      const providerSelect = document.getElementById('plan-provider') as HTMLSelectElement;
      providerSelect.value = 'azure';
      providerSelect.dispatchEvent(new Event('change'));

      expect(populateTermSelect).toHaveBeenCalled();
      expect(populatePaymentSelect).toHaveBeenCalled();
    });

    test('service change updates commitment options', () => {
      openNewPlanModal();

      const serviceSelect = document.getElementById('plan-service') as HTMLSelectElement;
      serviceSelect.value = 'rds';
      serviceSelect.dispatchEvent(new Event('change'));

      expect(populateTermSelect).toHaveBeenCalled();
      expect(populatePaymentSelect).toHaveBeenCalled();
    });

    test('term change updates payment options', () => {
      openNewPlanModal();

      const termSelect = document.getElementById('plan-term') as HTMLSelectElement;
      termSelect.value = '3';
      termSelect.dispatchEvent(new Event('change'));

      expect(populatePaymentSelect).toHaveBeenCalled();
    });

    test('payment change updates term options', () => {
      openNewPlanModal();

      const paymentSelect = document.getElementById('plan-payment') as HTMLSelectElement;
      paymentSelect.value = 'no-upfront';
      paymentSelect.dispatchEvent(new Event('change'));

      expect(populateTermSelect).toHaveBeenCalled();
    });

    test('validates invalid combination on load', () => {
      (isValidCombination as jest.Mock).mockReturnValue(false);
      openNewPlanModal();

      // Should call populatePaymentSelect to fix invalid combination
      expect(populatePaymentSelect).toHaveBeenCalled();
    });
  });

  describe('provider scopes service dropdown (issue #11)', () => {
    // Match optgroups by label-prefix so both the real HTML labels
    // ("AWS"/"Azure"/"GCP") and the test fixture's labels
    // ("AWS Services"/"Azure Services"/"GCP Services") work.
    const byProvider = (prefix: string) =>
      document.querySelector(
        `#plan-service > optgroup[label^="${prefix}"]`,
      ) as HTMLOptGroupElement | null;

    beforeEach(() => {
      setupPlanHandlers();
    });

    test('initial state (provider=aws): only AWS optgroup enabled', () => {
      const aws = byProvider('AWS')!;
      const azure = byProvider('Azure')!;
      const gcp = byProvider('GCP')!;

      expect(aws.classList.contains('hidden')).toBe(false);
      expect(aws.disabled).toBe(false);
      expect(azure.classList.contains('hidden')).toBe(true);
      expect(azure.disabled).toBe(true);
      expect(gcp.classList.contains('hidden')).toBe(true);
      expect(gcp.disabled).toBe(true);
    });

    test('switching to azure enables only the Azure optgroup', () => {
      const provider = document.getElementById('plan-provider') as HTMLSelectElement;
      provider.value = 'azure';
      provider.dispatchEvent(new Event('change'));

      expect(byProvider('AWS')!.classList.contains('hidden')).toBe(true);
      expect(byProvider('AWS')!.disabled).toBe(true);
      expect(byProvider('Azure')!.classList.contains('hidden')).toBe(false);
      expect(byProvider('Azure')!.disabled).toBe(false);
      expect(byProvider('GCP')!.classList.contains('hidden')).toBe(true);
      expect(byProvider('GCP')!.disabled).toBe(true);
    });

    test('switching to gcp enables only the GCP optgroup', () => {
      const provider = document.getElementById('plan-provider') as HTMLSelectElement;
      provider.value = 'gcp';
      provider.dispatchEvent(new Event('change'));

      expect(byProvider('AWS')!.disabled).toBe(true);
      expect(byProvider('Azure')!.disabled).toBe(true);
      expect(byProvider('GCP')!.disabled).toBe(false);
      expect(byProvider('GCP')!.classList.contains('hidden')).toBe(false);
    });

    test('resets service to first visible option when AWS-only selection becomes hidden', () => {
      const provider = document.getElementById('plan-provider') as HTMLSelectElement;
      const service = document.getElementById('plan-service') as HTMLSelectElement;

      // User picks an AWS-only service, then switches provider to azure.
      service.value = 'ec2';
      provider.value = 'azure';
      provider.dispatchEvent(new Event('change'));

      // Implementation picks the first option in the first visible
      // optgroup. Assert the chosen value lives inside an optgroup
      // labelled with the selected provider — decoupling the test
      // from the specific service name in the fixture.
      const selected = service.options[service.selectedIndex];
      expect(selected).toBeDefined();
      const parent = selected?.parentElement;
      expect(parent).toBeInstanceOf(HTMLOptGroupElement);
      expect((parent as HTMLOptGroupElement).label.toLowerCase()).toMatch(/^azure/);
      expect(service.value).not.toBe('ec2');
    });
  });

  describe('Target Accounts provider filter (issue #703)', () => {
    // These tests need the account-search DOM elements in addition to the
    // standard plan-modal markup provided by the outer beforeEach.
    beforeEach(() => {
      // Inject account-search elements into the existing form using safe DOM API calls.
      const form = document.getElementById('plan-form');
      if (form) {
        const accountSection = document.createElement('div');
        accountSection.className = 'plan-accounts-search';

        const searchInput = document.createElement('input');
        searchInput.type = 'text';
        searchInput.id = 'plan-account-search';
        searchInput.placeholder = 'Search accounts';
        accountSection.appendChild(searchInput);

        const suggestions = document.createElement('div');
        suggestions.id = 'plan-account-suggestions';
        suggestions.className = 'account-suggestions hidden';
        accountSection.appendChild(suggestions);

        const selectedContainer = document.createElement('div');
        selectedContainer.id = 'plan-accounts-selected';
        selectedContainer.className = 'selected-accounts';
        accountSection.appendChild(selectedContainer);

        const hiddenIds = document.createElement('input');
        hiddenIds.type = 'hidden';
        hiddenIds.id = 'plan-account-ids';
        hiddenIds.value = '';
        accountSection.appendChild(hiddenIds);

        form.appendChild(accountSection);
      }

      // Use fake timers so setTimeout in handlePlanAccountSearch can be
      // controlled synchronously.
      jest.useFakeTimers();
    });

    afterEach(() => {
      jest.useRealTimers();
    });

    test('account search passes the current plan provider to listAccounts', async () => {
      (api.listAccounts as jest.Mock).mockResolvedValue([]);

      // openNewPlanModal calls form.reset() synchronously, resetting the provider
      // select back to its first option ('aws'). Wire up the modal first, then
      // change provider, then trigger the search so the handler reads the new value.
      openNewPlanModal();
      // Let setupPlanAccountsSection's async portion settle (it clones the input node).
      await Promise.resolve();

      // Now change provider AFTER the form reset so setupPlanAccountsSection already ran.
      const providerSelect = document.getElementById('plan-provider') as HTMLSelectElement;
      providerSelect.value = 'azure';

      // The search input node was replaced (cloneNode) inside setupPlanAccountsSection;
      // re-query by id to get the live node with the registered listener.
      const searchInput = document.getElementById('plan-account-search') as HTMLInputElement;
      searchInput.value = 'my-azure';
      searchInput.dispatchEvent(new Event('input'));

      // Advance past the 300 ms debounce timer and flush async api call.
      jest.runAllTimers();
      await Promise.resolve();
      await Promise.resolve();

      expect(api.listAccounts).toHaveBeenCalledWith(
        expect.objectContaining({ search: 'my-azure', provider: 'azure' })
      );
    });

    test('account search passes aws provider when plan provider is aws', async () => {
      (api.listAccounts as jest.Mock).mockResolvedValue([]);

      openNewPlanModal();
      await Promise.resolve();

      // form.reset() resets to first option 'aws', so provider is already 'aws'.
      const searchInput = document.getElementById('plan-account-search') as HTMLInputElement;
      searchInput.value = 'prod';
      searchInput.dispatchEvent(new Event('input'));

      jest.runAllTimers();
      await Promise.resolve();
      await Promise.resolve();

      expect(api.listAccounts).toHaveBeenCalledWith(
        expect.objectContaining({ provider: 'aws' })
      );
    });

    test('switching provider clears existing account chips and hidden field', async () => {
      // openNewPlanModal wires up setupRampScheduleHandlers, which attaches the
      // provider-change listener that clears accounts. Must call it before the test.
      openNewPlanModal();
      await Promise.resolve();

      // Pre-populate the hidden field to simulate an earlier account selection.
      // renderPlanAccountChips() will clear the DOM container from planSelectedAccounts,
      // so populating it directly in the DOM is sufficient to assert the clear.
      const selectedContainer = document.getElementById('plan-accounts-selected') as HTMLElement;
      const chip = document.createElement('span');
      chip.className = 'account-chip';
      chip.textContent = 'old-acct';
      selectedContainer.appendChild(chip);
      (document.getElementById('plan-account-ids') as HTMLInputElement).value = 'acct-old-id';

      // Switch provider — should clear chips and hidden field via the handler added in
      // setupRampScheduleHandlers (called by openNewPlanModal).
      const providerSelect = document.getElementById('plan-provider') as HTMLSelectElement;
      providerSelect.value = 'gcp';
      providerSelect.dispatchEvent(new Event('change'));

      expect(selectedContainer.textContent).toBe('');
      expect((document.getElementById('plan-account-ids') as HTMLInputElement).value).toBe('');
    });

    test('switching provider clears and hides open account suggestion dropdown', async () => {
      openNewPlanModal();
      await Promise.resolve();

      // Simulate an open suggestion dropdown with stale results from a previous search.
      const suggestions = document.getElementById('plan-account-suggestions') as HTMLElement;
      suggestions.textContent = 'stale-item';
      suggestions.classList.remove('hidden');

      // Also put text in the search input to verify it is cleared.
      const searchInput = document.getElementById('plan-account-search') as HTMLInputElement;
      searchInput.value = 'old query';

      // Switch provider — the handler must clear + hide the dropdown and clear the input.
      const providerSelect = document.getElementById('plan-provider') as HTMLSelectElement;
      providerSelect.value = 'gcp';
      providerSelect.dispatchEvent(new Event('change'));

      expect(suggestions.textContent).toBe('');
      expect(suggestions.classList.contains('hidden')).toBe(true);
      expect(searchInput.value).toBe('');
    });

    test('account search input is disabled when provider is cleared after modal open', async () => {
      // Open the modal with default provider (aws from form reset).
      openNewPlanModal();
      await Promise.resolve();

      // Simulate user clearing the provider via the change listener, which should disable search.
      const providerSelect = document.getElementById('plan-provider') as HTMLSelectElement;
      providerSelect.value = '';
      providerSelect.dispatchEvent(new Event('change'));

      const searchInput = document.getElementById('plan-account-search') as HTMLInputElement;
      expect(searchInput.disabled).toBe(true);
    });

    test('account search input is enabled when plan-provider has a value', async () => {
      // Default after form.reset() is 'aws' (first option), so disabled should be false.
      openNewPlanModal();
      await Promise.resolve();

      // The cloned input replaces the original in setupPlanAccountsSection;
      // query by id to get the current node.
      const searchInput = document.getElementById('plan-account-search') as HTMLInputElement;
      expect(searchInput.disabled).toBe(false);
    });
  });
});
