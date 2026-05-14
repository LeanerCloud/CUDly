/**
 * Playwright config for the frontend end-to-end smoke suite.
 *
 * Scope: real-browser smoke against the production webpack bundle (`dist/`),
 * with API responses mocked at the network layer via `page.route`. Catches
 * CSS / layout / sticky-positioning / IntersectionObserver edges that the
 * jsdom-based jest suite cannot simulate (see issue #167).
 *
 * The suite stays hermetic on purpose:
 *   - No live cloud account, no Go backend boot.
 *   - The `webServer` block boots `npx serve` against `dist/` so the test
 *     hits the same minified bundle CI ships to production.
 *   - Mocks are wired per-test in tests-e2e/fixtures/recs.ts so individual
 *     tests get fresh `{ calls }` capture arrays (no shared mutable state).
 *
 * Browsers: Chromium only. Per the plan in issue #167, multi-browser
 * coverage (Firefox / WebKit) is explicitly out of scope — the CSS /
 * sticky-positioning concerns the smoke targets are Chromium-faithful in
 * practice, and the single-project setup keeps CI under ~90s.
 */

import { defineConfig, devices } from '@playwright/test';

const PORT = 4173;
const HOST = '127.0.0.1';

export default defineConfig({
  testDir: 'tests-e2e',
  testMatch: '**/*.spec.ts',

  /* Run tests in parallel within a single browser project. */
  fullyParallel: true,

  /* Fail the build on `test.only` left in source. */
  forbidOnly: !!process.env.CI,

  /* Retry once on CI to absorb transient flakes (e.g. webServer warmup). */
  retries: process.env.CI ? 1 : 0,

  /* Use a single worker on CI for predictable artefacts; full parallel locally. */
  workers: process.env.CI ? 1 : undefined,

  reporter: process.env.CI
    ? [['list'], ['html', { open: 'never' }]]
    : [['list']],

  use: {
    baseURL: `http://${HOST}:${PORT}`,
    trace: 'retain-on-failure',
    screenshot: 'only-on-failure',
    video: 'retain-on-failure',
    actionTimeout: 10_000,
    navigationTimeout: 15_000,
  },

  projects: [
    {
      name: 'chromium',
      use: { ...devices['Desktop Chrome'] },
    },
  ],

  /**
   * Boot a static file server against the built `dist/` bundle before
   * the tests run, and tear it down after.
   *
   * `serve -s` ("single-page-app" mode) falls back to `index.html` for
   * any unmatched path. That matters because the SPA may push routes
   * the static server otherwise wouldn't recognise.
   *
   * `reuseExistingServer: !process.env.CI` — locally, leave a running
   * `npx serve` alone so iteration is fast; on CI always boot fresh.
   */
  webServer: {
    command: `npx serve -s dist -l ${PORT} --no-clipboard`,
    url: `http://${HOST}:${PORT}/`,
    reuseExistingServer: !process.env.CI,
    timeout: 60_000,
    stdout: 'pipe',
    stderr: 'pipe',
  },
});
