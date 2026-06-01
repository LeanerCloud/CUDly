/**
 * Inventory & Coverage section tests (issue #340 T4, #754, #866).
 *
 * Verifies the sub-tab switching machinery for the umbrella section AND
 * the per-commitment / coverage fetch+render flows, AND the chip-
 * subscription pattern (issue #866: Main Header chips must propagate to
 * both sub-tabs).
 */

// loadRIExchange is a side-effect import from a module that touches the
// network in real use. Mock it out so the test only exercises sub-section
// switching, not real RI fetch.
jest.mock('../riexchange', () => ({
  loadRIExchange: jest.fn(),
}));

// The active-commitments and coverage load paths hit the API. Mock the
// entire api barrel so we don't need to stand up fetch — tests exercise
// the render machinery, not the network shape.
jest.mock('../api', () => ({
  listActiveCommitments: jest.fn(),
  getCoverageBreakdown: jest.fn(),
}));

// Mock state so chip subscription tests can control provider/account
// values and capture the registered callbacks. subscribeProvider and
// subscribeAccount must return a function (the unsubscribe handle) since
// inventory.ts calls the return value to tear down old subscriptions on
// repeated loadInventory() calls (feedback_event_listener_dedup pattern).
jest.mock('../state', () => ({
  subscribeProvider: jest.fn(() => jest.fn()),
  subscribeAccount: jest.fn(() => jest.fn()),
  getCurrentProvider: jest.fn(() => ''),
  getCurrentAccountIDs: jest.fn(() => []),
}));

// inventory.ts routes sub-nav clicks through navigation.switchInventorySubTab
// so the click both switches the view AND pushes /inventory/<subtab>
// (QA A.4). Mock it to delegate to the real (pure) view switcher: that
// keeps the click->DOM behaviour these tests assert, while letting us spy
// on the router call. The URL-push half of switchInventorySubTab is tested
// in navigation.test.ts where the router owns history.
jest.mock('../navigation', () => {
  const actual = jest.requireActual('../inventory');
  return {
    switchInventorySubTab: jest.fn((name: string) => actual.switchInventorySubSection(name)),
  };
});

import { loadInventory, switchInventorySubSection, loadActiveCommitments, loadCoverageBreakdown } from '../inventory';
import { switchInventorySubTab } from '../navigation';
import { loadRIExchange } from '../riexchange';
import * as api from '../api';
import * as state from '../state';
import type { ProviderCoverageSection } from '../api';

function buildInventoryDOM(): void {
  // Build the inventory tab + sub-nav via DOM methods rather than an
  // innerHTML string. Matches the no-innerHTML-with-interpolated-data
  // constraint from issue #340's plan.
  const tab = document.createElement('div');
  tab.id = 'inventory-tab';
  tab.classList.add('tab-content', 'active');

  const subnav = document.createElement('div');
  subnav.classList.add('inventory-subnav');

  for (const [name, label, isActive] of [
    ['active-commitments', 'Active commitments', true],
    ['coverage', 'Coverage', false],
    ['ri-exchange', 'RI Exchange', false],
  ] as const) {
    const btn = document.createElement('button');
    btn.classList.add('sub-tab-btn');
    if (isActive) btn.classList.add('active');
    btn.dataset['invSubtab'] = name;
    btn.setAttribute('role', 'tab');
    btn.setAttribute('aria-selected', isActive ? 'true' : 'false');
    btn.textContent = label;
    subnav.appendChild(btn);
  }
  tab.appendChild(subnav);

  // active-commitments section: contains the real refresh button + list
  // container (matches index.html shape).
  const ac = document.createElement('section');
  ac.id = 'inventory-active-commitments';
  const refresh = document.createElement('button');
  refresh.id = 'active-commitments-refresh-btn';
  refresh.textContent = 'Refresh';
  ac.appendChild(refresh);
  const list = document.createElement('div');
  list.id = 'active-commitments-list';
  ac.appendChild(list);
  tab.appendChild(ac);

  // coverage section: matches the real HTML structure with refresh button +
  // providers container that loadCoverageBreakdown renders into.
  const coverageSection = document.createElement('section');
  coverageSection.id = 'inventory-coverage';
  coverageSection.classList.add('hidden');
  const coverageRefresh = document.createElement('button');
  coverageRefresh.id = 'coverage-refresh-btn';
  coverageRefresh.textContent = 'Refresh';
  coverageSection.appendChild(coverageRefresh);
  const coverageProviders = document.createElement('div');
  coverageProviders.id = 'coverage-providers';
  coverageSection.appendChild(coverageProviders);
  tab.appendChild(coverageSection);

  const riSection = document.createElement('section');
  riSection.id = 'inventory-ri-exchange';
  riSection.classList.add('hidden');
  riSection.textContent = 'ri-exchange';
  tab.appendChild(riSection);

  document.body.appendChild(tab);
}

