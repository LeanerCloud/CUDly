/**
 * Tests for users module
 * Comprehensive tests for 80%+ coverage
 */

import './setup';

// Mock the api module
jest.mock('../api', () => ({
  listUsers: jest.fn(),
  listGroups: jest.fn(),
  deleteUser: jest.fn(),
  updateUser: jest.fn(),
  createUser: jest.fn(),
  getUser: jest.fn(),
}));

// Mock the groups/groupList module
jest.mock('../groups/groupList', () => ({
  renderGroups: jest.fn(),
}));

// Q7: userActions.deleteUser / bulkDeleteUsers migrated from window.confirm
// to confirmDialog. Mock it so existing tests don't hang waiting for a
// real modal click.
const mockConfirmDialog = jest.fn<Promise<boolean>, [unknown]>(() => Promise.resolve(true));
jest.mock('../confirmDialog', () => ({
  confirmDialog: (opts: unknown) => mockConfirmDialog(opts),
}));

import * as api from '../api';
import { renderGroups } from '../groups/groupList';
import * as userUtils from '../users/utils';
import * as userState from '../users/state';
import * as userFilters from '../users/filters';
import * as userList from '../users/userList';
import * as userActions from '../users/userActions';
import * as userModals from '../users/userModals';
import * as userHandlers from '../users/handlers';

// ============================================================================
// UTILS TESTS
// ============================================================================
describe('users/utils', () => {
  describe('formatRelativeTime', () => {
    it('should return "Just now" for very recent times', () => {
      const now = new Date();
      expect(userUtils.formatRelativeTime(now.toISOString())).toBe('Just now');
    });

    it('should return "Just now" for times less than 1 minute ago', () => {
      const date = new Date(Date.now() - 30 * 1000); // 30 seconds ago
      expect(userUtils.formatRelativeTime(date.toISOString())).toBe('Just now');
    });

    it('should return minutes ago for times within an hour', () => {
      const date = new Date(Date.now() - 30 * 60 * 1000); // 30 mins ago
      expect(userUtils.formatRelativeTime(date.toISOString())).toBe('30m ago');
    });

    it('should return 1m ago for exactly 1 minute', () => {
      const date = new Date(Date.now() - 60 * 1000); // 1 minute ago
      expect(userUtils.formatRelativeTime(date.toISOString())).toBe('1m ago');
    });

    it('should return 59m ago for 59 minutes', () => {
      const date = new Date(Date.now() - 59 * 60 * 1000); // 59 mins ago
      expect(userUtils.formatRelativeTime(date.toISOString())).toBe('59m ago');
    });

    it('should return hours ago for times within a day', () => {
      const date = new Date(Date.now() - 5 * 60 * 60 * 1000); // 5 hours ago
      expect(userUtils.formatRelativeTime(date.toISOString())).toBe('5h ago');
    });

    it('should return 1h ago for exactly 1 hour', () => {
      const date = new Date(Date.now() - 60 * 60 * 1000); // 1 hour ago
      expect(userUtils.formatRelativeTime(date.toISOString())).toBe('1h ago');
    });

    it('should return 23h ago for 23 hours', () => {
      const date = new Date(Date.now() - 23 * 60 * 60 * 1000); // 23 hours ago
      expect(userUtils.formatRelativeTime(date.toISOString())).toBe('23h ago');
    });

    it('should return days ago for times within a week', () => {
      const date = new Date(Date.now() - 3 * 24 * 60 * 60 * 1000); // 3 days ago
      expect(userUtils.formatRelativeTime(date.toISOString())).toBe('3d ago');
    });

    it('should return 1d ago for exactly 1 day', () => {
      const date = new Date(Date.now() - 24 * 60 * 60 * 1000); // 1 day ago
      expect(userUtils.formatRelativeTime(date.toISOString())).toBe('1d ago');
    });

    it('should return 6d ago for 6 days', () => {
      const date = new Date(Date.now() - 6 * 24 * 60 * 60 * 1000); // 6 days ago
      expect(userUtils.formatRelativeTime(date.toISOString())).toBe('6d ago');
    });

    it('should return formatted date for times 7 days or older', () => {
      const date = new Date(Date.now() - 7 * 24 * 60 * 60 * 1000); // 7 days ago
      const result = userUtils.formatRelativeTime(date.toISOString());
      // Canonical en-US short-month form: "Mar 15, 2026"
      expect(result).toMatch(/^[A-Z][a-z]{2} \d{1,2}, \d{4}$/);
    });

    it('should return formatted date for older times', () => {
      const date = new Date(Date.now() - 10 * 24 * 60 * 60 * 1000); // 10 days ago
      const result = userUtils.formatRelativeTime(date.toISOString());
      expect(result).toMatch(/^[A-Z][a-z]{2} \d{1,2}, \d{4}$/);
    });

    it('should handle dates far in the past', () => {
      const date = new Date('2020-01-01T00:00:00Z');
      const result = userUtils.formatRelativeTime(date.toISOString());
      // TZ-adjusted — could be Dec 31 2019 or Jan 1 2020. Match shape + year.
      expect(result).toMatch(/^[A-Z][a-z]{2} \d{1,2}, (2019|2020)$/);
    });
  });

  describe('escapeHtml', () => {
    it('should escape HTML special characters', () => {
      expect(userUtils.escapeHtml('<script>alert("xss")</script>')).toBe(
        '&lt;script&gt;alert(&quot;xss&quot;)&lt;/script&gt;'
      );
    });

    it('should handle normal text', () => {
      expect(userUtils.escapeHtml('Hello World')).toBe('Hello World');
    });

    it('should escape ampersands', () => {
      expect(userUtils.escapeHtml('Tom & Jerry')).toBe('Tom &amp; Jerry');
    });

    // Regression for ARCH-07 (issue #1195): the previous local DOM
    // round-trip implementation left both quote characters unescaped,
    // so values interpolated inside double-quoted attributes (e.g. the
    // aria-label carrying the user email in userList.ts) could break
    // out of the attribute and inject markup.
    it('should escape single quotes', () => {
      const result = userUtils.escapeHtml("It's a test");
      expect(result).toBe('It&#39;s a test');
    });

    it('should escape double quotes', () => {
      const result = userUtils.escapeHtml('Say "Hello"');
      expect(result).toBe('Say &quot;Hello&quot;');
    });

    it('issue #1195: quote in attribute position cannot break out of the attribute', () => {
      const malicious = 'a@b.com" onmouseover="alert(1)';
      const div = document.createElement('div');
      div.innerHTML = `<button aria-label="${userUtils.escapeHtml(malicious)}">x</button>`;
      const btn = div.querySelector('button');
      expect(btn?.getAttribute('onmouseover')).toBeNull();
      expect(btn?.getAttribute('aria-label')).toBe(malicious);
    });

    it('should handle empty string', () => {
      expect(userUtils.escapeHtml('')).toBe('');
    });

    it('should handle multiple special characters', () => {
      expect(userUtils.escapeHtml('<div class="test">&</div>')).toBe(
        '&lt;div class=&quot;test&quot;&gt;&amp;&lt;/div&gt;'
      );
    });
  });

  describe('showError', () => {
    beforeEach(() => {
      jest.useFakeTimers();
    });

    afterEach(() => {
      jest.useRealTimers();
    });

    it('should create and display error toast', () => {
      userUtils.showError('Test error message');

      const toast = document.querySelector('.toast-error');
      expect(toast).toBeTruthy();
      // New toast DOM includes icon + close button siblings, so textContent
      // is a concatenation. The message is in its own .toast-message child.
      expect(toast?.querySelector('.toast-message')?.textContent).toBe('Test error message');
      expect(toast?.classList.contains('toast')).toBe(true);
    });

    it('should remove error toast after timeout', () => {
      userUtils.showError('Test error');

      expect(document.querySelector('.toast-error')).toBeTruthy();

      // Q4 default for errors is 30s; plus the transition-end fallback (200ms).
      jest.advanceTimersByTime(30_000);
      jest.advanceTimersByTime(200);

      expect(document.querySelector('.toast-error')).toBeFalsy();
    });

    it('should handle multiple error toasts', () => {
      userUtils.showError('Error 1');
      userUtils.showError('Error 2');

      const toasts = document.querySelectorAll('.toast-error');
      expect(toasts.length).toBe(2);
    });
  });

  describe('showSuccess', () => {
    beforeEach(() => {
      jest.useFakeTimers();
    });

    afterEach(() => {
      jest.useRealTimers();
    });

    it('should create and display success toast', () => {
      userUtils.showSuccess('Success message');

      const toast = document.querySelector('.toast-success');
      expect(toast).toBeTruthy();
      expect(toast?.querySelector('.toast-message')?.textContent).toBe('Success message');
      expect(toast?.classList.contains('toast')).toBe(true);
    });

    it('should remove success toast after timeout', () => {
      userUtils.showSuccess('Success');

      expect(document.querySelector('.toast-success')).toBeTruthy();

      // Q4: showSuccess uses a 5s timeout + 200ms transition fallback.
      jest.advanceTimersByTime(5_000);
      jest.advanceTimersByTime(200);

      expect(document.querySelector('.toast-success')).toBeFalsy();
    });

    it('should handle multiple success toasts', () => {
      userUtils.showSuccess('Success 1');
      userUtils.showSuccess('Success 2');

      const toasts = document.querySelectorAll('.toast-success');
      expect(toasts.length).toBe(2);
    });
  });
});

