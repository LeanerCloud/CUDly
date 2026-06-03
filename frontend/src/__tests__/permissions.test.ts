/**
 * Permissions helper tests.
 *
 * Issue #917: canAccess() now consults user.effectivePermissions when
 * populated (fetched from GET /api/auth/me/permissions on bootstrap).
 * When effectivePermissions is absent (loading race) it falls back to
 * the group-membership admin check: admin passes, others block.
 *
 * isAdmin() returns true when the current user is a member of the
 * Administrators group (UUID 00000000-0000-5000-8000-000000000001).
 *
 * getRolePermissions() is kept for the effective-permissions display in
 * the admin Users page and still returns the same sets as before.
 */
import { canAccess, getRolePermissions, isAdmin, ADMINISTRATORS_GROUP_ID } from '../permissions';
import type { PermissionEntry } from '../api/types';

jest.mock('../state', () => ({
  getCurrentUser: jest.fn(),
}));

import * as state from '../state';

const ADMIN_GID = ADMINISTRATORS_GROUP_ID;
const STD_GID = '00000000-0000-5000-8000-000000000005';
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
    test('Administrators group member passes all checks via group-membership fallback', () => {
      mockUserWithGroups([ADMIN_GID]);
      expect(canAccess('admin', '*')).toBe(true);
      expect(canAccess('view', 'users')).toBe(true);
      expect(canAccess('delete', 'plans')).toBe(true);
      expect(canAccess('execute', 'purchases')).toBe(true);
      expect(canAccess('view', 'accounts')).toBe(true);
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

    test('admin wildcard in effectivePermissions grants everything', () => {
      const perms: PermissionEntry[] = [
        { action: 'admin', resource: '*' },
      ];
      mockUserWithGroups([ADMIN_GID], perms);
      expect(canAccess('admin', '*')).toBe(true);
      expect(canAccess('delete', 'plans')).toBe(true);
      expect(canAccess('view', 'users')).toBe(true);
      expect(canAccess('execute', 'purchases')).toBe(true);
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
  });
});
