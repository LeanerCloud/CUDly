/**
 * Regression tests for stored XSS via provider field injected into class
 * attribute (issue #443).
 *
 * providerCell() in history.ts used to interpolate p.provider directly into
 * the class attribute of a <span>. An attacker-controlled provider value such
 * as  `"><script>alert(1)</script><span class="` would break out of the
 * attribute and inject arbitrary HTML.  The fix whitelists provider against
 * VALID_PROVIDERS and falls back to the sentinel class `provider-unknown`.
 */

import { loadHistory } from '../history';

// Use the real escapeHtml so DOM-based escaping is exercised, not a pass-through stub.
jest.mock('../utils', () => {
  const actual = jest.requireActual<typeof import('../utils')>('../utils');
  return {
    ...actual,
    formatCurrency: jest.fn((val: number) => `$${val || 0}`),
    formatDate: jest.fn((val: string) => val ? new Date(val).toLocaleDateString() : ''),
    formatTerm: jest.fn((years: number | null | undefined) =>
      years == null ? '' : `${years} Year${years === 1 ? '' : 's'}`),
    populateAccountFilter: jest.fn(() => Promise.resolve()),
  };
});

jest.mock('../api', () => ({
  getHistory: jest.fn(),
}));

jest.mock('../navigation', () => ({
  switchTab: jest.fn(),
}));

const mockGetCurrentProvider = jest.fn<string, []>().mockReturnValue('all');
const mockGetCurrentAccountIDs = jest.fn<string[], []>().mockReturnValue([]);

jest.mock('../state', () => ({
  getCurrentUser: jest.fn(),
  getCurrentProvider: () => mockGetCurrentProvider(),
  setCurrentProvider: jest.fn(),
  getCurrentAccountIDs: () => mockGetCurrentAccountIDs(),
  setCurrentAccountIDs: jest.fn(),
  subscribeProvider: jest.fn().mockReturnValue(() => {}),
  subscribeAccount: jest.fn().mockReturnValue(() => {}),
  getAmortizeUpfront: jest.fn().mockReturnValue(false),
  setAmortizeUpfront: jest.fn(),
  subscribeAmortizeUpfront: jest.fn().mockReturnValue(() => {}),
  getPurchaseHistoryColumnFilters: jest.fn().mockReturnValue({}),
  setPurchaseHistoryColumnFilter: jest.fn(),
  clearAllPurchaseHistoryColumnFilters: jest.fn(),
  getApprovalQueueColumnFilters: jest.fn().mockReturnValue({}),
  setApprovalQueueColumnFilter: jest.fn(),
  clearAllApprovalQueueColumnFilters: jest.fn(),
}));

import * as api from '../api';

const XSS_PAYLOAD = '"><script>alert(1)</script><span class="';
const IMG_PAYLOAD = '" onmouseover="alert(1)" x="';

describe('XSS regression: provider class attribute injection (#443)', () => {
  beforeEach(() => {
    document.body.innerHTML = `
      <input type="date" id="history-start">
      <input type="date" id="history-end">
      <div id="history-summary"></div>
      <div id="history-list"></div>
      <div id="purchases-approval-queue"></div>
    `;
    jest.clearAllMocks();
    mockGetCurrentProvider.mockReturnValue('all');
    mockGetCurrentAccountIDs.mockReturnValue([]);
  });

  test('script-tag payload in provider field does not create a <script> element', async () => {
    (api.getHistory as jest.Mock).mockResolvedValue({
      summary: {},
      purchases: [
        {
          purchase_date: '2024-01-15',
          // eslint-disable-next-line @typescript-eslint/no-explicit-any
          provider: XSS_PAYLOAD as any,
          service: 'ec2',
          resource_type: 't3.medium',
          region: 'us-east-1',
          count: 1,
          term: 1,
          upfront_cost: 0,
          monthly_savings: 10,
        },
      ],
    });

    await loadHistory();

    const list = document.getElementById('history-list');
    expect(list).not.toBeNull();
    // No <script> element should have been created anywhere in the document.
    expect(document.querySelectorAll('script').length).toBe(0);
    // The rendered class attribute must not contain the raw payload.
    expect(list!.innerHTML).not.toContain('<script>');
    // The sentinel class "unknown" is applied instead (no raw payload in the class).
    expect(list!.innerHTML).toContain('provider-badge unknown');
  });

  test('img-onerror payload in provider field does not inject event handler attribute', async () => {
    (api.getHistory as jest.Mock).mockResolvedValue({
      summary: {},
      purchases: [
        {
          purchase_date: '2024-01-15',
          // eslint-disable-next-line @typescript-eslint/no-explicit-any
          provider: IMG_PAYLOAD as any,
          service: 'ec2',
          resource_type: 't3.medium',
          region: 'us-east-1',
          count: 1,
          term: 1,
          upfront_cost: 0,
          monthly_savings: 10,
        },
      ],
    });

    await loadHistory();

    const list = document.getElementById('history-list');
    expect(list).not.toBeNull();
    expect(list!.innerHTML).not.toContain('onmouseover');
    expect(list!.innerHTML).toContain('provider-badge unknown');
  });

  test('valid provider "aws" renders the provider-aws class and is not treated as unknown', async () => {
    (api.getHistory as jest.Mock).mockResolvedValue({
      summary: {},
      purchases: [
        {
          purchase_date: '2024-01-15',
          provider: 'aws',
          service: 'ec2',
          resource_type: 't3.medium',
          region: 'us-east-1',
          count: 1,
          term: 1,
          upfront_cost: 0,
          monthly_savings: 10,
        },
      ],
    });

    await loadHistory();

    const list = document.getElementById('history-list');
    expect(list).not.toBeNull();
    expect(list!.innerHTML).toContain('provider-badge aws');
    expect(list!.innerHTML).not.toContain('provider-badge unknown');
  });

  test('valid provider "azure" renders the provider-azure class', async () => {
    (api.getHistory as jest.Mock).mockResolvedValue({
      summary: {},
      purchases: [
        {
          purchase_date: '2024-01-15',
          provider: 'azure',
          service: 'compute',
          resource_type: 'Standard_D2s_v3',
          region: 'eastus',
          count: 1,
          term: 1,
          upfront_cost: 0,
          monthly_savings: 10,
        },
      ],
    });

    await loadHistory();

    const list = document.getElementById('history-list');
    expect(list).not.toBeNull();
    expect(list!.innerHTML).toContain('provider-badge azure');
  });

  test('valid provider "gcp" renders the provider-gcp class', async () => {
    (api.getHistory as jest.Mock).mockResolvedValue({
      summary: {},
      purchases: [
        {
          purchase_date: '2024-01-15',
          provider: 'gcp',
          service: 'compute',
          resource_type: 'n2-standard-2',
          region: 'us-central1',
          count: 1,
          term: 1,
          upfront_cost: 0,
          monthly_savings: 10,
        },
      ],
    });

    await loadHistory();

    const list = document.getElementById('history-list');
    expect(list).not.toBeNull();
    expect(list!.innerHTML).toContain('provider-badge gcp');
  });
});
