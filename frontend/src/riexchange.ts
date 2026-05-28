/**
 * RI Exchange module for CUDly
 * Manages convertible RI listing, reshape recommendations, and exchange operations
 */

import * as api from './api';
import * as state from './state';
import { formatDate, formatDateTime, escapeHtml, formatCurrency } from './utils';
import { switchTab, switchSettingsSubTab } from './navigation';
import { confirmDialog } from './confirmDialog';
import type {
  ConvertibleRI,
  RIUtilization,
  ReshapeRecommendation,
  ExchangeQuoteSummary,
  RIExchangeConfig,
  RIExchangeHistoryRecord,
  OfferingOption,
  TargetOffering,
  ReshapeRecommendationsResponse,
} from './api';
import { openModal, closeModal } from './modal';
import { showSkeletonRows, teardownSkeleton } from './lib/skeleton';
import { canAccess } from './permissions';
import { showToast } from './toast';
import { getCurrentUser } from './state';

// Module state
let currentRIs: ConvertibleRI[] = [];
let currentUtilization: Map<string, RIUtilization> = new Map();
let currentRecommendations: ReshapeRecommendation[] = [];

// Generation counter to prevent stale utilization data from overwriting fresh data
let utilizationGeneration = 0;

// Mode label mapping — single source of truth
const MODE_LABELS: Record<string, string> = { manual: "Manual Approval", auto: "Fully Automated" };
const MODE_VALUES: Record<string, string> = Object.fromEntries(
  Object.entries(MODE_LABELS).map(([k, v]) => [v, k])
);

// Suppress unused variable warning — MODE_VALUES is used in saveAutomationSettings
void MODE_VALUES;

/**
 * Load the RI Exchange tab — called when tab is activated
 */
export async function loadRIExchange(): Promise<void> {
  await Promise.all([
    loadConvertibleRIs(),
    loadReshapeRecommendations(),
    loadExchangeHistory(),
  ]);
}

/**
 * True when the RI Exchange sub-tab is the currently visible panel.
 * The sub-tab panel id is "inventory-ri-exchange" (see index.html).
 * Used by the provider/account change subscriptions below to avoid
 * unnecessary fetches while the user is on a different tab.
 */
function isRIExchangeSubtabActive(): boolean {
  const panel = document.getElementById('inventory-ri-exchange');
  return panel !== null && !panel.classList.contains('hidden');
}

/**
 * Setup RI Exchange event handlers.
 *
 * Wires the refresh button, the settings deep-link, and
 * provider/account state subscriptions so the convertible-RI list
 * and reshape recommendations reload when the operator switches the
 * global account filter (issue #186). An active-subtab guard
 * mirrors the Recommendations tab pattern to avoid redundant fetches
 * while the panel is off-screen.
 */
export function setupRIExchangeHandlers(): void {
  // Refresh button. Quote + execute flow lives in the per-row "Exchange"
  // button handlers, which open the exchange modal (openExchangeModal).
  const refreshBtn = document.getElementById('ri-exchange-refresh-btn');
  if (refreshBtn) {
    refreshBtn.addEventListener('click', () => void loadRIExchange());
  }

  const settingsBtn = document.getElementById('ri-exchange-settings-btn');
  if (settingsBtn) {
    settingsBtn.addEventListener('click', () => {
      switchTab('settings');
      switchSettingsSubTab('purchasing');
      const target = document.getElementById('ri-exchange-automation-settings');
      if (target) {
        requestAnimationFrame(() => {
          target.scrollIntoView({ behavior: 'smooth', block: 'start' });
        });
      }
    });
  }

  // issue #186: reload when the global provider/account filter changes
  // so the RI Exchange tables stay consistent with the rest of the UI.
  // Coalesce the two events into a single reload (provider change also
  // fires an account change via the topbar-filters.ts clearing logic).
  let reloadQueued = false;
  const scheduleReload = (): void => {
    if (!isRIExchangeSubtabActive() || reloadQueued) return;
    reloadQueued = true;
    queueMicrotask(() => {
      reloadQueued = false;
      if (isRIExchangeSubtabActive()) void loadRIExchange();
    });
  };
  state.subscribeProvider(scheduleReload);
  state.subscribeAccount(scheduleReload);
}

// ──────────────────────────────────────────────
// Convertible RIs table
// ──────────────────────────────────────────────

async function loadConvertibleRIs(): Promise<void> {
  const container = document.getElementById('ri-exchange-instances-list');
  if (!container) return;

  // Issue #344 T3: shimmer skeleton replaces the static "Loading…" text.
  // 3 rows × 8 cols matches the convertible-RI table's column shape
  // (RI ID / Instance Type / AZ / Count / Offering / Expiry /
  // Utilization / Actions — see renderRIsTable); renderRIsTable swaps
  // the children on success for a clean handoff.
  showSkeletonRows(container, 3, 8);

  try {
    currentRIs = await api.listConvertibleRIs();
    renderRIsTable(container);
    // Load utilization asynchronously (Cost Explorer is slow)
    utilizationGeneration++;
    void loadUtilization(utilizationGeneration);
  } catch (error) {
    teardownSkeleton(container);
    const err = error as Error;
    container.innerHTML = `<p class="error">Failed to load convertible RIs: ${escapeHtml(err.message)}</p>`;
  }
}

async function loadUtilization(generation: number): Promise<void> {
  try {
    const utilization = await api.getRIUtilization();
    // Discard if a newer load was started while we were waiting
    if (generation !== utilizationGeneration) return;
    currentUtilization = new Map(utilization.map(u => [u.reserved_instance_id, u]));
    // Re-render table with utilization data
    const container = document.getElementById('ri-exchange-instances-list');
    if (container) renderRIsTable(container);
  } catch (error) {
    console.error('Failed to load RI utilization:', error);
  }
}

