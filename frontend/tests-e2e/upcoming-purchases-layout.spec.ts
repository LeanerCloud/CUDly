/**
 * Layout smoke for the Home -> "Upcoming Scheduled Purchases" card (issue #1776).
 *
 * The reported failure is "the View Details / Cancel buttons render on top of
 * the '$0 Est. monthly savings' text". `.upcoming-card` is a flex row laid out
 * with `justify-content: space-between` and no `gap`, and `.upcoming-actions`
 * had no CSS rule at all. Once a realistic plan name fills the row the
 * space-between free space reaches zero, so the savings text ends up pixel-
 * adjacent to the button block, its label wraps, and the buttons stack.
 *
 * jsdom does not resolve stylesheets into layout, so the jest suite cannot see
 * this: it asserts on DOM structure and stays green while the user sees a
 * collision. The assertions below run against the real bundle in Chromium and
 * measure bounding boxes -- what the browser actually paints.
 *
 * Every card is measured in a single `evaluate` so all boxes come from one
 * layout pass; reading them one locator at a time lets an unrelated async
 * reflow (the Home charts settling after a viewport change) shift the numbers
 * mid-assertion.
 */

import { test, expect, type Page } from '@playwright/test';
import { mockApi, seedAuth } from './fixtures/recs';

/**
 * Minimum breathing room, in CSS px, between two adjacent blocks in the card.
 * Deliberately above zero: the defect this spec pins produced exactly 0px, and
 * text flush against a solid-background button reads as an overlap.
 */
const MIN_GAP = 8;

/** The width at which `.upcoming-card` switches from a flex row to a column. */
const COLUMN_BREAKPOINT = 768;

/**
 * Rows spanning the boundaries where the card's layout breaks: a long plan
 * name (squeezes the row hardest), an absent-savings "$0" figure (the exact
 * value in the bug report), the widest realistic currency string, and a short
 * row for the unsqueezed baseline.
 */
const UPCOMING = [
  {
    execution_id: 'exec-long',
    plan_id: 'plan-1',
    plan_name: 'Production EC2 Compute Savings Plan ramp - phase two rollout',
    scheduled_date: '2026-09-01T00:00:00Z',
    provider: 'aws',
    service: 'elasticache',
    step_number: 12,
    total_steps: 24,
    estimated_savings: 0,
    created_by_user_id: 'user-smoke',
  },
  {
    execution_id: 'exec-big',
    plan_id: 'plan-2',
    plan_name: 'Enterprise commitment',
    scheduled_date: '2026-09-15T00:00:00Z',
    provider: 'azure',
    service: 'compute',
    step_number: 2,
    total_steps: 2,
    estimated_savings: 1234567.89,
    created_by_user_id: 'user-smoke',
  },
  {
    execution_id: 'exec-short',
    plan_id: 'plan-3',
    plan_name: 'Short',
    scheduled_date: '2026-10-01T00:00:00Z',
    provider: 'gcp',
    service: 'gce',
    step_number: 1,
    total_steps: 1,
    estimated_savings: 42,
    created_by_user_id: 'user-smoke',
  },
];

const SUMMARY = {
  potential_monthly_savings: 100,
  total_recommendations: 3,
  active_commitments: 1,
  committed_monthly: 10,
  current_coverage: 50,
  target_coverage: 80,
  ytd_savings: 5,
  by_service: {},
};

/**
 * Widths to assert at. 1600/1280 are desktop, 1024-780 is the band where the
 * row fills up and the collision appeared, 768 is the column breakpoint, and
 * 480/360/320 are the mobile sizes the repo already carries findings for.
 */
const WIDTHS = [1600, 1280, 1024, 900, 800, 780, 768, 640, 480, 360, 320];

interface Box {
  left: number;
  right: number;
  top: number;
  bottom: number;
}

interface CardBoxes {
  card: Box;
  savings: Box;
  actions: Box;
  /** One entry per action button, in DOM order. */
  buttons: Box[];
  /** Number of rendered text lines in the "Est. monthly savings" label. */
  labelLines: number;
  /** Horizontal overflow of the card's own content box. */
  cardOverflow: number;
}

/** Overlapping area of two boxes, in px^2. Zero when they merely touch. */
function overlapArea(a: Box, b: Box): number {
  const x = Math.max(0, Math.min(a.right, b.right) - Math.max(a.left, b.left));
  const y = Math.max(0, Math.min(a.bottom, b.bottom) - Math.max(a.top, b.top));
  return x * y;
}

/**
 * Resize, let any pending reflow settle, then read every box of every card in
 * one pass.
 */
