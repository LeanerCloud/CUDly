/**
 * User modal functionality
 */

import * as api from '../api';
import {
  currentEditingUser,
  setCurrentEditingUser,
  availableGroups
} from './state';
import { escapeHtml, showError, showSuccess } from './utils';
import { loadUsers } from './userActions';

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

  // Show password field for new user
  const passwordFields = document.getElementById('password-fields');
  if (passwordFields) {
    passwordFields.style.display = 'block';
    (document.getElementById('user-password') as HTMLInputElement).required = true;
  }

  // Populate groups dropdown
  populateGroupsDropdown();

  modal.classList.remove('hidden');
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
    (document.getElementById('user-role') as HTMLSelectElement).value = user.role;

    // Hide password field for editing
    const passwordFields = document.getElementById('password-fields');
    if (passwordFields) {
      passwordFields.style.display = 'none';
      (document.getElementById('user-password') as HTMLInputElement).required = false;
    }

    // Populate and select groups
    populateGroupsDropdown(user.groups);

    modal.classList.remove('hidden');
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
    modal.classList.add('hidden');
  }
  setCurrentEditingUser(null);
}

/**
 * Save user (create or update)
 */
export async function saveUser(e: Event): Promise<void> {
  e.preventDefault();

  const email = (document.getElementById('user-email') as HTMLInputElement).value;
  const password = (document.getElementById('user-password') as HTMLInputElement).value;
  const role = (document.getElementById('user-role') as HTMLSelectElement).value;
  const groupsSelect = document.getElementById('user-groups') as HTMLSelectElement;
  const selectedGroups = Array.from(groupsSelect.selectedOptions).map(opt => opt.value);

  try {
    if (currentEditingUser) {
      // Update existing user
      await api.updateUser(currentEditingUser.id, {
        email,
        role,
        groups: selectedGroups
      });
      showSuccess('User updated successfully');
    } else {
      // Create new user
      if (!password || password.length < 12) {
        showError('Password must be at least 12 characters');
        return;
      }
      const hasUppercase = /[A-Z]/.test(password);
      const hasLowercase = /[a-z]/.test(password);
      const hasNumber = /[0-9]/.test(password);
      const hasSpecial = /[!@#$%^&*()_+\-=\[\]{};':"\\|,.<>\/?]/.test(password);
      if (!hasUppercase || !hasLowercase || !hasNumber || !hasSpecial) {
        showError('Password must contain uppercase, lowercase, number, and special character');
        return;
      }
      await api.createUser({
        email,
        password,
        role,
        groups: selectedGroups
      });
      showSuccess('User created successfully');
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
 * Populate groups dropdown
 */
function populateGroupsDropdown(selectedGroups: string[] = []): void {
  const groupsSelect = document.getElementById('user-groups') as HTMLSelectElement;
  if (!groupsSelect) return;

  groupsSelect.innerHTML = availableGroups
    .map(group => `
      <option value="${group.id}" ${selectedGroups.includes(group.id) ? 'selected' : ''}>
        ${escapeHtml(group.name)}
      </option>
    `)
    .join('');
}
