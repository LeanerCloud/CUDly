/**
 * Tests for the Purchase History deep-link parser — the suppression
 * badge on the Recommendations view links to
 * #history?execution=<id>, and the handler scrolls + highlights the
 * matching row.
 */
import { readDeepLinkExecutionID } from '../history';

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
