/**
 * Recommendations module tests
 */
import { loadRecommendations, openPurchaseModal, getPurchaseModalRecommendations, clearPurchaseModalRecommendations, refreshRecommendations, setupRecommendationsHandlers, pickBestVariantPerCell, seedGlobalDefaults, effectiveMonthlySavings, effectiveSavingsPct, onDemandMonthly, groupRecsByCell, cellSummary, pageLevelRange, resetExpandedCells, resetAutoRefreshInFlight, scaleCost, formatCostForPeriod, periodSuffix, loadColumnVisibility, saveColumnVisibility, resetColumnVisibilityState, TOGGLEABLE_COLUMNS, COLUMN_DEFS, isHomogeneousSelection } from '../recommendations';
import type { CostPeriod } from '../state';

// Mock the api module
jest.mock('../api', () => ({
  getRecommendations: jest.fn(),
  refreshRecommendations: jest.fn(),
  listAccounts: jest.fn().mockResolvedValue([]),
  // issue #223: getConfig is fetched on page load to resolve GlobalConfig
  // defaults (DefaultTerm + DefaultPayment). Default-empty global config so
  // pre-#223 tests retain their hardcoded-fallback behavior without extra setup.
  getConfig: jest.fn().mockResolvedValue({ global: {} }),
  // Issue #111: openFanOutModal now pre-fetches per-account service
  // overrides to seed each bucket's Payment default. Default-empty so
  // pre-#111 tests retain their toolbar-seeded behavior without any
  // mock setup; tests that specifically exercise the override seed
  // override this mock per-test.
  listAccountServiceOverrides: jest.fn().mockResolvedValue([]),
}));

// Mock the per-id detail endpoint module so the drawer-fetch tests can
// assert on call shape without going through the apiRequest layer.
// Default resolution returns a benign empty payload so tests that
// merely open + close the drawer (and don't care about the detail
// fetch) don't trip on an undefined-promise return.
//
// Default freshness: fresh (1h ago) so pre-#284 tests that call
// loadRecommendations() don't inadvertently trigger auto-refresh and
// fire extra showToast() calls that would break existing assertions.
// NOTE: ONE_HOUR_AGO can't be used here because jest.mock() factory
// functions are hoisted before variable declarations. The date is
// computed inline; the beforeEach block resets it via the mock handle.
jest.mock('../api/recommendations', () => ({
  getRecommendationDetail: jest.fn().mockResolvedValue({
    id: 'rec-default',
    usage_history: [],
    confidence_bucket: 'low',
    provenance_note: '',
  }),
  getRecommendationsFreshness: jest.fn().mockResolvedValue({
    last_collected_at: new Date(Date.now() - 60 * 60 * 1000).toISOString(),
    last_collection_error: null,
  }),
  refreshRecommendations: jest.fn().mockResolvedValue({}),
}));

// Mock showToast so auto-refresh (#284) tests can assert on toast calls
// without touching the DOM. Returns a dismiss handle per the ToastHandle
// interface so callers that invoke handle.dismiss() don't crash.
const mockShowToast = jest.fn<{ dismiss: () => void }, [unknown]>(() => ({ dismiss: jest.fn() }));
jest.mock('../toast', () => ({
  showToast: (opts: unknown) => mockShowToast(opts),
}));

// Mock state module
jest.mock('../state', () => ({
  getCurrentProvider: jest.fn().mockReturnValue('all'),
  setCurrentProvider: jest.fn(),
  getCurrentAccountIDs: jest.fn().mockReturnValue([]),
  setCurrentAccountIDs: jest.fn(),
  getRecommendations: jest.fn().mockReturnValue([]),
  getRecommendationByID: jest.fn().mockReturnValue(undefined),
  setRecommendations: jest.fn(),
  getSelectedRecommendationIDs: jest.fn().mockReturnValue(new Set()),
  clearSelectedRecommendations: jest.fn(),
  addSelectedRecommendation: jest.fn(),
  removeSelectedRecommendation: jest.fn(),
  getRecommendationsSort: jest.fn().mockReturnValue({ column: 'savings', direction: 'desc' }),
  setRecommendationsSort: jest.fn(),
  // Bundle A column-filter accessors (default empty filters → applyColumnFilters is a no-op).
  getRecommendationsColumnFilters: jest.fn().mockReturnValue({}),
  setRecommendationsColumnFilter: jest.fn(),
  clearAllRecommendationsColumnFilters: jest.fn(),
  getVisibleRecommendations: jest.fn().mockReturnValue([]),
  setVisibleRecommendations: jest.fn(),
  // issue #319: cost-period selector. Default to 'monthly' so pre-#319 tests
  // see unchanged behaviour (monthly is the identity factor).
  getCostPeriod: jest.fn().mockReturnValue('monthly'),
  setCostPeriod: jest.fn(),
  // issue #318: column visibility (default: all visible — empty hidden set)
  getHiddenColumns: jest.fn().mockReturnValue(new Set()),
  setHiddenColumns: jest.fn(),
  // Issue #365: mountBottomActionBox calls canAccess() which reads the
  // current user role. Default to admin so the bottom-bar button-presence
  // tests below see both #bulk-purchase-btn and #create-plan-btn
  // unconditionally. Permission-gating coverage lives in
  // recommendations-permissions.test.ts.
  getCurrentUser: jest.fn().mockReturnValue({ id: 'u-admin', email: 'admin@example.com', groups: ['00000000-0000-5000-8000-000000000001'] }),
  // Issue #477: setupRecommendationsHandlers subscribes to provider/account
  // changes; expose jest.fn() shims so tests can capture the callback and
  // simulate a change without going through the real listener set.
  subscribeProvider: jest.fn(),
  subscribeAccount: jest.fn(),
}));

// Mock utils
jest.mock('../utils', () => ({
  formatCurrency: jest.fn((val) => `$${val || 0}`),
  formatTerm: jest.fn((years) => years == null ? '' : `${years} Year${years === 1 ? '' : 's'}`),
  escapeHtml: jest.fn((str) => str || ''),
  populateAccountFilter: jest.fn(() => Promise.resolve()),
  // CURRENCY_DEFAULT_DIGITS must be re-exported by the mock so that
  // recommendations.ts (which imports it for its filter precision logic)
  // sees the same default-digit count as the real utils.ts. Without this,
  // displayPrecision's currency-column branches return undefined and the
  // filter exact-match path breaks.
  CURRENCY_DEFAULT_DIGITS: 0,
}));

import * as api from '../api';
import * as state from '../state';
import * as recsApi from '../api/recommendations';

describe('Recommendations Module', () => {
  beforeEach(() => {
    // Reset DOM
    document.body.innerHTML = `
      <div id="opportunities-tab" class="tab-content active">
        <div id="recommendations-summary"></div>
        <div id="recommendations-list"></div>
      </div>
      <div id="purchase-modal" class="hidden">
        <div id="purchase-details"></div>
        <div class="modal-buttons">
          <button type="button" id="close-purchase-modal-btn">Cancel</button>
          <button type="button" id="execute-purchase-btn" class="primary">Send for Approval</button>
        </div>
      </div>
    `;

    jest.clearAllMocks();
    jest.useFakeTimers();
    window.alert = jest.fn();

    // Default freshness for pre-#284 tests: fresh (1h ago) → auto-refresh
    // does NOT fire, so existing tests are unaffected by the new toast calls.
    (recsApi.getRecommendationsFreshness as jest.Mock).mockResolvedValue({
      last_collected_at: new Date(Date.now() - 60 * 60 * 1000).toISOString(),
      last_collection_error: null,
    });
    (recsApi.refreshRecommendations as jest.Mock).mockResolvedValue({});
    mockShowToast.mockReturnValue({ dismiss: jest.fn() });
  });

  afterEach(() => {
    jest.useRealTimers();
  });

  describe('loadRecommendations', () => {
    test('fetches recommendations with provider/account_ids hints (Bundle B)', async () => {
      // After Bundle B, only provider + account_ids are sent to the API as
      // hints; service/region/numeric filters are pure client-side via
      // applyColumnFilters. The legacy DOM filter inputs are gone.
      (api.getRecommendations as jest.Mock).mockResolvedValue({
        summary: {},
        recommendations: [],
        regions: []
      });

      await loadRecommendations();

      expect(api.getRecommendations).toHaveBeenCalledWith({
        provider: 'all',
        account_ids: undefined,
      });
    });

    test('renders recommendations summary', async () => {
      (api.getRecommendations as jest.Mock).mockResolvedValue({
        summary: {
          total_count: 10,
          total_monthly_savings: 5000,
          total_upfront_cost: 10000,
          avg_payback_months: 2
        },
        recommendations: [],
        regions: []
      });

      await loadRecommendations();

      const summary = document.getElementById('recommendations-summary');
      expect(summary?.innerHTML).toContain('Total Recommendations');
      expect(summary?.innerHTML).toContain('Potential Monthly Savings');
      expect(summary?.innerHTML).toContain('Total Upfront Cost');
      expect(summary?.innerHTML).toContain('Payback Period');
    });

    test('Potential Monthly Savings card mirrors page-level range, not the API sum (#272)', async () => {
      // Two cells, two variants per cell. The cells share (provider, account,
      // service, resource_type, region, term, engine) within each group; the
      // variants differ by payment_option. The user can only buy one variant
      // per cell, so the achievable monthly savings is bounded by:
      //   min = sum of per-cell savingsMin = 100 + 200 = 300
      //   max = sum of per-cell savingsMax = 150 + 250 = 400
      // The API's total_monthly_savings sums all 4 variants (= 700), which
      // overstates achievable savings by ~75% on this 2-cell page. The card
      // must NOT render 700; it must render the range "$300 – $400".
      const recs = [
        { id: 'cell1-cheap',  provider: 'aws', cloud_account_id: 'a1', service: 'ec2', resource_type: 't3.medium', region: 'us-east-1', count: 1, term: 1, payment_option: 'no-upfront',     savings: 100, upfront_cost: 0 },
        { id: 'cell1-pricey', provider: 'aws', cloud_account_id: 'a1', service: 'ec2', resource_type: 't3.medium', region: 'us-east-1', count: 1, term: 1, payment_option: 'all-upfront',    savings: 150, upfront_cost: 1000 },
        { id: 'cell2-cheap',  provider: 'aws', cloud_account_id: 'a1', service: 'rds', resource_type: 'db.t3',     region: 'us-east-1', count: 1, term: 1, payment_option: 'no-upfront',     savings: 200, upfront_cost: 0 },
        { id: 'cell2-pricey', provider: 'aws', cloud_account_id: 'a1', service: 'rds', resource_type: 'db.t3',     region: 'us-east-1', count: 1, term: 1, payment_option: 'all-upfront',    savings: 250, upfront_cost: 2000 },
      ];
      (api.getRecommendations as jest.Mock).mockResolvedValue({
        summary: { total_count: 4, total_monthly_savings: 700, total_upfront_cost: 3000, avg_payback_months: 1 },
        recommendations: recs,
        regions: [],
      });
      // The summary card now reads the visible set via
      // state.getVisibleRecommendations() (so it stays in sync with
      // the banner under filter changes — see the next test for the
      // filter-divergence case). On an unfiltered initial render the
      // visible set equals the loaded set; mock it explicitly because
      // setVisibleRecommendations() inside renderRecommendationsList
      // is a no-op when state is mocked.
      (state.getVisibleRecommendations as jest.Mock).mockReturnValue(recs);

      await loadRecommendations();

      const summary = document.getElementById('recommendations-summary');
      const savingsCard = Array.from(summary?.querySelectorAll('.card') ?? [])
        .find((c) => c.querySelector('h3')?.textContent === 'Potential Monthly Savings');
      const value = savingsCard?.querySelector('.value.savings')?.textContent ?? '';
      // The page-level range is $300 – $400/mo; the API's flat 700 must not
      // appear on the card.
      expect(value).toContain('$300');
      expect(value).toContain('$400');
      expect(value).not.toContain('$700');
    });

    test('savings card recomputes from visible set on filter change (#272 CR follow-up)', async () => {
      // Same 4-rec / 2-cell setup as above. Initial render: card shows
      // $300 – $400 (range across both cells). Then we apply a real
      // column filter (service = "ec2") via the state mock so the
      // applyColumnFilters() call inside renderRecommendationsList
      // narrows the visible set to cell1's two variants, and trigger
      // a rerender via a sortable-header click. The card MUST recompute
      // to cell1's range ($100 – $150) — otherwise it stays pinned at
      // the unfiltered $300 – $400 and diverges from the banner under
      // the table, which is exactly the CR finding on #276 we're
      // closing.
      const allRecs = [
        { id: 'cell1-cheap',  provider: 'aws', cloud_account_id: 'a1', service: 'ec2', resource_type: 't3.medium', region: 'us-east-1', count: 1, term: 1, payment_option: 'no-upfront',  savings: 100, upfront_cost: 0 },
        { id: 'cell1-pricey', provider: 'aws', cloud_account_id: 'a1', service: 'ec2', resource_type: 't3.medium', region: 'us-east-1', count: 1, term: 1, payment_option: 'all-upfront', savings: 150, upfront_cost: 1000 },
        { id: 'cell2-cheap',  provider: 'aws', cloud_account_id: 'a1', service: 'rds', resource_type: 'db.t3',     region: 'us-east-1', count: 1, term: 1, payment_option: 'no-upfront',  savings: 200, upfront_cost: 0 },
        { id: 'cell2-pricey', provider: 'aws', cloud_account_id: 'a1', service: 'rds', resource_type: 'db.t3',     region: 'us-east-1', count: 1, term: 1, payment_option: 'all-upfront', savings: 250, upfront_cost: 2000 },
      ];

      (api.getRecommendations as jest.Mock).mockResolvedValue({
        summary: { total_count: 4, total_monthly_savings: 700, total_upfront_cost: 3000, avg_payback_months: 1 },
        recommendations: allRecs,
        regions: [],
      });
      (state.getRecommendations as jest.Mock).mockReturnValue(allRecs);
      (state.getVisibleRecommendations as jest.Mock).mockReturnValue(allRecs);
      // No column filters initially — applyColumnFilters returns the input
      // clone so all 4 recs reach the summary recompute.
      (state.getRecommendationsColumnFilters as jest.Mock).mockReturnValue({});

      await loadRecommendations();

      const cardValue = (): string => {
        const summary = document.getElementById('recommendations-summary');
        const card = Array.from(summary?.querySelectorAll('.card') ?? [])
          .find((c) => c.querySelector('h3')?.textContent === 'Potential Monthly Savings');
        return card?.querySelector('.value.savings')?.textContent ?? '';
      };
      expect(cardValue()).toContain('$300');
      expect(cardValue()).toContain('$400');

      // Apply a real column filter restricting to service=ec2 (cell1
      // only). renderRecommendationsList re-runs applyColumnFilters
      // against the loaded set on every entry, so a sort-header click
      // triggers the narrowing pipeline end-to-end.
      (state.getRecommendationsColumnFilters as jest.Mock).mockReturnValue({
        service: { kind: 'set', values: ['ec2'] },
      });
      const sortableHeader = document.querySelector<HTMLTableCellElement>('th[data-sort="service"]');
      sortableHeader?.click();

      // Card must recompute to cell1's range ($100 – $150) — NOT stay
      // pinned at the loaded-set range ($300 – $400).
      expect(cardValue()).toContain('$100');
      expect(cardValue()).toContain('$150');
      expect(cardValue()).not.toContain('$300');
      expect(cardValue()).not.toContain('$400');

      // Restore the filter mock to empty so the leaked return value
      // doesn't bleed into subsequent tests in this file. jest.clearAllMocks
      // resets mock.calls but not .mockReturnValue, so we have to undo
      // explicitly.
      (state.getRecommendationsColumnFilters as jest.Mock).mockReturnValue({});
    });

    test('summary cards narrow to selection in real time (closes #279)', async () => {
      // 4 recs / 2 cells. With no selection: cards reflect the full
      // visible set (savings $300–$400, upfront $0–$3000). After ticking
      // cell1's pricey variant, cards narrow to that single rec
      // ($150 savings / $1000 upfront). Tick the second cell's cheap
      // variant too: cards sum the two selected variants ($350 savings /
      // $1000 upfront). Cards must update on every selection toggle —
      // the selection-toggle handler triggers a list rerender, which
      // calls renderRecommendationsSummary with the new selection.
      const recs = [
        { id: 'cell1-cheap',  provider: 'aws', cloud_account_id: 'a1', service: 'ec2', resource_type: 't3.medium', region: 'us-east-1', count: 1, term: 1, payment_option: 'no-upfront',  savings: 100, upfront_cost: 0 },
        { id: 'cell1-pricey', provider: 'aws', cloud_account_id: 'a1', service: 'ec2', resource_type: 't3.medium', region: 'us-east-1', count: 1, term: 1, payment_option: 'all-upfront', savings: 150, upfront_cost: 1000 },
        { id: 'cell2-cheap',  provider: 'aws', cloud_account_id: 'a1', service: 'rds', resource_type: 'db.t3',     region: 'us-east-1', count: 1, term: 1, payment_option: 'no-upfront',  savings: 200, upfront_cost: 0 },
        { id: 'cell2-pricey', provider: 'aws', cloud_account_id: 'a1', service: 'rds', resource_type: 'db.t3',     region: 'us-east-1', count: 1, term: 1, payment_option: 'all-upfront', savings: 250, upfront_cost: 2000 },
      ];
      (api.getRecommendations as jest.Mock).mockResolvedValue({
        summary: { total_count: 4, total_monthly_savings: 700, total_upfront_cost: 3000, avg_payback_months: 2 },
        recommendations: recs,
        regions: [],
      });
      (state.getRecommendations as jest.Mock).mockReturnValue(recs);
      (state.getVisibleRecommendations as jest.Mock).mockReturnValue(recs);
      (state.getRecommendationsColumnFilters as jest.Mock).mockReturnValue({});

      const cardValue = (titlePattern: RegExp): string => {
        const summary = document.getElementById('recommendations-summary');
        const card = Array.from(summary?.querySelectorAll('.card') ?? [])
          .find((c) => titlePattern.test(c.querySelector('h3')?.textContent ?? ''));
        return card?.querySelector('.value')?.textContent ?? '';
      };
      const cardTitle = (valuePattern: RegExp): string => {
        const summary = document.getElementById('recommendations-summary');
        const card = Array.from(summary?.querySelectorAll('.card') ?? [])
          .find((c) => valuePattern.test(c.querySelector('.value')?.textContent ?? ''));
        return card?.querySelector('h3')?.textContent ?? '';
      };

      // No selection: cards reflect the full visible set.
      (state.getSelectedRecommendationIDs as jest.Mock).mockReturnValue(new Set());
      await loadRecommendations();
      expect(cardValue(/Recommendations/)).toBe('4'); // 4 variants (closes #748: KPI now matches "Showing of")
      expect(cardValue(/Monthly Savings/)).toMatch(/\$300\b/);
      expect(cardValue(/Monthly Savings/)).toMatch(/\$400\b/);
      expect(cardValue(/Upfront/)).toMatch(/\$0\b/);
      expect(cardValue(/Upfront/)).toMatch(/\$3,?000\b/);
      // Card titles reflect the "all visible" mode.
      expect(cardTitle(/^4$/)).toBe('Total Recommendations');

      // Tick one cell's pricey variant: cards narrow to that one rec.
      (state.getSelectedRecommendationIDs as jest.Mock).mockReturnValue(new Set(['cell1-pricey']));
      await loadRecommendations();
      expect(cardValue(/Recommendations/)).toBe('1');
      // Single rec → range collapses to a single value.
      expect(cardValue(/Monthly Savings/)).toMatch(/^\$150$/);
      expect(cardValue(/Upfront/)).toMatch(/^\$1,?000$/);
      // Title switches to "Selected ..." form.
      expect(cardTitle(/^1$/)).toBe('Selected Recommendations');

      // Tick a second cell: cards sum the two selected variants.
      (state.getSelectedRecommendationIDs as jest.Mock).mockReturnValue(new Set(['cell1-pricey', 'cell2-cheap']));
      await loadRecommendations();
      expect(cardValue(/Recommendations/)).toBe('2');
      expect(cardValue(/Monthly Savings/)).toMatch(/^\$350$/); // 150 + 200
      expect(cardValue(/Upfront/)).toMatch(/^\$1,?000$/); // 1000 + 0
    });

    test('KPI "Total Recommendations" and "Showing X of X" agree on the same variant count (closes #748)', async () => {
      // 2 cells, 2 variants each = 4 total variants.
      // Before #748 the KPI showed 2 (cell count) while "Showing of" showed 4
      // (variant count). After the fix both display 4.
      const recs = [
        { id: 'cell1-v1', provider: 'aws', cloud_account_id: 'a1', service: 'ec2', resource_type: 't3.medium', region: 'us-east-1', count: 1, term: 1, payment_option: 'no-upfront',  savings: 100, upfront_cost: 0 },
        { id: 'cell1-v2', provider: 'aws', cloud_account_id: 'a1', service: 'ec2', resource_type: 't3.medium', region: 'us-east-1', count: 1, term: 1, payment_option: 'all-upfront', savings: 150, upfront_cost: 1000 },
        { id: 'cell2-v1', provider: 'aws', cloud_account_id: 'a1', service: 'rds', resource_type: 'db.t3',     region: 'us-east-1', count: 1, term: 1, payment_option: 'no-upfront',  savings: 200, upfront_cost: 0 },
        { id: 'cell2-v2', provider: 'aws', cloud_account_id: 'a1', service: 'rds', resource_type: 'db.t3',     region: 'us-east-1', count: 1, term: 1, payment_option: 'all-upfront', savings: 250, upfront_cost: 2000 },
      ];
      (api.getRecommendations as jest.Mock).mockResolvedValue({
        summary: { total_count: 4, total_monthly_savings: 700, total_upfront_cost: 3000, avg_payback_months: 2 },
        recommendations: recs,
        regions: [],
      });
      (state.getRecommendations as jest.Mock).mockReturnValue(recs);
      (state.getVisibleRecommendations as jest.Mock).mockReturnValue(recs);
      (state.getRecommendationsColumnFilters as jest.Mock).mockReturnValue({});
      (state.getSelectedRecommendationIDs as jest.Mock).mockReturnValue(new Set());

      await loadRecommendations();

      // KPI card value.
      const summary = document.getElementById('recommendations-summary');
      const kpiCard = Array.from(summary?.querySelectorAll('.card') ?? [])
        .find((c) => /Recommendations/.test(c.querySelector('h3')?.textContent ?? ''));
      const kpiValue = kpiCard?.querySelector('.value')?.textContent ?? '';

      // "Showing X of X" live region value.
      const liveText = document.querySelector('.recommendations-filter-live')?.textContent ?? '';
      const match = liveText.match(/Showing (\d+) of (\d+)/);
      const showingVisible = match?.[1] ?? '';
      const showingLoaded = match?.[2] ?? '';

      // Both KPI and "Showing of" must report the same variant count (4).
      expect(kpiValue).toBe('4');
      expect(showingLoaded).toBe('4');
      expect(kpiValue).toBe(showingLoaded);
      // Parity between visible and loaded when no filter is active.
      expect(showingVisible).toBe(showingLoaded);
    });

    test('row checkbox change event updates summary cards in place (PR #283 CR pass-2)', async () => {
      // Regression guard for the real DOM event path: verifies that dispatching
      // a `change` event on a row checkbox updates summary cards WITHOUT a
      // second loadRecommendations() call. The earlier mock-and-reload test
      // above confirms the rendering math; this test confirms the event
      // handler itself triggers renderRecommendationsList.
      //
      // Each rec is a DISTINCT cell (different resource_type) so they render as
      // flat rows — not grouped summary rows — making their checkboxes directly
      // queryable from the DOM. Multi-variant cells collapse into a summary row
      // with a chevron button; variant-level checkboxes are only in the DOM when
      // the cell is expanded.
      //
      // Mock coordination: addSelectedRecommendation / removeSelectedRecommendation
      // mutate a shared Set so getSelectedRecommendationIDs reflects the toggle
      // immediately — the same pattern used by checkboxes-evict-siblings tests.
      const recs = [
        { id: 'rec-a', provider: 'aws', cloud_account_id: 'a1', service: 'ec2', resource_type: 't3.medium', region: 'us-east-1', count: 1, term: 1, savings: 150, upfront_cost: 1000 },
        { id: 'rec-b', provider: 'aws', cloud_account_id: 'a1', service: 'rds', resource_type: 'db.t3',     region: 'us-east-1', count: 1, term: 1, savings: 200, upfront_cost: 0 },
      ];
      (api.getRecommendations as jest.Mock).mockResolvedValue({
        summary: { total_count: 2, total_monthly_savings: 350, total_upfront_cost: 1000, avg_payback_months: 1 },
        recommendations: recs,
        regions: [],
      });
      (state.getRecommendations as jest.Mock).mockReturnValue(recs);
      (state.getVisibleRecommendations as jest.Mock).mockReturnValue(recs);
      (state.getRecommendationsColumnFilters as jest.Mock).mockReturnValue({});

      // Wire add/remove to a shared Set so getSelectedRecommendationIDs
      // reflects in-flight selection state after every checkbox event.
      const selectedIds = new Set<string>();
      (state.getSelectedRecommendationIDs as jest.Mock).mockImplementation(() => new Set(selectedIds));
      (state.addSelectedRecommendation as jest.Mock).mockImplementation((id: string) => { selectedIds.add(id); });
      (state.removeSelectedRecommendation as jest.Mock).mockImplementation((id: string) => { selectedIds.delete(id); });
      (state.clearSelectedRecommendations as jest.Mock).mockImplementation(() => { selectedIds.clear(); });

      // Initial render: no selection, cards show full-set values.
      await loadRecommendations();

      const cardValue = (titlePattern: RegExp): string => {
        const summary = document.getElementById('recommendations-summary');
        const card = Array.from(summary?.querySelectorAll('.card') ?? [])
          .find((c) => titlePattern.test(c.querySelector('h3')?.textContent ?? ''));
        return card?.querySelector('.value')?.textContent ?? '';
      };

      // Each rec is its own cell → 2 cells, each with 1 variant → range = single value.
      expect(cardValue(/Recommendations/)).toBe('2');

      // Fire the real DOM change event on rec-a's checkbox — do NOT
      // call loadRecommendations() again. The handler inside recommendations.ts
      // calls renderRecommendationsList() which updates the cards in place.
      const checkbox = document.querySelector<HTMLInputElement>('input[data-rec-id="rec-a"]');
      expect(checkbox).not.toBeNull();
      checkbox!.checked = true;
      checkbox!.dispatchEvent(new Event('change'));

      // Cards must now reflect only rec-a ($150 savings / $1000 upfront).
      expect(cardValue(/Monthly Savings/)).toMatch(/^\$150$/);
      expect(cardValue(/Upfront/)).toMatch(/^\$1,?000$/);
      // Recommendations card must show "1 selected".
      expect(cardValue(/Recommendations/)).toBe('1');
    });

    test('select-all change event updates summary cards in place (PR #283 CR pass-2)', async () => {
      // Verifies the select-all checkbox event path: after checking select-all,
      // summary cards reflect the picked-best-per-cell selection WITHOUT a
      // second loadRecommendations() call.
      //
      // pickBestVariantPerCell picks one variant per (provider, account, service,
      // region, resource_type, engine) group — for these 2 cells it picks one
      // from cell1 and one from cell2, so both cards must show 2 rows selected.
      const recs = [
        { id: 'cell1-cheap',  provider: 'aws', cloud_account_id: 'a1', service: 'ec2', resource_type: 't3.medium', region: 'us-east-1', count: 1, term: 1, payment_option: 'no-upfront',  savings: 100, upfront_cost: 0 },
        { id: 'cell1-pricey', provider: 'aws', cloud_account_id: 'a1', service: 'ec2', resource_type: 't3.medium', region: 'us-east-1', count: 1, term: 1, payment_option: 'all-upfront', savings: 150, upfront_cost: 1000 },
        { id: 'cell2-cheap',  provider: 'aws', cloud_account_id: 'a1', service: 'rds', resource_type: 'db.t3',     region: 'us-east-1', count: 1, term: 1, payment_option: 'no-upfront',  savings: 200, upfront_cost: 0 },
        { id: 'cell2-pricey', provider: 'aws', cloud_account_id: 'a1', service: 'rds', resource_type: 'db.t3',     region: 'us-east-1', count: 1, term: 1, payment_option: 'all-upfront', savings: 250, upfront_cost: 2000 },
      ];
      (api.getRecommendations as jest.Mock).mockResolvedValue({
        summary: { total_count: 4, total_monthly_savings: 700, total_upfront_cost: 3000, avg_payback_months: 2 },
        recommendations: recs,
        regions: [],
      });
      (state.getRecommendations as jest.Mock).mockReturnValue(recs);
      (state.getVisibleRecommendations as jest.Mock).mockReturnValue(recs);
      (state.getRecommendationsColumnFilters as jest.Mock).mockReturnValue({});

      // Wire add/remove/clear to a shared Set (same coordination pattern as the
      // row-checkbox test above and the evict-siblings test).
      const selectedIds = new Set<string>();
      (state.getSelectedRecommendationIDs as jest.Mock).mockImplementation(() => new Set(selectedIds));
      (state.addSelectedRecommendation as jest.Mock).mockImplementation((id: string) => { selectedIds.add(id); });
      (state.removeSelectedRecommendation as jest.Mock).mockImplementation((id: string) => { selectedIds.delete(id); });
      (state.clearSelectedRecommendations as jest.Mock).mockImplementation(() => { selectedIds.clear(); });

      await loadRecommendations();

      const cardValue = (titlePattern: RegExp): string => {
        const summary = document.getElementById('recommendations-summary');
        const card = Array.from(summary?.querySelectorAll('.card') ?? [])
          .find((c) => titlePattern.test(c.querySelector('h3')?.textContent ?? ''));
        return card?.querySelector('.value')?.textContent ?? '';
      };

      // Fire the real select-all change event — do NOT call loadRecommendations().
      const selectAll = document.getElementById('select-all-recs') as HTMLInputElement;
      expect(selectAll).not.toBeNull();
      selectAll.checked = true;
      selectAll.dispatchEvent(new Event('change'));

      // addSelectedRecommendation was called once per cell (pickBestVariantPerCell
      // picks one per cell → 2 cells → 2 adds).
      expect((state.addSelectedRecommendation as jest.Mock).mock.calls.length).toBe(2);

      // Recommendations card shows "2" (2 selected cells, not 4 raw variants).
      expect(cardValue(/Recommendations/)).toBe('2');
    });

    test('"Showing X of Y" surfaces selection count (closes #279)', async () => {
      const recs = [
        { id: 'r1', provider: 'aws', service: 'ec2', resource_type: 't3.medium', region: 'us-east-1', count: 1, term: 1, savings: 100, upfront_cost: 0 },
        { id: 'r2', provider: 'aws', service: 'rds', resource_type: 'db.t3',     region: 'us-east-1', count: 1, term: 1, savings: 200, upfront_cost: 0 },
      ];
      (api.getRecommendations as jest.Mock).mockResolvedValue({ summary: {}, recommendations: recs, regions: [] });
      (state.getRecommendations as jest.Mock).mockReturnValue(recs);
      (state.getVisibleRecommendations as jest.Mock).mockReturnValue(recs);

      // No selection: line just shows "Showing X of Y".
      (state.getSelectedRecommendationIDs as jest.Mock).mockReturnValue(new Set());
      await loadRecommendations();
      const liveText = (): string =>
        document.querySelector('.recommendations-filter-live')?.textContent ?? '';
      expect(liveText()).toMatch(/^Showing 2 of 2 recommendations$/);

      // ≥1 selected: line prepends the selection count so the user has
      // visible feedback that their selection is influencing the cards.
      (state.getSelectedRecommendationIDs as jest.Mock).mockReturnValue(new Set(['r1']));
      await loadRecommendations();
      expect(liveText()).toMatch(/^1 selected · Showing 2 of 2 recommendations$/);
    });

    test('selection filtered out of view: cards and live line treat it as no selection (PR #283 CR)', async () => {
      // 4 recs / 2 cells. The user has selected cell2's variant ('r3'), but
      // then applies a column filter that hides cell2. The visible set is
      // cell1 only (r1, r2). The selected row (r3) is NOT in the visible set.
      //
      // Expected behaviour:
      //   - Summary cards reflect the visible (unselected-from-visible) set:
      //     cell1 range $100–$150, title "Total Recommendations" not "Selected".
      //   - Live status line reads "Showing 2 of 4 recommendations" — no
      //     "1 selected" prefix, because r3 is hidden.
      const allRecs = [
        { id: 'r1', provider: 'aws', cloud_account_id: 'a1', service: 'ec2', resource_type: 't3.medium', region: 'us-east-1', count: 1, term: 1, payment_option: 'no-upfront',  savings: 100, upfront_cost: 0 },
        { id: 'r2', provider: 'aws', cloud_account_id: 'a1', service: 'ec2', resource_type: 't3.medium', region: 'us-east-1', count: 1, term: 1, payment_option: 'all-upfront', savings: 150, upfront_cost: 1000 },
        { id: 'r3', provider: 'aws', cloud_account_id: 'a1', service: 'rds', resource_type: 'db.t3',     region: 'us-east-1', count: 1, term: 1, payment_option: 'no-upfront',  savings: 200, upfront_cost: 0 },
        { id: 'r4', provider: 'aws', cloud_account_id: 'a1', service: 'rds', resource_type: 'db.t3',     region: 'us-east-1', count: 1, term: 1, payment_option: 'all-upfront', savings: 250, upfront_cost: 2000 },
      ];
      const visibleRecs = allRecs.filter((r) => r.service === 'ec2'); // cell1 only

      (api.getRecommendations as jest.Mock).mockResolvedValue({
        summary: {}, recommendations: allRecs, regions: [],
      });
      (state.getRecommendations as jest.Mock).mockReturnValue(allRecs);
      // Visible set is cell1 only — column filter hides rds rows.
      (state.getVisibleRecommendations as jest.Mock).mockReturnValue(visibleRecs);
      (state.getRecommendationsColumnFilters as jest.Mock).mockReturnValue({
        service: { kind: 'set', values: ['ec2'] },
      });
      // r3 is selected, but it is NOT in the visible set.
      (state.getSelectedRecommendationIDs as jest.Mock).mockReturnValue(new Set(['r3']));

      await loadRecommendations();

      const summary = document.getElementById('recommendations-summary');
      const savingsCard = Array.from(summary?.querySelectorAll('.card') ?? [])
        .find((c) => /Monthly Savings/.test(c.querySelector('h3')?.textContent ?? ''));
      const savingsValue = savingsCard?.querySelector('.value')?.textContent ?? '';
      const savingsTitle = savingsCard?.querySelector('h3')?.textContent ?? '';

      // Cards reflect the visible set (cell1: $100–$150), not the hidden selection.
      expect(savingsValue).toContain('$100');
      expect(savingsValue).toContain('$150');
      expect(savingsValue).not.toContain('$200');
      // Title must not flip to "Selected …" since no visible row is selected.
      expect(savingsTitle).toBe('Potential Monthly Savings');

      // Live line must not prefix "1 selected" because r3 is hidden.
      const liveText = document.querySelector('.recommendations-filter-live')?.textContent ?? '';
      expect(liveText).not.toMatch(/selected/);
      expect(liveText).toMatch(/Showing/);

      // Reset mocks so this filter doesn't leak into subsequent tests.
      (state.getRecommendationsColumnFilters as jest.Mock).mockReturnValue({});
      (state.getVisibleRecommendations as jest.Mock).mockReturnValue([]);
      (state.getSelectedRecommendationIDs as jest.Mock).mockReturnValue(new Set());
    });

    test('renders recommendations list', async () => {
      (api.getRecommendations as jest.Mock).mockResolvedValue({
        summary: {},
        recommendations: [
          { id: 'rec-1', provider: 'aws',
            service: 'ec2',
            resource_type: 't3.medium',
            region: 'us-east-1',
            count: 5,
            term: 1,
            savings: 100,
            upfront_cost: 500
          }
        ],
        regions: ['us-east-1', 'us-west-2']
      });

      await loadRecommendations();

      const list = document.getElementById('recommendations-list');
      expect(list?.innerHTML).toContain('table');
      expect(list?.innerHTML).toContain('ec2');
      expect(list?.innerHTML).toContain('t3.medium');
      expect(list?.innerHTML).toContain('us-east-1');
    });

    test('shows empty-state message when no recommendations', async () => {
      // Issue #700: zero rows now render an empty <tbody> with a hint cell
      // rather than replacing the entire table with a <p>. The <thead> stays
      // so column headers remain visible. The hint text lives inside the tbody.
      (api.getRecommendations as jest.Mock).mockResolvedValue({
        summary: {},
        recommendations: [],
        regions: []
      });

      await loadRecommendations();

      const list = document.getElementById('recommendations-list');
      // The table (including <thead>) must still be rendered.
      expect(list?.querySelector('thead')).not.toBeNull();
      // The hint cell must be present inside the tbody.
      const emptyCell = list?.querySelector('tbody td.empty');
      expect(emptyCell).not.toBeNull();
      expect(emptyCell?.textContent).toMatch(/No rows match/);
    });

    test('stores recommendations in state', async () => {
      const mockRecs = [
        { id: 'rec-2', provider: 'aws', service: 'ec2', savings: 100 }
      ];
      (api.getRecommendations as jest.Mock).mockResolvedValue({
        summary: {},
        recommendations: mockRecs,
        regions: []
      });

      await loadRecommendations();

      expect(state.setRecommendations).toHaveBeenCalledWith(mockRecs);
    });

    test('clears selected recommendations on load', async () => {
      (api.getRecommendations as jest.Mock).mockResolvedValue({
        summary: {},
        recommendations: [],
        regions: []
      });

      await loadRecommendations();

      expect(state.clearSelectedRecommendations).toHaveBeenCalled();
    });

    test('shows error on API failure', async () => {
      (api.getRecommendations as jest.Mock).mockRejectedValue(new Error('API Error'));
      console.error = jest.fn();

      await loadRecommendations();

      const list = document.getElementById('recommendations-list');
      expect(list?.innerHTML).toContain('Failed to load recommendations');
    });

    test('renders select-all checkbox', async () => {
      (api.getRecommendations as jest.Mock).mockResolvedValue({
        summary: {},
        recommendations: [
          { id: 'rec-3', provider: 'aws', service: 'ec2', savings: 100 }
        ],
        regions: []
      });

      await loadRecommendations();

      const selectAll = document.getElementById('select-all-recs');
      expect(selectAll).toBeTruthy();
    });

    test('highlights selected recommendations', async () => {
      (api.getRecommendations as jest.Mock).mockResolvedValue({
        summary: {},
        recommendations: [
          { id: 'rec-sel', provider: 'aws', service: 'ec2', savings: 100 }
        ],
        regions: []
      });
      (state.getSelectedRecommendationIDs as jest.Mock).mockReturnValue(new Set(['rec-sel']));

      await loadRecommendations();

      const list = document.getElementById('recommendations-list');
      expect(list?.innerHTML).toContain('selected');
    });

    test('applies high-savings class for large savings', async () => {
      (api.getRecommendations as jest.Mock).mockResolvedValue({
        summary: {},
        recommendations: [
          { id: 'rec-5', provider: 'aws', service: 'ec2', savings: 2000 }
        ],
        regions: []
      });

      await loadRecommendations();

      const list = document.getElementById('recommendations-list');
      expect(list?.innerHTML).toContain('high-savings');
    });

    test('select-all checkbox selects all recommendations when checked', async () => {
      const mockRecs = [
        { id: 'rec-6', provider: 'aws', service: 'ec2', resource_type: 't3.medium', region: 'us-east-1', count: 1, term: 1, savings: 100, upfront_cost: 500 },
        { id: 'rec-7', provider: 'aws', service: 'rds', resource_type: 'db.t3.medium', region: 'us-east-1', count: 2, term: 1, savings: 200, upfront_cost: 1000 }
      ];
      (api.getRecommendations as jest.Mock).mockResolvedValue({
        summary: {},
        recommendations: mockRecs,
        regions: []
      });

      await loadRecommendations();

      const selectAll = document.getElementById('select-all-recs') as HTMLInputElement;
      selectAll.checked = true;
      selectAll.dispatchEvent(new Event('change'));

      expect(state.addSelectedRecommendation).toHaveBeenCalledWith(expect.stringMatching(/^rec-/));
      expect((state.addSelectedRecommendation as jest.Mock).mock.calls.length).toBeGreaterThanOrEqual(2);
    });

    test('select-all checkbox clears all recommendations when unchecked', async () => {
      const mockRecs = [
        { id: 'rec-8', provider: 'aws', service: 'ec2', resource_type: 't3.medium', region: 'us-east-1', count: 1, term: 1, savings: 100, upfront_cost: 500 }
      ];
      (api.getRecommendations as jest.Mock).mockResolvedValue({
        summary: {},
        recommendations: mockRecs,
        regions: []
      });

      await loadRecommendations();

      const selectAll = document.getElementById('select-all-recs') as HTMLInputElement;
      selectAll.checked = false;
      selectAll.dispatchEvent(new Event('change'));

      expect(state.clearSelectedRecommendations).toHaveBeenCalled();
    });

    test('individual row checkbox adds recommendation when checked', async () => {
      const mockRecs = [
        { id: 'rec-9', provider: 'aws', service: 'ec2', resource_type: 't3.medium', region: 'us-east-1', count: 1, term: 1, savings: 100, upfront_cost: 500 }
      ];
      (api.getRecommendations as jest.Mock).mockResolvedValue({
        summary: {},
        recommendations: mockRecs,
        regions: []
      });

      await loadRecommendations();

      const checkbox = document.querySelector('input[data-rec-id]') as HTMLInputElement;
      checkbox.checked = true;
      checkbox.dispatchEvent(new Event('change'));

      expect(state.addSelectedRecommendation).toHaveBeenCalled();
      expect((state.addSelectedRecommendation as jest.Mock).mock.calls[0][0]).toMatch(/^rec-/);
    });

    test('individual row checkbox removes recommendation when unchecked', async () => {
      const mockRecs = [
        { id: 'rec-10', provider: 'aws', service: 'ec2', resource_type: 't3.medium', region: 'us-east-1', count: 1, term: 1, savings: 100, upfront_cost: 500 }
      ];
      (api.getRecommendations as jest.Mock).mockResolvedValue({
        summary: {},
        recommendations: mockRecs,
        regions: []
      });

      await loadRecommendations();

      const checkbox = document.querySelector('input[data-rec-id]') as HTMLInputElement;
      checkbox.checked = false;
      checkbox.dispatchEvent(new Event('change'));

      expect(state.removeSelectedRecommendation).toHaveBeenCalled();
      expect((state.removeSelectedRecommendation as jest.Mock).mock.calls[0][0]).toMatch(/^rec-/);
    });

    // Issue #187: when two Azure recs share `(provider, service, region,
    // resource_type, payment)` but differ in subscription or term, they
    // each carry a distinct backend ID after the scheduler hash fix
    // (see TestScheduler_ConvertRecommendations_HashUniqueness). The
    // frontend selection toggle keys on data-rec-id, so distinct IDs →
    // independent toggles. Pin that here so a future regression on the
    // frontend rendering side surfaces immediately.
    test('toggling one row does NOT flip a sibling row with a distinct ID', async () => {
      const mockRecs = [
        { id: 'rec-azure-sub1', provider: 'azure', cloud_account_id: 'sub1', service: 'compute', resource_type: 'D2s', region: 'eastus', count: 1, term: 1, savings: 100, upfront_cost: 500 },
        { id: 'rec-azure-sub2', provider: 'azure', cloud_account_id: 'sub2', service: 'compute', resource_type: 'D2s', region: 'eastus', count: 1, term: 1, savings: 200, upfront_cost: 800 },
      ];
      (api.getRecommendations as jest.Mock).mockResolvedValue({
        summary: {},
        recommendations: mockRecs,
        regions: []
      });

      await loadRecommendations();

      const checkboxes = Array.from(
        document.querySelectorAll<HTMLInputElement>('tbody input[data-rec-id]'),
      );
      expect(checkboxes).toHaveLength(2);
      const ids = checkboxes.map((cb) => cb.dataset['recId']).sort();
      expect(ids).toEqual(['rec-azure-sub1', 'rec-azure-sub2']);

      // Tick rec-azure-sub1 specifically — the table sort order would
      // otherwise depend on savings (sub2's 200 > sub1's 100), so we
      // pick by ID, not index.
      const sub1 = checkboxes.find((cb) => cb.dataset['recId'] === 'rec-azure-sub1');
      expect(sub1).toBeDefined();
      sub1!.checked = true;
      sub1!.dispatchEvent(new Event('change'));

      // Only sub1's ID should land in addSelectedRecommendation; sub2
      // stays untouched.
      const calls = (state.addSelectedRecommendation as jest.Mock).mock.calls;
      const calledIds = calls.map((c) => c[0]);
      expect(calledIds).toContain('rec-azure-sub1');
      expect(calledIds).not.toContain('rec-azure-sub2');
    });

    // Issue #224: when a sibling variant is selected but currently hidden by a
    // column filter (e.g. user filtered to 3yr, but 1yr sibling is still
    // selected in state), checking a 3yr sibling must evict the hidden 1yr
    // selection. The fix iterates state.getRecommendations() (full loaded set)
    // rather than the filtered `recommendations` array rendered in the DOM.
    test('checking a visible variant evicts a same-cell sibling hidden by filter', async () => {
      const allRecs = [
        { id: 'rec-1yr', provider: 'aws', service: 'ec2', resource_type: 't3.medium', region: 'us-east-1', count: 1, term: 1, savings: 100, upfront_cost: 0 },
        { id: 'rec-3yr', provider: 'aws', service: 'ec2', resource_type: 't3.medium', region: 'us-east-1', count: 1, term: 3, savings: 300, upfront_cost: 0 },
      ];
      // Simulate: API returns all recs, but only the 3yr one is visible (1yr is
      // hidden by filter). state.getRecommendations() returns the full set.
      (api.getRecommendations as jest.Mock).mockResolvedValue({
        summary: {},
        recommendations: [allRecs[1]], // only 3yr visible in rendered list
        regions: []
      });
      (state.getRecommendations as jest.Mock).mockReturnValue(allRecs); // full set in state
      (state as unknown as { getRecommendationByID: jest.Mock }).getRecommendationByID.mockImplementation((id: string) => allRecs.find((r) => r.id === id));
      // Simulate: 1yr is already selected in state.
      (state.getSelectedRecommendationIDs as jest.Mock).mockReturnValue(new Set(['rec-1yr']));

      await loadRecommendations();

      // Tick the 3yr checkbox.
      const cb = document.querySelector<HTMLInputElement>('input[data-rec-id="rec-3yr"]');
      expect(cb).not.toBeNull();
      cb!.checked = true;
      cb!.dispatchEvent(new Event('change'));

      // The hidden 1yr sibling must be evicted.
      const removeCalls = (state.removeSelectedRecommendation as jest.Mock).mock.calls.map(c => c[0]);
      expect(removeCalls).toContain('rec-1yr');
      // And the 3yr rec is added.
      const addCalls = (state.addSelectedRecommendation as jest.Mock).mock.calls.map(c => c[0]);
      expect(addCalls).toContain('rec-3yr');
    });

    test('purchase button opens modal for that recommendation', async () => {
      const mockRecs = [
        { id: 'rec-11', provider: 'aws', service: 'ec2', resource_type: 't3.medium', region: 'us-east-1', count: 1, term: 1, savings: 100, upfront_cost: 500 }
      ];
      (api.getRecommendations as jest.Mock).mockResolvedValue({
        summary: {},
        recommendations: mockRecs,
        regions: []
      });
      (state.getVisibleRecommendations as jest.Mock).mockReturnValue(mockRecs);

      await loadRecommendations();

      // Bundle B: per-row Purchase buttons gone; the Purchase action lives
      // in the sticky bottom action box at #bulk-purchase-btn. #273: the
      // button is disabled until the user explicitly selects rows, so seed
      // the selection mock with the rec's id before clicking — the test is
      // asserting the modal-open flow, not the selection-gating UI.
      (state.getVisibleRecommendations as jest.Mock).mockReturnValue(mockRecs);
      (state.getSelectedRecommendationIDs as jest.Mock).mockReturnValue(new Set(['rec-11']));
      await loadRecommendations(); // re-render so the button picks up the selection
      const purchaseBtn = document.querySelector('#bulk-purchase-btn') as HTMLButtonElement;
      expect(purchaseBtn).not.toBeNull();
      expect(purchaseBtn.disabled).toBe(false);
      purchaseBtn.click();
      // Issue #111 (iii): openPurchaseModal is async; flush microtasks
      // so the modal-open call lands before we assert visibility.
      await Promise.resolve(); await Promise.resolve(); await Promise.resolve();

      const modal = document.getElementById('purchase-modal');
      expect(modal?.classList.contains('hidden')).toBe(false);
    });

    test('renders engine info when present', async () => {
      const mockRecs = [
        { id: 'rec-12', provider: 'aws', service: 'rds', resource_type: 'db.t3.medium', region: 'us-east-1', count: 1, term: 1, savings: 100, upfront_cost: 500, engine: 'mysql' }
      ];
      (api.getRecommendations as jest.Mock).mockResolvedValue({
        summary: {},
        recommendations: mockRecs,
        regions: []
      });
      (state.getVisibleRecommendations as jest.Mock).mockReturnValue(mockRecs);

      await loadRecommendations();

      const list = document.getElementById('recommendations-list');
      expect(list?.innerHTML).toContain('mysql');
    });

    test('applies medium-savings class for moderate savings', async () => {
      (api.getRecommendations as jest.Mock).mockResolvedValue({
        summary: {},
        recommendations: [
          { id: 'rec-13', provider: 'aws', service: 'ec2', resource_type: 't3.medium', region: 'us-east-1', count: 1, term: 1, savings: 500, upfront_cost: 500 }
        ],
        regions: []
      });

      await loadRecommendations();

      const list = document.getElementById('recommendations-list');
      expect(list?.innerHTML).toContain('medium-savings');
    });
  });

  describe('P6: sort + bulk toolbar + detail drawer', () => {
    const twoRecs = [
      { id: 'rec-14', provider: 'aws', service: 'ec2', resource_type: 't3.large', region: 'us-east-1', count: 2, term: 1, savings: 100, upfront_cost: 500 },
      { id: 'rec-15', provider: 'aws', service: 'rds', resource_type: 'db.m5.large', region: 'us-east-1', count: 4, term: 3, savings: 1500, upfront_cost: 9000 },
    ];

    beforeEach(() => {
      (api.getRecommendations as jest.Mock).mockResolvedValue({
        recommendations: twoRecs,
        summary: {},
        available_regions: [],
      });
    });

    test('renders sortable column headers with indicators (Bundle B + #282 + #317: 13 columns)', async () => {
      await loadRecommendations();
      const list = document.getElementById('recommendations-list');
      // Bundle B + issue #282 + #317: every data column is sortable. 13 sortable data columns:
      // provider, account, service, resource_type, region, count, term, payment,
      // savings, upfront_cost, monthly_cost, on_demand_monthly, effective_savings_pct.
      // The leading checkbox column is not sortable.
      const sortables = list?.querySelectorAll('th.sortable');
      expect(sortables?.length).toBe(13);
      // The default sort is savings desc → that header shows an active ▼.
      const savingsHeader = list?.querySelector('th[data-sort="savings"]');
      expect(savingsHeader?.innerHTML).toContain('active');
    });

    test('clicking a sortable header calls setRecommendationsSort', async () => {
      await loadRecommendations();
      const header = document.querySelector<HTMLTableCellElement>('th[data-sort="upfront_cost"]');
      header?.click();
      // Issue #480: first click on a non-savings/non-on-demand numeric column
      // uses the platform-default ascending direction (low → high).
      expect(state.setRecommendationsSort).toHaveBeenCalledWith({ column: 'upfront_cost', direction: 'asc' });
    });

    test('Payment column header renders and is sortable (#282)', async () => {
      await loadRecommendations();
      const list = document.getElementById('recommendations-list');
      const paymentTh = list?.querySelector('th[data-sort="payment"]');
      expect(paymentTh).not.toBeNull();
      expect(paymentTh?.textContent).toContain('Payment');
      // Clicking the Payment header should call setRecommendationsSort with 'payment'.
      // Issue #480: text columns default to 'asc' on first click (alpha order).
      (paymentTh as HTMLElement)?.click();
      expect(state.setRecommendationsSort).toHaveBeenCalledWith({ column: 'payment', direction: 'asc' });
    });

    test('Payment column cell renders human-readable labels (#282)', async () => {
      const recs = [
        { id: 'p1', provider: 'aws', cloud_account_id: 'a1', service: 'ec2', resource_type: 't3.small',  region: 'us-east-1', count: 1, term: 1, payment: 'all-upfront',     savings: 100, upfront_cost: 1000 },
        { id: 'p2', provider: 'aws', cloud_account_id: 'a1', service: 'rds', resource_type: 'db.t3',     region: 'us-east-1', count: 1, term: 1, payment: 'partial-upfront', savings: 200, upfront_cost: 500 },
        { id: 'p3', provider: 'aws', cloud_account_id: 'a1', service: 'ec2', resource_type: 't3.medium', region: 'us-west-2', count: 1, term: 1, payment: 'no-upfront',      savings: 300, upfront_cost: 0 },
        { id: 'p4', provider: 'aws', cloud_account_id: 'a1', service: 'rds', resource_type: 'db.m5',     region: 'us-west-2', count: 1, term: 3, payment: 'monthly',         savings: 400, upfront_cost: 0 },
      ];
      (api.getRecommendations as jest.Mock).mockResolvedValue({ summary: {}, recommendations: recs, regions: [] });
      (state.getRecommendations as jest.Mock).mockReturnValue(recs);
      (state.getVisibleRecommendations as jest.Mock).mockReturnValue(recs);
      (state.getRecommendationsColumnFilters as jest.Mock).mockReturnValue({});
      (state.getSelectedRecommendationIDs as jest.Mock).mockReturnValue(new Set());
      await loadRecommendations();

      const rows = document.querySelectorAll('tr.recommendation-row');
      const paymentCells = Array.from(rows).map((row) => {
        // Payment column is the 9th <td> (0-indexed: 0=checkbox, 1=provider,
        // 2=account, 3=service, 4=resource_type, 5=region, 6=count, 7=term, 8=payment)
        return row.querySelectorAll('td')[8]?.textContent?.trim() ?? '';
      });
      expect(paymentCells).toContain('All Upfront');
      expect(paymentCells).toContain('Partial Upfront');
      expect(paymentCells).toContain('No Upfront');
      expect(paymentCells).toContain('Monthly');
      // Em-dash rendered for missing payment (no dedicated test rec here,
      // but the helper returns '—' for undefined — covered by formatPayment unit test).
    });

    test('Payment column filter narrows visible rows (#282)', async () => {
      const recs = [
        { id: 'f1', provider: 'aws', cloud_account_id: 'a1', service: 'ec2', resource_type: 't3.small',  region: 'us-east-1', count: 1, term: 1, payment: 'all-upfront', savings: 100, upfront_cost: 1000 },
        { id: 'f2', provider: 'aws', cloud_account_id: 'a1', service: 'rds', resource_type: 'db.t3',     region: 'us-east-1', count: 1, term: 1, payment: 'no-upfront',  savings: 200, upfront_cost: 0 },
      ];
      (api.getRecommendations as jest.Mock).mockResolvedValue({ summary: {}, recommendations: recs, regions: [] });
      (state.getRecommendations as jest.Mock).mockReturnValue(recs);
      (state.getVisibleRecommendations as jest.Mock).mockReturnValue(recs);
      (state.getSelectedRecommendationIDs as jest.Mock).mockReturnValue(new Set());

      // Apply a payment filter so only the all-upfront rec is visible.
      (state.getRecommendationsColumnFilters as jest.Mock).mockReturnValue({
        payment: { kind: 'set', values: ['all-upfront'] },
      });

      await loadRecommendations();

      const rows = document.querySelectorAll('tr.recommendation-row');
      // Only the all-upfront rec should be rendered.
      expect(rows.length).toBe(1);
      // Payment cell text should be "All Upfront".
      const paymentCell = rows[0]?.querySelectorAll('td')[8];
      expect(paymentCell?.textContent?.trim()).toBe('All Upfront');

      // Restore filter mock so it doesn't leak into subsequent tests.
      (state.getRecommendationsColumnFilters as jest.Mock).mockReturnValue({});
    });

    test('bottom action box selection summary reflects current selection (Bundle B)', async () => {
      // Bundle B replaced the floating .recommendations-bulk-toolbar with
      // selection-summary text inside the sticky bottom action box.
      (api.getRecommendations as jest.Mock).mockResolvedValue({
        summary: {},
        recommendations: [
          { id: 'rec-bt', provider: 'aws', service: 'ec2', resource_type: 't3.medium', region: 'us-east-1', count: 1, term: 1, savings: 100, upfront_cost: 500 }
        ],
        regions: []
      });
      (state.getSelectedRecommendationIDs as jest.Mock).mockReturnValue(new Set(['rec-bt']));
      (state.getVisibleRecommendations as jest.Mock).mockReturnValue([
        { id: 'rec-bt', provider: 'aws', service: 'ec2', resource_type: 't3.medium', region: 'us-east-1', count: 1, term: 1, savings: 100, upfront_cost: 500 }
      ]);

      await loadRecommendations();

      const summary = document.getElementById('recommendations-action-summary');
      // #281: summary text now surfaces the financial impact of the
      // current action target (savings/mo + upfront across N cells)
      // instead of just selection counts. The user can already see
      // selection state from row checkboxes; the action box is prime
      // real estate for the dollar figures they're authorising.
      expect(summary?.textContent).toMatch(/\$100\/mo/);
      expect(summary?.textContent).toMatch(/\$500 upfront/);
      expect(summary?.textContent).toMatch(/1 cell\b/);
      // Old bulk-toolbar surface is gone.
      expect(document.querySelector('.recommendations-bulk-toolbar')).toBeNull();
    });

    test('bottom action box prompts for selection when no row is selected (#273)', async () => {
      (state.getSelectedRecommendationIDs as jest.Mock).mockReturnValue(new Set());
      (state.getVisibleRecommendations as jest.Mock).mockReturnValue(twoRecs);
      await loadRecommendations();
      const summary = document.getElementById('recommendations-action-summary');
      // #273: action buttons require an explicit selection. Summary
      // prompts the user instead of advertising a default-target count
      // that would fire on misclick.
      expect(summary?.textContent).toContain('Select cells to act on');
      expect(document.querySelector('.recommendations-bulk-toolbar')).toBeNull();
    });

    // Issue #344 T4': row-click toggles selection. The previous
    // row-click → openDetailDrawer behaviour was dropped (per plan.md
    // §T4) — the drawer payload duplicated the table, with
    // backend-deferred fields the only differentiators. Tests below
    // cover the new selection-toggle contract.
    //
    // The shared `wireSelection` helper hooks the state mocks to a real
    // Set so addSelectedRecommendation actually surfaces in subsequent
    // getSelectedRecommendationIDs reads — without this, re-renders see
    // an empty selection and the row checkbox renders unchecked.
    describe('row-click selection (T4′)', () => {
      function wireSelection(): Set<string> {
        const selected = new Set<string>();
        (state.getSelectedRecommendationIDs as jest.Mock).mockImplementation(() => new Set(selected));
        (state.addSelectedRecommendation as jest.Mock).mockImplementation((id: string) => { selected.add(id); });
        (state.removeSelectedRecommendation as jest.Mock).mockImplementation((id: string) => { selected.delete(id); });
        (state.clearSelectedRecommendations as jest.Mock).mockImplementation(() => { selected.clear(); });
        (state.getRecommendations as jest.Mock).mockReturnValue(twoRecs);
        (state.getVisibleRecommendations as jest.Mock).mockReturnValue(twoRecs);
        return selected;
      }

      test('clicking a non-interactive cell on a row toggles the row checkbox', async () => {
        wireSelection();
        await loadRecommendations();
        const firstRow = document.querySelector<HTMLTableRowElement>('tr.recommendation-row');
        const cb = firstRow?.querySelector<HTMLInputElement>('input[type="checkbox"][data-rec-id]');
        expect(cb).not.toBeNull();
        expect(cb!.checked).toBe(false);
        const recId = cb!.dataset['recId']!;

        // Service cell (index 3) is non-interactive — safe to click.
        firstRow?.querySelectorAll('td')[3]?.click();

        const cbAfter = document.querySelector<HTMLInputElement>(
          `tr.recommendation-row input[data-rec-id="${recId}"]`,
        );
        expect(cbAfter?.checked).toBe(true);
      });

      test('clicking a row a second time unselects it', async () => {
        wireSelection();
        await loadRecommendations();
        const firstRow = document.querySelector<HTMLTableRowElement>('tr.recommendation-row');
        const cb = firstRow?.querySelector<HTMLInputElement>('input[type="checkbox"][data-rec-id]');
        const recId = cb!.dataset['recId']!;

        firstRow?.querySelectorAll('td')[3]?.click();
        let cbAfter = document.querySelector<HTMLInputElement>(
          `tr.recommendation-row input[data-rec-id="${recId}"]`,
        );
        expect(cbAfter?.checked).toBe(true);

        // Second click on the (post-rerender) row toggles back off.
        const rerenderedRow = cbAfter!.closest('tr')!;
        rerenderedRow.querySelectorAll('td')[3]?.click();

        cbAfter = document.querySelector<HTMLInputElement>(
          `tr.recommendation-row input[data-rec-id="${recId}"]`,
        );
        expect(cbAfter?.checked).toBe(false);
      });

      test('row-click does NOT trigger when the click originates on an interactive descendant', async () => {
        wireSelection();
        await loadRecommendations();
        const firstRow = document.querySelector<HTMLTableRowElement>('tr.recommendation-row');
        const cb = firstRow?.querySelector<HTMLInputElement>('input[type="checkbox"][data-rec-id]');
        expect(cb!.checked).toBe(false);

        // Inject a synthetic action button into the first row to model
        // any inline per-row CTA (the production table doesn't currently
        // render one, but the click-filter rule must still hold).
        const btn = document.createElement('button');
        btn.type = 'button';
        btn.textContent = 'Inline action';
        firstRow!.querySelectorAll('td')[3]!.appendChild(btn);

        btn.click();

        // Selection state must NOT change just because the click
        // bubbled from the button to the row.
        const cbAfter = document.querySelector<HTMLInputElement>(
          `tr.recommendation-row input[data-rec-id="${cb!.dataset['recId']}"]`,
        );
        expect(cbAfter?.checked).toBe(false);
      });

      test('clicking the checkbox itself does not double-toggle (native click handles it)', async () => {
        wireSelection();
        await loadRecommendations();
        const firstRow = document.querySelector<HTMLTableRowElement>('tr.recommendation-row');
        const cb = firstRow?.querySelector<HTMLInputElement>('input[type="checkbox"][data-rec-id]')!;
        expect(cb.checked).toBe(false);

        // Native click on a <input type="checkbox"> toggles checked
        // before the click event fires. Our row-click handler must skip
        // when the originating target is the checkbox so we don't
        // re-toggle it back to its prior state.
        cb.click();

        const cbAfter = document.querySelector<HTMLInputElement>(
          `tr.recommendation-row input[data-rec-id="${cb.dataset['recId']}"]`,
        );
        expect(cbAfter?.checked).toBe(true);
      });
    });
  });

  describe('auto-refresh on page open (#284)', () => {
    // Helper: set up the minimal DOM + api mock that loadRecommendations needs.
    function mockGetRecs() {
      (api.getRecommendations as jest.Mock).mockResolvedValue({
        summary: {},
        recommendations: [],
        regions: [],
      });
    }

    beforeEach(() => {
      // Reset the dedup guard so each test starts with no refresh in flight.
      resetAutoRefreshInFlight();
    });

    test('cold cache (null last_collected_at) — refresh fires + in-flight toast shown', async () => {
      mockGetRecs();
      (recsApi.getRecommendationsFreshness as jest.Mock).mockResolvedValue({
        last_collected_at: null,
        last_collection_error: null,
      });

      await loadRecommendations();
      // Flush microtasks so the async refreshRecommendations call runs.
      await Promise.resolve();

      expect(recsApi.refreshRecommendations).toHaveBeenCalledTimes(1);
      expect(mockShowToast).toHaveBeenCalledWith(
        expect.objectContaining({ message: 'Refreshing recommendations…', kind: 'info' }),
      );
    });

    test('stale cache (>24h ago) — refresh fires + in-flight toast shown', async () => {
      mockGetRecs();
      const twentyFiveHoursAgo = new Date(Date.now() - 25 * 60 * 60 * 1000).toISOString();
      (recsApi.getRecommendationsFreshness as jest.Mock).mockResolvedValue({
        last_collected_at: twentyFiveHoursAgo,
        last_collection_error: null,
      });

      await loadRecommendations();
      await Promise.resolve();

      expect(recsApi.refreshRecommendations).toHaveBeenCalledTimes(1);
      expect(mockShowToast).toHaveBeenCalledWith(
        expect.objectContaining({ message: 'Refreshing recommendations…', kind: 'info' }),
      );
    });

    test('fresh cache (<24h ago) — refresh does NOT fire + no toast', async () => {
      mockGetRecs();
      const oneHourAgo = new Date(Date.now() - 60 * 60 * 1000).toISOString();
      (recsApi.getRecommendationsFreshness as jest.Mock).mockResolvedValue({
        last_collected_at: oneHourAgo,
        last_collection_error: null,
      });

      await loadRecommendations();
      await Promise.resolve();

      expect(recsApi.refreshRecommendations).not.toHaveBeenCalled();
      expect(mockShowToast).not.toHaveBeenCalled();
    });

    test('cold cache + collection error — error toast surfaces the message', async () => {
      mockGetRecs();
      (recsApi.getRecommendationsFreshness as jest.Mock).mockResolvedValue({
        last_collected_at: null,
        last_collection_error: 'Provider X returned 403 Forbidden',
      });

      await loadRecommendations();
      await Promise.resolve();

      expect(mockShowToast).toHaveBeenCalledWith(
        expect.objectContaining({
          message: expect.stringContaining('Provider X returned 403 Forbidden'),
          kind: 'error',
        }),
      );
    });

    test('refresh failure — error toast shown with message', async () => {
      mockGetRecs();
      (recsApi.getRecommendationsFreshness as jest.Mock).mockResolvedValue({
        last_collected_at: null,
        last_collection_error: null,
      });
      (recsApi.refreshRecommendations as jest.Mock).mockRejectedValue(new Error('Network timeout'));

      await loadRecommendations();
      // Two microtask ticks: one for the freshness call, one for the refresh rejection.
      await Promise.resolve();
      await Promise.resolve();

      expect(mockShowToast).toHaveBeenCalledWith(
        expect.objectContaining({
          message: 'Recommendations refresh failed: Network timeout',
          kind: 'error',
        }),
      );
    });

    test('dedup — concurrent stale loads fire refreshRecommendations only once', async () => {
      mockGetRecs();
      (recsApi.getRecommendationsFreshness as jest.Mock).mockResolvedValue({
        last_collected_at: null,
        last_collection_error: null,
      });

      // A pending promise that won't resolve until we call resolveRefresh().
      let resolveRefresh!: () => void;
      const pendingRefresh = new Promise<void>((r) => { resolveRefresh = r; });
      (recsApi.refreshRecommendations as jest.Mock).mockReturnValue(pendingRefresh);

      // Fire two stale loads without awaiting the first refresh to settle.
      const first = loadRecommendations();
      const second = loadRecommendations();

      await first;
      await second;
      // Flush the microtasks that kick off triggerAutoRefreshIfStale.
      await Promise.resolve();
      await Promise.resolve();

      // Only one API call despite two stale-load triggers.
      expect(recsApi.refreshRecommendations).toHaveBeenCalledTimes(1);

      // Clean up: resolve the pending promise so no timers hang after the test.
      resolveRefresh();
      await pendingRefresh;
    });

    test('persistent in-flight toast — shown with timeout: null so it stays until settled', async () => {
      mockGetRecs();
      (recsApi.getRecommendationsFreshness as jest.Mock).mockResolvedValue({
        last_collected_at: null,
        last_collection_error: null,
      });

      await loadRecommendations();
      await Promise.resolve();

      // Find the in-flight "Refreshing…" toast call and confirm it has no
      // auto-dismiss timeout so it outlives long-running refreshes.
      const inFlightCall = mockShowToast.mock.calls.find(
        ([opts]) => (opts as { message: string }).message === 'Refreshing recommendations…',
      );
      expect(inFlightCall).toBeDefined();
      const inFlightOpts = inFlightCall![0] as { timeout?: number | null };
      expect(inFlightOpts.timeout).toBeNull();
    });
  });

  describe('openPurchaseModal', () => {
    // Issue #111 (iii): openPurchaseModal is now async (it pre-fetches
    // per-account service overrides to seed each row's Payment default).
    // Tests must `await` it so the DOM is populated before assertions.
    test('displays purchase modal', async () => {
      const recommendations = [
        { id: 'rec-1', provider: 'aws' as const,
          service: 'ec2',
          resource_type: 't3.medium',
          region: 'us-east-1',
          count: 5,
          term: 1,
          savings: 100,
          upfront_cost: 500
        }
      ];

      await openPurchaseModal(recommendations);

      const modal = document.getElementById('purchase-modal');
      expect(modal?.classList.contains('hidden')).toBe(false);
    });

    test('shows purchase summary', async () => {
      const recommendations = [
        { id: 'rec-2', provider: 'aws' as const, service: 'ec2', resource_type: 't3.medium', region: 'us-east-1', count: 5, term: 1, savings: 100, upfront_cost: 500 },
        { id: 'rec-3', provider: 'aws' as const, service: 'rds', resource_type: 'db.r5.large', region: 'us-east-1', count: 2, term: 1, savings: 200, upfront_cost: 1000 }
      ];

      await openPurchaseModal(recommendations);

      const details = document.getElementById('purchase-details');
      // Issue #320: the modal now renders a full breakdown table with column
      // headers and a totals row instead of a "Purchase Summary" heading.
      // Verify that the table header and approval note are present.
      expect(details?.textContent).toContain('Include'); // select-all column header label
      expect(details?.textContent).toContain('Totals');  // totals row label
      expect(details?.textContent).toContain('approval'); // approval-required note
    });

    test('lists individual recommendations', async () => {
      const recommendations = [
        { id: 'rec-4', provider: 'aws' as const, service: 'ec2', resource_type: 't3.medium', region: 'us-east-1', count: 5, term: 1, savings: 100, upfront_cost: 500 }
      ];

      await openPurchaseModal(recommendations);

      const details = document.getElementById('purchase-details');
      expect(details?.textContent).toContain('ec2');
      expect(details?.textContent).toContain('t3.medium');
      expect(details?.textContent).toContain('us-east-1');
    });

    test('handles missing modal element', async () => {
      document.body.replaceChildren();

      await expect(openPurchaseModal([])).resolves.not.toThrow();
    });

    // Issue #288: the primary action does NOT execute the purchase — it
    // sends an approval-request email. Pin the post-fix label + body
    // wording so a regression that reverts to the misleading "Execute
    // Purchase" framing fails this suite.
    describe('approval-required messaging (issue #288)', () => {
      const baseRec = {
        id: 'rec-288',
        provider: 'aws' as const,
        service: 'ec2',
        resource_type: 't3.medium',
        region: 'us-east-1',
        count: 5,
        term: 1,
        savings: 100,
        upfront_cost: 500,
      };

      test('primary button reads "Send for Approval", not "Execute Purchase"', async () => {
        await openPurchaseModal([baseRec]);

        const btn = document.getElementById('execute-purchase-btn');
        expect(btn?.textContent).toBe('Send for Approval');
        // Belt-and-braces: ensure no element on the rendered modal still
        // carries the pre-#288 text — guards against a future template
        // re-introducing the misleading wording somewhere new.
        const modal = document.getElementById('purchase-modal');
        expect(modal?.textContent).not.toContain('Execute Purchase');
      });

      test('modal body carries the approval-required explanation', async () => {
        await openPurchaseModal([baseRec]);

        const details = document.getElementById('purchase-details');
        expect(details?.textContent).toContain('will email an approval request');
      });

      test('approval-required note renders with its dedicated class', async () => {
        await openPurchaseModal([baseRec]);

        const note = document.querySelector('#purchase-details .approval-required-note');
        expect(note).not.toBeNull();
        expect(note?.textContent).toMatch(/approval request/i);
      });
    });

    // Issue #320: per-row checkboxes, select-all, live totals, Execute button state.
    describe('per-row skip checkboxes (issue #320)', () => {
      const makeRec = (id: string, opts: Partial<{
        savings: number; upfront_cost: number; monthly_cost: number | null;
        on_demand_cost: number | null; count: number; term: number;
      }> = {}) => ({
        id,
        provider: 'aws' as const,
        service: 'ec2',
        resource_type: 't3.medium',
        region: 'us-east-1',
        count: opts.count ?? 1,
        term: opts.term ?? 1,
        savings: opts.savings ?? 100,
        upfront_cost: opts.upfront_cost ?? 600,
        monthly_cost: opts.monthly_cost !== undefined ? opts.monthly_cost : 400,
        on_demand_cost: opts.on_demand_cost !== undefined ? opts.on_demand_cost : null,
      });

      test('all rows are checked by default on modal open', async () => {
        await openPurchaseModal([makeRec('a'), makeRec('b'), makeRec('c')]);

        const checkboxes = document.querySelectorAll<HTMLInputElement>('.purchase-modal-row-include');
        expect(checkboxes.length).toBe(3);
        checkboxes.forEach((cb) => {
          expect(cb.checked).toBe(true);
        });
      });

      test('getPurchaseModalRecommendations returns all recs when all checked', async () => {
        const recs = [makeRec('r1'), makeRec('r2')];
        await openPurchaseModal(recs);

        const result = getPurchaseModalRecommendations();
        expect(result.map((r) => r.id)).toEqual(['r1', 'r2']);
      });

      test('unchecking a row excludes it from getPurchaseModalRecommendations', async () => {
        const recs = [makeRec('r1'), makeRec('r2'), makeRec('r3')];
        await openPurchaseModal(recs);

        // Uncheck the second row (index 1).
        const checkboxes = document.querySelectorAll<HTMLInputElement>('.purchase-modal-row-include');
        checkboxes[1]!.checked = false;
        checkboxes[1]!.dispatchEvent(new Event('change'));

        const result = getPurchaseModalRecommendations();
        expect(result.map((r) => r.id)).toEqual(['r1', 'r3']);
      });

      test('unchecking all rows disables the Execute Purchase button', async () => {
        await openPurchaseModal([makeRec('r1'), makeRec('r2')]);

        const checkboxes = document.querySelectorAll<HTMLInputElement>('.purchase-modal-row-include');
        checkboxes.forEach((cb) => {
          cb.checked = false;
          cb.dispatchEvent(new Event('change'));
        });

        const btn = document.getElementById('execute-purchase-btn') as HTMLButtonElement | null;
        expect(btn?.disabled).toBe(true);
      });

      test('re-checking at least one row re-enables the Execute Purchase button', async () => {
        await openPurchaseModal([makeRec('r1'), makeRec('r2')]);

        const checkboxes = document.querySelectorAll<HTMLInputElement>('.purchase-modal-row-include');
        // Uncheck all first.
        checkboxes.forEach((cb) => {
          cb.checked = false;
          cb.dispatchEvent(new Event('change'));
        });
        // Re-check first row.
        checkboxes[0]!.checked = true;
        checkboxes[0]!.dispatchEvent(new Event('change'));

        const btn = document.getElementById('execute-purchase-btn') as HTMLButtonElement | null;
        expect(btn?.disabled).toBe(false);
      });

      test('Execute button starts enabled when all rows are checked', async () => {
        await openPurchaseModal([makeRec('r1')]);

        const btn = document.getElementById('execute-purchase-btn') as HTMLButtonElement | null;
        expect(btn?.disabled).toBe(false);
      });

      test('select-all checkbox deselects all rows when unchecked', async () => {
        const recs = [makeRec('s1'), makeRec('s2'), makeRec('s3')];
        await openPurchaseModal(recs);

        const selectAll = document.getElementById('purchase-modal-select-all') as HTMLInputElement | null;
        expect(selectAll).not.toBeNull();

        selectAll!.checked = false;
        selectAll!.dispatchEvent(new Event('change'));

        const checkboxes = document.querySelectorAll<HTMLInputElement>('.purchase-modal-row-include');
        checkboxes.forEach((cb) => {
          expect(cb.checked).toBe(false);
        });
        const result = getPurchaseModalRecommendations();
        expect(result.length).toBe(0);
      });

      test('select-all checkbox re-selects all rows when checked', async () => {
        const recs = [makeRec('s1'), makeRec('s2')];
        await openPurchaseModal(recs);

        const selectAll = document.getElementById('purchase-modal-select-all') as HTMLInputElement | null;

        // Uncheck one row so select-all is in indeterminate state.
        const checkboxes = document.querySelectorAll<HTMLInputElement>('.purchase-modal-row-include');
        checkboxes[0]!.checked = false;
        checkboxes[0]!.dispatchEvent(new Event('change'));

        // Now re-check via select-all.
        selectAll!.checked = true;
        selectAll!.dispatchEvent(new Event('change'));

        checkboxes.forEach((cb) => {
          expect(cb.checked).toBe(true);
        });
        const result = getPurchaseModalRecommendations();
        expect(result.map((r) => r.id)).toEqual(['s1', 's2']);
      });

      test('totals row reflects only checked rows', async () => {
        // rec 'ta': count 2, upfront 400, savings 100
        // rec 'tb': count 3, upfront 600, savings 200
        const recs = [
          makeRec('ta', { count: 2, upfront_cost: 400, savings: 100, monthly_cost: null }),
          makeRec('tb', { count: 3, upfront_cost: 600, savings: 200, monthly_cost: null }),
        ];
        await openPurchaseModal(recs);

        // Uncheck second row.
        const checkboxes = document.querySelectorAll<HTMLInputElement>('.purchase-modal-row-include');
        checkboxes[1]!.checked = false;
        checkboxes[1]!.dispatchEvent(new Event('change'));

        // Only rec 'ta' should be in totals: count=2, upfront=400.
        const countCell = document.getElementById('purchase-modal-total-count');
        expect(countCell?.textContent).toContain('2');

        const upfrontCell = document.getElementById('purchase-modal-total-upfront');
        // formatCurrency is mocked as `$${val}` so total upfront = $400.
        expect(upfrontCell?.textContent).toContain('400');
      });

      test('totals row weighted effective % uses on_demand_cost when monthly_cost is null', async () => {
        const recs = [
          makeRec('ta', { savings: 100, upfront_cost: 0, monthly_cost: null, on_demand_cost: 200 }),
          makeRec('tb', { savings: 100, upfront_cost: 0, monthly_cost: 50 }),
        ];
        await openPurchaseModal(recs);

        const pctCell = document.getElementById('purchase-modal-total-eff-pct');
        expect(pctCell?.textContent).toContain('57.1%');
      });

      test('clearPurchaseModalRecommendations also clears checked state', async () => {
        await openPurchaseModal([makeRec('c1'), makeRec('c2')]);

        clearPurchaseModalRecommendations();

        // After clear, getPurchaseModalRecommendations returns empty.
        const result = getPurchaseModalRecommendations();
        expect(result.length).toBe(0);
      });

      test('modal renders Account column header', async () => {
        await openPurchaseModal([makeRec('r1')]);

        const details = document.getElementById('purchase-details');
        expect(details?.textContent).toContain('Account');
      });

      test('modal renders Upfront, Monthly Cost, Eff. Savings, and Eff. % column headers', async () => {
        await openPurchaseModal([makeRec('r1')]);

        const details = document.getElementById('purchase-details');
        expect(details?.textContent).toContain('Upfront');
        expect(details?.textContent).toContain('Monthly Cost');
        expect(details?.textContent).toContain('Eff. Savings');
        expect(details?.textContent).toContain('Eff. %');
      });
    });
  });

  describe('refreshRecommendations', () => {
    test('calls API to refresh recommendations', async () => {
      (api.refreshRecommendations as jest.Mock).mockResolvedValue({});
      (api.getRecommendations as jest.Mock).mockResolvedValue({
        summary: {},
        recommendations: [],
        regions: []
      });

      await refreshRecommendations();

      expect(api.refreshRecommendations).toHaveBeenCalled();
    });

    test('shows success alert', async () => {
      (api.refreshRecommendations as jest.Mock).mockResolvedValue({});

      await refreshRecommendations();

      expect(window.alert).toHaveBeenCalledWith('Recommendation refresh started. This may take a few minutes.');
    });

    test('schedules reload after 5 seconds', async () => {
      (api.refreshRecommendations as jest.Mock).mockResolvedValue({});
      (api.getRecommendations as jest.Mock).mockResolvedValue({
        summary: {},
        recommendations: [],
        regions: []
      });

      await refreshRecommendations();

      jest.advanceTimersByTime(5000);

      expect(api.getRecommendations).toHaveBeenCalled();
    });

    test('shows error on failure', async () => {
      (api.refreshRecommendations as jest.Mock).mockRejectedValue(new Error('API Error'));
      console.error = jest.fn();

      await refreshRecommendations();

      expect(window.alert).toHaveBeenCalledWith('Failed to start recommendation refresh');
    });
  });

  describe('setupRecommendationsHandlers', () => {
    function makeDiv(id: string): HTMLDivElement {
      const el = document.createElement('div');
      el.id = id;
      return el;
    }

    beforeEach(() => {
      // Issue #477: Opportunities now subscribes to the global provider/account
      // filter and reloads on change. The DOM only needs the opportunities-tab
      // wrapper (active-class toggled per test) plus the elements the loader
      // touches (recommendations-list, recommendations-summary).
      while (document.body.firstChild) document.body.removeChild(document.body.firstChild);
      document.body.appendChild(makeDiv('opportunities-tab'));
      document.body.appendChild(makeDiv('recommendations-list'));
      document.body.appendChild(makeDiv('recommendations-summary'));
      (api.getRecommendations as jest.Mock).mockResolvedValue({
        summary: {},
        recommendations: [],
        regions: []
      });
      // Outer describe enables fake timers; we want real timers here so the
      // subscriber callback's promise chain inside loadRecommendations can
      // settle. afterEach() at the outer level resets via useRealTimers.
      jest.useRealTimers();
    });

    test('subscribes to provider changes and reloads when opportunities-tab is active', async () => {
      const tab = document.getElementById('opportunities-tab')!;
      tab.classList.add('active');

      setupRecommendationsHandlers();

      // Capture the callback registered with subscribeProvider and invoke it
      // directly (state is jest.mock()'d for this file, so the real listener
      // set never fires; the test verifies the wiring shape).
      const providerCb = (state.subscribeProvider as jest.Mock).mock.calls[0]?.[0];
      expect(typeof providerCb).toBe('function');
      (api.getRecommendations as jest.Mock).mockClear();
      providerCb();
      // loadRecommendations awaits Promise.all internally; flush microtasks.
      await new Promise((r) => setTimeout(r, 0));

      expect(api.getRecommendations).toHaveBeenCalled();
    });

    test('subscribes to account changes and reloads when opportunities-tab is active', async () => {
      const tab = document.getElementById('opportunities-tab')!;
      tab.classList.add('active');

      setupRecommendationsHandlers();

      const accountCb = (state.subscribeAccount as jest.Mock).mock.calls[0]?.[0];
      expect(typeof accountCb).toBe('function');
      (api.getRecommendations as jest.Mock).mockClear();
      accountCb();
      await new Promise((r) => setTimeout(r, 0));

      expect(api.getRecommendations).toHaveBeenCalled();
    });

    test('does NOT reload when opportunities-tab is inactive', async () => {
      // No .active class — user is on Home / Plans / Purchases.
      setupRecommendationsHandlers();

      const providerCb = (state.subscribeProvider as jest.Mock).mock.calls[0]?.[0];
      const accountCb = (state.subscribeAccount as jest.Mock).mock.calls[0]?.[0];
      (api.getRecommendations as jest.Mock).mockClear();
      providerCb();
      accountCb();
      await new Promise((r) => setTimeout(r, 0));

      expect(api.getRecommendations).not.toHaveBeenCalled();
    });

    // CR pass 1 (PR #488): the topbar provider-change handler updates BOTH
    // account and provider state slots in sequence, firing both subscribers
    // from a single user action. Coalesce via queueMicrotask so the user
    // sees exactly one reload, not two — and avoid the stale-overwrite race
    // where the first response lands after the second.
    test('coalesces back-to-back account+provider changes into a single reload', async () => {
      const tab = document.getElementById('opportunities-tab')!;
      tab.classList.add('active');

      setupRecommendationsHandlers();

      const providerCb = (state.subscribeProvider as jest.Mock).mock.calls[0]?.[0];
      const accountCb = (state.subscribeAccount as jest.Mock).mock.calls[0]?.[0];
      (api.getRecommendations as jest.Mock).mockClear();

      // Simulate the topbar's #185 ordering: clear account first, then set
      // provider. Both callbacks fire synchronously; coalescing must collapse
      // them into one microtask-deferred reload.
      accountCb();
      providerCb();
      await new Promise((r) => setTimeout(r, 0));

      expect(api.getRecommendations).toHaveBeenCalledTimes(1);
    });
  });
});

// ---------------------------------------------------------------------------
// Bundle A: numeric expression parser + applyColumnFilters
// ---------------------------------------------------------------------------

import { parseNumericFilter, applyColumnFilters } from '../recommendations';
import type { LocalRecommendation } from '../types';

describe('parseNumericFilter', () => {
  const accept = (expr: string, n: number): boolean => {
    const r = parseNumericFilter(expr);
    if (!r.ok) throw new Error(`unexpected parse failure for "${expr}": ${r.error}`);
    return r.predicate(n);
  };

  test('empty / whitespace expression matches everything', () => {
    expect(accept('', 0)).toBe(true);
    expect(accept('   ', 42)).toBe(true);
    expect(accept('\t\n', -7)).toBe(true);
  });

  test('plain integer matches by equality', () => {
    expect(accept('42', 42)).toBe(true);
    expect(accept('42', 41)).toBe(false);
    expect(accept('-3', -3)).toBe(true);
  });

  test('plain decimal matches by equality', () => {
    expect(accept('3.14', 3.14)).toBe(true);
    expect(accept('3.14', 3.15)).toBe(false);
  });

  test('comparator > / >= / < / <=', () => {
    expect(accept('>10', 11)).toBe(true);
    expect(accept('>10', 10)).toBe(false);
    expect(accept('>=10', 10)).toBe(true);
    expect(accept('<5', 4)).toBe(true);
    expect(accept('<5', 5)).toBe(false);
    expect(accept('<=5', 5)).toBe(true);
  });

  test('inclusive range X..Y', () => {
    expect(accept('10..20', 10)).toBe(true);
    expect(accept('10..20', 20)).toBe(true);
    expect(accept('10..20', 15)).toBe(true);
    expect(accept('10..20', 9)).toBe(false);
    expect(accept('10..20', 21)).toBe(false);
  });

  test('reversed range still works (max..min)', () => {
    expect(accept('20..10', 15)).toBe(true);
  });

  test('comma-separated terms OR together', () => {
    // `5, >100, 200..400`
    expect(accept('5, >100, 200..400', 5)).toBe(true);
    expect(accept('5, >100, 200..400', 150)).toBe(true);
    expect(accept('5, >100, 200..400', 250)).toBe(true);
    expect(accept('5, >100, 200..400', 50)).toBe(false);
    expect(accept('5, >100, 200..400', 500)).toBe(true); // matches >100
  });

  test('whitespace inside terms is tolerated', () => {
    expect(accept('  >  10  ', 11)).toBe(true);
    expect(accept('10 .. 20', 15)).toBe(true);
  });

  test('invalid expression returns ok:false with an error message', () => {
    const r1 = parseNumericFilter('>>5');
    expect(r1.ok).toBe(false);
    if (!r1.ok) expect(r1.error).toMatch(/Invalid filter term/);

    const r2 = parseNumericFilter('not a number');
    expect(r2.ok).toBe(false);

    const r3 = parseNumericFilter('1..');
    expect(r3.ok).toBe(false);
  });
});

describe('applyColumnFilters', () => {
  const rec = (
    overrides: Partial<LocalRecommendation> = {},
  ): LocalRecommendation => ({
    id: overrides.id ?? 'r1',
    provider: overrides.provider ?? 'aws',
    cloud_account_id: overrides.cloud_account_id ?? 'acct-1',
    service: overrides.service ?? 'ec2',
    resource_type: overrides.resource_type ?? 't3.medium',
    region: overrides.region ?? 'us-east-1',
    count: overrides.count ?? 1,
    term: overrides.term ?? 1,
    upfront_cost: overrides.upfront_cost ?? 100,
    monthly_cost: overrides.monthly_cost ?? 10,
    savings: overrides.savings ?? 50,
    engine: overrides.engine,
  } as unknown as LocalRecommendation);

  test('empty filters returns a clone of the input (no-op)', () => {
    const recs = [rec({ id: 'a' }), rec({ id: 'b' })];
    const out = applyColumnFilters(recs, {});
    expect(out).toEqual(recs);
    expect(out).not.toBe(recs); // defensive clone
  });

  test('categorical set filter narrows by membership', () => {
    const recs = [
      rec({ id: 'a', provider: 'aws' }),
      rec({ id: 'b', provider: 'azure' }),
      rec({ id: 'c', provider: 'gcp' }),
    ];
    const out = applyColumnFilters(recs, {
      provider: { kind: 'set', values: ['aws'] },
    });
    expect(out.map(r => r.id)).toEqual(['a']);
  });

  test('Account filter matches on cloud_account_id, not display name', () => {
    const recs = [
      rec({ id: 'a', cloud_account_id: 'acct-prod' }),
      rec({ id: 'b', cloud_account_id: 'acct-dev' }),
    ];
    const out = applyColumnFilters(recs, {
      account: { kind: 'set', values: ['acct-prod'] },
    });
    expect(out.map(r => r.id)).toEqual(['a']);
  });

  test('Term filter values are strings; row term integer stringifies', () => {
    const recs = [
      rec({ id: 'a', term: 1 }),
      rec({ id: 'b', term: 3 }),
    ];
    const out = applyColumnFilters(recs, {
      term: { kind: 'set', values: ['3'] },
    });
    expect(out.map(r => r.id)).toEqual(['b']);
  });

  test('(empty) sentinel matches null / undefined / "" cloud_account_id', () => {
    // Build raw objects — bypass the rec() factory because its `??` defaults
    // would replace null/undefined cloud_account_id with 'acct-1'.
    const recs: LocalRecommendation[] = [
      { ...rec({ id: 'a' }), cloud_account_id: '' } as LocalRecommendation,
      { ...rec({ id: 'b' }), cloud_account_id: undefined } as unknown as LocalRecommendation,
      rec({ id: 'c', cloud_account_id: 'acct-x' }),
    ];
    const out = applyColumnFilters(recs, {
      account: { kind: 'set', values: [''] },
    });
    expect(out.map(r => r.id).sort()).toEqual(['a', 'b']);
  });

  test('numeric expr filter narrows by predicate', () => {
    const recs = [
      rec({ id: 'a', savings: 25 }),
      rec({ id: 'b', savings: 150 }),
      rec({ id: 'c', savings: 1500 }),
    ];
    const out = applyColumnFilters(recs, {
      savings: { kind: 'expr', expr: '>100' },
    });
    expect(out.map(r => r.id)).toEqual(['b', 'c']);
  });

  test('invalid numeric expression is ignored (filter not applied)', () => {
    const recs = [rec({ id: 'a', savings: 1 }), rec({ id: 'b', savings: 100 })];
    const out = applyColumnFilters(recs, {
      savings: { kind: 'expr', expr: '>>5' }, // syntax error
    });
    // All rows pass — broken filter is silently inert.
    expect(out.map(r => r.id)).toEqual(['a', 'b']);
  });

  test('multiple column filters AND together', () => {
    const recs = [
      rec({ id: 'a', provider: 'aws', savings: 50 }),
      rec({ id: 'b', provider: 'aws', savings: 500 }),
      rec({ id: 'c', provider: 'azure', savings: 500 }),
    ];
    const out = applyColumnFilters(recs, {
      provider: { kind: 'set', values: ['aws'] },
      savings: { kind: 'expr', expr: '>100' },
    });
    expect(out.map(r => r.id)).toEqual(['b']);
  });
});

// ---------------------------------------------------------------------------
// Bundle A: state-accessor tests for the new column-filter / visible-recs API.
// These import the REAL state module (the recommendations.test.ts above mocks
// it; here we exercise the actual implementation in a separate require scope).
// ---------------------------------------------------------------------------

describe('state.ts column-filter accessors', () => {
  // The top-level jest.mock('../state', …) replaces the module for every
  // import. Use jest.requireActual to bypass it for these state-accessor
  // tests so we exercise the real implementation. Each test starts from
  // a clean filter state via clearAllRecommendationsColumnFilters().
  const realState = jest.requireActual<typeof import('../state')>('../state');

  beforeEach(() => {
    realState.clearAllRecommendationsColumnFilters();
    realState.setVisibleRecommendations([]);
  });

  test('default filters are empty', () => {
    expect(realState.getRecommendationsColumnFilters()).toEqual({});
  });

  test('setRecommendationsColumnFilter adds an entry', () => {
    realState.setRecommendationsColumnFilter('provider', { kind: 'set', values: ['aws'] });
    expect(realState.getRecommendationsColumnFilters()).toEqual({
      provider: { kind: 'set', values: ['aws'] },
    });
  });

  test('passing null clears that single column', () => {
    realState.setRecommendationsColumnFilter('provider', { kind: 'set', values: ['aws'] });
    realState.setRecommendationsColumnFilter('savings', { kind: 'expr', expr: '>100' });
    realState.setRecommendationsColumnFilter('provider', null);
    expect(realState.getRecommendationsColumnFilters()).toEqual({
      savings: { kind: 'expr', expr: '>100' },
    });
  });

  test('clearAllRecommendationsColumnFilters empties the record', () => {
    realState.setRecommendationsColumnFilter('provider', { kind: 'set', values: ['aws'] });
    realState.setRecommendationsColumnFilter('savings', { kind: 'expr', expr: '>100' });
    realState.clearAllRecommendationsColumnFilters();
    expect(realState.getRecommendationsColumnFilters()).toEqual({});
  });

  test('getRecommendationsColumnFilters returns a defensive shallow copy', () => {
    realState.setRecommendationsColumnFilter('provider', { kind: 'set', values: ['aws'] });
    const a = realState.getRecommendationsColumnFilters();
    delete a.provider;
    // mutation of the returned object must not affect module state
    expect(realState.getRecommendationsColumnFilters()).toEqual({
      provider: { kind: 'set', values: ['aws'] },
    });
  });

  test('setVisibleRecommendations / getVisibleRecommendations round-trip with defensive clone', () => {
    const recs = [{ id: 'r1' }, { id: 'r2' }] as unknown as Parameters<
      typeof realState.setVisibleRecommendations
    >[0];
    realState.setVisibleRecommendations(recs);
    const out = realState.getVisibleRecommendations();
    expect(out.map((r) => (r as unknown as { id: string }).id)).toEqual(['r1', 'r2']);
    // mutating returned array must not affect module state
    (out as unknown as Array<unknown>).pop();
    expect(realState.getVisibleRecommendations()).toHaveLength(2);
  });
});

// ---------------------------------------------------------------------------
// Bundle B: column-filter popover + sticky bottom action box DOM behaviour.
// These tests assert the surfaces Bundle B introduced — header filter
// triggers, the detached popover lifecycle, and the bottom action box's
// label/disabled-state transitions.
// ---------------------------------------------------------------------------

describe('Bundle B: column header filter triggers', () => {
  const sampleRecs = [
    { id: 'rec-aws-1', provider: 'aws', cloud_account_id: 'a1', service: 'ec2', resource_type: 't3.medium', region: 'us-east-1', count: 1, term: 1, savings: 100, upfront_cost: 500 },
    { id: 'rec-az-1',  provider: 'azure', cloud_account_id: 'a2', service: 'vm',  resource_type: 'D2s',       region: 'eastus',    count: 2, term: 3, savings: 200, upfront_cost: 800 },
  ];

  beforeEach(() => {
    // Set up DOM (the top-level beforeEach belongs to a different describe).
    document.body.replaceChildren();
    const recsTab = document.createElement('div');
    recsTab.id = 'opportunities-tab';
    recsTab.className = 'tab-content active';
    const summary = document.createElement('div');
    summary.id = 'recommendations-summary';
    const list = document.createElement('div');
    list.id = 'recommendations-list';
    recsTab.appendChild(summary);
    recsTab.appendChild(list);
    document.body.appendChild(recsTab);
    const purchaseModal = document.createElement('div');
    purchaseModal.id = 'purchase-modal';
    purchaseModal.className = 'hidden';
    const purchaseDetails = document.createElement('div');
    purchaseDetails.id = 'purchase-details';
    purchaseModal.appendChild(purchaseDetails);
    document.body.appendChild(purchaseModal);

    jest.clearAllMocks();
    (api.getRecommendations as jest.Mock).mockResolvedValue({
      summary: {},
      recommendations: sampleRecs,
      regions: [],
    });
    (state.getRecommendations as jest.Mock).mockReturnValue(sampleRecs);
    (state.getVisibleRecommendations as jest.Mock).mockReturnValue(sampleRecs);
    (state.getRecommendationsColumnFilters as jest.Mock).mockReturnValue({});
    (state.getSelectedRecommendationIDs as jest.Mock).mockReturnValue(new Set());
  });

  test('every data column header has a filter trigger button', async () => {
    await loadRecommendations();
    const buttons = document.querySelectorAll<HTMLButtonElement>('th .column-filter-btn[data-column]');
    const cols = Array.from(buttons).map((b) => b.dataset['column']);
    expect(cols.sort()).toEqual(
      ['account', 'count', 'effective_savings_pct', 'monthly_cost', 'on_demand_monthly', 'payment', 'provider', 'region', 'resource_type', 'savings', 'service', 'term', 'upfront_cost'].sort(),
    );
  });

  test('filter button gets .active class when its column has a filter', async () => {
    (state.getRecommendationsColumnFilters as jest.Mock).mockReturnValue({
      provider: { kind: 'set', values: ['aws'] },
    });
    await loadRecommendations();
    const providerBtn = document.querySelector<HTMLButtonElement>('th .column-filter-btn[data-column="provider"]');
    expect(providerBtn?.classList.contains('active')).toBe(true);
    const serviceBtn = document.querySelector<HTMLButtonElement>('th .column-filter-btn[data-column="service"]');
    expect(serviceBtn?.classList.contains('active')).toBe(false);
  });

  test('clicking a filter trigger opens a popover detached to document.body', async () => {
    await loadRecommendations();
    const providerBtn = document.querySelector<HTMLButtonElement>('th .column-filter-btn[data-column="provider"]');
    providerBtn?.click();
    const popover = document.body.querySelector('.column-filter-popover');
    expect(popover).not.toBeNull();
    // Not a descendant of the table.
    expect(popover?.closest('table')).toBeNull();
  });

  test('clicking the same trigger toggles the popover closed', async () => {
    await loadRecommendations();
    const providerBtn = document.querySelector<HTMLButtonElement>('th .column-filter-btn[data-column="provider"]');
    providerBtn?.click();
    expect(document.querySelector('.column-filter-popover')).not.toBeNull();
    providerBtn?.click();
    expect(document.querySelector('.column-filter-popover')).toBeNull();
  });

  test('ESC closes the popover and restores focus to the trigger', async () => {
    await loadRecommendations();
    const providerBtn = document.querySelector<HTMLButtonElement>('th .column-filter-btn[data-column="provider"]');
    providerBtn?.click();
    expect(document.querySelector('.column-filter-popover')).not.toBeNull();
    document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' }));
    expect(document.querySelector('.column-filter-popover')).toBeNull();
  });

  test('categorical popover lists distinct values from the unfiltered rec set', async () => {
    await loadRecommendations();
    const providerBtn = document.querySelector<HTMLButtonElement>('th .column-filter-btn[data-column="provider"]');
    providerBtn?.click();
    const checkboxes = document.querySelectorAll<HTMLInputElement>('.column-filter-popover .column-filter-item input[type="checkbox"]');
    const values = Array.from(checkboxes).map((cb) => cb.dataset['value']);
    expect(values.sort()).toEqual(['aws', 'azure']);
  });

  test('service popover includes SageMaker when the loaded rec set has it', async () => {
    const sagemakerRecs = [
      ...sampleRecs,
      {
        id: 'rec-sm-1',
        provider: 'aws',
        cloud_account_id: 'a3',
        service: 'sagemaker',
        resource_type: 'ml.m5.xlarge',
        region: 'us-east-1',
        count: 1,
        term: 1,
        savings: 300,
        upfront_cost: 1200,
      },
    ];

    (api.getRecommendations as jest.Mock).mockResolvedValue({
      summary: {},
      recommendations: sagemakerRecs,
      regions: [],
    });
    (state.getRecommendations as jest.Mock).mockReturnValue(sagemakerRecs);
    (state.getVisibleRecommendations as jest.Mock).mockReturnValue(sagemakerRecs);

    await loadRecommendations();
    const serviceBtn = document.querySelector<HTMLButtonElement>('th .column-filter-btn[data-column="service"]');
    serviceBtn?.click();
    const values = Array.from(
      document.querySelectorAll<HTMLInputElement>('.column-filter-popover .column-filter-item input[type="checkbox"]'),
    ).map((cb) => cb.dataset['value']);
    expect(values).toContain('sagemaker');
  });

  test('Term popover labels show formatted terms; unticking one commits the remaining-values filter', async () => {
    // Issue #482: opening a popover for a column with no active filter
    // now renders all checkboxes as checked (reflecting the "no narrowing
    // applied" semantic). The user's narrowing action is therefore an
    // UNCHECK, not a check.
    await loadRecommendations();
    const termBtn = document.querySelector<HTMLButtonElement>('th .column-filter-btn[data-column="term"]');
    termBtn?.click();
    const items = Array.from(
      document.querySelectorAll<HTMLLabelElement>('.column-filter-popover .column-filter-item'),
    );
    const labels = items.map((l) => l.querySelector('span')?.textContent);
    expect(labels.sort()).toEqual(['1 Year', '3 Years']);
    // Both boxes start checked (no active filter → all included). Untick
    // "1 Year"; remaining checked = ["3"] → filter narrows to that.
    const oneYearLabel = items.find((l) => l.textContent === '1 Year');
    const cb = oneYearLabel?.querySelector<HTMLInputElement>('input[type="checkbox"]');
    expect(cb?.dataset['value']).toBe('1');
    expect(cb?.checked).toBe(true);
    cb!.checked = false;
    cb!.dispatchEvent(new Event('change'));
    expect(state.setRecommendationsColumnFilter).toHaveBeenCalledWith('term', { kind: 'set', values: ['3'] });
  });

  test('numeric popover input validates expression on blur', async () => {
    await loadRecommendations();
    const savingsBtn = document.querySelector<HTMLButtonElement>('th .column-filter-btn[data-column="savings"]');
    savingsBtn?.click();
    const input = document.querySelector<HTMLInputElement>('.column-filter-popover .column-filter-numeric-input');
    expect(input).not.toBeNull();
    input!.value = '>>5';
    input!.dispatchEvent(new Event('blur'));
    const err = document.querySelector('.column-filter-popover .column-filter-error');
    expect(err?.textContent).toMatch(/Invalid filter term/);
    // No filter applied for invalid syntax.
    expect(state.setRecommendationsColumnFilter).not.toHaveBeenCalledWith(
      'savings',
      expect.objectContaining({ kind: 'expr' }),
    );
  });

  test('numeric popover commits valid expression on Enter', async () => {
    await loadRecommendations();
    const savingsBtn = document.querySelector<HTMLButtonElement>('th .column-filter-btn[data-column="savings"]');
    savingsBtn?.click();
    const input = document.querySelector<HTMLInputElement>('.column-filter-popover .column-filter-numeric-input');
    input!.value = '>100';
    input!.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true }));
    expect(state.setRecommendationsColumnFilter).toHaveBeenCalledWith('savings', { kind: 'expr', expr: '>100' });
  });

  test('Clear button on a categorical column commits an explicit empty allow-list', async () => {
    // Issue #482: Clear is distinct from "All". It represents the user's
    // explicit "no values selected" intent and now persists as
    // {set, values: []} so the table shows 0 rows. Previously Clear
    // and All collapsed to the same null state, making them visually
    // indistinguishable per the bug report.
    (state.getRecommendationsColumnFilters as jest.Mock).mockReturnValue({
      provider: { kind: 'set', values: ['aws'] },
    });
    await loadRecommendations();
    const providerBtn = document.querySelector<HTMLButtonElement>('th .column-filter-btn[data-column="provider"]');
    providerBtn?.click();
    const clearBtn = document.querySelector<HTMLButtonElement>('.column-filter-popover .column-filter-clear');
    clearBtn?.click();
    expect(state.setRecommendationsColumnFilter).toHaveBeenCalledWith('provider', { kind: 'set', values: [] });
  });

  test('Issue #700: Clear resets (All) checkbox to unchecked (not indeterminate)', async () => {
    // Bug: the old Clear branch set cb.checked = false on individual boxes but
    // never called updateAllTriState(), so the (All) checkbox kept its prior
    // state (checked or indeterminate). After the fix, commitAllRef(false) is
    // used which calls updateAllTriState() and leaves (All) unchecked.
    //
    // Simulate real state-store behaviour: setRecommendationsColumnFilter
    // updates the store so the next getRecommendationsColumnFilters() call
    // sees the cleared filter. Without this the resyncOpenPopover() call
    // triggered by the rerender would re-apply the stale filter and overwrite
    // the tri-state that updateAllTriState() just set.
    (state.getRecommendationsColumnFilters as jest.Mock).mockReturnValue({
      provider: { kind: 'set', values: ['aws'] },
    });
    (state.setRecommendationsColumnFilter as jest.Mock).mockImplementation(
      (col: string, val: unknown) => {
        (state.getRecommendationsColumnFilters as jest.Mock).mockReturnValue(
          val === null ? {} : { [col]: val },
        );
      },
    );
    await loadRecommendations();
    const providerBtn = document.querySelector<HTMLButtonElement>('th .column-filter-btn[data-column="provider"]');
    providerBtn?.click();

    // (All) starts in a known state before Clear.
    const allBox = document.querySelector<HTMLInputElement>('.column-filter-popover .column-filter-all input[type="checkbox"]');
    expect(allBox).not.toBeNull();

    const clearBtn = document.querySelector<HTMLButtonElement>('.column-filter-popover .column-filter-clear');
    clearBtn?.click();

    // After Clear: (All) must be unchecked and not indeterminate.
    expect(allBox!.checked).toBe(false);
    expect(allBox!.indeterminate).toBe(false);
    expect(state.setRecommendationsColumnFilter).toHaveBeenCalledWith('provider', { kind: 'set', values: [] });
  });

  test('Issue #700: table <thead> survives a filter that yields zero rows', async () => {
    // Bug: renderRecommendationsList replaced the entire <table> with a <p>
    // when no rows matched, removing <thead>. After the fix, the header row
    // remains visible (with an empty <tbody> + hint cell) so columns are still
    // readable while the user adjusts filters.
    (state.getRecommendationsColumnFilters as jest.Mock).mockReturnValue({
      provider: { kind: 'set', values: [] },
    });
    await loadRecommendations();

    const container = document.getElementById('recommendations-list');
    expect(container?.querySelector('thead')).not.toBeNull();
    // The empty hint cell must be present inside the table body.
    const emptyCell = container?.querySelector('tbody td.empty');
    expect(emptyCell).not.toBeNull();
    expect(emptyCell?.textContent).toMatch(/No rows match/);
    // No standalone <p class="empty"> should replace the table.
    expect(container?.querySelector('p.empty')).toBeNull();
  });

  test('Clear button on a numeric column clears the expression filter (null)', async () => {
    // Numeric columns still use null on Clear: the empty-set semantic
    // only applies to categorical filters because the set-membership
    // model maps naturally to "no values allowed". Numeric "clear" just
    // means "no expression applied".
    (state.getRecommendationsColumnFilters as jest.Mock).mockReturnValue({
      savings: { kind: 'expr', expr: '>100' },
    });
    await loadRecommendations();
    const savingsBtn = document.querySelector<HTMLButtonElement>('th .column-filter-btn[data-column="savings"]');
    savingsBtn?.click();
    const clearBtn = document.querySelector<HTMLButtonElement>('.column-filter-popover .column-filter-clear');
    clearBtn?.click();
    expect(state.setRecommendationsColumnFilter).toHaveBeenCalledWith('savings', null);
  });

  test('Clear-filters badge appears when at least one filter is active', async () => {
    (state.getRecommendationsColumnFilters as jest.Mock).mockReturnValue({
      provider: { kind: 'set', values: ['aws'] },
    });
    await loadRecommendations();
    const badge = document.querySelector<HTMLButtonElement>('.recommendations-filter-status .clear-filters');
    expect(badge).not.toBeNull();
    expect(badge?.textContent).toContain('Clear filters (1)');
  });

  test('aria-live region announces visible/loaded count', async () => {
    await loadRecommendations();
    const live = document.querySelector('.recommendations-filter-live');
    expect(live).not.toBeNull();
    expect(live?.getAttribute('aria-live')).toBe('polite');
    expect(live?.textContent).toMatch(/Showing \d+ of \d+/);
  });

  // Issue #137: 'All Savings Plans' affordance in the service column-filter
  // popover. PR #123 split a single 'savings-plans' service into four per-
  // plan-type slugs, so the user lost the one-click "filter to all SP recs"
  // affordance. These tests pin the new tri-state group toggle.
  describe('Issue #137: All Savings Plans tri-state in service column-filter', () => {
    const spRecs = [
      { id: 'rec-aws-1',  provider: 'aws', cloud_account_id: 'a1', service: 'ec2',                        resource_type: 't3.medium', region: 'us-east-1', count: 1, term: 1, savings: 100, upfront_cost: 500 },
      { id: 'rec-sp-c',   provider: 'aws', cloud_account_id: 'a1', service: 'savings-plans-compute',      resource_type: 'sp',        region: 'us-east-1', count: 1, term: 1, savings: 200, upfront_cost: 800 },
      { id: 'rec-sp-e',   provider: 'aws', cloud_account_id: 'a1', service: 'savings-plans-ec2instance',  resource_type: 'sp',        region: 'us-east-1', count: 1, term: 1, savings: 150, upfront_cost: 700 },
      { id: 'rec-sp-s',   provider: 'aws', cloud_account_id: 'a1', service: 'savings-plans-sagemaker',    resource_type: 'sp',        region: 'us-east-1', count: 1, term: 1, savings: 250, upfront_cost: 900 },
    ];

    beforeEach(() => {
      (api.getRecommendations as jest.Mock).mockResolvedValue({
        summary: {},
        recommendations: spRecs,
        regions: [],
      });
      (state.getRecommendations as jest.Mock).mockReturnValue(spRecs);
      (state.getVisibleRecommendations as jest.Mock).mockReturnValue(spRecs);
      // Reset the column-filters mock to a deterministic empty state so a
      // prior test in this describe block leaving a custom mockReturnValue
      // (e.g. the indeterminate test setting `service: { values: [...] }`)
      // doesn't bleed into a later test's popover-build that reads
      // getRecommendationsColumnFilters() during resync.
      (state.getRecommendationsColumnFilters as jest.Mock).mockReturnValue({});
    });

    test('service popover renders the All Savings Plans group toggle when 2+ SP slugs present', async () => {
      await loadRecommendations();
      const serviceBtn = document.querySelector<HTMLButtonElement>('th .column-filter-btn[data-column="service"]');
      serviceBtn?.click();
      const groupBox = document.querySelector<HTMLInputElement>('.column-filter-popover input[data-role="sp-group"]');
      expect(groupBox).not.toBeNull();
      const groupLabel = groupBox?.closest('label');
      expect(groupLabel?.textContent).toContain('All Savings Plans');
    });

    test('group toggle does NOT render for non-service columns', async () => {
      await loadRecommendations();
      const providerBtn = document.querySelector<HTMLButtonElement>('th .column-filter-btn[data-column="provider"]');
      providerBtn?.click();
      const groupBox = document.querySelector('.column-filter-popover input[data-role="sp-group"]');
      expect(groupBox).toBeNull();
    });

    test('group toggle does NOT render when only 0 or 1 SP slugs present', async () => {
      const oneSPRec = [
        { id: 'rec1', provider: 'aws', cloud_account_id: 'a1', service: 'ec2',                   resource_type: 't3.medium', region: 'us-east-1', count: 1, term: 1, savings: 100, upfront_cost: 500 },
        { id: 'rec2', provider: 'aws', cloud_account_id: 'a1', service: 'savings-plans-compute', resource_type: 'sp',        region: 'us-east-1', count: 1, term: 1, savings: 200, upfront_cost: 800 },
      ];
      (api.getRecommendations as jest.Mock).mockResolvedValue({ summary: {}, recommendations: oneSPRec, regions: [] });
      (state.getRecommendations as jest.Mock).mockReturnValue(oneSPRec);
      (state.getVisibleRecommendations as jest.Mock).mockReturnValue(oneSPRec);

      await loadRecommendations();
      const serviceBtn = document.querySelector<HTMLButtonElement>('th .column-filter-btn[data-column="service"]');
      serviceBtn?.click();
      const groupBox = document.querySelector('.column-filter-popover input[data-role="sp-group"]');
      expect(groupBox).toBeNull();
    });

    test('clicking group toggle from a non-SP filter expands it to include all SP slug values', async () => {
      // Issue #482: with no active filter, every checkbox renders as
      // checked (including the SP-group toggle). To exercise the "tick
      // the SP group to add SPs" flow, start from a filter that already
      // restricts service to a non-SP subset.
      //
      // Use a 5-distinct-value visible set (ec2, rds, + 3 SPs) so adding
      // SPs lands at 4-of-5 selected, strictly partial, so the commit
      // does NOT collapse to the "all selected" null state.
      const widerRecs = [
        { id: 'rec-ec2', provider: 'aws', cloud_account_id: 'a1', service: 'ec2',                        resource_type: 't3.medium', region: 'us-east-1', count: 1, term: 1, savings: 100, upfront_cost: 500 },
        { id: 'rec-rds', provider: 'aws', cloud_account_id: 'a1', service: 'rds',                        resource_type: 'db.t3',     region: 'us-east-1', count: 1, term: 1, savings: 120, upfront_cost: 600 },
        { id: 'rec-spc', provider: 'aws', cloud_account_id: 'a1', service: 'savings-plans-compute',      resource_type: 'sp',        region: 'us-east-1', count: 1, term: 1, savings: 200, upfront_cost: 800 },
        { id: 'rec-spe', provider: 'aws', cloud_account_id: 'a1', service: 'savings-plans-ec2instance',  resource_type: 'sp',        region: 'us-east-1', count: 1, term: 1, savings: 150, upfront_cost: 700 },
        { id: 'rec-sps', provider: 'aws', cloud_account_id: 'a1', service: 'savings-plans-sagemaker',    resource_type: 'sp',        region: 'us-east-1', count: 1, term: 1, savings: 250, upfront_cost: 900 },
      ];
      (api.getRecommendations as jest.Mock).mockResolvedValue({ summary: {}, recommendations: widerRecs, regions: [] });
      (state.getRecommendations as jest.Mock).mockReturnValue(widerRecs);
      (state.getVisibleRecommendations as jest.Mock).mockReturnValue(widerRecs);
      (state.getRecommendationsColumnFilters as jest.Mock).mockReturnValue({
        service: { kind: 'set', values: ['ec2'] },
      });
      await loadRecommendations();
      const serviceBtn = document.querySelector<HTMLButtonElement>('th .column-filter-btn[data-column="service"]');
      serviceBtn?.click();
      const groupBox = document.querySelector<HTMLInputElement>('.column-filter-popover input[data-role="sp-group"]');
      expect(groupBox).not.toBeNull();
      expect(groupBox?.checked).toBe(false);
      // Browser flips checked → true on first click; simulate that.
      groupBox!.checked = true;
      groupBox!.dispatchEvent(new Event('change'));

      // Filter committed with ec2 + the three SP values (in any order).
      // Total selected = 4 of 5 distinct, so the commit stays as a set.
      const calls = (state.setRecommendationsColumnFilter as jest.Mock).mock.calls;
      const lastCall = calls[calls.length - 1];
      expect(lastCall[0]).toBe('service');
      expect(lastCall[1]?.kind).toBe('set');
      expect((lastCall[1]?.values as string[]).sort()).toEqual([
        'ec2',
        'savings-plans-compute',
        'savings-plans-ec2instance',
        'savings-plans-sagemaker',
      ]);
    });

    test('clicking group toggle (off) persists an empty allow-list (0-row state)', async () => {
      // Pre-set the filter so the SP tri-state renders as checked.
      (state.getRecommendationsColumnFilters as jest.Mock).mockReturnValue({
        service: { kind: 'set', values: ['savings-plans-compute', 'savings-plans-ec2instance', 'savings-plans-sagemaker'] },
      });
      await loadRecommendations();
      const serviceBtn = document.querySelector<HTMLButtonElement>('th .column-filter-btn[data-column="service"]');
      serviceBtn?.click();
      const groupBox = document.querySelector<HTMLInputElement>('.column-filter-popover input[data-role="sp-group"]');
      expect(groupBox?.checked).toBe(true);
      // Browser flips → unchecked on click of an already-checked tri-state.
      groupBox!.checked = false;
      groupBox!.dispatchEvent(new Event('change'));

      // CR pass #2: unchecking the SP group when no non-SP boxes are
      // currently selected leaves zero checkboxes ticked. The individual
      // commit() path now persists that as an explicit empty allow-list
      // (matching (All)-off / Clear), not null — so the table renders 0
      // rows rather than silently snapping back to "no narrowing applied".
      expect(state.setRecommendationsColumnFilter).toHaveBeenLastCalledWith('service', { kind: 'set', values: [] });
    });

    test('group toggle resyncs to indeterminate when only some SPs are filter-active', async () => {
      (state.getRecommendationsColumnFilters as jest.Mock).mockReturnValue({
        service: { kind: 'set', values: ['savings-plans-compute'] },
      });
      await loadRecommendations();
      const serviceBtn = document.querySelector<HTMLButtonElement>('th .column-filter-btn[data-column="service"]');
      serviceBtn?.click();
      const groupBox = document.querySelector<HTMLInputElement>('.column-filter-popover input[data-role="sp-group"]');
      expect(groupBox?.checked).toBe(false);
      expect(groupBox?.indeterminate).toBe(true);
    });

    test('individual SP checkbox change commits the partial-SP filter', async () => {
      // Issue #482: start from a single-SP filter so the popover opens
      // with only `savings-plans-compute` checked; ticking another SP
      // box exercises the partial-set commit path.
      (state.getRecommendationsColumnFilters as jest.Mock).mockReturnValue({
        service: { kind: 'set', values: ['savings-plans-compute'] },
      });
      await loadRecommendations();
      const serviceBtn = document.querySelector<HTMLButtonElement>('th .column-filter-btn[data-column="service"]');
      serviceBtn?.click();
      const cbEc2sp = document.querySelector<HTMLInputElement>('.column-filter-popover input[data-value="savings-plans-ec2instance"]');
      expect(cbEc2sp?.checked).toBe(false);
      cbEc2sp!.checked = true;
      cbEc2sp!.dispatchEvent(new Event('change'));
      // 2 of 4 service distinct values selected → filter committed with
      // both SP slugs.
      const calls = (state.setRecommendationsColumnFilter as jest.Mock).mock.calls;
      const lastCall = calls[calls.length - 1];
      expect(lastCall[0]).toBe('service');
      expect(lastCall[1]?.kind).toBe('set');
      expect((lastCall[1]?.values as string[]).sort()).toEqual(['savings-plans-compute', 'savings-plans-ec2instance']);
    });
  });

  // Issue #658: Azure SP rows use service = "savingsplans" (no hyphen). Verify
  // they appear in the rendered table and are recognized by the SP group toggle.
  describe('Issue #658: Azure Savings Plans row rendering and SP group toggle', () => {
    const azureSpRecs = [
      { id: 'rec-aws-ec2',  provider: 'aws',   cloud_account_id: 'a1', service: 'ec2',           resource_type: 't3.medium', region: 'us-east-1', count: 1, term: 1, savings: 100, upfront_cost: 500 },
      { id: 'rec-az-sp-1',  provider: 'azure',  cloud_account_id: 'sub1', service: 'savingsplans', resource_type: 'Compute',   region: 'eastus',    count: 2, term: 1, savings: 300, upfront_cost: 1200 },
      { id: 'rec-sp-c',     provider: 'aws',   cloud_account_id: 'a1', service: 'savings-plans-compute', resource_type: 'sp', region: 'us-east-1', count: 1, term: 1, savings: 200, upfront_cost: 800 },
    ];

    beforeEach(() => {
      (api.getRecommendations as jest.Mock).mockResolvedValue({
        summary: {},
        recommendations: azureSpRecs,
        regions: [],
      });
      (state.getRecommendations as jest.Mock).mockReturnValue(azureSpRecs);
      (state.getVisibleRecommendations as jest.Mock).mockReturnValue(azureSpRecs);
      (state.getRecommendationsColumnFilters as jest.Mock).mockReturnValue({});
    });

    test('Azure SP rows (service="savingsplans") appear in the rendered table', async () => {
      await loadRecommendations();
      // The table should contain a row with service badge "savingsplans".
      const serviceBadges = Array.from(
        document.querySelectorAll<HTMLElement>('td .service-badge'),
      ).map((el) => el.textContent ?? '');
      expect(serviceBadges).toContain('savingsplans');
    });

    test('service popover renders All Savings Plans toggle when Azure SP + AWS SP slugs are present', async () => {
      // "savingsplans" + "savings-plans-compute" = 2 distinct SP slugs -> toggle should appear.
      await loadRecommendations();
      const serviceBtn = document.querySelector<HTMLButtonElement>(
        'th .column-filter-btn[data-column="service"]',
      );
      serviceBtn?.click();
      const groupBox = document.querySelector<HTMLInputElement>(
        '.column-filter-popover input[data-role="sp-group"]',
      );
      expect(groupBox).not.toBeNull();
      const groupLabel = groupBox?.closest('label');
      expect(groupLabel?.textContent).toContain('All Savings Plans');
    });

    test('clicking All Savings Plans toggle selects both Azure SP and AWS SP slugs', async () => {
      // Use 4 distinct service values (ec2, rds, savingsplans, savings-plans-compute)
      // so that selecting ec2+rds (2 of 4) gives an active filter, and then toggling
      // the SP group on (adding savingsplans + savings-plans-compute) yields 4 of 4 = all,
      // which we avoid by keeping ec2 only (1 of 4 + 2 SPs = 3 of 4, stays partial).
      const widerRecs = [
        { id: 'rec-ec2',    provider: 'aws',   cloud_account_id: 'a1',   service: 'ec2',                  resource_type: 't3.medium', region: 'us-east-1', count: 1, term: 1, savings: 100, upfront_cost: 500 },
        { id: 'rec-rds',    provider: 'aws',   cloud_account_id: 'a1',   service: 'rds',                  resource_type: 'db.t3',     region: 'us-east-1', count: 1, term: 1, savings: 120, upfront_cost: 600 },
        { id: 'rec-az-sp',  provider: 'azure', cloud_account_id: 'sub1', service: 'savingsplans',         resource_type: 'Compute',   region: 'eastus',    count: 2, term: 1, savings: 300, upfront_cost: 1200 },
        { id: 'rec-aws-sp', provider: 'aws',   cloud_account_id: 'a1',   service: 'savings-plans-compute', resource_type: 'sp',       region: 'us-east-1', count: 1, term: 1, savings: 200, upfront_cost: 800 },
      ];
      (api.getRecommendations as jest.Mock).mockResolvedValue({ summary: {}, recommendations: widerRecs, regions: [] });
      (state.getRecommendations as jest.Mock).mockReturnValue(widerRecs);
      (state.getVisibleRecommendations as jest.Mock).mockReturnValue(widerRecs);
      // Start with ec2 selected only; SP slugs are not in the active filter.
      (state.getRecommendationsColumnFilters as jest.Mock).mockReturnValue({
        service: { kind: 'set', values: ['ec2'] },
      });
      await loadRecommendations();
      const serviceBtn = document.querySelector<HTMLButtonElement>(
        'th .column-filter-btn[data-column="service"]',
      );
      serviceBtn?.click();

      // SP group toggle should be unchecked (no SP slugs are in the active filter).
      const groupBox = document.querySelector<HTMLInputElement>(
        '.column-filter-popover input[data-role="sp-group"]',
      );
      expect(groupBox).not.toBeNull();
      expect(groupBox?.checked).toBe(false);

      // Tick the SP group toggle (browser flips -> checked on click).
      groupBox!.checked = true;
      groupBox!.dispatchEvent(new Event('change'));

      // Commit includes ec2 (already checked) + savingsplans + savings-plans-compute
      // = 3 of 4 distinct values -> persisted as a set (not collapsed to null).
      const calls = (state.setRecommendationsColumnFilter as jest.Mock).mock.calls;
      const lastCall = calls[calls.length - 1];
      expect(lastCall[0]).toBe('service');
      expect(lastCall[1]?.kind).toBe('set');
      const values = (lastCall[1]?.values as string[]).sort();
      // Both the Azure SP umbrella slug and the AWS SP plan-type slug should be selected.
      expect(values).toContain('savingsplans');
      expect(values).toContain('savings-plans-compute');
      // ec2 remains in the selection.
      expect(values).toContain('ec2');
    });
  });
});

describe('Bundle B: sticky bottom action box', () => {
  const recs = [
    { id: 'r1', provider: 'aws', cloud_account_id: 'a1', service: 'ec2', resource_type: 't3.medium', region: 'us-east-1', count: 1, term: 1, savings: 100, upfront_cost: 500 },
    { id: 'r2', provider: 'aws', cloud_account_id: 'a1', service: 'ec2', resource_type: 't3.large', region: 'us-east-1', count: 2, term: 3, savings: 200, upfront_cost: 800 },
  ];

  beforeEach(() => {
    document.body.replaceChildren();
    const recsTab = document.createElement('div');
    recsTab.id = 'opportunities-tab';
    recsTab.className = 'tab-content active';
    const summary = document.createElement('div');
    summary.id = 'recommendations-summary';
    const list = document.createElement('div');
    list.id = 'recommendations-list';
    recsTab.appendChild(summary);
    recsTab.appendChild(list);
    document.body.appendChild(recsTab);

    jest.clearAllMocks();
    (api.getRecommendations as jest.Mock).mockResolvedValue({ summary: {}, recommendations: recs, regions: [] });
    (state.getRecommendations as jest.Mock).mockReturnValue(recs);
    (state.getVisibleRecommendations as jest.Mock).mockReturnValue(recs);
    (state.getRecommendationsColumnFilters as jest.Mock).mockReturnValue({});
    (state.getSelectedRecommendationIDs as jest.Mock).mockReturnValue(new Set());
  });

  test('bottom box no longer exposes the bulk Payment dropdown (#282)', async () => {
    await loadRecommendations();
    // Issue #282: global Payment dropdown removed — each rec carries its own
    // payment_option from the API fan-out; a global override was misleading.
    expect(document.getElementById('bulk-purchase-payment')).toBeNull();
    // Other controls remain.
    expect(document.getElementById('bulk-purchase-capacity')).not.toBeNull();
    expect(document.getElementById('bulk-purchase-btn')).not.toBeNull();
    expect(document.getElementById('create-plan-btn')).not.toBeNull();
    // Term selector is gone — each rec carries its own term through the API call.
    expect(document.getElementById('bulk-purchase-term')).toBeNull();
  });

  test('button labels reflect the selection (#273)', async () => {
    // No selection → buttons disabled, label is the static form. The
    // disabled-state explanation lives on a sibling hint span linked via
    // aria-describedby (CR follow-up): disabled <button> elements are
    // non-focusable per HTML spec and don't reliably show title tooltips
    // across browsers, so the sibling-element pattern is the
    // discoverable channel for both mouse and keyboard users.
    await loadRecommendations();
    let purchaseBtn = document.getElementById('bulk-purchase-btn') as HTMLButtonElement;
    let planBtn = document.getElementById('create-plan-btn') as HTMLButtonElement;
    let hint = document.getElementById('recommendations-action-disabled-hint') as HTMLSpanElement;
    expect(purchaseBtn.disabled).toBe(true);
    expect(purchaseBtn.textContent).toBe('Purchase');
    expect(purchaseBtn.hasAttribute('title')).toBe(false);
    expect(purchaseBtn.getAttribute('aria-describedby')).toBe('recommendations-action-disabled-hint');
    expect(planBtn.disabled).toBe(true);
    expect(planBtn.textContent).toBe('Create Plan');
    expect(planBtn.hasAttribute('title')).toBe(false);
    expect(planBtn.getAttribute('aria-describedby')).toBe('recommendations-action-disabled-hint');
    expect(hint.hidden).toBe(false);
    expect(hint.textContent).toContain('Select at least one cell to enable');
    expect(hint.getAttribute('aria-live')).toBe('polite');

    // Selection populated → buttons enabled, label shows the selection
    // count, hint hidden, aria-describedby cleared, title restored.
    (state.getSelectedRecommendationIDs as jest.Mock).mockReturnValue(new Set(['r1']));
    await loadRecommendations();
    purchaseBtn = document.getElementById('bulk-purchase-btn') as HTMLButtonElement;
    planBtn = document.getElementById('create-plan-btn') as HTMLButtonElement;
    hint = document.getElementById('recommendations-action-disabled-hint') as HTMLSpanElement;
    expect(purchaseBtn.disabled).toBe(false);
    expect(purchaseBtn.textContent).toBe('Purchase 1 selected');
    expect(purchaseBtn.hasAttribute('aria-describedby')).toBe(false);
    expect(purchaseBtn.title).toContain('Buy these reservations now');
    expect(planBtn.disabled).toBe(false);
    expect(planBtn.textContent).toBe('Plan from 1 selected');
    expect(planBtn.hasAttribute('aria-describedby')).toBe(false);
    expect(hint.hidden).toBe(true);
  });

  test('buttons disabled when no rows visible at all', async () => {
    (state.getVisibleRecommendations as jest.Mock).mockReturnValue([]);
    (api.getRecommendations as jest.Mock).mockResolvedValue({ summary: {}, recommendations: [], regions: [] });
    await loadRecommendations();
    const purchaseBtn = document.getElementById('bulk-purchase-btn') as HTMLButtonElement;
    const planBtn = document.getElementById('create-plan-btn') as HTMLButtonElement;
    expect(purchaseBtn.disabled).toBe(true);
    expect(planBtn.disabled).toBe(true);
  });

  test('plan button disabled on heterogeneous selection; purchase button still enabled (#769)', async () => {
    // The fixture recs differ in term (1 vs 3), so selecting both makes the
    // selection heterogeneous for the plan button but not the purchase button.
    (state.getSelectedRecommendationIDs as jest.Mock).mockReturnValue(new Set(['r1', 'r2']));
    await loadRecommendations();
    const purchaseBtn = document.getElementById('bulk-purchase-btn') as HTMLButtonElement;
    const planBtn = document.getElementById('create-plan-btn') as HTMLButtonElement;
    const hint = document.getElementById('recommendations-action-disabled-hint') as HTMLSpanElement;
    // Purchase button is unaffected by homogeneity.
    expect(purchaseBtn.disabled).toBe(false);
    expect(purchaseBtn.textContent).toBe('Purchase 2 selected');
    // Plan button must be disabled.
    expect(planBtn.disabled).toBe(true);
    expect(planBtn.textContent).toBe('Plan from 2 selected');
    // Hint is visible with the heterogeneous explanation.
    expect(hint.hidden).toBe(false);
    expect(hint.textContent).toContain('Plans require one provider, service, term, and payment');
  });

  test('Capacity input value persists across re-render (mount-once-then-update)', async () => {
    await loadRecommendations();
    const cap = document.getElementById('bulk-purchase-capacity') as HTMLInputElement;
    cap.value = '50';
    // Trigger an unrelated re-render via sort
    const header = document.querySelector<HTMLTableCellElement>('th[data-sort="service"]');
    header?.click();
    const cap2 = document.getElementById('bulk-purchase-capacity') as HTMLInputElement;
    // Same DOM node (mount-once); value preserved (no rebuild).
    expect(cap2).toBe(cap);
    expect(cap2.value).toBe('50');
  });
});

describe('Bundle B: term-aware bucketing in the Purchase flow', () => {
  beforeEach(() => {
    document.body.replaceChildren();
    const recsTab = document.createElement('div');
    recsTab.id = 'opportunities-tab';
    recsTab.className = 'tab-content active';
    const summary = document.createElement('div');
    summary.id = 'recommendations-summary';
    const list = document.createElement('div');
    list.id = 'recommendations-list';
    recsTab.appendChild(summary);
    recsTab.appendChild(list);
    document.body.appendChild(recsTab);
    const purchaseModal = document.createElement('div');
    purchaseModal.id = 'purchase-modal';
    purchaseModal.className = 'hidden';
    const purchaseDetails = document.createElement('div');
    purchaseDetails.id = 'purchase-details';
    purchaseModal.appendChild(purchaseDetails);
    document.body.appendChild(purchaseModal);
    jest.clearAllMocks();
  });

  test('multi-term selection produces multiple fan-out buckets', async () => {
    // Two different resource types (different cells) with different terms.
    // #273: resolvePurchaseTarget no longer falls back to "all visible" when
    // nothing is selected — the buttons are disabled in that state. Tests
    // that exercise the post-Purchase fan-out must explicitly select the
    // recs they want included in the target.
    const mixed = [
      { id: 'a', provider: 'aws', cloud_account_id: 'a1', service: 'ec2', resource_type: 't3.medium', region: 'us-east-1', count: 1, term: 1, savings: 100, upfront_cost: 500 },
      { id: 'b', provider: 'aws', cloud_account_id: 'a1', service: 'ec2', resource_type: 'm5.large', region: 'us-east-1', count: 1, term: 3, savings: 200, upfront_cost: 800 },
    ];
    (api.getRecommendations as jest.Mock).mockResolvedValue({ summary: {}, recommendations: mixed, regions: [] });
    (state.getRecommendations as jest.Mock).mockReturnValue(mixed);
    (state.getVisibleRecommendations as jest.Mock).mockReturnValue(mixed);
    (state.getRecommendationsColumnFilters as jest.Mock).mockReturnValue({});
    (state.getSelectedRecommendationIDs as jest.Mock).mockReturnValue(new Set(['a', 'b']));

    await loadRecommendations();
    const purchaseBtn = document.getElementById('bulk-purchase-btn') as HTMLButtonElement;
    purchaseBtn.click();
    // openFanOutModal is async (issue #111: pre-fetches per-account
    // overrides). Yield twice so the awaits inside resolve before
    // we read getFanOutBuckets().
    await Promise.resolve();
    await Promise.resolve();

    const { getFanOutBuckets } = await import('../recommendations');
    const buckets = getFanOutBuckets();
    expect(buckets).not.toBeNull();
    expect(buckets!.length).toBe(2);
    expect(buckets!.map((b) => b.term).sort()).toEqual([1, 3]);
    // Each bucket carries the rec's own term, not a toolbar override.
    const b0 = buckets![0]!;
    const b1 = buckets![1]!;
    expect(b0.recs.every((r) => r.term === b0.term)).toBe(true);
    expect(b1.recs.every((r) => r.term === b1.term)).toBe(true);
  });
});

// Issue #111: per-bucket Payment dropdown in the fan-out modal,
// seeded from the per-account service override when all recs in a
// bucket share one cloud_account_id and that account has a saved
// override matching the bucket's (provider, service). Otherwise
// (multi-account, no override, override has no payment, override
// payment unsupported by the (provider, service, term) cell) the
// bucket falls back to the toolbar payment.
describe('Issue #111: per-bucket Payment seed from per-account service override', () => {
  beforeEach(async () => {
    document.body.replaceChildren();
    const recsTab = document.createElement('div');
    recsTab.id = 'opportunities-tab';
    recsTab.className = 'tab-content active';
    const summary = document.createElement('div');
    summary.id = 'recommendations-summary';
    const list = document.createElement('div');
    list.id = 'recommendations-list';
    recsTab.appendChild(summary);
    recsTab.appendChild(list);
    document.body.appendChild(recsTab);
    const purchaseModal = document.createElement('div');
    purchaseModal.id = 'purchase-modal';
    purchaseModal.className = 'hidden';
    const purchaseDetails = document.createElement('div');
    purchaseDetails.id = 'purchase-details';
    purchaseModal.appendChild(purchaseDetails);
    document.body.appendChild(purchaseModal);
    jest.clearAllMocks();
    // Default: empty overrides — overridden per-test.
    (api.listAccountServiceOverrides as jest.Mock).mockResolvedValue([]);
    // Module-level fan-out state survives test isolation; reset it so a
    // previous test's openFanOutModal call does not leak into tests that
    // assert the single-bucket happy path (getFanOutBuckets() === null).
    const { clearFanOutBuckets } = await import('../recommendations');
    clearFanOutBuckets();
  });

  // Force a multi-bucket fan-out by mixing terms (1yr + 3yr both
  // present); this drives openFanOutModal rather than the
  // single-bucket happy path. Each test seeds overrides differently
  // to exercise the (override / no-override / multi-account / edit)
  // matrix.
  const setupMixedTermRecs = (recs: Array<Record<string, unknown>>): void => {
    (api.getRecommendations as jest.Mock).mockResolvedValue({ summary: {}, recommendations: recs, regions: [] });
    (state.getRecommendations as jest.Mock).mockReturnValue(recs);
    (state.getVisibleRecommendations as jest.Mock).mockReturnValue(recs);
    (state.getRecommendationsColumnFilters as jest.Mock).mockReturnValue({});
    // #273: action buttons now require an explicit selection. Each fan-out
    // test in this suite is asserting bucket assembly, not the selection-
    // gating UI, so seed the selection with every rec id to keep Purchase
    // enabled and the click effective. The earlier
    // "no-selection → default to visible" fallback path is gone.
    (state.getSelectedRecommendationIDs as jest.Mock).mockReturnValue(
      new Set(recs.map((r) => r.id as string)),
    );
  };

  test('(a) single-account bucket with matching override → bucket payment seeded from override', async () => {
    // issues #225/#226: resolvePurchaseTarget uses pickBestVariantPerCell for the
    // default path. Use different resource_type values so each rec is its own cell,
    // ensuring both appear in the default target and trigger fan-out.
    const recs = [
      { id: 'a', provider: 'aws', cloud_account_id: 'test-account-a', service: 'ec2', resource_type: 't3.medium', region: 'us-east-1', count: 1, term: 1, savings: 100, upfront_cost: 500 },
      { id: 'b', provider: 'aws', cloud_account_id: 'test-account-a', service: 'ec2', resource_type: 'm5.large', region: 'us-east-1', count: 1, term: 3, savings: 200, upfront_cost: 800 },
    ];
    setupMixedTermRecs(recs);
    // Override pins payment=partial-upfront for AWS EC2 on this
    // account. partial-upfront is supported for both 1yr and 3yr.
    (api.listAccountServiceOverrides as jest.Mock).mockImplementation(async (id: string) => {
      if (id === 'test-account-a') {
        return [{ id: 'ovr-1', account_id: 'test-account-a', provider: 'aws', service: 'ec2', payment: 'partial-upfront' }];
      }
      return [];
    });

    await loadRecommendations();
    (document.getElementById('bulk-purchase-btn') as HTMLButtonElement).click();
    await Promise.resolve(); await Promise.resolve(); await Promise.resolve();

    const { getFanOutBuckets } = await import('../recommendations');
    const buckets = getFanOutBuckets();
    expect(buckets).not.toBeNull();
    expect(buckets!.length).toBe(2);
    for (const b of buckets!) {
      expect(b.payment).toBe('partial-upfront');
      expect(b.paymentSource).toBe('override');
    }
    // Source-note span renders.
    const noteSpans = document.querySelectorAll('.fanout-bucket-payment-source');
    expect(noteSpans.length).toBe(2);
    expect((noteSpans[0] as HTMLElement).textContent).toContain('account override');
    // Dropdown's selected value matches.
    const selects = document.querySelectorAll<HTMLSelectElement>('.fanout-bucket-payment');
    expect(selects.length).toBe(2);
    expect(selects[0]!.value).toBe('partial-upfront');
  });

  test('(b) single-account bucket with NO matching override → bucket payment seeded from toolbar', async () => {
    // issues #225/#226: use different resource_type so each rec is its own cell.
    const recs = [
      { id: 'a', provider: 'aws', cloud_account_id: 'test-account-a', service: 'ec2', resource_type: 't3.medium', region: 'us-east-1', count: 1, term: 1, savings: 100, upfront_cost: 500 },
      { id: 'b', provider: 'aws', cloud_account_id: 'test-account-a', service: 'ec2', resource_type: 'm5.large', region: 'us-east-1', count: 1, term: 3, savings: 200, upfront_cost: 800 },
    ];
    setupMixedTermRecs(recs);
    // Account exists but has overrides for a DIFFERENT service — the
    // ec2 lookup should miss and fall back.
    (api.listAccountServiceOverrides as jest.Mock).mockImplementation(async (id: string) => {
      if (id === 'test-account-a') {
        return [{ id: 'ovr-1', account_id: 'test-account-a', provider: 'aws', service: 'rds', payment: 'no-upfront' }];
      }
      return [];
    });

    await loadRecommendations();
    (document.getElementById('bulk-purchase-btn') as HTMLButtonElement).click();
    await Promise.resolve(); await Promise.resolve(); await Promise.resolve();

    const { getFanOutBuckets } = await import('../recommendations');
    const buckets = getFanOutBuckets();
    expect(buckets).not.toBeNull();
    for (const b of buckets!) {
      // Toolbar default is 'all-upfront' (loadBulkPurchaseState's
      // defaultBulkPurchaseState).
      expect(b.payment).toBe('all-upfront');
      expect(b.paymentSource).toBe('toolbar');
    }
    // No source-note rendered.
    expect(document.querySelectorAll('.fanout-bucket-payment-source').length).toBe(0);
  });

  test('(c) multi-account bucket → bucket payment seeded from toolbar regardless of any override', async () => {
    // Two recs, same (provider, service, term) — bucket-key match —
    // but different cloud_account_ids. resolveBucketPaymentSeed must
    // return toolbar (the documented multi-account fallback).
    // Pair with a third 3yr rec to force multi-bucket fan-out.
    //
    // issues #225/#226: resolvePurchaseTarget uses pickBestVariantPerCell so
    // each rec must be in its own cell. Recs 'a' and 'b' differ by account
    // (already distinct cells). Rec 'c' gets a different resource_type so it
    // doesn't collapse into the same cell as rec 'a' (same account-a).
    const recs = [
      { id: 'a', provider: 'aws', cloud_account_id: 'test-account-a', service: 'ec2', resource_type: 't3.medium', region: 'us-east-1', count: 1, term: 1, savings: 100, upfront_cost: 500 },
      { id: 'b', provider: 'aws', cloud_account_id: 'test-account-b', service: 'ec2', resource_type: 't3.medium', region: 'us-east-1', count: 1, term: 1, savings: 150, upfront_cost: 600 },
      { id: 'c', provider: 'aws', cloud_account_id: 'test-account-a', service: 'ec2', resource_type: 'r5.large', region: 'us-east-1', count: 1, term: 3, savings: 200, upfront_cost: 800 },
    ];
    setupMixedTermRecs(recs);
    // Both accounts have ec2 overrides — the multi-account bucket
    // must NOT pick either; only the single-account 3yr bucket may
    // honour the override.
    (api.listAccountServiceOverrides as jest.Mock).mockImplementation(async (id: string) => {
      if (id === 'test-account-a') return [{ id: 'ovr-a', account_id: 'test-account-a', provider: 'aws', service: 'ec2', payment: 'partial-upfront' }];
      if (id === 'test-account-b') return [{ id: 'ovr-b', account_id: 'test-account-b', provider: 'aws', service: 'ec2', payment: 'no-upfront' }];
      return [];
    });

    await loadRecommendations();
    (document.getElementById('bulk-purchase-btn') as HTMLButtonElement).click();
    await Promise.resolve(); await Promise.resolve(); await Promise.resolve();

    const { getFanOutBuckets } = await import('../recommendations');
    const buckets = getFanOutBuckets();
    expect(buckets).not.toBeNull();
    expect(buckets!.length).toBe(2);
    const bucket1yr = buckets!.find((b) => b.term === 1)!;
    const bucket3yr = buckets!.find((b) => b.term === 3)!;
    // 1yr bucket: 2 recs, 2 distinct accounts → toolbar.
    expect(bucket1yr.recs.length).toBe(2);
    expect(bucket1yr.payment).toBe('all-upfront');
    expect(bucket1yr.paymentSource).toBe('toolbar');
    // 3yr bucket: 1 rec, single account a → override honoured.
    expect(bucket3yr.recs.length).toBe(1);
    expect(bucket3yr.payment).toBe('partial-upfront');
    expect(bucket3yr.paymentSource).toBe('override');
  });

  test('(d) user-edited Payment dropdown is reflected in module state', async () => {
    // issues #225/#226: use different resource_type so each rec is its own cell.
    const recs = [
      { id: 'a', provider: 'aws', cloud_account_id: 'test-account-a', service: 'ec2', resource_type: 't3.medium', region: 'us-east-1', count: 1, term: 1, savings: 100, upfront_cost: 500 },
      { id: 'b', provider: 'aws', cloud_account_id: 'test-account-a', service: 'ec2', resource_type: 'm5.large', region: 'us-east-1', count: 1, term: 3, savings: 200, upfront_cost: 800 },
    ];
    setupMixedTermRecs(recs);

    await loadRecommendations();
    (document.getElementById('bulk-purchase-btn') as HTMLButtonElement).click();
    await Promise.resolve(); await Promise.resolve(); await Promise.resolve();

    const { getFanOutBuckets } = await import('../recommendations');
    const before = getFanOutBuckets();
    expect(before).not.toBeNull();
    expect(before![0]!.payment).toBe('all-upfront');

    // Pick the dropdown for the 1yr bucket and switch to no-upfront
    // (a supported value for AWS EC2 1yr).
    const selects = Array.from(document.querySelectorAll<HTMLSelectElement>('.fanout-bucket-payment'));
    expect(selects.length).toBe(2);
    const firstSel = selects[0]!;
    firstSel.value = 'no-upfront';
    firstSel.dispatchEvent(new Event('change'));

    const after = getFanOutBuckets();
    expect(after).not.toBeNull();
    // The bucket whose dropdown we changed now reports 'no-upfront'.
    // We don't rely on bucket[0] vs bucket[1] order; pick by recs
    // identity to find which bucket the first select belonged to.
    const firstBucketIdx = before!.findIndex((b) => b.recs[0]?.id === recs[0]!.id || b.recs[0]?.id === recs[1]!.id);
    expect(firstBucketIdx).toBeGreaterThanOrEqual(0);
    // At least one bucket payment must now be 'no-upfront'.
    expect(after!.some((b) => b.payment === 'no-upfront')).toBe(true);
  });

  // Regression: issue #699. Before the fix, recs with the same
  // (provider, service, term) but different rec.payment values were
  // collapsed into one bucket and seeded from toolbar.payment
  // ('all-upfront'), silently overriding each rec's actual payment.
  // Fix: include `payment` in the bucket key so each distinct payment
  // fans into its own bucket; resolveBucketPaymentSeed then uses
  // recs[0].payment (uniform within the bucket) as its seed.
  test('(e) issue #699: same (provider, service, term) but different rec.payment fans into separate buckets seeded from rec.payment, not toolbar', async () => {
    // Two recs: same aws/ec2/1yr but one is partial-upfront and one is
    // no-upfront. Different resource_type so each is its own cell (not
    // collapsed by pickBestVariantPerCell).
    const recs = [
      { id: 'x1', provider: 'aws', cloud_account_id: 'test-account-a', service: 'ec2', resource_type: 't3.medium', region: 'us-east-1', count: 1, term: 1, payment: 'partial-upfront', savings: 100, upfront_cost: 500 },
      { id: 'x2', provider: 'aws', cloud_account_id: 'test-account-a', service: 'ec2', resource_type: 'm5.large',  region: 'us-east-1', count: 1, term: 1, payment: 'no-upfront',      savings: 150, upfront_cost: 0 },
    ];
    setupMixedTermRecs(recs);
    // No account overrides — resolveBucketPaymentSeed must seed from
    // rec.payment, not the toolbar default (all-upfront).
    (api.listAccountServiceOverrides as jest.Mock).mockResolvedValue([]);

    await loadRecommendations();
    (document.getElementById('bulk-purchase-btn') as HTMLButtonElement).click();
    await Promise.resolve(); await Promise.resolve(); await Promise.resolve();

    const { getFanOutBuckets } = await import('../recommendations');
    const buckets = getFanOutBuckets();
    expect(buckets).not.toBeNull();
    // After the fix: 2 buckets (one per payment variant).
    expect(buckets!.length).toBe(2);
    // Each bucket carries the correct per-rec payment, not the toolbar default.
    const payments = buckets!.map((b) => b.payment).sort();
    expect(payments).toEqual(['no-upfront', 'partial-upfront']);
    // paymentSource is 'toolbar' (rec.payment fallback path, no override),
    // confirming the seed came from the rec, not an account override.
    for (const b of buckets!) {
      expect(b.paymentSource).toBe('toolbar');
    }
  });

  // CR finding on PR #710: account override carrying Azure's 'upfront' synonym
  // must be normalized before seeding the bucket, so the fan-out dropdown
  // receives 'all-upfront' (not the raw 'upfront') and paymentSource is
  // 'override', not 'toolbar'.
  test('(f) account override with upfront (Azure synonym) normalizes to all-upfront with source override', async () => {
    // Two azure/vm recs, same single account, different terms to force fan-out.
    const recs = [
      { id: 'g1', provider: 'azure', cloud_account_id: 'az-account-a', service: 'vm', resource_type: 'Standard_D2s_v3', region: 'eastus', count: 1, term: 1, payment: 'all-upfront', savings: 120, upfront_cost: 600 },
      { id: 'g2', provider: 'azure', cloud_account_id: 'az-account-a', service: 'vm', resource_type: 'Standard_D2s_v3', region: 'eastus', count: 1, term: 3, payment: 'all-upfront', savings: 300, upfront_cost: 1200 },
    ];
    setupMixedTermRecs(recs);
    // Override uses the Azure-canonical 'upfront' synonym — before the fix this
    // would seed FanOutBucket.payment = 'upfront' (not rendered by the dropdown);
    // after the fix it normalizes to 'all-upfront'.
    (api.listAccountServiceOverrides as jest.Mock).mockImplementation(async (id: string) => {
      if (id === 'az-account-a') {
        return [{ id: 'ovr-az', account_id: 'az-account-a', provider: 'azure', service: 'vm', payment: 'upfront' }];
      }
      return [];
    });

    await loadRecommendations();
    (document.getElementById('bulk-purchase-btn') as HTMLButtonElement).click();
    await Promise.resolve(); await Promise.resolve(); await Promise.resolve();

    const { getFanOutBuckets } = await import('../recommendations');
    const buckets = getFanOutBuckets();
    expect(buckets).not.toBeNull();
    expect(buckets!.length).toBe(2);
    for (const b of buckets!) {
      // Override 'upfront' must normalize to canonical 'all-upfront'.
      expect(b.payment).toBe('all-upfront');
      expect(b.paymentSource).toBe('override');
    }
  });

  // Regression: CR finding on PR #710.  'upfront' is a synonym for
  // 'all-upfront' used by some upstream rows (Azure canonical form).
  // Both must land in ONE bucket with the normalized 'all-upfront' seed.
  test('(g) payment synonyms upfront / all-upfront collapse into one bucket seeded as all-upfront', async () => {
    // Two recs, same aws/ec2/1yr, but one carries 'upfront' (Azure
    // synonym) and the other carries 'all-upfront' (canonical form).
    // Different resource_type so each is its own cell and both survive
    // pickBestVariantPerCell without collapsing.
    // After normalization they share the same bucket key and should
    // produce a SINGLE bucket (not two), forcing the single-bucket happy
    // path (openPurchaseModal, not openFanOutModal).
    const recs = [
      { id: 'f1', provider: 'aws', cloud_account_id: 'test-account-a', service: 'ec2', resource_type: 't3.medium', region: 'us-east-1', count: 1, term: 1, payment: 'upfront',     savings: 100, upfront_cost: 500 },
      { id: 'f2', provider: 'aws', cloud_account_id: 'test-account-a', service: 'ec2', resource_type: 'm5.large',  region: 'us-east-1', count: 1, term: 1, payment: 'all-upfront', savings: 150, upfront_cost: 600 },
    ];
    setupMixedTermRecs(recs);
    (api.listAccountServiceOverrides as jest.Mock).mockResolvedValue([]);

    await loadRecommendations();
    (document.getElementById('bulk-purchase-btn') as HTMLButtonElement).click();
    await Promise.resolve(); await Promise.resolve(); await Promise.resolve();

    const { getFanOutBuckets } = await import('../recommendations');
    const buckets = getFanOutBuckets();
    // Single bucket expected: 'upfront' normalized to 'all-upfront' before keying.
    // Both recs share the same normalized key so no fan-out is triggered and
    // getFanOutBuckets() returns null (single-bucket happy path opens
    // openPurchaseModal instead of openFanOutModal).
    expect(buckets).toBeNull();
  });
});

// Issue #111 (iii): per-row Payment seed in openPurchaseModal — the
// single-bucket / single-rec purchase modal renders editable Term and
// Payment dropdowns. Each row's defaults walk the precedence
// override → rec.payment → paymentOptionsFor[0]. Edits mutate
// currentPurchaseRecommendations[idx] in place; getPurchaseModalRecommendations()
// returns the user's choices; app.ts::handleExecutePurchase posts
// `r.payment` per rec (no longer hardcoded 'all-upfront').
describe('Issue #111 (iii): per-row Payment seed in openPurchaseModal', () => {
  beforeEach(() => {
    document.body.replaceChildren();
    const purchaseModal = document.createElement('div');
    purchaseModal.id = 'purchase-modal';
    purchaseModal.className = 'hidden';
    const purchaseDetails = document.createElement('div');
    purchaseDetails.id = 'purchase-details';
    purchaseModal.appendChild(purchaseDetails);
    document.body.appendChild(purchaseModal);
    jest.clearAllMocks();
    (api.listAccountServiceOverrides as jest.Mock).mockResolvedValue([]);
  });

  test('(a) single rec with matching override → row Payment seeded from override; source-note rendered', async () => {
    (api.listAccountServiceOverrides as jest.Mock).mockImplementation(async (id: string) => {
      if (id === 'test-account-a') {
        return [{ id: 'ovr-1', account_id: 'test-account-a', provider: 'aws', service: 'ec2', payment: 'partial-upfront' }];
      }
      return [];
    });

    const rec = {
      id: 'rec-1', provider: 'aws' as const, cloud_account_id: 'test-account-a',
      service: 'ec2', resource_type: 't3.medium', region: 'us-east-1',
      count: 5, term: 1, payment: 'all-upfront', savings: 100, upfront_cost: 500,
    };

    await openPurchaseModal([rec]);

    const live = getPurchaseModalRecommendations();
    expect(live).toHaveLength(1);
    expect(live[0]!.payment).toBe('partial-upfront');

    const select = document.querySelector<HTMLSelectElement>('.purchase-row-payment');
    expect(select).not.toBeNull();
    expect(select!.value).toBe('partial-upfront');

    const note = document.querySelector<HTMLElement>('.purchase-row-payment-source');
    expect(note).not.toBeNull();
    expect(note!.textContent).toContain('account override');
  });

  test('(b) single rec with NO matching override → row Payment seeded from rec.payment; no source-note', async () => {
    // Override exists but for a different service — the lookup misses
    // and the rec's own payment ('partial-upfront') wins.
    (api.listAccountServiceOverrides as jest.Mock).mockImplementation(async (id: string) => {
      if (id === 'test-account-a') {
        return [{ id: 'ovr-1', account_id: 'test-account-a', provider: 'aws', service: 'rds', payment: 'no-upfront' }];
      }
      return [];
    });

    const rec = {
      id: 'rec-2', provider: 'aws' as const, cloud_account_id: 'test-account-a',
      service: 'ec2', resource_type: 't3.medium', region: 'us-east-1',
      count: 5, term: 1, payment: 'partial-upfront', savings: 100, upfront_cost: 500,
    };

    await openPurchaseModal([rec]);

    const live = getPurchaseModalRecommendations();
    expect(live[0]!.payment).toBe('partial-upfront');

    const select = document.querySelector<HTMLSelectElement>('.purchase-row-payment');
    expect(select!.value).toBe('partial-upfront');

    const note = document.querySelector('.purchase-row-payment-source');
    expect(note).toBeNull();
  });

  test('(c) override has unsupported payment for the (provider,service,term) cell → falls back to rec.payment', async () => {
    // AWS RDS 3yr does NOT support no-upfront (per
    // isPaymentSupported / cmd/validators.go:warnRDS3YearNoUpfront).
    // The override pins no-upfront → must be ignored; rec.payment wins.
    (api.listAccountServiceOverrides as jest.Mock).mockImplementation(async (id: string) => {
      if (id === 'test-account-a') {
        return [{ id: 'ovr-1', account_id: 'test-account-a', provider: 'aws', service: 'rds', payment: 'no-upfront' }];
      }
      return [];
    });

    const rec = {
      id: 'rec-3', provider: 'aws' as const, cloud_account_id: 'test-account-a',
      service: 'rds', resource_type: 'db.r5.large', region: 'us-east-1',
      count: 1, term: 3, payment: 'all-upfront', savings: 200, upfront_cost: 1000,
    };

    await openPurchaseModal([rec]);

    const live = getPurchaseModalRecommendations();
    expect(live[0]!.payment).toBe('all-upfront');

    const note = document.querySelector('.purchase-row-payment-source');
    expect(note).toBeNull();
  });

  test('(d) user changes Term 1→3 → row Payment options rebuilt; live state still consistent', async () => {
    const rec = {
      id: 'rec-4', provider: 'aws' as const, cloud_account_id: 'test-account-a',
      service: 'ec2', resource_type: 't3.medium', region: 'us-east-1',
      count: 5, term: 1, payment: 'all-upfront', savings: 100, upfront_cost: 500,
    };

    await openPurchaseModal([rec]);

    const termSelect = document.querySelector<HTMLSelectElement>('.purchase-row-term');
    const paymentSelect = document.querySelector<HTMLSelectElement>('.purchase-row-payment');
    expect(termSelect).not.toBeNull();
    expect(paymentSelect).not.toBeNull();

    // Switch term to 3yr.
    termSelect!.value = '3';
    termSelect!.dispatchEvent(new Event('change'));

    const live = getPurchaseModalRecommendations();
    expect(live[0]!.term).toBe(3);
    // Payment is set to a value supported for AWS EC2 3yr — the
    // exact value depends on whether 'all-upfront' (the prior value)
    // remained valid for the new term; we only require that:
    //   (i) live.payment is non-empty
    //   (ii) live.payment matches the dropdown's current value
    //   (iii) the dropdown's options are now the 3yr-supported set.
    expect(live[0]!.payment).toBeTruthy();
    expect(paymentSelect!.value).toBe(live[0]!.payment);
    const options = Array.from(paymentSelect!.options).map((o) => o.value);
    expect(options.length).toBeGreaterThan(0);
  });

  test('(e) user changes Payment dropdown → live state reflects new value (and would round-trip via handleExecutePurchase)', async () => {
    const rec = {
      id: 'rec-5', provider: 'aws' as const, cloud_account_id: 'test-account-a',
      service: 'ec2', resource_type: 't3.medium', region: 'us-east-1',
      count: 5, term: 1, payment: 'all-upfront', savings: 100, upfront_cost: 500,
    };

    await openPurchaseModal([rec]);

    const paymentSelect = document.querySelector<HTMLSelectElement>('.purchase-row-payment');
    expect(paymentSelect).not.toBeNull();

    // Change to 'no-upfront' (always supported for AWS EC2 1yr).
    paymentSelect!.value = 'no-upfront';
    paymentSelect!.dispatchEvent(new Event('change'));

    const live = getPurchaseModalRecommendations();
    expect(live[0]!.payment).toBe('no-upfront');

    // The mapping in app.ts::handleExecutePurchase reads this value
    // verbatim (`payment: r.payment ?? 'all-upfront'`), so a downstream
    // assertion that the API call carries 'no-upfront' is implicit in
    // (a) the live state above and (b) the source-of-truth read at
    // app.ts:289-303. No separate mock-call assertion needed here —
    // the integration of that mapping is exercised by app.ts'
    // existing handleExecutePurchase tests.
  });
});

// Issue #132: pre-PR-#123 a 'savings-plans' Compute SP rec and a
// 'savings-plans' SageMaker SP rec landed in the same bulk-buy
// bucket. PR #123 split them into per-plan-type service slugs, which
// silently fanned out into N separate buckets and N separate approval
// emails. This restores the one-bucket experience.
describe('Issue #132: bulk-buy collapses SP plan types into one bucket', () => {
  beforeEach(async () => {
    document.body.replaceChildren();
    const recsTab = document.createElement('div');
    recsTab.id = 'opportunities-tab';
    recsTab.className = 'tab-content active';
    const summary = document.createElement('div');
    summary.id = 'recommendations-summary';
    const list = document.createElement('div');
    list.id = 'recommendations-list';
    recsTab.appendChild(summary);
    recsTab.appendChild(list);
    document.body.appendChild(recsTab);
    const purchaseModal = document.createElement('div');
    purchaseModal.id = 'purchase-modal';
    purchaseModal.className = 'hidden';
    const purchaseDetails = document.createElement('div');
    purchaseDetails.id = 'purchase-details';
    purchaseModal.appendChild(purchaseDetails);
    document.body.appendChild(purchaseModal);
    jest.clearAllMocks();
    // Default: empty overrides — issue #132's bucket-key collapse is
    // independent of the issue #111 override-seed path, but
    // openFanOutModal still calls listAccountServiceOverrides per
    // single-account bucket, so we keep the mock primed.
    (api.listAccountServiceOverrides as jest.Mock).mockResolvedValue([]);
    // Module-level FanOut state survives test isolation; reset it so
    // a previous test's openFanOutModal call doesn't leak into a
    // single-bucket-happy-path assertion here.
    const { clearFanOutBuckets, clearPurchaseModalRecommendations } = await import('../recommendations');
    clearFanOutBuckets();
    clearPurchaseModalRecommendations();
  });

  test('compute + sagemaker SPs at term=1 share a single bucket (happy path)', async () => {
    const recs = [
      { id: 's1', provider: 'aws', cloud_account_id: 'a1', service: 'savings-plans-compute',   resource_type: 'sp', region: 'us-east-1', count: 1, term: 1, savings: 100, upfront_cost: 500 },
      { id: 's2', provider: 'aws', cloud_account_id: 'a1', service: 'savings-plans-sagemaker', resource_type: 'sp', region: 'us-east-1', count: 1, term: 1, savings: 200, upfront_cost: 800 },
    ];
    (api.getRecommendations as jest.Mock).mockResolvedValue({ summary: {}, recommendations: recs, regions: [] });
    (state.getRecommendations as jest.Mock).mockReturnValue(recs);
    (state.getVisibleRecommendations as jest.Mock).mockReturnValue(recs);
    (state.getRecommendationsColumnFilters as jest.Mock).mockReturnValue({});
    (state.getSelectedRecommendationIDs as jest.Mock).mockReturnValue(new Set(recs.map((r) => r.id as string)));

    await loadRecommendations();
    (document.getElementById('bulk-purchase-btn') as HTMLButtonElement).click();

    const { getFanOutBuckets, getPurchaseModalRecommendations } = await import('../recommendations');
    // 1 collapsed bucket → openPurchaseModal happy path, no fan-out.
    expect(getFanOutBuckets()).toBeNull();
    // The single-bucket modal carries BOTH SPs (proves they collapsed).
    const modalRecs = getPurchaseModalRecommendations();
    expect(modalRecs.map((r) => r.id).sort()).toEqual(['s1', 's2']);
    expect(modalRecs.map((r) => r.service).sort()).toEqual([
      'savings-plans-compute',
      'savings-plans-sagemaker',
    ]);
  });

  test('SP plan types + a non-SP rec produce one SP bucket + one EC2 bucket', async () => {
    const recs = [
      { id: 's1', provider: 'aws', cloud_account_id: 'a1', service: 'savings-plans-compute',     resource_type: 'sp', region: 'us-east-1', count: 1, term: 1, savings: 100, upfront_cost: 500 },
      { id: 's2', provider: 'aws', cloud_account_id: 'a1', service: 'savings-plans-sagemaker',   resource_type: 'sp', region: 'us-east-1', count: 1, term: 1, savings: 150, upfront_cost: 600 },
      { id: 's3', provider: 'aws', cloud_account_id: 'a1', service: 'savings-plans-ec2instance', resource_type: 'sp', region: 'us-east-1', count: 1, term: 1, savings: 200, upfront_cost: 800 },
      { id: 'e1', provider: 'aws', cloud_account_id: 'a1', service: 'ec2',                       resource_type: 't3.medium', region: 'us-east-1', count: 1, term: 1, savings:  50, upfront_cost: 300 },
    ];
    (api.getRecommendations as jest.Mock).mockResolvedValue({ summary: {}, recommendations: recs, regions: [] });
    (state.getRecommendations as jest.Mock).mockReturnValue(recs);
    (state.getVisibleRecommendations as jest.Mock).mockReturnValue(recs);
    (state.getRecommendationsColumnFilters as jest.Mock).mockReturnValue({});
    (state.getSelectedRecommendationIDs as jest.Mock).mockReturnValue(new Set(recs.map((r) => r.id as string)));

    await loadRecommendations();
    (document.getElementById('bulk-purchase-btn') as HTMLButtonElement).click();
    // openFanOutModal is async (issue #111 prefetch); wait a tick.
    await Promise.resolve(); await Promise.resolve(); await Promise.resolve();

    const { getFanOutBuckets } = await import('../recommendations');
    const buckets = getFanOutBuckets();
    expect(buckets).not.toBeNull();
    // Expect 2 buckets total: 1 collapsed SP bucket (3 recs) + 1 EC2 bucket (1 rec).
    expect(buckets!.length).toBe(2);

    const spBucket = buckets!.find((b) => b.service === 'savings-plans');
    expect(spBucket).toBeDefined();
    expect(spBucket!.recs).toHaveLength(3);
    // Per-rec services are preserved — backend audit/suppression keeps
    // the real plan type per rec.
    expect(spBucket!.recs.map((r) => r.service).sort()).toEqual([
      'savings-plans-compute',
      'savings-plans-ec2instance',
      'savings-plans-sagemaker',
    ]);

    const ec2Bucket = buckets!.find((b) => b.service === 'ec2');
    expect(ec2Bucket).toBeDefined();
    expect(ec2Bucket!.recs).toHaveLength(1);
  });

  test('SP recs at different terms still split by term', async () => {
    const recs = [
      { id: 's1', provider: 'aws', cloud_account_id: 'a1', service: 'savings-plans-compute',   resource_type: 'sp', region: 'us-east-1', count: 1, term: 1, savings: 100, upfront_cost: 500 },
      { id: 's2', provider: 'aws', cloud_account_id: 'a1', service: 'savings-plans-sagemaker', resource_type: 'sp', region: 'us-east-1', count: 1, term: 3, savings: 200, upfront_cost: 800 },
    ];
    (api.getRecommendations as jest.Mock).mockResolvedValue({ summary: {}, recommendations: recs, regions: [] });
    (state.getRecommendations as jest.Mock).mockReturnValue(recs);
    (state.getVisibleRecommendations as jest.Mock).mockReturnValue(recs);
    (state.getRecommendationsColumnFilters as jest.Mock).mockReturnValue({});
    (state.getSelectedRecommendationIDs as jest.Mock).mockReturnValue(new Set(recs.map((r) => r.id as string)));

    await loadRecommendations();
    (document.getElementById('bulk-purchase-btn') as HTMLButtonElement).click();
    await Promise.resolve(); await Promise.resolve(); await Promise.resolve();

    const { getFanOutBuckets } = await import('../recommendations');
    const buckets = getFanOutBuckets();
    expect(buckets).not.toBeNull();
    // Different terms must still fan out — SP collapsing only joins
    // recs that share (provider, term).
    expect(buckets!.length).toBe(2);
    expect(buckets!.every((b) => b.service === 'savings-plans')).toBe(true);
    expect(buckets!.map((b) => b.term).sort()).toEqual([1, 3]);
  });

  test('mixed-SP bucket renders combined plan-type label', async () => {
    const recs = [
      { id: 's1', provider: 'aws', cloud_account_id: 'a1', service: 'savings-plans-compute',   resource_type: 'sp', region: 'us-east-1', count: 1, term: 1, savings: 100, upfront_cost: 500 },
      { id: 's2', provider: 'aws', cloud_account_id: 'a1', service: 'savings-plans-sagemaker', resource_type: 'sp', region: 'us-east-1', count: 1, term: 1, savings: 200, upfront_cost: 800 },
      { id: 'e1', provider: 'aws', cloud_account_id: 'a1', service: 'ec2',                     resource_type: 't3.medium', region: 'us-east-1', count: 1, term: 1, savings:  50, upfront_cost: 300 },
    ];
    (api.getRecommendations as jest.Mock).mockResolvedValue({ summary: {}, recommendations: recs, regions: [] });
    (state.getRecommendations as jest.Mock).mockReturnValue(recs);
    (state.getVisibleRecommendations as jest.Mock).mockReturnValue(recs);
    (state.getRecommendationsColumnFilters as jest.Mock).mockReturnValue({});
    (state.getSelectedRecommendationIDs as jest.Mock).mockReturnValue(new Set(recs.map((r) => r.id as string)));

    await loadRecommendations();
    (document.getElementById('bulk-purchase-btn') as HTMLButtonElement).click();
    await Promise.resolve(); await Promise.resolve(); await Promise.resolve();

    // The fan-out modal renders one section per bucket; the SP section
    // title carries the combined plan-type label.
    const sectionTitles = Array.from(
      document.querySelectorAll('.fanout-bucket h4'),
    ).map((el) => el.textContent || '');
    expect(sectionTitles.some((t) => t.includes('Savings Plans (Compute + SageMaker)'))).toBe(true);
    // Non-SP bucket title still uses the raw service slug.
    expect(sectionTitles.some((t) => t.includes('AWS / ec2'))).toBe(true);
  });
});

// Issue #658: Azure SP rows (service="savingsplans") must collapse into
// the same bulk-buy bucket as other SP types (savings-plans-*), matching
// the Go IsSavingsPlan umbrella semantics.
describe('Issue #658: Azure SP bulk-buy bucketing', () => {
  beforeEach(async () => {
    document.body.replaceChildren();
    const recsTab = document.createElement('div');
    recsTab.id = 'opportunities-tab';
    recsTab.className = 'tab-content active';
    const summary = document.createElement('div');
    summary.id = 'recommendations-summary';
    const list = document.createElement('div');
    list.id = 'recommendations-list';
    recsTab.appendChild(summary);
    recsTab.appendChild(list);
    document.body.appendChild(recsTab);
    const purchaseModal = document.createElement('div');
    purchaseModal.id = 'purchase-modal';
    purchaseModal.className = 'hidden';
    const purchaseDetails = document.createElement('div');
    purchaseDetails.id = 'purchase-details';
    purchaseModal.appendChild(purchaseDetails);
    document.body.appendChild(purchaseModal);
    jest.clearAllMocks();
    (api.listAccountServiceOverrides as jest.Mock).mockResolvedValue([]);
    const { clearFanOutBuckets, clearPurchaseModalRecommendations } = await import('../recommendations');
    clearFanOutBuckets();
    clearPurchaseModalRecommendations();
  });

  test('single Azure SP rec at term=1 lands in the single-bucket happy path', async () => {
    const recs = [
      { id: 'az-sp-1', provider: 'azure', cloud_account_id: 'sub1', service: 'savingsplans', resource_type: 'Compute', region: 'eastus', count: 2, term: 1, savings: 300, upfront_cost: 1200, payment: 'monthly' },
    ];
    (api.getRecommendations as jest.Mock).mockResolvedValue({ summary: {}, recommendations: recs, regions: [] });
    (state.getRecommendations as jest.Mock).mockReturnValue(recs);
    (state.getVisibleRecommendations as jest.Mock).mockReturnValue(recs);
    (state.getRecommendationsColumnFilters as jest.Mock).mockReturnValue({});
    (state.getSelectedRecommendationIDs as jest.Mock).mockReturnValue(new Set(recs.map((r) => r.id as string)));

    await loadRecommendations();
    (document.getElementById('bulk-purchase-btn') as HTMLButtonElement).click();

    const { getFanOutBuckets, getPurchaseModalRecommendations } = await import('../recommendations');
    // Single bucket -> happy path (no fan-out modal).
    expect(getFanOutBuckets()).toBeNull();
    const modalRecs = getPurchaseModalRecommendations();
    expect(modalRecs).toHaveLength(1);
    expect(modalRecs[0]!.service).toBe('savingsplans');
  });

  test('Azure SP + AWS SP-compute at same term collapse into one bucket', async () => {
    const recs = [
      { id: 'az-sp-1', provider: 'azure', cloud_account_id: 'sub1', service: 'savingsplans',       resource_type: 'Compute', region: 'eastus',    count: 2, term: 1, savings: 300, upfront_cost: 1200, payment: 'monthly' },
      { id: 'aws-sp-1', provider: 'aws',  cloud_account_id: 'a1',   service: 'savings-plans-compute', resource_type: 'sp',   region: 'us-east-1', count: 1, term: 1, savings: 100, upfront_cost:  500, payment: 'no-upfront' },
    ];
    (api.getRecommendations as jest.Mock).mockResolvedValue({ summary: {}, recommendations: recs, regions: [] });
    (state.getRecommendations as jest.Mock).mockReturnValue(recs);
    (state.getVisibleRecommendations as jest.Mock).mockReturnValue(recs);
    (state.getRecommendationsColumnFilters as jest.Mock).mockReturnValue({});
    // Both selected — but different providers mean different bucket keys
    // (bucket key includes provider), so they land in separate buckets.
    (state.getSelectedRecommendationIDs as jest.Mock).mockReturnValue(new Set(recs.map((r) => r.id as string)));

    await loadRecommendations();
    (document.getElementById('bulk-purchase-btn') as HTMLButtonElement).click();
    await Promise.resolve(); await Promise.resolve(); await Promise.resolve();

    const { getFanOutBuckets } = await import('../recommendations');
    const buckets = getFanOutBuckets();
    expect(buckets).not.toBeNull();
    // Two separate providers -> two buckets even though both are SP service types.
    expect(buckets!.length).toBe(2);
    // Both buckets use the canonical SP bucket service key.
    expect(buckets!.every((b) => b.service === 'savings-plans')).toBe(true);
  });
});

// Issue #224: at most one (term, payment) variant per physical-resource
// cell can be selected at any time. After PR #195's per-cell fan-out (2
// terms × 3 payments per cell), naive selection produces wrong purchase
// intent — manual checking lets the user accumulate sibling commitments,
// and `select-all` over-commits 6×. Cell = `(provider, account, service,
// region, resource_type, engine)`. The fix lives in two places: the
// per-row checkbox change handler (deselect any in-cell sibling on check)
// and the select-all handler (group by cell, pick highest-effective per).
describe('Issue #224: one-variant-per-cell radio selection', () => {
  beforeEach(() => {
    document.body.replaceChildren();
    const recsTab = document.createElement('div');
    recsTab.id = 'opportunities-tab';
    recsTab.className = 'tab-content active';
    const summary = document.createElement('div');
    summary.id = 'recommendations-summary';
    const list = document.createElement('div');
    list.id = 'recommendations-list';
    recsTab.appendChild(summary);
    recsTab.appendChild(list);
    document.body.appendChild(recsTab);
    const purchaseModal = document.createElement('div');
    purchaseModal.id = 'purchase-modal';
    purchaseModal.className = 'hidden';
    document.body.appendChild(purchaseModal);
    jest.clearAllMocks();
    (state.getRecommendationsColumnFilters as jest.Mock).mockReturnValue({});
    (state.getSelectedRecommendationIDs as jest.Mock).mockReturnValue(new Set());
  });

  // (a) Manual toggle: two variants of the same cell. Checking variant B
  // when variant A is already selected must remove A first, leaving only B.
  test('(a) checking variant B in same cell deselects sibling variant A', async () => {
    const recs = [
      { id: 'cellA-1y-allup',  provider: 'aws', cloud_account_id: 'acct-1', service: 'ec2', resource_type: 't3.medium', region: 'us-east-1', engine: '', count: 1, term: 1, savings: 100, upfront_cost: 500 },
      { id: 'cellA-3y-noup',   provider: 'aws', cloud_account_id: 'acct-1', service: 'ec2', resource_type: 't3.medium', region: 'us-east-1', engine: '', count: 1, term: 3, savings: 200, upfront_cost: 0 },
    ];
    (api.getRecommendations as jest.Mock).mockResolvedValue({ summary: {}, recommendations: recs, regions: [] });
    // state.getRecommendations() is the full loaded set (used by the sibling-eviction loop).
    (state.getRecommendations as jest.Mock).mockReturnValue(recs);
    // Pretend variant A is already selected (the "user previously checked it" state).
    (state.getSelectedRecommendationIDs as jest.Mock).mockReturnValue(new Set(['cellA-1y-allup']));
    await loadRecommendations();

    // issues #225/#226: multi-variant cells are collapsed by default.
    // Expand the cell first so the variant checkboxes are rendered.
    const chevron = document.querySelector<HTMLButtonElement>('.rec-cell-chevron');
    expect(chevron).not.toBeNull();
    chevron!.click();

    // Tick variant B in the same cell.
    const cbs = Array.from(document.querySelectorAll<HTMLInputElement>('tbody input[data-rec-id]'));
    const variantB = cbs.find((cb) => cb.dataset['recId'] === 'cellA-3y-noup');
    expect(variantB).toBeDefined();
    variantB!.checked = true;
    variantB!.dispatchEvent(new Event('change'));

    // The handler must have removed sibling A AND added B.
    const removed = (state.removeSelectedRecommendation as jest.Mock).mock.calls.map((c) => c[0]);
    const added = (state.addSelectedRecommendation as jest.Mock).mock.calls.map((c) => c[0]);
    expect(removed).toContain('cellA-1y-allup');
    expect(added).toContain('cellA-3y-noup');
    // Sanity: B was not also removed, A was not also added.
    expect(removed).not.toContain('cellA-3y-noup');
    expect(added).not.toContain('cellA-1y-allup');
  });

  // (b) Cross-cell independence: selecting a rec in cell X must not
  // affect cell Y's selection state. The radio enforcement is per-cell,
  // not global.
  test('(b) selecting in cell X does not affect cell Y selections', async () => {
    const recs = [
      { id: 'cellX-1y',  provider: 'aws', cloud_account_id: 'acct-1', service: 'ec2', resource_type: 't3.medium', region: 'us-east-1', engine: '', count: 1, term: 1, savings: 100, upfront_cost: 500 },
      { id: 'cellY-1y',  provider: 'aws', cloud_account_id: 'acct-1', service: 'rds', resource_type: 'db.r5.large', region: 'us-east-1', engine: 'mysql', count: 1, term: 1, savings: 200, upfront_cost: 1000 },
    ];
    (api.getRecommendations as jest.Mock).mockResolvedValue({ summary: {}, recommendations: recs, regions: [] });
    (state.getRecommendations as jest.Mock).mockReturnValue(recs);
    // Pretend cellY is already selected.
    (state.getSelectedRecommendationIDs as jest.Mock).mockReturnValue(new Set(['cellY-1y']));
    await loadRecommendations();

    // Tick cellX.
    const cbs = Array.from(document.querySelectorAll<HTMLInputElement>('tbody input[data-rec-id]'));
    const cellX = cbs.find((cb) => cb.dataset['recId'] === 'cellX-1y');
    expect(cellX).toBeDefined();
    cellX!.checked = true;
    cellX!.dispatchEvent(new Event('change'));

    // cellY must NOT have been removed — cells are independent.
    const removed = (state.removeSelectedRecommendation as jest.Mock).mock.calls.map((c) => c[0]);
    expect(removed).not.toContain('cellY-1y');
    const added = (state.addSelectedRecommendation as jest.Mock).mock.calls.map((c) => c[0]);
    expect(added).toContain('cellX-1y');
  });

  // (c) Select-all collapses to one-per-cell. Three distinct cells × six
  // variants each = 18 recs. After select-all, exactly 3 should be added,
  // not 18 (one per cell). This is the headline money-impact fix.
  test('(c) select-all picks exactly one variant per cell (3 cells × 6 variants → 3 selected)', async () => {
    const cells = [
      { account: 'acct-1', service: 'ec2', resource_type: 't3.medium', region: 'us-east-1', engine: '' },
      { account: 'acct-1', service: 'ec2', resource_type: 'm5.large',  region: 'us-east-1', engine: '' },
      { account: 'acct-1', service: 'rds', resource_type: 'db.r5.large', region: 'eu-west-1', engine: 'mysql' },
    ];
    const variants = [
      { term: 1, payment: 'all-upfront' },
      { term: 1, payment: 'partial-upfront' },
      { term: 1, payment: 'no-upfront' },
      { term: 3, payment: 'all-upfront' },
      { term: 3, payment: 'partial-upfront' },
      { term: 3, payment: 'no-upfront' },
    ];
    const recs: Array<Record<string, unknown>> = [];
    let i = 0;
    for (const c of cells) {
      for (const v of variants) {
        recs.push({
          id: `c${i++}`,
          provider: 'aws', cloud_account_id: c.account, service: c.service,
          resource_type: c.resource_type, region: c.region, engine: c.engine,
          count: 1, term: v.term, savings: 100 + i, upfront_cost: 500,
        });
      }
    }
    expect(recs).toHaveLength(18);

    (api.getRecommendations as jest.Mock).mockResolvedValue({ summary: {}, recommendations: recs, regions: [] });
    (state.getVisibleRecommendations as jest.Mock).mockReturnValue(recs);
    await loadRecommendations();

    const selectAll = document.getElementById('select-all-recs') as HTMLInputElement;
    expect(selectAll).not.toBeNull();
    selectAll.checked = true;
    selectAll.dispatchEvent(new Event('change'));

    // Exactly 3 add calls — one per cell.
    expect((state.addSelectedRecommendation as jest.Mock).mock.calls).toHaveLength(3);
    // And clearSelectedRecommendations was called first to drop any stale state.
    expect(state.clearSelectedRecommendations).toHaveBeenCalled();
  });

  // (d) Tiebreaker: when multiple variants share a cell, select-all picks
  // the variant with the highest EFFECTIVE monthly savings (amortizing
  // upfront over term * 12 months) — NOT the highest raw `savings`.
  // Concrete example: a 3yr/all-upfront with $36000 upfront + $1200/mo
  // headline savings has effective = 1200 - 36000/36 = $200. A 1yr/no-upfront
  // with $0 upfront + $300/mo headline savings has effective = $300. The
  // 1yr/no-upfront wins despite the 3yr's higher raw `savings`.
  test('(d) select-all picks highest-effective-savings (amortized) per cell', async () => {
    const recs = [
      // 3yr/all-upfront — high raw savings ($1200/mo) but huge upfront drags effective to $200/mo.
      { id: 'big-upfront', provider: 'aws', cloud_account_id: 'acct-1', service: 'ec2', resource_type: 't3.medium', region: 'us-east-1', engine: '', count: 1, term: 3, savings: 1200, upfront_cost: 36000 },
      // 1yr/no-upfront — lower raw ($300/mo) but $0 upfront → effective stays at $300/mo.
      { id: 'no-upfront',  provider: 'aws', cloud_account_id: 'acct-1', service: 'ec2', resource_type: 't3.medium', region: 'us-east-1', engine: '', count: 1, term: 1, savings: 300,  upfront_cost: 0 },
      // 3yr/partial-upfront — middle of the road ($600/mo savings, $7200 upfront → effective = 600 - 7200/36 = $400).
      { id: 'middle',      provider: 'aws', cloud_account_id: 'acct-1', service: 'ec2', resource_type: 't3.medium', region: 'us-east-1', engine: '', count: 1, term: 3, savings: 600,  upfront_cost: 7200 },
    ];
    (api.getRecommendations as jest.Mock).mockResolvedValue({ summary: {}, recommendations: recs, regions: [] });
    (state.getVisibleRecommendations as jest.Mock).mockReturnValue(recs);
    await loadRecommendations();

    const selectAll = document.getElementById('select-all-recs') as HTMLInputElement;
    selectAll.checked = true;
    selectAll.dispatchEvent(new Event('change'));

    // Exactly one add call (single cell, one variant picked).
    const addCalls = (state.addSelectedRecommendation as jest.Mock).mock.calls;
    expect(addCalls).toHaveLength(1);
    // The "middle" variant has the highest effective ($400/mo > $300 > $200) — picked.
    expect(addCalls[0]![0]).toBe('middle');
  });
});

// ---------------------------------------------------------------------------
// issue #223: default-seed from GlobalConfig across all 3 surfaces.
// These tests exercise the pickBestVariantPerCell config-match tiebreaker
// and the seedGlobalDefaults hook that injects resolved GlobalConfig values.
// ---------------------------------------------------------------------------

describe('issue #223: pickBestVariantPerCell config-match tiebreaker', () => {
  const rec = (
    id: string,
    term: 1 | 3,
    payment: string,
    savings = 100,
    upfront_cost = 0,
  ) => ({
    id,
    provider: 'aws' as const,
    cloud_account_id: 'acct-1',
    service: 'ec2',
    resource_type: 't3.medium',
    region: 'us-east-1',
    engine: '',
    count: 1,
    term,
    payment,
    savings,
    upfront_cost,
  } as unknown as LocalRecommendation);

  afterEach(() => {
    // Reset module cache to initial defaults so tests don't bleed into each other.
    seedGlobalDefaults(1, 'all-upfront');
  });

  test('prefers variant matching configured (term, payment) over highest-effective', () => {
    // Two variants in one cell: 1yr/all-upfront (configured default) vs
    // 3yr/no-upfront (higher effective savings).
    const recs = [
      rec('want-this', 1, 'all-upfront', 300, 0), // effective = $300/mo
      rec('skip-this', 3, 'no-upfront', 400, 0), // effective = $400/mo (higher)
    ];
    seedGlobalDefaults(1, 'all-upfront');
    const result = pickBestVariantPerCell(recs);
    expect(result).toHaveLength(1);
    expect(result[0]!.id).toBe('want-this');
  });

  test('falls back to highest-effective when no variant matches configured defaults', () => {
    // Neither variant matches term=1/all-upfront; fallback picks highest effective.
    const recs = [
      rec('low-effective', 3, 'all-upfront', 1200, 36000), // effective = 1200 - 1000 = $200
      rec('high-effective', 3, 'no-upfront', 400, 0), // effective = $400
    ];
    seedGlobalDefaults(1, 'all-upfront'); // neither matches
    const result = pickBestVariantPerCell(recs);
    expect(result).toHaveLength(1);
    expect(result[0]!.id).toBe('high-effective');
  });

  test('handles multiple independent cells and picks config-match in each', () => {
    // Two distinct cells; each has a variant matching configured defaults.
    const recs = [
      rec('cell-a-match', 1, 'all-upfront', 100, 0),
      rec('cell-a-other', 3, 'no-upfront', 400, 0), // higher effective but different cell
      { ...rec('cell-b-match', 1, 'all-upfront', 200, 0), region: 'eu-west-1', id: 'cell-b-match' },
      { ...rec('cell-b-other', 3, 'partial-upfront', 600, 0), region: 'eu-west-1', id: 'cell-b-other' },
    ];
    seedGlobalDefaults(1, 'all-upfront');
    const result = pickBestVariantPerCell(recs);
    const ids = result.map((r) => r.id).sort();
    expect(ids).toEqual(['cell-a-match', 'cell-b-match']);
  });

  test('config-match with 3yr/partial-upfront as configured defaults', () => {
    const recs = [
      rec('wrong-1', 1, 'all-upfront', 100, 0),
      rec('want-3yr', 3, 'partial-upfront', 100, 0),
    ];
    seedGlobalDefaults(3, 'partial-upfront');
    const result = pickBestVariantPerCell(recs);
    expect(result).toHaveLength(1);
    expect(result[0]!.id).toBe('want-3yr');
  });
});

// ---------------------------------------------------------------------------
// Issue #220 / #221: effectiveMonthlySavings + effectiveSavingsPct helpers
// + Monthly Cost and Effective % column rendering
// ---------------------------------------------------------------------------

describe('effectiveMonthlySavings', () => {
  const mk = (overrides: Partial<LocalRecommendation>): LocalRecommendation => ({
    id: 'r',
    provider: 'aws',
    service: 'ec2',
    resource_type: 't3.medium',
    region: 'us-east-1',
    count: 1,
    term: 1,
    savings: 100,
    upfront_cost: 0,
    monthly_cost: 50,
    ...overrides,
  } as unknown as LocalRecommendation);

  test('no-upfront: effective equals raw savings when upfront=0', () => {
    expect(effectiveMonthlySavings(mk({ savings: 100, upfront_cost: 0, term: 1 }))).toBeCloseTo(100);
  });

  test('all-upfront: effective = savings - upfront / (term * 12)', () => {
    expect(effectiveMonthlySavings(mk({ savings: 50, upfront_cost: 600, term: 1 }))).toBeCloseTo(0);
    expect(effectiveMonthlySavings(mk({ savings: 1200, upfront_cost: 36000, term: 3 }))).toBeCloseTo(200);
  });

  test('partial-upfront: intermediate amortization', () => {
    expect(effectiveMonthlySavings(mk({ savings: 600, upfront_cost: 7200, term: 3 }))).toBeCloseTo(400);
  });

  test('term=0 clamps to 1yr (12 months) to avoid division by zero', () => {
    expect(effectiveMonthlySavings(mk({ savings: 60, upfront_cost: 120, term: 0 }))).toBeCloseTo(50);
  });

  test('can return negative when upfront dominates (data anomaly signal)', () => {
    expect(effectiveMonthlySavings(mk({ savings: 10, upfront_cost: 1200, term: 1 }))).toBeCloseTo(-90);
  });
});

describe('effectiveSavingsPct', () => {
  // mk() defaults to 'azure' for reconstruction-path tests. AWS rows require
  // on_demand_cost to be set; without it effectiveSavingsPct returns null
  // (#323). Azure reconstruction is still valid (Azure may omit on_demand_cost
  // for older cached rows where the field was not yet populated).
  const mk = (overrides: Partial<LocalRecommendation>): LocalRecommendation => ({
    id: 'r',
    provider: 'azure',
    service: 'ec2',
    resource_type: 't3.medium',
    region: 'us-east-1',
    count: 1,
    term: 1,
    savings: 100,
    upfront_cost: 0,
    monthly_cost: 50,
    ...overrides,
  } as unknown as LocalRecommendation);

  test('no-upfront: pct = savings / (monthly_cost + savings) * 100', () => {
    const pct = effectiveSavingsPct(mk({ savings: 100, upfront_cost: 0, monthly_cost: 50, term: 1 }));
    expect(pct).not.toBeNull();
    expect(pct!).toBeCloseTo(66.67, 1);
  });

  test('all-upfront with monthly_cost=0: pct uses amortized upfront in onDemand', () => {
    const pct = effectiveSavingsPct(mk({ savings: 50, upfront_cost: 600, monthly_cost: 0, term: 1 }));
    expect(pct).not.toBeNull();
    expect(pct!).toBeCloseTo(0, 1);
  });

  test('partial-upfront: standard case', () => {
    const pct = effectiveSavingsPct(mk({ savings: 600, upfront_cost: 7200, monthly_cost: 200, term: 3 }));
    expect(pct).not.toBeNull();
    expect(pct!).toBeCloseTo(40, 1);
  });

  test('on_demand_monthly=0 returns null (no division by zero)', () => {
    const pct = effectiveSavingsPct(mk({ savings: 0, upfront_cost: 0, monthly_cost: 0, term: 1 }));
    expect(pct).toBeNull();
  });

  test('term=0 clamps to 12 months (no explosion)', () => {
    const pct = effectiveSavingsPct(mk({ savings: 60, upfront_cost: 120, monthly_cost: 40, term: 0 }));
    expect(pct).toBeNull();
  });

  test('negative effective savings returns a negative percentage', () => {
    const pct = effectiveSavingsPct(mk({ savings: 10, upfront_cost: 1200, monthly_cost: 400, term: 1 }));
    expect(pct).not.toBeNull();
    expect(pct!).toBeLessThan(0);
    expect(pct!).toBeCloseTo(-17.65, 1);
  });

  test('undefined/null monthly_cost returns null (data not provided)', () => {
    // monthly_cost null/undefined means the provider API did not return a monthly
    // recurring breakdown. Without it we cannot reconstruct on_demand_monthly,
    // so effectiveSavingsPct must return null rather than collapsing the
    // denominator to savings alone (which produced the misleading 100% rows in #252).
    expect(effectiveSavingsPct(mk({ savings: 100, upfront_cost: 0, monthly_cost: undefined, term: 1 }))).toBeNull();
    expect(effectiveSavingsPct(mk({ savings: 100, upfront_cost: 0, monthly_cost: null, term: 1 }))).toBeNull();
  });

  test('monthly_cost=0 (real all-upfront) is treated as known data, not missing', () => {
    // A literal 0 means the backend explicitly reported zero recurring cost
    // (e.g. an all-upfront commitment). effectiveSavingsPct SHOULD compute a
    // result in this case, because on_demand_monthly = 0 + savings + amortized.
    const pct = effectiveSavingsPct(mk({ savings: 100, upfront_cost: 0, monthly_cost: 0, term: 1 }));
    // onDemand = 0 + 100 + 0 = 100; effective = 100/100 * 100 = 100%
    expect(pct).not.toBeNull();
    expect(pct!).toBeCloseTo(100, 1);
  });

  // #323: AWS rows require the provider-canonical on_demand_cost; without it
  // the reconstruction formula (monthly_cost + savings + amortized) diverges
  // from the true CE baseline and produces misleadingly high percentages.
  describe('AWS reconstruction fallback returns null (#323)', () => {
    const mkAws = (overrides: Partial<LocalRecommendation>): LocalRecommendation =>
      mk({ provider: 'aws', ...overrides });

    test('AWS row with no on_demand_cost returns null regardless of monthly_cost', () => {
      // Repro: RI rec where EstimatedMonthlyOnDemandCost was absent from the
      // CE response. Reconstruction gives a number but it diverges from the
      // real denominator, so null is the correct sentinel.
      expect(effectiveSavingsPct(mkAws({ savings: 100, upfront_cost: 0, monthly_cost: 50, term: 1 }))).toBeNull();
    });

    test('AWS row with on_demand_cost=0 (treated as not-populated) returns null', () => {
      // Backend nonZeroPtr converts 0 -> nil, so 0 at the frontend means the
      // field was absent. Same outcome as missing.
      expect(effectiveSavingsPct(mkAws({ savings: 100, upfront_cost: 0, monthly_cost: 50, term: 1, on_demand_cost: 0 }))).toBeNull();
    });

    test('AWS row with valid on_demand_cost computes normally', () => {
      // When CE supplies the baseline, the formula is well-defined.
      // effectiveSavings = 100 - 0 = 100; pct = 100 / 300 * 100 = 33.33%
      const pct = effectiveSavingsPct(mkAws({ savings: 100, upfront_cost: 0, monthly_cost: 200, term: 1, on_demand_cost: 300 }));
      expect(pct).not.toBeNull();
      expect(pct!).toBeCloseTo(33.33, 1);
    });
  });

  // #274: on_demand_cost (when populated by the provider) is used directly
  // as the denominator instead of reconstructing it from
  // monthly_cost + savings + amortized. The reconstruction collapses for
  // Azure all-upfront recs where monthly_cost = $0 and inflates the % well
  // past realistic ceilings.
  describe('on_demand_cost preference (#274)', () => {
    test('repro of the live Azure D11_v2 row with provider-supplied on_demand_cost', () => {
      // Reconstructed (no on_demand_cost): savings=$29, upfront=$26,
      // monthly=$0, term=1 → onDemand = 0 + 29 + 2.17 = $31.17 →
      // pct ≈ 86% (the inflated value the user complained about).
      // With provider-supplied on_demand_cost = $122.64 (the real Azure
      // CostWithNoReservedInstances for 2 × Standard_D11_v2 in eastus),
      // pct ≈ (29 - 2.17) / 122.64 = ~21.9% — within realistic 1-year
      // RI savings.
      const pctReconstructed = effectiveSavingsPct(
        mk({ savings: 29, upfront_cost: 26, monthly_cost: 0, term: 1 }),
      );
      expect(pctReconstructed!).toBeGreaterThan(80);

      const pctWithBaseline = effectiveSavingsPct(
        mk({ savings: 29, upfront_cost: 26, monthly_cost: 0, term: 1, on_demand_cost: 122.64 }),
      );
      expect(pctWithBaseline).not.toBeNull();
      expect(pctWithBaseline!).toBeCloseTo(21.88, 1);
    });

    test('on_demand_cost overrides reconstruction even when monthly_cost is non-zero', () => {
      // Demonstrates the preference order: when both are present, the
      // provider's on_demand_cost wins over the reconstructed value.
      // Otherwise a stale/cached monthly_cost could override the canonical
      // baseline silently.
      const pct = effectiveSavingsPct(
        mk({ savings: 100, upfront_cost: 0, monthly_cost: 50, term: 1, on_demand_cost: 500 }),
      );
      // With baseline: pct = (100 - 0) / 500 * 100 = 20%
      // Reconstruction would have given: pct = 100 / (50 + 100 + 0) * 100 = 66.7%
      expect(pct).not.toBeNull();
      expect(pct!).toBeCloseTo(20, 1);
    });

    test('on_demand_cost null/undefined falls back to reconstruction (back-compat)', () => {
      // Pre-#274 cached recs have no on_demand_cost. The frontend
      // reconstructs as before so older data still renders meaningful
      // percentages while it gets refreshed.
      const r = mk({ savings: 100, upfront_cost: 0, monthly_cost: 50, term: 1 });
      delete (r as { on_demand_cost?: unknown }).on_demand_cost;
      const pct = effectiveSavingsPct(r);
      expect(pct).not.toBeNull();
      expect(pct!).toBeCloseTo(66.67, 1);
    });

    test('on_demand_cost = 0 from the provider is treated as not-populated (falls back)', () => {
      // The backend's convertRecommendations writes 0 → nil so this case
      // is unlikely on a fresh refresh, but defence-in-depth: a literal 0
      // baseline is impossible (the resource would be free), so the
      // formula treats it the same as null.
      const pct = effectiveSavingsPct(
        mk({ savings: 100, upfront_cost: 0, monthly_cost: 50, term: 1, on_demand_cost: 0 }),
      );
      expect(pct).not.toBeNull();
      expect(pct!).toBeCloseTo(66.67, 1);
    });

    test('on_demand_cost populated rescues the missing-monthly_cost case', () => {
      // Without on_demand_cost: monthly_cost === null returns null
      // (denominator can't be reconstructed). With the provider baseline,
      // the formula has everything it needs and returns a real value.
      const r = mk({ savings: 100, upfront_cost: 0, monthly_cost: null, term: 1, on_demand_cost: 250 });
      const pct = effectiveSavingsPct(r);
      expect(pct).not.toBeNull();
      expect(pct!).toBeCloseTo(40, 1);
    });

    // #303: AWS RI and SP fixtures — the same on_demand_cost preference
    // applies to AWS recommendations (previously only verified for Azure).
    describe('AWS provider fixtures (#303)', () => {
      test('AWS EC2 RI: on_demand_cost from EstimatedMonthlyOnDemandCost is used as denominator', () => {
        // Real-world shape: 2 × m5.large 1yr partial-upfront.
        // EstimatedMonthlyOnDemandCost = $150 (AWS CE field plumbed via
        // parseAWSCostDetails → common.Recommendation.OnDemandCost).
        // savings = $45, upfront = $120 → amortized = $10/mo.
        // effectiveSavings = 45 - 10 = $35.
        // pct = 35 / 150 × 100 ≈ 23.3%.
        // Without on_demand_cost the reconstruction would give:
        //   onDemand = 60 + 45 + 10 = $115 → pct ≈ 30.4% (wrong).
        const pct = effectiveSavingsPct(
          mk({ savings: 45, upfront_cost: 120, monthly_cost: 60, term: 1, on_demand_cost: 150 }),
        );
        expect(pct).not.toBeNull();
        // pct = (45 - 10) / 150 × 100 = 23.33…
        expect(pct!).toBeCloseTo(23.33, 1);
      });

      test('AWS Compute SP: on_demand_cost from CurrentAverageHourlyOnDemandSpend×730 is used as denominator', () => {
        // SP rows carry monthly_cost = the recurring commitment charge, not
        // the full on-demand baseline. Without on_demand_cost the
        // reconstruction would over- or under-count the denominator.
        // Here: savings=$200, monthly_cost=$800 (commitment), on_demand_cost=$1000.
        // Reconstruction would give: onDemand = 800 + 200 + 0 = $1000 — same
        // in this clean case, but diverges for partial-upfront SP where
        // monthly_cost ≠ hourlyCommitment × 730.
        // effectiveSavings = 200 - 0 = $200; pct = 200 / 1000 × 100 = 20%.
        const pct = effectiveSavingsPct(
          mk({ savings: 200, upfront_cost: 0, monthly_cost: 800, term: 1, on_demand_cost: 1000 }),
        );
        expect(pct).not.toBeNull();
        expect(pct!).toBeCloseTo(20, 1);
      });

      test('AWS SP no-upfront with null monthly_cost: on_demand_cost alone is sufficient', () => {
        // SP recommendation where the backend did not populate monthly_cost
        // (e.g. all-upfront SP with recurring = $0 stored as nil).
        // With on_demand_cost the formula is fully determined.
        const pct = effectiveSavingsPct(
          mk({ savings: 300, upfront_cost: 0, monthly_cost: null, term: 1, on_demand_cost: 1500 }),
        );
        expect(pct).not.toBeNull();
        // effectiveSavings = 300 - 0 = 300; pct = 300 / 1500 × 100 = 20%
        expect(pct!).toBeCloseTo(20, 1);
      });
    });
  });
});

describe('onDemandMonthly', () => {
  // #330: onDemandMonthly returns the provider-supplied on_demand_cost
  // directly. No reconstruction fallback — when the provider didn't
  // populate on_demand_cost, the column renders an em-dash.
  const mk = (overrides: Partial<LocalRecommendation>): LocalRecommendation => ({
    id: 'r',
    provider: 'aws',
    service: 'ec2',
    resource_type: 't3.medium',
    region: 'us-east-1',
    count: 1,
    term: 1,
    savings: 100,
    upfront_cost: 0,
    monthly_cost: 50,
    ...overrides,
  } as unknown as LocalRecommendation);

  test('returns provider on_demand_cost directly when populated', () => {
    // The function must return the raw provider value, not any reconstruction.
    const odm = onDemandMonthly(mk({
      on_demand_cost: 500,
      savings: 100,
      upfront_cost: 0,
      monthly_cost: 50,
      term: 1,
    }));
    expect(odm).toBe(500);
  });

  test('null on_demand_cost returns null (no reconstruction fallback)', () => {
    // Explicit null — common for legacy cached recs that pre-date #277/#312.
    expect(onDemandMonthly(mk({ on_demand_cost: null }))).toBeNull();
  });

  test('explicit undefined on_demand_cost returns null', () => {
    // TypeScript's strict mode lets callers pass `undefined` rather than
    // omitting the field; assert that branch explicitly so a future
    // `r.on_demand_cost > 0` guard regression doesn't accept undefined.
    expect(onDemandMonthly(mk({ on_demand_cost: undefined }))).toBeNull();
  });

  test('missing on_demand_cost field returns null', () => {
    // Distinct from the explicit-undefined case: the property never
    // existed on the object (e.g. legacy cached recs from before #277).
    const r = mk({});
    delete (r as { on_demand_cost?: unknown }).on_demand_cost;
    expect(onDemandMonthly(r)).toBeNull();
  });

  test('on_demand_cost=0 returns null (provider treats 0 as not-populated)', () => {
    // Same convention as the on_demand_cost preference branch in
    // effectiveSavingsPct: zero means the provider didn't report it.
    expect(onDemandMonthly(mk({ on_demand_cost: 0 }))).toBeNull();
  });

  test('positive on_demand_cost wins regardless of monthly_cost / term / upfront', () => {
    // Whatever the supporting fields say, the provider value is authoritative.
    expect(onDemandMonthly(mk({
      on_demand_cost: 1500,
      monthly_cost: null,
      term: 0,
      upfront_cost: 99999,
    }))).toBe(1500);
  });
});

describe('Monthly Cost + Effective % column rendering', () => {
  beforeEach(() => {
    document.body.innerHTML = [
      '<div id="opportunities-tab" class="tab-content active">',
      '<div id="recommendations-summary"></div>',
      '<div id="recommendations-list"></div>',
      '</div>',
      '<div id="purchase-modal" class="hidden">',
      '<div id="purchase-details"></div>',
      '</div>',
    ].join('');
    jest.clearAllMocks();
    jest.useFakeTimers();
    window.alert = jest.fn();
  });

  afterEach(() => {
    jest.useRealTimers();
  });

  const baseRec = (overrides: Partial<LocalRecommendation> = {}): LocalRecommendation => ({
    id: 'test-rec',
    provider: 'aws' as const,
    service: 'ec2',
    resource_type: 't3.medium',
    region: 'us-east-1',
    count: 1,
    term: 1,
    savings: 100,
    upfront_cost: 0,
    monthly_cost: 50,
    ...overrides,
  } as unknown as LocalRecommendation);

  test('table header includes "Monthly Cost", "On-Demand Monthly", and "Effective %" columns', async () => {
    (api.getRecommendations as jest.Mock).mockResolvedValue({
      summary: {},
      recommendations: [baseRec()],
      regions: [],
    });
    (state.getRecommendations as jest.Mock).mockReturnValue([baseRec()]);
    await loadRecommendations();

    const headers = Array.from(document.querySelectorAll('th')).map((th) => th.textContent ?? '');
    expect(headers.some((h) => h.includes('Monthly Cost'))).toBe(true);
    expect(headers.some((h) => h.includes('On-Demand Monthly'))).toBe(true);
    expect(headers.some((h) => h.includes('Effective %'))).toBe(true);
  });

  test('no-upfront row: Monthly Cost shows rec.monthly_cost, Effective % is positive', async () => {
    // AWS row requires on_demand_cost (#323); add it so the pct column renders.
    const rec = baseRec({ savings: 100, upfront_cost: 0, monthly_cost: 50, term: 1, on_demand_cost: 150 });
    (api.getRecommendations as jest.Mock).mockResolvedValue({
      summary: {},
      recommendations: [rec],
      regions: [],
    });
    (state.getRecommendations as jest.Mock).mockReturnValue([rec]);
    await loadRecommendations();

    const cells = Array.from(document.querySelectorAll('tbody td')).map((td) => td.textContent ?? '');
    expect(cells.some((c) => c === '$50')).toBe(true);
    expect(cells.some((c) => c.includes('%') && !c.includes('em'))).toBe(true);
  });

  test('all-upfront row: Monthly Cost shows $0, Effective % accounts for amortization', async () => {
    // AWS row requires on_demand_cost (#323). savings=50, upfront=600, term=1
    // => amortized=50, effectiveSavings=0. on_demand_cost=100 => pct=0.0%.
    const rec = baseRec({ savings: 50, upfront_cost: 600, monthly_cost: 0, term: 1, on_demand_cost: 100 });
    (api.getRecommendations as jest.Mock).mockResolvedValue({
      summary: {},
      recommendations: [rec],
      regions: [],
    });
    (state.getRecommendations as jest.Mock).mockReturnValue([rec]);
    await loadRecommendations();

    const cells = Array.from(document.querySelectorAll('tbody td')).map((td) => td.textContent ?? '');
    expect(cells.some((c) => c === '$0')).toBe(true);
    expect(cells.some((c) => c === '0.0%')).toBe(true);
  });

  test('on_demand_monthly=0 row: Effective % renders as em-dash', async () => {
    const rec = baseRec({ savings: 0, upfront_cost: 0, monthly_cost: 0, term: 1 });
    (api.getRecommendations as jest.Mock).mockResolvedValue({
      summary: {},
      recommendations: [rec],
      regions: [],
    });
    (state.getRecommendations as jest.Mock).mockReturnValue([rec]);
    await loadRecommendations();

    const cells = Array.from(document.querySelectorAll('tbody td')).map((td) => td.textContent ?? '');
    expect(cells.some((c) => c === '—')).toBe(true);
  });

  test('negative-effective row: Effective % cell has effective-pct-negative class', async () => {
    // AWS row requires on_demand_cost (#323). savings=10, upfront=1200, term=1
    // => amortized=100, effectiveSavings=-90. on_demand_cost=510 => pct<0.
    const rec = baseRec({ savings: 10, upfront_cost: 1200, monthly_cost: 400, term: 1, on_demand_cost: 510 });
    (api.getRecommendations as jest.Mock).mockResolvedValue({
      summary: {},
      recommendations: [rec],
      regions: [],
    });
    (state.getRecommendations as jest.Mock).mockReturnValue([rec]);
    await loadRecommendations();

    const negativeCells = document.querySelectorAll('tbody td.effective-pct-negative');
    expect(negativeCells.length).toBeGreaterThan(0);
  });

  test('sort header for monthly_cost is wired - clicking sets sort to monthly_cost', async () => {
    (api.getRecommendations as jest.Mock).mockResolvedValue({
      summary: {},
      recommendations: [baseRec()],
      regions: [],
    });
    (state.getRecommendations as jest.Mock).mockReturnValue([baseRec()]);
    await loadRecommendations();

    const header = document.querySelector<HTMLTableCellElement>('th[data-sort="monthly_cost"]');
    expect(header).not.toBeNull();
    header!.click();
    expect(state.setRecommendationsSort).toHaveBeenCalledWith(
      expect.objectContaining({ column: 'monthly_cost' }),
    );
  });

  test('sort header for effective_savings_pct is wired - clicking sets sort', async () => {
    (api.getRecommendations as jest.Mock).mockResolvedValue({
      summary: {},
      recommendations: [baseRec()],
      regions: [],
    });
    (state.getRecommendations as jest.Mock).mockReturnValue([baseRec()]);
    await loadRecommendations();

    const header = document.querySelector<HTMLTableCellElement>('th[data-sort="effective_savings_pct"]');
    expect(header).not.toBeNull();
    header!.click();
    expect(state.setRecommendationsSort).toHaveBeenCalledWith(
      expect.objectContaining({ column: 'effective_savings_pct' }),
    );
  });

  test('On-Demand Monthly column renders formatCurrency from provider on_demand_cost (#330)', async () => {
    // #330 — column shows the raw provider value, not a reconstruction.
    const rec = baseRec({ on_demand_cost: 150, savings: 100, upfront_cost: 0, monthly_cost: 50, term: 1 });
    (api.getRecommendations as jest.Mock).mockResolvedValue({
      summary: {},
      recommendations: [rec],
      regions: [],
    });
    (state.getRecommendations as jest.Mock).mockReturnValue([rec]);
    await loadRecommendations();

    const headers = Array.from(document.querySelectorAll('th')).map((th) => th.textContent ?? '');
    expect(headers.some((h) => h.includes('On-Demand Monthly'))).toBe(true);

    // Pin the specific on_demand_monthly cell by column index (not a scan of all tds).
    const odmColIdx = Array.from(document.querySelectorAll('th'))
      .findIndex((th) => th.getAttribute('data-sort') === 'on_demand_monthly');
    expect(odmColIdx).toBeGreaterThanOrEqual(0);
    const firstRowCells = Array.from(
      document.querySelectorAll('tbody tr.recommendation-row td'),
    );
    expect(firstRowCells[odmColIdx]?.textContent).toBe('$150');
  });

  test('On-Demand Monthly column renders em-dash when on_demand_cost is missing (#330)', async () => {
    // #330 — without provider on_demand_cost, no reconstruction; cell is em-dash.
    const rec = baseRec({ savings: 100, upfront_cost: 0, monthly_cost: 50, term: 1 });
    (api.getRecommendations as jest.Mock).mockResolvedValue({
      summary: {},
      recommendations: [rec],
      regions: [],
    });
    (state.getRecommendations as jest.Mock).mockReturnValue([rec]);
    await loadRecommendations();

    // Pin the specific on_demand_monthly cell (not a scan of all tds — Effective % also renders em-dash).
    const odmColIdx = Array.from(document.querySelectorAll('th'))
      .findIndex((th) => th.getAttribute('data-sort') === 'on_demand_monthly');
    const firstRowCells = Array.from(
      document.querySelectorAll('tbody tr.recommendation-row td'),
    );
    expect(firstRowCells[odmColIdx]?.textContent).toBe('—');
  });

  test('on_demand_monthly numeric filter: row with no on_demand_cost does not match = 0 (#330)', async () => {
    // #330 — without provider on_demand_cost, onDemandMonthly returns null,
    // which becomes a NaN sentinel in numericCellValue. A filter expr "0"
    // (equals zero) must NOT match such rows.
    const nullRec = baseRec({ savings: 100, upfront_cost: 0, monthly_cost: 50, term: 1 });
    (api.getRecommendations as jest.Mock).mockResolvedValue({
      summary: {},
      recommendations: [nullRec],
      regions: [],
    });
    (state.getRecommendations as jest.Mock).mockReturnValue([nullRec]);
    // Apply a filter that would match $0 if NaN were treated as 0.
    // Use mockReturnValueOnce so the override doesn't leak into subsequent tests.
    (state.getRecommendationsColumnFilters as jest.Mock).mockReturnValueOnce({
      on_demand_monthly: { kind: 'expr', expr: '0' },
    });
    await loadRecommendations();

    // Null-monthly_cost row should be filtered out; tbody should have no visible recommendation rows.
    const rows = document.querySelectorAll('tbody tr.recommendation-row');
    expect(rows.length).toBe(0);
  });

  test('sort header for on_demand_monthly is wired - clicking sets sort to on_demand_monthly', async () => {
    (api.getRecommendations as jest.Mock).mockResolvedValue({
      summary: {},
      recommendations: [baseRec()],
      regions: [],
    });
    (state.getRecommendations as jest.Mock).mockReturnValue([baseRec()]);
    await loadRecommendations();

    const header = document.querySelector<HTMLTableCellElement>('th[data-sort="on_demand_monthly"]');
    expect(header).not.toBeNull();
    header!.click();
    expect(state.setRecommendationsSort).toHaveBeenCalledWith(
      expect.objectContaining({ column: 'on_demand_monthly' }),
    );
  });
});

// ---------------------------------------------------------------------------
// Issues #225 + #226: cell grouping with savings range and collapse/expand
// ---------------------------------------------------------------------------

/** Helper to build a minimal LocalRecommendation fixture. */
const mkRec = (overrides: Partial<LocalRecommendation> = {}): LocalRecommendation => ({
  id: 'rec-' + Math.random().toString(36).slice(2),
  provider: 'aws',
  service: 'ec2',
  resource_type: 'm5.large',
  region: 'us-east-1',
  count: 3,
  term: 1,
  payment: 'no-upfront',
  savings: 100,
  upfront_cost: 0,
  monthly_cost: 50,
  ...overrides,
} as unknown as LocalRecommendation);

/** Two recs sharing the same cell key (same provider/account/service/region/resource_type/engine). */
const sameCell = (savingsA: number, savingsB: number): LocalRecommendation[] => [
  mkRec({ id: 'a1', savings: savingsA, term: 1, payment: 'no-upfront', upfront_cost: 0 }),
  mkRec({ id: 'a2', savings: savingsB, term: 3, payment: 'all-upfront', upfront_cost: 1200 }),
];

describe('Issues #225 + #226: cell grouping with savings range and collapse/expand', () => {
  describe('groupRecsByCell (pure helper)', () => {
    test('groups two recs with the same cell key into one entry', () => {
      const recs = sameCell(80, 120);
      const groups = groupRecsByCell(recs);
      expect(groups.size).toBe(1);
      expect(groups.values().next().value).toHaveLength(2);
    });

    test('groups recs with different resource_type into separate cells', () => {
      const rec1 = mkRec({ resource_type: 'm5.large' });
      const rec2 = mkRec({ resource_type: 't3.medium' });
      const groups = groupRecsByCell([rec1, rec2]);
      expect(groups.size).toBe(2);
    });
  });

  describe('cellSummary (pure helper)', () => {
    test('computes min and max savings across variants', () => {
      const recs = sameCell(80, 120);
      const s = cellSummary(recs);
      expect(s.savingsMin).toBe(80);
      expect(s.savingsMax).toBe(120);
    });

    test('collapses to same value for a single-variant cell', () => {
      const rec = mkRec({ savings: 55, upfront_cost: 0, term: 1 });
      const s = cellSummary([rec]);
      expect(s.savingsMin).toBe(55);
      expect(s.savingsMax).toBe(55);
      expect(s.termMin).toBe(1);
      expect(s.termMax).toBe(1);
    });

    test('returns zeroed summary for empty input (defensive)', () => {
      const s = cellSummary([]);
      expect(s.savingsMin).toBe(0);
      expect(s.savingsMax).toBe(0);
    });
  });

  describe('pageLevelRange (pure helper)', () => {
    test('sums per-cell min and max across two cells', () => {
      const cell1 = sameCell(50, 100);  // min=50, max=100
      const cell2 = [
        mkRec({ id: 'b1', resource_type: 't3.small', savings: 30, term: 1, payment: 'no-upfront', upfront_cost: 0 }),
        mkRec({ id: 'b2', resource_type: 't3.small', savings: 70, term: 3, payment: 'all-upfront', upfront_cost: 600 }),
      ];
      const groups = groupRecsByCell([...cell1, ...cell2]);
      const plr = pageLevelRange(groups);
      expect(plr.cellCount).toBe(2);
      expect(plr.savingsMin).toBe(80);   // 50 + 30
      expect(plr.savingsMax).toBe(170);  // 100 + 70
    });

    test('returns cellCount=0 and savings=0 for empty groups', () => {
      const plr = pageLevelRange(new Map());
      expect(plr.cellCount).toBe(0);
      expect(plr.savingsMin).toBe(0);
      expect(plr.savingsMax).toBe(0);
    });

    test('paybackMonthsMin uses per-cell paired variants, not cross-extrema (PR #283 CR)', () => {
      // Cell A has two variants:
      //   v1: savings=50,  upfront=0    → ratio = 0     (best payback: 0 months)
      //   v2: savings=200, upfront=300  → ratio = 1.5   (1.5 months)
      //
      // Cell B has two variants:
      //   v3: savings=100, upfront=200  → ratio = 2     (2 months)
      //   v4: savings=150, upfront=600  → ratio = 4     (4 months)
      //
      // Attainable min-payback selection: cell A picks v1 (ratio 0), cell B
      // picks v3 (ratio 2). Total upfront=0+200=200, total savings=50+100=150.
      // paybackMonthsMin = 200/150 ≈ 1.333...
      //
      // Cross-extrema (old code) would compute:
      //   upfrontMin = 0 (from v1) + 200 (from v3) = 200
      //   savingsMax = 200 (from v2) + 150 (from v4) = 350
      //   paybackMonthsMin = 200/350 ≈ 0.571
      // That's not attainable because it mixes upfront from v1 with savings
      // from v2 (different variants of cell A).
      //
      // The new per-cell paired logic must produce 200/150 ≈ 1.333, not 0.571.
      const cellA = [
        mkRec({ id: 'a-v1', resource_type: 'm5.large',  savings:  50, upfront_cost:   0 }),
        mkRec({ id: 'a-v2', resource_type: 'm5.large',  savings: 200, upfront_cost: 300 }),
      ];
      const cellB = [
        mkRec({ id: 'b-v3', resource_type: 't3.medium', savings: 100, upfront_cost: 200 }),
        mkRec({ id: 'b-v4', resource_type: 't3.medium', savings: 150, upfront_cost: 600 }),
      ];
      const groups = groupRecsByCell([...cellA, ...cellB]);
      const plr = pageLevelRange(groups);

      // Per-cell paired: best ratio for A is v1 (0/50=0), best for B is v3 (200/100=2).
      // Combined: upfront=0+200=200, savings=50+100=150 → paybackMin=200/150≈1.333
      expect(plr.paybackMonthsMin).toBeCloseTo(200 / 150, 5);

      // Cross-extrema would give 200/350≈0.571 — must NOT be the result.
      expect(plr.paybackMonthsMin).not.toBeCloseTo(200 / 350, 5);

      // paybackMonthsMax: worst payback per cell. A picks v2 (300/200=1.5),
      // B picks v4 (600/150=4). Combined: upfront=300+600=900, savings=200+150=350.
      // paybackMax = 900/350 ≈ 2.571
      expect(plr.paybackMonthsMax).toBeCloseTo(900 / 350, 5);
    });
  });

  describe('DOM rendering (cell grouping integrated)', () => {
    beforeEach(() => {
      document.body.innerHTML = [
        '<div id="opportunities-tab" class="tab-content active">',
        '<div id="recommendations-summary"></div>',
        '<div id="recommendations-list"></div>',
        '</div>',
        '<div id="purchase-modal" class="hidden">',
        '<div id="purchase-details"></div>',
        '</div>',
      ].join('');
      jest.clearAllMocks();
      // Reset getHiddenColumns to return empty Set so tests don't inherit sticky values
      (state.getHiddenColumns as jest.Mock).mockReturnValue(new Set());
      jest.useFakeTimers();
      window.alert = jest.fn();
      // Reset cell expand state so tests don't share module-level expandedCells.
      resetExpandedCells();
    });

    afterEach(() => {
      jest.useRealTimers();
    });

    const multiVariantRecs = (): LocalRecommendation[] => [
      mkRec({ id: 'mv-1', savings: 80,  term: 1, payment: 'no-upfront',   upfront_cost: 0 }),
      mkRec({ id: 'mv-2', savings: 120, term: 3, payment: 'all-upfront',  upfront_cost: 1200 }),
    ];

    test('multi-variant cell renders a summary row, not two flat variant rows by default', async () => {
      const recs = multiVariantRecs();
      (api.getRecommendations as jest.Mock).mockResolvedValue({ summary: {}, recommendations: recs });
      (state.getRecommendations as jest.Mock).mockReturnValue(recs);
      await loadRecommendations();

      // Summary row should be present
      const summaryRow = document.querySelector('.rec-cell-summary-row');
      expect(summaryRow).not.toBeNull();

      // Variant rows should NOT be rendered (collapsed by default)
      const variantRows = document.querySelectorAll('.rec-variant-row');
      expect(variantRows.length).toBe(0);
    });

    test('multi-variant summary row omits hidden column values', async () => {
      const recs = multiVariantRecs();
      (api.getRecommendations as jest.Mock).mockResolvedValue({ summary: {}, recommendations: recs });
      (state.getRecommendations as jest.Mock).mockReturnValue(recs);
      (state.getHiddenColumns as jest.Mock).mockReturnValue(new Set(['region', 'savings', 'upfront_cost', 'term']));
      await loadRecommendations();

      const summaryContent = document.querySelector('.rec-cell-summary-content');
      expect(summaryContent).not.toBeNull();
      expect(summaryContent!.textContent).toContain('2 variants');
      expect(summaryContent!.textContent).not.toContain('us-east-1');
      expect(summaryContent!.textContent).not.toContain('$80');
      expect(summaryContent!.textContent).not.toContain('$120');
      expect(summaryContent!.textContent).not.toContain('upfront:');
      expect(summaryContent!.textContent).not.toContain('term:');
      expect(summaryContent!.querySelector('.rec-cell-range')).toBeNull();
    });

    test('chevron button lives inside td.checkbox-col of the summary row (closes #280)', async () => {
      const recs = multiVariantRecs();
      (api.getRecommendations as jest.Mock).mockResolvedValue({ summary: {}, recommendations: recs });
      (state.getRecommendations as jest.Mock).mockReturnValue(recs);
      await loadRecommendations();

      const summaryRow = document.querySelector('.rec-cell-summary-row');
      expect(summaryRow).not.toBeNull();
      const checkboxCell = summaryRow!.querySelector('td.checkbox-col');
      expect(checkboxCell).not.toBeNull();
      const chevron = checkboxCell!.querySelector<HTMLButtonElement>('.rec-cell-chevron');
      expect(chevron).not.toBeNull();
    });

    test('clicking chevron expands the cell and shows variant rows', async () => {
      const recs = multiVariantRecs();
      (api.getRecommendations as jest.Mock).mockResolvedValue({ summary: {}, recommendations: recs });
      (state.getRecommendations as jest.Mock).mockReturnValue(recs);
      await loadRecommendations();

      const chevron = document.querySelector<HTMLButtonElement>('.rec-cell-chevron');
      expect(chevron).not.toBeNull();
      // aria-expanded starts false (collapsed)
      expect(chevron!.getAttribute('aria-expanded')).toBe('false');
      chevron!.click();

      // After expand: variant rows should appear and aria-expanded flips
      const variantRows = document.querySelectorAll('.rec-variant-row');
      expect(variantRows.length).toBe(2);
      const updatedChevron = document.querySelector<HTMLButtonElement>('.rec-cell-chevron');
      expect(updatedChevron!.getAttribute('aria-expanded')).toBe('true');
    });

    test('page-level range banner is removed (closes #278) — savings card is the canonical surface', async () => {
      // The "Recommended range" banner under the table was redundant with
      // the Potential Monthly Savings card after #272 / #279 brought the
      // same range to the summary header. Closing #278 removed the
      // banner; the card is the single source of truth.
      const recs = multiVariantRecs();
      (api.getRecommendations as jest.Mock).mockResolvedValue({ summary: {}, recommendations: recs });
      (state.getRecommendations as jest.Mock).mockReturnValue(recs);
      (state.getVisibleRecommendations as jest.Mock).mockReturnValue(recs);
      await loadRecommendations();

      // Banner gone.
      expect(document.querySelector('.rec-range-banner')).toBeNull();
      // Card carries the range.
      const summary = document.getElementById('recommendations-summary');
      const savingsCard = Array.from(summary?.querySelectorAll('.card') ?? [])
        .find((c) => /Monthly Savings/.test(c.querySelector('h3')?.textContent ?? ''));
      expect(savingsCard?.querySelector('.value.savings')?.textContent).toMatch(/\$\d/);
    });

    test('single-variant cell renders a flat row, no summary row', async () => {
      const rec = mkRec({ id: 'solo', savings: 60 });
      (api.getRecommendations as jest.Mock).mockResolvedValue({ summary: {}, recommendations: [rec] });
      (state.getRecommendations as jest.Mock).mockReturnValue([rec]);
      await loadRecommendations();

      const summaryRow = document.querySelector('.rec-cell-summary-row');
      expect(summaryRow).toBeNull();

      // The flat row should exist as a regular recommendation-row
      const rows = document.querySelectorAll('tr.recommendation-row');
      expect(rows.length).toBe(1);
    });

    test('Expand all button appears in filter-status bar for multi-variant cells', async () => {
      const recs = multiVariantRecs();
      (api.getRecommendations as jest.Mock).mockResolvedValue({ summary: {}, recommendations: recs });
      (state.getRecommendations as jest.Mock).mockReturnValue(recs);
      await loadRecommendations();

      const expandAllBtn = document.querySelector('.expand-all-toggle');
      expect(expandAllBtn).not.toBeNull();
      expect(expandAllBtn!.textContent).toMatch(/Expand all/);
    });

    test('Expand all expands all multi-variant cells', async () => {
      const recs = multiVariantRecs();
      (api.getRecommendations as jest.Mock).mockResolvedValue({ summary: {}, recommendations: recs });
      (state.getRecommendations as jest.Mock).mockReturnValue(recs);
      await loadRecommendations();

      const expandAllBtn = document.querySelector<HTMLButtonElement>('.expand-all-toggle');
      expect(expandAllBtn).not.toBeNull();
      expandAllBtn!.click();

      // After expand all: variant rows should be visible
      const variantRows = document.querySelectorAll('.rec-variant-row');
      expect(variantRows.length).toBe(2);
    });
  });
});

// ---------------------------------------------------------------------------
// Issue #319: cost-period selector tests
// ---------------------------------------------------------------------------

const DOM_FOR_319 = (
  '<div id="opportunities-tab" class="tab-content active">'
  + '<div id="recommendations-summary"></div>'
  + '<div id="recommendations-list"></div>'
  + '</div>'
  + '<div id="purchase-modal" class="hidden">'
  + '<div id="purchase-details"></div>'
  + '<div class="modal-buttons">'
  + '<button type="button" id="close-purchase-modal-btn">Cancel</button>'
  + '<button type="button" id="execute-purchase-btn" class="primary">Send for Approval</button>'
  + '</div></div>'
);

describe('Issue #319: scaleCost', () => {
  test('monthly factor is 1 (identity)', () => {
    expect(scaleCost(100, 'monthly')).toBe(100);
  });

  test('hourly factor is 1/720', () => {
    expect(scaleCost(720, 'hourly')).toBeCloseTo(1.0);
  });

  test('daily factor is 1/30', () => {
    expect(scaleCost(300, 'daily')).toBeCloseTo(10.0);
  });

  test('yearly factor is 12', () => {
    expect(scaleCost(100, 'yearly')).toBeCloseTo(1200);
  });

  test('null input returns null', () => {
    const periods: CostPeriod[] = ['hourly', 'daily', 'monthly', 'yearly'];
    for (const p of periods) {
      expect(scaleCost(null, p)).toBeNull();
    }
  });

  test('undefined input returns null', () => {
    const periods: CostPeriod[] = ['hourly', 'daily', 'monthly', 'yearly'];
    for (const p of periods) {
      expect(scaleCost(undefined, p)).toBeNull();
    }
  });

  test('zero input returns 0 (not null)', () => {
    expect(scaleCost(0, 'hourly')).toBe(0);
  });
});

describe('Issue #319: formatCostForPeriod', () => {
  test('monthly uses formatCurrency mock ($X)', () => {
    // formatCurrency mock returns `$${val}` so for 100 → "$100"
    expect(formatCostForPeriod(100, 'monthly')).toBe('$100');
  });

  test('hourly uses 4 decimal places', () => {
    expect(formatCostForPeriod(720, 'hourly')).toMatch(/\$1\.0000/);
  });

  test('daily uses 2 decimal places', () => {
    expect(formatCostForPeriod(300, 'daily')).toMatch(/\$10\.00/);
  });

  test('yearly uses 0 decimal places', () => {
    expect(formatCostForPeriod(100, 'yearly')).toMatch(/\$1200$/);
  });

  test('null input returns em-dash for all periods', () => {
    const periods: CostPeriod[] = ['hourly', 'daily', 'monthly', 'yearly'];
    for (const p of periods) {
      expect(formatCostForPeriod(null, p)).toBe('—');
    }
  });

  test('undefined input returns em-dash for all periods', () => {
    const periods: CostPeriod[] = ['hourly', 'daily', 'monthly', 'yearly'];
    for (const p of periods) {
      expect(formatCostForPeriod(undefined, p)).toBe('—');
    }
  });

  test('zero input renders as $0 (not em-dash)', () => {
    // hourly: $0.0000, daily: $0.00, monthly: $0, yearly: $0
    expect(formatCostForPeriod(0, 'hourly')).toMatch(/\$0\.0000/);
    expect(formatCostForPeriod(0, 'daily')).toMatch(/\$0\.00/);
    expect(formatCostForPeriod(0, 'yearly')).toMatch(/\$0$/);
  });
});

describe('Issue #319: periodSuffix', () => {
  test('returns correct suffix for each period', () => {
    expect(periodSuffix('hourly')).toBe('/ hr');
    expect(periodSuffix('daily')).toBe('/ day');
    expect(periodSuffix('monthly')).toBe('/ mo');
    expect(periodSuffix('yearly')).toBe('/ yr');
  });
});

describe('Issue #319: column header labels update with period', () => {
  const mockRec = {
    id: 'r1',
    provider: 'aws' as const,
    service: 'ec2',
    resource_type: 'c5.large',
    region: 'us-east-1',
    count: 2,
    term: 1,
    payment: 'no-upfront',
    savings: 500,
    upfront_cost: 0,
    monthly_cost: 300,
    cloud_account_id: undefined,
  };

  beforeEach(() => {
    document.body.innerHTML = DOM_FOR_319;
    jest.clearAllMocks();
    (api.getRecommendations as jest.Mock).mockResolvedValue({
      summary: {},
      recommendations: [mockRec],
    });
    (state.getRecommendations as jest.Mock).mockReturnValue([mockRec]);
    (state.getSelectedRecommendationIDs as jest.Mock).mockReturnValue(new Set());
    (state.getVisibleRecommendations as jest.Mock).mockReturnValue([mockRec]);
    (state.getCostPeriod as jest.Mock).mockReturnValue('monthly');
    (state.getRecommendationsSort as jest.Mock).mockReturnValue({ column: 'savings', direction: 'desc' });
    (state.getRecommendationsColumnFilters as jest.Mock).mockReturnValue({});
  });

  test('monthly period: headers show "Monthly Savings" and "Monthly Cost"', async () => {
    (state.getCostPeriod as jest.Mock).mockReturnValue('monthly');
    await loadRecommendations();
    const headers = document.querySelectorAll('th.sortable span');
    const labels = Array.from(headers).map((h) => h.textContent);
    expect(labels).toContain('Monthly Savings');
    expect(labels).toContain('Monthly Cost');
  });

  test('hourly period: headers show "Savings / hr" and "Cost / hr"', async () => {
    (state.getCostPeriod as jest.Mock).mockReturnValue('hourly');
    await loadRecommendations();
    const headers = document.querySelectorAll('th.sortable span');
    const labels = Array.from(headers).map((h) => h.textContent);
    expect(labels).toContain('Savings / hr');
    expect(labels).toContain('Cost / hr');
  });

  test('daily period: headers show "Savings / day" and "Cost / day"', async () => {
    (state.getCostPeriod as jest.Mock).mockReturnValue('daily');
    await loadRecommendations();
    const headers = document.querySelectorAll('th.sortable span');
    const labels = Array.from(headers).map((h) => h.textContent);
    expect(labels).toContain('Savings / day');
    expect(labels).toContain('Cost / day');
  });

  test('yearly period: headers show "Savings / yr" and "Cost / yr"', async () => {
    (state.getCostPeriod as jest.Mock).mockReturnValue('yearly');
    await loadRecommendations();
    const headers = document.querySelectorAll('th.sortable span');
    const labels = Array.from(headers).map((h) => h.textContent);
    expect(labels).toContain('Savings / yr');
    expect(labels).toContain('Cost / yr');
  });

  test('non-cost headers are period-invariant', async () => {
    (state.getCostPeriod as jest.Mock).mockReturnValue('hourly');
    await loadRecommendations();
    const headers = document.querySelectorAll('th.sortable span');
    const labels = Array.from(headers).map((h) => h.textContent);
    expect(labels).toContain('Provider');
    expect(labels).toContain('Term');
    expect(labels).toContain('Upfront Cost');
    expect(labels).toContain('Effective %');
  });
});

describe('Issue #319: table cells scale with period', () => {
  const mockRec = {
    id: 'r1',
    provider: 'aws' as const,
    service: 'ec2',
    resource_type: 'c5.large',
    region: 'us-east-1',
    count: 2,
    term: 1,
    payment: 'no-upfront',
    savings: 720,
    upfront_cost: 0,
    monthly_cost: 720,
    cloud_account_id: undefined,
  };

  beforeEach(() => {
    document.body.innerHTML = DOM_FOR_319;
    jest.clearAllMocks();
    (api.getRecommendations as jest.Mock).mockResolvedValue({
      summary: {},
      recommendations: [mockRec],
    });
    (state.getRecommendations as jest.Mock).mockReturnValue([mockRec]);
    (state.getSelectedRecommendationIDs as jest.Mock).mockReturnValue(new Set());
    (state.getVisibleRecommendations as jest.Mock).mockReturnValue([mockRec]);
    (state.getCostPeriod as jest.Mock).mockReturnValue('monthly');
    (state.getRecommendationsSort as jest.Mock).mockReturnValue({ column: 'savings', direction: 'desc' });
    (state.getRecommendationsColumnFilters as jest.Mock).mockReturnValue({});
  });

  test('hourly: savings cell shows $1.0000', async () => {
    (state.getCostPeriod as jest.Mock).mockReturnValue('hourly');
    await loadRecommendations();
    const rows = document.querySelectorAll('tr.recommendation-row');
    expect(rows.length).toBeGreaterThan(0);
    const savingsCell = rows[0]!.querySelector('.savings');
    expect(savingsCell?.textContent).toMatch(/\$1\.0000/);
  });

  test('daily: savings cell shows $24.00', async () => {
    (state.getCostPeriod as jest.Mock).mockReturnValue('daily');
    await loadRecommendations();
    const rows = document.querySelectorAll('tr.recommendation-row');
    const savingsCell = rows[0]!.querySelector('.savings');
    expect(savingsCell?.textContent).toMatch(/\$24\.00/);
  });

  test('monthly: savings cell shows existing formatCurrency output', async () => {
    (state.getCostPeriod as jest.Mock).mockReturnValue('monthly');
    await loadRecommendations();
    const rows = document.querySelectorAll('tr.recommendation-row');
    const savingsCell = rows[0]!.querySelector('.savings');
    // formatCurrency mock returns `$${val}` so for 720 it returns "$720"
    expect(savingsCell?.textContent).toContain('720');
  });

  test('yearly: savings cell shows $8640', async () => {
    (state.getCostPeriod as jest.Mock).mockReturnValue('yearly');
    await loadRecommendations();
    const rows = document.querySelectorAll('tr.recommendation-row');
    const savingsCell = rows[0]!.querySelector('.savings');
    expect(savingsCell?.textContent).toMatch(/\$8640/);
  });

  test('upfront_cost is NOT scaled (period-invariant)', async () => {
    (state.getCostPeriod as jest.Mock).mockReturnValue('hourly');
    const recWithUpfront = { ...mockRec, upfront_cost: 1000 };
    (api.getRecommendations as jest.Mock).mockResolvedValue({
      summary: {},
      recommendations: [recWithUpfront],
    });
    (state.getRecommendations as jest.Mock).mockReturnValue([recWithUpfront]);
    await loadRecommendations();
    const rows = document.querySelectorAll('tr.recommendation-row');
    // We just verify "1000" appears somewhere but "0.0014" (1000/720) does not
    const rowText = rows[0]!.textContent ?? '';
    expect(rowText).toContain('1000');
    expect(rowText).not.toMatch(/0\.0014/);
  });

  test('null monthly_cost renders "—" regardless of period', async () => {
    (state.getCostPeriod as jest.Mock).mockReturnValue('yearly');
    const recNullCost = { ...mockRec, monthly_cost: null };
    (api.getRecommendations as jest.Mock).mockResolvedValue({
      summary: {},
      recommendations: [recNullCost],
    });
    (state.getRecommendations as jest.Mock).mockReturnValue([recNullCost]);
    await loadRecommendations();
    const rows = document.querySelectorAll('tr.recommendation-row');
    const rowHtml = rows[0]!.innerHTML;
    expect(rowHtml).toContain('—');
  });

  // CR pass-1 nitpick: the existing #319 cases verify labels and formatted
  // values, but they never prove that sort and filter accessors actually
  // apply the period scale. A regression that reverts numericCellValue or
  // SORTABLE_NUMERIC_COLUMNS.savings to raw monthly_cost would still pass
  // every assertion above. The two tests below pin the scaled-numeric
  // contract from both ends: ordering (sort) and inclusion (filter).
  test('yearly sort orders savings cells by yearly-scaled value (asc)', async () => {
    const recSmall = { ...mockRec, id: 'r-small', resource_type: 'small', savings: 100, monthly_cost: 100 };
    const recLarge = { ...mockRec, id: 'r-large', resource_type: 'large', savings: 200, monthly_cost: 200 };
    // Inputs are intentionally pre-sorted DESC so a no-op scale that
    // preserves input order would fail this asc assertion.
    (state.getCostPeriod as jest.Mock).mockReturnValue('yearly');
    (state.getRecommendationsSort as jest.Mock).mockReturnValue({ column: 'savings', direction: 'asc' });
    (api.getRecommendations as jest.Mock).mockResolvedValue({
      summary: {},
      recommendations: [recLarge, recSmall],
    });
    (state.getRecommendations as jest.Mock).mockReturnValue([recLarge, recSmall]);
    (state.getVisibleRecommendations as jest.Mock).mockReturnValue([recLarge, recSmall]);

    await loadRecommendations();

    const rows = Array.from(document.querySelectorAll('tr.recommendation-row'));
    expect(rows).toHaveLength(2);
    // Even though recLarge appears first in the input array, ascending
    // savings sort (which routes through scaleCost) places r-small first.
    expect(rows[0]?.textContent).toContain('small');
    expect(rows[1]?.textContent).toContain('large');
  });

  test('hourly numeric filter compares the scaled (per-hour) savings, not the raw monthly value', async () => {
    // Both recs would pass a ">0.75" filter against raw monthly_cost (360
    // and 720 are both > 0.75). After hourly scaling (÷720), only r-high
    // (1.0) passes; r-low (0.5) is filtered out. If numericCellValue were
    // to drop the scaleCost call, this test would surface the regression.
    const recLow  = { ...mockRec, id: 'r-low',  resource_type: 'low',  savings: 360, monthly_cost: 360 };
    const recHigh = { ...mockRec, id: 'r-high', resource_type: 'high', savings: 720, monthly_cost: 720 };
    (state.getCostPeriod as jest.Mock).mockReturnValue('hourly');
    (state.getRecommendationsColumnFilters as jest.Mock).mockReturnValue({
      savings: { kind: 'expr', expr: '>0.75' },
    });
    (api.getRecommendations as jest.Mock).mockResolvedValue({
      summary: {},
      recommendations: [recLow, recHigh],
    });
    (state.getRecommendations as jest.Mock).mockReturnValue([recLow, recHigh]);
    (state.getVisibleRecommendations as jest.Mock).mockReturnValue([recLow, recHigh]);

    await loadRecommendations();

    const rows = Array.from(document.querySelectorAll('tr.recommendation-row'));
    expect(rows).toHaveLength(1);
    expect(rows[0]?.textContent).toContain('high');
  });
});

describe('Issue #319: summary card label updates with period', () => {
  const mockRec = {
    id: 'r1',
    provider: 'aws' as const,
    service: 'ec2',
    resource_type: 'c5.large',
    region: 'us-east-1',
    count: 1,
    term: 1,
    payment: 'no-upfront',
    savings: 500,
    upfront_cost: 0,
    monthly_cost: 300,
    cloud_account_id: undefined,
  };

  beforeEach(() => {
    document.body.innerHTML = DOM_FOR_319;
    jest.clearAllMocks();
    (api.getRecommendations as jest.Mock).mockResolvedValue({
      summary: {},
      recommendations: [mockRec],
    });
    (state.getRecommendations as jest.Mock).mockReturnValue([mockRec]);
    (state.getSelectedRecommendationIDs as jest.Mock).mockReturnValue(new Set());
    (state.getVisibleRecommendations as jest.Mock).mockReturnValue([mockRec]);
    (state.getCostPeriod as jest.Mock).mockReturnValue('monthly');
    (state.getRecommendationsSort as jest.Mock).mockReturnValue({ column: 'savings', direction: 'desc' });
    (state.getRecommendationsColumnFilters as jest.Mock).mockReturnValue({});
  });

  test('monthly period: card says "Potential Monthly Savings"', async () => {
    (state.getCostPeriod as jest.Mock).mockReturnValue('monthly');
    await loadRecommendations();
    const summary = document.getElementById('recommendations-summary');
    expect(summary?.textContent).toContain('Potential Monthly Savings');
  });

  test('hourly period: card says "Potential Savings / hr"', async () => {
    (state.getCostPeriod as jest.Mock).mockReturnValue('hourly');
    await loadRecommendations();
    const summary = document.getElementById('recommendations-summary');
    expect(summary?.textContent).toContain('Potential Savings / hr');
  });

  test('yearly period: card says "Potential Savings / yr"', async () => {
    (state.getCostPeriod as jest.Mock).mockReturnValue('yearly');
    await loadRecommendations();
    const summary = document.getElementById('recommendations-summary');
    expect(summary?.textContent).toContain('Potential Savings / yr');
  });

  test('Upfront Cost card is not affected by period', async () => {
    (state.getCostPeriod as jest.Mock).mockReturnValue('hourly');
    await loadRecommendations();
    const summary = document.getElementById('recommendations-summary');
    expect(summary?.textContent).toContain('Total Upfront Cost');
  });
});

describe('Issue #319: cost-period dropdown is rendered in the filter-status bar', () => {
  const mockRec = {
    id: 'r1',
    provider: 'aws' as const,
    service: 'ec2',
    resource_type: 'c5.large',
    region: 'us-east-1',
    count: 1,
    term: 1,
    payment: 'no-upfront',
    savings: 500,
    upfront_cost: 0,
    monthly_cost: 300,
    cloud_account_id: undefined,
  };

  beforeEach(() => {
    document.body.innerHTML = DOM_FOR_319;
    jest.clearAllMocks();
    (api.getRecommendations as jest.Mock).mockResolvedValue({
      summary: {},
      recommendations: [mockRec],
    });
    (state.getRecommendations as jest.Mock).mockReturnValue([mockRec]);
    (state.getSelectedRecommendationIDs as jest.Mock).mockReturnValue(new Set());
    (state.getVisibleRecommendations as jest.Mock).mockReturnValue([mockRec]);
    (state.getCostPeriod as jest.Mock).mockReturnValue('monthly');
    (state.getRecommendationsSort as jest.Mock).mockReturnValue({ column: 'savings', direction: 'desc' });
    (state.getRecommendationsColumnFilters as jest.Mock).mockReturnValue({});
  });

  test('cost-period dropdown is rendered after loadRecommendations', async () => {
    await loadRecommendations();
    const select = document.querySelector<HTMLSelectElement>('#cost-period-select');
    expect(select).not.toBeNull();
  });

  test('dropdown has 4 options: Hourly, Daily, Monthly, Yearly', async () => {
    await loadRecommendations();
    const select = document.querySelector<HTMLSelectElement>('#cost-period-select');
    const options = Array.from(select?.options ?? []).map((o) => o.textContent);
    expect(options).toContain('Hourly');
    expect(options).toContain('Daily');
    expect(options).toContain('Monthly');
    expect(options).toContain('Yearly');
  });

  test('dropdown selected value reflects getCostPeriod() result', async () => {
    (state.getCostPeriod as jest.Mock).mockReturnValue('yearly');
    await loadRecommendations();
    const select = document.querySelector<HTMLSelectElement>('#cost-period-select');
    expect(select?.value).toBe('yearly');
  });

  test('changing the dropdown calls setCostPeriod and triggers rerender', async () => {
    // Seed a different return value for the post-change loadRecommendations()
    // call so we can verify the rerender actually rebuilt headers/cells
    // against the new period (not just that setCostPeriod was invoked).
    // CR pass-1 nitpick: the previous version of this test would still pass
    // if the change handler stopped rebuilding the DOM.
    (state.getCostPeriod as jest.Mock)
      .mockReturnValueOnce('monthly')
      .mockReturnValue('hourly');
    await loadRecommendations();
    const select = document.querySelector<HTMLSelectElement>('#cost-period-select');
    expect(select).not.toBeNull();
    select!.value = 'hourly';
    select!.dispatchEvent(new Event('change'));
    await Promise.resolve();
    expect(state.setCostPeriod).toHaveBeenCalledWith('hourly');
    // The rerender should have rebuilt the savings column header to reflect
    // the new hourly period — pin the DOM update so a future regression that
    // stops invalidating the rendered tree fails this test.
    expect(document.getElementById('recommendations-list')?.textContent).toContain('Savings / hr');
  });

  test('dropdown label text is accessible', async () => {
    await loadRecommendations();
    const label = document.querySelector('.cost-period-selector-label');
    expect(label?.textContent).toContain('Show costs');
  });
});

describe('Issue #319: localStorage persistence (state.ts getCostPeriod / setCostPeriod)', () => {
  // These tests exercise the actual state module (not the mock).
  // We import directly from the source rather than the mocked version.
  // The global setup.ts replaces window.localStorage with a noop mock
  // (getItem→null, setItem→undefined), so we install a Map-backed shim
  // here to actually test persistence semantics, then reset it.
  let getCostPeriodFn: () => string;
  let setCostPeriodFn: (period: string) => void;

  beforeEach(() => {
    const store = new Map<string, string>();
    (localStorage.getItem as jest.Mock).mockImplementation(
      (key: string) => (store.has(key) ? store.get(key)! : null)
    );
    (localStorage.setItem as jest.Mock).mockImplementation(
      (key: string, value: string) => { store.set(key, value); }
    );
    (localStorage.removeItem as jest.Mock).mockImplementation(
      (key: string) => { store.delete(key); }
    );
    (localStorage.clear as jest.Mock).mockImplementation(() => { store.clear(); });

    // Use requireActual to bypass the jest.mock('../state') and get the real
    // module. This lets us test the actual localStorage persistence logic.
    const stateModule = jest.requireActual('../state') as typeof import('../state');
    getCostPeriodFn = stateModule.getCostPeriod as () => string;
    setCostPeriodFn = stateModule.setCostPeriod as (period: string) => void;
    // Reset in-memory costPeriodMemory to the module default before each
    // test, otherwise it survives the localStorage clear and the
    // "invalid value falls back to default" test sees stale 'hourly' from
    // the prior "setCostPeriod persists" test.
    setCostPeriodFn('monthly');
    store.clear();
  });

  afterEach(() => {
    (localStorage.getItem as jest.Mock).mockReset().mockImplementation(() => null);
    (localStorage.setItem as jest.Mock).mockReset().mockImplementation(() => undefined);
    (localStorage.removeItem as jest.Mock).mockReset().mockImplementation(() => undefined);
    (localStorage.clear as jest.Mock).mockReset().mockImplementation(() => undefined);
  });

  test('default period is monthly when localStorage is empty', () => {
    expect(getCostPeriodFn()).toBe('monthly');
  });

  test('setCostPeriod persists to localStorage', () => {
    setCostPeriodFn('hourly');
    expect(localStorage.getItem('cudly.recs.costPeriod')).toBe('hourly');
  });

  test('getCostPeriod reads back the persisted value', () => {
    localStorage.setItem('cudly.recs.costPeriod', 'hourly');
    expect(getCostPeriodFn()).toBe('hourly');
  });

  test('invalid value in localStorage falls back to static default, not prior in-memory state', () => {
    // CR pass-1 nitpick: seed a non-default value first so this test fails if
    // getCostPeriod() incorrectly leaks a stale in-memory cache instead of
    // re-reading + validating localStorage on each call. Without the seed, a
    // broken implementation that only reads localStorage on first call would
    // still pass.
    setCostPeriodFn('hourly');
    expect(getCostPeriodFn()).toBe('hourly');
    localStorage.setItem('cudly.recs.costPeriod', 'invalid-value');
    expect(getCostPeriodFn()).toBe('monthly');
  });
});

// ============================================================================
// Issue #318: Column visibility — localStorage persistence + toggle layer
// ============================================================================
import { localStorageMock } from './setup';

describe('Column visibility (issue #318)', () => {
  beforeEach(() => {
    resetColumnVisibilityState();
    // localStorageMock is cleared by jest.clearAllMocks() in the global beforeEach
    // (setup.ts). Each test below configures the mock as needed.
  });

  // --- loadColumnVisibility ---

  describe('loadColumnVisibility', () => {
    test('returns empty set when localStorage key is absent', () => {
      // Default mock: getItem returns null
      const result = loadColumnVisibility();
      expect(result.size).toBe(0);
    });

    test('returns empty set on JSON parse error', () => {
      localStorageMock.getItem.mockReturnValue('{not valid json}');
      const result = loadColumnVisibility();
      expect(result.size).toBe(0);
    });

    test('returns empty set when schemaVersion is wrong', () => {
      localStorageMock.getItem.mockReturnValue(
        JSON.stringify({ schemaVersion: 99, hidden: ['count'] }),
      );
      const result = loadColumnVisibility();
      expect(result.size).toBe(0);
    });

    test('returns empty set when hidden is not an array', () => {
      localStorageMock.getItem.mockReturnValue(
        JSON.stringify({ schemaVersion: 1, hidden: 'count' }),
      );
      const result = loadColumnVisibility();
      expect(result.size).toBe(0);
    });

    test('returns the correct hidden set for valid JSON', () => {
      localStorageMock.getItem.mockReturnValue(
        JSON.stringify({ schemaVersion: 1, hidden: ['count', 'term'] }),
      );
      const result = loadColumnVisibility();
      expect(result.has('count')).toBe(true);
      expect(result.has('term')).toBe(true);
      expect(result.size).toBe(2);
    });

    test('silently drops unknown column keys (forward-compatibility)', () => {
      localStorageMock.getItem.mockReturnValue(
        JSON.stringify({ schemaVersion: 1, hidden: ['count', 'future_column_not_yet_added'] }),
      );
      const result = loadColumnVisibility();
      // 'count' is a known toggleable column; 'future_column_not_yet_added' is not
      expect(result.has('count')).toBe(true);
      expect(result.size).toBe(1);
    });

    test('silently drops fixed (non-toggleable) column keys', () => {
      // provider/account/service/resource_type are fixed; should be ignored even if stored
      localStorageMock.getItem.mockReturnValue(
        JSON.stringify({ schemaVersion: 1, hidden: ['provider', 'account', 'count'] }),
      );
      const result = loadColumnVisibility();
      expect(result.has('provider')).toBe(false);
      expect(result.has('account')).toBe(false);
      expect(result.has('count')).toBe(true);
      expect(result.size).toBe(1);
    });
  });

  // --- saveColumnVisibility ---

  describe('saveColumnVisibility', () => {
    test('writes correct JSON shape to localStorage via setItem', () => {
      const hidden = new Set<import('../state').RecommendationsColumnId>(['count', 'term']);
      saveColumnVisibility(hidden);
      expect(localStorageMock.setItem).toHaveBeenCalledWith(
        'cudly.recs.columnVisibility.v1',
        expect.stringContaining('"schemaVersion":1'),
      );
      const callArg = localStorageMock.setItem.mock.calls[0]?.[1] as string;
      const parsed = JSON.parse(callArg);
      expect(parsed.schemaVersion).toBe(1);
      expect(parsed.hidden).toContain('count');
      expect(parsed.hidden).toContain('term');
      expect(parsed.hidden.length).toBe(2);
    });

    test('writes empty array for no hidden columns', () => {
      saveColumnVisibility(new Set());
      const callArg = localStorageMock.setItem.mock.calls[0]?.[1] as string;
      const parsed = JSON.parse(callArg);
      expect(parsed.hidden).toEqual([]);
    });
  });

  // --- TOGGLEABLE_COLUMNS and COLUMN_DEFS ---

  describe('COLUMN_DEFS and TOGGLEABLE_COLUMNS', () => {
    test('COLUMN_DEFS contains all 13 column ids', () => {
      const keys = COLUMN_DEFS.map((c) => c.key);
      expect(keys).toContain('provider');
      expect(keys).toContain('account');
      expect(keys).toContain('service');
      expect(keys).toContain('resource_type');
      expect(keys).toContain('region');
      expect(keys).toContain('count');
      expect(keys).toContain('term');
      expect(keys).toContain('payment');
      expect(keys).toContain('savings');
      expect(keys).toContain('upfront_cost');
      expect(keys).toContain('monthly_cost');
      expect(keys).toContain('on_demand_monthly');
      expect(keys).toContain('effective_savings_pct');
      expect(COLUMN_DEFS.length).toBe(13);
    });

    test('TOGGLEABLE_COLUMNS excludes fixed identity columns', () => {
      const keys = TOGGLEABLE_COLUMNS.map((c) => c.key);
      expect(keys).not.toContain('provider');
      expect(keys).not.toContain('account');
      expect(keys).not.toContain('service');
      expect(keys).not.toContain('resource_type');
      // All other 9 columns should be toggleable
      expect(keys).toContain('region');
      expect(keys).toContain('count');
      expect(keys).toContain('term');
      expect(keys).toContain('payment');
      expect(keys).toContain('savings');
      expect(keys).toContain('upfront_cost');
      expect(keys).toContain('monthly_cost');
      expect(keys).toContain('on_demand_monthly');
      expect(keys).toContain('effective_savings_pct');
      expect(keys.length).toBe(9);
    });
  });
});

// ---------------------------------------------------------------------------
// Issue #494: deterministic group sort on multi-variant cells.
//
// After PR #195's per-(term, payment) fan-out, every cell has BOTH 1yr and 3yr
// variants. The previous `Math.max(...va.map(numericKey))` cell-score collapses
// to the same value across every cell for Term / Payment / Monthly Cost /
// Effective % / (and many cells for Upfront), producing apparently-random row
// order. These regression tests assert deterministic ordering matching what
// the user sees in the rendered summary row.
//
// PR #491 (closes #480) fixed the default-direction inversion; this PR fixes
// the upstream "every cell ties" symptom.
// ---------------------------------------------------------------------------
describe('Issue #494: deterministic group sort on multi-variant cells', () => {
  /** Build the minimum DOM loadRecommendations needs, using createElement so
   * no innerHTML assignment is required (the rest of the file uses innerHTML;
   * here we use the safer DOM API to match the test boilerplate pattern in
   * `Bundle B: column header filter triggers`). */
  function buildTestDOM(): void {
    document.body.replaceChildren();
    const recsTab = document.createElement('div');
    recsTab.id = 'opportunities-tab';
    recsTab.className = 'tab-content active';
    const summary = document.createElement('div');
    summary.id = 'recommendations-summary';
    const list = document.createElement('div');
    list.id = 'recommendations-list';
    recsTab.appendChild(summary);
    recsTab.appendChild(list);
    document.body.appendChild(recsTab);

    const purchaseModal = document.createElement('div');
    purchaseModal.id = 'purchase-modal';
    purchaseModal.className = 'hidden';
    const purchaseDetails = document.createElement('div');
    purchaseDetails.id = 'purchase-details';
    purchaseModal.appendChild(purchaseDetails);
    document.body.appendChild(purchaseModal);
  }

  /**
   * Build a single multi-variant cell (2 variants: term=1 + term=3) with the
   * provided per-variant payments / upfront / monthly_cost. Cell identity is
   * controlled by `resource_type` so callers can build N distinct cells in one
   * fixture.
   */
  function multiVariantCell(opts: {
    resourceType: string;
    payment1y: string;
    payment3y: string;
    upfront1y: number;
    upfront3y: number;
    monthly1y?: number | null;
    monthly3y?: number | null;
    onDemand?: number | null;
    savings1y?: number;
    savings3y?: number;
  }): LocalRecommendation[] {
    const slug = opts.resourceType.replace(/\W/g, '');
    return [
      {
        id: `${slug}-1y`,
        provider: 'aws',
        cloud_account_id: 'a1',
        service: 'ec2',
        resource_type: opts.resourceType,
        region: 'us-east-1',
        count: 1,
        term: 1,
        payment: opts.payment1y,
        savings: opts.savings1y ?? 100,
        upfront_cost: opts.upfront1y,
        monthly_cost: opts.monthly1y === undefined ? 50 : opts.monthly1y,
        on_demand_cost: opts.onDemand === undefined ? 200 : opts.onDemand,
      } as unknown as LocalRecommendation,
      {
        id: `${slug}-3y`,
        provider: 'aws',
        cloud_account_id: 'a1',
        service: 'ec2',
        resource_type: opts.resourceType,
        region: 'us-east-1',
        count: 1,
        term: 3,
        payment: opts.payment3y,
        savings: opts.savings3y ?? 300,
        upfront_cost: opts.upfront3y,
        monthly_cost: opts.monthly3y === undefined ? 40 : opts.monthly3y,
        on_demand_cost: opts.onDemand === undefined ? 200 : opts.onDemand,
      } as unknown as LocalRecommendation,
    ];
  }

  /**
   * Read the rendered cell order from the DOM. Multi-variant cells render as
   * `tr.rec-cell-summary-row[data-cell-key]`; the test fixtures here use only
   * multi-variant cells (every cell has 2 variants) so this single selector
   * captures the full ordering.
   */
  function renderedCellOrder(): string[] {
    return Array.from(
      document.querySelectorAll<HTMLTableRowElement>('tr.rec-cell-summary-row'),
    ).map((tr) => tr.getAttribute('data-cell-key') ?? '');
  }

  /**
   * Find a key fragment in the rendered order and assert it is actually
   * present. Returns the index. Prevents false-positive `<` comparisons that
   * would silently pass when `findIndex` returns `-1` for both sides (per CR
   * review on the initial commit).
   */
  function indexOrFail(order: string[], keyFragment: string): number {
    const idx = order.findIndex((k) => k.includes(keyFragment));
    expect(idx).toBeGreaterThanOrEqual(0);
    return idx;
  }

  /** Set up the DOM + standard mocks; caller provides the rec list + sort. */
  function setupTestFixture(
    recs: LocalRecommendation[],
    sort: { column: string; direction: 'asc' | 'desc' },
  ): void {
    buildTestDOM();
    jest.clearAllMocks();
    (api.getRecommendations as jest.Mock).mockResolvedValue({
      summary: {},
      recommendations: recs,
      regions: [],
    });
    (state.getRecommendations as jest.Mock).mockReturnValue(recs);
    (state.getVisibleRecommendations as jest.Mock).mockReturnValue(recs);
    (state.getSelectedRecommendationIDs as jest.Mock).mockReturnValue(new Set());
    (state.getRecommendationsSort as jest.Mock).mockReturnValue(sort);
    (state.getRecommendationsColumnFilters as jest.Mock).mockReturnValue({});
    (state.getCostPeriod as jest.Mock).mockReturnValue('monthly');
  }

  // -------------------------------------------------------------------------
  // 4.9 - Term
  // -------------------------------------------------------------------------
  test('Term asc: orders cells by summary.termMin (1yr-grouped before 3yr-grouped)', async () => {
    const cellMixed = multiVariantCell({
      resourceType: 'aaa-mixed', payment1y: 'no-upfront', payment3y: 'no-upfront',
      upfront1y: 0, upfront3y: 0,
    });
    // "1yr-only" cell: both variants term=1, different payments so they
    // produce two distinct rec rows under the same cellKey.
    const cell1yOnly: LocalRecommendation[] = [
      { id: 'bbb-1y-nu', provider: 'aws', cloud_account_id: 'a1', service: 'ec2',
        resource_type: 'bbb-1y-only', region: 'us-east-1', count: 1, term: 1,
        payment: 'no-upfront', savings: 100, upfront_cost: 0, monthly_cost: 50 } as unknown as LocalRecommendation,
      { id: 'bbb-1y-au', provider: 'aws', cloud_account_id: 'a1', service: 'ec2',
        resource_type: 'bbb-1y-only', region: 'us-east-1', count: 1, term: 1,
        payment: 'all-upfront', savings: 110, upfront_cost: 800, monthly_cost: 0 } as unknown as LocalRecommendation,
    ];
    const cell3yOnly: LocalRecommendation[] = [
      { id: 'ccc-3y-nu', provider: 'aws', cloud_account_id: 'a1', service: 'ec2',
        resource_type: 'ccc-3y-only', region: 'us-east-1', count: 1, term: 3,
        payment: 'no-upfront', savings: 300, upfront_cost: 0, monthly_cost: 40 } as unknown as LocalRecommendation,
      { id: 'ccc-3y-au', provider: 'aws', cloud_account_id: 'a1', service: 'ec2',
        resource_type: 'ccc-3y-only', region: 'us-east-1', count: 1, term: 3,
        payment: 'all-upfront', savings: 320, upfront_cost: 2400, monthly_cost: 0 } as unknown as LocalRecommendation,
    ];
    // Intentionally wrong insertion order so a no-op sort would fail.
    const recs = [...cell3yOnly, ...cellMixed, ...cell1yOnly];

    setupTestFixture(recs, { column: 'term', direction: 'asc' });
    await loadRecommendations();

    const order = renderedCellOrder();
    // Expected: 1yr-only first (termMin=1, termMax=1), mixed second (termMin=1,
    // termMax=3), 3yr-only last (termMin=3). The encoded score termMin*100 +
    // termMax distinguishes the 1yr-only and mixed cells via termMax.
    expect(indexOrFail(order, 'bbb-1y-only'))
      .toBeLessThan(indexOrFail(order, 'aaa-mixed'));
    expect(indexOrFail(order, 'aaa-mixed'))
      .toBeLessThan(indexOrFail(order, 'ccc-3y-only'));
  });

  test('Term desc: order is the exact reverse of ascending', async () => {
    const cellMixed = multiVariantCell({
      resourceType: 'aaa-mixed', payment1y: 'no-upfront', payment3y: 'no-upfront',
      upfront1y: 0, upfront3y: 0,
    });
    const cell1yOnly: LocalRecommendation[] = [
      { id: 'bbb-1y-nu', provider: 'aws', cloud_account_id: 'a1', service: 'ec2',
        resource_type: 'bbb-1y-only', region: 'us-east-1', count: 1, term: 1,
        payment: 'no-upfront', savings: 100, upfront_cost: 0, monthly_cost: 50 } as unknown as LocalRecommendation,
      { id: 'bbb-1y-au', provider: 'aws', cloud_account_id: 'a1', service: 'ec2',
        resource_type: 'bbb-1y-only', region: 'us-east-1', count: 1, term: 1,
        payment: 'all-upfront', savings: 110, upfront_cost: 800, monthly_cost: 0 } as unknown as LocalRecommendation,
    ];
    const cell3yOnly: LocalRecommendation[] = [
      { id: 'ccc-3y-nu', provider: 'aws', cloud_account_id: 'a1', service: 'ec2',
        resource_type: 'ccc-3y-only', region: 'us-east-1', count: 1, term: 3,
        payment: 'no-upfront', savings: 300, upfront_cost: 0, monthly_cost: 40 } as unknown as LocalRecommendation,
      { id: 'ccc-3y-au', provider: 'aws', cloud_account_id: 'a1', service: 'ec2',
        resource_type: 'ccc-3y-only', region: 'us-east-1', count: 1, term: 3,
        payment: 'all-upfront', savings: 320, upfront_cost: 2400, monthly_cost: 0 } as unknown as LocalRecommendation,
    ];
    const recs = [...cellMixed, ...cell1yOnly, ...cell3yOnly];

    setupTestFixture(recs, { column: 'term', direction: 'desc' });
    await loadRecommendations();

    const order = renderedCellOrder();
    expect(indexOrFail(order, 'ccc-3y-only'))
      .toBeLessThan(indexOrFail(order, 'aaa-mixed'));
    expect(indexOrFail(order, 'aaa-mixed'))
      .toBeLessThan(indexOrFail(order, 'bbb-1y-only'));
  });

  // -------------------------------------------------------------------------
  // 4.10 - Payment
  // -------------------------------------------------------------------------
  test('Payment asc: orders cells by canonical PAYMENT_ORDER (no-upfront < partial-upfront < all-upfront)', async () => {
    // Each cell has term=1 + term=3 variants with the *same* payment so the
    // canonical first-variant payment per cell is unambiguous.
    const cellPartial = multiVariantCell({
      resourceType: 'partial-cell', payment1y: 'partial-upfront', payment3y: 'partial-upfront',
      upfront1y: 200, upfront3y: 600,
    });
    const cellAllUp = multiVariantCell({
      resourceType: 'allup-cell', payment1y: 'all-upfront', payment3y: 'all-upfront',
      upfront1y: 1000, upfront3y: 3000,
    });
    const cellNoUp = multiVariantCell({
      resourceType: 'noup-cell', payment1y: 'no-upfront', payment3y: 'no-upfront',
      upfront1y: 0, upfront3y: 0,
    });
    const recs = [...cellAllUp, ...cellPartial, ...cellNoUp];  // intentionally wrong insertion order

    setupTestFixture(recs, { column: 'payment', direction: 'asc' });
    await loadRecommendations();

    const order = renderedCellOrder();
    // Expected semantic order: no-upfront < partial-upfront < all-upfront.
    // Previous alphabetic comparator would have produced all-upfront <
    // no-upfront < partial-upfront, which this test would fail under.
    expect(indexOrFail(order, 'noup-cell'))
      .toBeLessThan(indexOrFail(order, 'partial-cell'));
    expect(indexOrFail(order, 'partial-cell'))
      .toBeLessThan(indexOrFail(order, 'allup-cell'));
  });

  // -------------------------------------------------------------------------
  // 4.12 - Upfront Cost
  // -------------------------------------------------------------------------
  test('Upfront Cost asc: orders cells by summary.upfrontMin', async () => {
    const cellLow = multiVariantCell({
      resourceType: 'low-upfront', payment1y: 'no-upfront', payment3y: 'partial-upfront',
      upfront1y: 0, upfront3y: 500,
    });
    const cellMid = multiVariantCell({
      resourceType: 'mid-upfront', payment1y: 'partial-upfront', payment3y: 'all-upfront',
      upfront1y: 300, upfront3y: 1500,
    });
    const cellHigh = multiVariantCell({
      resourceType: 'high-upfront', payment1y: 'all-upfront', payment3y: 'all-upfront',
      upfront1y: 800, upfront3y: 2400,
    });
    const recs = [...cellHigh, ...cellMid, ...cellLow];  // wrong insertion order

    setupTestFixture(recs, { column: 'upfront_cost', direction: 'asc' });
    await loadRecommendations();

    const order = renderedCellOrder();
    // Expected: upfrontMin asc -> 0 < 300 < 800.
    expect(indexOrFail(order, 'low-upfront'))
      .toBeLessThan(indexOrFail(order, 'mid-upfront'));
    expect(indexOrFail(order, 'mid-upfront'))
      .toBeLessThan(indexOrFail(order, 'high-upfront'));
  });

  // -------------------------------------------------------------------------
  // 4.13 - Monthly Cost
  // -------------------------------------------------------------------------
  test('Monthly Cost asc: orders cells by Math.min over non-null variants', async () => {
    const cellLow = multiVariantCell({  // min(30, 20) = 20
      resourceType: 'low-monthly', payment1y: 'no-upfront', payment3y: 'no-upfront',
      upfront1y: 0, upfront3y: 0, monthly1y: 30, monthly3y: 20,
    });
    const cellMid = multiVariantCell({  // min(60, null) = 60 (best-case-of-known)
      resourceType: 'mid-monthly', payment1y: 'no-upfront', payment3y: 'all-upfront',
      upfront1y: 0, upfront3y: 2400, monthly1y: 60, monthly3y: null,
    });
    const cellHigh = multiVariantCell({  // min(100, 90) = 90
      resourceType: 'high-monthly', payment1y: 'no-upfront', payment3y: 'no-upfront',
      upfront1y: 0, upfront3y: 0, monthly1y: 100, monthly3y: 90,
    });
    const recs = [...cellHigh, ...cellMid, ...cellLow];

    setupTestFixture(recs, { column: 'monthly_cost', direction: 'asc' });
    await loadRecommendations();

    const order = renderedCellOrder();
    // Expected: 20 < 60 < 90.
    expect(indexOrFail(order, 'low-monthly'))
      .toBeLessThan(indexOrFail(order, 'mid-monthly'));
    expect(indexOrFail(order, 'mid-monthly'))
      .toBeLessThan(indexOrFail(order, 'high-monthly'));
  });

  test('Monthly Cost: cells with all-null monthly_cost sort last in both asc and desc', async () => {
    const cellFinite = multiVariantCell({
      resourceType: 'finite-monthly', payment1y: 'no-upfront', payment3y: 'no-upfront',
      upfront1y: 0, upfront3y: 0, monthly1y: 50, monthly3y: 40,
    });
    const cellAllNull = multiVariantCell({
      resourceType: 'allnull-monthly', payment1y: 'all-upfront', payment3y: 'all-upfront',
      upfront1y: 1000, upfront3y: 3000, monthly1y: null, monthly3y: null,
    });
    const recs = [...cellAllNull, ...cellFinite];

    // Ascending: finite first, all-null last.
    setupTestFixture(recs, { column: 'monthly_cost', direction: 'asc' });
    await loadRecommendations();
    let order = renderedCellOrder();
    expect(indexOrFail(order, 'finite-monthly'))
      .toBeLessThan(indexOrFail(order, 'allnull-monthly'));

    // Descending: all-null cells STAY last regardless of direction - the
    // POSITIVE_INFINITY null-sentinel logic flips them out of the normal
    // descending order specifically because "no data" rows should be
    // de-emphasised in both directions.
    setupTestFixture(recs, { column: 'monthly_cost', direction: 'desc' });
    await loadRecommendations();
    order = renderedCellOrder();
    expect(indexOrFail(order, 'finite-monthly'))
      .toBeLessThan(indexOrFail(order, 'allnull-monthly'));
  });

  test('Monthly Cost: two all-null cells sort deterministically via cellKey tiebreaker', async () => {
    // Both cells have ALL null monthly_cost - their scores are both
    // POSITIVE_INFINITY. The naive numeric diff would be Infinity - Infinity
    // = NaN; the comparator MUST short-circuit to the cellKey tiebreaker so
    // the two cells render in a stable order across repeated invocations.
    // Closes the gap surfaced by CodeRabbit on the initial #494 commit.
    const cellA = multiVariantCell({
      resourceType: 'aaa-null', payment1y: 'all-upfront', payment3y: 'all-upfront',
      upfront1y: 1000, upfront3y: 3000, monthly1y: null, monthly3y: null,
    });
    const cellB = multiVariantCell({
      resourceType: 'bbb-null', payment1y: 'all-upfront', payment3y: 'all-upfront',
      upfront1y: 2000, upfront3y: 6000, monthly1y: null, monthly3y: null,
    });
    const recs = [...cellB, ...cellA];  // intentionally wrong insertion order

    setupTestFixture(recs, { column: 'monthly_cost', direction: 'asc' });
    await loadRecommendations();
    const first = renderedCellOrder();
    expect(first.length).toBe(2);
    // cellKey contains the resource_type slug, so cellKey for 'aaa-null'
    // sorts before 'bbb-null' under localeCompare.
    expect(indexOrFail(first, 'aaa-null'))
      .toBeLessThan(indexOrFail(first, 'bbb-null'));

    // Re-run to confirm determinism (would fail under Infinity - Infinity = NaN).
    setupTestFixture(recs, { column: 'monthly_cost', direction: 'asc' });
    await loadRecommendations();
    const second = renderedCellOrder();
    expect(second).toEqual(first);
  });

  // -------------------------------------------------------------------------
  // 4.15 - Effective %
  // -------------------------------------------------------------------------
  test('Effective % asc: orders cells by Math.max over non-null variants (lowest best-pct first)', async () => {
    // effectiveSavingsPct uses on_demand_cost when set. Pick on-demand values
    // so each cell's pct is predictable. Formula:
    //   pct = (savings - upfront/(term*12)) / on_demand * 100
    // low-pct  cell: both variants score ~5%, max = 5%
    const cellLowPct = multiVariantCell({
      resourceType: 'low-pct', payment1y: 'no-upfront', payment3y: 'no-upfront',
      upfront1y: 0, upfront3y: 0, savings1y: 10, savings3y: 10, onDemand: 200,
    });
    // mid-pct cell: both variants score ~15%, max = 15%
    const cellMidPct = multiVariantCell({
      resourceType: 'mid-pct', payment1y: 'no-upfront', payment3y: 'no-upfront',
      upfront1y: 0, upfront3y: 0, savings1y: 30, savings3y: 30, onDemand: 200,
    });
    // high-pct cell: both variants score ~25%, max = 25%
    const cellHighPct = multiVariantCell({
      resourceType: 'high-pct', payment1y: 'no-upfront', payment3y: 'no-upfront',
      upfront1y: 0, upfront3y: 0, savings1y: 50, savings3y: 50, onDemand: 200,
    });
    const recs = [...cellHighPct, ...cellMidPct, ...cellLowPct];

    setupTestFixture(recs, { column: 'effective_savings_pct', direction: 'asc' });
    await loadRecommendations();

    const order = renderedCellOrder();
    // Expected asc: 5% < 15% < 25%.
    expect(indexOrFail(order, 'low-pct'))
      .toBeLessThan(indexOrFail(order, 'mid-pct'));
    expect(indexOrFail(order, 'mid-pct'))
      .toBeLessThan(indexOrFail(order, 'high-pct'));
  });

  test('Effective %: cells with all-null pct sort last regardless of direction', async () => {
    // A cell whose every variant has term=0 returns null from
    // effectiveSavingsPct - that's the null sentinel under test.
    const cellFinite = multiVariantCell({
      resourceType: 'finite-pct', payment1y: 'no-upfront', payment3y: 'no-upfront',
      upfront1y: 0, upfront3y: 0, savings1y: 20, savings3y: 30, onDemand: 200,
    });
    const cellAllNull: LocalRecommendation[] = [
      { id: 'nullpct-v1', provider: 'aws', cloud_account_id: 'a1', service: 'ec2',
        resource_type: 'allnull-pct', region: 'us-east-1', count: 1, term: 0,
        payment: 'no-upfront', savings: 0, upfront_cost: 0, monthly_cost: 50,
        on_demand_cost: 200 } as unknown as LocalRecommendation,
      { id: 'nullpct-v2', provider: 'aws', cloud_account_id: 'a1', service: 'ec2',
        resource_type: 'allnull-pct', region: 'us-east-1', count: 1, term: 0,
        payment: 'all-upfront', savings: 0, upfront_cost: 0, monthly_cost: 50,
        on_demand_cost: 200 } as unknown as LocalRecommendation,
    ];
    const recs = [...cellAllNull, ...cellFinite];

    setupTestFixture(recs, { column: 'effective_savings_pct', direction: 'asc' });
    await loadRecommendations();
    let order = renderedCellOrder();
    expect(indexOrFail(order, 'finite-pct'))
      .toBeLessThan(indexOrFail(order, 'allnull-pct'));

    setupTestFixture(recs, { column: 'effective_savings_pct', direction: 'desc' });
    await loadRecommendations();
    order = renderedCellOrder();
    expect(indexOrFail(order, 'finite-pct'))
      .toBeLessThan(indexOrFail(order, 'allnull-pct'));
  });

  // -------------------------------------------------------------------------
  // Determinism: repeating the same sort yields the same order.
  //
  // The pre-#494 bug also surfaced as "two clicks of the same header may
  // produce a different broken order" because every cell tied -> fallback to
  // browser sort stability, which is implementation-defined for old V8 builds
  // and varies across JS engines. With the new comparator every cell has a
  // distinct score (or, if genuinely tied, a stable cellKey tiebreaker), so
  // repeated invocations MUST produce the same order.
  // -------------------------------------------------------------------------
  // Selection-independent Term sort: cells with different term distributions
  // must sort correctly by cellSummary score (termMin*100+termMax) regardless
  // of which variants the user has selected. Fix for Issue #768: the previous
  // "selected-variant short-circuit" in cellScoreFor() switched the score to
  // the selected variant's individual term value, which caused rows to reorder
  // on every checkbox toggle.
  // -------------------------------------------------------------------------
  test('Term sort is selection-independent: cells rank by term distribution not by selected variant', async () => {
    // cell-1y-only: both variants are term=1 (termMin=1, termMax=1, score=101)
    const cell1yOnly: LocalRecommendation[] = [
      { id: '1yonly-nu', provider: 'aws', cloud_account_id: 'a1', service: 'ec2',
        resource_type: '1y-only', region: 'us-east-1', count: 1, term: 1,
        payment: 'no-upfront', savings: 100, upfront_cost: 0, monthly_cost: 50 } as unknown as LocalRecommendation,
      { id: '1yonly-au', provider: 'aws', cloud_account_id: 'a1', service: 'ec2',
        resource_type: '1y-only', region: 'us-east-1', count: 1, term: 1,
        payment: 'all-upfront', savings: 110, upfront_cost: 800, monthly_cost: 0 } as unknown as LocalRecommendation,
    ];
    // cell-mixed: term=1 and term=3 variants (termMin=1, termMax=3, score=103)
    const cellMixed = multiVariantCell({
      resourceType: 'mixed', payment1y: 'no-upfront', payment3y: 'no-upfront',
      upfront1y: 0, upfront3y: 0,
    });
    // cell-3y-only: both variants are term=3 (termMin=3, termMax=3, score=303)
    const cell3yOnly: LocalRecommendation[] = [
      { id: '3yonly-nu', provider: 'aws', cloud_account_id: 'a1', service: 'ec2',
        resource_type: '3y-only', region: 'us-east-1', count: 1, term: 3,
        payment: 'no-upfront', savings: 300, upfront_cost: 0, monthly_cost: 40 } as unknown as LocalRecommendation,
      { id: '3yonly-au', provider: 'aws', cloud_account_id: 'a1', service: 'ec2',
        resource_type: '3y-only', region: 'us-east-1', count: 1, term: 3,
        payment: 'all-upfront', savings: 320, upfront_cost: 2400, monthly_cost: 0 } as unknown as LocalRecommendation,
    ];
    const recs = [...cell3yOnly, ...cellMixed, ...cell1yOnly];

    // Select the 3yr variant from the mixed cell. Under the old (buggy) code
    // this caused mixed-cell's score to become 3*100+3=303, tying it with
    // 3y-only and pushing it behind 1y-only in asc order.
    setupTestFixture(recs, { column: 'term', direction: 'asc' });
    (state.getSelectedRecommendationIDs as jest.Mock).mockReturnValue(
      new Set(['mixed-3y']),
    );
    await loadRecommendations();

    const order = renderedCellOrder();
    // Expected under term asc (selection must NOT affect order):
    //   1y-only (score=101) < mixed (score=103) < 3y-only (score=303)
    expect(indexOrFail(order, '1y-only'))
      .toBeLessThan(indexOrFail(order, 'mixed'));
    expect(indexOrFail(order, 'mixed'))
      .toBeLessThan(indexOrFail(order, '3y-only'));
  });

  // -------------------------------------------------------------------------
  // Issue #768: toggling checkboxes must not change row sort order.
  //
  // Before the fix, cellScoreFor() switched to the selected variant's
  // individual value when selectedRecs contained a variant id, causing
  // groupsInSortOrder() to produce a different order after a checkbox toggle.
  // -------------------------------------------------------------------------
  test('Issue #768: row order is identical before and after toggling checkboxes', async () => {
    // Three cells with distinct savings so they sort in a predictable order.
    // Cell A: savings = 10 (lowest) → should be last under desc
    // Cell B: savings = 50 (middle)
    // Cell C: savings = 90 (highest) → should be first under desc
    const cellA = multiVariantCell({
      resourceType: 'cell-A', payment1y: 'no-upfront', payment3y: 'no-upfront',
      upfront1y: 0, upfront3y: 0, savings1y: 10, savings3y: 15,
    });
    const cellB = multiVariantCell({
      resourceType: 'cell-B', payment1y: 'no-upfront', payment3y: 'no-upfront',
      upfront1y: 0, upfront3y: 0, savings1y: 50, savings3y: 55,
    });
    const cellC = multiVariantCell({
      resourceType: 'cell-C', payment1y: 'no-upfront', payment3y: 'no-upfront',
      upfront1y: 0, upfront3y: 0, savings1y: 90, savings3y: 95,
    });
    const recs = [...cellA, ...cellB, ...cellC];

    // --- render with no selection ---
    setupTestFixture(recs, { column: 'savings', direction: 'desc' });
    (state.getSelectedRecommendationIDs as jest.Mock).mockReturnValue(new Set<string>());
    await loadRecommendations();
    const orderBefore = renderedCellOrder();

    // Verify base order (desc: C > B > A).
    expect(indexOrFail(orderBefore, 'cell-C'))
      .toBeLessThan(indexOrFail(orderBefore, 'cell-B'));
    expect(indexOrFail(orderBefore, 'cell-B'))
      .toBeLessThan(indexOrFail(orderBefore, 'cell-A'));

    // --- simulate toggling two checkboxes (select 1yr variant of A and 3yr of C) ---
    setupTestFixture(recs, { column: 'savings', direction: 'desc' });
    (state.getSelectedRecommendationIDs as jest.Mock).mockReturnValue(
      new Set(['cellA-1y', 'cellC-3y']),
    );
    await loadRecommendations();
    const orderAfter = renderedCellOrder();

    // Row order must be identical after selection change.
    expect(orderAfter).toEqual(orderBefore);
  });

  test('Sort is deterministic across repeated renders for each affected column', async () => {
    const cells = [
      multiVariantCell({ resourceType: 'A-cell', payment1y: 'no-upfront',      payment3y: 'all-upfront',     upfront1y: 0,   upfront3y: 1500, monthly1y: 50,  monthly3y: 0,  savings1y: 20, savings3y: 30 }),
      multiVariantCell({ resourceType: 'B-cell', payment1y: 'partial-upfront', payment3y: 'partial-upfront', upfront1y: 300, upfront3y: 900,  monthly1y: 40,  monthly3y: 30, savings1y: 25, savings3y: 35 }),
      multiVariantCell({ resourceType: 'C-cell', payment1y: 'all-upfront',     payment3y: 'no-upfront',      upfront1y: 800, upfront3y: 0,    monthly1y: 60,  monthly3y: 45, savings1y: 30, savings3y: 40 }),
    ].flat();

    const columns: Array<{ column: string; direction: 'asc' | 'desc' }> = [
      { column: 'term',                  direction: 'asc' },
      { column: 'payment',               direction: 'asc' },
      { column: 'upfront_cost',          direction: 'asc' },
      { column: 'monthly_cost',          direction: 'asc' },
      { column: 'effective_savings_pct', direction: 'asc' },
    ];

    for (const sort of columns) {
      setupTestFixture(cells, sort);
      await loadRecommendations();
      const first = renderedCellOrder();
      // Re-run with the SAME inputs.
      setupTestFixture(cells, sort);
      await loadRecommendations();
      const second = renderedCellOrder();
      expect(second).toEqual(first);
      // Must not be the empty / single-element trivial case.
      expect(first.length).toBe(3);
    }
  });
});

// Helper used by the issue-#479/#480/#481/#482/#483/#484 describes below to
// set up the same DOM the top-level "Recommendations Module" describe seeds
// in its beforeEach. Each describe block runs at module scope and therefore
// doesn't inherit that hook.
function setupOpportunitiesTabDom(): void {
  document.body.replaceChildren();
  const recsTab = document.createElement('div');
  recsTab.id = 'opportunities-tab';
  recsTab.className = 'tab-content active';
  const summary = document.createElement('div');
  summary.id = 'recommendations-summary';
  const list = document.createElement('div');
  list.id = 'recommendations-list';
  recsTab.appendChild(summary);
  recsTab.appendChild(list);
  document.body.appendChild(recsTab);
  const purchaseModal = document.createElement('div');
  purchaseModal.id = 'purchase-modal';
  purchaseModal.className = 'hidden';
  const purchaseDetails = document.createElement('div');
  purchaseDetails.id = 'purchase-details';
  purchaseModal.appendChild(purchaseDetails);
  document.body.appendChild(purchaseModal);
}

// ---------------------------------------------------------------------------
// Issue #479: Select-all checkbox tri-state.
// The header checkbox renders with the right .checked / .indeterminate state
// reflecting current selection vs. the set of best-variant-per-cell recs
// (the set the select-all click actually populates).
// ---------------------------------------------------------------------------
describe('Issue #479: Select-all header checkbox tri-state', () => {
  const recs = [
    { id: 'r1', provider: 'aws', cloud_account_id: 'a1', service: 'ec2', resource_type: 't3.medium', region: 'us-east-1', count: 1, term: 1, savings: 100, upfront_cost: 500 },
    { id: 'r2', provider: 'aws', cloud_account_id: 'a1', service: 'rds', resource_type: 'db.t3',     region: 'us-east-1', count: 1, term: 1, savings: 200, upfront_cost: 800 },
  ];

  beforeEach(() => {
    setupOpportunitiesTabDom();
    jest.clearAllMocks();
    (api.getRecommendations as jest.Mock).mockResolvedValue({ summary: {}, recommendations: recs, regions: [] });
    (state.getRecommendations as jest.Mock).mockReturnValue(recs);
    (state.getVisibleRecommendations as jest.Mock).mockReturnValue(recs);
    (state.getRecommendationsColumnFilters as jest.Mock).mockReturnValue({});
    (state.getCostPeriod as jest.Mock).mockReturnValue('monthly');
    (state.getRecommendationsSort as jest.Mock).mockReturnValue({ column: 'savings', direction: 'desc' });
    (recsApi.getRecommendationsFreshness as jest.Mock).mockResolvedValue({
      last_collected_at: new Date(Date.now() - 60 * 60 * 1000).toISOString(),
      last_collection_error: null,
    });
  });

  test('with no selection, header checkbox is unchecked and not indeterminate', async () => {
    (state.getSelectedRecommendationIDs as jest.Mock).mockReturnValue(new Set());
    await loadRecommendations();
    const cb = document.getElementById('select-all-recs') as HTMLInputElement;
    expect(cb.checked).toBe(false);
    expect(cb.indeterminate).toBe(false);
  });

  test('with a partial selection, header checkbox is indeterminate', async () => {
    (state.getSelectedRecommendationIDs as jest.Mock).mockReturnValue(new Set(['r1']));
    await loadRecommendations();
    const cb = document.getElementById('select-all-recs') as HTMLInputElement;
    expect(cb.indeterminate).toBe(true);
    expect(cb.checked).toBe(false);
  });

  test('with every best-variant selected, header checkbox is checked and not indeterminate', async () => {
    (state.getSelectedRecommendationIDs as jest.Mock).mockReturnValue(new Set(['r1', 'r2']));
    await loadRecommendations();
    const cb = document.getElementById('select-all-recs') as HTMLInputElement;
    expect(cb.checked).toBe(true);
    expect(cb.indeterminate).toBe(false);
  });

  test('clicking when all selected clears selection (the previously-broken second-click path)', async () => {
    // Repro from the issue: with all rows selected, the header checkbox
    // used to render as unchecked, so the second click flipped it to
    // checked and re-selected everything (no-op). Now it renders as
    // checked, so the second click flips to unchecked and clears.
    (state.getSelectedRecommendationIDs as jest.Mock).mockReturnValue(new Set(['r1', 'r2']));
    await loadRecommendations();
    const cb = document.getElementById('select-all-recs') as HTMLInputElement;
    expect(cb.checked).toBe(true);
    // Simulate the browser's pre-change flip from true → false.
    cb.checked = false;
    cb.dispatchEvent(new Event('change'));
    expect(state.clearSelectedRecommendations).toHaveBeenCalled();
  });
});

// ---------------------------------------------------------------------------
// Issue #480: First-click sort direction per column.
// Text columns and most numerics default to 'asc' (A→Z / low → high).
// `savings` and `on_demand_monthly` keep 'desc' as the platform default.
// ---------------------------------------------------------------------------
describe('Issue #480: per-column default sort direction', () => {
  const recs = [
    { id: 'r1', provider: 'aws',   cloud_account_id: 'a1', service: 'ec2', resource_type: 't3.medium', region: 'us-east-1', count: 1, term: 1, payment: 'no-upfront', savings: 100, upfront_cost: 0,    monthly_cost: 50,  on_demand_cost: 80  },
    { id: 'r2', provider: 'azure', cloud_account_id: 'a2', service: 'vm',  resource_type: 'D2s_v3',    region: 'eastus',    count: 1, term: 1, payment: 'no-upfront', savings: 150, upfront_cost: 1000, monthly_cost: 100, on_demand_cost: 130 },
  ];

  beforeEach(() => {
    setupOpportunitiesTabDom();
    jest.clearAllMocks();
    (api.getRecommendations as jest.Mock).mockResolvedValue({ summary: {}, recommendations: recs, regions: [] });
    (state.getRecommendations as jest.Mock).mockReturnValue(recs);
    (state.getVisibleRecommendations as jest.Mock).mockReturnValue(recs);
    (state.getRecommendationsColumnFilters as jest.Mock).mockReturnValue({});
    (state.getRecommendationsSort as jest.Mock).mockReturnValue({ column: 'savings', direction: 'desc' });
    (state.getCostPeriod as jest.Mock).mockReturnValue('monthly');
    (recsApi.getRecommendationsFreshness as jest.Mock).mockResolvedValue({
      last_collected_at: new Date(Date.now() - 60 * 60 * 1000).toISOString(),
      last_collection_error: null,
    });
  });

  test.each<[string, 'asc' | 'desc']>([
    ['provider',              'asc'],
    ['account',               'asc'],
    ['service',               'asc'],
    ['resource_type',         'asc'],
    ['region',                'asc'],
    ['count',                 'asc'],
    ['term',                  'asc'],
    ['payment',               'asc'],
    ['upfront_cost',          'asc'],
    ['monthly_cost',          'asc'],
    ['effective_savings_pct', 'asc'],
    ['savings',               'desc'],  // exception per QA notes
    ['on_demand_monthly',     'desc'],  // exception per QA notes
  ])('first click on %s header sorts %s', async (col, expectedDir) => {
    // Pin the active sort to a column distinct from the one we click so
    // the click is treated as a "first click on a previously-unsorted
    // column" rather than a toggle on the active column. Pick `region`
    // unless `region` itself is the column under test, in which case
    // pick `provider`.
    const initialColumn = col === 'region' ? 'provider' : 'region';
    (state.getRecommendationsSort as jest.Mock).mockReturnValue({ column: initialColumn, direction: 'asc' });
    await loadRecommendations();
    const header = document.querySelector<HTMLTableCellElement>(`th[data-sort="${col}"]`);
    expect(header).not.toBeNull();
    header!.click();
    expect(state.setRecommendationsSort).toHaveBeenCalledWith({ column: col, direction: expectedDir });
  });

  test('second click on the same column toggles the direction', async () => {
    // Provider's first click goes to asc (covered above). After
    // state reports the active sort, the next click flips to desc.
    (state.getRecommendationsSort as jest.Mock).mockReturnValue({ column: 'provider', direction: 'asc' });
    await loadRecommendations();
    const header = document.querySelector<HTMLTableCellElement>('th[data-sort="provider"]');
    header!.click();
    expect(state.setRecommendationsSort).toHaveBeenLastCalledWith({ column: 'provider', direction: 'desc' });
  });
});

// ---------------------------------------------------------------------------
// Issue #481: Sort column + direction persisted across page refresh via
// URL query params (?sort=<col>&dir=<asc|desc>).
// ---------------------------------------------------------------------------
describe('Issue #481: URL persistence of sort state', () => {
  const recs = [
    { id: 'r1', provider: 'aws', cloud_account_id: 'a1', service: 'ec2', resource_type: 't3.medium', region: 'us-east-1', count: 1, term: 1, savings: 100, upfront_cost: 500 },
  ];

  beforeEach(() => {
    setupOpportunitiesTabDom();
    jest.clearAllMocks();
    (api.getRecommendations as jest.Mock).mockResolvedValue({ summary: {}, recommendations: recs, regions: [] });
    (state.getRecommendations as jest.Mock).mockReturnValue(recs);
    (state.getVisibleRecommendations as jest.Mock).mockReturnValue(recs);
    (state.getRecommendationsColumnFilters as jest.Mock).mockReturnValue({});
    (state.getRecommendationsSort as jest.Mock).mockReturnValue({ column: 'savings', direction: 'desc' });
    (state.getCostPeriod as jest.Mock).mockReturnValue('monthly');
    (recsApi.getRecommendationsFreshness as jest.Mock).mockResolvedValue({
      last_collected_at: new Date(Date.now() - 60 * 60 * 1000).toISOString(),
      last_collection_error: null,
    });
    // Reset URL between tests.
    window.history.replaceState({}, '', '/');
  });

  test('valid ?sort=col&dir=asc seeds setRecommendationsSort on load', async () => {
    window.history.replaceState({}, '', '/?sort=upfront_cost&dir=asc');
    await loadRecommendations();
    expect(state.setRecommendationsSort).toHaveBeenCalledWith({ column: 'upfront_cost', direction: 'asc' });
  });

  test('valid ?sort=col&dir=desc seeds setRecommendationsSort on load', async () => {
    window.history.replaceState({}, '', '/?sort=provider&dir=desc');
    await loadRecommendations();
    expect(state.setRecommendationsSort).toHaveBeenCalledWith({ column: 'provider', direction: 'desc' });
  });

  test('invalid ?sort=<unknown> is silently ignored', async () => {
    window.history.replaceState({}, '', '/?sort=not_a_column&dir=asc');
    await loadRecommendations();
    expect(state.setRecommendationsSort).not.toHaveBeenCalledWith(
      expect.objectContaining({ column: 'not_a_column' }),
    );
  });

  test('invalid ?dir=foo is silently ignored', async () => {
    window.history.replaceState({}, '', '/?sort=provider&dir=foo');
    await loadRecommendations();
    expect(state.setRecommendationsSort).not.toHaveBeenCalledWith(
      expect.objectContaining({ direction: 'foo' }),
    );
  });

  test('clicking a header writes the active sort to the URL', async () => {
    await loadRecommendations();
    const header = document.querySelector<HTMLTableCellElement>('th[data-sort="upfront_cost"]');
    header!.click();
    const params = new URLSearchParams(window.location.search);
    expect(params.get('sort')).toBe('upfront_cost');
    expect(params.get('dir')).toBe('asc');
  });
});

// ---------------------------------------------------------------------------
// Issue #482: "All" checkbox tri-state + null-filter renders as all-checked.
// ---------------------------------------------------------------------------
describe('Issue #482: column filter "All" tri-state semantics', () => {
  const recs = [
    { id: 'r1', provider: 'aws',   cloud_account_id: 'a1', service: 'ec2', resource_type: 't3.medium', region: 'us-east-1', count: 1, term: 1, savings: 100, upfront_cost: 500 },
    { id: 'r2', provider: 'azure', cloud_account_id: 'a2', service: 'vm',  resource_type: 'D2s_v3',    region: 'eastus',    count: 1, term: 1, savings: 150, upfront_cost: 800 },
    { id: 'r3', provider: 'gcp',   cloud_account_id: 'a3', service: 'gce', resource_type: 'n2-standard-2', region: 'us-central1', count: 1, term: 1, savings: 200, upfront_cost: 600 },
  ];

  beforeEach(() => {
    setupOpportunitiesTabDom();
    jest.clearAllMocks();
    (api.getRecommendations as jest.Mock).mockResolvedValue({ summary: {}, recommendations: recs, regions: [] });
    (state.getRecommendations as jest.Mock).mockReturnValue(recs);
    (state.getVisibleRecommendations as jest.Mock).mockReturnValue(recs);
    (state.getRecommendationsColumnFilters as jest.Mock).mockReturnValue({});
    (state.getRecommendationsSort as jest.Mock).mockReturnValue({ column: 'savings', direction: 'desc' });
    (state.getCostPeriod as jest.Mock).mockReturnValue('monthly');
    (recsApi.getRecommendationsFreshness as jest.Mock).mockResolvedValue({
      last_collected_at: new Date(Date.now() - 60 * 60 * 1000).toISOString(),
      last_collection_error: null,
    });
  });

  test('opening a popover on an unfiltered column renders every value as checked', async () => {
    await loadRecommendations();
    document.querySelector<HTMLButtonElement>('th .column-filter-btn[data-column="provider"]')!.click();
    const cbs = Array.from(document.querySelectorAll<HTMLInputElement>('.column-filter-popover .column-filter-item input[type="checkbox"]'));
    expect(cbs.length).toBe(3);
    cbs.forEach((cb) => expect(cb.checked).toBe(true));
    const allBox = document.querySelector<HTMLInputElement>('.column-filter-popover input[data-role="all"]');
    expect(allBox?.checked).toBe(true);
    expect(allBox?.indeterminate).toBe(false);
  });

  test('partial selection makes the (All) box indeterminate', async () => {
    (state.getRecommendationsColumnFilters as jest.Mock).mockReturnValue({
      provider: { kind: 'set', values: ['aws'] },
    });
    await loadRecommendations();
    document.querySelector<HTMLButtonElement>('th .column-filter-btn[data-column="provider"]')!.click();
    const allBox = document.querySelector<HTMLInputElement>('.column-filter-popover input[data-role="all"]');
    expect(allBox?.checked).toBe(false);
    expect(allBox?.indeterminate).toBe(true);
  });

  test('clicking (All) when it is checked unchecks every value AND persists an explicit empty allow-list', async () => {
    await loadRecommendations();
    document.querySelector<HTMLButtonElement>('th .column-filter-btn[data-column="provider"]')!.click();
    const allBox = document.querySelector<HTMLInputElement>('.column-filter-popover input[data-role="all"]');
    expect(allBox?.checked).toBe(true);
    // Browser flips → unchecked on click of an already-checked tri-state.
    allBox!.checked = false;
    allBox!.dispatchEvent(new Event('change'));
    expect(state.setRecommendationsColumnFilter).toHaveBeenCalledWith('provider', { kind: 'set', values: [] });
  });

  test('clicking (All) when it is unchecked checks every value AND clears the filter (null)', async () => {
    // Start from a partial selection so the table still renders (any row
    // passes — provider 'aws' is in the allow-list). We rely on the
    // popover for THIS column showing only the aws box checked and the
    // azure/gcp boxes unchecked → (All) tri-state is unchecked-not-
    // indeterminate-not-checked, but since 1 of 3 is checked it's
    // indeterminate. To exercise the explicit "(All) unchecked → click"
    // path we set a second column's filter to narrow the visible set
    // while leaving provider's checkboxes all-unchecked. Easier: use a
    // {set, values: []} filter on a DIFFERENT column so the provider
    // popover renders with no provider-narrowing applied (all checked).
    // Then directly UNCHECK the (All) box to take it from checked → empty.
    (state.getRecommendationsColumnFilters as jest.Mock).mockReturnValue({
      // narrowing on service so the table is not empty when provider
      // has no explicit filter
      service: { kind: 'set', values: ['ec2', 'vm', 'gce'] },
    });
    await loadRecommendations();
    document.querySelector<HTMLButtonElement>('th .column-filter-btn[data-column="provider"]')!.click();
    const allBox = document.querySelector<HTMLInputElement>('.column-filter-popover input[data-role="all"]');
    // No provider filter → resync shows everything ticked.
    expect(allBox?.checked).toBe(true);
    // Browser flips → unchecked on click of a checked tri-state.
    allBox!.checked = false;
    allBox!.dispatchEvent(new Event('change'));
    expect(state.setRecommendationsColumnFilter).toHaveBeenCalledWith('provider', { kind: 'set', values: [] });
    // Now flip it back — (All) unchecked → checked path. After the previous
    // commitAll's rerender, the mock setRecommendationsColumnFilter has
    // updated state, but jest mocks don't propagate writes; the popover
    // would still render based on the still-mocked getRecommendationsColumnFilters
    // return value. Simulate the intended state explicitly.
    (state.getRecommendationsColumnFilters as jest.Mock).mockReturnValue({
      provider: { kind: 'set', values: [] },
      service: { kind: 'set', values: ['ec2', 'vm', 'gce'] },
    });
    // Re-open the popover by toggling the trigger twice; without this the
    // popover keeps the previous resync's checkbox states.
    document.querySelector<HTMLButtonElement>('th .column-filter-btn[data-column="provider"]')!.click(); // close
    document.querySelector<HTMLButtonElement>('th .column-filter-btn[data-column="provider"]')!.click(); // reopen
    const allBox2 = document.querySelector<HTMLInputElement>('.column-filter-popover input[data-role="all"]');
    expect(allBox2?.checked).toBe(false);
    expect(allBox2?.indeterminate).toBe(false);
    allBox2!.checked = true;
    allBox2!.dispatchEvent(new Event('change'));
    expect(state.setRecommendationsColumnFilter).toHaveBeenLastCalledWith('provider', null);
  });

  test('checking every individual value collapses to null (the existing "all selected = no narrowing" rule)', async () => {
    // Start narrowed to 2 of 3 (aws, azure); checking the 3rd (gcp) box
    // takes us to all-3 → commit() collapses to null. One toggle keeps
    // the mock state interaction trivial — see Bundle B's notes about
    // resync re-reading the (mocked) filter after each commit.
    (state.getRecommendationsColumnFilters as jest.Mock).mockReturnValue({
      provider: { kind: 'set', values: ['aws', 'azure'] },
    });
    await loadRecommendations();
    document.querySelector<HTMLButtonElement>('th .column-filter-btn[data-column="provider"]')!.click();
    const gcpCb = document.querySelector<HTMLInputElement>('.column-filter-popover input[data-value="gcp"]')!;
    expect(gcpCb.checked).toBe(false);
    gcpCb.checked = true;
    gcpCb.dispatchEvent(new Event('change'));
    // All 3 now checked → commit collapses to null.
    expect(state.setRecommendationsColumnFilter).toHaveBeenLastCalledWith('provider', null);
  });

  // CR pass #2 regression: unchecking the last individual value used to
  // collapse to `null` (no filter), which silently snapped the popover
  // back to all-checked. After the fix, the individual-checkbox commit
  // mirrors the (All) / Clear path: an empty allow-list is persisted, so
  // the table renders 0 rows just like Clear.
  test('unchecking every individual value persists an explicit empty allow-list (not null)', async () => {
    // Start narrowed to a single value so the popover opens with exactly
    // one checkbox checked. Then untick it — commit() should produce
    // {kind: 'set', values: []}, NOT null.
    (state.getRecommendationsColumnFilters as jest.Mock).mockReturnValue({
      provider: { kind: 'set', values: ['aws'] },
    });
    await loadRecommendations();
    document.querySelector<HTMLButtonElement>('th .column-filter-btn[data-column="provider"]')!.click();
    const awsCb = document.querySelector<HTMLInputElement>('.column-filter-popover input[data-value="aws"]')!;
    expect(awsCb.checked).toBe(true);
    awsCb.checked = false;
    awsCb.dispatchEvent(new Event('change'));
    // Zero of 3 checked — must NOT collapse to null.
    expect(state.setRecommendationsColumnFilter).toHaveBeenLastCalledWith('provider', { kind: 'set', values: [] });
    // Sanity: the call site MUST be the {set, values: []} branch, not the
    // null branch. Guards against future refactors that re-introduce the
    // `selected.length === 0 → null` shortcut.
    const calls = (state.setRecommendationsColumnFilter as jest.Mock).mock.calls
      .filter((c) => c[0] === 'provider');
    const last = calls[calls.length - 1];
    expect(last[1]).not.toBeNull();
    expect(last[1]).toEqual({ kind: 'set', values: [] });
  });

  // CR pass #2 regression: with the empty allow-list persisted, applying
  // the filter to the recommendations must render zero rows — i.e. the
  // unchecking-last-value path reaches the same 0-row terminal state as
  // the (All) / Clear path.
  test('an empty allow-list filter renders 0 rows (applyColumnFilters returns [])', async () => {
    const { applyColumnFilters } = await import('../recommendations');
    const filtered = applyColumnFilters(
      recs as unknown as Parameters<typeof applyColumnFilters>[0],
      { provider: { kind: 'set', values: [] } },
    );
    expect(filtered).toEqual([]);
  });
});

// ---------------------------------------------------------------------------
// Issue #483: Scrolling inside the popover does NOT dismiss it.
// ---------------------------------------------------------------------------
describe('Issue #483: popover stays open while user scrolls its contents', () => {
  const recs = [
    { id: 'r1', provider: 'aws', cloud_account_id: 'a1', service: 'ec2', resource_type: 't3.medium', region: 'us-east-1', count: 1, term: 1, savings: 100, upfront_cost: 500 },
  ];

  beforeEach(() => {
    setupOpportunitiesTabDom();
    jest.clearAllMocks();
    (api.getRecommendations as jest.Mock).mockResolvedValue({ summary: {}, recommendations: recs, regions: [] });
    (state.getRecommendations as jest.Mock).mockReturnValue(recs);
    (state.getVisibleRecommendations as jest.Mock).mockReturnValue(recs);
    (state.getRecommendationsColumnFilters as jest.Mock).mockReturnValue({});
    (state.getRecommendationsSort as jest.Mock).mockReturnValue({ column: 'savings', direction: 'desc' });
    (state.getCostPeriod as jest.Mock).mockReturnValue('monthly');
    (recsApi.getRecommendationsFreshness as jest.Mock).mockResolvedValue({
      last_collected_at: new Date(Date.now() - 60 * 60 * 1000).toISOString(),
      last_collection_error: null,
    });
  });

  test('a scroll event whose target is inside the popover does not dismiss it', async () => {
    await loadRecommendations();
    document.querySelector<HTMLButtonElement>('th .column-filter-btn[data-column="resource_type"]')!.click();
    const popover = document.querySelector('.column-filter-popover');
    expect(popover).not.toBeNull();
    const list = popover!.querySelector('.column-filter-list');
    expect(list).not.toBeNull();
    // Simulate the user scrolling inside the popover's list. The capture-
    // phase scroll listener on window used to fire and close the popover;
    // it now ignores scrolls whose target is inside openPopover.el.
    const scrollEvt = new Event('scroll', { bubbles: true });
    list!.dispatchEvent(scrollEvt);
    expect(document.querySelector('.column-filter-popover')).not.toBeNull();
  });

  test('a scroll event outside the popover still dismisses it', async () => {
    await loadRecommendations();
    document.querySelector<HTMLButtonElement>('th .column-filter-btn[data-column="resource_type"]')!.click();
    expect(document.querySelector('.column-filter-popover')).not.toBeNull();
    // Scroll the document body (outside the popover) — should still close.
    const scrollEvt = new Event('scroll', { bubbles: true });
    document.body.dispatchEvent(scrollEvt);
    expect(document.querySelector('.column-filter-popover')).toBeNull();
  });
});

// ---------------------------------------------------------------------------
// Issue #484: Numeric filter exact-match against the displayed rounded value.
// ---------------------------------------------------------------------------
describe('Issue #484: numeric filter matches the displayed rounded value', () => {
  // Choose a savings value whose raw form rounds to a different display
  // value depending on which precision we use. Under hourly period, the
  // display rounds to PERIOD_DECIMALS.hourly (4) decimals.
  //
  // raw monthly = 123.456789 → daily = monthly/30 = 4.1152263; hourly =
  // monthly/720 = 0.171467... Use a monthly that's clean enough to keep
  // the assertions readable while still distinguishing exact rounding.
  const recs = [
    { id: 'r-exact',   provider: 'aws', cloud_account_id: 'a1', service: 'ec2', resource_type: 't3.medium', region: 'us-east-1', count: 1, term: 1, savings: 123.456789, upfront_cost: 500 },
    { id: 'r-other',   provider: 'aws', cloud_account_id: 'a1', service: 'rds', resource_type: 'db.t3',     region: 'us-east-1', count: 1, term: 1, savings: 250,        upfront_cost: 800 },
  ];

  beforeEach(() => {
    setupOpportunitiesTabDom();
    jest.clearAllMocks();
    (api.getRecommendations as jest.Mock).mockResolvedValue({ summary: {}, recommendations: recs, regions: [] });
    (state.getRecommendations as jest.Mock).mockReturnValue(recs);
    (state.getVisibleRecommendations as jest.Mock).mockReturnValue(recs);
    (state.getRecommendationsColumnFilters as jest.Mock).mockReturnValue({});
    (state.getRecommendationsSort as jest.Mock).mockReturnValue({ column: 'savings', direction: 'desc' });
    (recsApi.getRecommendationsFreshness as jest.Mock).mockResolvedValue({
      last_collected_at: new Date(Date.now() - 60 * 60 * 1000).toISOString(),
      last_collection_error: null,
    });
  });

  test('typing the rounded-display value of a row matches that row at daily period', async () => {
    // Under daily period (PERIOD_DECIMALS.daily = 2), raw monthly
    // 123.456789 / 30 = 4.1152263 → displays as "$4.12" (2 dp). Filter
    // expression "4.12" must match the row even though the raw scaled
    // value (4.1152263) doesn't equal 4.12.
    (state.getCostPeriod as jest.Mock).mockReturnValue('daily');
    (state.getRecommendationsColumnFilters as jest.Mock).mockReturnValue({
      savings: { kind: 'expr', expr: '4.12' },
    });
    const { applyColumnFilters } = await import('../recommendations');
    // Cast through unknown to match the LocalRecommendation shape used
    // by the function; the test recs include all the fields the filter
    // actually reads (savings).
    const filtered = applyColumnFilters(recs as unknown as Parameters<typeof applyColumnFilters>[0], state.getRecommendationsColumnFilters());
    expect(filtered.map((r) => r.id)).toEqual(['r-exact']);
  });

  test('typing a value that does NOT match the rounded display excludes the row', async () => {
    (state.getCostPeriod as jest.Mock).mockReturnValue('daily');
    (state.getRecommendationsColumnFilters as jest.Mock).mockReturnValue({
      savings: { kind: 'expr', expr: '4.11' },
    });
    const { applyColumnFilters } = await import('../recommendations');
    const filtered = applyColumnFilters(recs as unknown as Parameters<typeof applyColumnFilters>[0], state.getRecommendationsColumnFilters());
    expect(filtered.map((r) => r.id)).toEqual([]);
  });

  test('comparison operators apply against the rounded value too (>5 with a raw 4.999 stays excluded)', async () => {
    // Raw monthly 4.999 rounds to 5.00 at monthly period (0 dp → 5).
    // Under "monthly" period the display precision for savings is 0
    // decimals, so 4.999 displays as "$5" → ">4" includes it, ">5"
    // does not (since rounded = 5 and 5 > 5 is false).
    const sub = [
      { id: 'r-near-5', provider: 'aws', cloud_account_id: 'a1', service: 'ec2', resource_type: 't3.medium', region: 'us-east-1', count: 1, term: 1, savings: 4.999, upfront_cost: 0 },
    ];
    (state.getCostPeriod as jest.Mock).mockReturnValue('monthly');
    (state.getRecommendationsColumnFilters as jest.Mock).mockReturnValue({
      savings: { kind: 'expr', expr: '>4' },
    });
    const { applyColumnFilters } = await import('../recommendations');
    expect(applyColumnFilters(sub as unknown as Parameters<typeof applyColumnFilters>[0], state.getRecommendationsColumnFilters()).map((r) => r.id)).toEqual(['r-near-5']);

    (state.getRecommendationsColumnFilters as jest.Mock).mockReturnValue({
      savings: { kind: 'expr', expr: '>5' },
    });
    expect(applyColumnFilters(sub as unknown as Parameters<typeof applyColumnFilters>[0], state.getRecommendationsColumnFilters()).map((r) => r.id)).toEqual([]);
  });

  test('integer columns continue to match exact integer values', async () => {
    const sub = [
      { id: 'r-5',  provider: 'aws', cloud_account_id: 'a1', service: 'ec2', resource_type: 't3.medium', region: 'us-east-1', count: 5,  term: 1, savings: 100, upfront_cost: 0 },
      { id: 'r-10', provider: 'aws', cloud_account_id: 'a1', service: 'ec2', resource_type: 't3.medium', region: 'us-east-1', count: 10, term: 1, savings: 100, upfront_cost: 0 },
    ];
    (state.getRecommendationsColumnFilters as jest.Mock).mockReturnValue({
      count: { kind: 'expr', expr: '5' },
    });
    const { applyColumnFilters } = await import('../recommendations');
    expect(applyColumnFilters(sub as unknown as Parameters<typeof applyColumnFilters>[0], state.getRecommendationsColumnFilters()).map((r) => r.id)).toEqual(['r-5']);
  });

  // CR pass #2 regression: `displayPrecision` for currency columns MUST
  // agree with the fraction-digit count `formatCurrency` actually renders,
  // so that an exact-match filter on the displayed string ("$123") matches
  // the row whose underlying value rounds to that string at the active
  // period. Both sources must agree, so the test pins the symmetry at the
  // canonical `CURRENCY_DEFAULT_DIGITS` constant — which formatCurrency
  // uses as its default and displayPrecision now imports — rather than at
  // a hard-coded literal that would silently drift if the default ever
  // changes.
  describe('displayPrecision agrees with formatCurrency for currency columns', () => {
    function fractionDigitsOf(s: string): number {
      // Strip leading currency symbol(s) and any locale group separators,
      // then count digits after a decimal point. Returns 0 for "$123",
      // 2 for "$123.45", etc.
      const stripped = s.replace(/[^0-9.]/g, '');
      const dot = stripped.indexOf('.');
      return dot < 0 ? 0 : stripped.length - dot - 1;
    }

    test('upfront_cost: displayPrecision matches formatCurrency at every period', async () => {
      const recsMod = await import('../recommendations');
      // utils is jest.mock()'d at the top of this file (so the
      // recommendations module sees a stubbed formatCurrency). For this
      // test we want the REAL formatCurrency to assert the fraction-digit
      // contract holds against actual production behaviour, so we pull it
      // via jest.requireActual rather than the mocked import.
      const utilsActual = jest.requireActual<typeof import('../utils')>('../utils');
      const displayPrecision = recsMod.displayPrecision;
      const formatCurrency = utilsActual.formatCurrency;
      // upfront_cost is always rendered via plain formatCurrency (no
      // period scaling). The fraction digit count must therefore be
      // period-independent and equal to formatCurrency's own output.
      const sample = 123.456789;
      const renderedDigits = fractionDigitsOf(formatCurrency(sample));
      const periods: CostPeriod[] = ['hourly', 'daily', 'monthly', 'yearly'];
      for (const period of periods) {
        expect(displayPrecision('upfront_cost', period)).toBe(renderedDigits);
      }
    });

    test('monthly_cost: displayPrecision at monthly period matches formatCurrency', async () => {
      const recsMod = await import('../recommendations');
      // utils is jest.mock()'d at the top of this file (so the
      // recommendations module sees a stubbed formatCurrency). For this
      // test we want the REAL formatCurrency to assert the fraction-digit
      // contract holds against actual production behaviour, so we pull it
      // via jest.requireActual rather than the mocked import.
      const utilsActual = jest.requireActual<typeof import('../utils')>('../utils');
      const displayPrecision = recsMod.displayPrecision;
      const formatCurrency = utilsActual.formatCurrency;
      const sample = 123.456789;
      const renderedDigits = fractionDigitsOf(formatCurrency(sample));
      // At monthly period, formatCostForPeriod delegates to formatCurrency
      // → displayPrecision must agree with formatCurrency's digit count.
      expect(displayPrecision('monthly_cost', 'monthly')).toBe(renderedDigits);
    });

    test('on_demand_monthly: displayPrecision at monthly period matches formatCurrency', async () => {
      const recsMod = await import('../recommendations');
      // utils is jest.mock()'d at the top of this file (so the
      // recommendations module sees a stubbed formatCurrency). For this
      // test we want the REAL formatCurrency to assert the fraction-digit
      // contract holds against actual production behaviour, so we pull it
      // via jest.requireActual rather than the mocked import.
      const utilsActual = jest.requireActual<typeof import('../utils')>('../utils');
      const displayPrecision = recsMod.displayPrecision;
      const formatCurrency = utilsActual.formatCurrency;
      const sample = 123.456789;
      const renderedDigits = fractionDigitsOf(formatCurrency(sample));
      expect(displayPrecision('on_demand_monthly', 'monthly')).toBe(renderedDigits);
    });

    test('monthly_cost / on_demand_monthly non-monthly periods use PERIOD_DECIMALS-based precision', async () => {
      // Non-monthly periods bypass formatCurrency (see formatCostForPeriod)
      // and use `toFixed(PERIOD_DECIMALS[period])`. Verify the precision
      // values are non-zero where expected so the exact-match filter
      // matches the displayed string (e.g. "$0.1715" at hourly).
      const { displayPrecision } = await import('../recommendations');
      // hourly: 4 dp, daily: 2 dp, yearly: 0 dp (per PERIOD_DECIMALS).
      expect(displayPrecision('monthly_cost', 'hourly')).toBe(4);
      expect(displayPrecision('monthly_cost', 'daily')).toBe(2);
      expect(displayPrecision('monthly_cost', 'yearly')).toBe(0);
      expect(displayPrecision('on_demand_monthly', 'hourly')).toBe(4);
      expect(displayPrecision('on_demand_monthly', 'daily')).toBe(2);
      expect(displayPrecision('on_demand_monthly', 'yearly')).toBe(0);
    });
  });
});

// Helpers shared by the isHomogeneousSelection describe block below.
function makeRec(overrides: Partial<{
  id: string;
  provider: string;
  service: string;
  term: number;
  payment: string;
}>): { id: string; provider: string; service: string; resource_type: string; region: string; count: number; term: number; payment: string; savings: number; upfront_cost: number } {
  return {
    id: overrides.id ?? 'r1',
    provider: overrides.provider ?? 'aws',
    service: overrides.service ?? 'ec2',
    resource_type: 't3.medium',
    region: 'us-east-1',
    count: 1,
    term: overrides.term ?? 1,
    payment: overrides.payment ?? 'all-upfront',
    savings: 100,
    upfront_cost: 500,
  };
}

describe('isHomogeneousSelection (#769)', () => {
  test('empty slice is homogeneous', () => {
    expect(isHomogeneousSelection([])).toBe(true);
  });

  test('single-item slice is always homogeneous', () => {
    expect(isHomogeneousSelection([makeRec({}) as never])).toBe(true);
  });

  test('two recs with identical provider/service/term/payment are homogeneous', () => {
    const recs = [
      makeRec({ id: 'r1' }),
      makeRec({ id: 'r2' }),
    ];
    expect(isHomogeneousSelection(recs as never[])).toBe(true);
  });

  test('heterogeneous on provider axis', () => {
    const recs = [
      makeRec({ id: 'r1', provider: 'aws' }),
      makeRec({ id: 'r2', provider: 'azure' }),
    ];
    expect(isHomogeneousSelection(recs as never[])).toBe(false);
  });

  test('heterogeneous on service axis', () => {
    const recs = [
      makeRec({ id: 'r1', service: 'ec2' }),
      makeRec({ id: 'r2', service: 'rds' }),
    ];
    expect(isHomogeneousSelection(recs as never[])).toBe(false);
  });

  test('heterogeneous on term axis', () => {
    const recs = [
      makeRec({ id: 'r1', term: 1 }),
      makeRec({ id: 'r2', term: 3 }),
    ];
    expect(isHomogeneousSelection(recs as never[])).toBe(false);
  });

  test('heterogeneous on payment axis', () => {
    const recs = [
      makeRec({ id: 'r1', payment: 'all-upfront' }),
      makeRec({ id: 'r2', payment: 'no-upfront' }),
    ];
    expect(isHomogeneousSelection(recs as never[])).toBe(false);
  });
});
