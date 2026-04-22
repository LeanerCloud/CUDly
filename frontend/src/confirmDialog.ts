/**
 * Reusable confirmation dialog that replaces native window.confirm() for
 * destructive or cascading actions. Returns a promise that resolves true
 * on confirm and false on cancel / ESC / backdrop click.
 *
 * Rendered as a centred modal with backdrop, ESC cancels, focus-trap
 * keeps Tab inside the dialog. The `destructive: true` flag renders the
 * confirm button in the destructive variant so users see red before they
 * click a deletion.
 */

export interface ConfirmDialogOptions {
  title: string;
  body: string | HTMLElement;
  confirmLabel?: string;
  cancelLabel?: string;
  destructive?: boolean;
}

export function confirmDialog(opts: ConfirmDialogOptions): Promise<boolean> {
  return new Promise((resolve) => {
    const backdrop = document.createElement('div');
    backdrop.className = 'modal-confirm-backdrop';
    backdrop.setAttribute('role', 'dialog');
    backdrop.setAttribute('aria-modal', 'true');

    const dialog = document.createElement('div');
    dialog.className = 'modal-confirm';

    const titleEl = document.createElement('h3');
    titleEl.className = 'modal-confirm-title';
    titleEl.textContent = opts.title;
    dialog.appendChild(titleEl);

    const bodyEl = document.createElement('div');
    bodyEl.className = 'modal-confirm-body';
    if (typeof opts.body === 'string') {
      bodyEl.textContent = opts.body;
    } else {
      bodyEl.appendChild(opts.body);
    }
    dialog.appendChild(bodyEl);

    const actions = document.createElement('div');
    actions.className = 'modal-confirm-actions';

    const cancelBtn = document.createElement('button');
    cancelBtn.type = 'button';
    cancelBtn.className = 'btn btn-secondary';
    cancelBtn.textContent = opts.cancelLabel ?? 'Cancel';

    const confirmBtn = document.createElement('button');
    confirmBtn.type = 'button';
    confirmBtn.className = opts.destructive ? 'btn btn-destructive' : 'btn btn-primary';
    confirmBtn.textContent = opts.confirmLabel ?? 'Confirm';

    actions.appendChild(cancelBtn);
    actions.appendChild(confirmBtn);
    dialog.appendChild(actions);
    backdrop.appendChild(dialog);
    document.body.appendChild(backdrop);

    const previousActive = document.activeElement as HTMLElement | null;
    confirmBtn.focus();

    let settled = false;
    const settle = (value: boolean): void => {
      if (settled) return;
      settled = true;
      document.removeEventListener('keydown', onKey, true);
      backdrop.remove();
      if (previousActive && typeof previousActive.focus === 'function') {
        previousActive.focus();
      }
      resolve(value);
    };

    const onKey = (e: KeyboardEvent): void => {
      if (e.key === 'Escape') {
        e.preventDefault();
        settle(false);
      } else if (e.key === 'Tab') {
        // Focus trap: cycle Tab between the two buttons.
        e.preventDefault();
        const next = document.activeElement === confirmBtn ? cancelBtn : confirmBtn;
        next.focus();
      }
    };

    cancelBtn.addEventListener('click', () => settle(false));
    confirmBtn.addEventListener('click', () => settle(true));
    backdrop.addEventListener('click', (e) => {
      if (e.target === backdrop) settle(false);
    });
    document.addEventListener('keydown', onKey, true);
  });
}
