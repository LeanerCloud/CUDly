/**
 * Groups module barrel export
 */

// Re-export state
export { currentEditingGroup, setCurrentEditingGroup } from './state';

// Re-export group list rendering
export { renderGroups } from './groupList';

// Re-export group modals
export {
  openCreateGroupModal,
  openEditGroupModal,
  closeGroupModal,
  saveGroup,
  addPermission,
  openDuplicateGroupModal,
  closeDuplicateGroupModal,
  saveDuplicateGroup
} from './groupModals';

// Re-export group actions
export { deleteGroup } from './groupActions';

// Re-export handlers
export { setupGroupHandlers } from './handlers';
