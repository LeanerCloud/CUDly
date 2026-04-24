/**
 * Recommendations module for CUDly
 */

import * as api from './api';
import * as state from './state';
import { formatCurrency, formatTerm, escapeHtml, populateAccountFilter, formatRelativeTime } from './utils';
import { renderFreshness } from './freshness';
import { getRecommendationsFreshness } from './api/recommendations';
import { showToast } from './toast';
import { isPaymentSupported, type Provider as CompatProvider } from './lib/purchase-compatibility';
import type { RecommendationsResponse, LocalRecommendation, RecommendationsSummary } from './types';

// Module state for current purchase modal recommendations
let currentPurchaseRecommendations: LocalRecommendation[] = [];
// Cache of account ID → name for column display
let accountNamesCache: Map<string, string> = new Map();

function populateRecommendationsAccountFilter(provider?: string): Promise<void> {
  return populateAccountFilter('recommendations-account-filter', api.listAccounts, provider);
}

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
  const providerFilter = document.getElementById('recommendations-provider-filter') as HTMLSelectElement | null;
  if (providerFilter) {
    // Set initial value from state
    providerFilter.value = state.getCurrentProvider();

    providerFilter.addEventListener('change', () => {
      state.setCurrentProvider(providerFilter.value as '' | 'aws' | 'azure' | 'gcp');
      updateServiceFilterVisibility(providerFilter.value);
      void populateRecommendationsAccountFilter(providerFilter.value);
      void loadRecommendations();
    });
  }

  const accountFilter = document.getElementById('recommendations-account-filter') as HTMLSelectElement | null;
  if (accountFilter) {
    accountFilter.addEventListener('change', () => {
      const val = accountFilter.value;
      state.setCurrentAccountIDs(val ? [val] : []);
      void loadRecommendations();
    });
  }

  void populateRecommendationsAccountFilter(state.getCurrentProvider());

  // Setup service filter handler
  const serviceFilter = document.getElementById('service-filter') as HTMLSelectElement | null;
  if (serviceFilter) {
    serviceFilter.addEventListener('change', () => void loadRecommendations());
  }

  // Setup region filter handler
  const regionFilter = document.getElementById('region-filter') as HTMLSelectElement | null;
  if (regionFilter) {
    regionFilter.addEventListener('change', () => void loadRecommendations());
  }

  // Setup min savings filter handler
  const minSavingsFilter = document.getElementById('min-savings-filter') as HTMLInputElement | null;
  if (minSavingsFilter) {
    minSavingsFilter.addEventListener('change', () => void loadRecommendations());
  }
}

/**
 * Update service filter visibility based on selected provider
 */
function updateServiceFilterVisibility(provider: string): void {
  const serviceFilter = document.getElementById('service-filter') as HTMLSelectElement | null;
  if (!serviceFilter) return;

  // Show/hide optgroups based on selected provider
  const optgroups = serviceFilter.querySelectorAll('optgroup');
  optgroups.forEach(optgroup => {
    const providerLabel = optgroup.label.toLowerCase();
    if (provider === '') {
      // Show all optgroups when "All Providers" is selected
      (optgroup as HTMLOptGroupElement).style.display = '';
    } else if (providerLabel.includes(provider)) {
      (optgroup as HTMLOptGroupElement).style.display = '';
    } else {
      (optgroup as HTMLOptGroupElement).style.display = 'none';
    }
  });

  // Reset selection to "All Services" when switching providers
  serviceFilter.value = '';
}

/**
 * Load recommendations
 */
