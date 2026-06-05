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
  // #949/#951: the dropdown now uses the minimal-disclosure endpoint
  // (view:recommendations) so it populates for Standard / Read-Only users.
  listAccountsMinimal: jest.fn(),
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
    (api.listAccountsMinimal as jest.Mock).mockResolvedValue([]);
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

  // #951: a Standard / Read-Only user (who can call listAccountsMinimal but
  // would 403 on the full listAccounts) gets a populated Account dropdown.
  // The mock stands in for the minimal endpoint succeeding for such a user.
  test('populates the account dropdown from the minimal endpoint (Standard user, #951)', async () => {
    (api.listAccountsMinimal as jest.Mock).mockResolvedValue([
      { id: 'acct-1', name: 'Prod', external_id: '111', provider: 'aws' },
      { id: 'acct-2', name: 'Dev', external_id: '222', provider: 'aws' },
    ]);

    initTopbarFilters();
    await new Promise((r) => setTimeout(r, 0));

    expect(api.listAccountsMinimal).toHaveBeenCalled();

    // Open the account chip (second trigger) and assert the real accounts show
    // up beyond the seed "All Accounts" option.
    const triggers = document.querySelectorAll<HTMLButtonElement>('.chip-select');
    (triggers[1] as HTMLButtonElement).click();
    const optionValues = Array.from(
      document.querySelectorAll<HTMLLIElement>('.chip-select-option'),
    ).map((el) => el.dataset['value']);
    expect(optionValues).toContain('acct-1');
    expect(optionValues).toContain('acct-2');
  });

  // Issue #477: URL hydration + writeback tests.
  //
  // jsdom honours history.replaceState() — setting it before initTopbarFilters
  // simulates a hard refresh on a page that was previously filtered. We assert
  // the chip-select's seed `value:` was hydrated from the URL by checking the
  // post-init state via the state setters that the chip's onChange would fire.
  describe('URL persistence', () => {
    afterEach(() => {
      // Reset URL so cross-test bleed doesn't pollute the next test.
      window.history.replaceState({}, '', '/opportunities');
    });

    test('hydrates provider + account from URL query params before chips mount', () => {
      window.history.replaceState({}, '', '/opportunities?provider=aws&account=acct-7');

      initTopbarFilters();

      expect(state.getCurrentProvider()).toBe('aws');
      expect(state.getCurrentAccountIDs()).toEqual(['acct-7']);
    });

    test('ignores invalid provider values from URL (falls back to All)', () => {
      window.history.replaceState({}, '', '/opportunities?provider=bogus&account=acct-1');

      initTopbarFilters();

      expect(state.getCurrentProvider()).toBe('');
      // Account is opaque — passes through.
      expect(state.getCurrentAccountIDs()).toEqual(['acct-1']);
    });

    test('account-chip change writes back to URL query params', async () => {
      window.history.replaceState({}, '', '/opportunities');
      (api.listAccountsMinimal as jest.Mock).mockResolvedValue([
        { id: 'acct-99', name: 'prod', external_id: '999' },
      ]);

      initTopbarFilters();
      // Drain the async populateAccountOptions so the account chip carries
      // the real option list before we exercise its trigger.
      await new Promise((r) => setTimeout(r, 0));

      // Account chip is the second `.chip-select` trigger.
      const triggers = document.querySelectorAll<HTMLButtonElement>('.chip-select');
      const accountTrigger = triggers[1] as HTMLButtonElement;
      accountTrigger.click();

      const opt = Array.from(
        document.querySelectorAll<HTMLLIElement>('.chip-select-option'),
      ).find((el) => el.dataset['value'] === 'acct-99');
      expect(opt).toBeDefined();
      opt!.dispatchEvent(new MouseEvent('mousedown', { bubbles: true }));

      const params = new URLSearchParams(window.location.search);
      expect(params.get('account')).toBe('acct-99');
    });

    test('selecting "All Accounts" removes the account query param', async () => {
      // Start with an account already selected via URL.
      window.history.replaceState({}, '', '/opportunities?account=acct-7');
      (api.listAccountsMinimal as jest.Mock).mockResolvedValue([
        { id: 'acct-7', name: 'old', external_id: '7' },
      ]);

      initTopbarFilters();
      await new Promise((r) => setTimeout(r, 0));

      // Account chip is the second `.chip-select`.
      const triggers = document.querySelectorAll<HTMLButtonElement>('.chip-select');
      const accountTrigger = triggers[1] as HTMLButtonElement;
      accountTrigger.click();
      const allOpt = Array.from(
        document.querySelectorAll<HTMLLIElement>('.chip-select-option'),
      ).find((el) => el.dataset['value'] === '');
      expect(allOpt).toBeDefined();
      allOpt!.dispatchEvent(new MouseEvent('mousedown', { bubbles: true }));

      const params = new URLSearchParams(window.location.search);
      expect(params.has('account')).toBe(false);
    });

    test('provider change writes provider AND clears account in URL', async () => {
      window.history.replaceState({}, '', '/opportunities?account=acct-7');

      initTopbarFilters();
      await new Promise((r) => setTimeout(r, 0));

      const triggers = document.querySelectorAll<HTMLButtonElement>('.chip-select');
      const providerTrigger = triggers[0] as HTMLButtonElement;
      providerTrigger.click();
      const gcpOpt = Array.from(
        document.querySelectorAll<HTMLLIElement>('.chip-select-option'),
      ).find((el) => el.dataset['value'] === 'gcp');
      expect(gcpOpt).toBeDefined();
      gcpOpt!.dispatchEvent(new MouseEvent('mousedown', { bubbles: true }));

      const params = new URLSearchParams(window.location.search);
      expect(params.get('provider')).toBe('gcp');
      // Account was cleared by the #185 ordering rule; URL must reflect that.
      expect(params.has('account')).toBe(false);
    });
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
    (api.listAccountsMinimal as jest.Mock)
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
