/**
 * Tests for the inventory API module (issue #340 deferred sub-task).
 *
 * Verifies the wire format / envelope handling for
 * GET /api/inventory/commitments. The backend handler logic is covered
 * in handler_inventory_test.go; here we lock down the client adapter.
 */

import { apiRequest } from '../api/client';
import { listActiveCommitments } from '../api/inventory';

jest.mock('../api/client', () => ({
  apiRequest: jest.fn(),
}));

describe('listActiveCommitments', () => {
  beforeEach(() => {
    (apiRequest as jest.Mock).mockReset();
  });

  test('calls /inventory/commitments without query string by default', async () => {
    (apiRequest as jest.Mock).mockResolvedValue({ commitments: [] });

    const result = await listActiveCommitments();

    expect(apiRequest).toHaveBeenCalledWith('/inventory/commitments');
    expect(result).toEqual([]);
  });

  test('appends URL-encoded account_id when scoped to one account', async () => {
    (apiRequest as jest.Mock).mockResolvedValue({ commitments: [] });

    await listActiveCommitments('acc/with special');

    expect(apiRequest).toHaveBeenCalledWith('/inventory/commitments?account_id=acc%2Fwith%20special');
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
