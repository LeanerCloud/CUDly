/**
 * HTTP client and authentication state for CUDly API
 */

import type { ApiError, RequestOptions } from './types';

const API_BASE = '/api';

// State for authentication
// SECURITY: Tokens are stored in localStorage scoped to the dashboard origin
// (issue #462). localStorage was chosen over sessionStorage so a second tab
// opened on the same origin inherits the active session; admins need to
// compare Settings vs. Opportunities side by side without re-authenticating.
// Tradeoff: the XSS exposure window widens from "tab close" to "explicit
// logout". Mitigations:
//   - cross-tab logout via the `storage` event (signing out in one tab
//     clears state and reloads the others within ~1s).
//   - same-origin only; no third-party iframes have access.
//   - HttpOnly session cookies remain the cleaner long-term option (see
//     issue #462 discussion); that is a larger refactor and is tracked
//     separately.
let authToken = '';
let apiKey = '';
let csrfToken = '';

// Keys we mirror to/from localStorage. Listed once so the storage-event
// listener and the migration block stay in sync.
const STORAGE_KEYS = ['authToken', 'apiKey', 'csrfToken'] as const;

let storageListenerInstalled = false;

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
 * Initialize authentication from localStorage.
 *
 * Issue #462: tokens were previously kept in sessionStorage so a fresh
 * tab on the same origin would force a re-login. We moved them to
 * localStorage to support multi-tab workflows. See the SECURITY comment
 * at the top of the module for the tradeoff and mitigations.
 *
 * Migration: any token still living in sessionStorage from the previous
 * release is copied into localStorage and then removed, so users stay
 * signed in across the upgrade.
 */
export function initAuth(): void {
  // Migrate sessionStorage → localStorage for users upgrading from the
  // pre-#462 build. Removes the sessionStorage entry afterwards.
  for (const key of STORAGE_KEYS) {
    const fromSession = sessionStorage.getItem(key);
    if (fromSession && !localStorage.getItem(key)) {
      localStorage.setItem(key, fromSession);
    }
    if (fromSession) {
      sessionStorage.removeItem(key);
    }
  }

  authToken = localStorage.getItem('authToken') || '';
  apiKey = localStorage.getItem('apiKey') || '';
  csrfToken = localStorage.getItem('csrfToken') || '';

  installStorageListener();
}

/**
 * Sync sign-out across tabs (issue #462). When another tab clears the
 * auth keys (logout), reset our in-memory state and reload so the UI
 * lands on the login modal instead of leaving a stale logged-in view.
 * Idempotent; only registers once per page load.
 */
function installStorageListener(): void {
  if (storageListenerInstalled) return;
  if (typeof window === 'undefined' || !window.addEventListener) return;
  window.addEventListener('storage', (e: StorageEvent) => {
    if (!e.key || !(STORAGE_KEYS as readonly string[]).includes(e.key)) return;
    // Only react to a *cleared* key (newValue === null); partial
    // updates (e.g. token refresh) propagate via the in-memory state
    // and don't need a reload.
    if (e.newValue !== null) return;
    authToken = '';
    apiKey = '';
    csrfToken = '';
    if (typeof window.location?.reload === 'function') {
      window.location.reload();
    }
  });
  storageListenerInstalled = true;
}

/**
 * Set authentication token
 */
export function setAuthToken(token: string): void {
  authToken = token;
  if (token) {
    localStorage.setItem('authToken', token);
  } else {
    localStorage.removeItem('authToken');
  }
}

/**
 * Set CSRF token
 */
export function setCsrfToken(token: string): void {
  csrfToken = token;
  if (token) {
    localStorage.setItem('csrfToken', token);
  } else {
    localStorage.removeItem('csrfToken');
  }
}

/**
 * Set API key
 */
export function setApiKey(key: string): void {
  apiKey = key;
  if (key) {
    localStorage.setItem('apiKey', key);
  } else {
    localStorage.removeItem('apiKey');
  }
}

/**
 * Check if user is authenticated
 */
export function isAuthenticated(): boolean {
  return !!(authToken || apiKey);
}

/**
 * Clear all authentication. Removing from localStorage triggers the
 * `storage` event in any other open tab on the same origin, which is
 * how cross-tab sign-out propagates (see installStorageListener).
 */
export function clearAuth(): void {
  authToken = '';
  apiKey = '';
  csrfToken = '';
  for (const key of STORAGE_KEYS) {
    localStorage.removeItem(key);
    // Also clear any leftover sessionStorage entries from older builds.
    sessionStorage.removeItem(key);
  }
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
      const data = await response.json() as Record<string, unknown>;
      const message = typeof data['error'] === 'string' ? data['error'] : '';
      if (message) error.message = message;
      // Strip the `error` key and surface the rest as structured
      // details so callers can branch on ops_hint / retry_attempt_n /
      // threshold / retry_execution_id without substring-matching the
      // human message (see PurchaseHistory Retry button — issue #47).
      const details: Record<string, unknown> = {};
      for (const k of Object.keys(data)) {
        if (k === 'error') continue;
        details[k] = data[k];
      }
      if (Object.keys(details).length > 0) {
        error.details = details;
      }
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
