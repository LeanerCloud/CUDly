/**
 * Plans module for CUDly
 */

import * as api from './api';
import * as state from './state';
import { formatDate, formatTerm, getStatusBadge, escapeHtml, escapeHtmlAttr, formatCurrency, CURRENCY_DEFAULT_DIGITS, providerBadgeHtml } from './utils';
import { showToast } from './toast';
import { confirmDialog } from './confirmDialog';
import type { PlansResponse, LocalPlan, SavePlanData } from './types';
import { viewPlanHistory } from './history';
import type { PlannedPurchase } from './api';
import { populateTermSelect, populatePaymentSelect, isValidCombination, normalizePaymentValue } from './commitmentOptions';
import { openModal, closeModal } from './modal';
import { showSkeletonTiles, showSkeletonRows, teardownSkeleton } from './lib/skeleton';
import { canAccess } from './permissions';
import { parseNumericFilter, applyColumnFilters as applyColumnFiltersLib } from './lib/column-filters';

// pendingPlanRecommendations holds the resolved plan target captured at
// "Plan from N selected" button-click time. The Plan flow used to re-derive
// its target from state.getVisibleRecommendations() + getSelectedRecommendation
// IDs() at savePlan time, but state mutations between modal-open and
// modal-Save (Refresh, filter changes, deselections) could silently shrink
// or replace the planned set. This snapshot is stamped by openCreatePlan-
// Modal(snapshot) and consumed by savePlan; openNewPlanModal() clears it
// (the New-Plan-from-scratch path has no pre-resolved target). See #273
// CR follow-up.
let pendingPlanRecommendations: api.Recommendation[] = [];

// Install-once guard for setupRampScheduleHandlers. The elements it binds are
// static modal singletons that are never replaced, so adding listeners on
// every modal open stacks duplicate handlers. The flag ensures we bind once;
// reset to false only in tests via resetRampHandlersForTest().
let rampHandlersInstalled = false;

/**
 * Load plans and planned purchases
 */
