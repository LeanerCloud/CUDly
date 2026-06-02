/**
 * MFA enrollment / disable / regenerate-recovery-codes flows in the
 * profile modal (issue #497). Exercises the section render state
 * machine: disabled state → enroll start → QR step → verify → codes
 * step → back to enabled state.
 */

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

// Stub the qrcode dep so the test doesn't pull canvas into jsdom.
jest.mock('qrcode', () => ({
  toDataURL: jest.fn().mockResolvedValue('data:image/png;base64,FAKE'),
}));

import * as api from '../api';
import * as state from '../state';

// Re-render the profile modal between tests. openProfileModal is
// not exported (it's wired through an internal link click), so we
// trigger it indirectly by clicking the user-email element after
// updateUserUI mounts it.
import { updateUserUI } from '../auth';

beforeEach(() => {
  document.body.innerHTML = `
    <span id="user-info" class="hidden">
      <span id="user-email-display"></span>
      <span id="user-role-display"></span>
    </span>
    <button id="logout-btn"></button>
  `;
  jest.clearAllMocks();
  (state.getCurrentUser as jest.Mock).mockReturnValue({
    id: 'u1', email: 'user@x.com', groups: [], mfa_enabled: false,
  });
  updateUserUI();
});

async function openProfile(): Promise<void> {
  // updateUserUI wires a click on #user-email-display (cloned to
  // strip prior listeners) to open the profile modal. Re-query
  // because the listener was attached to the post-clone element.
  document.getElementById('user-email-display')!.dispatchEvent(
    new MouseEvent('click', { bubbles: true })
  );
  await new Promise((r) => setTimeout(r, 0));
}

describe('MFA enrollment flow', () => {
  test('disabled state shows the Set-up button', async () => {
    await openProfile();
    expect(document.getElementById('mfa-enable-btn')).not.toBeNull();
    expect(document.getElementById('mfa-disable-btn')).toBeNull();
  });

  test('enabled state shows Disable and Regenerate buttons', async () => {
    (state.getCurrentUser as jest.Mock).mockReturnValue({
      id: 'u1', email: 'user@x.com', groups: [], mfa_enabled: true,
    });
    updateUserUI();
    await openProfile();
    expect(document.getElementById('mfa-enable-btn')).toBeNull();
    expect(document.getElementById('mfa-disable-btn')).not.toBeNull();
    expect(document.getElementById('mfa-regenerate-btn')).not.toBeNull();
  });

  test('Set-up → password step → setupMFA called', async () => {
    (api.setupMFA as jest.Mock).mockResolvedValue({
      secret: 'JBSWY3DPEHPK3PXP',
      provisioning_uri: 'otpauth://totp/CUDly:user@x.com?secret=JBSWY3DPEHPK3PXP',
    });
    await openProfile();
    document.getElementById('mfa-enable-btn')!.dispatchEvent(new MouseEvent('click', { bubbles: true }));
    expect(document.getElementById('mfa-enroll-password')).not.toBeNull();
    (document.getElementById('mfa-enroll-password') as HTMLInputElement).value = 'pw';
    document.getElementById('mfa-enroll-continue')!.dispatchEvent(new MouseEvent('click', { bubbles: true }));
    await new Promise((r) => setTimeout(r, 10));
    expect(api.setupMFA).toHaveBeenCalledWith('pw');
    expect(document.getElementById('mfa-qr')).not.toBeNull();
    expect(document.getElementById('mfa-secret-display')?.textContent).toMatch(/JBSW/);
  });

  test('verify code → enableMFA called → codes displayed', async () => {
    (api.setupMFA as jest.Mock).mockResolvedValue({
      secret: 'JBSWY3DPEHPK3PXP',
      provisioning_uri: 'otpauth://totp/CUDly:user@x.com?secret=JBSWY3DPEHPK3PXP',
    });
    (api.enableMFA as jest.Mock).mockResolvedValue({
      recovery_codes: ['AAAA-BBBB', 'CCCC-DDDD'],
    });
    await openProfile();
    document.getElementById('mfa-enable-btn')!.dispatchEvent(new MouseEvent('click', { bubbles: true }));
    (document.getElementById('mfa-enroll-password') as HTMLInputElement).value = 'pw';
    document.getElementById('mfa-enroll-continue')!.dispatchEvent(new MouseEvent('click', { bubbles: true }));
    await new Promise((r) => setTimeout(r, 10));
    (document.getElementById('mfa-enroll-code') as HTMLInputElement).value = '123456';
    document.getElementById('mfa-qr-verify')!.dispatchEvent(new MouseEvent('click', { bubbles: true }));
    await new Promise((r) => setTimeout(r, 10));
    expect(api.enableMFA).toHaveBeenCalledWith('123456');
    expect(document.body.textContent).toContain('AAAA-BBBB');
    expect(document.body.textContent).toContain('CCCC-DDDD');
  });

  test('wrong code surfaces the error and stays on the QR step', async () => {
    (api.setupMFA as jest.Mock).mockResolvedValue({
      secret: 'S', provisioning_uri: 'otpauth://totp/CUDly:x?secret=S',
    });
    (api.enableMFA as jest.Mock).mockRejectedValue(new Error('invalid MFA code'));
    await openProfile();
    document.getElementById('mfa-enable-btn')!.dispatchEvent(new MouseEvent('click', { bubbles: true }));
    (document.getElementById('mfa-enroll-password') as HTMLInputElement).value = 'pw';
    document.getElementById('mfa-enroll-continue')!.dispatchEvent(new MouseEvent('click', { bubbles: true }));
    await new Promise((r) => setTimeout(r, 10));
    (document.getElementById('mfa-enroll-code') as HTMLInputElement).value = '000000';
    document.getElementById('mfa-qr-verify')!.dispatchEvent(new MouseEvent('click', { bubbles: true }));
    await new Promise((r) => setTimeout(r, 10));
    const err = document.getElementById('mfa-flow-error');
    expect(err?.classList.contains('hidden')).toBe(false);
    // Still on the QR step (#mfa-qr-verify still present).
    expect(document.getElementById('mfa-qr-verify')).not.toBeNull();
  });
});

