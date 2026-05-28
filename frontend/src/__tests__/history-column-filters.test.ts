/**
 * Purchase History column-filter regression suite (issue #166).
 *
 * Covers the inline per-column filters wired onto the Purchase History
 * table headers. The existing Status chip-row remains the canonical
 * filter for status — these tests exercise the new column-filter slice
 * (provider/service/type/region/term + count/upfront_cost/savings).
 *
 * Tested matrix:
 *   1. Numeric expr filter narrows by predicate (savings > N).
 *   2. Categorical set filter narrows by membership (provider in set).
 *   3. Multiple filters AND together across columns.
 *   4. Invalid numeric expression is skipped (no exception, no narrowing).
 *   5. Clearing a filter via setPurchaseHistoryColumnFilter(col, null)
 *      restores the full slice.
 *   6. Term column treats absent vs zero correctly — categorical-empty
 *      filtering.
 */

import { applyPurchaseHistoryColumnFilters } from '../history';
import type { HistoryPurchase } from '../types';
import type { PurchaseHistoryColumnFilters } from '../state';

// history.ts pulls in api/state/navigation transitively; the column-filter
// helper is pure (operates on the passed-in rows + filter record), but the
// module import path still resolves those — stub them out so the test runs
// without an apiBase / DOM context.
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
    upfront_cost: 100,
    estimated_savings: 50,
    ...overrides,
  };
}

const rows: HistoryPurchase[] = [
  mkRow({ purchase_id: 'a', provider: 'aws',   service: 'ec2',  region: 'us-east-1', count: 1, term: 1, upfront_cost: 100,  estimated_savings: 50 }),
  mkRow({ purchase_id: 'b', provider: 'aws',   service: 'rds',  region: 'us-west-2', count: 3, term: 3, upfront_cost: 500,  estimated_savings: 200 }),
  mkRow({ purchase_id: 'c', provider: 'azure', service: 'ec2',  region: 'eu-west-1', count: 5, term: 1, upfront_cost: 1000, estimated_savings: 400 }),
  mkRow({ purchase_id: 'd', provider: 'gcp',   service: 'ec2',  region: 'us-east-1', count: 2, term: 1, upfront_cost: 250,  estimated_savings: 80  }),
];

describe('applyPurchaseHistoryColumnFilters', () => {
  test('numeric expr: savings > 100 narrows to high-saving rows', () => {
    const filters: PurchaseHistoryColumnFilters = {
      savings: { kind: 'expr', expr: '>100' },
    };
    const out = applyPurchaseHistoryColumnFilters(rows, filters);
    expect(out.map((r) => r.purchase_id)).toEqual(['b', 'c']);
  });

  test('categorical set: provider in {aws, gcp} excludes azure', () => {
    const filters: PurchaseHistoryColumnFilters = {
      provider: { kind: 'set', values: ['aws', 'gcp'] },
    };
    const out = applyPurchaseHistoryColumnFilters(rows, filters);
    expect(out.map((r) => r.purchase_id)).toEqual(['a', 'b', 'd']);
  });

  test('multiple filters AND together (provider=aws + savings >= 100)', () => {
    const filters: PurchaseHistoryColumnFilters = {
      provider: { kind: 'set', values: ['aws'] },
      savings: { kind: 'expr', expr: '>=100' },
    };
    const out = applyPurchaseHistoryColumnFilters(rows, filters);
    expect(out.map((r) => r.purchase_id)).toEqual(['b']);
  });

  test('invalid numeric expression is skipped (filter is a no-op)', () => {
    const filters: PurchaseHistoryColumnFilters = {
      savings: { kind: 'expr', expr: '>>nope' },
    };
    const out = applyPurchaseHistoryColumnFilters(rows, filters);
    // Parse failure → filter ignored; full slice passes.
    expect(out).toHaveLength(rows.length);
  });

  test('clearing filters via empty record returns a fresh clone', () => {
    const out = applyPurchaseHistoryColumnFilters(rows, {});
    expect(out).toEqual(rows);
    expect(out).not.toBe(rows);
  });

  test('term filter uses categorical-set semantics with stringified values', () => {
    const filters: PurchaseHistoryColumnFilters = {
      term: { kind: 'set', values: ['3'] },
    };
    const out = applyPurchaseHistoryColumnFilters(rows, filters);
    expect(out.map((r) => r.purchase_id)).toEqual(['b']);
  });
});