function renderRIsTable(container: HTMLElement): void {
  if (!currentRIs || currentRIs.length === 0) {
    container.innerHTML = '<p class="empty">No active convertible Reserved Instances found.</p>';
    return;
  }

  // Issue #365: RI exchanges mutate cloud-provider RI state and are
  // admin-only by default on the backend. Hide the per-row Exchange
  // button for non-admin sessions so readonly users don't get a 403
  // on click. Defense in depth, backend still enforces.
  const canExchange = canAccess('admin', '*');

  container.innerHTML = `
    <table>
      <thead>
        <tr>
          <th>RI ID</th>
          <th>Instance Type</th>
          <th>AZ</th>
          <th>Count</th>
          <th>Offering</th>
          <th>Expiry</th>
          <th>Utilization</th>
          ${canExchange ? '<th>Actions</th>' : ''}
        </tr>
      </thead>
      <tbody>
        ${currentRIs.map(ri => {
          const util = currentUtilization.get(ri.reserved_instance_id);
          const utilPct = util ? util.utilization_percent : null;
          const utilClass = utilPct === null ? '' : utilPct >= 95 ? 'util-green' : utilPct >= 70 ? 'util-yellow' : 'util-red';
          const utilText = utilPct === null ? '<span class="loading-inline">...</span>' : `${utilPct.toFixed(1)}%`;
          return `
          <tr>
            <td class="monospace">${escapeHtml(ri.reserved_instance_id)}</td>
            <td>${escapeHtml(ri.instance_type)}</td>
            <td>${escapeHtml(ri.availability_zone)}</td>
            <td>${ri.instance_count}</td>
            <td>${escapeHtml(ri.offering_type)}</td>
            <td>${formatDate(ri.end)}</td>
            <td class="${utilClass}">${utilText}</td>
            ${canExchange ? `<td><button class="btn-small" data-action="quote-ri" data-ri-id="${escapeHtml(ri.reserved_instance_id)}" data-count="${ri.instance_count}" aria-label="Exchange ${escapeHtml(ri.reserved_instance_id)}">Exchange</button></td>` : ''}
          </tr>`;
        }).join('')}
      </tbody>
    </table>
  `;

  // Attach "Exchange" handlers for individual RIs
  container.querySelectorAll<HTMLButtonElement>('[data-action="quote-ri"]').forEach(btn => {
    btn.addEventListener('click', () => {
      const count = parseInt(btn.dataset['count'] || '1', 10);
      openExchangeModal(btn.dataset['riId'] || '', isNaN(count) ? 1 : count);
    });
  });
}

// ──────────────────────────────────────────────
// Reshape recommendations
// ──────────────────────────────────────────────

export async function loadReshapeRecommendations(): Promise<void> {
  const container = document.getElementById('ri-exchange-recommendations-list');
  if (!container) return;

  // Issue #344 T3: skeleton rows for the reshape-recommendations table.
  // 3 rows × 8 cols matches the rendered table shape — see
  // renderRecommendations: Source RI / Current / Suggested /
  // Alternatives / Utilization / Normalized Units / Reason / Actions.
  showSkeletonRows(container, 3, 8);

  try {
    const resp: ReshapeRecommendationsResponse = await api.getReshapeRecommendations();
    currentRecommendations = resp.recommendations ?? [];
    renderRecommendations(container);
    renderReshapeStalenessBanner(resp.recs_staleness, resp.recs_collected_at);
  } catch (error) {
    teardownSkeleton(container);
    const err = error as Error;
    container.innerHTML = `<p class="error">Failed to load recommendations: ${escapeHtml(err.message)}</p>`;
  }
}

/**
 * Render (or clear) the staleness banner above the reshape-recommendations
 * table. The banner slot is a sibling element with id
 * "ri-exchange-recommendations-freshness"; it is created here if absent.
 *
 * staleness: "" or undefined clears any existing banner (fresh data).
 * "soft"  : soft-warning copy ("data may be up to 12 h old").
 * "hard"  : hard-warning copy ("data is more than 24 h old").
 */
export function renderReshapeStalenessBanner(
  staleness: string | undefined,
  collectedAt: string | null | undefined,
): void {
  const BANNER_ID = 'ri-exchange-recommendations-freshness';
  // Locate or create the banner slot. It lives immediately before
  // ri-exchange-recommendations-list in the DOM.
  let banner = document.getElementById(BANNER_ID);
  if (!banner) {
    const listEl = document.getElementById('ri-exchange-recommendations-list');
    if (!listEl || !listEl.parentElement) return;
    banner = document.createElement('div');
    banner.id = BANNER_ID;
    listEl.parentElement.insertBefore(banner, listEl);
  }

  if (!staleness) {
    banner.textContent = '';
    banner.className = '';
    return;
  }

  // Build the age label from collectedAt when available.
  let ageLabel = '';
  if (collectedAt) {
    const ms = Date.now() - new Date(collectedAt).getTime();
    const hours = Math.floor(ms / (1000 * 60 * 60));
    const mins = Math.floor((ms % (1000 * 60 * 60)) / (1000 * 60));
    ageLabel = hours > 0 ? ` (last collected ${hours}h${mins > 0 ? ` ${mins}m` : ''} ago)` : ` (last collected ${mins}m ago)`;
  }

  const isSoft = staleness === 'soft';
  banner.className = isSoft ? 'freshness-banner warning' : 'freshness-banner error';

  const icon = isSoft ? '!' : '!!';
  const copy = isSoft
    ? `Cross-family alternatives are based on Cost Explorer recommendations that may be up to 24h old${ageLabel}. Some prices may be stale.`
    : `Cross-family alternatives are based on Cost Explorer recommendations older than 24h${ageLabel}. Prices may be significantly out of date.`;

  banner.textContent = '';
  const strong = document.createElement('strong');
  strong.textContent = icon + ' ';
  banner.appendChild(strong);
  banner.appendChild(document.createTextNode(copy));
}

