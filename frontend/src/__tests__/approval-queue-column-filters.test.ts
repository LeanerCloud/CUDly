/**
 * Approval Queue column-filter regression suite (issue #166).
 *
 * Covers the inline per-column filters wired onto the Approval Queue
 * table headers. Filterable columns: Provider, Account, Service, Term,
 * Payment, Created by (categorical), Count, Monthly Cost, Upfront Cost,
 * Monthly Savings (numeric). Status is excluded by design — the queue
 * scope is already pending|notified.
 *
 * Tested matrix:
 *   1. Numeric expr filter narrows by predicate (monthly_cost >= N).
 *   2. Categorical set filter narrows by membership (payment in set).
 *   3. Multiple filters AND together across columns.
 *   4. Invalid numeric expression is skipped (no exception, no narrowing).
 *   5. NaN-as-missing contract: monthly_cost == null produces NaN, which
 *      fails every numeric predicate (not coincidentally matches "= 0").
 *   6. Clearing returns a fresh clone of the input.
 */

import { applyApprovalQueueColumnFilters } from '../history';
import type { HistoryPurchase } from '../types';
import type { ApprovalQueueColumnFilters } from '../state';

jest.mock('../api', () => ({}));
jest.mock('../navigation', () => ({ switchTab: jest.fn() }));
jest.mock('../utils', () => ({
  formatCurrency: jest.fn((v) => `$${v ?? 0}`),
  formatDate: jest.fn((v) => v),
  formatTerm: jest.fn((y) => `${y} Year${y === 1 ? '' : 's'}`),
  escapeHtml: jest.fn((s) => s ?? ''),
}));
jest.mock('../state', () => ({
  subscribeProvider: jest.fn().mockReturnValue(() => {}),
  subscribeAccount: jest.fn().mockReturnValue(() => {}),
}));
jest.mock('../confirmDialog', () => ({ confirmDialog: jest.fn() }));
jest.mock('../approval-details', () => ({ buildApprovalDetailsBody: jest.fn() }));
jest.mock('../toast', () => ({ showToast: jest.fn() }));
jest.mock('../lib/skeleton', () => ({ showSkeletonRows: jest.fn(), teardownSkeleton: jest.fn() }));
jest.mock('../recommendations', () => ({ getAccountName: jest.fn((id: string) => id) }));

function mkRow(overrides: Partial<HistoryPurchase>): HistoryPurchase {
  return {
    purchase_id: 'p',
    timestamp: '2024-01-01T00:00:00Z',
    provider: 'aws',
    service: 'ec2',
    resource_type: 'reserved-instance',
    region: 'us-east-1',
    count: 1,
    term: 1,
    payment: 'all_upfront',
    upfront_cost: 100,
    monthly_cost: 50,
    estimated_savings: 30,
    account_id: 'acct-1',
    created_by_user_email: 'alice@example.com',
    status: 'pending',
    ...overrides,
  };
}

const rows: HistoryPurchase[] = [
  mkRow({ purchase_id: 'a', provider: 'aws',   account_id: 'acct-1', payment: 'all_upfront',     monthly_cost: 50,   estimated_savings: 30,  created_by_user_email: 'alice@example.com' }),
  mkRow({ purchase_id: 'b', provider: 'aws',   account_id: 'acct-2', payment: 'no_upfront',      monthly_cost: 200,  estimated_savings: 100, created_by_user_email: 'bob@example.com' }),
  mkRow({ purchase_id: 'c', provider: 'azure', account_id: 'acct-2', payment: 'partial_upfront', monthly_cost: null as unknown as number | undefined, estimated_savings: 60, created_by_user_email: 'alice@example.com' }),
  mkRow({ purchase_id: 'd', provider: 'gcp',   account_id: 'acct-3', payment: 'all_upfront',     monthly_cost: 500,  estimated_savings: 250, created_by_user_email: 'carol@example.com' }),
];

describe('applyApprovalQueueColumnFilters', () => {
  test('numeric expr: monthly_cost >= 200 narrows to expensive rows', () => {
    const filters: ApprovalQueueColumnFilters = {
      monthly_cost: { kind: 'expr', expr: '>=200' },
    };
    const out = applyApprovalQueueColumnFilters(rows, filters);
    expect(out.map((r) => r.purchase_id)).toEqual(['b', 'd']);
  });

  test('categorical set: payment in {all_upfront} narrows to those rows', () => {
    const filters: ApprovalQueueColumnFilters = {
      payment: { kind: 'set', values: ['all_upfront'] },
    };
    const out = applyApprovalQueueColumnFilters(rows, filters);
    expect(out.map((r) => r.purchase_id)).toEqual(['a', 'd']);
  });

  test('multiple filters AND together (provider=aws + created_by=alice)', () => {
    const filters: ApprovalQueueColumnFilters = {
      provider: { kind: 'set', values: ['aws'] },
      created_by: { kind: 'set', values: ['alice@example.com'] },
    };
    const out = applyApprovalQueueColumnFilters(rows, filters);
    expect(out.map((r) => r.purchase_id)).toEqual(['a']);
  });

  test('invalid numeric expression is skipped (filter is a no-op)', () => {
    const filters: ApprovalQueueColumnFilters = {
      savings: { kind: 'expr', expr: 'not-a-num' },
    };
    const out = applyApprovalQueueColumnFilters(rows, filters);
    expect(out).toHaveLength(rows.length);
  });

  test('null monthly_cost fails every numeric predicate (NaN contract)', () => {
    // Row c has monthly_cost: null. A "= 0" predicate must NOT match it,
    // and a ">0" predicate must NOT match it either.
    const eqZero: ApprovalQueueColumnFilters = {
      monthly_cost: { kind: 'expr', expr: '0' },
    };
    expect(applyApprovalQueueColumnFilters(rows, eqZero).map((r) => r.purchase_id))
      .not.toContain('c');

    const gtZero: ApprovalQueueColumnFilters = {
      monthly_cost: { kind: 'expr', expr: '>0' },
    };
    expect(applyApprovalQueueColumnFilters(rows, gtZero).map((r) => r.purchase_id))
      .not.toContain('c');
  });

  test('clearing filters via empty record returns a fresh clone', () => {
    const out = applyApprovalQueueColumnFilters(rows, {});
    expect(out).toEqual(rows);
    expect(out).not.toBe(rows);
  });
});
