/**
 * Issue #463: the Settings → General → Enabled Providers toggles must
 * filter the Opportunities list. The backend returns all collected
 * recs irrespective of `enabled_providers`, so `loadRecommendations`
 * applies a strict client-side filter against the GlobalConfig.
 *
 * Permissive default: empty/undefined `enabled_providers` falls
 * through with no filter (matches the Settings load path).
 */

import { loadRecommendations } from '../recommendations';
import type { LocalRecommendation } from '../types';

jest.mock('../api', () => ({
  getRecommendations: jest.fn(),
  refreshRecommendations: jest.fn(),
  listAccounts: jest.fn().mockResolvedValue([]),
  getConfig: jest.fn(),
  listAccountServiceOverrides: jest.fn().mockResolvedValue([]),
}));

jest.mock('../api/recommendations', () => ({
  getRecommendationDetail: jest.fn().mockResolvedValue({
    id: 'rec-default',
    usage_history: [],
    confidence_bucket: 'low',
    provenance_note: '',
  }),
  getRecommendationsFreshness: jest.fn().mockResolvedValue({
    last_collected_at: new Date(Date.now() - 60 * 60 * 1000).toISOString(),
    last_collection_error: null,
  }),
  refreshRecommendations: jest.fn().mockResolvedValue({}),
}));

const mockShowToast = jest.fn<{ dismiss: () => void }, [unknown]>(() => ({ dismiss: jest.fn() }));
jest.mock('../toast', () => ({
  showToast: (opts: unknown) => mockShowToast(opts),
}));

const setRecommendationsMock = jest.fn();
jest.mock('../state', () => ({
  getCurrentProvider: jest.fn().mockReturnValue('all'),
  setCurrentProvider: jest.fn(),
  getCurrentAccountIDs: jest.fn().mockReturnValue([]),
  setCurrentAccountIDs: jest.fn(),
  getRecommendations: jest.fn().mockReturnValue([]),
  getRecommendationByID: jest.fn().mockReturnValue(undefined),
  setRecommendations: (recs: unknown) => setRecommendationsMock(recs),
  getSelectedRecommendationIDs: jest.fn().mockReturnValue(new Set()),
  clearSelectedRecommendations: jest.fn(),
  addSelectedRecommendation: jest.fn(),
  removeSelectedRecommendation: jest.fn(),
  getRecommendationsSort: jest.fn().mockReturnValue({ column: 'savings', direction: 'desc' }),
  setRecommendationsSort: jest.fn(),
  getRecommendationsColumnFilters: jest.fn().mockReturnValue({}),
  setRecommendationsColumnFilter: jest.fn(),
  clearAllRecommendationsColumnFilters: jest.fn(),
  getVisibleRecommendations: jest.fn().mockReturnValue([]),
  setVisibleRecommendations: jest.fn(),
  getCostPeriod: jest.fn().mockReturnValue('monthly'),
  setCostPeriod: jest.fn(),
  getHiddenColumns: jest.fn().mockReturnValue(new Set()),
  setHiddenColumns: jest.fn(),
  getCurrentUser: jest.fn().mockReturnValue({ id: 'u-admin', email: 'admin@example.com', role: 'admin' }),
}));

jest.mock('../utils', () => ({
  formatCurrency: jest.fn((val) => `$${val || 0}`),
  formatTerm: jest.fn((years) => years == null ? '' : `${years} Year${years === 1 ? '' : 's'}`),
  escapeHtml: jest.fn((str) => str || ''),
  populateAccountFilter: jest.fn(() => Promise.resolve()),
}));

import * as api from '../api';

function makeRec(provider: 'aws' | 'azure' | 'gcp', id: string): LocalRecommendation {
  return {
    id,
    provider,
    service: 'ec2',
    resource_type: 'm5.large',
    region: 'us-east-1',
    count: 1,
    term: 3,
    savings: 100,
    upfront_cost: 1000,
  };
}

function seedDOM(): void {
  while (document.body.firstChild) document.body.removeChild(document.body.firstChild);
  const tab = document.createElement('div');
  tab.id = 'opportunities-tab';
  tab.className = 'tab-content active';
  const summary = document.createElement('div');
  summary.id = 'recommendations-summary';
  const list = document.createElement('div');
  list.id = 'recommendations-list';
  tab.appendChild(summary);
  tab.appendChild(list);
  document.body.appendChild(tab);
}

