/**
 * Per-service recommendation-filter controls (serviceFilters.ts).
 *
 * Covers the GUI surface for the CLI filter knobs:
 *   - parseCsvList / joinCsvList round-trip + de-dup + trim + lowercase.
 *   - injectServiceFilterControls is idempotent and finds the card.
 *   - populate fills inputs from a ServiceConfig (or clears when absent).
 *   - read returns parsed values, validates min-count, and errors loudly on
 *     a bad min-count instead of silently coercing.
 */
import {
  parseCsvList,
  joinCsvList,
  filterInputId,
  serviceFilterInputIds,
  injectServiceFilterControls,
  populateServiceFilterControls,
  readServiceFilterControls,
  isServiceFilterError,
  type ServiceFilterTarget,
} from '../serviceFilters';
import type { ServiceConfig } from '../api/types';

const target: ServiceFilterTarget = { provider: 'aws', service: 'rds', termId: 'aws-rds-term' };

function seedCard(): void {
  document.body.innerHTML = `
    <div class="service-default-card">
      <h5>RDS</h5>
      <label>Term: <select id="aws-rds-term"><option value="3">3</option></select></label>
    </div>`;
}

describe('parseCsvList / joinCsvList', () => {
  it('splits on comma and whitespace, trims, lowercases, de-dups', () => {
    expect(parseCsvList('MySQL, postgres  mysql,, AURORA')).toEqual(['mysql', 'postgres', 'aurora']);
  });
  it('returns [] for empty / whitespace-only input', () => {
    expect(parseCsvList('')).toEqual([]);
    expect(parseCsvList('   ,  , ')).toEqual([]);
  });
  it('joins back to the comma-separated display form', () => {
    expect(joinCsvList(['mysql', 'postgres'])).toBe('mysql, postgres');
    expect(joinCsvList(undefined)).toBe('');
  });
});

describe('id helpers', () => {
  it('builds deterministic input ids', () => {
    expect(filterInputId('aws', 'rds', 'include-engines')).toBe('aws-rds-include-engines');
  });
  it('lists all owned input ids including min-count', () => {
    const ids = serviceFilterInputIds(target);
    expect(ids).toContain('aws-rds-include-engines');
    expect(ids).toContain('aws-rds-exclude-regions');
    expect(ids).toContain('aws-rds-min-count');
    expect(ids).toHaveLength(7);
  });
});

describe('injectServiceFilterControls', () => {
  beforeEach(seedCard);

  it('injects the panel and all inputs once', () => {
    expect(injectServiceFilterControls(target)).toBe(true);
    for (const id of serviceFilterInputIds(target)) {
      expect(document.getElementById(id)).not.toBeNull();
    }
    // min-count input is a number input with min=0
    const minEl = document.getElementById('aws-rds-min-count') as HTMLInputElement;
    expect(minEl.type).toBe('number');
    expect(minEl.min).toBe('0');
  });

  it('is idempotent (no duplicate panel on re-run)', () => {
    expect(injectServiceFilterControls(target)).toBe(true);
    expect(injectServiceFilterControls(target)).toBe(false);
    expect(document.querySelectorAll('.service-filter-panel')).toHaveLength(1);
  });

  it('returns false when the card is missing', () => {
    document.body.innerHTML = '';
    expect(injectServiceFilterControls(target)).toBe(false);
  });
});

describe('populate + read round-trip', () => {
  beforeEach(() => {
    seedCard();
    injectServiceFilterControls(target);
  });

  it('populates inputs from a ServiceConfig', () => {
    const svc: ServiceConfig = {
      provider: 'aws', service: 'rds', enabled: true, term: 3, payment: 'all-upfront', coverage: 80,
      include_engines: ['mysql', 'postgres'],
      exclude_types: ['db.t3.micro'],
      include_regions: ['us-east-1'],
      min_count: 3,
    };
    populateServiceFilterControls(target, svc);
    expect((document.getElementById('aws-rds-include-engines') as HTMLInputElement).value).toBe('mysql, postgres');
    expect((document.getElementById('aws-rds-exclude-types') as HTMLInputElement).value).toBe('db.t3.micro');
    expect((document.getElementById('aws-rds-include-regions') as HTMLInputElement).value).toBe('us-east-1');
    expect((document.getElementById('aws-rds-min-count') as HTMLInputElement).value).toBe('3');
  });

  it('clears inputs when no config (min-count -> 0)', () => {
    populateServiceFilterControls(target, undefined);
    expect((document.getElementById('aws-rds-include-engines') as HTMLInputElement).value).toBe('');
    expect((document.getElementById('aws-rds-min-count') as HTMLInputElement).value).toBe('0');
  });

  it('reads inputs back into parsed values', () => {
    (document.getElementById('aws-rds-include-engines') as HTMLInputElement).value = 'MySQL, mysql, postgres';
    (document.getElementById('aws-rds-min-count') as HTMLInputElement).value = '5';
    const v = readServiceFilterControls(target);
    expect(isServiceFilterError(v)).toBe(false);
    if (!isServiceFilterError(v)) {
      expect(v.include_engines).toEqual(['mysql', 'postgres']);
      expect(v.min_count).toBe(5);
      expect(v.exclude_engines).toEqual([]);
    }
  });

  it('treats blank min-count as 0', () => {
    (document.getElementById('aws-rds-min-count') as HTMLInputElement).value = '   ';
    const v = readServiceFilterControls(target);
    expect(isServiceFilterError(v)).toBe(false);
    if (!isServiceFilterError(v)) expect(v.min_count).toBe(0);
  });

  it('errors loudly on a fractional min-count', () => {
    (document.getElementById('aws-rds-min-count') as HTMLInputElement).value = '1.5';
    const v = readServiceFilterControls(target);
    expect(isServiceFilterError(v)).toBe(true);
    if (isServiceFilterError(v)) expect(v.message).toContain('whole number');
  });

  it('errors loudly on a negative min-count', () => {
    (document.getElementById('aws-rds-min-count') as HTMLInputElement).value = '-2';
    const v = readServiceFilterControls(target);
    expect(isServiceFilterError(v)).toBe(true);
  });
});

describe('read with no injected panel', () => {
  it('returns empty lists and 0 min-count (no filters)', () => {
    document.body.innerHTML = '';
    const v = readServiceFilterControls(target);
    expect(isServiceFilterError(v)).toBe(false);
    if (!isServiceFilterError(v)) {
      expect(v.include_engines).toEqual([]);
      expect(v.min_count).toBe(0);
    }
  });
});
