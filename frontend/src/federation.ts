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
// Format matrix — which formats apply per target/source combination.
// Dropdown values use "tfvars" as the DOM value; the download handler maps
// it to an empty format= query param (the server's default).
// ---------------------------------------------------------------------------

interface FormatOption {
  value: string;  // DOM value; "tfvars" maps to empty format=
  label: string;
  title: string;
}

function formatOptionsFor(target: string): FormatOption[] {
  const opts: FormatOption[] = [
    { value: 'tfvars', label: 'Terraform tfvars', title: 'Single .tfvars file for Terraform.' },
    { value: 'cli', label: 'CLI script', title: 'Self-contained shell script using the cloud\'s official CLI.' },
  ];
  // CloudFormation applies for any AWS target (cross-account + WIF both supported).
  if (target === 'aws') {
    opts.push({
      value: 'cfn',
      label: 'CloudFormation',
      title: 'Zip with CloudFormation template, parameters, and deploy script.',
    });
  }
  // Azure-specific DSLs: Bicep + its compiled ARM equivalent.
  if (target === 'azure') {
    opts.push({
      value: 'bicep',
      label: 'Bicep',
      title: 'Zip with Bicep template, parameters, and deploy script. Run the CLI script first for identity setup.',
    });
    opts.push({
      value: 'arm',
      label: 'ARM Template',
      title: 'Zip with ARM JSON template, parameters, and deploy script. Run the CLI script first for identity setup.',
    });
  }
  return opts;
}

// ---------------------------------------------------------------------------
// Generic provider section builder — dropdown + Download + Bundle buttons
// ---------------------------------------------------------------------------

function buildFederationDownloads(target: string, source: string, containerID: string): void {
  const container = document.getElementById(containerID);
  if (!container) return;
  clearContainer(container);

  const row = document.createElement('div');
  row.className = 'federation-account-row';

  const actions = document.createElement('div');
  actions.className = 'federation-account-actions';

  const select = document.createElement('select');
  select.className = 'input-small';
  for (const opt of formatOptionsFor(target)) {
    const o = document.createElement('option');
    o.value = opt.value;
    o.textContent = opt.label;
    o.title = opt.title;
    select.appendChild(o);
  }

  const downloadBtn = document.createElement('button');
  downloadBtn.type = 'button';
  downloadBtn.className = 'btn btn-small';
  downloadBtn.textContent = 'Download';
  downloadBtn.title = 'Download the selected IaC format.';
  downloadBtn.addEventListener('click', () => {
    const selected = select.value;
    const format = selected === 'tfvars' ? undefined : selected;
    runDownload(downloadBtn, target, source, format);
  });

  actions.appendChild(select);
  actions.appendChild(downloadBtn);

  // Always-available bundle shortcut (zip with tfvars + terraform module + cfn when applicable).
  actions.appendChild(makeDownloadBtn(
    'Download bundle (Terraform)',
    'Zip bundle with Terraform module + .tfvars (and CloudFormation files for AWS WIF).',
    target, source, 'bundle',
  ));

  row.appendChild(actions);
  container.appendChild(row);
}

// runDownload fetches IaC from the backend and triggers a browser download.
// Shared with makeDownloadBtn's click handler but parameterised at call time.
function runDownload(btn: HTMLButtonElement, target: string, source: string, format?: string): void {
  const originalLabel = btn.textContent ?? 'Download';
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
      btn.textContent = originalLabel;
    });
}

// ---------------------------------------------------------------------------
// Public entry point
// ---------------------------------------------------------------------------

/**
 * Render the federation download panel in the consolidated Accounts section.
 * The `source` parameter comes from the backend (CUDLY_SOURCE_CLOUD env var)
 * and is fixed — users pick the target cloud via the dropdown.
 */
export async function initFederationPanel(source: string): Promise<void> {
  const targetSelect = document.getElementById('federation-target-provider') as HTMLSelectElement | null;
  if (!targetSelect) return;

  const render = (): void => {
    buildFederationDownloads(targetSelect.value, source, 'federation-setup-panel');
  };

  render();
  targetSelect.addEventListener('change', render);
}
