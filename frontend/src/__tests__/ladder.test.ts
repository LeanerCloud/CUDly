/**
 * Commitment Laddering settings module tests (issue #1333 phase 3).
 *
 * Covers the two security/correctness-sensitive surfaces:
 *  (a) renderConfigTable escapes attacker-controlled cloud_account_id/provider
 *      so an injected tag never lands raw in innerHTML (XSS).
 *  (b) saveLadderConfig's max_hourly_commit_per_run guard: blank -> null,
 *      positive -> passthrough, non-positive/invalid -> rejected (no store call).
 */
import { initLadderingSettings, renderConfigTable, saveLadderConfig } from '../ladder';
import type { LadderConfig } from '../api';

jest.mock('../api', () => ({
  getLadderConfigs: jest.fn(),
  upsertLadderConfig: jest.fn(),
  getConfig: jest.fn(),
  updateConfig: jest.fn(),
}));

jest.mock('../permissions', () => ({
  canAccess: jest.fn(() => true),
}));

const mockShowToast = jest.fn();
jest.mock('../toast', () => ({
  showToast: (opts: unknown) => mockShowToast(opts),
}));

import * as api from '../api';

function baseConfig(overrides: Partial<LadderConfig> = {}): LadderConfig {
  return {
    cloud_account_id: '11111111-1111-1111-1111-111111111111',
    provider: 'aws',
    enabled: true,
    mode: 'email_approval',
    cadence: 'daily',
    target_coverage: 100,
    buffer_fraction: 0.1,
    baseline_percentile: 5,
    lookback_days: 30,
    buffer_utilization_threshold: 90,
    max_hourly_commit_per_run: null,
    max_actions_per_run: 10,
    ramp_schedule: { steps: [{ after_days: 0, fraction: 1.0 }] },
    updated_at: '2026-01-15T10:00:00Z',
    ...overrides,
  } as LadderConfig;
}

// Render the full section (incl. the modal form) into the DOM so the exported
// helpers have the elements they read.
async function renderSection(): Promise<void> {
  document.body.innerHTML = '<div id="commitment-laddering-settings"></div>';
  (api.getLadderConfigs as jest.Mock).mockResolvedValue([]);
  await initLadderingSettings(false);
}

describe('ladder.ts', () => {
  beforeEach(() => {
    jest.clearAllMocks();
    mockShowToast.mockReset();
  });

  describe('renderLadderingSection: toggle label accessibility (issue #1412 row 3.1)', () => {
    // Regression guard: the Enable Commitment Laddering label text must NOT be
    // a <label for="setting-laddering-enabled"> because that makes the entire
    // text string clickable (toggling the checkbox on click). Pattern matches
    // the Auto-collect row fix from issue #464: use a <span> + aria-labelledby
    // instead.
    test('enable-laddering label is a span, not a <label for> element', async () => {
      await renderSection();
      // A <label for="setting-laddering-enabled"> would make the text clickable.
      const badLabel = document.querySelector('label[for="setting-laddering-enabled"]');
      expect(badLabel).toBeNull();
    });

    test('enable-laddering input carries aria-labelledby referencing the span', async () => {
      await renderSection();
      const input = document.getElementById('setting-laddering-enabled') as HTMLInputElement | null;
      expect(input).not.toBeNull();
      const labelledById = input!.getAttribute('aria-labelledby');
      expect(labelledById).toBe('setting-laddering-enabled-label');
      // The referenced element must exist and contain the label text.
      const labelSpan = document.getElementById('setting-laddering-enabled-label');
      expect(labelSpan).not.toBeNull();
      expect(labelSpan!.textContent).toMatch(/Enable Commitment Laddering/);
    });

    test('enable-laddering checkbox is wrapped in a .toggle-label element', async () => {
      await renderSection();
      const input = document.getElementById('setting-laddering-enabled') as HTMLInputElement | null;
      expect(input).not.toBeNull();
      // The input must be inside a .toggle-label so the toggle switch is the
      // only click target (not the surrounding descriptive text).
      expect(input!.closest('.toggle-label')).not.toBeNull();
    });
  });

  describe('renderConfigTable XSS escaping', () => {
    test('renders a malicious cloud_account_id/provider as inert text, not markup', async () => {
      await renderSection();
      const evil = '<img src=x onerror=alert(1)>';
      renderConfigTable([baseConfig({ cloud_account_id: evil, provider: evil })]);

      const container = document.getElementById('ladder-configs-table-container')!;

      // The injected payload must not create a live element anywhere in the
      // table (DOM-level assertion; reading serialized innerHTML is unreliable
      // because jsdom re-serializes attribute values with raw </> which are
      // inert inside a quoted attribute).
      expect(container.querySelector('img')).toBeNull();

      // The account-id cell holds the payload as TEXT (no child elements =>
      // it was escaped, not parsed as HTML).
      const firstCell = container.querySelector('tbody tr td');
      expect(firstCell?.textContent).toBe(evil);
      expect(firstCell?.children.length).toBe(0);

      // Exactly one edit button exists (no extra nodes injected) and it carries
      // the value as an inert data-* attribute rather than as markup.
      const btns = container.querySelectorAll<HTMLButtonElement>('button.ladder-edit-btn');
      expect(btns.length).toBe(1);
      expect(btns[0]!.dataset['provider']).toBe(evil);
    });
  });

  describe('saveLadderConfig max_hourly guard', () => {
    // Fill the modal form with a valid baseline so save reaches the store,
    // then override max-hourly per case.
    async function primeValidForm(maxHourly: string): Promise<void> {
      await renderSection();
      (document.getElementById('ladder-cfg-account') as HTMLInputElement).value = 'acct-1';
      (document.getElementById('ladder-cfg-ramp-schedule') as HTMLTextAreaElement).value =
        '{"steps":[{"after_days":0,"fraction":1.0}]}';
      (document.getElementById('ladder-cfg-max-hourly') as HTMLInputElement).value = maxHourly;
    }

    test('blank max-hourly serializes to null in the payload', async () => {
      await primeValidForm('');
      (api.upsertLadderConfig as jest.Mock).mockResolvedValue(baseConfig());

      await saveLadderConfig();

      expect(api.upsertLadderConfig).toHaveBeenCalledTimes(1);
      const sent = (api.upsertLadderConfig as jest.Mock).mock.calls[0][0] as LadderConfig;
      expect(sent.max_hourly_commit_per_run).toBeNull();
    });

    test('positive max-hourly passes through unchanged', async () => {
      await primeValidForm('5');
      (api.upsertLadderConfig as jest.Mock).mockResolvedValue(baseConfig());

      await saveLadderConfig();

      expect(api.upsertLadderConfig).toHaveBeenCalledTimes(1);
      const sent = (api.upsertLadderConfig as jest.Mock).mock.calls[0][0] as LadderConfig;
      expect(sent.max_hourly_commit_per_run).toBe(5);
    });

    test('non-positive max-hourly is rejected (guarded, no store call)', async () => {
      await primeValidForm('-1');
      await saveLadderConfig();

      expect(api.upsertLadderConfig).not.toHaveBeenCalled();
      expect(mockShowToast).toHaveBeenCalledWith(
        expect.objectContaining({ kind: 'error' }),
      );
    });
  });
});
