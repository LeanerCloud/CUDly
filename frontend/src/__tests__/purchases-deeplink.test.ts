/**
 * Deep-link parser tests. The handler itself is side-effectful (toast,
 * confirmDialog, fetch, location.replaceState) and exercised end-to-end
 * in manual smoke — here we pin the pure parser that decides whether a
 * given URL *is* a deep-link, which is the piece most likely to
 * regress when someone adds a new SPA route that overlaps with the
 * `/purchases/` prefix.
 */

import { parsePurchaseDeeplink } from '../purchases-deeplink';

describe('parsePurchaseDeeplink', () => {
  it('parses an approve deep-link with a token', () => {
    const dl = parsePurchaseDeeplink(
      '/purchases/approve/abc-123',
      '?token=tok-xyz',
    );
    expect(dl).toEqual({ action: 'approve', id: 'abc-123', token: 'tok-xyz' });
  });

  it('parses a cancel deep-link with a token', () => {
    const dl = parsePurchaseDeeplink(
      '/purchases/cancel/abc-123',
      '?token=tok-xyz',
    );
    expect(dl).toEqual({ action: 'cancel', id: 'abc-123', token: 'tok-xyz' });
  });

  it('parses a deep-link WITHOUT a token (handler surfaces the gap as a toast)', () => {
    const dl = parsePurchaseDeeplink('/purchases/approve/abc-123', '');
    expect(dl).toEqual({ action: 'approve', id: 'abc-123', token: '' });
  });

  it('rejects unknown action segments', () => {
    expect(parsePurchaseDeeplink('/purchases/hijack/abc', '?token=t')).toBeNull();
  });

  it('rejects paths with the wrong top-level segment', () => {
    expect(parsePurchaseDeeplink('/recommendations', '')).toBeNull();
    expect(parsePurchaseDeeplink('/history', '')).toBeNull();
    expect(parsePurchaseDeeplink('/', '')).toBeNull();
  });

  it('rejects paths missing the execution id', () => {
    expect(parsePurchaseDeeplink('/purchases/approve', '')).toBeNull();
    expect(parsePurchaseDeeplink('/purchases/approve/', '')).toBeNull();
  });

  it('rejects paths with extra segments (potential path-injection)', () => {
    expect(parsePurchaseDeeplink('/purchases/approve/abc/extra', '')).toBeNull();
  });

  it('tolerates trailing slashes on the id segment', () => {
    // split('/').filter(Boolean) collapses trailing /, so "/purchases/approve/abc/"
    // parses identically to the no-slash form.
    const dl = parsePurchaseDeeplink('/purchases/approve/abc/', '?token=t');
    expect(dl).toEqual({ action: 'approve', id: 'abc', token: 't' });
  });
});
