/**
 * Group modal functionality
 */

import * as api from '../api';
import type { APIGroup, Permission } from '../api';
import { currentEditingGroup, setCurrentEditingGroup } from './state';
import { availableGroups } from '../users/state';
import { escapeHtml, showError, showSuccess } from '../users/utils';
import { loadUsers } from '../users/userActions';

// Module-level state for the duplicate modal — holds the source group so
// saveDuplicateGroup doesn't need another lookup.
let duplicateSourceGroup: APIGroup | null = null;

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
          <option value="view" ${permission?.action === 'view' ? 'selected' : ''}>View</option>
          <option value="create" ${permission?.action === 'create' ? 'selected' : ''}>Create</option>
          <option value="update" ${permission?.action === 'update' ? 'selected' : ''}>Update</option>
          <option value="delete" ${permission?.action === 'delete' ? 'selected' : ''}>Delete</option>
          <option value="execute" ${permission?.action === 'execute' ? 'selected' : ''}>Execute</option>
          <option value="approve" ${permission?.action === 'approve' ? 'selected' : ''}>Approve</option>
          <option value="admin" ${permission?.action === 'admin' ? 'selected' : ''}>Admin (full)</option>
        </select>
      </label>
      <label>Resource:
        <select class="perm-resource" required>
          <option value="*" ${(!permission?.resource || permission?.resource === '*') ? 'selected' : ''}>All (*)</option>
          <option value="recommendations" ${permission?.resource === 'recommendations' ? 'selected' : ''}>Recommendations</option>
          <option value="plans" ${permission?.resource === 'plans' ? 'selected' : ''}>Plans</option>
          <option value="purchases" ${permission?.resource === 'purchases' ? 'selected' : ''}>Purchases</option>
          <option value="accounts" ${permission?.resource === 'accounts' ? 'selected' : ''}>Accounts</option>
          <option value="config" ${permission?.resource === 'config' ? 'selected' : ''}>Config</option>
          <option value="users" ${permission?.resource === 'users' ? 'selected' : ''}>Users</option>
          <option value="groups" ${permission?.resource === 'groups' ? 'selected' : ''}>Groups</option>
          <option value="api-keys" ${permission?.resource === 'api-keys' ? 'selected' : ''}>API Keys</option>
        </select>
      </label>
      <button type="button" class="btn-small btn-danger remove-permission-btn">Remove</button>
    </div>
    <div class="constraints-section">
      <h4>Constraints (Optional)</h4>
      <div class="form-row">
        <label>Providers (comma-separated):
          <input type="text" class="perm-providers" value="${escapeHtml(permission?.constraints?.providers?.join(', ') || '')}" placeholder="aws, azure, gcp">
        </label>
        <label>Services (comma-separated):
          <input type="text" class="perm-services" value="${escapeHtml(permission?.constraints?.services?.join(', ') || '')}" placeholder="ec2, rds">
        </label>
      </div>
      <div class="form-row">
        <label>Regions (comma-separated):
          <input type="text" class="perm-regions" value="${escapeHtml(permission?.constraints?.regions?.join(', ') || '')}" placeholder="us-east-1, us-west-2">
        </label>
        <label>Max Amount ($):
          <input type="number" class="perm-max-amount" value="${escapeHtml(String(permission?.constraints?.max_amount || ''))}" placeholder="10000" min="0">
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
    const resource = (item.querySelector('.perm-resource') as HTMLSelectElement)?.value;

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

// ---------------------------------------------------------------------------
// Duplicate group modal
// ---------------------------------------------------------------------------

const DUP_PROVIDER_PILLS: Array<{ value: string; label: string }> = [
  { value: 'all',   label: 'All' },
  { value: 'aws',   label: 'AWS' },
  { value: 'azure', label: 'Azure' },
  { value: 'gcp',   label: 'GCP' },
];

/**
 * Render a read-only badge list of source permissions as "action:resource"
 * entries. Uses textContent + createElement to avoid innerHTML with user
 * strings.
 */
function renderSourcePermissionBadges(container: HTMLElement, permissions: Permission[]): void {
  container.textContent = '';
  if (permissions.length === 0) {
    const empty = document.createElement('span');
    empty.className = 'dup-empty';
    empty.textContent = 'No permissions on source group';
    container.appendChild(empty);
    return;
  }
  for (const perm of permissions) {
    const badge = document.createElement('span');
    badge.className = 'permission-badge';
    badge.textContent = `${perm.action}:${perm.resource}`;
    container.appendChild(badge);
  }
}

/**
 * Render the provider filter pills (All / AWS / Azure / GCP). Each pill
 * filters the visible account checkboxes by data-provider; selection is
 * UI-only and never stored in the created group.
 */
function renderDuplicateProviderPills(container: HTMLElement, accountsList: HTMLElement): void {
  container.textContent = '';
  const buttons: HTMLButtonElement[] = [];

  for (const pill of DUP_PROVIDER_PILLS) {
    const btn = document.createElement('button');
    btn.type = 'button';
    btn.className = 'btn btn-small target-cloud-pill';
    btn.textContent = pill.label;
    btn.setAttribute('data-provider', pill.value);
    btn.setAttribute('aria-pressed', 'false');
    btn.addEventListener('click', () => {
      for (const b of buttons) {
        const selected = b === btn;
        b.setAttribute('aria-pressed', selected ? 'true' : 'false');
        b.classList.toggle('selected', selected);
      }
      applyDuplicateProviderFilter(accountsList, pill.value);
    });
    buttons.push(btn);
    container.appendChild(btn);
  }

  // Default selection: "All" (first option).
  const first = buttons[0];
  if (first) {
    first.setAttribute('aria-pressed', 'true');
    first.classList.add('selected');
  }
  applyDuplicateProviderFilter(accountsList, 'all');
}

