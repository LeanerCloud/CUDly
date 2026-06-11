/**
 * Permission helper for CUDly frontend.
 *
 * Issue #917: authorization is group-membership based. The server
 * exposes the effective permission set (union of all user groups) via
 * GET /api/auth/me/permissions. The frontend fetches this on
 * login/bootstrap and caches it on the current-user state so
 * canAccess() can consult the real set rather than blocking all
 * non-admins.
 *
 * Admin status = member of the Administrators group
 * (UUID 00000000-0000-5000-8000-000000000001). That group carries
 * the `admin:*` capability on the backend, which grants every action
 * on every resource.
 *
 * The closed-union Action and Resource types below are hand-written
 * and mirror the Action* / Resource* constants in
 * internal/auth/types.go.
 *
 * The frontend is a UX-only gate. The backend still enforces
 * permissions on every request; a wrong-positive here surfaces as a
 * 403 on click, and a wrong-negative just hides a button.
 *
 * The ADMIN_PERMS / USER_PERMS / READONLY_PERMS sets from
 * permissions.generated.ts remain exported for the effective-
 * permissions display in the admin Users page expand panel.
 */

import * as state from './state';
import { ADMIN_PERMS, USER_PERMS, READONLY_PERMS } from './permissions.generated';

// Re-export so the user expand panel can still display them.
export { ADMIN_PERMS, USER_PERMS, READONLY_PERMS };

// Action verbs. Closed enum so typos at call sites become compile
// errors. Mirrors the constants in internal/auth/types.go.
export type Action =
  | 'view'
  | 'create'
  | 'update'
  | 'delete'
  | 'execute'
  | 'approve'
  | 'cancel-own'
  | 'cancel-any'
  | 'retry-own'
  | 'retry-any'
  | 'approve-own'
  | 'approve-any'
  | 'execute-own'
  | 'execute-any'
  // update-any:purchases lets a holder manage (pause/resume/run/delete)
  // ANY user's scheduled purchase, bypassing the creator-scope ownership
  // check (issue #950). Mirrors cancel-any/approve-any on History rows.
  | 'update-any'
  | 'revoke-own'
  | 'revoke-any'
  // sell-own / sell-any gate the "Sell on Marketplace" button (issue #292).
  // Mirrors the backend ActionSellOwn / ActionSellAny constants in
  // internal/auth/types.go. sell-own:purchases is granted to every
  // authenticated user via DefaultUserPermissions; sell-any has no default
  // non-admin grant.
  | 'sell-own'
  | 'sell-any'
  | 'admin';

// Resource names. Closed enum for the same reason.
export type Resource =
  | 'recommendations'
  | 'plans'
  | 'purchases'
  | 'history'
  | 'config'
  | 'accounts'
  | 'users'
  | 'groups'
  | 'api-keys'
  // ri-exchange is a separate resource from purchases so that execute:ri-exchange
  // can be granted independently. RI exchanges are financially irreversible;
  // keeping the permission disjoint prevents execute:purchases from implicitly
  // covering the exchange path (issue #660).
  | 'ri-exchange'
  | '*';

/**
 * Well-known group UUID for the Administrators group seeded by
 * migration 000057. Being a member of this group is the frontend
 * equivalent of the backend's HasPermissionAPI(admin, *) == true.
 */
export const ADMINISTRATORS_GROUP_ID = '00000000-0000-5000-8000-000000000001';

/**
 * Well-known group UUID for the Purchaser group relocated by migration
 * 000064 (issue #942; originally seeded for issue #923). The three
 * money-spending verbs (execute:purchases, approve-any:purchases,
 * retry-any:purchases) are carved out of the admin:* wildcard and
 * require explicit membership in this group (or a custom group that
 * grants the same verbs).
 */
export const PURCHASER_GROUP_ID = '00000000-0000-5000-8000-000000000007';

/**
 * The set of (action, resource) pairs carved out of the admin:*
 * wildcard. Mirrors adminCarvedOuts in internal/auth/types.go.
 * Admin-group members must also be in the Purchaser group to pass
 * these checks.
 */
const ADMIN_CARVED_OUTS: ReadonlySet<string> = new Set([
  'execute:purchases',
  'approve-any:purchases',
  'retry-any:purchases',
]);

/**
 * Return true when the current session user is a member of the
 * Administrators group. This replaces the former `user.role === "admin"`
 * check that PR #912 removed from both the backend and the API response.
 *
 * A null user (logged out, pre-init race) returns false.
 */
