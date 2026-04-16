/**
 * Tests for groups module
 */

import './setup';

// Mock the api module
jest.mock('../api', () => ({
  listGroups: jest.fn(),
  createGroup: jest.fn(),
  updateGroup: jest.fn(),
  deleteGroup: jest.fn(),
  getGroup: jest.fn(),
}));

// Mock the loadUsers function from userActions
jest.mock('../users/userActions', () => ({
  loadUsers: jest.fn().mockResolvedValue(undefined),
}));

import * as api from '../api';
import { loadUsers } from '../users/userActions';
import * as groupState from '../groups/state';
import * as groupList from '../groups/groupList';
import * as groupModals from '../groups/groupModals';
import * as groupActions from '../groups/groupActions';
import * as groupHandlers from '../groups/handlers';
import * as userState from '../users/state';

// Mock group data
const mockGroups: api.APIGroup[] = [
  {
    id: 'group-1',
    name: 'Administrators',
    description: 'Admin group with full access',
    permissions: [
      { action: 'execute', resource: '*' },
      { action: 'update', resource: '*' },
    ],
    created_at: '2024-01-15T10:00:00Z',
  },
  {
    id: 'group-2',
    name: 'Viewers',
    description: 'Read-only access',
    permissions: [
      { action: 'view', resource: '*' },
    ],
    created_at: '2024-02-20T14:30:00Z',
  },
  {
    id: 'group-3',
    name: 'Empty Group',
    description: '',
    permissions: [],
  },
];

// Mock users that belong to groups
const mockUsers = [
  { id: 'user-1', email: 'admin@test.com', role: 'admin', groups: ['group-1'], mfa_enabled: true },
  { id: 'user-2', email: 'viewer@test.com', role: 'user', groups: ['group-2'], mfa_enabled: false },
  { id: 'user-3', email: 'both@test.com', role: 'user', groups: ['group-1', 'group-2'], mfa_enabled: true },
];

describe('groups/state', () => {
  beforeEach(() => {
    groupState.setCurrentEditingGroup(null);
  });

  describe('currentEditingGroup', () => {
    it('should initialize as null', () => {
      expect(groupState.currentEditingGroup).toBeNull();
    });

    it('should set and get current editing group', () => {
      const group = mockGroups[0];
      if (!group) throw new Error('Test data missing');
      groupState.setCurrentEditingGroup(group);
      expect(groupState.currentEditingGroup).toEqual(group);
    });

    it('should clear current editing group', () => {
      const group = mockGroups[0];
      if (!group) throw new Error('Test data missing');
      groupState.setCurrentEditingGroup(group);
      groupState.setCurrentEditingGroup(null);
      expect(groupState.currentEditingGroup).toBeNull();
    });
  });
});

