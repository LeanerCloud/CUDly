/**
 * Recommendations module for CUDly
 */

import * as api from './api';
import * as state from './state';
import { formatCurrency, formatTerm, escapeHtml, populateAccountFilter, formatRelativeTime } from './utils';
import { renderFreshness } from './freshness';
import { getRecommendationsFreshness } from './api/recommendations';
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
    const indices = Array.from(state.getSelectedRecommendations());
    const picks = indices
      .map((i) => recommendations[i])
      .filter((r): r is LocalRecommendation => r !== undefined);
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

function buildListMarkup(recommendations: LocalRecommendation[], selectedRecs: ReadonlySet<number>): string {
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
          <th>Actions</th>
        </tr>
      </thead>
      <tbody>
        ${sorted.map((rec) => {
          const index = recommendations.indexOf(rec);
          const savingsClass = rec.savings > 1000 ? 'high-savings' : rec.savings > 100 ? 'medium-savings' : '';
          const isSelected = selectedRecs.has(index);
          const accountName = rec.cloud_account_id ? (accountNamesCache.get(rec.cloud_account_id) || rec.cloud_account_id) : '\u2014';
          return `
          <tr class="recommendation-row ${savingsClass} ${isSelected ? 'selected' : ''}" data-index="${index}">
            <td class="checkbox-col">
              <input type="checkbox" data-index="${index}" ${isSelected ? 'checked' : ''} aria-label="Select recommendation ${index + 1}">
            </td>
            <td><span class="provider-badge ${rec.provider}">${rec.provider.toUpperCase()}</span></td>
            <td>${escapeHtml(accountName)}</td>
            <td><span class="service-badge">${escapeHtml(rec.service)}</span></td>
            <td title="${escapeHtml(rec.resource_type)}">${escapeHtml(rec.resource_type)}${rec.engine ? ` (${escapeHtml(rec.engine)})` : ''}</td>
            <td>${escapeHtml(rec.region)}</td>
            <td>${rec.count}</td>
            <td>${formatTerm(rec.term)}</td>
            <td class="savings">${formatCurrency(rec.savings)}</td>
            <td>${formatCurrency(rec.upfront_cost)}</td>
            <td>
              <button data-action="purchase" data-index="${index}">Purchase</button>
            </td>
          </tr>`;
        }).join('')}
      </tbody>
    </table>
  `;
}

function renderRecommendationsList(recommendations: LocalRecommendation[]): void {
  const container = document.getElementById('recommendations-list');
  if (!container) return;

  if (!recommendations || recommendations.length === 0) {
    container.innerHTML = '<p class="empty">No recommendations found. Try adjusting filters or refreshing.</p>';
    return;
  }

  const selectedRecs = state.getSelectedRecommendations();
  // Dynamic table markup: every caller-provided value passes through
  // escapeHtml or is a number. The string is built in buildListMarkup.
  container.innerHTML = buildListMarkup(recommendations, selectedRecs);

  renderBulkToolbar(container, recommendations, selectedRecs.size);

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
        recommendations.forEach((_, i) => state.addSelectedRecommendation(i));
      } else {
        state.clearSelectedRecommendations();
      }
      renderRecommendationsList(recommendations);
    });
  }

  container.querySelectorAll<HTMLInputElement>('input[data-index]').forEach(cb => {
    cb.addEventListener('change', () => {
      const idx = parseInt(cb.dataset['index'] || '0', 10);
      if (cb.checked) {
        state.addSelectedRecommendation(idx);
      } else {
        state.removeSelectedRecommendation(idx);
      }
      renderRecommendationsList(recommendations);
    });
  });

  container.querySelectorAll<HTMLButtonElement>('[data-action="purchase"]').forEach(btn => {
    btn.addEventListener('click', (e) => {
      e.stopPropagation();
      const idx = parseInt(btn.dataset['index'] || '0', 10);
      openPurchaseModal([recommendations[idx] as LocalRecommendation]);
    });
  });

  // Row-click opens the detail drawer — skip clicks that originated on
  // the checkbox or per-row button (so those still flow through to their
  // own handlers without also triggering the drawer).
  container.querySelectorAll<HTMLTableRowElement>('tr.recommendation-row').forEach((tr) => {
    tr.addEventListener('click', (e) => {
      const target = e.target as HTMLElement;
      if (target.closest('input[type="checkbox"], button')) return;
      const idx = parseInt(tr.dataset['index'] || '0', 10);
      const rec = recommendations[idx];
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
