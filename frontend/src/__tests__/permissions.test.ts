/**
 * Permissions helper tests.
 *
 * Issue #917: canAccess() now consults user.effectivePermissions when
 * populated (fetched from GET /api/auth/me/permissions on bootstrap).
 * When effectivePermissions is absent (loading race) it falls back to
 * group-membership checks: admin passes everywhere EXCEPT the three
 * money-spending verbs carved out by issue #923, which require
 * explicit Purchaser-group membership.
 *
 * isAdmin() returns true when the current user is a member of the
 * Administrators group (UUID 00000000-0000-5000-8000-000000000001).
 *
 * getRolePermissions() is kept for the effective-permissions display in
 * the admin Users page and still returns the same sets as before.
 */
import { canAccess, getRolePermissions, isAdmin, isPurchaser, ADMINISTRATORS_GROUP_ID, PURCHASER_GROUP_ID } from '../permissions';
import type { PermissionEntry } from '../api/types';

jest.mock('../state', () => ({
  getCurrentUser: jest.fn(),
}));

import * as state from '../state';

const ADMIN_GID = ADMINISTRATORS_GROUP_ID;
// Standard Users group seeded by migration 000057. Must NOT collide with
// PURCHASER_GROUP_ID ('...005'); otherwise the fallback path (effectivePermissions
// absent) would treat the standard-user fixture as a Purchaser and let the
// carved-out money-spending verbs through (CR finding, PR #924).
const STD_GID = '00000000-0000-5000-8000-000000000002';
const RO_GID = '00000000-0000-5000-8000-000000000006';

const mockUserWithGroups = (groups: string[], effectivePermissions?: PermissionEntry[]) => {
  (state.getCurrentUser as jest.Mock).mockReturnValue(
    { id: 'u1', email: 'u@example.com', groups, effectivePermissions },
  );
};

const mockNoUser = () => {
  (state.getCurrentUser as jest.Mock).mockReturnValue(null);
};