async function measureAt(page: Page, width: number): Promise<CardBoxes[]> {
  await page.setViewportSize({ width, height: 1000 });
  // Two rAFs: the first lands after the resize-driven style recalc, the second
  // after any layout it scheduled (Chart.js resizes on the same tick).
  await page.evaluate(
    () => new Promise<void>((r) => requestAnimationFrame(() => requestAnimationFrame(() => r()))),
  );

  return page.locator('.upcoming-card').evaluateAll((cards) => {
    const box = (el: Element) => {
      const r = el.getBoundingClientRect();
      return { left: r.left, right: r.right, top: r.top, bottom: r.bottom };
    };
    return cards.map((c) => ({
      card: box(c),
      savings: box(c.querySelector('.upcoming-savings')!),
      actions: box(c.querySelector('.upcoming-actions')!),
      buttons: Array.from(c.querySelectorAll('.upcoming-actions button')).map(box),
      // A Range over the text, not the element: `.label` is a block box, so
      // Element.getClientRects() returns a single rect however many lines the
      // text wraps to, which would make the assertion vacuous.
      labelLines: (() => {
        const range = document.createRange();
        range.selectNodeContents(c.querySelector('.upcoming-savings .label')!);
        return range.getClientRects().length;
      })(),
      cardOverflow: c.scrollWidth - c.clientWidth,
    }));
  });
}

async function openHome(page: Page): Promise<void> {
  await seedAuth(page);
  await mockApi(page);

  await page.route('**/api/dashboard/upcoming', (route) =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ purchases: UPCOMING }),
    }),
  );
  await page.route('**/api/dashboard/summary**', (route) =>
    route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(SUMMARY) }),
  );
  await page.goto('/home');
  await expect(page.locator('.upcoming-card')).toHaveCount(UPCOMING.length);
}

test('every fixture row renders both action buttons', async ({ page }) => {
  await openHome(page);
  const cards = await measureAt(page, 1280);
  // Without this, a regression that dropped the Cancel buttons -- the fixture
  // session is an admin, so the issue-#950 gate shows them on every row --
  // would let the button-spacing assertions below pass vacuously.
  expect(cards.map((c) => c.buttons.length)).toEqual([2, 2, 2]);
});

test('the action buttons never overlap or touch the savings figure', async ({ page }) => {
  await openHome(page);

  for (const width of WIDTHS) {
    const cards = await measureAt(page, width);

    for (const [index, purchase] of UPCOMING.entries()) {
      const { savings, actions } = cards[index]!;
      const where = `${purchase.plan_name} @ ${width}px`;

      // The reported defect: the buttons paint over the savings text.
      expect(overlapArea(savings, actions), `${where}: savings/actions overlap`).toBe(0);

      // Above the breakpoint the card is a flex row, so the blocks are
      // separated horizontally; at or below it the card is a column and the
      // separation is vertical.
      const separation =
        width > COLUMN_BREAKPOINT ? actions.left - savings.right : actions.top - savings.bottom;
      expect(separation, `${where}: savings/actions separation`).toBeGreaterThanOrEqual(MIN_GAP);
    }
  }
});

test('the action buttons stay on one row, spaced apart from each other', async ({ page }) => {
  await openHome(page);

  for (const width of WIDTHS) {
    const cards = await measureAt(page, width);

    for (const [index, purchase] of UPCOMING.entries()) {
      const { buttons } = cards[index]!;
      if (buttons.length < 2) continue;

      const [first, second] = buttons as [Box, Box];
      const where = `${purchase.plan_name} @ ${width}px`;

      // Buttons that wrap onto a second line are what pushed the action block
      // into the savings text in the first place.
      expect(second.top, `${where}: buttons share a row`).toBeCloseTo(first.top, 0);
      expect(second.left - first.right, `${where}: gap between buttons`).toBeGreaterThanOrEqual(
        MIN_GAP,
      );
    }
  }
});

test('the savings label keeps one line without overflowing the card', async ({ page }) => {
  await openHome(page);

  for (const width of WIDTHS) {
    const cards = await measureAt(page, width);

    for (const [index, purchase] of UPCOMING.entries()) {
      const { card, actions, labelLines, cardOverflow } = cards[index]!;
      const where = `${purchase.plan_name} @ ${width}px`;

      // A wrapped "Est. monthly / savings" is the squeeze that preceded the
      // collision; one line means the block kept its natural width.
      expect(labelLines, `${where}: savings label line count`).toBe(1);

      // Giving savings and actions their own space must not push the content
      // past the card's edge or open a horizontal scrollbar.
      expect(actions.right, `${where}: actions within card`).toBeLessThanOrEqual(card.right + 1);
      expect(cardOverflow, `${where}: card horizontal overflow`).toBeLessThanOrEqual(0);
    }
  }
});
