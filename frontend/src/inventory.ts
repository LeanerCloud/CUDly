/**
 * Inventory & Coverage section (issue #340 T4).
 *
 * Umbrella section that folds the former top-level "RI Exchange" tab into
 * a sub-section of a broader Inventory & Coverage view. Sub-sections:
 *   - active-commitments — placeholder until T7 wires the per-commitment list
 *   - coverage           — placeholder until T7 wires per-provider donuts
 *   - ri-exchange        — hosts the existing RI Exchange UI unchanged
 *
 * The two placeholder sub-sections are intentional empty states — their
 * backend endpoints aren't in scope for #340. They're tracked as deferred
 * sub-tasks in #340 so the path forward stays visible.
 *
 * Default sub-section: ri-exchange. Once T7 lights up active-commitments
 * with real data, the default flips so the user lands on substantive
 * content first.
 */

import { loadRIExchange } from './riexchange';

type InventorySubSection = 'active-commitments' | 'coverage' | 'ri-exchange';

const SUB_SECTION_IDS: Record<InventorySubSection, string> = {
  'active-commitments': 'inventory-active-commitments',
  'coverage': 'inventory-coverage',
  'ri-exchange': 'inventory-ri-exchange',
};

const DEFAULT_SUB_SECTION: InventorySubSection = 'ri-exchange';

let currentSubSection: InventorySubSection | undefined;
let listenersWired = false;

function isValidSubSection(name: string): name is InventorySubSection {
  return name === 'active-commitments' || name === 'coverage' || name === 'ri-exchange';
}

/**
 * Show one sub-section, hide the others. Activates the matching sub-nav
 * button and (for ri-exchange) triggers the RI exchange data load so the
 * existing flow stays identical to its pre-#340 behaviour.
 */
export function switchInventorySubSection(name: string): void {
  const target: InventorySubSection = isValidSubSection(name) ? name : DEFAULT_SUB_SECTION;

  document.querySelectorAll<HTMLButtonElement>('#inventory-tab .sub-tab-btn').forEach((btn) => {
    const isActive = btn.dataset['invSubtab'] === target;
    btn.classList.toggle('active', isActive);
    btn.setAttribute('aria-selected', isActive ? 'true' : 'false');
  });

  for (const key of Object.keys(SUB_SECTION_IDS) as InventorySubSection[]) {
    const el = document.getElementById(SUB_SECTION_IDS[key]);
    if (el) el.classList.toggle('hidden', key !== target);
  }

  if (target === 'ri-exchange') {
    void loadRIExchange();
  }

  currentSubSection = target;
}

/**
 * Wire sub-nav button clicks. Idempotent — calling this more than once
 * doesn't double-bind handlers.
 */
function wireSubNavListeners(): void {
  if (listenersWired) return;
  const buttons = document.querySelectorAll<HTMLButtonElement>('#inventory-tab .sub-tab-btn');
  if (buttons.length === 0) return;
  buttons.forEach((btn) => {
    btn.addEventListener('click', () => {
      const name = btn.dataset['invSubtab'] ?? DEFAULT_SUB_SECTION;
      switchInventorySubSection(name);
    });
  });
  listenersWired = true;
}

/**
 * Initialize the Inventory & Coverage section. Called by navigation.ts'
 * switchTab when 'inventory' is selected. Defaults to the ri-exchange
 * sub-section if the user hasn't selected one this session.
 */
export function loadInventory(): void {
  wireSubNavListeners();
  switchInventorySubSection(currentSubSection ?? DEFAULT_SUB_SECTION);
}
