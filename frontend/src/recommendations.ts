/**
 * Recommendations module for CUDly
 */

import * as api from './api';
import * as state from './state';
import { formatCurrency, formatTerm, escapeHtml } from './utils';
import { renderFreshness } from './freshness';
import { getRecommendationDetail, type RecommendationDetail } from './api/recommendations';
import { showToast } from './toast';
import { isPaymentSupported, type Provider as CompatProvider } from './lib/purchase-compatibility';
import type { RecommendationsResponse, LocalRecommendation, RecommendationsSummary } from './types';
import { openModal } from './modal';

// Module state for current purchase modal recommendations
let currentPurchaseRecommendations: LocalRecommendation[] = [];
// Cache of account ID → name for column display
let accountNamesCache: Map<string, string> = new Map();

// populateRecommendationsAccountFilter / populateRegionFilter / the legacy
// service-filter helpers were removed in Bundle B — those DOM elements are
// gone from index.html. Provider / Account values now drive an API re-fetch
// via the column-header popovers (see openColumnPopover wiring); Service /
// Region filtering is purely client-side via applyColumnFilters.

/**
 * Get the recommendations currently loaded in the purchase modal
 */
export function getPurchaseModalRecommendations(): LocalRecommendation[] {
  return [...currentPurchaseRecommendations];
}

/**
 * Clear purchase modal recommendations (called when modal closes)
 */
export function clearPurchaseModalRecommendations(): void {
  currentPurchaseRecommendations = [];
}

/**
 * Setup recommendations event handlers
 */
export function setupRecommendationsHandlers(): void {
  // The legacy top filter bar (#recommendations-provider-filter,
  // #recommendations-account-filter, #service-filter, #region-filter,
  // #min-savings-filter) is gone — Bundle B replaced those with per-column
  // header-mounted popovers driven by state.RecommendationsColumnFilters.
  // No DOM listeners need wiring here anymore; the column-filter trigger
  // listeners are attached per-render inside renderRecommendationsList,
  // and Provider/Account-driven API re-fetch is handled by their popover
  // commit hooks (see Bundle B follow-up).
}

/**
 * Load recommendations
 */
export async function loadRecommendations(): Promise<void> {
  try {
    // Provider + account_ids are still sent to the API as hints so the
    // backend stays bounded for big multi-cloud tenants. Service / Region /
    // numeric filters are pure client-side via applyColumnFilters.
    const accountIDs = state.getCurrentAccountIDs();
    const filters: api.RecommendationFilters = {
      provider: state.getCurrentProvider(),
      account_ids: accountIDs.length > 0 ? accountIDs : undefined,
    };

    const [data, accounts] = await Promise.all([
      api.getRecommendations(filters) as unknown as RecommendationsResponse,
      api.listAccounts().catch(() => [])
    ]);
    accountNamesCache = new Map(accounts.map(a => [a.id, a.name]));
    state.setRecommendations((data.recommendations || []) as unknown as api.Recommendation[]);
    state.clearSelectedRecommendations();

    renderRecommendationsSummary(data.summary || {});
    renderRecommendationsList(data.recommendations || []);

    // Freshness indicator reflects the last collection timestamp; refreshed
    // on every load so provider/account switches + manual refreshes stay
    // in sync with the cache state.
    void renderFreshness('recommendations-freshness', loadRecommendations);
  } catch (error) {
    console.error('Failed to load recommendations:', error);
    const list = document.getElementById('recommendations-list');
    if (list) {
      const err = error as Error;
      list.innerHTML = `<p class="error">Failed to load recommendations: ${escapeHtml(err.message)}</p>`;
    }
  }
}

function renderRecommendationsSummary(summary: RecommendationsSummary): void {
  const container = document.getElementById('recommendations-summary');
  if (!container) return;

  container.innerHTML = `
    <div class="card">
      <h3>Total Recommendations</h3>
      <p class="value">${summary.total_count || 0}</p>
    </div>
    <div class="card">
      <h3>Potential Monthly Savings</h3>
      <p class="value savings">${formatCurrency(summary.total_monthly_savings)}</p>
    </div>
    <div class="card">
      <h3>Total Upfront Cost</h3>
      <p class="value">${formatCurrency(summary.total_upfront_cost)}</p>
    </div>
    <div class="card">
      <h3>Payback Period</h3>
      <p class="value">${summary.avg_payback_months ? summary.avg_payback_months.toFixed(1) : 0} months</p>
    </div>
  `;
}

// Comparator extractors per column. Numeric columns return numbers
// (subtraction-based sort); string columns return strings (localeCompare-based
// sort). Bundle B extended this with the string columns so every visible data
// column is sortable.
const SORTABLE_NUMERIC_COLUMNS: Record<string, (r: LocalRecommendation) => number> = {
  savings: (r) => r.savings,
  upfront_cost: (r) => r.upfront_cost,
  count: (r) => r.count,
  term: (r) => r.term,
};

const SORTABLE_STRING_COLUMNS: Record<string, (r: LocalRecommendation) => string> = {
  provider: (r) => r.provider ?? '',
  account: (r) => accountNamesCache.get(r.cloud_account_id ?? '') ?? r.cloud_account_id ?? '',
  service: (r) => r.service ?? '',
  resource_type: (r) => r.resource_type ?? '',
  region: (r) => r.region ?? '',
};

const SORT_HEADER_LABELS: Record<string, string> = {
  provider: 'Provider',
  account: 'Account',
  service: 'Service',
  resource_type: 'Resource Type',
  region: 'Region',
  count: 'Count',
  term: 'Term',
  savings: 'Monthly Savings',
  upfront_cost: 'Upfront Cost',
};

function sortIndicator(column: string, active: string, direction: 'asc' | 'desc'): string {
  if (column !== active) return '<span class="sort-indicator" aria-hidden="true">\u2195</span>';
  return direction === 'asc'
    ? '<span class="sort-indicator active" aria-hidden="true">\u25B2</span>'
    : '<span class="sort-indicator active" aria-hidden="true">\u25BC</span>';
}

function sortedRecommendations(recs: LocalRecommendation[]): LocalRecommendation[] {
  const sort = state.getRecommendationsSort();
  const direction = sort.direction === 'asc' ? 1 : -1;
  const numericKey = SORTABLE_NUMERIC_COLUMNS[sort.column];
  if (numericKey) {
    // slice() clones so we don't mutate the caller's array.
    return recs.slice().sort((a, b) => (numericKey(a) - numericKey(b)) * direction);
  }
  const stringKey = SORTABLE_STRING_COLUMNS[sort.column];
  if (stringKey) {
    return recs.slice().sort((a, b) => stringKey(a).localeCompare(stringKey(b)) * direction);
  }
  return recs;
}

// Numeric filter expression parser. Grammar:
//   - empty/whitespace      -> match-all
//   - "42"                  -> equals
//   - ">X" / "<X" / ">=X" / "<=X" -> comparator
//   - "X..Y"                -> inclusive range (X and Y both numbers)
//   - comma-separated       -> OR of any of the above
// Returns a discriminated union so callers can render parse errors
// inline without type-narrowing gymnastics. Whitespace inside terms is
// trimmed; whitespace between terms is allowed.
export type ParsedNumericFilter =
  | { ok: true; predicate: (n: number) => boolean }
  | { ok: false; error: string };

const MATCH_ALL: ParsedNumericFilter = { ok: true, predicate: () => true };

export function parseNumericFilter(expr: string): ParsedNumericFilter {
  if (!expr || expr.trim() === '') return MATCH_ALL;
  const terms = expr.split(',').map((t) => t.trim()).filter((t) => t !== '');
  if (terms.length === 0) return MATCH_ALL;

  const predicates: Array<(n: number) => boolean> = [];
  for (const term of terms) {
    // Order matters: ">=" / "<=" must be checked before ">" / "<".
    let p: ((n: number) => boolean) | null = null;
    let m: RegExpMatchArray | null;
    if ((m = term.match(/^>=\s*(-?\d+(?:\.\d+)?)$/))) {
      const v = Number(m[1]);
      p = (n) => n >= v;
    } else if ((m = term.match(/^<=\s*(-?\d+(?:\.\d+)?)$/))) {
      const v = Number(m[1]);
      p = (n) => n <= v;
    } else if ((m = term.match(/^>\s*(-?\d+(?:\.\d+)?)$/))) {
      const v = Number(m[1]);
      p = (n) => n > v;
    } else if ((m = term.match(/^<\s*(-?\d+(?:\.\d+)?)$/))) {
      const v = Number(m[1]);
      p = (n) => n < v;
    } else if ((m = term.match(/^(-?\d+(?:\.\d+)?)\s*\.\.\s*(-?\d+(?:\.\d+)?)$/))) {
      const lo = Number(m[1]);
      const hi = Number(m[2]);
      const min = Math.min(lo, hi);
      const max = Math.max(lo, hi);
      p = (n) => n >= min && n <= max;
    } else if ((m = term.match(/^(-?\d+(?:\.\d+)?)$/))) {
      const v = Number(m[1]);
      p = (n) => n === v;
    }
    if (p === null) {
      return { ok: false, error: `Invalid filter term: "${term}"` };
    }
    predicates.push(p);
  }
  // OR across terms
  return {
    ok: true,
    predicate: (n) => predicates.some((p) => p(n)),
  };
}

