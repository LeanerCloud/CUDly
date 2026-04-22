/**
 * settings-subnav: rail rendering + dirty-state reflection.
 *
 * JSDOM doesn't implement IntersectionObserver, so the scrollspy path
 * behaves as a no-op in tests — we stub it to keep renderSubNav's code
 * path deterministic and assert on the links + class toggles it produces.
 */

import { renderSubNav, reflectDirtyState } from '../settings-subnav';

class FakeIntersectionObserver {
  // Minimal stub: store callback + observed targets so callers can invoke
  // manually if they want to simulate a section entering view.
  cb: IntersectionObserverCallback;
  constructor(cb: IntersectionObserverCallback) { this.cb = cb; }
  observe(): void { /* noop */ }
  unobserve(): void { /* noop */ }
  disconnect(): void { /* noop */ }
  takeRecords(): IntersectionObserverEntry[] { return []; }
  root: Element | null = null;
  rootMargin = '';
  thresholds: readonly number[] = [];
}

// IntersectionObserver isn't in jsdom; inject a fake.
(globalThis as unknown as { IntersectionObserver: typeof FakeIntersectionObserver }).IntersectionObserver =
  FakeIntersectionObserver;

function buildPurchasingPanel(): HTMLElement {
  const root = document.createElement('section');
  root.id = 'purchasing-panel';
  ['purchasing-global-defaults', 'aws-settings', 'azure-settings', 'gcp-settings', 'ri-exchange-automation-settings'].forEach((id) => {
    const f = document.createElement('fieldset');
    f.id = id;
    root.appendChild(f);
  });
  document.body.appendChild(root);
  return root;
}

function buildUsersPanel(): HTMLElement {
  const root = document.createElement('section');
  root.id = 'users-section';
  ['users-fieldset', 'groups-fieldset', 'permission-overview-fieldset'].forEach((id) => {
    const f = document.createElement('fieldset');
    f.id = id;
    root.appendChild(f);
  });
  // apikeys-section is a separate top-level block; add it beside.
  const apikeys = document.createElement('section');
  apikeys.id = 'apikeys-section';
  document.body.appendChild(root);
  document.body.appendChild(apikeys);
  return root;
}

