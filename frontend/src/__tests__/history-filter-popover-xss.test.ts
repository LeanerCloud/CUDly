/**
 * XSS regression for renderHistoryFilterButton (issue #166 follow-up).
 *
 * renderHistoryFilterButton injects `column` and `label` into HTML attribute
 * values (data-column, aria-label, title) via template literal. Any caller
 * passing a user-controlled string without escaping would produce a stored-XSS
 * vector. escapeHtmlAttr must be applied to both values before interpolation.
 */

import { renderHistoryFilterButton } from '../lib/history-filter-popover';

// Use the real escapeHtmlAttr so DOM-based escaping is exercised, not a
// pass-through stub.
jest.mock('../lib/column-filters', () => ({
  parseNumericFilter: jest.fn(),
}));

describe('renderHistoryFilterButton: attribute-injection XSS guard', () => {
  // This payload attempts to break out of the aria-label attribute and inject
  // a script element. In raw form it would produce:
  //   aria-label=""><script>alert(1)</script><span x=""
  const SCRIPT_PAYLOAD = '"><script>alert(1)</script><span x="';
  // This payload injects an event handler attribute.
  const EVENT_PAYLOAD = '" onmouseover="alert(1)" x="';

  test('hostile label does not inject raw <script> tag into attribute context', () => {
    const html = renderHistoryFilterButton('provider', SCRIPT_PAYLOAD, false);
    // The raw < > and " chars must be entity-encoded; no unescaped angle brackets.
    expect(html).not.toContain('"><script>');
    expect(html).not.toContain('</script>');
    // The encoded form is present (attribute value is escaped, not stripped).
    expect(html).toContain('&lt;script&gt;');
  });

  test('hostile label does not inject event handler attribute', () => {
    const html = renderHistoryFilterButton('provider', EVENT_PAYLOAD, false);
    // The injected " must be encoded; no raw onmouseover= outside an attribute value.
    expect(html).not.toContain('" onmouseover=');
    // The encoded form must appear inside the attribute value.
    expect(html).toContain('&quot; onmouseover=');
  });

  test('hostile column id does not inject raw markup into data-column', () => {
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    const html = renderHistoryFilterButton(SCRIPT_PAYLOAD as any, 'Provider', false);
    expect(html).not.toContain('><script>');
    expect(html).not.toContain('</script>');
    expect(html).toContain('&lt;script&gt;');
  });

  test('safe column and label pass through correctly', () => {
    const html = renderHistoryFilterButton('provider', 'Provider', false);
    expect(html).toContain('data-column="provider"');
    expect(html).toContain('aria-label="Filter Provider"');
    expect(html).toContain('class="history-column-filter-btn"');
  });

  test('active flag adds "active" class and updates aria-label', () => {
    const html = renderHistoryFilterButton('savings', 'Monthly Savings', true);
    expect(html).toContain('class="history-column-filter-btn active"');
    expect(html).toContain('aria-label="Filter Monthly Savings - currently active"');
  });
});
