/**
 * Unit tests for API module
 */
import { localStorageMock, sessionStorageMock, fetchMock } from './setup';
import {
  initAuth,
  setAuthToken,
  setApiKey,
  isAuthenticated,
  clearAuth,
  getAuthHeaders,
  apiRequest,
  login,
  logout,
  getCurrentUser,
  requestPasswordReset,
  checkAdminExists,
  setupAdmin,
  getDashboardSummary,
  getUpcomingPurchases,
  getRecommendations,
  refreshRecommendations,
  getPlans,
  getPlan,
  createPlan,
  updatePlan,
  patchPlan,
  deletePlan,
  getHistory,
  getConfig,
  updateConfig,
  executePurchase,
  getPurchaseDetails,
  cancelPurchase,
  getPublicInfo
} from '../api';
import type { CreatePlanRequest, Config, Recommendation } from '../api';

describe('Authentication', () => {
  beforeEach(() => {
    localStorageMock.getItem.mockReturnValue(null);
    sessionStorageMock.getItem.mockReturnValue(null);
    clearAuth();
  });

  describe('initAuth', () => {
    test('loads auth token from sessionStorage', () => {
      sessionStorageMock.getItem.mockImplementation((key: string) => {
        if (key === 'authToken') return 'test-token';
        return null;
      });
      initAuth();
      expect(isAuthenticated()).toBe(true);
    });

    test('loads api key from sessionStorage', () => {
      sessionStorageMock.getItem.mockImplementation((key: string) => {
        if (key === 'apiKey') return 'test-key';
        return null;
      });
      initAuth();
      expect(isAuthenticated()).toBe(true);
    });

    test('migrates token from localStorage to sessionStorage', () => {
      localStorageMock.getItem.mockImplementation((key: string) => {
        if (key === 'authToken') return 'legacy-token';
        return null;
      });
      initAuth();
      expect(sessionStorageMock.setItem).toHaveBeenCalledWith('authToken', 'legacy-token');
      expect(localStorageMock.removeItem).toHaveBeenCalledWith('authToken');
    });
  });

  describe('setAuthToken', () => {
    test('sets token and stores in sessionStorage', () => {
      setAuthToken('new-token');
      expect(sessionStorageMock.setItem).toHaveBeenCalledWith('authToken', 'new-token');
      expect(isAuthenticated()).toBe(true);
    });

    test('clears token when empty', () => {
      setAuthToken('');
      expect(sessionStorageMock.removeItem).toHaveBeenCalledWith('authToken');
    });
  });

  describe('setApiKey', () => {
    test('sets key and stores in sessionStorage', () => {
      setApiKey('new-key');
      expect(sessionStorageMock.setItem).toHaveBeenCalledWith('apiKey', 'new-key');
      expect(isAuthenticated()).toBe(true);
    });

    test('clears key when empty', () => {
      setApiKey('');
      expect(sessionStorageMock.removeItem).toHaveBeenCalledWith('apiKey');
    });
  });

  describe('isAuthenticated', () => {
    test('returns false when no credentials', () => {
      expect(isAuthenticated()).toBe(false);
    });

    test('returns true with auth token', () => {
      setAuthToken('token');
      expect(isAuthenticated()).toBe(true);
    });

    test('returns true with api key', () => {
      setApiKey('key');
      expect(isAuthenticated()).toBe(true);
    });
  });

  describe('clearAuth', () => {
    test('removes all credentials from sessionStorage and localStorage', () => {
      setAuthToken('token');
      setApiKey('key');
      clearAuth();
      expect(sessionStorageMock.removeItem).toHaveBeenCalledWith('authToken');
      expect(sessionStorageMock.removeItem).toHaveBeenCalledWith('apiKey');
      // Also clears legacy localStorage items
      expect(localStorageMock.removeItem).toHaveBeenCalledWith('authToken');
      expect(localStorageMock.removeItem).toHaveBeenCalledWith('apiKey');
      expect(isAuthenticated()).toBe(false);
    });
  });

  describe('getAuthHeaders', () => {
    test('returns content-type with no auth', () => {
      const headers = getAuthHeaders();
      expect(headers['Content-Type']).toBe('application/json');
      expect(headers['X-Authorization']).toBeUndefined();
      expect(headers['X-API-Key']).toBeUndefined();
    });

    test('includes Bearer token when set', () => {
      setAuthToken('my-token');
      const headers = getAuthHeaders();
      expect(headers['X-Authorization']).toBe('Bearer my-token');
    });

    test('includes API key when set', () => {
      setApiKey('my-key');
      const headers = getAuthHeaders();
      expect(headers['X-API-Key']).toBe('my-key');
    });

    test('prefers auth token over api key', () => {
      setAuthToken('token');
      setApiKey('key');
      const headers = getAuthHeaders();
      expect(headers['X-Authorization']).toBe('Bearer token');
      expect(headers['X-API-Key']).toBeUndefined();
    });
  });
});

