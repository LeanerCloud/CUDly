/**
 * Group list rendering functionality
 */

import type { APIGroup, Permission } from '../api';
import { allUsers } from '../users/state';
import { escapeHtml } from '../users/utils';
import { openEditGroupModal, deleteGroup } from './groupActions';

/**
 * Format permission constraints as a readable string for tooltips
 */
function formatConstraints(constraints: Permission['constraints']): string {
  if (!constraints) return '';
  const parts: string[] = [];
  if (constraints.accounts?.length) parts.push(`accounts: ${constraints.accounts.join(', ')}`);
  if (constraints.providers?.length) parts.push(`providers: ${constraints.providers.join(', ')}`);
  if (constraints.services?.length) parts.push(`services: ${constraints.services.join(', ')}`);
  if (constraints.regions?.length) parts.push(`regions: ${constraints.regions.join(', ')}`);
  if (constraints.max_amount != null) parts.push(`max_amount: ${constraints.max_amount}`);
  return parts.join('; ');
}

/**
 * Render groups list as cards with permission badges and member pills
 */
export function renderGroups(groups: APIGroup[]): void {
  const container = document.getElementById('groups-list');
  if (!container) return;

  if (groups.length === 0) {
    container.innerHTML = '<div class="empty">No groups found</div>';
    return;
  }

  const cards = groups.map(group => {
    const members = allUsers.filter(u => u.groups.includes(group.id));
    const memberCount = members.length;

    const permissionBadges = group.permissions.map(perm => {
      const label = escapeHtml(`${perm.action}:${perm.resource}`);
      const constraintStr = formatConstraints(perm.constraints);
      const titleAttr = constraintStr ? ` title="${escapeHtml(constraintStr)}"` : '';
      return `<span class="permission-badge"${titleAttr}>${label}</span>`;
    }).join('');

    const memberPills = members.length > 0
      ? members.map(m => `<span class="member-pill">${escapeHtml(m.email)}</span>`).join('')
      : '<span style="color:#999;font-size:0.85rem;">No members</span>';

    const description = group.description
      ? `<p class="group-description">${escapeHtml(group.description)}</p>`
      : '';

    return `
      <div class="group-card">
        <div class="group-card-header">
          <div>
            <h4>${escapeHtml(group.name)}</h4>
            ${description}
          </div>
          <div class="group-card-actions">
            <span class="badge">${memberCount} member${memberCount !== 1 ? 's' : ''}</span>
            <button class="btn-small edit-group-btn" data-group-id="${escapeHtml(group.id)}">Edit</button>
            <button class="btn-small btn-danger delete-group-btn" data-group-id="${escapeHtml(group.id)}">Delete</button>
          </div>
        </div>
        <div class="group-card-body">
          ${permissionBadges || '<span style="color:#999;font-size:0.85rem;">No permissions</span>'}
        </div>
        <div class="group-members">
          ${memberPills}
        </div>
      </div>
    `;
  }).join('');

  container.innerHTML = cards;

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
