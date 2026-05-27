/**
 * Inventory & Coverage section (issue #340 T4, #754).
 *
 * Umbrella section that folds the former top-level "RI Exchange" tab into
 * a sub-section of a broader Inventory & Coverage view. Sub-sections:
 *   - active-commitments — per-commitment list backed by
 *                          /api/inventory/commitments
 *   - coverage           — per-provider coverage breakdowns backed by
 *                          /api/inventory/coverage (issue #754)
 *   - ri-exchange        — hosts the existing RI Exchange UI unchanged
 */

import * as api from './api';
import type { ProviderCoverageSection, CoverageServiceRow } from './api';
import { loadRIExchange } from './riexchange';
import { showSkeletonRows, teardownSkeleton } from './lib/skeleton';
import { formatCurrency, formatDate } from './utils';

type InventorySubSection = 'active-commitments' | 'coverage' | 'ri-exchange';

const SUB_SECTION_IDS: Record<InventorySubSection, string> = {
  'active-commitments': 'inventory-active-commitments',
  'coverage': 'inventory-coverage',
  'ri-exchange': 'inventory-ri-exchange',
};

const DEFAULT_SUB_SECTION: InventorySubSection = 'ri-exchange';

let currentSubSection: InventorySubSection | undefined;
let listenersWired = false;

function isValidSubSection(name: string): name is InventorySubSection {
  return name === 'active-commitments' || name === 'coverage' || name === 'ri-exchange';
}

/**
 * Show one sub-section, hide the others. Activates the matching sub-nav
 * button and (for ri-exchange) triggers the RI exchange data load so the
 * existing flow stays identical to its pre-#340 behaviour.
 */
export function switchInventorySubSection(name: string): void {
  const target: InventorySubSection = isValidSubSection(name) ? name : DEFAULT_SUB_SECTION;

  document.querySelectorAll<HTMLButtonElement>('#inventory-tab .sub-tab-btn').forEach((btn) => {
    const isActive = btn.dataset['invSubtab'] === target;
    btn.classList.toggle('active', isActive);
    btn.setAttribute('aria-selected', isActive ? 'true' : 'false');
  });

  for (const key of Object.keys(SUB_SECTION_IDS) as InventorySubSection[]) {
    const el = document.getElementById(SUB_SECTION_IDS[key]);
    if (el) el.classList.toggle('hidden', key !== target);
  }

  if (target === 'ri-exchange') {
    void loadRIExchange();
  } else if (target === 'active-commitments') {
    void loadActiveCommitments();
  } else if (target === 'coverage') {
    void loadCoverageBreakdown();
  }

  currentSubSection = target;
}

// ──────────────────────────────────────────────
// Active commitments
// ──────────────────────────────────────────────

const ACTIVE_COMMITMENTS_LIST_ID = 'active-commitments-list';
const ACTIVE_COMMITMENTS_REFRESH_BTN_ID = 'active-commitments-refresh-btn';
const ACTIVE_COMMITMENTS_COLS = 10;

/**
 * Fetch and render the active-commitments table. Replaces #active-commitments-list
 * children with a shimmer skeleton on entry, then either the rendered
 * table or an empty-state / error paragraph on completion. Idempotent —
 * safe to call on every sub-tab switch and on every refresh click.
 */
export async function loadActiveCommitments(): Promise<void> {
  const container = document.getElementById(ACTIVE_COMMITMENTS_LIST_ID);
  if (!container) return;

  wireRefreshButton();

  // 5 rows × 10 cols matches the rendered table shape (see
  // renderActiveCommitmentsTable). The renderer wipes the container's
  // children for a clean handoff from the skeleton.
  showSkeletonRows(container, 5, ACTIVE_COMMITMENTS_COLS);

  try {
    const commitments = await api.listActiveCommitments();
    renderActiveCommitmentsTable(container, commitments);
  } catch (error) {
    teardownSkeleton(container);
    const err = error as Error;
    renderErrorParagraph(container, `Failed to load active commitments: ${err.message}`);
  }
}

function wireRefreshButton(): void {
  // Idempotency is tracked on the element itself rather than a
  // module-level flag — when the section is re-rendered (e.g. between
  // tests, or after a hot-swap in dev), the new button is unwired and
  // a stale flag would block the rebind. The dataset marker travels
  // with the element so it can't drift out of sync.
  const btn = document.getElementById(ACTIVE_COMMITMENTS_REFRESH_BTN_ID);
  if (!btn) return;
  if (btn.dataset['wired'] === '1') return;
  btn.addEventListener('click', () => {
    void loadActiveCommitments();
  });
  btn.dataset['wired'] = '1';
}