describe('API Requests', () => {
  beforeEach(() => {
    clearAuth();
    fetchMock.mockReset();
  });

  describe('apiRequest', () => {
    test('makes request with correct URL', async () => {
      fetchMock.mockResolvedValue({
        ok: true,
        json: () => Promise.resolve({ data: 'test' })
      });

      await apiRequest('/test-endpoint');
      expect(fetchMock).toHaveBeenCalledWith(
        '/api/test-endpoint',
        expect.objectContaining({
          headers: expect.objectContaining({
            'Content-Type': 'application/json'
          })
        })
      );
    });

    test('adds x-amz-content-sha256 header for POST requests with body', async () => {
      fetchMock.mockResolvedValue({
        ok: true,
        json: () => Promise.resolve({})
      });

      await apiRequest('/test', {
        method: 'POST',
        body: JSON.stringify({ data: 'test' })
      });

      expect(fetchMock).toHaveBeenCalledWith(
        '/api/test',
        expect.objectContaining({
          headers: expect.objectContaining({
            'x-amz-content-sha256': expect.stringMatching(/^[a-f0-9]{64}$/)
          })
        })
      );
    });

    test('adds x-amz-content-sha256 header for POST requests without body', async () => {
      fetchMock.mockResolvedValue({
        ok: true,
        json: () => Promise.resolve({})
      });

      await apiRequest('/test', { method: 'POST' });

      // SHA256 of empty string
      const emptyHash = 'e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855';
      expect(fetchMock).toHaveBeenCalledWith(
        '/api/test',
        expect.objectContaining({
          headers: expect.objectContaining({
            'x-amz-content-sha256': emptyHash
          })
        })
      );
    });

    test('adds x-amz-content-sha256 header for PUT requests', async () => {
      fetchMock.mockResolvedValue({
        ok: true,
        json: () => Promise.resolve({})
      });

      await apiRequest('/test', {
        method: 'PUT',
        body: JSON.stringify({ data: 'update' })
      });

      expect(fetchMock).toHaveBeenCalledWith(
        '/api/test',
        expect.objectContaining({
          headers: expect.objectContaining({
            'x-amz-content-sha256': expect.stringMatching(/^[a-f0-9]{64}$/)
          })
        })
      );
    });

    test('adds x-amz-content-sha256 header for PATCH requests', async () => {
      fetchMock.mockResolvedValue({
        ok: true,
        json: () => Promise.resolve({})
      });

      await apiRequest('/test', {
        method: 'PATCH',
        body: JSON.stringify({ enabled: false })
      });

      expect(fetchMock).toHaveBeenCalledWith(
        '/api/test',
        expect.objectContaining({
          headers: expect.objectContaining({
            'x-amz-content-sha256': expect.stringMatching(/^[a-f0-9]{64}$/)
          })
        })
      );
    });

    test('adds x-amz-content-sha256 header for DELETE requests', async () => {
      fetchMock.mockResolvedValue({
        ok: true,
        json: () => Promise.resolve({})
      });

      await apiRequest('/test', { method: 'DELETE' });

      expect(fetchMock).toHaveBeenCalledWith(
        '/api/test',
        expect.objectContaining({
          headers: expect.objectContaining({
            'x-amz-content-sha256': expect.stringMatching(/^[a-f0-9]{64}$/)
          })
        })
      );
    });

    test('does not add x-amz-content-sha256 header for GET requests', async () => {
      fetchMock.mockResolvedValue({
        ok: true,
        json: () => Promise.resolve({})
      });

      await apiRequest('/test');

      const callArgs = fetchMock.mock.calls[0][1] as { headers: Record<string, string> };
      expect(callArgs.headers['x-amz-content-sha256']).toBeUndefined();
    });

    test('produces consistent hash for same body content', async () => {
      fetchMock.mockResolvedValue({
        ok: true,
        json: () => Promise.resolve({})
      });

      const body = JSON.stringify({ email: 'test@example.com', password: 'secret' });

      await apiRequest('/test1', { method: 'POST', body });
      await apiRequest('/test2', { method: 'POST', body });

      const call1 = fetchMock.mock.calls[0][1] as { headers: Record<string, string> };
      const call2 = fetchMock.mock.calls[1][1] as { headers: Record<string, string> };

      expect(call1.headers['x-amz-content-sha256']).toBe(call2.headers['x-amz-content-sha256']);
    });

    test('includes auth headers', async () => {
      setAuthToken('test-token');
      fetchMock.mockResolvedValue({
        ok: true,
        json: () => Promise.resolve({})
      });

      await apiRequest('/test');
      expect(fetchMock).toHaveBeenCalledWith(
        '/api/test',
        expect.objectContaining({
          headers: expect.objectContaining({
            'X-Authorization': 'Bearer test-token'
          })
        })
      );
    });

    test('throws error for non-ok response', async () => {
      fetchMock.mockResolvedValue({
        ok: false,
        status: 404,
        json: () => Promise.resolve({ error: 'Not found' })
      });

      await expect(apiRequest('/test')).rejects.toThrow('Not found');
    });

    test('includes status code in error', async () => {
      fetchMock.mockResolvedValue({
        ok: false,
        status: 500,
        json: () => Promise.reject(new Error('parse error'))
      });

      try {
        await apiRequest('/test');
      } catch (error) {
        expect((error as { status?: number }).status).toBe(500);
      }
    });
  });

  describe('login', () => {
    test('sends credentials and stores token', async () => {
      fetchMock.mockResolvedValue({
        ok: true,
        json: () => Promise.resolve({ token: 'new-token' })
      });

      await login('test@example.com', 'password');

      expect(fetchMock).toHaveBeenCalledWith(
        '/api/auth/login',
        expect.objectContaining({
          method: 'POST',
          // Password is now base64 encoded
          body: JSON.stringify({ email: 'test@example.com', password: btoa('password') })
        })
      );
      expect(sessionStorageMock.setItem).toHaveBeenCalledWith('authToken', 'new-token');
    });

    test('includes x-amz-content-sha256 header for CloudFront OAC', async () => {
      fetchMock.mockResolvedValue({
        ok: true,
        json: () => Promise.resolve({ token: 'new-token' })
      });

      await login('test@example.com', 'password');

      expect(fetchMock).toHaveBeenCalledWith(
        '/api/auth/login',
        expect.objectContaining({
          headers: expect.objectContaining({
            'x-amz-content-sha256': expect.stringMatching(/^[a-f0-9]{64}$/)
          })
        })
      );
    });

    test('throws error on failure', async () => {
      fetchMock.mockResolvedValue({
        ok: false,
        json: () => Promise.resolve({ error: 'Invalid credentials' })
      });

      await expect(login('test@example.com', 'wrong')).rejects.toThrow('Invalid credentials');
    });
  });

  describe('logout', () => {
    test('calls logout endpoint and clears auth', async () => {
      setAuthToken('token');
      fetchMock.mockResolvedValue({ ok: true });

      await logout();

      expect(fetchMock).toHaveBeenCalledWith(
        '/api/auth/logout',
        expect.objectContaining({ method: 'POST' })
      );
      expect(isAuthenticated()).toBe(false);
    });

    test('includes x-amz-content-sha256 header for CloudFront OAC', async () => {
      setAuthToken('token');
      fetchMock.mockResolvedValue({ ok: true });

      await logout();

      // SHA256 of empty string since logout has no body
      const emptyHash = 'e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855';
      expect(fetchMock).toHaveBeenCalledWith(
        '/api/auth/logout',
        expect.objectContaining({
          headers: expect.objectContaining({
            'x-amz-content-sha256': emptyHash
          })
        })
      );
    });

    test('clears auth even if server call fails', async () => {
      setAuthToken('token');
      fetchMock.mockRejectedValue(new Error('Network error'));

      await logout();
      expect(isAuthenticated()).toBe(false);
    });
  });

  describe('getCurrentUser', () => {
    test('fetches current user', async () => {
      fetchMock.mockResolvedValue({
        ok: true,
        json: () => Promise.resolve({ email: 'test@example.com', role: 'admin' })
      });

      const user = await getCurrentUser();
      expect(user.email).toBe('test@example.com');
      expect(fetchMock).toHaveBeenCalledWith('/api/auth/me', expect.anything());
    });
  });

  describe('requestPasswordReset', () => {
    test('sends reset request', async () => {
      fetchMock.mockResolvedValue({ ok: true });

      await requestPasswordReset('test@example.com');

      expect(fetchMock).toHaveBeenCalledWith(
        '/api/auth/forgot-password',
        expect.objectContaining({
          method: 'POST',
          body: JSON.stringify({ email: 'test@example.com' })
        })
      );
    });
  });

  describe('checkAdminExists', () => {
    test('returns true when admin exists', async () => {
      fetchMock.mockResolvedValue({
        ok: true,
        json: () => Promise.resolve({ admin_exists: true })
      });

      const result = await checkAdminExists('api-key');
      expect(result).toBe(true);
    });

    test('returns false when no admin', async () => {
      fetchMock.mockResolvedValue({
        ok: true,
        json: () => Promise.resolve({ admin_exists: false })
      });

      const result = await checkAdminExists('api-key');
      expect(result).toBe(false);
    });

    test('returns false on error', async () => {
      fetchMock.mockResolvedValue({ ok: false });

      const result = await checkAdminExists('api-key');
      expect(result).toBe(false);
    });
  });

  describe('setupAdmin', () => {
    test('creates admin and stores token', async () => {
      fetchMock.mockResolvedValue({
        ok: true,
        json: () => Promise.resolve({ token: 'admin-token' })
      });

      await setupAdmin('api-key', 'admin@example.com', 'password');

      expect(fetchMock).toHaveBeenCalledWith(
        '/api/auth/setup-admin',
        expect.objectContaining({
          method: 'POST',
          headers: expect.objectContaining({ 'X-API-Key': 'api-key' }),
          // Password is now base64 encoded
          body: JSON.stringify({ email: 'admin@example.com', password: btoa('password') })
        })
      );
      expect(sessionStorageMock.setItem).toHaveBeenCalledWith('authToken', 'admin-token');
    });
  });
});

