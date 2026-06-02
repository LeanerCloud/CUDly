/**
 * Runtime-shape-validation tests for getUserPermissions() (CR #922 F3).
 *
 * canAccess() iterates user.effectivePermissions with
 * `for (const p of user.effectivePermissions)` and reads p.action /
 * p.resource. A non-array `permissions` (or null/non-object entries,
 * or non-string action/resource) would throw outside app.ts's
 * fetch/merge try/catch and crash the bootstrap path. The validator
 * must reject every such shape so the caller falls back to the safe
 * group-membership gating instead.
 */
import { getUserPermissions } from '../api/auth';

beforeEach(() => {
  global.fetch = jest.fn();
  localStorage.setItem('auth_token', 'tok');
});
afterEach(() => {
  jest.restoreAllMocks();
  localStorage.clear();
});

function mockFetchOk(body: unknown): void {
  (global.fetch as jest.Mock).mockResolvedValue({
    ok: true,
    status: 200,
    json: async () => body,
  });
}

describe('getUserPermissions()', () => {
  test('returns parsed UserPermissionsResponse on a well-formed payload', async () => {
    mockFetchOk({
      permissions: [
        { action: 'view', resource: 'recommendations' },
        { action: 'view', resource: 'plans' },
      ],
      is_admin: false,
    });
    const res = await getUserPermissions();
    expect(res.is_admin).toBe(false);
    expect(res.permissions).toEqual([
      { action: 'view', resource: 'recommendations' },
      { action: 'view', resource: 'plans' },
    ]);
  });

  test('returns is_admin=true with {admin,*} entry on admin payload', async () => {
    mockFetchOk({
      permissions: [{ action: 'admin', resource: '*' }],
      is_admin: true,
    });
    const res = await getUserPermissions();
    expect(res.is_admin).toBe(true);
    expect(res.permissions).toEqual([{ action: 'admin', resource: '*' }]);
  });

  test('rejects when response is not an object (e.g. string)', async () => {
    mockFetchOk('not-an-object');
    await expect(getUserPermissions()).rejects.toThrow(/was not an object/);
  });

  test('rejects when response is null', async () => {
    mockFetchOk(null);
    await expect(getUserPermissions()).rejects.toThrow(/was not an object/);
  });

  test('rejects when permissions is not an array (e.g. truthy non-array)', async () => {
    mockFetchOk({ permissions: { 0: { action: 'view', resource: 'plans' } }, is_admin: false });
    await expect(getUserPermissions()).rejects.toThrow(/permissions is not an array/);
  });

  test('rejects when a permission entry is null', async () => {
    mockFetchOk({ permissions: [null], is_admin: false });
    await expect(getUserPermissions()).rejects.toThrow(/permissions\[0\] is not an object/);
  });

  test('rejects when a permission entry is missing action', async () => {
    mockFetchOk({ permissions: [{ resource: 'plans' }], is_admin: false });
    await expect(getUserPermissions()).rejects.toThrow(/permissions\[0\]\.action is not a string/);
  });

  test('rejects when a permission entry has non-string resource', async () => {
    mockFetchOk({ permissions: [{ action: 'view', resource: 42 }], is_admin: false });
    await expect(getUserPermissions()).rejects.toThrow(/permissions\[0\]\.resource is not a string/);
  });

  test('rejects when is_admin is missing', async () => {
    mockFetchOk({ permissions: [] });
    await expect(getUserPermissions()).rejects.toThrow(/is_admin is not a boolean/);
  });

  test('rejects when is_admin is not boolean (e.g. "true" string)', async () => {
    mockFetchOk({ permissions: [], is_admin: 'true' });
    await expect(getUserPermissions()).rejects.toThrow(/is_admin is not a boolean/);
  });
});
