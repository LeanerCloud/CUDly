/**
 * Federation module tests — Archera opt-in checkbox (#314).
 *
 * DOM is built with createElement / replaceChildren to avoid innerHTML.
 * We mock the api module so no network calls are made.
 */
import { initFederationPanel } from '../federation';

// ---------------------------------------------------------------------------
// Mocks
// ---------------------------------------------------------------------------

jest.mock('../api', () => ({
  getFederationIaC: jest.fn(),
}));

import * as api from '../api';

const mockGetFederationIaC = api.getFederationIaC as jest.MockedFunction<
  typeof api.getFederationIaC
>;

// ---------------------------------------------------------------------------
// DOM helpers
// ---------------------------------------------------------------------------

/** Build the minimal DOM the federation panel needs. */
function buildFederationDOM(): void {
  document.body.replaceChildren();

  const pillContainer = document.createElement('div');
  pillContainer.id = 'federation-target-cloud-pills';
  document.body.appendChild(pillContainer);

  const archeraContainer = document.createElement('div');
  archeraContainer.id = 'federation-archera-options';
  document.body.appendChild(archeraContainer);

  const panelContainer = document.createElement('div');
  panelContainer.id = 'federation-setup-panel';
  document.body.appendChild(panelContainer);
}

/** Return the Archera checkbox element, throwing if absent. */
function archeraCheckbox(): HTMLInputElement {
  const cb = document.getElementById(
    'federation-include-archera',
  ) as HTMLInputElement | null;
  if (!cb) throw new Error('federation-include-archera checkbox not found');
  return cb;
}

// ---------------------------------------------------------------------------
// Test suites
// ---------------------------------------------------------------------------

describe('initFederationPanel — Archera checkbox', () => {
  beforeEach(() => {
    buildFederationDOM();
    jest.clearAllMocks();
  });

  afterEach(() => {
    document.body.replaceChildren();
  });

  test('checkbox is rendered and unchecked by default', async () => {
    await initFederationPanel('aws');

    const cb = archeraCheckbox();
    expect(cb).toBeTruthy();
    expect(cb.checked).toBe(false);
  });

  test('checkbox label text is "Provision Archera Insurance permissions?"', async () => {
    await initFederationPanel('aws');

    // The label wraps the checkbox; we look for the span with the text.
    const label = document.querySelector('.federation-archera-label');
    expect(label).toBeTruthy();
    expect(label!.textContent).toContain('Provision Archera Insurance permissions?');
  });

  test('tooltip sr-only span is present and contains tooltip text', async () => {
    await initFederationPanel('aws');

    const tooltip = document.getElementById('federation-archera-tooltip');
    expect(tooltip).toBeTruthy();
    expect(tooltip!.classList.contains('sr-only')).toBe(true);
    // Tooltip explains what Archera Insurance permissions do.
    expect(tooltip!.textContent).toContain('Archera');
  });
});

describe('initFederationPanel — download without Archera (unchecked)', () => {
  const mockResponse = {
    filename: 'cudly-federation-aws.zip',
    content: 'base64data',
    content_type: 'application/zip',
    content_encoding: 'base64' as const,
  };

  beforeEach(() => {
    buildFederationDOM();
    jest.clearAllMocks();
    mockGetFederationIaC.mockResolvedValue(mockResponse);

    // Stub URL.createObjectURL / URL.revokeObjectURL used by downloadFile.
    Object.defineProperty(URL, 'createObjectURL', {
      writable: true,
      value: jest.fn(() => 'blob:mock-url'),
    });
    Object.defineProperty(URL, 'revokeObjectURL', {
      writable: true,
      value: jest.fn(),
    });
  });

  afterEach(() => {
    document.body.replaceChildren();
  });

  test('does not pass include_archera when checkbox is unchecked', async () => {
    await initFederationPanel('aws');

    // The default target-cloud pill is AWS; click the first "bundle" download button.
    const downloadBtn = document.querySelector<HTMLButtonElement>('.btn-download');
    expect(downloadBtn).toBeTruthy();
    downloadBtn!.click();

    // Wait for the async handler to fire.
    await Promise.resolve();

    expect(mockGetFederationIaC).toHaveBeenCalledTimes(1);
    // includeArchera should be false (default, unchecked).
    // eslint-disable-next-line @typescript-eslint/no-non-null-assertion
    const [, , , includeArchera] = mockGetFederationIaC.mock.calls[0]!;
    expect(includeArchera).toBe(false);
  });
});

describe('initFederationPanel — download with Archera (checked)', () => {
  const mockResponse = {
    filename: 'cudly-federation-aws-archera.zip',
    content: 'base64data',
    content_type: 'application/zip',
    content_encoding: 'base64' as const,
  };

  beforeEach(() => {
    buildFederationDOM();
    jest.clearAllMocks();
    mockGetFederationIaC.mockResolvedValue(mockResponse);

    Object.defineProperty(URL, 'createObjectURL', {
      writable: true,
      value: jest.fn(() => 'blob:mock-url'),
    });
    Object.defineProperty(URL, 'revokeObjectURL', {
      writable: true,
      value: jest.fn(),
    });
  });

  afterEach(() => {
    document.body.replaceChildren();
  });

  test('passes includeArchera=true when checkbox is checked before download', async () => {
    await initFederationPanel('aws');

    // Check the Archera checkbox.
    const cb = archeraCheckbox();
    cb.checked = true;
    cb.dispatchEvent(new Event('change'));

    // Click the first download button.
    const downloadBtn = document.querySelector<HTMLButtonElement>('.btn-download');
    expect(downloadBtn).toBeTruthy();
    downloadBtn!.click();

    await Promise.resolve();

    expect(mockGetFederationIaC).toHaveBeenCalledTimes(1);
    // eslint-disable-next-line @typescript-eslint/no-non-null-assertion
    const [, , , includeArchera] = mockGetFederationIaC.mock.calls[0]!;
    expect(includeArchera).toBe(true);
  });

  test('checkbox state persists after switching target-cloud pill', async () => {
    await initFederationPanel('aws');

    // Check the Archera checkbox.
    const cb = archeraCheckbox();
    cb.checked = true;
    cb.dispatchEvent(new Event('change'));

    // Switch to GCP pill.
    const gcpPill = document.querySelector<HTMLButtonElement>(
      '[data-target="gcp"]',
    );
    expect(gcpPill).toBeTruthy();
    gcpPill!.click();

    // The archera container should still have the checkbox (it is not re-rendered).
    const cbAfter = document.getElementById(
      'federation-include-archera',
    ) as HTMLInputElement | null;
    expect(cbAfter).toBeTruthy();
    // The module-level _includeArchera is still true; clicking download should pass it.
    const downloadBtn = document.querySelector<HTMLButtonElement>('.btn-download');
    expect(downloadBtn).toBeTruthy();
    downloadBtn!.click();

    await Promise.resolve();

    expect(mockGetFederationIaC).toHaveBeenCalledTimes(1);
    // eslint-disable-next-line @typescript-eslint/no-non-null-assertion
    const [, , , includeArchera] = mockGetFederationIaC.mock.calls[0]!;
    expect(includeArchera).toBe(true);
  });
});
