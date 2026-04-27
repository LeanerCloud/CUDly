/**
 * Plans module for CUDly
 */

import * as api from './api';
import * as state from './state';
import { formatDate, formatTerm, getStatusBadge, escapeHtml, formatCurrency, populateAccountFilter } from './utils';
import { showToast } from './toast';
import { confirmDialog } from './confirmDialog';
import type { PlansResponse, LocalPlan, SavePlanData } from './types';
import { viewPlanHistory } from './history';
import type { PlannedPurchase } from './api';
import { populateTermSelect, populatePaymentSelect, isValidCombination, normalizePaymentValue } from './commitmentOptions';
import { openModal, closeModal } from './modal';

/**
 * Load plans and planned purchases
 */
export async function loadPlans(): Promise<void> {
  try {
    const data = await api.getPlans() as unknown as PlansResponse;
    let plans = data.plans || [];

    // Client-side provider filter. Backend `config.PurchasePlan` has no
    // top-level `provider` field — the plan's provider is derived from
    // its first service entry (see extractPlanInfo below). Filtering on
    // `p.provider` directly silently returned zero rows for every
    // non-empty filter value.
    const providerFilter = (document.getElementById('plans-provider-filter') as HTMLSelectElement | null)?.value;
    if (providerFilter) {
      plans = plans.filter(p => extractPlanInfo(p as unknown as BackendPlan).provider === providerFilter);
    }

    renderPlans(plans);
  } catch (error) {
    console.error('Failed to load plans:', error);
    const list = document.getElementById('plans-list');
    if (list) {
      const err = error as Error;
      list.innerHTML = `<p class="error">Failed to load plans: ${escapeHtml(err.message)}</p>`;
    }
  }

  // Load planned purchases
  await loadPlannedPurchases();
}

/**
 * Load planned purchases
 */
async function loadPlannedPurchases(): Promise<void> {
  const container = document.getElementById('planned-purchases-list');
  if (!container) return;

  try {
    const data = await api.getPlannedPurchases();
    renderPlannedPurchases(data.purchases || []);
  } catch (error) {
    console.error('Failed to load planned purchases:', error);
    const err = error as Error;
    container.innerHTML = `<p class="error">Failed to load planned purchases: ${escapeHtml(err.message)}</p>`;
  }
}

/**
 * Render planned purchases list
 */
function renderPlannedPurchases(purchases: PlannedPurchase[]): void {
  const container = document.getElementById('planned-purchases-list');
  if (!container) return;

  if (!purchases || purchases.length === 0) {
    container.innerHTML = '<p class="empty">No planned purchases. Create a purchase plan to schedule automatic purchases.</p>';
    return;
  }

  container.innerHTML = `
    <table class="planned-purchases-table">
      <thead>
        <tr>
          <th>Plan</th>
          <th>Scheduled Date</th>
          <th>Provider</th>
          <th>Service</th>
          <th>Resource</th>
          <th>Count</th>
          <th>Term</th>
          <th>Upfront</th>
          <th>Est. Savings</th>
          <th>Status</th>
          <th>Actions</th>
        </tr>
      </thead>
      <tbody>
        ${purchases.map(purchase => renderPlannedPurchaseRow(purchase)).join('')}
      </tbody>
    </table>
  `;

  // Add event listeners
  container.querySelectorAll<HTMLButtonElement>('[data-action]').forEach(btn => {
    btn.addEventListener('click', () => void handlePlannedPurchaseAction(
      btn.dataset['action'] || '',
      btn.dataset['id'] || ''
    ));
  });
}

/**
 * Render a single planned purchase row
 */
function renderPlannedPurchaseRow(purchase: PlannedPurchase): string {
  const statusClass = getPlannedPurchaseStatusClass(purchase.status);
  const isPaused = purchase.status === 'paused';
  const isPending = purchase.status === 'pending';
  const canRun = isPending || isPaused;
  const termCell = purchase.term > 0
    ? `${purchase.term}yr ${purchase.payment.replace('-', ' ')}`
    : '—';
  // Show an em-dash for upfront=0 unless the plan truly is all-upfront;
  // $0 upfront on "partial" or "no-upfront" is informative ($0 is real).
  // A zero on an all-upfront term almost always means missing data.
  const upfrontCell = purchase.upfront_cost > 0 || purchase.payment !== 'all-upfront'
    ? formatCurrency(purchase.upfront_cost)
    : '—';

  return `
    <tr class="planned-purchase-row ${statusClass}">
      <td>
        <span class="plan-name">${escapeHtml(purchase.plan_name)}</span>
        <span class="step-info">Step ${purchase.step_number}/${purchase.total_steps}</span>
      </td>
      <td>${formatDate(purchase.scheduled_date)}</td>
      <td><span class="provider-badge ${purchase.provider}">${purchase.provider.toUpperCase()}</span></td>
      <td>${escapeHtml(purchase.service)}</td>
      <td>${escapeHtml(purchase.resource_type)} (${escapeHtml(purchase.region)})</td>
      <td>${purchase.count}</td>
      <td>${termCell}</td>
      <td>${upfrontCell}</td>
      <td class="savings">${formatCurrency(purchase.estimated_savings)}/mo</td>
      <td><span class="status-badge ${statusClass}">${purchase.status}</span></td>
      <td class="actions">
        ${canRun ? `<button data-action="run" data-id="${purchase.id}" class="btn-small primary" title="Run now">▶</button>` : ''}
        ${isPending ? `<button data-action="pause" data-id="${purchase.id}" class="btn-small" title="Pause">⏸</button>` : ''}
        ${isPaused ? `<button data-action="resume" data-id="${purchase.id}" class="btn-small" title="Resume">⏵</button>` : ''}
        <button data-action="edit" data-id="${purchase.id}" class="btn-small" title="Edit Plan">✎</button>
        <button data-action="disable" data-id="${purchase.id}" class="btn-small danger" title="Disable Plan">✕</button>
      </td>
    </tr>
  `;
}

