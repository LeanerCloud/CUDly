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
  getSelectedRecommendations,
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
    state.currentProvider = 'all';
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
    test('getCurrentProvider returns "all" initially', () => {
      expect(getCurrentProvider()).toBe('all');
    });

    test('setCurrentProvider and getCurrentProvider work correctly', () => {
      setCurrentProvider('aws');
      expect(getCurrentProvider()).toBe('aws');

      setCurrentProvider('azure');
      expect(getCurrentProvider()).toBe('azure');

      setCurrentProvider('gcp');
      expect(getCurrentProvider()).toBe('gcp');

      setCurrentProvider('all');
      expect(getCurrentProvider()).toBe('all');
    });
  });

  describe('Recommendations State', () => {
    const mockRecommendations: Recommendation[] = [
      {
        id: '1',
        provider: 'aws',
        service: 'ec2',
        region: 'us-east-1',
        current_cost: 100,
        recommended_cost: 70,
        estimated_savings: 30,
        term_years: 1,
        payment_option: 'no-upfront',
        coverage: 80,
        description: 'Test recommendation 1'
      },
      {
        id: '2',
        provider: 'azure',
        service: 'compute',
        region: 'eastus',
        current_cost: 200,
        recommended_cost: 150,
        estimated_savings: 50,
        term_years: 3,
        payment_option: 'all-upfront',
        coverage: 90,
        description: 'Test recommendation 2'
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
    test('getSelectedRecommendations returns empty Set initially', () => {
      const selected = getSelectedRecommendations();
      expect(selected.size).toBe(0);
    });

    test('addSelectedRecommendation adds index to set', () => {
      addSelectedRecommendation(0);
      addSelectedRecommendation(2);
      addSelectedRecommendation(5);

      const selected = getSelectedRecommendations();
      expect(selected.size).toBe(3);
      expect(selected.has(0)).toBe(true);
      expect(selected.has(2)).toBe(true);
      expect(selected.has(5)).toBe(true);
      expect(selected.has(1)).toBe(false);
    });

    test('removeSelectedRecommendation removes index from set', () => {
      addSelectedRecommendation(0);
      addSelectedRecommendation(1);
      addSelectedRecommendation(2);

      removeSelectedRecommendation(1);

      const selected = getSelectedRecommendations();
      expect(selected.size).toBe(2);
      expect(selected.has(0)).toBe(true);
      expect(selected.has(1)).toBe(false);
      expect(selected.has(2)).toBe(true);
    });

    test('clearSelectedRecommendations clears all selections', () => {
      addSelectedRecommendation(0);
      addSelectedRecommendation(1);
      addSelectedRecommendation(2);

      clearSelectedRecommendations();

      expect(getSelectedRecommendations().size).toBe(0);
    });

    test('adding same index twice does not duplicate', () => {
      addSelectedRecommendation(0);
      addSelectedRecommendation(0);

      expect(getSelectedRecommendations().size).toBe(1);
    });

    test('removing non-existent index does not throw', () => {
      expect(() => removeSelectedRecommendation(999)).not.toThrow();
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
        current_cost: 100,
        recommended_cost: 70,
        estimated_savings: 30,
        term_years: 1,
        payment_option: 'no-upfront',
        coverage: 80,
        description: 'Test'
      }]);
      addSelectedRecommendation(0);

      // Verify all state is correct
      expect(getCurrentUser()?.email).toBe('a@b.com');
      expect(getCurrentProvider()).toBe('aws');
      expect(getRecommendations().length).toBe(1);
      expect(getSelectedRecommendations().has(0)).toBe(true);
    });
  });
});
