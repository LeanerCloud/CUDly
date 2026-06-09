/**
 * User Management API functions
 */

import { apiRequest, base64Encode } from './client';
import type {
  APIUser,
  CreateUserRequest,
  CreateUserResponse,
  UpdateUserRequest
} from './types';

/**
 * List all users
 */
export async function listUsers(): Promise<{ users: APIUser[] }> {
  return apiRequest<{ users: APIUser[] }>('/users');
}

/**
 * Get a single user
 */
export async function getUser(userId: string): Promise<APIUser> {
  return apiRequest<APIUser>(`/users/${userId}`);
}

/**
 * Create a new user
 */
export async function createUser(req: CreateUserRequest): Promise<CreateUserResponse> {
  // Base64 encode password to match backend expectation. Pass an empty
  // string through unmodified so the backend's invite-on-empty-password
  // path triggers — base64-encoding "" would still produce "" but
  // skipping the call keeps intent obvious.
  const encodedReq = {
    ...req,
    password: req.password ? base64Encode(req.password) : ''
  };
  return apiRequest<CreateUserResponse>('/users', {
    method: 'POST',
    body: JSON.stringify(encodedReq)
  });
}

/**
 * Update a user
 */
export async function updateUser(userId: string, req: UpdateUserRequest): Promise<APIUser> {
  return apiRequest<APIUser>(`/users/${userId}`, {
    method: 'PUT',
    body: JSON.stringify(req)
  });
}

/**
 * Delete a user
 */
export async function deleteUser(userId: string): Promise<void> {
  return apiRequest<void>(`/users/${userId}`, { method: 'DELETE' });
}
