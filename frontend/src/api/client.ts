/**
 * HTTP client and authentication state for CUDly API
 */

import type { ApiError, RequestOptions } from './types';

const API_BASE = '/api';

// State for authentication
// SECURITY: Tokens are stored in sessionStorage (not localStorage) to reduce XSS risk
// sessionStorage is cleared when the browser tab closes, limiting exposure window
let authToken = '';
let apiKey = '';
let csrfToken = '';

/**
 * Calculate SHA256 hash of a string using Web Crypto API
 * Required for CloudFront OAC with Lambda Function URL for POST/PUT/PATCH requests
 */
async function sha256(message: string): Promise<string> {
  const msgBuffer = new TextEncoder().encode(message);
  const hashBuffer = await crypto.subtle.digest('SHA-256', msgBuffer);
  const hashArray = Array.from(new Uint8Array(hashBuffer));
  return hashArray.map(b => b.toString(16).padStart(2, '0')).join('');
}

/**
 * Add x-amz-content-sha256 header for requests with body (required for CloudFront OAC)
 */
export async function addContentHashHeader(headers: Record<string, string>, body: string): Promise<void> {
  // crypto.subtle is only available in secure contexts (HTTPS).
  // Skip on plain HTTP (e.g. ALB without TLS) — the header is only required for CloudFront OAC.
  if (!crypto?.subtle) return;
  headers['x-amz-content-sha256'] = await sha256(body);
}

/**
 * Initialize authentication from sessionStorage
 * SECURITY: Using sessionStorage instead of localStorage reduces XSS exposure
 * - sessionStorage is cleared when the tab/window closes
 * - Data is not shared between tabs (isolates sessions)
 * Migration: Will attempt localStorage first for backward compatibility, then clear it
 */
export function initAuth(): void {
  // Migrate from localStorage to sessionStorage if needed (backward compatibility)
  const localToken = localStorage.getItem('authToken');
  const localApiKey = localStorage.getItem('apiKey');

  if (localToken || localApiKey) {
    // Migrate to sessionStorage
    if (localToken) {
      sessionStorage.setItem('authToken', localToken);
      localStorage.removeItem('authToken');
    }
    if (localApiKey) {
      sessionStorage.setItem('apiKey', localApiKey);
      localStorage.removeItem('apiKey');
    }
  }

  authToken = sessionStorage.getItem('authToken') || '';
  apiKey = sessionStorage.getItem('apiKey') || '';
  csrfToken = sessionStorage.getItem('csrfToken') || '';
}

/**
 * Set authentication token
 */
export function setAuthToken(token: string): void {
  authToken = token;
  if (token) {
    sessionStorage.setItem('authToken', token);
  } else {
    sessionStorage.removeItem('authToken');
  }
}

/**
 * Set CSRF token
 */
export function setCsrfToken(token: string): void {
  csrfToken = token;
  if (token) {
    sessionStorage.setItem('csrfToken', token);
  } else {
    sessionStorage.removeItem('csrfToken');
  }
}

/**
 * Set API key
 */
export function setApiKey(key: string): void {
  apiKey = key;
  if (key) {
    sessionStorage.setItem('apiKey', key);
  } else {
    sessionStorage.removeItem('apiKey');
  }
}

/**
 * Check if user is authenticated
 */
export function isAuthenticated(): boolean {
  return !!(authToken || apiKey);
}

/**
 * Clear all authentication
 */
export function clearAuth(): void {
  authToken = '';
  apiKey = '';
  csrfToken = '';
  sessionStorage.removeItem('authToken');
  sessionStorage.removeItem('apiKey');
  sessionStorage.removeItem('csrfToken');
  // Also clear any legacy localStorage items
  localStorage.removeItem('authToken');
  localStorage.removeItem('apiKey');
}

/**
 * Get auth headers for API requests
 * Note: Uses X-Authorization instead of Authorization because CloudFront OAC
 * signs requests with SigV4, which overwrites the Authorization header.
 */
export function getAuthHeaders(): Record<string, string> {
  const headers: Record<string, string> = { 'Content-Type': 'application/json' };
  if (authToken) {
    headers['X-Authorization'] = `Bearer ${authToken}`;
  } else if (apiKey) {
    headers['X-API-Key'] = apiKey;
  }
  // Include CSRF token for state-changing requests (added by caller as needed)
  if (csrfToken) {
    headers['X-CSRF-Token'] = csrfToken;
  }
  return headers;
}

/**
 * Base64 encode a string (for password encoding)
 */
export function base64Encode(str: string): string {
  return btoa(str);
}

/**
 * Make an authenticated API request
 */
export async function apiRequest<T>(endpoint: string, options: RequestOptions = {}): Promise<T> {
  const url = `${API_BASE}${endpoint}`;
  const headers = { ...getAuthHeaders(), ...options.headers };

  // Add content hash for CloudFront OAC (required for all POST/PUT/PATCH/DELETE requests)
  const method = options.method?.toUpperCase();
  if (method === 'POST' || method === 'PUT' || method === 'PATCH' || method === 'DELETE') {
    const body = typeof options.body === 'string' ? options.body : '';
    await addContentHashHeader(headers, body);
  }

  const response = await fetch(url, {
    ...options,
    headers
  });

  if (!response.ok) {
    const error: ApiError = new Error(`HTTP ${response.status}`);
    error.status = response.status;
    try {
      const data = await response.json() as { error?: string };
      error.message = data.error || error.message;
    } catch {
      // Ignore JSON parse errors
    }
    throw error;
  }

  return response.json() as Promise<T>;
}

/**
 * Get the API base URL
 */
export function getApiBase(): string {
  return API_BASE;
}
