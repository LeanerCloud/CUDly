/**
 * User modal functionality
 *
 * PR #912: role removed from the user API contract. Group membership
 * is now required (>= 1 group) on create and update. The role selector
 * has been replaced by a required group multi-select that mirrors the
 * backend's cardinality >= 1 constraint.
 */

import * as api from '../api';
import { describePasswordValidationError } from '../auth';
import {
  currentEditingUser,
  setCurrentEditingUser,
  availableGroups
} from './state';
import { escapeHtml, showError, showSuccess } from './utils';
import { loadUsers } from './userActions';
import { openModal, closeModal } from '../modal';

/**
 * Open create user modal
 */
export function openCreateUserModal(): void {
  setCurrentEditingUser(null);
  const modal = document.getElementById('user-modal');
  const title = document.getElementById('user-modal-title');
  const form = document.getElementById('user-form') as HTMLFormElement;

  if (!modal || !title || !form) return;

  title.textContent = 'Create User';
  form.reset();
  (document.getElementById('user-id') as HTMLInputElement).value = '';

  // Show password field for new user. Password is optional: leaving it
  // blank invites the user via email to set their own password on first
  // login, so don't mark the field required.
  const passwordFields = document.getElementById('password-fields');
  if (passwordFields) {
    passwordFields.classList.remove('hidden');
    (document.getElementById('user-password') as HTMLInputElement).required = false;
  }

  // Populate groups dropdown (no pre-selection for new user)
  populateGroupsDropdown();

  openModal(modal);
}

/**
 * Open edit user modal
 */
export async function openEditUserModal(userId: string): Promise<void> {
  try {
    const user = await api.getUser(userId);
    setCurrentEditingUser(user);

    const modal = document.getElementById('user-modal');
    const title = document.getElementById('user-modal-title');
    const form = document.getElementById('user-form') as HTMLFormElement;

    if (!modal || !title || !form) return;

    title.textContent = 'Edit User';
    (document.getElementById('user-id') as HTMLInputElement).value = user.id;
    (document.getElementById('user-email') as HTMLInputElement).value = user.email;
    // role field no longer exists in the API response (PR #912).

    // Hide password field for editing
    const passwordFields = document.getElementById('password-fields');
    if (passwordFields) {
      passwordFields.classList.add('hidden');
      (document.getElementById('user-password') as HTMLInputElement).required = false;
    }

    // Populate and pre-select groups
    populateGroupsDropdown(user.groups);

    openModal(modal);
  } catch (error) {
    console.error('Failed to load user:', error);
    showError('Failed to load user details');
  }
}

/**
 * Close user modal
 */
export function closeUserModal(): void {
  const modal = document.getElementById('user-modal');
  if (modal) {
    closeModal(modal);
  }
  setCurrentEditingUser(null);
}

/**
 * Save user (create or update).
 *
 * Groups are required (>= 1). The backend enforces this with a DB
 * CHECK constraint (migration 000057); the frontend mirrors it with
 * a pre-submit validation so the user gets a clear message rather
 * than a generic 400 from the server.
 */
export async function saveUser(e: Event): Promise<void> {
  e.preventDefault();

  const email = (document.getElementById('user-email') as HTMLInputElement).value;
  const password = (document.getElementById('user-password') as HTMLInputElement).value;
  // role selector removed: PR #912 drops the role column.
  const groupsSelect = document.getElementById('user-groups') as HTMLSelectElement;
  const selectedGroups = Array.from(groupsSelect.selectedOptions).map(opt => opt.value);

  // Enforce >= 1 group on the client side so the user gets a clear
  // validation message rather than a 400 from the backend.
  if (selectedGroups.length === 0) {
    showError('At least one group is required. Select one or more groups for this user.');
    return;
  }

  try {
    if (currentEditingUser) {
      // Update existing user
      await api.updateUser(currentEditingUser.id, {
        email,
        groups: selectedGroups
      });
      showSuccess('User updated successfully');
    } else {
      // Create new user. Password is optional: if blank, the backend
      // emails an invite that lets the user pick their own password on
      // first login. Only run client-side strength checks when a
      // password was actually entered.
      if (password) {
        const requirementError = describePasswordValidationError(password);
        if (requirementError) {
          showError(requirementError);
          return;
        }
      }
      const result = await api.createUser({
        email,
        password,
        groups: selectedGroups
      });
      // Three outcomes: password-up-front (no invite),
      // password-omitted + invite delivered, password-omitted + invite
      // send failed (user row exists but the recipient is unreachable
      // -- surface a warning so the admin knows to re-mail the link via
      // Forgot Password).
      if (password) {
        showSuccess('User created successfully');
      } else if (result.invite_email_sent === false) {
        showError(
          `User ${email} created but the invitation email could not be sent` +
            (result.invite_email_error ? `: ${result.invite_email_error}` : '') +
            '. Use the Forgot Password link from the sign-in page to re-mail the setup link.'
        );
      } else {
        showSuccess(
          `Invitation email sent to ${email} -- they will set their password on first login`
        );
      }
    }

    closeUserModal();
    await loadUsers();
  } catch (error) {
    console.error('Failed to save user:', error);
    const err = error as Error;
    showError(`Failed to save user: ${err.message}`);
  }
}

/**
 * Populate groups multi-select. Options are generated from the
 * availableGroups list loaded in loadUsers(); preSelectedGroups holds
 * the user's current group UUIDs (empty for new users).
 */
function populateGroupsDropdown(preSelectedGroups: string[] = []): void {
  const groupsSelect = document.getElementById('user-groups') as HTMLSelectElement;
  if (!groupsSelect) return;

  groupsSelect.innerHTML = availableGroups
    .map(group => `
      <option value="${escapeHtml(group.id)}" ${preSelectedGroups.includes(group.id) ? 'selected' : ''}>
        ${escapeHtml(group.name)}
      </option>
    `)
    .join('');
}
