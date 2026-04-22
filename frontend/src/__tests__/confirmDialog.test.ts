/**
 * Tests for the reusable confirmDialog helper.
 */

import { confirmDialog } from '../confirmDialog';

describe('confirmDialog', () => {
  afterEach(() => {
    const body = document.body;
    while (body.firstChild) body.removeChild(body.firstChild);
  });

  it('renders title + body text + both buttons', () => {
    void confirmDialog({ title: 'Delete?', body: 'Permanent action.', destructive: true });
    const dialog = document.querySelector('.modal-confirm')!;
    expect(dialog.querySelector('.modal-confirm-title')?.textContent).toBe('Delete?');
    expect(dialog.querySelector('.modal-confirm-body')?.textContent).toBe('Permanent action.');
    const buttons = dialog.querySelectorAll('button');
    expect(buttons.length).toBe(2);
  });

  it('applies .btn-destructive to the confirm button when destructive is true', () => {
    void confirmDialog({ title: 't', body: 'b', destructive: true });
    const confirmBtn = document.querySelector('.modal-confirm button.btn-destructive');
    expect(confirmBtn).not.toBeNull();
  });

  it('applies .btn-primary to the confirm button when destructive is false', () => {
    void confirmDialog({ title: 't', body: 'b' });
    const confirmBtn = document.querySelector('.modal-confirm button.btn-primary');
    expect(confirmBtn).not.toBeNull();
  });

  it('resolves true when the confirm button is clicked', async () => {
    const promise = confirmDialog({ title: 't', body: 'b' });
    const confirmBtn = document.querySelector<HTMLButtonElement>('.modal-confirm button.btn-primary')!;
    confirmBtn.click();
    await expect(promise).resolves.toBe(true);
    expect(document.querySelector('.modal-confirm')).toBeNull();
  });

  it('resolves false when the cancel button is clicked', async () => {
    const promise = confirmDialog({ title: 't', body: 'b' });
    const cancelBtn = document.querySelector<HTMLButtonElement>('.modal-confirm button.btn-secondary')!;
    cancelBtn.click();
    await expect(promise).resolves.toBe(false);
    expect(document.querySelector('.modal-confirm')).toBeNull();
  });

  it('resolves false when ESC is pressed', async () => {
    const promise = confirmDialog({ title: 't', body: 'b' });
    document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' }));
    await expect(promise).resolves.toBe(false);
  });

  it('resolves false when the backdrop is clicked', async () => {
    const promise = confirmDialog({ title: 't', body: 'b' });
    const backdrop = document.querySelector<HTMLDivElement>('.modal-confirm-backdrop')!;
    backdrop.click();
    await expect(promise).resolves.toBe(false);
  });

  it('honours custom confirm + cancel labels', () => {
    void confirmDialog({
      title: 't',
      body: 'b',
      confirmLabel: 'Reset all',
      cancelLabel: 'Keep them',
    });
    expect(document.querySelector('.btn-primary')?.textContent).toBe('Reset all');
    expect(document.querySelector('.btn-secondary')?.textContent).toBe('Keep them');
  });
});