/**
 * Get CSS class for planned purchase status
 */
function getPlannedPurchaseStatusClass(status: string): string {
  switch (status) {
    case 'pending': return 'status-pending';
    case 'paused': return 'status-paused';
    case 'running': return 'status-running';
    case 'completed': return 'status-completed';
    case 'failed': return 'status-failed';
    default: return '';
  }
}

/**
 * Handle planned purchase action
 */
async function handlePlannedPurchaseAction(action: string, purchaseId: string): Promise<void> {
  try {
    switch (action) {
      case 'run':
        if (confirm('Run this purchase now? This will immediately execute the purchase.')) {
          await api.runPlannedPurchase(purchaseId);
          showToast({ message: 'Purchase executed successfully', kind: 'success', timeout: 5_000 });
        }
        break;
      case 'pause':
        await api.pausePlannedPurchase(purchaseId);
        break;
      case 'resume':
        await api.resumePlannedPurchase(purchaseId);
        break;
      case 'edit':
        // Open edit modal for the plan
        await editPlan(purchaseId);
        return;
      case 'disable':
        if (confirm('Disable this plan? The plan will be paused and no purchases will be scheduled. You can re-enable it later from the Plans list.')) {
          await api.deletePlannedPurchase(purchaseId);
          // Reload full plans list since we disabled a plan
          await loadPlans();
          return;
        }
        break;
    }
    await loadPlannedPurchases();
  } catch (error) {
    console.error(`Failed to ${action} planned purchase:`, error);
    const err = error as Error;
    showToast({ message: `Failed to ${action} purchase: ${err.message}`, kind: 'error' });
  }
}

// Backend plan type (as returned from API)
interface BackendPlan {
  id: string;
  name: string;
  enabled: boolean;
  auto_purchase: boolean;
  notification_days_before: number;
  services?: Record<string, {
    provider: string;
    service: string;
    enabled: boolean;
    term: number;
    payment: string;
    coverage: number;
  }>;
  ramp_schedule: {
    type: string;
    percent_per_step: number;
    step_interval_days: number;
    current_step: number;
    total_steps: number;
  };
  next_execution_date?: string;
}

// Pretty label for a service slug used inside the plan card.
//
// SP slugs are abbreviated ("Compute SP") rather than spelled out
// ("Compute Savings Plans") so a multi-SP plan with 3-4 entries still
// fits in the summary line. Non-SP slugs pass through unchanged so
// existing single-service plans render exactly as before.
function planServiceLabel(slug: string): string {
  switch (slug) {
    case 'savings-plans-compute':     return 'Compute SP';
    case 'savings-plans-ec2instance': return 'EC2 Instance SP';
    case 'savings-plans-sagemaker':   return 'SageMaker SP';
    case 'savings-plans-database':    return 'Database SP';
    default:                          return slug;
  }
}

// Extract provider/service info from plan's services map.
//
// `service` is now a comma-separated list of all services covered by
// the plan, not just the first map entry. Pre-PR #123 a plan only ever
// had one service slug; post-split a plan targeting multiple SP plan
// types has up to four entries (savings-plans-{compute,ec2instance,
// sagemaker,database}) but the old "first entry wins" rendering hid
// all but one. See issue #131 for the bug; this fix shows them all.
//
// `term` and `coverage` continue to come from the first entry — they
// are plan-level today, not per-service, so picking any entry is
// correct. If the model ever differentiates per service, this needs
// to render the same way.
function extractPlanInfo(plan: BackendPlan): { provider: string; service: string; term: number; coverage: number } {
  const services = plan.services || {};
  const serviceValues = Object.values(services);
  const firstService = serviceValues[0];
  if (firstService) {
    const service = serviceValues.length === 0
      ? '—'
      : serviceValues
          .map(s => planServiceLabel(s.service || '—'))
          .join(', ');
    return {
      provider: firstService.provider || 'aws',
      service,
      term: firstService.term || 3,
      coverage: firstService.coverage || 80
    };
  }
  return { provider: 'aws', service: '—', term: 3, coverage: 80 };
}

// Returns true when the plan's next_execution_date is strictly before today.
function isPlanOverdue(plan: BackendPlan): boolean {
  if (!plan.next_execution_date) return false;
  const nextDate = new Date(plan.next_execution_date);
  if (isNaN(nextDate.getTime())) return false;
  const today = new Date();
  today.setHours(0, 0, 0, 0);
  return nextDate < today;
}

// Format ramp schedule from backend struct
function formatBackendRampSchedule(ramp: BackendPlan['ramp_schedule']): string {
  if (!ramp) return 'Immediate';
  switch (ramp.type) {
    case 'immediate': return 'Immediate';
    case 'weekly': return `Weekly ${ramp.percent_per_step}%`;
    case 'monthly': return `Monthly ${ramp.percent_per_step}%`;
    case 'custom': return `Custom ${ramp.percent_per_step}% every ${ramp.step_interval_days} days`;
    default: return ramp.type || 'Unknown';
  }
}