function makeCommitment(overrides: Partial<api.InventoryCommitment> = {}): api.InventoryCommitment {
  return {
    id: 'acc-1:p-1',
    provider: 'aws',
    account_id: 'acc-1',
    account_name: 'Prod Account',
    service: 'ec2',
    resource_type: 'm5.large',
    region: 'us-east-1',
    count: 4,
    term_years: 1,
    payment_option: 'no-upfront',
    start_date: '2025-01-01T00:00:00Z',
    end_date: '2026-01-01T00:00:00Z',
    upfront_cost: 0,
    monthly_cost: 240.5,
    estimated_savings: 80.0,
    status: 'active',
    ...overrides,
  };
}

function clearDOM(): void {
  while (document.body.firstChild) document.body.removeChild(document.body.firstChild);
}

describe('Inventory & Coverage sub-section switching', () => {
  beforeEach(() => {
    buildInventoryDOM();
    (loadRIExchange as jest.Mock).mockClear();
    // listActiveCommitments is invoked when switching to the
    // active-commitments sub-tab; default to a resolved-empty so the
    // switching tests don't need to care about the fetch outcome.
    (api.listActiveCommitments as jest.Mock).mockReset();
    (api.listActiveCommitments as jest.Mock).mockResolvedValue([]);
    // getCoverageBreakdown is invoked when switching to the coverage sub-tab.
    (api.getCoverageBreakdown as jest.Mock).mockReset();
    (api.getCoverageBreakdown as jest.Mock).mockResolvedValue({ providers: [] });
    // Reset state mocks to defaults. subscribeProvider/Account must keep
    // returning an unsubscribe function so inventory.ts can call it during
    // teardown without throwing.
    (state.subscribeProvider as jest.Mock).mockReset().mockReturnValue(jest.fn());
    (state.subscribeAccount as jest.Mock).mockReset().mockReturnValue(jest.fn());
    (state.getCurrentProvider as jest.Mock).mockReturnValue('');
    (state.getCurrentAccountIDs as jest.Mock).mockReturnValue([]);
  });

  afterEach(() => {
    clearDOM();
  });

  test('switchInventorySubSection shows active-commitments and hides others', () => {
    switchInventorySubSection('active-commitments');

    expect(document.getElementById('inventory-active-commitments')?.classList.contains('hidden')).toBe(false);
    expect(document.getElementById('inventory-coverage')?.classList.contains('hidden')).toBe(true);
    expect(document.getElementById('inventory-ri-exchange')?.classList.contains('hidden')).toBe(true);

    const activeBtn = document.querySelector('[data-inv-subtab="active-commitments"]');
    expect(activeBtn?.classList.contains('active')).toBe(true);
    expect(activeBtn?.getAttribute('aria-selected')).toBe('true');

    // Switching to active-commitments triggers the fetch — same shape
    // as the ri-exchange sub-tab triggers loadRIExchange.
    expect(api.listActiveCommitments).toHaveBeenCalledTimes(1);
  });

  test('switchInventorySubSection shows coverage and hides others', () => {
    switchInventorySubSection('coverage');

    expect(document.getElementById('inventory-coverage')?.classList.contains('hidden')).toBe(false);
    expect(document.getElementById('inventory-active-commitments')?.classList.contains('hidden')).toBe(true);
    expect(document.getElementById('inventory-ri-exchange')?.classList.contains('hidden')).toBe(true);
  });

  test('switchInventorySubSection shows ri-exchange and calls loadRIExchange', () => {
    switchInventorySubSection('ri-exchange');

    expect(document.getElementById('inventory-ri-exchange')?.classList.contains('hidden')).toBe(false);
    expect(document.getElementById('inventory-active-commitments')?.classList.contains('hidden')).toBe(true);
    expect(document.getElementById('inventory-coverage')?.classList.contains('hidden')).toBe(true);
    expect(loadRIExchange).toHaveBeenCalledTimes(1);
  });

  test('switchInventorySubSection falls back to active-commitments for unknown sub-section', () => {
    switchInventorySubSection('something-unknown');

    expect(document.getElementById('inventory-active-commitments')?.classList.contains('hidden')).toBe(false);
    expect(document.getElementById('inventory-ri-exchange')?.classList.contains('hidden')).toBe(true);
    expect(api.listActiveCommitments).toHaveBeenCalledTimes(1);
  });

  test('loadInventory wires sub-nav click handlers and lands on default', () => {
    loadInventory();

    // Default landing is active-commitments (issue #751).
    expect(document.getElementById('inventory-active-commitments')?.classList.contains('hidden')).toBe(false);
    expect(document.getElementById('inventory-ri-exchange')?.classList.contains('hidden')).toBe(true);

    // Clicking a sub-tab button routes through the router (QA A.4) so the
    // click both switches the view AND pushes /inventory/<subtab>. The
    // mocked router delegates to the real view switcher, so the DOM flips.
    const coverageBtn = document.querySelector<HTMLButtonElement>('[data-inv-subtab="coverage"]')!;
    coverageBtn.click();
    expect(switchInventorySubTab).toHaveBeenCalledWith('coverage');
    expect(document.getElementById('inventory-coverage')?.classList.contains('hidden')).toBe(false);
    expect(document.getElementById('inventory-active-commitments')?.classList.contains('hidden')).toBe(true);
  });

});

