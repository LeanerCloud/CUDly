/**
 * Cloud Accounts API functions
 */

import { apiRequest } from './client';
import type { Provider } from './types';

export interface CloudAccount {
  id: string;
  name: string;
  description?: string;
  provider: Provider;
  external_id: string;
  contact_email?: string;
  enabled: boolean;
  aws_auth_mode?: 'access_keys' | 'role_arn' | 'bastion';
  aws_role_arn?: string;
  aws_external_id?: string;
  aws_bastion_id?: string;
  aws_is_org_root?: boolean;
  azure_subscription_id?: string;
  azure_tenant_id?: string;
  azure_client_id?: string;
  gcp_project_id?: string;
  gcp_client_email?: string;
  credentials_configured: boolean;
  created_at: string;
  updated_at: string;
}

export interface CloudAccountRequest {
  name: string;
  description?: string;
  contact_email?: string;
  provider: Provider;
  external_id: string;
  enabled?: boolean;
  aws_auth_mode?: string;
  aws_role_arn?: string;
  aws_external_id?: string;
  aws_bastion_id?: string;
  aws_is_org_root?: boolean;
  azure_subscription_id?: string;
  azure_tenant_id?: string;
  azure_client_id?: string;
  gcp_project_id?: string;
  gcp_client_email?: string;
}

export interface AccountListFilters {
  provider?: Provider;
  enabled?: boolean;
  search?: string;
}

export interface AccountCredentialsRequest {
  credential_type: 'aws_access_keys' | 'azure_client_secret' | 'gcp_service_account';
  payload: Record<string, unknown>;
}

export interface AccountTestResult {
  ok: boolean;
  message: string;
}

export interface AccountServiceOverride {
  id: string;
  account_id: string;
  provider: string;
  service: string;
  enabled?: boolean;
  term?: number;
  payment?: string;
  coverage?: number;
  ramp_schedule?: string;
  include_engines?: string[];
  exclude_engines?: string[];
  include_regions?: string[];
  exclude_regions?: string[];
  include_types?: string[];
  exclude_types?: string[];
}

export interface AccountServiceOverrideRequest {
  enabled?: boolean;
  term?: number;
  payment?: string;
  coverage?: number;
  ramp_schedule?: string;
  include_engines?: string[];
  exclude_engines?: string[];
  include_regions?: string[];
  exclude_regions?: string[];
  include_types?: string[];
  exclude_types?: string[];
}

export async function listAccounts(filters?: AccountListFilters): Promise<CloudAccount[]> {
  const params = new URLSearchParams();
  if (filters?.provider) params.set('provider', filters.provider);
  if (filters?.enabled !== undefined) params.set('enabled', String(filters.enabled));
  if (filters?.search) params.set('search', filters.search);
  const qs = params.toString();
  return apiRequest<CloudAccount[]>(`/accounts${qs ? `?${qs}` : ''}`);
}

export async function createAccount(req: CloudAccountRequest): Promise<CloudAccount> {
  return apiRequest<CloudAccount>('/accounts', {
    method: 'POST',
    body: JSON.stringify(req)
  });
}

export async function getAccount(id: string): Promise<CloudAccount> {
  return apiRequest<CloudAccount>(`/accounts/${id}`);
}

export async function updateAccount(id: string, req: CloudAccountRequest): Promise<CloudAccount> {
  return apiRequest<CloudAccount>(`/accounts/${id}`, {
    method: 'PUT',
    body: JSON.stringify(req)
  });
}

export async function deleteAccount(id: string): Promise<void> {
  return apiRequest<void>(`/accounts/${id}`, { method: 'DELETE' });
}

export async function saveAccountCredentials(id: string, req: AccountCredentialsRequest): Promise<void> {
  return apiRequest<void>(`/accounts/${id}/credentials`, {
    method: 'POST',
    body: JSON.stringify(req)
  });
}

export async function testAccountCredentials(id: string): Promise<AccountTestResult> {
  return apiRequest<AccountTestResult>(`/accounts/${id}/test`, { method: 'POST' });
}

export async function listAccountServiceOverrides(id: string): Promise<AccountServiceOverride[]> {
  return apiRequest<AccountServiceOverride[]>(`/accounts/${id}/service-overrides`);
}

export async function saveAccountServiceOverride(
  accountId: string,
  provider: string,
  service: string,
  req: AccountServiceOverrideRequest
): Promise<AccountServiceOverride> {
  return apiRequest<AccountServiceOverride>(
    `/accounts/${accountId}/service-overrides/${provider}/${service}`,
    { method: 'PUT', body: JSON.stringify(req) }
  );
}

export async function deleteAccountServiceOverride(
  accountId: string,
  provider: string,
  service: string
): Promise<void> {
  return apiRequest<void>(
    `/accounts/${accountId}/service-overrides/${provider}/${service}`,
    { method: 'DELETE' }
  );
}

export async function listPlanAccounts(planId: string): Promise<CloudAccount[]> {
  return apiRequest<CloudAccount[]>(`/plans/${planId}/accounts`);
}

export async function setPlanAccounts(planId: string, accountIds: string[]): Promise<void> {
  return apiRequest<void>(`/plans/${planId}/accounts`, {
    method: 'PUT',
    body: JSON.stringify({ account_ids: accountIds })
  });
}