async function loadPlanAccountNames(planId: string, cardEl: Element): Promise<void> {
  try {
    const accounts = await api.listPlanAccounts(planId);
    if (accounts.length === 0) return;
    const detailsEl = cardEl.querySelector('.plan-details');
    if (!detailsEl) return;
    const names = accounts.map(a => escapeHtml(a.name)).join(', ');
    const div = document.createElement('div');
    div.className = 'plan-detail';
    div.innerHTML = `<span class="plan-detail-label">Accounts</span><span class="plan-detail-value">${names}</span>`;
    detailsEl.appendChild(div);
  } catch {
    // Non-critical --- just do not show account names
  }
}

function renderPlans(plans: LocalPlan[]): void {
  const container = document.getElementById('plans-list');
  if (!container) return;

  if (!plans || plans.length === 0) {
    container.innerHTML = '<p class="empty">No purchase plans configured. Create one to automate your commitment purchases.</p>';
    return;
  }

  container.innerHTML = plans.map(rawPlan => {
    // Cast to BackendPlan to handle the actual API response format
    const plan = rawPlan as unknown as BackendPlan;
    const info = extractPlanInfo(plan);
    const status = getStatusBadge(plan.enabled, plan.auto_purchase);
    const rampSchedule = plan.ramp_schedule || { type: 'immediate', current_step: 0, total_steps: 1 };
    const overdue = isPlanOverdue(plan);
    // Hide the stale next_execution_date for disabled plans — keeping it
    // visible implies the plan will still run on that date, which it won't.
    const showNextDate = Boolean(plan.next_execution_date) && plan.enabled;
    const overdueBadge = overdue && plan.enabled
      ? '<span class="status-badge badge-danger" title="Next purchase date is in the past">Overdue</span>'
      : '';

    return `
      <div class="plan-card">
        <div class="plan-header">
          <h3>${escapeHtml(plan.name)}</h3>
          <div class="plan-status">
            <span class="status-badge ${status.class}">${status.label}</span>
            ${overdueBadge}
            <label class="toggle-label">
              <input type="checkbox" data-action="toggle-plan" data-id="${plan.id}" ${plan.enabled ? 'checked' : ''}>
              <span class="slider"></span>
            </label>
          </div>
        </div>
        <div class="plan-body">
          <div class="plan-details">
            <div class="plan-detail">
              <span class="plan-detail-label">Provider</span>
              <span class="plan-detail-value"><span class="provider-badge ${info.provider}">${info.provider.toUpperCase()}</span></span>
            </div>
            <div class="plan-detail">
              <span class="plan-detail-label">Service</span>
              <span class="plan-detail-value">${escapeHtml(info.service)}</span>
            </div>
            <div class="plan-detail">
              <span class="plan-detail-label">Term</span>
              <span class="plan-detail-value">${formatTerm(info.term)}</span>
            </div>
            <div class="plan-detail">
              <span class="plan-detail-label">Coverage</span>
              <span class="plan-detail-value">${info.coverage}%</span>
            </div>
            <div class="plan-detail">
              <span class="plan-detail-label">Ramp Schedule</span>
              <span class="plan-detail-value">${formatBackendRampSchedule(rampSchedule)}</span>
            </div>
            <div class="plan-detail">
              <span class="plan-detail-label">Progress</span>
              <span class="plan-detail-value">${rampSchedule.current_step || 0}/${rampSchedule.total_steps || 1} steps</span>
            </div>
            ${showNextDate ? `
            <div class="plan-detail">
              <span class="plan-detail-label">Next Purchase</span>
              <span class="plan-detail-value">${formatDate(plan.next_execution_date || '')}</span>
            </div>
            ` : ''}
          </div>
          <div class="plan-actions">
            <button data-action="add-purchases" data-id="${plan.id}" data-name="${escapeHtml(plan.name)}" class="primary">Add Purchases</button>
            <button data-action="edit-plan" data-id="${plan.id}">Edit</button>
            <button data-action="view-history" data-id="${plan.id}" class="secondary">History</button>
            <button data-action="delete-plan" data-id="${plan.id}" class="danger">Delete</button>
          </div>
        </div>
      </div>
    `;
  }).join('');

  // Asynchronously populate account names per plan
  container.querySelectorAll<HTMLElement>('.plan-card').forEach((card, idx) => {
    const plan = plans[idx] as unknown as BackendPlan;
    if (plan.id) void loadPlanAccountNames(plan.id, card);
  });

  // Add event listeners
  container.querySelectorAll<HTMLInputElement>('[data-action="toggle-plan"]').forEach(toggle => {
    toggle.addEventListener('change', () => void togglePlan(toggle.dataset['id'] || '', toggle.checked));
  });
  container.querySelectorAll<HTMLButtonElement>('[data-action="add-purchases"]').forEach(btn => {
    btn.addEventListener('click', () => void openAddPurchasesModal(btn.dataset['id'] || '', btn.dataset['name'] || ''));
  });
  container.querySelectorAll<HTMLButtonElement>('[data-action="edit-plan"]').forEach(btn => {
    btn.addEventListener('click', () => void editPlan(btn.dataset['id'] || ''));
  });
  container.querySelectorAll<HTMLButtonElement>('[data-action="view-history"]').forEach(btn => {
    btn.addEventListener('click', () => void viewPlanHistory(btn.dataset['id'] || ''));
  });
  container.querySelectorAll<HTMLButtonElement>('[data-action="delete-plan"]').forEach(btn => {
    btn.addEventListener('click', () => void deletePlanAction(btn.dataset['id'] || ''));
  });
}

async function togglePlan(planId: string, enabled: boolean): Promise<void> {
  try {
    await api.patchPlan(planId, { enabled } as Partial<api.CreatePlanRequest>);
    await loadPlans();
  } catch (error) {
    console.error('Failed to toggle plan:', error);
    showToast({ message: 'Failed to update plan', kind: 'error' });
    await loadPlans();
  }
}

