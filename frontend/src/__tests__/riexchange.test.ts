/**
 * RI Exchange module tests — openExchangeModal and fillQuoteFromRI
 */

// Mock the api module defensively (riexchange.ts imports it)
jest.mock('../api', () => ({
  listConvertibleRIs: jest.fn(),
  getRIUtilization: jest.fn(),
  getReshapeRecommendations: jest.fn(),
  getExchangeQuote: jest.fn(),
  executeExchange: jest.fn(),
  getRIExchangeHistory: jest.fn(),
  getRIExchangeConfig: jest.fn(),
  updateRIExchangeConfig: jest.fn(),
  // listTargetOfferings is called by populateAwsOfferings() in openExchangeModal.
  // Default to returning an empty list so tests that don't care about the picker
  // content remain unaffected. Override per-test for picker-content assertions.
  listTargetOfferings: jest.fn().mockResolvedValue([]),
}));

// Mock navigation to avoid loading dashboard/plans/... transitively.
jest.mock('../navigation', () => ({
  switchTab: jest.fn(),
  switchSettingsSubTab: jest.fn(),
}));

import {
  fillQuoteFromRI,
  loadReshapeRecommendations,
  loadRIExchange,
  openExchangeModal,
  renderReshapeStalenessBanner,
  setupRIExchangeHandlers,
} from '../riexchange';
import * as api from '../api';
import * as navigation from '../navigation';

function createModal(): HTMLDivElement {
  const modal = document.createElement('div');
  modal.id = 'ri-exchange-modal';
  modal.className = 'modal hidden';
  const content = document.createElement('div');
  content.className = 'modal-content';
  modal.appendChild(content);
  document.body.appendChild(modal);
  return modal;
}

