/**
 * Tests for the cross-provider compute capacity comparator (issue #82).
 *
 * Covers:
 *   - compareByCapacity: vCPU primary key, MemoryGB secondary key,
 *     resource_type tertiary key
 *   - Null / absent / zero capacity values sort to the end (feedback_nullable_not_zero)
 *   - Mixed AWS / Azure / GCP recs produce a deterministic order
 *   - Non-compute details (no vcpu/memory_gb) are treated as unknown
 *   - Reflexivity: compareByCapacity(x, x) === 0
 *   - Symmetry: sign(compareByCapacity(a, b)) === -sign(compareByCapacity(b, a))
 */

import { compareByCapacity } from '../lib/capacity';

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

function rec(
  resourceType: string,
  vcpu: number | null | undefined,
  memoryGb: number | null | undefined
): { details: unknown; resource_type: string } {
  const details: Record<string, unknown> = {};
  if (vcpu !== undefined && vcpu !== null) details['vcpu'] = vcpu;
  if (memoryGb !== undefined && memoryGb !== null) details['memory_gb'] = memoryGb;
  return { details, resource_type: resourceType };
}

function recNoDetails(resourceType: string): { details: undefined; resource_type: string } {
  return { details: undefined, resource_type: resourceType };
}

/** Builds a rec with raw numeric values including NaN/Infinity (bypasses rec() guards). */
function recRaw(
  resourceType: string,
  vcpu: unknown,
  memoryGb: unknown
): { details: unknown; resource_type: string } {
  return { details: { vcpu, memory_gb: memoryGb }, resource_type: resourceType };
}

// ---------------------------------------------------------------------------
// Basic ordering
// ---------------------------------------------------------------------------

describe('compareByCapacity — primary sort: vCPU', () => {
  it('sorts smaller vCPU before larger', () => {
    const small = rec('m5.large', 2, 8);
    const large = rec('m5.4xlarge', 16, 64);
    expect(compareByCapacity(small, large)).toBeLessThan(0);
    expect(compareByCapacity(large, small)).toBeGreaterThan(0);
  });

  it('is reflexive', () => {
    const r = rec('m5.2xlarge', 8, 32);
    expect(compareByCapacity(r, r)).toBe(0);
  });
});

describe('compareByCapacity — secondary sort: MemoryGB', () => {
  it('breaks vCPU tie by memory ascending', () => {
    const lowMem = rec('c5.xlarge', 4, 8);
    const hiMem = rec('r5.xlarge', 4, 32);
    expect(compareByCapacity(lowMem, hiMem)).toBeLessThan(0);
    expect(compareByCapacity(hiMem, lowMem)).toBeGreaterThan(0);
  });
});

describe('compareByCapacity — tertiary sort: resource_type lexical', () => {
  it('breaks full capacity tie by resource_type', () => {
    const az = rec('Standard_D4s_v3', 4, 16);
    const aws = rec('m5.xlarge', 4, 16);
    // "Standard_D4s_v3" > "m5.xlarge" lexically (uppercase 'S' < lowercase 'm' in ASCII
    // but both are printable — what matters is determinism and symmetry)
    const cmp = compareByCapacity(az, aws);
    expect(cmp).not.toBe(0);
    expect(compareByCapacity(aws, az)).toBe(-cmp);
  });

  it('returns 0 for identical capacity and type', () => {
    const a = rec('m5.xlarge', 4, 16);
    const b = rec('m5.xlarge', 4, 16);
    expect(compareByCapacity(a, b)).toBe(0);
  });
});

// ---------------------------------------------------------------------------
// Null / absent / zero values sort to the end (feedback_nullable_not_zero)
// ---------------------------------------------------------------------------