// QA A.4: Inventory sub-tabs are URL-addressable (/inventory/<subtab>),
// default-first, and shareable. inventory.ts owns the view switch + the
// click->router wiring; the URL push itself lives in navigation.ts and is
// covered in navigation.test.ts. Here we assert:
//   - the pure switcher returns the resolved (validated) sub-section,
//   - loadInventory honours an explicit sub-section, defaults when absent,
//     and falls back when unknown (default-first),
//   - a sub-nav click routes through navigation.switchInventorySubTab so
//     the URL gets updated (no hidden session state).
describe('Inventory & Coverage sub-tab addressing (QA A.4)', () => {
  beforeEach(() => {
    buildInventoryDOM();
    (loadRIExchange as jest.Mock).mockClear();
    (switchInventorySubTab as jest.Mock).mockClear();
    (api.listActiveCommitments as jest.Mock).mockReset().mockResolvedValue([]);
    (api.getCoverageBreakdown as jest.Mock).mockReset().mockResolvedValue({ providers: [] });
    (state.subscribeProvider as jest.Mock).mockReset().mockReturnValue(jest.fn());
    (state.subscribeAccount as jest.Mock).mockReset().mockReturnValue(jest.fn());
    (state.getCurrentProvider as jest.Mock).mockReturnValue('');
    (state.getCurrentAccountIDs as jest.Mock).mockReturnValue([]);
  });

  afterEach(() => {
    clearDOM();
  });

  test('switchInventorySubSection returns the resolved sub-section', () => {
    expect(switchInventorySubSection('coverage')).toBe('coverage');
    // Unknown input resolves to the default (default-first).
    expect(switchInventorySubSection('bogus')).toBe('active-commitments');
  });

  test('(a) loadInventory(undefined) -> default sub-section (active-commitments)', () => {
    loadInventory(undefined);
    expect(document.getElementById('inventory-active-commitments')?.classList.contains('hidden')).toBe(false);
    expect(api.listActiveCommitments).toHaveBeenCalled();
  });

  test('(b) loadInventory(<subtab>) -> that sub-section', () => {
    loadInventory('coverage');
    expect(document.getElementById('inventory-coverage')?.classList.contains('hidden')).toBe(false);
    expect(document.getElementById('inventory-active-commitments')?.classList.contains('hidden')).toBe(true);
    expect(api.getCoverageBreakdown).toHaveBeenCalled();
  });

  test('(d) loadInventory(<unknown>) -> falls back to default', () => {
    loadInventory('bogus-subtab');
    expect(document.getElementById('inventory-active-commitments')?.classList.contains('hidden')).toBe(false);
  });
});

