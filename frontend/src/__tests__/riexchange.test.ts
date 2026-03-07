/**
 * RI Exchange module tests — fillQuoteFromRI
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

import { fillQuoteFromRI } from '../riexchange';

function createInput(id: string): HTMLInputElement {
  const el = document.createElement('input');
  el.id = id;
  document.body.appendChild(el);
  return el;
}

function createDiv(id: string): HTMLDivElement {
  const el = document.createElement('div');
  el.id = id;
  document.body.appendChild(el);
  return el;
}

describe('fillQuoteFromRI', () => {
  let riIdsInput: HTMLInputElement;
  let targetInput: HTMLInputElement;
  let countInput: HTMLInputElement;
  let executeSection: HTMLDivElement;
  let quoteResult: HTMLDivElement;
  let quoteSection: HTMLDivElement;

  beforeEach(() => {
    riIdsInput = createInput('ri-exchange-ri-ids');
    targetInput = createInput('ri-exchange-target-offering');
    countInput = createInput('ri-exchange-target-count');
    executeSection = createDiv('ri-exchange-execute-section');
    quoteResult = createDiv('ri-exchange-quote-result');
    quoteSection = createDiv('ri-exchange-quote-section');

    // jsdom doesn't implement scrollIntoView — define it as a mock
    Element.prototype.scrollIntoView = jest.fn();
    jest.useFakeTimers();
  });

  afterEach(() => {
    jest.useRealTimers();
    jest.restoreAllMocks();
    document.body.innerHTML = '';
  });

  it('populates RI ID input with given value', () => {
    fillQuoteFromRI('ri-abc123', 2);
    expect(riIdsInput.value).toBe('ri-abc123');
  });

  it('clears target offering input', () => {
    targetInput.value = 'old-offering';
    fillQuoteFromRI('ri-abc123', 2);
    expect(targetInput.value).toBe('');
  });

  it('populates count input with given count', () => {
    fillQuoteFromRI('ri-abc123', 5);
    expect(countInput.value).toBe('5');
  });

  it('adds hidden class to execute section', () => {
    executeSection.classList.remove('hidden');
    fillQuoteFromRI('ri-abc123', 1);
    expect(executeSection.classList.contains('hidden')).toBe(true);
  });

  it('clears quote result container', () => {
    quoteResult.textContent = 'stale result';
    fillQuoteFromRI('ri-abc123', 1);
    expect(quoteResult.textContent).toBe('');
  });

  it('scrolls quote section into view', () => {
    fillQuoteFromRI('ri-abc123', 1);
    expect(quoteSection.scrollIntoView).toHaveBeenCalledWith({ behavior: 'smooth' });
  });

  it('focuses target offering input after 500ms', () => {
    fillQuoteFromRI('ri-abc123', 1);
    expect(document.activeElement).not.toBe(targetInput);
    jest.advanceTimersByTime(500);
    expect(document.activeElement).toBe(targetInput);
  });

  it('does not throw when DOM elements are missing', () => {
    document.body.innerHTML = '';
    expect(() => fillQuoteFromRI('ri-abc123', 1)).not.toThrow();
  });
});
