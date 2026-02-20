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
  total_savings: number;
  monthly_savings: number;
  active_plans: number;
  pending_purchases: number;
  recommendations_count: number;
  savings_by_service: Record<string, number>;
  savings_by_provider: Record<string, number>;
}

export interface UpcomingPurchase {
  id: string;
  plan_id: string;
  plan_name: string;
  scheduled_date: string;
  provider: Provider;
  service: string;
  estimated_savings: number;
  status: string;
}

// Recommendation types
export interface Recommendation {
  id: string;
  provider: Provider;
  service: string;
  region: string;
  instance_type?: string;
  current_cost: number;
  recommended_cost: number;
  estimated_savings: number;
  term_years: number;
  payment_option: PaymentOption;
  coverage: number;
  description: string;
}

export interface RecommendationFilters {
  provider?: Provider | 'all';
  service?: string;
  region?: string;
  minSavings?: number;
}

// Plan types
export interface Plan {
  id: string;
  name: string;
  description?: string;
  provider: Provider;
  service: string;
  term: number;
  payment_option: PaymentOption;
  coverage: number;
  ramp_schedule: RampSchedule;
  auto_purchase: boolean;
  enabled: boolean;
  notify_days: number;
  created_at: string;
  updated_at: string;
}

export interface CreatePlanRequest {
  name: string;
  description?: string;
  provider: Provider;
  service: string;
  term: number;
  payment_option: PaymentOption;
  coverage: number;
  ramp_schedule: RampSchedule;
  auto_purchase: boolean;
  enabled: boolean;
  notify_days: number;
}

// History types
export interface PurchaseHistory {
  id: string;
  plan_id: string;
  plan_name: string;
  executed_at: string;
  provider: Provider;
  service: string;
  region: string;
  upfront_cost: number;
  estimated_savings: number;
  status: 'completed' | 'failed' | 'pending';
  error?: string;
}

export interface HistoryFilters {
  start?: string;
  end?: string;
  provider?: Provider;
  planId?: string;
}

// Config types
export interface Config {
  enabled_providers: Provider[];
  notification_email: string;
  auto_collect: boolean;
  default_term: number;
  default_payment: PaymentOption;
  default_coverage: number;
  notification_days: number;
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
}

export interface UpdateGroupRequest {
  name?: string;
  description?: string;
  permissions?: Permission[];
}

// Credential Types
export interface AzureCredentials {
  tenant_id: string;
  client_id: string;
  client_secret: string;
  subscription_id: string;
}

export interface GCPCredentials {
  type: string;
  project_id: string;
  private_key_id: string;
  private_key: string;
  client_email: string;
  client_id: string;
  auth_uri?: string;
  token_uri?: string;
  auth_provider_x509_cert_url?: string;
  client_x509_cert_url?: string;
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
}

// Internal types
export interface ApiError extends Error {
  status?: number;
}

export interface RequestOptions extends RequestInit {
  headers?: Record<string, string>;
}
