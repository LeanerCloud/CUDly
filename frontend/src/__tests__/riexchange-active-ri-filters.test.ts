/**
 * DOM-level regression tests for Active Convertible RIs inline column-filter
 * controls (issue #1414).
 *
 * Before the fix, renderRIsTable rendered no filter buttons. This suite
 * verifies the full wiring: button presence in every filterable header,
 * popover opening detached from the table, categorical row-narrowing, and
 * numeric-expression row-narrowing.
 *
 * Mirrors the structure of plans-column-filters.test.ts so both tables
 * have equivalent DOM-level coverage.
 */

jest.mock('../api', () => ({
  listConvertibleRIs: jest.fn(),
  listExchangeableAzureRIs: jest.fn().mockResolvedValue([]),
  getRIUtilization: jest.fn(),
  getReshapeRecommendations: jest.fn(),
  getExchangeQuote: jest.fn(),
  executeExchange: jest.fn(),
  getRIExchangeHistory: jest.fn(),
  getRIExchangeConfig: jest.fn(),
  updateRIExchangeConfig: jest.fn(),
  listTargetOfferings: jest.fn().mockResolvedValue([]),
}));

jest.mock('../navigation', () => ({
  switchTab: jest.fn(),
  switchSettingsSubTab: jest.fn(),
}));

// Module-scoped Active RI filter state; mirrors the real state module's
// slice so the popover commit path round-trips like the real store.
let activeRiFilters: Record<string, unknown> = {};

jest.mock('../state', () => ({
  subscribeProvider: jest.fn().mockReturnValue(() => {}),
  subscribeAccount: jest.fn().mockReturnValue(() => {}),
  getCurrentProvider: jest.fn().mockReturnValue('aws'),
  getCurrentAccountIDs: jest.fn().mockReturnValue([]),
  getCurrentUser: jest.fn().mockReturnValue({
    id: 'u-admin',
    email: 'admin@example.com',
    groups: ['00000000-0000-5000-8000-000000000001'],
  }),
  // Reshape-recommendations slice (existing; no-op for these tests)
  getRiExchangeColumnFilters: jest.fn().mockReturnValue({}),
  setRiExchangeColumnFilter: jest.fn(),
  clearAllRiExchangeColumnFilters: jest.fn(),
  // Active-RI filter slice (new; backs the popover commit path)
  getActiveRiColumnFilters: jest.fn(() => ({ ...activeRiFilters })),
  setActiveRiColumnFilter: jest.fn((col: string, filter: unknown) => {
    if (filter === null) {
      const next = { ...activeRiFilters };
      delete next[col];
      activeRiFilters = next;
      return;
    }
    activeRiFilters = { ...activeRiFilters, [col]: filter };
  }),
  clearAllActiveRiColumnFilters: jest.fn(() => { activeRiFilters = {}; }),
}));

import { loadRIExchange } from '../riexchange';
import * as api from '../api';

// Three convertible RIs with distinct instance types, AZs, and counts
// so the categorical and numeric filter assertions can distinguish rows.
const seedRIs = [
  {
    reserved_instance_id: 'ri-1',
    instance_type: 'm5.xlarge',
    availability_zone: 'us-east-1a',
    instance_count: 4,
    offering_type: 'Partial Upfront',
    start: '2024-01-01',
    end: '2025-01-01',
    fixed_price: 1000,
    usage_price: 0.5,
    state: 'active',
    normalization_factor: 8,
  },
  {
    reserved_instance_id: 'ri-2',
    instance_type: 'm5.xlarge',
    availability_zone: 'us-east-1b',
    instance_count: 2,
    offering_type: 'No Upfront',
    start: '2024-01-01',
    end: '2025-01-01',
    fixed_price: 0,
    usage_price: 0.8,
    state: 'active',
    normalization_factor: 8,
  },
  {
    reserved_instance_id: 'ri-3',
    instance_type: 'c6i.large',
    availability_zone: 'us-east-1a',
    instance_count: 6,
    offering_type: 'All Upfront',
    start: '2024-01-01',
    end: '2025-01-01',
    fixed_price: 2000,
    usage_price: 0,
    state: 'active',
    normalization_factor: 4,
  },
];

