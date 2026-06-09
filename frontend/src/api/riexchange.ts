/**
 * RI Exchange API functions
 */

import { apiRequest } from './client';
import type {
  ConvertibleRI,
  ExchangeableAzureRI,
  RIUtilization,
  ReshapeRecommendation,
  ExchangeQuoteRequest,
  ExchangeQuoteSummary,
  ExchangeExecuteRequest,
  ExchangeResult,
  RIExchangeConfig,
  RIExchangeHistoryRecord,
  TargetOffering,
} from './types';

/**
 * List active convertible Reserved Instances for the running AWS account.
 *
 * The optional accountID scopes the listing to a single AWS account so the
 * page honours the Main Header global account filter (issue #871). The
 * backend returns an empty list when the selected account is not the running
 * AWS account.
 */
export async function listConvertibleRIs(accountID?: string): Promise<ConvertibleRI[]> {
  const qs = accountID ? `?account_id=${encodeURIComponent(accountID)}` : '';
  const resp = await apiRequest<{ instances: ConvertibleRI[] }>(`/ri-exchange/instances${qs}`);
  return resp.instances ?? [];
}

/**
 * List active Azure VM reservations eligible for the cross-SKU/cross-region
 * exchange flow (issue #871). The optional subscriptionID scopes the
 * capacity-provider registration check to one subscription; the listing
 * itself is tenant-wide on the backend.
 */
export async function listExchangeableAzureRIs(subscriptionID?: string): Promise<ExchangeableAzureRI[]> {
  const qs = subscriptionID ? `?subscription_id=${encodeURIComponent(subscriptionID)}` : '';
  const resp = await apiRequest<{ reservations: ExchangeableAzureRI[] }>(`/ri-exchange/azure-instances${qs}`);
  return resp.reservations ?? [];
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
 * Reshape recommendations response, including the Cost Explorer cache
 * freshness fields populated by the backend.
 *
 * recs_staleness is "" when the underlying cache is fresh, "soft" when
 * it is older than 12 h, and "hard" when it is older than 24 h.
 * recs_collected_at carries the raw ISO-8601 timestamp so the banner
 * can display a relative-time label ("last collected 23h ago").
 */
export interface ReshapeRecommendationsResponse {
  recommendations: ReshapeRecommendation[];
  recs_staleness?: string;
  recs_collected_at?: string | null;
}

/**
 * Get automated reshape recommendations
 */
export async function getReshapeRecommendations(threshold?: number): Promise<ReshapeRecommendationsResponse> {
  const params = threshold !== undefined ? `?threshold=${threshold}` : '';
  return apiRequest<ReshapeRecommendationsResponse>(`/ri-exchange/reshape-recommendations${params}`);
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
 * List valid target offerings for a convertible RI exchange.
 * Returns offerings from DescribeReservedInstancesOfferings filtered to
 * the same convertible class / term / product-description as the source RI.
 */
export async function listTargetOfferings(sourceRIId: string, region?: string): Promise<TargetOffering[]> {
  let qs = `?source_ri_id=${encodeURIComponent(sourceRIId)}`;
  if (region) qs += `&region=${encodeURIComponent(region)}`;
  const resp = await apiRequest<{ offerings: TargetOffering[] }>(`/ri-exchange/target-offerings${qs}`);
  return resp.offerings ?? [];
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

/**
 * Approve a pending RI exchange via session auth (issue #300).
 * Mirrors the purchase approvePurchase API method.
 */
export async function approveRIExchange(id: string): Promise<void> {
  await apiRequest<unknown>(`/ri-exchange/approve/${encodeURIComponent(id)}`, {
    method: 'POST',
  });
}
