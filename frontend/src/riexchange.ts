/**
 * RI Exchange module for CUDly
 * Manages convertible RI listing, reshape recommendations, and exchange operations
 */

import * as api from './api';
import { formatDate, escapeHtml } from './utils';
import type {
  ConvertibleRI,
  RIUtilization,
  ReshapeRecommendation,
  ExchangeQuoteSummary,
} from './api';

// Module state
let currentRIs: ConvertibleRI[] = [];
let currentUtilization: Map<string, RIUtilization> = new Map();
let currentRecommendations: ReshapeRecommendation[] = [];
let lastQuote: ExchangeQuoteSummary | null = null;
let lastQuoteRequest: { ri_ids: string[]; target_offering_id: string; target_count: number } | null = null;

// Generation counter to prevent stale utilization data from overwriting fresh data
let utilizationGeneration = 0;

/**
 * Load the RI Exchange tab — called when tab is activated
 */
export async function loadRIExchange(): Promise<void> {
  await Promise.all([
    loadConvertibleRIs(),
    loadReshapeRecommendations(),
  ]);
}

/**
 * Setup RI Exchange event handlers
 */
export function setupRIExchangeHandlers(): void {
  // Refresh button
  const refreshBtn = document.getElementById('ri-exchange-refresh-btn');
  if (refreshBtn) {
    refreshBtn.addEventListener('click', () => void loadRIExchange());
  }

  // Quote form
  const quoteForm = document.getElementById('ri-exchange-quote-form');
  if (quoteForm) {
    quoteForm.addEventListener('submit', (e) => {
      e.preventDefault();
      void submitQuote();
    });
  }

  // Execute button
  const executeBtn = document.getElementById('ri-exchange-execute-btn');
  if (executeBtn) {
    executeBtn.addEventListener('click', () => void submitExecute());
  }
}

// ──────────────────────────────────────────────
// Convertible RIs table
// ──────────────────────────────────────────────