export async function loadRecommendations(): Promise<void> {
  try {
    const serviceFilter = document.getElementById('service-filter') as HTMLSelectElement | null;
    const regionFilter = document.getElementById('region-filter') as HTMLSelectElement | null;
    const minSavingsFilter = document.getElementById('min-savings-filter') as HTMLInputElement | null;

    const accountIDs = state.getCurrentAccountIDs();
    const filters: api.RecommendationFilters = {
      provider: state.getCurrentProvider(),
      service: serviceFilter?.value,
      region: regionFilter?.value,
      minSavings: minSavingsFilter?.value ? parseInt(minSavingsFilter.value, 10) : undefined,
      account_ids: accountIDs.length > 0 ? accountIDs : undefined
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
    populateRegionFilter(data.regions || []);

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

const SORTABLE_COLUMNS: Record<string, (r: LocalRecommendation) => number> = {
  savings: (r) => r.savings,
  upfront_cost: (r) => r.upfront_cost,
  count: (r) => r.count,
  term: (r) => r.term,
};

const SORT_HEADER_LABELS: Record<string, string> = {
  savings: 'Monthly Savings',
  upfront_cost: 'Upfront Cost',
  count: 'Count',
  term: 'Term',
};

function sortIndicator(column: string, active: string, direction: 'asc' | 'desc'): string {
  if (column !== active) return '<span class="sort-indicator" aria-hidden="true">\u2195</span>';
  return direction === 'asc'
    ? '<span class="sort-indicator active" aria-hidden="true">\u25B2</span>'
    : '<span class="sort-indicator active" aria-hidden="true">\u25BC</span>';
}

function sortedRecommendations(recs: LocalRecommendation[]): LocalRecommendation[] {
  const sort = state.getRecommendationsSort();
  const key = SORTABLE_COLUMNS[sort.column];
  if (!key) return recs;
  const direction = sort.direction === 'asc' ? 1 : -1;
  // slice() clones so we don't mutate the caller's array.
  return recs.slice().sort((a, b) => (key(a) - key(b)) * direction);
}

function renderBulkToolbar(container: HTMLElement, recommendations: LocalRecommendation[], selectedCount: number): void {
  const existing = container.querySelector('.recommendations-bulk-toolbar');
  if (existing) existing.remove();
  if (selectedCount === 0) return;
  const toolbar = document.createElement('div');
  toolbar.className = 'recommendations-bulk-toolbar';
  toolbar.setAttribute('role', 'toolbar');
  toolbar.setAttribute('aria-label', 'Bulk actions for selected recommendations');

  const count = document.createElement('span');
  count.className = 'bulk-count';
  count.textContent = `${selectedCount} selected`;
  toolbar.appendChild(count);

  const addBtn = document.createElement('button');
  addBtn.type = 'button';
  addBtn.className = 'btn btn-small btn-primary';
  addBtn.textContent = 'Add to plan';
  addBtn.addEventListener('click', () => {
    const picks = selectedRecsFromVisible(recommendations);
    if (picks.length > 0) openPurchaseModal(picks);
  });
  toolbar.appendChild(addBtn);

  const clearBtn = document.createElement('button');
  clearBtn.type = 'button';
  clearBtn.className = 'btn btn-small btn-secondary';
  clearBtn.textContent = 'Clear';
  clearBtn.addEventListener('click', () => {
    state.clearSelectedRecommendations();
    renderRecommendationsList(recommendations);
  });
  toolbar.appendChild(clearBtn);

  container.insertBefore(toolbar, container.firstChild);
}

// selectedRecsFromVisible returns the intersection of state-selected
// IDs with the currently-visible list. Out-of-view selection is
// silently discarded — the user filtered it out, so it shouldn't
// affect the bulk action.
function selectedRecsFromVisible(recommendations: LocalRecommendation[]): LocalRecommendation[] {
  const selected = state.getSelectedRecommendationIDs();
  return recommendations.filter((r) => selected.has(r.id));
}

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

  // Confidence bucket — computed client-side from the savings magnitude
  // + instance count. A proper per-recommendation confidence score that
  // accounts for historical usage variance needs a backend endpoint
  // (tracked in known_issues/28_recommendations_detail_endpoint.md);
  // this client-side heuristic gives users a directional signal in the
  // meantime.
  const bucket = confidenceBucketFor(rec);
  const confidenceRow = document.createElement('dl');
  confidenceRow.className = 'detail-drawer-fields';
  const confDt = document.createElement('dt');
  confDt.textContent = 'Confidence';
  const confDd = document.createElement('dd');
  const badge = document.createElement('span');
  badge.className = `confidence-badge confidence-${bucket}`;
  badge.textContent = bucket.charAt(0).toUpperCase() + bucket.slice(1);
  confDd.appendChild(badge);
  confidenceRow.appendChild(confDt);
  confidenceRow.appendChild(confDd);
  drawer.appendChild(confidenceRow);

  // Provenance — render immediately with a placeholder, then fill in
  // asynchronously from /api/recommendations/freshness (the endpoint is
  // already hit by the freshness pill so its response is cached on the
  // network side; this fetch is fast and non-blocking to the drawer
  // opening).
  const provenance = document.createElement('p');
  provenance.className = 'detail-drawer-note';
  provenance.textContent = `Derived from ${providerDisplayName(rec.provider)} recommendation APIs. Last collection timing loading\u2026`;
  drawer.appendChild(provenance);
  void getRecommendationsFreshness()
    .then((f) => {
      const rel = f.last_collected_at ? formatRelativeTime(f.last_collected_at) : 'never';
      provenance.textContent = `Derived from ${providerDisplayName(rec.provider)} recommendation APIs. Last collected ${rel}.`;
    })
    .catch(() => {
      provenance.textContent = `Derived from ${providerDisplayName(rec.provider)} recommendation APIs.`;
    });

  // Usage-history drill-down still requires backend work — see
  // known_issues/28_recommendations_detail_endpoint.md for the endpoint
  // contract (GET /api/recommendations/:id/detail returning a usage
  // series).
  const usageNote = document.createElement('p');
  usageNote.className = 'detail-drawer-note detail-drawer-note-muted';
  usageNote.textContent = 'Usage history over the collection window is not yet available; the detail endpoint is tracked separately.';
  drawer.appendChild(usageNote);

  document.body.appendChild(backdrop);
  document.body.appendChild(drawer);
  closeBtn.focus();
}

type ConfidenceBucket = 'low' | 'medium' | 'high';

/**
 * Client-side confidence heuristic. A real confidence score needs
 * historical usage variance from the backend (tracked in known_issues
 * #28); this directional bucket surfaces "probably a solid pick" vs
 * "marginal" to users immediately based on savings magnitude + size
 * of the target footprint.
 */
function confidenceBucketFor(rec: LocalRecommendation): ConfidenceBucket {
  const savings = rec.savings || 0;
  const count = rec.count || 1;
  // High: material monthly savings AND a non-trivial fleet — a single
  // $1000/mo rec from one tiny instance is likely an outlier, so we
  // require both signals.
  if (savings >= 200 && count >= 3) return 'high';
  if (savings >= 50) return 'medium';
  return 'low';
}

function providerDisplayName(provider: string): string {
  switch (provider.toLowerCase()) {
    case 'aws': return 'AWS';
    case 'azure': return 'Azure';
    case 'gcp': return 'GCP';
    default: return provider;
  }
}

function buildListMarkup(recommendations: LocalRecommendation[], selectedRecs: ReadonlySet<string>): string {
  const sort = state.getRecommendationsSort();
  const sorted = sortedRecommendations(recommendations);
  const sortHeader = (column: string): string =>
    `<th class="sortable" data-sort="${column}" tabindex="0" role="button" aria-label="Sort by ${SORT_HEADER_LABELS[column]}"><span>${SORT_HEADER_LABELS[column]}</span>${sortIndicator(column, sort.column, sort.direction)}</th>`;

  return `
    <table>
      <thead>
        <tr>
          <th class="checkbox-col">
            <input type="checkbox" id="select-all-recs" aria-label="Select all recommendations">
          </th>
          <th>Provider</th>
          <th>Account</th>
          <th>Service</th>
          <th>Resource Type</th>
          <th>Region</th>
          ${sortHeader('count')}
          ${sortHeader('term')}
          ${sortHeader('savings')}
          ${sortHeader('upfront_cost')}
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

interface BulkPurchaseToolbarState {
  term: 1 | 3;
  payment: 'all-upfront' | 'partial-upfront' | 'no-upfront' | 'monthly';
  capacity: number; // 1..100
}

const defaultBulkPurchaseState: BulkPurchaseToolbarState = {
  term: 3,
  payment: 'all-upfront',
  capacity: 100,
};

// loadBulkPurchaseState reads the toolbar state from localStorage with
// a try/catch around JSON.parse — a garbage value shouldn't blow up the
// page render; defaults take over and the next Save overwrites.
function loadBulkPurchaseState(): BulkPurchaseToolbarState {
  try {
    const raw = localStorage.getItem(BULK_PURCHASE_LS_KEY);
    if (!raw) return { ...defaultBulkPurchaseState };
    const parsed = JSON.parse(raw) as Partial<BulkPurchaseToolbarState>;
    return {
      term: parsed.term === 1 ? 1 : 3,
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
    // sticky choice. The toolbar still works in-session.
  }
}

// renderTopBulkPurchaseToolbar renders the always-visible toolbar above
// the recs list with Term / Payment / Capacity % controls and the
// Purchase button. Wired via ID-based selection: when no rows are
// selected the Purchase action targets the full visible list; with
// any selection, only the selected-and-visible subset.
function renderTopBulkPurchaseToolbar(container: HTMLElement, recommendations: LocalRecommendation[]): void {
  const existing = container.querySelector('.recommendations-top-toolbar');
  if (existing) existing.remove();

  const toolbar = document.createElement('div');
  toolbar.className = 'recommendations-top-toolbar';
  toolbar.setAttribute('role', 'toolbar');
  toolbar.setAttribute('aria-label', 'Bulk purchase controls');

  const tbState = loadBulkPurchaseState();

  const termLabel = document.createElement('label');
  termLabel.innerHTML = 'Term: ';
  const termSelect = document.createElement('select');
  termSelect.id = 'bulk-purchase-term';
  [['1', '1 Year'], ['3', '3 Years']].forEach(([v, t]) => {
    const opt = document.createElement('option');
    opt.value = v as string;
    opt.textContent = t as string;
    if (Number(v) === tbState.term) opt.selected = true;
    termSelect.appendChild(opt);
  });
  termLabel.appendChild(termSelect);
  toolbar.appendChild(termLabel);

  const paymentLabel = document.createElement('label');
  paymentLabel.innerHTML = 'Payment: ';
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
  toolbar.appendChild(paymentLabel);

  const capacityLabel = document.createElement('label');
  capacityLabel.innerHTML = 'Capacity %: ';
  const capacityInput = document.createElement('input');
  capacityInput.id = 'bulk-purchase-capacity';
  capacityInput.type = 'number';
  capacityInput.min = '1';
  capacityInput.max = '100';
  capacityInput.step = '1';
  capacityInput.value = String(tbState.capacity);
  capacityLabel.appendChild(capacityInput);
  toolbar.appendChild(capacityLabel);

  const purchaseBtn = document.createElement('button');
  purchaseBtn.type = 'button';
  purchaseBtn.className = 'btn btn-primary';
  purchaseBtn.id = 'bulk-purchase-btn';
  purchaseBtn.textContent = 'Purchase…';
  purchaseBtn.disabled = recommendations.length === 0;
  if (purchaseBtn.disabled) purchaseBtn.title = 'No recommendations to purchase';
  toolbar.appendChild(purchaseBtn);

  const persist = (): void => {
    saveBulkPurchaseState({
      term: (Number(termSelect.value) === 1 ? 1 : 3) as 1 | 3,
      payment: paymentSelect.value as BulkPurchaseToolbarState['payment'],
      capacity: Math.max(1, Math.min(100, parseInt(capacityInput.value, 10) || 100)),
    });
  };
  termSelect.addEventListener('change', persist);
  paymentSelect.addEventListener('change', persist);
  capacityInput.addEventListener('change', persist);

  purchaseBtn.addEventListener('click', () => {
    handleBulkPurchaseClick(recommendations);
  });

  container.insertBefore(toolbar, container.firstChild);
}

function handleBulkPurchaseClick(recommendations: LocalRecommendation[]): void {
  const tb = loadBulkPurchaseState();
  const selected = selectedRecsFromVisible(recommendations);
  const target = selected.length > 0 ? selected : recommendations;
  if (target.length === 0) {
    showToast({ message: 'No recommendations to purchase.', kind: 'warning' });
    return;
  }

  // Scale by capacity %; drop rows whose scaled count floors to 0.
  const scaled: LocalRecommendation[] = [];
  for (const r of target) {
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

  // Bucket by (provider, service) — the compatibility check runs per
  // bucket since e.g. AWS EC2 accepts no-upfront but AWS RDS doesn't
  // for 3yr.
  const buckets = new Map<string, LocalRecommendation[]>();
  for (const r of scaled) {
    const key = `${r.provider}|${r.service}`;
    const existing = buckets.get(key);
    if (existing) existing.push(r);
    else buckets.set(key, [r]);
  }
  const bucketEntries = Array.from(buckets.entries());

  // Check compatibility per bucket. A bucket with an unsupported
  // (term, payment) combo is just as problematic as having multiple
  // providers — neither case can proceed with a single POST.
  const incompatible = bucketEntries.filter(([key]) => {
    const [provider, service] = key.split('|') as [CompatProvider, string];
    return !isPaymentSupported(provider, service, tb.term as 1 | 3, tb.payment);
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
  const buckets: FanOutBucket[] = bucketEntries.map(([key, recs]) => {
    const [provider, service] = key.split('|') as [CompatProvider, string];
    return {
      provider,
      service,
      term: toolbar.term,
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

  modal.classList.remove('hidden');
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

function renderRecommendationsList(recommendations: LocalRecommendation[]): void {
  const container = document.getElementById('recommendations-list');
  if (!container) return;

  if (!recommendations || recommendations.length === 0) {
    container.innerHTML = '<p class="empty">No recommendations found. Try adjusting filters or refreshing.</p>';
    renderTopBulkPurchaseToolbar(container, []);
    return;
  }

  const selectedIDs = state.getSelectedRecommendationIDs();
  // Dynamic table markup: every caller-provided value passes through
  // escapeHtml or is a number. The string is built in buildListMarkup.
  container.innerHTML = buildListMarkup(recommendations, selectedIDs);

  // Count only the intersection with currently-visible recs so the
  // selection toolbar's "N selected" doesn't include stale selections
  // that live outside the current filter.
  const visibleSelectedCount = recommendations.reduce(
    (n, r) => n + (selectedIDs.has(r.id) ? 1 : 0),
    0,
  );
  renderBulkToolbar(container, recommendations, visibleSelectedCount);
  renderTopBulkPurchaseToolbar(container, recommendations);

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

function populateRegionFilter(regions: string[]): void {
  const select = document.getElementById('region-filter') as HTMLSelectElement | null;
  if (!select) return;

  const currentValue = select.value;
  select.innerHTML = '<option value="">All Regions</option>' +
    regions.map(r => `<option value="${escapeHtml(r)}" ${r === currentValue ? 'selected' : ''}>${escapeHtml(r)}</option>`).join('');
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

  document.getElementById('purchase-modal')?.classList.remove('hidden');
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