function renderRecommendations(container: HTMLElement): void {
  if (!currentRecommendations || currentRecommendations.length === 0) {
    // The "well-utilized" copy is only truthful when the RI fleet actually
    // contains convertibles to be utilized. Prior to this commit the same
    // message fired regardless of fleet presence, so a brand-new tenant saw
    // a claim about utilization of a non-existent fleet.
    const copy = currentRIs.length === 0
      ? 'Reshape recommendations appear once your accounts have active convertible Reserved Instances — none are registered yet.'
      : `All ${currentRIs.length} convertible RI${currentRIs.length === 1 ? '' : 's'} meet your utilization threshold. No reshape needed.`;
    container.innerHTML = `<p class="empty">${escapeHtml(copy)}</p>`;
    return;
  }

  // Issue #365: same admin-only gate as the convertible-RI table.
  const canExchange = canAccess('admin', '*');

  container.innerHTML = `
    <table>
      <thead>
        <tr>
          <th>Source RI</th>
          <th>Current</th>
          <th>Suggested</th>
          <th>Alternatives</th>
          <th>Utilization</th>
          <th>Normalized Units</th>
          <th>Reason</th>
          ${canExchange ? '<th>Actions</th>' : ''}
        </tr>
      </thead>
      <tbody>
        ${currentRecommendations.map((rec, idx) => {
          const utilClass = rec.utilization_percent >= 95 ? 'util-green' : rec.utilization_percent >= 70 ? 'util-yellow' : 'util-red';
          const altCell = renderAlternativesCell(rec.alternative_targets);
          return `
          <tr>
            <td class="monospace">${escapeHtml(rec.source_ri_id)}</td>
            <td>${rec.source_count}x ${escapeHtml(rec.source_instance_type)}</td>
            <td>${rec.target_count}x ${escapeHtml(rec.target_instance_type)}</td>
            <td>${altCell}</td>
            <td class="${utilClass}">${rec.utilization_percent.toFixed(1)}%</td>
            <td>${rec.normalized_used.toFixed(1)} / ${rec.normalized_purchased.toFixed(1)}</td>
            <td>${escapeHtml(rec.reason)}</td>
            ${canExchange ? `<td>
              <button class="btn-small" data-action="fill-quote" data-index="${idx}">Exchange</button>
            </td>` : ''}
          </tr>`;
        }).join('')}
      </tbody>
    </table>
  `;

  // Attach "Exchange" handlers. Same as renderRIsTable: for non-admin
  // sessions the selector matches zero elements and this loop is a no-op.
  container.querySelectorAll<HTMLButtonElement>('[data-action="fill-quote"]').forEach(btn => {
    btn.addEventListener('click', () => {
      const idx = parseInt(btn.dataset['index'] || '0', 10);
      const rec = currentRecommendations[idx];
      if (rec) fillQuoteFromRecommendation(rec);
    });
  });
}

// renderAlternativesCell formats the cross-family alternative targets
// as a comma-separated list of "<instance_type> $X.XX/mo" chips inside
// a single table cell. Each instance_type is escapeHtml'd; cost values
// go through the shared formatCurrency helper (digits: 2) so there's
// no HTML injection vector through this helper.
function renderAlternativesCell(alternatives: OfferingOption[] | undefined): string {
  if (!alternatives || alternatives.length === 0) {
    return '—';
  }
  return alternatives
    .map((alt) => `<span class="cost-chip">${escapeHtml(alt.instance_type)} ${formatCurrency(alt.effective_monthly_cost, '$', 2)}/mo</span>`)
    .join(' ');
}

function fillQuoteFromRecommendation(rec: ReshapeRecommendation): void {
  openExchangeModal(rec.source_ri_id, rec.target_count, rec.target_instance_type, rec.alternative_targets);
}

export function fillQuoteFromRI(riId: string, count: number): void {
  openExchangeModal(riId, count);
}

// ──────────────────────────────────────────────
// RI Exchange Modal
// ──────────────────────────────────────────────

// TargetRow captures the DOM inputs for one target-offering entry in
// the multi-target modal. Tests assert on the posted request shape by
// finding inputs through their class attributes.
//
// offeringInput is a hidden <input type="hidden"> that holds the resolved
// AWS ReservedInstancesOfferingId UUID. The visible picker
// (modal-exchange-target-select) drives this field on change so the
// submission path always sees a UUID and never a free-text instance type.
interface TargetRow {
  offeringInput: HTMLInputElement;  // hidden; holds the UUID
  pickerSelect: HTMLSelectElement;  // visible; drives offeringInput
  countInput: HTMLInputElement;
  chipEl: HTMLSpanElement; // cost chip; shows "$X.XX/mo each" or "—".
  rowEl: HTMLDivElement;
}

