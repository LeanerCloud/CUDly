/**
 * Regression tests for issue #1629: editing a group through the Admin UI
 * silently dropped permissions the perm-action <select> couldn't render
 * and silently widened the resource of every such permission to `*`.
 *
 * groupModals.ts's addPermission() used to hardcode the action list to 7
 * of the backend's 20 actions and the resource list to 9 of its 11
 * resources (internal/auth/types.go). When a stored permission's value
 * matched no <option>, the browser fell back to index 0:
 *   - action select index 0 is the empty "Select Action" placeholder ->
 *     collectPermissions() treats a falsy action as "skip this row", so
 *     the permission was DROPPED.
 *   - resource select index 0 is "All (*)" -> the permission was WIDENED
 *     to the wildcard resource.
 *
 * Concretely, the seeded Purchaser group (PURCHASER_PERMS in
 * permissions.generated.ts) held approve-any:purchases, execute:purchases,
 * retry-any:purchases, view:history, view:plans, view:purchases,
 * view:recommendations. Of these, approve-any and retry-any are not base
 * actions (dropped) and history is not a base resource (widened to *).
 * Opening that group, changing only the description, and clicking Save
 * silently deleted the two purchase-approval verbs tenant-wide (they are
 * carved out of admin:*, so no admin could approve or retry a purchase
 * either) and gave every Purchaser read access to users/groups/accounts/
 * config via the widened view:* grant.
 *
 * The fix builds both <option> lists from permissions.ts's ALL_ACTIONS /
 * ALL_RESOURCES (the same closed-union vocabulary the rest of the
 * frontend already mirrors from internal/auth/types.go, now exhaustiveness
 * -checked against it at compile time), and additionally preserves any
 * value that still isn't recognised -- visibly flagged rather than
 * silently coerced -- so a future backend verb the frontend hasn't been
 * taught yet fails loud instead of reintroducing this bug.
 */

import './setup';

jest.mock('../api', () => ({
  getGroup: jest.fn(),
  updateGroup: jest.fn(),
  createGroup: jest.fn(),
}));

jest.mock('../users/userActions', () => ({
  loadUsers: jest.fn().mockResolvedValue(undefined),
}));

jest.mock('../confirmDialog', () => ({
  confirmDialog: jest.fn().mockResolvedValue(true),
}));

import * as api from '../api';
import * as groupState from '../groups/state';
import * as groupModals from '../groups/groupModals';
import { setupGroupHandlers } from '../groups/handlers';
import { ALL_ACTIONS, ALL_RESOURCES } from '../permissions';

// The exact seeded Purchaser group permission set (PURCHASER_PERMS in
// permissions.generated.ts), reproduced here as a fixture so the test
// exercises the real failure scenario rather than a synthetic one.
const PURCHASER_PERMISSIONS: api.Permission[] = [
  { action: 'approve-any', resource: 'purchases' },
  { action: 'execute', resource: 'purchases' },
  { action: 'retry-any', resource: 'purchases' },
  { action: 'view', resource: 'history' },
  { action: 'view', resource: 'plans' },
  { action: 'view', resource: 'purchases' },
  { action: 'view', resource: 'recommendations' },
];