// ============================================================================
// STATE TESTS
// ============================================================================
describe('users/state', () => {
  beforeEach(() => {
    userState.clearSelectedUserIds();
    userState.setAllUsers([]);
    userState.setFilteredUsers([]);
    userState.setAvailableGroups([]);
    userState.setCurrentEditingUser(null);
    userState.setSearchQuery('');
    userState.setRoleFilter('');
    userState.setMfaFilter('');
    userState.setGroupFilter('');
  });

  describe('selectedUserIds', () => {
    it('should add and remove user IDs', () => {
      expect(userState.selectedUserIds.size).toBe(0);

      userState.addSelectedUserId('user-1');
      expect(userState.selectedUserIds.has('user-1')).toBe(true);

      userState.addSelectedUserId('user-2');
      expect(userState.selectedUserIds.size).toBe(2);

      userState.removeSelectedUserId('user-1');
      expect(userState.selectedUserIds.has('user-1')).toBe(false);
      expect(userState.selectedUserIds.size).toBe(1);
    });

    it('should clear all selected user IDs', () => {
      userState.addSelectedUserId('user-1');
      userState.addSelectedUserId('user-2');

      userState.clearSelectedUserIds();
      expect(userState.selectedUserIds.size).toBe(0);
    });

    it('should handle removing non-existent user ID', () => {
      userState.addSelectedUserId('user-1');
      userState.removeSelectedUserId('user-nonexistent');
      expect(userState.selectedUserIds.size).toBe(1);
    });
  });

  describe('allUsers and filteredUsers', () => {
    it('should set and get users', () => {
      const users = [
        { id: '1', email: 'user1@test.com', groups: [], mfa_enabled: false },
        { id: '2', email: 'user2@test.com', groups: ['00000000-0000-5000-8000-000000000001'], mfa_enabled: true }
      ];

      userState.setAllUsers(users as any);
      expect(userState.allUsers).toEqual(users);
    });

    it('should set and get filtered users', () => {
      const users = [
        { id: '1', email: 'user1@test.com', groups: [], mfa_enabled: false }
      ];

      userState.setFilteredUsers(users as any);
      expect(userState.filteredUsers).toEqual(users);
    });

    it('should handle empty arrays', () => {
      userState.setAllUsers([]);
      userState.setFilteredUsers([]);
      expect(userState.allUsers).toEqual([]);
      expect(userState.filteredUsers).toEqual([]);
    });
  });

  describe('availableGroups', () => {
    it('should set and get available groups', () => {
      const groups = [
        { id: 'g1', name: 'Admins', permissions: [], description: '' }
      ];

      userState.setAvailableGroups(groups as any);
      expect(userState.availableGroups).toEqual(groups);
    });
  });

  describe('currentEditingUser', () => {
    it('should set and get current editing user', () => {
      const user = { id: '1', email: 'test@test.com', groups: [], mfa_enabled: false };
      userState.setCurrentEditingUser(user as any);
      expect(userState.currentEditingUser).toEqual(user);
    });

    it('should set current editing user to null', () => {
      userState.setCurrentEditingUser(null);
      expect(userState.currentEditingUser).toBeNull();
    });
  });

  describe('filters', () => {
    it('should set search query', () => {
      userState.setSearchQuery('test@email.com');
      expect(userState.searchQuery).toBe('test@email.com');
    });

    it('should set role filter', () => {
      userState.setRoleFilter('admin');
      expect(userState.roleFilter).toBe('admin');
    });

    it('should set MFA filter', () => {
      userState.setMfaFilter('enabled');
      expect(userState.mfaFilter).toBe('enabled');
    });

    it('should set group filter', () => {
      userState.setGroupFilter('group-1');
      expect(userState.groupFilter).toBe('group-1');
    });

    it('should allow empty filter values', () => {
      userState.setSearchQuery('');
      userState.setRoleFilter('');
      userState.setMfaFilter('');
      userState.setGroupFilter('');

      expect(userState.searchQuery).toBe('');
      expect(userState.roleFilter).toBe('');
      expect(userState.mfaFilter).toBe('');
      expect(userState.groupFilter).toBe('');
    });
  });
});

// ============================================================================
// FILTERS TESTS
// ============================================================================
describe('users/filters', () => {
  const mockUsers = [
    { id: '1', email: 'admin@test.com', groups: ['00000000-0000-5000-8000-000000000001'], mfa_enabled: true },
    { id: '2', email: 'user@test.com', groups: ['users'], mfa_enabled: false },
    { id: '3', email: 'viewer@test.com', groups: [], mfa_enabled: true },
    { id: '4', email: 'another.user@example.com', groups: ['users', 'developers'], mfa_enabled: true }
  ];

  beforeEach(() => {
    document.body.innerHTML = `
      <div id="users-list"></div>
      <div id="user-stats"></div>
      <div id="bulk-actions-bar" class="hidden">
        <span id="selected-count">0</span>
      </div>
      <input type="text" id="user-search" />
      <select id="user-role-filter"><option value="">All</option></select>
      <select id="user-mfa-filter"><option value="">All</option></select>
      <select id="user-group-filter"><option value="">All</option></select>
    `;
    userState.setAllUsers(mockUsers as any);
    userState.setSearchQuery('');
    userState.setRoleFilter('');
    userState.setMfaFilter('');
    userState.setGroupFilter('');
    userState.clearSelectedUserIds();
  });

  describe('applyFilters', () => {
    it('should filter by search term (email)', () => {
      userState.setSearchQuery('admin');
      userFilters.applyFilters();
      expect(userState.filteredUsers.length).toBe(1);
      expect(userState.filteredUsers[0]?.email).toBe('admin@test.com');
    });

    it('should filter by search term case-insensitively', () => {
      userState.setSearchQuery('ADMIN');
      userFilters.applyFilters();
      expect(userState.filteredUsers.length).toBe(1);
      expect(userState.filteredUsers[0]?.email).toBe('admin@test.com');
    });

    it('should filter by partial email match', () => {
      userState.setSearchQuery('@test.com');
      userFilters.applyFilters();
      expect(userState.filteredUsers.length).toBe(3);
    });

    it('should filter by admin role (Administrators group membership)', () => {
      userState.setRoleFilter('admin');
      userFilters.applyFilters();
      // Only the user in the Administrators group matches
      expect(userState.filteredUsers.length).toBe(1);
      expect(userState.filteredUsers[0]?.email).toBe('admin@test.com');
    });

    it('should filter non-admin users with role filter', () => {
      userState.setRoleFilter('user'); // non-admin = not in Administrators group
      userFilters.applyFilters();
      // 3 users not in Administrators group
      expect(userState.filteredUsers.length).toBe(3);
    });

    it('should filter by MFA enabled', () => {
      userState.setMfaFilter('enabled');
      userFilters.applyFilters();
      expect(userState.filteredUsers.length).toBe(3);
      expect(userState.filteredUsers.every(u => u.mfa_enabled)).toBe(true);
    });

    it('should filter by MFA disabled', () => {
      userState.setMfaFilter('disabled');
      userFilters.applyFilters();
      expect(userState.filteredUsers.length).toBe(1);
      expect(userState.filteredUsers[0]!.mfa_enabled).toBe(false);
    });

    it('should filter by group', () => {
      userState.setGroupFilter('00000000-0000-5000-8000-000000000001');
      userFilters.applyFilters();
      expect(userState.filteredUsers.length).toBe(1);
      expect(userState.filteredUsers[0]!.groups).toContain('00000000-0000-5000-8000-000000000001');
    });

    it('should filter by users group (multiple users)', () => {
      userState.setGroupFilter('users');
      userFilters.applyFilters();
      expect(userState.filteredUsers.length).toBe(2);
    });

    it('should filter users with no groups (empty filter shows users without the specified group)', () => {
      userState.setGroupFilter('nonexistent');
      userFilters.applyFilters();
      expect(userState.filteredUsers.length).toBe(0);
    });

    it('should combine multiple filters (mfa + admin group)', () => {
      userState.setMfaFilter('enabled');
      userState.setRoleFilter('admin');
      userFilters.applyFilters();
      expect(userState.filteredUsers.length).toBe(1);
      expect(userState.filteredUsers[0]?.email).toBe('admin@test.com');
    });

    it('should combine search and non-admin role filter', () => {
      userState.setSearchQuery('user');
      userState.setRoleFilter('user'); // non-admin
      userFilters.applyFilters();
      // 'user@test.com' and 'another.user@example.com' match search + non-admin
      expect(userState.filteredUsers.length).toBe(2);
    });

    it('should combine all filters', () => {
      userState.setSearchQuery('user');
      userState.setRoleFilter('user'); // non-admin
      userState.setMfaFilter('enabled');
      userState.setGroupFilter('users');
      userFilters.applyFilters();
      expect(userState.filteredUsers.length).toBe(1);
      expect(userState.filteredUsers[0]?.email).toBe('another.user@example.com');
    });

    it('should return all users when no filters applied', () => {
      userFilters.applyFilters();
      expect(userState.filteredUsers.length).toBe(4);
    });

    it('should return empty when no users match filters', () => {
      userState.setSearchQuery('nonexistent');
      userFilters.applyFilters();
      expect(userState.filteredUsers.length).toBe(0);
    });
  });

  describe('handleUserSearch', () => {
    it('should update search query and apply filters', () => {
      userFilters.handleUserSearch('admin');
      expect(userState.searchQuery).toBe('admin');
      expect(userState.filteredUsers.length).toBe(1);
    });

    it('should render users after search', () => {
      userFilters.handleUserSearch('test');
      const container = document.getElementById('users-list');
      expect(container?.innerHTML).toContain('test');
    });

    it('should clear search when empty string', () => {
      userFilters.handleUserSearch('admin');
      userFilters.handleUserSearch('');
      expect(userState.searchQuery).toBe('');
      expect(userState.filteredUsers.length).toBe(4);
    });
  });

  describe('handleFilterChange', () => {
    it('should handle role filter change (admin = Administrators group)', () => {
      userFilters.handleFilterChange('role', 'admin');
      expect(userState.roleFilter).toBe('admin');
      // Only the Administrators group member matches
      expect(userState.filteredUsers.length).toBe(1);
    });

    it('should handle mfa filter change', () => {
      userFilters.handleFilterChange('mfa', 'enabled');
      expect(userState.mfaFilter).toBe('enabled');
      expect(userState.filteredUsers.length).toBe(3);
    });

    it('should handle group filter change', () => {
      userFilters.handleFilterChange('group', '00000000-0000-5000-8000-000000000001');
      expect(userState.groupFilter).toBe('00000000-0000-5000-8000-000000000001');
      expect(userState.filteredUsers.length).toBe(1);
    });

    it('should ignore unknown filter types', () => {
      userFilters.handleFilterChange('unknown', 'value');
      // Should not throw and filters should remain unchanged
      expect(userState.roleFilter).toBe('');
      expect(userState.mfaFilter).toBe('');
      expect(userState.groupFilter).toBe('');
    });
  });

  describe('clearFilters', () => {
    it('should clear all filters', () => {
      userState.setSearchQuery('test');
      userState.setRoleFilter('admin');
      userState.setMfaFilter('enabled');
      userState.setGroupFilter('00000000-0000-5000-8000-000000000001');

      userFilters.clearFilters();

      expect(userState.searchQuery).toBe('');
      expect(userState.roleFilter).toBe('');
      expect(userState.mfaFilter).toBe('');
      expect(userState.groupFilter).toBe('');
    });

    it('should reset filter inputs', () => {
      const searchInput = document.getElementById('user-search') as HTMLInputElement;
      const roleSelect = document.getElementById('user-role-filter') as HTMLSelectElement;
      const mfaSelect = document.getElementById('user-mfa-filter') as HTMLSelectElement;
      const groupSelect = document.getElementById('user-group-filter') as HTMLSelectElement;

      searchInput.value = 'test';
      roleSelect.value = 'admin';
      mfaSelect.value = 'enabled';
      groupSelect.value = 'admins';

      userFilters.clearFilters();

      expect(searchInput.value).toBe('');
      expect(roleSelect.value).toBe('');
      expect(mfaSelect.value).toBe('');
      expect(groupSelect.value).toBe('');
    });

    it('should re-render all users after clearing', () => {
      userState.setRoleFilter('admin');
      userFilters.applyFilters();
      // Only 1 Administrators-group member
      expect(userState.filteredUsers.length).toBe(1);

      userFilters.clearFilters();
      expect(userState.filteredUsers.length).toBe(4);
    });

    it('should handle missing DOM elements gracefully', () => {
      document.body.innerHTML = '';
      expect(() => userFilters.clearFilters()).not.toThrow();
    });
  });

  describe('updateGroupFilterDropdown', () => {
    beforeEach(() => {
      document.body.innerHTML = `
        <select id="user-group-filter">
          <option value="">All Groups</option>
        </select>
      `;
      userState.setAvailableGroups([
        { id: 'g1', name: 'Admins', permissions: [], description: '' },
        { id: 'g2', name: 'Users', permissions: [], description: '' }
      ] as any);
    });

    it('should populate group dropdown', () => {
      userFilters.updateGroupFilterDropdown();

      const select = document.getElementById('user-group-filter') as HTMLSelectElement;
      expect(select.options.length).toBe(3); // All Groups + 2 groups
      expect(select.options[1]?.value).toBe('g1');
      expect(select.options[1]?.textContent?.trim()).toBe('Admins');
      expect(select.options[2]?.value).toBe('g2');
      expect(select.options[2]?.textContent?.trim()).toBe('Users');
    });

    it('should preserve current selection', () => {
      const select = document.getElementById('user-group-filter') as HTMLSelectElement;
      select.innerHTML = '<option value="">All</option><option value="g1">Admins</option>';
      select.value = 'g1';

      userFilters.updateGroupFilterDropdown();

      expect(select.value).toBe('g1');
    });

    it('should handle missing element gracefully', () => {
      document.body.innerHTML = '';
      expect(() => userFilters.updateGroupFilterDropdown()).not.toThrow();
    });

    it('should escape HTML in group names', () => {
      userState.setAvailableGroups([
        { id: 'g1', name: '<script>alert("xss")</script>', permissions: [], description: '' }
      ] as any);

      userFilters.updateGroupFilterDropdown();

      const select = document.getElementById('user-group-filter') as HTMLSelectElement;
      expect(select.innerHTML).toContain('&lt;script&gt;');
    });

    it('11-M2: escapes HTML special chars in group id (value attribute)', () => {
      // Regression: group.id was injected raw into value="..." in the innerHTML
      // template. An id containing quotes or angle brackets could break the
      // option element boundary. Verify escapeHtml() is applied to the id too.
      userState.setAvailableGroups([
        { id: '"><img src=x onerror=alert(1)>', name: 'Evil Group', permissions: [], description: '' }
      ] as any);

      userFilters.updateGroupFilterDropdown();

      const select = document.getElementById('user-group-filter') as HTMLSelectElement;
      // No element may be injected out of the value attribute.
      expect(select.querySelector('img')).toBeNull();
      // The raw payload must not appear verbatim inside the rendered HTML.
      expect(select.innerHTML).not.toContain('"><img src=x');
      // With full escaping (issue #1195 made escapeHtml quote-safe), the
      // option value round-trips the original id intact instead of being
      // truncated at the first double quote.
      const opt = select.options[1];
      expect(opt?.value).toBe('"><img src=x onerror=alert(1)>');
    });
  });
});

