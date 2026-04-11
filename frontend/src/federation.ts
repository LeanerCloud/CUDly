/**
 * Federation Setup module for CUDly
 *
 * Builds per-provider federation subsections inside the Settings page.
 * IaC file content is fetched from the backend (GET /api/federation/iac),
 * which renders generic templates. Target account owners fill in their own
 * values via terraform -var flags when applying.
 * All DOM manipulation uses safe createElement/textContent/appendChild methods.
 */

import { getFederationIaC } from './api';

// ---------------------------------------------------------------------------
// Download helper
// ---------------------------------------------------------------------------

function downloadFile(filename: string, content: string | ArrayBuffer, contentType: string): void {
  const blob = new Blob([content], { type: contentType });
  const a    = document.createElement('a');
  a.href     = URL.createObjectURL(blob);
  a.download = filename;
  document.body.appendChild(a);
  a.click();
  document.body.removeChild(a);
  URL.revokeObjectURL(a.href);
}

// ---------------------------------------------------------------------------
// DOM helpers — no innerHTML
// ---------------------------------------------------------------------------

/**
 * Create a download button that fetches generic IaC content from the backend.
 */
function makeDownloadBtn(
  label: string,
  tooltipText: string,
  target: string,
  source: string,
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

    getFederationIaC(target, source, format)
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

function clearContainer(el: HTMLElement): void {
  while (el.firstChild) el.removeChild(el.firstChild);
}

// ---------------------------------------------------------------------------
// Section builders — generic downloads (not per-account)
// ---------------------------------------------------------------------------

function buildAWSFederationDownloads(source: string): void {
  const container = document.getElementById('aws-federation-accounts');
  if (!container) return;
  clearContainer(container);

  const row = document.createElement('div');
  row.className = 'federation-account-row';

  const actions = document.createElement('div');
  actions.className = 'federation-account-actions';

  if (source === 'aws') {
    actions.appendChild(makeDownloadBtn(
      'Terraform tfvars',
      'Download .tfvars for cross-account IAM role assumption. Fill in source_account_id and run terraform apply in the target account.',
      'aws', 'aws',
    ));
    actions.appendChild(makeDownloadBtn(
      'Download ZIP',
      'Download zip bundle with Terraform module + .tfvars for cross-account federation.',
      'aws', 'aws', 'bundle',
    ));
  } else {
    actions.appendChild(makeDownloadBtn(
      'Terraform tfvars',
      'Download .tfvars for IAM OIDC WIF federation. Fill in your target account details and run terraform apply.',
      'aws', source,
    ));
    actions.appendChild(makeDownloadBtn(
      'CloudFormation params',
      'Download CloudFormation parameters JSON for the AWS WIF template.',
      'aws', source, 'cf-params',
    ));
    actions.appendChild(makeDownloadBtn(
      'Download ZIP',
      'Download zip bundle with Terraform module, .tfvars, CloudFormation template + deploy script.',
      'aws', source, 'bundle',
    ));
  }

  row.appendChild(actions);
  container.appendChild(row);
}

function buildAzureFederationDownloads(source: string): void {
  const container = document.getElementById('azure-federation-accounts');
  if (!container) return;
  clearContainer(container);

  const row = document.createElement('div');
  row.className = 'federation-account-row';

  const actions = document.createElement('div');
  actions.className = 'federation-account-actions';

  actions.appendChild(makeDownloadBtn(
    'Terraform tfvars',
    'Download .tfvars for Azure WIF federation. Generate a cert, fill in subscription/tenant IDs, then terraform apply.',
    'azure', source,
  ));
  actions.appendChild(makeDownloadBtn(
    'Download ZIP',
    'Download zip bundle with Terraform module + .tfvars for Azure WIF federation.',
    'azure', source, 'bundle',
  ));

  row.appendChild(actions);
  container.appendChild(row);
}

function buildGCPFederationDownloads(source: string): void {
  const container = document.getElementById('gcp-federation-accounts');
  if (!container) return;
  clearContainer(container);

  const row = document.createElement('div');
  row.className = 'federation-account-row';

  const actions = document.createElement('div');
  actions.className = 'federation-account-actions';

  if (source === 'gcp') {
    actions.appendChild(makeDownloadBtn(
      'Terraform tfvars',
      'Download .tfvars for GCP SA impersonation. Fill in source/target service account emails, then terraform apply.',
      'gcp', 'gcp',
    ));
    actions.appendChild(makeDownloadBtn(
      'Download ZIP',
      'Download zip bundle with Terraform module + .tfvars for GCP SA impersonation.',
      'gcp', 'gcp', 'bundle',
    ));
  } else {
    actions.appendChild(makeDownloadBtn(
      'Terraform tfvars',
      'Download .tfvars for GCP WIF federation. Fill in project/SA details, then terraform apply.',
      'gcp', source,
    ));
    actions.appendChild(makeDownloadBtn(
      'Download ZIP',
      'Download zip bundle with Terraform module + .tfvars for GCP WIF federation.',
      'gcp', source, 'bundle',
    ));
  }

  row.appendChild(actions);
  container.appendChild(row);
}

// ---------------------------------------------------------------------------
// Public entry point
// ---------------------------------------------------------------------------

/**
 * Render the federation download subsections within each provider fieldset
 * in the Settings page. No account loading needed — templates are generic.
 */
export async function initFederationSections(): Promise<void> {
  const awsFedSource = document.getElementById('aws-fed-source') as HTMLSelectElement | null;
  const azureFedSource = document.getElementById('azure-fed-source') as HTMLSelectElement | null;
  const gcpFedSource = document.getElementById('gcp-fed-source') as HTMLSelectElement | null;

  buildAWSFederationDownloads(awsFedSource?.value ?? 'aws');
  buildAzureFederationDownloads(azureFedSource?.value ?? 'aws');
  buildGCPFederationDownloads(gcpFedSource?.value ?? 'aws');

  awsFedSource?.addEventListener('change', () => {
    buildAWSFederationDownloads(awsFedSource.value);
  });

  azureFedSource?.addEventListener('change', () => {
    buildAzureFederationDownloads(azureFedSource.value);
  });

  gcpFedSource?.addEventListener('change', () => {
    buildGCPFederationDownloads(gcpFedSource.value);
  });
}
