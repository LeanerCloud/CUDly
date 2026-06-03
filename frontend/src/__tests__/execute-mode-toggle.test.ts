/**
 * Execute-mode toggle tests (issue #289).
 *
 * Verifies that the purchase modal shows the direct-execute toggle only
 * when the session holds execute-any:purchases or execute-own:purchases,
 * and that the toggle is absent (not rendered) for sessions that only
 * hold the base execute:purchases verb or have no permissions at all.
 */

import { openPurchaseModal, getExecuteMode } from '../recommendations';

jest.mock('../api', () => ({
  getRecommendations: jest.fn().mockResolvedValue({ summary: {}, recommendations: [], regions: [] }),
  refreshRecommendations: jest.fn(),
  getConfig: jest.fn().mockResolvedValue({ global: {} }),
  listAccounts: jest.fn().mockResolvedValue([]),
  listAccountServiceOverrides: jest.fn().mockResolvedValue([]),
}));

jest.mock('../api/recommendations', () => ({
  getRecommendationsFreshness: jest.fn().mockResolvedValue({
    last_collected_at: new Date().toISOString(),
    last_collection_error: null,
  }),
  refreshRecommendations: jest.fn().mockResolvedValue({}),
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
  getCurrentUser: jest.fn(),
}));

jest.mock('../toast', () => ({
  showToast: jest.fn().mockReturnValue({ dismiss: jest.fn() }),
}));

import * as state from '../state';

type UserRole = 'admin' | 'user' | 'readonly';
const mockUser = (role: UserRole | null) => {
  (state.getCurrentUser as jest.Mock).mockReturnValue(
    role === null ? null : { id: 'u', email: 'u@example.com', role },
  );
};

const minimalRec = {
  id: 'r1',
  provider: 'aws',
  service: 'ec2',
  region: 'us-east-1',
  resource_type: 'm5.xlarge',
  engine: '',
  count: 1,
  term: 1,
  payment: 'all-upfront',
  upfront_cost: 1000,
  savings: 200,
  selected: false,
  purchased: false,
};

const setupPurchaseModal = () => {
  const modal = document.createElement('div');
  modal.id = 'purchase-modal';
  modal.setAttribute('aria-modal', 'true');
  const details = document.createElement('div');
  details.id = 'purchase-details';
  modal.appendChild(details);
  document.body.appendChild(modal);

  // execute-purchase-btn is referenced in updateExecuteMode
  const btn = document.createElement('button');
  btn.id = 'execute-purchase-btn';
  btn.textContent = 'Send for Approval';
  document.body.appendChild(btn);

  return { modal, details };
};

describe('Execute-mode toggle (issue #289)', () => {
  beforeEach(() => {
    jest.clearAllMocks();
    document.body.innerHTML = '';
  });

  test('admin sees the execute-mode toggle in the purchase modal', async () => {
    mockUser('admin');
    setupPurchaseModal();
    await openPurchaseModal([minimalRec as never]);
    const details = document.getElementById('purchase-details')!;
    const toggle = details.querySelector('.execute-mode-toggle');
    expect(toggle).not.toBeNull();
    // Both radio buttons must be present
    expect(details.querySelector('#execute-mode-approval')).not.toBeNull();
    expect(details.querySelector('#execute-mode-direct')).not.toBeNull();
  });

  test('user without execute-own/execute-any sees only the approval note (no toggle)', async () => {
    mockUser('user');
    setupPurchaseModal();
    await openPurchaseModal([minimalRec as never]);
    const details = document.getElementById('purchase-details')!;
    expect(details.querySelector('.execute-mode-toggle')).toBeNull();
    expect(details.querySelector('.approval-required-note')).not.toBeNull();
  });

  test('readonly user sees only the approval note (no toggle)', async () => {
    mockUser('readonly');
    setupPurchaseModal();
    await openPurchaseModal([minimalRec as never]);
    const details = document.getElementById('purchase-details')!;
    expect(details.querySelector('.execute-mode-toggle')).toBeNull();
    expect(details.querySelector('.approval-required-note')).not.toBeNull();
  });

  test('getExecuteMode defaults to "" (approval path) after modal open', async () => {
    mockUser('admin');
    setupPurchaseModal();
    await openPurchaseModal([minimalRec as never]);
    expect(getExecuteMode()).toBe('');
  });

  test('selecting Execute Now radio sets execute mode to "direct"', async () => {
    mockUser('admin');
    setupPurchaseModal();
    await openPurchaseModal([minimalRec as never]);

    const directRadio = document.getElementById('execute-mode-direct') as HTMLInputElement;
    expect(directRadio).not.toBeNull();
    directRadio.click();
    directRadio.dispatchEvent(new Event('change', { bubbles: true }));

    expect(getExecuteMode()).toBe('direct');

    // The submit button label should update.
    const btn = document.getElementById('execute-purchase-btn') as HTMLButtonElement;
    expect(btn.textContent).toBe('Execute Purchase Now');
  });

  test('switching back to Send for Approval resets execute mode', async () => {
    mockUser('admin');
    setupPurchaseModal();
    await openPurchaseModal([minimalRec as never]);

    // Switch to direct
    const directRadio = document.getElementById('execute-mode-direct') as HTMLInputElement;
    directRadio.click();
    directRadio.dispatchEvent(new Event('change', { bubbles: true }));
    expect(getExecuteMode()).toBe('direct');

    // Switch back to approval
    const approvalRadio = document.getElementById('execute-mode-approval') as HTMLInputElement;
    approvalRadio.click();
    approvalRadio.dispatchEvent(new Event('change', { bubbles: true }));
    expect(getExecuteMode()).toBe('');

    const btn = document.getElementById('execute-purchase-btn') as HTMLButtonElement;
    expect(btn.textContent).toBe('Send for Approval');
  });
});
