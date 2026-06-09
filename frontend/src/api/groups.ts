/**
 * Group Management API functions
 */

import { apiRequest } from './client';
import type { APIGroup, CreateGroupRequest, UpdateGroupRequest } from './types';

/**
 * List all groups
 */
export async function listGroups(): Promise<{ groups: APIGroup[] }> {
  return apiRequest<{ groups: APIGroup[] }>('/groups');
}

/**
 * Get a single group
 */
export async function getGroup(groupId: string): Promise<APIGroup> {
  return apiRequest<APIGroup>(`/groups/${groupId}`);
}

/**
 * Create a new group
 */
export async function createGroup(req: CreateGroupRequest): Promise<APIGroup> {
  return apiRequest<APIGroup>('/groups', {
    method: 'POST',
    body: JSON.stringify(req)
  });
}

/**
 * Update a group
 */
export async function updateGroup(groupId: string, req: UpdateGroupRequest): Promise<APIGroup> {
  return apiRequest<APIGroup>(`/groups/${groupId}`, {
    method: 'PUT',
    body: JSON.stringify(req)
  });
}

/**
 * Delete a group
 */
export async function deleteGroup(groupId: string): Promise<void> {
  return apiRequest<void>(`/groups/${groupId}`, { method: 'DELETE' });
}