// ============================================================================
// USER LIST TESTS
// ============================================================================
describe('users/userList', () => {
  const mockUsers = [
    { id: '1', email: 'admin@test.com', groups: ['00000000-0000-5000-8000-000000000001'], mfa_enabled: true, created_at: '2024-01-01T00:00:00Z' },
    { id: '2', email: 'user@test.com', groups: [], mfa_enabled: false, created_at: '2024-01-02T00:00:00Z' }
  ];

  beforeEach(() => {
    document.body.innerHTML = `
      <div id="users-list"></div>
      <div id="user-stats"></div>
      <div id="bulk-actions-bar" class="hidden">
        <span id="selected-count">0</span>
      </div>
    `;
    userState.setAllUsers(mockUsers as any);
    userState.setFilteredUsers(mockUsers as any);
    userState.clearSelectedUserIds();
    jest.clearAllMocks();
  });

  describe('renderUserStats', () => {
    it('should render user statistics', () => {
      userList.renderUserStats();

      const statsContainer = document.getElementById('user-stats');
      expect(statsContainer?.innerHTML).toContain('Total Users');
      expect(statsContainer?.innerHTML).toContain('2');
      expect(statsContainer?.innerHTML).toContain('Administrators');
      expect(statsContainer?.innerHTML).toContain('1');
      expect(statsContainer?.innerHTML).toContain('MFA Enabled');
      expect(statsContainer?.innerHTML).toContain('Showing');
    });

    it('should show correct admin count', () => {
      userList.renderUserStats();

      const statsContainer = document.getElementById('user-stats');
      const html = statsContainer?.innerHTML || '';
      expect(html).toContain('Administrators');
    });

    it('should show correct MFA enabled count', () => {
      userList.renderUserStats();

      const statsContainer = document.getElementById('user-stats');
      const html = statsContainer?.innerHTML || '';
      expect(html).toContain('MFA Enabled');
    });

    it('should highlight when showing filtered subset', () => {
      userState.setFilteredUsers([mockUsers[0]] as any);
      userList.renderUserStats();

      const statsContainer = document.getElementById('user-stats');
      expect(statsContainer?.innerHTML).toContain('stat-card-highlight');
    });

    it('should not highlight when showing all users', () => {
      userList.renderUserStats();

      const statsContainer = document.getElementById('user-stats');
      // When filteredUsers.length === allUsers.length, no highlight
      const highlightCount = (statsContainer?.innerHTML.match(/stat-card-highlight/g) || []).length;
      expect(highlightCount).toBe(0);
    });

    it('should handle missing container', () => {
      document.body.innerHTML = '';
      expect(() => userList.renderUserStats()).not.toThrow();
    });

    it('should handle empty users', () => {
      userState.setAllUsers([]);
      userState.setFilteredUsers([]);
      userList.renderUserStats();

      const statsContainer = document.getElementById('user-stats');
      expect(statsContainer?.innerHTML).toContain('0');
    });
  });

  describe('renderUsers', () => {
    it('should render users table', () => {
      userList.renderUsers(mockUsers as any);

      const container = document.getElementById('users-list');
      expect(container?.querySelector('table')).toBeTruthy();
      expect(container?.innerHTML).toContain('admin@test.com');
      expect(container?.innerHTML).toContain('user@test.com');
    });

    it('should show empty message when no users', () => {
      userList.renderUsers([]);

      const container = document.getElementById('users-list');
      expect(container?.innerHTML).toContain('No users found');
    });

    it('should show selected state for selected users', () => {
      userState.addSelectedUserId('1');

      userList.renderUsers(mockUsers as any);

      const container = document.getElementById('users-list');
      expect(container?.querySelector('.row-selected')).toBeTruthy();
    });

    it('should render checkboxes for each user', () => {
      userList.renderUsers(mockUsers as any);

      const checkboxes = document.querySelectorAll('.user-checkbox');
      expect(checkboxes.length).toBe(2);
    });

    it('should render select all checkbox', () => {
      userList.renderUsers(mockUsers as any);

      const selectAll = document.getElementById('select-all-users');
      expect(selectAll).toBeTruthy();
    });

    it('should check select all when all users selected', () => {
      userState.addSelectedUserId('1');
      userState.addSelectedUserId('2');

      userList.renderUsers(mockUsers as any);

      const selectAll = document.getElementById('select-all-users') as HTMLInputElement;
      expect(selectAll?.checked).toBe(true);
    });

    it('should render group badges (role column removed, PR #912)', () => {
      userList.renderUsers(mockUsers as any);
      // The Role column was removed; verify the table renders without throwing
      const container = document.getElementById('users-list');
      expect(container?.querySelector('table')).toBeTruthy();
      // Group badges still appear
      expect(container?.innerHTML).toContain('badge-group');
    });

    it('should render MFA status badges', () => {
      userList.renderUsers(mockUsers as any);

      const container = document.getElementById('users-list');
      expect(container?.innerHTML).toContain('badge-success');
      expect(container?.innerHTML).toContain('badge-warning');
    });

    it('should render group badges', () => {
      userList.renderUsers(mockUsers as any);

      const container = document.getElementById('users-list');
      expect(container?.innerHTML).toContain('badge-group');
      // User 1 is in the Administrators group (ADMIN_GID); since availableGroups
      // is empty the group name lookup falls back to the UUID itself.
      expect(container?.innerHTML).toContain('00000000');
    });

    it('should show "No groups" for users without groups', () => {
      userList.renderUsers(mockUsers as any);

      const container = document.getElementById('users-list');
      expect(container?.innerHTML).toContain('No groups');
    });

    it('should render edit and delete buttons', () => {
      userList.renderUsers(mockUsers as any);

      const container = document.getElementById('users-list');
      expect(container?.querySelectorAll('.edit-user-btn').length).toBe(2);
      expect(container?.querySelectorAll('.delete-user-btn').length).toBe(2);
    });

    it('should handle missing container gracefully', () => {
      document.body.innerHTML = '';
      expect(() => userList.renderUsers(mockUsers as any)).not.toThrow();
    });

    it('should show created date', () => {
      userList.renderUsers(mockUsers as any);

      const container = document.getElementById('users-list');
      // Canonical format via formatDate: "Jan 1, 2024" (day varies with TZ
      // interpretation of the 00:00Z instant, so match structure + year).
      expect(container?.innerHTML).toMatch(/Jan \d{1,2}, 2024|Dec 3\d, 2023/);
    });

    it('should show dash for missing created_at', () => {
      const usersWithoutDate = [
        { id: '1', email: 'test@test.com', groups: [], mfa_enabled: false }
      ];
      userList.renderUsers(usersWithoutDate as any);

      const container = document.getElementById('users-list');
      const html = container?.innerHTML || '';
      // Should have a dash for missing date
      expect(html).toContain('-');
    });

    it('should render last login as Never when not set', () => {
      userList.renderUsers(mockUsers as any);

      const container = document.getElementById('users-list');
      expect(container?.innerHTML).toContain('Never');
    });

    it('should render last login relative time when set', () => {
      const usersWithLogin = [
        { id: '1', email: 'test@test.com', groups: [], mfa_enabled: false, last_login: new Date().toISOString() }
      ];
      userList.renderUsers(usersWithLogin as any);

      const container = document.getElementById('users-list');
      expect(container?.innerHTML).toContain('Just now');
    });

    it('should mark current user with "You" badge', () => {
      const usersWithCurrent = [
        { id: 'current', email: 'me@test.com', groups: [], mfa_enabled: false }
      ];
      userList.renderUsers(usersWithCurrent as any);

      const container = document.getElementById('users-list');
      expect(container?.innerHTML).toContain('You');
      expect(container?.innerHTML).toContain('badge-info');
    });

    it('should escape HTML in email', () => {
      const usersWithXss = [
        { id: '1', email: '<script>alert("xss")</script>', groups: [], mfa_enabled: false }
      ];
      userList.renderUsers(usersWithXss as any);

      const container = document.getElementById('users-list');
      expect(container?.innerHTML).toContain('&lt;script&gt;');
    });
  });

  describe('user table event listeners', () => {
    beforeEach(() => {
      userList.renderUsers(mockUsers as any);
    });

    it('should toggle user selection on checkbox click', () => {
      const checkbox = document.querySelector('.user-checkbox') as HTMLInputElement;
      expect(checkbox).toBeTruthy();

      checkbox.checked = true;
      checkbox.dispatchEvent(new Event('change', { bubbles: true }));

      expect(userState.selectedUserIds.has('1')).toBe(true);
    });

    it('should deselect user on checkbox uncheck', () => {
      userState.addSelectedUserId('1');
      userList.renderUsers(mockUsers as any);

      const checkbox = document.querySelector('.user-checkbox[data-user-id="1"]') as HTMLInputElement;
      checkbox.checked = false;
      checkbox.dispatchEvent(new Event('change', { bubbles: true }));

      expect(userState.selectedUserIds.has('1')).toBe(false);
    });

    it('should select all users on select all click', () => {
      const selectAll = document.getElementById('select-all-users') as HTMLInputElement;
      selectAll.checked = true;
      selectAll.dispatchEvent(new Event('change', { bubbles: true }));

      expect(userState.selectedUserIds.size).toBe(2);
    });

    it('should deselect all users on select all uncheck', () => {
      userState.addSelectedUserId('1');
      userState.addSelectedUserId('2');
      userList.renderUsers(mockUsers as any);

      const selectAll = document.getElementById('select-all-users') as HTMLInputElement;
      selectAll.checked = false;
      selectAll.dispatchEvent(new Event('change', { bubbles: true }));

      expect(userState.selectedUserIds.size).toBe(0);
    });
  });

  describe('updateBulkActionsBar', () => {
    it('should hide bulk actions when no users selected', () => {
      userList.updateBulkActionsBar();

      const bar = document.getElementById('bulk-actions-bar');
      expect(bar?.classList.contains('hidden')).toBe(true);
    });

    it('should show bulk actions when users are selected', () => {
      userState.addSelectedUserId('user-1');
      userState.addSelectedUserId('user-2');

      userList.updateBulkActionsBar();

      const bar = document.getElementById('bulk-actions-bar');
      expect(bar?.classList.contains('hidden')).toBe(false);

      const count = document.getElementById('selected-count');
      expect(count?.textContent).toBe('2');
    });

    it('should update count correctly', () => {
      userState.addSelectedUserId('user-1');
      userList.updateBulkActionsBar();

      let count = document.getElementById('selected-count');
      expect(count?.textContent).toBe('1');

      userState.addSelectedUserId('user-2');
      userState.addSelectedUserId('user-3');
      userList.updateBulkActionsBar();

      count = document.getElementById('selected-count');
      expect(count?.textContent).toBe('3');
    });

    it('should handle missing elements gracefully', () => {
      document.body.innerHTML = '';
      expect(() => userList.updateBulkActionsBar()).not.toThrow();
    });
  });
});

