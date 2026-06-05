/**
 * Auth module tests
 */
import { showLoginModal, showResetPasswordModal, updateUserUI, logout } from '../auth';
import { ADMINISTRATORS_GROUP_ID } from '../permissions';

// Mock the api module
jest.mock('../api', () => {
  // Mirror the real MFALoginError class shape so production code's
  // `error instanceof api.MFALoginError` branch can be exercised by
  // tests. Defined inside the factory so the inline `class`
  // declaration isn't hoisted past the jest.mock factory boundary.
  class MFALoginError extends Error {
    code: string;
    constructor(code: string) {
      super(code);
      this.name = 'MFALoginError';
      this.code = code;
      Object.setPrototypeOf(this, MFALoginError.prototype);
    }
  }
  return {
    login: jest.fn(),
    logout: jest.fn(),
    requestPasswordReset: jest.fn(),
    resetPassword: jest.fn(),
    getResetTokenStatus: jest.fn(),
    apiRequest: jest.fn(),
    base64Encode: (s: string) => btoa(s),
    MFALoginError,
    setupMFA: jest.fn(),
    enableMFA: jest.fn(),
    disableMFA: jest.fn(),
    regenerateMFARecoveryCodes: jest.fn(),
  };
});

// Mock the state module
jest.mock('../state', () => ({
  getCurrentUser: jest.fn(),
  setCurrentUser: jest.fn()
}));

import * as api from '../api';
import * as state from '../state';

// Module-level mock time that persists across tests to ensure
// each test gets a time later than any previous rate limit timestamp
let globalMockTime = 1000000000000;

