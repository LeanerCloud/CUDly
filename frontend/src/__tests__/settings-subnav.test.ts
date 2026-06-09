/**
 * settings-subnav: dirty-state reflection.
 *
 * The in-panel sticky-rail renderer (renderSubNav, SUBTAB_ITEMS) was
 * removed in issue #340 follow-up — its float + negative-margin layout
 * collided with the new action-center left sidebar. Only the dirty-state
 * indicator survives.
 */

import { reflectDirtyState } from '../settings-subnav';

describe('reflectDirtyState', () => {
  afterEach(() => {
    const body = document.body;
    while (body.firstChild) body.removeChild(body.firstChild);
  });

  it('adds .has-unsaved to the admin tab button when dirty', () => {
    const tabBtn = document.createElement('button');
    tabBtn.id = 'admin-tab-btn';
    document.body.appendChild(tabBtn);

    reflectDirtyState(true);
    expect(tabBtn.classList.contains('has-unsaved')).toBe(true);

    reflectDirtyState(false);
    expect(tabBtn.classList.contains('has-unsaved')).toBe(false);
  });

  it('toggles .dirty on every .settings-buttons row', () => {
    const one = document.createElement('div');
    one.className = 'settings-buttons';
    const two = document.createElement('div');
    two.className = 'settings-buttons';
    document.body.append(one, two);

    reflectDirtyState(true);
    expect(one.classList.contains('dirty')).toBe(true);
    expect(two.classList.contains('dirty')).toBe(true);
    expect(one.classList.contains('settings-savebar')).toBe(true);

    reflectDirtyState(false);
    expect(one.classList.contains('dirty')).toBe(false);
    expect(two.classList.contains('dirty')).toBe(false);
  });

  it('skips .settings-buttons nested inside #ri-exchange-automation-settings', () => {
    // The RI Exchange fieldset renders its own save button; it must not
    // receive the sticky-save-bar treatment that would create two stacked
    // sticky bars. Regression guard for the exemption in reflectDirtyState().
    const wrapper = document.createElement('div');
    wrapper.id = 'ri-exchange-automation-settings';
    const nested = document.createElement('div');
    nested.className = 'settings-buttons';
    wrapper.appendChild(nested);

    const outside = document.createElement('div');
    outside.className = 'settings-buttons';

    document.body.append(wrapper, outside);

    reflectDirtyState(true);
    expect(nested.classList.contains('settings-savebar')).toBe(false);
    expect(nested.classList.contains('dirty')).toBe(false);
    expect(outside.classList.contains('settings-savebar')).toBe(true);
    expect(outside.classList.contains('dirty')).toBe(true);

    reflectDirtyState(false);
    expect(outside.classList.contains('dirty')).toBe(false);
  });
});
