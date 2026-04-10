/**
 * Federation Setup module for CUDly
 *
 * Builds per-provider federation subsections inside the Settings page.
 * IaC file content is fetched from the backend (GET /api/federation/iac),
 * which renders templates embedded from internal/iacfiles/templates/.
 * All DOM manipulation uses safe createElement/textContent/appendChild methods.
 */

import type { CloudAccount } from './api/accounts';
import { listAccounts, getFederationIaC } from './api';

// ---------------------------------------------------------------------------
// Download helper
// ---------------------------------------------------------------------------

function downloadFile(filename: string, content: string | ArrayBuffer, mimeType = 'text/plain'): void {
  const blob = new Blob([content], { type: mimeType });
  const url  = URL.createObjectURL(blob);
  const a    = document.createElement('a');
  a.href     = url;
  a.download = filename;
  document.body.appendChild(a);
  a.click();
  document.body.removeChild(a);
  setTimeout(() => URL.revokeObjectURL(url), 1000);
}

// ---------------------------------------------------------------------------
// DOM helpers — no innerHTML
// ---------------------------------------------------------------------------

/**
 * Create a download button that fetches IaC content from the backend when clicked.
 * Shows a brief "loading…" state while the request is in flight.
 */
function makeDownloadBtn(
  label: string,
  tooltipText: string,
  target: string,
  source: string,
  accountId: string,
  format?: string,
): HTMLButtonElement {
  const btn       = document.createElement('button');
  btn.type        = 'button';
  btn.className   = 'btn btn-small';
  btn.textContent = label;
  btn.title       = tooltipText;

  btn.addEventListener('click', () => {
    btn.disabled    = true;
    btn.textContent = 'Loading…';

    getFederationIaC(target, source, accountId, format)
      .then(res => {
        if (res.content_encoding === 'base64') {
          const binaryStr = atob(res.content);
          const bytes = new Uint8Array(binaryStr.length);
          for (let i = 0; i < binaryStr.length; i++) bytes[i] = binaryStr.charCodeAt(i);
          downloadFile(res.filename, bytes.buffer as ArrayBuffer, res.content_type);
        } else {
          downloadFile(res.filename, res.content, res.content_type);
        }
      })
      .catch((err: unknown) => {
        console.error('Federation IaC download failed:', err);
        alert(`Failed to generate IaC: ${(err as Error).message}`);
      })
      .finally(() => {
        btn.disabled    = false;
        btn.textContent = label;
      });
  });

  return btn;
}

function makeAccountRow(account: CloudAccount, buttons: HTMLButtonElement[]): HTMLElement {
  const row       = document.createElement('div');
  row.className   = 'federation-account-row';

  const info      = document.createElement('div');
  info.className  = 'federation-account-info';

  const nameSpan  = document.createElement('span');
  nameSpan.className   = 'federation-account-name';
  nameSpan.textContent = account.name;

  const idSpan    = document.createElement('span');
  idSpan.className   = 'federation-account-id';
  idSpan.textContent = account.external_id;

  info.appendChild(nameSpan);
  info.appendChild(document.createTextNode(' '));
  info.appendChild(idSpan);

  const actions   = document.createElement('div');
  actions.className = 'federation-account-actions';
  buttons.forEach(btn => actions.appendChild(btn));

  row.appendChild(info);
  row.appendChild(actions);
  return row;
}

function clearContainer(el: HTMLElement): void {
  while (el.firstChild) el.removeChild(el.firstChild);
}

function emptyMessage(text: string): HTMLParagraphElement {
  const p       = document.createElement('p');
  p.className   = 'federation-empty';
  p.textContent = text;
  return p;
}

// ---------------------------------------------------------------------------
// Section builders
// ---------------------------------------------------------------------------

function buildAWSFederationAccounts(accounts: CloudAccount[], source: string): void {
  const container = document.getElementById('aws-federation-accounts');
  if (!container) return;
  clearContainer(container);

  if (accounts.length === 0) {
    container.appendChild(
      emptyMessage('No AWS accounts registered. Add accounts above to generate federation IaC.'),
    );
    return;
  }

  if (source === 'aws') {
    // Same-cloud: cross-account IAM role assumption
    accounts.forEach(account => {
      const tfvarsBtn = makeDownloadBtn(
        'Terraform tfvars',
        'Download pre-filled .tfvars for iac/federation/aws-cross-account/terraform. ' +
        'Creates a cross-account IAM role in this target account. ' +
        'Set aws_auth_mode=role_arn in CUDly after applying.',
        'aws', 'aws', account.id,
      );
      const bundleBtn = makeDownloadBtn(
        'Download ZIP',
        'Download a zip bundle containing the Terraform module files and pre-filled .tfvars. ' +
        'Unzip, fill in source_account_id, then run: cd terraform && terraform init && terraform apply.',
        'aws', 'aws', account.id, 'bundle',
      );
      container.appendChild(makeAccountRow(account, [tfvarsBtn, bundleBtn]));
    });
    return;
  }

  // Cross-cloud: OIDC WIF
  accounts.forEach(account => {
    const tfvarsBtn = makeDownloadBtn(
      'Terraform tfvars',
      'Download pre-filled .tfvars for iac/federation/aws-target/terraform. ' +
      'Sets up an IAM OIDC provider and CUDly IAM role in this AWS account.',
      'aws', source, account.id,
    );

    const cfBtn = makeDownloadBtn(
      'CloudFormation params',
      'Download pre-filled parameters JSON for iac/federation/aws-target/cloudformation/template.yaml.',
      'aws', source, account.id, 'cf-params',
    );

    const bundleBtn = makeDownloadBtn(
      'Download ZIP',
      'Download a zip bundle with the Terraform module, pre-filled .tfvars, ' +
      'CloudFormation template + parameters, and a ready-to-run deploy-cfn.sh script.',
      'aws', source, account.id, 'bundle',
    );

    container.appendChild(makeAccountRow(account, [tfvarsBtn, cfBtn, bundleBtn]));
  });
}

