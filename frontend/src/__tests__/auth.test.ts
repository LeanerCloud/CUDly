/**
 * Auth module tests
 */
import { showLoginModal, updateUserUI, logout } from '../auth';

// Mock the api module
jest.mock('../api', () => ({
  login: jest.fn(),
  logout: jest.fn(),
  requestPasswordReset: jest.fn(),
  apiRequest: jest.fn()
}));

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

    test('password reset sends request', async () => {
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
      expect(window.alert).toHaveBeenCalledWith('If an account exists with that email, you will receive a password reset link.');
    });

    test('password reset handles empty email', async () => {
      await showLoginModal();

      const forgotLink = document.getElementById('forgot-password-link');
      forgotLink?.dispatchEvent(new MouseEvent('click', { bubbles: true, cancelable: true }));

      await new Promise(resolve => setTimeout(resolve, 10));

      const sendBtn = document.getElementById('send-reset-btn');
      sendBtn?.click();

      await new Promise(resolve => setTimeout(resolve, 10));

      expect(window.alert).toHaveBeenCalledWith('Please enter your email address');
      expect(api.requestPasswordReset).not.toHaveBeenCalled();
    });

    test('password reset handles API error', async () => {
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

      expect(window.alert).toHaveBeenCalledWith('Failed to send reset email. Please try again.');
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
        <div id="user-info" style="display: none;">
          <span id="user-email-display"></span>
          <button id="logout-btn">Logout</button>
        </div>
        <div class="admin-only" style="display: none;">Admin content</div>
      `;
    });

    test('updates user email when user is logged in', () => {
      (state.getCurrentUser as jest.Mock).mockReturnValue({
        id: 'user-1',
        email: 'test@example.com',
        role: 'user'
      });

      updateUserUI();

      const userEmail = document.getElementById('user-email-display');
      expect(userEmail?.textContent).toBe('test@example.com');
    });

    test('shows user info when logged in', () => {
      (state.getCurrentUser as jest.Mock).mockReturnValue({
        id: 'user-1',
        email: 'test@example.com',
        role: 'user'
      });

      updateUserUI();

      const userInfo = document.getElementById('user-info');
      expect((userInfo as HTMLElement).style.display).toBe('flex');
    });

    test('hides user info when not logged in', () => {
      (state.getCurrentUser as jest.Mock).mockReturnValue(null);

      updateUserUI();

      const userInfo = document.getElementById('user-info');
      expect((userInfo as HTMLElement).style.display).toBe('none');
    });

    test('shows admin-only elements for admin users', () => {
      (state.getCurrentUser as jest.Mock).mockReturnValue({
        id: 'admin-1',
        email: 'admin@example.com',
        role: 'admin'
      });

      updateUserUI();

      const adminElements = document.querySelectorAll<HTMLElement>('.admin-only');
      adminElements.forEach(el => {
        expect(el.style.display).toBe('');
      });
    });

    test('hides admin-only elements for regular users', () => {
      (state.getCurrentUser as jest.Mock).mockReturnValue({
        id: 'user-1',
        email: 'user@example.com',
        role: 'user'
      });

      updateUserUI();

      const adminElements = document.querySelectorAll<HTMLElement>('.admin-only');
      adminElements.forEach(el => {
        expect(el.style.display).toBe('none');
      });
    });

    test('makes user email clickable', () => {
      (state.getCurrentUser as jest.Mock).mockReturnValue({
        id: 'user-1',
        email: 'test@example.com',
        role: 'user'
      });

      updateUserUI();

      const userEmail = document.getElementById('user-email-display') as HTMLElement;
      expect(userEmail.style.cursor).toBe('pointer');
      expect(userEmail.title).toBe('Click to edit your profile');
    });

    test('sets up logout button handler', () => {
      (state.getCurrentUser as jest.Mock).mockReturnValue({
        id: 'user-1',
        email: 'test@example.com',
        role: 'user'
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
        role: 'user'
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
      newPasswordInput.value = 'newpassword';
      confirmPasswordInput.value = 'differentpassword';

      const form = document.getElementById('profile-form');
      form?.dispatchEvent(new Event('submit', { cancelable: true }));

      await new Promise(resolve => setTimeout(resolve, 10));

      expect(window.alert).toHaveBeenCalledWith('New passwords do not match');
      expect(api.apiRequest).not.toHaveBeenCalled();
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
      newPasswordInput.value = 'newpassword';
      confirmPasswordInput.value = 'newpassword';

      const form = document.getElementById('profile-form');
      form?.dispatchEvent(new Event('submit', { cancelable: true }));

      await new Promise(resolve => setTimeout(resolve, 100));

      const callArgs = (api.apiRequest as jest.Mock).mock.calls[0];
      const bodyData = JSON.parse(callArgs[1].body);
      expect(bodyData.new_password).toBeDefined();
    });
  });
});
