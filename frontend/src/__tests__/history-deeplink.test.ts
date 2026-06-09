/**
 * Tests for the Purchase History deep-link parser AND the scroll+
 * highlight handler. Two link sources land here:
 *   - the Recommendations view's "recently purchased" suppression
 *     badge, which links to #history?execution=<id>.
 *   - the scheduled-purchase email's Review & Edit button (#581
 *     follow-up), which links to /purchases#history?execution=<id>.
 */

jest.mock('../toast', () => ({
  showToast: jest.fn(),
}));

jest.mock('../state', () => ({
  subscribeProvider: jest.fn().mockReturnValue(() => {}),
  subscribeAccount: jest.fn().mockReturnValue(() => {}),
  getCurrentUser: jest.fn(),
  getCurrentProvider: jest.fn().mockReturnValue(''),
  getCurrentAccountIDs: jest.fn().mockReturnValue([]),
}));

import { applyExecutionDeepLink, readDeepLinkExecutionID } from '../history';
import { showToast } from '../toast';

describe('readDeepLinkExecutionID', () => {
  const originalHash = window.location.hash;
  afterEach(() => {
    window.location.hash = originalHash;
  });

  test("returns '' when hash is empty", () => {
    window.location.hash = '';
    expect(readDeepLinkExecutionID()).toBe('');
  });

  test("returns '' when hash has no query string", () => {
    window.location.hash = '#history';
    expect(readDeepLinkExecutionID()).toBe('');
  });

  test("extracts execution id from query portion of hash", () => {
    window.location.hash = '#history?execution=abc123';
    expect(readDeepLinkExecutionID()).toBe('abc123');
  });

  test("returns '' when query has no execution param", () => {
    window.location.hash = '#history?foo=bar';
    expect(readDeepLinkExecutionID()).toBe('');
  });

  test("handles URL-encoded execution IDs", () => {
    window.location.hash = '#history?execution=abc%20def';
    expect(readDeepLinkExecutionID()).toBe('abc def');
  });

  test("handles multiple query params", () => {
    window.location.hash = '#history?foo=bar&execution=xyz789';
    expect(readDeepLinkExecutionID()).toBe('xyz789');
  });
});

describe('applyExecutionDeepLink', () => {
  const originalHash = window.location.hash;
  const originalPathname = window.location.pathname;
  const showToastMock = showToast as jest.MockedFunction<typeof showToast>;
  let scrollIntoViewMock: jest.Mock;
  let setTimeoutSpy: jest.SpyInstance;

  beforeEach(() => {
    document.body.innerHTML = '';
    showToastMock.mockClear();

    // jsdom doesn't implement scrollIntoView — patch it on the
    // HTMLElement prototype so the production code's call site
    // doesn't throw, and the test can assert it ran.
    scrollIntoViewMock = jest.fn();
    Element.prototype.scrollIntoView = scrollIntoViewMock as unknown as typeof Element.prototype.scrollIntoView;

    // Stub setTimeout so we can verify the fade-out behaviour
    // without leaving a real timer behind that bleeds across tests.
    setTimeoutSpy = jest.spyOn(window, 'setTimeout');
  });

  afterEach(() => {
    window.location.hash = originalHash;
    window.history.replaceState({}, '', originalPathname);
    setTimeoutSpy.mockRestore();
  });

  test("returns false and skips work when no execution id in hash", () => {
    window.location.hash = '';
    expect(applyExecutionDeepLink()).toBe(false);
    expect(showToastMock).not.toHaveBeenCalled();
    expect(scrollIntoViewMock).not.toHaveBeenCalled();
  });

  test("scrolls + highlights + schedules a fade when the row exists", () => {
    document.body.innerHTML = `
      <table>
        <tbody>
          <tr data-execution-id="exec-abc-123" id="target-row"><td>row</td></tr>
          <tr data-execution-id="exec-other"><td>other</td></tr>
        </tbody>
      </table>
    `;
    window.location.hash = '#history?execution=exec-abc-123';

    expect(applyExecutionDeepLink()).toBe(true);

    const row = document.getElementById('target-row') as HTMLTableRowElement;
    expect(row.classList.contains('history-row-highlight')).toBe(true);
    expect(scrollIntoViewMock).toHaveBeenCalledTimes(1);
    expect(scrollIntoViewMock).toHaveBeenCalledWith({ behavior: 'smooth', block: 'center' });
    // A fade-out timer is scheduled so the highlight class is removed
    // and the row visually settles back to baseline styling.
    expect(setTimeoutSpy).toHaveBeenCalledWith(expect.any(Function), 4000);
    // No fallback toast on success.
    expect(showToastMock).not.toHaveBeenCalled();
  });

  test("falls back to an info toast and clears the hash when the row is missing", () => {
    document.body.innerHTML = `
      <table><tbody>
        <tr data-execution-id="some-other-execution"><td>nope</td></tr>
      </tbody></table>
    `;
    window.history.replaceState({}, '', '/purchases');
    window.location.hash = '#history?execution=missing-exec-id-12345678';

    expect(applyExecutionDeepLink()).toBe(false);

    expect(scrollIntoViewMock).not.toHaveBeenCalled();
    expect(showToastMock).toHaveBeenCalledTimes(1);
    const toastArg = showToastMock.mock.calls[0]?.[0] as { message: string; kind: string };
    // Toast shows a short prefix of the execution id so the user can
    // cross-reference against the email without a wall-of-UUID toast.
    expect(toastArg.message).toMatch(/missing-/);
    expect(toastArg.kind).toBe('info');
    // The execution query param is stripped from the hash so a
    // subsequent re-render doesn't re-fire the same toast.
    expect(window.location.hash).not.toMatch(/execution=/);
  });

  test("CSS-escapes execution IDs containing selector metacharacters", () => {
    // Defence in depth: an attacker can't craft an execution-id link
    // that escapes the attribute selector — CSS.escape neutralises
    // the closing quote / brackets. The row must still match by its
    // literal id and the highlight class must apply.
    const nasty = 'abc"]/script>';
    const row = document.createElement('tr');
    row.setAttribute('data-execution-id', nasty);
    row.id = 'nasty-row';
    const tbody = document.createElement('tbody');
    tbody.appendChild(row);
    const table = document.createElement('table');
    table.appendChild(tbody);
    document.body.appendChild(table);

    window.location.hash = '#history?execution=' + encodeURIComponent(nasty);

    expect(applyExecutionDeepLink()).toBe(true);
    expect(row.classList.contains('history-row-highlight')).toBe(true);
  });
});