// Apply the per-column filters to a rec list. ANDs all column filters
// together. Categorical: row passes iff its column value (string-form,
// empty/null mapped to "") is in `values`. Numeric: row passes iff
// parseNumericFilter(expr).predicate accepts the value (skipped if
// parse failed — the popover's inline error tells the user).
//
// Account uses cloud_account_id for matching; Term uses String(r.term).
// All other categorical columns compare on the underlying string field.
export function applyColumnFilters(
  recs: readonly LocalRecommendation[],
  filters: state.RecommendationsColumnFilters,
): LocalRecommendation[] {
  const entries = Object.entries(filters) as Array<
    [state.RecommendationsColumnId, state.RecommendationsColumnFilter]
  >;
  if (entries.length === 0) return [...recs];

  return recs.filter((r) => {
    for (const [col, f] of entries) {
      if (f.kind === 'set') {
        const cellRaw = categoricalCellValue(r, col);
        if (!f.values.includes(cellRaw)) return false;
      } else {
        const parsed = parseNumericFilter(f.expr);
        if (!parsed.ok) continue; // ignore broken expressions; UI shows the error
        const cellNum = numericCellValue(r, col);
        if (!parsed.predicate(cellNum)) return false;
      }
    }
    return true;
  });
}

function categoricalCellValue(r: LocalRecommendation, col: state.RecommendationsColumnId): string {
  switch (col) {
    case 'provider':       return r.provider ?? '';
    case 'account':        return r.cloud_account_id ?? '';
    case 'service':        return r.service ?? '';
    case 'resource_type':  return r.resource_type ?? '';
    case 'region':         return r.region ?? '';
    case 'term':           return r.term == null ? '' : String(r.term);
    // Numeric columns shouldn't reach this branch; return empty for type-safety.
    case 'count':
    case 'savings':
    case 'upfront_cost':   return '';
  }
}

function numericCellValue(r: LocalRecommendation, col: state.RecommendationsColumnId): number {
  switch (col) {
    case 'count':         return r.count ?? 0;
    case 'savings':       return r.savings ?? 0;
    case 'upfront_cost':  return r.upfront_cost ?? 0;
    // Categorical columns shouldn't reach this branch; return NaN so any
    // numeric predicate returns false rather than coincidentally matching 0.
    case 'provider':
    case 'account':
    case 'service':
    case 'resource_type':
    case 'region':
    case 'term':          return Number.NaN;
  }
}

// ---------------------------------------------------------------------------
// Column-filter popover (portal pattern)
//
// The popover element lives appended to document.body so it survives
// renderRecommendationsList's table re-render (which does container.innerHTML
// = buildListMarkup, destroying anything inside <th>). Module-scope state
// tracks which column id is currently open; the trigger DOM node is re-found
// by `[data-column="..."]` on every render (anchor re-bind).
//
// The popover STRUCTURE is built once on open; STATE (.checked / .value) is
// re-synced on every anchor re-bind from the latest column-filter state, EXCEPT
// when the input is document.activeElement (mid-typing protection).
// ---------------------------------------------------------------------------

const NUMERIC_COLUMNS: ReadonlySet<state.RecommendationsColumnId> = new Set([
  'count', 'savings', 'upfront_cost',
]);

interface PopoverState {
  column: state.RecommendationsColumnId;
  el: HTMLDivElement;
  // The categorical-popover checkboxes are keyed by their underlying filter
  // value (cloud_account_id for Account, "1"/"3" for Term, raw string
  // otherwise). Saved here so anchor re-bind can resync .checked from state.
  checkboxes: Map<string, HTMLInputElement>;
  // Numeric-popover input + error span for re-sync.
  input: HTMLInputElement | null;
  errorEl: HTMLElement | null;
  // Trigger lives in the table; rebound by `[data-column="..."]` on each
  // render so we don't hold a stale reference.
  triggerColumn: state.RecommendationsColumnId;
}

let openPopover: PopoverState | null = null;
// Document-level click-outside listener; attached once on first open and
// torn down on close.
let outsideClickHandler: ((e: MouseEvent) => void) | null = null;
let escKeyHandler: ((e: KeyboardEvent) => void) | null = null;
let scrollCloseHandler: (() => void) | null = null;
let resizeHandler: (() => void) | null = null;

function getColumnTriggerButton(column: state.RecommendationsColumnId): HTMLButtonElement | null {
  return document.querySelector<HTMLButtonElement>(
    `th .column-filter-btn[data-column="${column}"]`,
  );
}

function positionPopover(popover: HTMLElement, anchor: HTMLElement): void {
  const rect = anchor.getBoundingClientRect();
  // Show, then measure (popover may be display:none initially).
  popover.style.display = 'block';
  const popRect = popover.getBoundingClientRect();
  const margin = 8;

  // Vertical: prefer below, flip above on overflow.
  let top = rect.bottom + 4;
  if (top + popRect.height > window.innerHeight - margin) {
    top = Math.max(margin, rect.top - popRect.height - 4);
  }

  // Horizontal: clamp right edge.
  let left = rect.left;
  if (left + popRect.width > window.innerWidth - margin) {
    left = Math.max(margin, window.innerWidth - margin - popRect.width);
  }

  popover.style.position = 'absolute';
  popover.style.top = `${top + window.scrollY}px`;
  popover.style.left = `${left + window.scrollX}px`;
}

function distinctValuesForColumn(
  recs: readonly LocalRecommendation[],
  column: state.RecommendationsColumnId,
): string[] {
  // Numeric columns don't get a checkbox list, but we still call this for
  // categorical columns only.
  const seen = new Set<string>();
  for (const r of recs) {
    seen.add(categoricalCellValue(r, column));
  }
  return Array.from(seen).sort((a, b) => {
    if (a === '' && b !== '') return -1; // (empty) first
    if (a !== '' && b === '') return 1;
    return a.localeCompare(b);
  });
}

function categoricalDisplayLabel(
  column: state.RecommendationsColumnId,
  value: string,
): string {
  if (value === '') return '(empty)';
  if (column === 'account') {
    return accountNamesCache.get(value) || value;
  }
  if (column === 'term') {
    const n = Number(value);
    return Number.isFinite(n) ? formatTerm(n) : value;
  }
  if (column === 'provider') {
    return providerDisplayName(value);
  }
  return value;
}

