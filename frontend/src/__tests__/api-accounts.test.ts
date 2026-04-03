/**
 * Tests for src/api/accounts.ts module
 */
import {
  listAccounts,
  createAccount,
  getAccount,
  updateAccount,
  deleteAccount,
  saveAccountCredentials,
  testAccountCredentials,
  listAccountServiceOverrides,
  saveAccountServiceOverride,
  deleteAccountServiceOverride,
  listPlanAccounts,
  setPlanAccounts
} from '../api/accounts';
import { apiRequest } from '../api/client';

// Mock the client module
jest.mock('../api/client', () => ({
  apiRequest: jest.fn()
}));

const mockAccount = {
  id: 'acc-1',
  name: 'Test Account',
  description: 'A test account',
  provider: 'aws' as const,
  external_id: '123456789012',
  contact_email: 'test@example.com',
  enabled: true,
  credentials_configured: true,
  created_at: '2024-01-01T00:00:00Z',
  updated_at: '2024-01-02T00:00:00Z'
};

describe('Accounts API Module', () => {
  beforeEach(() => {
    jest.clearAllMocks();
  });

  describe('listAccounts', () => {
    test('calls apiRequest with /accounts and no query string when no filters', async () => {
      (apiRequest as jest.Mock).mockResolvedValue([mockAccount]);

      const result = await listAccounts();

      expect(apiRequest).toHaveBeenCalledWith('/accounts');
      expect(result).toEqual([mockAccount]);
    });

    test('includes provider filter in query string', async () => {
      (apiRequest as jest.Mock).mockResolvedValue([mockAccount]);

      await listAccounts({ provider: 'aws' });

      expect(apiRequest).toHaveBeenCalledWith('/accounts?provider=aws');
    });

    test('includes search filter in query string', async () => {
      (apiRequest as jest.Mock).mockResolvedValue([]);

      await listAccounts({ search: 'production' });

      expect(apiRequest).toHaveBeenCalledWith('/accounts?search=production');
    });

    test('includes enabled filter in query string', async () => {
      (apiRequest as jest.Mock).mockResolvedValue([]);

      await listAccounts({ enabled: false });

      expect(apiRequest).toHaveBeenCalledWith('/accounts?enabled=false');
    });

    test('combines multiple filters in query string', async () => {
      (apiRequest as jest.Mock).mockResolvedValue([mockAccount]);

      await listAccounts({ provider: 'aws', search: 'prod', enabled: true });

      expect(apiRequest).toHaveBeenCalledWith('/accounts?provider=aws&enabled=true&search=prod');
    });
  });

  describe('createAccount', () => {
    test('calls apiRequest with POST and serialized body', async () => {
      (apiRequest as jest.Mock).mockResolvedValue(mockAccount);

      const req = {
        name: 'New Account',
        provider: 'aws' as const,
        external_id: '123456789012'
      };

      const result = await createAccount(req);

      expect(apiRequest).toHaveBeenCalledWith('/accounts', {
        method: 'POST',
        body: JSON.stringify(req)
      });
      expect(result).toEqual(mockAccount);
    });

    test('includes optional fields in the body', async () => {
      (apiRequest as jest.Mock).mockResolvedValue(mockAccount);

      const req = {
        name: 'AWS Account',
        provider: 'aws' as const,
        external_id: '111122223333',
        description: 'Production AWS',
        contact_email: 'ops@example.com',
        enabled: true,
        aws_auth_mode: 'role_arn',
        aws_role_arn: 'arn:aws:iam::111122223333:role/CUDly'
      };

      await createAccount(req);

      expect(apiRequest).toHaveBeenCalledWith('/accounts', {
        method: 'POST',
        body: JSON.stringify(req)
      });
    });
  });

  describe('getAccount', () => {
    test('calls apiRequest with correct GET URL', async () => {
      (apiRequest as jest.Mock).mockResolvedValue(mockAccount);

      const result = await getAccount('acc-1');

      expect(apiRequest).toHaveBeenCalledWith('/accounts/acc-1');
      expect(result).toEqual(mockAccount);
    });
  });

  describe('updateAccount', () => {
    test('calls apiRequest with PUT and serialized body', async () => {
      const updated = { ...mockAccount, name: 'Updated Account' };
      (apiRequest as jest.Mock).mockResolvedValue(updated);

      const req = {
        name: 'Updated Account',
        provider: 'aws' as const,
        external_id: '123456789012'
      };

      const result = await updateAccount('acc-1', req);

      expect(apiRequest).toHaveBeenCalledWith('/accounts/acc-1', {
        method: 'PUT',
        body: JSON.stringify(req)
      });
      expect(result).toEqual(updated);
    });
  });

  describe('deleteAccount', () => {
    test('calls apiRequest with DELETE method', async () => {
      (apiRequest as jest.Mock).mockResolvedValue(undefined);

      await deleteAccount('acc-1');

      expect(apiRequest).toHaveBeenCalledWith('/accounts/acc-1', { method: 'DELETE' });
    });
  });

  describe('saveAccountCredentials', () => {
    test('calls apiRequest with POST and credential payload for aws_access_keys', async () => {
      (apiRequest as jest.Mock).mockResolvedValue(undefined);

      const req = {
        credential_type: 'aws_access_keys' as const,
        payload: { access_key_id: 'AKIA...', secret_access_key: 'secret' }
      };

      await saveAccountCredentials('acc-1', req);

      expect(apiRequest).toHaveBeenCalledWith('/accounts/acc-1/credentials', {
        method: 'POST',
        body: JSON.stringify(req)
      });
    });

    test('calls apiRequest with POST and credential payload for azure_client_secret', async () => {
      (apiRequest as jest.Mock).mockResolvedValue(undefined);

      const req = {
        credential_type: 'azure_client_secret' as const,
        payload: { client_secret: 'my-secret' }
      };

      await saveAccountCredentials('acc-2', req);

      expect(apiRequest).toHaveBeenCalledWith('/accounts/acc-2/credentials', {
        method: 'POST',
        body: JSON.stringify(req)
      });
    });

    test('calls apiRequest with POST and credential payload for gcp_service_account', async () => {
      (apiRequest as jest.Mock).mockResolvedValue(undefined);

      const req = {
        credential_type: 'gcp_service_account' as const,
        payload: { key_json: '{"type":"service_account"}' }
      };

      await saveAccountCredentials('acc-3', req);

      expect(apiRequest).toHaveBeenCalledWith('/accounts/acc-3/credentials', {
        method: 'POST',
        body: JSON.stringify(req)
      });
    });
  });

  describe('testAccountCredentials', () => {
    test('calls apiRequest with POST to /test endpoint', async () => {
      const testResult = { ok: true, message: 'Credentials are valid' };
      (apiRequest as jest.Mock).mockResolvedValue(testResult);

      const result = await testAccountCredentials('acc-1');

      expect(apiRequest).toHaveBeenCalledWith('/accounts/acc-1/test', { method: 'POST' });
      expect(result).toEqual(testResult);
    });

    test('returns failed test result when credentials are invalid', async () => {
      const testResult = { ok: false, message: 'Authentication failed' };
      (apiRequest as jest.Mock).mockResolvedValue(testResult);

      const result = await testAccountCredentials('acc-1');

      expect(result).toEqual(testResult);
    });
  });

  describe('listAccountServiceOverrides', () => {
    test('calls apiRequest with correct GET URL', async () => {
      const overrides = [
        {
          id: 'ovr-1',
          account_id: 'acc-1',
          provider: 'aws',
          service: 'ec2',
          enabled: true,
          term: 1,
          coverage: 80
        }
      ];
      (apiRequest as jest.Mock).mockResolvedValue(overrides);

      const result = await listAccountServiceOverrides('acc-1');

      expect(apiRequest).toHaveBeenCalledWith('/accounts/acc-1/service-overrides');
      expect(result).toEqual(overrides);
    });

    test('returns empty array when no overrides', async () => {
      (apiRequest as jest.Mock).mockResolvedValue([]);

      const result = await listAccountServiceOverrides('acc-1');

      expect(result).toEqual([]);
    });
  });

  describe('saveAccountServiceOverride', () => {
    test('calls apiRequest with PUT to provider/service path', async () => {
      const override = {
        id: 'ovr-1',
        account_id: 'acc-1',
        provider: 'aws',
        service: 'ec2',
        enabled: true,
        term: 1,
        coverage: 75
      };
      (apiRequest as jest.Mock).mockResolvedValue(override);

      const req = { enabled: true, term: 1, coverage: 75 };

      const result = await saveAccountServiceOverride('acc-1', 'aws', 'ec2', req);

      expect(apiRequest).toHaveBeenCalledWith('/accounts/acc-1/service-overrides/aws/ec2', {
        method: 'PUT',
        body: JSON.stringify(req)
      });
      expect(result).toEqual(override);
    });

    test('includes all optional fields in the body', async () => {
      const override = {
        id: 'ovr-2',
        account_id: 'acc-1',
        provider: 'aws',
        service: 'rds',
        enabled: true,
        term: 3,
        payment: 'all_upfront',
        coverage: 90,
        include_regions: ['us-east-1'],
        exclude_types: ['db.t2.micro']
      };
      (apiRequest as jest.Mock).mockResolvedValue(override);

      const req = {
        enabled: true,
        term: 3,
        payment: 'all_upfront',
        coverage: 90,
        include_regions: ['us-east-1'],
        exclude_types: ['db.t2.micro']
      };

      await saveAccountServiceOverride('acc-1', 'aws', 'rds', req);

      expect(apiRequest).toHaveBeenCalledWith('/accounts/acc-1/service-overrides/aws/rds', {
        method: 'PUT',
        body: JSON.stringify(req)
      });
    });
  });

  describe('deleteAccountServiceOverride', () => {
    test('calls apiRequest with DELETE to provider/service path', async () => {
      (apiRequest as jest.Mock).mockResolvedValue(undefined);

      await deleteAccountServiceOverride('acc-1', 'aws', 'ec2');

      expect(apiRequest).toHaveBeenCalledWith('/accounts/acc-1/service-overrides/aws/ec2', {
        method: 'DELETE'
      });
    });

    test('uses correct path for different provider and service', async () => {
      (apiRequest as jest.Mock).mockResolvedValue(undefined);

      await deleteAccountServiceOverride('acc-2', 'azure', 'compute');

      expect(apiRequest).toHaveBeenCalledWith('/accounts/acc-2/service-overrides/azure/compute', {
        method: 'DELETE'
      });
    });
  });

  describe('listPlanAccounts', () => {
    test('calls apiRequest with correct GET URL', async () => {
      (apiRequest as jest.Mock).mockResolvedValue([mockAccount]);

      const result = await listPlanAccounts('plan-1');

      expect(apiRequest).toHaveBeenCalledWith('/plans/plan-1/accounts');
      expect(result).toEqual([mockAccount]);
    });

    test('returns empty array when plan has no accounts', async () => {
      (apiRequest as jest.Mock).mockResolvedValue([]);

      const result = await listPlanAccounts('plan-99');

      expect(apiRequest).toHaveBeenCalledWith('/plans/plan-99/accounts');
      expect(result).toEqual([]);
    });
  });

  describe('setPlanAccounts', () => {
    test('calls apiRequest with PUT and account_ids body', async () => {
      (apiRequest as jest.Mock).mockResolvedValue(undefined);

      await setPlanAccounts('plan-1', ['acc-1', 'acc-2']);

      expect(apiRequest).toHaveBeenCalledWith('/plans/plan-1/accounts', {
        method: 'PUT',
        body: JSON.stringify({ account_ids: ['acc-1', 'acc-2'] })
      });
    });

    test('handles empty account list', async () => {
      (apiRequest as jest.Mock).mockResolvedValue(undefined);

      await setPlanAccounts('plan-1', []);

      expect(apiRequest).toHaveBeenCalledWith('/plans/plan-1/accounts', {
        method: 'PUT',
        body: JSON.stringify({ account_ids: [] })
      });
    });
  });
});
