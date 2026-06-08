/**
 * History inline Revoke button tests (issue #290).
 *
 * Regression guard for the "dead Revoke button" gap: canRevokeCompletedRow
 * gates the inline Revoke button on `revocation_window_closes_at`, which the
 * backend now stamps at purchase-write time for Azure rows. Before that stamp
 * existed, the field was always absent and the button never rendered on real
 * rows.
 *
 * The backend authorizeSessionRevoke remains the real security boundary; these
 * tests only verify the UX gate (don't render a button users can't use, do
 * render it when the row is genuinely revocable).
 *
 * Tested matrix:
 *   1. completed Azure row WITH a future revocation_window_closes_at -> shown.
 *   2. completed Azure row WITHOUT revocation_window_closes_at -> hidden.
 *   3. completed Azure row with a PAST revocation_window_closes_at -> hidden.
 *   4. already-revoked Azure row (revoked_at set) -> hidden.
 *   5. non-Azure (aws/gcp) row with a window stamped -> hidden.
 *   6. anonymous (no current user cached) -> hidden.
 */

import { loadHistory } from '../history';

jest.mock('../api', () => ({
  getHistory: jest.fn(),
  revokePurchase: jest.fn(),
}));

jest.mock('../navigation', () => ({
  switchTab: jest.fn(),
}));

jest.mock('../utils', () => ({
  formatCurrency: jest.fn((val) => `$${val || 0}`),
  formatDate: jest.fn((val) => (val ? new Date(val).toLocaleDateString() : '')),
  formatTerm: jest.fn((years) => (years == null ? '' : `${years} Year${years === 1 ? '' : 's'}`)),
  escapeHtml: jest.fn((str) => str || ''),
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
}));

import * as api from '../api';
import { getCurrentUser } from '../state';

// Administrators group GUID -- mirrors ADMINISTRATORS_GROUP_ID in
// frontend/src/permissions.ts. Without this, isAdmin() returns false and
// canAccess('admin', '*') / canAccess('revoke-any', 'purchases') in the
// fallback branch don't grant. Seeded-group GUID, not the label string.
const ADMIN_GROUP_ID = '00000000-0000-5000-8000-000000000001';
const ADMIN_USER = { id: 'admin-uuid', email: 'admin@example.com', groups: [ADMIN_GROUP_ID] };
// Plain authenticated user with no revoke verbs in their effective set --
// used by the RBAC regression test below.
const NON_REVOKER_USER = {
  id: 'plain-uuid',
  email: 'plain@example.com',
  groups: [],
  effectivePermissions: [
    { action: 'view', resource: 'history' },
  ],
};

const FUTURE = new Date(Date.now() + 5 * 24 * 60 * 60 * 1000).toISOString(); // +5 days
const PAST = new Date(Date.now() - 1 * 60 * 60 * 1000).toISOString(); // -1 hour

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

function makeRow(overrides: Record<string, unknown>) {
  return {
    purchase_id: 'commit-1',
    timestamp: '2024-01-15T00:00:00Z',
    provider: 'azure',
    service: 'compute',
    resource_type: 'Standard_D2s_v3',
    region: 'eastus',
    count: 1,
    term: 1,
    upfront_cost: 100,
    estimated_savings: 50,
    plan_name: '',
    status: 'completed',
    ...overrides,
  };
}

function revokeIds(): (string | undefined)[] {
  const list = document.getElementById('history-list')!;
  const buttons = list.querySelectorAll<HTMLButtonElement>('.history-revoke-btn');
  return Array.from(buttons).map((b) => b.dataset['revokeId']);
}

