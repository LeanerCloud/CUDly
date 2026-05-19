/**
 * Topbar filters (issue #344 T2).
 *
 * Owns the two global filter chips (Provider + Account) that sit in the
 * topbar after the brand. Each chip updates `state.ts` and the state's
 * subscription pattern (subscribeProvider / subscribeAccount) fans the
 * change out to every section that has registered a reload callback.
 *
 * Replaces the per-section provider/account `<select>` dropdowns that
 * Home / Plans / Purchases used to carry. Those are removed from
 * index.html in the same PR; sections register their reload callback
 * via state.subscribe* instead of binding to a DOM `change` event.
 *
 * Pre-#344 the filter logic in dashboard.ts had a subtle ordering rule
 * (issue #185 fix: clear accounts in state BEFORE awaiting
 * populateAccountFilter, so any in-flight read sees the cleared value).
 * That same rule applies here — keep it in the provider-change handler.
 */

import * as api from './api';
import * as state from './state';
import { createChipSelect, type ChipSelectHandle, type ChipSelectOption } from './lib/chip-select';

const PROVIDER_OPTIONS: ChipSelectOption[] = [
  { value: '', label: 'All Providers' },
  { value: 'aws', label: 'AWS' },
  { value: 'azure', label: 'Azure' },
  { value: 'gcp', label: 'GCP' },
];

// Issue #477: URL query params used to persist the global filter across
// hard refresh. URL is preferred over localStorage so the filter state is
// shareable via copy-paste link and avoids the cross-tab confusion that
// shared localStorage would create. Empty values omit the param entirely
// so `/opportunities` stays clean when nothing is filtered.
const URL_PARAM_PROVIDER = 'provider';
const URL_PARAM_ACCOUNT = 'account';

// Closed set used to reject invalid `?provider=` values that would otherwise
// flow into setCurrentProvider — which is typed `'' | 'aws' | 'azure' | 'gcp'`.
const VALID_PROVIDERS: ReadonlySet<string> = new Set(['', 'aws', 'azure', 'gcp']);

/**
 * Read provider + account from the current URL. Invalid provider values
 * (anything outside VALID_PROVIDERS) fall back to ''. The account id is
 * returned verbatim — validation against the user's actual account list
 * happens implicitly via the chip-select option matcher.
 */
function readFiltersFromURL(): { provider: '' | 'aws' | 'azure' | 'gcp'; account: string } {
  const params = new URLSearchParams(window.location.search);
  const rawProvider = params.get(URL_PARAM_PROVIDER) ?? '';
  const provider = VALID_PROVIDERS.has(rawProvider)
    ? (rawProvider as '' | 'aws' | 'azure' | 'gcp')
    : '';
  const account = params.get(URL_PARAM_ACCOUNT) ?? '';
  return { provider, account };
}

/**
 * Write provider + account back into `window.location.search` via
 * replaceState (not pushState — filter changes are a setting, not a new
 * navigation entry; the browser back button should not unwind individual
 * chip clicks). Empty values are omitted so the URL stays clean.
 */
function writeFiltersToURL(provider: string, accountIDs: readonly string[]): void {
  const params = new URLSearchParams(window.location.search);
  if (provider) params.set(URL_PARAM_PROVIDER, provider);
  else params.delete(URL_PARAM_PROVIDER);
  const accountId = accountIDs[0] ?? '';
  if (accountId) params.set(URL_PARAM_ACCOUNT, accountId);
  else params.delete(URL_PARAM_ACCOUNT);
  const qs = params.toString();
  const url = window.location.pathname + (qs ? '?' + qs : '') + window.location.hash;
  window.history.replaceState(window.history.state, '', url);
}

let providerChip: ChipSelectHandle | null = null;
let accountChip: ChipSelectHandle | null = null;

// Monotonically-increasing request counter. Each populateAccountOptions call
// captures its generation before awaiting; the result is discarded if a newer
// call has started, preventing stale responses from overwriting fresh options.
let _accountRequestGen = 0;

/**
 * Build the account chip's option list from the current provider context.
 * Always includes the "All Accounts" option at the top.
 * Uses a generation counter to discard responses from superseded requests.
 */
