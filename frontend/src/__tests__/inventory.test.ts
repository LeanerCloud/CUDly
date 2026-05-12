/**
 * Inventory & Coverage section tests (issue #340 T4).
 *
 * Verifies the sub-tab switching machinery for the umbrella section that
 * folds the former top-level RI Exchange tab in. The RI Exchange sub-section
 * is the default and the only one with real backend data today; the other
 * two sub-sections are placeholders tracked as deferred sub-tasks in #340.
 */

// loadRIExchange is a side-effect import from a module that touches the
// network in real use. Mock it out so the test only exercises sub-section
// switching, not real RI fetch.
jest.mock('../riexchange', () => ({
  loadRIExchange: jest.fn(),
}));

import { loadInventory, switchInventorySubSection } from '../inventory';
import { loadRIExchange } from '../riexchange';

function buildInventoryDOM(): void {
  // Build the inventory tab + sub-nav via DOM methods rather than an
  // innerHTML string. Matches the no-innerHTML-with-interpolated-data
  // constraint from issue #340's plan; here there's no interpolation
  // anyway, but the codebase prefers DOM construction in tests too.
  const tab = document.createElement('div');
  tab.id = 'inventory-tab';
  tab.classList.add('tab-content', 'active');

  const subnav = document.createElement('div');
  subnav.classList.add('inventory-subnav');

  for (const [name, label, isActive] of [
    ['active-commitments', 'Active commitments', false],
    ['coverage', 'Coverage', false],
    ['ri-exchange', 'RI Exchange', true],
  ] as const) {
    const btn = document.createElement('button');
    btn.classList.add('sub-tab-btn');
    if (isActive) btn.classList.add('active');
    btn.dataset['invSubtab'] = name;
    btn.setAttribute('role', 'tab');
    btn.setAttribute('aria-selected', isActive ? 'true' : 'false');
    btn.textContent = label;
    subnav.appendChild(btn);
  }
  tab.appendChild(subnav);

  for (const [id, hidden, body] of [
    ['inventory-active-commitments', true, 'active'],
    ['inventory-coverage', true, 'coverage'],
    ['inventory-ri-exchange', false, 'ri-exchange'],
  ] as const) {
    const section = document.createElement('section');
    section.id = id;
    if (hidden) section.classList.add('hidden');
    section.textContent = body;
    tab.appendChild(section);
  }

  document.body.appendChild(tab);
}

function clearDOM(): void {
  while (document.body.firstChild) document.body.removeChild(document.body.firstChild);
}

describe('Inventory & Coverage sub-section switching', () => {
  beforeEach(() => {
    buildInventoryDOM();
    (loadRIExchange as jest.Mock).mockClear();
  });

  afterEach(() => {
    clearDOM();
  });

  test('switchInventorySubSection shows active-commitments and hides others', () => {
    switchInventorySubSection('active-commitments');

    expect(document.getElementById('inventory-active-commitments')?.classList.contains('hidden')).toBe(false);
    expect(document.getElementById('inventory-coverage')?.classList.contains('hidden')).toBe(true);
    expect(document.getElementById('inventory-ri-exchange')?.classList.contains('hidden')).toBe(true);

    const activeBtn = document.querySelector('[data-inv-subtab="active-commitments"]');
    expect(activeBtn?.classList.contains('active')).toBe(true);
    expect(activeBtn?.getAttribute('aria-selected')).toBe('true');
  });

  test('switchInventorySubSection shows coverage and hides others', () => {
    switchInventorySubSection('coverage');

    expect(document.getElementById('inventory-coverage')?.classList.contains('hidden')).toBe(false);
    expect(document.getElementById('inventory-active-commitments')?.classList.contains('hidden')).toBe(true);
    expect(document.getElementById('inventory-ri-exchange')?.classList.contains('hidden')).toBe(true);
  });

  test('switchInventorySubSection shows ri-exchange and calls loadRIExchange', () => {
    switchInventorySubSection('ri-exchange');

    expect(document.getElementById('inventory-ri-exchange')?.classList.contains('hidden')).toBe(false);
    expect(document.getElementById('inventory-active-commitments')?.classList.contains('hidden')).toBe(true);
    expect(document.getElementById('inventory-coverage')?.classList.contains('hidden')).toBe(true);
    expect(loadRIExchange).toHaveBeenCalledTimes(1);
  });

  test('switchInventorySubSection falls back to ri-exchange for unknown sub-section', () => {
    switchInventorySubSection('something-unknown');

    expect(document.getElementById('inventory-ri-exchange')?.classList.contains('hidden')).toBe(false);
    expect(loadRIExchange).toHaveBeenCalledTimes(1);
  });

  test('loadInventory wires sub-nav click handlers and lands on default', () => {
    loadInventory();

    // Default landing is ri-exchange.
    expect(document.getElementById('inventory-ri-exchange')?.classList.contains('hidden')).toBe(false);

    // Clicking a sub-tab button switches the section.
    const coverageBtn = document.querySelector<HTMLButtonElement>('[data-inv-subtab="coverage"]')!;
    coverageBtn.click();
    expect(document.getElementById('inventory-coverage')?.classList.contains('hidden')).toBe(false);
    expect(document.getElementById('inventory-ri-exchange')?.classList.contains('hidden')).toBe(true);
  });
});
