/**
 * Permissions helper tests.
 *
 * The role-default sets MUST match the backend constants in
 * `internal/auth/types.go` (DefaultAdminPermissions /
 * DefaultUserPermissions / DefaultReadOnlyPermissions). These tests
 * enumerate every entry so a drift between this mirror and the
 * backend fails fast in CI rather than at runtime as a wrong-positive
 * (button shown, click 403s) or wrong-negative (functionality hidden
 * from a user who should have it).
 */
import { canAccess, getRolePermissions, isAdmin } from '../permissions';

jest.mock('../state', () => ({
  getCurrentUser: jest.fn(),
}));

import * as state from '../state';

const mockUser = (role: string | null) => {
  (state.getCurrentUser as jest.Mock).mockReturnValue(
    role === null ? null : { id: 'u1', email: 'u@example.com', role },
  );
};

describe('permissions', () => {
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

  describe('canAccess', () => {
    test('admin sees everything (admin:* superuser short-circuit)', () => {
      mockUser('admin');
      expect(canAccess('admin', '*')).toBe(true);
      expect(canAccess('view', 'users')).toBe(true);
      expect(canAccess('delete', 'plans')).toBe(true);
      expect(canAccess('execute', 'purchases')).toBe(true);
      expect(canAccess('view', 'accounts')).toBe(true);
    });

    test('user role: explicit grants', () => {
      mockUser('user');
      expect(canAccess('view', 'recommendations')).toBe(true);
      expect(canAccess('view', 'plans')).toBe(true);
      expect(canAccess('view', 'purchases')).toBe(true);
      expect(canAccess('view', 'history')).toBe(true);
      expect(canAccess('create', 'plans')).toBe(true);
      expect(canAccess('update', 'plans')).toBe(true);
      expect(canAccess('cancel-own', 'purchases')).toBe(true);
      expect(canAccess('retry-own', 'purchases')).toBe(true);
      expect(canAccess('approve-own', 'purchases')).toBe(true);
    });

    test('user role: denied for admin-gated actions', () => {
      mockUser('user');
      expect(canAccess('delete', 'plans')).toBe(false);
      expect(canAccess('execute', 'purchases')).toBe(false);
      expect(canAccess('admin', '*')).toBe(false);
      expect(canAccess('view', 'users')).toBe(false);
      expect(canAccess('view', 'accounts')).toBe(false);
      expect(canAccess('view', 'groups')).toBe(false);
      expect(canAccess('view', 'api-keys')).toBe(false);
      expect(canAccess('view', 'config')).toBe(false);
    });

    test('readonly role: only view grants on rec/plans/history', () => {
      mockUser('readonly');
      expect(canAccess('view', 'recommendations')).toBe(true);
      expect(canAccess('view', 'plans')).toBe(true);
      expect(canAccess('view', 'history')).toBe(true);
    });

    test('readonly role: denied for purchases view + every mutation', () => {
      mockUser('readonly');
      expect(canAccess('view', 'purchases')).toBe(false);
      expect(canAccess('create', 'plans')).toBe(false);
      expect(canAccess('update', 'plans')).toBe(false);
      expect(canAccess('delete', 'plans')).toBe(false);
      expect(canAccess('execute', 'purchases')).toBe(false);
      expect(canAccess('cancel-own', 'purchases')).toBe(false);
      expect(canAccess('retry-own', 'purchases')).toBe(false);
      expect(canAccess('approve-own', 'purchases')).toBe(false);
      expect(canAccess('admin', '*')).toBe(false);
      expect(canAccess('view', 'users')).toBe(false);
      expect(canAccess('view', 'accounts')).toBe(false);
    });

    test('null user (logged out) → false for everything', () => {
      mockUser(null);
      expect(canAccess('view', 'recommendations')).toBe(false);
      expect(canAccess('admin', '*')).toBe(false);
      expect(canAccess('create', 'plans')).toBe(false);
    });

    test('unknown role → false for everything', () => {
      mockUser('operator');
      expect(canAccess('view', 'recommendations')).toBe(false);
      expect(canAccess('admin', '*')).toBe(false);
      expect(canAccess('execute', 'purchases')).toBe(false);
    });
  });

  describe('isAdmin', () => {
    test('admin role → true', () => {
      mockUser('admin');
      expect(isAdmin()).toBe(true);
    });

    test('user role → false', () => {
      mockUser('user');
      expect(isAdmin()).toBe(false);
    });

    test('readonly role → false', () => {
      mockUser('readonly');
      expect(isAdmin()).toBe(false);
    });

    test('null user → false', () => {
      mockUser(null);
      expect(isAdmin()).toBe(false);
    });
  });
});