async function editPlan(planId: string): Promise<void> {
  try {
    const backendPlan = await api.getPlan(planId) as unknown as BackendPlan;

    // Extract info from the backend plan format
    const info = extractPlanInfo(backendPlan);
    const rampSchedule = backendPlan.ramp_schedule || { type: 'immediate', percent_per_step: 100, step_interval_days: 0 };

    // Map ramp schedule type to frontend value
    let rampValue = 'immediate';
    if (rampSchedule.type === 'weekly' && rampSchedule.percent_per_step === 25) {
      rampValue = 'weekly-25pct';
    } else if (rampSchedule.type === 'monthly' && rampSchedule.percent_per_step === 10) {
      rampValue = 'monthly-10pct';
    } else if (rampSchedule.type === 'custom' || (rampSchedule.type !== 'immediate' && rampSchedule.type !== 'weekly' && rampSchedule.type !== 'monthly')) {
      rampValue = 'custom';
    }

    // Get payment option from services and normalize for provider
    const firstService = Object.values(backendPlan.services || {})[0];
    const rawPayment = firstService?.payment || 'no-upfront';
    const payment = normalizePaymentValue(rawPayment, info.provider);

    const titleEl = document.getElementById('plan-modal-title');
    if (titleEl) titleEl.textContent = 'Edit Purchase Plan';

    (document.getElementById('plan-id') as HTMLInputElement).value = backendPlan.id;
    (document.getElementById('plan-name') as HTMLInputElement).value = backendPlan.name;
    (document.getElementById('plan-description') as HTMLTextAreaElement).value = '';

    // Set provider and service first
    (document.getElementById('plan-provider') as HTMLSelectElement).value = info.provider;
    (document.getElementById('plan-service') as HTMLSelectElement).value = info.service;

    // Update term/payment options based on provider/service
    const termSelect = document.getElementById('plan-term') as HTMLSelectElement;
    const paymentSelect = document.getElementById('plan-payment') as HTMLSelectElement;
    populateTermSelect(termSelect, info.provider, info.service);
    populatePaymentSelect(paymentSelect, info.provider, info.service);

    // Now set term and payment values
    termSelect.value = String(info.term);
    paymentSelect.value = payment;

    (document.getElementById('plan-coverage') as HTMLInputElement).value = String(info.coverage);
    (document.getElementById('plan-auto-purchase') as HTMLInputElement).checked = backendPlan.auto_purchase;
    (document.getElementById('plan-notify-days') as HTMLInputElement).value = String(backendPlan.notification_days_before || 3);
    (document.getElementById('plan-enabled') as HTMLInputElement).checked = backendPlan.enabled;

    const rampRadio = document.querySelector<HTMLInputElement>(`input[name="ramp-schedule"][value="${rampValue}"]`);
    if (rampRadio) rampRadio.checked = true;

    const customConfig = document.getElementById('custom-ramp-config');
    if (customConfig) {
      customConfig.classList.toggle('hidden', rampValue !== 'custom');
    }

    if (rampValue === 'custom') {
      (document.getElementById('ramp-step-percent') as HTMLInputElement).value = String(rampSchedule.percent_per_step || 20);
      (document.getElementById('ramp-interval-days') as HTMLInputElement).value = String(rampSchedule.step_interval_days || 7);
    }

    void setupPlanAccountsSection(backendPlan.id);
    const planModal = document.getElementById('plan-modal');
    if (planModal) openModal(planModal);
  } catch (error) {
    console.error('Failed to load plan:', error);
    showToast({ message: 'Failed to load plan details', kind: 'error' });
  }
}

async function deletePlanAction(planId: string): Promise<void> {
  const ok = await confirmDialog({
    title: 'Delete this plan?',
    body: 'This removes the plan and cancels all its scheduled purchases. This action cannot be undone.',
    confirmLabel: 'Delete plan',
    destructive: true,
  });
  if (!ok) return;

  try {
    await api.deletePlan(planId);
    await loadPlans();
    showToast({ message: 'Plan deleted', kind: 'success', timeout: 5_000 });
  } catch (error) {
    console.error('Failed to delete plan:', error);
    showToast({ message: 'Failed to delete plan', kind: 'error' });
  }
}

/**
 * Save plan (create or update)
 */
