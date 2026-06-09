/**
 * API History module tests
 */
import { getHistory, getSavingsAnalytics, getSavingsBreakdown } from '../api/history';
import { apiRequest } from '../api/client';

// Mock the client module
jest.mock('../api/client', () => ({
  apiRequest: jest.fn()
}));

describe('API History Module', () => {
  beforeEach(() => {
    jest.clearAllMocks();
  });

  describe('getHistory', () => {
    test('calls apiRequest with correct endpoint and no filters', async () => {
      const mockData = [
        {
          id: 'hist-1',
          plan_id: 'plan-1',
          plan_name: 'Test Plan',
          executed_at: '2024-01-15',
          provider: 'aws',
          service: 'ec2',
          region: 'us-east-1',
          upfront_cost: 1000,
          estimated_savings: 200,
          status: 'completed'
        }
      ];
      (apiRequest as jest.Mock).mockResolvedValue(mockData);

      const result = await getHistory();

      expect(apiRequest).toHaveBeenCalledWith('/history');
      expect(result).toEqual(mockData);
    });

    test('includes start filter in query string', async () => {
      (apiRequest as jest.Mock).mockResolvedValue([]);

      await getHistory({ start: '2024-01-01' });

      expect(apiRequest).toHaveBeenCalledWith('/history?start=2024-01-01');
    });

    test('includes end filter in query string', async () => {
      (apiRequest as jest.Mock).mockResolvedValue([]);

      await getHistory({ end: '2024-03-31' });

      expect(apiRequest).toHaveBeenCalledWith('/history?end=2024-03-31');
    });

    test('includes provider filter in query string', async () => {
      (apiRequest as jest.Mock).mockResolvedValue([]);

      await getHistory({ provider: 'aws' });

      expect(apiRequest).toHaveBeenCalledWith('/history?provider=aws');
    });

    test('includes planId filter in query string', async () => {
      (apiRequest as jest.Mock).mockResolvedValue([]);

      await getHistory({ planId: 'plan-123' });

      expect(apiRequest).toHaveBeenCalledWith('/history?plan_id=plan-123');
    });

    test('includes multiple filters in query string', async () => {
      (apiRequest as jest.Mock).mockResolvedValue([]);

      await getHistory({
        start: '2024-01-01',
        end: '2024-03-31',
        provider: 'azure',
        planId: 'plan-456'
      });

      expect(apiRequest).toHaveBeenCalledWith(
        '/history?start=2024-01-01&end=2024-03-31&provider=azure&plan_id=plan-456'
      );
    });

    test('handles empty filters object', async () => {
      (apiRequest as jest.Mock).mockResolvedValue([]);

      await getHistory({});

      expect(apiRequest).toHaveBeenCalledWith('/history');
    });
  });

  describe('getSavingsAnalytics', () => {
    test('calls apiRequest with correct endpoint and no filters', async () => {
      const mockData = {
        start: '2024-01-01',
        end: '2024-03-31',
        interval: 'daily',
        summary: {
          total_period_savings: 5000,
          total_upfront_spent: 10000,
          purchase_count: 5,
          average_savings_per_period: 50,
          peak_savings: 200
        },
        data_points: []
      };
      (apiRequest as jest.Mock).mockResolvedValue(mockData);

      const result = await getSavingsAnalytics();

      expect(apiRequest).toHaveBeenCalledWith('/history/analytics');
      expect(result).toEqual(mockData);
    });

    test('includes start filter in query string', async () => {
      (apiRequest as jest.Mock).mockResolvedValue({ data_points: [] });

      await getSavingsAnalytics({ start: '2024-01-01' });

      expect(apiRequest).toHaveBeenCalledWith('/history/analytics?start=2024-01-01');
    });

    test('includes end filter in query string', async () => {
      (apiRequest as jest.Mock).mockResolvedValue({ data_points: [] });

      await getSavingsAnalytics({ end: '2024-03-31' });

      expect(apiRequest).toHaveBeenCalledWith('/history/analytics?end=2024-03-31');
    });

    test('includes interval filter in query string', async () => {
      (apiRequest as jest.Mock).mockResolvedValue({ data_points: [] });

      await getSavingsAnalytics({ interval: 'hourly' });

      expect(apiRequest).toHaveBeenCalledWith('/history/analytics?interval=hourly');
    });

    test('includes provider filter in query string', async () => {
      (apiRequest as jest.Mock).mockResolvedValue({ data_points: [] });

      await getSavingsAnalytics({ provider: 'gcp' });

      expect(apiRequest).toHaveBeenCalledWith('/history/analytics?provider=gcp');
    });

    test('includes service filter in query string', async () => {
      (apiRequest as jest.Mock).mockResolvedValue({ data_points: [] });

      await getSavingsAnalytics({ service: 'ec2' });

      expect(apiRequest).toHaveBeenCalledWith('/history/analytics?service=ec2');
    });

    test('includes multiple filters in query string', async () => {
      (apiRequest as jest.Mock).mockResolvedValue({ data_points: [] });

      await getSavingsAnalytics({
        start: '2024-01-01',
        end: '2024-03-31',
        interval: 'daily',
        provider: 'aws',
        service: 'rds'
      });

      expect(apiRequest).toHaveBeenCalledWith(
        '/history/analytics?start=2024-01-01&end=2024-03-31&interval=daily&provider=aws&service=rds'
      );
    });

    test('handles empty filters object', async () => {
      (apiRequest as jest.Mock).mockResolvedValue({ data_points: [] });

      await getSavingsAnalytics({});

      expect(apiRequest).toHaveBeenCalledWith('/history/analytics');
    });

    test('supports weekly interval', async () => {
      (apiRequest as jest.Mock).mockResolvedValue({ data_points: [] });

      await getSavingsAnalytics({ interval: 'weekly' });

      expect(apiRequest).toHaveBeenCalledWith('/history/analytics?interval=weekly');
    });

    test('supports monthly interval', async () => {
      (apiRequest as jest.Mock).mockResolvedValue({ data_points: [] });

      await getSavingsAnalytics({ interval: 'monthly' });

      expect(apiRequest).toHaveBeenCalledWith('/history/analytics?interval=monthly');
    });
  });

  describe('getSavingsBreakdown', () => {
    test('calls apiRequest with service dimension', async () => {
      const mockData = {
        dimension: 'service',
        start: '2024-01-01',
        end: '2024-03-31',
        data: {
          ec2: { total_savings: 1000, total_upfront: 2000, purchase_count: 3, percentage: 50 },
          rds: { total_savings: 1000, total_upfront: 2000, purchase_count: 2, percentage: 50 }
        }
      };
      (apiRequest as jest.Mock).mockResolvedValue(mockData);

      const result = await getSavingsBreakdown('service');

      expect(apiRequest).toHaveBeenCalledWith('/history/breakdown?dimension=service');
      expect(result).toEqual(mockData);
    });

    test('calls apiRequest with provider dimension', async () => {
      (apiRequest as jest.Mock).mockResolvedValue({ data: {} });

      await getSavingsBreakdown('provider');

      expect(apiRequest).toHaveBeenCalledWith('/history/breakdown?dimension=provider');
    });

    test('calls apiRequest with region dimension', async () => {
      (apiRequest as jest.Mock).mockResolvedValue({ data: {} });

      await getSavingsBreakdown('region');

      expect(apiRequest).toHaveBeenCalledWith('/history/breakdown?dimension=region');
    });

    test('includes start date filter', async () => {
      (apiRequest as jest.Mock).mockResolvedValue({ data: {} });

      await getSavingsBreakdown('service', { start: '2024-01-01' });

      expect(apiRequest).toHaveBeenCalledWith('/history/breakdown?dimension=service&start=2024-01-01');
    });

    test('includes end date filter', async () => {
      (apiRequest as jest.Mock).mockResolvedValue({ data: {} });

      await getSavingsBreakdown('service', { end: '2024-03-31' });

      expect(apiRequest).toHaveBeenCalledWith('/history/breakdown?dimension=service&end=2024-03-31');
    });

    test('includes both start and end date filters', async () => {
      (apiRequest as jest.Mock).mockResolvedValue({ data: {} });

      await getSavingsBreakdown('provider', { start: '2024-01-01', end: '2024-03-31' });

      expect(apiRequest).toHaveBeenCalledWith(
        '/history/breakdown?dimension=provider&start=2024-01-01&end=2024-03-31'
      );
    });

    test('handles empty filters object', async () => {
      (apiRequest as jest.Mock).mockResolvedValue({ data: {} });

      await getSavingsBreakdown('region', {});

      expect(apiRequest).toHaveBeenCalledWith('/history/breakdown?dimension=region');
    });
  });
});
