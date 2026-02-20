/**
 * User list rendering functionality
 */

import type { APIUser } from '../api';
import {
  allUsers,
  filteredUsers,
  selectedUserIds,
  addSelectedUserId,
  removeSelectedUserId,
  clearSelectedUserIds
} from './state';
import { escapeHtml, formatRelativeTime } from './utils';
import { openEditUserModal, deleteUser } from './userActions';

/**
 * Render user statistics
 */
export function renderUserStats(): void {
  const statsContainer = document.getElementById('user-stats');
  if (!statsContainer) return;

  const totalUsers = allUsers.length;
  const adminUsers = allUsers.filter(u => u.role === 'admin').length;
  const mfaEnabled = allUsers.filter(u => u.mfa_enabled).length;
  const showing = filteredUsers.length;

  statsContainer.innerHTML = `
    <div class="stats-grid">
      <div class="stat-card">
        <div class="stat-value">${totalUsers}</div>
        <div class="stat-label">Total Users</div>
      </div>
      <div class="stat-card">
        <div class="stat-value">${adminUsers}</div>
        <div class="stat-label">Administrators</div>
      </div>
      <div class="stat-card">
        <div class="stat-value">${mfaEnabled}</div>
        <div class="stat-label">MFA Enabled</div>
      </div>
      <div class="stat-card ${showing !== totalUsers ? 'stat-card-highlight' : ''}">
        <div class="stat-value">${showing}</div>
        <div class="stat-label">Showing</div>
      </div>
    </div>
  `;
}

/**
 * Render users list with enhanced design
 */
export function renderUsers(users: APIUser[]): void {
  const container = document.getElementById('users-list');
  if (!container) return;

  if (users.length === 0) {
    container.innerHTML = '<div class="empty">No users found matching your filters</div>';
    return;
  }

  const table = `
    <table class="data-table users-table">
      <thead>
        <tr>
          <th width="40">
            <input type="checkbox" id="select-all-users" ${selectedUserIds.size > 0 && selectedUserIds.size === users.length ? 'checked' : ''}>
          </th>
          <th>Email</th>
          <th>Role</th>
          <th>Groups</th>
          <th>MFA</th>
          <th>Created</th>
          <th>Last Login</th>
          <th>Actions</th>
        </tr>
      </thead>
      <tbody>
        ${users.map(user => `
          <tr class="${selectedUserIds.has(user.id) ? 'row-selected' : ''}">
            <td>
              <input type="checkbox" class="user-checkbox" data-user-id="${escapeHtml(user.id)}" ${selectedUserIds.has(user.id) ? 'checked' : ''}>
            </td>
            <td>
              <div class="user-email">
                <strong>${escapeHtml(user.email)}</strong>
                ${user.id === 'current' ? '<span class="badge badge-info">You</span>' : ''}
              </div>
            </td>
            <td><span class="badge ${user.role === 'admin' ? 'badge-admin' : 'badge-user'}">${user.role}</span></td>
            <td>
              <div class="groups-cell">
                ${user.groups.length > 0 ? user.groups.map(g => `<span class="badge badge-group">${escapeHtml(g)}</span>`).join(' ') : '<span class="text-muted">No groups</span>'}
              </div>
            </td>
            <td>
              ${user.mfa_enabled
                ? '<span class="badge badge-success"><i class="icon-shield"></i> Enabled</span>'
                : '<span class="badge badge-warning"><i class="icon-shield-off"></i> Disabled</span>'}
            </td>
            <td><span class="text-muted">${user.created_at ? new Date(user.created_at).toLocaleDateString() : '-'}</span></td>
            <td><span class="text-muted">${(user as any).last_login ? formatRelativeTime((user as any).last_login) : 'Never'}</span></td>
            <td>
              <div class="action-buttons">
                <button class="btn-small btn-icon edit-user-btn" data-user-id="${escapeHtml(user.id)}" title="Edit user">
                  <i class="icon-edit"></i>
                </button>
                <button class="btn-small btn-icon btn-danger delete-user-btn" data-user-id="${escapeHtml(user.id)}" title="Delete user">
                  <i class="icon-trash"></i>
                </button>
              </div>
            </td>
          </tr>
        `).join('')}
      </tbody>
    </table>
  `;

  container.innerHTML = table;

  // Setup event listeners
  setupUserTableListeners();
}

/**
 * Setup event listeners for user table
 */
function setupUserTableListeners(): void {
  // Select all checkbox
  const selectAllCheckbox = document.getElementById('select-all-users') as HTMLInputElement;
  if (selectAllCheckbox) {
    selectAllCheckbox.addEventListener('change', (e) => {
      const checked = (e.target as HTMLInputElement).checked;
      if (checked) {
        filteredUsers.forEach(user => addSelectedUserId(user.id));
      } else {
        clearSelectedUserIds();
      }
      renderUsers(filteredUsers);
      updateBulkActionsBar();
    });
  }

  // Individual checkboxes
  document.querySelectorAll('.user-checkbox').forEach(checkbox => {
    checkbox.addEventListener('change', (e) => {
      const userId = (e.target as HTMLElement).dataset.userId;
      if (!userId) return;

      if ((e.target as HTMLInputElement).checked) {
        addSelectedUserId(userId);
      } else {
        removeSelectedUserId(userId);
      }

      renderUsers(filteredUsers);
      updateBulkActionsBar();
    });
  });

  // Edit buttons
  document.querySelectorAll('.edit-user-btn').forEach(btn => {
    btn.addEventListener('click', () => {
      const userId = (btn as HTMLElement).dataset.userId;
      if (userId) void openEditUserModal(userId);
    });
  });

  // Delete buttons
  document.querySelectorAll('.delete-user-btn').forEach(btn => {
    btn.addEventListener('click', () => {
      const userId = (btn as HTMLElement).dataset.userId;
      if (userId) void deleteUser(userId);
    });
  });
}

/**
 * Update bulk actions bar
 */
export function updateBulkActionsBar(): void {
  const bulkBar = document.getElementById('bulk-actions-bar');
  if (!bulkBar) return;

  const selectedCount = selectedUserIds.size;

  if (selectedCount === 0) {
    bulkBar.classList.add('hidden');
  } else {
    bulkBar.classList.remove('hidden');
    const countEl = document.getElementById('selected-count');
    if (countEl) countEl.textContent = selectedCount.toString();
  }
}
