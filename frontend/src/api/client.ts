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
 * Default request timeout in milliseconds. Generous enough for legit
 * long-running endpoints (AWS Cost Explorer / RI utilization can take
 * 20–40s during cold starts), tight enough that a hung backend
 * surfaces as an error in the UI instead of spinning forever (issue
 * #20). Callers can override via `options.timeoutMs` or by passing
 * their own `signal`.
 */
const DEFAULT_API_TIMEOUT_MS = 90_000;

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

  // Layer a timeout-driven AbortController on top of any caller-
  // provided signal so both paths can cancel the request. If the
  // caller passes their own signal we still add a timeout (unless
  // they opt out by passing timeoutMs: 0) — the UI doesn't want an
  // indefinite hang regardless of who owns cancellation.
  const timeoutMs = options.timeoutMs ?? DEFAULT_API_TIMEOUT_MS;
  const timeoutController = new AbortController();
  const timeoutHandle = timeoutMs > 0
    ? setTimeout(() => timeoutController.abort(), timeoutMs)
    : undefined;
  const signal = options.signal
    ? anySignal([options.signal, timeoutController.signal])
    : timeoutController.signal;

  let response: Response;
  try {
    response = await fetch(url, {
      ...options,
      headers,
      signal,
    });
  } catch (err) {
    if (timeoutController.signal.aborted) {
      const error: ApiError = new Error(
        `Request to ${endpoint} timed out after ${timeoutMs}ms`,
      );
      error.status = 0;
      throw error;
    }
    throw err;
  } finally {
    if (timeoutHandle !== undefined) clearTimeout(timeoutHandle);
  }

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

  // Q3: handlers that intentionally produce no body (e.g. DELETE
  // /accounts/:id) used to crash the caller with SyntaxError from
  // response.json() on an empty body. Q1 fixed this at the backend by
  // emitting "{}" instead, but keep a defensive catch here for any
  // upstream proxy that strips the body, 204 No Content responses, or
  // future handlers that might miss the convention.
  try {
    return await response.json() as T;
  } catch {
    return null as T;
  }
}

/**
 * Combine multiple AbortSignals into one. AbortSignal.any is available
 * in modern browsers (Chrome 116+, Firefox 124+, Safari 17.4+); fall
 * back to a manual listener for older targets.
 */
function anySignal(signals: AbortSignal[]): AbortSignal {
  const AnyFn = (AbortSignal as unknown as {
    any?: (s: AbortSignal[]) => AbortSignal;
  }).any;
  if (typeof AnyFn === 'function') {
    return AnyFn.call(AbortSignal, signals);
  }
  const controller = new AbortController();
  for (const s of signals) {
    if (s.aborted) {
      controller.abort(s.reason);
      return controller.signal;
    }
    s.addEventListener('abort', () => controller.abort(s.reason), { once: true });
  }
  return controller.signal;
}

/**
 * Get the API base URL
 */
export function getApiBase(): string {
  return API_BASE;
}
