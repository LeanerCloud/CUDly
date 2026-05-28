/**
 * Unit tests for the shared column-filter lib (issue #166).
 *
 * parseNumericFilter is the core utility extracted from recommendations.ts.
 * applyColumnFilters is the generic version that any tab can use.
 * recommendations.ts continues to re-export ParsedNumericFilter for backward
 * compat with existing consumers.
 */
import { parseNumericFilter, applyColumnFilters } from '../lib/column-filters';

// ---------------------------------------------------------------------------
// parseNumericFilter
// ---------------------------------------------------------------------------

describe('parseNumericFilter (lib)', () => {
  const accept = (expr: string, n: number): boolean => {
    const r = parseNumericFilter(expr);
    if (!r.ok) throw new Error(`unexpected parse failure for "${expr}": ${r.error}`);
    return r.predicate(n);
  };

  test('empty / blank returns match-all', () => {
    const r = parseNumericFilter('');
    expect(r.ok).toBe(true);
    if (r.ok) expect(r.predicate(0)).toBe(true);
    expect(parseNumericFilter('   ').ok).toBe(true);
  });

  test('plain number: exact equality', () => {
    expect(accept('42', 42)).toBe(true);
    expect(accept('42', 43)).toBe(false);
    expect(accept('-5', -5)).toBe(true);
    expect(accept('3.14', 3.14)).toBe(true);
    expect(accept('3.14', 3.15)).toBe(false);
  });

  test('comparators >, >=, <, <=', () => {
    expect(accept('>10', 11)).toBe(true);
    expect(accept('>10', 10)).toBe(false);
    expect(accept('>=10', 10)).toBe(true);
    expect(accept('<5', 4)).toBe(true);
    expect(accept('<5', 5)).toBe(false);
    expect(accept('<=5', 5)).toBe(true);
  });

  test('inclusive range X..Y (order-independent)', () => {
    expect(accept('10..20', 10)).toBe(true);
    expect(accept('10..20', 20)).toBe(true);
    expect(accept('10..20', 15)).toBe(true);
    expect(accept('10..20', 9)).toBe(false);
    expect(accept('20..10', 15)).toBe(true);
  });

  test('comma-separated terms OR together', () => {
    expect(accept('5, >100', 5)).toBe(true);
    expect(accept('5, >100', 150)).toBe(true);
    expect(accept('5, >100', 50)).toBe(false);
  });

  test('invalid expression returns ok:false', () => {
    const r1 = parseNumericFilter('>>5');
    expect(r1.ok).toBe(false);
    if (!r1.ok) expect(r1.error).toMatch(/Invalid filter term/);
    expect(parseNumericFilter('not-a-number').ok).toBe(false);
    expect(parseNumericFilter('1..').ok).toBe(false);
  });
});

// ---------------------------------------------------------------------------
// applyColumnFilters (generic)
// ---------------------------------------------------------------------------

type Row = { id: string; service: string; savings: number };
type Col = 'service' | 'savings';

const rows: Row[] = [
  { id: 'a', service: 'ec2', savings: 10 },
  { id: 'b', service: 'rds', savings: 200 },
  { id: 'c', service: 'ec2', savings: 500 },
];

const extractors = {
  categorical: (r: Row, col: Col) => (col === 'service' ? r.service : String(r.savings)),
  numeric: (r: Row, _col: Col) => r.savings,
};

describe('applyColumnFilters (lib)', () => {
  test('empty filters returns a clone of the input', () => {
    const out = applyColumnFilters<Row, Col>(rows, {}, extractors);
    expect(out).toEqual(rows);
    expect(out).not.toBe(rows);
  });

  test('categorical set filter narrows by membership', () => {
    const out = applyColumnFilters<Row, Col>(
      rows,
      { service: { kind: 'set', values: ['ec2'] } },
      extractors,
    );
    expect(out.map((r) => r.id)).toEqual(['a', 'c']);
  });

  test('numeric expr filter narrows by predicate', () => {
    const out = applyColumnFilters<Row, Col>(
      rows,
      { savings: { kind: 'expr', expr: '>100' } },
      extractors,
    );
    expect(out.map((r) => r.id)).toEqual(['b', 'c']);
  });

  test('multiple filters AND together', () => {
    const out = applyColumnFilters<Row, Col>(
      rows,
      {
        service: { kind: 'set', values: ['ec2'] },
        savings: { kind: 'expr', expr: '>100' },
      },
      extractors,
    );
    expect(out.map((r) => r.id)).toEqual(['c']);
  });

  test('broken numeric expr is skipped (not treated as match-none)', () => {
    const out = applyColumnFilters<Row, Col>(
      rows,
      { savings: { kind: 'expr', expr: '>>invalid' } },
      extractors,
    );
    // Parse fails -> filter is skipped -> all rows pass
    expect(out).toHaveLength(rows.length);
  });
});