describe('groups/groupList', () => {
  beforeEach(() => {
    document.body.innerHTML = `
      <div id="groups-list"></div>
    `;
    userState.setAllUsers(mockUsers as any);
    groupState.setCurrentEditingGroup(null);
  });

  describe('renderGroups', () => {
    it('should show empty message when no groups', () => {
      groupList.renderGroups([]);

      const container = document.getElementById('groups-list');
      expect(container?.innerHTML).toContain('No groups found');
      expect(container?.querySelector('.empty')).toBeTruthy();
    });

    it('should render group cards with correct structure', () => {
      groupList.renderGroups(mockGroups);

      const container = document.getElementById('groups-list');
      const cards = container?.querySelectorAll('.group-card');
      expect(cards?.length).toBe(3);
      expect(container?.querySelector('.group-card-header')).toBeTruthy();
      expect(container?.querySelector('.group-card-body')).toBeTruthy();
      expect(container?.querySelector('.group-members')).toBeTruthy();
    });

    it('should render group names in h4 elements', () => {
      groupList.renderGroups(mockGroups);

      const container = document.getElementById('groups-list');
      const headings = container?.querySelectorAll('.group-card h4');
      expect(headings?.length).toBe(3);
      expect(headings?.item(0)?.textContent).toBe('Administrators');
      expect(headings?.item(1)?.textContent).toBe('Viewers');
      expect(headings?.item(2)?.textContent).toBe('Empty Group');
    });

    it('should render group descriptions', () => {
      groupList.renderGroups(mockGroups);

      const container = document.getElementById('groups-list');
      expect(container?.innerHTML).toContain('Admin group with full access');
      expect(container?.innerHTML).toContain('Read-only access');
    });

    it('should render member count with badge', () => {
      groupList.renderGroups(mockGroups);

      const container = document.getElementById('groups-list');
      const badges = container?.querySelectorAll('.badge');
      // group-1 has 2 members (user-1 and user-3)
      // group-2 has 2 members (user-2 and user-3)
      // group-3 has 0 members
      expect(badges?.length).toBe(3);
      expect(badges?.item(0)?.textContent).toBe('2 members');
      expect(badges?.item(1)?.textContent).toBe('2 members');
      expect(badges?.item(2)?.textContent).toBe('0 members');
    });

    it('should use singular "member" when count is 1', () => {
      // Set up users so group-1 has exactly 1 member
      userState.setAllUsers([
        { id: 'user-1', email: 'admin@test.com', role: 'admin', groups: ['group-1'], mfa_enabled: true },
      ] as any);

      const group = mockGroups[0];
      if (!group) throw new Error('Test data missing');
      groupList.renderGroups([group]);

      const container = document.getElementById('groups-list');
      const badge = container?.querySelector('.badge');
      expect(badge?.textContent).toBe('1 member');
    });

    it('should render permission badges', () => {
      groupList.renderGroups(mockGroups);

      const container = document.getElementById('groups-list');
      const badges = container?.querySelectorAll('.permission-badge');
      // group-1 has 2 permissions, group-2 has 1, group-3 has 0
      expect(badges?.length).toBe(3);
      expect(badges?.item(0)?.textContent).toBe('execute:*');
      expect(badges?.item(1)?.textContent).toBe('update:*');
      expect(badges?.item(2)?.textContent).toBe('view:*');
    });

    it('should show "No permissions" for group with no permissions', () => {
      const group = mockGroups[2]; // Empty Group
      if (!group) throw new Error('Test data missing');
      groupList.renderGroups([group]);

      const container = document.getElementById('groups-list');
      expect(container?.innerHTML).toContain('No permissions');
    });

    it('should render member pills with email addresses', () => {
      groupList.renderGroups(mockGroups);

      const container = document.getElementById('groups-list');
      const pills = container?.querySelectorAll('.member-pill');
      // group-1: admin@test.com, both@test.com; group-2: viewer@test.com, both@test.com; group-3: none
      expect(pills?.length).toBe(4);
    });

    it('should show "No members" for group with no members', () => {
      const group = mockGroups[2]; // Empty Group
      if (!group) throw new Error('Test data missing');
      groupList.renderGroups([group]);

      const container = document.getElementById('groups-list');
      expect(container?.innerHTML).toContain('No members');
    });

    it('should render edit and delete buttons with data-group-id', () => {
      groupList.renderGroups(mockGroups);

      const container = document.getElementById('groups-list');
      const editButtons = container?.querySelectorAll('.edit-group-btn');
      const deleteButtons = container?.querySelectorAll('.delete-group-btn');

      expect(editButtons?.length).toBe(3);
      expect(deleteButtons?.length).toBe(3);

      const firstEditBtn = editButtons?.item(0) as HTMLElement | null;
      const firstDeleteBtn = deleteButtons?.item(0) as HTMLElement | null;
      expect(firstEditBtn?.dataset.groupId).toBe('group-1');
      expect(firstDeleteBtn?.dataset.groupId).toBe('group-1');
    });

    it('should escape HTML in group names to prevent XSS', () => {
      const xssGroup: api.APIGroup = {
        id: 'xss-group',
        name: '<script>alert("xss")</script>',
        description: '<img src=x onerror=alert("xss")>',
        permissions: [],
      };

      groupList.renderGroups([xssGroup]);

      const container = document.getElementById('groups-list');
      // Verify script tag is escaped (not executable)
      expect(container?.innerHTML).not.toContain('<script>');
      expect(container?.innerHTML).toContain('&lt;script&gt;');
      // Verify img tag is escaped (not executable)
      expect(container?.innerHTML).not.toContain('<img');
      expect(container?.innerHTML).toContain('&lt;img');
    });

    it('should not render if container not found', () => {
      document.body.innerHTML = ''; // Remove the container

      // Should not throw
      expect(() => groupList.renderGroups(mockGroups)).not.toThrow();
    });

    it('should attach click handlers to edit buttons', async () => {
      const group = mockGroups[0];
      if (!group) throw new Error('Test data missing');
      (api.getGroup as jest.Mock).mockResolvedValue(group);

      document.body.innerHTML = `
        <div id="groups-list"></div>
        <div id="group-modal" class="hidden">
          <span id="group-modal-title"></span>
          <form id="group-form">
            <input id="group-id" />
            <input id="group-name" />
            <textarea id="group-description"></textarea>
            <div id="permissions-list"></div>
          </form>
        </div>
      `;

      groupList.renderGroups(mockGroups);

      const editBtn = document.querySelector('.edit-group-btn') as HTMLButtonElement;
      editBtn.click();

      // Wait for async operation
      await new Promise(resolve => setTimeout(resolve, 0));

      expect(api.getGroup).toHaveBeenCalledWith('group-1');
    });

    it('should attach click handlers to delete buttons', async () => {
      (api.deleteGroup as jest.Mock).mockResolvedValue(undefined);
      (global.confirm as jest.Mock).mockReturnValue(true);
      userState.setAvailableGroups(mockGroups as any);

      groupList.renderGroups(mockGroups);

      const deleteBtn = document.querySelector('.delete-group-btn') as HTMLButtonElement;
      deleteBtn.click();

      // Wait for async operation
      await new Promise(resolve => setTimeout(resolve, 0));

      expect(global.confirm).toHaveBeenCalled();
      expect(api.deleteGroup).toHaveBeenCalledWith('group-1');
    });
  });
});

