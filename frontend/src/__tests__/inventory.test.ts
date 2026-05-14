/**
 * Inventory & Coverage section tests (issue #340 T4 + deferred sub-task
 * for the Active commitments table).
 *
 * Verifies the sub-tab switching machinery for the umbrella section AND
 * the per-commitment table fetch+render flow (skeleton → table | empty |
 * error). The coverage sub-section remains an intentional placeholder.
 */

// loadRIExchange is a side-effect import from a module that touches the
// network in real use. Mock it out so the test only exercises sub-section
// switching, not real RI fetch.
jest.mock('../riexchange', () => ({
  loadRIExchange: jest.fn(),
}));

// The active-commitments load path hits the API. Mock the entire api
// barrel so we don't need to stand up fetch — tests exercise the render
// machinery, not the network shape.
jest.mock('../api', () => ({
  listActiveCommitments: jest.fn(),
}));

import { loadInventory, switchInventorySubSection, loadActiveCommitments } from '../inventory';
import { loadRIExchange } from '../riexchange';
import * as api from '../api';

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
    ['active-commitments', 'Active commitments', false],
    ['coverage', 'Coverage', false],
    ['ri-exchange', 'RI Exchange', true],
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
  ac.classList.add('hidden');
  const refresh = document.createElement('button');
  refresh.id = 'active-commitments-refresh-btn';
  refresh.textContent = 'Refresh';
  ac.appendChild(refresh);
  const list = document.createElement('div');
  list.id = 'active-commitments-list';
  ac.appendChild(list);
  tab.appendChild(ac);

  for (const [id, hidden, body] of [
    ['inventory-coverage', true, 'coverage'],
    ['inventory-ri-exchange', false, 'ri-exchange'],
  ] as const) {
    const section = document.createElement('section');
    section.id = id;
    if (hidden) section.classList.add('hidden');
    section.textContent = body;
    tab.appendChild(section);
  }

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

  test('switchInventorySubSection falls back to ri-exchange for unknown sub-section', () => {
    switchInventorySubSection('something-unknown');

    expect(document.getElementById('inventory-ri-exchange')?.classList.contains('hidden')).toBe(false);
    expect(loadRIExchange).toHaveBeenCalledTimes(1);
  });

  test('loadInventory wires sub-nav click handlers and lands on default', () => {
    loadInventory();

    // Default landing is ri-exchange.
    expect(document.getElementById('inventory-ri-exchange')?.classList.contains('hidden')).toBe(false);

    // Clicking a sub-tab button switches the section.
    const coverageBtn = document.querySelector<HTMLButtonElement>('[data-inv-subtab="coverage"]')!;
    coverageBtn.click();
    expect(document.getElementById('inventory-coverage')?.classList.contains('hidden')).toBe(false);
    expect(document.getElementById('inventory-ri-exchange')?.classList.contains('hidden')).toBe(true);
  });
});

describe('loadActiveCommitments — fetch + render flow', () => {
  beforeEach(() => {
    buildInventoryDOM();
    (api.listActiveCommitments as jest.Mock).mockReset();
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
      'Provider', 'Account', 'Service', 'Resource type', 'Region', 'Count', 'Term', 'Payment', 'Monthly cost', 'Expires',
    ]);

    const rows = list.querySelectorAll('tbody tr');
    expect(rows.length).toBe(2);
    // Account cell contains the account name AND its monospaced ID.
    expect(rows[0]!.textContent).toContain('Prod');
    expect(rows[0]!.textContent).toContain('ec2');
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