describe('loadActiveCommitments — fetch + render flow', () => {
  beforeEach(() => {
    buildInventoryDOM();
    (api.listActiveCommitments as jest.Mock).mockReset();
    (state.subscribeProvider as jest.Mock).mockReturnValue(jest.fn());
    (state.subscribeAccount as jest.Mock).mockReturnValue(jest.fn());
    (state.getCurrentProvider as jest.Mock).mockReturnValue('');
    (state.getCurrentAccountIDs as jest.Mock).mockReturnValue([]);
  });

  afterEach(() => {
    clearDOM();
  });

  test('renders a table with header columns and one row per commitment', async () => {
    (api.listActiveCommitments as jest.Mock).mockResolvedValue([
      makeCommitment({ id: 'a:1', account_name: 'Prod', service: 'ec2', region: 'us-east-1', count: 4, term_years: 1, monthly_cost: 240.5 }),
      makeCommitment({ id: 'a:2', account_name: 'Stg', service: 'rds', region: 'eu-west-1', count: 1, term_years: 3, monthly_cost: 60.0 }),
    ]);

    await loadActiveCommitments();

    const list = document.getElementById('active-commitments-list')!;
    const table = list.querySelector('table');
    expect(table).not.toBeNull();

    // Header columns lock the table shape so a refactor that re-orders
    // (or drops) a column trips the test.
    const headers = Array.from(list.querySelectorAll('thead th')).map(th => th.textContent);
    expect(headers).toEqual([
      'Provider', 'Account', 'Service', 'Resource type', 'Region', 'Count', 'Term', 'Payment', 'Monthly cost', 'Monthly savings', 'Expires',
    ]);

    const rows = list.querySelectorAll('tbody tr');
    expect(rows.length).toBe(2);
    // Account cell contains the account name AND its monospaced ID.
    expect(rows[0]!.textContent).toContain('Prod');
    expect(rows[0]!.textContent).toContain('ec2');

    // Monthly Savings column (10th, 0-indexed 9) must render the estimated_savings value.
    // makeCommitment defaults estimated_savings to 80.0; formatCurrency mock returns "$80".
    const savingsCell = rows[0]!.querySelector('td:nth-child(10)');
    expect(savingsCell).not.toBeNull();
    expect(savingsCell!.textContent).toContain('80');
  });

  test('renders an empty paragraph when no commitments are returned', async () => {
    (api.listActiveCommitments as jest.Mock).mockResolvedValue([]);

    await loadActiveCommitments();

    const list = document.getElementById('active-commitments-list')!;
    expect(list.querySelector('table')).toBeNull();
    const empty = list.querySelector('.empty');
    expect(empty).not.toBeNull();
    expect(empty!.textContent).toMatch(/no active commitments/i);
  });

  test('renders an error paragraph when the API rejects, and tears down the skeleton', async () => {
    (api.listActiveCommitments as jest.Mock).mockRejectedValue(new Error('boom'));

    await loadActiveCommitments();

    const list = document.getElementById('active-commitments-list')!;
    expect(list.querySelector('table')).toBeNull();
    // Skeleton marker should be gone — error path must call teardownSkeleton
    // before rendering the error paragraph.
    expect(list.dataset['skeletonActive']).toBeUndefined();
    const err = list.querySelector('.error');
    expect(err).not.toBeNull();
    expect(err!.textContent).toContain('boom');
  });

  test('refresh button click re-invokes the fetch', async () => {
    (api.listActiveCommitments as jest.Mock).mockResolvedValue([]);

    await loadActiveCommitments();
    expect(api.listActiveCommitments).toHaveBeenCalledTimes(1);

    const btn = document.getElementById('active-commitments-refresh-btn')!;
    btn.click();
    // Click fires loadActiveCommitments; let the microtasks drain.
    await Promise.resolve();
    expect(api.listActiveCommitments).toHaveBeenCalledTimes(2);
  });

  test('falls back to monospaced account_id when account_name is missing', async () => {
    (api.listActiveCommitments as jest.Mock).mockResolvedValue([
      makeCommitment({ account_name: undefined, account_id: 'acc-no-name' }),
    ]);

    await loadActiveCommitments();

    const list = document.getElementById('active-commitments-list')!;
    const accountCell = list.querySelector('tbody tr td:nth-child(2)');
    expect(accountCell?.textContent).toContain('acc-no-name');
    expect(accountCell?.querySelector('.monospace')).not.toBeNull();
  });
});

// ──────────────────────────────────────────────
// loadCoverageBreakdown — fetch + render flow (issue #754)
// ──────────────────────────────────────────────

function makeProviderSection(
  provider: string,
  services: ProviderCoverageSection['services'],
  overallPct: number | null
): ProviderCoverageSection {
  return { provider, services, overall_coverage_pct: overallPct };
}

