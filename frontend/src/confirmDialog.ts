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
  // When true, hides the left dismiss button. Useful when the natural
  // dismiss label ("Cancel") would collide with the confirm action (e.g.
  // confirming a purchase cancellation — you'd end up with two buttons
  // both labelled "Cancel"). Users can still dismiss via the close-X,
  // ESC, or a backdrop click.
  hideCancelButton?: boolean;
}

export function confirmDialog(opts: ConfirmDialogOptions): Promise<boolean> {
  return new Promise((resolve) => {
    const backdrop = document.createElement('div');
    backdrop.className = 'modal-confirm-backdrop';
    backdrop.setAttribute('role', 'dialog');
    backdrop.setAttribute('aria-modal', 'true');

    const dialog = document.createElement('div');
    dialog.className = 'modal-confirm';

    // Close-X in the top-right corner — always present so any dialog can
    // be dismissed without relying on the left action button. This is
    // what makes `hideCancelButton: true` a viable option for callers
    // whose confirm action would otherwise fight the dismiss label.
    const closeBtn = document.createElement('button');
    closeBtn.type = 'button';
    closeBtn.className = 'modal-confirm-close';
    closeBtn.setAttribute('aria-label', 'Close');
    closeBtn.textContent = '×';
    dialog.appendChild(closeBtn);

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

    const hideCancel = opts.hideCancelButton === true;
    const cancelBtn = document.createElement('button');
    cancelBtn.type = 'button';
    cancelBtn.className = 'btn btn-secondary';
    cancelBtn.textContent = opts.cancelLabel ?? 'Cancel';

    const confirmBtn = document.createElement('button');
    confirmBtn.type = 'button';
    confirmBtn.className = opts.destructive ? 'btn btn-destructive' : 'btn btn-primary';
    confirmBtn.textContent = opts.confirmLabel ?? 'Confirm';

    if (!hideCancel) {
      actions.appendChild(cancelBtn);
    }
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
        // Focus trap: cycle Tab between the focusable elements the dialog
        // actually rendered. When hideCancelButton is true, cycle between
        // close-X and confirm; otherwise include the dismiss button too.
        e.preventDefault();
        const cycle: HTMLButtonElement[] = hideCancel
          ? [confirmBtn, closeBtn]
          : [confirmBtn, cancelBtn, closeBtn];
        const idx = cycle.indexOf(document.activeElement as HTMLButtonElement);
        const next = cycle[(idx + 1 + cycle.length) % cycle.length] ?? confirmBtn;
        next.focus();
      }
    };

    closeBtn.addEventListener('click', () => settle(false));
    cancelBtn.addEventListener('click', () => settle(false));
    confirmBtn.addEventListener('click', () => settle(true));
    backdrop.addEventListener('click', (e) => {
      if (e.target === backdrop) settle(false);
    });
    document.addEventListener('keydown', onKey, true);
  });
}
