/**
 * Account Registrations API functions
 */

import { apiRequest } from './client';
import type { Provider } from './types';
import type { CloudAccount, CloudAccountRequest } from './accounts';

export interface AccountRegistration {
  id: string;
  reference_token: string;
  status: 'pending' | 'approved' | 'rejected';
  provider: Provider;
  external_id: string;
  account_name: string;
  contact_email: string;
  description?: string;
  source_provider?: Provider;
  aws_role_arn?: string;
  aws_auth_mode?: string;
  aws_external_id?: string;
  azure_subscription_id?: string;
  azure_tenant_id?: string;
  azure_client_id?: string;
  azure_auth_mode?: string;
  gcp_project_id?: string;
  gcp_client_email?: string;
  gcp_auth_mode?: string;
  has_credentials?: boolean;
  rejection_reason?: string;
  cloud_account_id?: string;
  reviewed_by?: string;
  reviewed_at?: string;
  created_at: string;
  updated_at: string;
}

export async function listRegistrations(status?: string): Promise<AccountRegistration[]> {
  const params = new URLSearchParams();
  if (status) params.set('status', status);
  const query = params.toString();
  return apiRequest<AccountRegistration[]>(`/registrations${query ? '?' + query : ''}`);
}

export async function getRegistration(id: string): Promise<AccountRegistration> {
  return apiRequest<AccountRegistration>(`/registrations/${id}`);
}

export async function approveRegistration(id: string, account: CloudAccountRequest): Promise<CloudAccount> {
  return apiRequest<CloudAccount>(`/registrations/${id}/approve`, {
    method: 'POST',
    body: JSON.stringify(account),
  });
}

export async function rejectRegistration(id: string, reason?: string): Promise<AccountRegistration> {
  return apiRequest<AccountRegistration>(`/registrations/${id}/reject`, {
    method: 'POST',
    body: JSON.stringify({ reason: reason || '' }),
  });
}

export async function deleteRegistration(id: string): Promise<void> {
  await apiRequest<void>(`/registrations/${id}`, { method: 'DELETE' });
}
