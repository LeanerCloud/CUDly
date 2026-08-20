/**
 * Mobile topbar / drawer layout at a phone viewport (issue #1779).
 *
 * The reported failure is that below the drawer breakpoint the CUDly logo,
 * the signed-in email, the admin badge, API Docs, Feedback, Logout and the
 * Provider / Account filter chips paint as faint text over the page content
 * and cannot be used. The markup was always correct: the topbar is a fixed
 * --cudly-topbar-h box, and a `header { flex-direction: column }` rule
 * stacked those regions inside it, so every row past the first overflowed
 * past the gradient and landed on the content below in white-on-white.
 *
 * That is invisible to jsdom, which resolves no stylesheets into layout: a
 * jest assertion that "the header is hidden on mobile" passes whether or not
 * anything was fixed. Every assertion below therefore measures what Chromium
 * actually paints -- bounding boxes against the topbar and the viewport --
 * rather than what the DOM contains.
 */

import { test, expect, type Page, type Locator } from '@playwright/test';
import { mockApi, seedAuth } from './fixtures/recs';

/* iPhone 12/13/14 logical width, the viewport quoted in the report. */
const PHONE = { width: 390, height: 844 };

/* Apple HIG / WCAG 2.5.8 minimum touch target, enforced repo-wide. */
const MIN_TOUCH_PX = 44;

interface Box {
  x: number;
  y: number;
  width: number;
  height: number;
  bottom: number;
  right: number;
}

async function box(locator: Locator): Promise<Box> {
  return locator.evaluate((el) => {
    const r = el.getBoundingClientRect();
    return { x: r.x, y: r.y, width: r.width, height: r.height, bottom: r.bottom, right: r.right };
  });
}

/**
 * Every control the report names, keyed by the label used in assertion
 * messages. Scoped to the sidebar so a stray desktop copy cannot satisfy
 * them. The chips are matched on their aria-label, set by createChipSelect.
 */
const DRAWER_CONTROLS: Record<string, string> = {
  'signed-in email': '#sidebar #user-email-display',
  'admin badge': '#sidebar #user-role-display',
  'API Docs link': '#sidebar #user-info a[href="/docs/"]',
  'Feedback link': '#sidebar #feedback-link',
  'Logout button': '#sidebar #logout-btn',
  'Provider chip': '#sidebar button[aria-label="Provider"]',
  'Account chip': '#sidebar button[aria-label="Account"]',
};

/** The subset that is tappable and so bound by the 44x44 minimum. */
const TAPPABLE = [
  'API Docs link',
  'Feedback link',
  'Logout button',
  'Provider chip',
  'Account chip',
] as const;

async function openApp(page: Page): Promise<void> {
  await seedAuth(page);
  await mockApi(page);
  await page.goto('/home');
  // The account chip is the last thing to mount (it awaits /accounts/list),
  // so its presence means the topbar controls have finished rendering
  // wherever they were placed.
  await expect(page.locator('button[aria-label="Account"]')).toHaveCount(1);
}

async function openDrawer(page: Page): Promise<void> {
  await page.locator('#hamburger-btn').click();
  await expect(page.locator('#hamburger-btn')).toHaveAttribute('aria-expanded', 'true');
  // Wait out the 0.25s slide-in so boxes are measured at rest, not mid-transform.
  await expect
    .poll(async () => (await box(page.locator('#sidebar'))).x, { timeout: 5_000 })
    .toBeCloseTo(0, 1);
}