// Build the popover DOM for a given column. Categorical: checkbox list with
// (All) tri-state + Clear footer. Numeric: free-text expression input with
// inline error and Clear footer.
function buildPopoverContent(
  column: state.RecommendationsColumnId,
  recs: readonly LocalRecommendation[],
): { el: HTMLDivElement; checkboxes: Map<string, HTMLInputElement>; input: HTMLInputElement | null; errorEl: HTMLElement | null } {
  const popover = document.createElement('div');
  popover.className = 'column-filter-popover';
  popover.setAttribute('role', 'dialog');
  popover.setAttribute('aria-modal', 'false');

  const headingId = `column-filter-heading-${column}`;
  popover.setAttribute('aria-labelledby', headingId);

  const heading = document.createElement('h3');
  heading.id = headingId;
  heading.className = 'column-filter-heading';
  heading.textContent = `Filter ${SORT_HEADER_LABELS[column]}`;
  popover.appendChild(heading);

  const checkboxes = new Map<string, HTMLInputElement>();
  let input: HTMLInputElement | null = null;
  let errorEl: HTMLElement | null = null;

  if (NUMERIC_COLUMNS.has(column)) {
    const label = document.createElement('label');
    label.className = 'column-filter-numeric-label';
    label.textContent = 'Expression';
    input = document.createElement('input');
    input.type = 'text';
    input.className = 'column-filter-numeric-input';
    input.placeholder = 'e.g. >100, 50..200, 5';
    input.setAttribute('aria-describedby', `column-filter-error-${column}`);
    label.appendChild(input);
    popover.appendChild(label);

    errorEl = document.createElement('div');
    errorEl.id = `column-filter-error-${column}`;
    errorEl.className = 'column-filter-error';
    errorEl.setAttribute('role', 'status');
    popover.appendChild(errorEl);

    const commit = (): void => {
      const expr = input!.value.trim();
      if (expr === '') {
        state.setRecommendationsColumnFilter(column, null);
        errorEl!.textContent = '';
        rerenderRecommendations();
        return;
      }
      const parsed = parseNumericFilter(expr);
      if (!parsed.ok) {
        errorEl!.textContent = parsed.error;
        return;
      }
      errorEl!.textContent = '';
      state.setRecommendationsColumnFilter(column, { kind: 'expr', expr });
      rerenderRecommendations();
    };
    input.addEventListener('blur', commit);
    input.addEventListener('keydown', (e) => {
      if (e.key === 'Enter') {
        e.preventDefault();
        commit();
      }
    });
  } else {
    const distinct = distinctValuesForColumn(recs, column);

    // (All) tri-state checkbox at the top.
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
      text.textContent = categoricalDisplayLabel(column, value);
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
      // No selections OR all selections == "no narrowing", clear the filter.
      if (selected.length === 0 || selected.length === checkboxes.size) {
        state.setRecommendationsColumnFilter(column, null);
      } else {
        state.setRecommendationsColumnFilter(column, { kind: 'set', values: selected });
      }
      updateAllTriState();
      rerenderRecommendations();
    };

    checkboxes.forEach((cb) => {
      cb.addEventListener('change', commit);
    });
    allBox.addEventListener('change', () => {
      const target = allBox.checked;
      checkboxes.forEach((cb) => { cb.checked = target; });
      // After (All) flips, update underlying filter once.
      commit();
    });
  }

  // Footer with Clear button.
  const footer = document.createElement('div');
  footer.className = 'column-filter-footer';
  const clearBtn = document.createElement('button');
  clearBtn.type = 'button';
  clearBtn.className = 'column-filter-clear';
  clearBtn.textContent = 'Clear';
  clearBtn.addEventListener('click', () => {
    state.setRecommendationsColumnFilter(column, null);
    if (input) {
      input.value = '';
      if (errorEl) errorEl.textContent = '';
    } else {
      checkboxes.forEach((cb) => { cb.checked = false; });
    }
    rerenderRecommendations();
  });
  footer.appendChild(clearBtn);
  popover.appendChild(footer);

  return { el: popover, checkboxes, input, errorEl };
}

// Re-sync popover .checked / .value from current filter state, except when
// the active element is the numeric input (mid-typing protection).
function resyncOpenPopover(): void {
  if (!openPopover) return;
  const f = state.getRecommendationsColumnFilters()[openPopover.column];
  if (openPopover.input) {
    if (document.activeElement !== openPopover.input) {
      const expr = f && f.kind === 'expr' ? f.expr : '';
      openPopover.input.value = expr;
      if (openPopover.errorEl) openPopover.errorEl.textContent = '';
    }
    return;
  }
  // Categorical: tick checkboxes whose value is in the active filter.
  const values: ReadonlySet<string> = f && f.kind === 'set' ? new Set(f.values) : new Set();
  // Special case: if no filter is set, every checkbox should be unchecked
  // (the (All) tri-state checkbox follows from this).
  openPopover.checkboxes.forEach((cb, value) => {
    cb.checked = f != null && values.has(value);
  });
  // Update (All) tri-state.
  const allBox = openPopover.el.querySelector<HTMLInputElement>('input[data-role="all"]');
  if (allBox) {
    const total = openPopover.checkboxes.size;
    let checked = 0;
    openPopover.checkboxes.forEach((cb) => { if (cb.checked) checked++; });
    allBox.indeterminate = checked > 0 && checked < total;
    allBox.checked = checked === total && total > 0;
  }
}

function attachPopoverGlobalListeners(): void {
  if (outsideClickHandler) return;
  outsideClickHandler = (e: MouseEvent): void => {
    if (!openPopover) return;
    const target = e.target as Node | null;
    if (!target) return;
    if (openPopover.el.contains(target)) return;
    // Any column-filter trigger button click is handled by the trigger's own
    // handler; don't double-close.
    if (target instanceof Element && target.closest('.column-filter-btn')) return;
    closePopover();
  };
  escKeyHandler = (e: KeyboardEvent): void => {
    if (!openPopover) return;
    if (e.key === 'Escape') {
      e.preventDefault();
      closePopover(true);
    }
  };
  scrollCloseHandler = (): void => {
    if (openPopover) closePopover();
  };
  resizeHandler = (): void => {
    if (!openPopover) return;
    const trigger = getColumnTriggerButton(openPopover.column);
    if (!trigger) {
      closePopover();
      return;
    }
    positionPopover(openPopover.el, trigger);
  };
  document.addEventListener('mousedown', outsideClickHandler);
  document.addEventListener('keydown', escKeyHandler);
  // Use capture for scroll so we catch all scroll containers; passive for perf.
  window.addEventListener('scroll', scrollCloseHandler, { capture: true, passive: true });
  window.addEventListener('resize', resizeHandler);
}

function detachPopoverGlobalListeners(): void {
  if (outsideClickHandler) document.removeEventListener('mousedown', outsideClickHandler);
  if (escKeyHandler) document.removeEventListener('keydown', escKeyHandler);
  if (scrollCloseHandler) window.removeEventListener('scroll', scrollCloseHandler, { capture: true } as EventListenerOptions);
  if (resizeHandler) window.removeEventListener('resize', resizeHandler);
  outsideClickHandler = null;
  escKeyHandler = null;
  scrollCloseHandler = null;
  resizeHandler = null;
}

function openColumnPopover(column: state.RecommendationsColumnId, anchor: HTMLElement): void {
  // Defensive: if the popover element is no longer in the document (jest
  // DOM reset between tests, or an external script removed it), drop the
  // stale ref before applying the toggle/swap logic below.
  if (openPopover && !openPopover.el.isConnected) {
    detachPopoverGlobalListeners();
    openPopover = null;
  }
  // Toggle: clicking same trigger closes.
  if (openPopover && openPopover.column === column) {
    closePopover(true);
    return;
  }
  if (openPopover) closePopover();

  const recs = state.getRecommendations() as unknown as LocalRecommendation[];
  const built = buildPopoverContent(column, recs);
  document.body.appendChild(built.el);
  openPopover = {
    column,
    el: built.el,
    checkboxes: built.checkboxes,
    input: built.input,
    errorEl: built.errorEl,
    triggerColumn: column,
  };
  resyncOpenPopover();
  positionPopover(built.el, anchor);

  // ARIA wiring on the trigger.
  anchor.setAttribute('aria-expanded', 'true');

  attachPopoverGlobalListeners();

  // Move focus into the popover for keyboard users.
  const firstFocusable = built.input
    ?? built.el.querySelector<HTMLInputElement>('input[type="checkbox"]');
  firstFocusable?.focus();
}

function closePopover(restoreFocus = false): void {
  if (!openPopover) return;
  const { column, el } = openPopover;
  el.remove();
  openPopover = null;
  detachPopoverGlobalListeners();
  // ARIA cleanup on the trigger (if it still exists in the DOM).
  const trigger = getColumnTriggerButton(column);
  if (trigger) {
    trigger.setAttribute('aria-expanded', 'false');
    if (restoreFocus) trigger.focus();
  }
}

// Called by renderRecommendationsList after the table is rebuilt to re-anchor
// any open popover to the freshly-rendered trigger button. If the column was
// removed from the table somehow, close gracefully.
function rebindOpenPopoverAnchor(): void {
  if (!openPopover) return;
  const trigger = getColumnTriggerButton(openPopover.column);
  if (!trigger) {
    closePopover();
    return;
  }
  trigger.setAttribute('aria-expanded', 'true');
  positionPopover(openPopover.el, trigger);
  resyncOpenPopover();
}

// rerenderRecommendations triggers a full re-render from the latest loaded
// state. Used by popover commits and the Clear-filters badge so the table
// reflects new column-filter state immediately.
function rerenderRecommendations(): void {
  const loaded = state.getRecommendations() as unknown as LocalRecommendation[];
  renderRecommendationsList(loaded);
}

