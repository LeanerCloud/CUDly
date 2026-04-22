/**
 * Account Registrations module — admin approval panel for self-registered accounts.
 */

import * as api from '../api';
import type { AccountRegistration } from '../api/registrations';
import type { CloudAccount, CloudAccountRequest } from '../api/accounts';
import { openAccountModal, loadAccountsForProvider } from '../settings';
import { formatDateTime } from '../utils';
import { showToast } from '../toast';
import { confirmDialog } from '../confirmDialog';

type AccountProvider = 'aws' | 'azure' | 'gcp';

/** Build a synthetic CloudAccount from registration fields for the modal. */
function registrationToAccount(reg: AccountRegistration): CloudAccount {
  return {
    id: '',
    name: reg.account_name,
    description: reg.description,
    provider: reg.provider,
    external_id: reg.external_id,
    contact_email: reg.contact_email,
    enabled: false,
    aws_auth_mode: reg.aws_auth_mode as CloudAccount['aws_auth_mode'],
    aws_role_arn: reg.aws_role_arn,
    aws_external_id: reg.aws_external_id,
    azure_subscription_id: reg.azure_subscription_id,
    azure_tenant_id: reg.azure_tenant_id,
    azure_client_id: reg.azure_client_id,
    azure_auth_mode: reg.azure_auth_mode as CloudAccount['azure_auth_mode'],
    gcp_project_id: reg.gcp_project_id,
    gcp_client_email: reg.gcp_client_email,
    gcp_auth_mode: reg.gcp_auth_mode as CloudAccount['gcp_auth_mode'],
    gcp_wif_audience: reg.gcp_wif_audience,
    credentials_configured: false,
    created_at: '',
    updated_at: '',
  };
}

function providerLabel(p: string): string {
  return p === 'aws' ? 'AWS' : p === 'azure' ? 'Azure' : p === 'gcp' ? 'GCP' : p;
}

function createStatusBadge(status: string): HTMLSpanElement {
  const span = document.createElement('span');
  const cls = status === 'pending' ? 'badge-warning'
    : status === 'approved' ? 'badge-success'
    : 'badge-danger';
  span.className = `status-badge ${cls}`;
  span.textContent = status;
  return span;
}

function createCell(content: string | HTMLElement): HTMLTableCellElement {
  const td = document.createElement('td');
  if (typeof content === 'string') {
    td.textContent = content;
  } else {
    td.appendChild(content);
  }
  return td;
}

function createCodeCell(text: string): HTMLTableCellElement {
  const td = document.createElement('td');
  const code = document.createElement('code');
  code.textContent = text;
  td.appendChild(code);
  return td;
}

// Auto-generated registration names often embed a UUID (8-4-4-4-12 hex).
// Dim the UUID portion so the readable prefix (e.g. "Azure") stands out.
const UUID_RE = /([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})/i;

function createNameCell(name: string): HTMLTableCellElement {
  const td = document.createElement('td');
  const match = name.match(UUID_RE);
  if (!match || match.index === undefined) {
    td.textContent = name;
    return td;
  }
  const uuid = match[1] ?? '';
  const before = name.slice(0, match.index);
  const after = name.slice(match.index + uuid.length);
  if (before) td.appendChild(document.createTextNode(before));
  const uuidSpan = document.createElement('span');
  uuidSpan.className = 'text-muted';
  uuidSpan.textContent = uuid;
  td.appendChild(uuidSpan);
  if (after) td.appendChild(document.createTextNode(after));
  return td;
}

