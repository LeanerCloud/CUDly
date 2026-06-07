/**
 * Pre-purchase approval-confirmation modal body builder.
 *
 * Closes issue #374. Before this module existed, the confirm dialog
 * shown when a user clicks Approve only carried a static sentence and
 * the execution UUID — not informed consent for a financial commitment.
 *
 * This module fetches `/api/purchases/{id}` + `/api/accounts` and builds
 * an HTMLElement carrying:
 *   - An aggregate header (total upfront, total monthly + annual
 *     savings, recommendation count, distinct providers, distinct
 *     accounts touched).
 *   - A per-recommendation table: account / provider / service /
 *     resource type / engine / region / count / term + payment /
 *     upfront / monthly savings / effective savings % when the rec
 *     carries a non-null `on_demand_cost`.
 *
 * The pure builder `renderApprovalDetailsBody(details, accountsById)`
 * is split out from the network-fetching wrapper
 * `buildApprovalDetailsBody(executionId)` so unit tests can exercise
 * the rendering without mocking fetch. Both code paths share the same
 * fallback shape: when the GET fails (404, 403, network error) we
 * return a plain-text element preserving the legacy behaviour so the
 * dialog still opens and the Approve button still works — just without
 * the details. Showing a half-built modal would be worse UX than
 * gracefully falling back.
 */

import * as api from './api';
import type { CloudAccount } from './api/accounts';
import type { PurchaseDetails, Recommendation } from './api/types';
import { escapeHtml, formatCurrency, formatTerm } from './utils';

/**
 * accountsById maps the internal CloudAccount UUID (the value carried
 * on `Recommendation.cloud_account_id`) to the full CloudAccount
 * record. Used to resolve "acct-uuid" -> "Production AWS
 * (123456789012)" in the table without re-fetching the accounts list
 * per row.
 */
export type AccountsById = Map<string, CloudAccount>;

/**
 * Build an HTMLElement to pass as `body` to confirmDialog when the
 * user is about to approve a purchase. Renders the rich details from
 * `details.recommendations`, falling back to a plain text element
 * (with the legacy approval sentence) when no recs are present.
 *
 * Exported separately from `buildApprovalDetailsBody` so tests can
 * render the body deterministically — no fetch mocking required.
 */
export function renderApprovalDetailsBody(details: PurchaseDetails, accountsById: AccountsById, hostAWSAccountID?: string): HTMLElement {
  const root = document.createElement('div');
  root.className = 'approval-details';

  const recs = details.recommendations ?? [];
  if (recs.length === 0) {
    // Direct-execute paths (capacity_percent flow) can sometimes
    // race the JSONB write — surface the legacy sentence so the user
    // can still confirm rather than blocking the click on a missing
    // payload. Shared helper keeps the fallback text identical to
    // the network-failure branch.
    root.appendChild(buildApprovalDetailsFallback());
    return root;
  }

  root.appendChild(renderApprovalDetailsHeader(details, recs, hostAWSAccountID));
  root.appendChild(renderApprovalDetailsTable(recs, accountsById, hostAWSAccountID));
  return root;
}

/**
 * Render the at-a-glance stat row that sits above the per-rec table.
 * Each stat surfaces one number the user needs to weigh before
 * clicking Approve: total upfront, monthly + annual savings,
 * commitment count, distinct providers and distinct accounts touched.
 * The annual figure is derived from monthly × 12; if it diverges from
 * the sum of per-row monthly savings it's surfaced as a tooltip on
 * the stat so the user has a path to the underlying numbers.
 */