export async function savePlan(e: Event): Promise<void> {
  e.preventDefault();

  const planId = (document.getElementById('plan-id') as HTMLInputElement).value;
  const rampScheduleRadio = document.querySelector<HTMLInputElement>('input[name="ramp-schedule"]:checked');
  const rampSchedule = rampScheduleRadio?.value || 'immediate';

  const plan: SavePlanData = {
    name: (document.getElementById('plan-name') as HTMLInputElement).value,
    description: (document.getElementById('plan-description') as HTMLTextAreaElement).value,
    provider: (document.getElementById('plan-provider') as HTMLSelectElement).value,
    service: (document.getElementById('plan-service') as HTMLSelectElement).value,
    term: parseInt((document.getElementById('plan-term') as HTMLSelectElement).value, 10),
    payment: (document.getElementById('plan-payment') as HTMLSelectElement).value,
    target_coverage: parseInt((document.getElementById('plan-coverage') as HTMLInputElement).value, 10),
    ramp_schedule: rampSchedule,
    auto_purchase: (document.getElementById('plan-auto-purchase') as HTMLInputElement).checked,
    notification_days_before: parseInt((document.getElementById('plan-notify-days') as HTMLInputElement).value, 10),
    enabled: (document.getElementById('plan-enabled') as HTMLInputElement).checked
  };

  if (rampSchedule === 'custom') {
    plan.custom_step_percent = parseInt((document.getElementById('ramp-step-percent') as HTMLInputElement).value, 10);
    plan.custom_interval_days = parseInt((document.getElementById('ramp-interval-days') as HTMLInputElement).value, 10);
  }

  const selectedIDs = state.getSelectedRecommendationIDs();
  if (selectedIDs.size > 0) {
    const currentRecs = state.getRecommendations();
    plan.recommendations = currentRecs.filter((r) => selectedIDs.has(r.id)) as api.Recommendation[];
  }

  try {
    let savedPlanId = planId;
    if (planId) {
      await api.updatePlan(planId, plan as unknown as api.CreatePlanRequest);
    } else {
      const created = await api.createPlan(plan as unknown as api.CreatePlanRequest) as unknown as { id: string };
      savedPlanId = created.id;
    }

    const accountIdsField = document.getElementById('plan-account-ids') as HTMLInputElement | null;
    const accountIds = accountIdsField?.value ? accountIdsField.value.split(',').filter(Boolean) : [];
    if (savedPlanId) {
      await api.setPlanAccounts(savedPlanId, accountIds);
    }

    closePlanModal();
    await loadPlans();
    showToast({ message: planId ? 'Plan updated successfully' : 'Plan created successfully', kind: 'success', timeout: 5_000 });
  } catch (error) {
    console.error('Failed to save plan:', error);
    const err = error as Error;
    showToast({ message: `Failed to save plan: ${err.message}`, kind: 'error' });
  }
}

/**
 * Close plan modal
 */
export function closePlanModal(): void {
  const planModal = document.getElementById('plan-modal');
  if (planModal) closeModal(planModal);
}

// Selected accounts for the plan modal
let planSelectedAccounts: Array<{ id: string; name: string; external_id: string }> = [];

/**
 * Render selected account chips in the plan modal
 */
function renderPlanAccountChips(): void {
  const container = document.getElementById('plan-accounts-selected');
  if (!container) return;
  container.textContent = '';
  planSelectedAccounts.forEach(acct => {
    const chip = document.createElement('span');
    chip.className = 'account-chip';
    chip.textContent = `${acct.name} (${acct.external_id})`;

    const removeBtn = document.createElement('button');
    removeBtn.type = 'button';
    removeBtn.textContent = '\u00d7';
    removeBtn.addEventListener('click', () => {
      planSelectedAccounts = planSelectedAccounts.filter(a => a.id !== acct.id);
      renderPlanAccountChips();
      updatePlanAccountIdsField();
    });
    chip.appendChild(removeBtn);
    container.appendChild(chip);
  });
}

/**
 * Update hidden plan-account-ids field
 */
function updatePlanAccountIdsField(): void {
  const field = document.getElementById('plan-account-ids') as HTMLInputElement | null;
  if (field) field.value = planSelectedAccounts.map(a => a.id).join(',');
}

let planAccountSearchTimer: ReturnType<typeof setTimeout> | null = null;

/**
 * Handle plan account search input
 */
async function handlePlanAccountSearch(value: string): Promise<void> {
  const suggestions = document.getElementById('plan-account-suggestions');
  if (!suggestions) return;

  if (!value.trim()) {
    suggestions.classList.add('hidden');
    return;
  }

  try {
    const accounts = await api.listAccounts({ search: value });
    suggestions.textContent = '';
    if (accounts.length === 0) {
      suggestions.classList.add('hidden');
      return;
    }
    accounts.forEach(a => {
      if (planSelectedAccounts.some(s => s.id === a.id)) return;
      const item = document.createElement('div');
      item.className = 'account-suggestion-item';
      item.textContent = `${a.name} (${a.external_id})`;
      item.addEventListener('click', () => {
        planSelectedAccounts.push({ id: a.id, name: a.name, external_id: a.external_id });
        renderPlanAccountChips();
        updatePlanAccountIdsField();
        suggestions.classList.add('hidden');
        (document.getElementById('plan-account-search') as HTMLInputElement).value = '';
      });
      suggestions.appendChild(item);
    });
    suggestions.classList.remove('hidden');
  } catch {
    suggestions.classList.add('hidden');
  }
}

/**
 * Set up plan accounts section in the modal
 */
async function setupPlanAccountsSection(planId?: string): Promise<void> {
  planSelectedAccounts = [];

  if (planId) {
    try {
      const existingAccounts = await api.listPlanAccounts(planId);
      planSelectedAccounts = existingAccounts.map(a => ({ id: a.id, name: a.name, external_id: a.external_id }));
    } catch {
      // Non-critical — section just starts empty
    }
  }

  renderPlanAccountChips();
  updatePlanAccountIdsField();

  const searchInput = document.getElementById('plan-account-search') as HTMLInputElement | null;
  if (searchInput) {
    // Remove previous listeners by replacing node
    const newInput = searchInput.cloneNode(true) as HTMLInputElement;
    searchInput.parentNode?.replaceChild(newInput, searchInput);
    newInput.addEventListener('input', () => {
      if (planAccountSearchTimer) clearTimeout(planAccountSearchTimer);
      planAccountSearchTimer = setTimeout(() => {
        void handlePlanAccountSearch(newInput.value);
      }, 300);
    });
  }
}

/**
 * Open create plan modal with selected recommendations.
 *
 * When the user has no selection (issue #17 reproducer: filter
 * active, no checkboxes ticked), fall through to the plain new-plan
 * flow instead of silently noop-ing behind a toast the user may
 * miss. Same UX as the dedicated "New Plan" button — the modal
 * always opens, and the user fills in provider/service from scratch.
 */
