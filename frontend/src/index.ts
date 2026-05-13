/**
 * CUDly - Cloud Commitment Optimizer Dashboard
 * Main entry point
 */

import './styles.css';
import * as api from './api';
import * as utils from './utils';
import { init } from './app';
import { refreshRecommendations } from './recommendations';
import { openCreatePlanModal, openNewPlanModal, closePlanModal, closePurchaseModal } from './plans';
import { resetSettings } from './settings';
import { loadHistory } from './history';
import { logout } from './auth';
import { openCreateUserModal, closeUserModal } from './users/userModals';
import { openCreateGroupModal, closeGroupModal, addPermission, closeDuplicateGroupModal, saveDuplicateGroup } from './groups/groupModals';
import { initTopbarFilters } from './topbar-filters';

// Re-export for external use
export { api, utils };

// Import types for global window declarations
import './types';

// Set up global window functions for HTML onclick handlers
window.refreshRecommendations = refreshRecommendations;
window.openCreatePlanModal = openCreatePlanModal;
window.openNewPlanModal = openNewPlanModal;
window.closePlanModal = closePlanModal;
window.closePurchaseModal = closePurchaseModal;
window.resetSettings = resetSettings;
window.loadHistory = loadHistory;
window.logout = logout;
window.openCreateUserModal = openCreateUserModal;
window.closeUserModal = closeUserModal;
window.openCreateGroupModal = openCreateGroupModal;
window.closeGroupModal = closeGroupModal;
window.addPermission = addPermission;

// Wire event listeners for buttons that previously used inline onclick
// (CSP blocks inline event handlers when script-src is 'self' without 'unsafe-inline')
document.addEventListener('DOMContentLoaded', () => {
  document.getElementById('create-user-btn')?.addEventListener('click', openCreateUserModal);
  document.getElementById('create-group-btn')?.addEventListener('click', openCreateGroupModal);
  document.getElementById('close-user-modal-btn')?.addEventListener('click', closeUserModal);
  document.getElementById('close-group-modal-btn')?.addEventListener('click', closeGroupModal);
  document.getElementById('add-permission-btn')?.addEventListener('click', () => addPermission());
  document.getElementById('close-group-duplicate-modal-btn')?.addEventListener('click', closeDuplicateGroupModal);
  document.getElementById('cancel-group-duplicate-btn')?.addEventListener('click', closeDuplicateGroupModal);
  document.getElementById('group-duplicate-form')?.addEventListener('submit', (e) => void saveDuplicateGroup(e));

  // Sidebar collapse toggle (issue #340). Persisted in localStorage so the
  // user's preference survives reloads.
  const sidebar = document.querySelector<HTMLElement>('.app-sidebar');
  const sidebarToggle = document.querySelector<HTMLButtonElement>('.app-sidebar-toggle');
  if (sidebar && sidebarToggle) {
    const STORAGE_KEY = 'cudly_sidebar_collapsed';
    if (localStorage.getItem(STORAGE_KEY) === '1') {
      sidebar.classList.add('collapsed');
      sidebarToggle.setAttribute('aria-expanded', 'false');
    }
    sidebarToggle.addEventListener('click', () => {
      const isCollapsed = sidebar.classList.toggle('collapsed');
      sidebarToggle.setAttribute('aria-expanded', isCollapsed ? 'false' : 'true');
      localStorage.setItem(STORAGE_KEY, isCollapsed ? '1' : '0');
    });
  }

  // Global filter chips in the topbar (issue #344 T2). Replaces the
  // per-section provider/account dropdowns that Home / Plans / Purchases
  // used to carry; sections subscribe to state.subscribeProvider /
  // subscribeAccount and reload themselves when the filter changes.
  initTopbarFilters();
});

// Initialize on page load
document.addEventListener('DOMContentLoaded', () => void init());
