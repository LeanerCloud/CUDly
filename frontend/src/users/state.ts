/**
 * User list state and filtering state
 */

import type { APIUser, APIGroup } from '../api';

// State for modal management
export let currentEditingUser: APIUser | null = null;
export let availableGroups: APIGroup[] = [];

// State for filtering and search
export let allUsers: APIUser[] = [];
export let filteredUsers: APIUser[] = [];
export let searchQuery = '';
export let roleFilter = '';
export let mfaFilter = '';
export let groupFilter = '';

// State for bulk operations
export let selectedUserIds = new Set<string>();

// Setters for state
export function setCurrentEditingUser(user: APIUser | null): void {
  currentEditingUser = user;
}

export function setAvailableGroups(groups: APIGroup[]): void {
  availableGroups = groups;
}

export function setAllUsers(users: APIUser[]): void {
  allUsers = users;
}

export function setFilteredUsers(users: APIUser[]): void {
  filteredUsers = users;
}

export function setSearchQuery(query: string): void {
  searchQuery = query;
}

export function setRoleFilter(filter: string): void {
  roleFilter = filter;
}

export function setMfaFilter(filter: string): void {
  mfaFilter = filter;
}

export function setGroupFilter(filter: string): void {
  groupFilter = filter;
}

export function clearSelectedUserIds(): void {
  selectedUserIds.clear();
}

export function addSelectedUserId(id: string): void {
  selectedUserIds.add(id);
}

export function removeSelectedUserId(id: string): void {
  selectedUserIds.delete(id);
}