export function openCreatePlanModal(): void {
  const titleEl = document.getElementById('plan-modal-title');
  const hasSelection = state.getSelectedRecommendationIDs().size > 0;
  if (titleEl) {
    titleEl.textContent = hasSelection ? 'Create Purchase Plan' : 'New Purchase Plan';
  }
  (document.getElementById('plan-id') as HTMLInputElement).value = '';
  (document.getElementById('plan-form') as HTMLFormElement | null)?.reset();

  // Set up ramp schedule change handlers for dynamic plan name
  setupRampScheduleHandlers();

  // Generate initial plan name
  updatePlanNameFromSchedule();

  void setupPlanAccountsSection();

  const planModal = document.getElementById('plan-modal');
  if (planModal) openModal(planModal);
}

/**
 * Open new plan modal (without pre-selected recommendations)
 */
export function openNewPlanModal(): void {
  const titleEl = document.getElementById('plan-modal-title');
  if (titleEl) titleEl.textContent = 'New Purchase Plan';
  (document.getElementById('plan-id') as HTMLInputElement).value = '';
  (document.getElementById('plan-form') as HTMLFormElement | null)?.reset();

  // Set up ramp schedule change handlers for dynamic plan name
  setupRampScheduleHandlers();

  // Generate initial plan name
  updatePlanNameFromSchedule();

  void setupPlanAccountsSection();

  const planModal = document.getElementById('plan-modal');
  if (planModal) openModal(planModal);
}

/**
 * Generate a plan name based on the selected ramp schedule
 */
function generatePlanName(rampSchedule: string, customStepPercent?: number, customIntervalDays?: number): string {
  const service = (document.getElementById('plan-service') as HTMLSelectElement)?.value || 'EC2';
  const serviceUpper = service.toUpperCase();

  switch (rampSchedule) {
    case 'immediate':
      return `${serviceUpper} Full Coverage Purchase`;
    case 'weekly-25pct':
      return `${serviceUpper} Weekly 25% Ramp-up (4 weeks)`;
    case 'monthly-10pct':
      return `${serviceUpper} Monthly 10% Ramp-up (10 months)`;
    case 'custom':
      if (customStepPercent && customIntervalDays) {
        const totalSteps = Math.ceil(100 / customStepPercent);
        const intervalLabel = customIntervalDays === 7 ? 'weekly' :
                              customIntervalDays === 30 ? 'monthly' :
                              `every ${customIntervalDays} days`;
        return `${serviceUpper} Custom ${customStepPercent}% ${intervalLabel} (${totalSteps} steps)`;
      }
      return `${serviceUpper} Custom Ramp-up Plan`;
    default:
      return `${serviceUpper} Purchase Plan`;
  }
}

/**
 * Update plan name field based on current ramp schedule selection
 */
function updatePlanNameFromSchedule(): void {
  const planNameInput = document.getElementById('plan-name') as HTMLInputElement;
  const planIdInput = document.getElementById('plan-id') as HTMLInputElement;

  // Only auto-generate name for new plans (not editing existing ones)
  if (planIdInput?.value) return;

  const rampScheduleRadio = document.querySelector<HTMLInputElement>('input[name="ramp-schedule"]:checked');
  const rampSchedule = rampScheduleRadio?.value || 'immediate';

  let customStepPercent: number | undefined;
  let customIntervalDays: number | undefined;

  if (rampSchedule === 'custom') {
    customStepPercent = parseInt((document.getElementById('ramp-step-percent') as HTMLInputElement)?.value || '20', 10);
    customIntervalDays = parseInt((document.getElementById('ramp-interval-days') as HTMLInputElement)?.value || '7', 10);
  }

  if (planNameInput) {
    planNameInput.value = generatePlanName(rampSchedule, customStepPercent, customIntervalDays);
  }
}

/**
 * Set up event handlers for ramp schedule changes
 */
function setupRampScheduleHandlers(): void {
  // Listen to ramp schedule radio changes
  document.querySelectorAll<HTMLInputElement>('input[name="ramp-schedule"]').forEach(radio => {
    radio.addEventListener('change', () => {
      // Update custom config fields based on selected preset
      updateCustomConfigFromPreset(radio.value);

      updatePlanNameFromSchedule();

      // Show/hide custom config
      const customConfig = document.getElementById('custom-ramp-config');
      if (customConfig) {
        customConfig.classList.toggle('hidden', radio.value !== 'custom');
      }
    });
  });

  // Listen to custom schedule field changes
  const stepPercentInput = document.getElementById('ramp-step-percent');
  const intervalDaysInput = document.getElementById('ramp-interval-days');

  stepPercentInput?.addEventListener('input', updatePlanNameFromSchedule);
  intervalDaysInput?.addEventListener('input', updatePlanNameFromSchedule);

  // Listen to provider/service changes to update payment/term options
  const providerSelect = document.getElementById('plan-provider') as HTMLSelectElement | null;
  const serviceSelect = document.getElementById('plan-service') as HTMLSelectElement | null;
  const termSelect = document.getElementById('plan-term') as HTMLSelectElement | null;
  const paymentSelect = document.getElementById('plan-payment') as HTMLSelectElement | null;

  providerSelect?.addEventListener('change', () => {
    updateCommitmentOptions();
    updatePlanNameFromSchedule();
  });

  serviceSelect?.addEventListener('change', () => {
    updateCommitmentOptions();
    updatePlanNameFromSchedule();
  });

  termSelect?.addEventListener('change', () => {
    updatePaymentOptionsForTerm();
  });

  paymentSelect?.addEventListener('change', () => {
    updateTermOptionsForPayment();
  });

  // Initialize options based on default provider/service
  updateCommitmentOptions();
}

