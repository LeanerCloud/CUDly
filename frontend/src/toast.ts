/**
 * Toast notification system (AutoSpotting-style bottom-right stack).
 *
 * Usage:
 *   import { showToast } from './toast';
 *   showToast({ message: 'Saved', kind: 'success' });
 *   const { dismiss } = showToast({ message: 'Uploading...', timeout: null });
 *   // later:
 *   dismiss();
 *
 * Defaults: 30s timeout across all kinds (matches the user's spec). Pass
 * `timeout: null` for a sticky toast that stays until × clicked, or
 * `timeout: 5000` for short confirms. Toasts stack bottom-up, newest at
 * the bottom.
 *
 * Dismissal: × button click, timeout expiry, or Esc while the toast has
 * focus. The dismiss() return value lets callers clear a toast
 * programmatically (e.g. tie an "uploading..." toast to the completion
 * of the upload).
 */

export type ToastKind = 'success' | 'info' | 'warning' | 'error';

export interface ToastAction {
  label: string;
  onClick: () => void;
}

export interface ToastOptions {
  message: string;
  kind?: ToastKind;
  /** ms; null/0 = sticky. Defaults to 30_000. */
  timeout?: number | null;
  actions?: ToastAction[];
}

export interface ToastHandle {
  dismiss: () => void;
}

const DEFAULT_TIMEOUT_MS = 30_000;
const CONTAINER_ID = 'toast-container';

const KIND_ICONS: Record<ToastKind, string> = {
  success: '\u2713',  // ✓
  info: '\u2139',     // ℹ
  warning: '\u26A0',  // ⚠
  error: '\u2715',    // ✕
};

function ensureContainer(): HTMLElement {
  let container = document.getElementById(CONTAINER_ID);
  if (!container) {
    container = document.createElement('div');
    container.id = CONTAINER_ID;
    container.className = 'toast-container';
    container.setAttribute('aria-live', 'polite');
    container.setAttribute('aria-atomic', 'false');
    document.body.appendChild(container);
  }
  return container;
}

export function showToast(opts: ToastOptions): ToastHandle {
  const kind: ToastKind = opts.kind ?? 'info';
  const timeout = opts.timeout === null || opts.timeout === 0
    ? null
    : (opts.timeout ?? DEFAULT_TIMEOUT_MS);

  const container = ensureContainer();

  const toast = document.createElement('div');
  // Double-class: the new BEM-style `toast--<kind>` for the rewritten
  // styles, plus the legacy `toast-<kind>` that older tests + any
  // lingering stylesheet still target. Both are safe to keep.
  toast.className = `toast toast--${kind} toast-${kind}`;
  toast.setAttribute('role', kind === 'error' ? 'alert' : 'status');
  toast.tabIndex = 0;

  const icon = document.createElement('span');
  icon.className = 'toast-icon';
  icon.setAttribute('aria-hidden', 'true');
  icon.textContent = KIND_ICONS[kind];
  toast.appendChild(icon);

  const body = document.createElement('div');
  body.className = 'toast-message';
  body.textContent = opts.message;
  toast.appendChild(body);

  if (opts.actions && opts.actions.length > 0) {
    const actions = document.createElement('div');
    actions.className = 'toast-actions';
    opts.actions.forEach((a) => {
      const btn = document.createElement('button');
      btn.type = 'button';
      btn.className = 'btn btn-small';
      btn.textContent = a.label;
      btn.addEventListener('click', (e) => {
        e.stopPropagation();
        try {
          a.onClick();
        } finally {
          handle.dismiss();
        }
      });
      actions.appendChild(btn);
    });
    toast.appendChild(actions);
  }

  const closeBtn = document.createElement('button');
  closeBtn.type = 'button';
  closeBtn.className = 'toast-close';
  closeBtn.setAttribute('aria-label', 'Dismiss notification');
  closeBtn.textContent = '\u00D7'; // ×
  closeBtn.addEventListener('click', () => handle.dismiss());
  toast.appendChild(closeBtn);

  let dismissed = false;
  let timeoutId: ReturnType<typeof setTimeout> | null = null;

  const handle: ToastHandle = {
    dismiss: () => {
      if (dismissed) return;
      dismissed = true;
      if (timeoutId !== null) clearTimeout(timeoutId);
      toast.removeEventListener('keydown', onKey);
      toast.classList.add('toast--leaving');
      // Allow CSS transition to play out; fall back to immediate remove
      // if the element has no transition (tests run without computed
      // transitions).
      const remove = (): void => toast.remove();
      toast.addEventListener('transitionend', remove, { once: true });
      setTimeout(remove, 200);
    },
  };

  const onKey = (e: KeyboardEvent): void => {
    if (e.key === 'Escape') {
      e.preventDefault();
      handle.dismiss();
    }
  };
  toast.addEventListener('keydown', onKey);

  container.appendChild(toast);

  if (timeout !== null) {
    timeoutId = setTimeout(() => handle.dismiss(), timeout);
  }

  return handle;
}