describe('Auth Module', () => {
  beforeEach(() => {
    document.body.innerHTML = '';
    jest.clearAllMocks();
    // Advance the global mock time for each test to bypass rate limiting
    globalMockTime += 100000;
    let mockTime = globalMockTime;
    jest.spyOn(Date, 'now').mockImplementation(() => {
      mockTime += 10000; // Advance 10 seconds each call
      return mockTime;
    });
    // Mock location.reload
    Object.defineProperty(window, 'location', {
      writable: true,
      value: { reload: jest.fn() }
    });
    window.alert = jest.fn();
  });

  afterEach(() => {
    // Restore Date.now
    jest.spyOn(Date, 'now').mockRestore();
  });

  describe('showLoginModal', () => {
    test('creates login modal', async () => {
      await showLoginModal();

      const modal = document.getElementById('login-modal');
      expect(modal).toBeTruthy();
    });

    test('has login form', async () => {
      await showLoginModal();

      const loginForm = document.getElementById('login-form');
      expect(loginForm).toBeTruthy();
    });

    test('has email and password fields', async () => {
      await showLoginModal();

      const emailInput = document.getElementById('login-email');
      const passwordInput = document.getElementById('login-password');
      expect(emailInput).toBeTruthy();
      expect(passwordInput).toBeTruthy();
    });

    test('has forgot password link', async () => {
      await showLoginModal();

      const forgotLink = document.getElementById('forgot-password-link');
      expect(forgotLink).toBeTruthy();
    });

    test('handles login form submission', async () => {
      (api.login as jest.Mock).mockResolvedValue({});
      await showLoginModal();

      const emailInput = document.getElementById('login-email') as HTMLInputElement;
      const passwordInput = document.getElementById('login-password') as HTMLInputElement;
      const loginForm = document.getElementById('login-form');

      emailInput.value = 'test@example.com';
      passwordInput.value = 'password123';

      loginForm?.dispatchEvent(new Event('submit', { cancelable: true }));

      await new Promise(resolve => setTimeout(resolve, 50));

      expect(api.login).toHaveBeenCalledWith('test@example.com', 'password123');
    });

    test('displays login error on failure', async () => {
      (api.login as jest.Mock).mockRejectedValue(new Error('Invalid credentials'));
      await showLoginModal();

      const emailInput = document.getElementById('login-email') as HTMLInputElement;
      const passwordInput = document.getElementById('login-password') as HTMLInputElement;
      const loginForm = document.getElementById('login-form');

      emailInput.value = 'test@example.com';
      passwordInput.value = 'wrongpassword';

      loginForm?.dispatchEvent(new Event('submit', { cancelable: true }));

      await new Promise(resolve => setTimeout(resolve, 50));

      const errorDiv = document.getElementById('login-error');
      expect(errorDiv?.classList.contains('hidden')).toBe(false);
      expect(errorDiv?.textContent).toBe('Invalid credentials');
    });

    // Pre-flight + server-error mapping tests (issues #455 and #456).
    // Helper: submit the login form with the given values and return the
    // resulting error-div text. Uses the same async settle pattern as the
    // surrounding tests.
    async function submitLogin(email: string, password: string): Promise<string | null | undefined> {
      const emailInput = document.getElementById('login-email') as HTMLInputElement;
      const passwordInput = document.getElementById('login-password') as HTMLInputElement;
      const loginForm = document.getElementById('login-form');
      emailInput.value = email;
      passwordInput.value = password;
      loginForm?.dispatchEvent(new Event('submit', { cancelable: true }));
      await new Promise(resolve => setTimeout(resolve, 50));
      const errorDiv = document.getElementById('login-error');
      return errorDiv?.textContent;
    }

    test('empty email shows "Enter email address" without calling api.login', async () => {
      await showLoginModal();
      const text = await submitLogin('', 'somepassword');
      expect(text).toBe('Enter email address');
      expect(api.login).not.toHaveBeenCalled();
    });

    test('empty password shows "Enter password" without calling api.login', async () => {
      await showLoginModal();
      const text = await submitLogin('user@example.com', '');
      expect(text).toBe('Enter password');
      expect(api.login).not.toHaveBeenCalled();
    });

    test('both empty shows single "Enter email and password" message', async () => {
      await showLoginModal();
      const text = await submitLogin('', '');
      expect(text).toBe('Enter email and password');
      expect(api.login).not.toHaveBeenCalled();
    });

    test('malformed email shows "Incorrect email format" without calling api.login', async () => {
      await showLoginModal();
      const text = await submitLogin('not-an-email', 'somepassword');
      expect(text).toBe('Incorrect email format');
      expect(api.login).not.toHaveBeenCalled();
    });

    test('backend "authentication failed" maps to softened copy', async () => {
      (api.login as jest.Mock).mockRejectedValue(new Error('authentication failed'));
      await showLoginModal();
      const text = await submitLogin('user@example.com', 'wrongpassword');
      expect(text).toBe('Check your email address and password and try again');
    });

    test('backend "Check your email address and password and try again" passes through unchanged', async () => {
      (api.login as jest.Mock).mockRejectedValue(
        new Error('Check your email address and password and try again'),
      );
      await showLoginModal();
      const text = await submitLogin('user@example.com', 'wrongpassword');
      expect(text).toBe('Check your email address and password and try again');
    });

    test('backend "invalid email format" maps to "Incorrect email format"', async () => {
      (api.login as jest.Mock).mockRejectedValue(new Error('invalid email format'));
      await showLoginModal();
      // Bypass client-side check with an email that passes the regex but
      // that the backend would reject (e.g. extra-strict server policy).
      const text = await submitLogin('shape-ok@example.com', 'somepassword');
      expect(text).toBe('Incorrect email format');
    });

    test('unknown backend error passes through unchanged (e.g. MFA prompt)', async () => {
      (api.login as jest.Mock).mockRejectedValue(new Error('MFA code required'));
      await showLoginModal();
      const text = await submitLogin('user@example.com', 'somepassword');
      expect(text).toBe('MFA code required');
    });

    test('non-Error rejection (string) is handled defensively', async () => {
      // Guards against a non-Error rejection causing undefined.toLowerCase()
      // inside the server-error mapper.
      (api.login as jest.Mock).mockImplementation(() => Promise.reject('boom'));
      await showLoginModal();
      const text = await submitLogin('user@example.com', 'somepassword');
      expect(text).toBe('boom');
    });

    test('non-Error rejection (plain object) is handled defensively', async () => {
      (api.login as jest.Mock).mockImplementation(() => Promise.reject({ code: 500 }));
      await showLoginModal();
      const text = await submitLogin('user@example.com', 'somepassword');
      // String({...}) returns "[object Object]" — the exact stringification is
      // less important than the fact that no exception is thrown and the
      // login-error div is populated with something.
      expect(text).toBe('[object Object]');
    });

    test('reloads page after successful login', async () => {
      (api.login as jest.Mock).mockResolvedValue({});
      await showLoginModal();

      const emailInput = document.getElementById('login-email') as HTMLInputElement;
      const passwordInput = document.getElementById('login-password') as HTMLInputElement;
      const loginForm = document.getElementById('login-form');

      emailInput.value = 'test@example.com';
      passwordInput.value = 'password123';

      loginForm?.dispatchEvent(new Event('submit', { cancelable: true }));

      await new Promise(resolve => setTimeout(resolve, 50));

      expect(window.location.reload).toHaveBeenCalled();
    });

    test('removes existing modal before creating new one', async () => {
      await showLoginModal();
      const firstModal = document.getElementById('login-modal');

      await showLoginModal();
      const secondModal = document.getElementById('login-modal');

      expect(firstModal).not.toBe(secondModal);
      expect(document.querySelectorAll('#login-modal').length).toBe(1);
    });

    test('forgot password link shows reset form', async () => {
      await showLoginModal();

      const forgotLink = document.getElementById('forgot-password-link');
      forgotLink?.dispatchEvent(new MouseEvent('click', { bubbles: true, cancelable: true }));

      await new Promise(resolve => setTimeout(resolve, 10));

      const resetEmail = document.getElementById('reset-email');
      const sendBtn = document.getElementById('send-reset-btn');
      expect(resetEmail).toBeTruthy();
      expect(sendBtn).toBeTruthy();
    });

    test('password reset swaps modal body to confirmation panel (issue #457)', async () => {
      (api.requestPasswordReset as jest.Mock).mockResolvedValue({});
      await showLoginModal();

      const forgotLink = document.getElementById('forgot-password-link');
      forgotLink?.dispatchEvent(new MouseEvent('click', { bubbles: true, cancelable: true }));

      await new Promise(resolve => setTimeout(resolve, 10));

      const resetEmail = document.getElementById('reset-email') as HTMLInputElement;
      const sendBtn = document.getElementById('send-reset-btn');

      resetEmail.value = 'test@example.com';
      sendBtn?.click();

      await new Promise(resolve => setTimeout(resolve, 50));

      expect(api.requestPasswordReset).toHaveBeenCalledWith('test@example.com');
      // No alert(): the lingering-modal bug came from stacking an
      // alert on top of the modal; we now swap the modal body in place.
      expect(window.alert).not.toHaveBeenCalled();
      // Modal still in DOM with a confirmation panel, NOT the email form.
      const modal = document.getElementById('login-modal');
      expect(modal).toBeTruthy();
      expect(document.getElementById('reset-email')).toBeNull();
      expect(document.getElementById('send-reset-btn')).toBeNull();
      const closeBtn = document.getElementById('reset-confirmation-close');
      expect(closeBtn).toBeTruthy();
      expect(modal?.textContent).toContain('Check your email');
    });

    test('confirmation Close button reloads to return to login (issue #457)', async () => {
      (api.requestPasswordReset as jest.Mock).mockResolvedValue({});
      await showLoginModal();

      const forgotLink = document.getElementById('forgot-password-link');
      forgotLink?.dispatchEvent(new MouseEvent('click', { bubbles: true, cancelable: true }));
      await new Promise(resolve => setTimeout(resolve, 10));

      const resetEmail = document.getElementById('reset-email') as HTMLInputElement;
      resetEmail.value = 'test@example.com';
      document.getElementById('send-reset-btn')?.click();
      await new Promise(resolve => setTimeout(resolve, 50));

      document.getElementById('reset-confirmation-close')?.click();

      expect(window.location.reload).toHaveBeenCalled();
    });

    test('password reset handles empty email inline (no alert)', async () => {
      await showLoginModal();

      const forgotLink = document.getElementById('forgot-password-link');
      forgotLink?.dispatchEvent(new MouseEvent('click', { bubbles: true, cancelable: true }));

      await new Promise(resolve => setTimeout(resolve, 10));

      const sendBtn = document.getElementById('send-reset-btn');
      sendBtn?.click();

      await new Promise(resolve => setTimeout(resolve, 10));

      // Issue #457: error is now inline in the modal, not via alert().
      const errorDiv = document.getElementById('login-error');
      expect(errorDiv?.textContent).toBe('Please enter your email address');
      expect(errorDiv?.classList.contains('hidden')).toBe(false);
      expect(window.alert).not.toHaveBeenCalled();
      expect(api.requestPasswordReset).not.toHaveBeenCalled();
    });

    test('password reset handles API error inline (no alert)', async () => {
      (api.requestPasswordReset as jest.Mock).mockRejectedValue(new Error('Network error'));
      console.error = jest.fn();
      await showLoginModal();

      const forgotLink = document.getElementById('forgot-password-link');
      forgotLink?.dispatchEvent(new MouseEvent('click', { bubbles: true, cancelable: true }));

      await new Promise(resolve => setTimeout(resolve, 10));

      const resetEmail = document.getElementById('reset-email') as HTMLInputElement;
      const sendBtn = document.getElementById('send-reset-btn');

      resetEmail.value = 'test@example.com';
      sendBtn?.click();

      await new Promise(resolve => setTimeout(resolve, 50));

      const errorDiv = document.getElementById('login-error');
      expect(errorDiv?.textContent).toBe('Failed to send reset email. Please try again.');
      expect(errorDiv?.classList.contains('hidden')).toBe(false);
      expect(window.alert).not.toHaveBeenCalled();
    });

    test('back to login link reloads page', async () => {
      await showLoginModal();

      const forgotLink = document.getElementById('forgot-password-link');
      forgotLink?.dispatchEvent(new MouseEvent('click', { bubbles: true, cancelable: true }));

      await new Promise(resolve => setTimeout(resolve, 10));

      const backLink = document.getElementById('back-to-login-link');
      backLink?.dispatchEvent(new MouseEvent('click', { bubbles: true, cancelable: true }));

      await new Promise(resolve => setTimeout(resolve, 10));

      expect(window.location.reload).toHaveBeenCalled();
    });
  });

  describe('updateUserUI', () => {
    beforeEach(() => {
      document.body.innerHTML = `
        <div id="user-info" class="hidden">
          <span id="user-email-display"></span>
          <button id="logout-btn">Logout</button>
        </div>
        <div class="admin-only hidden">Admin content</div>
        <a class="requires-purchases" id="purchases-tab-btn">Purchases</a>
        <a class="requires-purchases" id="inventory-tab-btn">Inventory &amp; Coverage</a>
      `;
    });

    test('updates user email when user is logged in', () => {
      (state.getCurrentUser as jest.Mock).mockReturnValue({
        id: 'user-1',
        email: 'test@example.com',
        groups: []
      });

      updateUserUI();

      const userEmail = document.getElementById('user-email-display');
      expect(userEmail?.textContent).toBe('test@example.com');
    });

    test('shows user info when logged in', () => {
      (state.getCurrentUser as jest.Mock).mockReturnValue({
        id: 'user-1',
        email: 'test@example.com',
        groups: []
      });

      updateUserUI();

      const userInfo = document.getElementById('user-info');
      expect((userInfo as HTMLElement).classList.contains('hidden')).toBe(false);
    });

    test('hides user info when not logged in', () => {
      (state.getCurrentUser as jest.Mock).mockReturnValue(null);

      updateUserUI();

      const userInfo = document.getElementById('user-info');
      expect((userInfo as HTMLElement).classList.contains('hidden')).toBe(true);
    });

    test('shows admin-only elements for admin users', () => {
      (state.getCurrentUser as jest.Mock).mockReturnValue({
        id: 'admin-1',
        email: 'admin@example.com',
        groups: [ADMINISTRATORS_GROUP_ID]
      });

      updateUserUI();

      const adminElements = document.querySelectorAll<HTMLElement>('.admin-only');
      adminElements.forEach(el => {
        expect(el.classList.contains('visible')).toBe(true);
      });
    });

    test('hides admin-only elements for regular users', () => {
      (state.getCurrentUser as jest.Mock).mockReturnValue({
        id: 'user-1',
        email: 'user@example.com',
        groups: []
      });

      updateUserUI();

      const adminElements = document.querySelectorAll<HTMLElement>('.admin-only');
      adminElements.forEach(el => {
        expect(el.classList.contains('visible')).toBe(false);
      });
    });

    // issue #1000: requires-purchases nav gating
    test('shows requires-purchases nav elements for a user with view:purchases', () => {
      (state.getCurrentUser as jest.Mock).mockReturnValue({
        id: 'user-1',
        email: 'user@example.com',
        groups: [],
        effectivePermissions: [{ action: 'view', resource: 'purchases' }],
      });

      updateUserUI();

      const els = document.querySelectorAll<HTMLElement>('.requires-purchases');
      expect(els.length).toBeGreaterThan(0);
      els.forEach(el => {
        expect(el.classList.contains('visible')).toBe(true);
      });
    });

    test('hides requires-purchases nav elements for a read-only user without view:purchases', () => {
      (state.getCurrentUser as jest.Mock).mockReturnValue({
        id: 'readonly-1',
        email: 'readonly@example.com',
        groups: [],
        // READONLY_PERMS does not include view:purchases
        effectivePermissions: [
          { action: 'view', resource: 'recommendations' },
          { action: 'view', resource: 'plans' },
          { action: 'view', resource: 'history' },
        ],
      });

      updateUserUI();

      const els = document.querySelectorAll<HTMLElement>('.requires-purchases');
      expect(els.length).toBeGreaterThan(0);
      els.forEach(el => {
        expect(el.classList.contains('visible')).toBe(false);
      });
    });

    test('shows requires-purchases nav elements for an admin (admin:* covers view:purchases)', () => {
      (state.getCurrentUser as jest.Mock).mockReturnValue({
        id: 'admin-1',
        email: 'admin@example.com',
        groups: [ADMINISTRATORS_GROUP_ID],
        effectivePermissions: [{ action: 'admin', resource: '*' }],
      });

      updateUserUI();

      const els = document.querySelectorAll<HTMLElement>('.requires-purchases');
      expect(els.length).toBeGreaterThan(0);
      els.forEach(el => {
        expect(el.classList.contains('visible')).toBe(true);
      });
    });

    test('makes user email clickable', () => {
      (state.getCurrentUser as jest.Mock).mockReturnValue({
        id: 'user-1',
        email: 'test@example.com',
        groups: []
      });

      updateUserUI();

      const userEmail = document.getElementById('user-email-display') as HTMLElement;
      expect(userEmail.classList.contains('cursor-pointer')).toBe(true);
      expect(userEmail.title).toBe('Click to edit your profile');
    });

    test('sets up logout button handler', () => {
      (state.getCurrentUser as jest.Mock).mockReturnValue({
        id: 'user-1',
        email: 'test@example.com',
        groups: []
      });
      (api.logout as jest.Mock).mockResolvedValue({});

      updateUserUI();

      const logoutBtn = document.getElementById('logout-btn');
      expect(logoutBtn).toBeTruthy();

      // Click logout button
      logoutBtn?.click();

      // Should call logout
      expect(api.logout).toHaveBeenCalled();
    });
  });

  describe('logout', () => {
    test('calls API logout and clears user state', async () => {
      (api.logout as jest.Mock).mockResolvedValue({});

      await logout();

      expect(api.logout).toHaveBeenCalled();
      expect(state.setCurrentUser).toHaveBeenCalledWith(null);
      expect(window.location.reload).toHaveBeenCalled();
    });
  });

  describe('profile modal', () => {
    beforeEach(() => {
      document.body.innerHTML = `
        <div id="user-info">
          <span id="user-email-display"></span>
        </div>
      `;
      (state.getCurrentUser as jest.Mock).mockReturnValue({
        id: 'user-1',
        email: 'test@example.com',
        groups: []
      });
    });

    test('clicking email opens profile modal', async () => {
      updateUserUI();

      const userEmail = document.getElementById('user-email-display') as HTMLElement;
      userEmail.click();

      await new Promise(resolve => setTimeout(resolve, 10));

      const modal = document.getElementById('profile-modal');
      expect(modal).toBeTruthy();
      expect(modal?.classList.contains('hidden')).toBe(false);
    });

    test('profile modal populates with current user email', async () => {
      updateUserUI();

      const userEmail = document.getElementById('user-email-display') as HTMLElement;
      userEmail.click();

      await new Promise(resolve => setTimeout(resolve, 10));

      const emailInput = document.getElementById('profile-email') as HTMLInputElement;
      expect(emailInput.value).toBe('test@example.com');
    });

    test('cancel button closes profile modal', async () => {
      updateUserUI();

      const userEmail = document.getElementById('user-email-display') as HTMLElement;
      userEmail.click();

      await new Promise(resolve => setTimeout(resolve, 10));

      const cancelBtn = document.getElementById('profile-cancel');
      cancelBtn?.click();

      const modal = document.getElementById('profile-modal');
      expect(modal?.classList.contains('hidden')).toBe(true);
    });

    test('save profile requires current password', async () => {
      updateUserUI();

      const userEmail = document.getElementById('user-email-display') as HTMLElement;
      userEmail.click();

      await new Promise(resolve => setTimeout(resolve, 10));

      const form = document.getElementById('profile-form');
      form?.dispatchEvent(new Event('submit', { cancelable: true }));

      await new Promise(resolve => setTimeout(resolve, 10));

      expect(window.alert).toHaveBeenCalledWith('Please enter your current password to save changes');
      expect(api.apiRequest).not.toHaveBeenCalled();
    });

    test('save profile validates new password confirmation', async () => {
      updateUserUI();

      const userEmail = document.getElementById('user-email-display') as HTMLElement;
      userEmail.click();

      await new Promise(resolve => setTimeout(resolve, 10));

      const currentPasswordInput = document.getElementById('profile-current-password') as HTMLInputElement;
      const newPasswordInput = document.getElementById('profile-new-password') as HTMLInputElement;
      const confirmPasswordInput = document.getElementById('profile-confirm-password') as HTMLInputElement;

      currentPasswordInput.value = 'oldpassword';
      newPasswordInput.value = 'NewPassword123!';
      confirmPasswordInput.value = 'DifferentPass1!';

      const form = document.getElementById('profile-form');
      form?.dispatchEvent(new Event('submit', { cancelable: true }));

      await new Promise(resolve => setTimeout(resolve, 10));

      // Password mismatch is surfaced inline (parity with reset / setup
      // flows). `alert` must not fire for this path.
      const errorDiv = document.getElementById('profile-password-error') as HTMLElement;
      expect(errorDiv.textContent).toBe('New passwords do not match');
      expect(errorDiv.classList.contains('hidden')).toBe(false);
      expect(window.alert).not.toHaveBeenCalled();
      expect(api.apiRequest).not.toHaveBeenCalled();
    });

    test('save profile shows inline error when new password fails complexity rules', async () => {
      updateUserUI();

      const userEmail = document.getElementById('user-email-display') as HTMLElement;
      userEmail.click();

      await new Promise(resolve => setTimeout(resolve, 10));

      const currentPasswordInput = document.getElementById('profile-current-password') as HTMLInputElement;
      const newPasswordInput = document.getElementById('profile-new-password') as HTMLInputElement;
      const confirmPasswordInput = document.getElementById('profile-confirm-password') as HTMLInputElement;

      currentPasswordInput.value = 'oldpassword';
      // Too short — fails the length rule first.
      newPasswordInput.value = 'short';
      confirmPasswordInput.value = 'short';

      const form = document.getElementById('profile-form');
      form?.dispatchEvent(new Event('submit', { cancelable: true }));

      await new Promise(resolve => setTimeout(resolve, 10));

      const errorDiv = document.getElementById('profile-password-error') as HTMLElement;
      expect(errorDiv.textContent).toBe('Password must be at least 12 characters long');
      expect(errorDiv.classList.contains('hidden')).toBe(false);
      expect(window.alert).not.toHaveBeenCalled();
      expect(api.apiRequest).not.toHaveBeenCalled();
    });

    test('profile modal renders live password-requirement indicators', async () => {
      updateUserUI();

      const userEmail = document.getElementById('user-email-display') as HTMLElement;
      userEmail.click();

      await new Promise(resolve => setTimeout(resolve, 10));

      // All five indicators must be present and prefixed `profile-req-`
      // so they cannot collide with the reset / setup flows.
      expect(document.getElementById('profile-req-length')).toBeTruthy();
      expect(document.getElementById('profile-req-uppercase')).toBeTruthy();
      expect(document.getElementById('profile-req-lowercase')).toBeTruthy();
      expect(document.getElementById('profile-req-number')).toBeTruthy();
      expect(document.getElementById('profile-req-special')).toBeTruthy();
    });

    test('typing into new password toggles requirement classes live', async () => {
      updateUserUI();

      const userEmail = document.getElementById('user-email-display') as HTMLElement;
      userEmail.click();

      await new Promise(resolve => setTimeout(resolve, 10));

      const newPasswordInput = document.getElementById('profile-new-password') as HTMLInputElement;

      // A fully-valid password should mark every rule as met.
      newPasswordInput.value = 'Abcdefghijk1!';
      newPasswordInput.dispatchEvent(new Event('input'));

      expect(document.getElementById('profile-req-length')?.classList.contains('met')).toBe(true);
      expect(document.getElementById('profile-req-uppercase')?.classList.contains('met')).toBe(true);
      expect(document.getElementById('profile-req-lowercase')?.classList.contains('met')).toBe(true);
      expect(document.getElementById('profile-req-number')?.classList.contains('met')).toBe(true);
      expect(document.getElementById('profile-req-special')?.classList.contains('met')).toBe(true);

      // Drop every rule simultaneously — short, no upper, no number, no
      // special. Lowercase is the only remaining match.
      newPasswordInput.value = 'abc';
      newPasswordInput.dispatchEvent(new Event('input'));

      expect(document.getElementById('profile-req-length')?.classList.contains('unmet')).toBe(true);
      expect(document.getElementById('profile-req-uppercase')?.classList.contains('unmet')).toBe(true);
      expect(document.getElementById('profile-req-lowercase')?.classList.contains('met')).toBe(true);
      expect(document.getElementById('profile-req-number')?.classList.contains('unmet')).toBe(true);
      expect(document.getElementById('profile-req-special')?.classList.contains('unmet')).toBe(true);
    });

    test('reopening profile modal resets stale indicators and error', async () => {
      updateUserUI();

      const userEmail = document.getElementById('user-email-display') as HTMLElement;
      userEmail.click();
      await new Promise(resolve => setTimeout(resolve, 10));

      // Dirty the indicators and the inline error.
      const newPasswordInput = document.getElementById('profile-new-password') as HTMLInputElement;
      newPasswordInput.value = 'Abcdefghijk1!';
      newPasswordInput.dispatchEvent(new Event('input'));
      const errorDiv = document.getElementById('profile-password-error') as HTMLElement;
      errorDiv.textContent = 'old error';
      errorDiv.classList.remove('hidden');

      // Close and re-open.
      const cancelBtn = document.getElementById('profile-cancel');
      cancelBtn?.click();
      userEmail.click();
      await new Promise(resolve => setTimeout(resolve, 10));

      // Re-query after reopen so we assert on the live DOM node, not a
      // potentially stale pre-close reference.
      const reopenedErrorDiv = document.getElementById('profile-password-error') as HTMLElement;

      // Length applies to empty string ("" < 12) so on reset it should
      // be `unmet`, and the inline error must be cleared + hidden.
      // Re-query the error div because the close-and-reopen cycle
      // re-renders the modal; the pre-reopen reference points at the
      // detached element and would silently pass the assertions even
      // if the new modal regressed (CodeRabbit on #470).
      expect(document.getElementById('profile-req-length')?.classList.contains('unmet')).toBe(true);
      expect(document.getElementById('profile-req-uppercase')?.classList.contains('unmet')).toBe(true);
      expect(reopenedErrorDiv.textContent).toBe('');
      expect(reopenedErrorDiv.classList.contains('hidden')).toBe(true);
    });

    test('save profile updates user info on success', async () => {
      (api.apiRequest as jest.Mock).mockResolvedValue({});
      updateUserUI();

      const userEmail = document.getElementById('user-email-display') as HTMLElement;
      userEmail.click();

      await new Promise(resolve => setTimeout(resolve, 10));

      const emailInput = document.getElementById('profile-email') as HTMLInputElement;
      const currentPasswordInput = document.getElementById('profile-current-password') as HTMLInputElement;

      emailInput.value = 'newemail@example.com';
      currentPasswordInput.value = 'oldpassword';

      const form = document.getElementById('profile-form');
      form?.dispatchEvent(new Event('submit', { cancelable: true }));

      await new Promise(resolve => setTimeout(resolve, 100));

      expect(api.apiRequest).toHaveBeenCalledWith('/auth/profile', expect.objectContaining({
        method: 'PUT',
        body: expect.any(String)
      }));
      expect(state.setCurrentUser).toHaveBeenCalledWith(expect.objectContaining({
        email: 'newemail@example.com'
      }));
      expect(window.alert).toHaveBeenCalledWith('Profile updated successfully');
    });

    test('save profile handles API errors', async () => {
      (api.apiRequest as jest.Mock).mockRejectedValue(new Error('Update failed'));
      console.error = jest.fn();
      updateUserUI();

      const userEmail = document.getElementById('user-email-display') as HTMLElement;
      userEmail.click();

      await new Promise(resolve => setTimeout(resolve, 10));

      const emailInput = document.getElementById('profile-email') as HTMLInputElement;
      const currentPasswordInput = document.getElementById('profile-current-password') as HTMLInputElement;

      emailInput.value = 'newemail@example.com';
      currentPasswordInput.value = 'oldpassword';

      const form = document.getElementById('profile-form');
      form?.dispatchEvent(new Event('submit', { cancelable: true }));

      await new Promise(resolve => setTimeout(resolve, 100));

      expect(window.alert).toHaveBeenCalledWith('Failed to update profile: Update failed');
    });

    test('save profile includes new password when provided', async () => {
      (api.apiRequest as jest.Mock).mockResolvedValue({});
      updateUserUI();

      const userEmail = document.getElementById('user-email-display') as HTMLElement;
      userEmail.click();

      await new Promise(resolve => setTimeout(resolve, 10));

      const emailInput = document.getElementById('profile-email') as HTMLInputElement;
      const currentPasswordInput = document.getElementById('profile-current-password') as HTMLInputElement;
      const newPasswordInput = document.getElementById('profile-new-password') as HTMLInputElement;
      const confirmPasswordInput = document.getElementById('profile-confirm-password') as HTMLInputElement;

      emailInput.value = 'test@example.com';
      currentPasswordInput.value = 'oldpassword';
      newPasswordInput.value = 'NewPassword123!';
      confirmPasswordInput.value = 'NewPassword123!';

      const form = document.getElementById('profile-form');
      form?.dispatchEvent(new Event('submit', { cancelable: true }));

      await new Promise(resolve => setTimeout(resolve, 100));

      const callArgs = (api.apiRequest as jest.Mock).mock.calls[0];
      const bodyData = JSON.parse(callArgs[1].body);
      expect(bodyData.new_password).toBeDefined();
    });
  });

  // Issues #460 and #461: branch the reset modal on token status BEFORE
  // rendering the form, so expired / already-used tokens land on a
  // dedicated UX rather than a form that can never submit.
  describe('showResetPasswordModal', () => {
    test('valid + reset flow renders the form with "Reset Your Password" heading', async () => {
      (api.getResetTokenStatus as jest.Mock).mockResolvedValue({ state: 'valid', flow: 'reset' });

      await showResetPasswordModal('valid-token');

      const modal = document.getElementById('reset-password-modal');
      expect(modal).toBeTruthy();
      expect(modal?.textContent).toContain('Reset Your Password');
      expect(document.getElementById('reset-password-form')).toBeTruthy();
      expect(document.getElementById('new-password')).toBeTruthy();
    });

    test('valid + invite flow uses "Set Your Password" wording (issue #461)', async () => {
      (api.getResetTokenStatus as jest.Mock).mockResolvedValue({ state: 'valid', flow: 'invite' });

      await showResetPasswordModal('valid-invite-token');

      const modal = document.getElementById('reset-password-modal');
      expect(modal).toBeTruthy();
      expect(modal?.textContent).toContain('Set Your Password');
      expect(modal?.textContent).not.toContain('Reset Your Password');
      // Form still renders so the user can complete the invite.
      expect(document.getElementById('reset-password-form')).toBeTruthy();
    });

    test('expired token renders the expired view, not the form (issue #460)', async () => {
      (api.getResetTokenStatus as jest.Mock).mockResolvedValue({ state: 'expired', flow: 'reset' });

      await showResetPasswordModal('expired-token');

      const modal = document.getElementById('reset-password-modal');
      expect(modal).toBeTruthy();
      expect(modal?.textContent).toContain('expired');
      // The password-entry form must NOT render.
      expect(document.getElementById('reset-password-form')).toBeNull();
      expect(document.getElementById('new-password')).toBeNull();
      // The CTA to request a new email is present.
      expect(document.getElementById('reset-expired-request-new')).toBeTruthy();
    });

    test('used token renders the used view, not the form (issue #461)', async () => {
      (api.getResetTokenStatus as jest.Mock).mockResolvedValue({ state: 'used', flow: 'reset' });

      await showResetPasswordModal('stale-token');

      const modal = document.getElementById('reset-password-modal');
      expect(modal).toBeTruthy();
      expect(modal?.textContent).toContain('already been used');
      expect(document.getElementById('reset-password-form')).toBeNull();
      expect(document.getElementById('reset-used-go-to-login')).toBeTruthy();
    });

    test('used + invite flow uses invitation wording (issue #461)', async () => {
      (api.getResetTokenStatus as jest.Mock).mockResolvedValue({ state: 'used', flow: 'invite' });

      await showResetPasswordModal('used-invite-token');

      const modal = document.getElementById('reset-password-modal');
      expect(modal?.textContent).toContain('Invitation link already used');
    });

    test('status-check failure falls back to rendering the form', async () => {
      (api.getResetTokenStatus as jest.Mock).mockRejectedValue(new Error('Network error'));

      await showResetPasswordModal('uncertain-token');

      const modal = document.getElementById('reset-password-modal');
      expect(modal).toBeTruthy();
      // Form renders so the user is not stranded on a transient outage.
      expect(document.getElementById('reset-password-form')).toBeTruthy();
      expect(document.getElementById('new-password')).toBeTruthy();
    });

    test('expired view "Send a new reset email" clears the token and routes to login', async () => {
      (api.getResetTokenStatus as jest.Mock).mockResolvedValue({ state: 'expired', flow: 'reset' });

      // Stub window.history.replaceState since jsdom's implementation
      // does not noop on absent listeners.
      const replaceState = jest.fn();
      Object.defineProperty(window, 'history', {
        writable: true,
        value: { replaceState }
      });

      await showResetPasswordModal('expired-token');
      document.getElementById('reset-expired-request-new')?.click();

      await new Promise(resolve => setTimeout(resolve, 20));

      // The reset modal is gone; the login modal is up.
      expect(document.getElementById('reset-password-modal')).toBeNull();
      expect(document.getElementById('login-modal')).toBeTruthy();
      // URL query string is cleaned so a reload does not re-enter the
      // reset flow with the stale token.
      expect(replaceState).toHaveBeenCalled();
    });
  });
});
