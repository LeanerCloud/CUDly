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
export function renderPermissionMatrix(groups: APIGroup[], container: HTMLElement): void {
  if (groups.length === 0) {
    container.innerHTML = '<p style="color:#999;font-size:0.85rem;">No groups defined.</p>';
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
        return `<td class="has-perm"${titleAttr}>\u2713</td>`;
      }
      return '<td class="no-perm">\u2014</td>';
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