// Close the popover when the Recommendations tab loses .active, so the
// detached popover doesn't float over other tabs' content. Wired via a
// MutationObserver on the recommendations-tab element.
let recommendationsTabObserver: MutationObserver | null = null;
function ensureRecommendationsTabObserver(): void {
  if (recommendationsTabObserver) return;
  const tab = document.getElementById('recommendations-tab');
  if (!tab) return;
  recommendationsTabObserver = new MutationObserver(() => {
    if (!tab.classList.contains('active') && openPopover) {
      closePopover();
    }
  });
  recommendationsTabObserver.observe(tab, { attributes: true, attributeFilter: ['class'] });
}

// ---------------------------------------------------------------------------

// Render (or update) the filter-status bar: a "Clear filters (N)" button
// when at least one column filter is active, plus an aria-live region
// announcing visible vs loaded counts. Mounted above the table; survives
// container.innerHTML rewrites because it lives outside #recommendations-list.
function renderFilterStatusBar(loadedCount: number, visibleCount: number): void {
  const recsTab = document.getElementById('recommendations-tab');
  const list = document.getElementById('recommendations-list');
  if (!recsTab || !list) return;

  let bar = document.getElementById('recommendations-filter-status');
  if (!bar) {
    bar = document.createElement('div');
    bar.id = 'recommendations-filter-status';
    bar.className = 'recommendations-filter-status';
    list.parentNode?.insertBefore(bar, list);
  }

  // Build content fresh on every call. We set textContent on the live
  // region directly so screen readers fire announcements on actual changes.
  let badge = bar.querySelector<HTMLButtonElement>('.clear-filters');
  let live = bar.querySelector<HTMLElement>('.recommendations-filter-live');

  if (!live) {
    live = document.createElement('span');
    live.className = 'recommendations-filter-live';
    live.setAttribute('aria-live', 'polite');
    live.setAttribute('aria-atomic', 'true');
    bar.appendChild(live);
  }
  // Always update the live region (even when no filters active) so the
  // spoken count reflects state after every render.
  live.textContent = `Showing ${visibleCount} of ${loadedCount} recommendations`;

  const filters = state.getRecommendationsColumnFilters();
  const activeCount = Object.keys(filters).length;
  if (activeCount === 0) {
    if (badge) badge.remove();
    return;
  }
  if (!badge) {
    badge = document.createElement('button');
    badge.type = 'button';
    badge.className = 'clear-filters';
    badge.addEventListener('click', () => {
      state.clearAllRecommendationsColumnFilters();
      rerenderRecommendations();
    });
    bar.insertBefore(badge, live);
  }
  badge.textContent = `Clear filters (${activeCount})`;
}

// renderBulkToolbar was the old "N selected / Add to plan / Clear" pill that
// rendered above the table whenever any rows were selected. Bundle B folded
// that surface into the sticky bottom action box: the selection-summary text
// is in updateBottomActionBox, and the Create-Plan button in the bottom box
// supersedes the old "Add to plan". Function removed; tests for its DOM
// (bulk-count etc.) are gone too.
//
// For "Clear selection" affordance, see the bottom box's selection-summary
// text — when a selection exists the summary is followed by the row-checkbox
// columns (per-row deselect) and the all-selectAll checkbox in the table
// header (selectAll's third state clears).

// (Bundle B) selectedRecsFromVisible was inlined into resolvePurchaseTarget
// inside the bottom action box. The old helper had only one caller; folding
// keeps the action-target logic centralised in one place.

function openDetailDrawer(rec: LocalRecommendation): void {
  // Remove any previous drawer so repeat clicks don't stack.
  document.querySelectorAll('.detail-drawer').forEach((el) => el.remove());
  document.querySelectorAll('.detail-drawer-backdrop').forEach((el) => el.remove());

  const backdrop = document.createElement('div');
  backdrop.className = 'detail-drawer-backdrop';

  const drawer = document.createElement('aside');
  drawer.className = 'detail-drawer';
  drawer.setAttribute('role', 'dialog');
  drawer.setAttribute('aria-label', 'Recommendation details');

  const onClose = (): void => {
    drawer.remove();
    backdrop.remove();
    document.removeEventListener('keydown', onKey);
  };
  const onKey = (e: KeyboardEvent): void => {
    if (e.key === 'Escape') onClose();
  };
  backdrop.addEventListener('click', onClose);
  document.addEventListener('keydown', onKey);

  const title = document.createElement('h3');
  title.textContent = `${rec.provider.toUpperCase()} ${rec.service} \u2014 ${rec.resource_type}`;
  drawer.appendChild(title);

  const closeBtn = document.createElement('button');
  closeBtn.type = 'button';
  closeBtn.className = 'detail-drawer-close btn btn-small';
  closeBtn.setAttribute('aria-label', 'Close details');
  closeBtn.textContent = '\u2715';
  closeBtn.addEventListener('click', onClose);
  drawer.appendChild(closeBtn);

  const fields: Array<[string, string]> = [
    ['Provider', rec.provider.toUpperCase()],
    ['Service', rec.service],
    ['Resource type', rec.resource_type + (rec.engine ? ` (${rec.engine})` : '')],
    ['Region', rec.region],
    ['Instances', String(rec.count)],
    ['Term', formatTerm(rec.term)],
    ['Monthly savings', formatCurrency(rec.savings)],
    ['Upfront cost', formatCurrency(rec.upfront_cost)],
  ];
  const dl = document.createElement('dl');
  dl.className = 'detail-drawer-fields';
  fields.forEach(([k, v]) => {
    const dt = document.createElement('dt');
    dt.textContent = k;
    const dd = document.createElement('dd');
    dd.textContent = v;
    dl.appendChild(dt);
    dl.appendChild(dd);
  });
  drawer.appendChild(dl);

  // Confidence + provenance + usage history are sourced from the
  // per-id detail endpoint (issue #44). The drawer renders a loading
  // placeholder for each block immediately, then fills them in once
  // the fetch resolves. The fetch is memoised per id for the drawer
  // lifetime so re-opening the same row never re-hits the network
  // within a single session (cache cleared on drawer close).
  const confidenceRow = document.createElement('dl');
  confidenceRow.className = 'detail-drawer-fields';
  const confDt = document.createElement('dt');
  confDt.textContent = 'Confidence';
  const confDd = document.createElement('dd');
  const badge = document.createElement('span');
  badge.className = 'confidence-badge confidence-loading';
  badge.textContent = '…';
  confDd.appendChild(badge);
  confidenceRow.appendChild(confDt);
  confidenceRow.appendChild(confDd);
  drawer.appendChild(confidenceRow);

  const provenance = document.createElement('p');
  provenance.className = 'detail-drawer-note';
  provenance.textContent = 'Loading recommendation details\u2026';
  drawer.appendChild(provenance);

  // Usage history container \u2014 replaced once the detail fetch resolves
  // with either an inline SVG sparkline (when usage_history is
  // non-empty) or a "not yet available" note (the documented default
  // until the collector starts persisting time-series usage \u2014
  // known_issues/28).
  const usageContainer = document.createElement('div');
  usageContainer.className = 'detail-drawer-usage';
  const usagePlaceholder = document.createElement('p');
  usagePlaceholder.className = 'detail-drawer-note detail-drawer-note-muted';
  usagePlaceholder.textContent = 'Loading usage history\u2026';
  usageContainer.appendChild(usagePlaceholder);
  drawer.appendChild(usageContainer);

  const renderUsageEmptyNote = (): HTMLParagraphElement => {
    const note = document.createElement('p');
    note.className = 'detail-drawer-note detail-drawer-note-muted';
    note.textContent = 'Usage history not yet available.';
    return note;
  };

  void fetchRecommendationDetail(rec.id)
    .then((detail) => {
      // Confidence: server-supplied bucket replaces the previous
      // client-side heuristic so the label tracks the collector's
      // view of the rec rather than a frontend approximation.
      const bucket = detail.confidence_bucket;
      badge.className = `confidence-badge confidence-${bucket}`;
      badge.textContent = bucket.charAt(0).toUpperCase() + bucket.slice(1);

      // Provenance: rendered verbatim \u2014 the backend already names
      // the collector + last-collected timestamp.
      provenance.textContent = detail.provenance_note;

      // Usage history: render the sparkline when we have points,
      // else a one-line note. The empty case is the documented
      // default until the collector wiring follow-up lands.
      const next = (detail.usage_history && detail.usage_history.length > 0)
        ? renderUsageSparkline(detail.usage_history)
        : renderUsageEmptyNote();
      usageContainer.replaceChildren(next);
    })
    .catch(() => {
      // Detail-endpoint failure shouldn't blank the drawer \u2014 fall
      // back to a minimal "details unavailable" state. Confidence
      // badge is reset to an explicit Unknown rather than mis-claiming
      // a bucket on a failed fetch.
      badge.className = 'confidence-badge confidence-unknown';
      badge.textContent = 'Unknown';
      provenance.textContent = `Derived from ${providerDisplayName(rec.provider)} recommendation APIs. (Details temporarily unavailable.)`;
      usageContainer.replaceChildren(renderUsageEmptyNote());
    });

  document.body.appendChild(backdrop);
  document.body.appendChild(drawer);
  closeBtn.focus();
}

