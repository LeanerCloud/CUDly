/**
 * Permission helper for CUDly frontend.
 *
 * Issue #365: drives UI gating so a non-admin session never sees a
 * button whose only outcome is a backend 403 + "admin access required"
 * toast. The backend remains the security boundary (handlers still call
 * `requirePermission`); this helper is a UX-only gate.
 *
 * The role-to-default-permissions map mirrors the backend constants in
 * internal/auth/types.go (DefaultAdminPermissions /
 * DefaultUserPermissions / DefaultReadOnlyPermissions). When the
 * backend lists drift, update this file too: the
 * permissions.test.ts unit tests are the canary that fails if the
 * mirror gets out of sync.
 *
 * Group-grant permissions are not folded in here. The current
 * `state.currentUser` carries only role; group memberships live in
 * `availableGroups` which is loaded only on the admin Users page (a
 * path readonly users can't reach). When a future enhancement adds a
 * `/me/permissions` round-trip, replace `getRolePermissions(role)`
 * with the server-provided permission set and keep the API the same.
 */

import * as state from './state';

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

// Default permission sets per role. Strings are `${action}:${resource}`.
// Kept in lockstep with internal/auth/types.go:367-415. The unit tests
// enumerate every entry so a drift fails CI.
const ADMIN_PERMS: ReadonlySet<string> = new Set(['admin:*']);

const USER_PERMS: ReadonlySet<string> = new Set([
  'view:recommendations',
  'view:plans',
  'view:purchases',
  'view:history',
  'create:plans',
  'update:plans',
  'cancel-own:purchases',
  'retry-own:purchases',
  'approve-own:purchases',
]);

const READONLY_PERMS: ReadonlySet<string> = new Set([
  'view:recommendations',
  'view:plans',
  'view:history',
]);

/**
 * Return the default permission set for the given role as a readonly
 * set of `${action}:${resource}` strings. Unknown roles return the
 * empty set (no permissions) so a typo in role assignment fails closed
 * rather than open.
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

/**
 * Returns true when the current session's role grants the
 * `${action}:${resource}` permission.
 *
 * Admin role short-circuits to true for every check (admin:* covers
 * every action/resource pair). A null user (logged out, or pre-init
 * race) returns false for everything.
 *
 * UX-only gate. The backend still enforces; a wrong-positive here
 * surfaces as a 403 on click, and a wrong-negative just hides a
 * button.
 */
export function canAccess(action: Action, resource: Resource): boolean {
  const user = state.getCurrentUser();
  if (!user) return false;
  const perms = getRolePermissions(user.role);
  if (perms.has('admin:*')) return true;
  return perms.has(`${action}:${resource}`);
}

/**
 * Convenience predicate for the legacy `.admin-only` toggle in
 * auth.ts:updateUserUI. Strictly equivalent to
 * `canAccess('admin', '*')` but spelled for readability at call sites
 * that pre-date the `canAccess` helper.
 */
export function isAdmin(): boolean {
  return canAccess('admin', '*');
}
