/**
 * Group modal functionality
 */

import * as api from '../api';
import type { Permission } from '../api';
import { currentEditingGroup, setCurrentEditingGroup } from './state';
import { showError, showSuccess } from '../users/utils';
import { loadUsers } from '../users/userActions';

/**
 * Open create group modal
 */
export function openCreateGroupModal(): void {
  setCurrentEditingGroup(null);
  const modal = document.getElementById('group-modal');
  const title = document.getElementById('group-modal-title');
  const form = document.getElementById('group-form') as HTMLFormElement;

  if (!modal || !title || !form) return;

  title.textContent = 'Create Group';
  form.reset();
  (document.getElementById('group-id') as HTMLInputElement).value = '';

  // Clear permissions list
  const permissionsList = document.getElementById('permissions-list');
  if (permissionsList) {
    permissionsList.innerHTML = '';
  }

  modal.classList.remove('hidden');
}

/**
 * Open edit group modal
 */
export async function openEditGroupModal(groupId: string): Promise<void> {
  try {
    const group = await api.getGroup(groupId);
    setCurrentEditingGroup(group);

    const modal = document.getElementById('group-modal');
    const title = document.getElementById('group-modal-title');
    const form = document.getElementById('group-form') as HTMLFormElement;

    if (!modal || !title || !form) return;

    title.textContent = 'Edit Group';
    (document.getElementById('group-id') as HTMLInputElement).value = group.id;
    (document.getElementById('group-name') as HTMLInputElement).value = group.name;
    (document.getElementById('group-description') as HTMLTextAreaElement).value = group.description || '';

    // Render existing permissions
    renderPermissions(group.permissions);

    modal.classList.remove('hidden');
  } catch (error) {
    console.error('Failed to load group:', error);
    showError('Failed to load group details');
  }
}

/**
 * Close group modal
 */
export function closeGroupModal(): void {
  const modal = document.getElementById('group-modal');
  if (modal) {
    modal.classList.add('hidden');
  }
  setCurrentEditingGroup(null);
}

/**
 * Save group (create or update)
 */
export async function saveGroup(e: Event): Promise<void> {
  e.preventDefault();

  const name = (document.getElementById('group-name') as HTMLInputElement).value;
  const description = (document.getElementById('group-description') as HTMLTextAreaElement).value;
  const permissions = collectPermissions();

  try {
    if (currentEditingGroup) {
      // Update existing group
      await api.updateGroup(currentEditingGroup.id, {
        name,
        description,
        permissions
      });
      showSuccess('Group updated successfully');
    } else {
      // Create new group
      await api.createGroup({
        name,
        description,
        permissions
      });
      showSuccess('Group created successfully');
    }

    closeGroupModal();
    await loadUsers();
  } catch (error) {
    console.error('Failed to save group:', error);
    const err = error as Error;
    showError(`Failed to save group: ${err.message}`);
  }
}

/**
 * Add a new permission to the form
 */
export function addPermission(permission?: Permission): void {
  const permissionsList = document.getElementById('permissions-list');
  if (!permissionsList) return;

  const permDiv = document.createElement('div');
  permDiv.className = 'permission-item';
  permDiv.innerHTML = `
    <div class="form-row">
      <label>Action:
        <select class="perm-action" required>
          <option value="">Select Action</option>
          <option value="purchase:execute" ${permission?.action === 'purchase:execute' ? 'selected' : ''}>Execute Purchase</option>
          <option value="purchase:approve" ${permission?.action === 'purchase:approve' ? 'selected' : ''}>Approve Purchase</option>
          <option value="plan:create" ${permission?.action === 'plan:create' ? 'selected' : ''}>Create Plan</option>
          <option value="plan:update" ${permission?.action === 'plan:update' ? 'selected' : ''}>Update Plan</option>
          <option value="plan:delete" ${permission?.action === 'plan:delete' ? 'selected' : ''}>Delete Plan</option>
          <option value="recommendation:view" ${permission?.action === 'recommendation:view' ? 'selected' : ''}>View Recommendations</option>
          <option value="config:update" ${permission?.action === 'config:update' ? 'selected' : ''}>Update Config</option>
        </select>
      </label>
      <label>Resource:
        <input type="text" class="perm-resource" value="${permission?.resource || '*'}" required placeholder="*">
      </label>
      <button type="button" class="btn-small btn-danger remove-permission-btn">Remove</button>
    </div>
    <div class="constraints-section">
      <h4>Constraints (Optional)</h4>
      <div class="form-row">
        <label>Providers (comma-separated):
          <input type="text" class="perm-providers" value="${permission?.constraints?.providers?.join(', ') || ''}" placeholder="aws, azure, gcp">
        </label>
        <label>Services (comma-separated):
          <input type="text" class="perm-services" value="${permission?.constraints?.services?.join(', ') || ''}" placeholder="ec2, rds">
        </label>
      </div>
      <div class="form-row">
        <label>Regions (comma-separated):
          <input type="text" class="perm-regions" value="${permission?.constraints?.regions?.join(', ') || ''}" placeholder="us-east-1, us-west-2">
        </label>
        <label>Max Amount ($):
          <input type="number" class="perm-max-amount" value="${permission?.constraints?.max_amount || ''}" placeholder="10000" min="0">
        </label>
      </div>
    </div>
  `;

  permissionsList.appendChild(permDiv);

  // Add event listener for remove button
  const removeBtn = permDiv.querySelector('.remove-permission-btn');
  if (removeBtn) {
    removeBtn.addEventListener('click', () => {
      permDiv.remove();
    });
  }
}

/**
 * Render permissions list
 */
function renderPermissions(permissions: Permission[]): void {
  const permissionsList = document.getElementById('permissions-list');
  if (!permissionsList) return;

  permissionsList.innerHTML = '';

  if (permissions.length === 0) {
    addPermission();
  } else {
    permissions.forEach(perm => addPermission(perm));
  }
}

/**
 * Collect permissions from form
 */
function collectPermissions(): Permission[] {
  const permissionsList = document.getElementById('permissions-list');
  if (!permissionsList) return [];

  const permissions: Permission[] = [];
  const items = permissionsList.querySelectorAll('.permission-item');

  items.forEach(item => {
    const action = (item.querySelector('.perm-action') as HTMLSelectElement)?.value;
    const resource = (item.querySelector('.perm-resource') as HTMLInputElement)?.value;

    if (!action || !resource) return;

    const permission: Permission = { action, resource };

    // Collect constraints
    const providers = (item.querySelector('.perm-providers') as HTMLInputElement)?.value;
    const services = (item.querySelector('.perm-services') as HTMLInputElement)?.value;
    const regions = (item.querySelector('.perm-regions') as HTMLInputElement)?.value;
    const maxAmount = (item.querySelector('.perm-max-amount') as HTMLInputElement)?.value;

    if (providers || services || regions || maxAmount) {
      permission.constraints = {};
      if (providers) permission.constraints.providers = providers.split(',').map(s => s.trim()).filter(s => s);
      if (services) permission.constraints.services = services.split(',').map(s => s.trim()).filter(s => s);
      if (regions) permission.constraints.regions = regions.split(',').map(s => s.trim()).filter(s => s);
      if (maxAmount) permission.constraints.max_amount = parseFloat(maxAmount);
    }

    permissions.push(permission);
  });

  return permissions;
}