function renderRegistrationsTable(container: HTMLElement, regs: AccountRegistration[]): void {
  container.textContent = '';

  if (regs.length === 0) {
    const p = document.createElement('p');
    p.className = 'empty-state';
    p.textContent = 'No registrations found.';
    container.appendChild(p);
    return;
  }

  const table = document.createElement('table');
  table.className = 'data-table';

  const thead = document.createElement('thead');
  const headerRow = document.createElement('tr');
  for (const h of ['Provider', 'Name', 'External ID', 'Contact', 'Submitted', 'Status', 'Actions']) {
    const th = document.createElement('th');
    th.textContent = h;
    headerRow.appendChild(th);
  }
  thead.appendChild(headerRow);
  table.appendChild(thead);

  const tbody = document.createElement('tbody');
  for (const reg of regs) {
    const row = document.createElement('tr');
    row.appendChild(createCell(providerLabel(reg.provider)));
    row.appendChild(createNameCell(reg.account_name));
    row.appendChild(createCodeCell(reg.external_id));
    row.appendChild(createCell(reg.contact_email));
    row.appendChild(createCell(formatDateTime(reg.created_at)));

    const statusTd = document.createElement('td');
    statusTd.appendChild(createStatusBadge(reg.status));
    if (reg.has_credentials) {
      const credBadge = document.createElement('span');
      credBadge.className = 'status-badge badge-info';
      credBadge.textContent = 'creds';
      credBadge.title = 'Credentials included — will be auto-stored on approval';
      credBadge.style.marginLeft = '4px';
      statusTd.appendChild(credBadge);
    }
    row.appendChild(statusTd);

    const actionsTd = document.createElement('td');
    if (reg.status === 'pending') {
      const approveBtn = document.createElement('button');
      approveBtn.className = 'btn btn-small btn-primary';
      approveBtn.textContent = 'Approve';
      approveBtn.addEventListener('click', () => handleApprove(reg));
      actionsTd.appendChild(approveBtn);

      const rejectBtn = document.createElement('button');
      rejectBtn.className = 'btn btn-small btn-danger';
      rejectBtn.textContent = 'Reject';
      rejectBtn.style.marginLeft = '4px';
      rejectBtn.addEventListener('click', () => void handleReject(reg));
      actionsTd.appendChild(rejectBtn);
    } else if (reg.status === 'approved') {
      // After deleteAccount resets the linked registration back to pending, and
      // the 000021 migration healed historical drift, an approved row always has
      // a live cloud_account_id. Render accordingly.
      const span = document.createElement('span');
      span.className = 'text-muted';
      span.textContent = 'Account created';
      actionsTd.appendChild(span);
    } else if (reg.rejection_reason) {
      const span = document.createElement('span');
      span.className = 'text-muted';
      span.textContent = 'Rejected';
      span.title = reg.rejection_reason;
      actionsTd.appendChild(span);
    }
    // Delete button (available for any status)
    const deleteBtn = document.createElement('button');
    deleteBtn.className = 'btn btn-small';
    deleteBtn.textContent = 'Delete';
    deleteBtn.style.marginLeft = '4px';
    deleteBtn.addEventListener('click', () => void handleDelete(reg));
    actionsTd.appendChild(deleteBtn);

    row.appendChild(actionsTd);
    tbody.appendChild(row);
  }
  table.appendChild(tbody);
  container.appendChild(table);
}

/** Load and render registrations list. */
export async function loadRegistrations(): Promise<void> {
  const container = document.getElementById('registrations-list');
  if (!container) return;

  const filterEl = document.getElementById('registrations-status-filter') as HTMLSelectElement | null;
  // Empty value = "All" (default). Don't coerce to "pending" — that would hide
  // approved/rejected registrations after the user approves them.
  const status = filterEl?.value ?? '';

  try {
    const regs = await api.listRegistrations(status || undefined);
    renderRegistrationsTable(container, regs);
  } catch {
    container.textContent = '';
    const p = document.createElement('p');
    p.className = 'error-message';
    p.textContent = 'Failed to load registrations.';
    container.appendChild(p);
  }
}

function handleApprove(reg: AccountRegistration): void {
  const syntheticAccount = registrationToAccount(reg);

  openAccountModal(reg.provider as AccountProvider, syntheticAccount, {
    onSave: async (provider: AccountProvider, request: CloudAccountRequest) => {
      await api.approveRegistration(reg.id, request);
      await loadRegistrations();
      // Refresh the provider's account list so the newly-approved account appears
      // without requiring a page reload.
      await loadAccountsForProvider(provider);
    },
  });
}

async function handleDelete(reg: AccountRegistration): Promise<void> {
  const ok = await confirmDialog({
    title: `Delete registration for "${reg.account_name}"?`,
    body: `Provider: ${reg.provider}. External ID: ${reg.external_id}. This removes the registration request. The target account itself is not affected.`,
    confirmLabel: 'Delete registration',
    destructive: true,
  });
  if (!ok) return;
  try {
    await api.deleteRegistration(reg.id);
    await loadRegistrations();
  } catch {
    showToast({ message: 'Failed to delete registration.', kind: 'error' });
  }
}

async function handleReject(reg: AccountRegistration): Promise<void> {
  const reason = prompt(`Reject registration for "${reg.account_name}"?\n\nOptional reason:`);
  if (reason === null) return; // User cancelled.

  try {
    await api.rejectRegistration(reg.id, reason || undefined);
    await loadRegistrations();
  } catch {
    alert('Failed to reject registration.');
  }
}

/** Initialize the registrations panel: wire filter change and initial load. */
export function initRegistrations(): void {
  const filterEl = document.getElementById('registrations-status-filter');
  filterEl?.addEventListener('change', () => void loadRegistrations());
  void loadRegistrations();
}
