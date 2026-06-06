/**
 * Cross-provider compute capacity ranking utilities.
 *
 * Provides a comparator for sorting cloud commitment recommendations by their
 * underlying compute capacity (vCPU first, then memory in GB, then instance
 * type lexically). This lets a mixed AWS/Azure/GCP recommendation table be
 * sorted by actual size rather than by opaque type-name strings whose
 * lexicographic order carries no capacity meaning.
 *
 * The fields below mirror the JSON keys emitted by ComputeDetails in
 * pkg/common/types.go (vcpu / memory_gb) — the shapes must stay in sync.
 *
 * Missing capacity values (0 or null/undefined from the API) sort to the
 * end of the list rather than the front, so well-populated rows stay visible
 * at the top when the user sorts ascending by size.
 */

/**
 * Minimum capability representation carried by a recommendation's details
 * payload when the service is "compute". Matches the JSON produced by
 * ComputeDetails (pkg/common/types.go): vcpu and memory_gb are omitempty so
 * they may be absent from the wire payload.
 */
export interface ComputeCapacity {
  vcpu?: number | null;
  memory_gb?: number | null;
  [key: string]: unknown;
}

/**
 * compareByCapacity ranks two compute recommendations by capacity.
 *
 * Sort key priority:
 *   1. vCPU count  (ascending, unknowns / 0 sort last)
 *   2. MemoryGB    (ascending, unknowns / 0 sort last)
 *   3. resource_type lexical (ascending, stable tie-break across providers)
 *
 * "Unknown" means the capacity value is absent, null, or 0 (see the
 * ComputeDetails godoc: "0 = unknown"). Nulls sort to the end so that
 * well-populated rows stay at the top when the user chooses ascending order.
 * (Rationale: feedback_nullable_not_zero.md — absent numeric fields must not
 * be treated as zero in sort/rank paths.)
 *
 * @param a - first recommendation (only details + resource_type used)
 * @param b - second recommendation (same)
 * @returns negative / 0 / positive per Array.prototype.sort contract
 */
export function compareByCapacity(
  a: { details?: unknown; resource_type?: string },
  b: { details?: unknown; resource_type?: string }
): number {
  const ca = extractCapacity(a.details);
  const cb = extractCapacity(b.details);

  const vcpuCmp = compareNullable(ca.vcpu ?? null, cb.vcpu ?? null);
  if (vcpuCmp !== 0) return vcpuCmp;

  const memCmp = compareNullable(ca.memory_gb ?? null, cb.memory_gb ?? null);
  if (memCmp !== 0) return memCmp;

  // Stable tie-break: lexical by resource_type across providers.
  const ta = a.resource_type ?? '';
  const tb = b.resource_type ?? '';
  return ta < tb ? -1 : ta > tb ? 1 : 0;
}

/**
 * extractCapacity pulls vcpu / memory_gb from an opaque details blob.
 * Returns null for non-compute payloads or absent/non-finite fields,
 * matching the ComputeDetails omitempty behaviour on the wire.
 *
 * Note: Number.isFinite() is used (not typeof === 'number') to reject NaN,
 * which passes the typeof check but would produce NaN subtraction results in
 * compareNullable and cause an unstable sort. NaN falls into the unknown bucket
 * and sorts last alongside null/absent/0 values.
 *
 * Intended consumer: the recommendations table capacity sort (clicking the
 * vCPU/Memory column header). Wiring that column requires a UX decision
 * (adding a new RecommendationsColumnId entry and a rendered column header);
 * see issue #82 for context.
 */
function extractCapacity(details: unknown): ComputeCapacity {
  if (
    details !== null &&
    typeof details === 'object' &&
    !Array.isArray(details)
  ) {
    const d = details as Record<string, unknown>;
    return {
      vcpu: Number.isFinite(d['vcpu']) ? (d['vcpu'] as number) : null,
      memory_gb: Number.isFinite(d['memory_gb']) ? (d['memory_gb'] as number) : null,
    };
  }
  return { vcpu: null, memory_gb: null };
}

/**
 * compareNullable sorts null/zero values after all known-positive values.
 * For two positive numbers it falls back to numeric ascending order.
 * A value of 0 is treated as "unknown" per the ComputeDetails convention.
 */
function compareNullable(a: number | null, b: number | null): number {
  const aUnknown = a === null || a === 0;
  const bUnknown = b === null || b === 0;

  if (aUnknown && bUnknown) return 0;
  if (aUnknown) return 1;  // a is unknown -> sort after b
  if (bUnknown) return -1; // b is unknown -> sort before it

  return (a as number) - (b as number);
}
