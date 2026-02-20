/**
 * CUDly - Application initialization and event setup
 */

import * as api from './api';
import * as state from './state';
import { showLoginModal, showResetPasswordModal, updateUserUI, logout } from './auth';
import { loadDashboard, setupDashboardHandlers } from './dashboard';
import { setupRecommendationsHandlers, refreshRecommendations } from './recommendations';
import { switchTab } from './navigation';
import { savePlan, setupPlanHandlers, closePlanModal, openCreatePlanModal, openNewPlanModal, closePurchaseModal } from './plans';
import { saveGlobalSettings, setupSettingsHandlers, resetSettings, closeAzureCredsModal, closeGCPCredsModal, copyToClipboard } from './settings';
import { setupUserHandlers } from './users';
import { initApiKeys } from './apikeys';
import { loadHistory } from './history';
import { initSavingsHistory } from './modules/savings-history';

/**
 * Initialize app
 */
export async function init(): Promise<void> {
  api.initAuth();

  // Check if this is a password reset link
  const urlParams = new URLSearchParams(window.location.search);
  const resetToken = urlParams.get('token');
  if (resetToken) {
    await showResetPasswordModal(resetToken);
    return;
  }

  if (!api.isAuthenticated()) {
    await showLoginModal();
    return;
  }

  try {
    const user = await api.getCurrentUser();
    state.setCurrentUser(user);
    await loadDashboard();
    setupEventListeners();
    updateUserUI();
  } catch (error) {
    console.error('Init error:', error);
    const err = error as { status?: number; message?: string };
    if (err.status === 401 || err.message?.includes('Unauthorized')) {
      await showLoginModal();
    }
  }
}

/**
 * Setup event listeners
 */
export function setupEventListeners(): void {
  // Tab switching
  document.querySelectorAll<HTMLButtonElement>('.tab-btn').forEach(btn => {
    btn.addEventListener('click', () => {
      const tab = btn.dataset['tab'];
      if (tab) switchTab(tab);
    });
  });

  // Forms
  const planForm = document.getElementById('plan-form');
  if (planForm) {
    planForm.addEventListener('submit', (e) => void savePlan(e));
  }

  const settingsForm = document.getElementById('global-settings-form');
  if (settingsForm) {
    settingsForm.addEventListener('submit', (e) => void saveGlobalSettings(e));
  }

  // Setup settings event handlers (provider toggles, schedule visibility, etc.)
  setupSettingsHandlers();

  // Setup dashboard event handlers (provider filter)
  setupDashboardHandlers();

  // Setup recommendations event handlers (provider filter, service filter, etc.)
  setupRecommendationsHandlers();

  // Setup plan event handlers (provider-aware service dropdown)
  setupPlanHandlers();

  // Setup user management event handlers
  setupUserHandlers();

  // Setup API keys management
  initApiKeys();

  // Setup savings history charts
  initSavingsHistory();

  // Setup feedback link
  setupFeedbackLink();

  // Setup all button event listeners (replacing onclick handlers)
  setupButtonHandlers();

  // Ramp schedule toggle
  document.querySelectorAll<HTMLInputElement>('input[name="ramp-schedule"]').forEach(radio => {
    radio.addEventListener('change', () => {
      const customConfig = document.getElementById('custom-ramp-config');
      if (customConfig) {
        customConfig.classList.toggle('hidden', radio.value !== 'custom');
      }
    });
  });
}

/**
 * Setup all button event listeners (Security improvement: replaces inline onclick handlers)
 */
