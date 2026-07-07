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
 * Update global configuration.
 *
 * Accepts a partial config: the backend merges the body over the stored config
 * (any key absent from the JSON keeps its persisted value), so callers may send
 * only the fields they intend to change (e.g. the laddering kill-switch toggle
 * sends just { laddering_enabled }). A full Config is still valid input.
 */
export async function updateConfig(config: Partial<Config>): Promise<Config> {
  return apiRequest<Config>('/config', {
    method: 'PUT',
    body: JSON.stringify(config)
  });
}