// ============================================================================
// USER ACTIONS TESTS
// ============================================================================
describe('users/userActions', () => {
  const mockUsers = [
    { id: '1', email: 'user1@test.com', groups: [], mfa_enabled: false },
    { id: '2', email: 'user2@test.com', groups: ['admins'], mfa_enabled: true }
  ];

  // `allowed_accounts: []` matches the backend Group shape after the
  // multi-cloud account-scoping work; renderGroups + state hydration
  // now expect this field on every group, so the fixture has to carry
  // it or deep-equal assertions diverge.
  const mockGroups = [
    { id: 'admins', name: 'Admins', permissions: [], description: '', allowed_accounts: [] },
    { id: 'developers', name: 'Developers', permissions: [], description: '', allowed_accounts: [] }
  ];

  beforeEach(() => {
    document.body.innerHTML = `
      <div id="users-list"></div>
      <div id="groups-list"></div>
      <div id="user-stats"></div>
      <div id="bulk-actions-bar" class="hidden">
        <span id="selected-count">0</span>
      </div>
    `;

    (api.listUsers as jest.Mock).mockResolvedValue({ users: mockUsers });
    (api.listGroups as jest.Mock).mockResolvedValue({ groups: mockGroups });
    (api.deleteUser as jest.Mock).mockResolvedValue({});
    (api.updateUser as jest.Mock).mockResolvedValue({});
    (api.createUser as jest.Mock).mockResolvedValue({});

    userState.setAllUsers([]);
    userState.setFilteredUsers([]);
    userState.setAvailableGroups([]);
    userState.clearSelectedUserIds();
    jest.clearAllMocks();
  });

  describe('loadUsers', () => {
    it('should load users and groups', async () => {
      await userActions.loadUsers();

      expect(api.listUsers).toHaveBeenCalled();
      expect(api.listGroups).toHaveBeenCalled();
      expect(userState.allUsers).toEqual(mockUsers);
      expect(userState.availableGroups).toEqual(mockGroups);
    });

    it('should render users after loading', async () => {
      await userActions.loadUsers();

      const container = document.getElementById('users-list');
      expect(container?.innerHTML).toContain('user1@test.com');
    });

    it('should render groups after loading', async () => {
      await userActions.loadUsers();

      expect(renderGroups).toHaveBeenCalledWith(mockGroups);
    });

    it('should render user stats after loading', async () => {
      await userActions.loadUsers();

      const statsContainer = document.getElementById('user-stats');
      expect(statsContainer?.innerHTML).toContain('Total Users');
    });

    it('should handle API error gracefully', async () => {
      (api.listUsers as jest.Mock).mockRejectedValue(new Error('Network error'));

      await userActions.loadUsers();

      // Should show error toast
      expect(document.querySelector('.toast-error')).toBeTruthy();
    });

    it('should handle groups API error gracefully', async () => {
      (api.listGroups as jest.Mock).mockRejectedValue(new Error('Groups error'));

      await userActions.loadUsers();

      expect(document.querySelector('.toast-error')).toBeTruthy();
    });
  });

  describe('deleteUser', () => {
    beforeEach(() => {
      userState.setAllUsers(mockUsers as any);
      (global.confirm as jest.Mock).mockReturnValue(true);
    });

    it('should delete user when confirmed', async () => {
      await userActions.deleteUser('1');

      expect(api.deleteUser).toHaveBeenCalledWith('1');
    });

    it('should show confirmation dialog', async () => {
      await userActions.deleteUser('1');

      expect(mockConfirmDialog).toHaveBeenCalledWith(
        expect.objectContaining({ title: expect.stringContaining('user1@test.com') })
      );
    });

    it('should reload users after deletion', async () => {
      await userActions.deleteUser('1');

      expect(api.listUsers).toHaveBeenCalled();
    });

    it('should show success message', async () => {
      jest.useFakeTimers();
      await userActions.deleteUser('1');

      expect(document.querySelector('.toast-success')).toBeTruthy();
      jest.useRealTimers();
    });

    it('should not delete when user not found', async () => {
      await userActions.deleteUser('nonexistent');

      expect(api.deleteUser).not.toHaveBeenCalled();
    });

    it('should not delete when cancelled', async () => {
      mockConfirmDialog.mockResolvedValueOnce(false);

      await userActions.deleteUser('1');

      expect(api.deleteUser).not.toHaveBeenCalled();
    });

    it('should handle delete error', async () => {
      (api.deleteUser as jest.Mock).mockRejectedValue(new Error('Delete failed'));

      await userActions.deleteUser('1');

      expect(document.querySelector('.toast-error')).toBeTruthy();
    });
  });

  describe('bulkDeleteUsers', () => {
    beforeEach(() => {
      userState.setAllUsers(mockUsers as any);
      (global.confirm as jest.Mock).mockReturnValue(true);
    });

    it('should delete multiple users', async () => {
      userState.addSelectedUserId('1');
      userState.addSelectedUserId('2');

      await userActions.bulkDeleteUsers();

      expect(api.deleteUser).toHaveBeenCalledTimes(2);
      expect(api.deleteUser).toHaveBeenCalledWith('1');
      expect(api.deleteUser).toHaveBeenCalledWith('2');
    });

    it('should not delete when no users selected', async () => {
      await userActions.bulkDeleteUsers();

      expect(api.deleteUser).not.toHaveBeenCalled();
    });

    it('should show confirmation with count', async () => {
      userState.addSelectedUserId('1');
      userState.addSelectedUserId('2');

      await userActions.bulkDeleteUsers();

      expect(mockConfirmDialog).toHaveBeenCalledWith(
        expect.objectContaining({ title: expect.stringContaining('2 user') })
      );
    });

    it('should clear selection after deletion', async () => {
      userState.addSelectedUserId('1');

      await userActions.bulkDeleteUsers();

      expect(userState.selectedUserIds.size).toBe(0);
    });

    it('should not delete when cancelled', async () => {
      mockConfirmDialog.mockResolvedValueOnce(false);
      userState.addSelectedUserId('1');

      await userActions.bulkDeleteUsers();

      expect(api.deleteUser).not.toHaveBeenCalled();
    });

    it('should show success message', async () => {
      jest.useFakeTimers();
      userState.addSelectedUserId('1');

      await userActions.bulkDeleteUsers();

      expect(document.querySelector('.toast-success')).toBeTruthy();
      jest.useRealTimers();
    });

    it('should handle partial failure', async () => {
      userState.addSelectedUserId('1');
      userState.addSelectedUserId('2');
      (api.deleteUser as jest.Mock)
        .mockResolvedValueOnce({})
        .mockRejectedValueOnce(new Error('Delete failed'));

      await userActions.bulkDeleteUsers();

      expect(document.querySelector('.toast-error')).toBeTruthy();
    });
  });

  describe('bulkChangeRole (removed in PR #912)', () => {
    it('bulkChangeRole no longer exists on userActions', () => {
      // PR #912 removed the role concept. bulkChangeRole was removed from
      // userActions. Verify the export is absent so callers that relied
      // on it are caught at compile/test time.
      expect((userActions as any).bulkChangeRole).toBeUndefined();
    });
  });

  describe('bulkAddToGroup', () => {
    beforeEach(() => {
      userState.setAllUsers(mockUsers as any);
      userState.setAvailableGroups(mockGroups as any);
      (global.confirm as jest.Mock).mockReturnValue(true);
    });

    it('should add selected users to group', async () => {
      userState.addSelectedUserId('1');

      await userActions.bulkAddToGroup('admins');

      expect(api.updateUser).toHaveBeenCalledWith('1', { groups: ['admins'] });
    });

    it('should not duplicate groups', async () => {
      userState.addSelectedUserId('2'); // Already in 'admins' group

      await userActions.bulkAddToGroup('admins');

      expect(api.updateUser).toHaveBeenCalledWith('2', { groups: ['admins'] });
    });

    it('should add to existing groups', async () => {
      userState.addSelectedUserId('2');

      await userActions.bulkAddToGroup('developers');

      expect(api.updateUser).toHaveBeenCalledWith('2', { groups: ['admins', 'developers'] });
    });

    it('should not add when no users selected', async () => {
      await userActions.bulkAddToGroup('admins');

      expect(api.updateUser).not.toHaveBeenCalled();
    });

    it('should not add when group not found', async () => {
      userState.addSelectedUserId('1');

      await userActions.bulkAddToGroup('nonexistent');

      expect(api.updateUser).not.toHaveBeenCalled();
    });

    it('should show confirmation with group name', async () => {
      userState.addSelectedUserId('1');

      await userActions.bulkAddToGroup('admins');

      expect(global.confirm).toHaveBeenCalledWith(
        expect.stringContaining('Admins')
      );
    });

    it('should not add when cancelled', async () => {
      (global.confirm as jest.Mock).mockReturnValue(false);
      userState.addSelectedUserId('1');

      await userActions.bulkAddToGroup('admins');

      expect(api.updateUser).not.toHaveBeenCalled();
    });

    it('should clear selection after adding', async () => {
      userState.addSelectedUserId('1');

      await userActions.bulkAddToGroup('admins');

      expect(userState.selectedUserIds.size).toBe(0);
    });

    it('should show success message', async () => {
      jest.useFakeTimers();
      userState.addSelectedUserId('1');

      await userActions.bulkAddToGroup('admins');

      expect(document.querySelector('.toast-success')).toBeTruthy();
      jest.useRealTimers();
    });

    it('should handle update error', async () => {
      userState.addSelectedUserId('1');
      (api.updateUser as jest.Mock).mockRejectedValue(new Error('Update failed'));

      await userActions.bulkAddToGroup('admins');

      expect(document.querySelector('.toast-error')).toBeTruthy();
    });

    it('should skip users that are not found in allUsers', async () => {
      userState.addSelectedUserId('1');
      userState.addSelectedUserId('nonexistent');

      await userActions.bulkAddToGroup('admins');

      // Should only update user '1'
      expect(api.updateUser).toHaveBeenCalledTimes(1);
      expect(api.updateUser).toHaveBeenCalledWith('1', { groups: ['admins'] });
    });
  });
});