function setUpModalDom(): void {
  document.body.innerHTML = `
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
}

// Simulates opening a group for edit, then saving after ONLY the
// description changed (the exact scenario from the issue: "an admin
// opens a group, changes only the description, and clicks Save").
async function openEditDescriptionOnlyAndSave(group: api.APIGroup, newDescription: string): Promise<void> {
  (api.getGroup as jest.Mock).mockResolvedValue(group);
  await groupModals.openEditGroupModal(group.id);

  (document.getElementById('group-description') as HTMLTextAreaElement).value = newDescription;

  const event = { preventDefault: jest.fn() } as unknown as Event;
  await groupModals.saveGroup(event);
}

describe('regression: group edit no longer drops/widens unrepresentable permissions (#1629)', () => {
  beforeEach(() => {
    setUpModalDom();
    groupState.setCurrentEditingGroup(null);
    jest.clearAllMocks();
  });

  test('seeded Purchaser permissions survive an edit that only changes the description', async () => {
    const group: api.APIGroup = {
      id: 'purchaser-group-id',
      name: 'Purchaser',
      description: 'Old description',
      permissions: PURCHASER_PERMISSIONS,
      created_at: '2024-01-01T00:00:00Z',
    };

    await openEditDescriptionOnlyAndSave(group, 'New description');

    expect(api.updateGroup).toHaveBeenCalledWith('purchaser-group-id', {
      name: 'Purchaser',
      description: 'New description',
      // Byte-identical to the input: same 7 permissions, same order, same
      // action/resource values. Neither approve-any/retry-any dropped nor
      // history widened to '*'.
      permissions: PURCHASER_PERMISSIONS,
    });
  });

  test('approve-any:purchases specifically is not dropped (the four-eyes approval verb)', async () => {
    const group: api.APIGroup = {
      id: 'purchaser-group-id',
      name: 'Purchaser',
      description: 'Old description',
      permissions: PURCHASER_PERMISSIONS,
      created_at: '2024-01-01T00:00:00Z',
    };

    await openEditDescriptionOnlyAndSave(group, 'New description');

    const call = (api.updateGroup as jest.Mock).mock.calls[0];
    const saved = call[1].permissions as api.Permission[];
    expect(saved).toContainEqual({ action: 'approve-any', resource: 'purchases' });
    expect(saved).toContainEqual({ action: 'retry-any', resource: 'purchases' });
  });

  test('view:history specifically is not widened to view:* (would leak users/groups/config/accounts read access)', async () => {
    const group: api.APIGroup = {
      id: 'purchaser-group-id',
      name: 'Purchaser',
      description: 'Old description',
      permissions: PURCHASER_PERMISSIONS,
      created_at: '2024-01-01T00:00:00Z',
    };

    await openEditDescriptionOnlyAndSave(group, 'New description');

    const call = (api.updateGroup as jest.Mock).mock.calls[0];
    const saved = call[1].permissions as api.Permission[];
    expect(saved).toContainEqual({ action: 'view', resource: 'history' });
    // No permission was widened to the wildcard resource as a side effect.
    expect(saved.some(p => p.action === 'view' && p.resource === '*')).toBe(false);
  });

  test('a genuinely unrecognised action/resource pair round-trips unchanged instead of being dropped or widened', async () => {
    // Simulates a future backend verb the frontend's ALL_ACTIONS/
    // ALL_RESOURCES hasn't been taught yet (or legacy/foreign data) --
    // the defense-in-depth path, independent of whether the known-vocabulary
    // list is currently complete.
    const foreignPermission: api.Permission = { action: 'time-travel', resource: 'flux-capacitor' };
    const group: api.APIGroup = {
      id: 'exotic-group-id',
      name: 'Exotic',
      description: 'Old description',
      permissions: [foreignPermission],
      created_at: '2024-01-01T00:00:00Z',
    };

    await openEditDescriptionOnlyAndSave(group, 'New description');

    expect(api.updateGroup).toHaveBeenCalledWith('exotic-group-id', {
      name: 'Exotic',
      description: 'New description',
      permissions: [foreignPermission],
    });
  });

  test('the unrecognised value is visibly flagged in the select, not silently presented as a normal option', async () => {
    const foreignPermission: api.Permission = { action: 'time-travel', resource: 'flux-capacitor' };
    const group: api.APIGroup = {
      id: 'exotic-group-id',
      name: 'Exotic',
      description: 'Old description',
      permissions: [foreignPermission],
      created_at: '2024-01-01T00:00:00Z',
    };

    (api.getGroup as jest.Mock).mockResolvedValue(group);
    await groupModals.openEditGroupModal(group.id);

    const actionSelect = document.querySelector('.perm-action') as HTMLSelectElement;
    const resourceSelect = document.querySelector('.perm-resource') as HTMLSelectElement;
    expect(actionSelect.value).toBe('time-travel');
    expect(resourceSelect.value).toBe('flux-capacitor');
    // The selected option's own label carries a visible warning, not a
    // plain/normal-looking label, so an admin scanning the closed <select>
    // sees something is off before ever opening the dropdown.
    expect(actionSelect.selectedOptions[0]?.textContent).toContain('not recognized');
    expect(resourceSelect.selectedOptions[0]?.textContent).toContain('not recognized');
  });

  // F2 (adversarial review of this PR): the escaping above is correct today
  // but nothing was pinning it. buildActionOptions/buildResourceOptions in
  // groupModals.ts interpolate the raw stored permission value into both an
  // HTML attribute (`value="..."`) and a text label (`⚠ ... (not
  // recognized...)`) for any value outside ALL_ACTIONS/ALL_RESOURCES --
  // exactly the "API string reaching innerHTML" pattern #1727 fixed twice
  // elsewhere in this codebase a few hours before this file was written.
  // These five breakout payloads (from the reviewer) pin the strong
  // properties rather than just "the string appears somewhere": no element
  // was parsed out of the payload, the selected <option> gained no
  // attributes beyond value/selected, its label has zero child elements,
  // and the value is preserved byte-for-byte through the DOM and through
  // an actual save.
  describe('F2: unrecognised-value option escaping holds under XSS breakout payloads', () => {
    const BREAKOUT_PAYLOADS: Array<[string, string]> = [
      ['element injection', '"><img src=x onerror=alert(1)>'],
      ['attribute injection onto the <option> tag', '" autofocus onfocus=alert(1) x="'],
      ['raw script tag', '<script>alert(1)</script>'],
      ['single-quote context breakout', "'><svg onload=alert(1)>"],
      ['closing-tag option injection', '</option><option value="x" selected>evil</option>'],
    ];

    test.each(BREAKOUT_PAYLOADS)('%s does not inject markup and round-trips unchanged', async (_label, payload) => {
      const group: api.APIGroup = {
        id: 'breakout-group-id',
        name: 'Breakout',
        description: 'Old description',
        permissions: [{ action: payload, resource: payload }],
        created_at: '2024-01-01T00:00:00Z',
      };

      (api.getGroup as jest.Mock).mockResolvedValue(group);
      await groupModals.openEditGroupModal(group.id);

      const actionSelect = document.querySelector('.perm-action') as HTMLSelectElement;
      const resourceSelect = document.querySelector('.perm-resource') as HTMLSelectElement;

      // No breakout payload parsed as a live element anywhere in the modal.
      expect(document.querySelectorAll('img, script, svg, iframe, style').length).toBe(0);

      // No extra <option> was smuggled in via the closing-tag breakout: the
      // action select is ALL_ACTIONS + the empty placeholder + the one
      // fallback option for this unrecognised value; the resource select is
      // ALL_RESOURCES + the one fallback (it has no placeholder).
      expect(actionSelect.options.length).toBe(ALL_ACTIONS.length + 1 + 1);
      expect(resourceSelect.options.length).toBe(ALL_RESOURCES.length + 1);

      const selectedAction = actionSelect.selectedOptions[0];
      const selectedResource = resourceSelect.selectedOptions[0];
      expect(selectedAction).toBeDefined();
      expect(selectedResource).toBeDefined();

      // The attribute-injection payload gained no new attributes on the
      // <option> element itself: exactly value + selected, nothing else
      // (no autofocus/onfocus/etc smuggled in).
      expect(Array.from(selectedAction!.attributes).map(a => a.name).sort()).toEqual(['selected', 'value']);
      expect(Array.from(selectedResource!.attributes).map(a => a.name).sort()).toEqual(['selected', 'value']);

      // The label rendered as text, not markup: zero child elements.
      expect(selectedAction!.children.length).toBe(0);
      expect(selectedResource!.children.length).toBe(0);

      // The value round-trips byte-identically through the DOM.
      expect(actionSelect.value).toBe(payload);
      expect(resourceSelect.value).toBe(payload);

      // ...and through an actual save: the exact payload reaches the API
      // call, neither dropped nor mangled by an unrelated description edit.
      (document.getElementById('group-description') as HTMLTextAreaElement).value = 'New description';
      const event = { preventDefault: jest.fn() } as unknown as Event;
      await groupModals.saveGroup(event);

      expect(api.updateGroup).toHaveBeenCalledWith('breakout-group-id', {
        name: 'Breakout',
        description: 'New description',
        permissions: [{ action: payload, resource: payload }],
      });
    });
  });
});

