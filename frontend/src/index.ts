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
import { openCreateGroupModal, closeGroupModal, addPermission } from './groups/groupModals';

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
});

// Initialize on page load
document.addEventListener('DOMContentLoaded', () => void init());
