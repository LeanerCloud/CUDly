/**
 * CUDly - Application initialization and event setup
 */

import * as api from './api';
import * as state from './state';
import { showLoginModal, showAdminSetupModal, showResetPasswordModal, updateUserUI } from './auth';
import { loadDashboard, setupDashboardHandlers } from './dashboard';
import { setupRecommendationsHandlers, getPurchaseModalRecommendations, clearPurchaseModalRecommendations, getFanOutBuckets, clearFanOutBuckets, getExecuteMode, clearExecuteMode, type FanOutBucket } from './recommendations';
import { switchTab, applyTabFromPath, initRouter, switchSettingsSubTab, getSettingsSubTabFromPath } from './navigation';
import { savePlan, setupPlanHandlers, closePlanModal, openNewPlanModal, closePurchaseModal } from './plans';
import { saveGlobalSettings, setupSettingsHandlers, resetSettings } from './settings';
import { setupUserHandlers } from './users';
import { initApiKeys } from './apikeys';
import { loadHistory, setupHistoryHandlers } from './history';
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

    // Fetch the effective permission set from /api/auth/me/permissions
    // (issue #917) and merge it onto the user state so canAccess() can
    // consult the real group-derived set instead of blocking non-admins.
    // Best-effort: a failure here must not prevent the app from loading.
    try {
      const permsResp = await api.getUserPermissions();
      const updatedUser = state.getCurrentUser();
      if (updatedUser) {
        state.setCurrentUser({ ...updatedUser, effectivePermissions: permsResp.permissions });
      }
    } catch (permErr) {
      console.warn('Failed to fetch effective permissions, using fallback gating:', permErr);
    }

    initRouter();
    // Deep-link check BEFORE tab routing: the path /purchases/{approve,
    // cancel}/:id?token=… isn't a tab — it's a one-shot action landing
    // page from the approval email. handlePurchaseDeeplink runs the
    // confirm+POST flow, replaces the URL with /purchases, then falls
    // through so the user lands on the Purchases tab with their action's
    // outcome rendered as a toast.
    await handlePurchaseDeeplink();
    // Archera education deep-links (/archera-insurance, /archera-insurance/
    // how-it-works) open the overlay panel on top of the dashboard. Normal
    // tab routing still runs underneath so the app is fully functional.
    handleArcheraDeeplink();
    const target = applyTabFromPath();
    let url = '/' + target;
    if (target === 'admin') {
      url = '/admin/' + getSettingsSubTabFromPath();
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
  // Tab switching. The sidebar nav items are anchor tags (issue #462)
  // so the browser's "Open Link in New Tab" affordance works. For
  // un-modified left clicks we preventDefault and route via the SPA;
  // middle-clicks, Ctrl/Cmd/Shift-clicks fall through so the browser
  // can handle "open in new tab/window" natively.
  document.querySelectorAll<HTMLElement>('.tab-btn').forEach(btn => {
    btn.addEventListener('click', (e) => {
      const mouseEvent = e as MouseEvent;
      // Bail out on modifier-key or middle-click so the browser can
      // do its default new-tab/new-window behaviour for anchor elements.
      if (
        mouseEvent.button !== 0 ||
        mouseEvent.metaKey ||
        mouseEvent.ctrlKey ||
        mouseEvent.shiftKey ||
        mouseEvent.altKey
      ) {
        return;
      }
      const tab = btn.dataset['tab'];
      if (tab) {
        e.preventDefault();
        switchTab(tab);
      }
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

  // Wire provider/account topbar filter chips to Purchase History +
  // Approval Queue (issue #701). initSavingsHistory above subscribes
  // the chart; setupHistoryHandlers subscribes the two list consumers
  // so all three reload together when a chip changes.
  setupHistoryHandlers();

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
  // Recommendations buttons — there is no visible Refresh affordance.
  // The previous filter-bar Refresh button was replaced by the freshness
  // indicator's Refresh, which was itself removed (#284 follow-up):
  // triggerAutoRefreshIfStale in recommendations.ts now fires on every
  // load if the cache is older than 24h, surfacing collection errors
  // via toast. Users who need a manual refresh can reload the page;
  // operators should expose a more-explicit affordance if support
  // demand surfaces it.
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

  // Read the execute mode set by the modal toggle (issue #289).
  // "direct" means the session has execute-any/execute-own and chose to
  // bypass approval; "" is the default approval-required path.
  const executeMode = getExecuteMode();
  const isDirect = executeMode === 'direct';

  // Disable the button BEFORE awaiting the confirm dialog and the network
  // call so a double-click or rapid re-click can't fire a second POST and
  // mint a duplicate pending execution (#644). The button is re-enabled on
  // cancel below and in the finally block once the request settles.
  const executeBtn = document.getElementById('execute-purchase-btn') as HTMLButtonElement | null;
  if (executeBtn) {
    executeBtn.disabled = true;
    executeBtn.textContent = 'Sending...';
  }

  const defaultBtnLabel = isDirect ? 'Execute Purchase Now' : 'Send for Approval';

  // Confirmation dialog varies by mode:
  //   - Approval path: low-friction, non-destructive.
  //   - Direct-execute path: red destructive dialog with cost callout and
  //     cancellation-window reminder (issue #289 acceptance criteria).
  const ok = isDirect
    ? await confirmDialog({
        title: `Execute ${localRecs.length} purchase${localRecs.length === 1 ? '' : 's'} now?`,
        body: 'This will charge the full upfront amount immediately. This bypasses the approval step. AWS allows cancellation within 24 hours via the Account & Billing console.',
        confirmLabel: 'Execute Purchase Now',
        destructive: true,
      })
    : await confirmDialog({
        title: `Send ${localRecs.length} purchase${localRecs.length === 1 ? '' : 's'} for approval?`,
        body: 'This will email an approval request to the configured approver. Cloud commitments are charged only after the approver clicks the link in that email.',
        confirmLabel: 'Send for approval',
        destructive: false,
      });

  if (!ok) {
    if (executeBtn) {
      executeBtn.disabled = false;
      executeBtn.textContent = defaultBtnLabel;
    }
    return;
  }

  // Build the POST body recs by spreading the server-provided rec so that
  // all fields (including `details`, `engine`, `cloud_account_id`, and any
  // future additions) flow through unchanged. Only `payment`, `selected`,
  // and `purchased` are overridden: `payment` uses the user-edited value
  // (issue #111), and `selected`/`purchased` are forced to the canonical
  // purchase-intent values. The `?? 'all-upfront'` on payment is defensive
  // only — direct test-harness callers that bypass the modal may not set
  // `payment`; the production path always does. Passing `details` ensures
  // Windows EC2, dedicated-tenancy, AZ-scoped RIs, and non-default-engine
  // RDS/Cache recs reach the backend with the correct ServiceDetails payload
  // instead of falling back to Linux/regional/default (issue #597).
  const apiRecs: api.Recommendation[] = localRecs.map((r) => ({
    ...r,
    monthly_cost: r.monthly_cost ?? null,
    payment: r.payment ?? 'all-upfront',
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

  try {
    const result = await api.executePurchase(apiRecs, capacityPercent, executeMode || undefined);
    closePurchaseModal();
    clearPurchaseModalRecommendations();
    clearExecuteMode();

    if (isDirect) {
      // Direct-execute: purchase is already committed; inform the user.
      showToast({
        message: `Purchase executed immediately (id ${result.execution_id.slice(0, 8)}). Check Purchase History for the result.`,
        kind: 'success',
        timeout: 15_000,
      });
    } else if (result.email_sent === false) {
      // The backend now surfaces email-send status so the toast can be honest
      // about what the user should do next. When email_sent is undefined we
      // fall back to the old "check your email" message for backward compat
      // with any pre-deploy caller that hasn't picked up the new field yet.
      const reason = result.email_reason || 'reason unavailable';
      showToast({
        message: `Purchase queued as pending (id ${result.execution_id.slice(0, 8)}) but the approval email did not send: ${reason}. Approve or cancel it from the Purchase History tab.`,
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
          : 'Purchase submitted - check your email to approve.',
        kind: 'success',
        timeout: 10_000,
      });
    }
    // Offer Archera Insurance immediately after the user approves the
    // pre-purchase confirmation and the approval-submission call succeeds
    // (issue #499 follow-up). Firing here, rather than after the async
    // email-link execution completes, surfaces the optional coverage at
    // the moment the user has committed their intent. Gated on the
    // executePurchase success path so a failed submission shows no offer.
    openArcheraOfferModal('purchase');
    await loadDashboard();
  } catch (error) {
    const err = error as Error;
    const verb = isDirect ? 'execute' : 'send for approval';
    showToast({ message: `Failed to ${verb} purchase: ${err.message}`, kind: 'error' });
  } finally {
    if (executeBtn) {
      executeBtn.disabled = false;
      executeBtn.textContent = defaultBtnLabel;
    }
  }
}

/**
 * fanOutBucketLabel renders a short human identifier for a fan-out bucket so a
 * partial-failure toast can name which buckets created pending executions
 * (issue #642). Uses the (provider/service @ capacity%) tuple — the same
 * fields the user chose in the modal — so the orphaned-but-actionable pending
 * requests are identifiable rather than hidden behind a bare count.
 */
function fanOutBucketLabel(b: FanOutBucket): string {
  const provider = (b.provider || '').toString().toUpperCase();
  const service = b.service || 'commitment';
  const cap = Number.isFinite(b.capacityPercent) ? `@${b.capacityPercent}%` : '';
  return `${provider} ${service}${cap}`.trim();
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
  // Disable the button BEFORE the confirm dialog and the parallel POSTs so a
  // double-click can't fan out a second wave of duplicate executions (#644).
  // Re-enabled on cancel below and after the calls settle at the end.
  const executeBtn = document.getElementById('execute-purchase-btn') as HTMLButtonElement | null;
  if (executeBtn) {
    executeBtn.disabled = true;
    executeBtn.textContent = `Sending 0/${buckets.length}…`;
  }

  // Same approval-required default as the single-purchase path: each
  // bucket POSTs a request that triggers an approval email; the actual
  // charges fire when each approver clicks the link in their email.
  const ok = await confirmDialog({
    title: `Send ${buckets.length} bulk purchase${buckets.length === 1 ? '' : 's'} for approval?`,
    body: `This will submit ${buckets.length} separate purchase request${buckets.length === 1 ? '' : 's'} and email ${buckets.length} approval request${buckets.length === 1 ? '' : 's'}. Each must be approved individually before its commitments are charged.`,
    confirmLabel: 'Send all for approval',
    destructive: false,
  });
  if (!ok) {
    if (executeBtn) {
      executeBtn.disabled = false;
      executeBtn.textContent = 'Send for Approval';
    }
    return;
  }

  // Fire all POSTs in parallel via allSettled so one failure doesn't
  // cascade. Each bucket's recs are already scaled by its capacity %;
  // the POST body records capacity_percent for audit. Spread the full
  // server-provided rec so `details`, `engine`, `cloud_account_id`, and
  // any future additions flow through unchanged. Only `payment`,
  // `monthly_cost`, `selected`, and `purchased` are overridden: `payment`
  // comes from the bucket (user's per-bucket choice), `monthly_cost` is
  // coerced to null for absent values, and the purchase-intent flags are
  // forced to their canonical values. Passing `details` ensures
  // non-default platforms (Windows EC2, dedicated tenancy, AZ-scoped RIs,
  // non-default-engine RDS/Cache) reach the backend correctly (issue #597).
  const promises = buckets.map((b) =>
    api.executePurchase(
      b.recs.map((r) => ({
        ...r,
        monthly_cost: r.monthly_cost ?? null,
        payment: b.payment,
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
    // #642: on a partial fan-out failure the succeeded buckets created real
    // pending executions an approver can still act on. Name them so the user
    // knows which requests are live (and which to re-submit) rather than
    // leaving them as silent orphans behind a bare count.
    let submittedNote = '';
    if (succeeded > 0 && succeeded < results.length) {
      const submittedLabels: string[] = [];
      results.forEach((r, i) => {
        const ok =
          r.status === 'fulfilled' &&
          r.value.email_sent !== false &&
          r.value.status !== 'failed';
        if (ok && buckets[i]) submittedLabels.push(fanOutBucketLabel(buckets[i]));
      });
      if (submittedLabels.length > 0) {
        submittedNote = ` Submitted (awaiting approval): ${submittedLabels.slice(0, 5).join(', ')}${
          submittedLabels.length > 5 ? ` +${submittedLabels.length - 5} more` : ''
        }.`;
      }
    }
    showToast({
      message: `${succeeded} of ${results.length} submitted · ${failed} failed: ${failureMsgs}${failed > 3 ? ' (…)' : ''}${submittedNote}`,
      kind: failed === results.length ? 'error' : 'warning',
      timeout: null,
    });
  }
  // Offer Archera Insurance when at least one bucket's approval submission
  // succeeded (issue #499 follow-up). Fires right after the user approves
  // the pre-purchase confirmation and the calls resolve, not after the
  // async email-link execution completes. Skipping the all-fail path keeps
  // the modal from layering on top of an error toast for a user who has
  // nothing to insure yet.
  if (succeeded > 0) {
    openArcheraOfferModal('purchase');
  }

  await loadDashboard();

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