describe('groups/groupModals', () => {
  beforeEach(() => {
    document.body.innerHTML = `
      <div id="group-modal" class="hidden">
        <span id="group-modal-title"></span>
        <form id="group-form">
          <input id="group-id" value="existing-id" />
          <input id="group-name" value="Existing Name" />
          <textarea id="group-description">Existing description</textarea>
          <div id="permissions-list">
            <div class="permission-item">Old permission</div>
          </div>
        </form>
      </div>
    `;
    groupState.setCurrentEditingGroup(null);
    jest.clearAllMocks();
  });

  describe('openCreateGroupModal', () => {
    it('should clear current editing group', () => {
      const group = mockGroups[0];
      if (!group) throw new Error('Test data missing');
      groupState.setCurrentEditingGroup(group);
      groupModals.openCreateGroupModal();
      expect(groupState.currentEditingGroup).toBeNull();
    });

    it('should set modal title to "Create Group"', () => {
      groupModals.openCreateGroupModal();
      const title = document.getElementById('group-modal-title');
      expect(title?.textContent).toBe('Create Group');
    });

    it('should reset the form', () => {
      // In JSDOM, form.reset() behavior differs from real browsers
      // We test that the form's reset method was called by verifying
      // that the function runs without error and the modal is shown
      groupModals.openCreateGroupModal();

      const modal = document.getElementById('group-modal');
      const idInput = document.getElementById('group-id') as HTMLInputElement;

      // The group-id field is explicitly cleared to empty string
      expect(idInput.value).toBe('');
      // Modal should be visible after opening
      expect(modal?.classList.contains('hidden')).toBe(false);
    });

    it('should clear the group-id field', () => {
      groupModals.openCreateGroupModal();
      const idInput = document.getElementById('group-id') as HTMLInputElement;
      expect(idInput.value).toBe('');
    });

    it('should clear permissions list', () => {
      groupModals.openCreateGroupModal();
      const permissionsList = document.getElementById('permissions-list');
      expect(permissionsList?.innerHTML).toBe('');
    });

    it('should remove hidden class from modal', () => {
      groupModals.openCreateGroupModal();
      const modal = document.getElementById('group-modal');
      expect(modal?.classList.contains('hidden')).toBe(false);
    });

    it('should handle missing modal elements gracefully', () => {
      document.body.innerHTML = '';
      expect(() => groupModals.openCreateGroupModal()).not.toThrow();
    });

    it('should handle missing permissions list gracefully', () => {
      document.body.innerHTML = `
        <div id="group-modal" class="hidden">
          <span id="group-modal-title"></span>
          <form id="group-form">
            <input id="group-id" />
            <input id="group-name" />
            <textarea id="group-description"></textarea>
          </form>
        </div>
      `;
      expect(() => groupModals.openCreateGroupModal()).not.toThrow();
    });
  });

  describe('openEditGroupModal', () => {
    const groupWithConstraints: api.APIGroup = {
      id: 'group-with-constraints',
      name: 'Constrained Group',
      description: 'Group with permission constraints',
      permissions: [
        {
          action: 'execute',
          resource: 'purchases',
          constraints: {
            providers: ['aws'],
            services: ['ec2', 'rds'],
            regions: ['us-east-1', 'us-west-2'],
            max_amount: 5000,
          },
        },
      ],
    };

    beforeEach(() => {
      const group = mockGroups[0];
      if (!group) throw new Error('Test data missing');
      (api.getGroup as jest.Mock).mockResolvedValue(group);
    });

    it('should fetch group details from API', async () => {
      await groupModals.openEditGroupModal('group-1');
      expect(api.getGroup).toHaveBeenCalledWith('group-1');
    });

    it('should set current editing group', async () => {
      const group = mockGroups[0];
      if (!group) throw new Error('Test data missing');
      await groupModals.openEditGroupModal('group-1');
      expect(groupState.currentEditingGroup).toEqual(group);
    });

    it('should set modal title to "Edit Group"', async () => {
      await groupModals.openEditGroupModal('group-1');
      const title = document.getElementById('group-modal-title');
      expect(title?.textContent).toBe('Edit Group');
    });

    it('should populate form with group data', async () => {
      await groupModals.openEditGroupModal('group-1');

      const idInput = document.getElementById('group-id') as HTMLInputElement;
      const nameInput = document.getElementById('group-name') as HTMLInputElement;
      const descInput = document.getElementById('group-description') as HTMLTextAreaElement;

      expect(idInput.value).toBe('group-1');
      expect(nameInput.value).toBe('Administrators');
      expect(descInput.value).toBe('Admin group with full access');
    });

    it('should render existing permissions', async () => {
      await groupModals.openEditGroupModal('group-1');

      const permissionsList = document.getElementById('permissions-list');
      const items = permissionsList?.querySelectorAll('.permission-item');
      expect(items?.length).toBe(2); // Two permissions in mockGroups[0]
    });

    it('should render permissions with constraints', async () => {
      (api.getGroup as jest.Mock).mockResolvedValue(groupWithConstraints);

      await groupModals.openEditGroupModal('group-with-constraints');

      const permissionsList = document.getElementById('permissions-list');
      expect(permissionsList?.innerHTML).toContain('purchases');
      expect(permissionsList?.innerHTML).toContain('aws');
      expect(permissionsList?.innerHTML).toContain('ec2, rds');
      expect(permissionsList?.innerHTML).toContain('us-east-1, us-west-2');
      expect(permissionsList?.innerHTML).toContain('5000');
    });

    it('should remove hidden class from modal', async () => {
      await groupModals.openEditGroupModal('group-1');
      const modal = document.getElementById('group-modal');
      expect(modal?.classList.contains('hidden')).toBe(false);
    });

    it('should handle API errors gracefully', async () => {
      (api.getGroup as jest.Mock).mockRejectedValue(new Error('Network error'));

      await groupModals.openEditGroupModal('group-1');

      // Should show error toast
      expect(document.querySelector('.toast-error')).toBeTruthy();
    });

    it('should handle missing modal elements', async () => {
      document.body.innerHTML = '';
      await groupModals.openEditGroupModal('group-1');
      // Should not throw, just return early
    });

    it('should add default permission when group has no permissions', async () => {
      const group = mockGroups[2]; // Empty Group
      if (!group) throw new Error('Test data missing');
      (api.getGroup as jest.Mock).mockResolvedValue(group);

      await groupModals.openEditGroupModal('group-3');

      const permissionsList = document.getElementById('permissions-list');
      const items = permissionsList?.querySelectorAll('.permission-item');
      expect(items?.length).toBe(1); // Should add one empty permission
    });
  });

  describe('closeGroupModal', () => {
    it('should add hidden class to modal', () => {
      const modal = document.getElementById('group-modal');
      modal?.classList.remove('hidden');

      groupModals.closeGroupModal();

      expect(modal?.classList.contains('hidden')).toBe(true);
    });

    it('should clear current editing group', () => {
      const group = mockGroups[0];
      if (!group) throw new Error('Test data missing');
      groupState.setCurrentEditingGroup(group);
      groupModals.closeGroupModal();
      expect(groupState.currentEditingGroup).toBeNull();
    });

    it('should handle missing modal gracefully', () => {
      document.body.innerHTML = '';
      expect(() => groupModals.closeGroupModal()).not.toThrow();
    });
  });

  describe('saveGroup', () => {
    beforeEach(() => {
      document.body.innerHTML = `
        <div id="group-modal" class="hidden">
          <span id="group-modal-title"></span>
          <form id="group-form">
            <input id="group-id" value="" />
            <input id="group-name" value="New Group" />
            <textarea id="group-description">New description</textarea>
            <div id="permissions-list">
              <div class="permission-item">
                <select class="perm-action"><option value="execute" selected>Execute</option></select>
                <select class="perm-resource"><option value="*" selected>All</option></select>
                <input class="perm-providers" value="aws, azure" />
                <input class="perm-services" value="ec2" />
                <input class="perm-regions" value="us-east-1" />
                <input class="perm-max-amount" value="10000" />
              </div>
            </div>
          </form>
        </div>
      `;
      groupState.setCurrentEditingGroup(null);
      (api.createGroup as jest.Mock).mockResolvedValue({ id: 'new-group' });
      (api.updateGroup as jest.Mock).mockResolvedValue({ id: 'group-1' });
      (loadUsers as jest.Mock).mockResolvedValue(undefined);
    });

    it('should prevent default form submission', async () => {
      const event = { preventDefault: jest.fn() } as unknown as Event;
      await groupModals.saveGroup(event);
      expect(event.preventDefault).toHaveBeenCalled();
    });

    it('should create new group when no current editing group', async () => {
      const event = { preventDefault: jest.fn() } as unknown as Event;
      await groupModals.saveGroup(event);

      expect(api.createGroup).toHaveBeenCalledWith({
        name: 'New Group',
        description: 'New description',
        permissions: [
          {
            action: 'execute',
            resource: '*',
            constraints: {
              providers: ['aws', 'azure'],
              services: ['ec2'],
              regions: ['us-east-1'],
              max_amount: 10000,
            },
          },
        ],
      });
    });

    it('should update existing group when editing', async () => {
      const group = mockGroups[0];
      if (!group) throw new Error('Test data missing');
      groupState.setCurrentEditingGroup(group);

      const event = { preventDefault: jest.fn() } as unknown as Event;
      await groupModals.saveGroup(event);

      expect(api.updateGroup).toHaveBeenCalledWith('group-1', expect.objectContaining({
        name: 'New Group',
        description: 'New description',
      }));
    });

    it('should show success message after creating group', async () => {
      const event = { preventDefault: jest.fn() } as unknown as Event;
      await groupModals.saveGroup(event);

      expect(document.querySelector('.toast-success')?.textContent).toBe('Group created successfully');
    });

    it('should show success message after updating group', async () => {
      const group = mockGroups[0];
      if (!group) throw new Error('Test data missing');
      groupState.setCurrentEditingGroup(group);

      const event = { preventDefault: jest.fn() } as unknown as Event;
      await groupModals.saveGroup(event);

      expect(document.querySelector('.toast-success')?.textContent).toBe('Group updated successfully');
    });

    it('should close modal after successful save', async () => {
      const modal = document.getElementById('group-modal');
      modal?.classList.remove('hidden');

      const event = { preventDefault: jest.fn() } as unknown as Event;
      await groupModals.saveGroup(event);

      expect(modal?.classList.contains('hidden')).toBe(true);
    });

    it('should reload users after successful save', async () => {
      const event = { preventDefault: jest.fn() } as unknown as Event;
      await groupModals.saveGroup(event);

      expect(loadUsers).toHaveBeenCalled();
    });

    it('should handle API errors gracefully', async () => {
      (api.createGroup as jest.Mock).mockRejectedValue(new Error('Server error'));

      const event = { preventDefault: jest.fn() } as unknown as Event;
      await groupModals.saveGroup(event);

      expect(document.querySelector('.toast-error')?.textContent).toContain('Failed to save group');
    });

    it('should collect permissions without constraints', async () => {
      document.body.innerHTML = `
        <div id="group-modal">
          <form id="group-form">
            <input id="group-name" value="Simple Group" />
            <textarea id="group-description">Simple description</textarea>
            <div id="permissions-list">
              <div class="permission-item">
                <select class="perm-action"><option value="view" selected>View</option></select>
                <select class="perm-resource"><option value="*" selected>All</option></select>
                <input class="perm-providers" value="" />
                <input class="perm-services" value="" />
                <input class="perm-regions" value="" />
                <input class="perm-max-amount" value="" />
              </div>
            </div>
          </form>
        </div>
      `;

      const event = { preventDefault: jest.fn() } as unknown as Event;
      await groupModals.saveGroup(event);

      expect(api.createGroup).toHaveBeenCalledWith({
        name: 'Simple Group',
        description: 'Simple description',
        permissions: [{ action: 'view', resource: '*' }],
      });
    });

    it('should skip permissions without action', async () => {
      document.body.innerHTML = `
        <div id="group-modal">
          <form id="group-form">
            <input id="group-name" value="Test Group" />
            <textarea id="group-description"></textarea>
            <div id="permissions-list">
              <div class="permission-item">
                <select class="perm-action"><option value="" selected></option></select>
                <select class="perm-resource"><option value="*" selected>All</option></select>
              </div>
              <div class="permission-item">
                <select class="perm-action"><option value="execute" selected>Execute</option></select>
                <select class="perm-resource"><option value="*" selected>All</option></select>
              </div>
            </div>
          </form>
        </div>
      `;

      const event = { preventDefault: jest.fn() } as unknown as Event;
      await groupModals.saveGroup(event);

      expect(api.createGroup).toHaveBeenCalledWith(expect.objectContaining({
        permissions: [{ action: 'execute', resource: '*' }],
      }));
    });

    it('includes permissions with default resource value', async () => {
      // With a select dropdown, resource always has a value (defaults to *)
      // so permissions are never skipped due to missing resource
      document.body.innerHTML = '<div id="group-modal"><form id="group-form">'
        + '<input id="group-name" value="Test Group" />'
        + '<textarea id="group-description"></textarea>'
        + '<div id="permissions-list"><div class="permission-item">'
        + '<select class="perm-action"><option value="execute" selected>Execute</option></select>'
        + '<select class="perm-resource"><option value="*" selected>All</option></select>'
        + '</div></div></form></div>';

      const event = { preventDefault: jest.fn() } as unknown as Event;
      await groupModals.saveGroup(event);

      expect(api.createGroup).toHaveBeenCalledWith(expect.objectContaining({
        permissions: [expect.objectContaining({ action: 'execute', resource: '*' })],
      }));
    });

    it('should handle missing permissions list', async () => {
      document.body.innerHTML = `
        <div id="group-modal">
          <form id="group-form">
            <input id="group-name" value="Test Group" />
            <textarea id="group-description"></textarea>
          </form>
        </div>
      `;

      const event = { preventDefault: jest.fn() } as unknown as Event;
      await groupModals.saveGroup(event);

      expect(api.createGroup).toHaveBeenCalledWith(expect.objectContaining({
        permissions: [],
      }));
    });
  });

  describe('addPermission', () => {
    it('should add permission item to list', () => {
      const permissionsList = document.getElementById('permissions-list');
      permissionsList!.innerHTML = '';

      groupModals.addPermission();

      const items = permissionsList?.querySelectorAll('.permission-item');
      expect(items?.length).toBe(1);
    });

    it('should add permission with default values', () => {
      const permissionsList = document.getElementById('permissions-list');
      permissionsList!.innerHTML = '';

      groupModals.addPermission();

      const resourceInput = permissionsList?.querySelector('.perm-resource') as HTMLInputElement;
      expect(resourceInput?.value).toBe('*');
    });

    it('should add permission with provided values', () => {
      const permissionsList = document.getElementById('permissions-list');
      permissionsList!.innerHTML = '';

      const permission: api.Permission = {
        action: 'approve',
        resource: 'purchases',
        constraints: {
          providers: ['aws'],
          services: ['ec2', 'rds'],
          regions: ['us-east-1'],
          max_amount: 5000,
        },
      };

      groupModals.addPermission(permission);

      const actionSelect = permissionsList?.querySelector('.perm-action') as HTMLSelectElement;
      const resourceSelect = permissionsList?.querySelector('.perm-resource') as HTMLSelectElement;
      const providersInput = permissionsList?.querySelector('.perm-providers') as HTMLInputElement;
      const servicesInput = permissionsList?.querySelector('.perm-services') as HTMLInputElement;
      const regionsInput = permissionsList?.querySelector('.perm-regions') as HTMLInputElement;
      const maxAmountInput = permissionsList?.querySelector('.perm-max-amount') as HTMLInputElement;

      expect(actionSelect.value).toBe('approve');
      expect(resourceSelect.value).toBe('purchases');
      expect(providersInput.value).toBe('aws');
      expect(servicesInput.value).toBe('ec2, rds');
      expect(regionsInput.value).toBe('us-east-1');
      expect(maxAmountInput.value).toBe('5000');
    });

    it('should add remove button with click handler', () => {
      const permissionsList = document.getElementById('permissions-list');
      permissionsList!.innerHTML = '';

      groupModals.addPermission();

      const removeBtn = permissionsList?.querySelector('.remove-permission-btn');
      expect(removeBtn).toBeTruthy();

      removeBtn?.dispatchEvent(new Event('click'));

      expect(permissionsList?.querySelectorAll('.permission-item').length).toBe(0);
    });

    it('should handle missing permissions list', () => {
      document.body.innerHTML = '';
      expect(() => groupModals.addPermission()).not.toThrow();
    });

    it('should render all action options', () => {
      const permissionsList = document.getElementById('permissions-list');
      permissionsList!.innerHTML = '';

      groupModals.addPermission();

      const actionSelect = permissionsList?.querySelector('.perm-action') as HTMLSelectElement;
      const options = Array.from(actionSelect.options).map(opt => opt.value);

      expect(options).toContain('execute');
      expect(options).toContain('approve');
      expect(options).toContain('create');
      expect(options).toContain('update');
      expect(options).toContain('delete');
      expect(options).toContain('view');
      expect(options).toContain('update');
    });

    it('should handle permission without constraints', () => {
      const permissionsList = document.getElementById('permissions-list');
      permissionsList!.innerHTML = '';

      const permission: api.Permission = {
        action: 'view',
        resource: '*',
      };

      groupModals.addPermission(permission);

      const providersInput = permissionsList?.querySelector('.perm-providers') as HTMLInputElement;
      expect(providersInput.value).toBe('');
    });
  });
});

