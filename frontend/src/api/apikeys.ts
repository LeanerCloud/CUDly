/**
 * API Keys Management API functions
 */

import { apiRequest } from './client';
import type { GetAPIKeysResponse, CreateAPIKeyRequest, CreateAPIKeyResponse } from './types';

/**
 * Get all API keys
 */
export async function getApiKeys(): Promise<GetAPIKeysResponse> {
  return apiRequest<GetAPIKeysResponse>('/api-keys');
}

/**
 * Create a new API key
 */
export async function createApiKey(req: CreateAPIKeyRequest): Promise<CreateAPIKeyResponse> {
  return apiRequest<CreateAPIKeyResponse>('/api-keys', {
    method: 'POST',
    body: JSON.stringify(req)
  });
}

/**
 * Revoke an API key
 */
export async function revokeApiKey(keyId: string): Promise<void> {
  return apiRequest<void>(`/api-keys/${keyId}/revoke`, { method: 'POST' });
}

/**
 * Delete an API key
 */
export async function deleteApiKey(keyId: string): Promise<void> {
  return apiRequest<void>(`/api-keys/${keyId}`, { method: 'DELETE' });
}