describe('permissions', () => {
  describe('ADMINISTRATORS_GROUP_ID', () => {
    test('has the expected UUID', () => {
      expect(ADMINISTRATORS_GROUP_ID).toBe('00000000-0000-5000-8000-000000000001');
    });
  });

  describe('getRolePermissions', () => {
    test('admin role grants admin:*', () => {
      const perms = getRolePermissions('admin');
      expect(perms.has('admin:*')).toBe(true);
      expect(perms.size).toBe(1);
    });

    test('user role grants the standard non-admin verbs', () => {
      const perms = getRolePermissions('user');
      const expected = [
        'view:recommendations',
        'view:plans',
        'view:purchases',
        'view:history',
        'create:plans',
        'update:plans',
        'delete:plans',
        'update:purchases',
        'cancel-own:purchases',
        'retry-own:purchases',
        'approve-own:purchases',
        // Added by PR #804: revoke-own gates the History inline Revoke button
        // for completed Azure purchases within the free-cancel window.
        'revoke-own:purchases',
      ];
      expected.forEach((p) => expect(perms.has(p)).toBe(true));
      expect(perms.size).toBe(expected.length);
    });

    test('readonly role grants only view on recommendations/plans/history', () => {
      const perms = getRolePermissions('readonly');
      expect(perms.has('view:recommendations')).toBe(true);
      expect(perms.has('view:plans')).toBe(true);
      expect(perms.has('view:history')).toBe(true);
      expect(perms.has('view:purchases')).toBe(false);
      expect(perms.has('create:plans')).toBe(false);
      expect(perms.size).toBe(3);
    });

    test('unknown role grants no permissions', () => {
      expect(getRolePermissions('superadmin').size).toBe(0);
      expect(getRolePermissions('').size).toBe(0);
      expect(getRolePermissions(undefined).size).toBe(0);
      expect(getRolePermissions(null).size).toBe(0);
    });
  });

  describe('isAdmin', () => {
    test('Administrators group member is admin', () => {
      mockUserWithGroups([ADMIN_GID]);
      expect(isAdmin()).toBe(true);
    });

    test('Administrators group member alongside other groups is still admin', () => {
      mockUserWithGroups([STD_GID, ADMIN_GID, RO_GID]);
      expect(isAdmin()).toBe(true);
    });

    test('Standard Users group member is not admin', () => {
      mockUserWithGroups([STD_GID]);
      expect(isAdmin()).toBe(false);
    });

    test('Read-Only Users group member is not admin', () => {
      mockUserWithGroups([RO_GID]);
      expect(isAdmin()).toBe(false);
    });

    test('user with empty groups is not admin', () => {
      mockUserWithGroups([]);
      expect(isAdmin()).toBe(false);
    });

    test('null user (logged out) is not admin', () => {
      mockNoUser();
      expect(isAdmin()).toBe(false);
    });

    test('user with non-array groups field is not admin (defense-in-depth)', () => {
      (state.getCurrentUser as jest.Mock).mockReturnValue(
        { id: 'u1', email: 'u@example.com', groups: null },
      );
      expect(isAdmin()).toBe(false);
    });
  });

  describe('canAccess - fallback (effectivePermissions absent)', () => {
    test('Administrators group member passes non-spending checks via group-membership fallback', () => {
      mockUserWithGroups([ADMIN_GID]);
      expect(canAccess('admin', '*')).toBe(true);
      expect(canAccess('view', 'users')).toBe(true);
      expect(canAccess('delete', 'plans')).toBe(true);
      expect(canAccess('view', 'accounts')).toBe(true);
      // execute:ri-exchange is NOT carved out of admin:* (issue #660 split it
      // from execute:purchases), so an admin-only member still passes it.
      expect(canAccess('execute', 'ri-exchange')).toBe(true);
      // execute:purchases is carved out of admin:* and requires Purchaser membership.
      expect(canAccess('execute', 'purchases')).toBe(false);
      expect(canAccess('approve-any', 'purchases')).toBe(false);
      expect(canAccess('retry-any', 'purchases')).toBe(false);
    });

    test('Administrators + Purchaser group member passes all checks including spending', () => {
      mockUserWithGroups([ADMIN_GID, PURCHASER_GROUP_ID]);
      expect(canAccess('admin', '*')).toBe(true);
      expect(canAccess('view', 'users')).toBe(true);
      expect(canAccess('delete', 'plans')).toBe(true);
      expect(canAccess('execute', 'purchases')).toBe(true);
      expect(canAccess('approve-any', 'purchases')).toBe(true);
      expect(canAccess('retry-any', 'purchases')).toBe(true);
      expect(canAccess('view', 'accounts')).toBe(true);
    });

    test('Purchaser-only (no admin) passes carved-out verbs but not other admin actions', () => {
      mockUserWithGroups([PURCHASER_GROUP_ID]);
      // Purchaser group grants the three carved-out verbs.
      expect(canAccess('execute', 'purchases')).toBe(true);
      expect(canAccess('approve-any', 'purchases')).toBe(true);
      expect(canAccess('retry-any', 'purchases')).toBe(true);
      // But Purchaser membership alone is not admin -- non-spending admin
      // actions remain denied during the fallback path.
      expect(canAccess('admin', '*')).toBe(false);
      expect(canAccess('view', 'users')).toBe(false);
      expect(canAccess('delete', 'plans')).toBe(false);
    });

    test('Standard Users group member blocked during loading (effectivePermissions undefined)', () => {
      // Before /me/permissions returns, non-admins are blocked (fails closed).
      mockUserWithGroups([STD_GID]);
      expect(canAccess('view', 'recommendations')).toBe(false);
      expect(canAccess('view', 'plans')).toBe(false);
      expect(canAccess('admin', '*')).toBe(false);
    });

    test('Read-Only Users group member blocked during loading', () => {
      mockUserWithGroups([RO_GID]);
      expect(canAccess('view', 'recommendations')).toBe(false);
      expect(canAccess('admin', '*')).toBe(false);
    });

    test('empty-groups user fails all checks', () => {
      mockUserWithGroups([]);
      expect(canAccess('view', 'recommendations')).toBe(false);
      expect(canAccess('admin', '*')).toBe(false);
    });

    test('null user (logged out) fails all checks', () => {
      mockNoUser();
      expect(canAccess('view', 'recommendations')).toBe(false);
      expect(canAccess('admin', '*')).toBe(false);
      expect(canAccess('create', 'plans')).toBe(false);
    });
  });

  describe('canAccess - effective permissions (post /me/permissions fetch)', () => {
    test('Standard Users group member gets explicit grants from effectivePermissions', () => {
      const perms: PermissionEntry[] = [
        { action: 'view', resource: 'recommendations' },
        { action: 'view', resource: 'plans' },
        { action: 'view', resource: 'purchases' },
        { action: 'view', resource: 'history' },
        { action: 'create', resource: 'plans' },
        { action: 'cancel-own', resource: 'purchases' },
      ];
      mockUserWithGroups([STD_GID], perms);
      expect(canAccess('view', 'recommendations')).toBe(true);
      expect(canAccess('view', 'plans')).toBe(true);
      expect(canAccess('view', 'purchases')).toBe(true);
      expect(canAccess('view', 'history')).toBe(true);
      expect(canAccess('create', 'plans')).toBe(true);
      expect(canAccess('cancel-own', 'purchases')).toBe(true);
    });

    test('Standard Users group member is denied for permissions not in effective set', () => {
      const perms: PermissionEntry[] = [
        { action: 'view', resource: 'recommendations' },
      ];
      mockUserWithGroups([STD_GID], perms);
      expect(canAccess('delete', 'plans')).toBe(false);
      expect(canAccess('execute', 'purchases')).toBe(false);
      expect(canAccess('admin', '*')).toBe(false);
      expect(canAccess('view', 'users')).toBe(false);
    });

    test('execute:purchases in effective set does NOT imply execute:ri-exchange (issue #660 isolation)', () => {
      // RI exchanges are financially irreversible (no AWS rollback). The
      // permission split intentionally makes execute:ri-exchange disjoint
      // from execute:purchases so granting one does not transitively grant
      // the other. This guards the frontend gating mirror of the backend
      // gate exercised in router_660_permission_flips_test.go.
      const perms: PermissionEntry[] = [
        { action: 'execute', resource: 'purchases' },
      ];
      mockUserWithGroups([STD_GID], perms);
      expect(canAccess('execute', 'purchases')).toBe(true);
      expect(canAccess('execute', 'ri-exchange')).toBe(false);
    });

    test('execute:ri-exchange in effective set grants only ri-exchange, not purchases', () => {
      // Inverse of the previous test: holding execute:ri-exchange does NOT
      // imply execute:purchases either. The two permissions are disjoint.
      const perms: PermissionEntry[] = [
        { action: 'execute', resource: 'ri-exchange' },
      ];
      mockUserWithGroups([STD_GID], perms);
      expect(canAccess('execute', 'ri-exchange')).toBe(true);
      expect(canAccess('execute', 'purchases')).toBe(false);
    });

    test('admin wildcard in effectivePermissions grants everything except carved-out spending verbs', () => {
      // Mirror the backend (issue #923): admin:* covers every check EXCEPT
      // execute/approve-any/retry-any on purchases. Those require an
      // explicit (action, resource) entry from a non-admin group such
      // as Purchaser.
      const perms: PermissionEntry[] = [
        { action: 'admin', resource: '*' },
      ];
      mockUserWithGroups([ADMIN_GID], perms);
      expect(canAccess('admin', '*')).toBe(true);
      expect(canAccess('delete', 'plans')).toBe(true);
      expect(canAccess('view', 'users')).toBe(true);
      // Carved-out verbs deny even with admin:* in the effective set.
      expect(canAccess('execute', 'purchases')).toBe(false);
      expect(canAccess('approve-any', 'purchases')).toBe(false);
      expect(canAccess('retry-any', 'purchases')).toBe(false);
    });

    test('admin wildcard plus explicit Purchaser grants in effectivePermissions cover everything', () => {
      // What the backend returns for an Administrators + Purchaser user:
      // admin:* (from Administrators) PLUS the three explicit verbs (from
      // Purchaser). The explicit entries cover the carve-out.
      const perms: PermissionEntry[] = [
        { action: 'admin', resource: '*' },
        { action: 'execute', resource: 'purchases' },
        { action: 'approve-any', resource: 'purchases' },
        { action: 'retry-any', resource: 'purchases' },
      ];
      mockUserWithGroups([ADMIN_GID, PURCHASER_GROUP_ID], perms);
      expect(canAccess('admin', '*')).toBe(true);
      expect(canAccess('delete', 'plans')).toBe(true);
      expect(canAccess('view', 'users')).toBe(true);
      expect(canAccess('execute', 'purchases')).toBe(true);
      expect(canAccess('approve-any', 'purchases')).toBe(true);
      expect(canAccess('retry-any', 'purchases')).toBe(true);
    });

    test('Purchaser explicit grants in effectivePermissions allow spending without admin:*', () => {
      // A Purchaser-only user (no admin) gets just the seven verbs in
      // DefaultPurchaserPermissions. canAccess must allow the spending
      // verbs and the four view verbs, and deny everything else.
      const perms: PermissionEntry[] = [
        { action: 'execute', resource: 'purchases' },
        { action: 'approve-any', resource: 'purchases' },
        { action: 'retry-any', resource: 'purchases' },
        { action: 'view', resource: 'recommendations' },
        { action: 'view', resource: 'plans' },
        { action: 'view', resource: 'purchases' },
        { action: 'view', resource: 'history' },
      ];
      mockUserWithGroups([PURCHASER_GROUP_ID], perms);
      expect(canAccess('execute', 'purchases')).toBe(true);
      expect(canAccess('approve-any', 'purchases')).toBe(true);
      expect(canAccess('retry-any', 'purchases')).toBe(true);
      expect(canAccess('view', 'recommendations')).toBe(true);
      expect(canAccess('view', 'plans')).toBe(true);
      expect(canAccess('view', 'purchases')).toBe(true);
      expect(canAccess('view', 'history')).toBe(true);
      // Non-Purchaser admin actions remain denied.
      expect(canAccess('admin', '*')).toBe(false);
      expect(canAccess('delete', 'plans')).toBe(false);
      expect(canAccess('view', 'users')).toBe(false);
      expect(canAccess('cancel-any', 'purchases')).toBe(false);
    });

    test('empty effectivePermissions array denies everything', () => {
      mockUserWithGroups([STD_GID], []);
      expect(canAccess('view', 'recommendations')).toBe(false);
      expect(canAccess('admin', '*')).toBe(false);
    });

    test('multi-group user gets the union: all granted actions from both groups', () => {
      const perms: PermissionEntry[] = [
        { action: 'view', resource: 'recommendations' },
        { action: 'view', resource: 'plans' },
        { action: 'create', resource: 'plans' },
      ];
      mockUserWithGroups([STD_GID, RO_GID], perms);
      expect(canAccess('view', 'recommendations')).toBe(true);
      expect(canAccess('view', 'plans')).toBe(true);
      expect(canAccess('create', 'plans')).toBe(true);
      expect(canAccess('delete', 'plans')).toBe(false);
    });

    test('null user (logged out) fails regardless of any permissions', () => {
      mockNoUser();
      expect(canAccess('view', 'recommendations')).toBe(false);
      expect(canAccess('admin', '*')).toBe(false);
    });

    test('custom (non-seeded) group with carved-out grants in effectivePermissions allows spending', () => {
      // CR #924 F4 + F5 regression: a custom group that does not include
      // PURCHASER_GROUP_ID but DOES carry execute/approve-any/retry-any
      // on purchases must satisfy canAccess for those verbs. The seeded
      // Purchaser group is not the only legitimate source of the
      // carve-out; the backend already allows this shape via the
      // group's effectivePermissions, so the frontend must agree.
      const customGid = '00000000-0000-5000-8000-00000000abcd';
      const perms: PermissionEntry[] = [
        { action: 'execute', resource: 'purchases' },
        { action: 'approve-any', resource: 'purchases' },
        { action: 'retry-any', resource: 'purchases' },
        { action: 'view', resource: 'recommendations' },
        { action: 'view', resource: 'plans' },
        { action: 'view', resource: 'purchases' },
        { action: 'view', resource: 'history' },
      ];
      mockUserWithGroups([customGid], perms);
      // The three carved-out spending verbs are granted by the explicit
      // entries even without PURCHASER_GROUP_ID membership.
      expect(canAccess('execute', 'purchases')).toBe(true);
      expect(canAccess('approve-any', 'purchases')).toBe(true);
      expect(canAccess('retry-any', 'purchases')).toBe(true);
      expect(canAccess('view', 'recommendations')).toBe(true);
      expect(canAccess('view', 'history')).toBe(true);
      // Non-purchaser admin actions remain denied.
      expect(canAccess('admin', '*')).toBe(false);
      expect(canAccess('delete', 'plans')).toBe(false);
      expect(canAccess('view', 'users')).toBe(false);
      expect(canAccess('cancel-any', 'purchases')).toBe(false);
    });
  });

  describe('isPurchaser', () => {
    test('seeded Purchaser group member (no effectivePermissions yet) returns true via fallback', () => {
      // Pre-bootstrap loading window: effectivePermissions not yet
      // populated. The helper falls back to seeded group membership.
      mockUserWithGroups([PURCHASER_GROUP_ID]);
      expect(isPurchaser()).toBe(true);
    });

    test('user without Purchaser group and no effectivePermissions returns false', () => {
      mockUserWithGroups([ADMIN_GID]); // admin only
      expect(isPurchaser()).toBe(false);
    });

    test('explicit execute:purchases grant in effectivePermissions (custom group) returns true', () => {
      // CR #924 F4: isPurchaser() must reflect effective permissions,
      // not just seeded group ID. A user whose custom group grants
      // execute:purchases is a spender even without PURCHASER_GROUP_ID.
      const customGid = '00000000-0000-5000-8000-00000000beef';
      mockUserWithGroups([customGid], [
        { action: 'execute', resource: 'purchases' },
        { action: 'view', resource: 'recommendations' },
      ]);
      expect(isPurchaser()).toBe(true);
    });

    test('explicit approve-any:purchases grant returns true', () => {
      const customGid = '00000000-0000-5000-8000-00000000cafe';
      mockUserWithGroups([customGid], [
        { action: 'approve-any', resource: 'purchases' },
      ]);
      expect(isPurchaser()).toBe(true);
    });

    test('explicit retry-any:purchases grant returns true', () => {
      const customGid = '00000000-0000-5000-8000-00000000face';
      mockUserWithGroups([customGid], [
        { action: 'retry-any', resource: 'purchases' },
      ]);
      expect(isPurchaser()).toBe(true);
    });

    test('wildcard resource on a carved-out action grants Purchaser (matches canAccess semantics)', () => {
      // canAccess('execute', 'purchases') returns true for a
      // {execute, *} entry. isPurchaser() must agree so the two helpers
      // do not diverge when an unusual-but-legal permission shape
      // arrives from the backend.
      const customGid = '00000000-0000-5000-8000-00000000d00d';
      mockUserWithGroups([customGid], [
        { action: 'execute', resource: '*' },
      ]);
      expect(isPurchaser()).toBe(true);
    });

    test('admin:* in effectivePermissions WITHOUT explicit carved-out grants returns false', () => {
      // The backend carves the three spending verbs OUT of admin:*.
      // isPurchaser() must reflect that carve-out: a user whose
      // effectivePermissions are admin:* only (no explicit carve-out
      // entries from Purchaser membership) is NOT a spender. Note that
      // {admin, *} does NOT match {execute, *} / {approve-any, *} /
      // {retry-any, *} because the actions differ.
      mockUserWithGroups([ADMIN_GID], [
        { action: 'admin', resource: '*' },
      ]);
      expect(isPurchaser()).toBe(false);
    });

    test('empty effectivePermissions array returns false even with PURCHASER_GROUP_ID', () => {
      // Loading is complete (effectivePermissions is defined and
      // empty); group membership without backend confirmation is not
      // enough.
      mockUserWithGroups([PURCHASER_GROUP_ID], []);
      expect(isPurchaser()).toBe(false);
    });

    test('null user (logged out) returns false', () => {
      mockNoUser();
      expect(isPurchaser()).toBe(false);
    });
  });
});