describe('Dashboard API', () => {
  beforeEach(() => {
    fetchMock.mockReset();
    fetchMock.mockResolvedValue({
      ok: true,
      json: () => Promise.resolve({})
    });
  });

  describe('getDashboardSummary', () => {
    test('fetches summary with provider filter', async () => {
      await getDashboardSummary('aws');
      expect(fetchMock).toHaveBeenCalledWith(
        '/api/dashboard/summary?provider=aws',
        expect.anything()
      );
    });

    test('uses all providers by default', async () => {
      await getDashboardSummary();
      expect(fetchMock).toHaveBeenCalledWith(
        '/api/dashboard/summary?provider=all',
        expect.anything()
      );
    });
  });

  describe('getUpcomingPurchases', () => {
    test('fetches upcoming purchases', async () => {
      await getUpcomingPurchases();
      expect(fetchMock).toHaveBeenCalledWith(
        '/api/dashboard/upcoming',
        expect.anything()
      );
    });
  });
});

describe('Recommendations API', () => {
  beforeEach(() => {
    fetchMock.mockReset();
    fetchMock.mockResolvedValue({
      ok: true,
      json: () => Promise.resolve({})
    });
  });

  describe('getRecommendations', () => {
    test('fetches with no filters', async () => {
      await getRecommendations();
      expect(fetchMock).toHaveBeenCalledWith('/api/recommendations', expect.anything());
    });

    test('applies filters to query string', async () => {
      await getRecommendations({
        provider: 'aws',
        service: 'ec2',
        region: 'us-east-1',
        minSavings: 100
      });

      const url = fetchMock.mock.calls[0][0] as string;
      expect(url).toContain('provider=aws');
      expect(url).toContain('service=ec2');
      expect(url).toContain('region=us-east-1');
      expect(url).toContain('min_savings=100');
    });
  });

  describe('refreshRecommendations', () => {
    test('sends POST request', async () => {
      await refreshRecommendations();
      expect(fetchMock).toHaveBeenCalledWith(
        '/api/recommendations/refresh',
        expect.objectContaining({ method: 'POST' })
      );
    });
  });
});

