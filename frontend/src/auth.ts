/**
 * Authentication module for CUDly
 */

import * as api from './api';
import * as state from './state';
import { escapeHtml } from './utils';

// Login rate limiting
let lastLoginAttempt = 0;
const LOGIN_COOLDOWN_MS = 2000; // 2 seconds between attempts

/**
 * Check if current user is admin
 */
export function isAdmin(): boolean {
  const currentUser = state.getCurrentUser();
  return currentUser?.role === 'admin';
}

/**
 * Show reset password modal (for password reset links)
 */
export async function showResetPasswordModal(token: string): Promise<void> {
  // Remove any existing modal to prevent duplicates
  document.getElementById('reset-password-modal')?.remove();

  const modal = document.createElement('div');
  modal.id = 'reset-password-modal';
  modal.innerHTML = `
    <div class="modal-overlay">
      <div class="modal-content">
        <h2>Reset Your Password</h2>

        <form id="reset-password-form">
          <label>New Password:
            <div class="password-input-wrapper">
              <input type="password" id="new-password" placeholder="Enter new password" autocomplete="new-password" required minlength="12">
              <button type="button" class="toggle-password" data-target="new-password" aria-label="Show password">
                <svg class="eye-icon" width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                  <path d="M1 12s4-8 11-8 11 8 11 8-4 8-11 8-11-8-11-8z"></path>
                  <circle cx="12" cy="12" r="3"></circle>
                </svg>
              </button>
            </div>
          </label>

          <div id="password-requirements" class="password-requirements">
            <div class="requirement" id="req-length">
              <span class="req-icon">○</span>
              <span class="req-text">At least 12 characters</span>
            </div>
            <div class="requirement" id="req-uppercase">
              <span class="req-icon">○</span>
              <span class="req-text">One uppercase letter (A-Z)</span>
            </div>
            <div class="requirement" id="req-lowercase">
              <span class="req-icon">○</span>
              <span class="req-text">One lowercase letter (a-z)</span>
            </div>
            <div class="requirement" id="req-number">
              <span class="req-icon">○</span>
              <span class="req-text">One number (0-9)</span>
            </div>
            <div class="requirement" id="req-special">
              <span class="req-icon">○</span>
              <span class="req-text">One special character (!@#$%^&*)</span>
            </div>
          </div>

          <label>Confirm Password:
            <div class="password-input-wrapper">
              <input type="password" id="confirm-password" placeholder="Confirm new password" autocomplete="new-password" required minlength="12">
              <button type="button" class="toggle-password" data-target="confirm-password" aria-label="Show password">
                <svg class="eye-icon" width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                  <path d="M1 12s4-8 11-8 11 8 11 8-4 8-11 8-11-8-11-8z"></path>
                  <circle cx="12" cy="12" r="3"></circle>
                </svg>
              </button>
            </div>
          </label>

          <div id="reset-error" class="error-message hidden"></div>
          <div id="reset-success" class="success-message hidden"></div>
          <button type="submit" class="primary">Reset Password</button>
        </form>
      </div>
    </div>
  `;
  document.body.appendChild(modal);

  const form = document.getElementById('reset-password-form');
  const passwordInput = document.getElementById('new-password') as HTMLInputElement;

  if (form) {
    form.addEventListener('submit', (e) => void handleResetPasswordSubmit(e, token));
  }

  // Add real-time password validation
  if (passwordInput) {
    passwordInput.addEventListener('input', () => {
      updatePasswordRequirements(passwordInput.value);
    });
  }

  // Add password visibility toggle
  setupPasswordToggle(modal);
}

function setupPasswordToggle(container: HTMLElement | Document = document): void {
  const toggleButtons = container.querySelectorAll('.toggle-password');

  toggleButtons.forEach(button => {
    button.addEventListener('click', () => {
      const targetId = button.getAttribute('data-target');
      if (!targetId) return;

      const input = document.getElementById(targetId) as HTMLInputElement;
      if (!input) return;

      const isPassword = input.type === 'password';
      input.type = isPassword ? 'text' : 'password';

      // Update aria-label for accessibility
      button.setAttribute('aria-label', isPassword ? 'Hide password' : 'Show password');

      // Toggle eye icon (add slash when password is visible)
      const svg = button.querySelector('.eye-icon');
      if (svg) {
        if (isPassword) {
          // Show eye-off icon (with slash)
          svg.innerHTML = `
            <path d="M17.94 17.94A10.07 10.07 0 0 1 12 20c-7 0-11-8-11-8a18.45 18.45 0 0 1 5.06-5.94M9.9 4.24A9.12 9.12 0 0 1 12 4c7 0 11 8 11 8a18.5 18.5 0 0 1-2.16 3.19m-6.72-1.07a3 3 0 1 1-4.24-4.24"></path>
            <line x1="1" y1="1" x2="23" y2="23"></line>
          `;
        } else {
          // Show normal eye icon
          svg.innerHTML = `
            <path d="M1 12s4-8 11-8 11 8 11 8-4 8-11 8-11-8-11-8z"></path>
            <circle cx="12" cy="12" r="3"></circle>
          `;
        }
      }
    });
  });
}