export function openExchangeModal(riId: string, count: number, suggestedTargetType?: string, alternativeTargets?: OfferingOption[]): void {
  const modalEl = document.getElementById('ri-exchange-modal');
  if (!modalEl) return;
  const modal = modalEl; // non-null const for use in closures

  const content = modal.querySelector('.modal-content');
  if (!content) return;

  // Scoped state for this modal session. Stored against the modal
  // across quote/execute so execute can re-post the same shape the
  // user saw quoted.
  type QuoteReqShape = {
    ri_ids: string[];
    targets?: Array<{ offering_id: string; count: number }>;
    target_offering_id?: string;
    target_count?: number;
  };
  let modalQuote: ExchangeQuoteSummary | null = null;
  let modalQuoteReq: QuoteReqShape | null = null;

  // Build header
  const h3 = document.createElement('h3');
  h3.textContent = 'RI Exchange';
  content.textContent = '';
  content.appendChild(h3);

  // RI ID display
  const riRow = document.createElement('div');
  riRow.className = 'setting-row';
  const riLabel = document.createElement('label');
  riLabel.textContent = 'RI ID: ';
  const riSpan = document.createElement('span');
  riSpan.className = 'monospace';
  riSpan.textContent = riId;
  riLabel.appendChild(riSpan);
  riRow.appendChild(riLabel);
  content.appendChild(riRow);

  // Targets container: one or more rows, each with offering picker +
  // count. Users click "+ Add target" to split a source RI across
  // multiple target shapes in a single atomic AWS exchange.
  const targetsContainer = document.createElement('div');
  targetsContainer.id = 'modal-exchange-targets';
  targetsContainer.className = 'exchange-targets-container';
  content.appendChild(targetsContainer);

  const targetRows: TargetRow[] = [];

  // awsOfferings holds the list loaded from the target-offerings endpoint.
  // Starts empty; populateAwsOfferings() fills it once after the modal opens.
  let awsOfferings: TargetOffering[] = [];
  // offeringsLoaded tracks whether the async load has completed so new
  // rows added after completion are seeded with the already-loaded list.
  let offeringsLoaded = false;
  let offeringsError = false;

  // buildPickerOptions rebuilds the <select> options for all existing
  // rows. Called once after the AWS offerings are loaded, and for any
  // row added after that point.
  const buildPickerOptions = (
    select: HTMLSelectElement,
    initialOfferingId?: string,
  ): void => {
    // Remove all existing options to avoid duplicate listeners.
    select.innerHTML = '';

    const placeholder = document.createElement('option');
    placeholder.value = '';
    if (offeringsError) {
      placeholder.textContent = 'Could not load offerings -- type a UUID';
    } else if (!offeringsLoaded) {
      placeholder.textContent = 'Loading offerings...';
    } else {
      placeholder.textContent = 'Select a target offering';
    }
    select.appendChild(placeholder);

    // Group 1: AWS-driven target offerings from DescribeReservedInstancesOfferings
    if (awsOfferings.length > 0) {
      const grp = document.createElement('optgroup');
      grp.label = 'AWS Target Offerings';
      for (const o of awsOfferings) {
        const opt = document.createElement('option');
        opt.value = o.offering_id;
        // Display: "m5.large -- No Upfront" (no HTML injection: all fields from API)
        const label = escapeHtml(o.instance_type) + (o.offering_type ? ' -- ' + escapeHtml(o.offering_type) : '');
        opt.textContent = label;
        if (initialOfferingId && o.offering_id === initialOfferingId) {
          opt.selected = true;
        }
        grp.appendChild(opt);
      }
      select.appendChild(grp);
    }

    // Group 2: CE recommendations (alternativeTargets from reshape recs)
    if (alternativeTargets && alternativeTargets.length > 0) {
      const grp = document.createElement('optgroup');
      grp.label = 'CE Recommendations';
      for (const alt of alternativeTargets) {
        const opt = document.createElement('option');
        opt.value = alt.offering_id;
        const costStr = formatCurrency(alt.effective_monthly_cost, '$', 2);
        opt.textContent = escapeHtml(alt.instance_type) + ' -- ' + costStr + '/mo';
        if (initialOfferingId && alt.offering_id === initialOfferingId) {
          opt.selected = true;
        }
        grp.appendChild(opt);
      }
      select.appendChild(grp);
    }
  };

  // populateAwsOfferings loads target offerings from the backend and
  // refreshes all existing pickers.
  const populateAwsOfferings = async (): Promise<void> => {
    try {
      awsOfferings = await api.listTargetOfferings(riId);
      offeringsLoaded = true;
    } catch {
      offeringsLoaded = true;
      offeringsError = true;
    }
    // Refresh all existing row pickers with the loaded offerings.
    for (const row of targetRows) {
      buildPickerOptions(row.pickerSelect, row.offeringInput.value || undefined);
    }
  };

  const addTargetRow = (initialOfferingId?: string, initialCount?: number): void => {
    const rowIndex = targetRows.length;
    const rowEl = document.createElement('div');
    rowEl.className = 'setting-row exchange-target-row';
    rowEl.dataset.rowIndex = String(rowIndex);

    // Hidden input holds the resolved offering-id UUID. The picker
    // select drives this field; collectTargets reads from it. Tests can
    // set this directly to inject a UUID without going through the UI.
    const offeringInput = document.createElement('input');
    offeringInput.type = 'hidden';
    offeringInput.className = 'modal-exchange-target';
    if (initialOfferingId) offeringInput.value = initialOfferingId;
    rowEl.appendChild(offeringInput);

    // Visible picker: a <select> with two optgroups (AWS + CE recs).
    const pickerLabel = document.createElement('label');
    pickerLabel.textContent = 'Target offering: ';
    const pickerSelect = document.createElement('select');
    pickerSelect.className = 'modal-exchange-target-select';
    buildPickerOptions(pickerSelect, initialOfferingId);
    pickerLabel.appendChild(pickerSelect);
    rowEl.appendChild(pickerLabel);

    const countLabel = document.createElement('label');
    countLabel.textContent = 'Count: ';
    const countInput = document.createElement('input');
    countInput.type = 'number';
    countInput.min = '1';
    countInput.value = String(initialCount ?? 1);
    countInput.className = 'modal-exchange-count';
    countLabel.appendChild(countInput);
    rowEl.appendChild(countLabel);

    const removeBtn = document.createElement('button');
    removeBtn.type = 'button';
    removeBtn.className = 'btn exchange-remove-target';
    removeBtn.textContent = '×';
    removeBtn.setAttribute('aria-label', 'Remove target');
    removeBtn.addEventListener('click', () => {
      if (targetRows.length <= 1) return; // keep at least one row
      const idx = targetRows.findIndex((r) => r.rowEl === rowEl);
      if (idx >= 0) {
        targetRows.splice(idx, 1);
        rowEl.remove();
        updateRunningTotal();
      }
    });
    rowEl.appendChild(removeBtn);

    // Cost chip: shows per-instance monthly cost for the selected offering
    // when it appears in alternativeTargets; otherwise shows "—".
    const chipEl = document.createElement('span');
    chipEl.className = 'cost-chip';
    chipEl.textContent = '—';
    rowEl.appendChild(chipEl);

    // pickerSelect drives the hidden offeringInput and refreshes the chip.
    pickerSelect.addEventListener('change', () => {
      offeringInput.value = pickerSelect.value;
      updateRowChip(pickerSelect.value, chipEl);
      updateRunningTotal();
    });
    countInput.addEventListener('input', updateRunningTotal);

    targetsContainer.appendChild(rowEl);
    targetRows.push({ offeringInput, pickerSelect, countInput, chipEl, rowEl });
    // Initial chip population for pre-filled rows.
    updateRowChip(offeringInput.value, chipEl);
  };

  // lookupCECost returns the per-instance monthly cost for an offering_id
  // that appears in alternativeTargets, or undefined when absent.
  function lookupCECost(offeringId: string): number | undefined {
    if (!alternativeTargets || !offeringId) return undefined;
    const hit = alternativeTargets.find((a) => a.offering_id === offeringId);
    return hit?.effective_monthly_cost;
  }

  function updateRowChip(offeringId: string, chip: HTMLSpanElement): void {
    const cost = lookupCECost(offeringId);
    chip.textContent = cost !== undefined ? `${formatCurrency(cost, '$', 2)}/mo each` : '—';
  }

  // Seed the modal with one row, optionally pre-selecting by offering_id.
  // suggestedTargetType is an instance type (from reshape recs); we
  // match it against CE alternatives to find the offering_id to pre-select.
  const suggestedOfferingId = suggestedTargetType && alternativeTargets
    ? (alternativeTargets.find((a) => a.instance_type === suggestedTargetType)?.offering_id)
    : undefined;
  addTargetRow(suggestedOfferingId, count);

  // Kick off the async AWS offerings load after the first row exists.
  void populateAwsOfferings();

  const addTargetBtnRow = document.createElement('div');
  addTargetBtnRow.className = 'setting-row';
  const addTargetBtn = document.createElement('button');
  addTargetBtn.type = 'button';
  addTargetBtn.className = 'btn';
  addTargetBtn.id = 'modal-exchange-add-target';
  addTargetBtn.textContent = '+ Add target';
  addTargetBtn.addEventListener('click', () => {
    addTargetRow();
    updateRunningTotal();
  });
  addTargetBtnRow.appendChild(addTargetBtn);
  content.appendChild(addTargetBtnRow);

  // Running total line. Hidden when openExchangeModal is called without
  // alternativeTargets (Convertible RIs table path) since there's no
  // pricing to sum.
  const runningTotalEl = document.createElement('div');
  runningTotalEl.className = 'setting-row exchange-running-total';
  runningTotalEl.id = 'modal-exchange-running-total';
  if (!alternativeTargets || alternativeTargets.length === 0) {
    runningTotalEl.classList.add('hidden');
  }
  content.appendChild(runningTotalEl);

  function updateRunningTotal(): void {
    if (!alternativeTargets || alternativeTargets.length === 0) {
      return;
    }
    let total = 0;
    let anyMissing = false;
    for (const row of targetRows) {
      const cost = lookupCECost(row.offeringInput.value);
      const rawCount = parseInt(row.countInput.value, 10);
      const cnt = isNaN(rawCount) || rawCount < 1 ? 1 : rawCount;
      if (cost === undefined) {
        anyMissing = true;
        continue;
      }
      total += cost * cnt;
    }
    const suffix = anyMissing ? ' (incomplete -- some targets have no pricing data)' : '';
    runningTotalEl.textContent = `Estimated monthly cost for the quoted target set: ${formatCurrency(total, '$', 2)}/mo${suffix}`;
  }
  updateRunningTotal();

  // Result container
  const resultContainer = document.createElement('div');
  resultContainer.id = 'modal-exchange-result';
  content.appendChild(resultContainer);

  // Execute button (hidden until valid quote)
  const executeBtn = document.createElement('button');
  executeBtn.className = 'btn primary hidden';
  executeBtn.textContent = 'Execute Exchange';

  // Buttons row
  const btnRow = document.createElement('div');
  btnRow.className = 'modal-buttons';

  const quoteBtn = document.createElement('button');
  quoteBtn.className = 'btn primary';
  quoteBtn.textContent = 'Get Quote';

  const cancelBtn = document.createElement('button');
  cancelBtn.className = 'btn';
  cancelBtn.textContent = 'Cancel';

  btnRow.appendChild(quoteBtn);
  btnRow.appendChild(executeBtn);
  btnRow.appendChild(cancelBtn);
  content.appendChild(btnRow);

  // Show modal
  openModal(modal);

  cancelBtn.addEventListener('click', () => {
    closeModal(modal);
  });

  quoteBtn.addEventListener('click', () => {
    void submitModalQuote();
  });

  executeBtn.addEventListener('click', () => {
    void submitModalExecute();
  });

  // offeringUUIDPattern mirrors the backend regex for AWS offering UUIDs.
  const offeringUUIDPattern = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/;

  // collectTargets reads each row into a validated target. Rows with
  // empty or non-UUID offering IDs are treated as an error; the first
  // offending row's message is surfaced to the user. Count defaults to 1
  // when the input is empty or non-numeric.
  function collectTargets(): { targets: Array<{ offering_id: string; count: number }>; error?: string } {
    const targets: Array<{ offering_id: string; count: number }> = [];
    for (let i = 0; i < targetRows.length; i++) {
      const row = targetRows[i];
      if (!row) continue;
      const offeringId = row.offeringInput.value.trim();
      if (!offeringId) {
        return { targets: [], error: `Please select a target offering for target ${i + 1}.` };
      }
      if (!offeringUUIDPattern.test(offeringId)) {
        return {
          targets: [],
          error: `Target ${i + 1}: "${offeringId}" is not a valid offering UUID. Please select an offering from the dropdown.`,
        };
      }
      const rawCount = parseInt(row.countInput.value, 10);
      const targetCount = isNaN(rawCount) || rawCount < 1 ? 1 : rawCount;
      targets.push({ offering_id: offeringId, count: targetCount });
    }
    return { targets };
  }

  // buildQuoteReq shapes the request body. With one target we post
  // the singleton shape (target_offering_id + target_count) for
  // backward-compat with older backends and simpler CI harnesses.
  // With >1 target we post targets[] so the backend builds a
  // multi-element TargetConfigurations slice in the AWS Accept call.
  function buildQuoteReq(targets: Array<{ offering_id: string; count: number }>): QuoteReqShape {
    const first = targets[0];
    if (targets.length === 1 && first) {
      return {
        ri_ids: [riId],
        target_offering_id: first.offering_id,
        target_count: first.count,
      };
    }
    return { ri_ids: [riId], targets };
  }

  async function submitModalQuote(): Promise<void> {
    const { targets, error } = collectTargets();
    if (error) {
      setResultText(resultContainer, error, 'error');
      return;
    }

    setResultText(resultContainer, 'Getting exchange quote...', 'loading');
    executeBtn.classList.add('hidden');

    const quoteReq = buildQuoteReq(targets);
    try {
      modalQuote = await api.getExchangeQuote(quoteReq);
      modalQuoteReq = quoteReq;
      renderModalQuoteResult(resultContainer, modalQuote);
      if (modalQuote.IsValidExchange) executeBtn.classList.remove('hidden');
    } catch (error) {
      const err = error as Error;
      setResultText(resultContainer, 'Quote failed: ' + err.message, 'error');
    }
  }

  async function submitModalExecute(): Promise<void> {
    if (!modalQuote || !modalQuoteReq) return;

    setResultText(resultContainer, 'Executing exchange...', 'loading');
    executeBtn.disabled = true;

    try {
      const result = await api.executeExchange({
        ri_ids: modalQuoteReq.ri_ids,
        targets: modalQuoteReq.targets,
        target_offering_id: modalQuoteReq.target_offering_id,
        target_count: modalQuoteReq.target_count,
        max_payment_due_usd: modalQuote.PaymentDueRaw,
      });

      setResultText(resultContainer, 'Exchange completed. ID: ' + result.exchange_id, 'success-message');
      executeBtn.classList.add('hidden');
      modalQuote = null;
      modalQuoteReq = null;

      setTimeout(() => {
        closeModal(modal);
        void loadConvertibleRIs();
        void loadExchangeHistory();
      }, 2000);
    } catch (error) {
      const err = error as Error;
      setResultText(resultContainer, 'Exchange failed: ' + err.message, 'error');
      executeBtn.disabled = false;
    }
  }
}

