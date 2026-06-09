/**
 * API client tests for the MFA endpoints + the login MFALoginError
 * branch (issue #497).
 */
import { login, MFALoginError, setupMFA, enableMFA, disableMFA, regenerateMFARecoveryCodes } from '../api/auth';

beforeEach(() => {
  global.fetch = jest.fn();
  // Stub the session token so apiRequest's auth header is harmless.
  localStorage.setItem('auth_token', 'tok');
});
afterEach(() => {
  jest.restoreAllMocks();
  localStorage.clear();
});

function mockFetchOk(body: unknown): void {
  (global.fetch as jest.Mock).mockResolvedValue({
    ok: true,
    status: 200,
    json: async () => body,
  });
}
function mockFetchErr(status: number, body: unknown): void {
  (global.fetch as jest.Mock).mockResolvedValue({
    ok: false,
    status,
    json: async () => body,
  });
}

describe('login() MFA error branch', () => {
  test('mfa_required throws typed MFALoginError', async () => {
    mockFetchErr(401, { error: 'mfa_required' });
    await expect(login('u@x.com', 'pw')).rejects.toBeInstanceOf(MFALoginError);
    try {
      await login('u@x.com', 'pw');
    } catch (err) {
      expect(err).toBeInstanceOf(MFALoginError);
      expect((err as MFALoginError).code).toBe('mfa_required');
    }
  });

  test('invalid_mfa_code throws typed MFALoginError', async () => {
    mockFetchErr(401, { error: 'invalid_mfa_code' });
    try {
      await login('u@x.com', 'pw', '000000');
      throw new Error('should have thrown');
    } catch (err) {
      expect(err).toBeInstanceOf(MFALoginError);
      expect((err as MFALoginError).code).toBe('invalid_mfa_code');
    }
  });

  test('non-MFA login error throws plain Error', async () => {
    const errMsg = 'Check your email address and password and try again';
    mockFetchErr(401, { error: errMsg });
    await expect(login('u@x.com', 'pw')).rejects.toThrow(errMsg);
    await expect(login('u@x.com', 'pw')).rejects.not.toBeInstanceOf(MFALoginError);
  });

  test('mfaCode argument is sent on resubmit', async () => {
    mockFetchOk({ token: 'tok', csrf_token: 'csrf' });
    await login('u@x.com', 'pw', '123456');
    const call = (global.fetch as jest.Mock).mock.calls[0];
    const body = JSON.parse(call[1].body) as { email: string; password: string; mfa_code?: string };
    expect(body.email).toBe('u@x.com');
    expect(body.mfa_code).toBe('123456');
  });

  test('mfaCode omitted when undefined (initial submit)', async () => {
    mockFetchOk({ token: 'tok' });
    await login('u@x.com', 'pw');
    const call = (global.fetch as jest.Mock).mock.calls[0];
    const body = JSON.parse(call[1].body) as { mfa_code?: string };
    expect(body.mfa_code).toBeUndefined();
  });
});

describe('setupMFA()', () => {
  test('returns secret + provisioning URI on success', async () => {
    mockFetchOk({ secret: 'SECRET', provisioning_uri: 'otpauth://totp/CUDly:x?secret=SECRET' });
    const res = await setupMFA('pw');
    expect(res.secret).toBe('SECRET');
    expect(res.provisioning_uri).toMatch(/^otpauth:\/\//);
  });

  test('rejects malformed provisioning URI', async () => {
    mockFetchOk({ secret: 'S', provisioning_uri: 'not-a-uri' });
    await expect(setupMFA('pw')).rejects.toThrow(/provisioning_uri/);
  });

  test('rejects empty secret', async () => {
    mockFetchOk({ secret: '', provisioning_uri: 'otpauth://x' });
    await expect(setupMFA('pw')).rejects.toThrow(/secret/);
  });
});

describe('enableMFA()', () => {
  test('returns recovery codes array', async () => {
    mockFetchOk({ recovery_codes: ['AAAA', 'BBBB'] });
    const res = await enableMFA('123456');
    expect(res.recovery_codes).toEqual(['AAAA', 'BBBB']);
  });

  test('rejects when recovery_codes is missing or non-string', async () => {
    mockFetchOk({});
    await expect(enableMFA('123456')).rejects.toThrow(/recovery_codes/);
    mockFetchOk({ recovery_codes: [1, 2] });
    await expect(enableMFA('123456')).rejects.toThrow(/recovery_codes/);
  });
});

describe('disableMFA()', () => {
  test('sends base64-encoded password and code', async () => {
    mockFetchOk({ status: 'mfa disabled' });
    await disableMFA('pw', '123456');
    const call = (global.fetch as jest.Mock).mock.calls[0];
    const body = JSON.parse(call[1].body) as { password: string; code: string };
    expect(body.password).toBe(btoa('pw'));
    expect(body.code).toBe('123456');
  });
});

describe('regenerateMFARecoveryCodes()', () => {
  test('returns fresh codes', async () => {
    mockFetchOk({ recovery_codes: ['NEW1', 'NEW2'] });
    const res = await regenerateMFARecoveryCodes('123456');
    expect(res.recovery_codes).toEqual(['NEW1', 'NEW2']);
  });
});