describe('Plans API', () => {
  beforeEach(() => {
    fetchMock.mockReset();
    fetchMock.mockResolvedValue({
      ok: true,
      json: () => Promise.resolve({})
    });
  });

  describe('getPlans', () => {
    test('fetches all plans', async () => {
      await getPlans();
      expect(fetchMock).toHaveBeenCalledWith('/api/plans', expect.anything());
    });
  });

  describe('getPlan', () => {
    test('fetches single plan by ID', async () => {
      await getPlan('plan-123');
      expect(fetchMock).toHaveBeenCalledWith('/api/plans/plan-123', expect.anything());
    });
  });

  describe('createPlan', () => {
    test('sends POST with plan data', async () => {
      const plan: CreatePlanRequest = {
        name: 'Test Plan',
        enabled: true,
        auto_purchase: false,
        notification_days_before: 3,
        services: { 'aws:ec2': { provider: 'aws', service: 'ec2', enabled: true, term: 3, payment: 'all-upfront', coverage: 80 } },
        ramp_schedule: { type: 'immediate', percent_per_step: 100, step_interval_days: 0, current_step: 0, total_steps: 1 },
      };
      await createPlan(plan);

      expect(fetchMock).toHaveBeenCalledWith(
        '/api/plans',
        expect.objectContaining({
          method: 'POST',
          body: JSON.stringify(plan)
        })
      );
    });
  });

  describe('updatePlan', () => {
    test('sends PUT with plan data', async () => {
      const plan: CreatePlanRequest = {
        name: 'Updated Plan',
        enabled: true,
        auto_purchase: true,
        notification_days_before: 5,
        services: { 'aws:rds': { provider: 'aws', service: 'rds', enabled: true, term: 1, payment: 'no-upfront', coverage: 70 } },
        ramp_schedule: { type: 'weekly', percent_per_step: 25, step_interval_days: 7, current_step: 0, total_steps: 4 },
      };
      await updatePlan('plan-123', plan);

      expect(fetchMock).toHaveBeenCalledWith(
        '/api/plans/plan-123',
        expect.objectContaining({
          method: 'PUT',
          body: JSON.stringify(plan)
        })
      );
    });
  });

  describe('patchPlan', () => {
    test('sends PATCH with partial data', async () => {
      await patchPlan('plan-123', { enabled: false });

      expect(fetchMock).toHaveBeenCalledWith(
        '/api/plans/plan-123',
        expect.objectContaining({
          method: 'PATCH',
          body: JSON.stringify({ enabled: false })
        })
      );
    });
  });

  describe('deletePlan', () => {
    test('sends DELETE request', async () => {
      await deletePlan('plan-123');

      expect(fetchMock).toHaveBeenCalledWith(
        '/api/plans/plan-123',
        expect.objectContaining({ method: 'DELETE' })
      );
    });
  });
});

