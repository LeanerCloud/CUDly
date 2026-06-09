/**
 * Regression tests for the swagger-ui-dist CDN tags in docs.html.
 *
 * PR #521 pinned the version and added Subresource Integrity (SRI) attributes
 * to the swagger-ui-dist <link>/<script> tags but added no test guarding them
 * (issue #543). Without this guard a future edit could silently drop the
 * `integrity` attribute or revert the exact `@5.32.6` pin to a floating `@5`
 * tag, re-opening the CDN-poisoning exposure tracked in #418 / #447.
 */
import fs from 'fs';
import path from 'path';

const docsHtmlPath = path.join(__dirname, '..', 'docs.html');
const docsHtml = fs.readFileSync(docsHtmlPath, 'utf8');

// Match each swagger-ui-dist <link>/<script> tag so each can be asserted on.
const swaggerTagRe = /<(?:link|script)\b[^>]*unpkg\.com\/swagger-ui-dist@[^>]*>/g;

describe('docs.html swagger-ui CDN tags', () => {
  const tags = docsHtml.match(swaggerTagRe) ?? [];

  test('references at least three swagger-ui-dist assets', () => {
    // css + bundle + standalone-preset
    expect(tags.length).toBeGreaterThanOrEqual(3);
  });

  test.each(tags)('tag pins an exact version and carries SRI: %s', (tag) => {
    // Exact x.y.z version, never a floating major tag like @5.
    expect(tag).toMatch(/swagger-ui-dist@\d+\.\d+\.\d+\//);
    expect(tag).not.toMatch(/swagger-ui-dist@\d+\//);
    // SRI integrity hash present.
    expect(tag).toMatch(/integrity="sha384-[^"]+"/);
    // crossorigin required for SRI on cross-origin resources to apply.
    expect(tag).toContain('crossorigin="anonymous"');
  });
});
