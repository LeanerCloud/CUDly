/**
 * Permissions helper tests.
 *
 * PR #912 replaced role-based gating with group-membership-based
 * gating. isAdmin() now returns true when the current user is a member
 * of the Administrators group (UUID 00000000-0000-5000-8000-000000000001).
 * canAccess() is currently a pass-through to isAdmin() -- non-admin users
 * always get false because the /me/permissions endpoint is deferred.
 *
 * getRolePermissions() is kept for the effective-permissions display in
 * the admin Users page and still returns the same sets as before.
 */
import { canAccess, getRolePermissions, isAdmin, ADMINISTRATORS_GROUP_ID } from '../permissions';

jest.mock('../state', () => ({
  getCurrentUser: jest.fn(),
}));

import * as state from '../state';

const ADMIN_GID = ADMINISTRATORS_GROUP_ID;
const STD_GID = '00000000-0000-5000-8000-000000000005';
const RO_GID = '00000000-0000-5000-8000-000000000006';

const mockUserWithGroups = (groups: string[]) => {
  (state.getCurrentUser as jest.Mock).mockReturnValue(
    { id: 'u1', email: 'u@example.com', groups },
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

  describe('canAccess', () => {
    test('Administrators group member passes all checks', () => {
      mockUserWithGroups([ADMIN_GID]);
      expect(canAccess('admin', '*')).toBe(true);
      expect(canAccess('view', 'users')).toBe(true);
      expect(canAccess('delete', 'plans')).toBe(true);
      expect(canAccess('execute', 'purchases')).toBe(true);
      expect(canAccess('view', 'accounts')).toBe(true);
    });

    test('Standard Users group member fails all checks (no /me/permissions endpoint yet)', () => {
      mockUserWithGroups([STD_GID]);
      expect(canAccess('view', 'recommendations')).toBe(false);
      expect(canAccess('view', 'plans')).toBe(false);
      expect(canAccess('admin', '*')).toBe(false);
    });

    test('Read-Only Users group member fails all checks', () => {
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
});