/**
 * The constraints axis of the same defect (#1629).
 *
 * PermissionConstraints (internal/auth/types.go) carries five dimensions;
 * the form rendered inputs for four. constraints.accounts had nowhere to
 * live and collectPermissions() never read one back, so every save dropped
 * it. That WIDENS the permission: matchStringListConstraints
 * (internal/auth/service_group.go) treats an empty list as "no restriction
 * on this dimension", so a permission stored as "manage any scheduled
 * purchase, but only in acct-prod-1" came back out of a cosmetic rename as
 * "manage any scheduled purchase, in every cloud account". The backend
 * grant ceiling does not catch it either: grantCeilingAllows short-circuits
 * on the caller's admin:*.
 *
 * These tests drive the REAL save path -- the submit listener installed by
 * setupGroupHandlers(), reached by clicking the form's submit button --
 * rather than calling saveGroup(event) directly. #group-form carries no
 * novalidate and .perm-resource is `required`, so a direct call bypasses
 * browser constraint validation and cannot tell whether a fix is on the
 * path the admin actually uses.
 */
describe('regression: group edit preserves constraints.accounts (#1629)', () => {
  // Mirrors the #group-form markup in index.html: required name input, the
  // permissions list, and a real submit button.
  function setUpRealFormDom(): void {
    document.body.innerHTML = `
      <div id="group-modal" class="hidden">
        <span id="group-modal-title"></span>
        <form id="group-form">
          <input type="hidden" id="group-id">
          <input type="text" id="group-name" required>
          <textarea id="group-description"></textarea>
          <div id="permissions-list"></div>
          <button type="submit" class="primary">Save Group</button>
        </form>
      </div>
    `;
    setupGroupHandlers();
  }

  function submitButton(): HTMLButtonElement {
    return document.querySelector('#group-form button[type="submit"]') as HTMLButtonElement;
  }

  // Clicks Save the way an admin does and waits for saveGroup's promise
  // chain to settle (the submit listener fires it without awaiting).
  async function clickSave(): Promise<void> {
    submitButton().click();
    await new Promise(resolve => setTimeout(resolve, 0));
  }

  async function openEdit(group: api.APIGroup): Promise<void> {
    (api.getGroup as jest.Mock).mockResolvedValue(group);
    await groupModals.openEditGroupModal(group.id);
  }

  function accountsInput(index = 0): HTMLInputElement {
    const items = document.querySelectorAll('.permission-item');
    return items[index]!.querySelector('.perm-accounts') as HTMLInputElement;
  }

  function savedPermissions(): api.Permission[] {
    const call = (api.updateGroup as jest.Mock).mock.calls[0];
    return call[1].permissions as api.Permission[];
  }

  // A realistic account-scoped operator group. The second permission is the
  // narrowest form of the bug: accounts is its ONLY constraint, so the old
  // `if (providers || services || regions || maxAmount)` guard never fired
  // and the permission round-tripped fully unconstrained.
  const ACCOUNT_SCOPED_PERMISSIONS: api.Permission[] = [
    { action: 'update-any', resource: 'purchases', constraints: { accounts: ['acct-prod-1'], max_amount: 5000 } },
    { action: 'view', resource: 'history', constraints: { accounts: ['acct-prod-1'] } },
  ];

  function accountScopedGroup(): api.APIGroup {
    return {
      id: 'operators-group-id',
      name: 'Prod Operators',
      description: 'Old description',
      permissions: ACCOUNT_SCOPED_PERMISSIONS,
      created_at: '2024-01-01T00:00:00Z',
    };
  }

  beforeEach(() => {
    setUpRealFormDom();
    groupState.setCurrentEditingGroup(null);
    jest.clearAllMocks();
  });

  test('the harness really goes through browser constraint validation', async () => {
    // Pins the property the rest of this describe relies on: an invalid form
    // never reaches saveGroup. Without this, a fix that never runs could pass
    // every test below.
    await openEdit(accountScopedGroup());
    (document.getElementById('group-name') as HTMLInputElement).value = '';

    const form = document.getElementById('group-form') as HTMLFormElement;
    expect(form.checkValidity()).toBe(false);
    await clickSave();

    expect(api.updateGroup).not.toHaveBeenCalled();
  });

  test('an account-scoped permission set survives an edit that only changes the description', async () => {
    await openEdit(accountScopedGroup());
    (document.getElementById('group-description') as HTMLTextAreaElement).value = 'New description';

    const form = document.getElementById('group-form') as HTMLFormElement;
    expect(form.checkValidity()).toBe(true);
    await clickSave();

    expect(api.updateGroup).toHaveBeenCalledWith('operators-group-id', {
      name: 'Prod Operators',
      description: 'New description',
      permissions: ACCOUNT_SCOPED_PERMISSIONS,
    });

    // Non-vacuity: "no permission lost its fence" is trivially true of an
    // empty list, and an empty list is itself one of this bug's outcomes.
    const saved = savedPermissions();
    expect(saved).toHaveLength(2);
    expect(saved.every(p => (p.constraints?.accounts?.length ?? 0) > 0)).toBe(true);
  });

  test('the stored account fence is rendered into the form, so the admin edits what is actually stored', async () => {
    await openEdit(accountScopedGroup());

    expect(accountsInput(0).value).toBe('acct-prod-1');
    expect(accountsInput(1).value).toBe('acct-prod-1');
  });

  test('narrowing the fence saves exactly what was entered', async () => {
    const group = accountScopedGroup();
    group.permissions = [
      { action: 'update-any', resource: 'purchases', constraints: { accounts: ['acct-prod-1', 'acct-prod-2'] } },
    ];
    await openEdit(group);

    accountsInput().value = 'acct-prod-1';
    await clickSave();

    expect(savedPermissions()).toEqual([
      { action: 'update-any', resource: 'purchases', constraints: { accounts: ['acct-prod-1'] } },
    ]);
  });

  test('clearing one fence removes only that one and is not blocked', async () => {
    // The other direction: an admin who deliberately clears the field gets an
    // unconstrained permission, which is what they asked for.
    //
    // Only the FIRST row is cleared, and the second is asserted to keep its
    // fence. "The cleared row has no accounts" is on its own true of a build
    // that renders the input but never reads it back -- the very defect this
    // file exists for -- so the assertion is paired with one that such a build
    // fails. Both are keyed on the outgoing payload rather than on any DOM
    // hook this change introduces.
    await openEdit(accountScopedGroup());

    accountsInput(0).value = '';
    await clickSave();

    const saved = savedPermissions();
    expect(saved).toHaveLength(2);
    expect(saved[0]!.constraints?.accounts).toBeUndefined();
    expect(saved[0]!.constraints?.max_amount).toBe(5000);
    expect(saved[1]!.constraints?.accounts).toEqual(['acct-prod-1']);
  });

  test('adding a fence to a previously unconstrained permission saves it', async () => {
    const group = accountScopedGroup();
    group.permissions = [{ action: 'view', resource: 'history' }];
    await openEdit(group);

    accountsInput().value = 'acct-prod-1, acct-prod-2';
    await clickSave();

    expect(savedPermissions()).toEqual([
      { action: 'view', resource: 'history', constraints: { accounts: ['acct-prod-1', 'acct-prod-2'] } },
    ]);
  });

  test('an account id is escaped on the way into the input and round-trips byte-identically', async () => {
    // The new value="" attribute is another API string reaching innerHTML.
    const payload = '"><img src=x onerror=alert(1)>';
    const group = accountScopedGroup();
    group.permissions = [{ action: 'view', resource: 'history', constraints: { accounts: [payload] } }];
    await openEdit(group);

    expect(document.querySelectorAll('img, script, svg, iframe, style').length).toBe(0);
    expect(accountsInput().value).toBe(payload);

    await clickSave();

    expect(savedPermissions()).toEqual([
      { action: 'view', resource: 'history', constraints: { accounts: [payload] } },
    ]);
  });
});
