/**
 * Regression suite for the RI Exchange column-filter wiring (issue #166
 * follow-up to merged #570).
 *
 * Covers the contract that `applyRiExchangeColumnFilters` is the
 * canonical filter pipeline for the reshape-recommendations table:
 *   - categorical set-membership narrows by exact match;
 *   - numeric expressions narrow by the parsed predicate;
 *   - multiple column filters AND together;
 *   - invalid numeric expressions are skipped (not treated as match-none);
 *   - clearing a column drops its narrowing.
 *
 * The renderer wiring on top of this (popover, state slice mutations,
 * filter button) is exercised indirectly by the existing riexchange
 * test suite — those tests assert the table still renders rows with the
 * default empty-filter state. The contract here is the pure function
 * that decides which rows pass.
 */

// Mock api/state defensively so the riexchange module's transitive
// imports don't drag in jsdom plumbing we don't need for a pure-fn test.
jest.mock('../api', () => ({
  listConvertibleRIs: jest.fn(),
  getRIUtilization: jest.fn(),
  getReshapeRecommendations: jest.fn(),
  getExchangeQuote: jest.fn(),
  executeExchange: jest.fn(),
  getRIExchangeHistory: jest.fn(),
  getRIExchangeConfig: jest.fn(),
  updateRIExchangeConfig: jest.fn(),
  listTargetOfferings: jest.fn().mockResolvedValue([]),
}));

jest.mock('../navigation', () => ({
  switchTab: jest.fn(),
  switchSettingsSubTab: jest.fn(),
}));

import type { ReshapeRecommendation } from '../api';
import { applyRiExchangeColumnFilters } from '../riexchange';
import type { RiExchangeColumnFilters } from '../state';

function makeRec(overrides: Partial<ReshapeRecommendation> = {}): ReshapeRecommendation {
  return {
    source_ri_id: 'ri-a',
    source_instance_type: 'm5.xlarge',
    source_count: 4,
    target_instance_type: 'm5.large',
    target_count: 8,
    utilization_percent: 50,
    normalized_used: 4,
    normalized_purchased: 8,
    reason: 'underutilized',
    ...overrides,
  };
}

describe('applyRiExchangeColumnFilters', () => {
  const recs: ReshapeRecommendation[] = [
    makeRec({ source_ri_id: 'ri-a', source_instance_type: 'm5.xlarge', utilization_percent: 50, target_count: 2 }),
    makeRec({ source_ri_id: 'ri-b', source_instance_type: 'm5.xlarge', utilization_percent: 95, target_count: 8 }),
    makeRec({ source_ri_id: 'ri-c', source_instance_type: 'c6i.large',  utilization_percent: 30, target_count: 1 }),
  ];

  test('empty filter record returns a clone of the input', () => {
    const out = applyRiExchangeColumnFilters(recs, {});
    expect(out).toEqual(recs);
    expect(out).not.toBe(recs);
  });

  test('numeric expression filter narrows by predicate', () => {
    const filters: RiExchangeColumnFilters = {
      utilization_percent: { kind: 'expr', expr: '>=70' },
    };
    const out = applyRiExchangeColumnFilters(recs, filters);
    expect(out.map((r) => r.source_ri_id)).toEqual(['ri-b']);
  });

  test('categorical set filter narrows by membership', () => {
    const filters: RiExchangeColumnFilters = {
      source_instance_type: { kind: 'set', values: ['m5.xlarge'] },
    };
    const out = applyRiExchangeColumnFilters(recs, filters);
    expect(out.map((r) => r.source_ri_id)).toEqual(['ri-a', 'ri-b']);
  });

  test('multiple filters AND together across categorical + numeric', () => {
    const filters: RiExchangeColumnFilters = {
      source_instance_type: { kind: 'set', values: ['m5.xlarge'] },
      target_count: { kind: 'expr', expr: '>5' },
    };
    const out = applyRiExchangeColumnFilters(recs, filters);
    // Only ri-b is m5.xlarge AND target_count>5
    expect(out.map((r) => r.source_ri_id)).toEqual(['ri-b']);
  });

  test('broken numeric expression is skipped (not match-none)', () => {
    const filters: RiExchangeColumnFilters = {
      utilization_percent: { kind: 'expr', expr: '>>oops' },
    };
    const out = applyRiExchangeColumnFilters(recs, filters);
    // Parse fails -> filter is skipped -> every row passes.
    expect(out).toHaveLength(recs.length);
  });

  test('numeric filter compares against the display-rounded value', () => {
    // utilization is rendered with toFixed(1); a row whose raw value
    // would round to 95.0 must match an exact-value filter "95.0".
    const rec = makeRec({ source_ri_id: 'ri-x', utilization_percent: 94.96 });
    const filters: RiExchangeColumnFilters = {
      utilization_percent: { kind: 'expr', expr: '95.0' },
    };
    const out = applyRiExchangeColumnFilters([rec], filters);
    expect(out).toHaveLength(1);
  });

  test('clearing a column (no entry) means no narrowing on that column', () => {
    // Simulate the "Clear" footer button: drop the column entry from
    // the filter record. Remaining filters still apply.
    const filters: RiExchangeColumnFilters = {
      source_instance_type: { kind: 'set', values: ['m5.xlarge'] },
    };
    const out = applyRiExchangeColumnFilters(recs, filters);
    expect(out.map((r) => r.source_ri_id)).toEqual(['ri-a', 'ri-b']);

    // Now clear by passing an empty filter record.
    const cleared = applyRiExchangeColumnFilters(recs, {});
    expect(cleared).toHaveLength(recs.length);
  });
});
