/**
 * Deep-link handler for /purchases/approve/:id and /purchases/cancel/:id.
 *
 * The approval email's action links target these SPA paths (not the raw
 * API endpoints) so the click lands on an authenticated session that can
 * attribute the action to the logged-in user. Flow:
 *
 *   1. init() sees the URL, calls handlePurchaseDeeplink().
 *   2. If the user isn't authenticated, init()'s showLoginModal() runs
 *      first; login's location.reload() re-enters init() with the same
 *      URL, and handlePurchaseDeeplink() then runs post-auth.
 *   3. User confirms the action via confirmDialog.
 *   4. We POST to /api/purchases/{approve,cancel}/:id?token=… with the
 *      session cookie attached; the backend records session.Email as
 *      approved_by / cancelled_by (see handler_purchases.go:
 *      tryResolveActorEmail).
 *   5. On result, we replace the URL with /history so the user isn't
 *      stuck on a stale deep-link if they navigate back.
 */

import { apiRequest } from './api/client';
import { showToast } from './toast';
import { confirmDialog } from './confirmDialog';

type DeeplinkAction = 'approve' | 'cancel';

interface ParsedDeeplink {
  action: DeeplinkAction;
  id: string;
  token: string;
}

/**
 * Parse the current URL as a purchase action deep-link. Returns null when
 * the path isn't `/purchases/{approve,cancel}/:id`.
 */
export function parsePurchaseDeeplink(pathname: string, search: string): ParsedDeeplink | null {
  const parts = pathname.split('/').filter(Boolean);
  if (parts.length !== 3 || parts[0] !== 'purchases') return null;
  const action = parts[1];
  const id = parts[2];
  if ((action !== 'approve' && action !== 'cancel') || !id) return null;
  const token = new URLSearchParams(search).get('token') || '';
  return { action, id: id, token };
}

/**
 * Handle the deep-link if the current URL is one. Returns true when the
 * caller should STOP its normal tab-routing flow (a deep-link was
 * detected); false when there's nothing to do and the caller should fall
 * through to its usual tab handling.
 *
 * In the no-token and user-cancelled paths we replace the URL with
 * /history so the user doesn't bounce back into the same deep-link on a
 * browser back navigation.
 */
export async function handlePurchaseDeeplink(): Promise<boolean> {
  const dl = parsePurchaseDeeplink(window.location.pathname, window.location.search);
  if (!dl) return false;

  if (!dl.token) {
    showToast({
      message: 'Missing approval token in link — open it from the original email instead of a shared copy.',
      kind: 'error',
      timeout: null,
    });
    window.history.replaceState({}, '', '/history');
    return true;
  }

  // Only one action button — "Approve purchase" / "Cancel purchase".
  // Dismissal is via the close-X, ESC, or a backdrop click, so a
  // cancel-purchase dialog doesn't end up with two buttons both labelled
  // "Cancel" (one to dismiss, one to confirm the purchase cancellation).
  const titleVerb = dl.action === 'approve' ? 'Approve' : 'Cancel';
  const gerund = dl.action === 'approve' ? 'approve' : 'cancel';
  const confirmLabel = dl.action === 'approve' ? 'Approve purchase' : 'Cancel purchase';
  const ok = await confirmDialog({
    title: `${titleVerb} purchase ${dl.id.slice(0, 8)}…?`,
    body: `You're about to ${gerund} purchase execution ${dl.id}. This action will be recorded against your logged-in account.`,
    confirmLabel,
    hideCancelButton: true,
    destructive: dl.action === 'cancel',
  });
  if (!ok) {
    window.history.replaceState({}, '', '/history');
    return true;
  }

  const endpoint = `/purchases/${dl.action}/${encodeURIComponent(dl.id)}?token=${encodeURIComponent(dl.token)}`;
  const actionPast = dl.action === 'approve' ? 'approved' : 'cancelled';
  try {
    await apiRequest<{ status: string }>(endpoint, { method: 'POST' });
    showToast({ message: `Purchase ${actionPast}.`, kind: 'success', timeout: 5_000 });
  } catch (err) {
    const msg = err instanceof Error ? err.message : String(err);
    showToast({
      message: `Failed to ${dl.action} purchase: ${msg}`,
      kind: 'error',
      timeout: null,
    });
  }
  window.history.replaceState({}, '', '/history');
  return true;
}