// ============================================================================
// USER MODALS TESTS
// ============================================================================
describe('users/userModals', () => {
  const mockUser = {
    id: '1',
    email: 'test@test.com',
    // role removed: PR #912 -- authorization is group-membership based.
    groups: ['users'],
    mfa_enabled: false
  };

  const mockGroups = [
    { id: 'admins', name: 'Admins', permissions: [], description: '' },
    { id: 'users', name: 'Users', permissions: [], description: '' }
  ];

  beforeEach(() => {
    document.body.innerHTML = `
      <div id="user-modal" class="hidden">
        <h2 id="user-modal-title">Create User</h2>
        <form id="user-form">
          <input type="hidden" id="user-id" />
          <input type="email" id="user-email" />
          <div id="password-fields">
            <input type="password" id="user-password" />
          </div>
          <!-- role selector removed in PR #912 -->
          <select id="user-groups" multiple></select>
          <button type="submit">Save</button>
        </form>
      </div>
      <div id="users-list"></div>
      <div id="groups-list"></div>
      <div id="user-stats"></div>
    `;

    userState.setAvailableGroups(mockGroups as any);
    userState.setCurrentEditingUser(null);
    userState.setAllUsers([mockUser] as any);
    userState.setFilteredUsers([mockUser] as any);

    (api.getUser as jest.Mock).mockResolvedValue(mockUser);
    (api.createUser as jest.Mock).mockResolvedValue({});
    (api.updateUser as jest.Mock).mockResolvedValue({});
    (api.listUsers as jest.Mock).mockResolvedValue({ users: [mockUser] });
    (api.listGroups as jest.Mock).mockResolvedValue({ groups: mockGroups });

    jest.clearAllMocks();
  });

  describe('openCreateUserModal', () => {
    it('should open modal for creating user', () => {
      userModals.openCreateUserModal();

      const modal = document.getElementById('user-modal');
      expect(modal?.classList.contains('hidden')).toBe(false);
    });

    it('should set title to Create User', () => {
      userModals.openCreateUserModal();

      const title = document.getElementById('user-modal-title');
      expect(title?.textContent).toBe('Create User');
    });

    it('should reset form', () => {
      (document.getElementById('user-email') as HTMLInputElement).value = 'old@test.com';

      userModals.openCreateUserModal();

      expect((document.getElementById('user-email') as HTMLInputElement).value).toBe('');
    });

    it('should clear user-id', () => {
      (document.getElementById('user-id') as HTMLInputElement).value = '123';

      userModals.openCreateUserModal();

      expect((document.getElementById('user-id') as HTMLInputElement).value).toBe('');
    });

    it('should show password field', () => {
      userModals.openCreateUserModal();

      const passwordFields = document.getElementById('password-fields');
      expect(passwordFields?.classList.contains('hidden')).toBe(false);
    });

    it('should not mark password as required (blank invites the user)', () => {
      // Issue #348 made password optional on create: leaving it blank
      // emails an invite that lets the user set their own password on
      // first login. Marking the field `required` on the form would
      // block that flow at HTML-validation time, so userModals
      // explicitly sets required=false on openCreateUserModal.
      userModals.openCreateUserModal();

      const passwordInput = document.getElementById('user-password') as HTMLInputElement;
      expect(passwordInput?.required).toBe(false);
    });

    it('should populate groups dropdown', () => {
      userModals.openCreateUserModal();

      const groupsSelect = document.getElementById('user-groups') as HTMLSelectElement;
      expect(groupsSelect.options.length).toBe(2);
    });

    it('should clear current editing user', () => {
      userState.setCurrentEditingUser(mockUser as any);

      userModals.openCreateUserModal();

      expect(userState.currentEditingUser).toBeNull();
    });

    it('should handle missing elements gracefully', () => {
      document.body.innerHTML = '';
      expect(() => userModals.openCreateUserModal()).not.toThrow();
    });
  });

  describe('openEditUserModal', () => {
    it('should open modal for editing user', async () => {
      await userModals.openEditUserModal('1');

      const modal = document.getElementById('user-modal');
      expect(modal?.classList.contains('hidden')).toBe(false);
    });

    it('should set title to Edit User', async () => {
      await userModals.openEditUserModal('1');

      const title = document.getElementById('user-modal-title');
      expect(title?.textContent).toBe('Edit User');
    });

    it('should load user from API', async () => {
      await userModals.openEditUserModal('1');

      expect(api.getUser).toHaveBeenCalledWith('1');
    });

    it('should populate form with user data', async () => {
      await userModals.openEditUserModal('1');

      expect((document.getElementById('user-id') as HTMLInputElement).value).toBe('1');
      expect((document.getElementById('user-email') as HTMLInputElement).value).toBe('test@test.com');
      // role field removed in PR #912
    });

    it('should hide password field when editing', async () => {
      await userModals.openEditUserModal('1');

      const passwordFields = document.getElementById('password-fields');
      expect(passwordFields?.classList.contains('hidden')).toBe(true);
    });

    it('should make password not required when editing', async () => {
      await userModals.openEditUserModal('1');

      const passwordInput = document.getElementById('user-password') as HTMLInputElement;
      expect(passwordInput?.required).toBe(false);
    });

    it('should select user groups in dropdown', async () => {
      await userModals.openEditUserModal('1');

      const groupsSelect = document.getElementById('user-groups') as HTMLSelectElement;
      const selectedValues = Array.from(groupsSelect.selectedOptions).map(o => o.value);
      expect(selectedValues).toContain('users');
    });

    it('should set current editing user', async () => {
      await userModals.openEditUserModal('1');

      expect(userState.currentEditingUser).toEqual(mockUser);
    });

    it('should handle API error', async () => {
      (api.getUser as jest.Mock).mockRejectedValue(new Error('Not found'));

      await userModals.openEditUserModal('1');

      expect(document.querySelector('.toast-error')).toBeTruthy();
    });

    it('should handle missing elements gracefully', async () => {
      document.body.innerHTML = '';
      await userModals.openEditUserModal('1');
      // Should not throw, just log error
    });
  });

  describe('closeUserModal', () => {
    it('should hide modal', () => {
      const modal = document.getElementById('user-modal');
      modal?.classList.remove('hidden');

      userModals.closeUserModal();

      expect(modal?.classList.contains('hidden')).toBe(true);
    });

    it('should clear current editing user', () => {
      userState.setCurrentEditingUser(mockUser as any);

      userModals.closeUserModal();

      expect(userState.currentEditingUser).toBeNull();
    });

    it('should handle missing modal gracefully', () => {
      document.body.innerHTML = '';
      expect(() => userModals.closeUserModal()).not.toThrow();
    });
  });

  describe('saveUser', () => {
    it('should create new user', async () => {
      jest.useFakeTimers();
      userModals.openCreateUserModal();

      (document.getElementById('user-email') as HTMLInputElement).value = 'new@test.com';
      (document.getElementById('user-password') as HTMLInputElement).value = 'SecurePass123!';

      // PR #912: >= 1 group required. Select the first option populated by openCreateUserModal.
      const gs7 = document.getElementById('user-groups') as HTMLSelectElement;
      if (gs7.options.length > 0) gs7.options[0]!.selected = true;

      const event = new Event('submit');
      event.preventDefault = jest.fn();

      await userModals.saveUser(event);

      expect(api.createUser).toHaveBeenCalledWith(
        expect.objectContaining({
          email: 'new@test.com',
          password: 'SecurePass123!',
        })
      );
      jest.useRealTimers();
    });

    it('should update existing user', async () => {
      jest.useFakeTimers();
      await userModals.openEditUserModal('1');

      (document.getElementById('user-email') as HTMLInputElement).value = 'updated@test.com';
      const event = new Event('submit');
      event.preventDefault = jest.fn();

      await userModals.saveUser(event);

      expect(api.updateUser).toHaveBeenCalledWith('1', {
        email: 'updated@test.com',
        // role removed: PR #912
        groups: ['users']
      });
      jest.useRealTimers();
    });

    it('should prevent default form submission', async () => {
      const event = new Event('submit');
      event.preventDefault = jest.fn();

      userModals.openCreateUserModal();
      (document.getElementById('user-email') as HTMLInputElement).value = 'test@test.com';
      (document.getElementById('user-password') as HTMLInputElement).value = 'SecurePass123!';

      await userModals.saveUser(event);

      expect(event.preventDefault).toHaveBeenCalled();
    });

    it('should validate password length for new user', async () => {
      userModals.openCreateUserModal();

      (document.getElementById('user-email') as HTMLInputElement).value = 'new@test.com';
      (document.getElementById('user-password') as HTMLInputElement).value = 'short';

      const event = new Event('submit');
      event.preventDefault = jest.fn();

      await userModals.saveUser(event);

      expect(api.createUser).not.toHaveBeenCalled();
      expect(document.querySelector('.toast-error')).toBeTruthy();
    });

    it('should allow empty password for new user (invite flow, issue #348)', async () => {
      // Empty password used to be a validation error; after issue #348
      // it triggers the invite flow -- createUser is called with an
      // empty password and the backend emails a set-password link.
      userModals.openCreateUserModal();

      (document.getElementById('user-email') as HTMLInputElement).value = 'new@test.com';
      (document.getElementById('user-password') as HTMLInputElement).value = '';

      // PR #912: >= 1 group required. Select first option so validation passes.
      const gs5 = document.getElementById('user-groups') as HTMLSelectElement;
      if (gs5.options.length > 0) gs5.options[0]!.selected = true;

      const event = new Event('submit');
      event.preventDefault = jest.fn();

      await userModals.saveUser(event);

      expect(api.createUser).toHaveBeenCalled();
      const callArg = (api.createUser as jest.Mock).mock.calls[0][0];
      expect(callArg.email).toBe('new@test.com');
      expect(callArg.password).toBe('');
      // No error toast on the invite-path success.
      expect(document.querySelector('.toast-error')).toBeNull();
    });


    it('should reject save when zero groups selected (PR #912 required-group validation)', async () => {
      userModals.openCreateUserModal();

      (document.getElementById('user-email') as HTMLInputElement).value = 'new@test.com';
      (document.getElementById('user-password') as HTMLInputElement).value = 'SecurePass123!';

      // Ensure no groups are selected
      const groupsSelect = document.getElementById('user-groups') as HTMLSelectElement;
      Array.from(groupsSelect.options).forEach(o => { o.selected = false; });

      const event = new Event('submit');
      event.preventDefault = jest.fn();

      await userModals.saveUser(event);

      // Backend would reject with 400; frontend should show a clear validation message.
      expect(api.createUser).not.toHaveBeenCalled();
      expect(document.querySelector('.toast-error')).toBeTruthy();
    });
    it('should close modal after save', async () => {
      jest.useFakeTimers();
      userModals.openCreateUserModal();

      (document.getElementById('user-email') as HTMLInputElement).value = 'new@test.com';
      (document.getElementById('user-password') as HTMLInputElement).value = 'SecurePass123!';

      const gs2 = document.getElementById('user-groups') as HTMLSelectElement;
      if (gs2.options.length > 0) gs2.options[0]!.selected = true;

      const event = new Event('submit');
      event.preventDefault = jest.fn();

      await userModals.saveUser(event);

      const modal = document.getElementById('user-modal');
      expect(modal?.classList.contains('hidden')).toBe(true);
      jest.useRealTimers();
    });

    it('should reload users after save', async () => {
      jest.useFakeTimers();
      userModals.openCreateUserModal();

      (document.getElementById('user-email') as HTMLInputElement).value = 'new@test.com';
      (document.getElementById('user-password') as HTMLInputElement).value = 'SecurePass123!';

      const gs3 = document.getElementById('user-groups') as HTMLSelectElement;
      if (gs3.options.length > 0) gs3.options[0]!.selected = true;

      const event = new Event('submit');
      event.preventDefault = jest.fn();

      await userModals.saveUser(event);

      expect(api.listUsers).toHaveBeenCalled();
      jest.useRealTimers();
    });

    it('should show success message on create', async () => {
      jest.useFakeTimers();
      userModals.openCreateUserModal();

      (document.getElementById('user-email') as HTMLInputElement).value = 'new@test.com';
      (document.getElementById('user-password') as HTMLInputElement).value = 'SecurePass123!';

      const gs4 = document.getElementById('user-groups') as HTMLSelectElement;
      if (gs4.options.length > 0) gs4.options[0]!.selected = true;

      const event = new Event('submit');
      event.preventDefault = jest.fn();

      await userModals.saveUser(event);

      expect(document.querySelector('.toast-success')).toBeTruthy();
      jest.useRealTimers();
    });

    it('should show success message on update', async () => {
      jest.useFakeTimers();
      await userModals.openEditUserModal('1');

      const event = new Event('submit');
      event.preventDefault = jest.fn();

      await userModals.saveUser(event);

      expect(document.querySelector('.toast-success')).toBeTruthy();
      jest.useRealTimers();
    });

    it('should handle save error', async () => {
      (api.createUser as jest.Mock).mockRejectedValue(new Error('Create failed'));

      userModals.openCreateUserModal();

      (document.getElementById('user-email') as HTMLInputElement).value = 'new@test.com';
      (document.getElementById('user-password') as HTMLInputElement).value = 'SecurePass123!';

      // PR #912: select a group so the required-group check passes and
      // the error comes from the API call, not the validation.
      const gs6 = document.getElementById('user-groups') as HTMLSelectElement;
      if (gs6.options.length > 0) gs6.options[0]!.selected = true;

      const event = new Event('submit');
      event.preventDefault = jest.fn();

      await userModals.saveUser(event);

      expect(document.querySelector('.toast-error')).toBeTruthy();
      expect(document.querySelector('.toast-error')?.textContent).toContain('Create failed');
    });

    it('should include selected groups', async () => {
      jest.useFakeTimers();
      userModals.openCreateUserModal();

      (document.getElementById('user-email') as HTMLInputElement).value = 'new@test.com';
      (document.getElementById('user-password') as HTMLInputElement).value = 'SecurePass123!';

      const groupsSelect = document.getElementById('user-groups') as HTMLSelectElement;
      if (groupsSelect.options[0]) {
        groupsSelect.options[0].selected = true; // Select 'admins'
      }

      const event = new Event('submit');
      event.preventDefault = jest.fn();

      await userModals.saveUser(event);

      expect(api.createUser).toHaveBeenCalledWith(
        expect.objectContaining({
          groups: ['admins']
        })
      );
      jest.useRealTimers();
    });
  });
});