export function isAdmin(): boolean {
  const user = state.getCurrentUser();
  if (!user) return false;
  return Array.isArray(user.groups) && user.groups.includes(ADMINISTRATORS_GROUP_ID);
}

/**
 * Return true when the current session is authorised to execute the
 * three carved-out money-spending verbs (execute:purchases,
 * approve-any:purchases, retry-any:purchases). When the backend has
 * delivered effectivePermissions (post-bootstrap) we drive off the
 * permission set itself so a user who holds any of those verbs via a
 * custom group (not just the seeded Purchaser group) also returns
 * true. While effectivePermissions is still loading we fall back to
 * seeded-group membership so the helper agrees with the canAccess()
 * carve-out fallback in the same window.
 *
 * Callers that need a hard verb-specific gate should prefer
 * canAccess('execute', 'purchases'). isPurchaser() is the
 * verb-agnostic "can spend money at all" predicate (true if ANY of
 * the three carved-out verbs is granted), which is what the
 * no-Purchaser banners use.
 */
export function isPurchaser(): boolean {
  const user = state.getCurrentUser();
  if (!user) return false;
  if (user.effectivePermissions) {
    // Match canAccess()'s semantics: a permission entry with
    // resource '*' satisfies the carved-out verb on 'purchases' the
    // same way the backend's HasPermission accepts ResourceAll. Walk
    // each carved-out key and accept either an exact match or a
    // wildcard-resource match on the same action.
    for (const key of ADMIN_CARVED_OUTS) {
      const colon = key.indexOf(':');
      if (colon < 0) continue;
      const action = key.slice(0, colon);
      const resource = key.slice(colon + 1);
      for (const p of user.effectivePermissions) {
        if (p.action === action && (p.resource === resource || p.resource === '*')) {
          return true;
        }
      }
    }
    return false;
  }
  return Array.isArray(user.groups) && user.groups.includes(PURCHASER_GROUP_ID);
}

/**
 * Returns true when the current session's effective permissions grant
 * the specified action on the specified resource.
 *
 * When effectivePermissions is populated (fetched from
 * GET /api/auth/me/permissions on login/bootstrap) the set is
 * consulted directly: admin:* grants everything EXCEPT the three
 * money-spending verbs carved out of admin:* by the backend
 * (issue #923) -- those require an explicit (action, resource) entry
 * in effectivePermissions (which the backend only returns when the
 * user is in the Purchaser group or a custom group that grants the
 * verb directly). For non-admin entries an exact action:resource
 * match (or matching action with resource '*') is required.
 *
 * While effectivePermissions is not yet loaded (e.g. during the first
 * render before the async fetch completes) the function falls back to
 * group-membership checks: Administrators-group members pass every
 * check EXCEPT the carved-out money-spending verbs, which require
 * Purchaser-group membership. This mirrors the backend's
 * HasPermission carve-out so UX and enforcement agree on the same
 * verbs whether or not effectivePermissions has loaded yet.
 *
 * UX-only gate. The backend still enforces on every request; a
 * wrong-positive surfaces as a 403 on click, a wrong-negative just
 * hides a button.
 */
export function canAccess(action: Action, resource: Resource): boolean {
  const user = state.getCurrentUser();
  if (!user) return false;

  const key = `${action}:${resource}`;
  const isCarvedOut = ADMIN_CARVED_OUTS.has(key);

  // Use the server-provided effective permission set when available.
  if (user.effectivePermissions) {
    for (const p of user.effectivePermissions) {
      // admin:* covers everything EXCEPT the carved-out verbs.
      if (p.action === 'admin' && p.resource === '*' && !isCarvedOut) {
        return true;
      }
      if (p.action === action && (p.resource === resource || p.resource === '*')) {
        return true;
      }
    }
    return false;
  }

  // Fallback while permissions are still loading. Mirror the backend's
  // carve-out: admin grants everything except the money-spending verbs,
  // which require explicit Purchaser-group membership.
  if (isCarvedOut) {
    return isPurchaser();
  }
  return isAdmin();
}

/**
 * Return the well-known permission set for a legacy role name. Kept
 * for the effective-permissions display on the admin Users page, which
 * shows what permissions the built-in role-mirror groups carry.
 * No longer used for session gating.
 */
export function getRolePermissions(role: string | undefined | null): ReadonlySet<string> {
  switch (role) {
    case 'admin':
      return ADMIN_PERMS;
    case 'user':
      return USER_PERMS;
    case 'readonly':
      return READONLY_PERMS;
    default:
      return new Set();
  }
}
