/**
 * Group modal functionality
 */

import * as api from '../api';
import type { APIGroup, Permission } from '../api';
import { currentEditingGroup, setCurrentEditingGroup } from './state';
import { availableGroups } from '../users/state';
import { escapeHtml, showError, showSuccess } from '../users/utils';
import { loadUsers } from '../users/userActions';
import { openModal, closeModal } from '../modal';
import { ALL_ACTIONS, ALL_RESOURCES } from '../permissions';
import type { Action, Resource } from '../permissions';

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

  openModal(modal);
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

    openModal(modal);
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
    closeModal(modal);
  }
  setCurrentEditingGroup(null);
}

/**
 * Save group (create or update)
 */
export async function saveGroup(e: Event): Promise<void> {
  e.preventDefault();

  // Refuse rather than widen. A stored constraint value this form cannot
  // carry unchanged is flagged when the row is rendered; saving anyway would
  // re-submit a materially different permission set than the one loaded, and
  // for a constraint list that means an unintended widening (an empty list is
  // "no restriction" at enforcement). The group stays uneditable through the
  // form until the value is repaired via the API or in the database.
  const blocked = unrepresentablePermissionErrors();
  if (blocked.length > 0) {
    showError(
      `Cannot save: ${blocked.join('; ')}. ` +
      'Saving would silently drop the restriction, so the edit is refused. ' +
      'Repair the value through the API or in the database, then reload this page.'
    );
    return;
  }

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

// Human-readable <option> labels for the action/resource vocabulary.
// Falls back to a generic title-case of the raw value ("cancel-own" ->
// "Cancel Own") for every entry that doesn't need a special-cased
// override; only acronyms and the symbolic "*" need one.
const ACTION_LABEL_OVERRIDES: Partial<Record<Action, string>> = {
  admin: 'Admin (full)',
};

const RESOURCE_LABEL_OVERRIDES: Partial<Record<Resource, string>> = {
  '*': 'All (*)',
  'api-keys': 'API Keys',
  'ri-exchange': 'RI Exchange',
};

function titleCase(value: string): string {
  return value
    .split('-')
    .map(word => word.charAt(0).toUpperCase() + word.slice(1))
    .join(' ');
}

function actionOptionLabel(action: string): string {
  return ACTION_LABEL_OVERRIDES[action as Action] ?? titleCase(action);
}

function resourceOptionLabel(resource: string): string {
  return RESOURCE_LABEL_OVERRIDES[resource as Resource] ?? titleCase(resource);
}

// Builds the <option> list for the perm-action / perm-resource <select>
// elements (issue #1629). This list used to be hardcoded to 7 of the 20
// actions and 9 of the 11 resources the backend can store. A stored
// permission carrying one of the missing values had no matching <option>,
// so the browser fell back to the first entry in the list -- silently
// DROPPING the permission on the action select (index 0 was the empty
// "Select Action" placeholder; collectPermissions() skips a falsy action)
// and silently WIDENING it to the `*` wildcard on the resource select
// (index 0 there was "All (*)"). Editing any group that held one of those
// permissions and clicking Save re-submitted a materially different
// permission list than the one shown, with no error.
//
// Building the lists from permissions.ts's ALL_ACTIONS / ALL_RESOURCES
// (which mirror the backend's full vocabulary, and are exhaustiveness
// -checked against it at compile time) closes that gap for every value
// currently known to the frontend. `currentValue` is still handled
// defensively beyond the known list: if a stored permission's value isn't
// one of them (a future backend verb this form hasn't been taught yet, or
// legacy/foreign data), an extra option is appended for that *exact*
// value, selected and visibly flagged, instead of silently falling back
// to a different one. The select then still represents -- and
// round-trips unchanged through collectPermissions() -- the real stored
// permission rather than corrupting it.
function buildActionOptions(currentValue: string | undefined): string {
  const options = ['<option value="">Select Action</option>'];
  for (const action of ALL_ACTIONS) {
    const selected = currentValue === action ? ' selected' : '';
    options.push(`<option value="${escapeHtml(action)}"${selected}>${escapeHtml(actionOptionLabel(action))}</option>`);
  }
  if (currentValue && !(ALL_ACTIONS as readonly string[]).includes(currentValue)) {
    options.push(`<option value="${escapeHtml(currentValue)}" selected>⚠ ${escapeHtml(currentValue)} (not recognized by this form)</option>`);
  }
  return options.join('');
}

function buildResourceOptions(currentValue: string | undefined): string {
  const options: string[] = [];
  for (const resource of ALL_RESOURCES) {
    const isDefault = !currentValue && resource === '*';
    const selected = currentValue === resource || isDefault ? ' selected' : '';
    options.push(`<option value="${escapeHtml(resource)}"${selected}>${escapeHtml(resourceOptionLabel(resource))}</option>`);
  }
  if (currentValue && !(ALL_RESOURCES as readonly string[]).includes(currentValue)) {
    options.push(`<option value="${escapeHtml(currentValue)}" selected>⚠ ${escapeHtml(currentValue)} (not recognized by this form)</option>`);
  }
  return options.join('');
}

/**
 * Add a new permission to the form
 */
export function addPermission(permission?: Permission): void {
  const permissionsList = document.getElementById('permissions-list');
  if (!permissionsList) return;

  const permDiv = document.createElement('div');
  permDiv.className = 'permission-item';

  // Record the verdict, not the value: which of this row's constraint lists
  // hold something the text encoding cannot carry back unchanged. saveGroup
  // refuses while any row carries this. The attribute lives and dies with the
  // row, so removing the row clears it and nothing outlives the modal.
  const unsafe = unrepresentableDimensions(permission?.constraints);
  if (unsafe.length > 0) {
    permDiv.setAttribute('data-unrepresentable', unsafe.join(', '));
    permDiv.setAttribute('data-permission-label', `${permission?.action ?? ''}:${permission?.resource ?? ''}`);
  }

  permDiv.innerHTML = `
    <div class="form-row">
      <label>Action:
        <select class="perm-action" required>
          ${buildActionOptions(permission?.action)}
        </select>
      </label>
      <label>Resource:
        <select class="perm-resource" required>
          ${buildResourceOptions(permission?.resource)}
        </select>
      </label>
      <button type="button" class="btn-small btn-danger remove-permission-btn">Remove</button>
    </div>
    <div class="constraints-section">
      <h4>Constraints (Optional)</h4>
      <div class="form-row">
        <label>Cloud Account IDs (comma-separated):
          <input type="text" class="perm-accounts" value="${escapeHtml(permission?.constraints?.accounts?.join(', ') || '')}" placeholder="2f1c8e40-...-uuid, unattributed">
        </label>
      </div>
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

// The one tokenizer for every comma-separated constraint input. Both the save
// path and the render-time safety check below go through it, deliberately: if
// this drops or alters a value, the check must catch it, and sharing the
// function is what stops the two from drifting apart.
function parseConstraintList(raw: string): string[] {
  return raw.split(',').map(s => s.trim()).filter(s => s);
}

// Names the constraint lists in `constraints` that this form cannot carry
// back unchanged (issue #1629, raised again by CodeRabbit on PR #1875).
//
// The form encodes a list as comma-separated text, and that encoding is not
// injective. A stored [""] renders blank and re-parses as ABSENT; a stored
// [","] re-parses as an empty list; a stored [" acct A "] comes back trimmed,
// which is a different fence, since enforcement compares values exactly. In
// every case the form would re-submit a materially different restriction than
// the one it loaded, and for a constraint list "different" means WIDER: an
// empty list is "no restriction on this dimension" at enforcement
// (matchStringListConstraints). The refusal in saveGroup is the loud
// alternative to that silent widening.
//
// The test is a round trip through parseConstraintList rather than a
// hand-written blank check, for two reasons: it is the same predicate the
// save path uses, so the two cannot disagree; and a per-entry "is it blank"
// test would MISS [","], whose entry is not blank yet still vanishes, because
// the split runs before the filter.
//
// An absent or empty list is NOT flagged. Both mean "no restriction on this
// dimension", both are perfectly normal, and refusing them would make
// ordinary unconstrained groups uneditable.
function unrepresentableDimensions(constraints: Permission['constraints']): string[] {
  if (!constraints) return [];

  const unsafe: string[] = [];
  for (const dimension of ['accounts', 'providers', 'services', 'regions'] as const) {
    const values = constraints[dimension];
    if (!values || values.length === 0) continue;
    const reparsed = parseConstraintList(values.join(', '));
    if (reparsed.length !== values.length || reparsed.some((value, i) => value !== values[i])) {
      unsafe.push(dimension);
    }
  }
  return unsafe;
}

// Builds one message per flagged row, naming the permission by index and by
// action:resource and naming the constraint list, so an operator with a
// multi-permission group knows exactly which value to repair. Mirrors the
// specificity of the backend's own refusal in validateConstraintEntries.
function unrepresentablePermissionErrors(): string[] {
  const permissionsList = document.getElementById('permissions-list');
  if (!permissionsList) return [];

  const errors: string[] = [];
  permissionsList.querySelectorAll('.permission-item').forEach((item, index) => {
    const dimensions = item.getAttribute('data-unrepresentable');
    if (!dimensions) return;
    const label = item.getAttribute('data-permission-label') || 'unknown';
    errors.push(`permission ${index} (${label}) has a stored "${dimensions}" constraint value this form cannot represent`);
  });
  return errors;
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

    // Collect constraints. Dropping `accounts` here WIDENS the permission
    // rather than merely losing data: an empty AccountIDs list means "no
    // restriction on this dimension" at enforcement
    // (matchStringListConstraints), so a permission scoped to one cloud
    // account came back out of a cosmetic rename scoped to all of them
    // (issue #1629).
    const accounts = (item.querySelector('.perm-accounts') as HTMLInputElement)?.value;
    const providers = (item.querySelector('.perm-providers') as HTMLInputElement)?.value;
    const services = (item.querySelector('.perm-services') as HTMLInputElement)?.value;
    const regions = (item.querySelector('.perm-regions') as HTMLInputElement)?.value;
    const maxAmount = (item.querySelector('.perm-max-amount') as HTMLInputElement)?.value;

    if (accounts || providers || services || regions || maxAmount) {
      permission.constraints = {};
      if (accounts) permission.constraints.accounts = parseConstraintList(accounts);
      if (providers) permission.constraints.providers = parseConstraintList(providers);
      if (services) permission.constraints.services = parseConstraintList(services);
      if (regions) permission.constraints.regions = parseConstraintList(regions);
      if (maxAmount) {
        const parsed = parseFloat(maxAmount);
        // Reject non-finite or negative values (feedback_nullable_not_zero).
        // A malformed entry is silently skipped so the rest of the constraints
        // still reach the API; the input's type="number" min="0" already
        // prevents browser submission of non-numeric values, but the JS
        // path must guard too.
        if (Number.isFinite(parsed) && parsed >= 0) {
          permission.constraints.max_amount = parsed;
        }
      }
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

    openModal(modal);
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
  if (modal) closeModal(modal);
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
