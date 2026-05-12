/**
 * CUDly - Application initialization and event setup
 */

import * as api from './api';
import * as state from './state';
import { showLoginModal, showAdminSetupModal, showResetPasswordModal, updateUserUI } from './auth';
import { loadDashboard, setupDashboardHandlers } from './dashboard';
import { setupRecommendationsHandlers, getPurchaseModalRecommendations, clearPurchaseModalRecommendations, getFanOutBuckets, clearFanOutBuckets, type FanOutBucket } from './recommendations';
import { switchTab, applyTabFromPath, initRouter, switchSettingsSubTab, getSettingsSubTabFromPath } from './navigation';
import { savePlan, setupPlanHandlers, closePlanModal, openNewPlanModal, closePurchaseModal } from './plans';
import { saveGlobalSettings, setupSettingsHandlers, resetSettings } from './settings';
import { setupUserHandlers } from './users';
import { initApiKeys } from './apikeys';
import { loadHistory } from './history';
import { initSavingsHistory } from './modules/savings-history';
import { setupRIExchangeHandlers, saveAutomationSettings } from './riexchange';
import { showToast } from './toast';
import { confirmDialog } from './confirmDialog';
import { handlePurchaseDeeplink } from './purchases-deeplink';
import { handleArcheraDeeplink, openArcheraOfferModal } from './archera';
import { closeModal } from './modal';

/**
 * Initialize app
 */
