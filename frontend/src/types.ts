/**
 * Shared type definitions for CUDly frontend
 */

import { Chart } from 'chart.js';
import * as api from './api';

// App state interface
export interface AppState {
  currentUser: api.User | null;
  currentProvider: api.Provider | '';
  currentAccountIDs: string[]; // selected account UUIDs; empty = all accounts
  currentRecommendations: api.Recommendation[];
  selectedRecommendations: Set<number>;
  savingsChart: Chart | null;
}

// Dashboard types
export interface DashboardSummary {
  potential_monthly_savings?: number;
  total_recommendations?: number;
  active_commitments?: number;
  committed_monthly?: number;
  current_coverage?: number;
  target_coverage?: number;
  ytd_savings?: number;
  by_service?: Record<string, ServiceSavings>;
}

export interface ServiceSavings {
  potential_savings: number;
  current_savings: number;
}

export interface UpcomingPurchase {
  execution_id: string;
  plan_name: string;
  scheduled_date: string;
  provider: api.Provider;
  service: string;
  step_number: number;
  total_steps: number;
  estimated_savings: number;
}

// Recommendations types
export interface RecommendationsResponse {
  recommendations?: LocalRecommendation[];
  summary?: RecommendationsSummary;
  regions?: string[];
}

export interface LocalRecommendation {
  provider: api.Provider;
  service: string;
  resource_type: string;
  engine?: string;
  region: string;
  count: number;
  term: number;
  savings: number;
  upfront_cost: number;
  cloud_account_id?: string;
}

export interface RecommendationsSummary {
  total_count?: number;
  total_monthly_savings?: number;
  total_upfront_cost?: number;
  avg_payback_months?: number;
}

// Plans types
export interface PlansResponse {
  plans?: LocalPlan[];
}

export interface LocalPlan {
  id: string;
  name: string;
  description?: string;
  provider: api.Provider;
  service: string;
  term: number;
  payment: string;
  target_coverage: number;
  ramp_schedule: api.RampSchedule;
  auto_purchase: boolean;
  enabled: boolean;
  notification_days_before: number;
  current_step?: number;
  total_steps?: number;
  next_execution_date?: string;
  custom_step_percent?: number;
  custom_interval_days?: number;
}

export interface SavePlanData {
  name: string;
  description: string;
  provider: string;
  service: string;
  term: number;
  payment: string;
  target_coverage: number;
  ramp_schedule: string;
  auto_purchase: boolean;
  notification_days_before: number;
  enabled: boolean;
  custom_step_percent?: number;
  custom_interval_days?: number;
  recommendations?: api.Recommendation[];
}

// History types
export interface HistoryResponse {
  summary?: HistorySummary;
  purchases?: HistoryPurchase[];
}

export interface HistorySummary {
  total_purchases?: number;
  total_upfront?: number;
  total_monthly_savings?: number;
  total_annual_savings?: number;
}

export interface HistoryPurchase {
  timestamp: string;
  provider: string;
  service: string;
  resource_type: string;
  region: string;
  count: number;
  term: number;
  upfront_cost: number;
  estimated_savings: number;
  plan_name?: string;
}

// Savings Analytics types
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

// Settings types
export interface ConfigResponse {
  global?: GlobalConfig;
  services?: api.ServiceConfig[];
  credentials?: CredentialsConfig;
  source_cloud?: string;
}

export interface GlobalConfig {
  enabled_providers?: string[];
  notification_email?: string;
  auto_collect?: boolean;
  collection_schedule?: string;
  default_term?: number;
  default_payment?: string;
  default_coverage?: number;
  notification_days_before?: number;
}

export interface CredentialsConfig {
  azure_configured?: boolean;
  gcp_configured?: boolean;
}

// API Keys types
export interface APIKeyInfo {
  id: string;
  name: string;
  key_prefix: string;
  is_active: boolean;
  expires_at?: string;
  created_at: string;
  last_used_at?: string;
  permissions?: api.Permission[];
}

export interface CreateAPIKeyResponse {
  api_key: string;  // Full key shown only once
  key_id: string;
  key: APIKeyInfo;
}

// Window type declarations
declare global {
  interface Window {
    refreshRecommendations: () => Promise<void>;
    openCreatePlanModal: () => void;
    openNewPlanModal: () => void;
    closePlanModal: () => void;
    closePurchaseModal: () => void;
    resetSettings: () => void;
    loadHistory: () => Promise<void>;
    logout: () => Promise<void>;
    openCreateUserModal: () => void;
    closeUserModal: () => void;
    openCreateGroupModal: () => void;
    closeGroupModal: () => void;
    addPermission: () => void;
  }
}