function setResultText(container: HTMLElement, message: string, cls: string): void {
  container.textContent = '';
  const p = document.createElement('p');
  p.className = cls;
  p.textContent = message;
  container.appendChild(p);
}

function renderModalQuoteResult(container: HTMLElement, quote: ExchangeQuoteSummary): void {
  container.textContent = '';

  const div = document.createElement('div');
  div.className = 'quote-summary ' + (quote.IsValidExchange ? 'quote-valid' : 'quote-invalid');

  const h4 = document.createElement('h4');
  h4.textContent = quote.IsValidExchange ? 'Valid Exchange' : 'Invalid Exchange';
  div.appendChild(h4);

  if (quote.ValidationFailureReason) {
    const p = document.createElement('p');
    p.className = 'error';
    p.textContent = quote.ValidationFailureReason;
    div.appendChild(p);
  }

  const rows: [string, string][] = [
    ['Currency', quote.CurrencyCode],
    ['Payment Due', quote.PaymentDueRaw],
    ['Source Hourly Price', quote.SourceHourlyPriceRaw],
    ['Target Hourly Price', quote.TargetHourlyPriceRaw],
  ];
  if (quote.OutputReservedInstancesExp) {
    rows.push(['New RI Expiry', quote.OutputReservedInstancesExp]);
  }

  const details = document.createElement('div');
  details.className = 'quote-details';
  for (const [label, value] of rows) {
    const row = document.createElement('div');
    row.className = 'quote-row';
    const span = document.createElement('span');
    span.textContent = label + ':';
    const strong = document.createElement('strong');
    strong.textContent = value;
    row.appendChild(span);
    row.appendChild(strong);
    details.appendChild(row);
  }
  div.appendChild(details);
  container.appendChild(div);
}

