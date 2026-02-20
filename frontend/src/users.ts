/**
 * User and Group Management module for CUDly - Enhanced Version
 *
 * This file is a backward-compatibility wrapper.
 * All functionality has been refactored into users/ and groups/ directories.
 *
 * @see ./users/index.ts for user management
 * @see ./groups/index.ts for group management
 */

// Re-export everything from users module
export {
  loadUsers,
  handleUserSearch,
  handleFilterChange,
  clearFilters,
  openCreateUserModal,
  openEditUserModal,
  closeUserModal,
  saveUser,
  deleteUser,
  bulkDeleteUsers,
  bulkChangeRole,
  bulkAddToGroup,
  setupUserHandlers
} from './users/index';

// Re-export everything from groups module
export {
  openCreateGroupModal,
  openEditGroupModal,
  closeGroupModal,
  saveGroup,
  deleteGroup,
  addPermission,
  setupGroupHandlers
} from './groups/index';

// Combined setup function for backward compatibility
export function setupHandlers(): void {
  // Import dynamically to avoid circular dependencies
  import('./users/handlers').then(({ setupUserHandlers }) => setupUserHandlers());
  import('./groups/handlers').then(({ setupGroupHandlers }) => setupGroupHandlers());
}
