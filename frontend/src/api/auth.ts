/**
 * Authentication API functions
 */

import { apiRequest, getAuthHeaders, setAuthToken, setCsrfToken, clearAuth, addContentHashHeader, base64Encode, getApiBase } from './client';
import type { LoginResponse, User, PublicInfo } from './types';

/**
 * Login with email and password
 */
export async function login(email: string, password: string): Promise<LoginResponse> {
  const API_BASE = getApiBase();
  // Base64 encode password to match backend expectation
  const body = JSON.stringify({ email, password: base64Encode(password) });
  const headers: Record<string, string> = { 'Content-Type': 'application/json' };

  // Add content hash for CloudFront OAC
  await addContentHashHeader(headers, body);

  const response = await fetch(`${API_BASE}/auth/login`, {
    method: 'POST',
    headers,
    body
  });

  if (!response.ok) {
    const data = await response.json() as { error?: string };
    throw new Error(data.error || 'Login failed');
  }

  const data = await response.json() as LoginResponse & { csrf_token?: string };
  setAuthToken(data.token);
  // Store CSRF token if provided by server
  if (data.csrf_token) {
    setCsrfToken(data.csrf_token);
  }
  return data;
}

/**
 * Logout the current user
 */
export async function logout(): Promise<void> {
  const API_BASE = getApiBase();
  try {
    const headers = getAuthHeaders();
    // Add empty content hash for POST without body
    await addContentHashHeader(headers, '');

    await fetch(`${API_BASE}/auth/logout`, {
      method: 'POST',
      headers
    });
  } catch (e) {
    // Non-critical: local logout will happen anyway
    console.warn('Server logout failed:', e);
  }
  clearAuth();
}

/**
 * Get current user info
 */
export async function getCurrentUser(): Promise<User> {
  return apiRequest<User>('/auth/me');
}

/**
 * Request password reset
 */
export async function requestPasswordReset(email: string): Promise<void> {
  const API_BASE = getApiBase();
  const body = JSON.stringify({ email });
  const headers: Record<string, string> = { 'Content-Type': 'application/json' };
  await addContentHashHeader(headers, body);

  const response = await fetch(`${API_BASE}/auth/forgot-password`, {
    method: 'POST',
    headers,
    body
  });
  if (!response.ok) {
    const data = await response.json().catch(() => ({})) as { error?: string };
    throw new Error(data.error || 'Failed to send password reset email');
  }
}

/**
 * Reset password with token
 */
export async function resetPassword(token: string, newPassword: string): Promise<void> {
  const API_BASE = getApiBase();
  const body = JSON.stringify({ token, new_password: base64Encode(newPassword) });
  const headers: Record<string, string> = { 'Content-Type': 'application/json' };
  await addContentHashHeader(headers, body);

  const response = await fetch(`${API_BASE}/auth/reset-password`, {
    method: 'POST',
    headers,
    body
  });

  if (!response.ok) {
    // Backend's NewClientError emits {error: "<msg>"} (see internal/api/
    // handler.go writeError). We previously only read `message`, so the
    // specific reason (e.g. "this is your current password, choose a
    // different one") never reached the toast and users saw the opaque
    // fallback. Read `error` first; keep `message` as a defensive
    // fallback for any other shape. See issue #459.
    const data = await response.json().catch(() => ({})) as { error?: string; message?: string };
    const msg = data.error || data.message;
    throw new Error(msg || 'Failed to reset password');
  }
}

/**
 * ResetTokenStatus describes the runtime state of a reset token. The
 * frontend calls getResetTokenStatus() before rendering the reset-
 * password form so it can show an "expired" or "already used" view
 * instead of a form that can never submit (issues #460, #461).
 *
 * state: "valid" | "expired" | "used"  (the server collapses "never
 *        existed" into "used" since the row is wiped on consumption)
 * flow:  "reset" | "invite"             (drives "Reset" vs "Set" copy)
 */
export interface ResetTokenStatus {
  state: 'valid' | 'expired' | 'used';
  flow: 'reset' | 'invite';
}

