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

let providerChip: ChipSelectHandle | null = null;
let accountChip: ChipSelectHandle | null = null;

/**
 * Build the account chip's option list from the current provider context.
 * Always includes the "All Accounts" option at the top.
 */
async function populateAccountOptions(provider: string): Promise<void> {
  if (!accountChip) return;
  try {
    const filter =
      provider && provider !== '' && provider !== 'all'
        ? { provider: provider as 'aws' | 'azure' | 'gcp' }
        : undefined;
    const accounts = await api.listAccounts(filter);
    const options: ChipSelectOption[] = [
      { value: '', label: 'All Accounts' },
      ...accounts.map((a) => ({
        value: a.id,
        label: `${a.name} (${a.external_id})`,
      })),
    ];
    accountChip.setOptions(options);
  } catch (err) {
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
    },
  });

  slot.appendChild(providerChip.root);
  slot.appendChild(accountChip.root);

  // Kick off the initial account list fetch.
  void populateAccountOptions(state.getCurrentProvider());
}