async function loadConvertibleRIs(): Promise<void> {
  const container = document.getElementById('ri-exchange-instances-list');
  if (!container) return;

  container.innerHTML = '<p class="loading">Loading convertible RIs...</p>';

  try {
    currentRIs = await api.listConvertibleRIs();
    renderRIsTable(container);
    // Load utilization asynchronously (Cost Explorer is slow)
    utilizationGeneration++;
    void loadUtilization(utilizationGeneration);
  } catch (error) {
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
          </tr>`;
        }).join('')}
      </tbody>
    </table>
  `;
}

// ──────────────────────────────────────────────
// Reshape recommendations
// ──────────────────────────────────────────────

async function loadReshapeRecommendations(): Promise<void> {
  const container = document.getElementById('ri-exchange-recommendations-list');
  if (!container) return;

  container.innerHTML = '<p class="loading">Analyzing reshape opportunities...</p>';

  try {
    currentRecommendations = await api.getReshapeRecommendations();
    renderRecommendations(container);
  } catch (error) {
    const err = error as Error;
    container.innerHTML = `<p class="error">Failed to load recommendations: ${escapeHtml(err.message)}</p>`;
  }
}

function renderRecommendations(container: HTMLElement): void {
  if (!currentRecommendations || currentRecommendations.length === 0) {
    container.innerHTML = '<p class="empty">No reshape recommendations. All convertible RIs are well-utilized.</p>';
    return;
  }

  container.innerHTML = `
    <table>
      <thead>
        <tr>
          <th>Source RI</th>
          <th>Current</th>
          <th>Suggested</th>
          <th>Utilization</th>
          <th>Normalized Units</th>
          <th>Reason</th>
          <th>Actions</th>
        </tr>
      </thead>
      <tbody>
        ${currentRecommendations.map((rec, idx) => {
          const utilClass = rec.utilization_percent >= 95 ? 'util-green' : rec.utilization_percent >= 70 ? 'util-yellow' : 'util-red';
          return `
          <tr>
            <td class="monospace">${escapeHtml(rec.source_ri_id)}</td>
            <td>${rec.source_count}x ${escapeHtml(rec.source_instance_type)}</td>
            <td>${rec.target_count}x ${escapeHtml(rec.target_instance_type)}</td>
            <td class="${utilClass}">${rec.utilization_percent.toFixed(1)}%</td>
            <td>${rec.normalized_used.toFixed(1)} / ${rec.normalized_purchased.toFixed(1)}</td>
            <td>${escapeHtml(rec.reason)}</td>
            <td>
              <button data-action="fill-quote" data-index="${idx}">Get Quote</button>
            </td>
          </tr>`;
        }).join('')}
      </tbody>
    </table>
  `;

  // Attach "Get Quote" handlers
  container.querySelectorAll<HTMLButtonElement>('[data-action="fill-quote"]').forEach(btn => {
    btn.addEventListener('click', () => {
      const idx = parseInt(btn.dataset['index'] || '0', 10);
      const rec = currentRecommendations[idx];
      if (rec) fillQuoteFromRecommendation(rec);
    });
  });
}

function fillQuoteFromRecommendation(rec: ReshapeRecommendation): void {
  const riIdsInput = document.getElementById('ri-exchange-ri-ids') as HTMLInputElement | null;
  const targetInput = document.getElementById('ri-exchange-target-offering') as HTMLInputElement | null;
  const countInput = document.getElementById('ri-exchange-target-count') as HTMLInputElement | null;

  if (riIdsInput) riIdsInput.value = rec.source_ri_id;
  if (targetInput) targetInput.value = '';
  if (countInput) countInput.value = String(rec.target_count);

  // Scroll the quote form into view
  document.getElementById('ri-exchange-quote-section')?.scrollIntoView({ behavior: 'smooth' });
}

// ──────────────────────────────────────────────
// Quote + Execute
// ──────────────────────────────────────────────

async function submitQuote(): Promise<void> {
  const riIdsInput = document.getElementById('ri-exchange-ri-ids') as HTMLInputElement | null;
  const targetInput = document.getElementById('ri-exchange-target-offering') as HTMLInputElement | null;
  const countInput = document.getElementById('ri-exchange-target-count') as HTMLInputElement | null;
  const resultContainer = document.getElementById('ri-exchange-quote-result');
  const executeSection = document.getElementById('ri-exchange-execute-section');

  if (!riIdsInput || !targetInput || !resultContainer) return;

  const riIds = riIdsInput.value.split(',').map(s => s.trim()).filter(Boolean);
  const targetOfferingId = targetInput.value.trim();
  const rawCount = parseInt(countInput?.value ?? '1', 10);
  const targetCount = isNaN(rawCount) || rawCount < 1 ? 1 : rawCount;

  if (riIds.length === 0) {
    resultContainer.innerHTML = '<p class="error">Please enter at least one RI ID.</p>';
    return;
  }
  if (!targetOfferingId) {
    resultContainer.innerHTML = '<p class="error">Please enter a target offering ID.</p>';
    return;
  }

  resultContainer.innerHTML = '<p class="loading">Getting exchange quote...</p>';
  if (executeSection) executeSection.classList.add('hidden');

  try {
    lastQuote = await api.getExchangeQuote({
      ri_ids: riIds,
      target_offering_id: targetOfferingId,
      target_count: targetCount,
    });
    lastQuoteRequest = { ri_ids: riIds, target_offering_id: targetOfferingId, target_count: targetCount };
    renderQuoteResult(resultContainer);
    if (executeSection && lastQuote.IsValidExchange) {
      executeSection.classList.remove('hidden');
    }
  } catch (error) {
    const err = error as Error;
    resultContainer.innerHTML = `<p class="error">Quote failed: ${escapeHtml(err.message)}</p>`;
  }
}

function renderQuoteResult(container: HTMLElement): void {
  if (!lastQuote) return;

  const validClass = lastQuote.IsValidExchange ? 'quote-valid' : 'quote-invalid';
  const validText = lastQuote.IsValidExchange ? 'Valid Exchange' : 'Invalid Exchange';

  container.innerHTML = `
    <div class="quote-summary ${validClass}">
      <h4>${validText}</h4>
      ${lastQuote.ValidationFailureReason ? `<p class="error">${escapeHtml(lastQuote.ValidationFailureReason)}</p>` : ''}
      <div class="quote-details">
        <div class="quote-row">
          <span>Currency:</span>
          <strong>${escapeHtml(lastQuote.CurrencyCode)}</strong>
        </div>
        <div class="quote-row">
          <span>Payment Due:</span>
          <strong>${escapeHtml(lastQuote.PaymentDueRaw)}</strong>
        </div>
        <div class="quote-row">
          <span>Source Hourly Price:</span>
          <strong>${escapeHtml(lastQuote.SourceHourlyPriceRaw)}</strong>
        </div>
        <div class="quote-row">
          <span>Source Remaining Total:</span>
          <strong>${escapeHtml(lastQuote.SourceRemainingTotalRaw)}</strong>
        </div>
        <div class="quote-row">
          <span>Target Hourly Price:</span>
          <strong>${escapeHtml(lastQuote.TargetHourlyPriceRaw)}</strong>
        </div>
        <div class="quote-row">
          <span>Target Remaining Total:</span>
          <strong>${escapeHtml(lastQuote.TargetRemainingTotalRaw)}</strong>
        </div>
        ${lastQuote.OutputReservedInstancesExp ? `
        <div class="quote-row">
          <span>New RI Expiry:</span>
          <strong>${escapeHtml(lastQuote.OutputReservedInstancesExp)}</strong>
        </div>` : ''}
      </div>
    </div>
  `;
}

async function submitExecute(): Promise<void> {
  const maxPaymentInput = document.getElementById('ri-exchange-max-payment') as HTMLInputElement | null;
  const confirmCheckbox = document.getElementById('ri-exchange-confirm') as HTMLInputElement | null;
  const resultContainer = document.getElementById('ri-exchange-execute-result');

  if (!resultContainer || !lastQuote || !lastQuoteRequest) return;

  if (!confirmCheckbox?.checked) {
    resultContainer.innerHTML = '<p class="error">Please confirm you want to execute this exchange.</p>';
    return;
  }

  const maxPayment = maxPaymentInput?.value.trim();
  const maxPaymentNum = parseFloat(maxPayment ?? '');
  if (!maxPayment || isNaN(maxPaymentNum) || maxPaymentNum < 0) {
    resultContainer.innerHTML = '<p class="error">Please enter a valid non-negative payment cap (USD).</p>';
    return;
  }

  resultContainer.innerHTML = '<p class="loading">Executing exchange...</p>';

  try {
    const result = await api.executeExchange({
      ri_ids: lastQuoteRequest.ri_ids,
      target_offering_id: lastQuoteRequest.target_offering_id,
      target_count: lastQuoteRequest.target_count,
      max_payment_due_usd: maxPayment,
    });

    resultContainer.innerHTML = `
      <div class="exchange-success">
        <h4>Exchange Completed</h4>
        <p>Exchange ID: <strong class="monospace">${escapeHtml(result.exchange_id)}</strong></p>
        <p>Payment Due: <strong>${escapeHtml(result.quote.PaymentDueRaw)}</strong> ${escapeHtml(result.quote.CurrencyCode)}</p>
      </div>
    `;

    // Clear exchange state to prevent accidental re-execution
    lastQuote = null;
    lastQuoteRequest = null;
    const executeSection = document.getElementById('ri-exchange-execute-section');
    if (executeSection) executeSection.classList.add('hidden');
    const quoteResultContainer = document.getElementById('ri-exchange-quote-result');
    if (quoteResultContainer) quoteResultContainer.innerHTML = '';

    // Refresh the RI list after successful exchange
    void loadConvertibleRIs();
  } catch (error) {
    const err = error as Error;
    resultContainer.innerHTML = `<p class="error">Exchange failed: ${escapeHtml(err.message)}</p>`;
  }
}