// Whitelists for runtime validation of the token-status response. Kept
// in sync with the ResetTokenStatus union above — if the union grows,
// these arrays must grow with it (the satisfies clause makes that a
// type-error rather than a silent drift).
const VALID_RESET_TOKEN_STATES = ['valid', 'expired', 'used'] as const satisfies readonly ResetTokenStatus['state'][];
const VALID_RESET_TOKEN_FLOWS  = ['reset', 'invite']           as const satisfies readonly ResetTokenStatus['flow'][];
const VALID_RESET_TOKEN_STATE_SET: ReadonlySet<ResetTokenStatus['state']> = new Set(VALID_RESET_TOKEN_STATES);
const VALID_RESET_TOKEN_FLOW_SET:  ReadonlySet<ResetTokenStatus['flow']>  = new Set(VALID_RESET_TOKEN_FLOWS);

/**
 * Probe a reset token's state before rendering the form. On any
 * network or non-OK response, throws so the caller can fall back to
 * rendering the form (a safer default than hiding the form on a
 * transient failure).
 *
 * The server response is validated at runtime: a malicious or
 * misconfigured server cannot inject arbitrary state/flow values
 * that downstream code (modal-routing, copy selection) is not
 * prepared to handle. Any deviation throws a descriptive error.
 */
export async function getResetTokenStatus(token: string): Promise<ResetTokenStatus> {
  const API_BASE = getApiBase();
  const url = `${API_BASE}/auth/reset-password/status?token=${encodeURIComponent(token)}`;
  const response = await fetch(url, { method: 'GET' });
  if (!response.ok) {
    throw new Error(`reset-password status check failed: ${response.status}`);
  }
  const data = await response.json() as unknown;
  if (data === null || typeof data !== 'object') {
    throw new Error(`reset-password status response was not an object: ${JSON.stringify(data)}`);
  }
  const { state, flow } = data as { state?: unknown; flow?: unknown };
  if (typeof state !== 'string' || !VALID_RESET_TOKEN_STATE_SET.has(state as ResetTokenStatus['state'])) {
    throw new Error(`reset-password status response has invalid state: ${JSON.stringify(state)}`);
  }
  if (typeof flow !== 'string' || !VALID_RESET_TOKEN_FLOW_SET.has(flow as ResetTokenStatus['flow'])) {
    throw new Error(`reset-password status response has invalid flow: ${JSON.stringify(flow)}`);
  }
  return { state: state as ResetTokenStatus['state'], flow: flow as ResetTokenStatus['flow'] };
}

/**
 * Check if admin exists
 */
export async function checkAdminExists(key: string): Promise<boolean> {
  const API_BASE = getApiBase();
  const response = await fetch(`${API_BASE}/auth/check-admin`, {
    headers: { 'X-API-Key': key }
  });
  if (response.ok) {
    const data = await response.json() as { admin_exists: boolean };
    return data.admin_exists;
  }
  return false;
}

/**
 * Setup admin account
 */
export async function setupAdmin(key: string, email: string, password: string): Promise<LoginResponse> {
  const API_BASE = getApiBase();
  // Base64 encode password to match backend expectation
  const body = JSON.stringify({ email, password: base64Encode(password) });
  const headers: Record<string, string> = { 'X-API-Key': key, 'Content-Type': 'application/json' };
  await addContentHashHeader(headers, body);

  const response = await fetch(`${API_BASE}/auth/setup-admin`, {
    method: 'POST',
    headers,
    body
  });

  if (!response.ok) {
    const data = await response.json() as { error?: string };
    throw new Error(data.error || 'Failed to create admin');
  }

  const data = await response.json() as LoginResponse & { csrf_token?: string };
  setAuthToken(data.token);
  // Store CSRF token if provided by server
  if (data.csrf_token) {
    setCsrfToken(data.csrf_token);
  }
  return data;
}

/**
 * Change password
 */
export async function changePassword(currentPassword: string, newPassword: string): Promise<void> {
  // Base64 encode passwords to match backend expectation
  return apiRequest<void>('/auth/change-password', {
    method: 'POST',
    body: JSON.stringify({
      current_password: base64Encode(currentPassword),
      new_password: base64Encode(newPassword)
    })
  });
}

/**
 * Get public info (no auth required)
 */
export async function getPublicInfo(): Promise<PublicInfo> {
  const API_BASE = getApiBase();
  const response = await fetch(`${API_BASE}/info`);
  if (response.ok) {
    return response.json() as Promise<PublicInfo>;
  }
  return { version: '', admin_exists: false };
}
