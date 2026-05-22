/**
 * Tests for handleExecutePurchase / handleFanOutExecute toast messages.
 *
 * CR pass on PR #294 / issue #288:
 *   Finding 1 — success toasts must name the approval recipient when the
 *               backend returns approval_recipient.
 *   Finding 2 — fulfilled executePurchase responses with email_sent === false
 *               or status === 'failed' must count as failures, not successes.
 */

// ── mocks (must precede imports) ─────────────────────────────────────────────

jest.mock('../api', () => ({
  initAuth: jest.fn(),
  isAuthenticated: jest.fn(),
  getCurrentUser: jest.fn(),
  executePurchase: jest.fn(),
}));

jest.mock('../state', () => ({
  setCurrentUser: jest.fn(),
  setCurrentProvider: jest.fn(),
}));

jest.mock('../auth', () => ({
  showLoginModal: jest.fn(),
  updateUserUI: jest.fn(),
}));

jest.mock('../dashboard', () => ({
  loadDashboard: jest.fn().mockResolvedValue(undefined),
  setupDashboardHandlers: jest.fn(),
}));

jest.mock('../navigation', () => ({
  switchTab: jest.fn(),
  applyTabFromPath: jest.fn().mockReturnValue('dashboard'),
  initRouter: jest.fn(),
  switchSettingsSubTab: jest.fn(),
  getSettingsSubTabFromPath: jest.fn().mockReturnValue('general'),
}));

jest.mock('../recommendations', () => ({
  setupRecommendationsHandlers: jest.fn(),
  getPurchaseModalRecommendations: jest.fn(),
  clearPurchaseModalRecommendations: jest.fn(),
  getFanOutBuckets: jest.fn(),
  clearFanOutBuckets: jest.fn(),
}));

jest.mock('../plans', () => ({
  savePlan: jest.fn(),
  setupPlanHandlers: jest.fn(),
  closePlanModal: jest.fn(),
  openNewPlanModal: jest.fn(),
  closePurchaseModal: jest.fn(),
}));

jest.mock('../settings', () => ({
  saveGlobalSettings: jest.fn(),
  setupSettingsHandlers: jest.fn(),
  resetSettings: jest.fn(),
}));

jest.mock('../riexchange', () => ({
  setupRIExchangeHandlers: jest.fn(),
  saveAutomationSettings: jest.fn(),
}));

jest.mock('../users', () => ({
  setupUserHandlers: jest.fn(),
}));

jest.mock('../apikeys', () => ({
  initApiKeys: jest.fn(),
}));

jest.mock('../history', () => ({
  loadHistory: jest.fn(),
}));

jest.mock('../modules/savings-history', () => ({
  initSavingsHistory: jest.fn(),
}));

jest.mock('../purchases-deeplink', () => ({
  handlePurchaseDeeplink: jest.fn(),
}));

jest.mock('../modal', () => ({
  closeModal: jest.fn(),
}));

// confirmDialog defaults to true (user confirms); individual tests can override
jest.mock('../confirmDialog', () => ({
  confirmDialog: jest.fn().mockResolvedValue(true),
}));

// Archera offer modal: after issue #499 follow-up the offer must fire on the
// success path of the approval-submission call (right after the user approves
// the pre-purchase confirmation), not after the async execution completes.
jest.mock('../archera', () => ({
  handleArcheraDeeplink: jest.fn(),
  openArcheraOfferModal: jest.fn(),
}));

// ── imports ───────────────────────────────────────────────────────────────────

import { setupEventListeners } from '../app';
import * as api from '../api';
import * as recs from '../recommendations';
import * as plans from '../plans';
import * as archera from '../archera';

// ── helpers ───────────────────────────────────────────────────────────────────

function buildMinimalRec() {
  return {
    id: 'rec-1',
    provider: 'aws' as const,
    service: 'ec2',
    region: 'us-east-1',
    resource_type: 't3.medium',
    count: 1,
    term: 1,
    payment: 'all-upfront',
    upfront_cost: 100,
    monthly_cost: 10,
    savings: 20,
    selected: true,
    purchased: false,
  };
}

/**
 * Mount a minimal DOM using safe DOM API methods and wire up event listeners.
 * Returns the execute-purchase button so tests can click it.
 */
function setup(): HTMLButtonElement {
  const modal = document.createElement('div');
  modal.id = 'purchase-modal';
  document.body.appendChild(modal);

  const btn = document.createElement('button');
  btn.id = 'execute-purchase-btn';
  btn.textContent = 'Send for Approval';
  document.body.appendChild(btn);

  const capacityInput = document.createElement('input');
  capacityInput.id = 'bulk-purchase-capacity';
  capacityInput.value = '100';
  document.body.appendChild(capacityInput);

  setupEventListeners();
  return btn;
}