function renderApprovalDetailsHeader(details: PurchaseDetails, recs: Recommendation[], hostAWSAccountID?: string): HTMLElement {
  const header = document.createElement('div');
  header.className = 'approval-details-header';

  // Distinct provider / account counts give the user a quick
  // "am I committing across N clouds and M accounts?" signal
  // before they scroll the per-row table.
  const providers = new Set<string>();
  const accounts = new Set<string>();
  let totalMonthly = 0;
  for (const rec of recs) {
    if (rec.provider) providers.add(rec.provider);
    // Mirror the table's formatAccountLabel logic: if the rec has no
    // cloud_account_id but is an AWS rec and we know the host account,
    // count the host account — otherwise skip (orphan "Account deleted"
    // rows must not inflate the count).
    const effectiveAccountID = rec.cloud_account_id ?? (rec.provider === 'aws' && hostAWSAccountID ? hostAWSAccountID : null);
    if (effectiveAccountID) accounts.add(effectiveAccountID);
    // M-3: skip null savings in the aggregate rather than coercing to 0.
    // A null savings field means the data is absent, not that the saving is
    // zero; adding 0 would understate the real total (e.g. all rows null
    // would produce $0 total monthly savings on the approval header).
    if (rec.savings != null) totalMonthly += rec.savings;
  }
  const totalAnnual = totalMonthly * 12;

  // M-3: show '--' for absent total_upfront_cost rather than $0, which would
  // falsely imply no upfront charge applies. formatCurrency handles null -> '--'.
  const upfront = details.total_upfront_cost ?? null;
  // estimated_savings on the response is monthly; aggregating the
  // per-rec savings field above is identical math but lets the
  // table footer stay numerically in sync with the per-row column.
  const monthly = details.estimated_savings ?? totalMonthly;

  // Epsilon-tolerant comparison: strict !== on two floats that come
  // from independent sums (backend estimated_savings vs frontend
  // per-rec sum) will trigger on rounding noise as small as 1e-14,
  // making the "from per-row sum" tooltip fire on rows that are
  // conceptually equal. 0.005 keeps the tooltip silent up to the
  // sub-cent level, well below the precision formatCurrency renders.
  const annualMismatch = Math.abs(totalAnnual - monthly * 12) > 0.005;

  header.appendChild(headerStat('Upfront', formatCurrency(upfront)));
  header.appendChild(headerStat('Monthly savings', formatCurrency(monthly)));
  header.appendChild(headerStat('Annual savings', formatCurrency(monthly * 12), undefined, annualMismatch ? `${formatCurrency(totalAnnual)} from per-row sum` : undefined));
  header.appendChild(headerStat('Commitments', String(recs.length)));
  header.appendChild(headerStat('Providers', String(providers.size), Array.from(providers).join(', ')));
  header.appendChild(headerStat('Accounts', String(accounts.size)));

  return header;
}

/**
 * Build one label/value cell for the header grid. `hover` becomes the
 * value's title attribute (used to surface the comma-joined provider
 * list on the "Providers: 3" stat without crowding the visible cell);
 * `title` is set on the wrapping container for stats whose deeper
 * context belongs on the whole entry rather than just the value.
 */
function headerStat(label: string, value: string, hover?: string, title?: string): HTMLElement {
  const stat = document.createElement('div');
  stat.className = 'approval-details-stat';
  const labelEl = document.createElement('div');
  labelEl.className = 'approval-details-stat-label';
  labelEl.textContent = label;
  const valueEl = document.createElement('div');
  valueEl.className = 'approval-details-stat-value';
  valueEl.textContent = value;
  if (hover) valueEl.setAttribute('title', hover);
  if (title) stat.setAttribute('title', title);
  stat.appendChild(labelEl);
  stat.appendChild(valueEl);
  return stat;
}

/**
 * Render the per-recommendation table with sticky thead and one row
 * per rec. Wrapped in a scrollable container so a 20-rec execution
 * spanning AWS + Azure + GCP stays inside the modal viewport without
 * pushing the action buttons off-screen. Columns mirror the order
 * documented on issue #374: identity (account / provider / service /
 * resource / engine / region) -> sizing (count / term / payment) ->
 * money (upfront / monthly savings / effective savings %).
 */
