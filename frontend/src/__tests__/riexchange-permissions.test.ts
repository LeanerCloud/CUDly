/**
 * RI Exchange tables permission-gating tests for issue #365.
 *
 * Both the convertible-RI table and the reshape-recommendations
 * table expose per-row "Exchange" buttons that hit admin-only
 * backend endpoints. Hide them for non-admin sessions.
 */

jest.mock('../api', () => ({
  listConvertibleRIs: jest.fn(),
  getRIUtilization: jest.fn().mockResolvedValue([]),
  getReshapeRecommendations: jest.fn(),
  getRIExchangeHistory: jest.fn().mockResolvedValue([]),
}));

jest.mock('../navigation', () => ({
  switchTab: jest.fn(),
  switchSettingsSubTab: jest.fn(),
}));

jest.mock('../state', () => ({
  getCurrentUser: jest.fn(),
  // loadRIExchange reads the provider/account chips to scope the request
  // (issue #871); default to the AWS, all-accounts path used by these tests.
  getCurrentProvider: jest.fn(() => 'aws'),
  getCurrentAccountIDs: jest.fn(() => []),
}));

import * as api from '../api';
import * as state from '../state';
import { loadReshapeRecommendations } from '../riexchange';

// The riexchange module keeps `currentRIs` in module-scoped state and
// the reshape rendering reads `currentRIs.length` to pick the empty-
// state copy. We load the convertible-RI table via the module's own
// import too so that state is non-empty during the reshape assertions.
import { loadRIExchange } from '../riexchange';

const sampleRI = {
  reserved_instance_id: 'ri-1',
  instance_type: 't3.medium',
  availability_zone: 'us-east-1a',
  instance_count: 5,
  offering_type: 'Convertible',
  end: '2027-01-01T00:00:00Z',
};

const sampleReshape = {
  source_ri_id: 'ri-1',
  source_instance_type: 't3.medium',
  source_count: 5,
  target_instance_type: 'm5.large',
  target_count: 5,
  utilization_percent: 45,
  normalized_used: 12,
  normalized_purchased: 24,
  reason: 'Underutilized convertible RI',
  alternative_targets: [],
};

const mockUser = (role: string | null) => {
  (state.getCurrentUser as jest.Mock).mockReturnValue(
    role === null ? null : { id: 'u', email: 'u@example.com', groups: role === 'admin' ? ['00000000-0000-5000-8000-000000000001'] : [] },
  );
};

const setupDom = () => {
  const ris = document.createElement('div');
  ris.id = 'ri-exchange-instances-list';
  const recs = document.createElement('div');
  recs.id = 'ri-exchange-recommendations-list';
  const hist = document.createElement('div');
  hist.id = 'ri-exchange-history-list';
  document.body.replaceChildren(ris, recs, hist);
};

describe('RI Exchange tables permission gating (issue #365)', () => {
  beforeEach(() => {
    jest.clearAllMocks();
    setupDom();
    (api.listConvertibleRIs as jest.Mock).mockResolvedValue([sampleRI]);
    (api.getReshapeRecommendations as jest.Mock).mockResolvedValue({ recommendations: [sampleReshape], recs_staleness: '', recs_collected_at: null });
  });

  describe('admin role', () => {
    beforeEach(() => mockUser('admin'));

    test('convertible-RI table renders an Actions column with Exchange button', async () => {
      await loadRIExchange();
      const html = document.getElementById('ri-exchange-instances-list')!.innerHTML;
      expect(html).toContain('data-action="quote-ri"');
      expect(html).toContain('<th>Actions</th>');
    });

    test('reshape-recommendations table renders an Actions column with Exchange button', async () => {
      await loadRIExchange();
      await loadReshapeRecommendations();
      const html = document.getElementById('ri-exchange-recommendations-list')!.innerHTML;
      expect(html).toContain('data-action="fill-quote"');
      expect(html).toContain('<th>Actions</th>');
    });
  });

  describe('user role', () => {
    beforeEach(() => mockUser('user'));

    test('convertible-RI table renders without an Actions column or Exchange button', async () => {
      await loadRIExchange();
      const html = document.getElementById('ri-exchange-instances-list')!.innerHTML;
      expect(html).not.toContain('data-action="quote-ri"');
      expect(html).not.toContain('<th>Actions</th>');
    });

    test('reshape-recommendations table renders without an Actions column or Exchange button', async () => {
      await loadRIExchange();
      await loadReshapeRecommendations();
      const html = document.getElementById('ri-exchange-recommendations-list')!.innerHTML;
      expect(html).not.toContain('data-action="fill-quote"');
      expect(html).not.toContain('<th>Actions</th>');
    });
  });

  describe('readonly role', () => {
    beforeEach(() => mockUser('readonly'));

    test('convertible-RI table hides the Exchange affordance entirely', async () => {
      await loadRIExchange();
      const html = document.getElementById('ri-exchange-instances-list')!.innerHTML;
      expect(html).not.toContain('quote-ri');
    });

    test('reshape-recommendations table hides the Exchange affordance entirely', async () => {
      await loadRIExchange();
      await loadReshapeRecommendations();
      const html = document.getElementById('ri-exchange-recommendations-list')!.innerHTML;
      expect(html).not.toContain('fill-quote');
    });
  });

  describe('null user', () => {
    beforeEach(() => mockUser(null));

    test('convertible-RI and reshape tables both hide the Exchange button', async () => {
      await loadRIExchange();
      await loadReshapeRecommendations();
      const ris = document.getElementById('ri-exchange-instances-list')!.innerHTML;
      const recs = document.getElementById('ri-exchange-recommendations-list')!.innerHTML;
      expect(ris).not.toContain('quote-ri');
      expect(recs).not.toContain('fill-quote');
    });
  });
});
