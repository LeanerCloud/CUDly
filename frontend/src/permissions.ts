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
  | '*';

/**
 * Well-known group UUID for the Administrators group seeded by
 * migration 000057. Being a member of this group is the frontend
 * equivalent of the backend's HasPermissionAPI(admin, *) == true.
 */
export const ADMINISTRATORS_GROUP_ID = '00000000-0000-5000-8000-000000000001';

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
 * Returns true when the current session's effective permissions grant
 * the specified action on the specified resource.
 *
 * When effectivePermissions is populated (fetched from
 * GET /api/auth/me/permissions on login/bootstrap) the set is
 * consulted directly: admin:* grants everything; otherwise an exact
 * action:resource match is required.
 *
 * While effectivePermissions is not yet loaded (e.g. during the first
 * render before the async fetch completes) the function falls back to
 * the group-membership admin check so Administrators-group members
 * aren't locked out during bootstrap. Non-admins see buttons hidden
 * briefly -- acceptable because the full set loads immediately after
 * login.
 *
 * UX-only gate. The backend still enforces on every request.
 */
export function canAccess(action: Action, resource: Resource): boolean {
  const user = state.getCurrentUser();
  if (!user) return false;

  // Use the server-provided effective permission set when available.
  if (user.effectivePermissions) {
    for (const p of user.effectivePermissions) {
      if (p.action === 'admin' && p.resource === '*') return true;
      if (p.action === action && (p.resource === resource || p.resource === '*')) return true;
    }
    return false;
  }

  // Fallback while permissions are still loading: admins pass, others block.
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