describe('openExchangeModal', () => {
  let modal: HTMLDivElement;

  beforeEach(() => {
    modal = createModal();
  });

  afterEach(() => {
    document.body.innerHTML = '';
  });

  it('removes hidden class to show the modal', () => {
    openExchangeModal('ri-abc123', 2);
    expect(modal.classList.contains('hidden')).toBe(false);
  });

  it('displays the RI ID in the modal content', () => {
    openExchangeModal('ri-abc123', 2);
    expect(modal.textContent).toContain('ri-abc123');
  });

  it('pre-fills count input with given count', () => {
    openExchangeModal('ri-abc123', 5);
    // Multi-target refactor: count + target inputs now live inside
    // per-row containers; selectors changed from #id to class.
    const countInput = modal.querySelector<HTMLInputElement>('.modal-exchange-count');
    expect(countInput?.value).toBe('5');
  });

  it('pre-fills hidden target input with offering_id when suggestedTargetType matches an alternativeTarget', () => {
    // suggestedTargetType is resolved to an offering_id via alternativeTargets lookup.
    openExchangeModal('ri-abc123', 2, 'm5.large', [
      { instance_type: 'm5.large', offering_id: '4b2293b4-5fbc-4017-9c75-d5a9d3aa8c91', effective_monthly_cost: 42.5 },
    ]);
    const targetInput = modal.querySelector<HTMLInputElement>('.modal-exchange-target');
    // Hidden input must contain the UUID, not the instance type string.
    expect(targetInput?.value).toBe('4b2293b4-5fbc-4017-9c75-d5a9d3aa8c91');
  });

  it('leaves hidden target input empty when suggestedTargetType is not provided', () => {
    openExchangeModal('ri-abc123', 2);
    const targetInput = modal.querySelector<HTMLInputElement>('.modal-exchange-target');
    expect(targetInput?.value).toBe('');
  });

  it('leaves hidden target input empty when suggestedTargetType has no matching alternativeTarget', () => {
    openExchangeModal('ri-abc123', 2, 'm5.large');
    const targetInput = modal.querySelector<HTMLInputElement>('.modal-exchange-target');
    // No alternativeTargets provided -- cannot resolve instance type to UUID.
    expect(targetInput?.value).toBe('');
  });

  it('starts with exactly one target row', () => {
    openExchangeModal('ri-abc123', 2);
    const rows = modal.querySelectorAll<HTMLDivElement>('.exchange-target-row');
    expect(rows.length).toBe(1);
  });

  it('adds a second target row when "+ Add target" is clicked', () => {
    openExchangeModal('ri-abc123', 2);
    const addBtn = modal.querySelector<HTMLButtonElement>('#modal-exchange-add-target');
    expect(addBtn).not.toBeNull();
    addBtn?.click();
    const rows = modal.querySelectorAll<HTMLDivElement>('.exchange-target-row');
    expect(rows.length).toBe(2);
  });

  it('posts singleton target_offering_id/target_count when exactly one row is present', async () => {
    const mockGetQuote = api.getExchangeQuote as jest.Mock;
    mockGetQuote.mockResolvedValueOnce({
      IsValidExchange: false,
      ValidationFailureReason: 'test',
      CurrencyCode: 'USD',
      PaymentDueRaw: '0',
      SourceHourlyPriceRaw: '',
      SourceRemainingUpfrontRaw: '',
      SourceRemainingTotalRaw: '',
      TargetHourlyPriceRaw: '',
      TargetRemainingUpfrontRaw: '',
      TargetRemainingTotalRaw: '',
    });
    // Pre-seed with a CE alternative so suggestedTargetType resolves to a UUID.
    const offeringUUID = '4b2293b4-5fbc-4017-9c75-d5a9d3aa8c91';
    openExchangeModal('ri-abc', 3, 'm5.large', [
      { instance_type: 'm5.large', offering_id: offeringUUID, effective_monthly_cost: 42.5 },
    ]);
    const quoteBtn = Array.from(modal.querySelectorAll('button')).find((b) => b.textContent === 'Get Quote');
    quoteBtn?.click();
    // Wait for the async submit handler to settle.
    await new Promise((resolve) => setTimeout(resolve, 0));

    expect(mockGetQuote).toHaveBeenCalledTimes(1);
    const req = mockGetQuote.mock.calls[0][0];
    expect(req.ri_ids).toEqual(['ri-abc']);
    // Singleton shape: target_offering_id must be the UUID, not the instance type.
    expect(req.target_offering_id).toBe(offeringUUID);
    expect(req.target_count).toBe(3);
    expect(req.targets).toBeUndefined();
  });

  it('posts targets[] when two or more rows are present', async () => {
    const mockGetQuote = api.getExchangeQuote as jest.Mock;
    mockGetQuote.mockResolvedValueOnce({
      IsValidExchange: false,
      ValidationFailureReason: 'test',
      CurrencyCode: 'USD',
      PaymentDueRaw: '0',
      SourceHourlyPriceRaw: '',
      SourceRemainingUpfrontRaw: '',
      SourceRemainingTotalRaw: '',
      TargetHourlyPriceRaw: '',
      TargetRemainingUpfrontRaw: '',
      TargetRemainingTotalRaw: '',
    });
    const uuid1 = '4b2293b4-5fbc-4017-9c75-d5a9d3aa8c91';
    const uuid2 = '7e123456-0000-4567-abcd-ef0123456789';
    // Pre-seed with CE alternatives so the first row can resolve a UUID.
    openExchangeModal('ri-multi', 1, 'm5.large', [
      { instance_type: 'm5.large', offering_id: uuid1, effective_monthly_cost: 40.0 },
    ]);
    modal.querySelector<HTMLButtonElement>('#modal-exchange-add-target')?.click();

    // Inject a UUID into the second row's hidden input directly (simulates
    // the user picking from the dropdown for the second target).
    const rows = modal.querySelectorAll<HTMLDivElement>('.exchange-target-row');
    const secondOffering = rows[1]?.querySelector<HTMLInputElement>('.modal-exchange-target');
    const secondCount = rows[1]?.querySelector<HTMLInputElement>('.modal-exchange-count');
    if (secondOffering) secondOffering.value = uuid2;
    if (secondCount) secondCount.value = '2';

    const quoteBtn = Array.from(modal.querySelectorAll('button')).find((b) => b.textContent === 'Get Quote');
    quoteBtn?.click();
    await new Promise((resolve) => setTimeout(resolve, 0));

    expect(mockGetQuote).toHaveBeenCalledTimes(1);
    const req = mockGetQuote.mock.calls[0][0];
    expect(req.ri_ids).toEqual(['ri-multi']);
    expect(req.targets).toEqual([
      { offering_id: uuid1, count: 1 },
      { offering_id: uuid2, count: 2 },
    ]);
    expect(req.target_offering_id).toBeUndefined();
    expect(req.target_count).toBeUndefined();
  });

  it('removes a target row when the × button is clicked, but keeps at least one', () => {
    openExchangeModal('ri-abc123', 2);
    const addBtn = modal.querySelector<HTMLButtonElement>('#modal-exchange-add-target');
    addBtn?.click();
    addBtn?.click();
    // Three rows now; removing one should leave 2.
    const removes = modal.querySelectorAll<HTMLButtonElement>('.exchange-remove-target');
    removes[0]?.click();
    let rows = modal.querySelectorAll<HTMLDivElement>('.exchange-target-row');
    expect(rows.length).toBe(2);

    // Remove down to 1 row; further clicks are ignored.
    modal.querySelector<HTMLButtonElement>('.exchange-remove-target')?.click();
    rows = modal.querySelectorAll<HTMLDivElement>('.exchange-target-row');
    expect(rows.length).toBe(1);
    modal.querySelector<HTMLButtonElement>('.exchange-remove-target')?.click();
    rows = modal.querySelectorAll<HTMLDivElement>('.exchange-target-row');
    expect(rows.length).toBe(1);
  });

  it('hides modal when cancel button is clicked', () => {
    openExchangeModal('ri-abc123', 2);
    const cancelBtn = Array.from(modal.querySelectorAll('button')).find(b => b.textContent === 'Cancel');
    cancelBtn?.click();
    expect(modal.classList.contains('hidden')).toBe(true);
  });

  it('shows a cost chip when the selected offering_id matches an alternative', () => {
    // suggestedTargetType='m5.large' resolves to offering_id 'off-m5' via alternativeTargets.
    // The chip should show the cost for that offering.
    openExchangeModal('ri-abc', 2, 'm5.large', [
      { instance_type: 'm5.large', offering_id: 'off-m5', effective_monthly_cost: 42.5 },
      { instance_type: 'm6i.large', offering_id: 'off-m6i', effective_monthly_cost: 35.0 },
    ]);
    const chip = modal.querySelector<HTMLSpanElement>('.cost-chip');
    expect(chip).not.toBeNull();
    expect(chip?.textContent).toBe('$42.50/mo each');
  });

  it('shows an em-dash in the cost chip when the selected offering_id has no CE pricing match', () => {
    // 'unknown.shape' cannot be resolved to an offering_id from alternativeTargets,
    // so the hidden input stays empty and the chip shows "—".
    openExchangeModal('ri-abc', 2, 'unknown.shape', [
      { instance_type: 'm5.large', offering_id: 'off-m5', effective_monthly_cost: 42.5 },
    ]);
    const chip = modal.querySelector<HTMLSpanElement>('.cost-chip');
    expect(chip?.textContent).toBe('—');
  });

  it('computes the running total across two rows using per-row count', () => {
    openExchangeModal('ri-multi', 1, 'm5.large', [
      { instance_type: 'm5.large', offering_id: 'off-m5', effective_monthly_cost: 40.0 },
      { instance_type: 'm6i.large', offering_id: 'off-m6i', effective_monthly_cost: 30.0 },
    ]);
    const firstCountInput = modal.querySelector<HTMLInputElement>('.modal-exchange-count');
    if (firstCountInput) {
      firstCountInput.value = '2';
      firstCountInput.dispatchEvent(new Event('input'));
    }
    modal.querySelector<HTMLButtonElement>('#modal-exchange-add-target')?.click();
    const rows = modal.querySelectorAll<HTMLDivElement>('.exchange-target-row');
    const secondOffering = rows[1]?.querySelector<HTMLInputElement>('.modal-exchange-target');
    const secondCount = rows[1]?.querySelector<HTMLInputElement>('.modal-exchange-count');
    if (secondOffering) {
      // Inject the offering_id UUID directly into the hidden input and
      // trigger an input event so updateRunningTotal fires.
      secondOffering.value = 'off-m6i';
      secondOffering.dispatchEvent(new Event('input'));
    }
    if (secondCount) {
      secondCount.value = '3';
      secondCount.dispatchEvent(new Event('input'));
    }
    const total = modal.querySelector<HTMLDivElement>('#modal-exchange-running-total');
    expect(total?.textContent).toContain('$170.00/mo');
    expect(total?.textContent).not.toContain('incomplete');
  });

  it('marks the running total as incomplete when some rows have no pricing match', () => {
    openExchangeModal('ri-incomplete', 1, 'm5.large', [
      { instance_type: 'm5.large', offering_id: 'off-m5', effective_monthly_cost: 40.0 },
    ]);
    modal.querySelector<HTMLButtonElement>('#modal-exchange-add-target')?.click();
    const rows = modal.querySelectorAll<HTMLDivElement>('.exchange-target-row');
    const secondOffering = rows[1]?.querySelector<HTMLInputElement>('.modal-exchange-target');
    if (secondOffering) {
      // 'unknown-offering' does not match any CE alternative offering_id.
      secondOffering.value = 'unknown-offering';
      secondOffering.dispatchEvent(new Event('input'));
    }
    const total = modal.querySelector<HTMLDivElement>('#modal-exchange-running-total');
    expect(total?.textContent).toContain('$40.00/mo');
    expect(total?.textContent).toContain('incomplete');
  });

  it('hides the running total when called without alternativeTargets', () => {
    openExchangeModal('ri-no-alts', 2, 'm5.large');
    const total = modal.querySelector<HTMLDivElement>('#modal-exchange-running-total');
    expect(total?.classList.contains('hidden')).toBe(true);
  });

  it('does not throw when modal element is missing', () => {
    document.body.innerHTML = '';
    expect(() => openExchangeModal('ri-abc123', 2)).not.toThrow();
  });

  // Defect 1 -- picker tests
  it('renders a select picker (not a free-text input) for the target offering', () => {
    openExchangeModal('ri-abc123', 2);
    // There must be a <select> for the picker.
    const picker = modal.querySelector<HTMLSelectElement>('.modal-exchange-target-select');
    expect(picker).not.toBeNull();
    // There must NOT be a visible text input (the hidden field has type="hidden").
    const textInput = modal.querySelector<HTMLInputElement>('input[type="text"].modal-exchange-target');
    expect(textInput).toBeNull();
  });

  it('populates the select with CE recommendation options when alternativeTargets are provided', async () => {
    openExchangeModal('ri-abc', 2, undefined, [
      { instance_type: 'm5.large', offering_id: 'off-m5', effective_monthly_cost: 42.5 },
      { instance_type: 'm6i.large', offering_id: 'off-m6i', effective_monthly_cost: 35.0 },
    ]);
    // CE recommendations land in the select synchronously (no async fetch needed).
    const picker = modal.querySelector<HTMLSelectElement>('.modal-exchange-target-select');
    const options = picker ? Array.from(picker.querySelectorAll('option')) : [];
    const optionValues = options.map((o) => o.value);
    expect(optionValues).toContain('off-m5');
    expect(optionValues).toContain('off-m6i');
  });

  it('populates the select with AWS offerings after async load completes', async () => {
    const mockListOfferings = api.listTargetOfferings as jest.Mock;
    mockListOfferings.mockResolvedValueOnce([
      {
        offering_id: '4b2293b4-5fbc-4017-9c75-d5a9d3aa8c91',
        instance_type: 'm5.xlarge',
        offering_type: 'No Upfront',
        product_description: 'Linux/UNIX',
        duration: 31536000,
        fixed_price: 0,
        usage_price: 0.12,
        currency_code: 'USD',
        scope: 'Region',
        normalization_factor: 8,
      },
    ]);
    openExchangeModal('ri-abc', 2);
    // Let the async populateAwsOfferings() settle.
    await new Promise((resolve) => setTimeout(resolve, 10));

    const picker = modal.querySelector<HTMLSelectElement>('.modal-exchange-target-select');
    const options = picker ? Array.from(picker.querySelectorAll('option')) : [];
    const optionValues = options.map((o) => o.value);
    expect(optionValues).toContain('4b2293b4-5fbc-4017-9c75-d5a9d3aa8c91');
    expect(mockListOfferings).toHaveBeenCalledWith('ri-abc');
  });

  it('selecting a picker option drives the hidden offering input with a UUID', () => {
    openExchangeModal('ri-abc', 2, undefined, [
      { instance_type: 'm5.large', offering_id: '4b2293b4-5fbc-4017-9c75-d5a9d3aa8c91', effective_monthly_cost: 42.5 },
    ]);
    const picker = modal.querySelector<HTMLSelectElement>('.modal-exchange-target-select');
    const hiddenInput = modal.querySelector<HTMLInputElement>('.modal-exchange-target');
    if (picker) {
      picker.value = '4b2293b4-5fbc-4017-9c75-d5a9d3aa8c91';
      picker.dispatchEvent(new Event('change'));
    }
    expect(hiddenInput?.value).toBe('4b2293b4-5fbc-4017-9c75-d5a9d3aa8c91');
  });

  it('rejects submission when the hidden offering input contains a non-UUID value', async () => {
    const mockGetQuote = api.getExchangeQuote as jest.Mock;
    openExchangeModal('ri-abc', 1);
    // Force a non-UUID value into the hidden input (simulates a bypass attempt).
    const hiddenInput = modal.querySelector<HTMLInputElement>('.modal-exchange-target');
    if (hiddenInput) hiddenInput.value = 't3.medium';

    const quoteBtn = Array.from(modal.querySelectorAll('button')).find((b) => b.textContent === 'Get Quote');
    quoteBtn?.click();
    await new Promise((resolve) => setTimeout(resolve, 0));

    // The quote API must NOT have been called -- the frontend guard fires first.
    expect(mockGetQuote).not.toHaveBeenCalled();
    // The result container must show an error mentioning the invalid value.
    const result = modal.querySelector('#modal-exchange-result');
    expect(result?.textContent).toContain('t3.medium');
  });
});