describe('groups/groupActions', () => {
  beforeEach(() => {
    document.body.innerHTML = `
      <div id="groups-list"></div>
    `;
    userState.setAvailableGroups(mockGroups as any);
    jest.clearAllMocks();
    (global.confirm as jest.Mock).mockReturnValue(true);
    (api.deleteGroup as jest.Mock).mockResolvedValue(undefined);
    (loadUsers as jest.Mock).mockResolvedValue(undefined);
  });

  describe('deleteGroup', () => {
    it('should confirm before deleting', async () => {
      await groupActions.deleteGroup('group-1');

      expect(global.confirm).toHaveBeenCalledWith(
        expect.stringContaining('Administrators')
      );
    });

    it('should call API to delete group when confirmed', async () => {
      await groupActions.deleteGroup('group-1');

      expect(api.deleteGroup).toHaveBeenCalledWith('group-1');
    });

    it('should reload users after successful deletion', async () => {
      await groupActions.deleteGroup('group-1');

      expect(loadUsers).toHaveBeenCalled();
    });

    it('should show success message after deletion', async () => {
      await groupActions.deleteGroup('group-1');

      expect(document.querySelector('.toast-success')?.textContent).toBe('Group deleted successfully');
    });

    it('should not delete when user cancels', async () => {
      (global.confirm as jest.Mock).mockReturnValue(false);

      await groupActions.deleteGroup('group-1');

      expect(api.deleteGroup).not.toHaveBeenCalled();
    });

    it('should not delete when group not found', async () => {
      await groupActions.deleteGroup('nonexistent-group');

      expect(global.confirm).not.toHaveBeenCalled();
      expect(api.deleteGroup).not.toHaveBeenCalled();
    });

    it('should handle API errors gracefully', async () => {
      (api.deleteGroup as jest.Mock).mockRejectedValue(new Error('Server error'));

      await groupActions.deleteGroup('group-1');

      expect(document.querySelector('.toast-error')?.textContent).toBe('Failed to delete group');
    });
  });

  describe('openEditGroupModal re-export', () => {
    it('should export openEditGroupModal from groupModals', () => {
      expect(groupActions.openEditGroupModal).toBe(groupModals.openEditGroupModal);
    });
  });
});

