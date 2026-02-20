/**
 * Group list rendering functionality
 */

import type { APIGroup } from '../api';
import { allUsers } from '../users/state';
import { escapeHtml } from '../users/utils';
import { openEditGroupModal, deleteGroup } from './groupActions';

/**
 * Render groups list
 */
export function renderGroups(groups: APIGroup[]): void {
  const container = document.getElementById('groups-list');
  if (!container) return;

  if (groups.length === 0) {
    container.innerHTML = '<div class="empty">No groups found</div>';
    return;
  }

  const table = `
    <table class="data-table">
      <thead>
        <tr>
          <th>Name</th>
          <th>Description</th>
          <th>Members</th>
          <th>Permissions</th>
          <th>Created</th>
          <th>Actions</th>
        </tr>
      </thead>
      <tbody>
        ${groups.map(group => {
          const memberCount = allUsers.filter(u => u.groups.includes(group.id)).length;
          return `
            <tr>
              <td><strong>${escapeHtml(group.name)}</strong></td>
              <td>${escapeHtml(group.description || '')}</td>
              <td><span class="badge">${memberCount} member${memberCount !== 1 ? 's' : ''}</span></td>
              <td>${group.permissions.length} permission(s)</td>
              <td>${group.created_at ? new Date(group.created_at).toLocaleDateString() : '-'}</td>
              <td>
                <button class="btn-small edit-group-btn" data-group-id="${escapeHtml(group.id)}">Edit</button>
                <button class="btn-small btn-danger delete-group-btn" data-group-id="${escapeHtml(group.id)}">Delete</button>
              </td>
            </tr>
          `;
        }).join('')}
      </tbody>
    </table>
  `;

  container.innerHTML = table;

  // Add event delegation after rendering
  container.querySelectorAll('.edit-group-btn').forEach(btn => {
    btn.addEventListener('click', () => {
      const groupId = (btn as HTMLElement).dataset.groupId;
      if (groupId) void openEditGroupModal(groupId);
    });
  });
  container.querySelectorAll('.delete-group-btn').forEach(btn => {
    btn.addEventListener('click', () => {
      const groupId = (btn as HTMLElement).dataset.groupId;
      if (groupId) void deleteGroup(groupId);
    });
  });
}