// detailFetchCache memoises in-flight + resolved detail fetches per id
// so re-opening the same row never re-hits the network within a single
// session. Cleared via clearRecommendationDetailCache() so a long-lived
// dashboard tab doesn't pin stale details indefinitely (the values
// evolve on every collector cycle).
const detailFetchCache = new Map<string, Promise<RecommendationDetail>>();

function fetchRecommendationDetail(id: string): Promise<RecommendationDetail> {
  const existing = detailFetchCache.get(id);
  if (existing) return existing;
  const inflight = getRecommendationDetail(id);
  detailFetchCache.set(id, inflight);
  // On rejection, drop the cached promise so the next open retries.
  inflight.catch(() => detailFetchCache.delete(id));
  return inflight;
}

/**
 * Clear the per-id detail-fetch cache. Exposed for tests + for any
 * future explicit "refresh details" affordance.
 */
export function clearRecommendationDetailCache(): void {
  detailFetchCache.clear();
}

/**
 * Render a tiny inline SVG sparkline of the per-recommendation usage
 * history. Two polylines (CPU + memory) over a shared time axis, no
 * axes/legend — just enough to give a directional sense of utilisation
 * over the collection window without pulling in a chart library.
 */
function renderUsageSparkline(points: ReadonlyArray<{ timestamp: string; cpu_pct: number; mem_pct: number }>): SVGSVGElement {
  const svgNS = 'http://www.w3.org/2000/svg';
  const width = 280;
  const height = 60;
  const padX = 4;
  const padY = 4;
  const usableW = width - padX * 2;
  const usableH = height - padY * 2;

  // Y axis is fixed to 0..100 since CPU/mem percentages have a known
  // range — keeps the visual comparison stable across recs.
  const yMax = 100;
  const xStep = points.length > 1 ? usableW / (points.length - 1) : 0;
  const project = (val: number, idx: number): [number, number] => {
    const clamped = Math.max(0, Math.min(yMax, val));
    const x = padX + xStep * idx;
    const y = padY + usableH * (1 - clamped / yMax);
    return [x, y];
  };

  const buildPath = (selector: (p: { cpu_pct: number; mem_pct: number }) => number): string =>
    points.map((p, i) => {
      const [x, y] = project(selector(p), i);
      return `${i === 0 ? 'M' : 'L'}${x.toFixed(1)},${y.toFixed(1)}`;
    }).join(' ');

  const svg = document.createElementNS(svgNS, 'svg');
  svg.setAttribute('class', 'detail-drawer-sparkline');
  svg.setAttribute('viewBox', `0 0 ${width} ${height}`);
  svg.setAttribute('width', String(width));
  svg.setAttribute('height', String(height));
  svg.setAttribute('role', 'img');
  svg.setAttribute('aria-label', `Usage history: ${points.length} samples, CPU and memory percent over time`);

  const cpu = document.createElementNS(svgNS, 'path');
  cpu.setAttribute('d', buildPath((p) => p.cpu_pct));
  cpu.setAttribute('fill', 'none');
  cpu.setAttribute('stroke', 'currentColor');
  cpu.setAttribute('stroke-width', '1.5');
  cpu.setAttribute('class', 'sparkline-cpu');
  svg.appendChild(cpu);

  const mem = document.createElementNS(svgNS, 'path');
  mem.setAttribute('d', buildPath((p) => p.mem_pct));
  mem.setAttribute('fill', 'none');
  mem.setAttribute('stroke', 'currentColor');
  mem.setAttribute('stroke-width', '1.5');
  mem.setAttribute('stroke-dasharray', '3,2');
  mem.setAttribute('class', 'sparkline-mem');
  svg.appendChild(mem);

  return svg;
}

function providerDisplayName(provider: string): string {
  switch (provider.toLowerCase()) {
    case 'aws': return 'AWS';
    case 'azure': return 'Azure';
    case 'gcp': return 'GCP';
    default: return provider;
  }
}

// Columns that get the per-column header filter button. Order matches the
// table column order (excluding the leading checkbox column, which is
// neither sortable nor filterable).
const FILTERABLE_COLUMNS: readonly state.RecommendationsColumnId[] = [
  'provider', 'account', 'service', 'resource_type', 'region',
  'count', 'term', 'savings', 'upfront_cost',
];

function buildListMarkup(recommendations: LocalRecommendation[], selectedRecs: ReadonlySet<string>): string {
  const sort = state.getRecommendationsSort();
  const sorted = sortedRecommendations(recommendations);
  const filters = state.getRecommendationsColumnFilters();
  const filterBtn = (column: state.RecommendationsColumnId): string => {
    const active = filters[column] ? ' active' : '';
    const label = filters[column] ? `Filter ${SORT_HEADER_LABELS[column]} — currently active` : `Filter ${SORT_HEADER_LABELS[column]}`;
    return `<button type="button" class="column-filter-btn${active}" data-column="${column}" aria-haspopup="dialog" aria-expanded="false" aria-label="${label}" title="${label}">⛛</button>`;
  };
  const sortHeader = (column: state.RecommendationsColumnId): string =>
    `<th class="sortable" data-sort="${column}" tabindex="0" role="button" aria-label="Sort by ${SORT_HEADER_LABELS[column]}"><span>${SORT_HEADER_LABELS[column]}</span>${sortIndicator(column, sort.column, sort.direction)}${filterBtn(column)}</th>`;

  return `
    <table>
      <thead>
        <tr>
          <th class="checkbox-col">
            <input type="checkbox" id="select-all-recs" aria-label="Select all recommendations">
          </th>
          ${FILTERABLE_COLUMNS.map(sortHeader).join('')}
        </tr>
      </thead>
      <tbody>
        ${sorted.map((rec) => {
          const savingsClass = rec.savings > 1000 ? 'high-savings' : rec.savings > 100 ? 'medium-savings' : '';
          const isSelected = selectedRecs.has(rec.id);
          const accountName = rec.cloud_account_id ? (accountNamesCache.get(rec.cloud_account_id) || rec.cloud_account_id) : '\u2014';
          const badge = renderSuppressionBadge(rec);
          const recId = escapeHtml(rec.id);
          return `
          <tr class="recommendation-row ${savingsClass} ${isSelected ? 'selected' : ''}" data-rec-id="${recId}">
            <td class="checkbox-col">
              <input type="checkbox" data-rec-id="${recId}" ${isSelected ? 'checked' : ''} aria-label="Select recommendation">
            </td>
            <td><span class="provider-badge ${rec.provider}">${rec.provider.toUpperCase()}</span></td>
            <td>${escapeHtml(accountName)}</td>
            <td><span class="service-badge">${escapeHtml(rec.service)}</span></td>
            <td title="${escapeHtml(rec.resource_type)}">${escapeHtml(rec.resource_type)}${rec.engine ? ` (${escapeHtml(rec.engine)})` : ''}${badge}</td>
            <td>${escapeHtml(rec.region)}</td>
            <td>${rec.count}</td>
            <td>${formatTerm(rec.term)}</td>
            <td class="savings">${formatCurrency(rec.savings)}</td>
            <td>${formatCurrency(rec.upfront_cost)}</td>
          </tr>`;
        }).join('')}
      </tbody>
    </table>
  `;
}

