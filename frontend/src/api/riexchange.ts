/**
 * RI Exchange API functions
 */

import { apiRequest } from './client';
import type {
  ConvertibleRI,
  RIUtilization,
  ReshapeRecommendation,
  ExchangeQuoteRequest,
  ExchangeQuoteSummary,
  ExchangeExecuteRequest,
  ExchangeResult,
  RIExchangeConfig,
  RIExchangeHistoryRecord
} from './types';

/**
 * List active convertible Reserved Instances
 */
export async function listConvertibleRIs(): Promise<ConvertibleRI[]> {
  const resp = await apiRequest<{ instances: ConvertibleRI[] }>('/ri-exchange/instances');
  return resp.instances ?? [];
}

/**
 * Get per-RI utilization from Cost Explorer
 */
export async function getRIUtilization(lookbackDays?: number): Promise<RIUtilization[]> {
  const params = lookbackDays ? `?lookback_days=${lookbackDays}` : '';
  const resp = await apiRequest<{ utilization: RIUtilization[] }>(`/ri-exchange/utilization${params}`);
  return resp.utilization ?? [];
}

/**
 * Get automated reshape recommendations
 */
export async function getReshapeRecommendations(threshold?: number): Promise<ReshapeRecommendation[]> {
  const params = threshold !== undefined ? `?threshold=${threshold}` : '';
  const resp = await apiRequest<{ recommendations: ReshapeRecommendation[] }>(`/ri-exchange/reshape-recommendations${params}`);
  return resp.recommendations ?? [];
}

/**
 * Get an exchange quote
 */
export async function getExchangeQuote(req: ExchangeQuoteRequest): Promise<ExchangeQuoteSummary> {
  return apiRequest<ExchangeQuoteSummary>('/ri-exchange/quote', {
    method: 'POST',
    body: JSON.stringify(req),
  });
}

/**
 * Execute an RI exchange
 */
export async function executeExchange(req: ExchangeExecuteRequest): Promise<ExchangeResult> {
  return apiRequest<ExchangeResult>('/ri-exchange/execute', {
    method: 'POST',
    body: JSON.stringify(req),
  });
}

/**
 * Get RI exchange automation config
 */
export async function getRIExchangeConfig(): Promise<RIExchangeConfig> {
  return apiRequest<RIExchangeConfig>('/ri-exchange/config');
}

/**
 * Update RI exchange automation config
 */
export async function updateRIExchangeConfig(config: RIExchangeConfig): Promise<void> {
  await apiRequest<{ status: string }>('/ri-exchange/config', {
    method: 'PUT',
    body: JSON.stringify(config),
  });
}

/**
 * Get RI exchange history records
 */
export async function getRIExchangeHistory(
  params?: { status?: string; limit?: number }
): Promise<RIExchangeHistoryRecord[]> {
  let qs = '';
  if (params) {
    const parts: string[] = [];
    if (params.status) parts.push(`status=${encodeURIComponent(params.status)}`);
    if (params.limit) parts.push(`limit=${params.limit}`);
    if (parts.length > 0) qs = `?${parts.join('&')}`;
  }
  const resp = await apiRequest<{ records: RIExchangeHistoryRecord[] }>(`/ri-exchange/history${qs}`);
  return resp.records ?? [];
}