describe('loadRecommendations — Enabled Providers filter (#463)', () => {
  beforeEach(() => {
    seedDOM();
    jest.clearAllMocks();
    setRecommendationsMock.mockClear();
    mockShowToast.mockReturnValue({ dismiss: jest.fn() });
  });

  it('filters out providers absent from enabled_providers (aws-only)', async () => {
    (api.getRecommendations as jest.Mock).mockResolvedValue({
      summary: {},
      recommendations: [
        makeRec('aws', 'r1'),
        makeRec('azure', 'r2'),
        makeRec('gcp', 'r3'),
      ],
      regions: [],
    });
    (api.getConfig as jest.Mock).mockResolvedValue({
      global: { enabled_providers: ['aws'] },
    });

    await loadRecommendations();

    expect(setRecommendationsMock).toHaveBeenCalledTimes(1);
    const passed = setRecommendationsMock.mock.calls[0]![0] as LocalRecommendation[];
    expect(passed.map(r => r.id)).toEqual(['r1']);
  });

  it('keeps multiple enabled providers (aws + azure, drop gcp)', async () => {
    (api.getRecommendations as jest.Mock).mockResolvedValue({
      summary: {},
      recommendations: [
        makeRec('aws', 'r1'),
        makeRec('azure', 'r2'),
        makeRec('gcp', 'r3'),
      ],
      regions: [],
    });
    (api.getConfig as jest.Mock).mockResolvedValue({
      global: { enabled_providers: ['aws', 'azure'] },
    });

    await loadRecommendations();

    const passed = setRecommendationsMock.mock.calls[0]![0] as LocalRecommendation[];
    expect(passed.map(r => r.id).sort()).toEqual(['r1', 'r2']);
  });

  it('empty enabled_providers falls back to no filter (permissive default)', async () => {
    (api.getRecommendations as jest.Mock).mockResolvedValue({
      summary: {},
      recommendations: [
        makeRec('aws', 'r1'),
        makeRec('azure', 'r2'),
        makeRec('gcp', 'r3'),
      ],
      regions: [],
    });
    (api.getConfig as jest.Mock).mockResolvedValue({
      global: { enabled_providers: [] },
    });

    await loadRecommendations();

    const passed = setRecommendationsMock.mock.calls[0]![0] as LocalRecommendation[];
    expect(passed.map(r => r.id)).toEqual(['r1', 'r2', 'r3']);
  });

  it('undefined global config falls back to no filter', async () => {
    (api.getRecommendations as jest.Mock).mockResolvedValue({
      summary: {},
      recommendations: [
        makeRec('aws', 'r1'),
        makeRec('azure', 'r2'),
      ],
      regions: [],
    });
    (api.getConfig as jest.Mock).mockResolvedValue(null);

    await loadRecommendations();

    const passed = setRecommendationsMock.mock.calls[0]![0] as LocalRecommendation[];
    expect(passed.map(r => r.id)).toEqual(['r1', 'r2']);
  });

  it('renders only the filtered set in the Opportunities list DOM', async () => {
    (api.getRecommendations as jest.Mock).mockResolvedValue({
      summary: { total_count: 3 },
      recommendations: [
        makeRec('aws', 'r1'),
        makeRec('azure', 'r2'),
        makeRec('gcp', 'r3'),
      ],
      regions: [],
    });
    (api.getConfig as jest.Mock).mockResolvedValue({
      global: { enabled_providers: ['gcp'] },
    });

    await loadRecommendations();

    // Only the gcp row's id should appear in the rendered list markup.
    // buildListMarkup emits per-row checkboxes with data-rec-id="<id>"
    // (see recommendations.ts ~L2142); assert against that attribute so
    // the test exercises the actual rendered DOM instead of relying on
    // a fallback.
    const list = document.getElementById('recommendations-list');
    expect(list).not.toBeNull();
    const checkboxes = list?.querySelectorAll('input[data-rec-id]') ?? [];
    const renderedIds = Array.from(checkboxes).map(el => el.getAttribute('data-rec-id'));
    expect(renderedIds).toContain('r3');
    expect(renderedIds).not.toContain('r1');
    expect(renderedIds).not.toContain('r2');
  });
});
