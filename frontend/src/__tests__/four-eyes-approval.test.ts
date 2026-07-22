/**
 * 4-eyes approval mode tests (issue #1005).
 *
 * GlobalConfig.require_different_approver gates the inline Approve button so
 * a session cannot approve a purchase execution it created itself, mirroring
 * the backend's requireDifferentApprover in internal/api/handler_purchases.go.
 * This is a UX gate only -- the backend remains the security boundary; these
 * tests verify the button/badge rendering, not the API enforcement.
 */

import { loadHistory } from '../history';

jest.mock('../api', () => ({
  getHistory: jest.fn(),
  getConfig: jest.fn(),
  approvePurchase: jest.fn(),
  cancelPurchase: jest.fn(),
}));

jest.mock('../navigation', () => ({
  switchTab: jest.fn(),
}));

jest.mock('../utils', () => ({
  formatCurrency: jest.fn((val) => `$${val || 0}`),
  formatDate: jest.fn((val) => (val ? new Date(val).toLocaleDateString() : '')),
  formatTerm: jest.fn((years) => (years == null ? '' : `${years} Year${years === 1 ? '' : 's'}`)),
  escapeHtml: jest.fn((str) => str || ''),
  escapeHtmlAttr: jest.fn((str: string | null | undefined) => {
    if (!str) return '';
    return str.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;').replace(/'/g, '&#39;');
  }),
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
  getPurchaseHistoryColumnFilters: jest.fn().mockReturnValue({}),
  setPurchaseHistoryColumnFilter: jest.fn(),
  clearAllPurchaseHistoryColumnFilters: jest.fn(),
  getApprovalQueueColumnFilters: jest.fn().mockReturnValue({}),
  setApprovalQueueColumnFilter: jest.fn(),
  clearAllApprovalQueueColumnFilters: jest.fn(),
}));

import * as api from '../api';
import { getCurrentUser } from '../state';
import { ADMINISTRATORS_GROUP_ID, PURCHASER_GROUP_ID } from '../permissions';

// Admin with approve-any:purchases (carved out of admin:* per issue #923;
// requires Purchaser group membership). Four-eyes must still block
// self-approval for this user -- the admin wildcard is NOT exempt.
const ADMIN_USER = { id: 'admin-uuid', email: 'admin@example.com', groups: [ADMINISTRATORS_GROUP_ID, PURCHASER_GROUP_ID] };

// Regular user with approve-own (but not approve-any) on purchases.
const REG_USER = {
  id: 'user-uuid',
  email: 'user@example.com',
  groups: [],
  effectivePermissions: [
    { action: 'approve-own', resource: 'purchases' },
    { action: 'cancel-own', resource: 'purchases' },
    { action: 'retry-own', resource: 'purchases' },
    { action: 'view', resource: 'history' },
  ],
};
const OTHER_UUID = 'other-uuid';

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

  // Banner toggled by loadHistory() based on GlobalConfig.require_different_approver.
  const banner = document.createElement('div');
  banner.id = 'four-eyes-banner';
  banner.className = 'info-banner hidden';
  document.body.appendChild(banner);
}

function makeRow(overrides: Record<string, unknown>) {
  return {
    purchase_id: 'exec-1',
    timestamp: '2024-01-15T00:00:00Z',
    provider: 'aws',
    service: 'ec2',
    resource_type: 't3.medium',
    region: 'us-east-1',
    count: 1,
    term: 1,
    upfront_cost: 100,
    estimated_savings: 50,
    plan_name: '',
    status: 'pending',
    ...overrides,
  };
}

