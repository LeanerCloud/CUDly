/**
 * Topbar filter chips (issue #344 T2).
 *
 * Carries the regression coverage that used to live in dashboard.test.ts
 * before the per-section provider/account <select>s were retired:
 *
 *   - Issue #185 ordering invariant: a provider change MUST clear
 *     state.currentAccountIDs BEFORE awaiting the new account-list
 *     fetch. Any reader that races us must see the cleared value, not
 *     the stale account id from the prior provider.
 *
 * The chip-select internals are covered by chip-select.test.ts; this
 * file only exercises the wiring topbar-filters.ts adds on top.
 */

jest.mock('../api', () => ({
  listAccounts: jest.fn(),
}));

import * as api from '../api';
import * as state from '../state';
import { initTopbarFilters } from '../topbar-filters';

function setupSlot(): void {
  while (document.body.firstChild) document.body.removeChild(document.body.firstChild);
  const slot = document.createElement('div');
  slot.id = 'topbar-filters';
  document.body.appendChild(slot);
}

describe('Topbar filters', () => {
  beforeEach(() => {
    jest.clearAllMocks();
    setupSlot();
    state.setCurrentProvider('');
    state.setCurrentAccountIDs([]);
    (api.listAccounts as jest.Mock).mockResolvedValue([]);
  });

  afterEach(() => {
    state.setCurrentProvider('');
    state.setCurrentAccountIDs([]);
  });

  test('initTopbarFilters mounts two chip-selects into the topbar slot', () => {
    initTopbarFilters();
    const slot = document.getElementById('topbar-filters');
    // Each chip is a `.chip-select-root` (see lib/chip-select.ts). Two
    // chips: provider + account.
    const chips = slot?.querySelectorAll('.chip-select-root');
    expect(chips?.length).toBe(2);
  });

  // Issue #185 ordering invariant: a provider change clears
  // state.currentAccountIDs BEFORE awaiting the new account-list refetch.
  // The implementation lives in topbar-filters.ts::initTopbarFilters'
  // onChange handler — see the inline comment there.
  test('provider change clears currentAccountIDs before listAccounts is awaited', async () => {
    state.setCurrentProvider('aws');
    state.setCurrentAccountIDs(['aws-acct-1']);

    // Block the in-flight account refetch so we can observe state mid-flight.
    let resolveFirst: ((value: unknown[]) => void) | undefined;
    const firstBlocker = new Promise<unknown[]>((r) => { resolveFirst = r; });
    let resolveSecond: ((value: unknown[]) => void) | undefined;
    const secondBlocker = new Promise<unknown[]>((r) => { resolveSecond = r; });
    (api.listAccounts as jest.Mock)
      .mockReturnValueOnce(firstBlocker)
      .mockReturnValueOnce(secondBlocker);

    initTopbarFilters();
    // Drain the initial populateAccountOptions call so subsequent
    // listAccounts() goes through secondBlocker.
    resolveFirst!([]);
    await new Promise(r => setTimeout(r, 0));

    // Locate the provider chip's trigger (first .chip-select.) and open it.
    const triggers = document.querySelectorAll<HTMLButtonElement>('.chip-select');
    expect(triggers.length).toBe(2);
    const providerTrigger = triggers[0] as HTMLButtonElement;
    providerTrigger.click();

    // Pick the "Azure" option by data-value.
    const azureOption = Array.from(
      document.querySelectorAll<HTMLLIElement>('.chip-select-option')
    ).find((el) => el.dataset['value'] === 'azure');
    expect(azureOption).toBeDefined();
    // chip-select listens on `mousedown` (not click) to dodge a focus
    // race with the listbox's close-on-blur — fire mousedown so the
    // option commits.
    azureOption!.dispatchEvent(new MouseEvent('mousedown', { bubbles: true }));

    // listAccounts({provider: 'azure'}) is now in-flight (blocked on
    // secondBlocker). Issue #185 invariant: state.currentAccountIDs
    // must already be cleared, even though the new account list hasn't
    // arrived yet.
    expect(state.getCurrentAccountIDs()).toEqual([]);
    expect(state.getCurrentProvider()).toBe('azure');

    // Release the refetch so the pending promise doesn't leak.
    resolveSecond!([]);
    await new Promise(r => setTimeout(r, 0));
  });
});
