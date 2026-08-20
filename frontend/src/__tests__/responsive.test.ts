import * as fs from 'fs';
import * as path from 'path';

/**
 * CSS-level regression test for issue #10 (responsive nav).
 *
 * JSDOM does not evaluate @media queries, so asserting on
 * getComputedStyle(el).flexWrap would silently pass even if the rule
 * were deleted. Reading the source CSS and asserting on its contents
 * is the only way to lock down the fix.
 */
describe('responsive.css nav wrap rules', () => {
  const css = fs.readFileSync(
    path.join(__dirname, '../styles/responsive.css'),
    'utf-8',
  );

  it('declares a @media (max-width: 1100px) block', () => {
    expect(css).toMatch(/@media\s*\(max-width:\s*1100px\)/);
  });

  it('sets flex-wrap: wrap on .tabs inside the 1100px block', () => {
    // Extract the contents of the 1100px block and assert the .tabs rule
    // wraps. Using a [\s\S] group instead of a dotall flag to stay
    // compatible with older TS lib targets.
    const match = css.match(/@media\s*\(max-width:\s*1100px\)\s*{([\s\S]*?)\n}/);
    expect(match).not.toBeNull();
    const body = match?.[1] ?? '';
    expect(body).toMatch(/\.tabs\s*{[\s\S]*?flex-wrap:\s*wrap/);
  });

  it('drops overflow-x: auto from the .tabs rule at ≤768px (wrap replaces it)', () => {
    const match = css.match(/@media\s*\(max-width:\s*768px\)\s*{([\s\S]*?)\n}/);
    expect(match).not.toBeNull();
    const body = match?.[1] ?? '';
    // Isolate the .tabs rule specifically and ensure it no longer sets
    // overflow-x. Other selectors in the block (e.g. `table`) may still
    // use overflow-x: auto — that is intentional and unrelated.
    const tabsRule = body.match(/\.tabs\s*{[\s\S]*?}/);
    expect(tabsRule).not.toBeNull();
    expect(tabsRule?.[0] ?? '').not.toMatch(/overflow-x/);
  });

  /**
   * Issue #10 wrapped #user-info at ≤768px to stop it overflowing the
   * header. That never worked: the topbar is a fixed --cudly-topbar-h box,
   * so the extra rows painted over the page content instead (issue #1779).
   * The regions are relocated into the drawer now, and the topbar copies
   * are laid out away rather than left to paint behind the content.
   *
   * Source-text assertions only -- whether the relocated controls are
   * actually visible and tappable is measured in a real browser by
   * tests-e2e/mobile-header-drawer.spec.ts.
   */
  it('takes #user-info and #topbar-filters out of the topbar at ≤768px', () => {
    const match = css.match(/@media\s*\(max-width:\s*768px\)\s*{([\s\S]*?)\n}/);
    expect(match).not.toBeNull();
    const body = match?.[1] ?? '';
    expect(body).toMatch(
      /\.app-topbar\s*>\s*#topbar-filters,\s*\.app-topbar\s*>\s*#user-info\s*{[\s\S]*?display:\s*none/,
    );
  });

  it('styles both regions for the drawer slot at ≤768px', () => {
    const match = css.match(/@media\s*\(max-width:\s*768px\)\s*{([\s\S]*?)\n}/);
    expect(match).not.toBeNull();
    const body = match?.[1] ?? '';
    expect(body).toMatch(/\.app-sidebar-extras\s*{[\s\S]*?display:\s*flex/);
    expect(body).toMatch(/\.app-sidebar-extras #topbar-filters/);
    expect(body).toMatch(/\.app-sidebar-extras #user-info/);
  });

  it('does not stack the topbar into a column at ≤768px', () => {
    const match = css.match(/@media\s*\(max-width:\s*768px\)\s*{([\s\S]*?)\n}/);
    expect(match).not.toBeNull();
    const body = match?.[1] ?? '';
    // The `header { flex-direction: column }` rule was the overflow source.
    expect(body).not.toMatch(/(^|\n)\s*header\s*{/);
    expect(body).toMatch(/\.app-topbar\s*{[\s\S]*?justify-content:\s*flex-start/);
  });
});