describe('loadCoverageBreakdown — fetch + render flow', () => {
  beforeEach(() => {
    buildInventoryDOM();
    (api.getCoverageBreakdown as jest.Mock).mockReset();
    (state.subscribeProvider as jest.Mock).mockReturnValue(jest.fn());
    (state.subscribeAccount as jest.Mock).mockReturnValue(jest.fn());
    (state.getCurrentProvider as jest.Mock).mockReturnValue('');
    (state.getCurrentAccountIDs as jest.Mock).mockReturnValue([]);
  });

  afterEach(() => {
    clearDOM();
  });

  test('renders per-provider sections with service rows', async () => {
    (api.getCoverageBreakdown as jest.Mock).mockResolvedValue({
      providers: [
        makeProviderSection('aws', [
          { service: 'ec2', covered_monthly: 200, on_demand_monthly: 300, coverage_pct: 40 },
          { service: 'rds', covered_monthly: 100, on_demand_monthly: 0, coverage_pct: 100 },
        ], 50),
        makeProviderSection('azure', null, null),
        makeProviderSection('gcp', null, null),
      ],
    });

    await loadCoverageBreakdown();

    const container = document.getElementById('coverage-providers')!;
    const cards = container.querySelectorAll('.coverage-provider-card');
    expect(cards.length).toBe(3);

    // AWS card: has service table rows.
    const awsCard = cards[0]!;
    expect(awsCard.textContent).toContain('AWS');
    expect(awsCard.textContent).toContain('50.0% covered');
    const rows = awsCard.querySelectorAll('tbody tr');
    expect(rows.length).toBe(2);
    expect(rows[0]!.textContent).toContain('ec2');
    expect(rows[0]!.textContent).toContain('40.0%');
    expect(rows[1]!.textContent).toContain('rds');
    expect(rows[1]!.textContent).toContain('100.0%');
  });

  test('renders "No usage detected" for providers with null services', async () => {
    (api.getCoverageBreakdown as jest.Mock).mockResolvedValue({
      providers: [
        makeProviderSection('aws', null, null),
        makeProviderSection('azure', null, null),
        makeProviderSection('gcp', null, null),
      ],
    });

    await loadCoverageBreakdown();

    const container = document.getElementById('coverage-providers')!;
    const empties = container.querySelectorAll('.empty');
    expect(empties.length).toBe(3);
    expect(empties[0]!.textContent).toContain('AWS');
  });

  test('renders N/A for null coverage_pct (no usage signal on that service)', async () => {
    (api.getCoverageBreakdown as jest.Mock).mockResolvedValue({
      providers: [
        makeProviderSection('aws', [
          { service: 'ec2', covered_monthly: 0, on_demand_monthly: 0, coverage_pct: null },
        ], null),
        makeProviderSection('azure', null, null),
        makeProviderSection('gcp', null, null),
      ],
    });

    await loadCoverageBreakdown();

    const container = document.getElementById('coverage-providers')!;
    const row = container.querySelector('tbody tr');
    expect(row).not.toBeNull();
    expect(row!.textContent).toContain('N/A');
  });

  test('renders an error paragraph when the API rejects', async () => {
    (api.getCoverageBreakdown as jest.Mock).mockRejectedValue(new Error('network failure'));

    await loadCoverageBreakdown();

    const container = document.getElementById('coverage-providers')!;
    const err = container.querySelector('.error');
    expect(err).not.toBeNull();
    expect(err!.textContent).toContain('network failure');
  });

  test('refresh button re-invokes the fetch', async () => {
    (api.getCoverageBreakdown as jest.Mock).mockResolvedValue({ providers: [] });

    await loadCoverageBreakdown();
    expect(api.getCoverageBreakdown).toHaveBeenCalledTimes(1);

    const btn = document.getElementById('coverage-refresh-btn')!;
    btn.click();
    await Promise.resolve();
    expect(api.getCoverageBreakdown).toHaveBeenCalledTimes(2);
  });

  test('coverage bar <th> has non-empty text and aria-label for screen readers', async () => {
    (api.getCoverageBreakdown as jest.Mock).mockResolvedValue({
      providers: [
        makeProviderSection('aws', [
          { service: 'ec2', covered_monthly: 100, on_demand_monthly: 100, coverage_pct: 50 },
        ], 50),
        makeProviderSection('azure', null, null),
        makeProviderSection('gcp', null, null),
      ],
    });

    await loadCoverageBreakdown();

    const container = document.getElementById('coverage-providers')!;
    const headers = Array.from(container.querySelectorAll('thead th'));
    // The last header column is the coverage bar — it must have a visible
    // label (not empty string) so screen readers announce the column purpose.
    const barTh = headers[headers.length - 1];
    expect(barTh).not.toBeNull();
    expect(barTh!.textContent).toBe('Coverage bar');
    expect(barTh!.getAttribute('aria-label')).toBe('Coverage bar');
  });
});

