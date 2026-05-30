/**
 * isPaymentSupported is a UI-side guardrail that hides payment options
 * the backend providers won't produce recommendations for. The backend
 * itself still accepts every combination (RDS 3yr no-upfront just logs
 * a warning in `cmd/validators.go:warnRDS3YearNoUpfront`); this function
 * exists so the Purchase flow doesn't offer a choice the user will
 * definitely get back an empty result for.
 *
 * Rules are grounded in real provider code — each case cites the file
 * that defines the acceptance. Everything not listed defaults to true
 * (conservative: only hide options we've positively confirmed don't
 * work).
 */

export type Provider = 'aws' | 'azure' | 'gcp';
export type Term = 1 | 3;
export type Payment =
  | 'all-upfront'
  | 'upfront' // Azure / GCP synonym for all-upfront
  | 'partial-upfront'
  | 'no-upfront'
  | 'monthly';

export function isPaymentSupported(
  provider: Provider,
  service: string,
  term: Term,
  payment: Payment,
): boolean {
  if (provider === 'aws') {
    // AWS RDS 3-year RIs don't support no-upfront — only all-upfront
    // and partial-upfront are offered by the provider. Surfacing
    // no-upfront here would produce zero recommendations for the
    // user. See cmd/validators.go:warnRDS3YearNoUpfront.
    if (service === 'rds' && term === 3 && payment === 'no-upfront') {
      return false;
    }
    return true;
  }

  // Azure provider clients (compute / cache / cosmosdb / database /
  // search) accept only `all-upfront` / `upfront` / `monthly` — see
  // providers/azure/services/*/client.go case statements.
  if (provider === 'azure') {
    return payment === 'all-upfront' || payment === 'upfront' || payment === 'monthly';
  }

  // GCP CUDs accept the same three options as Azure per
  // providers/gcp/services/computeengine/client.go:554.
  if (provider === 'gcp') {
    return payment === 'all-upfront' || payment === 'upfront' || payment === 'monthly';
  }

  return true;
}

/**
 * paymentOptionsFor returns the valid Payment values for a given
 * provider + service + term, in canonical display order. Used to
 * populate the bulk-purchase Payment dropdown after the user narrows
 * the filter to a single provider+service.
 */
export function paymentOptionsFor(provider: Provider, service: string, term: Term): Payment[] {
  const candidates: Payment[] = ['all-upfront', 'partial-upfront', 'no-upfront', 'monthly'];
  return candidates.filter((p) => isPaymentSupported(provider, service, term, p));
}

// Mirror of common.IsSavingsPlan (pkg/common/types.go). PR #123 split a
// single 'savings-plans' service into four plan-type slugs
// (savings-plans-{compute,ec2instance,sagemaker,database}). Code that
// needs to treat all four as one logical group — service-filter
// "All Savings Plans" affordance, bulk-buy bucketing, aggregate
// filters — uses this predicate. Kept as a `startsWith` rather than a
// hardcoded set so a future plan type added on the backend
// (`common.IsSavingsPlan` is also `HasPrefix`) is picked up without a
// frontend edit.
//
// Issue #658: Azure Savings Plans use the umbrella slug "savingsplans"
// (no hyphen) — the same constant as common.ServiceSavingsPlans. The
// Go IsSavingsPlan function handles this via an explicit equality check
// (`string(s) == "savingsplans"`); we mirror that here so Azure SP rows
// are grouped correctly in the filter popover and bulk-buy bucketing.
export function isSavingsPlanService(service: string): boolean {
  return service.startsWith('savings-plans') || service === 'savingsplans';
}

// SAVINGS_PLANS_BUCKET_KEY is the canonical service slug used in the
// bulk-buy bucket key for any SP rec, so all four plan types collapse
// into one bucket. Each rec keeps its real per-plan-type service slug
// — only the bucket key is canonicalized.
export const SAVINGS_PLANS_BUCKET_KEY = 'savings-plans';

// Pretty short label per SP plan type used inside the mixed-bucket
// label (e.g. "Savings Plans (Compute + SageMaker)"). Mirrors the
// abbreviations in plans.ts:planServiceLabel without coupling to it.
const SP_SHORT_LABEL: Record<string, string> = {
  'savings-plans-compute':     'Compute',
  'savings-plans-ec2instance': 'EC2 Instance',
  'savings-plans-sagemaker':   'SageMaker',
  'savings-plans-database':    'Database',
};

// UMBRELLA_SLUGS are SP slugs that identify the family as a whole rather
// than a specific plan type; they are excluded from the parenthetical
// plan-type list in savingsPlansBucketLabel so a bucket of Azure SP recs
// renders as "Savings Plans" rather than the raw slug.
//
// Issue #658: "savingsplans" (no hyphen) is the Azure SP client's service
// slug and the legacy AWS SP umbrella (common.ServiceSavingsPlans). It is
// a family marker, not a plan-type label, so it is treated the same way
// as SAVINGS_PLANS_BUCKET_KEY.
export const UMBRELLA_SLUGS = new Set<string>([SAVINGS_PLANS_BUCKET_KEY, 'savingsplans']);

// savingsPlansBucketLabel formats the bulk-buy bucket title for one
// or more SP plan types. Returns:
//   - 'Savings Plans (Compute)' for a single named plan type
//   - 'Savings Plans (Compute + SageMaker)' for a mixed bucket
//   - 'Savings Plans' for umbrella/Azure SP slugs or when no types resolve
// Plan-type order in the output follows insertion order of the input
// slugs — caller controls the order.
export function savingsPlansBucketLabel(serviceSlugs: readonly string[]): string {
  const seen = new Set<string>();
  const parts: string[] = [];
  for (const slug of serviceSlugs) {
    if (!isSavingsPlanService(slug) || seen.has(slug)) continue;
    // Skip umbrella slugs (bucket-key marker and the "savingsplans" Azure
    // SP / legacy AWS SP identifier) — they represent the family, not a
    // specific plan type, so rendering them in the parentheses would be
    // misleading.
    if (UMBRELLA_SLUGS.has(slug)) continue;
    seen.add(slug);
    parts.push(SP_SHORT_LABEL[slug] ?? slug);
  }
  if (parts.length === 0) return 'Savings Plans';
  return `Savings Plans (${parts.join(' + ')})`;
}
