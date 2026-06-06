/**
 * Authentication module for CUDly
 */

import * as api from './api';
import * as state from './state';
import { escapeHtml } from './utils';
import { openModal, closeModal } from './modal';
import { isAdmin as permissionsIsAdmin, canAccess } from './permissions';

// Login rate limiting
let lastLoginAttempt = 0;
const LOGIN_COOLDOWN_MS = 2000; // 2 seconds between attempts

/**
 * Check if current user is admin. Re-exported from `permissions.ts`
 * so callers that already import from `auth` keep compiling while the
 * canonical definition lives alongside `canAccess` (issue #365).
 */
export function isAdmin(): boolean {
  return permissionsIsAdmin();
}

/**
 * Show reset password modal (for password reset links).
 *
 * Probes the token state first (issues #460, #461) so the user lands
 * on the right view: a form for valid tokens, an "expired link" view
 * for expired tokens, an "already used" view for stale/consumed
 * tokens. On a status-check failure the form is rendered as a
 * fallback so the user still has a path forward; submit-time
 * validation still catches bad tokens server-side.
 */
export async function showResetPasswordModal(token: string): Promise<void> {
  // Remove any existing modal to prevent duplicates
  document.getElementById('reset-password-modal')?.remove();

  const modal = document.createElement('div');
  modal.id = 'reset-password-modal';
  document.body.appendChild(modal);

  let status: { state: string; flow: string };
  try {
    status = await api.getResetTokenStatus(token);
  } catch {
    // Fallback: render the form unconditionally so an offline
    // status endpoint does not strand users who have a valid token.
    renderResetForm(modal, token, 'reset');
    return;
  }

  if (status.state === 'expired') {
    renderExpiredView(modal, status.flow);
    return;
  }
  if (status.state === 'used') {
    renderUsedView(modal, status.flow);
    return;
  }
  // 'valid' (or any unexpected state defaults to the form path).
  renderResetForm(modal, token, status.flow);
}

// renderResetForm builds the password-entry form. Heading/submit copy
// flips between "Reset" and "Set" based on flow (issue #461 invite
// path). Static template, no user-controlled interpolation.
function renderResetForm(modal: HTMLElement, token: string, flow: string): void {
  const heading = flow === 'invite' ? 'Set Your Password' : 'Reset Your Password';
  const submitLabel = flow === 'invite' ? 'Set Password' : 'Reset Password';
  modal.innerHTML = `
    <div class="modal-overlay">
      <div class="modal-content">
        <h2>${heading}</h2>

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
          <button type="submit" class="primary">${submitLabel}</button>
        </form>
      </div>
    </div>
  `;

  const form = document.getElementById('reset-password-form');
  const passwordInput = document.getElementById('new-password') as HTMLInputElement;

  if (form) {
    form.addEventListener('submit', (e) => void handleResetPasswordSubmit(e, token));
  }

  if (passwordInput) {
    passwordInput.addEventListener('input', () => {
      updatePasswordRequirements(passwordInput.value);
    });
  }

  setupPasswordToggle(modal);
}

// renderExpiredView replaces the reset modal with a "link expired"
// view + CTA to request a new reset email (issue #460). The email
// tied to the expired token is not embedded in the URL, so the CTA
// hands the user back to the forgot-password form where they re-type
// their email.
function renderExpiredView(modal: HTMLElement, flow: string): void {
  const heading = flow === 'invite'
    ? 'Invitation link expired'
    : 'Password reset link expired';
  const windowCopy = flow === 'invite'
    ? 'Invitation links are valid for 7 days.'
    : 'Password reset links are valid for one hour.';
  modal.innerHTML = `
    <div class="modal-overlay">
      <div class="modal-content">
        <h2>${heading}</h2>
        <p>${windowCopy}</p>
        <p>Request a new link to continue.</p>
        <button type="button" id="reset-expired-request-new" class="primary">Send a new reset email</button>
        <p><a href="#" id="reset-expired-back-to-login">Back to login</a></p>
      </div>
    </div>
  `;

  document.getElementById('reset-expired-request-new')?.addEventListener('click', () => {
    void openForgotPasswordFromExpired();
  });
  document.getElementById('reset-expired-back-to-login')?.addEventListener('click', (e) => {
    e.preventDefault();
    void closeResetModalAndShowLogin();
  });
}

// renderUsedView covers both consumed and never-existed tokens (the
// server collapses them into one state because the row is wiped on
// consumption; issue #461). Offers two exit paths: log in with the
// password they already set, or restart the forgot-password flow.
function renderUsedView(modal: HTMLElement, flow: string): void {
  const heading = flow === 'invite'
    ? 'Invitation link already used'
    : 'Password reset link already used';
  const body = flow === 'invite'
    ? "This invitation link has already been used. Sign in with the password you set, or use Forgot Password if you do not remember it."
    : "This password reset link has already been used. Sign in with the password you set, or use Forgot Password if you do not remember it.";
  modal.innerHTML = `
    <div class="modal-overlay">
      <div class="modal-content">
        <h2>${heading}</h2>
        <p>${body}</p>
        <button type="button" id="reset-used-go-to-login" class="primary">Go to login</button>
        <p><a href="#" id="reset-used-forgot-password">Forgot password?</a></p>
      </div>
    </div>
  `;

  document.getElementById('reset-used-go-to-login')?.addEventListener('click', () => {
    void closeResetModalAndShowLogin();
  });
  document.getElementById('reset-used-forgot-password')?.addEventListener('click', (e) => {
    e.preventDefault();
    void openForgotPasswordFromExpired();
  });
}

