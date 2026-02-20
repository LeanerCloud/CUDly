/**
 * User filtering functionality
 */

import {
  allUsers,
  filteredUsers,
  searchQuery,
  roleFilter,
  mfaFilter,
  groupFilter,
  setFilteredUsers,
  setSearchQuery,
  setRoleFilter,
  setMfaFilter,
  setGroupFilter,
  availableGroups
} from './state';
import { renderUsers, renderUserStats } from './userList';
import { escapeHtml } from './utils';

/**
 * Apply search and filters to user list
 */
export function applyFilters(): void {
  const filtered = allUsers.filter(user => {
    // Search filter
    if (searchQuery && !user.email.toLowerCase().includes(searchQuery.toLowerCase())) {
      return false;
    }

    // Role filter
    if (roleFilter && user.role !== roleFilter) {
      return false;
    }

    // MFA filter
    if (mfaFilter === 'enabled' && !user.mfa_enabled) {
      return false;
    }
    if (mfaFilter === 'disabled' && user.mfa_enabled) {
      return false;
    }

    // Group filter
    if (groupFilter && !user.groups.includes(groupFilter)) {
      return false;
    }

    return true;
  });
  setFilteredUsers(filtered);
}

/**
 * Handle search input
 */
export function handleUserSearch(query: string): void {
  setSearchQuery(query);
  applyFilters();
  renderUsers(filteredUsers);
  renderUserStats();
}

/**
 * Handle filter changes
 */
export function handleFilterChange(filterType: string, value: string): void {
  switch (filterType) {
    case 'role':
      setRoleFilter(value);
      break;
    case 'mfa':
      setMfaFilter(value);
      break;
    case 'group':
      setGroupFilter(value);
      break;
  }

  applyFilters();
  renderUsers(filteredUsers);
  renderUserStats();
}

/**
 * Clear all filters
 */
export function clearFilters(): void {
  setSearchQuery('');
  setRoleFilter('');
  setMfaFilter('');
  setGroupFilter('');

  const searchInput = document.getElementById('user-search') as HTMLInputElement;
  if (searchInput) searchInput.value = '';

  const roleSelect = document.getElementById('user-role-filter') as HTMLSelectElement;
  if (roleSelect) roleSelect.value = '';

  const mfaSelect = document.getElementById('user-mfa-filter') as HTMLSelectElement;
  if (mfaSelect) mfaSelect.value = '';

  const groupSelect = document.getElementById('user-group-filter') as HTMLSelectElement;
  if (groupSelect) groupSelect.value = '';

  applyFilters();
  renderUsers(filteredUsers);
  renderUserStats();
}

/**
 * Update group filter dropdown with available groups
 */
export function updateGroupFilterDropdown(): void {
  const groupFilterEl = document.getElementById('user-group-filter') as HTMLSelectElement;
  if (!groupFilterEl) return;

  const currentValue = groupFilterEl.value;
  groupFilterEl.innerHTML = `
    <option value="">All Groups</option>
    ${availableGroups.map(group => `
      <option value="${group.id}">${escapeHtml(group.name)}</option>
    `).join('')}
  `;
  groupFilterEl.value = currentValue;
}
