/**
 * Permission helper for CUDly frontend.
 *
 * PR #912 removed the `role` column from users and sessions.
 * Authorization is now purely group-membership based. The server
 * derives every permission from the union of the groups a user
 * belongs to via HasPermissionAPI; the frontend mirrors that by
 * checking group membership for the UI-gating predicates below.
 *
 * Admin status = member of the Administrators group
 * (UUID 00000000-0000-5000-8000-000000000001). That group carries
 * the `admin:*` capability on the backend, which grants every action
 * on every resource. The three built-in groups seeded by migration
 * 000057 are:
 *
 *   Administrators   00000000-0000-5000-8000-000000000001  (admin:*)
 *   Standard Users   00000000-0000-5000-8000-000000000005
 *   Read-Only Users  00000000-0000-5000-8000-000000000006
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
 * They are not used for session gating anymore.
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
 * Returns true when the current session's group membership grants the
 * specified permission.
 *
 * Administrators-group members pass every check (admin:* covers every
 * action/resource pair). For all other groups a full /me/permissions
 * round-trip is needed to resolve fine-grained permissions; that
 * endpoint is not yet available, so non-admins return false here and
 * the backend remains the authoritative gate.
 *
 * UX-only gate. A wrong-positive surfaces as a 403 on click; a
 * wrong-negative just hides a button.
 */
export function canAccess(action: Action, resource: Resource): boolean {
  // Suppress unused-variable warning -- action/resource are kept in
  // the signature for forward-compatibility with the /me/permissions
  // endpoint that will replace this stub.
  void action; void resource;
  const user = state.getCurrentUser();
  if (!user) return false;
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
