/**
 * History inline "Sell on Marketplace" button tests (issue #292, FINDING D).
 *
 * Regression guard for the "dead Sell button" gap: canSellOnMarketplace used to
 * require offering_class === 'standard', but nothing ever wrote a 'standard'
 * row end-to-end -- CUDly stamps 'convertible' on its own EC2 purchases, and
 * externally-created Standard RIs arrive with an EMPTY offering_class until the
 * backend lazily populates it from AWS on the list call. So the button never
 * rendered for the very case the feature targets.
 *
 * The fix shows the Sell button for completed AWS EC2 rows whose offering_class
 * is 'standard' OR still unknown (empty), and lets the backend
 * populateOfferingClass + gate (it 400s a fetched 'convertible') decide. The
 * backend authorizeSessionSell remains the real security boundary; these tests
 * only verify the UX gate.
 *
 * Tested matrix:
 *   1. completed aws/ec2 row, offering_class 'standard'         -> shown.
 *   2. completed aws/ec2 row, offering_class EMPTY (unknown)    -> shown (fix).
 *   3. completed aws/ec2 row, offering_class 'convertible'      -> hidden.
 *   4. completed aws/ec2 row with an active listing             -> hidden.
 *   5. completed aws/rds row, offering_class EMPTY              -> hidden.
 *   6. completed azure/compute row, offering_class EMPTY        -> hidden.
 *   7. anonymous (no current user cached)                       -> hidden.
 */

import { loadHistory } from '../history';

jest.mock('../api', () => ({
  getHistory: jest.fn(),
  createMarketplaceListing: jest.fn(),
  cancelMarketplaceListing: jest.fn(),
}));

jest.mock('../navigation', () => ({
  switchTab: jest.fn(),
}));

jest.mock('../utils', () => ({
  formatCurrency: jest.fn((val) => `$${val || 0}`),
  formatDate: jest.fn((val) => (val ? new Date(val).toLocaleDateString() : '')),
  formatTerm: jest.fn((years) => (years == null ? '' : `${years} Year${years === 1 ? '' : 's'}`)),
  escapeHtml: jest.fn((str) => str || ''),
  escapeHtmlAttr: jest.fn((str) => str || ''),
  amortizedMonthly: jest.fn((monthly) => monthly),
  populateAccountFilter: jest.fn(() => Promise.resolve()),
}));

jest.mock('../confirmDialog', () => ({
  confirmDialog: jest.fn(),
}));

jest.mock('../toast', () => ({
  showToast: jest.fn(),
}));

jest.mock('../state', () => ({
  getCurrentUser: jest.fn(),
  getCurrentProvider: jest.fn().mockReturnValue(''),
  setCurrentProvider: jest.fn(),
  getCurrentAccountIDs: jest.fn().mockReturnValue([]),
  setCurrentAccountIDs: jest.fn(),
  subscribeProvider: jest.fn().mockReturnValue(() => {}),
  subscribeAccount: jest.fn().mockReturnValue(() => {}),
  getAmortizeUpfront: jest.fn().mockReturnValue(false),
  setAmortizeUpfront: jest.fn(),
  subscribeAmortizeUpfront: jest.fn().mockReturnValue(() => {}),
}));

import * as api from '../api';
import { getCurrentUser } from '../state';

// Administrators group GUID -- mirrors ADMINISTRATORS_GROUP_ID in
// frontend/src/permissions.ts. Without this, isAdmin() returns false and
// canAccess('admin', '*') in canSellOnMarketplace's fallback branch does not
// grant. Seeded-group GUID, not the label string.
const ADMIN_GROUP_ID = '00000000-0000-5000-8000-000000000001';
const ADMIN_USER = { id: 'admin-uuid', email: 'admin@example.com', groups: [ADMIN_GROUP_ID] };

// A recent purchase with plenty of term left so the remaining-months guard in
// canSellOnMarketplace (needs >= 1 month remaining) always passes.
const RECENT = new Date(Date.now() - 30 * 24 * 60 * 60 * 1000).toISOString(); // -30 days

