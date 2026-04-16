/**
 * Users module barrel export
 */

// Re-export state
export {
  currentEditingUser,
  availableGroups,
  allUsers,
  filteredUsers,
  selectedUserIds
} from './state';

// Re-export utilities
export { formatRelativeTime, escapeHtml, showError, showSuccess } from './utils';

// Re-export filters
export { applyFilters, handleUserSearch, handleFilterChange, clearFilters, updateGroupFilterDropdown } from './filters';

// Re-export user list rendering
export { renderUsers, renderUserStats, updateBulkActionsBar } from './userList';

// Re-export user modals
export { openCreateUserModal, openEditUserModal, closeUserModal, saveUser } from './userModals';

// Re-export user actions
export { loadUsers, deleteUser, bulkDeleteUsers, bulkChangeRole, bulkAddToGroup } from './userActions';

// Re-export permission matrix
export { renderPermissionMatrix } from './permissionMatrix';

// Re-export handlers
export { setupUserHandlers } from './handlers';
