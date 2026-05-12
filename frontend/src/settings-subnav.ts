/**
 * Settings/Admin dirty-state indicator helper.
 *
 * Originally this file also rendered a sticky in-panel sub-section rail
 * (Global Defaults / AWS / Azure / GCP / Exchange Automation on Purchasing
 * policies, Federation Setup / Registrations / per-cloud Accounts on
 * Accounts & onboarding, Users / Groups / Permission Overview / API Keys
 * on Users, roles & API keys). The rail used `margin-left: -220px` on
 * wide viewports to park itself in the page gutter, which collided with
 * the new action-center left sidebar after issue #340 — the rail rendered
 * on top of the primary navigation items.
 *
 * The rail was deleted entirely (issue #340 follow-up). The remaining
 * surface — the dirty-state indicator that shows "Unsaved changes" on
 * the Admin sub-tab button + sticky save-bar — stays here because it's
 * still wired from settings.ts.
 */

/**
 * Update the "Unsaved changes" sticky footer + dirty dot on the Admin
 * top-tab. Called whenever the settings form's dirty state changes.
 *
 * The RI Exchange fieldset renders its own `<form>` with its own
 * `.settings-buttons` + Save Settings button; that one is *not*
 * panel-level and must not stick to the viewport bottom — two sticky
 * save-bars stack awkwardly, and the user asked for Save + Reset in a
 * single bar. Filter nested ones out here.
 */
export function reflectDirtyState(dirty: boolean): void {
  const tabBtn = document.getElementById('admin-tab-btn');
  if (tabBtn) tabBtn.classList.toggle('has-unsaved', dirty);
  document.querySelectorAll('.settings-buttons').forEach((el) => {
    if (el.closest('#ri-exchange-automation-settings')) return;
    el.classList.toggle('settings-savebar', true);
    el.classList.toggle('dirty', dirty);
  });
}