function setupButtonHandlers(): void {
  // Recommendations buttons
  const refreshRecsBtn = document.getElementById('refresh-recommendations-btn');
  if (refreshRecsBtn) {
    refreshRecsBtn.addEventListener('click', () => void refreshRecommendations());
  }

  const createPlanBtn = document.getElementById('create-plan-btn');
  if (createPlanBtn) {
    createPlanBtn.addEventListener('click', () => openCreatePlanModal());
  }

  // Plans buttons
  const newPlanBtn = document.getElementById('new-plan-btn');
  if (newPlanBtn) {
    newPlanBtn.addEventListener('click', () => openNewPlanModal());
  }

  const closePlanBtn = document.getElementById('close-plan-modal-btn');
  if (closePlanBtn) {
    closePlanBtn.addEventListener('click', () => closePlanModal());
  }

  // Purchase modal buttons
  const closePurchaseBtn = document.getElementById('close-purchase-modal-btn');
  if (closePurchaseBtn) {
    closePurchaseBtn.addEventListener('click', () => closePurchaseModal());
  }

  const executePurchaseBtn = document.getElementById('execute-purchase-btn');
  if (executePurchaseBtn) {
    // Note: executePurchase is handled internally by plans.ts - this button may be dynamically added
    executePurchaseBtn.addEventListener('click', () => {
      console.log('Execute purchase clicked');
    });
  }

  // Recommendation selection modal buttons - these may be dynamically added
  const closeSelectRecsBtn = document.getElementById('close-select-recommendations-btn');
  if (closeSelectRecsBtn) {
    closeSelectRecsBtn.addEventListener('click', () => {
      const modal = document.getElementById('select-recommendations-modal');
      if (modal) modal.classList.add('hidden');
    });
  }

  const confirmSelectRecsBtn = document.getElementById('confirm-select-recommendations-btn');
  if (confirmSelectRecsBtn) {
    confirmSelectRecsBtn.addEventListener('click', () => {
      console.log('Confirm recommendations clicked');
    });
  }

  // Settings buttons
  const resetSettingsBtn = document.getElementById('reset-settings-btn');
  if (resetSettingsBtn) {
    resetSettingsBtn.addEventListener('click', () => resetSettings());
  }

  // Azure credentials modal
  const closeAzureBtn = document.getElementById('close-azure-modal-btn');
  if (closeAzureBtn) {
    closeAzureBtn.addEventListener('click', () => closeAzureCredsModal());
  }

  // GCP credentials modal
  const closeGCPBtn = document.getElementById('close-gcp-modal-btn');
  if (closeGCPBtn) {
    closeGCPBtn.addEventListener('click', () => closeGCPCredsModal());
  }

  // Copy to clipboard buttons for Azure
  document.querySelectorAll('.copy-azure-login').forEach(btn => {
    btn.addEventListener('click', () => copyToClipboard('azure-login-cmd'));
  });
  document.querySelectorAll('.copy-azure-sp').forEach(btn => {
    btn.addEventListener('click', () => copyToClipboard('azure-sp-cmd'));
  });
  document.querySelectorAll('.copy-azure-cli').forEach(btn => {
    btn.addEventListener('click', () => copyToClipboard('azure-cli-cmd'));
  });

  // Copy to clipboard buttons for GCP
  document.querySelectorAll('.copy-gcp-login').forEach(btn => {
    btn.addEventListener('click', () => copyToClipboard('gcp-login-cmd'));
  });
  document.querySelectorAll('.copy-gcp-sa-create').forEach(btn => {
    btn.addEventListener('click', () => copyToClipboard('gcp-sa-create-cmd'));
  });
  document.querySelectorAll('.copy-gcp-role').forEach(btn => {
    btn.addEventListener('click', () => copyToClipboard('gcp-role-cmd'));
  });
  document.querySelectorAll('.copy-gcp-key').forEach(btn => {
    btn.addEventListener('click', () => copyToClipboard('gcp-key-cmd'));
  });
  document.querySelectorAll('.copy-gcp-cli').forEach(btn => {
    btn.addEventListener('click', () => copyToClipboard('gcp-cli-cmd'));
  });

  // History button
  const loadHistoryBtn = document.getElementById('load-history-btn');
  if (loadHistoryBtn) {
    loadHistoryBtn.addEventListener('click', () => void loadHistory());
  }

  // Logout button (Note: already has handler in auth.ts updateUserUI, but keeping for safety)
  const logoutBtn = document.getElementById('logout-btn');
  if (logoutBtn) {
    // Remove any existing listeners first to avoid duplicates
    const newLogoutBtn = logoutBtn.cloneNode(true) as HTMLButtonElement;
    logoutBtn.parentNode?.replaceChild(newLogoutBtn, logoutBtn);
    newLogoutBtn.addEventListener('click', () => void logout());
  }
}

/**
 * Setup feedback mailto link with template
 */
function setupFeedbackLink(): void {
  const feedbackLink = document.getElementById('feedback-link') as HTMLAnchorElement;
  if (!feedbackLink) return;

  const feedbackEmail = 'contact@leanercloud.com';
  const subject = 'CUDly Feedback';

  const body = `Hi CUDly Team,

I'd like to share some feedback about CUDly:


## Feedback Type

[ ] Bug Report
[ ] Feature Request
[ ] General Feedback
[ ] Question


## Description

Please describe your feedback in detail:



## Steps to Reproduce (for bugs)

1.
2.
3.


## Expected vs Actual Behavior (for bugs)

Expected:
Actual:


## Screenshots

Please attach any relevant screenshots to this email.


## Environment

- Browser: ${navigator.userAgent}
- URL: ${window.location.href}
- Date: ${new Date().toISOString()}


---
Thank you for helping us improve CUDly!
`;

  const mailtoUrl = `mailto:${feedbackEmail}?subject=${encodeURIComponent(subject)}&body=${encodeURIComponent(body)}`;
  feedbackLink.href = mailtoUrl;
}