describe('renderSubNav', () => {
  afterEach(() => {
    const body = document.body;
    while (body.firstChild) body.removeChild(body.firstChild);
  });

  it('renders a rail with one link per section on Purchasing', () => {
    buildPurchasingPanel();
    renderSubNav('purchasing');

    const rail = document.querySelector('.settings-subnav');
    expect(rail).not.toBeNull();
    const links = rail?.querySelectorAll('a[data-anchor]');
    expect(links?.length).toBe(5);
    expect(Array.from(links ?? []).map((a) => a.textContent)).toEqual([
      'Global Defaults', 'AWS', 'Azure', 'GCP', 'Exchange Automation',
    ]);
  });

  it('filters out items whose section is absent from the DOM', () => {
    // Build a partial panel: only 2 of the 5 sections present.
    const root = document.createElement('section');
    root.id = 'purchasing-panel';
    ['purchasing-global-defaults', 'aws-settings'].forEach((id) => {
      const f = document.createElement('fieldset');
      f.id = id;
      root.appendChild(f);
    });
    document.body.appendChild(root);

    renderSubNav('purchasing');

    const links = document.querySelectorAll('.settings-subnav a[data-anchor]');
    expect(links.length).toBe(2);
  });

  it('omits the rail entirely when fewer than 2 sections exist', () => {
    const root = document.createElement('section');
    root.id = 'purchasing-panel';
    const f = document.createElement('fieldset');
    f.id = 'purchasing-global-defaults';
    root.appendChild(f);
    document.body.appendChild(root);

    renderSubNav('purchasing');

    expect(document.querySelector('.settings-subnav')).toBeNull();
  });

  it('is a no-op for sub-tabs without a configured list (e.g. General)', () => {
    const root = document.createElement('section');
    root.id = 'settings-section';
    document.body.appendChild(root);

    renderSubNav('general');

    expect(document.querySelector('.settings-subnav')).toBeNull();
  });

  it('tears down a previous rail when switching sub-tabs', () => {
    buildPurchasingPanel();
    renderSubNav('purchasing');
    expect(document.querySelector('.settings-subnav')).not.toBeNull();

    // Switch to Users — Purchasing rail must go.
    const body = document.body;
    while (body.firstChild) body.removeChild(body.firstChild);
    buildUsersPanel();
    renderSubNav('users');

    const rails = document.querySelectorAll('.settings-subnav');
    expect(rails.length).toBe(1);
    expect(rails[0]?.getAttribute('aria-label')).toBe('users sub-sections');
  });

  it('clicking a sub-nav link scrolls the target section into view', () => {
    buildPurchasingPanel();
    renderSubNav('purchasing');

    const target = document.getElementById('aws-settings')!;
    const scrollSpy = jest.fn();
    target.scrollIntoView = scrollSpy;

    const awsLink = document.querySelector<HTMLAnchorElement>('a[data-anchor="aws-settings"]')!;
    awsLink.click();

    expect(scrollSpy).toHaveBeenCalledTimes(1);
    expect(scrollSpy).toHaveBeenCalledWith({ behavior: 'smooth', block: 'start' });
  });

  it('seeds the first item as active when the rail first renders', () => {
    buildPurchasingPanel();
    renderSubNav('purchasing');

    const active = document.querySelectorAll<HTMLAnchorElement>('.settings-subnav a.active');
    expect(active.length).toBe(1);
    expect(active[0]?.dataset['anchor']).toBe('purchasing-global-defaults');
  });

  // Regression: prior to this wrapping, each pre-existing child of the
  // panel became its own grid item and auto-placed into alternating
  // columns — producing the "heading overlaps the sub-nav" and "AWS card
  // shows in the left rail" layouts we saw in the 2026-04-22 screenshots.
  it('wraps pre-existing panel children into a single content column', () => {
    const panel = buildPurchasingPanel();
    const originalChildIds = Array.from(panel.children).map((c) => c.id);

    renderSubNav('purchasing');

    // Panel now has exactly 2 grid children: the nav + a content wrapper.
    const directChildren = Array.from(panel.children);
    expect(directChildren.length).toBe(2);
    expect(directChildren[0]?.classList.contains('settings-subnav')).toBe(true);
    expect(directChildren[1]?.classList.contains('settings-layout-content')).toBe(true);

    // All original children live inside the content wrapper, in order.
    const wrapped = Array.from(directChildren[1]?.children ?? []).map((c) => c.id);
    expect(wrapped).toEqual(originalChildIds);

    // The layout class is applied to the panel itself so the 220px rail
    // + 1fr content grid kicks in.
    expect(panel.classList.contains('settings-layout')).toBe(true);
  });

  it('unwraps the content column when tearing down the rail', () => {
    const panel = buildPurchasingPanel();
    const originalChildIds = Array.from(panel.children).map((c) => c.id);

    renderSubNav('purchasing');
    expect(panel.querySelector('.settings-layout-content')).not.toBeNull();

    // Teardown path: re-render into a sub-tab with no matching container
    // (General) — rail must disappear AND the wrapper must be gone, with
    // original children restored as direct panel children.
    renderSubNav('general');

    expect(panel.querySelector('.settings-layout-content')).toBeNull();
    expect(panel.querySelector('.settings-subnav')).toBeNull();
    expect(panel.classList.contains('settings-layout')).toBe(false);
    expect(Array.from(panel.children).map((c) => c.id)).toEqual(originalChildIds);
  });
});

describe('reflectDirtyState', () => {
  afterEach(() => {
    const body = document.body;
    while (body.firstChild) body.removeChild(body.firstChild);
  });

  it('adds .has-unsaved to the settings tab button when dirty', () => {
    const tabBtn = document.createElement('button');
    tabBtn.id = 'settings-tab-btn';
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
});
