import * as fs from 'fs';
import * as path from 'path';

/**
 * Regression test for issue #13 — dashboard savings-trend chart collapsed
 * to zero size when filters switched. The CSS now reserves space for both
 * the canvas and the empty-state paragraph so the widget never flashes
 * at zero height during Chart.js destroy→recreate cycles.
 *
 * JSDOM can't lay out CSS, so a computed-style check would silently pass
 * even if the rule were deleted. File-content assertion is the reliable
 * lock.
 */
describe('charts.css empty-state size safeguards', () => {
  const css = fs.readFileSync(
    path.join(__dirname, '..', 'styles', 'charts.css'),
    'utf-8',
  );

  it('.chart-section canvas rule reserves a min-height', () => {
    const rule = css.match(/\.chart-section canvas\s*{([\s\S]*?)}/);
    expect(rule).not.toBeNull();
    expect(rule?.[1] ?? '').toMatch(/min-height\s*:\s*\d+px/);
  });

  it('.chart-section .empty rule reserves height and centres content', () => {
    const rule = css.match(/\.chart-section \.empty\s*{([\s\S]*?)}/);
    expect(rule).not.toBeNull();
    const body = rule?.[1] ?? '';
    expect(body).toMatch(/min-height\s*:\s*\d+px/);
    expect(body).toMatch(/display\s*:\s*flex/);
    expect(body).toMatch(/align-items\s*:\s*center/);
    expect(body).toMatch(/justify-content\s*:\s*center/);
  });
});
