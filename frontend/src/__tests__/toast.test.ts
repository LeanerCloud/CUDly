/**
 * Tests for the shared toast notification system.
 */

import { showToast } from '../toast';

describe('showToast', () => {
  beforeEach(() => {
    jest.useFakeTimers();
  });

  afterEach(() => {
    jest.useRealTimers();
    const body = document.body;
    while (body.firstChild) body.removeChild(body.firstChild);
  });

  it('mounts a toast into a lazily-created container', () => {
    showToast({ message: 'Hello' });
    const container = document.getElementById('toast-container');
    expect(container).not.toBeNull();
    expect(container?.querySelectorAll('.toast').length).toBe(1);
  });

  it('maps kind to a --kind class', () => {
    showToast({ message: 'S', kind: 'success' });
    showToast({ message: 'I', kind: 'info' });
    showToast({ message: 'W', kind: 'warning' });
    showToast({ message: 'E', kind: 'error' });
    expect(document.querySelector('.toast.toast--success')).not.toBeNull();
    expect(document.querySelector('.toast.toast--info')).not.toBeNull();
    expect(document.querySelector('.toast.toast--warning')).not.toBeNull();
    expect(document.querySelector('.toast.toast--error')).not.toBeNull();
  });

  it('defaults to 30s timeout and removes the toast when it expires', () => {
    showToast({ message: 'bye' });
    expect(document.querySelector('.toast')).not.toBeNull();
    jest.advanceTimersByTime(29_999);
    expect(document.querySelector('.toast')).not.toBeNull();
    jest.advanceTimersByTime(2);
    // After timeout, leaving class is applied; the inline setTimeout(200)
    // fallback runs next to actually remove the node.
    jest.advanceTimersByTime(200);
    expect(document.querySelector('.toast')).toBeNull();
  });

  it('sticky mode (timeout: null) does not auto-dismiss', () => {
    showToast({ message: 'stay', timeout: null });
    jest.advanceTimersByTime(60_000);
    expect(document.querySelector('.toast')).not.toBeNull();
  });

  it('× button dismisses the toast', () => {
    showToast({ message: 'close me' });
    const closeBtn = document.querySelector<HTMLButtonElement>('.toast-close');
    expect(closeBtn).not.toBeNull();
    closeBtn!.click();
    jest.advanceTimersByTime(200);
    expect(document.querySelector('.toast')).toBeNull();
  });

  it('Esc while toast has focus dismisses it', () => {
    showToast({ message: 'esc me' });
    const toast = document.querySelector<HTMLDivElement>('.toast')!;
    toast.focus();
    toast.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }));
    jest.advanceTimersByTime(200);
    expect(document.querySelector('.toast')).toBeNull();
  });

  it('stacks multiple toasts in the container', () => {
    showToast({ message: 'one' });
    showToast({ message: 'two' });
    showToast({ message: 'three' });
    const toasts = document.querySelectorAll('.toast');
    expect(toasts.length).toBe(3);
  });

  it('action button invokes its handler and dismisses the toast', () => {
    const handler = jest.fn();
    showToast({ message: 'with action', actions: [{ label: 'Undo', onClick: handler }] });
    const actionBtn = document.querySelector<HTMLButtonElement>('.toast-actions button');
    expect(actionBtn?.textContent).toBe('Undo');
    actionBtn!.click();
    expect(handler).toHaveBeenCalledTimes(1);
    jest.advanceTimersByTime(200);
    expect(document.querySelector('.toast')).toBeNull();
  });

  it('programmatic dismiss() removes the toast', () => {
    const { dismiss } = showToast({ message: 'prog', timeout: null });
    expect(document.querySelector('.toast')).not.toBeNull();
    dismiss();
    jest.advanceTimersByTime(200);
    expect(document.querySelector('.toast')).toBeNull();
  });

  it('error-kind has role="alert" for assistive-tech urgency', () => {
    showToast({ message: 'bad', kind: 'error' });
    expect(document.querySelector('.toast--error')?.getAttribute('role')).toBe('alert');
  });

  it('non-error kind has role="status" (polite)', () => {
    showToast({ message: 'ok', kind: 'success' });
    expect(document.querySelector('.toast--success')?.getAttribute('role')).toBe('status');
  });
});