describe('compareByCapacity — null/absent/zero sort last', () => {
  it('null vcpu sorts after known vcpu', () => {
    const known = rec('m5.large', 2, 8);
    const unknown = rec('unknown-type', null, null);
    expect(compareByCapacity(known, unknown)).toBeLessThan(0);
    expect(compareByCapacity(unknown, known)).toBeGreaterThan(0);
  });

  it('zero vcpu (unknown sentinel) sorts after positive vcpu', () => {
    const known = rec('m5.large', 2, 8);
    const zeroVcpu = rec('z1d.large', 0, 8);
    expect(compareByCapacity(known, zeroVcpu)).toBeLessThan(0);
  });

  it('absent details sorts after fully-populated rec', () => {
    const known = rec('m5.xlarge', 4, 16);
    const noDetails = recNoDetails('mystery-type');
    expect(compareByCapacity(known, noDetails)).toBeLessThan(0);
    expect(compareByCapacity(noDetails, known)).toBeGreaterThan(0);
  });

  it('two unknown recs: tie resolved by resource_type', () => {
    const a = recNoDetails('alpha');
    const b = recNoDetails('beta');
    expect(compareByCapacity(a, b)).toBeLessThan(0);
    expect(compareByCapacity(b, a)).toBeGreaterThan(0);
  });

  it('null memoryGB sorts after known memoryGB when vCPU ties', () => {
    const known = rec('c5.xlarge', 4, 8);
    const nullMem = rec('something', 4, null);
    expect(compareByCapacity(known, nullMem)).toBeLessThan(0);
  });

  // NaN guard: typeof NaN === 'number' but NaN is not a finite capacity value.
  // extractCapacity must fold NaN into the unknown bucket (null) so that
  // compareNullable never sees it; NaN subtraction would return NaN and violate
  // the sort contract (NaN is neither < 0 nor > 0 nor === 0).
  it('NaN vcpu is treated as unknown and sorts after positive vcpu', () => {
    const known = rec('m5.large', 2, 8);
    const nanVcpu = recRaw('bad-type', NaN, 8);
    expect(compareByCapacity(known, nanVcpu)).toBeLessThan(0);
    expect(compareByCapacity(nanVcpu, known)).toBeGreaterThan(0);
  });

  it('NaN memoryGB is treated as unknown and sorts after known memoryGB when vCPU ties', () => {
    const known = rec('c5.xlarge', 4, 8);
    const nanMem = recRaw('r5.xlarge', 4, NaN);
    expect(compareByCapacity(known, nanMem)).toBeLessThan(0);
    expect(compareByCapacity(nanMem, known)).toBeGreaterThan(0);
  });

  it('two NaN-vcpu recs: tie resolved by resource_type (no NaN-comparison instability)', () => {
    const a = recRaw('alpha', NaN, NaN);
    const b = recRaw('beta', NaN, NaN);
    const ab = compareByCapacity(a, b);
    const ba = compareByCapacity(b, a);
    expect(ab).toBeLessThan(0);
    expect(ba).toBeGreaterThan(0);
    // Symmetry: sign must flip, not collapse to 0 or NaN.
    expect(Math.sign(ab)).toBe(-Math.sign(ba));
  });
});

// ---------------------------------------------------------------------------
// Mixed AWS / Azure / GCP scenario
// ---------------------------------------------------------------------------

describe('compareByCapacity — cross-provider sort', () => {
  it('produces deterministic order across providers', () => {
    const awsSmall  = rec('m5.large',         2,  8);   // AWS
    const azureMid  = rec('Standard_D4s_v3',  4, 16);   // Azure
    const gcpLarge  = rec('n2-standard-8',    8, 32);   // GCP
    const unknown   = recNoDetails('unknown'); // no catalogue entry

    const recs = [gcpLarge, unknown, awsSmall, azureMid];
    const sorted = [...recs].sort(compareByCapacity);

    expect(sorted[0]).toBe(awsSmall);
    expect(sorted[1]).toBe(azureMid);
    expect(sorted[2]).toBe(gcpLarge);
    expect(sorted[3]).toBe(unknown);
  });

  it('Azure fractional memory (0.5 GB) sorts before 1 GB', () => {
    const tiny    = rec('Standard_A0', 1, 0.5);
    const small   = rec('Standard_A1', 1, 1.75);
    expect(compareByCapacity(tiny, small)).toBeLessThan(0);
  });
});

// ---------------------------------------------------------------------------
// Symmetry invariant
// ---------------------------------------------------------------------------

describe('compareByCapacity — symmetry', () => {
  const pairs: Array<[
    { details: unknown; resource_type: string },
    { details: unknown; resource_type: string }
  ]> = [
    [rec('m5.large', 2, 8),      rec('m5.4xlarge', 16, 64)],
    [rec('c5.xlarge', 4, 8),     rec('r5.xlarge', 4, 32)],
    [rec('m5.xlarge', 4, 16),    recNoDetails('no-data')],
    [recNoDetails('alpha'),      recNoDetails('beta')],
  ];

  it.each(pairs)('sign(a,b) === -sign(b,a)', (a, b) => {
    const ab = compareByCapacity(a, b);
    const ba = compareByCapacity(b, a);
    if (ab === 0) {
      expect(ba).toBe(0);
    } else {
      expect(Math.sign(ab)).toBe(-Math.sign(ba));
    }
  });
});