// ──────────────────────────────────────────────
// Automation Settings
// ──────────────────────────────────────────────

export async function loadAutomationSettings(): Promise<void> {
  const container = document.getElementById('ri-exchange-automation-settings');
  if (!container) return;

  container.textContent = '';
  const loadingP = document.createElement('p');
  loadingP.className = 'loading';
  loadingP.textContent = 'Loading settings...';
  container.appendChild(loadingP);

  try {
    const config = await api.getRIExchangeConfig();
    renderAutomationSettings(container, config);
  } catch (error) {
    const err = error as Error;
    container.textContent = '';
    const errorP = document.createElement('p');
    errorP.className = 'error';
    errorP.textContent = 'Failed to load settings: ' + err.message;
    container.appendChild(errorP);
    const retryBtn = document.createElement('button');
    retryBtn.className = 'btn btn-small mt-2';
    retryBtn.textContent = 'Retry';
    retryBtn.addEventListener('click', () => { void loadAutomationSettings(); });
    container.appendChild(retryBtn);
  }
}

function buildAutoWarningBanner(): HTMLDivElement {
  const banner = document.createElement('div');
  banner.className = 'warning-message';
  const strong = document.createElement('strong');
  strong.textContent = 'Warning:';
  banner.appendChild(strong);
  banner.appendChild(document.createTextNode(' Fully Automated mode will execute RI exchanges without manual approval. Ensure spending caps are configured properly.'));
  return banner;
}

