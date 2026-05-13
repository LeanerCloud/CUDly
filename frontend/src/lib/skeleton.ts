/**
 * Loading skeleton helpers (issue #344 T3).
 *
 * Small DOM-construction primitives for shimmer skeleton placeholders.
 * Each helper returns a detached element you can append into the target
 * container before kicking off the fetch. The render-success path then
 * replaces the container's children with the real content for a clean
 * handoff. The render-error path calls `teardownSkeleton(container)`
 * before showing the error so a skeleton never sits beside a stale
 * error message.
 *
 * Lifecycle contract (see plan.md §T3):
 *   - Show:        synchronously at fetch start (no debounce).
 *   - Hide:        the success render replaces children — implicit teardown.
 *   - Hide-error:  call `teardownSkeleton(container)` before `renderError`.
 *
 * All helpers use `createElement` only — no `innerHTML` — to match the
 * codebase's XSS posture from issue #340.
 */

/**
 * A single shimmer block. Width/height are CSS sizes (e.g. "60%", "1rem",
 * "12px"). Useful as a primitive inside higher-level helpers.
 */
export function skeletonBox(width: string, height: string): HTMLElement {
  const el = document.createElement('div');
  el.classList.add('skeleton');
  el.setAttribute('aria-hidden', 'true');
  el.style.width = width;
  el.style.height = height;
  return el;
}

/**
 * N stacked text-line skeletons. Each line is 1rem tall. When
 * `widthVaries` is true the last line is shorter (60%) so the
 * placeholder reads like prose; when false every line is full-width.
 */
export function skeletonText(lines: number, widthVaries = true): HTMLElement {
  const group = document.createElement('div');
  group.classList.add('skeleton-group');
  group.setAttribute('aria-hidden', 'true');
  const safeLines = Math.max(1, Math.floor(lines));
  for (let i = 0; i < safeLines; i += 1) {
    const isLast = i === safeLines - 1;
    const width = widthVaries && isLast ? '60%' : '100%';
    group.appendChild(skeletonBox(width, '1rem'));
  }
  return group;
}

/**
 * KPI tile placeholder — matches the .kpi-tile layout (title line +
 * value line + detail line) so post-render swap doesn't shift the page.
 */
export function skeletonTile(): HTMLElement {
  const tile = document.createElement('div');
  tile.classList.add('card', 'kpi-tile', 'skeleton-tile');
  tile.setAttribute('aria-hidden', 'true');
  tile.appendChild(skeletonBox('50%', '0.875rem')); // title
  tile.appendChild(skeletonBox('70%', '1.75rem')); // value (--cudly-fs-2xl)
  tile.appendChild(skeletonBox('40%', '0.75rem')); // detail
  return tile;
}

/**
 * Table row placeholder with `cols` cells. Renders as a real `<tr>`
 * so it can be appended to an existing `<tbody>` and laid out by the
 * surrounding table's column widths.
 */
export function skeletonRow(cols: number): HTMLTableRowElement {
  const tr = document.createElement('tr');
  tr.classList.add('skeleton-row');
  tr.setAttribute('aria-hidden', 'true');
  const safeCols = Math.max(1, Math.floor(cols));
  for (let i = 0; i < safeCols; i += 1) {
    const td = document.createElement('td');
    td.appendChild(skeletonBox('80%', '1rem'));
    tr.appendChild(td);
  }
  return tr;
}

/**
 * Wipe whatever is currently in `container` (skeleton, real content,
 * stale error message — anything) and mark the container as actively
 * holding a skeleton. The `show*` helpers all start with this so the
 * placeholder *replaces* prior content, never appends to it. The
 * previous implementation called `teardownSkeleton`, which is
 * conditional on the marker being present — that meant a first render
 * (no marker yet) where the container already had real content would
 * end up with skeletons appended below the real content instead of
 * replacing it (CR review on PR #346).
 */
function activateSkeleton(container: HTMLElement): void {
  while (container.firstChild) container.removeChild(container.firstChild);
  container.dataset['skeletonActive'] = '1';
}

/**
 * Replace the children of `container` with a stack of `count` tile
 * skeletons inside a grid wrapper — used for the dashboard KPI grid and
 * Plans cards row.
 */
export function showSkeletonTiles(container: HTMLElement, count: number): void {
  activateSkeleton(container);
  const safeCount = Math.max(1, Math.floor(count));
  for (let i = 0; i < safeCount; i += 1) {
    container.appendChild(skeletonTile());
  }
}

/**
 * Replace the children of `container` with `count` skeleton rows inside
 * a minimal `<table><tbody>` shell. The cols argument controls the
 * number of cells per row so the placeholder matches the eventual table
 * shape (11 cols for history, 8 for RI tables, etc.).
 */
export function showSkeletonRows(container: HTMLElement, count: number, cols: number): void {
  activateSkeleton(container);
  const table = document.createElement('table');
  table.classList.add('skeleton-table');
  const tbody = document.createElement('tbody');
  const safeCount = Math.max(1, Math.floor(count));
  for (let i = 0; i < safeCount; i += 1) {
    tbody.appendChild(skeletonRow(cols));
  }
  table.appendChild(tbody);
  container.appendChild(table);
}

/**
 * Replace the children of `container` with a single shimmer block sized
 * to `width` x `height`. Useful for chart placeholders.
 */
export function showSkeletonBlock(container: HTMLElement, width: string, height: string): void {
  activateSkeleton(container);
  container.appendChild(skeletonBox(width, height));
}

/**
 * Remove any skeleton placeholders inside `container`. Idempotent —
 * safe to call from both the success render path (where the render
 * function already wipes children) and the error path. The
 * `data-skeleton-active` marker is what lets the error path detect a
 * pending skeleton and clear it without disturbing already-rendered
 * content.
 */
export function teardownSkeleton(container: HTMLElement): void {
  if (container.dataset['skeletonActive']) {
    while (container.firstChild) container.removeChild(container.firstChild);
    delete container.dataset['skeletonActive'];
  }
}

/**
 * Returns true when `container` currently has a skeleton active. Used
 * by tests + per-module render functions that need to know whether
 * they're replacing a skeleton or merging into existing content.
 */
export function isSkeletonActive(container: HTMLElement): boolean {
  return container.dataset['skeletonActive'] === '1';
}
