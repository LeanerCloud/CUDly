/**
 * Group action functionality
 */

import * as api from '../api';
import { availableGroups } from '../users/state';
import { showError, showSuccess } from '../users/utils';
import { loadUsers } from '../users/userActions';

/**
 * Delete group
 */
export async function deleteGroup(groupId: string): Promise<void> {
  const group = availableGroups.find(g => g.id === groupId);
  if (!group) return;

  if (!confirm(`Are you sure you want to delete group "${group.name}"?`)) {
    return;
  }

  try {
    await api.deleteGroup(groupId);
    await loadUsers();
    showSuccess('Group deleted successfully');
  } catch (error) {
    console.error('Failed to delete group:', error);
    showError('Failed to delete group');
  }
}

// Re-export openEditGroupModal from groupModals
export { openEditGroupModal } from './groupModals';
