/**
 * Settings and Configuration API functions
 */

import { apiRequest } from './client';
import type { Config, AzureCredentials, GCPCredentials } from './types';

/**
 * Get global configuration
 */
export async function getConfig(): Promise<Config> {
  return apiRequest<Config>('/config');
}

/**
 * Update global configuration
 */
export async function updateConfig(config: Config): Promise<Config> {
  return apiRequest<Config>('/config', {
    method: 'PUT',
    body: JSON.stringify(config)
  });
}

/**
 * Save Azure credentials
 */
export async function saveAzureCredentials(credentials: AzureCredentials): Promise<void> {
  return apiRequest<void>('/credentials/azure', {
    method: 'POST',
    body: JSON.stringify(credentials)
  });
}

/**
 * Save GCP credentials
 */
export async function saveGCPCredentials(credentials: GCPCredentials): Promise<void> {
  return apiRequest<void>('/credentials/gcp', {
    method: 'POST',
    body: JSON.stringify(credentials)
  });
}
