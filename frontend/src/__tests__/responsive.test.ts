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

  it('wraps #user-info inside the ≤768px block so header children do not overflow', () => {
    const match = css.match(/@media\s*\(max-width:\s*768px\)\s*{([\s\S]*?)\n}/);
    expect(match).not.toBeNull();
    const body = match?.[1] ?? '';
    expect(body).toMatch(/#user-info\s*{[\s\S]*?flex-wrap:\s*wrap/);
  });
});