export async function loadPlans(): Promise<void> {
  // Issue #344 T3: skeleton tiles for the plans list. Synchronous
  // render before fetch so the page doesn't sit blank during the
  // round-trip. The planned-purchases skeleton lives in
  // loadPlannedPurchases so direct callers of that fetch (not via
  // loadPlans) get the same loading affordance.
  const plansList = document.getElementById('plans-list');
  if (plansList) showSkeletonTiles(plansList, 3);

  // Issue #365: hide the top-level "New Plan" button for sessions that
  // can't create plans. Readonly users hit a 403 on click otherwise.
  // The button itself stays in the DOM (HTML keeps the static markup)
  // so admin/user sessions get the same layout they always did.
  const newPlanBtn = document.getElementById('new-plan-btn');
  if (newPlanBtn) newPlanBtn.hidden = !canAccess('create', 'plans');

  try {
    // Account filter: pass account_ids to the backend so it JOINs
    // plan_accounts and returns only plans that reference one of the
    // selected accounts. Empty array means "all plans" — the backend
    // omits the JOIN entirely in that case. Mirrors the pattern used by
    // getRecommendations (see recommendations.ts, issue #705).
    const accountIDs = state.getCurrentAccountIDs();
    const data = await api.getPlans(
      accountIDs.length > 0 ? { account_ids: accountIDs } : {}
    ) as unknown as PlansResponse;
    let plans = data.plans || [];

    // Client-side provider filter. Backend `config.PurchasePlan` has no
    // top-level `provider` field — the plan's provider is derived from
    // its first service entry (see extractPlanInfo below). Filtering on
    // `p.provider` directly silently returned zero rows for every
    // non-empty filter value.
    // Filter source is the global topbar (state.ts), shared across
    // sections. The topbar's "All Providers" chip writes '' to state,
    // which is falsy so the filter is naturally skipped.
    const providerFilter = state.getCurrentProvider();
    if (providerFilter) {
      plans = plans.filter(p => extractPlanInfo(p as unknown as BackendPlan).provider === providerFilter);
    }

    renderPlans(plans);
  } catch (error) {
    console.error('Failed to load plans:', error);
    const list = document.getElementById('plans-list');
    if (list) {
      teardownSkeleton(list);
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

  // Issue #344 T3 (CR follow-up on PR #346): skeleton lives here, not
  // in loadPlans, so direct callers (e.g. follow-up refresh paths after
  // a single purchase action) also get the loading affordance. 5 rows
  // × 11 cols matches the rendered table — see renderPlannedPurchases.
  showSkeletonRows(container, 5, 11);

  try {
    const data = await api.getPlannedPurchases();
    renderPlannedPurchases(data.purchases || []);
  } catch (error) {
    console.error('Failed to load planned purchases:', error);
    teardownSkeleton(container);
    const err = error as Error;
    container.innerHTML = `<p class="error">Failed to load planned purchases: ${escapeHtml(err.message)}</p>`;
  }
}

// Cached last-fetched planned purchases. The filter popover commits re-render
// without re-fetching, so we hold the unfiltered set in module scope. Reset
// on every successful loadPlannedPurchases() and consumed by
// rerenderPlannedPurchases() (called by popover commits + Clear button).
let lastLoadedPurchases: PlannedPurchase[] = [];

/**
 * Render planned purchases list
 */
function renderPlannedPurchases(purchases: PlannedPurchase[]): void {
  lastLoadedPurchases = [...purchases];
  renderPlannedPurchasesInternal();
}

// renderPlannedPurchasesInternal is the actual render — separate from
// renderPlannedPurchases() so popover commits can re-render the table
// against the cached unfiltered set without re-fetching from the API.
function renderPlannedPurchasesInternal(): void {
  const container = document.getElementById('planned-purchases-list');
  if (!container) return;

  const purchases = lastLoadedPurchases;
  if (!purchases || purchases.length === 0) {
    container.innerHTML = '<p class="empty">No planned purchases. Create a purchase plan to schedule automatic purchases.</p>';
    return;
  }

  const filters = state.getPlansColumnFilters();
  const filtered = applyPlansColumnFilters(purchases, filters);

  const filterBtn = (column: state.PlansColumnId, lbl: string): string => {
    const active = filters[column] ? ' active' : '';
    const label = filters[column] ? `Filter ${lbl} \u2014 currently active` : `Filter ${lbl}`;
    return `<button type="button" class="column-filter-btn${active}" data-column="${column}" aria-haspopup="dialog" aria-expanded="false" aria-label="${label}" title="${label}">\u26db</button>`;
  };

  const tbody = filtered.length === 0
    ? `<tr><td colspan="11" class="empty">No rows match these filters.</td></tr>`
    : filtered.map(purchase => renderPlannedPurchaseRow(purchase)).join('');

  container.innerHTML = `
    <table class="planned-purchases-table">
      <thead>
        <tr>
          <th>Plan</th>
          <th>Scheduled Date</th>
          <th><span>Provider</span>${filterBtn('provider', 'Provider')}</th>
          <th><span>Service</span>${filterBtn('service', 'Service')}</th>
          <th><span>Resource Type</span>${filterBtn('resource_type', 'Resource Type')}</th>
          <th><span>Count</span>${filterBtn('count', 'Count')}</th>
          <th><span>Term</span>${filterBtn('term', 'Term')}${filterBtn('payment', 'Payment')}</th>
          <th><span>Upfront</span>${filterBtn('upfront_cost', 'Upfront')}</th>
          <th><span>Est. Savings</span>${filterBtn('estimated_savings', 'Est. Savings')}</th>
          <th><span>Status</span>${filterBtn('status', 'Status')}</th>
          <th>Actions</th>
        </tr>
      </thead>
      <tbody>
        ${tbody}
      </tbody>
    </table>
  `;

  // Add event listeners for row action buttons
  container.querySelectorAll<HTMLButtonElement>('[data-action]').forEach(btn => {
    btn.addEventListener('click', () => void handlePlannedPurchaseAction(
      btn.dataset['action'] || '',
      btn.dataset['id'] || '',
      btn.dataset['planId'] || ''
    ));
  });

  // Per-column filter trigger buttons. e.stopPropagation prevents any future
  // surrounding-th handlers (sort etc.) from firing on the same click.
  container.querySelectorAll<HTMLButtonElement>('.column-filter-btn').forEach((btn) => {
    const column = btn.dataset['column'] as state.PlansColumnId | undefined;
    if (!column) return;
    btn.addEventListener('click', (e) => {
      e.stopPropagation();
      openPlansColumnPopover(column, btn);
    });
  });

  // Re-anchor any open popover to the freshly-rendered trigger so the
  // popover survives table re-renders triggered by other state changes.
  rebindOpenPlansPopoverAnchor();
}

// ---------------------------------------------------------------------------
// Per-column filter pipeline (issue #166 follow-up to #570).
//
// Mirrors the canonical recommendations.ts wiring: cell extractors map each
// row + column id to its raw value, numeric values are rounded to the cell's
// display precision so filter predicates match what the user sees.
// ---------------------------------------------------------------------------

function applyPlansColumnFilters(
  purchases: readonly PlannedPurchase[],
  filters: state.PlansColumnFilters,
): PlannedPurchase[] {
  return applyColumnFiltersLib<PlannedPurchase, state.PlansColumnId>(
    purchases,
    filters,
    {
      categorical: categoricalCellValueForPlan,
      numeric: (p, col) => roundForDisplay(numericCellValueForPlan(p, col), displayPrecisionForPlan(col)),
    },
  );
}

function categoricalCellValueForPlan(p: PlannedPurchase, col: state.PlansColumnId): string {
  switch (col) {
    case 'provider':       return p.provider ?? '';
    case 'service':        return p.service ?? '';
    case 'resource_type':  return p.resource_type ?? '';
    case 'term':           return p.term == null ? '' : String(p.term);
    case 'payment':        return p.payment ?? '';
    case 'status':         return p.status ?? '';
    // Numeric columns shouldn't reach this branch; return empty for type-safety.
    case 'count':
    case 'upfront_cost':
    case 'estimated_savings':
      return '';
  }
}

function numericCellValueForPlan(p: PlannedPurchase, col: state.PlansColumnId): number {
  switch (col) {
    case 'count':              return p.count ?? 0;
    case 'upfront_cost':       return p.upfront_cost ?? 0;
    case 'estimated_savings':  return p.estimated_savings ?? 0;
    case 'provider':
    case 'service':
    case 'resource_type':
    case 'term':
    case 'payment':
    case 'status':
      return Number.NaN;
  }
}

// Issue #484 parity: filter predicates compare against the rounded display
// value so "exact-match" filters work for rows whose raw value rounds to the
// typed value. The planned-purchases table renders count as integer and
// currency cells via formatCurrency (CURRENCY_DEFAULT_DIGITS = 0).
function displayPrecisionForPlan(col: state.PlansColumnId): number {
  switch (col) {
    case 'count':
      return 0;
    case 'upfront_cost':
    case 'estimated_savings':
      return CURRENCY_DEFAULT_DIGITS;
    case 'provider':
    case 'service':
    case 'resource_type':
    case 'term':
    case 'payment':
    case 'status':
      return CURRENCY_DEFAULT_DIGITS;
  }
}

function roundForDisplay(n: number, precision: number): number {
  if (!Number.isFinite(n)) return n;
  return Number(n.toFixed(precision));
}

// ---------------------------------------------------------------------------
// Plans column-filter popover (portal pattern — sibling of the one in
// recommendations.ts). Lives appended to document.body so it survives the
// table's innerHTML rewrite on every render.
// ---------------------------------------------------------------------------

const PLANS_NUMERIC_COLUMNS: ReadonlySet<state.PlansColumnId> = new Set<state.PlansColumnId>([
  'count', 'upfront_cost', 'estimated_savings',
]);

interface PlansPopoverState {
  column: state.PlansColumnId;
  el: HTMLDivElement;
  checkboxes: Map<string, HTMLInputElement>;
  input: HTMLInputElement | null;
  errorEl: HTMLElement | null;
}

let openPlansPopover: PlansPopoverState | null = null;
let plansOutsideClickHandler: ((e: MouseEvent) => void) | null = null;
let plansEscKeyHandler: ((e: KeyboardEvent) => void) | null = null;
let plansResizeHandler: (() => void) | null = null;

function plansColumnLabel(column: state.PlansColumnId): string {
  switch (column) {
    case 'provider':           return 'Provider';
    case 'service':            return 'Service';
    case 'resource_type':      return 'Resource Type';
    case 'term':               return 'Term';
    case 'payment':            return 'Payment';
    case 'status':             return 'Status';
    case 'count':              return 'Count';
    case 'upfront_cost':       return 'Upfront';
    case 'estimated_savings':  return 'Est. Savings';
  }
}

function plansCategoricalDisplayLabel(column: state.PlansColumnId, value: string): string {
  if (value === '') return '(empty)';
  if (column === 'term') {
    const n = Number(value);
    return Number.isFinite(n) && n > 0 ? formatTerm(n) : value;
  }
  if (column === 'provider') {
    return value.toUpperCase();
  }
  return value;
}

function getPlansColumnTriggerButton(column: state.PlansColumnId): HTMLButtonElement | null {
  return document.querySelector<HTMLButtonElement>(
    `#planned-purchases-list th .column-filter-btn[data-column="${column}"]`,
  );
}

function positionPlansPopover(popover: HTMLElement, anchor: HTMLElement): void {
  const rect = anchor.getBoundingClientRect();
  popover.style.display = 'block';
  const popRect = popover.getBoundingClientRect();
  const margin = 8;

  let top = rect.bottom + 4;
  if (top + popRect.height > window.innerHeight - margin) {
    top = Math.max(margin, rect.top - popRect.height - 4);
  }
  let left = rect.left;
  if (left + popRect.width > window.innerWidth - margin) {
    left = Math.max(margin, window.innerWidth - margin - popRect.width);
  }
  popover.style.position = 'absolute';
  popover.style.top = `${top + window.scrollY}px`;
  popover.style.left = `${left + window.scrollX}px`;
}

function plansDistinctValuesForColumn(
  purchases: readonly PlannedPurchase[],
  column: state.PlansColumnId,
): string[] {
  const seen = new Set<string>();
  for (const p of purchases) {
    seen.add(categoricalCellValueForPlan(p, column));
  }
  return Array.from(seen).sort((a, b) => {
    if (a === '' && b !== '') return -1;
    if (a !== '' && b === '') return 1;
    return a.localeCompare(b);
  });
}

function buildPlansPopoverContent(
  column: state.PlansColumnId,
  purchases: readonly PlannedPurchase[],
): { el: HTMLDivElement; checkboxes: Map<string, HTMLInputElement>; input: HTMLInputElement | null; errorEl: HTMLElement | null } {
  const popover = document.createElement('div');
  popover.className = 'column-filter-popover';
  popover.setAttribute('role', 'dialog');
  popover.setAttribute('aria-modal', 'false');

  const headingId = `plans-column-filter-heading-${column}`;
  popover.setAttribute('aria-labelledby', headingId);

  const heading = document.createElement('h3');
  heading.id = headingId;
  heading.className = 'column-filter-heading';
  heading.textContent = `Filter ${plansColumnLabel(column)}`;
  popover.appendChild(heading);

  const checkboxes = new Map<string, HTMLInputElement>();
  let input: HTMLInputElement | null = null;
  let errorEl: HTMLElement | null = null;
  let commitAllRef: ((target: boolean) => void) | null = null;

  if (PLANS_NUMERIC_COLUMNS.has(column)) {
    const label = document.createElement('label');
    label.className = 'column-filter-numeric-label';
    label.textContent = 'Expression';
    input = document.createElement('input');
    input.type = 'text';
    input.className = 'column-filter-numeric-input';
    input.placeholder = 'e.g. >100, 50..200, 5';
    input.setAttribute('aria-describedby', `plans-column-filter-error-${column}`);
    label.appendChild(input);
    popover.appendChild(label);

    errorEl = document.createElement('div');
    errorEl.id = `plans-column-filter-error-${column}`;
    errorEl.className = 'column-filter-error';
    errorEl.setAttribute('role', 'status');
    popover.appendChild(errorEl);

    const commit = (): void => {
      const expr = input!.value.trim();
      if (expr === '') {
        state.setPlansColumnFilter(column, null);
        errorEl!.textContent = '';
        rerenderPlannedPurchases();
        return;
      }
      const parsed = parseNumericFilter(expr);
      if (!parsed.ok) {
        errorEl!.textContent = parsed.error;
        return;
      }
      errorEl!.textContent = '';
      state.setPlansColumnFilter(column, { kind: 'expr', expr });
      rerenderPlannedPurchases();
    };
    input.addEventListener('blur', commit);
    input.addEventListener('keydown', (e) => {
      if (e.key === 'Enter') {
        e.preventDefault();
        commit();
      }
    });
  } else {
    const distinct = plansDistinctValuesForColumn(purchases, column);

    const allLabel = document.createElement('label');
    allLabel.className = 'column-filter-all';
    const allBox = document.createElement('input');
    allBox.type = 'checkbox';
    allBox.dataset['role'] = 'all';
    allLabel.appendChild(allBox);
    const allText = document.createElement('span');
    allText.textContent = '(All)';
    allLabel.appendChild(allText);
    popover.appendChild(allLabel);

    const list = document.createElement('div');
    list.className = 'column-filter-list';
    for (const value of distinct) {
      const itemLabel = document.createElement('label');
      itemLabel.className = 'column-filter-item';
      const cb = document.createElement('input');
      cb.type = 'checkbox';
      cb.dataset['value'] = value;
      itemLabel.appendChild(cb);
      const text = document.createElement('span');
      text.textContent = plansCategoricalDisplayLabel(column, value);
      itemLabel.appendChild(text);
      list.appendChild(itemLabel);
      checkboxes.set(value, cb);
    }
    popover.appendChild(list);

    const updateAllTriState = (): void => {
      const total = checkboxes.size;
      let checked = 0;
      checkboxes.forEach((cb) => { if (cb.checked) checked++; });
      allBox.indeterminate = checked > 0 && checked < total;
      allBox.checked = checked === total && total > 0;
    };

    const commit = (): void => {
      const selected: string[] = [];
      checkboxes.forEach((cb, value) => { if (cb.checked) selected.push(value); });
      if (selected.length === checkboxes.size) {
        state.setPlansColumnFilter(column, null);
      } else {
        state.setPlansColumnFilter(column, { kind: 'set', values: selected });
      }
      updateAllTriState();
      rerenderPlannedPurchases();
    };

    const commitAll = (target: boolean): void => {
      checkboxes.forEach((cb) => { cb.checked = target; });
      if (target) {
        state.setPlansColumnFilter(column, null);
      } else {
        state.setPlansColumnFilter(column, { kind: 'set', values: [] });
      }
      updateAllTriState();
      rerenderPlannedPurchases();
    };
    commitAllRef = commitAll;

    checkboxes.forEach((cb) => {
      cb.addEventListener('change', commit);
    });
    allBox.addEventListener('change', () => {
      commitAll(allBox.checked);
    });
  }

  const footer = document.createElement('div');
  footer.className = 'column-filter-footer';
  const clearBtn = document.createElement('button');
  clearBtn.type = 'button';
  clearBtn.className = 'column-filter-clear';
  clearBtn.textContent = 'Clear';
  clearBtn.addEventListener('click', () => {
    if (input) {
      state.setPlansColumnFilter(column, null);
      input.value = '';
      if (errorEl) errorEl.textContent = '';
      rerenderPlannedPurchases();
    } else {
      commitAllRef?.(false);
    }
  });
  footer.appendChild(clearBtn);
  popover.appendChild(footer);

  return { el: popover, checkboxes, input, errorEl };
}

function resyncOpenPlansPopover(): void {
  if (!openPlansPopover) return;
  const f = state.getPlansColumnFilters()[openPlansPopover.column];
  if (openPlansPopover.input) {
    if (document.activeElement !== openPlansPopover.input) {
      const expr = f && f.kind === 'expr' ? f.expr : '';
      openPlansPopover.input.value = expr;
      if (openPlansPopover.errorEl) openPlansPopover.errorEl.textContent = '';
    }
    return;
  }
  if (f == null) {
    openPlansPopover.checkboxes.forEach((cb) => { cb.checked = true; });
  } else {
    const values: ReadonlySet<string> = f.kind === 'set' ? new Set(f.values) : new Set();
    openPlansPopover.checkboxes.forEach((cb, value) => {
      cb.checked = values.has(value);
    });
  }
  const allBox = openPlansPopover.el.querySelector<HTMLInputElement>('input[data-role="all"]');
  if (allBox) {
    const total = openPlansPopover.checkboxes.size;
    let checked = 0;
    openPlansPopover.checkboxes.forEach((cb) => { if (cb.checked) checked++; });
    allBox.indeterminate = checked > 0 && checked < total;
    allBox.checked = checked === total && total > 0;
  }
}

function attachPlansPopoverGlobalListeners(): void {
  if (plansOutsideClickHandler) return;
  plansOutsideClickHandler = (e: MouseEvent): void => {
    if (!openPlansPopover) return;
    const target = e.target as Node | null;
    if (!target) return;
    if (openPlansPopover.el.contains(target)) return;
    if (target instanceof Element && target.closest('.column-filter-btn')) return;
    closePlansPopover();
  };
  plansEscKeyHandler = (e: KeyboardEvent): void => {
    if (!openPlansPopover) return;
    if (e.key === 'Escape') {
      e.preventDefault();
      closePlansPopover(true);
    }
  };
  plansResizeHandler = (): void => {
    if (!openPlansPopover) return;
    const trigger = getPlansColumnTriggerButton(openPlansPopover.column);
    if (!trigger) {
      closePlansPopover();
      return;
    }
    positionPlansPopover(openPlansPopover.el, trigger);
  };
  document.addEventListener('mousedown', plansOutsideClickHandler);
  document.addEventListener('keydown', plansEscKeyHandler);
  window.addEventListener('resize', plansResizeHandler);
}

function detachPlansPopoverGlobalListeners(): void {
  if (plansOutsideClickHandler) document.removeEventListener('mousedown', plansOutsideClickHandler);
  if (plansEscKeyHandler) document.removeEventListener('keydown', plansEscKeyHandler);
  if (plansResizeHandler) window.removeEventListener('resize', plansResizeHandler);
  plansOutsideClickHandler = null;
  plansEscKeyHandler = null;
  plansResizeHandler = null;
}

function openPlansColumnPopover(column: state.PlansColumnId, anchor: HTMLElement): void {
  if (openPlansPopover && !openPlansPopover.el.isConnected) {
    detachPlansPopoverGlobalListeners();
    openPlansPopover = null;
  }
  if (openPlansPopover && openPlansPopover.column === column) {
    closePlansPopover(true);
    return;
  }
  if (openPlansPopover) closePlansPopover();

  const built = buildPlansPopoverContent(column, lastLoadedPurchases);
  document.body.appendChild(built.el);
  openPlansPopover = {
    column,
    el: built.el,
    checkboxes: built.checkboxes,
    input: built.input,
    errorEl: built.errorEl,
  };
  resyncOpenPlansPopover();
  positionPlansPopover(built.el, anchor);
  anchor.setAttribute('aria-expanded', 'true');
  attachPlansPopoverGlobalListeners();

  const firstFocusable = built.input
    ?? built.el.querySelector<HTMLInputElement>('input[type="checkbox"]');
  firstFocusable?.focus();
}

function closePlansPopover(restoreFocus = false): void {
  if (!openPlansPopover) return;
  const { column, el } = openPlansPopover;
  el.remove();
  openPlansPopover = null;
  detachPlansPopoverGlobalListeners();
  const trigger = getPlansColumnTriggerButton(column);
  if (trigger) {
    trigger.setAttribute('aria-expanded', 'false');
    if (restoreFocus) trigger.focus();
  }
}

function rebindOpenPlansPopoverAnchor(): void {
  if (!openPlansPopover) return;
  const trigger = getPlansColumnTriggerButton(openPlansPopover.column);
  if (!trigger) {
    closePlansPopover();
    return;
  }
  trigger.setAttribute('aria-expanded', 'true');
  positionPlansPopover(openPlansPopover.el, trigger);
  resyncOpenPlansPopover();
}

function rerenderPlannedPurchases(): void {
  renderPlannedPurchasesInternal();
}

// canManageScheduledPurchase returns true when the current session is
// permitted to act on the given scheduled purchase's row buttons (issue #950).
// UX gate only -- the backend authorizeExecutionManagement in
// internal/api/handler_purchases.go remains the security boundary; a
// false-positive here surfaces as a 403 toast on click rather than a
// successful mutation.
//
// Heuristic (mirrors the creator-scope model on History rows):
//   * admin (admin:* wildcard) or update-any:purchases -> manage anyone's row;
//   * otherwise the row's created_by_user_id must match the current user;
//   * legacy rows with a NULL created_by_user_id -> no buttons for non-
//     privileged users (out of reach without update-any).
function canManageScheduledPurchase(purchase: PlannedPurchase): boolean {
  if (canAccess('admin', '*') || canAccess('update-any', 'purchases')) return true;
  const user = state.getCurrentUser();
  if (!user) return false;
  if (!purchase.created_by_user_id) return false;
  return purchase.created_by_user_id === user.id;
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

  // Issue #365: gate row actions by the same plan-management permissions
  // a click on each button would require. Readonly users see no buttons
  // (status badge only); user role sees Run/Pause/Resume/Edit but not
  // Disable; admins see everything.
  //
  // Issue #950: AND in creator-scope ownership. A non-creator who lacks
  // update-any:purchases (a standard user looking at someone else's row)
  // sees NO action buttons, mirroring the backend ownership gate. This is
  // a UX gate; the backend authorizePlannedPurchaseCancel is the real
  // boundary.
  //
  // Issue #1442: the Disable button (cancel) must be shown to the creator
  // when they hold cancel-own:purchases or cancel-any:purchases, not only
  // when they hold delete:purchases. This mirrors the cancel-verb logic in
  // canCancelUpcomingPurchase (dashboard.ts) and the backend
  // requireDeleteOrCancelPurchasePermission gate introduced by PR #1421.
  // canManagePurchase already enforces the ownership check, so accepting
  // cancel-own here only widens the Disable button for the creator's own row.
  const canManagePurchase = canManageScheduledPurchase(purchase);
  const canRunPurchase = canManagePurchase && canAccess('execute', 'purchases') && canRun;
  const canPauseOrResumePurchase = canManagePurchase && canAccess('update', 'purchases');
  const canEditPlan = canManagePurchase && canAccess('update', 'plans');
  const canDisablePlan = canManagePurchase && (
    canAccess('delete', 'purchases') ||
    canAccess('cancel-any', 'purchases') ||
    canAccess('cancel-own', 'purchases')
  );

  return `
    <tr class="planned-purchase-row ${statusClass}">
      <td>
        <span class="plan-name">${escapeHtml(purchase.plan_name)}</span>
        <span class="step-info">Step ${purchase.step_number}/${purchase.total_steps}</span>
      </td>
      <td>${formatDate(purchase.scheduled_date)}</td>
      <td>${providerBadgeHtml(purchase.provider)}</td>
      <td>${escapeHtml(purchase.service)}</td>
      <td>${escapeHtml(purchase.resource_type)} (${escapeHtml(purchase.region)})</td>
      <td>${purchase.count}</td>
      <td>${termCell}</td>
      <td>${upfrontCell}</td>
      <td class="savings">${formatCurrency(purchase.estimated_savings)}/mo</td>
      <td><span class="status-badge ${statusClass}">${escapeHtml(purchase.status)}</span></td>
      <td class="actions">
        ${canRunPurchase ? `<button data-action="run" data-id="${escapeHtmlAttr(purchase.id)}" class="btn-small primary" title="Run now">▶</button>` : ''}
        ${canPauseOrResumePurchase && isPending ? `<button data-action="pause" data-id="${escapeHtmlAttr(purchase.id)}" class="btn-small" title="Pause">⏸</button>` : ''}
        ${canPauseOrResumePurchase && isPaused ? `<button data-action="resume" data-id="${escapeHtmlAttr(purchase.id)}" class="btn-small" title="Resume">⏵</button>` : ''}
        ${canEditPlan ? `<button data-action="edit" data-id="${escapeHtmlAttr(purchase.id)}" data-plan-id="${escapeHtmlAttr(purchase.plan_id)}" class="btn-small" title="Edit Plan">✎</button>` : ''}
        ${canDisablePlan ? `<button data-action="disable" data-id="${escapeHtmlAttr(purchase.id)}" class="btn-small danger" title="Disable Plan">✕</button>` : ''}
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
async function handlePlannedPurchaseAction(action: string, purchaseId: string, planId = ''): Promise<void> {
  try {
    switch (action) {
      case 'run': {
        // Use styled async dialog (11-L2) instead of blocking browser confirm().
        const runOk = await confirmDialog({
          title: 'Run purchase now?',
          body: 'This will immediately execute the purchase.',
          confirmLabel: 'Run now',
          destructive: true,
        });
        if (runOk) {
          await api.runPlannedPurchase(purchaseId);
          showToast({ message: 'Purchase executed successfully', kind: 'success', timeout: 5_000 });
        }
        break;
      }
      case 'pause':
        // Pause is reversible and scoped to a single execution: the plan stays
        // enabled (unlike Disable plan) and the row stays listed with a Paused
        // badge (unlike the old silent-removal behaviour).
        await api.pausePlannedPurchase(purchaseId);
        showToast({ message: 'Purchase paused', kind: 'success', timeout: 5_000 });
        break;
      case 'resume':
        await api.resumePlannedPurchase(purchaseId);
        showToast({ message: 'Purchase resumed', kind: 'success', timeout: 5_000 });
        break;
      case 'edit':
        // Open edit modal for the parent plan using plan_id, not the purchase id.
        // The purchase row's data-plan-id attribute carries the plan FK (#773).
        if (!planId) {
          console.warn('edit action ignored: missing plan id');
          return;
        }
        // If the plan can't be loaded (deleted or no longer accessible while
        // the row was on screen), reconcile the list so the orphaned row is
        // dropped instead of leaving a dead Edit button (issue #1403).
        if (!(await editPlan(planId))) {
          await loadPlannedPurchases();
        }
        return;
      case 'disable': {
        // Use styled async dialog (11-L2) instead of blocking browser confirm().
        const disableOk = await confirmDialog({
          title: 'Disable this plan?',
          body: 'The plan will be paused and no purchases will be scheduled. You can re-enable it later from the Plans list.',
          confirmLabel: 'Disable plan',
          destructive: true,
        });
        if (disableOk) {
          await api.deletePlannedPurchase(purchaseId);
          // Reload full plans list since we disabled a plan
          await loadPlans();
          return;
        }
        break;
      }
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
  // unassigned is true for legacy plans that have zero plan_accounts rows
  // (issue #973). The backend sets this flag when an account filter is
  // active so the frontend can bucket them under an "Unassigned" section.
  unassigned?: boolean;
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
function extractPlanInfo(plan: BackendPlan): { provider: string | null; service: string; term: number | null; coverage: number | null } {
  const services = plan.services || {};
  const serviceValues = Object.values(services);
  const firstService = serviceValues[0];
  if (firstService) {
    const service = serviceValues.length === 0
      ? '—'
      : serviceValues
          .map(s => planServiceLabel(s.service || '—'))
          .join(', ');
    // H-4: never fabricate provider/term/coverage when absent.
    // Return null so callers can surface '--'/'Unknown' in the display
    // layer rather than silently committing to aws/3yr/80% defaults.
    return {
      provider: firstService.provider || null,
      service,
      term: firstService.term || null,
      coverage: firstService.coverage ?? null
    };
  }
  // No services at all: all fields are unknown.
  return { provider: null, service: '—', term: null, coverage: null };
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

// renderPlanCard generates the HTML for a single plan card.
// When the plan is unassigned (no plan_accounts rows) account-scoped
// actions that require an account (Add Purchases, Edit) are suppressed
// — only History and Delete remain — and a read-only "Unassigned" badge
// is shown so operators can identify and re-scope the plan.
function renderPlanCard(plan: BackendPlan, canManagePlan: boolean, canDeletePlan: boolean, canAddPurchases: boolean): string {
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
  // Read-only mode: unassigned plans cannot be purchased against until an
  // operator re-assigns them to at least one account.
  const isUnassigned = Boolean(plan.unassigned);

  return `
    <div class="plan-card">
      <div class="plan-header">
        <h3>${escapeHtml(plan.name)}</h3>
        <div class="plan-status">
          <span class="status-badge ${status.class}">${status.label}</span>
          ${overdueBadge}
          ${canManagePlan && !isUnassigned ? `
          <label class="toggle-label">
            <input type="checkbox" data-action="toggle-plan" data-id="${escapeHtmlAttr(plan.id)}" ${plan.enabled ? 'checked' : ''}>
            <span class="slider"></span>
          </label>
          ` : ''}
        </div>
      </div>
      <div class="plan-body">
        <div class="plan-details">
          <div class="plan-detail">
            <span class="plan-detail-label">Provider</span>
            <span class="plan-detail-value">${providerBadgeHtml(info.provider)}</span>
          </div>
          <div class="plan-detail">
            <span class="plan-detail-label">Service</span>
            <span class="plan-detail-value">${escapeHtml(info.service)}</span>
          </div>
          <div class="plan-detail">
            <span class="plan-detail-label">Term</span>
            <span class="plan-detail-value">${info.term !== null ? formatTerm(info.term) : '--'}</span>
          </div>
          <div class="plan-detail">
            <span class="plan-detail-label">Coverage</span>
            <span class="plan-detail-value">${info.coverage !== null ? `${info.coverage}%` : '--'}</span>
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
          ${canAddPurchases && !isUnassigned ? `<button data-action="add-purchases" data-id="${escapeHtmlAttr(plan.id)}" data-name="${escapeHtmlAttr(plan.name)}" class="primary">Add Purchases</button>` : ''}
          ${canManagePlan && !isUnassigned ? `<button data-action="edit-plan" data-id="${escapeHtmlAttr(plan.id)}">Edit</button>` : ''}
          <button data-action="view-history" data-id="${escapeHtmlAttr(plan.id)}" class="secondary">History</button>
          ${canDeletePlan ? `<button data-action="delete-plan" data-id="${escapeHtmlAttr(plan.id)}" class="danger">Delete</button>` : ''}
        </div>
      </div>
    </div>
  `;
}

function renderPlans(plans: LocalPlan[]): void {
  const container = document.getElementById('plans-list');
  if (!container) return;

  if (!plans || plans.length === 0) {
    container.innerHTML = '<p class="empty">No purchase plans configured. Create one to automate your commitment purchases.</p>';
    return;
  }

  // Issue #365: cache permission checks once per render rather than
  // per-card so a 100-plan list doesn't bounce through the helper
  // 600 times. Action buttons hidden for sessions that lack the verb.
  const canManagePlan = canAccess('update', 'plans');
  const canDeletePlan = canAccess('delete', 'plans');
  // Issue #1406 / #1418: "Add Purchases" requires BOTH plan-management AND
  // purchase-write permission. Plan Authors hold update:plans but not
  // update:purchases; Standard Users hold both. Gate the button on the
  // conjunction so Plan Authors cannot schedule purchases.
  const canAddPurchases = canManagePlan && canAccess('update', 'purchases');

  // Issue #973: split plans into assigned (have plan_accounts rows) and
  // unassigned (legacy plans with zero plan_accounts rows, flagged by the
  // backend). Unassigned plans are rendered under a separate read-only
  // section so operators can discover and re-scope them.
  const assignedPlans = plans.filter(p => !(p as unknown as BackendPlan).unassigned);
  const unassignedPlans = plans.filter(p => (p as unknown as BackendPlan).unassigned);

  const assignedHtml = assignedPlans.map(rawPlan =>
    renderPlanCard(rawPlan as unknown as BackendPlan, canManagePlan, canDeletePlan, canAddPurchases)
  ).join('');

  let unassignedHtml = '';
  if (unassignedPlans.length > 0) {
    const cards = unassignedPlans.map(rawPlan =>
      renderPlanCard(rawPlan as unknown as BackendPlan, canManagePlan, canDeletePlan, canAddPurchases)
    ).join('');
    unassignedHtml = `
      <div class="plans-section-header unassigned-plans-header">
        <h4>Unassigned</h4>
        <span class="plans-section-description">These legacy plans have no associated accounts and cannot be purchased against. Assign accounts or delete them.</span>
      </div>
      ${cards}
    `;
  }

  container.innerHTML = assignedHtml + unassignedHtml;

  // Asynchronously populate account names per assigned plan card.
  // Unassigned plans intentionally skip this: they have no plan_accounts
  // rows so the API call would return an empty list.
  container.querySelectorAll<HTMLElement>('.plan-card').forEach((card) => {
    const planId = card.querySelector<HTMLElement>('[data-id]')?.dataset['id'];
    // Find the matching plan to check unassigned status.
    const allPlans = [...assignedPlans, ...unassignedPlans];
    const matchedPlan = allPlans.find(p => (p as unknown as BackendPlan).id === planId) as unknown as BackendPlan | undefined;
    if (planId && matchedPlan && !matchedPlan.unassigned) {
      void loadPlanAccountNames(planId, card);
    }
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

// editPlan loads the plan and opens the edit modal pre-filled. Returns true
// when the modal opened, false when the plan could not be loaded (e.g. it was
// deleted or is no longer accessible). Callers that render the plan in a list
// use the false result to reconcile a now-stale row (issue #1403).
async function editPlan(planId: string): Promise<boolean> {
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

    // Get payment option from services and normalize for provider.
    // H-5: do not pre-select 'no-upfront' when the payment field is absent.
    // An absent payment field leaves the select empty so the user must
    // explicitly choose rather than silently inheriting a fabricated default
    // that could change the plan's payment type on re-save.
    const firstService = Object.values(backendPlan.services || {})[0];
    const rawPayment = firstService?.payment || null;
    const payment = rawPayment !== null ? normalizePaymentValue(rawPayment, info.provider ?? '') : '';

    const titleEl = document.getElementById('plan-modal-title');
    if (titleEl) titleEl.textContent = 'Edit Purchase Plan';

    (document.getElementById('plan-id') as HTMLInputElement).value = backendPlan.id;
    (document.getElementById('plan-name') as HTMLInputElement).value = backendPlan.name;
    (document.getElementById('plan-description') as HTMLTextAreaElement).value = '';

    // Set provider and service first. When provider is absent from the API
    // response, leave the select at its default empty/first option rather
    // than fabricating 'aws' (H-4: never default provider silently).
    const providerSelect = document.getElementById('plan-provider') as HTMLSelectElement;
    providerSelect.value = info.provider ?? '';
    (document.getElementById('plan-service') as HTMLSelectElement).value = info.service;

    // Update term/payment options based on provider/service
    const termSelect = document.getElementById('plan-term') as HTMLSelectElement;
    const paymentSelect = document.getElementById('plan-payment') as HTMLSelectElement;
    populateTermSelect(termSelect, info.provider ?? '', info.service);
    populatePaymentSelect(paymentSelect, info.provider ?? '', info.service);

    // Set term only when present; absent term leaves the select unset so
    // the user must explicitly choose rather than silently inheriting a
    // fabricated 3yr default (H-4).
    termSelect.value = info.term !== null ? String(info.term) : '';

    paymentSelect.value = payment;

    // Set coverage only when present; absent coverage leaves the input
    // blank so the user sees the field is missing (H-4).
    (document.getElementById('plan-coverage') as HTMLInputElement).value = info.coverage !== null ? String(info.coverage) : '';
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
    // Wire live range validation on all five numeric inputs (#702).
    wirePlanRangeInputs();
    const planModal = document.getElementById('plan-modal');
    if (planModal) openModal(planModal);
    return true;
  } catch (error) {
    console.error('Failed to load plan:', error);
    // A missing/inaccessible plan comes back as 404 (issue #1403): the
    // scheduled-purchase row outlived its plan (deleted, or the caller's
    // account scope changed). Surface an actionable message instead of the
    // generic "Failed to load plan details", and signal failure so the
    // caller can reconcile the stale row. Other errors keep the generic
    // message (network blip, transient 5xx) since the plan may still exist.
    const status = (error as { status?: number }).status;
    const message = status === 404
      ? 'This plan is no longer available. It may have been deleted.'
      : 'Failed to load plan details';
    showToast({ message, kind: 'error' });
    return false;
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

  // Parse and validate integer fields up front. Use Number() not parseInt so
  // fractions like "2.5" fail Number.isInteger() rather than silently truncating
  // to 2. Mirrors the strict parse pattern from handleAddPurchases and settings.ts
  // validatePurchasingSettings (feedback_strict_int_parse, finding 11-M1).
  const rawTerm = Number((document.getElementById('plan-term') as HTMLSelectElement).value);
  const rawCoverage = Number((document.getElementById('plan-coverage') as HTMLInputElement).value);
  const rawNotifyDays = Number((document.getElementById('plan-notify-days') as HTMLInputElement).value);

  if (!Number.isFinite(rawTerm) || !Number.isInteger(rawTerm) || rawTerm < 1) {
    showToast({ message: 'Term must be a valid whole number of years', kind: 'error' });
    return;
  }
  if (!Number.isFinite(rawCoverage) || !Number.isInteger(rawCoverage) || rawCoverage < 0 || rawCoverage > 100) {
    showToast({ message: 'Target Coverage must be a whole number between 0 and 100', kind: 'error' });
    return;
  }
  if (!Number.isFinite(rawNotifyDays) || !Number.isInteger(rawNotifyDays) || rawNotifyDays < 1 || rawNotifyDays > 30) {
    showToast({ message: 'Notification Days must be a whole number between 1 and 30', kind: 'error' });
    return;
  }

  const plan: SavePlanData = {
    name: (document.getElementById('plan-name') as HTMLInputElement).value,
    description: (document.getElementById('plan-description') as HTMLTextAreaElement).value,
    provider: (document.getElementById('plan-provider') as HTMLSelectElement).value,
    service: (document.getElementById('plan-service') as HTMLSelectElement).value,
    term: rawTerm,
    payment: (document.getElementById('plan-payment') as HTMLSelectElement).value,
    target_coverage: rawCoverage,
    ramp_schedule: rampSchedule,
    auto_purchase: (document.getElementById('plan-auto-purchase') as HTMLInputElement).checked,
    notification_days_before: rawNotifyDays,
    enabled: (document.getElementById('plan-enabled') as HTMLInputElement).checked
  };

  if (rampSchedule === 'custom') {
    const rawStepPercent = Number((document.getElementById('ramp-step-percent') as HTMLInputElement).value);
    const rawIntervalDays = Number((document.getElementById('ramp-interval-days') as HTMLInputElement).value);
    if (!Number.isFinite(rawStepPercent) || !Number.isInteger(rawStepPercent) || rawStepPercent < 1 || rawStepPercent > 100) {
      showToast({ message: 'Ramp Step Percent must be a whole number between 1 and 100', kind: 'error' });
      return;
    }
    if (!Number.isFinite(rawIntervalDays) || !Number.isInteger(rawIntervalDays) || rawIntervalDays < 1 || rawIntervalDays > 365) {
      showToast({ message: 'Ramp Interval Days must be a whole number between 1 and 365', kind: 'error' });
      return;
    }
    plan.custom_step_percent = rawStepPercent;
    plan.custom_interval_days = rawIntervalDays;
  }

  // Use the snapshot stamped at Plan-button click time (#273 CR follow-up).
  // Reading state.getVisibleRecommendations() / getSelectedRecommendation
  // IDs() here would re-derive the target at Save time — racing Refresh,
  // filter changes, and deselections that happen while the modal is open.
  // openCreatePlanModal(snapshot) freezes the Plan target the moment the
  // user clicked "Plan from N selected"; we read it back here.
  // openNewPlanModal() clears the snapshot for the New-Plan-from-scratch
  // path (no pre-resolved target — plan submits without `recommendations`).
  if (pendingPlanRecommendations.length > 0) {
    plan.recommendations = [...pendingPlanRecommendations];
  }

  // Universal-plans fix: read the selected account chips and reject submit
  // when the list is empty. The Save button is also disabled in the same
  // condition via refreshPlanSaveButtonState() so this branch is mostly a
  // belt-and-suspenders against scripted form submission; the toast keeps
  // the failure mode loud either way.
  const accountIdsField = document.getElementById('plan-account-ids') as HTMLInputElement | null;
  const accountIds = accountIdsField?.value ? accountIdsField.value.split(',').filter(Boolean) : [];
  if (accountIds.length === 0) {
    showToast({
      message: 'Target Accounts is required: pick at least one account before saving the plan.',
      kind: 'error',
    });
    return;
  }
  plan.target_accounts = accountIds;

  try {
    let savedPlanId = planId;
    if (planId) {
      await api.updatePlan(planId, plan as unknown as api.CreatePlanRequest);
    } else {
      const created = await api.createPlan(plan as unknown as api.CreatePlanRequest) as unknown as { id: string };
      savedPlanId = created.id;
    }

    // On update, the create path's atomic plan_accounts insert doesn't fire
    // (we only POST /plans on create). For updates we still need to push the
    // selected account list via the dedicated endpoint. On create, we also
    // re-push here so that subsequent reselection is reflected even if the
    // backend later opens an "atomic-create-only" path that diverges from
    // PUT semantics — same call already handles dedupe via DELETE+INSERT.
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

// refreshPlanSaveButtonState toggles the Save button's disabled state based
// on whether at least one Target Account is selected. Universal plans (rows
// in purchase_plans with no plan_accounts row) are no longer allowed by the
// API; surfacing the failure in the disabled state is friendlier than
// letting the user fill in every other field and get rejected at submit.
function refreshPlanSaveButtonState(): void {
  const form = document.getElementById('plan-form') as HTMLFormElement | null;
  if (!form) return;
  const submitBtn = form.querySelector<HTMLButtonElement>('button[type="submit"]');
  if (!submitBtn) return;
  const hasAccounts = planSelectedAccounts.length > 0;
  submitBtn.disabled = !hasAccounts;
  submitBtn.title = hasAccounts ? '' : 'Select at least one Target Account to save the plan';
}

/**
 * Close plan modal
 */
export function closePlanModal(): void {
  const planModal = document.getElementById('plan-modal');
  if (planModal) closeModal(planModal);
  // Invalidate the resolved-target snapshot stamped by openCreatePlanModal
  // so a subsequent flow doesn't accidentally inherit it. The snapshot
  // ties the plan to a specific button-click moment; once the modal
  // closes — by Save, Cancel, or any other path — that moment is over.
  pendingPlanRecommendations = [];
}

// Selected accounts for the plan modal
let planSelectedAccounts: Array<{ id: string; name: string; external_id: string }> = [];

// Monotonically incrementing counter scoped to the plan modal lifecycle.
// Incremented each time the create modal opens so that async callbacks
// from a previous session (stale promises) can detect they're out-of-date
// and discard their results rather than mutating state in the new session.
let planModalSession = 0;

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
  // Recalc Save-button disabled state every time the account list changes
  // so the user gets immediate feedback when they remove the last chip.
  refreshPlanSaveButtonState();
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
    const providerSelect = document.getElementById('plan-provider') as HTMLSelectElement | null;
    const provider = providerSelect?.value as api.Provider | undefined;
    // Minimal-disclosure list (view:recommendations) so plan-account search
    // works for Standard / Read-Only users; the full view:accounts list 403s
    // for them. See issues #949/#951.
    const accounts = await api.listAccountsMinimal({ search: value, ...(provider ? { provider } : {}) });
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

  const planProvider = (document.getElementById('plan-provider') as HTMLSelectElement | null)?.value;

  if (planId) {
    try {
      const existingAccounts = await api.listPlanAccounts(planId);
      // Filter out any account whose provider does not match the current plan
      // provider. This prevents stale cross-provider assignments from silently
      // surviving a provider switch on an existing plan.
      planSelectedAccounts = existingAccounts
        .filter(a => !planProvider || a.provider === planProvider)
        .map(a => ({ id: a.id, name: a.name, external_id: a.external_id }));
    } catch {
      // Non-critical — section just starts empty
    }
  }

  renderPlanAccountChips();
  updatePlanAccountIdsField();

  const searchInput = document.getElementById('plan-account-search') as HTMLInputElement | null;
  if (searchInput) {
    // Disable the search input until a provider is selected.
    searchInput.disabled = !planProvider;

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
 * Prefill the Purchase Configuration section (provider / service / term /
 * payment) from a representative selected commitment. Called after
 * form.reset() so the defaults are already in place; each field is still
 * editable. (#770)
 *
 * For a multi-commitment selection the caller passes the first commitment as
 * the representative: the "Plan from N selected" button is only enabled when
 * the selection is homogeneous (same provider/service/term/payment — see
 * isHomogeneousSelection in recommendations.ts), so any element shares those
 * four values. (#898)
 */
function prefillPurchaseConfigFromCommitment(rec: api.Recommendation): void {
  const providerSelect = document.getElementById('plan-provider') as HTMLSelectElement | null;
  const serviceSelect = document.getElementById('plan-service') as HTMLSelectElement | null;
  const termSelect = document.getElementById('plan-term') as HTMLSelectElement | null;
  const paymentSelect = document.getElementById('plan-payment') as HTMLSelectElement | null;

  if (!providerSelect || !serviceSelect || !termSelect || !paymentSelect) return;

  const provider = rec.provider ?? '';
  const service = rec.service ?? '';

  if (provider) providerSelect.value = provider;
  if (service) serviceSelect.value = service;

  // Repopulate term/payment options for the chosen provider+service, then
  // apply the commitment's own values so the dropdowns are consistent.
  if (provider && service) {
    populateTermSelect(termSelect, provider, service);
    populatePaymentSelect(paymentSelect, provider, service);
  }

  if (rec.term != null) termSelect.value = String(rec.term);
  const normalizedPayment = rec.payment ? normalizePaymentValue(rec.payment, provider) : '';
  if (normalizedPayment) paymentSelect.value = normalizedPayment;
}

/**
 * Fetch the account with the given internal UUID and add it as a pre-selected
 * chip in the plan modal accounts section. Runs after setupPlanAccountsSection
 * has reset the chip list for the create flow. Silently no-ops on failure so
 * the user can still pick the account manually. (#770)
 *
 * @param accountId - Internal UUID of the account to prefill.
 * @param session   - planModalSession value captured at call time. If the
 *                    modal is closed and reopened before this promise resolves,
 *                    the counter will have advanced and the stale result is
 *                    discarded to prevent wrong-modal pollution. (#770 CR)
 */
async function prefillAccountChipFromId(accountId: string, session: number): Promise<void> {
  try {
    // Resolve via the minimal-disclosure list (view:recommendations) instead of
    // GET /api/accounts/:id (view:accounts) so the target prefills for
    // Standard / Read-Only users too — the per-id endpoint 403s for them, which
    // previously left the target empty and made Save Plan silently no-op once
    // the empty-target guard rejected submit. See issues #949/#951.
    const account = (await api.listAccountsMinimal()).find((a) => a.id === accountId);
    // Session guard: discard the result if the modal was closed and reopened
    // while this promise was in-flight. planModalSession is incremented on
    // each new modal open, so a mismatch means this callback is stale.
    if (session !== planModalSession) return;
    // Guard: only add if the chip is not already present (e.g. a concurrent
    // edit flow somehow set it) and the account record is usable.
    if (account && account.id && !planSelectedAccounts.some(a => a.id === account.id)) {
      planSelectedAccounts.push({ id: account.id, name: account.name, external_id: account.external_id });
      renderPlanAccountChips();
      updatePlanAccountIdsField();
    }
  } catch {
    // Non-critical: the user can still search and add the account manually.
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
export function openCreatePlanModal(snapshot?: readonly api.Recommendation[]): void {
  // Stamp the resolved-target snapshot from the caller so savePlan can
  // consume it without re-deriving from global state at Save time. The
  // Bottom Action Box passes the result of resolvePurchaseTarget(); a
  // missing arg falls back to the legacy behaviour (no captured target,
  // savePlan submits a plan without `recommendations`). See #273 CR.
  pendingPlanRecommendations = snapshot ? [...snapshot] : [];

  const titleEl = document.getElementById('plan-modal-title');
  const hasSelection = pendingPlanRecommendations.length > 0;
  if (titleEl) {
    titleEl.textContent = hasSelection ? 'Create Purchase Plan' : 'New Purchase Plan';
  }
  (document.getElementById('plan-id') as HTMLInputElement).value = '';
  (document.getElementById('plan-form') as HTMLFormElement | null)?.reset();

  // When one or more commitments are selected, prefill the Purchase
  // Configuration fields so the user does not have to re-enter them. The
  // first commitment is the representative: for a multi-selection the
  // "Plan from N selected" button only enables on a homogeneous selection,
  // so every element shares provider/service/term/payment. Fields are still
  // fully editable after prefill. (#770, #898)
  if (pendingPlanRecommendations.length >= 1) {
    prefillPurchaseConfigFromCommitment(pendingPlanRecommendations[0]!);
  }

  // Set up ramp schedule change handlers for dynamic plan name
  setupRampScheduleHandlers();

  // Wire live range validation on all five numeric inputs (#702).
  wirePlanRangeInputs();

  // Generate initial plan name
  updatePlanNameFromSchedule();

  // Stamp a new session so any in-flight prefillAccountChipFromId promise
  // from a prior modal open can detect it belongs to a stale session and
  // discard its result without mutating planSelectedAccounts. (#770 CR)
  planModalSession += 1;

  // setupPlanAccountsSection clears planSelectedAccounts and re-renders.
  // When every selected commitment carries the SAME cloud_account_id, we look
  // up that account after the section has reset and add it as a pre-selected
  // chip. Homogeneity of provider/service/term/payment (enforced by the
  // "Plan from N selected" gate) does not imply a single account, so a
  // multi-account selection leaves the chip empty for the user to fill. (#898)
  void setupPlanAccountsSection();
  if (pendingPlanRecommendations.length >= 1) {
    const firstAccountId = pendingPlanRecommendations[0]!.cloud_account_id;
    const sharedAccountId =
      firstAccountId &&
      pendingPlanRecommendations.every((r) => r.cloud_account_id === firstAccountId)
        ? firstAccountId
        : undefined;
    if (sharedAccountId) {
      void prefillAccountChipFromId(sharedAccountId, planModalSession);
    }
  }

  const planModal = document.getElementById('plan-modal');
  if (planModal) {
    openModal(planModal);
  }
}

/**
 * Open new plan modal (without pre-selected recommendations)
 */
export function openNewPlanModal(): void {
  // No pre-resolved target — this is the "New Plan from scratch" path.
  // Clear any stale snapshot from a prior openCreatePlanModal call so a
  // subsequent savePlan doesn't accidentally inherit a previous flow's
  // recs. See #273 CR.
  pendingPlanRecommendations = [];

  const titleEl = document.getElementById('plan-modal-title');
  if (titleEl) titleEl.textContent = 'New Purchase Plan';
  (document.getElementById('plan-id') as HTMLInputElement).value = '';
  (document.getElementById('plan-form') as HTMLFormElement | null)?.reset();

  // Set up ramp schedule change handlers for dynamic plan name
  setupRampScheduleHandlers();

  // Wire live range validation on all five numeric inputs (#702).
  wirePlanRangeInputs();

  // Generate initial plan name
  updatePlanNameFromSchedule();

  void setupPlanAccountsSection();

  const planModal = document.getElementById('plan-modal');
  if (planModal) {
    openModal(planModal);
  }
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
 * Wire live range + integer-only validation on a plan numeric input.
 *
 * Registers `input` and `blur` event handlers that:
 *   - reject non-integer values via the regex `^\d+$` (blocks scientific
 *     notation such as `1e+30` and decimal fractions)
 *   - show a sibling `.field-error` span when the value is out of [min, max]
 *     or non-integer, and hide it when the value is valid or the field is empty
 *   - set / clear `aria-invalid` for screen-reader accessibility
 *
 * Idempotent: the `data-range-wired` attribute guards against re-registering
 * duplicate listeners when the modal is closed and reopened. The error span
 * is created once on the first call and reused thereafter. Explicit `min` /
 * `max` parameters are used instead of reading HTML attributes so callers are
 * the source of truth and the helper stays self-contained.
 */
function wireRangeInput(inputId: string, min: number, max: number): void {
  const input = document.getElementById(inputId) as HTMLInputElement | null;
  if (!input) return;

  // Idempotency guard: skip registration if already wired during a prior
  // modal open. The error span, aria-describedby, and listeners persist
  // across opens because the modal node stays in the DOM.
  if (input.dataset['rangeWired']) {
    // Re-trigger validation so stale error UI is reconciled on modal reopen.
    input.dispatchEvent(new Event('input'));
    return;
  }
  input.dataset['rangeWired'] = '1';

  const errorId = `${inputId}-range-error`;
  let errorEl = document.getElementById(errorId);
  if (!errorEl) {
    errorEl = document.createElement('small');
    errorEl.id = errorId;
    errorEl.className = 'field-error hidden';
    errorEl.setAttribute('role', 'status');
    errorEl.setAttribute('aria-live', 'polite');
    input.insertAdjacentElement('afterend', errorEl);
    const existing = input.getAttribute('aria-describedby');
    input.setAttribute(
      'aria-describedby',
      existing ? `${existing} ${errorId}` : errorId,
    );
  }
  const error = errorEl;
  const message = `Must be a whole number between ${min} and ${max}`;
  const integerPattern = /^\d+$/;

  const check = (): void => {
    const raw = input.value.trim();
    if (raw === '') {
      input.removeAttribute('aria-invalid');
      error.classList.add('hidden');
      return;
    }
    if (!integerPattern.test(raw)) {
      input.setAttribute('aria-invalid', 'true');
      error.textContent = message;
      error.classList.remove('hidden');
      return;
    }
    const parsed = parseInt(raw, 10);
    if (parsed < min || parsed > max) {
      input.setAttribute('aria-invalid', 'true');
      error.textContent = message;
      error.classList.remove('hidden');
    } else {
      input.removeAttribute('aria-invalid');
      error.classList.add('hidden');
    }
  };

  const clampOnBlur = (): void => {
    const raw = input.value.trim();
    if (!integerPattern.test(raw) || raw === '') return;
    const parsed = parseInt(raw, 10);
    if (parsed < min || parsed > max) {
      input.value = String(Math.min(max, Math.max(min, parsed)));
      input.removeAttribute('aria-invalid');
      error.classList.add('hidden');
    }
  };

  input.addEventListener('input', check);
  input.addEventListener('blur', clampOnBlur);
}

/**
 * Wire live range validation on all five plan-creation number inputs.
 * Called every time the plan modal opens so validation is active for both
 * the create and edit flows.
 */
function wirePlanRangeInputs(): void {
  wireRangeInput('plan-coverage', 0, 100);
  wireRangeInput('ramp-step-percent', 1, 100);
  wireRangeInput('ramp-interval-days', 1, 365);
  wireRangeInput('plan-notify-days', 1, 30);
}

/**
 * Set up event handlers for ramp schedule changes.
 * Guarded by rampHandlersInstalled so re-opening the plan modal does not
 * stack duplicate listeners on the static modal elements (H3, feedback_event_listener_dedup).
 */
function setupRampScheduleHandlers(): void {
  if (rampHandlersInstalled) return;
  rampHandlersInstalled = true;

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
    // Clear all selected accounts when the provider changes. Selected account
    // entries only carry id/name/external_id (no provider field), so we cannot
    // filter by provider directly. Clearing on change is the safe default:
    // an account valid for the old provider is almost never valid for the new
    // one, and it prevents a cross-provider assignment from silently reaching
    // the backend validator.
    planSelectedAccounts = [];
    renderPlanAccountChips();
    updatePlanAccountIdsField();
    // Clear and hide any open account suggestion dropdown so stale suggestions
    // from the previous provider cannot be clicked and add a mismatched account.
    const suggestions = document.getElementById('plan-account-suggestions') as HTMLElement | null;
    if (suggestions) {
      suggestions.textContent = '';
      suggestions.classList.add('hidden');
    }
    // Re-enable/disable the account search input to match the new provider state.
    const accountSearchInput = document.getElementById('plan-account-search') as HTMLInputElement | null;
    if (accountSearchInput) {
      accountSearchInput.value = '';
      accountSearchInput.disabled = !providerSelect.value;
    }
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
export async function openAddPurchasesModal(planId: string, planName: string): Promise<void> {
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
  startDateInput.min = new Date().toISOString().split('T')[0] ?? '';

  // Add event listeners
  document.getElementById('add-purchases-cancel')?.addEventListener('click', closeAddPurchasesModal);
  document.getElementById('add-purchases-form')?.addEventListener('submit', (e) => void handleAddPurchases(e));

  // Inline range validation on the count field: surfaces a
  // "Must be a whole number between 1 and 52" message under the input
  // as the user types, instead of waiting for the API call to reject
  // the value (mirrors the wireRangeInput pattern from #702/#714,
  // closes #771). The modal is built fresh on every open so no
  // data-range-wired guard is needed here.
  wireRangeInput('add-purchases-count', 1, 52);

  // Keep the submit button disabled while the count field is invalid.
  // Checked after every input/blur so the button reflects the live
  // validation state set by wireRangeInput above.
  const countInput = document.getElementById('add-purchases-count') as HTMLInputElement | null;
  const submitBtn = modal.querySelector<HTMLButtonElement>('button[type="submit"]');
  if (countInput && submitBtn) {
    const syncSubmitBtn = (): void => {
      submitBtn.disabled = countInput.getAttribute('aria-invalid') === 'true';
    };
    countInput.addEventListener('input', syncSubmitBtn);
    countInput.addEventListener('blur', syncSubmitBtn);
  }

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
    // Use Number() (not parseInt) so fractional input like "2.5" fails
    // Number.isInteger() instead of silently truncating to 2. Mirrors
    // the strict parse pattern from #471/#702 (feedback_strict_int_parse).
    const rawCount = Number((document.getElementById('add-purchases-count') as HTMLInputElement).value);
    if (!Number.isFinite(rawCount) || !Number.isInteger(rawCount) || rawCount < 1 || rawCount > 52) {
      if (errorDiv) {
        errorDiv.textContent = 'Number of Purchases must be a whole number between 1 and 52';
        errorDiv.classList.remove('hidden');
      }
      return;
    }
    const count = rawCount;
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

// Unsubscribe handles kept at module scope so setupPlanHandlers stays
// idempotent: calling it more than once (e.g. in tests or after HMR)
// replaces the old subscriptions rather than stacking duplicates.
let _unsubProvider: (() => void) | null = null;
let _unsubAccount: (() => void) | null = null;

/**
 * Setup plan form event handlers (provider-aware service dropdown).
 *
 * Provider/account filter source-of-truth is the global topbar (state.ts);
 * subscribe so the plans list re-renders when the user changes filters at
 * the page level.
 */
export function setupPlanHandlers(): void {
  // Drop any previous subscriptions before re-registering so we don't
  // accumulate duplicate loadPlans() calls on each invocation.
  _unsubProvider?.();
  _unsubAccount?.();
  _unsubProvider = state.subscribeProvider(() => void loadPlans());
  _unsubAccount = state.subscribeAccount(() => void loadPlans());

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

/**
 * Reset the ramp-handlers install-once guard.
 * Exported for unit tests only: each test rebuilds the DOM so the static
 * modal elements are fresh; without this reset the guard prevents listener
 * re-attachment and tests that open the modal multiple times break.
 * Must NOT be called in production code.
 */
export function _resetRampHandlersForTest(): void {
  rampHandlersInstalled = false;
}
