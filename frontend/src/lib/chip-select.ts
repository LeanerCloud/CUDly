/**
 * chip-select — a filter chip that opens a popover menu (issue #344 T1).
 *
 * Replaces native `<select>` for filter use-cases where we want a compact
 * pill-shaped trigger that opens a custom listbox. The native dropdown
 * styling is the browser's job; this lets us own the menu's look + the
 * search-filter behaviour for long option lists.
 *
 * Usage:
 *   const { root, setValue, getValue, setOptions } = createChipSelect({
 *     label: 'Provider',
 *     options: [
 *       { value: '', label: 'All Providers' },
 *       { value: 'aws', label: 'AWS' },
 *       { value: 'azure', label: 'Azure' },
 *       { value: 'gcp', label: 'GCP' },
 *     ],
 *     value: '',
 *     onChange: (newValue) => console.log(newValue),
 *   });
 *   container.appendChild(root);
 *
 * Keyboard:
 *   Enter / Space / Down on the trigger → open
 *   Esc on the menu → close + focus trigger
 *   Arrow Up / Down → move highlight
 *   Enter on highlighted option → select + close
 *   Tab on menu → close (and let focus continue)
 *
 * Search-filter input appears at top of popover when options.length > 8.
 *
 * ARIA: the trigger uses `aria-haspopup="listbox"` + `aria-expanded`. The
 * menu is `role="listbox"` with `role="option"` children; the active
 * option carries `aria-selected="true"`.
 */

export interface ChipSelectOption {
  value: string;
  label: string;
}

export interface ChipSelectConfig {
  label: string;
  options: readonly ChipSelectOption[];
  value: string;
  onChange: (newValue: string) => void;
  /** Threshold (inclusive) above which the in-popover search input renders. */
  searchThreshold?: number;
}

export interface ChipSelectHandle {
  root: HTMLElement;
  setValue: (v: string) => void;
  getValue: () => string;
  setOptions: (opts: readonly ChipSelectOption[]) => void;
}

const DEFAULT_SEARCH_THRESHOLD = 8;

function findOptionLabel(opts: readonly ChipSelectOption[], value: string): string {
  const hit = opts.find((o) => o.value === value);
  return hit ? hit.label : '';
}