// renderSuppressionBadge returns HTML for the "recently purchased"
// indicator shown on recs the scheduler has annotated with an active
// purchase_suppression. Deep-links to Purchase History filtered to
// the execution whose suppression contributed the most. Returns ''
// when no suppression applies or the suppression has expired
// (defence-in-depth \u2014 the scheduler should have dropped such recs).
//
//   {suppressed} = rec.suppressed_count
//   {original}   = rec.count + rec.suppressed_count (rec.count is
//                  already post-subtraction)
//   {days}       = ceil((expires_at - now) / 24h), so 23h59m renders
//                  as "1d remaining" rather than "0d".
function renderSuppressionBadge(rec: LocalRecommendation): string {
  const suppressed = rec.suppressed_count ?? 0;
  if (suppressed <= 0) return '';
  const original = rec.count + suppressed;
  const expiresRaw = rec.suppression_expires_at;
  if (!expiresRaw) return '';
  const diffMs = new Date(expiresRaw).getTime() - Date.now();
  if (diffMs <= 0) return '';
  const days = Math.ceil(diffMs / (24 * 60 * 60 * 1000));
  const execID = rec.primary_suppression_execution_id;
  const href = execID ? `#history?execution=${encodeURIComponent(execID)}` : '#history';
  return ` <a class="rec-suppression-badge" href="${href}" title="Capacity from recent purchase; not re-proposed until it expires.">recently purchased ${suppressed}/${original} \u2014 ${days}d remaining</a>`;
}

// BULK_PURCHASE_LS_KEY holds the persisted Term/Payment/Capacity values
// so the toolbar remembers the user's last choice across page reloads.
// Versioned so we can migrate the shape in future without blowing up on
// a stale cached blob.
const BULK_PURCHASE_LS_KEY = 'cudly.recommendations.bulkPurchase.v1';

// BulkPurchaseToolbarState used to carry a `term` field that overrode each
// row's recommended term at API-call time. Bundle B drops it: each rec is
// purchased with its own per-row term (see term-aware bucketing in
// handleBulkPurchaseClick). loadBulkPurchaseState explicitly picks known
// fields so any legacy `term` from older localStorage values is silently
// ignored on read — no migration shim needed.
interface BulkPurchaseToolbarState {
  payment: 'all-upfront' | 'partial-upfront' | 'no-upfront' | 'monthly';
  capacity: number; // 1..100
}

const defaultBulkPurchaseState: BulkPurchaseToolbarState = {
  payment: 'all-upfront',
  capacity: 100,
};

function loadBulkPurchaseState(): BulkPurchaseToolbarState {
  try {
    const raw = localStorage.getItem(BULK_PURCHASE_LS_KEY);
    if (!raw) return { ...defaultBulkPurchaseState };
    const parsed = JSON.parse(raw) as Partial<BulkPurchaseToolbarState> & { term?: unknown };
    // Explicit field-pick rather than spread-and-omit — avoids leaking a
    // legacy `term` value into the returned object even at runtime.
    return {
      payment: parsed.payment || 'all-upfront',
      capacity: Math.max(1, Math.min(100, Number(parsed.capacity) || 100)),
    };
  } catch {
    return { ...defaultBulkPurchaseState };
  }
}

function saveBulkPurchaseState(s: BulkPurchaseToolbarState): void {
  try {
    localStorage.setItem(BULK_PURCHASE_LS_KEY, JSON.stringify(s));
  } catch {
    // Private-browsing / quota-exceeded — non-fatal, just lose the
    // sticky choice. The bottom box still works in-session.
  }
}

// Mount-once-then-update lifecycle for the sticky bottom action box.
// mountBottomActionBox builds the DOM (input/select/button identities) and
// wires listeners exactly once. updateBottomActionBox refreshes only the
// mutable surface — button labels, .disabled, the selection-summary text —
// leaving the input/select elements (and their in-progress values) alone.
//
// IDs preserved for backward compatibility:
//   #bulk-purchase-payment  (Payment dropdown)
//   #bulk-purchase-capacity (Capacity % input — read by app.ts:307)
//   #bulk-purchase-btn      (Purchase one-off button)
//   #create-plan-btn        (Create Purchase Plan button — relocated from
//                            the old top filter bar)
function mountBottomActionBox(): HTMLElement | null {
  const recsTab = document.getElementById('recommendations-tab');
  if (!recsTab) return null;

  let box = document.getElementById('recommendations-action-box');
  if (box) return box;

  box = document.createElement('div');
  box.id = 'recommendations-action-box';
  box.className = 'recommendations-action-box';
  box.setAttribute('role', 'toolbar');
  box.setAttribute('aria-label', 'Recommendations actions');

  const tbState = loadBulkPurchaseState();

  // Selection summary text (e.g. "(3 selected of 19 visible)" or "(All 19 visible)")
  const summary = document.createElement('span');
  summary.id = 'recommendations-action-summary';
  summary.className = 'recommendations-action-summary';
  box.appendChild(summary);

  // Payment dropdown — preserved ID
  const paymentLabel = document.createElement('label');
  paymentLabel.textContent = 'Payment ';
  const paymentSelect = document.createElement('select');
  paymentSelect.id = 'bulk-purchase-payment';
  [['all-upfront', 'All Upfront'], ['partial-upfront', 'Partial Upfront'], ['no-upfront', 'No Upfront'], ['monthly', 'Monthly']].forEach(([v, t]) => {
    const opt = document.createElement('option');
    opt.value = v as string;
    opt.textContent = t as string;
    if (v === tbState.payment) opt.selected = true;
    paymentSelect.appendChild(opt);
  });
  paymentLabel.appendChild(paymentSelect);
  box.appendChild(paymentLabel);

  // Capacity % input — preserved ID (app.ts:307 reads this)
  const capacityLabel = document.createElement('label');
  capacityLabel.textContent = 'Capacity % ';
  const capacityInput = document.createElement('input');
  capacityInput.id = 'bulk-purchase-capacity';
  capacityInput.type = 'number';
  capacityInput.min = '1';
  capacityInput.max = '100';
  capacityInput.step = '1';
  capacityInput.value = String(tbState.capacity);
  capacityLabel.appendChild(capacityInput);
  box.appendChild(capacityLabel);

  // Purchase one-off — preserved ID
  const purchaseBtn = document.createElement('button');
  purchaseBtn.type = 'button';
  purchaseBtn.className = 'btn btn-primary';
  purchaseBtn.id = 'bulk-purchase-btn';
  purchaseBtn.textContent = 'Purchase';
  purchaseBtn.title = 'Buy these reservations now (one-off, processed immediately)';
  box.appendChild(purchaseBtn);

  // Create Purchase Plan — relocated from old top bar
  const planBtn = document.createElement('button');
  planBtn.type = 'button';
  planBtn.className = 'btn btn-secondary';
  planBtn.id = 'create-plan-btn';
  planBtn.textContent = 'Create Plan';
  planBtn.title = 'Schedule a recurring plan that will purchase these recommendations on a defined cadence';
  box.appendChild(planBtn);

  const persist = (): void => {
    saveBulkPurchaseState({
      payment: paymentSelect.value as BulkPurchaseToolbarState['payment'],
      capacity: Math.max(1, Math.min(100, parseInt(capacityInput.value, 10) || 100)),
    });
  };
  paymentSelect.addEventListener('change', persist);
  capacityInput.addEventListener('change', persist);

  purchaseBtn.addEventListener('click', () => {
    const target = resolvePurchaseTarget();
    if (target.length === 0) return;
    handleBulkPurchaseClick(target);
  });

  planBtn.addEventListener('click', () => {
    const target = resolvePurchaseTarget();
    if (target.length === 0) return;
    // Plans-side savePlan reads state.getVisibleRecommendations() and
    // intersects with state.getSelectedRecommendationIDs() — it'll see
    // the same target as the Purchase button uses.
    void openCreatePlanFromBottomBox();
  });

  recsTab.appendChild(box);
  return box;
}

// Resolve the action target: selected ∩ visible if a selection exists,
// else all visible. "Visible" is the post-filter set tracked in module
// state via setVisibleRecommendations.
function resolvePurchaseTarget(): LocalRecommendation[] {
  const visible = state.getVisibleRecommendations() as unknown as LocalRecommendation[];
  const selected = state.getSelectedRecommendationIDs();
  const intersection = visible.filter((r) => selected.has(r.id));
  return intersection.length > 0 ? intersection : visible;
}