/**
 * Update term and payment options based on current provider/service selection
 */
function updateCommitmentOptions(): void {
  const providerSelect = document.getElementById('plan-provider') as HTMLSelectElement | null;
  const serviceSelect = document.getElementById('plan-service') as HTMLSelectElement | null;
  const termSelect = document.getElementById('plan-term') as HTMLSelectElement | null;
  const paymentSelect = document.getElementById('plan-payment') as HTMLSelectElement | null;

  if (!providerSelect || !serviceSelect || !termSelect || !paymentSelect) return;

  const provider = providerSelect.value;
  const service = serviceSelect.value;

  // Populate both selects with provider/service specific options
  populateTermSelect(termSelect, provider, service);
  populatePaymentSelect(paymentSelect, provider, service);

  // Validate current selection
  validateAndFixCombination();
}

/**
 * Update payment options based on selected term
 */
function updatePaymentOptionsForTerm(): void {
  const providerSelect = document.getElementById('plan-provider') as HTMLSelectElement | null;
  const serviceSelect = document.getElementById('plan-service') as HTMLSelectElement | null;
  const termSelect = document.getElementById('plan-term') as HTMLSelectElement | null;
  const paymentSelect = document.getElementById('plan-payment') as HTMLSelectElement | null;

  if (!providerSelect || !termSelect || !paymentSelect) return;

  const provider = providerSelect.value;
  const service = serviceSelect?.value;
  const term = parseInt(termSelect.value, 10);

  populatePaymentSelect(paymentSelect, provider, service, term);
}

/**
 * Update term options based on selected payment
 */
function updateTermOptionsForPayment(): void {
  const providerSelect = document.getElementById('plan-provider') as HTMLSelectElement | null;
  const serviceSelect = document.getElementById('plan-service') as HTMLSelectElement | null;
  const termSelect = document.getElementById('plan-term') as HTMLSelectElement | null;
  const paymentSelect = document.getElementById('plan-payment') as HTMLSelectElement | null;

  if (!providerSelect || !termSelect || !paymentSelect) return;

  const provider = providerSelect.value;
  const service = serviceSelect?.value;
  const payment = paymentSelect.value;

  populateTermSelect(termSelect, provider, service, payment);
}

/**
 * Validate and fix invalid term/payment combinations
 */
function validateAndFixCombination(): void {
  const providerSelect = document.getElementById('plan-provider') as HTMLSelectElement | null;
  const serviceSelect = document.getElementById('plan-service') as HTMLSelectElement | null;
  const termSelect = document.getElementById('plan-term') as HTMLSelectElement | null;
  const paymentSelect = document.getElementById('plan-payment') as HTMLSelectElement | null;

  if (!providerSelect || !termSelect || !paymentSelect) return;

  const provider = providerSelect.value;
  const service = serviceSelect?.value;
  const term = parseInt(termSelect.value, 10);
  const payment = paymentSelect.value;

  // Check if current combination is valid
  if (!isValidCombination(provider, service, term, payment)) {
    // Invalid combination - update payment to first valid option
    updatePaymentOptionsForTerm();
  }
}

/**
 * Update custom config fields based on the selected ramp schedule preset
 */
function updateCustomConfigFromPreset(rampSchedule: string): void {
  const stepPercentInput = document.getElementById('ramp-step-percent') as HTMLInputElement;
  const intervalDaysInput = document.getElementById('ramp-interval-days') as HTMLInputElement;

  if (!stepPercentInput || !intervalDaysInput) return;

  switch (rampSchedule) {
    case 'immediate':
      stepPercentInput.value = '100';
      intervalDaysInput.value = '0';
      break;
    case 'weekly-25pct':
      stepPercentInput.value = '25';
      intervalDaysInput.value = '7';
      break;
    case 'monthly-10pct':
      stepPercentInput.value = '10';
      intervalDaysInput.value = '30';
      break;
    case 'custom':
      // Don't change values when switching to custom - let user modify
      break;
  }
}

/**
 * Close purchase modal
 */
export function closePurchaseModal(): void {
  const purchaseModal = document.getElementById('purchase-modal');
  if (purchaseModal) closeModal(purchaseModal);
}

/**
 * Open modal to add planned purchases for a plan
 */
async function openAddPurchasesModal(planId: string, planName: string): Promise<void> {
  // Remove existing modal if present
  document.getElementById('add-purchases-modal')?.remove();

  const modal = document.createElement('div');
  modal.id = 'add-purchases-modal';
  modal.innerHTML = `
    <div class="modal-overlay">
      <div class="modal-content">
        <h2>Add Planned Purchases</h2>
        <p class="help-text">Schedule additional purchases for <strong>${escapeHtml(planName)}</strong></p>

        <form id="add-purchases-form">
          <input type="hidden" id="add-purchases-plan-id" value="${planId}">

          <label>Number of Purchases:
            <input type="number" id="add-purchases-count" min="1" max="52" value="1" required>
          </label>
          <p class="help-text">How many purchase steps to schedule (based on the plan's ramp schedule settings)</p>

          <label>Start Date:
            <input type="date" id="add-purchases-start-date" required>
          </label>
          <p class="help-text">When to schedule the first purchase. Subsequent purchases follow the plan's interval.</p>

          <div id="add-purchases-error" class="error-message hidden"></div>

          <div class="modal-buttons">
            <button type="button" id="add-purchases-cancel">Cancel</button>
            <button type="submit" class="primary">Add Purchases</button>
          </div>
        </form>
      </div>
    </div>
  `;
  document.body.appendChild(modal);

  // Set default start date to tomorrow
  const tomorrow = new Date();
  tomorrow.setDate(tomorrow.getDate() + 1);
  const startDateInput = document.getElementById('add-purchases-start-date') as HTMLInputElement;
  startDateInput.value = tomorrow.toISOString().split('T')[0] ?? '';

  // Add event listeners
  document.getElementById('add-purchases-cancel')?.addEventListener('click', closeAddPurchasesModal);
  document.getElementById('add-purchases-form')?.addEventListener('submit', (e) => void handleAddPurchases(e));

  // Engage focus trap + Escape handler. The modal element itself is
  // removed from the DOM on close (see closeAddPurchasesModal) instead
  // of just toggling .hidden, so the closeModal call there is what
  // actually triggers focus restoration to the trigger.
  openModal(modal);
}