export async function init(): Promise<void> {
  api.initAuth();

  // Check if this is a password reset link. Must be scoped to the
  // /reset-password path specifically — other deep-links (e.g. the
  // purchase approve/cancel flow at /purchases/{action}/:id?token=…)
  // also carry a `token` query param and would otherwise hijack into
  // the reset-password modal. The backend emits exactly
  // `${dashboardURL}/reset-password?token=…` in
  // internal/auth/service_password.go:276, so pinning the check to
  // that path matches the issuer contract.
  const urlParams = new URLSearchParams(window.location.search);
  const resetToken = urlParams.get('token');
  if (resetToken && window.location.pathname.replace(/\/+$/, '') === '/reset-password') {
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
    // Deep-link check BEFORE tab routing: the path /purchases/{approve,
    // cancel}/:id?token=… isn't a tab — it's a one-shot action landing
    // page from the approval email. handlePurchaseDeeplink runs the
    // confirm+POST flow, replaces the URL with /history, then falls
    // through so the user lands on the History tab with their action's
    // outcome rendered as a toast.
    await handlePurchaseDeeplink();
    // Archera education deep-links (/archera-insurance, /archera-insurance/
    // how-it-works) open the overlay panel on top of the dashboard. Normal
    // tab routing still runs underneath so the app is fully functional.
    handleArcheraDeeplink();
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
  //
  // Bundle B note (column-filter UX overhaul): #create-plan-btn was relocated
  // from the old top filter bar into the sticky bottom action box. The
  // button is now created and wired by recommendations.ts:mountBottomActionBox,
  // which calls openCreatePlanModal directly. The wiring used to live here.

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
      clearFanOutBuckets();
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
      if (modal) closeModal(modal);
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

  // Purchasing save button — mirrors the General tab's Save Settings by
  // dispatching submit on the shared #global-settings-form (which reads
  // both General-tab and Purchasing-tab fields from across the DOM) and
  // *also* saves the RI Exchange Automation settings when that form is
  // loaded. Keeping both saves behind a single button avoids the prior
  // duplicate "Save Settings" in the panel: the scrolling one inside the
  // RI Exchange form was removed and its persistence rolled into this
  // sticky bar. If the RI Exchange form hasn't rendered yet (panel never
  // visited), the guard inside saveAutomationSettings no-ops cleanly.
  const savePurchasingBtn = document.getElementById('save-purchasing-btn');
  if (savePurchasingBtn) {
    savePurchasingBtn.addEventListener('click', () => {
      const form = document.getElementById('global-settings-form') as HTMLFormElement | null;
      if (form) form.dispatchEvent(new Event('submit', { cancelable: true, bubbles: true }));
      if (document.getElementById('ri-exchange-settings-form')) {
        void saveAutomationSettings();
      }
    });
  }

  // History button
  const loadHistoryBtn = document.getElementById('load-history-btn');
  if (loadHistoryBtn) {
    loadHistoryBtn.addEventListener('click', () => void loadHistory());
  }

}

/**
 * Handle execute purchase button click. Routes to the single-bucket
 * path when getPurchaseModalRecommendations has content, or to the
 * multi-bucket fan-out path when the fan-out modal set buckets.
 */
async function handleExecutePurchase(): Promise<void> {
  const fanOutBuckets = getFanOutBuckets();
  if (fanOutBuckets && fanOutBuckets.length > 0) {
    await handleFanOutExecute(fanOutBuckets);
    return;
  }

  const localRecs = getPurchaseModalRecommendations();
  if (localRecs.length === 0) {
    showToast({ message: 'No recommendations selected for purchase.', kind: 'warning' });
    return;
  }

  // Default approval-required path: clicking sends an approval request to
  // the configured approver(s) — it does NOT spend money. The actual
  // upfront charge fires only after an approver clicks the email link.
  // Issue #289 will introduce a session-permission branch where holders
  // of `execute-any:purchases` can opt into direct execution; until that
  // lands, every user is on this approval path.
  const ok = await confirmDialog({
    title: `Send ${localRecs.length} purchase${localRecs.length === 1 ? '' : 's'} for approval?`,
    body: 'This will email an approval request to the configured approver. Cloud commitments are charged only after the approver clicks the link in that email.',
    confirmLabel: 'Send for approval',
    destructive: false,
  });
  if (!ok) return;

  // Map LocalRecommendation to API Recommendation format. The counts +
  // costs here are already scaled by the bulk-toolbar's capacity % so
  // the backend records exactly what the user saw in the preview.
  // Issue #111 (iii): payment now reads `r.payment` (set by
  // `openPurchaseModal`'s per-row seed and live edits), replacing the
  // historical hardcoded 'all-upfront' that silently dropped the
  // toolbar's Payment for the single-bucket path. The `?? 'all-upfront'`
  // is defensive only — direct test-harness callers that bypass the
  // modal may not set `payment`; the production path always does.
  const apiRecs: api.Recommendation[] = localRecs.map((r) => ({
    id: r.id,
    provider: r.provider,
    service: r.service,
    region: r.region,
    resource_type: r.resource_type,
    count: r.count,
    term: r.term,
    payment: r.payment ?? 'all-upfront',
    upfront_cost: r.upfront_cost,
    monthly_cost: r.monthly_cost ?? null,
    savings: r.savings,
    selected: true,
    purchased: false,
  }));

  // Read the sticky toolbar Capacity % so the execution record carries
  // the user's intent in audit logs. Graceful fallback to 100 if the
  // toolbar hasn't rendered yet (e.g. direct test harness call).
  const capacityInput = document.getElementById('bulk-purchase-capacity') as HTMLInputElement | null;
  const capacityPercent = capacityInput
    ? Math.max(1, Math.min(100, parseInt(capacityInput.value, 10) || 100))
    : 100;

  const executeBtn = document.getElementById('execute-purchase-btn') as HTMLButtonElement | null;
  if (executeBtn) {
    executeBtn.disabled = true;
    executeBtn.textContent = 'Sending...';
  }

  try {
    const result = await api.executePurchase(apiRecs, capacityPercent);
    closePurchaseModal();
    clearPurchaseModalRecommendations();

    // The backend now surfaces email-send status so the toast can be honest
    // about what the user should do next. When email_sent is undefined we
    // fall back to the old "check your email" message for backward compat
    // with any pre-deploy caller that hasn't picked up the new field yet.
    if (result.email_sent === false) {
      const reason = result.email_reason || 'reason unavailable';
      showToast({
        message: `Purchase queued as pending (id ${result.execution_id.slice(0, 8)}…) but the approval email did not send: ${reason}. Approve or cancel it from the Purchase History tab.`,
        kind: 'warning',
        timeout: null,
      });
    } else {
      // Name the approver in the success toast so the user can confirm WHO
      // received the request (CR pass on PR #294 / issue #288). The backend
      // surfaces `approval_recipient` from `resolveApprovalRecipients`; the
      // field may be absent on older deploys or when only the global notify
      // mailbox is configured — fall back to the generic line in that case.
      const recipient = result.approval_recipient;
      showToast({
        message: recipient
          ? `Approval request sent to ${recipient}.`
          : 'Purchase submitted — check your email to approve.',
        kind: 'success',
        timeout: 10_000,
      });
    }
    await loadDashboard();
    // Offer Archera Insurance after a successful approval submission.
    // The actual cloud commitment isn't charged until the approver clicks
    // the email link, but the user has now committed their intent — this
    // is the natural moment to surface optional insurance coverage.
    // Only fires on the success path; on email_sent=false we still opened
    // a pending execution so the offer is still relevant.
    openArcheraOfferModal('purchase');
  } catch (error) {
    const err = error as Error;
    showToast({ message: `Failed to send purchase for approval: ${err.message}`, kind: 'error' });
  } finally {
    if (executeBtn) {
      executeBtn.disabled = false;
      executeBtn.textContent = 'Send for Approval';
    }
  }
}

/**
 * handleFanOutExecute submits one executePurchase POST per bucket.
 *
 * The backend API is a per-execution endpoint — a multi-bucket purchase
 * is N independent submissions, each with its own approval email. The
 * user already saw "Will send N approval emails" in the modal, so no
 * further confirmation is required here beyond the standard destructive
 * confirmDialog.
 */
async function handleFanOutExecute(buckets: FanOutBucket[]): Promise<void> {
  // Same approval-required default as the single-purchase path: each
  // bucket POSTs a request that triggers an approval email; the actual
  // charges fire when each approver clicks the link in their email.
  const ok = await confirmDialog({
    title: `Send ${buckets.length} bulk purchase${buckets.length === 1 ? '' : 's'} for approval?`,
    body: `This will submit ${buckets.length} separate purchase request${buckets.length === 1 ? '' : 's'} and email ${buckets.length} approval request${buckets.length === 1 ? '' : 's'}. Each must be approved individually before its commitments are charged.`,
    confirmLabel: 'Send all for approval',
    destructive: false,
  });
  if (!ok) return;

  const executeBtn = document.getElementById('execute-purchase-btn') as HTMLButtonElement | null;
  if (executeBtn) {
    executeBtn.disabled = true;
    executeBtn.textContent = `Sending 0/${buckets.length}…`;
  }

  // Fire all POSTs in parallel via allSettled so one failure doesn't
  // cascade. Each bucket's recs are already scaled by its capacity %;
  // the POST body records capacity_percent for audit.
  const promises = buckets.map((b) =>
    api.executePurchase(
      b.recs.map((r) => ({
        id: r.id,
        provider: r.provider,
        service: r.service,
        region: r.region,
        resource_type: r.resource_type,
        count: r.count,
        term: r.term,
        payment: b.payment,
        upfront_cost: r.upfront_cost,
        monthly_cost: r.monthly_cost ?? null,
        savings: r.savings,
        selected: true,
        purchased: false,
      })),
      b.capacityPercent,
    ),
  );
  const results = await Promise.allSettled(promises);

  // Reclassify business-level email failures: a fulfilled POST that returns
  // email_sent === false or status === 'failed' is not a true success —
  // the approval email never went out (CR pass on PR #294 Finding 2).
  const fulfilled = results.filter(
    (r): r is PromiseFulfilledResult<api.PurchaseResult> => r.status === 'fulfilled',
  );
  const submissionFailures = fulfilled.filter(
    (r) => r.value.email_sent === false || r.value.status === 'failed',
  );
  const succeeded = fulfilled.length - submissionFailures.length;
  const failed = results.length - succeeded;
  closePurchaseModal();
  clearFanOutBuckets();
  clearPurchaseModalRecommendations();

  if (failed === 0) {
    // Collect the unique approval-recipient set from truly-succeeded responses
    // only (email_sent !== false and status !== 'failed') so the toast doesn't
    // name a recipient whose email never arrived. Multi-bucket purchases can
    // route to different approvers; dedupe so the toast is compact.
    const recipients = new Set<string>();
    for (const r of fulfilled) {
      if (
        r.value.email_sent !== false &&
        r.value.status !== 'failed' &&
        r.value.approval_recipient
      ) {
        recipients.add(r.value.approval_recipient);
      }
    }
    const noun = succeeded === 1 ? 'purchase' : 'purchases';
    let message: string;
    if (recipients.size === 0) {
      message = `${succeeded} ${noun} submitted — check your email to approve each.`;
    } else if (recipients.size === 1) {
      message = `${succeeded} ${noun} sent for approval to ${[...recipients][0]}.`;
    } else {
      message = `${succeeded} ${noun} sent for approval to ${recipients.size} recipients (${[...recipients].sort().join(', ')}).`;
    }
    showToast({
      message,
      kind: 'success',
      timeout: 15_000,
    });
  } else {
    const failureMsgs = [
      ...results
        .filter((r): r is PromiseRejectedResult => r.status === 'rejected')
        .map((r) => (r.reason instanceof Error ? r.reason.message : String(r.reason))),
      ...submissionFailures.map((r) => r.value.email_reason || 'approval email did not send'),
    ]
      .slice(0, 3)
      .join('; ');
    showToast({
      message: `${succeeded} of ${results.length} submitted · ${failed} failed: ${failureMsgs}${failed > 3 ? ' (…)' : ''}`,
      kind: failed === results.length ? 'error' : 'warning',
      timeout: null,
    });
  }
  await loadDashboard();

  // Offer Archera Insurance when at least one bucket succeeded. Skipping
  // the offer on the all-fail path keeps the modal from layering on top
  // of an error toast for a user who has nothing to insure yet.
  if (succeeded > 0) {
    openArcheraOfferModal('purchase');
  }

  if (executeBtn) {
    executeBtn.disabled = false;
    executeBtn.textContent = 'Send for Approval';
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
