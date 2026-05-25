/**
 * Plans API functions
 */

import { apiRequest } from './client';
import type { Plan, CreatePlanRequest, PlanFilters } from './types';

/**
 * Get purchase plans, optionally filtered by account IDs.
 *
 * When filters.account_ids is non-empty the backend returns only plans
 * that reference at least one of those accounts via the plan_accounts
 * join table. Mirrors the account_ids filtering pattern in
 * getRecommendations (see recommendations.ts).
 */
export async function getPlans(filters: PlanFilters = {}): Promise<Plan[]> {
  const params = new URLSearchParams();
  if (filters.account_ids && filters.account_ids.length > 0) {
    params.set('account_ids', filters.account_ids.join(','));
  }
  const queryString = params.toString();
  return apiRequest<Plan[]>(`/plans${queryString ? '?' + queryString : ''}`);
}

/**
 * Get a single plan
 */
export async function getPlan(planId: string): Promise<Plan> {
  return apiRequest<Plan>(`/plans/${planId}`);
}

/**
 * Create a new plan
 */
export async function createPlan(plan: CreatePlanRequest): Promise<Plan> {
  return apiRequest<Plan>('/plans', {
    method: 'POST',
    body: JSON.stringify(plan)
  });
}

/**
 * Update a plan
 */
export async function updatePlan(planId: string, plan: CreatePlanRequest): Promise<Plan> {
  return apiRequest<Plan>(`/plans/${planId}`, {
    method: 'PUT',
    body: JSON.stringify(plan)
  });
}

/**
 * Patch a plan (partial update)
 */
export async function patchPlan(planId: string, data: Partial<CreatePlanRequest>): Promise<Plan> {
  return apiRequest<Plan>(`/plans/${planId}`, {
    method: 'PATCH',
    body: JSON.stringify(data)
  });
}

/**
 * Delete a plan
 */
export async function deletePlan(planId: string): Promise<void> {
  return apiRequest<void>(`/plans/${planId}`, { method: 'DELETE' });
}