describe('4-eyes approval mode (issue #1005)', () => {
  beforeEach(() => {
    setupDOM();
    jest.clearAllMocks();
  });

  test('Approve hidden when mode on + own row', async () => {
    (getCurrentUser as jest.Mock).mockReturnValue(REG_USER);
    (api.getConfig as jest.Mock).mockResolvedValue({ global: { require_different_approver: true } });
    (api.getHistory as jest.Mock).mockResolvedValue({
      summary: {},
      purchases: [makeRow({ purchase_id: 'exec-own', created_by_user_id: REG_USER.id })],
    });

    await loadHistory();

    const list = document.getElementById('history-list')!;
    expect(list.querySelectorAll('.history-approve-btn')).toHaveLength(0);
  });

  test('Approve shown when mode on + other row', async () => {
    // ADMIN_USER holds approve-any, so RBAC alone would allow approving any
    // row; this isolates the 4-eyes overlay: creator (OTHER_UUID) differs
    // from the session (ADMIN_USER.id), so canApproveUnder4Eyes allows it.
    (getCurrentUser as jest.Mock).mockReturnValue(ADMIN_USER);
    (api.getConfig as jest.Mock).mockResolvedValue({ global: { require_different_approver: true } });
    (api.getHistory as jest.Mock).mockResolvedValue({
      summary: {},
      purchases: [makeRow({ purchase_id: 'exec-other', created_by_user_id: OTHER_UUID })],
    });

    await loadHistory();

    const list = document.getElementById('history-list')!;
    const buttons = list.querySelectorAll<HTMLButtonElement>('.history-approve-btn');
    expect(buttons).toHaveLength(1);
    expect(buttons[0]?.dataset['approveId']).toBe('exec-other');
  });

  test('Approve shown when mode off', async () => {
    (getCurrentUser as jest.Mock).mockReturnValue(REG_USER);
    (api.getConfig as jest.Mock).mockResolvedValue({ global: { require_different_approver: false } });
    (api.getHistory as jest.Mock).mockResolvedValue({
      summary: {},
      purchases: [makeRow({ purchase_id: 'exec-own', created_by_user_id: REG_USER.id })],
    });

    await loadHistory();

    const list = document.getElementById('history-list')!;
    const buttons = list.querySelectorAll<HTMLButtonElement>('.history-approve-btn');
    expect(buttons).toHaveLength(1);
    expect(buttons[0]?.dataset['approveId']).toBe('exec-own');
    // Banner must stay hidden when mode is off.
    expect(document.getElementById('four-eyes-banner')?.classList.contains('hidden')).toBe(true);
  });

  test('badge shown when button hidden by mode (admin approve-any self-approval)', async () => {
    // Admin holds approve-any, which would normally show Approve on every
    // pending row regardless of creator. Four-eyes still blocks self-approval
    // -- the admin wildcard is NOT exempt -- so the badge must render instead.
    (getCurrentUser as jest.Mock).mockReturnValue(ADMIN_USER);
    (api.getConfig as jest.Mock).mockResolvedValue({ global: { require_different_approver: true } });
    (api.getHistory as jest.Mock).mockResolvedValue({
      summary: {},
      purchases: [makeRow({ purchase_id: 'exec-own', created_by_user_id: ADMIN_USER.id })],
    });

    await loadHistory();

    const list = document.getElementById('history-list')!;
    expect(list.querySelectorAll('.history-approve-btn')).toHaveLength(0);
    const badge = list.querySelector('.badge-muted');
    expect(badge).not.toBeNull();
    expect(badge?.textContent).toBe('Awaiting different approver');
    // Banner must show when mode is on.
    expect(document.getElementById('four-eyes-banner')?.classList.contains('hidden')).toBe(false);
  });

  test('null creator (legacy row): approve hidden', async () => {
    (getCurrentUser as jest.Mock).mockReturnValue(REG_USER);
    (api.getConfig as jest.Mock).mockResolvedValue({ global: { require_different_approver: true } });
    (api.getHistory as jest.Mock).mockResolvedValue({
      summary: {},
      purchases: [makeRow({ purchase_id: 'exec-legacy', created_by_user_id: undefined })],
    });

    await loadHistory();

    const list = document.getElementById('history-list')!;
    expect(list.querySelectorAll('.history-approve-btn')).toHaveLength(0);
  });
});