/**
 * Hide/show account checkbox rows by data-provider. "all" shows everything.
 */
function applyDuplicateProviderFilter(accountsList: HTMLElement, provider: string): void {
  const labels = accountsList.querySelectorAll('label[data-provider]');
  labels.forEach(label => {
    const rowProvider = (label as HTMLElement).getAttribute('data-provider') || '';
    const visible = provider === 'all' || provider === rowProvider;
    label.classList.toggle('dup-account-hidden', !visible);
  });
}

/**
 * Render the account checkbox list. Each row is a label + checkbox whose
 * value is the account name (names are what the backend matcher accepts
 * for human-readable scoping).
 */
function renderDuplicateAccountsList(container: HTMLElement, accounts: api.CloudAccount[]): void {
  container.textContent = '';
  if (accounts.length === 0) {
    const empty = document.createElement('p');
    empty.className = 'dup-empty';
    empty.textContent = 'No cloud accounts configured yet. Duplicating without scope clones the full source group — add accounts first if you want to restrict.';
    container.appendChild(empty);
    return;
  }

  for (const acct of accounts) {
    const label = document.createElement('label');
    label.setAttribute('data-provider', acct.provider);

    const cb = document.createElement('input');
    cb.type = 'checkbox';
    cb.className = 'dup-account-checkbox';
    cb.value = acct.name;
    cb.setAttribute('data-provider', acct.provider);

    const text = document.createElement('span');
    text.textContent = `${acct.name} (${acct.external_id}) [${acct.provider}]`;

    label.appendChild(cb);
    label.appendChild(text);
    container.appendChild(label);
  }
}

/**
 * Open the Duplicate Group modal for the given source group.
 *
 * Looks up the source in cached `availableGroups` first, falling back to
 * a fresh `api.getGroup` fetch. Prefills name (with " (copy)" suffix),
 * description, and renders source permissions as read-only badges.
 * Populates account checkboxes from `api.listAccounts()`.
 */
export async function openDuplicateGroupModal(groupId: string): Promise<void> {
  try {
    let source = availableGroups.find(g => g.id === groupId) || null;
    if (!source) {
      source = await api.getGroup(groupId);
    }
    duplicateSourceGroup = source;

    const modal = document.getElementById('group-duplicate-modal');
    if (!modal) return;

    const nameInput = document.getElementById('dup-group-name') as HTMLInputElement | null;
    const descInput = document.getElementById('dup-group-description') as HTMLTextAreaElement | null;
    const permsContainer = document.getElementById('dup-source-permissions');
    const providerFilter = document.getElementById('dup-provider-filter');
    const accountsList = document.getElementById('dup-accounts-list');

    if (nameInput) nameInput.value = `${source.name} (copy)`;
    if (descInput) descInput.value = source.description || '';
    if (permsContainer) renderSourcePermissionBadges(permsContainer, source.permissions);

    // Populate accounts, then wire provider pills to filter them.
    let accounts: api.CloudAccount[] = [];
    try {
      accounts = await api.listAccounts();
    } catch (err) {
      console.error('Failed to list accounts for duplicate modal:', err);
      accounts = [];
    }
    if (accountsList) renderDuplicateAccountsList(accountsList, accounts);
    if (providerFilter && accountsList) renderDuplicateProviderPills(providerFilter, accountsList);

    modal.classList.remove('hidden');
  } catch (error) {
    console.error('Failed to open duplicate group modal:', error);
    showError('Failed to load group details');
  }
}

/**
 * Close the Duplicate Group modal and clear its module-level state.
 */
export function closeDuplicateGroupModal(): void {
  const modal = document.getElementById('group-duplicate-modal');
  if (modal) modal.classList.add('hidden');
  duplicateSourceGroup = null;
}

/**
 * Save the duplicate group — posts to the existing POST /api/groups
 * endpoint. If account checkboxes are ticked, their names become the new
 * group's `allowed_accounts`; otherwise the source's `allowed_accounts`
 * is inherited as-is. Permissions are copied verbatim from the source.
 */
export async function saveDuplicateGroup(e: Event): Promise<void> {
  e.preventDefault();

  const source = duplicateSourceGroup;
  if (!source) {
    showError('No source group to duplicate');
    return;
  }

  const nameInput = document.getElementById('dup-group-name') as HTMLInputElement | null;
  const descInput = document.getElementById('dup-group-description') as HTMLTextAreaElement | null;
  const accountsList = document.getElementById('dup-accounts-list');

  const name = nameInput?.value.trim() || '';
  const description = descInput?.value || '';

  const tickedNames: string[] = [];
  if (accountsList) {
    const checked = accountsList.querySelectorAll('.dup-account-checkbox:checked');
    checked.forEach(cb => {
      const val = (cb as HTMLInputElement).value;
      if (val) tickedNames.push(val);
    });
  }

  const allowedAccounts = tickedNames.length > 0
    ? tickedNames
    : (source.allowed_accounts || []);

  try {
    await api.createGroup({
      name,
      description,
      permissions: source.permissions,
      allowed_accounts: allowedAccounts,
    });
    showSuccess('Group duplicated successfully');
    closeDuplicateGroupModal();
    await loadUsers();
  } catch (error) {
    console.error('Failed to duplicate group:', error);
    const err = error as Error;
    showError(`Failed to duplicate group: ${err.message}`);
  }
}
