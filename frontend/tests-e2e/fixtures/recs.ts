/**
 * Canned API fixtures + page.route helper for the Recommendations smoke spec
 * (issue #167).
 *
 * The fixture seeds ~19 recommendations spanning the matrix needed by every
 * checklist assertion:
 *   - 3 providers (aws / azure / gcp)
 *   - 4 services (ec2 / rds / aks / gce)
 *   - 2 terms (1y / 3y)
 *   - monthly savings spanning [10, 50, 100, 150, 250, 1200] so numeric
 *     expressions like ">100", "50..200", "5, 50..200, >1000" narrow to
 *     specific counts the assertions can pin against.
 *
 * `mockApi(page)` wires `page.route` against every `/api/*` endpoint the
 * Opportunities tab touches on load, plus the POST endpoints the bottom-
 * action box invokes (`/purchases/execute`, `/plans`). Each call is recorded
 * on the returned `calls` array so tests can assert on URLs and POST bodies
 * without leaning on `expect.assertions`-style counts.
 *
 * The fixture is intentionally stateless across tests - each test calls
 * `mockApi(page)` for a fresh `{ calls }` record. Tests are free to
 * post-hoc override individual routes with their own `page.route` calls
 * (which take precedence over the catch-all installed here, because Playwright
 * executes handlers in reverse-registration order).
 */

import type { Page, Route } from '@playwright/test';

// Mirrors api.Provider (frontend/src/api/types.ts) — kept inline to avoid
// pulling the full type graph into a Playwright test bundle.
export type Provider = 'aws' | 'azure' | 'gcp';

// LocalRecommendation subset used by the smoke. The full type ships extra
// optional fields the table renders defensively, so we set only what the
// table reads.
export interface SmokeRec {
  id: string;
  provider: Provider;
  cloud_account_id: string;
  service: string;
  resource_type: string;
  region: string;
  count: number;
  term: 1 | 3;
  payment: 'all-upfront' | 'partial-upfront' | 'no-upfront' | 'monthly';
  savings: number;
  upfront_cost: number;
  monthly_cost: number;
  on_demand_monthly: number;
  effective_savings_pct: number;
}

/**
 * 19 rows - chosen so:
 *   - `Provider = AWS` narrows to 11.
 *   - `Service = ec2` narrows to 6.
 *   - `Monthly Savings > 100` narrows to 9 (savings in {150, 250, 1200}).
 *   - `Monthly Savings 50..200` narrows to 13 (inclusive range; savings in
 *     {50, 100, 150}).
 *   - `Monthly Savings 5, 50..200, >1000` narrows to 14 (adds row r04 with
 *     savings=1200 to the 50..200 set; the literal "5" matches no rows).
 *   - Two different terms (1y, 3y) present in any single-provider slice so
 *     multi-term selections produce >1 fan-out bucket on Purchase.
 */