describe('fillQuoteFromRI', () => {
  let modal: HTMLDivElement;

  beforeEach(() => {
    modal = createModal();
  });

  afterEach(() => {
    document.body.innerHTML = '';
  });

  it('opens the exchange modal', () => {
    fillQuoteFromRI('ri-abc123', 2);
    expect(modal.classList.contains('hidden')).toBe(false);
  });

  it('displays the RI ID in the modal', () => {
    fillQuoteFromRI('ri-xyz', 3);
    expect(modal.textContent).toContain('ri-xyz');
  });

  it('does not throw when modal is missing', () => {
    const body = document.body;
    while (body.firstChild) body.removeChild(body.firstChild);
    expect(() => fillQuoteFromRI('ri-abc123', 1)).not.toThrow();
  });
});

// Reshape-table rendering tests. Kept in a separate describe block so
// the shared modal-element fixture doesn't leak in.
describe('reshape recommendations table', () => {
  let tableContainer: HTMLDivElement;

  beforeEach(() => {
    tableContainer = document.createElement('div');
    tableContainer.id = 'ri-exchange-recommendations-list';
    document.body.appendChild(tableContainer);
  });

  afterEach(() => {
    const body = document.body;
    while (body.firstChild) body.removeChild(body.firstChild);
    jest.resetAllMocks();
  });

  // Baseline rec with the reshape fields the renderer expects. Tests
  // extend this with alternative_targets when needed.
  const baseRec = {
    source_ri_id: 'ri-abc',
    source_instance_type: 'm5.xlarge',
    source_count: 1,
    target_instance_type: 'm5.large',
    target_count: 2,
    utilization_percent: 50,
    normalized_used: 4,
    normalized_purchased: 8,
    reason: 'underutilized',
  };

  it('renders the Alternatives column with cost chips when the rec carries alternative_targets', async () => {
    const mockGet = api.getReshapeRecommendations as jest.Mock;
    mockGet.mockResolvedValueOnce({
      recommendations: [
        {
          ...baseRec,
          alternative_targets: [
            { instance_type: 'm7g.large', offering_id: 'off-m7g', effective_monthly_cost: 30.0 },
            { instance_type: 'm6i.large', offering_id: 'off-m6i', effective_monthly_cost: 35.0 },
          ],
        },
      ],
      recs_staleness: '',
      recs_collected_at: null,
    });

    await loadReshapeRecommendations();

    const chips = tableContainer.querySelectorAll<HTMLSpanElement>('.cost-chip');
    expect(chips.length).toBe(2);
    // Chips include the instance type + formatted cost; the backend
    // emits them in ascending-cost order (commit 7378ceaa5 sorts
    // fillAlternativesFromOfferings output by EffectiveMonthlyCost)
    // so the UI shows cheapest first.
    expect(chips[0]?.textContent).toContain('m7g.large');
    expect(chips[0]?.textContent).toContain('$30.00/mo');
    expect(chips[1]?.textContent).toContain('m6i.large');
    expect(chips[1]?.textContent).toContain('$35.00/mo');
  });

  it('renders an em-dash in the Alternatives column when the rec has no alternative_targets', async () => {
    const mockGet = api.getReshapeRecommendations as jest.Mock;
    mockGet.mockResolvedValueOnce({ recommendations: [{ ...baseRec }], recs_staleness: '', recs_collected_at: null }); // no alternative_targets

    await loadReshapeRecommendations();

    // The Alternatives <td> is the 4th column (Source RI, Current,
    // Suggested, Alternatives, Utilization, Normalized Units, Reason,
    // Actions). Select the single data row and pull the 4th cell.
    const row = tableContainer.querySelector<HTMLTableRowElement>('tbody tr');
    expect(row).not.toBeNull();
    const cells = row?.querySelectorAll<HTMLTableCellElement>('td');
    expect(cells?.[3]?.textContent).toBe('—');
    expect(tableContainer.querySelectorAll('.cost-chip').length).toBe(0);
  });
});