// ============================================================================
// HANDLERS TESTS
// ============================================================================
describe('users/handlers', () => {
  const mockGroups = [
    { id: 'admins', name: 'Admins', permissions: [], description: '' },
    { id: 'users', name: 'Users', permissions: [], description: '' }
  ];

  beforeEach(() => {
    document.body.innerHTML = `
      <form id="user-form"></form>
      <input type="text" id="user-search" />
      <select id="user-role-filter">
        <option value="">All</option>
        <option value="admin">Admin</option>
        <option value="user">User</option>
      </select>
      <select id="user-mfa-filter">
        <option value="">All</option>
        <option value="enabled">Enabled</option>
        <option value="disabled">Disabled</option>
      </select>
      <select id="user-group-filter">
        <option value="">All Groups</option>
      </select>
      <button id="clear-filters-btn">Clear</button>
      <button id="bulk-delete-btn">Delete</button>
      <button id="bulk-role-btn">Change Role</button>
      <button id="bulk-group-btn">Add to Group</button>
      <div id="users-list"></div>
      <div id="user-stats"></div>
      <div id="bulk-actions-bar" class="hidden">
        <span id="selected-count">0</span>
      </div>
    `;

    userState.setAvailableGroups(mockGroups as any);
    userState.setAllUsers([]);
    userState.setFilteredUsers([]);
    userState.setSearchQuery('');
    userState.setRoleFilter('');
    userState.setMfaFilter('');
    userState.setGroupFilter('');
    userState.clearSelectedUserIds();

    jest.clearAllMocks();
  });

  describe('setupUserHandlers', () => {
    it('should set up global modal functions', () => {
      userHandlers.setupUserHandlers();

      expect((window as any).openCreateUserModal).toBeDefined();
      expect((window as any).closeUserModal).toBeDefined();
    });

    it('should set up form submit handler', () => {
      userHandlers.setupUserHandlers();

      const form = document.getElementById('user-form');
      expect(form?.onsubmit || form?.getAttribute('data-handler')).toBeDefined;
    });

    it('should set up search input handler', () => {
      userHandlers.setupUserHandlers();

      const searchInput = document.getElementById('user-search') as HTMLInputElement;
      searchInput.value = 'test';
      searchInput.dispatchEvent(new Event('input', { bubbles: true }));

      expect(userState.searchQuery).toBe('test');
    });

    it('should set up role filter handler', () => {
      userHandlers.setupUserHandlers();

      const roleFilter = document.getElementById('user-role-filter') as HTMLSelectElement;
      roleFilter.value = 'admin';
      roleFilter.dispatchEvent(new Event('change', { bubbles: true }));

      expect(userState.roleFilter).toBe('admin');
    });

    it('should set up mfa filter handler', () => {
      userHandlers.setupUserHandlers();

      const mfaFilter = document.getElementById('user-mfa-filter') as HTMLSelectElement;
      mfaFilter.value = 'enabled';
      mfaFilter.dispatchEvent(new Event('change', { bubbles: true }));

      expect(userState.mfaFilter).toBe('enabled');
    });

    it('should set up group filter handler', () => {
      userHandlers.setupUserHandlers();

      const groupFilter = document.getElementById('user-group-filter') as HTMLSelectElement;
      groupFilter.value = 'admins';
      groupFilter.dispatchEvent(new Event('change', { bubbles: true }));

      expect(userState.groupFilter).toBe('admins');
    });

    it('should set up clear filters button handler', () => {
      userState.setSearchQuery('test');
      userState.setRoleFilter('admin');

      userHandlers.setupUserHandlers();

      const clearBtn = document.getElementById('clear-filters-btn');
      clearBtn?.click();

      expect(userState.searchQuery).toBe('');
      expect(userState.roleFilter).toBe('');
    });

    it('should populate group filter dropdown', () => {
      userHandlers.setupUserHandlers();

      const groupFilter = document.getElementById('user-group-filter') as HTMLSelectElement;
      expect(groupFilter.options.length).toBe(3); // All Groups + 2 groups
    });

    it('should handle missing elements gracefully', () => {
      document.body.innerHTML = '';
      expect(() => userHandlers.setupUserHandlers()).not.toThrow();
    });
  });

  describe('bulk action handlers', () => {
    beforeEach(() => {
      (api.deleteUser as jest.Mock).mockResolvedValue({});
      (api.updateUser as jest.Mock).mockResolvedValue({});
      (api.listUsers as jest.Mock).mockResolvedValue({ users: [] });
      (api.listGroups as jest.Mock).mockResolvedValue({ groups: mockGroups });
      (global.confirm as jest.Mock).mockReturnValue(true);
      (global.prompt as jest.Mock) = jest.fn();
    });

    it('should set up bulk delete handler', async () => {
      userState.addSelectedUserId('1');

      userHandlers.setupUserHandlers();

      const bulkDeleteBtn = document.getElementById('bulk-delete-btn');
      bulkDeleteBtn?.click();

      // Wait for async operation
      await new Promise(resolve => setTimeout(resolve, 0));

      expect(api.deleteUser).toHaveBeenCalled();
    });

    it('bulk-role-btn is intentionally not wired (PR #912: role concept dropped)', async () => {
      // bulkChangeRole was removed. The handler does not wire bulk-role-btn
      // anymore. Clicking it must be a no-op rather than throwing.
      userState.addSelectedUserId('1');
      userHandlers.setupUserHandlers();
      const bulkRoleBtn = document.getElementById('bulk-role-btn');
      if (bulkRoleBtn) bulkRoleBtn.click();
      await new Promise(resolve => setTimeout(resolve, 0));
      expect(api.updateUser).not.toHaveBeenCalled();
    });

    it('should set up bulk group handler', async () => {
      const mockUsers = [{ id: '1', email: 'test@test.com', groups: [], mfa_enabled: false }];
      userState.setAllUsers(mockUsers as any);
      userState.addSelectedUserId('1');
      (global.prompt as jest.Mock).mockReturnValue('admins');

      userHandlers.setupUserHandlers();

      const bulkGroupBtn = document.getElementById('bulk-group-btn');
      bulkGroupBtn?.click();

      await new Promise(resolve => setTimeout(resolve, 0));

      expect(api.updateUser).toHaveBeenCalled();
    });

    it('should handle cancelled group prompt', async () => {
      userState.addSelectedUserId('1');
      (global.prompt as jest.Mock).mockReturnValue(null);

      userHandlers.setupUserHandlers();

      const bulkGroupBtn = document.getElementById('bulk-group-btn');
      bulkGroupBtn?.click();

      await new Promise(resolve => setTimeout(resolve, 0));

      expect(api.updateUser).not.toHaveBeenCalled();
    });
  });
});

