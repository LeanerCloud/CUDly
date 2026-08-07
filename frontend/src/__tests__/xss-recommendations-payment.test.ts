/**
 * Regression tests for stored XSS via rec.payment rendered unescaped in the
 * Opportunities table's payment column (issue #1632).
 *
 * recommendations.ts:renderColumnCell used to emit:
 *   case 'payment': return `<td>${formatPayment(rec.payment)}</td>`;
 *
 * while every other string-bearing arm of the same switch (provider,
 * service, resource_type, capacity, region) is escaped with escapeHtml().
 * formatPayment() passes any value that is not a key of
 * PAYMENT_DISPLAY_LABELS straight through, so an unrecognised `payment`
 * string reached innerHTML verbatim. `payment` is a bare `string` on
 * LocalRecommendation (types.ts), so nothing on the frontend constrains
 * its content. The fix wraps the emission site with escapeHtml().
 */

// Mock the api module
jest.mock('../api', () => ({
  getRecommendations: jest.fn(),
  refreshRecommendations: jest.fn(),
  listAccounts: jest.fn().mockResolvedValue([]),
  listAccountsMinimal: jest.fn().mockResolvedValue([]),
  getConfig: jest.fn().mockResolvedValue({ global: {} }),
  listAccountServiceOverrides: jest.fn().mockResolvedValue([]),
}));

jest.mock('../api/recommendations', () => ({
  getRecommendationDetail: jest.fn().mockResolvedValue({
    id: 'rec-default',
    usage_history: [],
    confidence_bucket: 'low',
    provenance_note: '',
  }),
  getRecommendationsFreshness: jest.fn().mockResolvedValue({
    last_collected_at: new Date(Date.now() - 60 * 60 * 1000).toISOString(),
    last_collection_error: null,
  }),
  refreshRecommendations: jest.fn().mockResolvedValue({}),
}));

const mockShowToast = jest.fn<{ dismiss: () => void }, [unknown]>(() => ({ dismiss: jest.fn() }));
jest.mock('../toast', () => ({
  showToast: (opts: unknown) => mockShowToast(opts),
}));

jest.mock('../state', () => ({
  getCurrentProvider: jest.fn().mockReturnValue('all'),
  setCurrentProvider: jest.fn(),
  getCurrentAccountIDs: jest.fn().mockReturnValue([]),
  setCurrentAccountIDs: jest.fn(),
  getRecommendations: jest.fn().mockReturnValue([]),
  getRecommendationByID: jest.fn().mockReturnValue(undefined),
  setRecommendations: jest.fn(),
  getSelectedRecommendationIDs: jest.fn().mockReturnValue(new Set()),
  clearSelectedRecommendations: jest.fn(),
  addSelectedRecommendation: jest.fn(),
  removeSelectedRecommendation: jest.fn(),
  getRecommendationsSort: jest.fn().mockReturnValue({ column: 'savings', direction: 'desc' }),
  setRecommendationsSort: jest.fn(),
  getRecommendationsColumnFilters: jest.fn().mockReturnValue({}),
  setRecommendationsColumnFilter: jest.fn(),
  clearAllRecommendationsColumnFilters: jest.fn(),
  getVisibleRecommendations: jest.fn().mockReturnValue([]),
  setVisibleRecommendations: jest.fn(),
  getCostPeriod: jest.fn().mockReturnValue('monthly'),
  setCostPeriod: jest.fn(),
  getHiddenColumns: jest.fn().mockReturnValue(new Set()),
  setHiddenColumns: jest.fn(),
  getCurrentUser: jest.fn().mockReturnValue({ id: 'u-admin', email: 'admin@example.com', groups: ['00000000-0000-5000-8000-000000000001'] }),
  subscribeProvider: jest.fn(),
  subscribeAccount: jest.fn(),
}));