function updatePasswordRequirements(password: string, prefix = 'req-'): void {
  const requirements = {
    length: password.length >= 12,
    uppercase: /[A-Z]/.test(password),
    lowercase: /[a-z]/.test(password),
    number: /[0-9]/.test(password),
    special: /[!@#$%^&*()_+\-=\[\]{};':"\\|,.<>\/?]/.test(password)
  };

  // Update each requirement indicator
  updateRequirement(`${prefix}length`, requirements.length);
  updateRequirement(`${prefix}uppercase`, requirements.uppercase);
  updateRequirement(`${prefix}lowercase`, requirements.lowercase);
  updateRequirement(`${prefix}number`, requirements.number);
  updateRequirement(`${prefix}special`, requirements.special);
}

function updateRequirement(id: string, isMet: boolean): void {
  const element = document.getElementById(id);
  if (!element) return;

  const icon = element.querySelector('.req-icon');
  if (!icon) return;

  if (isMet) {
    element.classList.add('met');
    element.classList.remove('unmet');
    icon.textContent = '✓';
  } else {
    element.classList.add('unmet');
    element.classList.remove('met');
    icon.textContent = '○';
  }
}

async function handleResetPasswordSubmit(e: Event, token: string): Promise<void> {
  e.preventDefault();

  const errorDiv = document.getElementById('reset-error');
  const successDiv = document.getElementById('reset-success');
  errorDiv?.classList.add('hidden');
  successDiv?.classList.add('hidden');

  const newPasswordInput = document.getElementById('new-password') as HTMLInputElement | null;
  const confirmPasswordInput = document.getElementById('confirm-password') as HTMLInputElement | null;
  const newPassword = newPasswordInput?.value || '';
  const confirmPassword = confirmPasswordInput?.value || '';

  if (newPassword.length < 12) {
    if (errorDiv) {
      errorDiv.textContent = 'Password must be at least 12 characters long';
      errorDiv.classList.remove('hidden');
    }
    return;
  }

  // Validate password complexity
  const hasUppercase = /[A-Z]/.test(newPassword);
  const hasLowercase = /[a-z]/.test(newPassword);
  const hasNumber = /[0-9]/.test(newPassword);
  const hasSpecial = /[!@#$%^&*()_+\-=\[\]{};':"\\|,.<>\/?]/.test(newPassword);

  if (!hasUppercase || !hasLowercase || !hasNumber || !hasSpecial) {
    if (errorDiv) {
      errorDiv.textContent = 'Password must contain at least one uppercase letter, one lowercase letter, one number, and one special character';
      errorDiv.classList.remove('hidden');
    }
    return;
  }

  if (newPassword !== confirmPassword) {
    if (errorDiv) {
      errorDiv.textContent = 'Passwords do not match';
      errorDiv.classList.remove('hidden');
    }
    return;
  }

  try {
    await api.resetPassword(token, newPassword);

    if (successDiv) {
      successDiv.textContent = 'Password reset successful! Redirecting to login...';
      successDiv.classList.remove('hidden');
    }

    // Redirect to login after 2 seconds
    setTimeout(() => {
      document.getElementById('reset-password-modal')?.remove();
      // Clear URL parameters
      window.history.replaceState({}, document.title, window.location.pathname);
      location.reload();
    }, 2000);
  } catch (error) {
    const err = error as Error;
    if (errorDiv) {
      errorDiv.textContent = err.message || 'Failed to reset password. The link may have expired.';
      errorDiv.classList.remove('hidden');
    }
  }
}

/**
 * Show admin setup modal (first-time bootstrap)
 */
export async function showAdminSetupModal(apiKeyHint?: string): Promise<void> {
  // Remove any existing modal to prevent duplicates
  document.getElementById('admin-setup-modal')?.remove();

  const modal = document.createElement('div');
  modal.id = 'admin-setup-modal';
  modal.innerHTML = `
    <div class="modal-overlay">
      <div class="modal-content">
        <h2>Welcome to CUDly</h2>
        <p>No admin account exists yet. Set up the first admin to get started.</p>
        ${apiKeyHint ? `<p class="help-text">API Key can be found at: <code>${escapeHtml(apiKeyHint)}</code></p>` : ''}

        <form id="admin-setup-form">
          <label>API Key:
            <input type="password" id="setup-api-key" placeholder="Enter your API key" autocomplete="off" required>
          </label>
          <label>Admin Email:
            <input type="email" id="setup-email" placeholder="admin@example.com" autocomplete="email" required>
          </label>
          <label>Password:
            <div class="password-input-wrapper">
              <input type="password" id="setup-password" placeholder="Enter password" autocomplete="new-password" required minlength="12">
              <button type="button" class="toggle-password" data-target="setup-password" aria-label="Show password">
                <svg class="eye-icon" width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                  <path d="M1 12s4-8 11-8 11 8 11 8-4 8-11 8-11-8-11-8z"></path>
                  <circle cx="12" cy="12" r="3"></circle>
                </svg>
              </button>
            </div>
          </label>

          <div id="setup-password-requirements" class="password-requirements">
            <div class="requirement" id="setup-req-length">
              <span class="req-icon">&#9675;</span>
              <span class="req-text">At least 12 characters</span>
            </div>
            <div class="requirement" id="setup-req-uppercase">
              <span class="req-icon">&#9675;</span>
              <span class="req-text">One uppercase letter (A-Z)</span>
            </div>
            <div class="requirement" id="setup-req-lowercase">
              <span class="req-icon">&#9675;</span>
              <span class="req-text">One lowercase letter (a-z)</span>
            </div>
            <div class="requirement" id="setup-req-number">
              <span class="req-icon">&#9675;</span>
              <span class="req-text">One number (0-9)</span>
            </div>
            <div class="requirement" id="setup-req-special">
              <span class="req-icon">&#9675;</span>
              <span class="req-text">One special character (!@#$%^&*)</span>
            </div>
          </div>

          <label>Confirm Password:
            <div class="password-input-wrapper">
              <input type="password" id="setup-confirm-password" placeholder="Confirm password" autocomplete="new-password" required minlength="12">
              <button type="button" class="toggle-password" data-target="setup-confirm-password" aria-label="Show password">
                <svg class="eye-icon" width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                  <path d="M1 12s4-8 11-8 11 8 11 8-4 8-11 8-11-8-11-8z"></path>
                  <circle cx="12" cy="12" r="3"></circle>
                </svg>
              </button>
            </div>
          </label>

          <div id="setup-error" class="error-message hidden"></div>
          <button type="submit" class="primary">Create Admin Account</button>
        </form>
        <p class="help-text"><a href="#" id="admin-setup-login-link">Already have an account? Log in</a></p>
      </div>
    </div>
  `;
  document.body.appendChild(modal);

  const form = document.getElementById('admin-setup-form');
  const passwordInput = document.getElementById('setup-password') as HTMLInputElement;

  if (form) {
    form.addEventListener('submit', (e) => void handleAdminSetupSubmit(e));
  }

  if (passwordInput) {
    passwordInput.addEventListener('input', () => {
      updatePasswordRequirements(passwordInput.value, 'setup-req-');
    });
  }

  const loginLink = document.getElementById('admin-setup-login-link');
  if (loginLink) {
    loginLink.addEventListener('click', (e) => {
      e.preventDefault();
      modal.remove();
      void showLoginModal();
    });
  }

  setupPasswordToggle(modal);
}

async function handleAdminSetupSubmit(e: Event): Promise<void> {
  e.preventDefault();

  const errorDiv = document.getElementById('setup-error');
  errorDiv?.classList.add('hidden');

  const apiKey = (document.getElementById('setup-api-key') as HTMLInputElement)?.value.trim() || '';
  const email = (document.getElementById('setup-email') as HTMLInputElement)?.value.trim() || '';
  const password = (document.getElementById('setup-password') as HTMLInputElement)?.value || '';
  const confirmPassword = (document.getElementById('setup-confirm-password') as HTMLInputElement)?.value || '';

  if (password.length < 12) {
    if (errorDiv) {
      errorDiv.textContent = 'Password must be at least 12 characters long';
      errorDiv.classList.remove('hidden');
    }
    return;
  }

  const hasUppercase = /[A-Z]/.test(password);
  const hasLowercase = /[a-z]/.test(password);
  const hasNumber = /[0-9]/.test(password);
  const hasSpecial = /[!@#$%^&*()_+\-=\[\]{};':"\\|,.<>\/?]/.test(password);

  if (!hasUppercase || !hasLowercase || !hasNumber || !hasSpecial) {
    if (errorDiv) {
      errorDiv.textContent = 'Password must contain at least one uppercase letter, one lowercase letter, one number, and one special character';
      errorDiv.classList.remove('hidden');
    }
    return;
  }

  if (password !== confirmPassword) {
    if (errorDiv) {
      errorDiv.textContent = 'Passwords do not match';
      errorDiv.classList.remove('hidden');
    }
    return;
  }

  const submitBtn = document.querySelector('#admin-setup-form button[type="submit"]') as HTMLButtonElement | null;
  if (submitBtn) {
    submitBtn.disabled = true;
    submitBtn.textContent = 'Creating...';
  }

  try {
    await api.setupAdmin(apiKey, email, password);
    document.getElementById('admin-setup-modal')?.remove();
    location.reload();
  } catch (error) {
    const err = error as Error;
    if (errorDiv) {
      errorDiv.textContent = err.message || 'Failed to create admin account';
      errorDiv.classList.remove('hidden');
    }
  } finally {
    if (submitBtn) {
      submitBtn.disabled = false;
      submitBtn.textContent = 'Create Admin Account';
    }
  }
}

/**
 * Show login modal
 */
export async function showLoginModal(): Promise<void> {
  // Remove any existing login modal to prevent duplicates
  document.getElementById('login-modal')?.remove();

  const modal = document.createElement('div');
  modal.id = 'login-modal';
  modal.innerHTML = `
    <div class="modal-overlay">
      <div class="modal-content">
        <h2>CUDly Login</h2>

        <form id="login-form">
          <label>Email:
            <input type="email" id="login-email" placeholder="admin@example.com" autocomplete="email">
          </label>
          <label>Password:
            <div class="password-input-wrapper">
              <input type="password" id="login-password" placeholder="Password" autocomplete="current-password">
              <button type="button" class="toggle-password" data-target="login-password" aria-label="Show password">
                <svg class="eye-icon" width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                  <path d="M1 12s4-8 11-8 11 8 11 8-4 8-11 8-11-8-11-8z"></path>
                  <circle cx="12" cy="12" r="3"></circle>
                </svg>
              </button>
            </div>
          </label>
          <p class="help-text"><a href="#" id="forgot-password-link">Forgot password?</a></p>

          <div id="login-error" class="error-message hidden"></div>
          <button type="submit" class="primary">Login</button>
        </form>
      </div>
    </div>
  `;
  document.body.appendChild(modal);

  setupLoginModalHandlers(modal);
}

function setupLoginModalHandlers(modal: HTMLElement): void {
  // Forgot password link
  const forgotLink = document.getElementById('forgot-password-link');
  if (forgotLink) {
    forgotLink.addEventListener('click', (e) => {
      e.preventDefault();
      showForgotPasswordForm(modal);
    });
  }

  // Login form submission
  const loginForm = document.getElementById('login-form');
  if (loginForm) {
    loginForm.addEventListener('submit', (e) => void handleLogin(e));
  }

  // Setup password toggle
  setupPasswordToggle(modal);
}

async function handleLogin(e: Event): Promise<void> {
  e.preventDefault();

  // Rate limiting check
  const now = Date.now();
  if (now - lastLoginAttempt < LOGIN_COOLDOWN_MS) {
    const errorDiv = document.getElementById('login-error');
    if (errorDiv) {
      errorDiv.textContent = 'Please wait before trying again';
      errorDiv.classList.remove('hidden');
    }
    return;
  }
  lastLoginAttempt = now;

  const errorDiv = document.getElementById('login-error');
  errorDiv?.classList.add('hidden');

  try {
    const emailInput = document.getElementById('login-email') as HTMLInputElement | null;
    const passwordInput = document.getElementById('login-password') as HTMLInputElement | null;
    const email = emailInput?.value.trim() || '';
    const password = passwordInput?.value || '';
    await api.login(email, password);

    document.getElementById('login-modal')?.remove();
    location.reload();
  } catch (error) {
    const err = error as Error;
    if (errorDiv) {
      errorDiv.textContent = err.message;
      errorDiv.classList.remove('hidden');
    }
  }
}

function showForgotPasswordForm(modal: HTMLElement): void {
  const form = modal.querySelector('#login-form');
  if (!form) return;

  form.innerHTML = `
    <h3>Reset Password</h3>
    <label>Email:
      <input type="email" id="reset-email" placeholder="Your email address" required>
    </label>
    <p class="help-text">We'll send you a link to reset your password.</p>
    <div id="login-error" class="error-message hidden"></div>
    <button type="button" id="send-reset-btn" class="primary">Send Reset Link</button>
    <p><a href="#" id="back-to-login-link">Back to login</a></p>
  `;

  document.getElementById('send-reset-btn')?.addEventListener('click', () => void handlePasswordReset());
  document.getElementById('back-to-login-link')?.addEventListener('click', (e) => {
    e.preventDefault();
    location.reload();
  });
}

async function handlePasswordReset(): Promise<void> {
  const emailInput = document.getElementById('reset-email') as HTMLInputElement | null;
  const email = emailInput?.value.trim() || '';
  if (!email) {
    alert('Please enter your email address');
    return;
  }

  try {
    await api.requestPasswordReset(email);
    alert('If an account exists with that email, you will receive a password reset link.');
  } catch (error) {
    console.error('Password reset error:', error);
    alert('Failed to send reset email. Please try again.');
  }
}

/**
 * Update user UI after login
 */
export function updateUserUI(): void {
  const currentUser = state.getCurrentUser();
  const userEmailEl = document.getElementById('user-email-display');
  const userInfoEl = document.getElementById('user-info');
  const logoutBtn = document.getElementById('logout-btn');

  const roleEl = document.getElementById('user-role-display');

  if (currentUser) {
    // Update the user email display with click-to-edit functionality
    if (userEmailEl) {
      userEmailEl.textContent = currentUser.email;
      userEmailEl.title = 'Click to edit your profile';
      userEmailEl.style.cursor = 'pointer';
      // Replace element to avoid duplicate listeners on repeated calls
      const freshEmailEl = userEmailEl.cloneNode(true) as HTMLElement;
      userEmailEl.parentNode?.replaceChild(freshEmailEl, userEmailEl);
      freshEmailEl.addEventListener('click', () => void openProfileModal());
    }
    // Show role badge for admin users
    if (roleEl) {
      if (currentUser.role === 'admin') {
        roleEl.textContent = '(admin)';
        roleEl.style.display = '';
      } else {
        roleEl.textContent = '';
        roleEl.style.display = 'none';
      }
    }
    // Show the user info section
    if (userInfoEl) {
      userInfoEl.style.display = 'flex';
    }

    const adminOnly = currentUser.role === 'admin';
    document.querySelectorAll<HTMLElement>('.admin-only').forEach(el => {
      el.style.display = adminOnly ? '' : 'none';
    });
  } else {
    // Hide user info when not logged in
    if (userInfoEl) {
      userInfoEl.style.display = 'none';
    }
    if (roleEl) {
      roleEl.style.display = 'none';
    }
  }

  // Setup logout handler
  if (logoutBtn) {
    logoutBtn.addEventListener('click', () => void logout());
  }
}

/**
 * Open profile edit modal
 */
async function openProfileModal(): Promise<void> {
  const currentUser = state.getCurrentUser();
  if (!currentUser) return;

  // Create modal if it doesn't exist
  let modal = document.getElementById('profile-modal');
  if (!modal) {
    modal = document.createElement('div');
    modal.id = 'profile-modal';
    modal.className = 'modal hidden';
    modal.innerHTML = `
      <div class="modal-content">
        <h2>Edit Profile</h2>
        <form id="profile-form">
          <label>
            Email
            <input type="email" id="profile-email" required>
          </label>
          <label>
            Current Password (required to save changes)
            <div class="password-input-wrapper">
              <input type="password" id="profile-current-password" placeholder="Enter current password">
              <button type="button" class="toggle-password" data-target="profile-current-password" aria-label="Show password">
                <svg class="eye-icon" width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                  <path d="M1 12s4-8 11-8 11 8 11 8-4 8-11 8-11-8-11-8z"></path>
                  <circle cx="12" cy="12" r="3"></circle>
                </svg>
              </button>
            </div>
          </label>
          <label>
            New Password (leave blank to keep current)
            <div class="password-input-wrapper">
              <input type="password" id="profile-new-password" placeholder="Enter new password">
              <button type="button" class="toggle-password" data-target="profile-new-password" aria-label="Show password">
                <svg class="eye-icon" width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                  <path d="M1 12s4-8 11-8 11 8 11 8-4 8-11 8-11-8-11-8z"></path>
                  <circle cx="12" cy="12" r="3"></circle>
                </svg>
              </button>
            </div>
          </label>
          <label>
            Confirm New Password
            <div class="password-input-wrapper">
              <input type="password" id="profile-confirm-password" placeholder="Confirm new password">
              <button type="button" class="toggle-password" data-target="profile-confirm-password" aria-label="Show password">
                <svg class="eye-icon" width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                  <path d="M1 12s4-8 11-8 11 8 11 8-4 8-11 8-11-8-11-8z"></path>
                  <circle cx="12" cy="12" r="3"></circle>
                </svg>
              </button>
            </div>
          </label>
          <div class="modal-buttons">
            <button type="button" id="profile-cancel">Cancel</button>
            <button type="submit" class="primary">Save Changes</button>
          </div>
        </form>
      </div>
    `;
    document.body.appendChild(modal);

    // Add event listeners
    document.getElementById('profile-cancel')?.addEventListener('click', closeProfileModal);
    document.getElementById('profile-form')?.addEventListener('submit', (e) => void saveProfile(e));

    // Setup password toggle
    setupPasswordToggle(modal);
  }

  // Populate with current values
  (document.getElementById('profile-email') as HTMLInputElement).value = currentUser.email;
  (document.getElementById('profile-current-password') as HTMLInputElement).value = '';
  (document.getElementById('profile-new-password') as HTMLInputElement).value = '';
  (document.getElementById('profile-confirm-password') as HTMLInputElement).value = '';

  // Show modal
  modal.classList.remove('hidden');
}

/**
 * Close profile modal
 */
function closeProfileModal(): void {
  document.getElementById('profile-modal')?.classList.add('hidden');
}

/**
 * Save profile changes
 */
async function saveProfile(e: Event): Promise<void> {
  e.preventDefault();

  const email = (document.getElementById('profile-email') as HTMLInputElement).value;
  const currentPassword = (document.getElementById('profile-current-password') as HTMLInputElement).value;
  const newPassword = (document.getElementById('profile-new-password') as HTMLInputElement).value;
  const confirmPassword = (document.getElementById('profile-confirm-password') as HTMLInputElement).value;

  if (!currentPassword) {
    alert('Please enter your current password to save changes');
    return;
  }

  if (newPassword) {
    if (newPassword.length < 12) {
      alert('New password must be at least 12 characters long');
      return;
    }
    const hasUppercase = /[A-Z]/.test(newPassword);
    const hasLowercase = /[a-z]/.test(newPassword);
    const hasNumber = /[0-9]/.test(newPassword);
    const hasSpecial = /[!@#$%^&*()_+\-=\[\]{};':"\\|,.<>\/?]/.test(newPassword);
    if (!hasUppercase || !hasLowercase || !hasNumber || !hasSpecial) {
      alert('Password must contain uppercase, lowercase, number, and special character');
      return;
    }
    if (newPassword !== confirmPassword) {
      alert('New passwords do not match');
      return;
    }
  }

  try {
    // Call API to update profile
    await api.apiRequest('/auth/profile', {
      method: 'PUT',
      body: JSON.stringify({
        email,
        current_password: api.base64Encode(currentPassword),
        new_password: newPassword ? api.base64Encode(newPassword) : undefined
      })
    });

    // Update local state
    const currentUser = state.getCurrentUser();
    if (currentUser) {
      state.setCurrentUser({ ...currentUser, email });
      const userEmailEl = document.getElementById('user-email-display');
      if (userEmailEl) userEmailEl.textContent = email;
    }

    closeProfileModal();
    alert('Profile updated successfully');
  } catch (error) {
    console.error('Failed to update profile:', error);
    const err = error as Error;
    alert(`Failed to update profile: ${err.message}`);
  }
}

/**
 * Logout handler
 */
export async function logout(): Promise<void> {
  await api.logout();
  state.setCurrentUser(null);
  location.reload();
}