test.describe('phone viewport', () => {
  test.use({ viewport: PHONE });

  /**
   * The defect itself. Anything still parented to the topbar must paint
   * inside it; pre-fix the stacked rows spilled hundreds of pixels below
   * the topbar's bottom edge and over the page content.
   */
  test('nothing in the topbar paints outside it', async ({ page }) => {
    await openApp(page);

    const topbar = await box(page.locator('.app-topbar'));
    expect(topbar.height, 'topbar height').toBeGreaterThan(0);

    const overflowing = await page.locator('.app-topbar').evaluate((header, limit) => {
      const spills: { text: string; bottom: number }[] = [];
      for (const el of Array.from(header.querySelectorAll('*'))) {
        const r = el.getBoundingClientRect();
        if (r.width === 0 && r.height === 0) continue;
        if (r.bottom > limit + 1) {
          spills.push({ text: (el.textContent ?? '').trim().slice(0, 40), bottom: r.bottom });
        }
      }
      return spills;
    }, topbar.bottom);

    expect(overflowing, 'topbar descendants painting below the topbar').toEqual([]);

    // Catches the other shape of the same regression: a topbar that grows to
    // fit the extra rows rather than overflowing. Compared against the token
    // the layout declares, not a number copied into this file.
    const declaredHeight = await page.evaluate(() =>
      parseFloat(
        getComputedStyle(document.documentElement).getPropertyValue('--cudly-topbar-h'),
      ),
    );
    expect(declaredHeight, '--cudly-topbar-h resolves').toBeGreaterThan(0);
    expect(topbar.height, 'topbar stays one row tall').toBeCloseTo(declaredHeight, 0);
  });

  test('the drawer controls are off-screen until the drawer is opened', async ({ page }) => {
    await openApp(page);

    // Parked off the left edge by the sidebar transform, not merely painted
    // behind the content where a stray tap could still reach them.
    for (const [label, selector] of Object.entries(DRAWER_CONTROLS)) {
      const { right } = await box(page.locator(selector));
      expect(right, `${label} is off-screen while the drawer is closed`).toBeLessThanOrEqual(0);
    }
  });

  test('every header control and both filter chips are reachable in the drawer', async ({ page }) => {
    await openApp(page);
    await openDrawer(page);

    for (const [label, selector] of Object.entries(DRAWER_CONTROLS)) {
      const locator = page.locator(selector);
      await expect(locator, `${label} is in the drawer`).toHaveCount(1);
      await expect(locator, `${label} is visible`).toBeVisible();

      const b = await box(locator);
      expect(b.x, `${label} left edge on-screen`).toBeGreaterThanOrEqual(0);
      expect(b.right, `${label} right edge on-screen`).toBeLessThanOrEqual(PHONE.width);
      expect(b.y, `${label} top edge on-screen`).toBeGreaterThanOrEqual(0);
      expect(b.bottom, `${label} bottom edge on-screen`).toBeLessThanOrEqual(PHONE.height);
    }
  });

  test('the admin badge reads admin for an administrator', async ({ page }) => {
    await openApp(page);
    await openDrawer(page);
    await expect(page.locator('#sidebar #user-role-display')).toHaveText('admin');
    await expect(page.locator('#sidebar #user-email-display')).toHaveText('smoke@example.com');
  });

  test('drawer controls meet the 44x44 touch minimum', async ({ page }) => {
    await openApp(page);
    await openDrawer(page);

    for (const label of TAPPABLE) {
      const b = await box(page.locator(DRAWER_CONTROLS[label]!));
      expect(b.width, `${label} hit-target width`).toBeGreaterThanOrEqual(MIN_TOUCH_PX);
      expect(b.height, `${label} hit-target height`).toBeGreaterThanOrEqual(MIN_TOUCH_PX);
    }
  });

  /**
   * Present and on-screen is not the same as usable: the chips have to open
   * their popover inside the viewport and actually apply the filter.
   */
  test('the Provider chip filters from inside the drawer', async ({ page }) => {
    await openApp(page);
    await openDrawer(page);

    const chip = page.locator('#sidebar button[aria-label="Provider"]');
    await chip.click();

    const menu = page.locator('#sidebar .chip-select-menu:not(.hidden)');
    await expect(menu).toBeVisible();
    const menuBox = await box(menu);
    expect(menuBox.x, 'popover left edge on-screen').toBeGreaterThanOrEqual(0);
    expect(menuBox.right, 'popover right edge on-screen').toBeLessThanOrEqual(PHONE.width);
    expect(menuBox.bottom, 'popover bottom edge on-screen').toBeLessThanOrEqual(PHONE.height);

    await menu.getByRole('option', { name: 'AWS', exact: true }).click();

    await expect(chip).toContainText('AWS');
    await expect.poll(() => new URL(page.url()).searchParams.get('provider')).toBe('aws');
    // Picking a filter must not dismiss the drawer -- the next chip is
    // right below it.
    await expect(page.locator('#hamburger-btn')).toHaveAttribute('aria-expanded', 'true');
  });

  test('an account action closes the drawer', async ({ page }) => {
    await openApp(page);
    await openDrawer(page);

    await page.locator('#sidebar #feedback-link').click();
    await expect(page.locator('#hamburger-btn')).toHaveAttribute('aria-expanded', 'false');
  });

  /**
   * Widening past the breakpoint has to hand the controls back, or a
   * desktop browser resized down and up again loses its header for good.
   */
  test('widening past the breakpoint returns the controls to the topbar', async ({ page }) => {
    await openApp(page);
    await expect(page.locator('#sidebar #user-info')).toHaveCount(1);

    await page.setViewportSize({ width: 1280, height: 900 });
    await expect(page.locator('.app-topbar > #user-info')).toHaveCount(1);
    await expect(page.locator('.app-topbar > #topbar-filters')).toHaveCount(1);

    for (const selector of ['#logout-btn', '#feedback-link', 'button[aria-label="Account"]']) {
      await expect(page.locator(selector)).toBeVisible();
    }

    const topbar = await box(page.locator('.app-topbar'));
    const userInfo = await box(page.locator('#user-info'));
    expect(userInfo.bottom, 'user actions paint inside the topbar').toBeLessThanOrEqual(
      topbar.bottom + 1,
    );
  });
});