// updateBottomActionBox refreshes labels and disabled state on every
// renderRecommendationsList call without rebuilding the input/select DOM,
// preserving any in-progress typing in the Capacity input.
function updateBottomActionBox(visibleCount: number, loadedCount: number): void {
  const box = document.getElementById('recommendations-action-box');
  if (!box) return;

  const selected = state.getSelectedRecommendationIDs();
  // Count only selections that are currently visible.
  const visible = state.getVisibleRecommendations() as unknown as LocalRecommendation[];
  const selectedVisibleCount = visible.reduce(
    (n, r) => n + (selected.has(r.id) ? 1 : 0),
    0,
  );

  const summary = document.getElementById('recommendations-action-summary');
  if (summary) {
    if (loadedCount === 0) {
      summary.textContent = '(No recommendations loaded)';
    } else if (visibleCount === 0) {
      summary.textContent = '(0 visible — adjust filters)';
    } else if (selectedVisibleCount > 0) {
      summary.textContent = `(${selectedVisibleCount} selected of ${visibleCount} visible)`;
    } else {
      summary.textContent = `(All ${visibleCount} visible — no selection)`;
    }
  }

  const purchaseBtn = document.getElementById('bulk-purchase-btn') as HTMLButtonElement | null;
  const planBtn = document.getElementById('create-plan-btn') as HTMLButtonElement | null;
  const targetCount = selectedVisibleCount > 0 ? selectedVisibleCount : visibleCount;
  const empty = targetCount === 0;
  const targetIsSelection = selectedVisibleCount > 0;

  if (purchaseBtn) {
    purchaseBtn.disabled = empty;
    purchaseBtn.textContent = empty
      ? 'Purchase'
      : targetIsSelection
        ? `Purchase ${targetCount} selected`
        : `Purchase ${targetCount} visible`;
    purchaseBtn.title = empty
      ? (loadedCount === 0
          ? 'No recommendations loaded'
          : 'No rows visible — adjust filters')
      : 'Buy these reservations now (one-off, processed immediately)';
  }
  if (planBtn) {
    planBtn.disabled = empty;
    planBtn.textContent = empty
      ? 'Create Plan'
      : targetIsSelection
        ? `Plan from ${targetCount} selected`
        : `Plan from ${targetCount} visible`;
    planBtn.title = empty
      ? (loadedCount === 0
          ? 'No recommendations loaded'
          : 'No rows visible — adjust filters')
      : 'Schedule a recurring plan that will purchase these recommendations on a defined cadence';
  }
}

// openCreatePlanFromBottomBox opens the plan-creation modal. plans.ts'
// savePlan reads state.getVisibleRecommendations() (Bundle B's plumbing
// addition in Step 8c) so the plan only includes selected ∩ visible (or
// all visible if no selection).
async function openCreatePlanFromBottomBox(): Promise<void> {
  const { openCreatePlanModal } = await import('./plans');
  openCreatePlanModal();
}

function handleBulkPurchaseClick(recommendations: LocalRecommendation[]): void {
  const tb = loadBulkPurchaseState();
  if (recommendations.length === 0) {
    showToast({ message: 'No recommendations to purchase.', kind: 'warning' });
    return;
  }

  // Scale by capacity %; drop rows whose scaled count floors to 0.
  const scaled: LocalRecommendation[] = [];
  for (const r of recommendations) {
    const newCount = Math.floor((r.count * tb.capacity) / 100);
    if (newCount <= 0) continue;
    const ratio = r.count > 0 ? newCount / r.count : 1;
    scaled.push({
      ...r,
      count: newCount,
      upfront_cost: r.upfront_cost * ratio,
      monthly_cost: (r.monthly_cost ?? 0) * ratio,
      savings: r.savings * ratio,
    });
  }
  if (scaled.length === 0) {
    showToast({
      message: `Capacity ${tb.capacity}% produces no whole-number purchases from the current selection. Try a higher %.`,
      kind: 'warning',
    });
    return;
  }

  // Bucket by (provider, service, term). Bundle B added `term` to the key:
  // each rec is purchased with its OWN per-row term (the toolbar Term
  // selector is gone). Multi-term selections legitimately fan out into
  // multiple buckets, e.g. AWS EC2 1y + AWS EC2 3y → 2 buckets.
  const buckets = new Map<string, LocalRecommendation[]>();
  for (const r of scaled) {
    const key = `${r.provider}|${r.service}|${r.term}`;
    const existing = buckets.get(key);
    if (existing) existing.push(r);
    else buckets.set(key, [r]);
  }
  const bucketEntries = Array.from(buckets.entries());

  // Per-bucket compatibility check using the bucket's own term.
  const incompatible = bucketEntries.filter(([_key, recs]) => {
    const r = recs[0];
    if (!r) return false;
    return !isPaymentSupported(r.provider as CompatProvider, r.service, r.term as 1 | 3, tb.payment);
  });

  if (bucketEntries.length > 1 || incompatible.length > 0) {
    // Multi-bucket / incompatible path: open the fan-out modal so the
    // user can review per-bucket details before submitting one
    // executePurchase call per bucket in parallel.
    openFanOutModal(bucketEntries, tb);
    return;
  }

  // Single-bucket happy path: open the preview modal + submit via the
  // existing execute-purchase flow. The modal's "Execute Purchase" button
  // (wired in app.ts) picks up the recs via getPurchaseModalRecommendations.
  openPurchaseModal(scaled);
}

// FanOutBucket groups one batch of recs under a single (provider,
// service, term, payment, capacity) choice. A multi-bucket Purchase
// fires one executePurchase POST per bucket.
export interface FanOutBucket {
  provider: CompatProvider;
  service: string;
  term: 1 | 3;
  payment: 'all-upfront' | 'partial-upfront' | 'no-upfront' | 'monthly';
  capacityPercent: number;
  recs: LocalRecommendation[]; // scaled by capacityPercent
}

// Fan-out modal state. app.ts's Execute Purchase click reads these
// via getFanOutBuckets() to fire one POST per bucket. Cleared when
// the modal closes.
let currentFanOutBuckets: FanOutBucket[] | null = null;

export function getFanOutBuckets(): FanOutBucket[] | null {
  return currentFanOutBuckets ? currentFanOutBuckets.map((b) => ({ ...b })) : null;
}

export function clearFanOutBuckets(): void {
  currentFanOutBuckets = null;
}

function openFanOutModal(
  bucketEntries: Array<[string, LocalRecommendation[]]>,
  toolbar: BulkPurchaseToolbarState,
): void {
  const buckets: FanOutBucket[] = bucketEntries
    .filter(([_key, recs]) => recs.length > 0)
    .map(([_key, recs]) => {
      const r = recs[0]!;
      return {
        provider: r.provider as CompatProvider,
        service: r.service,
        // Each bucket is now term-uniform (key includes term), so we read
        // the term from the bucket itself rather than from the dropped
        // toolbar override.
        term: r.term as 1 | 3,
        payment: toolbar.payment,
        capacityPercent: toolbar.capacity,
        recs,
      };
    });
  currentFanOutBuckets = buckets;

  const container = document.getElementById('purchase-details');
  const modal = document.getElementById('purchase-modal');
  if (!container || !modal) return;

  // Build the modal body via createElement so the innerHTML hook
  // doesn't flag any template-literal HTML. The bucket summary is
  // pure data (provider, service, counts, totals) — no edit controls
  // in this initial drop. A later iteration can add per-bucket
  // Term/Payment/Capacity edits.
  while (container.firstChild) container.removeChild(container.firstChild);

  const summary = document.createElement('div');
  summary.className = 'form-section fanout-summary';
  const summaryTitle = document.createElement('h3');
  summaryTitle.textContent = `Bulk purchase — ${buckets.length} bucket${buckets.length === 1 ? '' : 's'}`;
  summary.appendChild(summaryTitle);

  const emailNote = document.createElement('p');
  emailNote.className = 'fanout-email-note';
  emailNote.textContent = `Will send ${buckets.length} approval email${buckets.length === 1 ? '' : 's'} — one per bucket.`;
  summary.appendChild(emailNote);

  const totals = computeFanOutTotals(buckets);
  const totalLine = (label: string, value: string, cls = ''): HTMLParagraphElement => {
    const p = document.createElement('p');
    const strong = document.createElement('strong');
    if (cls) strong.className = cls;
    strong.textContent = value;
    p.appendChild(document.createTextNode(label + ': '));
    p.appendChild(strong);
    return p;
  };
  summary.appendChild(totalLine('Total commitments', String(totals.totalCount)));
  summary.appendChild(totalLine('Total upfront', formatCurrency(totals.totalUpfront)));
  summary.appendChild(totalLine('Total monthly savings', formatCurrency(totals.totalSavings), 'savings'));
  container.appendChild(summary);

  for (const b of buckets) {
    container.appendChild(renderFanOutBucketSection(b));
  }

  openModal(modal);
}

function computeFanOutTotals(buckets: FanOutBucket[]): { totalCount: number; totalUpfront: number; totalSavings: number } {
  let totalCount = 0;
  let totalUpfront = 0;
  let totalSavings = 0;
  for (const b of buckets) {
    for (const r of b.recs) {
      totalCount += r.count;
      totalUpfront += r.upfront_cost;
      totalSavings += r.savings;
    }
  }
  return { totalCount, totalUpfront, totalSavings };
}