// ──────────────────────────────────────────────
// Chip subscription wiring (issue #866)
//
// Main Header global chips (Provider + Account) must propagate to the
// Active Commitments and Coverage sub-tabs. Mirrors the savings-history
// subscriber tests (PR #741) and dashboard tests (PR #747).
// ──────────────────────────────────────────────

describe('chip subscriptions (issue #866)', () => {
  /**
   * Add an inventory-tab div with the .active class so isInventoryTabActive()
   * returns true. The DOM is built fresh in buildInventoryDOM but doesn't
   * receive the .active class — add it here for the guard to pass.
   */
  function activateInventoryTab(): void {
    const tab = document.getElementById('inventory-tab');
    if (tab) tab.classList.add('active');
  }

  beforeEach(() => {
    buildInventoryDOM();
    (api.listActiveCommitments as jest.Mock).mockResolvedValue([]);
    (api.getCoverageBreakdown as jest.Mock).mockResolvedValue({ providers: [] });
    // Each test's loadInventory() call will call subscribeProvider/Account;
    // return a fresh jest.fn() as the unsubscribe handle each time.
    (state.subscribeProvider as jest.Mock).mockReset().mockReturnValue(jest.fn());
    (state.subscribeAccount as jest.Mock).mockReset().mockReturnValue(jest.fn());
    (state.getCurrentProvider as jest.Mock).mockReturnValue('');
    (state.getCurrentAccountIDs as jest.Mock).mockReturnValue([]);
  });

  afterEach(() => {
    clearDOM();
  });

  test('loadInventory registers callbacks with state.subscribeProvider and state.subscribeAccount', () => {
    loadInventory();

    expect(state.subscribeProvider).toHaveBeenCalledTimes(1);
    expect(state.subscribeAccount).toHaveBeenCalledTimes(1);
    expect(typeof (state.subscribeProvider as jest.Mock).mock.calls[0]?.[0]).toBe('function');
    expect(typeof (state.subscribeAccount as jest.Mock).mock.calls[0]?.[0]).toBe('function');
  });

  test('account chip change re-fetches Active Commitments when inventory tab is active', async () => {
    activateInventoryTab();
    (state.getCurrentAccountIDs as jest.Mock).mockReturnValue(['acct-X']);

    // Start on active-commitments sub-tab. Force the sub-section in case
    // a prior test left module-scoped currentSubSection at 'coverage'.
    loadInventory();
    switchInventorySubSection('active-commitments');

    // Clear the initial load call, then simulate chip change.
    (api.listActiveCommitments as jest.Mock).mockClear();
    const accountCb = (state.subscribeAccount as jest.Mock).mock.calls[0]?.[0] as () => void;
    accountCb();
    // queueMicrotask drains within a setTimeout(0).
    await new Promise((r) => setTimeout(r, 0));

    expect(api.listActiveCommitments).toHaveBeenCalledTimes(1);
    expect(api.listActiveCommitments).toHaveBeenCalledWith(
      expect.objectContaining({ accountID: 'acct-X' })
    );
  });

  test('provider chip change re-fetches Active Commitments when inventory tab is active', async () => {
    activateInventoryTab();
    (state.getCurrentProvider as jest.Mock).mockReturnValue('aws');

    loadInventory();
    switchInventorySubSection('active-commitments');

    (api.listActiveCommitments as jest.Mock).mockClear();
    const providerCb = (state.subscribeProvider as jest.Mock).mock.calls[0]?.[0] as () => void;
    providerCb();
    await new Promise((r) => setTimeout(r, 0));

    expect(api.listActiveCommitments).toHaveBeenCalledTimes(1);
    expect(api.listActiveCommitments).toHaveBeenCalledWith(
      expect.objectContaining({ provider: 'aws' })
    );
  });

  test('provider chip change re-fetches Coverage when coverage sub-tab is active', async () => {
    activateInventoryTab();
    (state.getCurrentProvider as jest.Mock).mockReturnValue('azure');

    loadInventory();
    // Switch to the coverage sub-tab.
    switchInventorySubSection('coverage');
    (api.getCoverageBreakdown as jest.Mock).mockClear();

    const providerCb = (state.subscribeProvider as jest.Mock).mock.calls[0]?.[0] as () => void;
    providerCb();
    await new Promise((r) => setTimeout(r, 0));

    expect(api.getCoverageBreakdown).toHaveBeenCalledTimes(1);
    expect(api.getCoverageBreakdown).toHaveBeenCalledWith(
      expect.objectContaining({ provider: 'azure' })
    );
  });

  test('account chip change re-fetches Coverage when coverage sub-tab is active', async () => {
    activateInventoryTab();
    (state.getCurrentAccountIDs as jest.Mock).mockReturnValue(['acct-Y']);

    loadInventory();
    switchInventorySubSection('coverage');
    (api.getCoverageBreakdown as jest.Mock).mockClear();

    const accountCb = (state.subscribeAccount as jest.Mock).mock.calls[0]?.[0] as () => void;
    accountCb();
    await new Promise((r) => setTimeout(r, 0));

    expect(api.getCoverageBreakdown).toHaveBeenCalledTimes(1);
    expect(api.getCoverageBreakdown).toHaveBeenCalledWith(
      expect.objectContaining({ accountID: 'acct-Y' })
    );
  });

  test('does NOT re-fetch when inventory tab is inactive (active-tab guard)', async () => {
    // First load with tab active to fix module-scoped currentSubSection
    // to 'active-commitments'. Then deactivate the tab and verify the
    // chip callbacks early-return.
    activateInventoryTab();
    loadInventory();
    switchInventorySubSection('active-commitments');
    const tab = document.getElementById('inventory-tab');
    if (tab) tab.classList.remove('active');

    (api.listActiveCommitments as jest.Mock).mockClear();
    (api.getCoverageBreakdown as jest.Mock).mockClear();
    const providerCb = (state.subscribeProvider as jest.Mock).mock.calls[0]?.[0] as () => void;
    const accountCb = (state.subscribeAccount as jest.Mock).mock.calls[0]?.[0] as () => void;
    providerCb();
    accountCb();
    await new Promise((r) => setTimeout(r, 0));

    expect(api.listActiveCommitments).not.toHaveBeenCalled();
    expect(api.getCoverageBreakdown).not.toHaveBeenCalled();
  });

  test('coalesces back-to-back provider+account fires into one fetch', async () => {
    activateInventoryTab();

    loadInventory();
    switchInventorySubSection('active-commitments');
    (api.listActiveCommitments as jest.Mock).mockClear();

    const providerCb = (state.subscribeProvider as jest.Mock).mock.calls[0]?.[0] as () => void;
    const accountCb = (state.subscribeAccount as jest.Mock).mock.calls[0]?.[0] as () => void;

    // Simulate topbar: clear accounts then set provider synchronously.
    accountCb();
    providerCb();
    await new Promise((r) => setTimeout(r, 0));

    expect(api.listActiveCommitments).toHaveBeenCalledTimes(1);
  });

  test('filter-aware empty state: shows provider name when provider chip is set', async () => {
    (state.getCurrentProvider as jest.Mock).mockReturnValue('gcp');

    await loadActiveCommitments();

    const list = document.getElementById('active-commitments-list')!;
    const empty = list.querySelector('.empty');
    expect(empty).not.toBeNull();
    expect(empty!.textContent).toContain('"gcp"');
  });

  test('filter-aware empty state: shows account ID when account chip is set', async () => {
    (state.getCurrentAccountIDs as jest.Mock).mockReturnValue(['acct-42']);

    await loadActiveCommitments();

    const list = document.getElementById('active-commitments-list')!;
    const empty = list.querySelector('.empty');
    expect(empty).not.toBeNull();
    expect(empty!.textContent).toContain('acct-42');
  });

  test('generic empty state: shown when no chip filters are active', async () => {
    await loadActiveCommitments();

    const list = document.getElementById('active-commitments-list')!;
    const empty = list.querySelector('.empty');
    expect(empty).not.toBeNull();
    expect(empty!.textContent).toMatch(/no active commitments found across your registered accounts/i);
  });
});
