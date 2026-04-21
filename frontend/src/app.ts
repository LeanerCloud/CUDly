/**
 * CUDly - Application initialization and event setup
 */

import * as api from './api';
import * as state from './state';
import { showLoginModal, showAdminSetupModal, showResetPasswordModal, updateUserUI } from './auth';
import { loadDashboard, setupDashboardHandlers } from './dashboard';
import { setupRecommendationsHandlers, getPurchaseModalRecommendations, clearPurchaseModalRecommendations } from './recommendations';
import { switchTab, applyTabFromPath, initRouter, switchSettingsSubTab, getSettingsSubTabFromPath } from './navigation';
import { savePlan, setupPlanHandlers, closePlanModal, openCreatePlanModal, openNewPlanModal, closePurchaseModal } from './plans';
import { saveGlobalSettings, setupSettingsHandlers, resetSettings } from './settings';
import { setupUserHandlers } from './users';
import { initApiKeys } from './apikeys';
import { loadHistory } from './history';
import { initSavingsHistory } from './modules/savings-history';
import { setupRIExchangeHandlers } from './riexchange';

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
    try {
      const publicInfo = await api.getPublicInfo();
      if (!publicInfo.admin_exists) {
        await showAdminSetupModal(publicInfo.api_key_secret_url);
        return;
      }
    } catch {
      // If public info fails, fall through to login
    }
    await showLoginModal();
    return;
  }

  try {
    const user = await api.getCurrentUser();
    state.setCurrentUser(user);
    initRouter();
    const target = applyTabFromPath();
    let url = '/' + target;
    if (target === 'settings') {
      url = '/settings/' + getSettingsSubTabFromPath();
    }
    window.history.replaceState(
      { tab: target, id: 0 },
      '',
      url + window.location.search + window.location.hash,
    );
    switchTab(target, { push: false });
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

  // Settings sub-tab switching
  document.querySelectorAll<HTMLButtonElement>('.sub-tab-btn').forEach(btn => {
    btn.addEventListener('click', () => {
      const subTab = btn.dataset['settingsTab'];
      if (subTab) switchSettingsSubTab(subTab);
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

  // Setup RI Exchange event handlers
  setupRIExchangeHandlers();

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
  // Recommendations buttons — the sole Refresh affordance now lives in
  // the freshness indicator (`recommendations-freshness`), rendered and
  // wired by renderFreshness in `freshness.ts`. The older filter-bar
  // Refresh button was removed because it duplicated the freshness-
  // indicator Refresh with strictly worse UX (alert() popup + 5s delay
  // vs. the freshness button's inline disable + re-render).
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
    closePurchaseBtn.addEventListener('click', () => {
      closePurchaseModal();
      clearPurchaseModalRecommendations();
    });
  }

  const executePurchaseBtn = document.getElementById('execute-purchase-btn');
  if (executePurchaseBtn) {
    executePurchaseBtn.addEventListener('click', () => void handleExecutePurchase());
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
    });
  }

  // Settings buttons
  const resetSettingsBtn = document.getElementById('reset-settings-btn');
  if (resetSettingsBtn) {
    resetSettingsBtn.addEventListener('click', () => resetSettings());
  }

  // Purchasing reset button
  const resetPurchasingBtn = document.getElementById('reset-purchasing-btn');
  if (resetPurchasingBtn) {
    resetPurchasingBtn.addEventListener('click', () => resetSettings());
  }

  // History button
  const loadHistoryBtn = document.getElementById('load-history-btn');
  if (loadHistoryBtn) {
    loadHistoryBtn.addEventListener('click', () => void loadHistory());
  }

}

/**
 * Handle execute purchase button click
 */
async function handleExecutePurchase(): Promise<void> {
  const localRecs = getPurchaseModalRecommendations();
  if (localRecs.length === 0) {
    alert('No recommendations selected for purchase.');
    return;
  }

  if (!confirm(`Are you sure you want to execute ${localRecs.length} purchase(s)? This action will purchase cloud commitments.`)) {
    return;
  }

  // Map LocalRecommendation to API Recommendation format
  const apiRecs: api.Recommendation[] = localRecs.map((r, i) => ({
    id: `rec-${i}`,
    provider: r.provider,
    service: r.service,
    region: r.region,
    resource_type: r.resource_type,
    count: r.count,
    term: r.term,
    payment: 'all-upfront',
    upfront_cost: 0,
    monthly_cost: 0,
    savings: r.savings,
    selected: true,
    purchased: false,
  }));

  const executeBtn = document.getElementById('execute-purchase-btn') as HTMLButtonElement | null;
  if (executeBtn) {
    executeBtn.disabled = true;
    executeBtn.textContent = 'Executing...';
  }

  try {
    await api.executePurchase(apiRecs);
    closePurchaseModal();
    clearPurchaseModalRecommendations();

    alert('Purchase submitted — check your email to approve.');
    await loadDashboard();
  } catch (error) {
    const err = error as Error;
    alert(`Failed to execute purchase: ${err.message}`);
  } finally {
    if (executeBtn) {
      executeBtn.disabled = false;
      executeBtn.textContent = 'Execute Purchase';
    }
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