function renderAutomationSettings(container: HTMLElement, config: RIExchangeConfig): void {
  const modeOptions = Object.entries(MODE_LABELS)
    .map(([value, label]) => {
      const selected = value === config.mode ? ' selected' : '';
      return '<option value="' + escapeHtml(value) + '"' + selected + '>' + escapeHtml(label) + '</option>';
    })
    .join('');

  // Build the form via innerHTML since this is all developer-controlled markup
  // (all dynamic values are escaped via escapeHtml or are numbers)
  const formHTML = '<form id="ri-exchange-settings-form" class="settings-form">'
    + '<fieldset class="settings-category">'
    + '<legend>Exchange Automation</legend>'
    + '<div id="ri-exchange-warning-slot"></div>'
    + '<div class="setting-row">'
    +   '<div class="setting-info"><label for="ri-exchange-enabled">Enable Automated Exchange</label></div>'
    +   '<div class="setting-input"><label class="toggle-label">'
    +     '<input type="checkbox" id="ri-exchange-enabled"' + (config.auto_exchange_enabled ? ' checked' : '') + '>'
    +     '<span class="slider"></span>'
    +   '</label></div>'
    + '</div>'
    + '<div class="setting-row">'
    +   '<div class="setting-info"><label for="ri-exchange-mode">Mode</label></div>'
    +   '<div class="setting-input"><select id="ri-exchange-mode">' + modeOptions + '</select></div>'
    + '</div>'
    + '<div class="setting-row">'
    +   '<div class="setting-info"><label for="ri-exchange-threshold">Utilization Threshold (%)</label></div>'
    +   '<div class="setting-input"><input type="number" id="ri-exchange-threshold" value="' + config.utilization_threshold + '" min="0" max="100" step="0.1"></div>'
    + '</div>'
    + '<div class="setting-row">'
    +   '<div class="setting-info"><label for="ri-exchange-max-per-exchange">Max Payment Per Exchange (USD)</label></div>'
    +   '<div class="setting-input"><input type="number" id="ri-exchange-max-per-exchange" value="' + config.max_payment_per_exchange_usd + '" min="0" step="0.01"></div>'
    + '</div>'
    + '<div class="setting-row">'
    +   '<div class="setting-info"><label for="ri-exchange-max-daily">Max Payment Daily (USD)</label></div>'
    +   '<div class="setting-input"><input type="number" id="ri-exchange-max-daily" value="' + config.max_payment_daily_usd + '" min="0" step="0.01"></div>'
    + '</div>'
    + '<div class="setting-row">'
    +   '<div class="setting-info"><label for="ri-exchange-lookback">Lookback Days</label></div>'
    +   '<div class="setting-input"><input type="number" id="ri-exchange-lookback" value="' + config.lookback_days + '" min="1" max="365"></div>'
    + '</div>'
    + '</fieldset>'
    + '<div id="ri-exchange-settings-message" class="mt-3"></div>'
    + '</form>';

  container.textContent = '';
  const wrapper = document.createElement('div');
  wrapper.innerHTML = formHTML;
  while (wrapper.firstChild) {
    container.appendChild(wrapper.firstChild);
  }

  // Insert warning banner via DOM if mode is auto
  if (config.mode === 'auto') {
    const slot = document.getElementById('ri-exchange-warning-slot');
    if (slot) slot.appendChild(buildAutoWarningBanner());
  }

  // No per-form Save button: the Purchasing panel's sticky "Save Settings"
  // bar is the single source of truth and calls saveAutomationSettings()
  // alongside the global config save. We still stop any stray submit (e.g.
  // Enter inside a number field) so it doesn't navigate the page.
  const form = document.getElementById('ri-exchange-settings-form');
  if (form) {
    form.addEventListener('submit', (e) => {
      e.preventDefault();
    });
  }

  // Update warning banner when mode changes
  const modeSelect = document.getElementById('ri-exchange-mode') as HTMLSelectElement | null;
  if (modeSelect) {
    modeSelect.addEventListener('change', () => {
      const slot = document.getElementById('ri-exchange-warning-slot');
      if (!slot) return;
      if (modeSelect.value === 'auto') {
        if (!slot.firstChild) slot.appendChild(buildAutoWarningBanner());
      } else {
        slot.textContent = '';
      }
    });
  }
}

export async function saveAutomationSettings(): Promise<void> {
  const messageContainer = document.getElementById('ri-exchange-settings-message');
  if (!messageContainer) return;

  const enabledInput = document.getElementById('ri-exchange-enabled') as HTMLInputElement | null;
  const modeInput = document.getElementById('ri-exchange-mode') as HTMLSelectElement | null;
  const thresholdInput = document.getElementById('ri-exchange-threshold') as HTMLInputElement | null;
  const maxPerExchangeInput = document.getElementById('ri-exchange-max-per-exchange') as HTMLInputElement | null;
  const maxDailyInput = document.getElementById('ri-exchange-max-daily') as HTMLInputElement | null;
  const lookbackInput = document.getElementById('ri-exchange-lookback') as HTMLInputElement | null;

  if (!enabledInput || !modeInput || !thresholdInput || !maxPerExchangeInput || !maxDailyInput || !lookbackInput) return;

  const threshold = parseFloat(thresholdInput.value);
  const maxPerExchange = parseFloat(maxPerExchangeInput.value);
  const maxDaily = parseFloat(maxDailyInput.value);
  const lookback = parseInt(lookbackInput.value, 10);
  const mode = modeInput.value as RIExchangeConfig['mode'];

  // Client-side validation
  if (isNaN(threshold) || threshold < 0 || threshold > 100) {
    messageContainer.textContent = '';
    const p = document.createElement('p');
    p.className = 'error';
    p.textContent = 'Utilization threshold must be between 0 and 100.';
    messageContainer.appendChild(p);
    return;
  }
  if (isNaN(lookback) || lookback < 1 || lookback > 365) {
    messageContainer.textContent = '';
    const p = document.createElement('p');
    p.className = 'error';
    p.textContent = 'Lookback days must be between 1 and 365.';
    messageContainer.appendChild(p);
    return;
  }
  if (isNaN(maxPerExchange) || maxPerExchange < 0) {
    messageContainer.textContent = '';
    const p = document.createElement('p');
    p.className = 'error';
    p.textContent = 'Max payment per exchange must be >= 0.';
    messageContainer.appendChild(p);
    return;
  }
  if (isNaN(maxDaily) || maxDaily < 0) {
    messageContainer.textContent = '';
    const p = document.createElement('p');
    p.className = 'error';
    p.textContent = 'Max daily payment must be >= 0.';
    messageContainer.appendChild(p);
    return;
  }

  // Confirm auto mode — financial operations ship without further review
  // once enabled, so this gets the destructive-style confirm.
  if (mode === 'auto') {
    const confirmed = await confirmDialog({
      title: 'Enable Fully Automated mode?',
      body: 'RI exchanges will be executed automatically without manual approval. Make sure the payment caps below are set to values you are comfortable spending.',
      confirmLabel: 'Enable automation',
      destructive: true,
    });
    if (!confirmed) return;
  }

  const config: RIExchangeConfig = {
    auto_exchange_enabled: enabledInput.checked,
    mode,
    utilization_threshold: threshold,
    max_payment_per_exchange_usd: maxPerExchange,
    max_payment_daily_usd: maxDaily,
    lookback_days: lookback,
  };

  messageContainer.textContent = '';
  const loadingP = document.createElement('p');
  loadingP.className = 'loading';
  loadingP.textContent = 'Saving settings...';
  messageContainer.appendChild(loadingP);

  try {
    await api.updateRIExchangeConfig(config);
    messageContainer.textContent = '';
    const successP = document.createElement('p');
    successP.className = 'success-message';
    successP.textContent = 'Settings saved successfully.';
    messageContainer.appendChild(successP);
    setTimeout(() => {
      messageContainer.textContent = '';
    }, 3000);
  } catch (error) {
    const err = error as Error;
    messageContainer.textContent = '';
    const errorP = document.createElement('p');
    errorP.className = 'error';
    errorP.textContent = 'Failed to save settings: ' + err.message;
    messageContainer.appendChild(errorP);
  }
}

