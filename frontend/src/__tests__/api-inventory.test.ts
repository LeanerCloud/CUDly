/**
 * Tests for the inventory API module (issue #340 deferred sub-task, #866).
 *
 * Verifies the wire format / envelope handling for
 * GET /api/inventory/commitments and GET /api/inventory/coverage,
 * including the provider + account_id query params added by issue #866.
 * Backend handler logic is covered in handler_inventory_test.go.
 */

import { apiRequest } from '../api/client';
import { listActiveCommitments, getCoverageBreakdown } from '../api/inventory';

jest.mock('../api/client', () => ({
  apiRequest: jest.fn(),
}));

describe('listActiveCommitments', () => {
  beforeEach(() => {
    (apiRequest as jest.Mock).mockReset();
  });

  test('calls /inventory/commitments without query string when no filter', async () => {
    (apiRequest as jest.Mock).mockResolvedValue({ commitments: [] });

    const result = await listActiveCommitments();

    expect(apiRequest).toHaveBeenCalledWith('/inventory/commitments');
    expect(result).toEqual([]);
  });

  test('appends URL-encoded account_id when scoped to one account', async () => {
    (apiRequest as jest.Mock).mockResolvedValue({ commitments: [] });

    await listActiveCommitments({ accountID: 'acc/with special' });

    expect(apiRequest).toHaveBeenCalledWith('/inventory/commitments?account_id=acc%2Fwith%20special');
  });

  test('appends provider query param when provider filter is set', async () => {
    (apiRequest as jest.Mock).mockResolvedValue({ commitments: [] });

    await listActiveCommitments({ provider: 'aws' });

    expect(apiRequest).toHaveBeenCalledWith('/inventory/commitments?provider=aws');
  });

  test('appends both account_id and provider when both are set', async () => {
    (apiRequest as jest.Mock).mockResolvedValue({ commitments: [] });

    await listActiveCommitments({ accountID: 'acc-1', provider: 'azure' });

    const url = (apiRequest as jest.Mock).mock.calls[0][0] as string;
    expect(url).toContain('account_id=acc-1');
    expect(url).toContain('provider=azure');
  });

  test('returns the commitments array unwrapped from the envelope', async () => {
    const commitments = [
      {
        id: 'a:1',
        provider: 'aws',
        account_id: 'a',
        service: 'ec2',
        region: 'us-east-1',
        count: 1,
        term_years: 1,
        start_date: '2025-01-01T00:00:00Z',
        end_date: '2026-01-01T00:00:00Z',
        upfront_cost: 0,
        monthly_cost: 10,
        estimated_savings: 2,
        status: 'active',
      },
    ];
    (apiRequest as jest.Mock).mockResolvedValue({ commitments });

    const result = await listActiveCommitments();
    expect(result).toEqual(commitments);
  });

  test('returns an empty array when the envelope is missing the commitments field', async () => {
    // Defensive: a buggy/old backend could omit the field. The client
    // adapter must default to [] so callers can safely call .length.
    (apiRequest as jest.Mock).mockResolvedValue({});

    const result = await listActiveCommitments();
    expect(result).toEqual([]);
  });
});

describe('getCoverageBreakdown', () => {
  beforeEach(() => {
    (apiRequest as jest.Mock).mockReset();
  });

  test('calls /inventory/coverage without query string when no filter', async () => {
    (apiRequest as jest.Mock).mockResolvedValue({ providers: [] });

    await getCoverageBreakdown();

    expect(apiRequest).toHaveBeenCalledWith('/inventory/coverage');
  });

  test('appends provider query param when provider filter is set', async () => {
    (apiRequest as jest.Mock).mockResolvedValue({ providers: [] });

    await getCoverageBreakdown({ provider: 'gcp' });

    expect(apiRequest).toHaveBeenCalledWith('/inventory/coverage?provider=gcp');
  });

  test('appends account_id query param when accountID filter is set', async () => {
    (apiRequest as jest.Mock).mockResolvedValue({ providers: [] });

    await getCoverageBreakdown({ accountID: 'acc-42' });

    expect(apiRequest).toHaveBeenCalledWith('/inventory/coverage?account_id=acc-42');
  });

  test('appends both params when both are set', async () => {
    (apiRequest as jest.Mock).mockResolvedValue({ providers: [] });

    await getCoverageBreakdown({ accountID: 'acc-1', provider: 'aws' });

    const url = (apiRequest as jest.Mock).mock.calls[0][0] as string;
    expect(url).toContain('account_id=acc-1');
    expect(url).toContain('provider=aws');
  });

  test('returns the full response envelope including providers array', async () => {
    const payload = {
      providers: [
        { provider: 'aws', services: null, overall_coverage_pct: null },
        { provider: 'azure', services: null, overall_coverage_pct: null },
        { provider: 'gcp', services: null, overall_coverage_pct: null },
      ],
    };
    (apiRequest as jest.Mock).mockResolvedValue(payload);

    const result = await getCoverageBreakdown();

    expect(result).toEqual(payload);
    expect(result.providers).toHaveLength(3);
  });
});
