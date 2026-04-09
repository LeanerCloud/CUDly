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
    const countInput = modal.querySelector<HTMLInputElement>('#modal-exchange-count');
    expect(countInput?.value).toBe('5');
  });

  it('pre-fills target input with suggestedTargetType when provided', () => {
    openExchangeModal('ri-abc123', 2, 'm5.large');
    const targetInput = modal.querySelector<HTMLInputElement>('#modal-exchange-target');
    expect(targetInput?.value).toBe('m5.large');
  });

  it('leaves target input empty when suggestedTargetType is not provided', () => {
    openExchangeModal('ri-abc123', 2);
    const targetInput = modal.querySelector<HTMLInputElement>('#modal-exchange-target');
    expect(targetInput?.value).toBe('');
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