describe('MFA disable flow', () => {
  beforeEach(() => {
    (state.getCurrentUser as jest.Mock).mockReturnValue({
      id: 'u1', email: 'user@x.com', groups: [], mfa_enabled: true,
    });
    updateUserUI();
  });

  test('happy path calls disableMFA with password + code', async () => {
    (api.disableMFA as jest.Mock).mockResolvedValue(undefined);
    await openProfile();
    document.getElementById('mfa-disable-btn')!.dispatchEvent(new MouseEvent('click', { bubbles: true }));
    (document.getElementById('mfa-disable-password') as HTMLInputElement).value = 'pw';
    (document.getElementById('mfa-disable-code') as HTMLInputElement).value = '123456';
    document.getElementById('mfa-disable-submit')!.dispatchEvent(new MouseEvent('click', { bubbles: true }));
    await new Promise((r) => setTimeout(r, 10));
    expect(api.disableMFA).toHaveBeenCalledWith('pw', '123456');
  });

  test('empty password blocked client-side without calling backend', async () => {
    await openProfile();
    document.getElementById('mfa-disable-btn')!.dispatchEvent(new MouseEvent('click', { bubbles: true }));
    (document.getElementById('mfa-disable-code') as HTMLInputElement).value = '123456';
    document.getElementById('mfa-disable-submit')!.dispatchEvent(new MouseEvent('click', { bubbles: true }));
    await new Promise((r) => setTimeout(r, 10));
    expect(api.disableMFA).not.toHaveBeenCalled();
    expect(document.getElementById('mfa-flow-error')?.textContent).toMatch(/password/i);
  });
});

describe('MFA regenerate-recovery-codes flow', () => {
  beforeEach(() => {
    (state.getCurrentUser as jest.Mock).mockReturnValue({
      id: 'u1', email: 'user@x.com', groups: [], mfa_enabled: true,
    });
    updateUserUI();
  });

  test('happy path surfaces new plaintext codes', async () => {
    (api.regenerateMFARecoveryCodes as jest.Mock).mockResolvedValue({
      recovery_codes: ['NEW1-XXXX'],
    });
    await openProfile();
    document.getElementById('mfa-regenerate-btn')!.dispatchEvent(new MouseEvent('click', { bubbles: true }));
    (document.getElementById('mfa-regen-code') as HTMLInputElement).value = '123456';
    document.getElementById('mfa-regen-submit')!.dispatchEvent(new MouseEvent('click', { bubbles: true }));
    await new Promise((r) => setTimeout(r, 10));
    expect(api.regenerateMFARecoveryCodes).toHaveBeenCalledWith('123456');
    expect(document.body.textContent).toContain('NEW1-XXXX');
  });
});
