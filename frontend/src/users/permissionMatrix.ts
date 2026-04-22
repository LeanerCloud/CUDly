/**
 * Read-only permission matrix rendering
 *
 * Displays a table with actions as rows and groups as columns,
 * showing which actions each group has for any resource.
 */

import type { APIGroup } from '../api';
import { escapeHtml } from './utils';

const ACTIONS = ['view', 'create', 'update', 'delete', 'execute', 'approve', 'admin'] as const;

/**
 * Render a permission matrix table into the given container.
 * Rows = actions, Columns = group names, Cells = checkmark or dash.
 *
 * All dynamic values are escaped via escapeHtml before insertion.
 */
function renderEmptyState(container: HTMLElement, message: string): void {
  container.textContent = '';
  const p = document.createElement('p');
  p.className = 'empty-state';
  p.textContent = message;
  container.appendChild(p);
}

export function renderPermissionMatrix(groups: APIGroup[], container: HTMLElement): void {
  if (groups.length === 0) {
    renderEmptyState(container, 'No groups defined yet. Create one to assign permissions.');
    return;
  }

  // Groups exist but none carry permissions — matrix would be all dashes.
  // Show an explicit empty-state instead so the section doesn't read as
  // broken or unfinished.
  const anyPermission = groups.some(g => g.permissions && g.permissions.length > 0);
  if (!anyPermission) {
    renderEmptyState(container, 'No custom permissions configured yet. Edit a group to assign actions to resources.');
    return;
  }

  const headerCells = groups
    .map(g => `<th>${escapeHtml(g.name)}</th>`)
    .join('');

  const rows = ACTIONS.map(action => {
    const cells = groups.map(group => {
      const matchingResources = group.permissions
        .filter(p => p.action === action)
        .map(p => p.resource);

      if (matchingResources.length > 0) {
        const titleAttr = ` title="${escapeHtml(matchingResources.join(', '))}"`;
        // WCAG 1.4.1: pair the ✓ glyph with an sr-only label so screen
        // readers announce "Granted" instead of reading nothing / reading
        // a bare checkmark symbol.
        return `<td class="has-perm"${titleAttr} aria-label="Granted">\u2713<span class="sr-only"> Granted</span></td>`;
      }
      return '<td class="no-perm" aria-label="Not granted">\u2014<span class="sr-only"> Not granted</span></td>';
    }).join('');

    return `<tr><td>${escapeHtml(action)}</td>${cells}</tr>`;
  }).join('');

  // All dynamic content (group names, action names, resources) is escaped
  // via escapeHtml before insertion — safe against XSS.
  container.innerHTML = `
    <table class="permission-matrix">
      <thead>
        <tr><th>Action</th>${headerCells}</tr>
      </thead>
      <tbody>
        ${rows}
      </tbody>
    </table>
  `;
}
