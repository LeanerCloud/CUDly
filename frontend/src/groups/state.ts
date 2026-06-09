/**
 * Group management state
 */

import type { APIGroup } from '../api';

// State for modal management
export let currentEditingGroup: APIGroup | null = null;

// Setters for state
export function setCurrentEditingGroup(group: APIGroup | null): void {
  currentEditingGroup = group;
}
