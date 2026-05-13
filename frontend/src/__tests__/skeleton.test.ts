/**
 * Skeleton helper tests (issue #344 T3).
 *
 * Verifies the show / teardown lifecycle and that the helpers produce
 * the DOM shape each call site expects (tile / row / box).
 */

import {
  skeletonBox,
  skeletonText,
  skeletonTile,
  skeletonRow,
  showSkeletonTiles,
  showSkeletonRows,
  showSkeletonBlock,
  teardownSkeleton,
  isSkeletonActive,
} from '../lib/skeleton';

describe('skeleton primitives', () => {
  test('skeletonBox renders a div with .skeleton + width/height styles', () => {
    const el = skeletonBox('80%', '1rem');
    expect(el.tagName).toBe('DIV');
    expect(el.classList.contains('skeleton')).toBe(true);
    expect(el.getAttribute('aria-hidden')).toBe('true');
    expect(el.style.width).toBe('80%');
    expect(el.style.height).toBe('1rem');
  });

  test('skeletonText emits N lines with a shorter final line by default', () => {
    const group = skeletonText(3);
    expect(group.children).toHaveLength(3);
    const last = group.children[2] as HTMLElement;
    expect(last.style.width).toBe('60%');
    const first = group.children[0] as HTMLElement;
    expect(first.style.width).toBe('100%');
  });

  test('skeletonText with widthVaries=false makes every line full-width', () => {
    const group = skeletonText(2, false);
    expect(group.children).toHaveLength(2);
    Array.from(group.children).forEach((child) => {
      expect((child as HTMLElement).style.width).toBe('100%');
    });
  });

  test('skeletonText clamps non-positive line counts to 1', () => {
    expect(skeletonText(0).children).toHaveLength(1);
    expect(skeletonText(-3).children).toHaveLength(1);
  });

  test('skeletonTile has title + value + detail rows in tile shape', () => {
    const tile = skeletonTile();
    expect(tile.classList.contains('kpi-tile')).toBe(true);
    expect(tile.classList.contains('skeleton-tile')).toBe(true);
    expect(tile.children).toHaveLength(3);
  });

  test('skeletonRow renders a <tr> with N <td>s, each containing a skeleton', () => {
    const tr = skeletonRow(5);
    expect(tr.tagName).toBe('TR');
    expect(tr.classList.contains('skeleton-row')).toBe(true);
    expect(tr.children).toHaveLength(5);
    Array.from(tr.children).forEach((td) => {
      expect(td.tagName).toBe('TD');
      expect(td.querySelector('.skeleton')).not.toBeNull();
    });
  });

  test('skeletonRow clamps non-positive col counts to 1', () => {
    expect(skeletonRow(0).children).toHaveLength(1);
    expect(skeletonRow(-2).children).toHaveLength(1);
  });
});

describe('skeleton show / teardown lifecycle', () => {
  let container: HTMLElement;

  beforeEach(() => {
    container = document.createElement('div');
    document.body.appendChild(container);
  });

  afterEach(() => {
    document.body.removeChild(container);
  });

  test('showSkeletonTiles fills container with N tile skeletons + marks active', () => {
    showSkeletonTiles(container, 4);
    expect(container.querySelectorAll('.skeleton-tile')).toHaveLength(4);
    expect(isSkeletonActive(container)).toBe(true);
  });

  test('showSkeletonRows fills container with N row skeletons + table shell', () => {
    showSkeletonRows(container, 8, 6);
    expect(container.querySelector('table.skeleton-table')).not.toBeNull();
    expect(container.querySelectorAll('tr.skeleton-row')).toHaveLength(8);
    expect(container.querySelectorAll('tr.skeleton-row td')).toHaveLength(8 * 6);
    expect(isSkeletonActive(container)).toBe(true);
  });

  test('showSkeletonBlock fills container with one sized shimmer block', () => {
    showSkeletonBlock(container, '100%', '12rem');
    const skel = container.querySelector('.skeleton') as HTMLElement;
    expect(skel).not.toBeNull();
    expect(skel.style.width).toBe('100%');
    expect(skel.style.height).toBe('12rem');
    expect(isSkeletonActive(container)).toBe(true);
  });

  test('teardownSkeleton wipes children when active + clears the marker', () => {
    showSkeletonTiles(container, 3);
    teardownSkeleton(container);
    expect(container.children).toHaveLength(0);
    expect(isSkeletonActive(container)).toBe(false);
  });

  test('teardownSkeleton is a no-op when no skeleton is active', () => {
    // Container has real content (post-render); teardown must NOT wipe it.
    const real = document.createElement('p');
    real.textContent = 'rendered';
    container.appendChild(real);
    teardownSkeleton(container);
    expect(container.children).toHaveLength(1);
    expect(container.firstChild).toBe(real);
  });

  test('successive show* calls swap the placeholder without leaking prior children', () => {
    showSkeletonTiles(container, 4);
    showSkeletonRows(container, 5, 3);
    expect(container.querySelectorAll('.skeleton-tile')).toHaveLength(0);
    expect(container.querySelectorAll('tr.skeleton-row')).toHaveLength(5);
  });

  // PR #346 CR follow-up: the previous implementation called
  // teardownSkeleton (which is conditional on the marker) so a first
  // render where the container already held real content would APPEND
  // skeletons below it instead of replacing them. The show* helpers
  // must always clear children unconditionally.
  test('show* helpers replace existing real content unconditionally on first render', () => {
    const stale = document.createElement('p');
    stale.textContent = 'previous content (no skeleton marker)';
    container.appendChild(stale);
    expect(container.children).toHaveLength(1);

    showSkeletonTiles(container, 3);

    // The pre-existing <p> must be gone, not still sitting at the top.
    expect(container.querySelector('p')).toBeNull();
    expect(container.querySelectorAll('.skeleton-tile')).toHaveLength(3);
    expect(isSkeletonActive(container)).toBe(true);
  });

  test('showSkeletonRows also clears unmarked prior content', () => {
    const stale = document.createElement('div');
    stale.classList.add('error');
    stale.textContent = 'old error';
    container.appendChild(stale);

    showSkeletonRows(container, 4, 5);

    expect(container.querySelector('.error')).toBeNull();
    expect(container.querySelectorAll('tr.skeleton-row')).toHaveLength(4);
  });

  test('showSkeletonBlock also clears unmarked prior content', () => {
    const stale = document.createElement('canvas');
    container.appendChild(stale);

    showSkeletonBlock(container, '100%', '6rem');

    expect(container.querySelector('canvas')).toBeNull();
    expect(container.querySelectorAll('.skeleton')).toHaveLength(1);
  });
});
