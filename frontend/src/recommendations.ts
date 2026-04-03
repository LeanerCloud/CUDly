/**
 * Recommendations module for CUDly
 */

import * as api from './api';
import * as state from './state';
import { formatCurrency, escapeHtml } from './utils';
import type { RecommendationsResponse, LocalRecommendation, RecommendationsSummary } from './types';

// Module state for current purchase modal recommendations
let currentPurchaseRecommendations: LocalRecommendation[] = [];

/**
 * Reset account filter select to just the "All Accounts" option and repopulate
 */
function resetAccountSelect(select: HTMLSelectElement): void {
  while (select.options.length > 1) select.remove(1);
}

/**
 * Populate the recommendations account filter select
 */
async function populateRecommendationsAccountFilter(provider?: string): Promise<void> {
  const select = document.getElementById('recommendations-account-filter') as HTMLSelectElement | null;
  if (!select) return;
  try {
    const filter = provider && provider !== 'all' ? { provider: provider as api.Provider } : undefined;
    const accounts = await api.listAccounts(filter);
    const current = select.value;
    resetAccountSelect(select);
    accounts.forEach(a => {
      const opt = document.createElement('option');
      opt.value = a.id;
      opt.textContent = `${a.name} (${a.external_id})`;
      select.appendChild(opt);
    });
    select.value = current;
  } catch {
    // Non-critical — filter just won't be populated
  }
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
      state.setCurrentProvider(providerFilter.value as 'all' | 'aws' | 'azure' | 'gcp');
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
    if (provider === '' || provider === 'all') {
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

    const data = await api.getRecommendations(filters) as unknown as RecommendationsResponse;
    state.setRecommendations((data.recommendations || []) as unknown as api.Recommendation[]);
    state.clearSelectedRecommendations();

    renderRecommendationsSummary(data.summary || {});
    renderRecommendationsList(data.recommendations || []);
    populateRegionFilter(data.regions || []);
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
      <p class="value">${summary.avg_payback_months || 0} months</p>
    </div>
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

  container.innerHTML = `
    <table>
      <thead>
        <tr>
          <th class="checkbox-col">
            <input type="checkbox" id="select-all-recs">
          </th>
          <th>Provider</th>
          <th>Service</th>
          <th>Resource Type</th>
          <th>Region</th>
          <th>Count</th>
          <th>Term</th>
          <th>Monthly Savings</th>
          <th>Upfront Cost</th>
          <th>Actions</th>
        </tr>
      </thead>
      <tbody>
        ${recommendations.map((rec, index) => {
          const savingsClass = rec.monthly_savings > 1000 ? 'high-savings' : rec.monthly_savings > 100 ? 'medium-savings' : '';
          const isSelected = selectedRecs.has(index);
          return `
          <tr class="${savingsClass} ${isSelected ? 'selected' : ''}">
            <td class="checkbox-col">
              <input type="checkbox" data-index="${index}" ${isSelected ? 'checked' : ''}>
            </td>
            <td><span class="provider-badge ${rec.provider}">${rec.provider.toUpperCase()}</span></td>
            <td><span class="service-badge">${escapeHtml(rec.service)}</span></td>
            <td>${escapeHtml(rec.resource_type)}${rec.engine ? ` (${escapeHtml(rec.engine)})` : ''}</td>
            <td>${escapeHtml(rec.region)}</td>
            <td>${rec.count}</td>
            <td>${rec.term} year</td>
            <td class="savings">${formatCurrency(rec.monthly_savings)}</td>
            <td>${formatCurrency(rec.upfront_cost)}</td>
            <td>
              <button data-action="purchase" data-index="${index}">Purchase</button>
            </td>
          </tr>`;
        }).join('')}
      </tbody>
    </table>
  `;

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
    btn.addEventListener('click', () => {
      const idx = parseInt(btn.dataset['index'] || '0', 10);
      openPurchaseModal([recommendations[idx] as LocalRecommendation]);
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

  const totalSavings = recommendations.reduce((sum, r) => sum + (r.monthly_savings || 0), 0);
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
              <td class="savings">${formatCurrency(r.monthly_savings)}</td>
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