/** Return the text content of the most-recently rendered toast message node. */
function lastToastMessage(): string | null {
  const container = document.getElementById('toast-container');
  if (!container) return null;
  const toasts = container.querySelectorAll('.toast');
  if (toasts.length === 0) return null;
  const last = toasts[toasts.length - 1];
  return last?.querySelector('.toast-message')?.textContent ?? last?.textContent ?? null;
}

/** Return the kind class of the most recently rendered toast (success/error/warning). */
function lastToastKind(): string | null {
  const container = document.getElementById('toast-container');
  if (!container) return null;
  const toasts = container.querySelectorAll('.toast');
  if (toasts.length === 0) return null;
  const last = toasts[toasts.length - 1];
  for (const cls of Array.from(last?.classList ?? [])) {
    const m = cls.match(/^toast--(.+)$/);
    if (m) return m[1] ?? null;
  }
  return null;
}

// ── tests ─────────────────────────────────────────────────────────────────────

describe('handleExecutePurchase — single-record path', () => {
  beforeEach(() => {
    jest.clearAllMocks();
    // Default: no fan-out buckets, one single-record rec
    (recs.getFanOutBuckets as jest.Mock).mockReturnValue([]);
    (recs.getPurchaseModalRecommendations as jest.Mock).mockReturnValue([buildMinimalRec()]);
    (plans.closePurchaseModal as jest.Mock).mockImplementation(() => undefined);
  });

  afterEach(() => {
    document.body.textContent = '';
  });

  test('Finding 1 — with approval_recipient: toast shows recipient name', async () => {
    (api.executePurchase as jest.Mock).mockResolvedValue({
      execution_id: 'exec-1',
      status: 'queued',
      email_sent: true,
      approval_recipient: 'approver@example.com',
    });

    const btn = setup();
    btn.click();
    await new Promise((r) => setTimeout(r, 0));

    expect(lastToastMessage()).toContain('approver@example.com');
    expect(lastToastMessage()).toContain('Approval request sent to');
    expect(lastToastKind()).toBe('success');
  });

  test('Finding 1 — without approval_recipient: toast falls back to generic copy', async () => {
    (api.executePurchase as jest.Mock).mockResolvedValue({
      execution_id: 'exec-2',
      status: 'queued',
      email_sent: true,
      // approval_recipient intentionally absent
    });

    const btn = setup();
    btn.click();
    await new Promise((r) => setTimeout(r, 0));

    const msg = lastToastMessage();
    expect(msg).toContain('check your email');
    expect(lastToastKind()).toBe('success');
  });

  test('email_sent === false: warning toast, not success', async () => {
    (api.executePurchase as jest.Mock).mockResolvedValue({
      execution_id: 'exec-3',
      status: 'queued',
      email_sent: false,
      email_reason: 'no notification email configured',
    });

    const btn = setup();
    btn.click();
    await new Promise((r) => setTimeout(r, 0));

    expect(lastToastKind()).toBe('warning');
    expect(lastToastMessage()).toContain('no notification email configured');
  });

  // Issue #499 follow-up: offer timing moved from post-execution to the
  // approval-submission success path (right after the user approves the
  // pre-purchase confirmation).
  test('opens Archera offer modal once the approval submission succeeds', async () => {
    (api.executePurchase as jest.Mock).mockResolvedValue({
      execution_id: 'exec-ok',
      status: 'queued',
      email_sent: true,
      approval_recipient: 'approver@example.com',
    });

    const btn = setup();
    btn.click();
    await new Promise((r) => setTimeout(r, 0));

    expect(archera.openArcheraOfferModal).toHaveBeenCalledTimes(1);
    expect(archera.openArcheraOfferModal).toHaveBeenCalledWith('purchase');
  });

  test('does NOT open Archera offer modal when the approval submission throws', async () => {
    (api.executePurchase as jest.Mock).mockRejectedValue(new Error('boom'));

    const btn = setup();
    btn.click();
    await new Promise((r) => setTimeout(r, 0));

    expect(lastToastKind()).toBe('error');
    expect(archera.openArcheraOfferModal).not.toHaveBeenCalled();
  });

  // Issue #597: details must be preserved in the POST body on the single-rec path.
  test('#597 single-rec — details blob preserved for EC2 Windows rec', async () => {
    const windowsDetails = { platform: 'Windows', tenancy: 'dedicated', scope: 'Region' };
    (recs.getPurchaseModalRecommendations as jest.Mock).mockReturnValue([
      {
        id: 'rec-win-single',
        provider: 'aws' as const,
        service: 'ec2',
        region: 'us-east-1',
        resource_type: 'm5.xlarge',
        engine: undefined,
        details: windowsDetails,
        count: 1,
        term: 1,
        payment: 'all-upfront',
        upfront_cost: 400,
        monthly_cost: 40,
        savings: 80,
      },
    ]);

    (api.executePurchase as jest.Mock).mockResolvedValue({
      execution_id: 'exec-win-single',
      status: 'queued',
      email_sent: true,
      approval_recipient: 'approver@example.com',
    });

    const btn = setup();
    btn.click();
    await new Promise((r) => setTimeout(r, 0));

    expect(api.executePurchase).toHaveBeenCalledTimes(1);
    const [submittedRecs] = (api.executePurchase as jest.Mock).mock.calls[0] as [
      Array<{ details?: unknown }>,
      number,
    ];
    expect(submittedRecs[0]?.details).toEqual(windowsDetails);
  });

  // Issue #597: details must be preserved in the POST body on the single-rec path.
  test('#597 single-rec — details blob preserved for RDS Postgres rec', async () => {
    const rdsDetails = { engine: 'postgres', multi_az: false };
    (recs.getPurchaseModalRecommendations as jest.Mock).mockReturnValue([
      {
        id: 'rec-rds-single',
        provider: 'aws' as const,
        service: 'rds',
        region: 'ap-southeast-1',
        resource_type: 'db.r5.large',
        engine: 'postgres',
        details: rdsDetails,
        count: 1,
        term: 3,
        payment: 'no-upfront',
        upfront_cost: 0,
        monthly_cost: 120,
        savings: 90,
      },
    ]);

    (api.executePurchase as jest.Mock).mockResolvedValue({
      execution_id: 'exec-rds-single',
      status: 'queued',
      email_sent: true,
      approval_recipient: 'approver@example.com',
    });

    const btn = setup();
    btn.click();
    await new Promise((r) => setTimeout(r, 0));

    expect(api.executePurchase).toHaveBeenCalledTimes(1);
    const [submittedRecs] = (api.executePurchase as jest.Mock).mock.calls[0] as [
      Array<{ details?: unknown; engine?: string }>,
      number,
    ];
    expect(submittedRecs[0]?.details).toEqual(rdsDetails);
    expect(submittedRecs[0]?.engine).toBe('postgres');
  });
});

