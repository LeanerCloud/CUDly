/**
 * Group action functionality
 */

import * as api from '../api';
import { availableGroups } from '../users/state';
import { showError, showSuccess } from '../users/utils';
import { loadUsers } from '../users/userActions';
import { confirmDialog } from '../confirmDialog';

/**
 * Delete group
 */
export async function deleteGroup(groupId: string): Promise<void> {
  const group = availableGroups.find(g => g.id === groupId);
  if (!group) return;

  const ok = await confirmDialog({
    title: `Delete group "${group.name}"?`,
    body: 'Members of this group will lose any permissions granted by it. Users themselves are not deleted. This action cannot be undone.',
    confirmLabel: 'Delete group',
    destructive: true,
  });
  if (!ok) return;

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