export const RECS: SmokeRec[] = [
  { id: 'r01', provider: 'aws',   cloud_account_id: 'acct-001', service: 'ec2', resource_type: 't3.medium',      region: 'us-east-1',      count: 4, term: 1, payment: 'all-upfront',  savings: 150,  upfront_cost: 2400, monthly_cost: 350, on_demand_monthly: 500, effective_savings_pct: 30 },
  { id: 'r02', provider: 'aws',   cloud_account_id: 'acct-001', service: 'ec2', resource_type: 't3.large',       region: 'us-east-1',      count: 2, term: 3, payment: 'no-upfront',   savings: 250,  upfront_cost: 0,    monthly_cost: 280, on_demand_monthly: 530, effective_savings_pct: 47 },
  { id: 'r03', provider: 'aws',   cloud_account_id: 'acct-001', service: 'ec2', resource_type: 'm5.xlarge',      region: 'us-west-2',      count: 1, term: 1, payment: 'partial-upfront', savings: 50,  upfront_cost: 600,  monthly_cost: 200, on_demand_monthly: 250, effective_savings_pct: 20 },
  { id: 'r04', provider: 'aws',   cloud_account_id: 'acct-002', service: 'ec2', resource_type: 'm5.large',       region: 'eu-west-1',      count: 3, term: 3, payment: 'all-upfront',  savings: 1200, upfront_cost: 8400, monthly_cost: 0,   on_demand_monthly: 1200, effective_savings_pct: 60 },
  { id: 'r05', provider: 'aws',   cloud_account_id: 'acct-002', service: 'ec2', resource_type: 'c5.large',       region: 'eu-west-1',      count: 2, term: 1, payment: 'all-upfront',  savings: 100,  upfront_cost: 1600, monthly_cost: 0,   on_demand_monthly: 200, effective_savings_pct: 50 },
  { id: 'r06', provider: 'aws',   cloud_account_id: 'acct-002', service: 'ec2', resource_type: 'r5.large',       region: 'us-east-1',      count: 1, term: 1, payment: 'no-upfront',   savings: 10,   upfront_cost: 0,    monthly_cost: 150, on_demand_monthly: 160, effective_savings_pct: 6  },
  { id: 'r07', provider: 'aws',   cloud_account_id: 'acct-001', service: 'rds', resource_type: 'db.t3.medium',   region: 'us-east-1',      count: 1, term: 1, payment: 'all-upfront',  savings: 50,   upfront_cost: 800,  monthly_cost: 130, on_demand_monthly: 180, effective_savings_pct: 28 },
  { id: 'r08', provider: 'aws',   cloud_account_id: 'acct-001', service: 'rds', resource_type: 'db.m5.large',    region: 'us-west-2',      count: 2, term: 3, payment: 'all-upfront',  savings: 150,  upfront_cost: 3600, monthly_cost: 0,   on_demand_monthly: 290, effective_savings_pct: 52 },
  { id: 'r09', provider: 'aws',   cloud_account_id: 'acct-002', service: 'rds', resource_type: 'db.r5.xlarge',   region: 'eu-west-1',      count: 1, term: 1, payment: 'no-upfront',   savings: 100,  upfront_cost: 0,    monthly_cost: 450, on_demand_monthly: 550, effective_savings_pct: 18 },
  { id: 'r10', provider: 'aws',   cloud_account_id: 'acct-002', service: 'rds', resource_type: 'db.m5.xlarge',   region: 'us-east-1',      count: 1, term: 3, payment: 'partial-upfront', savings: 150, upfront_cost: 1800, monthly_cost: 280, on_demand_monthly: 450, effective_savings_pct: 33 },
  { id: 'r11', provider: 'aws',   cloud_account_id: 'acct-001', service: 'rds', resource_type: 'db.r5.large',    region: 'us-west-2',      count: 2, term: 1, payment: 'all-upfront',  savings: 50,   upfront_cost: 700,  monthly_cost: 220, on_demand_monthly: 280, effective_savings_pct: 21 },
  { id: 'r12', provider: 'azure', cloud_account_id: 'acct-100', service: 'aks', resource_type: 'Standard_D2_v3', region: 'eastus',         count: 3, term: 1, payment: 'all-upfront',  savings: 100,  upfront_cost: 1500, monthly_cost: 200, on_demand_monthly: 320, effective_savings_pct: 31 },
  { id: 'r13', provider: 'azure', cloud_account_id: 'acct-100', service: 'aks', resource_type: 'Standard_D4_v3', region: 'eastus',         count: 1, term: 3, payment: 'all-upfront',  savings: 150,  upfront_cost: 3000, monthly_cost: 0,   on_demand_monthly: 280, effective_savings_pct: 53 },
  { id: 'r14', provider: 'azure', cloud_account_id: 'acct-101', service: 'aks', resource_type: 'Standard_F2_v2', region: 'westeurope',     count: 2, term: 1, payment: 'no-upfront',   savings: 50,   upfront_cost: 0,    monthly_cost: 140, on_demand_monthly: 190, effective_savings_pct: 26 },
  { id: 'r15', provider: 'azure', cloud_account_id: 'acct-100', service: 'aks', resource_type: 'Standard_E4_v3', region: 'eastus',         count: 1, term: 3, payment: 'partial-upfront', savings: 250, upfront_cost: 4200, monthly_cost: 120, on_demand_monthly: 470, effective_savings_pct: 53 },
  { id: 'r16', provider: 'gcp',   cloud_account_id: 'acct-200', service: 'gce', resource_type: 'n2-standard-2',  region: 'us-central1',    count: 4, term: 1, payment: 'monthly',      savings: 100,  upfront_cost: 0,    monthly_cost: 240, on_demand_monthly: 340, effective_savings_pct: 29 },
  { id: 'r17', provider: 'gcp',   cloud_account_id: 'acct-200', service: 'gce', resource_type: 'n2-standard-4',  region: 'us-central1',    count: 1, term: 3, payment: 'all-upfront',  savings: 250,  upfront_cost: 5400, monthly_cost: 0,   on_demand_monthly: 410, effective_savings_pct: 61 },
  { id: 'r18', provider: 'gcp',   cloud_account_id: 'acct-201', service: 'gce', resource_type: 'e2-small',       region: 'europe-west1',   count: 2, term: 1, payment: 'monthly',      savings: 10,   upfront_cost: 0,    monthly_cost: 80,  on_demand_monthly: 95,  effective_savings_pct: 11 },
  { id: 'r19', provider: 'gcp',   cloud_account_id: 'acct-201', service: 'gce', resource_type: 'c2-standard-4',  region: 'europe-west1',   count: 1, term: 3, payment: 'no-upfront',   savings: 150,  upfront_cost: 0,    monthly_cost: 220, on_demand_monthly: 380, effective_savings_pct: 42 },
];

