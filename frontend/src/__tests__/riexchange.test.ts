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
}));

import { fillQuoteFromRI, openExchangeModal } from '../riexchange';
import * as api from '../api';

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

  it('pre-fills target input with suggestedTargetType when provided', () => {
    openExchangeModal('ri-abc123', 2, 'm5.large');
    const targetInput = modal.querySelector<HTMLInputElement>('.modal-exchange-target');
    expect(targetInput?.value).toBe('m5.large');
  });

  it('leaves target input empty when suggestedTargetType is not provided', () => {
    openExchangeModal('ri-abc123', 2);
    const targetInput = modal.querySelector<HTMLInputElement>('.modal-exchange-target');
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
    openExchangeModal('ri-abc', 3, 'm5.large');
    const quoteBtn = Array.from(modal.querySelectorAll('button')).find((b) => b.textContent === 'Get Quote');
    quoteBtn?.click();
    // Wait for the async submit handler to settle.
    await new Promise((resolve) => setTimeout(resolve, 0));

    expect(mockGetQuote).toHaveBeenCalledTimes(1);
    const req = mockGetQuote.mock.calls[0][0];
    expect(req.ri_ids).toEqual(['ri-abc']);
    expect(req.target_offering_id).toBe('m5.large');
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
    openExchangeModal('ri-multi', 1, 'm5.large');
    modal.querySelector<HTMLButtonElement>('#modal-exchange-add-target')?.click();

    // Populate the second row.
    const rows = modal.querySelectorAll<HTMLDivElement>('.exchange-target-row');
    const secondOffering = rows[1]?.querySelector<HTMLInputElement>('.modal-exchange-target');
    const secondCount = rows[1]?.querySelector<HTMLInputElement>('.modal-exchange-count');
    if (secondOffering) secondOffering.value = 'm6i.large';
    if (secondCount) secondCount.value = '2';

    const quoteBtn = Array.from(modal.querySelectorAll('button')).find((b) => b.textContent === 'Get Quote');
    quoteBtn?.click();
    await new Promise((resolve) => setTimeout(resolve, 0));

    expect(mockGetQuote).toHaveBeenCalledTimes(1);
    const req = mockGetQuote.mock.calls[0][0];
    expect(req.ri_ids).toEqual(['ri-multi']);
    expect(req.targets).toEqual([
      { offering_id: 'm5.large', count: 1 },
      { offering_id: 'm6i.large', count: 2 },
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

  it('does not throw when modal element is missing', () => {
    document.body.innerHTML = '';
    expect(() => openExchangeModal('ri-abc123', 2)).not.toThrow();
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
    document.body.innerHTML = '';
    expect(() => fillQuoteFromRI('ri-abc123', 1)).not.toThrow();
  });
});