async function closeResetModalAndShowLogin(): Promise<void> {
  document.getElementById('reset-password-modal')?.remove();
  // Clear ?token= so a reload does not bounce back into the reset flow.
  window.history.replaceState({}, document.title, window.location.pathname);
  await showLoginModal();
}

async function openForgotPasswordFromExpired(): Promise<void> {
  document.getElementById('reset-password-modal')?.remove();
  window.history.replaceState({}, document.title, window.location.pathname);
  await showLoginModal();
  // Trigger the forgot-password swap inside the freshly-shown login
  // modal, matching the path a user would take by clicking the
  // "Forgot password?" link manually.
  document.getElementById('forgot-password-link')?.dispatchEvent(
    new MouseEvent('click', { bubbles: true, cancelable: true })
  );
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

// Special-character set used by every password-strength check in this
// module (live indicator + submit-time validator). Module-level constant
// so the regex isn't recompiled on every keystroke AND so the live
// indicator and validator can't silently drift apart (#470 review).
const SPECIAL_CHAR_RE = /[!@#$%^&*()_+\-=\[\]{};':"\\|,.<>\/?]/;

function updatePasswordRequirements(password: string, prefix = 'req-'): void {
  const requirements = {
    length: password.length >= 12,
    uppercase: /[A-Z]/.test(password),
    lowercase: /[a-z]/.test(password),
    number: /[0-9]/.test(password),
    special: SPECIAL_CHAR_RE.test(password)
  };

  // Update each requirement indicator
  updateRequirement(`${prefix}length`, requirements.length);
  updateRequirement(`${prefix}uppercase`, requirements.uppercase);
  updateRequirement(`${prefix}lowercase`, requirements.lowercase);
  updateRequirement(`${prefix}number`, requirements.number);
  updateRequirement(`${prefix}special`, requirements.special);
}

/**
 * Return a user-facing description of which password complexity rules
 * the supplied password fails, or "" when the password satisfies every
 * rule. Length is treated as its own message (an empty password isn't
 * missing "one of N character classes", it's just too short); the
 * complexity rules are joined into a single sentence that names ONLY
 * the rules that actually failed, in priority order. Closes issue #458.
 */
export function describePasswordValidationError(password: string): string {
  if (password.length < 12) {
    return 'Password must be at least 12 characters long';
  }
  const missing: string[] = [];
  if (!/[A-Z]/.test(password)) missing.push('one uppercase letter');
  if (!/[a-z]/.test(password)) missing.push('one lowercase letter');
  if (!/[0-9]/.test(password)) missing.push('one number');
  if (!SPECIAL_CHAR_RE.test(password)) missing.push('one special character');
  if (missing.length === 0) return '';
  if (missing.length === 1) return `Password must contain ${missing[0]}`;
  const last = missing.pop()!;
  return `Password must contain ${missing.join(', ')} and ${last}`;
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

  const requirementError = describePasswordValidationError(newPassword);
  if (requirementError) {
    if (errorDiv) {
      errorDiv.textContent = requirementError;
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

  const requirementError = describePasswordValidationError(password);
  if (requirementError) {
    if (errorDiv) {
      errorDiv.textContent = requirementError;
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

  // Login form submission. Reuse the named listener constant so the
  // MFA two-step swap (issue #497) can swap to handleMFACodeSubmitListener
  // by reference without leaking the original closure.
  const loginForm = document.getElementById('login-form');
  if (loginForm) {
    loginForm.addEventListener('submit', handleLoginSubmitListener);
  }

  // Setup password toggle
  setupPasswordToggle(modal);
}

// Basic email shape check used to short-circuit obviously-malformed input
// client-side before sending it to the server. Intentionally permissive: it
// only catches "obviously not an email" (missing @, missing TLD, whitespace).
// The backend's regex remains the authoritative validator.
const EMAIL_SHAPE_RE = /^[^\s@]+@[^\s@]+\.[^\s@]+$/;

/**
 * Pre-flight validation for the login form. Returns a user-facing message
 * naming the specific problem, or null if the inputs are submittable.
 *
 * Issue #455: empty fields previously produced "Invalid email format" or
 * "Invalid email or password" which hid which field was actually blank.
 * Issue #456 (case 3.3): malformed-email + valid-password previously came
 * back from the server as "authentication failed", which made it look like
 * a credential problem rather than a typo in the email.
 */
function validateLoginInputs(email: string, password: string): string | null {
  if (email === '' && password === '') {
    return 'Enter email and password';
  }
  if (email === '') {
    return 'Enter email address';
  }
  if (password === '') {
    return 'Enter password';
  }
  if (!EMAIL_SHAPE_RE.test(email)) {
    return 'Incorrect email format';
  }
  return null;
}

/**
 * Translate the known generic backend error strings into clearer
 * user-facing copy. The mapping deliberately collapses both
 * "authentication failed" (user not found) and "check your email address
 * and password" (wrong password / inactive / locked) into the same
 * client-side message so we do not regress the account-enumeration
 * mitigation called out in #456 / #550.
 * Anything else (MFA prompts, rate-limit, server errors) passes through
 * unchanged so operational signals are not suppressed.
 */
function mapServerLoginError(message: string): string {
  const lower = message.toLowerCase();
  if (lower.includes('invalid email format')) {
    return 'Incorrect email format';
  }
  if (
    lower.includes('authentication failed') ||
    lower.includes('check your email address and password')
  ) {
    return 'Check your email address and password and try again';
  }
  return message;
}

function showLoginError(message: string): void {
  const errorDiv = document.getElementById('login-error');
  if (errorDiv) {
    errorDiv.textContent = message;
    errorDiv.classList.remove('hidden');
  }
}

// Pending-MFA closure (issue #497). When the server responds with
// `mfa_required` on the first POST /api/auth/login, we keep the
// email + password in memory ONLY for the resubmit. Cleared on
// success, cancel, or modal close. Never persisted.
let pendingMFAEmail = '';
let pendingMFAPassword = '';

function clearPendingMFA(): void {
  pendingMFAEmail = '';
  pendingMFAPassword = '';
}

async function handleLogin(e: Event): Promise<void> {
  e.preventDefault();

  const emailInput = document.getElementById('login-email') as HTMLInputElement | null;
  const passwordInput = document.getElementById('login-password') as HTMLInputElement | null;
  const email = emailInput?.value.trim() || '';
  const password = passwordInput?.value || '';

  // Client-side pre-flight (issues #455 + #456). Runs before the rate-limit
  // check so an accidental click on an empty form does not burn the
  // cooldown window the user needs for their real attempt.
  const preflightError = validateLoginInputs(email, password);
  if (preflightError !== null) {
    showLoginError(preflightError);
    return;
  }

  // Rate limiting check
  const now = Date.now();
  if (now - lastLoginAttempt < LOGIN_COOLDOWN_MS) {
    showLoginError('Please wait before trying again');
    return;
  }
  lastLoginAttempt = now;

  document.getElementById('login-error')?.classList.add('hidden');

  try {
    await api.login(email, password);
    clearPendingMFA();
    document.getElementById('login-modal')?.remove();
    location.reload();
  } catch (error) {
    if (error instanceof api.MFALoginError) {
      // First-leg success: password is correct, server wants the
      // TOTP code. Stash credentials in the closure and swap the
      // form to the MFA code prompt.
      pendingMFAEmail = email;
      pendingMFAPassword = password;
      showMFACodeStep();
      return;
    }
    // `error` is `unknown` per TS strict catch typing. Extract a string
    // defensively so a non-Error rejection (e.g. a thrown string or a
    // plain object) does not produce `undefined.toLowerCase()` inside
    // `mapServerLoginError`.
    const message = error instanceof Error ? error.message : String(error);
    showLoginError(mapServerLoginError(message));
  }
}

/**
 * Render the MFA code prompt as the second step of the login flow.
 * Reuses the existing #login-form container so the rest of the
 * modal chrome (title, layout) stays put. The single 6-char input
 * uses `inputmode="numeric"` + `autocomplete="one-time-code"` so
 * iOS / Android autofill from SMS-like prompts works (issue #497).
 */
function showMFACodeStep(): void {
  const form = document.getElementById('login-form');
  if (!form) return;
  form.innerHTML = `
    <h3>Two-factor code</h3>
    <p class="help-text">Enter the 6-digit code from your authenticator app, or a recovery code.</p>
    <label>Code:
      <input
        type="text"
        id="mfa-code"
        inputmode="numeric"
        autocomplete="one-time-code"
        pattern="[0-9A-Za-z\\-]{6,16}"
        maxlength="16"
        style="font-family: monospace; letter-spacing: 0.2em;"
        autofocus
      >
    </label>
    <div id="login-error" class="error-message hidden"></div>
    <button type="submit" class="primary" id="mfa-submit-btn">Verify</button>
    <p><a href="#" id="mfa-cancel-link">Back to login</a></p>
  `;
  document.getElementById('mfa-cancel-link')?.addEventListener('click', (e) => {
    e.preventDefault();
    clearPendingMFA();
    // Easiest re-render: tear down and re-open the modal.
    document.getElementById('login-modal')?.remove();
    void showLoginModal();
  });
  // Re-bind submit to handleMFACodeSubmit (the form's submit
  // listener attached to handleLogin still fires; replace the form's
  // listener via a fresh handler).
  form.removeEventListener('submit', handleLoginSubmitListener);
  form.addEventListener('submit', handleMFACodeSubmitListener);
}

// Stored listener references so we can swap them on the form
// without losing reference identity. Function-expression form keeps
// each addEventListener / removeEventListener call referencing the
// same Function — arrow IIFEs would create a new function each time.
const handleLoginSubmitListener = (e: Event): void => { void handleLogin(e); };
const handleMFACodeSubmitListener = (e: Event): void => { void handleMFACodeSubmit(e); };

async function handleMFACodeSubmit(e: Event): Promise<void> {
  e.preventDefault();
  const codeInput = document.getElementById('mfa-code') as HTMLInputElement | null;
  const code = codeInput?.value.trim() || '';
  if (code === '') {
    showLoginError('Enter your authenticator code');
    return;
  }
  document.getElementById('login-error')?.classList.add('hidden');
  try {
    await api.login(pendingMFAEmail, pendingMFAPassword, code);
    clearPendingMFA();
    document.getElementById('login-modal')?.remove();
    location.reload();
  } catch (error) {
    if (error instanceof api.MFALoginError && error.code === 'invalid_mfa_code') {
      // Wrong code — stay on the MFA step, surface a specific error.
      showLoginError('Incorrect code, try again');
      if (codeInput) { codeInput.value = ''; codeInput.focus(); }
      return;
    }
    if (error instanceof api.MFALoginError && error.code === 'mfa_required') {
      // Shouldn't reach here (we sent a code), but defensively stay.
      showLoginError('Two-factor code required');
      return;
    }
    const message = error instanceof Error ? error.message : String(error);
    showLoginError(mapServerLoginError(message));
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
  const errorDiv = document.getElementById('login-error');
  const email = emailInput?.value.trim() || '';

  // Reset any previous error before validating.
  errorDiv?.classList.add('hidden');

  if (!email) {
    if (errorDiv) {
      errorDiv.textContent = 'Please enter your email address';
      errorDiv.classList.remove('hidden');
    }
    return;
  }

  try {
    await api.requestPasswordReset(email);
    // Issue #457: previously this surfaced as an alert() that, when
    // closed, left the underlying modal open with nothing for the user
    // to do. Swap the modal body in place with a confirmation panel +
    // single Close button so the user has one explicit exit that
    // returns them to login (option 2 from the issue, agreed cleaner).
    showResetEmailConfirmation();
  } catch (error) {
    console.error('Password reset error:', error);
    if (errorDiv) {
      errorDiv.textContent = 'Failed to send reset email. Please try again.';
      errorDiv.classList.remove('hidden');
    }
  }
}

// showResetEmailConfirmation swaps the inner #login-form body with a
// confirmation panel after a successful forgot-password submission.
// The wrapping #login-modal stays in the DOM so the user has a single
// explicit Close action; fixes issue #457 (lingering modal after the
// confirmation pop-up closed).
function showResetEmailConfirmation(): void {
  const form = document.querySelector('#login-modal #login-form');
  if (!form) return;

  // Static template, no user-controlled interpolation; same pattern
  // the surrounding modal code uses for every panel.
  form.innerHTML = `
    <h3>Check your email</h3>
    <p>If an account exists with that email, you will receive a password reset link shortly.</p>
    <p class="help-text">The link is valid for one hour. If you don't see it, check your spam folder.</p>
    <button type="button" id="reset-confirmation-close" class="primary">Close</button>
  `;

  document.getElementById('reset-confirmation-close')?.addEventListener('click', () => {
    // Reloading returns the user to the login modal in a clean state
    // (same exit-path as the existing back-to-login-link).
    location.reload();
  });
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
      userEmailEl.classList.add('cursor-pointer');
      // Replace element to avoid duplicate listeners on repeated calls
      const freshEmailEl = userEmailEl.cloneNode(true) as HTMLElement;
      userEmailEl.parentNode?.replaceChild(freshEmailEl, userEmailEl);
      freshEmailEl.addEventListener('click', () => void openProfileModal());
    }
    // Show admin badge when the user is a member of the Administrators
    // group. PR #912 removed user.role from the API response; use the
    // group-membership-based isAdmin() predicate instead.
    if (roleEl) {
      if (permissionsIsAdmin()) {
        roleEl.textContent = 'admin';
        roleEl.classList.remove('hidden');
      } else {
        roleEl.textContent = '';
        roleEl.classList.add('hidden');
      }
    }
    // Show the user info section
    if (userInfoEl) {
      userInfoEl.classList.remove('hidden');
    }

    const adminOnly = permissionsIsAdmin();
    document.querySelectorAll<HTMLElement>('.admin-only').forEach(el => {
      el.classList.toggle('visible', adminOnly);
    });

    // Gate nav entries and page containers that require view:purchases.
    // Mirrors the admin-only pattern: the CSS class hides by default;
    // .visible makes the element visible. Direct URL navigation into a
    // gated page is handled in navigation.ts switchTab().
    const canViewPurchases = canAccess('view', 'purchases');
    document.querySelectorAll<HTMLElement>('.requires-purchases').forEach(el => {
      el.classList.toggle('visible', canViewPurchases);
    });
  } else {
    // Hide user info when not logged in
    if (userInfoEl) {
      userInfoEl.classList.add('hidden');
    }
    if (roleEl) {
      roleEl.classList.add('hidden');
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
              <input type="password" id="profile-new-password" placeholder="Enter new password" autocomplete="new-password">
              <button type="button" class="toggle-password" data-target="profile-new-password" aria-label="Show password">
                <svg class="eye-icon" width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                  <path d="M1 12s4-8 11-8 11 8 11 8-4 8-11 8-11-8-11-8z"></path>
                  <circle cx="12" cy="12" r="3"></circle>
                </svg>
              </button>
            </div>
          </label>

          <div id="profile-password-requirements" class="password-requirements">
            <div class="requirement" id="profile-req-length">
              <span class="req-icon">&#9675;</span>
              <span class="req-text">At least 12 characters</span>
            </div>
            <div class="requirement" id="profile-req-uppercase">
              <span class="req-icon">&#9675;</span>
              <span class="req-text">One uppercase letter (A-Z)</span>
            </div>
            <div class="requirement" id="profile-req-lowercase">
              <span class="req-icon">&#9675;</span>
              <span class="req-text">One lowercase letter (a-z)</span>
            </div>
            <div class="requirement" id="profile-req-number">
              <span class="req-icon">&#9675;</span>
              <span class="req-text">One number (0-9)</span>
            </div>
            <div class="requirement" id="profile-req-special">
              <span class="req-icon">&#9675;</span>
              <span class="req-text">One special character (!@#$%^&*)</span>
            </div>
          </div>

          <label>
            Confirm New Password
            <div class="password-input-wrapper">
              <input type="password" id="profile-confirm-password" placeholder="Confirm new password" autocomplete="new-password">
              <button type="button" class="toggle-password" data-target="profile-confirm-password" aria-label="Show password">
                <svg class="eye-icon" width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                  <path d="M1 12s4-8 11-8 11 8 11 8-4 8-11 8-11-8-11-8z"></path>
                  <circle cx="12" cy="12" r="3"></circle>
                </svg>
              </button>
            </div>
          </label>
          <div id="profile-password-error" class="error-message hidden"></div>
          <div class="modal-buttons">
            <button type="button" id="profile-cancel">Cancel</button>
            <button type="submit" class="primary">Save Changes</button>
          </div>
        </form>
        <hr style="margin: 24px 0;">
        <div id="profile-mfa-section">
          <!-- Populated by renderMFASection() on open -->
        </div>
      </div>
    `;
    document.body.appendChild(modal);

    // Add event listeners
    document.getElementById('profile-cancel')?.addEventListener('click', closeProfileModal);
    document.getElementById('profile-form')?.addEventListener('submit', (e) => void saveProfile(e));

    // Live password-strength indicator. Mirrors the reset / admin-setup
    // flows so the user sees criterion check-marks as they type instead
    // of only on submit. Uses a flow-specific prefix so the IDs never
    // collide with other modals that might be mounted on the same page.
    const newPasswordInput = document.getElementById('profile-new-password') as HTMLInputElement | null;
    if (newPasswordInput) {
      newPasswordInput.addEventListener('input', () => {
        updatePasswordRequirements(newPasswordInput.value, 'profile-req-');
      });
    }

    // Setup password toggle
    setupPasswordToggle(modal);
  }

  // Populate with current values
  (document.getElementById('profile-email') as HTMLInputElement).value = currentUser.email;
  (document.getElementById('profile-current-password') as HTMLInputElement).value = '';
  (document.getElementById('profile-new-password') as HTMLInputElement).value = '';
  (document.getElementById('profile-confirm-password') as HTMLInputElement).value = '';

  // (Re-)render the MFA section so its state reflects whatever the
  // user's mfa_enabled flag is right now (could have flipped since
  // the modal was last opened). See renderMFASection below.
  renderMFASection();

  // Reset live indicators and any stale error so the previous open
  // doesn't bleed into this one (modal is created once and reused).
  updatePasswordRequirements('', 'profile-req-');
  const errorDiv = document.getElementById('profile-password-error');
  if (errorDiv) {
    errorDiv.textContent = '';
    errorDiv.classList.add('hidden');
  }

  // Show modal
  openModal(modal);
}

/**
 * Close profile modal
 */
function closeProfileModal(): void {
  const modal = document.getElementById('profile-modal');
  if (modal) closeModal(modal);
}

// ----------------------------------------------------------------
// MFA enrollment / lifecycle UI (issue #497).
//
// The "Two-factor authentication" section in the profile modal has
// two top-level states (disabled / enabled) and three transient
// flows reachable from those states (enroll, disable, regenerate
// recovery codes). The implementation renders the section's HTML
// into a single host element (#profile-mfa-section) and re-renders
// the whole thing on every state change rather than mutating in
// place — easier to reason about, and the section is small.
// ----------------------------------------------------------------

function renderMFASection(): void {
  const host = document.getElementById('profile-mfa-section');
  if (!host) return;
  const currentUser = state.getCurrentUser();
  const mfaEnabled = currentUser?.mfa_enabled === true;
  if (mfaEnabled) {
    host.innerHTML = `
      <h3>Two-factor authentication</h3>
      <p class="help-text">Two-factor authentication is <strong>enabled</strong> for your account.</p>
      <div class="modal-buttons">
        <button type="button" id="mfa-disable-btn">Disable</button>
        <button type="button" id="mfa-regenerate-btn">Regenerate recovery codes</button>
      </div>
      <div id="mfa-flow-host"></div>
    `;
    document.getElementById('mfa-disable-btn')?.addEventListener('click', () => renderMFADisableFlow());
    document.getElementById('mfa-regenerate-btn')?.addEventListener('click', () => renderMFARegenerateFlow());
  } else {
    host.innerHTML = `
      <h3>Two-factor authentication</h3>
      <p class="help-text">Add an extra layer of security by requiring a 6-digit code from an authenticator app at sign-in.</p>
      <div class="modal-buttons">
        <button type="button" id="mfa-enable-btn" class="primary">Set up two-factor authentication</button>
      </div>
      <div id="mfa-flow-host"></div>
    `;
    document.getElementById('mfa-enable-btn')?.addEventListener('click', () => renderMFAEnrollPasswordStep());
  }
}

function getMFAFlowHost(): HTMLElement | null {
  return document.getElementById('mfa-flow-host');
}

function setMFAFlowError(message: string): void {
  const errEl = document.getElementById('mfa-flow-error');
  if (errEl) {
    errEl.textContent = message;
    errEl.classList.remove('hidden');
  }
}

function clearMFAFlowError(): void {
  document.getElementById('mfa-flow-error')?.classList.add('hidden');
}

/**
 * Step 1 of enrollment: re-prompt for the user's password. We don't
 * trust the session token alone to authorise an MFA change — a
 * stolen tab/cookie alone shouldn't let an attacker re-key MFA.
 */
function renderMFAEnrollPasswordStep(): void {
  const host = getMFAFlowHost();
  if (!host) return;
  host.innerHTML = `
    <div style="margin-top: 12px; padding: 12px; border: 1px solid var(--border-color, #ddd); border-radius: 4px;">
      <h4>Confirm password</h4>
      <p class="help-text">Re-enter your password to begin setting up two-factor authentication.</p>
      <label>Password:
        <input type="password" id="mfa-enroll-password" autocomplete="current-password">
      </label>
      <div id="mfa-flow-error" class="error-message hidden"></div>
      <div class="modal-buttons">
        <button type="button" id="mfa-enroll-cancel">Cancel</button>
        <button type="button" id="mfa-enroll-continue" class="primary">Continue</button>
      </div>
    </div>
  `;
  document.getElementById('mfa-enroll-cancel')?.addEventListener('click', () => { host.innerHTML = ''; });
  document.getElementById('mfa-enroll-continue')?.addEventListener('click', () => { void handleMFAEnrollStart(); });
}

async function handleMFAEnrollStart(): Promise<void> {
  const pwInput = document.getElementById('mfa-enroll-password') as HTMLInputElement | null;
  const password = pwInput?.value || '';
  if (password === '') {
    setMFAFlowError('Enter your password');
    return;
  }
  clearMFAFlowError();
  try {
    const setupRes = await api.setupMFA(password);
    await renderMFAEnrollQRStep(setupRes.secret, setupRes.provisioning_uri);
  } catch (err) {
    setMFAFlowError(err instanceof Error ? err.message : String(err));
  }
}

/**
 * Step 2 of enrollment: show the QR code + manual secret + a code
 * input. Dynamically imports the `qrcode` library so the
 * ~30KB-gzipped dep only loads when a user actually starts an
 * enrollment.
 */
async function renderMFAEnrollQRStep(secret: string, provisioningURI: string): Promise<void> {
  const host = getMFAFlowHost();
  if (!host) return;
  host.innerHTML = `
    <div style="margin-top: 12px; padding: 12px; border: 1px solid var(--border-color, #ddd); border-radius: 4px;">
      <h4>Scan QR code</h4>
      <p class="help-text">Scan this code with your authenticator app (Google Authenticator, Authy, Bitwarden, 1Password, etc.).</p>
      <div id="mfa-qr" style="display: flex; justify-content: center; padding: 8px;"></div>
      <p class="help-text">Can't scan? Enter this secret manually:</p>
      <div style="font-family: monospace; padding: 8px; background: var(--bg-subtle, #f5f5f5); border-radius: 4px; word-break: break-all;">
        <span id="mfa-secret-display">${escapeHtml(formatSecretForDisplay(secret))}</span>
        <button type="button" id="mfa-secret-copy" style="margin-left: 8px;">Copy</button>
      </div>
      <label style="margin-top: 12px;">Enter the 6-digit code shown in your app:
        <input
          type="text"
          id="mfa-enroll-code"
          inputmode="numeric"
          autocomplete="one-time-code"
          pattern="[0-9]{6}"
          maxlength="6"
          style="font-family: monospace; letter-spacing: 0.2em;"
        >
      </label>
      <div id="mfa-flow-error" class="error-message hidden"></div>
      <div class="modal-buttons">
        <button type="button" id="mfa-qr-cancel">Cancel</button>
        <button type="button" id="mfa-qr-verify" class="primary">Verify and enable</button>
      </div>
    </div>
  `;

  document.getElementById('mfa-secret-copy')?.addEventListener('click', () => {
    void navigator.clipboard?.writeText(secret);
  });
  document.getElementById('mfa-qr-cancel')?.addEventListener('click', () => { host.innerHTML = ''; });
  document.getElementById('mfa-qr-verify')?.addEventListener('click', () => { void handleMFAEnrollVerify(); });

  // Render the QR. Lazy-imported so the dep stays out of the initial
  // bundle until the user actually starts an enrollment.
  try {
    const qrcode = await import('qrcode');
    const container = document.getElementById('mfa-qr');
    if (container) {
      const dataUrl = await qrcode.toDataURL(provisioningURI, { width: 200, margin: 1 });
      const img = document.createElement('img');
      img.src = dataUrl;
      img.alt = 'Two-factor authentication QR code';
      img.style.maxWidth = '200px';
      container.appendChild(img);
    }
  } catch (err) {
    setMFAFlowError('QR rendering failed; use the manual secret above. ' + (err instanceof Error ? err.message : ''));
  }
}

// formatSecretForDisplay groups the secret into 4-char chunks
// separated by spaces so a user typing it into an authenticator app
// has visual landmarks to track their place.
function formatSecretForDisplay(secret: string): string {
  return secret.match(/.{1,4}/g)?.join(' ') ?? secret;
}

async function handleMFAEnrollVerify(): Promise<void> {
  const codeInput = document.getElementById('mfa-enroll-code') as HTMLInputElement | null;
  const code = codeInput?.value.trim() || '';
  if (!/^\d{6}$/.test(code)) {
    setMFAFlowError('Enter a 6-digit code');
    return;
  }
  clearMFAFlowError();
  try {
    const res = await api.enableMFA(code);
    // Reflect the new state in the cached current user so the
    // section re-renders into the Enabled view when we exit the
    // recovery-codes pane.
    const cur = state.getCurrentUser();
    if (cur) {
      state.setCurrentUser({ ...cur, mfa_enabled: true });
    }
    renderMFAEnrollCodesStep(res.recovery_codes);
  } catch (err) {
    setMFAFlowError(err instanceof Error ? err.message : String(err));
  }
}

/**
 * Step 3 of enrollment: display the plaintext recovery codes. Shown
 * exactly once; backend stores only bcrypt hashes. Offers a download
 * button so the user has a way to keep them somewhere other than a
 * password manager.
 */
function renderMFAEnrollCodesStep(codes: string[]): void {
  const host = getMFAFlowHost();
  if (!host) return;
  const codesList = codes.map((c) => `<li style="font-family: monospace; padding: 2px 0;">${escapeHtml(c)}</li>`).join('');
  host.innerHTML = `
    <div style="margin-top: 12px; padding: 12px; border: 2px solid var(--accent-color, #2563eb); border-radius: 4px;">
      <h4>Save your recovery codes</h4>
      <p class="help-text"><strong>Each code can be used once</strong> if you lose access to your authenticator app. Save them somewhere safe — this is the only time they'll be shown.</p>
      <ul style="list-style: none; padding: 8px; background: var(--bg-subtle, #f5f5f5); border-radius: 4px;">
        ${codesList}
      </ul>
      <div class="modal-buttons">
        <button type="button" id="mfa-codes-download">Download .txt</button>
        <button type="button" id="mfa-codes-done" class="primary">I've saved them</button>
      </div>
    </div>
  `;
  document.getElementById('mfa-codes-download')?.addEventListener('click', () => downloadRecoveryCodes(codes));
  document.getElementById('mfa-codes-done')?.addEventListener('click', () => {
    // Acknowledged — re-render the section so it now shows the
    // Enabled view (Disable / Regenerate buttons).
    renderMFASection();
  });
}

function downloadRecoveryCodes(codes: string[]): void {
  const body = [
    'CUDly two-factor recovery codes',
    `Generated: ${new Date().toISOString()}`,
    '',
    'Each code can be used only once. Save these somewhere safe.',
    '',
    ...codes,
  ].join('\n');
  const blob = new Blob([body], { type: 'text/plain' });
  const url = URL.createObjectURL(blob);
  const a = document.createElement('a');
  a.href = url;
  a.download = 'cudly-recovery-codes.txt';
  document.body.appendChild(a);
  a.click();
  document.body.removeChild(a);
  URL.revokeObjectURL(url);
}

/**
 * Disable flow: prompt for password + (TOTP or recovery code).
 */
function renderMFADisableFlow(): void {
  const host = getMFAFlowHost();
  if (!host) return;
  host.innerHTML = `
    <div style="margin-top: 12px; padding: 12px; border: 1px solid var(--border-color, #ddd); border-radius: 4px;">
      <h4>Disable two-factor authentication</h4>
      <p class="help-text">Enter your password and a current code (or a recovery code) to disable.</p>
      <label>Password:
        <input type="password" id="mfa-disable-password" autocomplete="current-password">
      </label>
      <label style="margin-top: 8px;">Authenticator code or recovery code:
        <input
          type="text"
          id="mfa-disable-code"
          inputmode="numeric"
          autocomplete="one-time-code"
          maxlength="16"
          style="font-family: monospace; letter-spacing: 0.2em;"
        >
      </label>
      <div id="mfa-flow-error" class="error-message hidden"></div>
      <div class="modal-buttons">
        <button type="button" id="mfa-disable-cancel">Cancel</button>
        <button type="button" id="mfa-disable-submit" class="primary">Disable</button>
      </div>
    </div>
  `;
  document.getElementById('mfa-disable-cancel')?.addEventListener('click', () => { host.innerHTML = ''; });
  document.getElementById('mfa-disable-submit')?.addEventListener('click', () => { void handleMFADisableSubmit(); });
}

async function handleMFADisableSubmit(): Promise<void> {
  const pwInput = document.getElementById('mfa-disable-password') as HTMLInputElement | null;
  const codeInput = document.getElementById('mfa-disable-code') as HTMLInputElement | null;
  const password = pwInput?.value || '';
  const code = codeInput?.value.trim() || '';
  if (password === '') { setMFAFlowError('Enter your password'); return; }
  if (code === '') { setMFAFlowError('Enter your authenticator code or a recovery code'); return; }
  clearMFAFlowError();
  try {
    await api.disableMFA(password, code);
    const cur = state.getCurrentUser();
    if (cur) {
      state.setCurrentUser({ ...cur, mfa_enabled: false });
    }
    renderMFASection();
  } catch (err) {
    setMFAFlowError(err instanceof Error ? err.message : String(err));
  }
}

/**
 * Regenerate-recovery-codes flow: prompt for a current TOTP code,
 * call the endpoint, surface the new plaintext codes.
 */
function renderMFARegenerateFlow(): void {
  const host = getMFAFlowHost();
  if (!host) return;
  host.innerHTML = `
    <div style="margin-top: 12px; padding: 12px; border: 1px solid var(--border-color, #ddd); border-radius: 4px;">
      <h4>Regenerate recovery codes</h4>
      <p class="help-text">Enter a current 6-digit code from your authenticator app. Existing recovery codes will stop working.</p>
      <label>Code:
        <input
          type="text"
          id="mfa-regen-code"
          inputmode="numeric"
          autocomplete="one-time-code"
          pattern="[0-9]{6}"
          maxlength="6"
          style="font-family: monospace; letter-spacing: 0.2em;"
        >
      </label>
      <div id="mfa-flow-error" class="error-message hidden"></div>
      <div class="modal-buttons">
        <button type="button" id="mfa-regen-cancel">Cancel</button>
        <button type="button" id="mfa-regen-submit" class="primary">Regenerate</button>
      </div>
    </div>
  `;
  document.getElementById('mfa-regen-cancel')?.addEventListener('click', () => { host.innerHTML = ''; });
  document.getElementById('mfa-regen-submit')?.addEventListener('click', () => { void handleMFARegenerateSubmit(); });
}

async function handleMFARegenerateSubmit(): Promise<void> {
  const codeInput = document.getElementById('mfa-regen-code') as HTMLInputElement | null;
  const code = codeInput?.value.trim() || '';
  if (!/^\d{6}$/.test(code)) { setMFAFlowError('Enter a 6-digit code'); return; }
  clearMFAFlowError();
  try {
    const res = await api.regenerateMFARecoveryCodes(code);
    // Reuse the codes-display step from enrollment — same UX,
    // identical contents.
    renderMFAEnrollCodesStep(res.recovery_codes);
  } catch (err) {
    setMFAFlowError(err instanceof Error ? err.message : String(err));
  }
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

  // Clear any stale password-validation error from a prior submit so we
  // start with a clean slate on every attempt.
  const passwordErrorDiv = document.getElementById('profile-password-error');
  if (passwordErrorDiv) {
    passwordErrorDiv.textContent = '';
    passwordErrorDiv.classList.add('hidden');
  }

  if (!currentPassword) {
    alert('Please enter your current password to save changes');
    return;
  }

  if (newPassword) {
    // Password-validation failures render inline (matches reset and
    // admin-setup flows) so the criterion text appears next to the
    // requirements indicator the user is already looking at.
    const requirementError = describePasswordValidationError(newPassword);
    if (requirementError) {
      if (passwordErrorDiv) {
        passwordErrorDiv.textContent = requirementError;
        passwordErrorDiv.classList.remove('hidden');
      }
      return;
    }
    if (newPassword !== confirmPassword) {
      if (passwordErrorDiv) {
        passwordErrorDiv.textContent = 'New passwords do not match';
        passwordErrorDiv.classList.remove('hidden');
      }
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