export const ACCOUNTS = [
  { id: 'acct-001', name: 'AWS Prod (acct-001)',  provider: 'aws',   external_id: '111111111111' },
  { id: 'acct-002', name: 'AWS Staging (acct-002)', provider: 'aws',   external_id: '222222222222' },
  { id: 'acct-100', name: 'Azure Prod (acct-100)', provider: 'azure', external_id: 'sub-aaaa' },
  { id: 'acct-101', name: 'Azure Dev (acct-101)',  provider: 'azure', external_id: 'sub-bbbb' },
  { id: 'acct-200', name: 'GCP Prod (acct-200)',   provider: 'gcp',   external_id: 'project-zzzz' },
  { id: 'acct-201', name: 'GCP Sandbox (acct-201)', provider: 'gcp',   external_id: 'project-yyyy' },
];

export const SUMMARY = {
  total_recommendations: RECS.length,
  total_upfront_cost: RECS.reduce((s, r) => s + r.upfront_cost, 0),
  potential_monthly_savings: RECS.reduce((s, r) => s + r.savings, 0),
  avg_payback_months: 6,
};

/**
 * Record of an intercepted HTTP call. The smoke spec asserts on these to
 * confirm e.g. that toggling Provider triggers a backend re-fetch, while
 * toggling Service does not.
 */
export interface ApiCall {
  url: string;
  method: string;
  postData: string | null;
}

export interface MockHandle {
  /** All intercepted /api/* calls in the order they fired. */
  calls: ApiCall[];
  /**
   * Add a one-shot delay (ms) to the next `/api/recommendations*` GET so a
   * test can observe `aria-busy="true"` between the request and its response.
   * Cleared after one use.
   */
  delayNextRecommendationsFetch(ms: number): void;
}

/**
 * Build a JSON response with sensible defaults.
 */
function jsonRoute(route: Route, body: unknown, status = 200): Promise<void> {
  return route.fulfill({
    status,
    contentType: 'application/json',
    body: JSON.stringify(body),
  });
}

/**
 * Wire `page.route` against every `/api/*` endpoint the Opportunities tab
 * exercises. Returns a `MockHandle` whose `.calls` array captures the URL,
 * method, and POST body of every intercepted request.
 */
