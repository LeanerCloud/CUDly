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

  await fetch(`${API_BASE}/auth/forgot-password`, {
    method: 'POST',
    headers,
    body
  });
}

/**
 * Reset password with token
 */
export async function resetPassword(token: string, newPassword: string): Promise<void> {
  const API_BASE = getApiBase();
  const body = JSON.stringify({ token, new_password: newPassword });
  const headers: Record<string, string> = { 'Content-Type': 'application/json' };
  await addContentHashHeader(headers, body);

  const response = await fetch(`${API_BASE}/auth/reset-password`, {
    method: 'POST',
    headers,
    body
  });

  if (!response.ok) {
    const error = await response.json().catch(() => ({ message: 'Failed to reset password' }));
    throw new Error(error.message || 'Failed to reset password');
  }
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