// Empty-state copy varies with RI-fleet presence. Prior to commit P4 the
// "All convertible RIs are well-utilized" copy ran on every zero-rec state
// including a totally empty fleet — a truthy claim about an empty set.
describe('reshape recommendations empty state', () => {
  let instancesEl: HTMLDivElement;
  let recsEl: HTMLDivElement;
  let historyEl: HTMLDivElement;

  const sampleRI = {
    reserved_instance_id: 'ri-1',
    instance_type: 'm5.large',
    availability_zone: 'us-east-1a',
    instance_count: 1,
    start: '2026-01-01T00:00:00Z',
    end: '2027-01-01T00:00:00Z',
    offering_type: 'Convertible',
    fixed_price: 0,
    usage_price: 0,
    state: 'active',
    normalization_factor: 4,
  };

  beforeEach(() => {
    instancesEl = document.createElement('div');
    instancesEl.id = 'ri-exchange-instances-list';
    recsEl = document.createElement('div');
    recsEl.id = 'ri-exchange-recommendations-list';
    historyEl = document.createElement('div');
    historyEl.id = 'ri-exchange-history-list';
    document.body.append(instancesEl, recsEl, historyEl);

    (api.getRIUtilization as jest.Mock).mockResolvedValue([]);
    (api.getRIExchangeHistory as jest.Mock).mockResolvedValue([]);
    (api.getReshapeRecommendations as jest.Mock).mockResolvedValue({ recommendations: [], recs_staleness: '', recs_collected_at: null });
  });

  afterEach(() => {
    const body = document.body;
    while (body.firstChild) body.removeChild(body.firstChild);
    jest.resetAllMocks();
  });

  it('advises the user their accounts have no convertible RIs yet when both lists are empty', async () => {
    (api.listConvertibleRIs as jest.Mock).mockResolvedValue([]);

    await loadRIExchange();

    expect(recsEl.textContent).toContain('none are registered yet');
    expect(recsEl.textContent).not.toContain('well-utilized');
    expect(recsEl.textContent).not.toContain('utilization threshold');
  });

  it('claims all RIs meet the threshold only when RIs actually exist', async () => {
    (api.listConvertibleRIs as jest.Mock).mockResolvedValue([sampleRI]);

    await loadRIExchange();

    expect(recsEl.textContent).toContain('meet your utilization threshold');
    expect(recsEl.textContent).toContain('1 convertible RI ');
    expect(recsEl.textContent).not.toContain('none are registered');
  });
});

