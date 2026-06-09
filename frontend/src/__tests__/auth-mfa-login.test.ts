/**
 * Two-step login MFA flow tests (issue #497).
 *
 * Exercises the handleLogin path: a fresh login with no MFA code
 * succeeds normally, a login that receives `mfa_required` swaps the
 * form to the code prompt, and the code-prompt submit either logs
 * the user in or surfaces `invalid_mfa_code` without resetting the
 * step.
 */
import { showLoginModal } from '../auth';

jest.mock('../api', () => {
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

jest.mock('../state', () => ({
  getCurrentUser: jest.fn(),
  setCurrentUser: jest.fn(),
}));

import * as api from '../api';

// Each test gets a strictly-increasing time so the login-rate-limit
// cooldown never blocks an attempt across tests.
let mockTime = 1000000000000;
beforeEach(() => {
  document.body.innerHTML = '';
  jest.clearAllMocks();
  mockTime += 10000;
  jest.spyOn(Date, 'now').mockReturnValue(mockTime);
});
afterEach(() => {
  jest.restoreAllMocks();
});

// Suppress jsdom "Not implemented: navigation" noise.
Object.defineProperty(window, 'location', {
  writable: true,
  value: { reload: jest.fn() },
});

async function submitLogin(email: string, password: string): Promise<void> {
  (document.getElementById('login-email') as HTMLInputElement).value = email;
  (document.getElementById('login-password') as HTMLInputElement).value = password;
  document.getElementById('login-form')!.dispatchEvent(new Event('submit'));
  // Let the promise chain inside handleLogin run.
  await new Promise((r) => setTimeout(r, 0));
}

async function submitMFACode(code: string): Promise<void> {
  (document.getElementById('mfa-code') as HTMLInputElement).value = code;
  document.getElementById('login-form')!.dispatchEvent(new Event('submit'));
  await new Promise((r) => setTimeout(r, 0));
}

describe('Login two-step MFA flow', () => {
  test('successful login with no MFA reloads the page', async () => {
    (api.login as jest.Mock).mockResolvedValue({ token: 'tok' });
    await showLoginModal();
    await submitLogin('user@x.com', 'pw');
    expect(api.login).toHaveBeenCalledWith('user@x.com', 'pw');
    expect(window.location.reload).toHaveBeenCalled();
  });

  test('mfa_required swaps to code prompt and keeps modal open', async () => {
    const MFALoginErrorCtor = (api as unknown as { MFALoginError: typeof Error }).MFALoginError as new (c: string) => Error;
    (api.login as jest.Mock).mockRejectedValueOnce(new MFALoginErrorCtor('mfa_required'));
    await showLoginModal();
    await submitLogin('user@x.com', 'pw');
    expect(document.getElementById('mfa-code')).not.toBeNull();
    expect(document.getElementById('login-email')).toBeNull(); // first step gone
    expect(window.location.reload).not.toHaveBeenCalled();
  });

  test('correct MFA code logs in', async () => {
    const MFALoginErrorCtor = (api as unknown as { MFALoginError: typeof Error }).MFALoginError as new (c: string) => Error;
    (api.login as jest.Mock)
      .mockRejectedValueOnce(new MFALoginErrorCtor('mfa_required'))
      .mockResolvedValueOnce({ token: 'tok' });
    await showLoginModal();
    await submitLogin('user@x.com', 'pw');
    await submitMFACode('123456');
    // Verify the second call carried the code.
    expect(api.login).toHaveBeenNthCalledWith(2, 'user@x.com', 'pw', '123456');
    expect(window.location.reload).toHaveBeenCalled();
  });

  test('wrong MFA code stays on the MFA step and shows specific error', async () => {
    const MFALoginErrorCtor = (api as unknown as { MFALoginError: typeof Error }).MFALoginError as new (c: string) => Error;
    (api.login as jest.Mock)
      .mockRejectedValueOnce(new MFALoginErrorCtor('mfa_required'))
      .mockRejectedValueOnce(new MFALoginErrorCtor('invalid_mfa_code'));
    await showLoginModal();
    await submitLogin('user@x.com', 'pw');
    await submitMFACode('000000');
    // Still on the MFA step.
    expect(document.getElementById('mfa-code')).not.toBeNull();
    const err = document.getElementById('login-error');
    expect(err?.classList.contains('hidden')).toBe(false);
    expect(err?.textContent).toMatch(/incorrect/i);
    expect(window.location.reload).not.toHaveBeenCalled();
  });

  test('Back to login link clears the closure and re-opens the email/password step', async () => {
    const MFALoginErrorCtor = (api as unknown as { MFALoginError: typeof Error }).MFALoginError as new (c: string) => Error;
    (api.login as jest.Mock).mockRejectedValueOnce(new MFALoginErrorCtor('mfa_required'));
    await showLoginModal();
    await submitLogin('user@x.com', 'pw');
    document.getElementById('mfa-cancel-link')!.dispatchEvent(new MouseEvent('click', { bubbles: true, cancelable: true }));
    // Give the void showLoginModal call a tick.
    await new Promise((r) => setTimeout(r, 0));
    expect(document.getElementById('login-email')).not.toBeNull();
    expect(document.getElementById('mfa-code')).toBeNull();
  });
});