async function populateAccountOptions(provider: string): Promise<void> {
  if (!accountChip) return;
  const gen = ++_accountRequestGen;
  try {
    const filter =
      provider && provider !== '' && provider !== 'all'
        ? { provider: provider as 'aws' | 'azure' | 'gcp' }
        : undefined;
    const accounts = await api.listAccounts(filter);
    // Discard if a newer request has already started.
    if (gen !== _accountRequestGen) return;
    const options: ChipSelectOption[] = [
      { value: '', label: 'All Accounts' },
      ...accounts.map((a) => ({
        value: a.id,
        label: `${a.name} (${a.external_id})`,
      })),
    ];
    accountChip.setOptions(options);
  } catch (err) {
    if (gen !== _accountRequestGen) return;
    console.warn('topbar-filters: failed to list accounts for chip:', err);
    accountChip.setOptions([{ value: '', label: 'All Accounts' }]);
  }
}

/**
 * Initialise the topbar filter chips. Idempotent — calling again rebuilds
 * the chips in case the slot was wiped (it shouldn't be, but defensive).
 *
 * Returns immediately; the initial account list populates asynchronously
 * in the background.
 */
export function initTopbarFilters(): void {
  const slot = document.getElementById('topbar-filters');
  if (!slot) {
    console.warn('topbar-filters: #topbar-filters slot not in DOM, skipping');
    return;
  }

  // Issue #477: hydrate provider + account from URL query params BEFORE
  // building the chips so each chip's `value:` seed picks up the persisted
  // selection. Direct state setters here would fire the subscribers, but
  // the subscribers are registered later in setupRecommendationsHandlers
  // (after init() returns) so this is a no-op for listeners and just seeds
  // the read path for the chip mount below.
  const { provider: urlProvider, account: urlAccount } = readFiltersFromURL();
  if (state.getCurrentProvider() !== urlProvider) {
    state.setCurrentProvider(urlProvider);
  }
  const currentAccountIDs = state.getCurrentAccountIDs();
  const accountChanged =
    (urlAccount && currentAccountIDs[0] !== urlAccount) ||
    (!urlAccount && currentAccountIDs.length > 0);
  if (accountChanged) {
    state.setCurrentAccountIDs(urlAccount ? [urlAccount] : []);
  }

  // Tear down any prior chips before re-mount (idempotent).
  while (slot.firstChild) slot.removeChild(slot.firstChild);
  providerChip = null;
  accountChip = null;

  providerChip = createChipSelect({
    label: 'Provider',
    options: PROVIDER_OPTIONS,
    value: state.getCurrentProvider(),
    onChange: (newProvider) => {
      // Per issue #185 ordering: clear account selection in state BEFORE
      // we await the account-list refetch. Any in-flight reader that
      // races us sees the cleared value, not the stale account id from
      // the prior provider.
      state.setCurrentAccountIDs([]);
      state.setCurrentProvider(newProvider as '' | 'aws' | 'azure' | 'gcp');
      // Issue #477: persist the new selection (and the now-cleared account)
      // to the URL so a hard refresh restores the same view.
      writeFiltersToURL(newProvider, []);
      // Refresh account options for the new provider; chip resets to
      // "All Accounts".
      void populateAccountOptions(newProvider).then(() => {
        accountChip?.setValue('');
      });
    },
  });

  accountChip = createChipSelect({
    label: 'Account',
    // Seed with just "All Accounts"; real list arrives after
    // populateAccountOptions resolves.
    options: [{ value: '', label: 'All Accounts' }],
    value: state.getCurrentAccountIDs()[0] ?? '',
    onChange: (newAccountId) => {
      state.setCurrentAccountIDs(newAccountId ? [newAccountId] : []);
      // Issue #477: keep URL in sync with the new account selection.
      writeFiltersToURL(state.getCurrentProvider(), newAccountId ? [newAccountId] : []);
    },
  });

  slot.appendChild(providerChip.root);
  slot.appendChild(accountChip.root);

  // Kick off the initial account list fetch.
  void populateAccountOptions(state.getCurrentProvider());
}