function renderApprovalDetailsTable(recs: Recommendation[], accountsById: AccountsById, hostAWSAccountID?: string): HTMLElement {
  const wrap = document.createElement('div');
  wrap.className = 'approval-details-table-wrap';

  const table = document.createElement('table');
  table.className = 'approval-details-table';

  const thead = document.createElement('thead');
  thead.innerHTML = `
    <tr>
      <th>Account</th>
      <th>Provider</th>
      <th>Service</th>
      <th>Resource</th>
      <th>Engine</th>
      <th>Region</th>
      <th class="num">Count</th>
      <th>Term</th>
      <th>Payment</th>
      <th class="num">Upfront</th>
      <th class="num">Monthly savings</th>
      <th class="num">Eff. savings %</th>
    </tr>`;
  table.appendChild(thead);

  const tbody = document.createElement('tbody');
  for (const rec of recs) {
    tbody.appendChild(renderRecRow(rec, accountsById, hostAWSAccountID));
  }
  table.appendChild(tbody);
  wrap.appendChild(table);
  return wrap;
}

/**
 * Render one tbody row from a Recommendation. The account UUID is
 * resolved against accountsById via formatAccountLabel; engine falls
 * back to "—" for services that don't carry a DB engine (EC2, ALB);
 * effective savings % follows the same baseline-preferred /
 * reconstruction-fallback policy as the recommendations table (see
 * computeEffectiveSavingsPct). Every interpolated string flows
 * through escapeHtml so a tampered recommendation field can't inject
 * markup.
 */
function renderRecRow(rec: Recommendation, accountsById: AccountsById, hostAWSAccountID?: string): HTMLElement {
  const row = document.createElement('tr');
  const acct = rec.cloud_account_id ? accountsById.get(rec.cloud_account_id) : undefined;
  const accountLabel = formatAccountLabel(acct, rec.cloud_account_id, rec.provider ?? '', hostAWSAccountID);
  // rec.engine is a string for DB-shaped services and "" otherwise;
  // both `undefined` and "" are falsy so the single check is enough.
  const engineLabel = rec.engine ? rec.engine : '—';
  const effSavings = computeEffectiveSavingsPct(rec);
  // innerHTML is safe here because every interpolated value goes
  // through escapeHtml or is a numeric/preformatted constant. Using
  // innerHTML rather than 12 createElement calls per row keeps the
  // render code readable for the table layout, mirroring the
  // recommendations.ts pattern.
  row.innerHTML = `
    <td>${escapeHtml(accountLabel)}</td>
    <td>${escapeHtml(rec.provider ?? '')}</td>
    <td>${escapeHtml(rec.service ?? '')}</td>
    <td>${escapeHtml(rec.resource_type ?? '')}</td>
    <td>${escapeHtml(engineLabel)}</td>
    <td>${escapeHtml(rec.region ?? '')}</td>
    <td class="num">${escapeHtml(String(rec.count ?? 0))}</td>
    <td>${escapeHtml(formatTerm(rec.term))}</td>
    <td>${escapeHtml(rec.payment ?? '')}</td>
    <td class="num">${escapeHtml(formatCurrency(rec.upfront_cost ?? null))}</td>
    <td class="num">${escapeHtml(formatCurrency(rec.savings ?? null))}</td>
    <td class="num">${effSavings === null ? '—' : escapeHtml(`${effSavings.toFixed(1)}%`)}</td>`;
  return row;
}

/**
 * Compose the user-facing account label.
 *
 *   - When the account record is resolved, render "Name (external_id)".
 *   - When only the UUID is on the rec (account not yet listed because
 *     of a stale cache or a deletion race), show the first 8 chars of
 *     the UUID so the user can still cross-reference.
 *   - When the rec carries no account at all, distinguish two cases
 *     (issue #608):
 *       a) provider === 'aws' AND hostAWSAccountID is known: the rec
 *          targets the CUDly Lambda's own account — label it as the
 *          host account so the operator sees exactly which account
 *          will be charged.
 *       b) Any other combination (Azure/GCP without an account, or AWS
 *          but host account ID is unavailable): the account was almost
 *          certainly deleted. Show a warning so the operator knows to
 *          cancel rather than approve.
 */