/**
 * Close add purchases modal — restore focus first (closeModal), then
 * remove the dynamically-injected element from the DOM.
 */
function closeAddPurchasesModal(): void {
  const modal = document.getElementById('add-purchases-modal');
  if (modal) closeModal(modal);
  modal?.remove();
}

/**
 * Handle form submission for adding planned purchases
 */
async function handleAddPurchases(e: Event): Promise<void> {
  e.preventDefault();
  const errorDiv = document.getElementById('add-purchases-error');
  errorDiv?.classList.add('hidden');

  try {
    const planId = (document.getElementById('add-purchases-plan-id') as HTMLInputElement).value;
    const count = parseInt((document.getElementById('add-purchases-count') as HTMLInputElement).value, 10);
    const startDate = (document.getElementById('add-purchases-start-date') as HTMLInputElement).value;

    await api.createPlannedPurchases(planId, count, startDate);

    closeAddPurchasesModal();
    await loadPlannedPurchases();
    showToast({ message: `Successfully scheduled ${count} purchase${count > 1 ? 's' : ''}`, kind: 'success', timeout: 5_000 });
  } catch (error) {
    const err = error as Error;
    if (errorDiv) {
      errorDiv.textContent = err.message;
      errorDiv.classList.remove('hidden');
    }
  }
}

/**
 * Setup plan form event handlers (provider-aware service dropdown)
 */
function populatePlansAccountFilter(provider?: string): Promise<void> {
  return populateAccountFilter('plans-account-filter', api.listAccounts, provider);
}

export function setupPlanHandlers(): void {
  const plansProviderFilter = document.getElementById('plans-provider-filter') as HTMLSelectElement | null;
  if (plansProviderFilter) {
    plansProviderFilter.addEventListener('change', () => {
      void populatePlansAccountFilter(plansProviderFilter.value);
      void loadPlans();
    });
  }

  const plansAccountFilter = document.getElementById('plans-account-filter') as HTMLSelectElement | null;
  if (plansAccountFilter) {
    plansAccountFilter.addEventListener('change', () => void loadPlans());
  }

  void populatePlansAccountFilter();

  const providerSelect = document.getElementById('plan-provider') as HTMLSelectElement | null;
  const serviceSelect = document.getElementById('plan-service') as HTMLSelectElement | null;

  if (providerSelect && serviceSelect) {
    // Update service dropdown visibility when provider changes
    providerSelect.addEventListener('change', () => {
      updateServiceDropdownForProvider(providerSelect.value);
    });

    // Initialize with current provider value
    updateServiceDropdownForProvider(providerSelect.value);
  }
}

/**
 * Update service dropdown to show only services for selected provider
 */
function updateServiceDropdownForProvider(provider: string): void {
  const serviceSelect = document.getElementById('plan-service') as HTMLSelectElement | null;
  if (!serviceSelect) return;

  // Show/hide optgroups based on selected provider.
  //
  // Toggle the `hidden` class (same class the HTML starts with) so the
  // DOM state tracks a single source of truth — previously we flipped
  // `style.display`, which doesn't clear the pre-existing `hidden`
  // class, so Azure/GCP stayed hidden forever even when selected.
  //
  // Also flip `optgroup.disabled`: Chrome has a long-standing quirk
  // where a `display: none` <optgroup> still renders its <option>s as
  // selectable, so users could end up with provider=Azure + ec2. The
  // `disabled` attribute disables selection in every browser.
  const optgroups = serviceSelect.querySelectorAll('optgroup');
  let firstVisibleOptionValue = '';

  optgroups.forEach(optgroup => {
    const optgroupLabel = optgroup.label.toLowerCase();
    const shouldShow = optgroupLabel.includes(provider.toLowerCase());
    optgroup.classList.toggle('hidden', !shouldShow);
    optgroup.disabled = !shouldShow;

    // Track first visible option value to auto-select
    if (shouldShow && !firstVisibleOptionValue) {
      const firstOption = optgroup.querySelector('option');
      if (firstOption) {
        firstVisibleOptionValue = firstOption.value;
      }
    }
  });

  // If current selection is hidden, select first visible option
  const currentOption = serviceSelect.options[serviceSelect.selectedIndex];
  const parentOptgroup = currentOption?.parentElement;
  const isHidden = parentOptgroup instanceof HTMLOptGroupElement && parentOptgroup.classList.contains('hidden');
  if (isHidden && firstVisibleOptionValue) {
    serviceSelect.value = firstVisibleOptionValue;
    // Trigger change event to update term/payment options
    serviceSelect.dispatchEvent(new Event('change'));
  }
}