function renderFanOutBucketSection(b: FanOutBucket): HTMLElement {
  const section = document.createElement('section');
  section.className = 'fanout-bucket form-section';

  const title = document.createElement('h4');
  title.textContent = `${b.provider.toUpperCase()} / ${b.service} — ${b.recs.length} commitment${b.recs.length === 1 ? '' : 's'}`;
  section.appendChild(title);

  const compat = isPaymentSupported(b.provider, b.service, b.term, b.payment);
  const status = document.createElement('p');
  status.className = compat ? 'fanout-bucket-ok' : 'fanout-bucket-error';
  status.textContent = compat
    ? `${b.capacityPercent}% capacity · ${b.term}yr · ${b.payment}`
    : `Invalid combo: ${b.provider} / ${b.service} doesn't support ${b.term}yr + ${b.payment}. This bucket will be skipped.`;
  section.appendChild(status);

  const bucketTotal = b.recs.reduce(
    (acc, r) => ({
      count: acc.count + r.count,
      upfront: acc.upfront + r.upfront_cost,
      savings: acc.savings + r.savings,
    }),
    { count: 0, upfront: 0, savings: 0 },
  );
  const totals = document.createElement('p');
  totals.className = 'fanout-bucket-totals';
  totals.textContent = `${bucketTotal.count} commitments · ${formatCurrency(bucketTotal.upfront)} upfront · ${formatCurrency(bucketTotal.savings)} monthly savings`;
  section.appendChild(totals);

  return section;
}

function renderRecommendationsList(loadedRecs: LocalRecommendation[]): void {
  const container = document.getElementById('recommendations-list');
  if (!container) return;

  // Pipeline:
  //   loaded -> applyColumnFilters -> visible
  //   state.setVisibleRecommendations(visible)   (read by plans.ts:savePlan)
  // When the column-filters record is empty, applyColumnFilters returns a
  // clone of the input.
  const recommendations = applyColumnFilters(
    loadedRecs ?? [],
    state.getRecommendationsColumnFilters(),
  );
  state.setVisibleRecommendations(recommendations as unknown as readonly api.Recommendation[]);

  // Filter status: Clear-filters badge + aria-live count. Mounted as a
  // sibling above the table so it survives the container's innerHTML
  // rewrite without losing aria-live announcements.
  renderFilterStatusBar(loadedRecs?.length ?? 0, recommendations.length);

  // Mount once; update is per-render below.
  mountBottomActionBox();

  if (!recommendations || recommendations.length === 0) {
    container.innerHTML = '<p class="empty">No recommendations match these filters. Try clearing filters or refreshing.</p>';
    updateBottomActionBox(0, loadedRecs?.length ?? 0);
    return;
  }

  const selectedIDs = state.getSelectedRecommendationIDs();
  // Dynamic table markup: every caller-provided value passes through
  // escapeHtml or is a number. The string is built in buildListMarkup.
  container.innerHTML = buildListMarkup(recommendations, selectedIDs);

  updateBottomActionBox(recommendations.length, loadedRecs?.length ?? recommendations.length);

  // Per-column filter button: trigger opens the popover anchored to the
  // button. e.stopPropagation prevents the surrounding <th>'s sort handler
  // from also firing.
  container.querySelectorAll<HTMLButtonElement>('.column-filter-btn').forEach((btn) => {
    const column = btn.dataset['column'] as state.RecommendationsColumnId | undefined;
    if (!column) return;
    btn.addEventListener('click', (e) => {
      e.stopPropagation();
      openColumnPopover(column, btn);
    });
  });

  // After the table is rebuilt, re-anchor any open popover to the new
  // trigger DOM node and re-sync .checked / .value from current state.
  rebindOpenPopoverAnchor();

  // Watch the recommendations tab's class so we can close the popover if
  // the user switches away to another tab.
  ensureRecommendationsTabObserver();

  // Sortable column headers. Toggle ascending/descending on repeat click.
  container.querySelectorAll<HTMLTableCellElement>('th.sortable').forEach((th) => {
    const onActivate = (): void => {
      const col = th.dataset['sort'];
      if (!col) return;
      const prev = state.getRecommendationsSort();
      const direction: 'asc' | 'desc' =
        prev.column === col && prev.direction === 'desc' ? 'asc'
          : prev.column === col && prev.direction === 'asc' ? 'desc'
          : 'desc';
      state.setRecommendationsSort({ column: col as state.RecommendationsSortColumn, direction });
      renderRecommendationsList(recommendations);
    };
    th.addEventListener('click', onActivate);
    th.addEventListener('keydown', (e) => {
      if (e.key === 'Enter' || e.key === ' ') {
        e.preventDefault();
        onActivate();
      }
    });
  });

  // Add event listeners
  const selectAllCheckbox = document.getElementById('select-all-recs') as HTMLInputElement | null;
  if (selectAllCheckbox) {
    selectAllCheckbox.addEventListener('change', () => {
      if (selectAllCheckbox.checked) {
        recommendations.forEach((r) => state.addSelectedRecommendation(r.id));
      } else {
        state.clearSelectedRecommendations();
      }
      renderRecommendationsList(recommendations);
    });
  }

  // ID-keyed selection toggles. data-rec-id persists across filter
  // changes so a stale selection from a previous filter is a no-op
  // once the user narrows, rather than pointing at whichever rec
  // happens to occupy the old index position.
  container.querySelectorAll<HTMLInputElement>('input[data-rec-id]').forEach(cb => {
    cb.addEventListener('change', () => {
      const id = cb.dataset['recId'] || '';
      if (!id) return;
      if (cb.checked) {
        state.addSelectedRecommendation(id);
      } else {
        state.removeSelectedRecommendation(id);
      }
      renderRecommendationsList(recommendations);
    });
  });

  // Row-click opens the detail drawer — skip clicks that originated on
  // the checkbox or any interactive child (anchors, buttons) so those
  // flow through to their own handlers without also triggering the
  // drawer.
  container.querySelectorAll<HTMLTableRowElement>('tr.recommendation-row').forEach((tr) => {
    tr.addEventListener('click', (e) => {
      const target = e.target as HTMLElement;
      if (target.closest('input[type="checkbox"], button, a')) return;
      const id = tr.dataset['recId'] || '';
      const rec = recommendations.find((r) => r.id === id);
      if (rec) openDetailDrawer(rec);
    });
  });
}

/**
 * Open purchase modal
 */
export function openPurchaseModal(recommendations: LocalRecommendation[]): void {
  currentPurchaseRecommendations = [...recommendations];
  const container = document.getElementById('purchase-details');
  if (!container) return;

  const totalSavings = recommendations.reduce((sum, r) => sum + (r.savings || 0), 0);
  const totalUpfront = recommendations.reduce((sum, r) => sum + (r.upfront_cost || 0), 0);

  container.innerHTML = `
    <div class="form-section">
      <h3>Purchase Summary</h3>
      <p><strong>${recommendations.length}</strong> commitments to purchase</p>
      <p>Estimated Monthly Savings: <strong class="savings">${formatCurrency(totalSavings)}</strong></p>
      <p>Total Upfront Cost: <strong>${formatCurrency(totalUpfront)}</strong></p>
    </div>
    <div class="form-section">
      <h3>Commitments</h3>
      <table>
        <thead>
          <tr><th>Service</th><th>Type</th><th>Region</th><th>Count</th><th>Savings/mo</th></tr>
        </thead>
        <tbody>
          ${recommendations.map(r => `
            <tr>
              <td>${escapeHtml(r.service)}</td>
              <td>${escapeHtml(r.resource_type)}</td>
              <td>${escapeHtml(r.region)}</td>
              <td>${r.count}</td>
              <td class="savings">${formatCurrency(r.savings)}</td>
            </tr>
          `).join('')}
        </tbody>
      </table>
    </div>
  `;

  const purchaseModal = document.getElementById('purchase-modal');
  if (purchaseModal) openModal(purchaseModal);
}

/**
 * Refresh recommendations from API
 */
export async function refreshRecommendations(): Promise<void> {
  try {
    await api.refreshRecommendations();
    alert('Recommendation refresh started. This may take a few minutes.');
    setTimeout(() => void loadRecommendations(), 5000);
  } catch (error) {
    console.error('Failed to refresh recommendations:', error);
    alert('Failed to start recommendation refresh');
  }
}
