/**
 * Event handlers setup for user management
 *
 * PR #912: bulkChangeRole removed (role concept dropped). The role
 * filter UI element is kept wired for backward compatibility with
 * any deployed HTML that may still have the element, but new builds
 * will not render it.
 */

import { availableGroups } from './state';
import { handleUserSearch, handleFilterChange, clearFilters, updateGroupFilterDropdown } from './filters';
import { openCreateUserModal, closeUserModal, saveUser } from './userModals';
import { bulkDeleteUsers, bulkAddToGroup } from './userActions';

/**
 * Setup event handlers for user management
 */
export function setupUserHandlers(): void {
  // Make functions globally available for modal buttons that still need onclick handlers
  (window as any).openCreateUserModal = openCreateUserModal;
  (window as any).closeUserModal = closeUserModal;

  // Setup form handlers
  const userForm = document.getElementById('user-form');
  if (userForm) {
    userForm.addEventListener('submit', (e) => void saveUser(e));
  }

  // Search input
  const searchInput = document.getElementById('user-search') as HTMLInputElement;
  if (searchInput) {
    searchInput.addEventListener('input', (e) => {
      handleUserSearch((e.target as HTMLInputElement).value);
    });
  }

  // Filter dropdowns
  const roleFilterEl = document.getElementById('user-role-filter') as HTMLSelectElement;
  if (roleFilterEl) {
    roleFilterEl.addEventListener('change', (e) => {
      handleFilterChange('role', (e.target as HTMLSelectElement).value);
    });
  }

  const mfaFilterEl = document.getElementById('user-mfa-filter') as HTMLSelectElement;
  if (mfaFilterEl) {
    mfaFilterEl.addEventListener('change', (e) => {
      handleFilterChange('mfa', (e.target as HTMLSelectElement).value);
    });
  }

  const groupFilterEl = document.getElementById('user-group-filter') as HTMLSelectElement;
  if (groupFilterEl) {
    groupFilterEl.addEventListener('change', (e) => {
      handleFilterChange('group', (e.target as HTMLSelectElement).value);
    });
  }

  // Clear filters button
  const clearFiltersBtn = document.getElementById('clear-filters-btn');
  if (clearFiltersBtn) {
    clearFiltersBtn.addEventListener('click', () => clearFilters());
  }

  // Bulk action buttons
  const bulkDeleteBtn = document.getElementById('bulk-delete-btn');
  if (bulkDeleteBtn) {
    bulkDeleteBtn.addEventListener('click', () => void bulkDeleteUsers());
  }

  // bulk-role-btn removed: bulkChangeRole no longer exists (PR #912).
  // Any existing UI element for it is intentionally left unwired so
  // it becomes a no-op rather than crashing.

  const bulkGroupBtn = document.getElementById('bulk-group-btn');
  if (bulkGroupBtn) {
    bulkGroupBtn.addEventListener('click', () => {
      // Show group selection dialog
      const groupId = prompt(`Enter group ID (available: ${availableGroups.map(g => g.name).join(', ')}):`);
      if (groupId) {
        void bulkAddToGroup(groupId);
      }
    });
  }

  // Populate group filter dropdown
  updateGroupFilterDropdown();
}
