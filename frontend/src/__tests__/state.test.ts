/**
 * State module tests
 */
import {
  state,
  getCurrentUser,
  setCurrentUser,
  getCurrentProvider,
  setCurrentProvider,
  getRecommendations,
  setRecommendations,
  getSelectedRecommendationIDs,
  clearSelectedRecommendations,
  addSelectedRecommendation,
  removeSelectedRecommendation,
  getSavingsChart,
  setSavingsChart
} from '../state';
import type { Recommendation } from '../api';

describe('State Module', () => {
  // Reset state before each test
  beforeEach(() => {
    state.currentUser = null;
    state.currentProvider = '';
    state.currentRecommendations = [];
    state.selectedRecommendations = new Set();
    state.savingsChart = null;
  });

  describe('User State', () => {
    test('getCurrentUser returns null initially', () => {
      expect(getCurrentUser()).toBeNull();
    });

    test('setCurrentUser and getCurrentUser work correctly', () => {
      const user = { id: '123', email: 'test@example.com', role: 'admin' };
      setCurrentUser(user);
      expect(getCurrentUser()).toEqual(user);
    });

    test('setCurrentUser with null clears user', () => {
      setCurrentUser({ id: '123', email: 'test@example.com', role: 'admin' });
      setCurrentUser(null);
      expect(getCurrentUser()).toBeNull();
    });
  });

  describe('Provider State', () => {
    test('getCurrentProvider returns "" initially (all providers)', () => {
      expect(getCurrentProvider()).toBe('');
    });

    test('setCurrentProvider and getCurrentProvider work correctly', () => {
      setCurrentProvider('aws');
      expect(getCurrentProvider()).toBe('aws');

      setCurrentProvider('azure');
      expect(getCurrentProvider()).toBe('azure');

      setCurrentProvider('gcp');
      expect(getCurrentProvider()).toBe('gcp');

      setCurrentProvider('');
      expect(getCurrentProvider()).toBe('');
    });
  });

  describe('Recommendations State', () => {
    const mockRecommendations: Recommendation[] = [
      {
        id: '1',
        provider: 'aws',
        service: 'ec2',
        region: 'us-east-1',
        resource_type: 'm5.large',
        count: 2,
        term: 1,
        payment: 'no-upfront',
        upfront_cost: 0,
        monthly_cost: 70,
        savings: 30,
        selected: true,
        purchased: false,
      },
      {
        id: '2',
        provider: 'azure',
        service: 'compute',
        region: 'eastus',
        resource_type: 'Standard_D2s_v3',
        count: 1,
        term: 3,
        payment: 'all-upfront',
        upfront_cost: 150,
        monthly_cost: 0,
        savings: 50,
        selected: false,
        purchased: false,
      }
    ];

    test('getRecommendations returns empty array initially', () => {
      expect(getRecommendations()).toEqual([]);
    });

    test('setRecommendations and getRecommendations work correctly', () => {
      setRecommendations(mockRecommendations);
      expect(getRecommendations()).toEqual(mockRecommendations);
      expect(getRecommendations().length).toBe(2);
    });

    test('setRecommendations with empty array clears recommendations', () => {
      setRecommendations(mockRecommendations);
      setRecommendations([]);
      expect(getRecommendations()).toEqual([]);
    });
  });

  describe('Selected Recommendations State', () => {
    test('getSelectedRecommendationIDs returns empty Set initially', () => {
      const selected = getSelectedRecommendationIDs();
      expect(selected.size).toBe(0);
    });

    test('addSelectedRecommendation adds id to set', () => {
      addSelectedRecommendation('rec-0');
      addSelectedRecommendation('rec-2');
      addSelectedRecommendation('rec-5');

      const selected = getSelectedRecommendationIDs();
      expect(selected.size).toBe(3);
      expect(selected.has('rec-0')).toBe(true);
      expect(selected.has('rec-2')).toBe(true);
      expect(selected.has('rec-5')).toBe(true);
      expect(selected.has('rec-1')).toBe(false);
    });

    test('removeSelectedRecommendation removes id from set', () => {
      addSelectedRecommendation('rec-0');
      addSelectedRecommendation('rec-1');
      addSelectedRecommendation('rec-2');

      removeSelectedRecommendation('rec-1');

      const selected = getSelectedRecommendationIDs();
      expect(selected.size).toBe(2);
      expect(selected.has('rec-0')).toBe(true);
      expect(selected.has('rec-1')).toBe(false);
      expect(selected.has('rec-2')).toBe(true);
    });

    test('clearSelectedRecommendations clears all selections', () => {
      addSelectedRecommendation('rec-0');
      addSelectedRecommendation('rec-1');
      addSelectedRecommendation('rec-2');

      clearSelectedRecommendations();

      expect(getSelectedRecommendationIDs().size).toBe(0);
    });

    test('adding same id twice does not duplicate', () => {
      addSelectedRecommendation('rec-0');
      addSelectedRecommendation('rec-0');

      expect(getSelectedRecommendationIDs().size).toBe(1);
    });

    test('removing non-existent id does not throw', () => {
      expect(() => removeSelectedRecommendation('rec-999')).not.toThrow();
    });
  });

  describe('Savings Chart State', () => {
    test('getSavingsChart returns null initially', () => {
      expect(getSavingsChart()).toBeNull();
    });

    test('setSavingsChart and getSavingsChart work correctly', () => {
      const mockChart = { destroy: jest.fn() } as unknown as import('chart.js').Chart;
      setSavingsChart(mockChart);
      expect(getSavingsChart()).toBe(mockChart);
    });

    test('setSavingsChart with null clears chart', () => {
      const mockChart = { destroy: jest.fn() } as unknown as import('chart.js').Chart;
      setSavingsChart(mockChart);
      setSavingsChart(null);
      expect(getSavingsChart()).toBeNull();
    });
  });

  describe('State Integration', () => {
    test('state changes are reflected across getters', () => {
      // Set various state
      setCurrentUser({ id: '1', email: 'a@b.com', role: 'user' });
      setCurrentProvider('aws');
      setRecommendations([{
        id: '1',
        provider: 'aws',
        service: 'ec2',
        region: 'us-east-1',
        resource_type: 'm5.large',
        count: 1,
        term: 1,
        payment: 'no-upfront',
        upfront_cost: 0,
        monthly_cost: 70,
        savings: 30,
        selected: true,
        purchased: false,
      }]);
      addSelectedRecommendation('rec-0');

      // Verify all state is correct
      expect(getCurrentUser()?.email).toBe('a@b.com');
      expect(getCurrentProvider()).toBe('aws');
      expect(getRecommendations().length).toBe(1);
      expect(getSelectedRecommendationIDs().has('rec-0')).toBe(true);
    });
  });
});
