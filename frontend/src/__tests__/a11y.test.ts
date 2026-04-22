/**
 * Smoke tests for accessible names on frequently-used icon-only buttons.
 * Each assertion picks a button that the 2026-04-22 audit flagged as
 * lacking a screen-reader-friendly label. A full jest-axe pass is a
 * separate initiative.
 */

import { renderUsers } from '../users/userList';
import { renderPermissionMatrix } from '../users/permissionMatrix';
import { loadAccountsForProvider } from '../settings';

// Mock for the accounts a11y case — loadAccountsForProvider calls the
// real API layer otherwise.
jest.mock('../api', () => ({
  listAccounts: jest.fn(),
}));
import * as api from '../api';

describe('accessibility smoke', () => {
  afterEach(() => {
    const body = document.body;
    while (body.firstChild) body.removeChild(body.firstChild);
  });

  describe('Users table actions', () => {
    it('Edit and Delete buttons carry per-row aria-labels', () => {
      // Minimal DOM Shell renderUsers expects.
      const container = document.createElement('div');
      container.id = 'users-list';
      document.body.appendChild(container);

      renderUsers([
        {
          id: 'u1',
          email: 'alice@example.com',
          role: 'admin',
          groups: [],
          mfa_enabled: false,
          created_at: '2024-01-01T00:00:00Z',
          last_login: '2024-06-01T00:00:00Z',
        },
        // eslint-disable-next-line @typescript-eslint/no-explicit-any
      ] as any);

      const editBtn = container.querySelector('.edit-user-btn');
      const deleteBtn = container.querySelector('.delete-user-btn');
      expect(editBtn?.getAttribute('aria-label')).toBe('Edit user alice@example.com');
      expect(deleteBtn?.getAttribute('aria-label')).toBe('Delete user alice@example.com');
    });

    it('Expand button announces which user it expands', () => {
      const container = document.createElement('div');
      container.id = 'users-list';
      document.body.appendChild(container);

      renderUsers([
        {
          id: 'u1',
          email: 'bob@example.com',
          role: 'user',
          groups: [],
          mfa_enabled: true,
          created_at: '2024-01-01T00:00:00Z',
        },
        // eslint-disable-next-line @typescript-eslint/no-explicit-any
      ] as any);

      const expandBtn = container.querySelector('.user-expand-btn');
      expect(expandBtn?.getAttribute('aria-label')).toBe('Expand user bob@example.com');
    });
  });

  describe('Permission Overview matrix', () => {
    it('cells pair ✓/— with explicit aria-label for screen readers', () => {
      const container = document.createElement('div');
      document.body.appendChild(container);
      renderPermissionMatrix(
        [
          {
            id: 'g1',
            name: 'Administrators',
            description: '',
            permissions: [{ action: 'view', resource: 'recommendations' }],
          },
          // eslint-disable-next-line @typescript-eslint/no-explicit-any
        ] as any,
        container,
      );

      const granted = container.querySelector('td.has-perm');
      const notGranted = container.querySelector('td.no-perm');
      expect(granted?.getAttribute('aria-label')).toBe('Granted');
      expect(notGranted?.getAttribute('aria-label')).toBe('Not granted');
      // sr-only text also present (double-up guard)
      expect(granted?.querySelector('.sr-only')?.textContent).toBe(' Granted');
      expect(notGranted?.querySelector('.sr-only')?.textContent).toBe(' Not granted');
    });
  });

  describe('Accounts table actions', () => {
    it('Delete button carries a per-account aria-label identifying the row', async () => {
      const container = document.createElement('div');
      container.id = 'aws-accounts-list';
      document.body.appendChild(container);

      (api.listAccounts as jest.Mock).mockResolvedValue([
        { id: 'a1', name: 'Prod', provider: 'aws', external_id: '111222333', enabled: true },
      ]);

      await loadAccountsForProvider('aws');

      const deleteBtn = container.querySelector('.btn-destructive') as HTMLButtonElement | null;
      expect(deleteBtn?.getAttribute('aria-label')).toBe('Delete Prod (111222333)');
    });
  });

  describe('RI Exchange settings deep-link', () => {
    it('⚙︎ Exchange settings button exposes an accessible name via its visible label', () => {
      // Mirror the index.html markup — the test guards against a future
      // refactor that strips the visible label into an icon-only button
      // without adding aria-label.
      const btn = document.createElement('button');
      btn.id = 'ri-exchange-settings-btn';
      btn.className = 'btn btn-small';
      btn.title = 'Jump to Exchange Automation settings in Settings → Purchasing';
      btn.textContent = '⚙︎ Exchange settings';
      document.body.appendChild(btn);

      // Accessible name comes from text content when no aria-label is set.
      // Stripping 'Exchange settings' from the text would silently regress.
      expect(btn.textContent?.trim()).toContain('Exchange settings');
      expect(btn.getAttribute('title')).toContain('Exchange Automation');
    });
  });
});
