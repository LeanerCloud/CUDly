/**
 * Type definitions for CUDly API
 */

// Core types
export type Provider = 'aws' | 'azure' | 'gcp';
export type PaymentOption = 'no-upfront' | 'partial-upfront' | 'all-upfront';
export type RampSchedule = 'immediate' | 'weekly-25pct' | 'monthly-10pct' | 'custom';

// User types
export interface User {
  id: string;
  email: string;
  role: string;
}

export interface LoginResponse {
  token: string;
  user?: User;
}

// Dashboard types
export interface DashboardSummary {
  potential_monthly_savings: number;
  total_recommendations: number;
  active_commitments: number;
  committed_monthly: number;
  current_coverage: number;
  target_coverage: number;
  ytd_savings: number;
  by_service: Record<string, { potential_savings: number; current_savings: number }>;
}

export interface UpcomingPurchase {
  execution_id: string;
  plan_name: string;
  scheduled_date: string;
  provider: string;
  service: string;
  step_number: number;
  total_steps: number;
  estimated_savings: number;
}

// Recommendation types
export interface Recommendation {
  id: string;
  provider: string;
  service: string;
  region: string;
  resource_type: string;
  engine?: string;
  count: number;
  term: number;
  payment: string;
  upfront_cost: number;
  monthly_cost: number;
  savings: number;
  selected: boolean;
  purchased: boolean;
  purchase_id?: string;
  error?: string;
  cloud_account_id?: string;
}

export interface RecommendationFilters {
  provider?: Provider | '';
  service?: string;
  region?: string;
  minSavings?: number;
  account_ids?: string[];
}

// Plan types
export interface PlanRampSchedule {
  type: string;
  percent_per_step: number;
  step_interval_days: number;
  current_step: number;
  total_steps: number;
  start_date?: string;
}

export interface Plan {
  id: string;
  name: string;
  enabled: boolean;
  auto_purchase: boolean;
  notification_days_before: number;
  services: Record<string, ServiceConfig>;
  ramp_schedule: PlanRampSchedule;
  created_at: string;
  updated_at: string;
  next_execution_date?: string;
  last_execution_date?: string;
}

export interface CreatePlanRequest {
  name: string;
  enabled: boolean;
  auto_purchase: boolean;
  notification_days_before: number;
  services: Record<string, ServiceConfig>;
  ramp_schedule: PlanRampSchedule;
}

// History types
export interface PurchaseHistory {
  account_id: string;
  purchase_id: string;
  timestamp: string;
  provider: string;
  service: string;
  region: string;
  resource_type: string;
  count: number;
  term: number;
  payment: string;
  upfront_cost: number;
  monthly_cost: number;
  estimated_savings: number;
  plan_id?: string;
  plan_name?: string;
  ramp_step?: number;
  cloud_account_id?: string;
}

export interface HistoryFilters {
  start?: string;
  end?: string;
  provider?: Provider;
  planId?: string;
  account_ids?: string[];
}

// Config types
export interface Config {
  enabled_providers: Provider[];
  notification_email?: string;
  auto_collect: boolean;
  collection_schedule: string;
  notification_days_before: number;
  approval_required?: boolean;
  default_term: number;
  default_payment: PaymentOption;
  default_coverage: number;
  default_ramp_schedule?: string;
  ri_exchange_enabled?: boolean;
  ri_exchange_mode?: string;
  ri_exchange_utilization_threshold?: number;
  ri_exchange_max_per_exchange_usd?: number;
  ri_exchange_max_daily_usd?: number;
  ri_exchange_lookback_days?: number;
}

export interface ServiceConfig {
  provider: string;
  service: string;
  enabled: boolean;
  term: number;
  payment: string;
  coverage: number;
  ramp_schedule?: string;
  include_engines?: string[];
  exclude_engines?: string[];
  include_regions?: string[];
  exclude_regions?: string[];
  include_types?: string[];
  exclude_types?: string[];
}

export interface PublicInfo {
  version: string;
  admin_exists: boolean;
  api_key_secret_url?: string;
}

// Purchase types
export interface PurchaseResult {
  execution_id: string;
  status: string;
  results: Array<{
    recommendation_id: string;
    status: string;
    error?: string;
  }>;
}

export interface PurchaseDetails {
  execution_id: string;
  status: string;
  created_at: string;
  completed_at?: string;
  results: Array<{
    recommendation_id: string;
    status: string;
    confirmation_id?: string;
    error?: string;
  }>;
}

export interface PlannedPurchasesResponse {
  purchases: PlannedPurchase[];
}

export interface PlannedPurchase {
  id: string;
  plan_id: string;
  plan_name: string;
  scheduled_date: string;
  provider: Provider;
  service: string;
  resource_type: string;
  region: string;
  count: number;
  term: number;
  payment: string;
  estimated_savings: number;
  upfront_cost: number;
  status: 'pending' | 'paused' | 'running' | 'completed' | 'failed';
  step_number: number;
  total_steps: number;
}

// User Management Types
export interface APIUser {
  id: string;
  email: string;
  role: string;
  groups: string[];
  mfa_enabled: boolean;
  created_at?: string;
  updated_at?: string;
  last_login?: string;
}

export interface CreateUserRequest {
  email: string;
  password: string;
  role: string;
  groups?: string[];
}

