/**
 * Shared column-filter primitives (issue #166).
 *
 * Extracted from recommendations.ts so Plans, History, and RI Exchange
 * can reuse the same numeric-filter parser without copy-pasting it.
 *
 * Each consuming tab owns its own column-id type and filter record (keeping
 * column ids type-safe per tab), but the low-level parser and the generic
 * apply pipeline live here.
 */

// ---------------------------------------------------------------------------
// Numeric filter parser
// ---------------------------------------------------------------------------

export type ParsedNumericFilter =
  | { ok: true; predicate: (n: number) => boolean }
  | { ok: false; error: string };

const MATCH_ALL: ParsedNumericFilter = { ok: true, predicate: () => true };

/**
 * Parse a numeric filter expression such as ">= 100", "< 50", "10..20",
 * or a comma-separated OR list of those. Returns a predicate function on
 * success, or an error object on parse failure so callers can surface inline
 * validation messages.
 *
 * Supported syntax (case-insensitive, whitespace-tolerant):
 *   - `>=N` / `<=N` / `>N` / `<N` — comparison
 *   - `N..M` — inclusive range (order-independent)
 *   - `N` — exact match
 *   - Comma-separated terms are OR-combined
 *   - Empty string / blank — match all
 */
export function parseNumericFilter(expr: string): ParsedNumericFilter {
  if (!expr || expr.trim() === '') return MATCH_ALL;
  const terms = expr.split(',').map((t) => t.trim()).filter((t) => t !== '');
  if (terms.length === 0) return MATCH_ALL;

  const predicates: Array<(n: number) => boolean> = [];
  for (const term of terms) {
    // Order matters: ">=" / "<=" must be checked before ">" / "<".
    let p: ((n: number) => boolean) | null = null;
    let m: RegExpMatchArray | null;
    if ((m = term.match(/^>=\s*(-?\d+(?:\.\d+)?)$/))) {
      const v = Number(m[1]);
      p = (n) => n >= v;
    } else if ((m = term.match(/^<=\s*(-?\d+(?:\.\d+)?)$/))) {
      const v = Number(m[1]);
      p = (n) => n <= v;
    } else if ((m = term.match(/^>\s*(-?\d+(?:\.\d+)?)$/))) {
      const v = Number(m[1]);
      p = (n) => n > v;
    } else if ((m = term.match(/^<\s*(-?\d+(?:\.\d+)?)$/))) {
      const v = Number(m[1]);
      p = (n) => n < v;
    } else if ((m = term.match(/^(-?\d+(?:\.\d+)?)\s*\.\.\s*(-?\d+(?:\.\d+)?)$/))) {
      const lo = Number(m[1]);
      const hi = Number(m[2]);
      const min = Math.min(lo, hi);
      const max = Math.max(lo, hi);
      p = (n) => n >= min && n <= max;
    } else if ((m = term.match(/^(-?\d+(?:\.\d+)?)$/))) {
      const v = Number(m[1]);
      p = (n) => n === v;
    }
    if (p === null) {
      return { ok: false, error: `Invalid filter term: "${term}"` };
    }
    predicates.push(p);
  }
  // OR across terms
  return {
    ok: true,
    predicate: (n) => predicates.some((p) => p(n)),
  };
}

// ---------------------------------------------------------------------------
// Generic filter types
// ---------------------------------------------------------------------------

export type ColumnFilterKind =
  | { kind: 'set'; values: string[] }
  | { kind: 'expr'; expr: string };

/**
 * Apply a column-filter record to a list of rows. Each column maps to either
 * a categorical set-filter or a numeric expression filter. All active filters
 * are ANDed together.
 *
 * @param rows - The full list of rows to filter.
 * @param filters - A partial record mapping column ids to their active filter.
 * @param cellExtractors - Per-column functions that return the raw cell value
 *   for a given row. Categorical columns return a string; numeric columns
 *   return a number.
 *
 * Returns a new array containing only rows that pass all active filters.
 * Broken numeric expressions (parse failure) are skipped so the UI can show
 * an inline validation error without forcing the user to clear the field first.
 */
export function applyColumnFilters<TRow, TColumnId extends string>(
  rows: readonly TRow[],
  filters: Partial<Record<TColumnId, ColumnFilterKind>>,
  cellExtractors: {
    categorical: (row: TRow, col: TColumnId) => string;
    numeric: (row: TRow, col: TColumnId) => number;
  },
): TRow[] {
  const entries = Object.entries(filters) as Array<[TColumnId, ColumnFilterKind]>;
  if (entries.length === 0) return [...rows];

  return rows.filter((row) => {
    for (const [col, filter] of entries) {
      if (filter.kind === 'set') {
        const cellValue = cellExtractors.categorical(row, col);
        if (!filter.values.includes(cellValue)) return false;
      } else {
        const parsed = parseNumericFilter(filter.expr);
        if (!parsed.ok) continue; // ignore broken expressions; UI shows the error
        const cellNum = cellExtractors.numeric(row, col);
        if (!parsed.predicate(cellNum)) return false;
      }
    }
    return true;
  });
}