describe('handleFanOutExecute — fan-out path', () => {
  function buildBucket(id: string, capacityPercent = 100) {
    return {
      key: `key-${id}`,
      label: `Bucket ${id}`,
      provider: 'aws',
      service: `svc-${id}`,
      recs: [buildMinimalRec()],
      payment: 'all-upfront',
      capacityPercent,
    };
  }

  beforeEach(() => {
    jest.clearAllMocks();
    (recs.getPurchaseModalRecommendations as jest.Mock).mockReturnValue([]);
    (plans.closePurchaseModal as jest.Mock).mockImplementation(() => undefined);
  });

  afterEach(() => {
    document.body.textContent = '';
  });

  test('Finding 2 — email_sent === false counts as failure, email_reason in toast, recipient excluded', async () => {
    (recs.getFanOutBuckets as jest.Mock).mockReturnValue([
      buildBucket('a'),
      buildBucket('b'),
    ]);

    (api.executePurchase as jest.Mock)
      .mockResolvedValueOnce({
        execution_id: 'exec-a',
        status: 'queued',
        email_sent: true,
        approval_recipient: 'alice@example.com',
      })
      .mockResolvedValueOnce({
        execution_id: 'exec-b',
        status: 'queued',
        email_sent: false,
        email_reason: 'SMTP timeout',
        approval_recipient: 'bob@example.com',
      });

    const btn = setup();
    btn.click();
    await new Promise((r) => setTimeout(r, 0));

    const msg = lastToastMessage();
    // 1 of 2 succeeded; 1 failed (business-level email failure)
    expect(msg).toContain('1 of 2');
    expect(msg).toContain('failed');
    // The email_reason from the submission failure must appear in the message
    expect(msg).toContain('SMTP timeout');
    // bob's email_sent was false — must NOT appear as approved recipient
    expect(msg).not.toContain('bob@example.com');
    // Toast kind must reflect partial failure
    expect(lastToastKind()).toBe('warning');
  });

  test('#642 — partial fan-out failure names the submitted (still-actionable) buckets', async () => {
    (recs.getFanOutBuckets as jest.Mock).mockReturnValue([
      buildBucket('a'),
      buildBucket('b'),
    ]);

    // Bucket a submits cleanly (creates a pending execution); bucket b fails.
    (api.executePurchase as jest.Mock)
      .mockResolvedValueOnce({
        execution_id: 'exec-a',
        status: 'queued',
        email_sent: true,
        approval_recipient: 'alice@example.com',
      })
      .mockRejectedValueOnce(new Error('network down'));

    const btn = setup();
    btn.click();
    await new Promise((r) => setTimeout(r, 0));

    const msg = lastToastMessage();
    expect(msg).toContain('1 of 2');
    expect(msg).toContain('failed');
    // The orphaned-but-actionable pending execution must be surfaced by name so
    // the user knows which request is live (issue #642).
    expect(msg).toContain('Submitted (awaiting approval)');
    expect(msg).toContain('AWS svc-a@100%');
    // The failed bucket must NOT be listed as submitted.
    expect(msg).not.toContain('svc-b');
    expect(lastToastKind()).toBe('warning');
  });

  test('Finding 2 — status === "failed" also counts as failure, recipient excluded', async () => {
    (recs.getFanOutBuckets as jest.Mock).mockReturnValue([
      buildBucket('a'),
      buildBucket('b'),
    ]);

    (api.executePurchase as jest.Mock)
      .mockResolvedValueOnce({
        execution_id: 'exec-a',
        status: 'queued',
        email_sent: true,
        approval_recipient: 'alice@example.com',
      })
      .mockResolvedValueOnce({
        execution_id: 'exec-b',
        status: 'failed',
        email_sent: undefined,
        email_reason: 'backend error',
        approval_recipient: 'bob@example.com',
      });

    const btn = setup();
    btn.click();
    await new Promise((r) => setTimeout(r, 0));

    const msg = lastToastMessage();
    expect(msg).toContain('1 of 2');
    expect(msg).toContain('failed');
    expect(msg).toContain('backend error');
    expect(msg).not.toContain('bob@example.com');
    expect(lastToastKind()).toBe('warning');
  });

  test('Finding 1 fan-out — all succeed: success toast lists unique recipients', async () => {
    (recs.getFanOutBuckets as jest.Mock).mockReturnValue([
      buildBucket('a'),
      buildBucket('b'),
    ]);

    (api.executePurchase as jest.Mock)
      .mockResolvedValueOnce({
        execution_id: 'exec-a',
        status: 'queued',
        email_sent: true,
        approval_recipient: 'alice@example.com',
      })
      .mockResolvedValueOnce({
        execution_id: 'exec-b',
        status: 'queued',
        email_sent: true,
        approval_recipient: 'bob@example.com',
      });

    const btn = setup();
    btn.click();
    await new Promise((r) => setTimeout(r, 0));

    const msg = lastToastMessage();
    expect(lastToastKind()).toBe('success');
    expect(msg).toContain('alice@example.com');
    expect(msg).toContain('bob@example.com');
    expect(msg).toContain('sent for approval');
  });

  test('Finding 1 fan-out — deduplicates same recipient across buckets', async () => {
    (recs.getFanOutBuckets as jest.Mock).mockReturnValue([
      buildBucket('a'),
      buildBucket('b'),
    ]);

    (api.executePurchase as jest.Mock)
      .mockResolvedValueOnce({
        execution_id: 'exec-a',
        status: 'queued',
        email_sent: true,
        approval_recipient: 'shared@example.com',
      })
      .mockResolvedValueOnce({
        execution_id: 'exec-b',
        status: 'queued',
        email_sent: true,
        approval_recipient: 'shared@example.com',
      });

    const btn = setup();
    btn.click();
    await new Promise((r) => setTimeout(r, 0));

    const msg = lastToastMessage() ?? '';
    expect(lastToastKind()).toBe('success');
    // Address should appear exactly once after deduplication
    const occurrences = (msg.match(/shared@example\.com/g) ?? []).length;
    expect(occurrences).toBe(1);
  });

  test('Finding 2 — all email failures: error toast with reason', async () => {
    (recs.getFanOutBuckets as jest.Mock).mockReturnValue([
      buildBucket('a'),
    ]);

    (api.executePurchase as jest.Mock).mockResolvedValueOnce({
      execution_id: 'exec-a',
      status: 'queued',
      email_sent: false,
      email_reason: 'no SMTP config',
    });

    const btn = setup();
    btn.click();
    await new Promise((r) => setTimeout(r, 0));

    const msg = lastToastMessage();
    expect(lastToastKind()).toBe('error');
    expect(msg).toContain('no SMTP config');
    // Issue #499 follow-up: with zero successful submissions there is nothing
    // to insure, so the offer must not fire.
    expect(archera.openArcheraOfferModal).not.toHaveBeenCalled();
  });

  // Issue #499 follow-up: offer fires when at least one bucket's approval
  // submission succeeds, immediately after the user approves the confirmation.
  test('opens Archera offer modal when at least one bucket succeeds', async () => {
    (recs.getFanOutBuckets as jest.Mock).mockReturnValue([
      buildBucket('a'),
      buildBucket('b'),
    ]);

    (api.executePurchase as jest.Mock)
      .mockResolvedValueOnce({
        execution_id: 'exec-a',
        status: 'queued',
        email_sent: true,
        approval_recipient: 'alice@example.com',
      })
      .mockResolvedValueOnce({
        execution_id: 'exec-b',
        status: 'failed',
        email_reason: 'backend error',
      });

    const btn = setup();
    btn.click();
    await new Promise((r) => setTimeout(r, 0));

    expect(archera.openArcheraOfferModal).toHaveBeenCalledTimes(1);
    expect(archera.openArcheraOfferModal).toHaveBeenCalledWith('purchase');
  });

  // Issue #597: details must be preserved in the POST body on the fan-out path.
  // Windows EC2 rec with a non-trivial details blob.
  test('#597 fan-out — details blob preserved for EC2 Windows rec', async () => {
    const windowsDetails = { platform: 'Windows', tenancy: 'default', scope: 'Region' };
    (recs.getFanOutBuckets as jest.Mock).mockReturnValue([
      {
        key: 'key-win',
        label: 'Windows EC2',
        payment: 'all-upfront' as const,
        capacityPercent: 100,
        recs: [
          {
            id: 'rec-win-1',
            provider: 'aws' as const,
            service: 'ec2',
            region: 'us-east-1',
            resource_type: 'm5.large',
            engine: undefined,
            details: windowsDetails,
            count: 2,
            term: 1,
            payment: 'all-upfront',
            upfront_cost: 500,
            monthly_cost: 50,
            savings: 100,
          },
        ],
      },
    ]);

    (api.executePurchase as jest.Mock).mockResolvedValueOnce({
      execution_id: 'exec-win',
      status: 'queued',
      email_sent: true,
      approval_recipient: 'approver@example.com',
    });

    const btn = setup();
    btn.click();
    await new Promise((r) => setTimeout(r, 0));

    expect(api.executePurchase).toHaveBeenCalledTimes(1);
    const [submittedRecs] = (api.executePurchase as jest.Mock).mock.calls[0] as [
      Array<{ details?: unknown }>,
      number,
    ];
    expect(submittedRecs[0]?.details).toEqual(windowsDetails);
  });

  // Issue #597: details must be preserved in the POST body on the fan-out path.
  // RDS Postgres rec.
  test('#597 fan-out — details blob preserved for RDS Postgres rec', async () => {
    const postgresDetails = { engine: 'postgres', multi_az: true };
    (recs.getFanOutBuckets as jest.Mock).mockReturnValue([
      {
        key: 'key-rds',
        label: 'RDS Postgres',
        payment: 'partial-upfront' as const,
        capacityPercent: 100,
        recs: [
          {
            id: 'rec-rds-1',
            provider: 'aws' as const,
            service: 'rds',
            region: 'eu-west-1',
            resource_type: 'db.t3.medium',
            engine: 'postgres',
            details: postgresDetails,
            count: 1,
            term: 1,
            payment: 'partial-upfront',
            upfront_cost: 300,
            monthly_cost: 80,
            savings: 60,
          },
        ],
      },
    ]);

    (api.executePurchase as jest.Mock).mockResolvedValueOnce({
      execution_id: 'exec-rds',
      status: 'queued',
      email_sent: true,
      approval_recipient: 'approver@example.com',
    });

    const btn = setup();
    btn.click();
    await new Promise((r) => setTimeout(r, 0));

    expect(api.executePurchase).toHaveBeenCalledTimes(1);
    const [submittedRecs] = (api.executePurchase as jest.Mock).mock.calls[0] as [
      Array<{ details?: unknown; engine?: string }>,
      number,
    ];
    expect(submittedRecs[0]?.details).toEqual(postgresDetails);
    expect(submittedRecs[0]?.engine).toBe('postgres');
  });
});
