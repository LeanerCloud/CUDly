/**
 * RI Exchange module for CUDly
 * Manages convertible RI listing, reshape recommendations, and exchange operations
 */

import * as api from './api';
import { formatDate, formatDateTime, escapeHtml } from './utils';
import type {
  ConvertibleRI,
  RIUtilization,
  ReshapeRecommendation,
  ExchangeQuoteSummary,
  RIExchangeConfig,
  RIExchangeHistoryRecord,
} from './api';

// Module state
let currentRIs: ConvertibleRI[] = [];
let currentUtilization: Map<string, RIUtilization> = new Map();
let currentRecommendations: ReshapeRecommendation[] = [];
let lastQuote: ExchangeQuoteSummary | null = null;
let lastQuoteRequest: { ri_ids: string[]; target_offering_id: string; target_count: number } | null = null;

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
          <th>Actions</th>
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
            <td><button class="btn-small" data-action="quote-ri" data-ri-id="${escapeHtml(ri.reserved_instance_id)}" data-count="${ri.instance_count}" aria-label="Exchange ${escapeHtml(ri.reserved_instance_id)}">Exchange</button></td>
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
              <button class="btn-small" data-action="fill-quote" data-index="${idx}">Exchange</button>
            </td>
          </tr>`;
        }).join('')}
      </tbody>
    </table>
  `;

  // Attach "Exchange" handlers
  container.querySelectorAll<HTMLButtonElement>('[data-action="fill-quote"]').forEach(btn => {
    btn.addEventListener('click', () => {
      const idx = parseInt(btn.dataset['index'] || '0', 10);
      const rec = currentRecommendations[idx];
      if (rec) fillQuoteFromRecommendation(rec);
    });
  });
}

function fillQuoteFromRecommendation(rec: ReshapeRecommendation): void {
  openExchangeModal(rec.source_ri_id, rec.target_count, rec.target_instance_type);
}

export function fillQuoteFromRI(riId: string, count: number): void {
  openExchangeModal(riId, count);
}

// ──────────────────────────────────────────────
// RI Exchange Modal
// ──────────────────────────────────────────────