describe('History API', () => {
  beforeEach(() => {
    fetchMock.mockReset();
    fetchMock.mockResolvedValue({
      ok: true,
      json: () => Promise.resolve({})
    });
  });

  describe('getHistory', () => {
    test('fetches with no filters', async () => {
      await getHistory();
      expect(fetchMock).toHaveBeenCalledWith('/api/history', expect.anything());
    });

    test('applies date filters', async () => {
      await getHistory({ start: '2024-01-01', end: '2024-03-31' });

      const url = fetchMock.mock.calls[0][0] as string;
      expect(url).toContain('start=2024-01-01');
      expect(url).toContain('end=2024-03-31');
    });

    test('applies provider and plan filters', async () => {
      await getHistory({ provider: 'aws', planId: 'plan-123' });

      const url = fetchMock.mock.calls[0][0] as string;
      expect(url).toContain('provider=aws');
      expect(url).toContain('plan_id=plan-123');
    });
  });
});

describe('Config API', () => {
  beforeEach(() => {
    fetchMock.mockReset();
    fetchMock.mockResolvedValue({
      ok: true,
      json: () => Promise.resolve({})
    });
  });

  describe('getConfig', () => {
    test('fetches config', async () => {
      await getConfig();
      expect(fetchMock).toHaveBeenCalledWith('/api/config', expect.anything());
    });
  });

  describe('updateConfig', () => {
    test('sends PUT with config data', async () => {
      const config: Config = {
        enabled_providers: ['aws', 'azure'],
        notification_email: 'test@example.com',
        auto_collect: true,
        collection_schedule: 'daily',
        default_term: 3,
        default_payment: 'all-upfront',
        default_coverage: 80,
        notification_days_before: 3
      };
      await updateConfig(config);

      expect(fetchMock).toHaveBeenCalledWith(
        '/api/config',
        expect.objectContaining({
          method: 'PUT',
          body: JSON.stringify(config)
        })
      );
    });
  });
});

