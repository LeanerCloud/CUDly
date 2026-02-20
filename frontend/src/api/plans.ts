/**
 * Plans API functions
 */

import { apiRequest } from './client';
import type { Plan, CreatePlanRequest } from './types';

/**
 * Get purchase plans
 */
export async function getPlans(): Promise<Plan[]> {
  return apiRequest<Plan[]>('/plans');
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
