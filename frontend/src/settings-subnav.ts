/**
 * Sticky sub-nav for long Settings panels. Renders a left-rail of anchor
 * links tied to section IDs already present in the DOM, with scrollspy
 * (IntersectionObserver) highlighting whichever section is in view.
 *
 * Panels with only one section (General) skip the rail — a 1-item list
 * isn't navigation, and the single-column layout keeps more room for the
 * form itself.
 */

interface SubNavItem {
  id: string;
  label: string;
}

const SUBTAB_ITEMS: Record<string, SubNavItem[]> = {
  purchasing: [
    { id: 'purchasing-global-defaults', label: 'Global Defaults' },
    { id: 'aws-settings', label: 'AWS' },
    { id: 'azure-settings', label: 'Azure' },
    { id: 'gcp-settings', label: 'GCP' },
    { id: 'ri-exchange-automation-settings', label: 'Exchange Automation' },
  ],
  accounts: [
    { id: 'accounts-federation', label: 'Federation Setup' },
    { id: 'accounts-registrations', label: 'Registrations' },
    { id: 'accounts-aws-block', label: 'AWS Accounts' },
    { id: 'accounts-azure-block', label: 'Azure Accounts' },
    { id: 'accounts-gcp-block', label: 'GCP Accounts' },
  ],
  users: [
    { id: 'users-fieldset', label: 'Users' },
    { id: 'groups-fieldset', label: 'Groups' },
    { id: 'permission-overview-fieldset', label: 'Permission Overview' },
    { id: 'apikeys-section', label: 'API Keys' },
  ],
};

let activeObserver: IntersectionObserver | null = null;

/**
 * Render the sub-nav rail for the given sub-tab inside the corresponding
 * `<section>` container. No-op for sub-tabs with fewer than 2 sections.
 * Safe to call repeatedly — tears down any previous scrollspy observer
 * before rebuilding.
 */
export function renderSubNav(subTab: string): void {
  // Clear any prior observer so anchored sections don't fire spy updates
  // into a stale rail.
  if (activeObserver) {
    activeObserver.disconnect();
    activeObserver = null;
  }

  // Always remove a previously rendered rail, regardless of what we're
  // about to show — switching from Purchasing to General should not leave
  // the Purchasing rail lingering in the DOM.
  document.querySelectorAll('.settings-subnav').forEach((el) => el.remove());
  document.querySelectorAll('.settings-layout').forEach((el) => {
    el.classList.remove('settings-layout');
  });

  const items = SUBTAB_ITEMS[subTab];
  if (!items || items.length < 2) return;

  // Anchor the rail to the panel container for the given sub-tab. For
  // 'users' the rail wraps both `#users-section` and `#apikeys-section`
  // since sub-nav items span both; we attach to #users-section and rely
  // on sticky positioning to keep it visible as the user scrolls through
  // the combined region.
  const containerId = subTab === 'purchasing'
    ? 'purchasing-panel'
    : subTab === 'accounts'
      ? 'accounts-section'
      : subTab === 'users'
        ? 'users-section'
        : null;
  if (!containerId) return;
  const container = document.getElementById(containerId);
  if (!container) return;

  // Filter out items whose target section doesn't exist in the DOM (e.g.
  // provider blocks that may be hidden or legacy IDs that were removed).
  const presentItems = items.filter((i) => document.getElementById(i.id));
  if (presentItems.length < 2) return;

  const nav = document.createElement('nav');
  nav.className = 'settings-subnav';
  nav.setAttribute('aria-label', `${subTab} sub-sections`);
  const ul = document.createElement('ul');
  presentItems.forEach((item) => {
    const li = document.createElement('li');
    const a = document.createElement('a');
    a.href = `#${item.id}`;
    a.dataset['anchor'] = item.id;
    a.textContent = item.label;
    a.addEventListener('click', (e) => {
      e.preventDefault();
      const target = document.getElementById(item.id);
      if (target) target.scrollIntoView({ behavior: 'smooth', block: 'start' });
    });
    li.appendChild(a);
    ul.appendChild(li);
  });
  nav.appendChild(ul);

  // Wrap: wrap the container's existing children in a content div, and
  // insert the nav as a sibling inside a two-column grid.
  container.classList.add('settings-layout');
  container.insertBefore(nav, container.firstChild);

  // Scrollspy: highlight whichever section is currently most-visible.
  const links = Array.from(nav.querySelectorAll<HTMLAnchorElement>('a[data-anchor]'));
  const updateActive = (activeId: string): void => {
    links.forEach((l) => l.classList.toggle('active', l.dataset['anchor'] === activeId));
  };

  if ('IntersectionObserver' in window) {
    activeObserver = new IntersectionObserver(
      (entries) => {
        const visible = entries
          .filter((e) => e.isIntersecting)
          .sort((a, b) => b.intersectionRatio - a.intersectionRatio)[0];
        if (visible) updateActive(visible.target.id);
      },
      { rootMargin: '-20% 0px -60% 0px', threshold: [0, 0.25, 0.5, 0.75, 1] },
    );
    presentItems.forEach((item) => {
      const el = document.getElementById(item.id);
      if (el && activeObserver) activeObserver.observe(el);
    });
  }

  // Seed an initial active state so the rail isn't blank before the user
  // scrolls.
  if (presentItems[0]) updateActive(presentItems[0].id);
}

/**
 * Update the "Unsaved changes" sticky footer + dirty dot on the Settings
 * top-tab. Called whenever the settings form's dirty state changes.
 */
export function reflectDirtyState(dirty: boolean): void {
  const tabBtn = document.getElementById('settings-tab-btn');
  if (tabBtn) tabBtn.classList.toggle('has-unsaved', dirty);
  document.querySelectorAll('.settings-buttons').forEach((el) => {
    el.classList.toggle('settings-savebar', true);
    el.classList.toggle('dirty', dirty);
  });
}