// Use the real escapeHtml so the DOM-based escaping is exercised, not a
// stub. formatCurrency/formatTerm are stubbed for readable assertions,
// matching recommendations.test.ts's utils mock shape.
jest.mock('../utils', () => {
  const actual = jest.requireActual<typeof import('../utils')>('../utils');
  return {
    ...actual,
    formatCurrency: jest.fn((val: number) => `$${val || 0}`),
    formatTerm: jest.fn((years: number | null | undefined) =>
      years == null ? '' : `${years} Year${years === 1 ? '' : 's'}`),
    populateAccountFilter: jest.fn(() => Promise.resolve()),
    CURRENCY_DEFAULT_DIGITS: 0,
  };
});

import { loadRecommendations } from '../recommendations';
import * as api from '../api';

const SCRIPT_PAYLOAD = '<script>alert(1)</script>';
const IMG_PAYLOAD = '"><img src=x onerror="alert(1)">';

function makeRec(payment: string) {
  return {
    id: 'rec-1',
    provider: 'aws',
    service: 'ec2',
    resource_type: 't3.medium',
    region: 'us-east-1',
    count: 1,
    term: 1,
    payment,
    savings: 100,
    upfront_cost: 500,
  };
}

describe('XSS regression: rec.payment rendered as text, not markup (#1632)', () => {
  beforeEach(() => {
    document.body.innerHTML = `
      <div id="opportunities-tab" class="tab-content active">
        <div id="recommendations-summary"></div>
        <div id="recommendations-list"></div>
      </div>
      <div id="purchase-modal" class="hidden">
        <div id="purchase-details"></div>
        <div class="modal-buttons">
          <button type="button" id="close-purchase-modal-btn">Cancel</button>
          <button type="button" id="execute-purchase-btn" class="primary">Send for Approval</button>
        </div>
      </div>
    `;
    jest.clearAllMocks();
    jest.useFakeTimers();
    (api.getRecommendations as jest.Mock).mockResolvedValue({ summary: {}, recommendations: [], regions: [] });
  });

  afterEach(() => {
    jest.useRealTimers();
  });

  test('script-tag payload in rec.payment does not create a <script> element', async () => {
    (api.getRecommendations as jest.Mock).mockResolvedValue({
      summary: {},
      recommendations: [makeRec(SCRIPT_PAYLOAD)],
      regions: ['us-east-1'],
    });

    await loadRecommendations();

    expect(document.querySelectorAll('script').length).toBe(0);

    const list = document.getElementById('recommendations-list');
    expect(list).not.toBeNull();
    expect(list!.innerHTML).not.toContain('<script>');
    expect(list!.textContent).toContain(SCRIPT_PAYLOAD);
  });

  test('img-onerror payload in rec.payment does not inject a live <img> element', async () => {
    (api.getRecommendations as jest.Mock).mockResolvedValue({
      summary: {},
      recommendations: [makeRec(IMG_PAYLOAD)],
      regions: ['us-east-1'],
    });

    await loadRecommendations();

    const list = document.getElementById('recommendations-list');
    expect(list).not.toBeNull();
    // Strongest assertion: no <img> DOM node was ever parsed out of the cell.
    expect(list!.querySelectorAll('img').length).toBe(0);
    expect(list!.textContent).toContain('onerror');
  });

  test('known payment option "all-upfront" still renders its display label', async () => {
    (api.getRecommendations as jest.Mock).mockResolvedValue({
      summary: {},
      recommendations: [makeRec('all-upfront')],
      regions: ['us-east-1'],
    });

    await loadRecommendations();

    const list = document.getElementById('recommendations-list');
    expect(list).not.toBeNull();
    expect(list!.textContent).toContain('All Upfront');
  });

  test('unrecognised payment value falls through formatPayment as escaped text', async () => {
    (api.getRecommendations as jest.Mock).mockResolvedValue({
      summary: {},
      recommendations: [makeRec('some-future-option')],
      regions: ['us-east-1'],
    });

    await loadRecommendations();

    const list = document.getElementById('recommendations-list');
    expect(list).not.toBeNull();
    expect(list!.textContent).toContain('some-future-option');
  });
});
