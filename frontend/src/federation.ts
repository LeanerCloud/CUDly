/**
 * Federation Setup module for CUDly
 *
 * Builds the target-cloud pills + per-format download buttons in the Settings →
 * Accounts → Federation Setup panel. IaC content is fetched from the backend
 * (GET /api/federation/iac) — target account owners fill in their own values
 * via terraform -var flags when applying.
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

function clearContainer(el: HTMLElement): void {
  while (el.firstChild) el.removeChild(el.firstChild);
}

// ---------------------------------------------------------------------------
// Format matrix — which formats apply per target cloud.
// ---------------------------------------------------------------------------

interface FormatOption {
  value: string;     // backend format code
  label: string;     // bold first line
  sublabel: string;  // dim second line
  title: string;     // tooltip
}

function formatOptionsFor(target: string): FormatOption[] {
  const bundleSublabel = target === 'aws'
    ? 'full Terraform module + variables + CloudFormation fallback'
    : 'full Terraform module + variables';

  const opts: FormatOption[] = [
    {
      value: 'bundle',
      label: 'Terraform bundle',
      sublabel: bundleSublabel,
      title: 'Zip with Terraform module + .tfvars, ready to terraform apply.',
    },
    {
      value: 'cli',
      label: 'CLI script',
      sublabel: 'self-contained shell script',
      title: 'Self-contained shell script using the cloud\'s official CLI.',
    },
  ];

  if (target === 'aws') {
    opts.push({
      value: 'cfn',
      label: 'CloudFormation',
      sublabel: 'template + params + deploy script (zip)',
      title: 'Zip with CloudFormation template, parameters, and deploy script.',
    });
  }

  if (target === 'azure') {
    opts.push({
      value: 'bicep',
      label: 'Bicep',
      sublabel: 'template + params + deploy script (zip)',
      title: 'Zip with Bicep template, parameters, and deploy script. Run the CLI script first for identity setup.',
    });
    opts.push({
      value: 'arm',
      label: 'ARM Template',
      sublabel: 'template + params + deploy script (zip)',
      title: 'Zip with ARM JSON template, parameters, and deploy script. Run the CLI script first for identity setup.',
    });
  }

  return opts;
}

// ---------------------------------------------------------------------------
// Format button rendering
// ---------------------------------------------------------------------------

function makeFormatButton(opt: FormatOption, target: string, source: string): HTMLButtonElement {
  const btn = document.createElement('button');
  btn.type = 'button';
  btn.className = 'btn btn-small btn-multiline btn-download';
  btn.title = opt.title;
  // Make the button's download affordance obvious to assistive tech
  // (the visual label alone reads like static text; aria-label + the
  // .btn-download class together signal the click action).
  btn.setAttribute('aria-label', `Download ${opt.label} (${opt.sublabel}) for ${target.toUpperCase()}`);

  // A leading ↓ glyph communicates "download" visually. aria-hidden so
  // screen readers rely on the aria-label above, not the glyph.
  const iconSpan = document.createElement('span');
  iconSpan.className = 'download-icon';
  iconSpan.setAttribute('aria-hidden', 'true');
  iconSpan.textContent = '↓';
  btn.appendChild(iconSpan);

  const labelSpan = document.createElement('span');
  labelSpan.className = 'label';
  labelSpan.textContent = opt.label;
  btn.appendChild(labelSpan);

  const sublabelSpan = document.createElement('span');
  sublabelSpan.className = 'sublabel';
  sublabelSpan.textContent = opt.sublabel;
  btn.appendChild(sublabelSpan);

  btn.addEventListener('click', () => {
    runDownload(btn, target, source, opt.value);
  });

  return btn;
}

function buildFederationDownloads(target: string, source: string, containerID: string): void {
  const container = document.getElementById(containerID);
  if (!container) return;
  clearContainer(container);

  const row = document.createElement('div');
  row.className = 'federation-format-buttons';

  for (const opt of formatOptionsFor(target)) {
    row.appendChild(makeFormatButton(opt, target, source));
  }

  container.appendChild(row);
}

// runDownload fetches IaC from the backend and triggers a browser download.
function runDownload(btn: HTMLButtonElement, target: string, source: string, format: string): void {
  const labelEl = btn.querySelector<HTMLElement>('.label');
  const originalLabel = labelEl?.textContent ?? '';
  btn.disabled = true;
  if (labelEl) labelEl.textContent = 'Loading…';

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
      btn.disabled = false;
      if (labelEl) labelEl.textContent = originalLabel;
    });
}

// ---------------------------------------------------------------------------
// Target-cloud pills
// ---------------------------------------------------------------------------

interface TargetCloudOption {
  value: string;
  label: string;
}

const TARGET_CLOUDS = [
  { value: 'aws',   label: 'AWS' },
  { value: 'azure', label: 'Azure' },
  { value: 'gcp',   label: 'GCP' },
] as const satisfies readonly TargetCloudOption[];

const DEFAULT_TARGET_CLOUD: string = TARGET_CLOUDS[0].value;

function buildTargetCloudPills(
  container: HTMLElement,
  onSelect: (target: string) => void,
): void {
  clearContainer(container);

  const buttons: HTMLButtonElement[] = [];

  for (const cloud of TARGET_CLOUDS) {
    const pill = document.createElement('button');
    pill.type = 'button';
    pill.className = 'btn btn-small target-cloud-pill';
    pill.textContent = cloud.label;
    pill.setAttribute('data-target', cloud.value);
    pill.setAttribute('aria-pressed', 'false');
    pill.addEventListener('click', () => {
      for (const b of buttons) {
        const selected = b === pill;
        b.setAttribute('aria-pressed', selected ? 'true' : 'false');
        b.classList.toggle('selected', selected);
      }
      onSelect(cloud.value);
    });
    buttons.push(pill);
    container.appendChild(pill);
  }

  // Default selection: AWS (first option).
  const first = buttons[0];
  if (first) {
    first.setAttribute('aria-pressed', 'true');
    first.classList.add('selected');
  }
}

// ---------------------------------------------------------------------------
// Public entry point
// ---------------------------------------------------------------------------

/**
 * Render the federation download panel in the consolidated Accounts section.
 * The `source` parameter comes from the backend (CUDLY_SOURCE_CLOUD env var)
 * and is fixed — users pick the target cloud via the pill buttons.
 */
export async function initFederationPanel(source: string): Promise<void> {
  const pillContainer = document.getElementById('federation-target-cloud-pills');
  if (!pillContainer) return;

  buildTargetCloudPills(pillContainer, target => {
    buildFederationDownloads(target, source, 'federation-setup-panel');
  });

  // Initial render with the default target (AWS).
  buildFederationDownloads(DEFAULT_TARGET_CLOUD, source, 'federation-setup-panel');
}