// Staleness banner tests for issue #150.
describe('renderReshapeStalenessBanner', () => {
  let listEl: HTMLDivElement;

  beforeEach(() => {
    const wrapper = document.createElement('div');
    listEl = document.createElement('div');
    listEl.id = 'ri-exchange-recommendations-list';
    wrapper.appendChild(listEl);
    document.body.appendChild(wrapper);
  });

  afterEach(() => {
    document.body.innerHTML = '';
  });

  it('renders no banner when staleness is empty', () => {
    renderReshapeStalenessBanner('', null);
    const banner = document.getElementById('ri-exchange-recommendations-freshness');
    expect(banner?.textContent).toBe('');
  });

  it('renders a soft-warning banner for staleness=soft', () => {
    renderReshapeStalenessBanner('soft', null);
    const banner = document.getElementById('ri-exchange-recommendations-freshness');
    expect(banner?.className).toContain('warning');
    expect(banner?.textContent).toContain('may be up to 24h old');
  });

  it('renders a hard-warning banner for staleness=hard', () => {
    renderReshapeStalenessBanner('hard', null);
    const banner = document.getElementById('ri-exchange-recommendations-freshness');
    expect(banner?.className).toContain('error');
    expect(banner?.textContent).toContain('older than 24h');
  });

  it('includes an age label when recs_collected_at is provided', () => {
    const twoHoursAgo = new Date(Date.now() - 2 * 60 * 60 * 1000).toISOString();
    renderReshapeStalenessBanner('soft', twoHoursAgo);
    const banner = document.getElementById('ri-exchange-recommendations-freshness');
    expect(banner?.textContent).toContain('last collected 2h');
  });

  it('clears an existing banner when staleness becomes empty', () => {
    renderReshapeStalenessBanner('hard', null);
    expect(document.getElementById('ri-exchange-recommendations-freshness')?.className).toContain('error');
    renderReshapeStalenessBanner('', null);
    const banner = document.getElementById('ri-exchange-recommendations-freshness');
    expect(banner?.textContent).toBe('');
    expect(banner?.className).toBe('');
  });
});

describe('⚙︎ Exchange settings deep-link', () => {
  beforeEach(() => {
    const btn = document.createElement('button');
    btn.id = 'ri-exchange-settings-btn';
    document.body.appendChild(btn);
  });

  afterEach(() => {
    const body = document.body;
    while (body.firstChild) body.removeChild(body.firstChild);
    jest.resetAllMocks();
  });

  it('switches to Settings → Purchasing when clicked', () => {
    setupRIExchangeHandlers();
    const btn = document.getElementById('ri-exchange-settings-btn')!;
    btn.click();
    expect(navigation.switchTab).toHaveBeenCalledWith('settings');
    expect(navigation.switchSettingsSubTab).toHaveBeenCalledWith('purchasing');
  });
});
