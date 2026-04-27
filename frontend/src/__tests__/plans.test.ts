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
  listAccounts: jest.fn().mockResolvedValue([])
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
      const filter = document.createElement('select');
      filter.id = 'plans-provider-filter';
      ['', 'aws', 'azure', 'gcp'].forEach((v) => {
        const o = document.createElement('option');
        o.value = v;
        filter.appendChild(o);
      });
      filter.value = 'aws';
      document.body.appendChild(filter);

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

      await loadPlans();

      const list = document.getElementById('plans-list');
      expect(list?.innerHTML).toContain('AWS fanout');
      expect(list?.innerHTML).not.toContain('GCP CUDs');
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

      const disableBtn = document.querySelector('[data-action="disable"]') as HTMLButtonElement;
      disableBtn?.click();

      await new Promise(resolve => setTimeout(resolve, 50));

      expect(window.confirm).toHaveBeenCalled();
      expect(api.deletePlannedPurchase).toHaveBeenCalledWith('purchase-1');
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

    test('includes selected ∩ visible recommendations (Bundle B)', async () => {
      // Bundle B: savePlan reads getVisibleRecommendations (post-filter set)
      // so plans never include filtered-out rows. Selection is intersected
      // with that visible list.
      (state.getSelectedRecommendationIDs as jest.Mock).mockReturnValue(new Set(['rec-1', 'rec-2']));
      (state.getVisibleRecommendations as jest.Mock).mockReturnValue([
        { id: 'rec-1', service: 'ec2' },
        { id: 'rec-2', service: 'rds' }
      ]);
      (api.createPlan as jest.Mock).mockResolvedValue({});
      (api.getPlans as jest.Mock).mockResolvedValue({ plans: [] });
      (api.getPlannedPurchases as jest.Mock).mockResolvedValue({ purchases: [] });

      const event = { preventDefault: jest.fn() } as unknown as Event;
      await savePlan(event);

      const sentRecs = (api.createPlan as jest.Mock).mock.calls[0][0].recommendations;
      expect(sentRecs.map((r: { id: string }) => r.id).sort()).toEqual(['rec-1', 'rec-2']);
    });

    test('falls back to all visible when no selection (Bundle B)', async () => {
      (state.getSelectedRecommendationIDs as jest.Mock).mockReturnValue(new Set());
      (state.getVisibleRecommendations as jest.Mock).mockReturnValue([
        { id: 'rec-1', service: 'ec2' },
        { id: 'rec-2', service: 'rds' }
      ]);
      (api.createPlan as jest.Mock).mockResolvedValue({});
      (api.getPlans as jest.Mock).mockResolvedValue({ plans: [] });
      (api.getPlannedPurchases as jest.Mock).mockResolvedValue({ purchases: [] });

      const event = { preventDefault: jest.fn() } as unknown as Event;
      await savePlan(event);

      const sentRecs = (api.createPlan as jest.Mock).mock.calls[0][0].recommendations;
      expect(sentRecs.map((r: { id: string }) => r.id).sort()).toEqual(['rec-1', 'rec-2']);
    });

    test('plan does not include filtered-out recs (Bundle B)', async () => {
      // Selection points at IDs that aren't in the visible set — they're
      // filtered out, so the plan should silently ignore them.
      (state.getSelectedRecommendationIDs as jest.Mock).mockReturnValue(new Set(['stale-1']));
      (state.getVisibleRecommendations as jest.Mock).mockReturnValue([
        { id: 'rec-1', service: 'ec2' }
      ]);
      (api.createPlan as jest.Mock).mockResolvedValue({});
      (api.getPlans as jest.Mock).mockResolvedValue({ plans: [] });
      (api.getPlannedPurchases as jest.Mock).mockResolvedValue({ purchases: [] });

      const event = { preventDefault: jest.fn() } as unknown as Event;
      await savePlan(event);

      const sentRecs = (api.createPlan as jest.Mock).mock.calls[0][0].recommendations;
      // selection had no intersection with visible → recommendations field empty/undefined.
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

    test('sets "Create Purchase Plan" title when a selection exists', () => {
      (state.getSelectedRecommendationIDs as jest.Mock).mockReturnValue(new Set([0]));

      openCreatePlanModal();

      const title = document.getElementById('plan-modal-title');
      expect(title?.textContent).toBe('Create Purchase Plan');
    });

    test('clears plan ID for new plan', () => {
      (state.getSelectedRecommendationIDs as jest.Mock).mockReturnValue(new Set([0]));
      (document.getElementById('plan-id') as HTMLInputElement).value = 'existing-plan';

      openCreatePlanModal();

      expect((document.getElementById('plan-id') as HTMLInputElement).value).toBe('');
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
});