// ============================================================================
// INTEGRATION TESTS
// ============================================================================
describe('users module integration', () => {
  const mockUsers = [
    { id: '1', email: 'admin@test.com', groups: ['00000000-0000-5000-8000-000000000001'], mfa_enabled: true, created_at: '2024-01-01' },
    { id: '2', email: 'user@test.com', groups: [], mfa_enabled: false, created_at: '2024-01-02' }
  ];

  const mockGroups = [
    { id: 'admins', name: 'Admins', permissions: [], description: '' }
  ];

  beforeEach(() => {
    document.body.innerHTML = `
      <div id="users-list"></div>
      <div id="groups-list"></div>
      <div id="user-stats"></div>
      <input type="text" id="user-search" />
      <select id="user-role-filter"><option value="">All</option></select>
      <select id="user-mfa-filter"><option value="">All</option></select>
      <select id="user-group-filter"><option value="">All</option></select>
      <div id="bulk-actions-bar" class="hidden">
        <span id="selected-count">0</span>
      </div>
    `;

    (api.listUsers as jest.Mock).mockResolvedValue({ users: mockUsers });
    (api.listGroups as jest.Mock).mockResolvedValue({ groups: mockGroups });

    userState.setAllUsers([]);
    userState.setFilteredUsers([]);
    userState.setAvailableGroups([]);
    userState.clearSelectedUserIds();
    userState.setSearchQuery('');
    userState.setRoleFilter('');
    userState.setMfaFilter('');
    userState.setGroupFilter('');

    jest.clearAllMocks();
  });

  it('should load users and render them', async () => {
    await userActions.loadUsers();

    const container = document.getElementById('users-list');
    expect(container?.innerHTML).toContain('admin@test.com');
    expect(container?.innerHTML).toContain('user@test.com');
  });

  it('should filter and re-render users', async () => {
    await userActions.loadUsers();

    userFilters.handleUserSearch('admin');

    const container = document.getElementById('users-list');
    expect(container?.innerHTML).toContain('admin@test.com');
    expect(container?.innerHTML).not.toContain('user@test.com');
  });

  it('should update stats when filtering', async () => {
    await userActions.loadUsers();

    const statsContainer = document.getElementById('user-stats');
    expect(statsContainer?.innerHTML).toContain('2'); // Total users

    userFilters.handleFilterChange('role', 'admin');

    expect(statsContainer?.innerHTML).toContain('1'); // Showing filtered
  });

  it('should track user selection state', async () => {
    await userActions.loadUsers();

    userState.addSelectedUserId('1');

    const container = document.getElementById('users-list');
    // Re-render to show selection
    userList.renderUsers(userState.filteredUsers);

    expect(container?.querySelector('.row-selected')).toBeTruthy();
  });

  it('should update bulk actions bar on selection', async () => {
    await userActions.loadUsers();

    userState.addSelectedUserId('1');
    userList.updateBulkActionsBar();

    const bar = document.getElementById('bulk-actions-bar');
    expect(bar?.classList.contains('hidden')).toBe(false);
  });
});

// ============================================================================
// BULK TOOLBAR TESTS (issue #974)
// ============================================================================
describe('users bulk-actions toolbar', () => {
  const mockGroups = [
    { id: 'admins', name: 'Admins', permissions: [], description: '', allowed_accounts: [] },
    { id: 'developers', name: 'Developers', permissions: [], description: '', allowed_accounts: [] },
  ];

  function buildToolbarDom(): void {
    document.body.innerHTML = `
      <form id="user-form"></form>
      <input type="text" id="user-search" />
      <select id="user-role-filter"><option value="">All</option></select>
      <select id="user-mfa-filter"><option value="">All</option></select>
      <select id="user-group-filter"><option value="">All Groups</option></select>
      <button id="clear-filters-btn">Clear</button>
      <div id="users-list"></div>
      <div id="user-stats"></div>
      <div id="bulk-actions-bar" class="hidden">
        <span id="selected-count">0</span>
        <button id="bulk-delete-btn">Delete selected</button>
        <select id="bulk-group-select">
          <option value="">Add to group...</option>
        </select>
      </div>
    `;
  }

  beforeEach(() => {
    buildToolbarDom();
    userState.setAllUsers([]);
    userState.setFilteredUsers([]);
    userState.setAvailableGroups(mockGroups as any);
    userState.clearSelectedUserIds();
    jest.clearAllMocks();
  });

  describe('toolbar visibility', () => {
    it('should be hidden when no users are selected', () => {
      userList.updateBulkActionsBar();

      const bar = document.getElementById('bulk-actions-bar');
      expect(bar?.classList.contains('hidden')).toBe(true);
    });

    it('should be visible with correct count when one user is selected', () => {
      userState.addSelectedUserId('user-1');

      userList.updateBulkActionsBar();

      const bar = document.getElementById('bulk-actions-bar');
      expect(bar?.classList.contains('hidden')).toBe(false);
      const count = document.getElementById('selected-count');
      expect(count?.textContent).toBe('1');
    });

    it('should be visible with correct count when multiple users are selected', () => {
      userState.addSelectedUserId('user-1');
      userState.addSelectedUserId('user-2');
      userState.addSelectedUserId('user-3');

      userList.updateBulkActionsBar();

      const bar = document.getElementById('bulk-actions-bar');
      expect(bar?.classList.contains('hidden')).toBe(false);
      const count = document.getElementById('selected-count');
      expect(count?.textContent).toBe('3');
    });

    it('should become hidden again after all rows are unchecked', () => {
      userState.addSelectedUserId('user-1');
      userList.updateBulkActionsBar();

      const barBefore = document.getElementById('bulk-actions-bar');
      expect(barBefore?.classList.contains('hidden')).toBe(false);

      userState.clearSelectedUserIds();
      userList.updateBulkActionsBar();

      const barAfter = document.getElementById('bulk-actions-bar');
      expect(barAfter?.classList.contains('hidden')).toBe(true);
    });
  });

  describe('bulk-delete-btn', () => {
    it('should call bulkDeleteUsers when clicked via setupUserHandlers', async () => {
      const mockUser = { id: '1', email: 'u@t.com', role: 'user', groups: [], mfa_enabled: false };
      userState.setAllUsers([mockUser] as any);
      userState.addSelectedUserId('1');

      (api.deleteUser as jest.Mock).mockResolvedValue({});
      (api.listUsers as jest.Mock).mockResolvedValue({ users: [] });
      (api.listGroups as jest.Mock).mockResolvedValue({ groups: [] });

      userHandlers.setupUserHandlers();

      const bulkDeleteBtn = document.getElementById('bulk-delete-btn');
      bulkDeleteBtn?.click();

      await new Promise(resolve => setTimeout(resolve, 0));

      expect(api.deleteUser).toHaveBeenCalledWith('1');
    });
  });

  describe('bulk-group-select', () => {
    it('should populate with available groups', () => {
      userList.populateBulkGroupSelect();

      const select = document.getElementById('bulk-group-select') as HTMLSelectElement;
      // placeholder + 2 groups
      expect(select.options.length).toBe(3);
      expect(select.options[1]?.value).toBe('admins');
      expect(select.options[1]?.text).toBe('Admins');
      expect(select.options[2]?.value).toBe('developers');
      expect(select.options[2]?.text).toBe('Developers');
    });

    it('should call bulkAddToGroup with selected group id on change via setupUserHandlers', async () => {
      const mockUser = { id: '1', email: 'test@test.com', role: 'user', groups: [], mfa_enabled: false };
      userState.setAllUsers([mockUser] as any);
      userState.addSelectedUserId('1');

      (api.updateUser as jest.Mock).mockResolvedValue({});
      (api.listUsers as jest.Mock).mockResolvedValue({ users: [mockUser] });
      (api.listGroups as jest.Mock).mockResolvedValue({ groups: mockGroups });

      userHandlers.setupUserHandlers();

      const select = document.getElementById('bulk-group-select') as HTMLSelectElement;
      select.value = 'admins';
      select.dispatchEvent(new Event('change', { bubbles: true }));

      await new Promise(resolve => setTimeout(resolve, 0));

      expect(api.updateUser).toHaveBeenCalledWith('1', { groups: ['admins'] });
    });

    it('should not call bulkAddToGroup when placeholder is selected', async () => {
      userState.addSelectedUserId('1');
      (api.updateUser as jest.Mock).mockResolvedValue({});

      userHandlers.setupUserHandlers();

      const select = document.getElementById('bulk-group-select') as HTMLSelectElement;
      select.value = '';
      select.dispatchEvent(new Event('change', { bubbles: true }));

      await new Promise(resolve => setTimeout(resolve, 0));

      expect(api.updateUser).not.toHaveBeenCalled();
    });
  });
});