export function createChipSelect(cfg: ChipSelectConfig): ChipSelectHandle {
  const searchThreshold = cfg.searchThreshold ?? DEFAULT_SEARCH_THRESHOLD;

  // State held in closure so handles + handlers see the latest.
  let currentValue = cfg.value;
  let options = cfg.options.slice();
  let isOpen = false;
  let filterText = '';
  let activeIndex = -1;
  let outsideListener: ((ev: MouseEvent) => void) | null = null;
  let escListener: ((ev: KeyboardEvent) => void) | null = null;

  // DOM nodes — built once, mutated thereafter.
  const root = document.createElement('div');
  root.classList.add('chip-select-root');

  const trigger = document.createElement('button');
  trigger.type = 'button';
  trigger.classList.add('chip-select');
  trigger.setAttribute('aria-haspopup', 'listbox');
  trigger.setAttribute('aria-expanded', 'false');
  trigger.setAttribute('aria-label', cfg.label);

  const triggerLabel = document.createElement('span');
  triggerLabel.classList.add('chip-select-label');
  trigger.appendChild(triggerLabel);

  const triggerValue = document.createElement('span');
  triggerValue.classList.add('chip-select-value');
  trigger.appendChild(triggerValue);

  const triggerCaret = document.createElementNS('http://www.w3.org/2000/svg', 'svg');
  triggerCaret.classList.add('chip-select-caret');
  triggerCaret.setAttribute('viewBox', '0 0 12 12');
  triggerCaret.setAttribute('width', '10');
  triggerCaret.setAttribute('height', '10');
  triggerCaret.setAttribute('aria-hidden', 'true');
  const caretPath = document.createElementNS('http://www.w3.org/2000/svg', 'path');
  caretPath.setAttribute('d', 'M2 4l4 4 4-4');
  caretPath.setAttribute('fill', 'none');
  caretPath.setAttribute('stroke', 'currentColor');
  caretPath.setAttribute('stroke-width', '1.5');
  caretPath.setAttribute('stroke-linecap', 'round');
  caretPath.setAttribute('stroke-linejoin', 'round');
  triggerCaret.appendChild(caretPath);
  trigger.appendChild(triggerCaret);

  root.appendChild(trigger);

  const menu = document.createElement('div');
  menu.classList.add('chip-select-menu', 'hidden');
  root.appendChild(menu);

  // Search input is created lazily because it only renders for long lists.
  const search = document.createElement('input');
  search.type = 'search';
  search.classList.add('chip-select-search');
  search.placeholder = 'Filter…';
  search.setAttribute('aria-label', `Filter ${cfg.label} options`);

  const optionsList = document.createElement('ul');
  optionsList.classList.add('chip-select-options');
  // role="listbox" lives on the ul so option elements are directly owned by
  // their listbox — placing it on the outer div would make them grandchildren,
  // which violates the ARIA ownership requirement (WAI-ARIA §6.6.12).
  optionsList.setAttribute('role', 'listbox');
  optionsList.setAttribute('aria-label', cfg.label);
  menu.appendChild(optionsList);

  /**
   * Visible options after applying the search-filter text. Returned in the
   * same order as `options`; case-insensitive substring match on label.
   */
  function visibleOptions(): ChipSelectOption[] {
    if (filterText === '') return options.slice();
    const needle = filterText.toLowerCase();
    return options.filter((o) => o.label.toLowerCase().includes(needle));
  }

  function renderTrigger(): void {
    triggerLabel.textContent = `${cfg.label}:`;
    triggerValue.textContent = findOptionLabel(options, currentValue) || '(any)';
  }

  // Stable id prefix for this instance, used for aria-activedescendant.
  const idPrefix = `chip-select-opt-${cfg.label.toLowerCase().replace(/\s+/g, '-')}`;

  function renderOptions(): void {
    while (optionsList.firstChild) optionsList.removeChild(optionsList.firstChild);
    const visible = visibleOptions();
    let activeOptId: string | null = null;
    visible.forEach((opt, i) => {
      const li = document.createElement('li');
      const optId = `${idPrefix}-${opt.value !== '' ? opt.value : 'empty'}-${i}`;
      li.id = optId;
      li.setAttribute('role', 'option');
      li.classList.add('chip-select-option');
      li.dataset['value'] = opt.value;
      li.textContent = opt.label;
      const isCurrent = opt.value === currentValue;
      li.setAttribute('aria-selected', isCurrent ? 'true' : 'false');
      if (i === activeIndex) {
        li.classList.add('active');
        activeOptId = optId;
      }
      if (isCurrent) li.classList.add('current');
      li.addEventListener('mousedown', (ev) => {
        // mousedown not click — fires before the focusout that would close
        // the menu, so we can read currentValue without race.
        ev.preventDefault();
        selectByValue(opt.value);
      });
      optionsList.appendChild(li);
    });
    // Keep aria-activedescendant in sync with the highlighted option.
    if (activeOptId !== null) {
      trigger.setAttribute('aria-activedescendant', activeOptId);
    } else {
      trigger.removeAttribute('aria-activedescendant');
    }
    // Empty-result hint when filter doesn't match anything.
    if (visible.length === 0) {
      const li = document.createElement('li');
      li.classList.add('chip-select-empty');
      li.textContent = 'No matches';
      // role="option" + aria-disabled so AT users hear "No matches, dimmed"
      // rather than encountering an orphaned element inside role="listbox".
      li.setAttribute('role', 'option');
      li.setAttribute('aria-disabled', 'true');
      li.setAttribute('aria-selected', 'false');
      optionsList.appendChild(li);
    }
  }

  function selectByValue(value: string): void {
    currentValue = value;
    renderTrigger();
    closeMenu();
    cfg.onChange(value);
  }

  function shouldShowSearch(): boolean {
    return options.length > searchThreshold;
  }

  function syncSearchVisibility(): void {
    // Either present at top of menu or absent. Idempotent on repeated calls.
    if (shouldShowSearch()) {
      if (!menu.contains(search)) {
        menu.insertBefore(search, optionsList);
      }
    } else if (menu.contains(search)) {
      menu.removeChild(search);
    }
  }

  function openMenu(): void {
    if (isOpen) return;
    isOpen = true;
    filterText = '';
    search.value = '';
    activeIndex = visibleOptions().findIndex((o) => o.value === currentValue);
    syncSearchVisibility();
    renderOptions();
    menu.classList.remove('hidden');
    trigger.setAttribute('aria-expanded', 'true');
    // Flip-upward when the menu would extend past the viewport bottom.
    positionMenu();
    // Focus management — for keyboard users.
    if (shouldShowSearch()) {
      search.focus();
    } else {
      // Don't steal focus from the trigger when no search input;
      // keyboard arrow handlers still work because the trigger keeps focus.
    }

    outsideListener = (ev: MouseEvent): void => {
      if (!root.contains(ev.target as Node)) closeMenu();
    };
    document.addEventListener('mousedown', outsideListener);

    escListener = (ev: KeyboardEvent): void => {
      if (ev.key === 'Escape') {
        ev.preventDefault();
        closeMenu();
        trigger.focus();
      }
    };
    document.addEventListener('keydown', escListener);
  }

  function closeMenu(): void {
    if (!isOpen) return;
    isOpen = false;
    menu.classList.add('hidden');
    trigger.setAttribute('aria-expanded', 'false');
    trigger.removeAttribute('aria-activedescendant');
    if (outsideListener) {
      document.removeEventListener('mousedown', outsideListener);
      outsideListener = null;
    }
    if (escListener) {
      document.removeEventListener('keydown', escListener);
      escListener = null;
    }
  }

  function positionMenu(): void {
    // Reset any prior flip; default is below the trigger.
    menu.classList.remove('chip-select-menu-up');
    const rect = trigger.getBoundingClientRect();
    const viewportH = window.innerHeight;
    // Estimate menu height as 280px (search + ~6 visible rows) — close enough
    // for the flip decision. Refine after first render if needed.
    const estimatedMenuH = 280;
    if (rect.bottom + estimatedMenuH > viewportH && rect.top > estimatedMenuH) {
      menu.classList.add('chip-select-menu-up');
    }
  }

  // Trigger interactions.
  trigger.addEventListener('click', () => {
    if (isOpen) {
      closeMenu();
    } else {
      openMenu();
    }
  });
  // Single keydown handler on the trigger — captures wasOpen before any
  // mutation so open and navigate are mutually exclusive for the same event.
  // Previously two separate listeners caused ArrowDown to both open the menu
  // (setting activeIndex) and immediately advance it by one.
  trigger.addEventListener('keydown', (ev) => {
    const wasOpen = isOpen;
    if (!wasOpen) {
      // Closed — open on Enter / Space / ArrowDown.
      if (ev.key === 'Enter' || ev.key === ' ' || ev.key === 'ArrowDown') {
        ev.preventDefault();
        openMenu();
      }
      return;
    }
    // Already open — navigate.
    const visible = visibleOptions();
    if (ev.key === 'ArrowDown') {
      ev.preventDefault();
      activeIndex = Math.min(activeIndex + 1, visible.length - 1);
      renderOptions();
    } else if (ev.key === 'ArrowUp') {
      ev.preventDefault();
      activeIndex = Math.max(activeIndex - 1, 0);
      renderOptions();
    } else if (ev.key === 'Enter') {
      ev.preventDefault();
      const pick = visible[activeIndex];
      if (pick) selectByValue(pick.value);
    }
  });

  // Search input interactions.
  search.addEventListener('input', () => {
    filterText = search.value;
    activeIndex = 0;
    renderOptions();
  });
  search.addEventListener('keydown', (ev) => {
    if (ev.key === 'Tab') {
      // Close menu but let Tab continue to the next focusable element.
      closeMenu();
      return;
    }
    if (ev.key === 'ArrowDown') {
      ev.preventDefault();
      activeIndex = Math.min(activeIndex + 1, visibleOptions().length - 1);
      renderOptions();
    } else if (ev.key === 'ArrowUp') {
      ev.preventDefault();
      activeIndex = Math.max(activeIndex - 1, 0);
      renderOptions();
    } else if (ev.key === 'Enter') {
      ev.preventDefault();
      const visible = visibleOptions();
      const pick = visible[activeIndex];
      if (pick) selectByValue(pick.value);
    }
  });

  // Initial render.
  renderTrigger();

  return {
    root,
    getValue: () => currentValue,
    setValue: (v: string): void => {
      currentValue = v;
      renderTrigger();
      if (isOpen) renderOptions();
    },
    setOptions: (opts: readonly ChipSelectOption[]): void => {
      options = opts.slice();
      renderTrigger();
      if (isOpen) {
        syncSearchVisibility();
        renderOptions();
      }
    },
  };
}
