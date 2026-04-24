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