// ============================================================================
// GROUP ASSIGNMENT UX TESTS (issue #998)
// ============================================================================
describe('group assignment UX (issue #998)', () => {
  const mockUsers = [
    { id: '1', email: 'alice@test.com', groups: ['g1'], mfa_enabled: false },
    { id: '2', email: 'bob@test.com', groups: [], mfa_enabled: false },
  ];
  const mockGroups = [
    { id: 'g1', name: 'Admins', permissions: [], description: '', allowed_accounts: [] },
    { id: 'g2', name: 'Editors', permissions: [], description: '', allowed_accounts: [] },
  ];

  function buildDom(): void {
    document.body.innerHTML = `
      <div id="users-list"></div>
      <div id="user-stats"></div>
      <div id="bulk-actions-bar" class="hidden">
        <span id="selected-count">0</span>
      </div>
    `;
  }

  beforeEach(() => {
    buildDom();
    userState.setAllUsers(mockUsers as any);
    userState.setFilteredUsers(mockUsers as any);
    userState.setAvailableGroups(mockGroups as any);
    userState.clearSelectedUserIds();
    (api.updateUser as jest.Mock).mockResolvedValue({});
    jest.clearAllMocks();
  });

  afterEach(() => {
    jest.useRealTimers();
  });

  // -----------------------------------------------------------------------
  // 1. Single toast per group update
  // -----------------------------------------------------------------------
  describe('single toast per group update', () => {
    it('emits exactly one success toast when a group checkbox is toggled once', async () => {
      jest.useFakeTimers();
      userList.renderUsers(mockUsers as any);

      // Expand user "1" so the panel + checkboxes are in the DOM.
      const expandBtn = document.querySelector<HTMLButtonElement>(
        '.user-expand-btn[data-user-id="1"]',
      );
      expect(expandBtn).toBeTruthy();
      expandBtn!.click();

      // Fire the change event on the group-assign checkbox.
      const cb = document.querySelector<HTMLInputElement>(
        '.group-assign-checkbox[data-user-id="1"][data-group-id="g2"]',
      );
      expect(cb).toBeTruthy();
      cb!.checked = true;
      cb!.dispatchEvent(new Event('change', { bubbles: true }));

      // Flush the async toggleUserGroup promise.
      await Promise.resolve();
      await Promise.resolve();

      const toasts = document.querySelectorAll('.toast-success');
      expect(toasts.length).toBe(1);
    });

    it('still emits exactly one toast even after multiple renderUsers calls (listener-stacking regression)', async () => {
      // Pre-fix: each renderUsers call stacked a new document-level listener,
      // so N renders produced N toasts for a single toggle.
      jest.useFakeTimers();

      // Simulate three renderUsers calls (as would happen after 3 prior group
      // toggles that called loadUsers -> renderUsers).
      userList.renderUsers(mockUsers as any);
      userList.renderUsers(mockUsers as any);
      userList.renderUsers(mockUsers as any);

      const expandBtn = document.querySelector<HTMLButtonElement>(
        '.user-expand-btn[data-user-id="1"]',
      );
      expandBtn!.click();

      const cb = document.querySelector<HTMLInputElement>(
        '.group-assign-checkbox[data-user-id="1"][data-group-id="g2"]',
      );
      expect(cb).toBeTruthy();
      cb!.checked = true;
      cb!.dispatchEvent(new Event('change', { bubbles: true }));

      await Promise.resolve();
      await Promise.resolve();

      const toasts = document.querySelectorAll('.toast-success');
      // Must be exactly 1, not 3 (one per stacked listener).
      expect(toasts.length).toBe(1);
    });
  });

  // -----------------------------------------------------------------------
  // 2. Expand panel stays open after a group checkbox toggle
  // -----------------------------------------------------------------------
  describe('expand panel stays open after checkbox toggle', () => {
    it('does not collapse the expand row after a group toggle', async () => {
      jest.useFakeTimers();
      userList.renderUsers(mockUsers as any);

      const expandBtn = document.querySelector<HTMLButtonElement>(
        '.user-expand-btn[data-user-id="1"]',
      );
      expandBtn!.click();

      // Verify it is open.
      const expandRow = document.querySelector<HTMLElement>(
        'tr.user-expand-row[data-user-id="1"]',
      );
      expect(expandRow?.classList.contains('hidden')).toBe(false);

      // Toggle a group checkbox.
      const cb = document.querySelector<HTMLInputElement>(
        '.group-assign-checkbox[data-user-id="1"][data-group-id="g2"]',
      );
      cb!.checked = true;
      cb!.dispatchEvent(new Event('change', { bubbles: true }));

      await Promise.resolve();
      await Promise.resolve();

      // The row must still be visible - the fix does NOT call renderUsers (which
      // would replace the whole table and reset all rows to hidden).
      expect(expandRow?.classList.contains('hidden')).toBe(false);
    });

    it('keeps the expand panel in the DOM after a successful toggle', async () => {
      jest.useFakeTimers();
      userList.renderUsers(mockUsers as any);

      const expandBtn = document.querySelector<HTMLButtonElement>(
        '.user-expand-btn[data-user-id="1"]',
      );
      expandBtn!.click();

      const panelBefore = document.querySelector('.user-expand-panel[data-user-id="1"]');
      expect(panelBefore).toBeTruthy();

      const cb = document.querySelector<HTMLInputElement>(
        '.group-assign-checkbox[data-user-id="1"][data-group-id="g2"]',
      );
      cb!.checked = true;
      cb!.dispatchEvent(new Event('change', { bubbles: true }));

      await Promise.resolve();
      await Promise.resolve();

      // The panel element must still exist (not destroyed by a table re-render).
      const panelAfter = document.querySelector('.user-expand-panel[data-user-id="1"]');
      expect(panelAfter).toBeTruthy();
    });
  });

  // -----------------------------------------------------------------------
  // 3. Expand button reacts reliably (single listener bound per render)
  // -----------------------------------------------------------------------
  describe('expand button listener bound once', () => {
    it('expand button opens the panel on the first click after renderUsers', () => {
      userList.renderUsers(mockUsers as any);

      const expandBtn = document.querySelector<HTMLButtonElement>(
        '.user-expand-btn[data-user-id="1"]',
      );
      expect(expandBtn).toBeTruthy();

      // First click must open the panel.
      expandBtn!.click();

      const expandRow = document.querySelector<HTMLElement>(
        'tr.user-expand-row[data-user-id="1"]',
      );
      expect(expandRow?.classList.contains('hidden')).toBe(false);
      expect(expandBtn!.getAttribute('aria-expanded')).toBe('true');
    });

    it('expand button toggles correctly after multiple renderUsers calls', () => {
      // Each renderUsers replaces the table innerHTML, so each new expand
      // button element gets exactly one click listener.
      userList.renderUsers(mockUsers as any);
      userList.renderUsers(mockUsers as any);
      userList.renderUsers(mockUsers as any);

      const expandBtn = document.querySelector<HTMLButtonElement>(
        '.user-expand-btn[data-user-id="2"]',
      );
      expandBtn!.click();

      const expandRow = document.querySelector<HTMLElement>(
        'tr.user-expand-row[data-user-id="2"]',
      );
      expect(expandRow?.classList.contains('hidden')).toBe(false);

      // Second click must collapse.
      expandBtn!.click();
      expect(expandRow?.classList.contains('hidden')).toBe(true);
    });
  });

  // -----------------------------------------------------------------------
  // 4. In-memory state is patched after toggle (no full round-trip)
  // -----------------------------------------------------------------------
  describe('in-memory state updated after toggle', () => {
    it('patches allUsers so the toggled group appears in user.groups', async () => {
      jest.useFakeTimers();
      userList.renderUsers(mockUsers as any);

      const expandBtn = document.querySelector<HTMLButtonElement>(
        '.user-expand-btn[data-user-id="2"]',
      );
      expandBtn!.click();

      // Bob starts with no groups; add g1.
      const cb = document.querySelector<HTMLInputElement>(
        '.group-assign-checkbox[data-user-id="2"][data-group-id="g1"]',
      );
      cb!.checked = true;
      cb!.dispatchEvent(new Event('change', { bubbles: true }));

      await Promise.resolve();
      await Promise.resolve();

      const updated = userState.allUsers.find(u => u.id === '2');
      expect(updated?.groups).toContain('g1');
    });

    it('patches allUsers so the removed group disappears from user.groups', async () => {
      jest.useFakeTimers();
      userList.renderUsers(mockUsers as any);

      const expandBtn = document.querySelector<HTMLButtonElement>(
        '.user-expand-btn[data-user-id="1"]',
      );
      expandBtn!.click();

      // Alice starts with ['g1']; remove g1.
      const cb = document.querySelector<HTMLInputElement>(
        '.group-assign-checkbox[data-user-id="1"][data-group-id="g1"]',
      );
      cb!.checked = false;
      cb!.dispatchEvent(new Event('change', { bubbles: true }));

      await Promise.resolve();
      await Promise.resolve();

      const updated = userState.allUsers.find(u => u.id === '1');
      expect(updated?.groups).not.toContain('g1');
    });
  });
});

// ============================================================================
// GROUP UNION HINT (issue #1001)
// ============================================================================
describe('group union permission hint (issue #1001)', () => {
  const mockUsers = [
    { id: '1', email: 'alice@test.com', groups: ['g1'], mfa_enabled: false },
  ];
  const mockGroups = [
    { id: 'g1', name: 'Admins', permissions: [], description: '', allowed_accounts: [] },
    { id: 'g2', name: 'Viewers', permissions: [], description: '', allowed_accounts: [] },
  ];

  beforeEach(() => {
    document.body.innerHTML = `
      <div id="users-list"></div>
      <div id="user-stats"></div>
      <div id="bulk-actions-bar" class="hidden">
        <span id="selected-count">0</span>
      </div>
    `;
    userState.setAllUsers(mockUsers as any);
    userState.setFilteredUsers(mockUsers as any);
    userState.setAvailableGroups(mockGroups as any);
    userState.clearSelectedUserIds();
    jest.clearAllMocks();
  });

  it('renders the union hint in the group membership expand panel', () => {
    userList.renderUsers(mockUsers as any);

    const expandBtn = document.querySelector<HTMLButtonElement>(
      '.user-expand-btn[data-user-id="1"]',
    );
    expect(expandBtn).toBeTruthy();
    expandBtn!.click();

    const panel = document.querySelector('.user-expand-panel[data-user-id="1"]');
    expect(panel).toBeTruthy();
    expect(panel!.textContent).toContain('combined (union) of all selected groups');
  });

  it('hint appears in expand-panel-groups section, before the checkbox list', () => {
    userList.renderUsers(mockUsers as any);

    const expandBtn = document.querySelector<HTMLButtonElement>(
      '.user-expand-btn[data-user-id="1"]',
    );
    expandBtn!.click();

    const groupsSection = document.querySelector('.expand-panel-groups');
    expect(groupsSection).toBeTruthy();
    expect(groupsSection!.textContent).toContain('combined (union) of all selected groups');
  });
});
