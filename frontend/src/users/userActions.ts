/**
 * User action functionality (CRUD operations, bulk operations)
 */

import * as api from '../api';
import { getCurrentUser } from '../state';
import {
  allUsers,
  filteredUsers,
  selectedUserIds,
  setAllUsers,
  setAvailableGroups,
  clearSelectedUserIds
} from './state';
import { showError, showSuccess } from './utils';
import { applyFilters } from './filters';
import { renderUsers, renderUserStats } from './userList';
import { renderGroups } from '../groups/groupList';
import { renderPermissionMatrix } from './permissionMatrix';

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
    role: current.role,
    groups: [],
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

    setAvailableGroups(groupsResponse.groups);
    setAllUsers(withCurrentUser(usersResponse.users));

    // Apply current filters
    applyFilters();

    renderUsers(filteredUsers);
    renderGroups(groupsResponse.groups);
    renderUserStats();

    const matrixContainer = document.getElementById('permission-matrix');
    if (matrixContainer) {
      renderPermissionMatrix(groupsResponse.groups, matrixContainer);
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

  if (!confirm(`Are you sure you want to delete user "${user.email}"?`)) {
    return;
  }

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
  if (!confirm(`Are you sure you want to delete ${count} user(s)? This action cannot be undone.`)) {
    return;
  }

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

/**
 * Bulk change role
 */
export async function bulkChangeRole(newRole: string): Promise<void> {
  if (selectedUserIds.size === 0) return;

  const count = selectedUserIds.size;
  if (!confirm(`Change role to "${newRole}" for ${count} user(s)?`)) {
    return;
  }

  try {
    // Update users in parallel
    await Promise.all(
      Array.from(selectedUserIds).map(userId =>
        api.updateUser(userId, { role: newRole })
      )
    );

    clearSelectedUserIds();
    await loadUsers();
    showSuccess(`Successfully updated ${count} user(s)`);
  } catch (error) {
    console.error('Failed to update users:', error);
    showError('Failed to update some users');
  }
}

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