describe('Purchase API', () => {
  beforeEach(() => {
    fetchMock.mockReset();
    fetchMock.mockResolvedValue({
      ok: true,
      json: () => Promise.resolve({})
    });
  });

  describe('executePurchase', () => {
    test('sends POST with recommendations', async () => {
      const recs: Recommendation[] = [{
        id: 'rec-1',
        provider: 'aws',
        service: 'ec2',
        region: 'us-east-1',
        resource_type: 'm5.large',
        count: 1,
        term: 3,
        payment: 'all-upfront',
        upfront_cost: 100,
        monthly_cost: 0,
        savings: 30,
        selected: true,
        purchased: false,
      }];
      await executePurchase(recs);

      expect(fetchMock).toHaveBeenCalledWith(
        '/api/purchases/execute',
        expect.objectContaining({
          method: 'POST',
          body: JSON.stringify({ recommendations: recs })
        })
      );
    });
  });

  describe('getPurchaseDetails', () => {
    test('fetches purchase by ID', async () => {
      await getPurchaseDetails('exec-123');
      expect(fetchMock).toHaveBeenCalledWith('/api/purchases/exec-123', expect.anything());
    });
  });

  describe('cancelPurchase', () => {
    test('sends POST to cancel', async () => {
      await cancelPurchase('exec-123');

      expect(fetchMock).toHaveBeenCalledWith(
        '/api/purchases/cancel/exec-123',
        expect.objectContaining({ method: 'POST' })
      );
    });
  });
});

describe('Public Info API', () => {
  describe('getPublicInfo', () => {
    test('fetches public info without auth', async () => {
      fetchMock.mockResolvedValue({
        ok: true,
        json: () => Promise.resolve({ version: '1.0.0', admin_exists: true, api_key_secret_url: 'https://...' })
      });

      const info = await getPublicInfo();
      expect(info.api_key_secret_url).toBeTruthy();
      expect(fetchMock).toHaveBeenCalledWith('/api/info');
    });

    test('returns default values on error', async () => {
      fetchMock.mockResolvedValue({ ok: false });

      const info = await getPublicInfo();
      expect(info.version).toBe('');
      expect(info.admin_exists).toBe(false);
    });
  });
});