function buildAzureFederationAccounts(accounts: CloudAccount[]): void {
  const container = document.getElementById('azure-federation-accounts');
  if (!container) return;
  clearContainer(container);

  if (accounts.length === 0) {
    container.appendChild(
      emptyMessage('No Azure accounts registered. Add accounts above to generate federation IaC.'),
    );
    return;
  }

  accounts.forEach(account => {
    const tfvarsBtn = makeDownloadBtn(
      'Terraform tfvars',
      'Download pre-filled .tfvars for iac/federation/azure-target/terraform. ' +
      'Creates an Azure App Registration with a certificate credential and grants the ' +
      '"Reservations Administrator" role on the subscription.',
      'azure', 'any', account.id,
    );

    const bundleBtn = makeDownloadBtn(
      'Download ZIP',
      'Download a zip bundle containing the Terraform module files and pre-filled .tfvars. ' +
      'Generate an RSA key + cert first (see tfvars comments), then terraform apply.',
      'azure', 'any', account.id, 'bundle',
    );

    container.appendChild(makeAccountRow(account, [tfvarsBtn, bundleBtn]));
  });
}

function buildGCPFederationAccounts(accounts: CloudAccount[], source: string): void {
  const container = document.getElementById('gcp-federation-accounts');
  if (!container) return;
  clearContainer(container);

  if (accounts.length === 0) {
    container.appendChild(
      emptyMessage('No GCP accounts registered. Add accounts above to generate federation IaC.'),
    );
    return;
  }

  if (source === 'gcp') {
    // Same-cloud: service account impersonation
    accounts.forEach(account => {
      const tfvarsBtn = makeDownloadBtn(
        'Terraform tfvars',
        'Download pre-filled .tfvars for iac/federation/gcp-sa-impersonation/terraform. ' +
        'Grants your source GCP service account the iam.serviceAccountTokenCreator role ' +
        'on this project\'s service account. Set gcp_auth_mode=application_default in CUDly after applying.',
        'gcp', 'gcp', account.id,
      );
      const bundleBtn = makeDownloadBtn(
        'Download ZIP',
        'Download a zip bundle with the Terraform module and pre-filled .tfvars. ' +
        'Fill in source_service_account, then terraform apply.',
        'gcp', 'gcp', account.id, 'bundle',
      );
      container.appendChild(makeAccountRow(account, [tfvarsBtn, bundleBtn]));
    });
    return;
  }

  // Cross-cloud: WIF pool + provider
  accounts.forEach(account => {
    const tfvarsBtn = makeDownloadBtn(
      'Terraform tfvars',
      'Download pre-filled .tfvars for iac/federation/gcp-target/terraform. ' +
      'Creates a Workload Identity Pool and provider, and grants the service account ' +
      '"roles/iam.workloadIdentityUser" binding. No service account keys generated.',
      'gcp', source, account.id,
    );

    const bundleBtn = makeDownloadBtn(
      'Download ZIP',
      'Download a zip bundle with the Terraform module and pre-filled .tfvars. ' +
      'After terraform apply, run the gcloud_command output to generate the WIF credential JSON.',
      'gcp', source, account.id, 'bundle',
    );

    container.appendChild(makeAccountRow(account, [tfvarsBtn, bundleBtn]));
  });
}

// ---------------------------------------------------------------------------
// Public entry point
// ---------------------------------------------------------------------------

/**
 * Load accounts and render the federation subsections within each provider
 * fieldset in the Settings page. Safe to call multiple times (re-renders).
 */
export async function initFederationSections(): Promise<void> {
  let awsAccounts:   CloudAccount[] = [];
  let azureAccounts: CloudAccount[] = [];
  let gcpAccounts:   CloudAccount[] = [];

  try {
    [awsAccounts, azureAccounts, gcpAccounts] = await Promise.all([
      listAccounts({ provider: 'aws' }),
      listAccounts({ provider: 'azure' }),
      listAccounts({ provider: 'gcp' }),
    ]);
  } catch (err) {
    console.error('Federation: failed to load accounts', err);
    return;
  }

  const awsFedSource = document.getElementById('aws-fed-source') as HTMLSelectElement | null;
  const gcpFedSource = document.getElementById('gcp-fed-source') as HTMLSelectElement | null;

  buildAWSFederationAccounts(awsAccounts, awsFedSource?.value ?? 'aws');
  buildAzureFederationAccounts(azureAccounts);
  buildGCPFederationAccounts(gcpAccounts, gcpFedSource?.value ?? 'aws');

  // Re-render affected section when source cloud changes
  awsFedSource?.addEventListener('change', () => {
    buildAWSFederationAccounts(awsAccounts, awsFedSource.value);
  });

  gcpFedSource?.addEventListener('change', () => {
    buildGCPFederationAccounts(gcpAccounts, gcpFedSource.value);
  });
}