describe('History inline Revoke button (issue #290)', () => {
  beforeEach(() => {
    setupDOM();
    jest.clearAllMocks();
    (getCurrentUser as jest.Mock).mockReturnValue(ADMIN_USER);
  });

  test('shows Revoke for a completed Azure row WITH a future revocation window', async () => {
    (api.getHistory as jest.Mock).mockResolvedValue({
      summary: {},
      purchases: [
        makeRow({ purchase_id: 'commit-azure', revocation_window_closes_at: FUTURE }),
      ],
    });

    await loadHistory();

    expect(revokeIds()).toEqual(['commit-azure']);
  });

  test('hides Revoke for a completed Azure row WITHOUT a revocation window', async () => {
    (api.getHistory as jest.Mock).mockResolvedValue({
      summary: {},
      purchases: [
        makeRow({ purchase_id: 'commit-no-window' }), // revocation_window_closes_at absent
      ],
    });

    await loadHistory();

    expect(revokeIds()).toEqual([]);
  });

  test('hides Revoke once the stamped window has closed', async () => {
    (api.getHistory as jest.Mock).mockResolvedValue({
      summary: {},
      purchases: [
        makeRow({ purchase_id: 'commit-closed', revocation_window_closes_at: PAST }),
      ],
    });

    await loadHistory();

    expect(revokeIds()).toEqual([]);
  });

  test('hides Revoke for an already-revoked row', async () => {
    (api.getHistory as jest.Mock).mockResolvedValue({
      summary: {},
      purchases: [
        makeRow({
          purchase_id: 'commit-revoked',
          revocation_window_closes_at: FUTURE,
          revoked_at: '2024-01-16T00:00:00Z',
        }),
      ],
    });

    await loadHistory();

    expect(revokeIds()).toEqual([]);
  });

  test('hides Revoke for non-Azure rows even with a window stamped', async () => {
    (api.getHistory as jest.Mock).mockResolvedValue({
      summary: {},
      purchases: [
        makeRow({ purchase_id: 'commit-aws', provider: 'aws', revocation_window_closes_at: FUTURE }),
        makeRow({ purchase_id: 'commit-gcp', provider: 'gcp', revocation_window_closes_at: FUTURE }),
      ],
    });

    await loadHistory();

    expect(revokeIds()).toEqual([]);
  });

  test('hides Revoke when no user is cached (anonymous)', async () => {
    (getCurrentUser as jest.Mock).mockReturnValue(null);
    (api.getHistory as jest.Mock).mockResolvedValue({
      summary: {},
      purchases: [
        makeRow({ purchase_id: 'commit-anon', revocation_window_closes_at: FUTURE }),
      ],
    });

    await loadHistory();

    expect(revokeIds()).toEqual([]);
  });

  // Regression guard for the missing RBAC gate (PR #804 review pass).
  // canRevokeCompletedRow previously returned true for any signed-in user --
  // the button rendered, then the backend 403d on click, replicating the
  // same UX-vs-RBAC drift PR #995 caught for the approve / delete paths.
  // canCancelPendingRow / canApprovePendingRow / canRetryFailedRow all check
  // canAccess; canRevokeCompletedRow must do the same. With an explicit
  // effectivePermissions set lacking revoke-* the button must be hidden.
  test('hides Revoke when the session has no revoke-* permission', async () => {
    (getCurrentUser as jest.Mock).mockReturnValue(NON_REVOKER_USER);
    (api.getHistory as jest.Mock).mockResolvedValue({
      summary: {},
      purchases: [
        makeRow({ purchase_id: 'commit-noperms', revocation_window_closes_at: FUTURE }),
      ],
    });

    await loadHistory();

    expect(revokeIds()).toEqual([]);
  });

  // Regression guard: legacy rows written before the status column existed have
  // status='' (empty string). canRevokeCompletedRow must treat blank status the
  // same as "completed" so these rows remain revocable for Azure.
  test('shows Revoke for a legacy blank-status Azure row with a future revocation window', async () => {
    (api.getHistory as jest.Mock).mockResolvedValue({
      summary: {},
      purchases: [
        makeRow({
          status: '',
          provider: 'azure',
          purchase_id: 'commit-legacy-blank-status',
          revocation_window_closes_at: FUTURE,
        }),
      ],
    });

    await loadHistory();

    expect(revokeIds()).toContain('commit-legacy-blank-status');
  });

  // Regression guard: Gmail-style pre-fire delay creates rows with
  // status='scheduled' (cloud SDK not yet called). canRevokeCompletedRow must
  // accept this status so the Revoke button renders before the execution fires
  // (issue #290, second-wave CR Findings E + G).
  test('shows Revoke for a scheduled Azure row with a future revocation window', async () => {
    (api.getHistory as jest.Mock).mockResolvedValue({
      summary: {},
      purchases: [
        makeRow({
          status: 'scheduled',
          provider: 'azure',
          purchase_id: 'commit-scheduled',
          revocation_window_closes_at: FUTURE,
        }),
      ],
    });

    await loadHistory();

    expect(revokeIds()).toContain('commit-scheduled');
  });
});
