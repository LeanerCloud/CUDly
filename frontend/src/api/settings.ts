/**
 * Settings and Configuration API functions
 */

import { apiRequest } from './client';
import type { Config, ServiceConfig } from './types';
import type { ConfigResponse } from '../types';

/**
 * Get global configuration
 */
export async function getConfig(): Promise<ConfigResponse> {
  return apiRequest<ConfigResponse>('/config');
}

/**
 * Update per-service configuration
 */
export async function updateServiceConfig(provider: string, service: string, cfg: ServiceConfig): Promise<void> {
  return apiRequest<void>(`/config/service/${provider}/${service}`, {
    method: 'PUT',
    body: JSON.stringify(cfg)
  });
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
