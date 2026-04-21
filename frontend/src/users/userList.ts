/**
 * User list rendering functionality
 */

import type { APIUser } from '../api';
import * as api from '../api';
import {
  allUsers,
  filteredUsers,
  selectedUserIds,
  availableGroups,
  addSelectedUserId,
  removeSelectedUserId,
  clearSelectedUserIds
} from './state';
import { escapeHtml, formatRelativeTime } from './utils';
import { openEditUserModal, deleteUser, loadUsers } from './userActions';

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
          <th width="32"></th>
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
          <tr class="user-row ${selectedUserIds.has(user.id) ? 'row-selected' : ''}" data-user-id="${escapeHtml(user.id)}">
            <td>
              <input type="checkbox" class="user-checkbox" data-user-id="${escapeHtml(user.id)}" ${selectedUserIds.has(user.id) ? 'checked' : ''}>
            </td>
            <td>
              <button class="btn-small btn-icon user-expand-btn" data-user-id="${escapeHtml(user.id)}" title="Expand" aria-expanded="false">&#x25B8;</button>
            </td>
            <td>
              <div class="user-email">
                <strong>${escapeHtml(user.email)}</strong>
                ${user.id === 'current' ? '<span class="badge badge-info">You</span>' : ''}
              </div>
            </td>
            <td><span class="badge badge-${user.role === 'readonly' ? 'readonly' : user.role}">${user.role}</span></td>
            <td>
              <div class="groups-cell">
                ${user.groups.length > 0 ? user.groups.map(g => `<span class="badge badge-group">${escapeHtml(groupName(g))}</span>`).join(' ') : '<span class="text-muted">No groups</span>'}
              </div>
            </td>
            <td>
              ${user.mfa_enabled
                ? '<span class="badge badge-success"><i class="icon-shield"></i> Enabled</span>'
                : '<span class="badge badge-warning"><i class="icon-shield-off"></i> Disabled</span>'}
            </td>
            <td><span class="text-muted">${user.created_at ? new Date(user.created_at).toLocaleDateString() : '-'}</span></td>
            <td><span class="text-muted">${user.last_login ? formatRelativeTime(user.last_login) : 'Never'}</span></td>
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
          <tr class="user-expand-row hidden" data-user-id="${escapeHtml(user.id)}">
            <td colspan="9">
              <div class="user-expand-panel" data-user-id="${escapeHtml(user.id)}"></div>
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
 * Look up a group's display name by ID. Falls back to the ID if the group
 * isn't in the loaded availableGroups list.
 */
function groupName(groupId: string): string {
  const g = availableGroups.find(g => g.id === groupId);
  return g?.name ?? groupId;
}

/**
 * Compute the effective permissions for a user: union of role defaults and
 * all group permissions. Returns permissions as {action}:{resource} strings.
 */
function effectivePermissions(user: APIUser): string[] {
  const perms = new Set<string>();

  // Role defaults (mirror of backend auth/types.go DefaultUserPermissions /
  // DefaultReadOnlyPermissions / DefaultAdminPermissions)
  if (user.role === 'admin') {
    perms.add('admin:*');
  } else if (user.role === 'readonly') {
    ['recommendations', 'plans', 'history'].forEach(r => perms.add(`view:${r}`));
  } else {
    ['recommendations', 'plans', 'purchases', 'history'].forEach(r => perms.add(`view:${r}`));
    perms.add('create:plans');
    perms.add('update:plans');
  }

  // Group permissions
  user.groups.forEach(gid => {
    const g = availableGroups.find(gr => gr.id === gid);
    g?.permissions?.forEach(p => {
      if (p.action && p.resource) perms.add(`${p.action}:${p.resource}`);
    });
  });

  return Array.from(perms).sort();
}

/**
 * Render the expandable panel for a user row with inline group checkboxes
 * and an effective-permissions summary.
 */
function renderUserExpandPanel(user: APIUser): string {
  const userGroups = new Set(user.groups);
  const perms = effectivePermissions(user);

  return `
    <div class="expand-panel-body">
      <div class="expand-panel-groups">
        <h5>Group Membership</h5>
        ${availableGroups.length === 0
          ? '<p class="text-muted">No groups defined yet.</p>'
          : `<div class="group-checkbox-list">
              ${availableGroups.map(g => `
                <label class="group-checkbox-label">
                  <input type="checkbox" class="group-assign-checkbox"
                    data-user-id="${escapeHtml(user.id)}"
                    data-group-id="${escapeHtml(g.id)}"
                    ${userGroups.has(g.id) ? 'checked' : ''}>
                  <span>${escapeHtml(g.name)}</span>
                </label>
              `).join('')}
            </div>`}
      </div>
      <div class="expand-panel-perms">
        <h5>Effective Permissions</h5>
        ${perms.length === 0
          ? '<p class="text-muted">No permissions.</p>'
          : `<div class="perm-badge-list">${perms.map(p => `<span class="permission-badge">${escapeHtml(p)}</span>`).join(' ')}</div>`}
      </div>
    </div>
  `;
}

/**
 * Toggle a group membership for a user inline (no modal).
 */
async function toggleUserGroup(userId: string, groupId: string, checked: boolean): Promise<void> {
  const user = allUsers.find(u => u.id === userId);
  if (!user) return;

  const next = checked
    ? [...new Set([...user.groups, groupId])]
    : user.groups.filter(g => g !== groupId);

  try {
    await api.updateUser(userId, { groups: next });
    await loadUsers();
  } catch (error) {
    console.error('Failed to update user groups:', error);
    // Revert the checkbox
    const cb = document.querySelector<HTMLInputElement>(
      `.group-assign-checkbox[data-user-id="${userId}"][data-group-id="${groupId}"]`,
    );
    if (cb) cb.checked = !checked;
  }
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

  // Row expand toggle: click to expand/collapse a row and populate the
  // inline panel with group checkboxes + effective permissions.
  document.querySelectorAll('.user-expand-btn').forEach(btn => {
    btn.addEventListener('click', () => {
      const userId = (btn as HTMLElement).dataset.userId;
      if (!userId) return;
      const expandRow = document.querySelector<HTMLElement>(
        `tr.user-expand-row[data-user-id="${userId}"]`,
      );
      const panel = document.querySelector<HTMLElement>(
        `.user-expand-panel[data-user-id="${userId}"]`,
      );
      if (!expandRow || !panel) return;

      const isHidden = expandRow.classList.contains('hidden');
      if (isHidden) {
        const user = allUsers.find(u => u.id === userId);
        // eslint-disable-next-line no-unsanitized/property
        if (user) panel.innerHTML = renderUserExpandPanel(user);
        expandRow.classList.remove('hidden');
        btn.setAttribute('aria-expanded', 'true');
        btn.textContent = '\u25BE'; // ▾ down-pointing triangle
      } else {
        expandRow.classList.add('hidden');
        btn.setAttribute('aria-expanded', 'false');
        btn.textContent = '\u25B8'; // ▸ right-pointing triangle
      }
    });
  });

  // Inline group assignment checkboxes inside the expand panel.
  document.addEventListener('change', (e) => {
    const target = e.target as HTMLElement;
    if (!target.classList.contains('group-assign-checkbox')) return;
    const userId = target.dataset.userId;
    const groupId = target.dataset.groupId;
    if (!userId || !groupId) return;
    void toggleUserGroup(userId, groupId, (target as HTMLInputElement).checked);
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