function setupDOM(): void {
  while (document.body.firstChild) document.body.removeChild(document.body.firstChild);

  const mkInput = (id: string): HTMLInputElement => {
    const el = document.createElement('input');
    el.type = 'date';
    el.id = id;
    return el;
  };
  const mkSelect = (id: string): HTMLSelectElement => {
    const el = document.createElement('select');
    el.id = id;
    const opt = document.createElement('option');
    opt.value = '';
    opt.textContent = 'All';
    el.appendChild(opt);
    return el;
  };
  const mkDiv = (id: string): HTMLDivElement => {
    const el = document.createElement('div');
    el.id = id;
    return el;
  };

  document.body.appendChild(mkInput('history-start'));
  document.body.appendChild(mkInput('history-end'));
  document.body.appendChild(mkSelect('history-provider-filter'));
  document.body.appendChild(mkSelect('history-account-filter'));
  document.body.appendChild(mkDiv('history-summary'));
  document.body.appendChild(mkDiv('history-list'));
  document.body.appendChild(mkDiv('purchases-approval-queue'));
}

// A completed AWS EC2 RI row with a long remaining term. Overrides tune the
// offering_class / provider / service / listing_state for each case.
function makeRow(overrides: Record<string, unknown>) {
  return {
    purchase_id: 'ri-1',
    timestamp: RECENT,
    provider: 'aws',
    service: 'ec2',
    resource_type: 't3.large',
    region: 'us-east-1',
    count: 1,
    term: 36,
    upfront_cost: 1200,
    estimated_savings: 300,
    plan_name: '',
    status: 'completed',
    offering_class: 'standard',
    ...overrides,
  };
}

function sellIds(): (string | undefined)[] {
  const list = document.getElementById('history-list')!;
  const buttons = list.querySelectorAll<HTMLButtonElement>('.history-marketplace-sell-btn');
  return Array.from(buttons).map((b) => b.dataset['marketplaceSellId']);
}

describe('History inline Sell on Marketplace button (issue #292)', () => {
  beforeEach(() => {
    setupDOM();
    jest.clearAllMocks();
    (getCurrentUser as jest.Mock).mockReturnValue(ADMIN_USER);
  });

  test('shows Sell for a completed AWS EC2 row with offering_class=standard', async () => {
    (api.getHistory as jest.Mock).mockResolvedValue({
      summary: {},
      purchases: [makeRow({ purchase_id: 'ri-standard', offering_class: 'standard' })],
    });

    await loadHistory();

    expect(sellIds()).toEqual(['ri-standard']);
  });

  test('shows Sell for a completed AWS EC2 row with EMPTY offering_class (the reachability fix)', async () => {
    (api.getHistory as jest.Mock).mockResolvedValue({
      summary: {},
      purchases: [makeRow({ purchase_id: 'ri-unknown', offering_class: '' })],
    });

    await loadHistory();

    // The backend lazily populates + gates; the UI must surface the button so
    // an externally-created Standard RI is actually listable end-to-end.
    expect(sellIds()).toEqual(['ri-unknown']);
  });

  test('hides Sell for a known convertible RI', async () => {
    (api.getHistory as jest.Mock).mockResolvedValue({
      summary: {},
      purchases: [makeRow({ purchase_id: 'ri-convertible', offering_class: 'convertible' })],
    });

    await loadHistory();

    expect(sellIds()).toEqual([]);
  });

  test('hides Sell when a listing is already active (Cancel is shown instead)', async () => {
    (api.getHistory as jest.Mock).mockResolvedValue({
      summary: {},
      purchases: [
        makeRow({ purchase_id: 'ri-active', offering_class: 'standard', listing_state: 'active', listing_id: 'ril-1' }),
      ],
    });

    await loadHistory();

    expect(sellIds()).toEqual([]);
  });

  test('hides Sell for a non-EC2 AWS row even when offering_class is unknown', async () => {
    (api.getHistory as jest.Mock).mockResolvedValue({
      summary: {},
      purchases: [makeRow({ purchase_id: 'rds-unknown', service: 'rds', offering_class: '' })],
    });

    await loadHistory();

    expect(sellIds()).toEqual([]);
  });

  test('hides Sell for a non-AWS row even when offering_class is unknown', async () => {
    (api.getHistory as jest.Mock).mockResolvedValue({
      summary: {},
      purchases: [
        makeRow({ purchase_id: 'az-unknown', provider: 'azure', service: 'compute', offering_class: '' }),
      ],
    });

    await loadHistory();

    expect(sellIds()).toEqual([]);
  });

  test('hides Sell when no user is cached (anonymous)', async () => {
    (getCurrentUser as jest.Mock).mockReturnValue(null);
    (api.getHistory as jest.Mock).mockResolvedValue({
      summary: {},
      purchases: [makeRow({ purchase_id: 'ri-anon', offering_class: '' })],
    });

    await loadHistory();

    expect(sellIds()).toEqual([]);
  });
});
