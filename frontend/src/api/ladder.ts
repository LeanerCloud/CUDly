/**
 * Commitment Laddering API functions (issue #1333 phase 3).
 *
 * The feature is flag-gated default-off: the global kill-switch
 * (global_config.laddering_enabled) must be true AND the per-account
 * LadderConfig.enabled must be true before any laddering engine run fires.
 */

import { apiRequest } from './client';

/**
 * A single ramp step within a ladder ramp schedule.
 * AfterDays is the delay from run start; Fraction is the share of the
 * total target allocated by this tranche (fractions must sum to 1.0).
 */
export interface LadderRampStep {
  after_days: number;
  fraction: number;
}

/**
 * Per-account, per-provider ladder configuration.
 *
 * Mode controls whether runs require human approval before executing:
 *   email_approval - sends an approval email; purchases fire only after approval
 *   auto_approve   - purchases fire immediately (no human gate)
 *
 * Cadence controls how often the engine runs:
 *   daily  - once per day
 *   weekly - once per week
 *
 * All numeric money fields use number|null rather than 0 so absent/unconfigured
 * values are distinguishable from a deliberately configured $0.
 */
export interface LadderConfig {
  id?: string;
  cloud_account_id: string;
  provider: string;
  enabled: boolean;
  mode: 'email_approval' | 'auto_approve';
  cadence: 'daily' | 'weekly';
  target_coverage: number;
  buffer_fraction: number;
  baseline_percentile: number;
  lookback_days: number;
  buffer_utilization_threshold: number;
  /** null = no cap on hourly commitment delta per run */
  max_hourly_commit_per_run: number | null;
  max_actions_per_run: number;
  ramp_schedule: { steps: LadderRampStep[] };
  created_at?: string;
  updated_at?: string;
}

/**
 * List all per-account ladder configurations.
 * Requires view:config permission.
 */
export async function getLadderConfigs(): Promise<LadderConfig[]> {
  const resp = await apiRequest<{ configs: LadderConfig[] }>('/ladder/configs');
  return resp.configs ?? [];
}

/**
 * Upsert (insert or update) a per-account ladder configuration.
 * The upsert key is (cloud_account_id, provider).
 * Requires update:config permission.
 */
export async function upsertLadderConfig(cfg: LadderConfig): Promise<LadderConfig> {
  return apiRequest<LadderConfig>('/ladder/configs', {
    method: 'PUT',
    body: JSON.stringify(cfg),
  });
}
