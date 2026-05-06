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
// Module-level state for the Archera opt-in checkbox.
// The checkbox persists its checked state for the lifetime of the panel —
// toggling it immediately affects the next download without a page reload.
// ---------------------------------------------------------------------------

let _includeArchera = false;

/**
 * Reset module-level state for testing. Not called in production code.
 * @internal
 */
export function _resetIncludeArcheraForTesting(): void {
  _includeArchera = false;
}

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

// ---------------------------------------------------------------------------
// Archera checkbox
// ---------------------------------------------------------------------------

const ARCHERA_TOOLTIP =
  'When enabled, the downloaded bundle additionally provisions a cross-account ' +
  'role / service principal that grants the Archera commitment-insurance platform ' +
  'read-only access to your cost data and (optionally) the right to purchase ' +
  'reservations / savings plans on your behalf. This lets Archera underwrite ' +
  'commitment-overuse insurance for your account. Leave unchecked if you\'re not ' +
  'enrolled with Archera. Default: off.';

/**
 * Build the Archera opt-in checkbox row and append it to containerEl.
 * Returns the checkbox element so callers can read its checked state.
 */
function buildArcheraCheckbox(containerEl: HTMLElement): HTMLInputElement {
  const row = document.createElement('div');
  row.className = 'federation-archera-row';

  const label = document.createElement('label');
  label.className = 'federation-archera-label';

  const checkbox = document.createElement('input');
  checkbox.type = 'checkbox';
  checkbox.id = 'federation-include-archera';
  checkbox.name = 'include_archera';
  // Reflect current module state so repeated renders stay in sync with
  // the _includeArchera flag (e.g. hot-reload or SPA re-navigation).
  checkbox.checked = _includeArchera;
  checkbox.addEventListener('change', () => {
    _includeArchera = checkbox.checked;
  });

  const labelText = document.createElement('span');
  labelText.textContent = 'Provision Archera Insurance permissions?';

  // Tooltip via title attribute — accessible on keyboard focus and hover.
  label.title = ARCHERA_TOOLTIP;
  label.setAttribute('aria-describedby', 'federation-archera-tooltip');

  const tooltipSpan = document.createElement('span');
  tooltipSpan.id = 'federation-archera-tooltip';
  tooltipSpan.className = 'federation-archera-tooltip sr-only';
  tooltipSpan.textContent = ARCHERA_TOOLTIP;

  label.appendChild(checkbox);
  label.appendChild(labelText);
  row.appendChild(label);
  row.appendChild(tooltipSpan);
  containerEl.appendChild(row);

  return checkbox;
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

  getFederationIaC(target, source, format, _includeArchera)
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

  // Render the Archera opt-in checkbox above the download buttons.
  // The checkbox container is separate from the pills so it persists across
  // target-cloud pill switches (the download button row is re-rendered on each
  // pill click, but the checkbox row is not).
  const archeraContainer = document.getElementById('federation-archera-options');
  if (archeraContainer) {
    clearContainer(archeraContainer);
    buildArcheraCheckbox(archeraContainer);
  }

  buildTargetCloudPills(pillContainer, target => {
    buildFederationDownloads(target, source, 'federation-setup-panel');
  });

  // Initial render with the default target (AWS).
  buildFederationDownloads(DEFAULT_TARGET_CLOUD, source, 'federation-setup-panel');
}