const seedUtilization = [
  { reserved_instance_id: 'ri-1', utilization_percent: 50.0, purchased_hours: 100, total_actual_hours: 50, unused_hours: 50 },
  { reserved_instance_id: 'ri-2', utilization_percent: 95.0, purchased_hours: 100, total_actual_hours: 95, unused_hours: 5 },
  { reserved_instance_id: 'ri-3', utilization_percent: 30.0, purchased_hours: 100, total_actual_hours: 30, unused_hours: 70 },
];

describe('Active Convertible RIs column filters (issue #1414)', () => {
  beforeEach(() => {
    activeRiFilters = {};
    document.body.innerHTML = `
      <div id="ri-exchange-instances-list"></div>
      <div id="ri-exchange-recommendations-list"></div>
      <div id="ri-exchange-history-list"></div>
      <div id="ri-exchange-staleness-banner" class="hidden"></div>
    `;
    jest.clearAllMocks();
    (api.listConvertibleRIs as jest.Mock).mockResolvedValue(seedRIs);
    (api.getRIUtilization as jest.Mock).mockResolvedValue(seedUtilization);
    (api.getReshapeRecommendations as jest.Mock).mockResolvedValue({
      recommendations: [],
      recs_staleness: '',
      recs_collected_at: null,
    });
    (api.getRIExchangeHistory as jest.Mock).mockResolvedValue([]);
  });

  afterEach(() => {
    // Any detached popovers on body; clean up so they don't bleed.
    document.body.querySelectorAll('.column-filter-popover').forEach((n) => n.remove());
  });

  /** Load and flush all micro/macro-tasks (incl. the fire-and-forget loadUtilization). */
  async function load(): Promise<void> {
    await loadRIExchange();
    for (let i = 0; i < 3; i++) {
      await new Promise<void>((r) => setTimeout(r, 0));
    }
  }

  function countRows(): number {
    return document.querySelectorAll(
      '#ri-exchange-instances-list tbody tr',
    ).length;
  }

  test('every filterable column header has a trigger button', async () => {
    await load();
    const buttons = document.querySelectorAll<HTMLButtonElement>(
      '#ri-exchange-instances-list th .column-filter-btn[data-column]',
    );
    const cols = Array.from(buttons).map((b) => b.dataset['column']);
    expect(cols.sort()).toEqual(
      ['availability_zone', 'instance_count', 'instance_type', 'offering_type', 'utilization_pct'].sort(),
    );
  });

  test('clicking a trigger button opens a popover detached to document.body', async () => {
    await load();
    const btn = document.querySelector<HTMLButtonElement>(
      '#ri-exchange-instances-list th .column-filter-btn[data-column="instance_type"]',
    );
    expect(btn).not.toBeNull();
    btn!.click();
    const popover = document.body.querySelector('.column-filter-popover');
    expect(popover).not.toBeNull();
    // Popover must be appended to body, not nested inside the table
    expect(popover?.closest('#ri-exchange-instances-list')).toBeNull();
  });

  test('categorical filter (instance_type) narrows displayed rows', async () => {
    await load();
    expect(countRows()).toBe(3);

    const btn = document.querySelector<HTMLButtonElement>(
      '#ri-exchange-instances-list th .column-filter-btn[data-column="instance_type"]',
    );
    btn!.click();

    // Uncheck c6i.large to keep only m5.xlarge rows
    const c6iCb = document.querySelector<HTMLInputElement>(
      '.column-filter-popover .column-filter-item input[data-value="c6i.large"]',
    );
    expect(c6iCb).not.toBeNull();
    c6iCb!.checked = false;
    c6iCb!.dispatchEvent(new Event('change'));

    // ri-1 and ri-2 remain (m5.xlarge); ri-3 (c6i.large) is filtered out
    expect(countRows()).toBe(2);
  });

  test('numeric filter (instance_count >= 4) narrows displayed rows', async () => {
    await load();
    expect(countRows()).toBe(3);

    const btn = document.querySelector<HTMLButtonElement>(
      '#ri-exchange-instances-list th .column-filter-btn[data-column="instance_count"]',
    );
    btn!.click();

    const input = document.querySelector<HTMLInputElement>(
      '.column-filter-popover .column-filter-numeric-input',
    );
    expect(input).not.toBeNull();
    input!.value = '>=4';
    input!.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true }));

    // ri-1 (count=4) and ri-3 (count=6) pass; ri-2 (count=2) is filtered out
    expect(countRows()).toBe(2);
  });
});