function clearChildren(el: HTMLElement): void {
  while (el.firstChild) el.removeChild(el.firstChild);
}

function renderErrorParagraph(container: HTMLElement, message: string): void {
  clearChildren(container);
  const p = document.createElement('p');
  p.className = 'error';
  p.textContent = message;
  container.appendChild(p);
}

function renderEmptyParagraph(container: HTMLElement, message: string): void {
  clearChildren(container);
  const p = document.createElement('p');
  p.className = 'empty';
  p.textContent = message;
  container.appendChild(p);
}

/**
 * Render the per-commitment table into `container`. Empty list yields
 * an inline `.empty` paragraph instead of an empty table so the user
 * gets a real message ("no active commitments"), not a blank header.
 *
 * All text uses textContent / DOM construction — no innerHTML — to
 * keep the section safe by default against any unescaped backend
 * field (issue #340 XSS posture).
 */
function renderActiveCommitmentsTable(container: HTMLElement, commitments: api.InventoryCommitment[]): void {
  if (!commitments || commitments.length === 0) {
    renderEmptyParagraph(container, 'No active commitments found across your registered accounts.');
    return;
  }

  clearChildren(container);
  const table = document.createElement('table');

  const thead = document.createElement('thead');
  const headerRow = document.createElement('tr');
  const headers = ['Provider', 'Account', 'Service', 'Resource type', 'Region', 'Count', 'Term', 'Payment', 'Monthly cost', 'Expires'];
  for (const label of headers) {
    const th = document.createElement('th');
    th.textContent = label;
    headerRow.appendChild(th);
  }
  thead.appendChild(headerRow);
  table.appendChild(thead);

  const tbody = document.createElement('tbody');
  for (const c of commitments) {
    tbody.appendChild(buildCommitmentRow(c));
  }
  table.appendChild(tbody);

  container.appendChild(table);
}

function buildCommitmentRow(c: api.InventoryCommitment): HTMLTableRowElement {
  const tr = document.createElement('tr');

  appendCell(tr, c.provider);
  tr.appendChild(buildAccountCell(c));
  appendCell(tr, c.service);
  appendCell(tr, c.resource_type ?? '');
  appendCell(tr, c.region);
  appendCell(tr, String(c.count));
  appendCell(tr, `${c.term_years}y`);
  appendCell(tr, c.payment_option ?? '');
  appendCell(tr, formatCurrency(c.monthly_cost));
  appendCell(tr, formatDate(c.end_date));

  return tr;
}

function appendCell(tr: HTMLTableRowElement, text: string): void {
  const td = document.createElement('td');
  td.textContent = text;
  tr.appendChild(td);
}

function buildAccountCell(c: api.InventoryCommitment): HTMLTableCellElement {
  const td = document.createElement('td');
  if (c.account_name) {
    td.appendChild(document.createTextNode(c.account_name + ' '));
    const id = document.createElement('span');
    id.className = 'monospace';
    id.textContent = `(${c.account_id})`;
    td.appendChild(id);
  } else {
    const id = document.createElement('span');
    id.className = 'monospace';
    id.textContent = c.account_id;
    td.appendChild(id);
  }
  return td;
}

// ──────────────────────────────────────────────
// Coverage breakdown
// ──────────────────────────────────────────────

const COVERAGE_CONTAINER_ID = 'coverage-providers';
const COVERAGE_REFRESH_BTN_ID = 'coverage-refresh-btn';

const PROVIDER_DISPLAY_NAMES: Record<string, string> = {
  aws: 'AWS',
  azure: 'Azure',
  gcp: 'GCP',
};

/**
 * Fetch and render per-provider coverage breakdowns into #coverage-providers.
 * Shows a skeleton on entry, then either the rendered sections or an error.
 * Idempotent — safe to call on every sub-tab switch and on every refresh click.
 */
export async function loadCoverageBreakdown(): Promise<void> {
  const container = document.getElementById(COVERAGE_CONTAINER_ID);
  if (!container) return;

  wireCoverageRefreshButton();

  // One skeleton row per known provider while loading.
  showSkeletonRows(container, 3, 1);

  try {
    const data = await api.getCoverageBreakdown();
    renderCoverageBreakdown(container, data.providers);
  } catch (error) {
    teardownSkeleton(container);
    const err = error as Error;
    renderErrorParagraph(container, `Failed to load coverage data: ${err.message}`);
  }
}

function wireCoverageRefreshButton(): void {
  const btn = document.getElementById(COVERAGE_REFRESH_BTN_ID);
  if (!btn) return;
  if (btn.dataset['wired'] === '1') return;
  btn.addEventListener('click', () => {
    void loadCoverageBreakdown();
  });
  btn.dataset['wired'] = '1';
}

