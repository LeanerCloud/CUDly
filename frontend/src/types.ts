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
  // selectedRecommendations holds rec IDs (not indices). ID-keyed
  // storage survives filter changes that would reshuffle index
  // positions — selecting row 3 and then changing filter used to
  // mean "row 3 is selected" relative to the new list, i.e. some
  // other rec the user never clicked.
  selectedRecommendations: Set<string>;
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
  id: string;
  provider: api.Provider;
  service: string;
  resource_type: string;
  engine?: string;
  region: string;
  count: number;
  term: number;
  savings: number;
  upfront_cost: number;
  monthly_cost?: number;
  cloud_account_id?: string;
  // Populated by the scheduler when any active purchase_suppression
  // matches this rec's 6-tuple. The three fields drive the "recently
  // purchased N/M — Xd remaining" badge. Absent / 0 means no active
  // suppression (badge hidden). Added in Commit 2 of the bulk-purchase-
  // with-grace feature.
  suppressed_count?: number;
  suppression_expires_at?: string;
  primary_suppression_execution_id?: string;
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
  total_completed?: number;
  total_pending?: number;
  total_failed?: number;
  total_expired?: number;
  total_upfront?: number;
  total_monthly_savings?: number;
  total_annual_savings?: number;
}

export interface HistoryPurchase {
  purchase_id?: string;
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
  // Status is set by the API to "completed" or "pending". Legacy pre-schema
  // rows come back without it; the UI treats absent status as completed for
  // counting + badge rendering.
  status?: string;
  // Approver: the email address the approval request was sent to. Only set
  // on pending rows — the UI renders "awaiting approval from <approver>" so
  // the user knows exactly whose inbox to check.
  approver?: string;
  // StatusDescription: short reason rendered below the status badge for
  // non-ok rows. "failed" → backend's send-error message; "expired" → canned
  // 7-day-window reminder. Empty on completed / pending rows.
  status_description?: string;
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
export interface SourceIdentity {
  provider: string;
  account_id?: string;
  subscription_id?: string;
  tenant_id?: string;
  client_id?: string;
  project_id?: string;
}

export interface ConfigResponse {
  global?: GlobalConfig;
  services?: api.ServiceConfig[];
  source_cloud?: string;
  source_identity?: SourceIdentity;
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
  // Per-provider grace-period window (days) for the recently-purchased
  // suppression feature. Keys: 'aws' / 'azure' / 'gcp'. Missing keys
  // fall back to the backend default (7). Explicit 0 = disabled.
  grace_period_days?: Record<string, number>;
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