// ──────────────────────────────────────────────
// Exchange History
// ──────────────────────────────────────────────

// canApproveRIExchangeRow returns true when the current session may approve
// the given pending RI exchange via the inline Approve button (issue #300).
// UX gate only — the backend authorizeSessionApproveRIExchange remains the
// security boundary; a false-positive here surfaces as a 403 toast on click.
//
// Heuristic mirrors canApprovePendingRow in history.ts:
//   * status must be "pending";
//   * admin -> always yes;
//   * non-admin matching created_by_user_id -> yes (approve-own);
//   * legacy rows without created_by_user_id -> no (email-link path only).
function canApproveRIExchangeRow(rec: RIExchangeHistoryRecord): boolean {
  if ((rec.status || '').toLowerCase() !== 'pending') return false;
  const user = getCurrentUser();
  if (!user) return false;
  if (canAccess('admin', '*') || canAccess('approve-any', 'purchases')) return true;
  if (!rec.created_by_user_id) return false;
  return canAccess('approve-own', 'purchases') && rec.created_by_user_id === user.id;
}

async function loadExchangeHistory(): Promise<void> {
  const container = document.getElementById('ri-exchange-history-list');
  if (!container) return;

  container.textContent = '';
  const loadingP = document.createElement('p');
  loadingP.className = 'loading';
  loadingP.textContent = 'Loading exchange history...';
  container.appendChild(loadingP);

  try {
    const records = await api.getRIExchangeHistory();
    renderExchangeHistory(container, records);
  } catch (error) {
    const err = error as Error;
    container.textContent = '';
    const errorP = document.createElement('p');
    errorP.className = 'error';
    errorP.textContent = 'Failed to load exchange history: ' + err.message;
    container.appendChild(errorP);
  }
}

function getStatusBadgeClass(status: string): string {
  switch (status) {
    case 'completed': return 'status-badge completed';
    case 'pending': return 'status-badge pending';
    case 'processing': return 'status-badge running';
    case 'failed': return 'status-badge failed';
    case 'cancelled': return 'status-badge disabled';
    default: return 'status-badge';
  }
}

function renderExchangeHistory(container: HTMLElement, records: RIExchangeHistoryRecord[]): void {
  if (!records || records.length === 0) {
    container.textContent = '';
    const emptyP = document.createElement('p');
    emptyP.className = 'empty';
    emptyP.textContent = 'No exchange history found.';
    container.appendChild(emptyP);
    return;
  }

  // Build table rows — all dynamic values go through escapeHtml
  const rowsHTML = records.map(rec => {
    const exchangeIdCell = rec.exchange_id
      ? '<span class="monospace">' + escapeHtml(rec.exchange_id) + '</span>'
      : '&mdash;';
    const approveBtn = canApproveRIExchangeRow(rec)
      ? '<button type="button" class="btn-link riexchange-approve-btn" data-approve-id="' + escapeHtml(rec.id) + '">Approve</button>'
      : '';
    return '<tr>'
      + '<td>' + escapeHtml(formatDateTime(rec.created_at)) + '</td>'
      + '<td>' + escapeHtml(String(rec.source_count)) + 'x ' + escapeHtml(rec.source_instance_type) + '</td>'
      + '<td>' + escapeHtml(String(rec.target_count)) + 'x ' + escapeHtml(rec.target_instance_type) + '</td>'
      + '<td>' + escapeHtml(String(rec.source_count)) + '</td>'
      + '<td>$' + escapeHtml(rec.payment_due) + '</td>'
      + '<td><span class="' + getStatusBadgeClass(rec.status) + '">' + escapeHtml(rec.status) + '</span></td>'
      + '<td>' + exchangeIdCell + '</td>'
      + '<td>' + approveBtn + '</td>'
      + '</tr>';
  }).join('');

  const tableHTML = '<table>'
    + '<thead><tr>'
    + '<th>Date</th><th>Source Type</th><th>Target Type</th><th>Count</th><th>Payment</th><th>Status</th><th>Exchange ID</th><th>Actions</th>'
    + '</tr></thead>'
    + '<tbody>' + rowsHTML + '</tbody>'
    + '</table>';

  container.textContent = '';
  const wrapper = document.createElement('div');
  wrapper.innerHTML = tableHTML;
  while (wrapper.firstChild) {
    container.appendChild(wrapper.firstChild);
  }

  // Wire Approve button click handlers
  container.querySelectorAll<HTMLButtonElement>('.riexchange-approve-btn[data-approve-id]').forEach(btn => {
    btn.addEventListener('click', () => handleRIExchangeApproveClick(btn));
  });
}

async function handleRIExchangeApproveClick(btn: HTMLButtonElement): Promise<void> {
  const id = btn.dataset.approveId;
  if (!id) return;

  const confirmed = await confirmDialog({
    title: 'Approve RI Exchange',
    body: 'Approve this pending RI exchange? The exchange will execute immediately.',
    confirmLabel: 'Approve',
  });
  if (!confirmed) return;

  btn.disabled = true;
  try {
    await api.approveRIExchange(id);
    showToast({ kind: 'success', message: 'RI exchange approved and executing.' });
  } catch (err) {
    const msg = err instanceof Error ? err.message : String(err);
    showToast({ kind: 'error', message: 'Failed to approve exchange: ' + msg });
    btn.disabled = false;
    return;
  }
  try {
    await loadExchangeHistory();
  } catch (err) {
    const msg = err instanceof Error ? err.message : String(err);
    showToast({ kind: 'error', message: 'Approved, but failed to refresh history: ' + msg });
  }
}