/**
 * Render coverage sections. Each provider gets its own card. Providers
 * with services=null show an empty-state paragraph. All text is set via
 * textContent -- no innerHTML -- so no escaping helper is needed (XSS posture
 * matches the active-commitments section per issue #340).
 */
function renderCoverageBreakdown(container: HTMLElement, providers: ProviderCoverageSection[]): void {
  clearChildren(container);

  if (!providers || providers.length === 0) {
    renderEmptyParagraph(container, 'No coverage data available.');
    return;
  }

  for (const section of providers) {
    container.appendChild(buildProviderSection(section));
  }
}

function buildProviderSection(section: ProviderCoverageSection): HTMLElement {
  const card = document.createElement('section');
  card.className = 'card coverage-provider-card';

  // Header row: provider name + overall coverage badge.
  const header = document.createElement('div');
  header.className = 'section-header';

  const title = document.createElement('h3');
  title.textContent = PROVIDER_DISPLAY_NAMES[section.provider] ?? section.provider.toUpperCase();
  header.appendChild(title);

  if (section.overall_coverage_pct !== null && section.overall_coverage_pct !== undefined) {
    const badge = document.createElement('span');
    badge.className = 'coverage-overall-badge';
    badge.textContent = `Overall: ${section.overall_coverage_pct.toFixed(1)}% covered`;
    header.appendChild(badge);
  }
  card.appendChild(header);

  // Body: empty-state or per-service table.
  if (!section.services || section.services.length === 0) {
    const empty = document.createElement('p');
    empty.className = 'empty';
    empty.textContent = `No usage detected for ${PROVIDER_DISPLAY_NAMES[section.provider] ?? section.provider}.`;
    card.appendChild(empty);
    return card;
  }

  card.appendChild(buildServiceTable(section.services));
  return card;
}

function buildServiceTable(rows: CoverageServiceRow[]): HTMLTableElement {
  const table = document.createElement('table');
  table.className = 'coverage-service-table';

  const thead = document.createElement('thead');
  const headerRow = document.createElement('tr');
  for (const label of ['Service', 'Covered/mo', 'On-demand gap/mo', 'Coverage %', '']) {
    const th = document.createElement('th');
    th.textContent = label;
    headerRow.appendChild(th);
  }
  thead.appendChild(headerRow);
  table.appendChild(thead);

  const tbody = document.createElement('tbody');
  for (const row of rows) {
    tbody.appendChild(buildServiceRow(row));
  }
  table.appendChild(tbody);
  return table;
}

function buildServiceRow(row: CoverageServiceRow): HTMLTableRowElement {
  const tr = document.createElement('tr');

  appendCell(tr, row.service);
  appendCell(tr, formatCurrency(row.covered_monthly));
  appendCell(tr, formatCurrency(row.on_demand_monthly));
  appendCell(tr, row.coverage_pct !== null && row.coverage_pct !== undefined
    ? `${row.coverage_pct.toFixed(1)}%`
    : 'N/A');
  // Bar cell: visual coverage indicator.
  const barTd = document.createElement('td');
  barTd.className = 'coverage-bar-cell';
  if (row.coverage_pct !== null && row.coverage_pct !== undefined) {
    const bar = document.createElement('div');
    bar.className = 'coverage-bar';
    const fill = document.createElement('div');
    fill.className = 'coverage-bar-fill';
    // Clamp to [0, 100] so a misconfigured value can't overflow.
    const pct = Math.min(100, Math.max(0, row.coverage_pct));
    fill.style.width = `${pct}%`;
    bar.appendChild(fill);
    barTd.appendChild(bar);
  }
  tr.appendChild(barTd);

  return tr;
}

/**
 * Wire sub-nav button clicks. Idempotent — calling this more than once
 * doesn't double-bind handlers.
 */
function wireSubNavListeners(): void {
  if (listenersWired) return;
  const buttons = document.querySelectorAll<HTMLButtonElement>('#inventory-tab .sub-tab-btn');
  if (buttons.length === 0) return;
  buttons.forEach((btn) => {
    btn.addEventListener('click', () => {
      const name = btn.dataset['invSubtab'] ?? DEFAULT_SUB_SECTION;
      switchInventorySubSection(name);
    });
  });
  listenersWired = true;
}

/**
 * Initialize the Inventory & Coverage section. Called by navigation.ts'
 * switchTab when 'inventory' is selected. Defaults to the ri-exchange
 * sub-section if the user hasn't selected one this session.
 */
export function loadInventory(): void {
  wireSubNavListeners();
  switchInventorySubSection(currentSubSection ?? DEFAULT_SUB_SECTION);
}