export interface UpdateUserRequest {
  email?: string;
  role?: string;
  groups?: string[];
}

// Group Management Types
export interface APIGroup {
  id: string;
  name: string;
  description: string;
  permissions: Permission[];
  allowed_accounts?: string[];
  created_at?: string;
  updated_at?: string;
}

export interface Permission {
  action: string;
  resource: string;
  constraints?: {
    accounts?: string[];
    providers?: string[];
    services?: string[];
    regions?: string[];
    max_amount?: number;
  };
}

export interface CreateGroupRequest {
  name: string;
  description?: string;
  permissions: Permission[];
  allowed_accounts?: string[];
}

export interface UpdateGroupRequest {
  name?: string;
  description?: string;
  permissions?: Permission[];
  allowed_accounts?: string[];
}

// API Keys Management Types
export interface APIKeyInfo {
  id: string;
  name: string;
  key_prefix: string;
  is_active: boolean;
  expires_at?: string;
  created_at: string;
  last_used_at?: string;
  permissions?: Permission[];
}

export interface CreateAPIKeyRequest {
  name: string;
  permissions?: Permission[];
  expires_at?: string;
}

export interface CreateAPIKeyResponse {
  api_key: string;  // Full key shown only once
  key_id: string;
  key: APIKeyInfo;
}

export interface GetAPIKeysResponse {
  keys: APIKeyInfo[];
}

// Savings Analytics Types
export interface SavingsAnalyticsResponse {
  start: string;
  end: string;
  interval: string;
  summary: SavingsAnalyticsSummary;
  data_points: SavingsDataPoint[];
}

export interface SavingsAnalyticsSummary {
  total_period_savings: number;
  total_upfront_spent: number;
  purchase_count: number;
  average_savings_per_period: number;
  peak_savings: number;
}

export interface SavingsDataPoint {
  timestamp: string;
  total_savings: number;
  total_upfront: number;
  purchase_count: number;
  cumulative_savings: number;
  by_service?: Record<string, number>;
  by_provider?: Record<string, number>;
}

export interface SavingsBreakdownResponse {
  dimension: string;
  start: string;
  end: string;
  data: Record<string, SavingsBreakdownValue>;
}

export interface SavingsBreakdownValue {
  total_savings: number;
  total_upfront: number;
  purchase_count: number;
  percentage: number;
}

export interface SavingsAnalyticsFilters {
  start?: string;
  end?: string;
  interval?: 'hourly' | 'daily' | 'weekly' | 'monthly';
  provider?: Provider;
  service?: string;
  account_ids?: string[];
}

// RI Exchange Types
export interface ConvertibleRI {
  reserved_instance_id: string;
  instance_type: string;
  availability_zone: string;
  instance_count: number;
  start: string;
  end: string;
  offering_type: string;
  fixed_price: number;
  usage_price: number;
  state: string;
  normalization_factor: number;
}

export interface RIUtilization {
  reserved_instance_id: string;
  utilization_percent: number;
  purchased_hours: number;
  total_actual_hours: number;
  unused_hours: number;
}

export interface OfferingOption {
  instance_type: string;
  offering_id: string;
  effective_monthly_cost: number;
}

export interface ReshapeRecommendation {
  source_ri_id: string;
  source_instance_type: string;
  source_count: number;
  target_instance_type: string;
  target_count: number;
  alternative_targets?: OfferingOption[];
  utilization_percent: number;
  normalized_used: number;
  normalized_purchased: number;
  reason: string;
}

export interface ExchangeQuoteRequest {
  ri_ids: string[];
  target_offering_id: string;
  target_count: number;
  region?: string;
}

export interface ExchangeQuoteSummary {
  IsValidExchange: boolean;
  ValidationFailureReason: string;
  CurrencyCode: string;
  PaymentDueRaw: string;
  SourceHourlyPriceRaw: string;
  SourceRemainingUpfrontRaw: string;
  SourceRemainingTotalRaw: string;
  TargetHourlyPriceRaw: string;
  TargetRemainingUpfrontRaw: string;
  TargetRemainingTotalRaw: string;
  OutputReservedInstancesExp?: string;
}

export interface ExchangeExecuteRequest {
  ri_ids: string[];
  target_offering_id: string;
  target_count: number;
  max_payment_due_usd: string;
  region?: string;
}

export interface ExchangeResult {
  exchange_id: string;
  quote: ExchangeQuoteSummary;
}

// RI Exchange Config Types
export interface RIExchangeConfig {
  auto_exchange_enabled: boolean;
  mode: "manual" | "auto";
  utilization_threshold: number;
  max_payment_per_exchange_usd: number;
  max_payment_daily_usd: number;
  lookback_days: number;
}

export interface RIExchangeHistoryRecord {
  id: string;
  account_id: string;
  exchange_id: string;
  region: string;
  source_ri_ids: string[];
  source_instance_type: string;
  source_count: number;
  target_offering_id: string;
  target_instance_type: string;
  target_count: number;
  payment_due: string;
  status: string;
  mode: string;
  error?: string;
  created_at: string;
  updated_at: string;
  completed_at?: string;
  expires_at?: string;
}

// Internal types
export interface ApiError extends Error {
  status?: number;
}

export interface RequestOptions extends RequestInit {
  headers?: Record<string, string>;
}