export function formatAccountLabel(
  acct: CloudAccount | undefined,
  recAccountId: string | undefined,
  provider: string,
  hostAWSAccountID: string | undefined,
): string {
  if (acct) {
    if (acct.external_id) return `${acct.name} (${acct.external_id})`;
    return acct.name;
  }
  if (recAccountId) {
    return `acct ${recAccountId.slice(0, 8)}…`;
  }
  // No account on the rec. Distinguish 'genuine ambient' from 'deleted'.
  if (provider === 'aws' && hostAWSAccountID) {
    return `CUDly host (${hostAWSAccountID})`;
  }
  return '⚠ Account deleted — purchase cannot execute';
}

/**
 * Compute the "effective savings %" the same way the recommendations
 * table does (see recommendations.ts) — prefer the raw on_demand_cost
 * baseline when the provider returned one. Returns null when we can't
 * confidently compute (no on-demand baseline AND the reconstructed
 * denominator would be zero or negative) so the table can render "—"
 * instead of an inflated %.
 *
 * Formula: savings / on_demand_cost × 100, monthly-normalized.
 * `savings` on the rec is already the monthly savings; the upfront
 * cost is amortized into the rec.savings during recommendation
 * collection, so we don't re-add it here.
 */
export function computeEffectiveSavingsPct(rec: Recommendation): number | null {
  if (rec.savings === null || rec.savings === undefined) {
    return null;
  }
  if (rec.on_demand_cost !== undefined && rec.on_demand_cost !== null && rec.on_demand_cost > 0) {
    return (rec.savings / rec.on_demand_cost) * 100;
  }
  // Fallback: reconstruct on-demand from monthly + savings. Only valid
  // when monthly_cost is present and positive — for all-upfront recs
  // with monthly_cost = 0 the reconstruction collapses (see #274).
  if (rec.monthly_cost !== null && rec.monthly_cost !== undefined && rec.monthly_cost > 0) {
    const reconstructed = rec.monthly_cost + rec.savings;
    if (reconstructed > 0) return (rec.savings / reconstructed) * 100;
  }
  return null;
}

/**
 * Network-fetching wrapper. Fetches `/api/purchases/{id}` + the
 * accounts list in parallel, builds the rich body, and returns it.
 * Any fetch failure falls back to a plain-text element carrying the
 * legacy approval sentence so confirmDialog still works.
 */
export async function buildApprovalDetailsBody(executionId: string): Promise<HTMLElement> {
  try {
    // Fetch purchase details, account list, and public info in parallel.
    // listAccounts and getPublicInfo are caught inline so the modal still
    // renders the full details when either endpoint is unreachable; the
    // per-rec table degrades gracefully. console.warn keeps failures
    // traceable rather than silently dropping them.
    const [details, accounts, deploymentInfo] = await Promise.all([
      api.getPurchaseDetails(executionId),
      api.listAccounts().catch((err) => {
        console.warn('Failed to load accounts for approval modal — falling back to UUID-prefixed labels:', err);
        return [] as CloudAccount[];
      }),
      // getDeploymentInfo requires an authenticated session (AuthUser). The
      // approval modal is only reachable post-login, so this is safe. On
      // failure (e.g. token expiry) the orphan label degrades to "Account deleted"
      // which is the safe default (#633).
      api.getDeploymentInfo().catch((err) => {
        console.warn('Failed to load deployment info for approval modal — orphan label will show "Account deleted":', err);
        return undefined;
      }),
    ]);
    const accountsById = new Map<string, CloudAccount>();
    for (const acct of accounts) accountsById.set(acct.id, acct);
    const hostAWSAccountID = deploymentInfo?.deployment_aws_account_id;
    return renderApprovalDetailsBody(details, accountsById, hostAWSAccountID);
  } catch (err) {
    console.error('Failed to load purchase details for approval modal:', err);
    return buildApprovalDetailsFallback();
  }
}

/**
 * Build the legacy approval sentence as a standalone fallback
 * element. Extracted so the two failure paths (empty recommendations
 * + fetch failure) render identical text and class hooks; previously
 * the literal string lived in two places and could drift.
 */
function buildApprovalDetailsFallback(): HTMLElement {
  const fallback = document.createElement('div');
  fallback.className = 'approval-details-fallback';
  fallback.textContent = 'This authorises the purchase to execute. Cloud commitments will be charged once the executor picks up the approved row.';
  return fallback;
}