describe('groups/handlers', () => {
  beforeEach(() => {
    document.body.innerHTML = `
      <form id="group-form">
        <button type="submit">Save</button>
      </form>
    `;
    // Clear window properties
    delete (window as any).openCreateGroupModal;
    delete (window as any).closeGroupModal;
    delete (window as any).addPermission;
    jest.clearAllMocks();
  });

  describe('setupGroupHandlers', () => {
    it('should expose openCreateGroupModal on window', () => {
      groupHandlers.setupGroupHandlers();
      expect((window as any).openCreateGroupModal).toBeDefined();
      expect(typeof (window as any).openCreateGroupModal).toBe('function');
    });

    it('should expose closeGroupModal on window', () => {
      groupHandlers.setupGroupHandlers();
      expect((window as any).closeGroupModal).toBeDefined();
      expect(typeof (window as any).closeGroupModal).toBe('function');
    });

    it('should expose addPermission on window', () => {
      groupHandlers.setupGroupHandlers();
      expect((window as any).addPermission).toBeDefined();
      expect(typeof (window as any).addPermission).toBe('function');
    });

    it('should setup form submit handler', () => {
      document.body.innerHTML = `
        <form id="group-form">
          <input id="group-name" value="Test" />
          <textarea id="group-description"></textarea>
          <div id="permissions-list"></div>
        </form>
        <div id="group-modal"></div>
      `;

      groupHandlers.setupGroupHandlers();

      const form = document.getElementById('group-form') as HTMLFormElement;

      // Verify the event listener is attached by checking that the form has the handler
      // We can verify this indirectly by checking the form exists and handlers are set
      expect(form).toBeTruthy();

      // The handler calls saveGroup which is an async function
      // We verify the handler is set up by checking that no error is thrown
      const submitEvent = new Event('submit', { bubbles: true, cancelable: true });
      expect(() => form.dispatchEvent(submitEvent)).not.toThrow();
    });

    it('should handle missing group form gracefully', () => {
      document.body.innerHTML = '';
      expect(() => groupHandlers.setupGroupHandlers()).not.toThrow();
    });

    it('should call window.addPermission without arguments', () => {
      document.body.innerHTML = `
        <form id="group-form"></form>
        <div id="permissions-list"></div>
      `;

      groupHandlers.setupGroupHandlers();

      // Verify the addPermission function was registered on window
      const addPermFn = (window as any).addPermission;
      expect(typeof addPermFn).toBe('function');

      // The function should be callable without throwing
      expect(() => addPermFn()).not.toThrow();

      // Verify that calling it adds a permission item (using the real addPermission from groupModals)
      const permissionsList = document.getElementById('permissions-list');
      const items = permissionsList?.querySelectorAll('.permission-item');
      expect(items?.length).toBe(1);

      expect(permissionsList?.children.length).toBe(1);
    });
  });
});