export function openExchangeModal(riId: string, count: number, suggestedTargetType?: string): void {
  const modalEl = document.getElementById('ri-exchange-modal');
  if (!modalEl) return;
  const modal = modalEl; // non-null const for use in closures

  const content = modal.querySelector('.modal-content');
  if (!content) return;

  // Scoped state for this modal session
  let modalQuote: ExchangeQuoteSummary | null = null;
  let modalQuoteReq: { ri_ids: string[]; target_offering_id: string; target_count: number } | null = null;

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

  // Count input
  const countRow = document.createElement('div');
  countRow.className = 'setting-row';
  const countLabel = document.createElement('label');
  countLabel.textContent = 'Target Count: ';
  const countInput = document.createElement('input');
  countInput.type = 'number';
  countInput.min = '1';
  countInput.value = String(count);
  countInput.id = 'modal-exchange-count';
  countLabel.appendChild(countInput);
  countRow.appendChild(countLabel);
  content.appendChild(countRow);

  // Target offering ID input
  const targetRow = document.createElement('div');
  targetRow.className = 'setting-row';
  const targetLabel = document.createElement('label');
  targetLabel.textContent = 'Target Offering ID: ';
  const targetInput = document.createElement('input');
  targetInput.type = 'text';
  targetInput.id = 'modal-exchange-target';
  targetInput.placeholder = 'e.g. t3.medium';
  if (suggestedTargetType) targetInput.value = suggestedTargetType;
  targetLabel.appendChild(targetInput);
  targetRow.appendChild(targetLabel);
  content.appendChild(targetRow);

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
  modal.classList.remove('hidden');

  cancelBtn.addEventListener('click', () => {
    modal.classList.add('hidden');
  });

  quoteBtn.addEventListener('click', () => {
    void submitModalQuote();
  });

  executeBtn.addEventListener('click', () => {
    void submitModalExecute();
  });

  async function submitModalQuote(): Promise<void> {
    const targetOfferingId = targetInput.value.trim();
    const rawCount = parseInt(countInput.value, 10);
    const targetCount = isNaN(rawCount) || rawCount < 1 ? 1 : rawCount;

    if (!targetOfferingId) {
      setResultText(resultContainer, 'Please enter a target offering ID.', 'error');
      return;
    }

    setResultText(resultContainer, 'Getting exchange quote...', 'loading');
    executeBtn.classList.add('hidden');

    try {
      modalQuote = await api.getExchangeQuote({
        ri_ids: [riId],
        target_offering_id: targetOfferingId,
        target_count: targetCount,
      });
      modalQuoteReq = { ri_ids: [riId], target_offering_id: targetOfferingId, target_count: targetCount };
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
        target_offering_id: modalQuoteReq.target_offering_id,
        target_count: modalQuoteReq.target_count,
        max_payment_due_usd: modalQuote.PaymentDueRaw,
      });

      setResultText(resultContainer, 'Exchange completed. ID: ' + result.exchange_id, 'success-message');
      executeBtn.classList.add('hidden');
      modalQuote = null;
      modalQuoteReq = null;

      setTimeout(() => {
        modal.classList.add('hidden');
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
    if (confirmCheckbox) confirmCheckbox.checked = false;
    const executeSection = document.getElementById('ri-exchange-execute-section');
    if (executeSection) executeSection.classList.add('hidden');
    const quoteResultContainer = document.getElementById('ri-exchange-quote-result');
    if (quoteResultContainer) quoteResultContainer.innerHTML = '';

    // Refresh the RI list and history after successful exchange
    void loadConvertibleRIs();
    void loadExchangeHistory();
  } catch (error) {
    const err = error as Error;
    resultContainer.innerHTML = `<p class="error">Exchange failed: ${escapeHtml(err.message)}</p>`;
  }
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
  banner.className = 'error-message';
  banner.style.background = '#fff3e0';
  banner.style.borderColor = '#fbbc04';
  banner.style.color = '#e65100';
  banner.style.marginBottom = '1rem';
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
    + '<div class="settings-buttons"><button type="submit" class="primary">Save Settings</button></div>'
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

  const form = document.getElementById('ri-exchange-settings-form');
  if (form) {
    form.addEventListener('submit', (e) => {
      e.preventDefault();
      void saveAutomationSettings();
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

async function saveAutomationSettings(): Promise<void> {
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

  // Confirm auto mode
  if (mode === 'auto') {
    const confirmed = window.confirm(
      'You are enabling Fully Automated mode. RI exchanges will be executed ' +
      'automatically without manual approval. Are you sure?'
    );
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
    return '<tr>'
      + '<td>' + escapeHtml(formatDateTime(rec.created_at)) + '</td>'
      + '<td>' + rec.source_count + 'x ' + escapeHtml(rec.source_instance_type) + '</td>'
      + '<td>' + rec.target_count + 'x ' + escapeHtml(rec.target_instance_type) + '</td>'
      + '<td>' + rec.target_count + '</td>'
      + '<td>$' + escapeHtml(rec.payment_due) + '</td>'
      + '<td><span class="' + getStatusBadgeClass(rec.status) + '">' + escapeHtml(rec.status) + '</span></td>'
      + '<td>' + exchangeIdCell + '</td>'
      + '</tr>';
  }).join('');

  const tableHTML = '<table>'
    + '<thead><tr>'
    + '<th>Date</th><th>Source Type</th><th>Target Type</th><th>Count</th><th>Payment</th><th>Status</th><th>Exchange ID</th>'
    + '</tr></thead>'
    + '<tbody>' + rowsHTML + '</tbody>'
    + '</table>';

  container.textContent = '';
  const wrapper = document.createElement('div');
  wrapper.innerHTML = tableHTML;
  while (wrapper.firstChild) {
    container.appendChild(wrapper.firstChild);
  }
}