export async function mockApi(page: Page): Promise<MockHandle> {
  const calls: ApiCall[] = [];
  let pendingDelayMs = 0;

  const record = (route: Route): void => {
    const request = route.request();
    calls.push({
      url: request.url(),
      method: request.method(),
      postData: request.postData(),
    });
  };

  // Catch-all for any /api/* not covered by specific handlers below.
  // Registered FIRST so specific handlers (registered after this) take
  // precedence: Playwright executes handlers in reverse-registration order,
  // so the last-registered handler wins for any URL that matches multiple
  // patterns. Registering the catch-all first ensures it only fires for
  // truly unmatched requests.
  await page.route('**/api/**', async (route) => {
    record(route);
    await jsonRoute(route, {}, 404);
  });

  // Public info - admin already exists, login modal does not appear.
  await page.route('**/api/public-info', async (route) => {
    record(route);
    await jsonRoute(route, {
      admin_exists: true,
      api_key_secret_url: '',
      version: 'smoke-test',
    });
  });

  // Auth me — returns a logged-in user so updateUserUI populates header.
  await page.route('**/api/auth/me', async (route) => {
    record(route);
    await jsonRoute(route, {
      id: 'user-smoke',
      email: 'smoke@example.com',
      role: 'admin',
    });
  });

  // Config — empty global defaults so cachedGlobalDefault* stays at fallback.
  await page.route('**/api/config', async (route) => {
    record(route);
    await jsonRoute(route, { global: {}, services: {} });
  });

  // Accounts — feeds accountNamesCache so categorical popovers show names.
  await page.route('**/api/accounts**', async (route) => {
    record(route);
    if (route.request().method() !== 'GET') {
      await route.fulfill({ status: 405, body: '' });
      return;
    }
    await jsonRoute(route, ACCOUNTS);
  });

  // Main recommendations list. Filtered server-side by the `provider` query
  // param (the only filter the frontend still pushes down — Service / Region
  // / numerics are all client-side via applyColumnFilters).
  //
  // Registered before the more-specific /freshness, /refresh, and /*/detail
  // handlers below: Playwright runs handlers in reverse-registration order, so
  // those later-registered patterns take precedence over this wildcard when the
  // URL matches both.
  await page.route('**/api/recommendations*', async (route) => {
    record(route);
    if (route.request().method() !== 'GET') {
      await route.fulfill({ status: 405, body: '' });
      return;
    }
    const u = new URL(route.request().url());
    const provider = u.searchParams.get('provider');
    let rows = RECS;
    if (provider && provider !== 'all' && provider !== '') {
      rows = RECS.filter((r) => r.provider === provider);
    }
    if (pendingDelayMs > 0) {
      const wait = pendingDelayMs;
      pendingDelayMs = 0;
      await new Promise((res) => setTimeout(res, wait));
    }
    await jsonRoute(route, {
      recommendations: rows,
      summary: SUMMARY,
      regions: ['us-east-1', 'us-west-2', 'eu-west-1', 'eastus', 'westeurope', 'us-central1', 'europe-west1'],
    });
  });

  // Freshness — fresh, no error, so auto-refresh-on-stale stays quiet.
  // Registered AFTER **/api/recommendations* so it takes precedence over the
  // wildcard for freshness-specific URLs.
  await page.route('**/api/recommendations/freshness', async (route) => {
    record(route);
    await jsonRoute(route, {
      last_collected_at: new Date(Date.now() - 60 * 60 * 1000).toISOString(),
      last_collection_error: null,
    });
  });

  // Refresh — synchronous-success path. Tests that need to assert on refresh
  // behaviour can override this with their own page.route.
  // Registered AFTER **/api/recommendations* so it takes precedence over the
  // wildcard for refresh-specific URLs.
  await page.route('**/api/recommendations/refresh', async (route) => {
    record(route);
    await jsonRoute(route, {
      started_at: new Date().toISOString(),
      last_collected_at: new Date().toISOString(),
    });
  });

  // Purchase execute — POST. We always return a fake execution id so the
  // toast path resolves; tests assert on the recorded POST body.
  await page.route('**/api/purchases/execute', async (route) => {
    record(route);
    await jsonRoute(route, {
      execution_id: `exec-${Date.now()}`,
      status: 'completed',
      total_savings: 0,
      total_upfront: 0,
      results: [],
    });
  });

  // Plans CRUD — POST creates, GET lists. The smoke only exercises POST
  // from the bottom-action-box "Create Plan" flow.
  await page.route('**/api/plans**', async (route) => {
    record(route);
    const method = route.request().method();
    if (method === 'POST') {
      await jsonRoute(route, { id: `plan-${Date.now()}`, name: 'smoke-plan' });
      return;
    }
    if (method === 'GET') {
      await jsonRoute(route, []);
      return;
    }
    await route.fulfill({ status: 405, body: '' });
  });

  // Per-id detail — covers the row-click drawer. Returns a benign empty
  // payload so accidental row clicks (e.g. while clicking a checkbox)
  // do not crash on a 404.
  await page.route('**/api/recommendations/*/detail', async (route) => {
    record(route);
    await jsonRoute(route, {
      id: 'detail-smoke',
      usage_history: [],
      confidence_bucket: 'low',
      provenance_note: '',
    });
  });

  return {
    calls,
    delayNextRecommendationsFetch(ms: number): void {
      pendingDelayMs = ms;
    },
  };
}

/**
 * Seed sessionStorage with an auth token before page navigation so the
 * SPA's `isAuthenticated()` returns true and the login modal stays hidden.
 *
 * Must be called BEFORE `page.goto(...)`.
 */
export async function seedAuth(page: Page): Promise<void> {
  await page.addInitScript(() => {
    sessionStorage.setItem('authToken', 'smoke-token');
    sessionStorage.setItem('csrfToken', 'smoke-csrf');
  });
}
