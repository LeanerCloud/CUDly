/**
 * Event handlers setup for group management
 */

import { openCreateGroupModal, closeGroupModal, saveGroup, addPermission } from './groupModals';

/**
 * Setup event handlers for group management
 */
export function setupGroupHandlers(): void {
  // Make functions globally available for modal buttons that still need onclick handlers
  (window as any).openCreateGroupModal = openCreateGroupModal;
  (window as any).closeGroupModal = closeGroupModal;
  (window as any).addPermission = () => addPermission();

  // Setup form handlers
  const groupForm = document.getElementById('group-form');
  if (groupForm) {
    groupForm.addEventListener('submit', (e) => void saveGroup(e));
  }
}
