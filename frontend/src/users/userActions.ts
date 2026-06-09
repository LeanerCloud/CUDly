/**
 * User action functionality (CRUD operations, bulk operations)
 */

import * as api from '../api';
import { getCurrentUser } from '../state';
import { isAdmin, isPurchaser, PURCHASER_GROUP_ID } from '../permissions';
import {
  allUsers,
  filteredUsers,
  selectedUserIds,
  setAllUsers,
  setAvailableGroups,
  clearSelectedUserIds
} from './state';
import { showError, showSuccess } from './utils';
import { confirmDialog } from '../confirmDialog';
import { applyFilters } from './filters';
import { renderUsers, renderUserStats, populateBulkGroupSelect } from './userList';
import { renderGroups } from '../groups/groupList';
import { renderPermissionMatrix } from './permissionMatrix';

/**
 * Defence-in-depth normaliser for the API contract. The backend's TS-typed
 * APIUser declares `groups: string[]` as required, but historical bugs
 * (and unforeseen future ones) can omit the field or send `null`. Renderers
 * iterate `user.groups`, so a missing field crashes the page with a
 * generic "Failed to load users and groups" toast that obscures the real
 * cause. See issue #350 — the backend fix lives there too; this guard is
 * the second line of defence so a future contract regression degrades
 * gracefully instead of throwing a TypeError in an Array.map.
 */
function normalizeUser(user: api.APIUser): api.APIUser {
  return {
    ...user,
    groups: Array.isArray(user.groups) ? user.groups : [],
  };
}

function normalizeGroup(group: api.APIGroup): api.APIGroup {
  return {
    ...group,
    permissions: Array.isArray(group.permissions) ? group.permissions : [],
    allowed_accounts: Array.isArray(group.allowed_accounts) ? group.allowed_accounts : [],
  };
}

/**
 * Ensure the currently-logged-in user is visible in the list.
 * If the API didn't include them (e.g. self-management not wired up yet),
 * prepend a synthetic entry flagged with id='current' so the userList "You"
 * badge still renders.
 */
function withCurrentUser(users: api.APIUser[]): api.APIUser[] {
  const current = getCurrentUser();
  if (!current) return users;
  if (users.some(u => u.email === current.email)) return users;
  const synthetic: api.APIUser = {
    id: 'current',
    email: current.email,
    groups: Array.isArray(current.groups) ? current.groups : [],
    mfa_enabled: false,
  };
  return [synthetic, ...users];
}

/**
 * Load and display users and groups
 */
export async function loadUsers(): Promise<void> {
  try {
    const [usersResponse, groupsResponse] = await Promise.all([
      api.listUsers(),
      api.listGroups()
    ]);

    // Normalise at the boundary so downstream renderers can trust
    // user.groups / group.permissions / group.allowed_accounts to be
    // real arrays (see normalizeUser/normalizeGroup above for the
    // rationale — issue #350 backend + frontend defence-in-depth).
    const users = (usersResponse.users ?? []).map(normalizeUser);
    const groups = (groupsResponse.groups ?? []).map(normalizeGroup);

    setAvailableGroups(groups);
    setAllUsers(withCurrentUser(users));

    // Apply current filters
    applyFilters();

    renderUsers(filteredUsers);
    renderGroups(groups);
    renderUserStats();
    populateBulkGroupSelect();

    const matrixContainer = document.getElementById('permission-matrix');
    if (matrixContainer) {
      renderPermissionMatrix(groups, matrixContainer);
    }

    // Issue #923: first-run prompt for admins not in the Purchaser group.
    // Show once per browser (stored in localStorage). The Purchaser group
    // must exist (migration 000058) before the prompt is relevant, so we
    // check that availableGroups contains it before surfacing the dialog.
    const PROMPT_KEY = 'cudly:purchaser-prompt-dismissed';
    const purchaserGroupExists = groups.some(g => g.id === PURCHASER_GROUP_ID);
    if (
      purchaserGroupExists &&
      isAdmin() &&
      !isPurchaser() &&
      !localStorage.getItem(PROMPT_KEY)
    ) {
      localStorage.setItem(PROMPT_KEY, '1');
      // Use the existing confirmDialog as a non-destructive notification.
      void confirmDialog({
        title: 'Purchaser group: separation of duties',
        body:
          'Recommended: add yourself to the Purchaser group only if no separate ' +
          'finance team will execute purchases. Otherwise leave it to dedicated ' +
          'Purchaser user(s). You can manage membership in the Groups panel below.',
        confirmLabel: 'Got it',
        destructive: false,
      });
    }
  } catch (error) {
    console.error('Failed to load users/groups:', error);
    showError('Failed to load users and groups');
  }
}

/**
 * Delete user
 */
export async function deleteUser(userId: string): Promise<void> {
  const user = allUsers.find(u => u.id === userId);
  if (!user) return;

  const ok = await confirmDialog({
    title: `Delete user "${user.email}"?`,
    body: 'This removes the user from the system along with all of their group memberships. This action cannot be undone.',
    confirmLabel: 'Delete user',
    destructive: true,
  });
  if (!ok) return;

  try {
    await api.deleteUser(userId);
    await loadUsers();
    showSuccess('User deleted successfully');
  } catch (error) {
    console.error('Failed to delete user:', error);
    showError('Failed to delete user');
  }
}

/**
 * Bulk delete users
 */
export async function bulkDeleteUsers(): Promise<void> {
  if (selectedUserIds.size === 0) return;

  const count = selectedUserIds.size;
  const ok = await confirmDialog({
    title: `Delete ${count} user${count === 1 ? '' : 's'}?`,
    body: 'This removes the selected users from the system along with all of their group memberships. This action cannot be undone.',
    confirmLabel: `Delete ${count} user${count === 1 ? '' : 's'}`,
    destructive: true,
  });
  if (!ok) return;

  try {
    // Delete users in parallel
    await Promise.all(
      Array.from(selectedUserIds).map(userId => api.deleteUser(userId))
    );

    clearSelectedUserIds();
    await loadUsers();
    showSuccess(`Successfully deleted ${count} user(s)`);
  } catch (error) {
    console.error('Failed to delete users:', error);
    showError('Failed to delete some users');
  }
}

// bulkChangeRole removed in PR #912 frontend: role no longer exists.
// Use the group multi-select in the Edit User modal to manage access.

/**
 * Bulk add to group
 */
export async function bulkAddToGroup(groupId: string): Promise<void> {
  if (selectedUserIds.size === 0) return;

  const { availableGroups } = await import('./state');
  const count = selectedUserIds.size;
  const group = availableGroups.find(g => g.id === groupId);
  if (!group) return;

  if (!confirm(`Add ${count} user(s) to group "${group.name}"?`)) {
    return;
  }

  try {
    // Get current user data and add group
    const updates = Array.from(selectedUserIds).map(async userId => {
      const user = allUsers.find(u => u.id === userId);
      if (!user) return;

      const updatedGroups = [...new Set([...user.groups, groupId])];
      await api.updateUser(userId, { groups: updatedGroups });
    });

    await Promise.all(updates);

    clearSelectedUserIds();
    await loadUsers();
    showSuccess(`Successfully added ${count} user(s) to group`);
  } catch (error) {
    console.error('Failed to add users to group:', error);
    showError('Failed to add some users to group');
  }
}

// Re-export for use in userModals
export { openEditUserModal } from './userModals';
