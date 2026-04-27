/**
 * HTML structure validation tests
 * These tests verify that the HTML structure is correct and accessible
 */
import fs from 'fs';
import path from 'path';

// Read HTML content once at module load
const htmlPath = path.join(__dirname, '..', 'index.html');
const htmlContent = fs.readFileSync(htmlPath, 'utf8');

// Extract head and body content using regex
const headMatch = htmlContent.match(/<head[^>]*>([\s\S]*?)<\/head>/i);
const bodyMatch = htmlContent.match(/<body[^>]*>([\s\S]*?)<\/body>/i);
const headContent = headMatch?.[1] || '';
const bodyContent = bodyMatch?.[1] || '';

describe('HTML Structure', () => {
  beforeEach(() => {
    // Restore HTML content before each test (since afterEach in setup clears it)
    document.head.innerHTML = headContent;
    document.body.innerHTML = bodyContent;

    // Set lang attribute
    const langMatch = htmlContent.match(/<html[^>]+lang=["']([^"']+)["']/i);
    if (langMatch && langMatch[1]) {
      document.documentElement.setAttribute('lang', langMatch[1]);
    }
  });

  describe('Document Structure', () => {
    test('has DOCTYPE declaration', () => {
      expect(htmlContent).toMatch(/<!DOCTYPE html>/i);
    });

    test('has html lang attribute', () => {
      expect(htmlContent).toMatch(/<html[^>]+lang=["']en["']/);
    });

    test('has head element', () => {
      expect(document.head).toBeTruthy();
    });

    test('has body element', () => {
      expect(document.body).toBeTruthy();
    });

    test('has meta charset', () => {
      const charset = document.querySelector('meta[charset]');
      expect(charset).toBeTruthy();
      expect(charset?.getAttribute('charset')?.toLowerCase()).toBe('utf-8');
    });

    test('has viewport meta tag', () => {
      const viewport = document.querySelector('meta[name="viewport"]');
      expect(viewport).toBeTruthy();
      expect(viewport?.getAttribute('content')).toContain('width=device-width');
    });

    test('has title containing CUDly', () => {
      expect(htmlContent).toMatch(/<title>[^<]*CUDly[^<]*<\/title>/);
    });
  });

  describe('Main Layout', () => {
    test('has app container', () => {
      const app = document.getElementById('app');
      expect(app).toBeTruthy();
    });

    test('has header', () => {
      const header = document.querySelector('header');
      expect(header).toBeTruthy();
    });

    test('has main content area', () => {
      const main = document.querySelector('main');
      expect(main).toBeTruthy();
    });

    test('has tab navigation', () => {
      const tabs = document.querySelector('.tabs');
      expect(tabs).toBeTruthy();
    });
  });

  describe('Tab Structure', () => {
    test('has tab buttons', () => {
      const tabs = document.querySelectorAll('.tab-btn');
      expect(tabs.length).toBeGreaterThan(0);
    });

    test('has dashboard tab button', () => {
      const dashboardTab = document.querySelector('[data-tab="dashboard"]');
      expect(dashboardTab).toBeTruthy();
    });

    test('has recommendations tab button', () => {
      const recsTab = document.querySelector('[data-tab="recommendations"]');
      expect(recsTab).toBeTruthy();
    });

    test('has plans tab button', () => {
      const plansTab = document.querySelector('[data-tab="plans"]');
      expect(plansTab).toBeTruthy();
    });

    test('has history tab button', () => {
      const historyTab = document.querySelector('[data-tab="history"]');
      expect(historyTab).toBeTruthy();
    });

    test('has settings tab button', () => {
      const settingsTab = document.querySelector('[data-tab="settings"]');
      expect(settingsTab).toBeTruthy();
    });

    test('has tab content for each tab', () => {
      const tabContents = document.querySelectorAll('.tab-content');
      expect(tabContents.length).toBe(6);
    });
  });

  describe('Dashboard Tab', () => {
    test('has summary section', () => {
      const summary = document.getElementById('summary');
      expect(summary).toBeTruthy();
    });

    test('has savings chart section', () => {
      const chartSection = document.getElementById('savings-chart-section');
      expect(chartSection).toBeTruthy();
    });

    test('has savings chart canvas', () => {
      const canvas = document.getElementById('savings-chart');
      expect(canvas).toBeTruthy();
      expect(canvas?.tagName.toLowerCase()).toBe('canvas');
    });

    test('has upcoming purchases section', () => {
      const upcoming = document.getElementById('upcoming-purchases');
      expect(upcoming).toBeTruthy();
    });
  });

  describe('Recommendations Tab', () => {
    // Bundle B (column-filter UX overhaul) deleted the legacy top filter bar
    // and the #recommendations-controls section that hosted it. The
    // service-filter / region-filter / min-savings-filter / provider-filter /
    // account-filter <select>s are gone — replaced by per-column header-mounted
    // popovers driven by recommendations.ts:openColumnPopover. Negative-guard
    // tests below ensure no regression accidentally re-introduces them.
    test('legacy top filter bar is absent (Bundle B)', () => {
      const recsTab = document.getElementById('recommendations-tab');
      expect(recsTab).toBeTruthy();
      expect(document.getElementById('recommendations-controls')).toBeNull();
      // .controls-bar still exists on other tabs (Dashboard, Plans, …).
      // Only assert the rec tab is clean.
      expect(recsTab?.querySelector('.controls-bar')).toBeNull();
      expect(document.getElementById('service-filter')).toBeNull();
      expect(document.getElementById('region-filter')).toBeNull();
      expect(document.getElementById('min-savings-filter')).toBeNull();
      expect(document.getElementById('recommendations-account-filter')).toBeNull();
      expect(document.getElementById('recommendations-provider-filter')).toBeNull();
    });

    test('has recommendations list container', () => {
      const list = document.getElementById('recommendations-list');
      expect(list).toBeTruthy();
    });
  });

  describe('Plans Tab', () => {
    test('has plans header', () => {
      const header = document.getElementById('plans-header');
      expect(header).toBeTruthy();
    });

    test('has plans list container', () => {
      const list = document.getElementById('plans-list');
      expect(list).toBeTruthy();
    });
  });

  describe('History Tab', () => {
    test('has history controls', () => {
      const controls = document.getElementById('history-controls');
      expect(controls).toBeTruthy();
    });

    test('has date range inputs', () => {
      const startDate = document.getElementById('history-start') as HTMLInputElement | null;
      const endDate = document.getElementById('history-end') as HTMLInputElement | null;
      expect(startDate).toBeTruthy();
      expect(endDate).toBeTruthy();
      expect(startDate?.getAttribute('type')).toBe('date');
      expect(endDate?.getAttribute('type')).toBe('date');
    });

    test('has provider filter', () => {
      const filter = document.getElementById('history-provider-filter');
      expect(filter).toBeTruthy();
    });

    test('has history list container', () => {
      const list = document.getElementById('history-list');
      expect(list).toBeTruthy();
    });
  });

  describe('Settings Tab', () => {
    test('has settings section', () => {
      const section = document.getElementById('settings-section');
      expect(section).toBeTruthy();
    });

    test('has settings form', () => {
      const form = document.getElementById('global-settings-form');
      expect(form).toBeTruthy();
    });

    test('has provider checkboxes', () => {
      const awsCheck = document.getElementById('provider-aws');
      const azureCheck = document.getElementById('provider-azure');
      const gcpCheck = document.getElementById('provider-gcp');
      expect(awsCheck).toBeTruthy();
      expect(azureCheck).toBeTruthy();
      expect(gcpCheck).toBeTruthy();
    });

    test('has notification email input', () => {
      const email = document.getElementById('setting-notification-email') as HTMLInputElement | null;
      expect(email).toBeTruthy();
      expect(email?.getAttribute('type')).toBe('email');
    });

    test('has default term select', () => {
      const term = document.getElementById('setting-default-term');
      expect(term).toBeTruthy();
    });

    test('has default payment select', () => {
      const payment = document.getElementById('setting-default-payment');
      expect(payment).toBeTruthy();
    });
  });

  describe('Modals', () => {
    test('has plan modal', () => {
      const modal = document.getElementById('plan-modal');
      expect(modal).toBeTruthy();
      expect(modal?.classList.contains('hidden')).toBe(true);
    });

    test('has plan form', () => {
      const form = document.getElementById('plan-form');
      expect(form).toBeTruthy();
    });

    test('has purchase modal', () => {
      const modal = document.getElementById('purchase-modal');
      expect(modal).toBeTruthy();
    });

    test('has recommendations selection modal', () => {
      const modal = document.getElementById('select-recommendations-modal');
      expect(modal).toBeTruthy();
    });
  });

  describe('Form Elements', () => {
    test('plan name input exists', () => {
      const input = document.getElementById('plan-name') as HTMLInputElement | null;
      expect(input).toBeTruthy();
      expect(input?.hasAttribute('required')).toBe(true);
    });

    test('plan provider select exists', () => {
      const select = document.getElementById('plan-provider');
      expect(select).toBeTruthy();
      expect(select?.querySelectorAll('option').length).toBeGreaterThan(0);
    });

    test('plan service select exists', () => {
      const select = document.getElementById('plan-service');
      expect(select).toBeTruthy();
    });

    test('ramp schedule options exist', () => {
      const radios = document.querySelectorAll('input[name="ramp-schedule"]');
      expect(radios.length).toBe(4);
    });
  });

  describe('Accessibility', () => {
    test('buttons have text content', () => {
      const buttons = document.querySelectorAll('button');
      buttons.forEach(button => {
        const hasText = (button.textContent?.trim().length ?? 0) > 0;
        const hasAriaLabel = button.hasAttribute('aria-label');
        expect(hasText || hasAriaLabel).toBe(true);
      });
    });

    test('images have alt attributes', () => {
      const images = document.querySelectorAll('img');
      images.forEach(img => {
        expect(img.hasAttribute('alt')).toBe(true);
      });
    });

    test('links have href attributes', () => {
      const links = document.querySelectorAll('a');
      links.forEach(link => {
        const href = link.getAttribute('href');
        expect(href).toBeTruthy();
      });
    });
  });

  describe('Select Options', () => {
    test('dashboard provider filter has correct options', () => {
      const selector = document.getElementById('dashboard-provider-filter');
      const options = selector?.querySelectorAll('option') ?? [];
      const values = Array.from(options).map(o => o.value);

      expect(values).toContain('');
      expect(values).toContain('aws');
      expect(values).toContain('azure');
      expect(values).toContain('gcp');
    });

    // Bundle B: #service-filter <select> deleted; service filtering happens
    // via the per-column header popover. See tests/recommendations.test.ts
    // for the popover behaviour.

    test('term select has 1 and 3 year options', () => {
      const selector = document.getElementById('setting-default-term');
      const options = selector?.querySelectorAll('option') ?? [];
      const values = Array.from(options).map(o => o.value);

      expect(values).toContain('1');
      expect(values).toContain('3');
    });

    test('payment select has all options', () => {
      const selector = document.getElementById('setting-default-payment');
      const options = selector?.querySelectorAll('option') ?? [];
      const values = Array.from(options).map(o => o.value);

      expect(values).toContain('no-upfront');
      expect(values).toContain('partial-upfront');
      expect(values).toContain('all-upfront');
    });

    // Bundle B: legacy service-filter optgroups deleted along with the
    // <select id="service-filter">. The Service column-header popover lists
    // distinct service values from the loaded recs at runtime, no static
    // optgroup tree.

    // Refs #45 — admins land on the Accounts page primarily to action the
    // pending registration queue, so the filter must default to "pending".
    test('registrations status filter defaults to "pending"', () => {
      const selector = document.getElementById('registrations-status-filter') as HTMLSelectElement | null;
      expect(selector).toBeTruthy();
      // value reflects the initial `selected` option in static HTML.
      expect(selector?.value).toBe('pending');
      const selected = selector?.querySelector('option[selected]') as HTMLOptionElement | null;
      expect(selected?.value).toBe('pending');
    });
  });

  describe('Accounts Section Layout', () => {
    // Refs #45 — Account Registrations must render above all per-provider
    // account tables so pending registrations across providers stay visible
    // at the top of the Accounts page.
    test('registrations fieldset precedes per-provider account fieldsets in DOM order', () => {
      const registrations = document.getElementById('accounts-registrations');
      const awsBlock = document.getElementById('accounts-aws-block');
      const azureBlock = document.getElementById('accounts-azure-block');
      const gcpBlock = document.getElementById('accounts-gcp-block');

      expect(registrations).toBeTruthy();
      expect(awsBlock).toBeTruthy();
      expect(azureBlock).toBeTruthy();
      expect(gcpBlock).toBeTruthy();

      // DOCUMENT_POSITION_FOLLOWING (0x04) means the argument follows `node`.
      const FOLLOWING = Node.DOCUMENT_POSITION_FOLLOWING;
      expect(registrations!.compareDocumentPosition(awsBlock!) & FOLLOWING).toBeTruthy();
      expect(registrations!.compareDocumentPosition(azureBlock!) & FOLLOWING).toBeTruthy();
      expect(registrations!.compareDocumentPosition(gcpBlock!) & FOLLOWING).toBeTruthy();
    });
  });

  describe('Security Headers', () => {
    test('has Content Security Policy meta tag', () => {
      const csp = document.querySelector('meta[http-equiv="Content-Security-Policy"]');
      expect(csp).toBeTruthy();
      expect(csp?.getAttribute('content')).toContain("default-src 'self'");
    });

    // Regression guard for issue #8 — these three directives must NOT creep
    // back into the meta tag. frame-ancestors is header-only (browsers ignore
    // it in <meta>), and the two execute-api / lambda-url patterns are
    // invalid mid-hostname wildcards that browsers silently drop.
    test('meta CSP omits frame-ancestors and invalid wildcard hosts', () => {
      const csp = document.querySelector('meta[http-equiv="Content-Security-Policy"]');
      const content = csp?.getAttribute('content') ?? '';
      expect(content).not.toContain('frame-ancestors');
      expect(content).not.toContain('*.execute-api.*.amazonaws.com');
      expect(content).not.toContain('*.lambda-url.*.on.aws');
    });

    test('has referrer policy meta tag', () => {
      const referrer = document.querySelector('meta[name="referrer"]');
      expect(referrer).toBeTruthy();
      expect(referrer?.getAttribute('content')).toBe('strict-origin-when-cross-origin');
    });
  });

  describe('Admin-Only Sections', () => {
    test('has API keys section', () => {
      const section = document.getElementById('apikeys-section');
      expect(section).toBeTruthy();
      expect(section?.classList.contains('admin-only')).toBe(true);
    });

    test('has users management section', () => {
      const section = document.getElementById('users-section');
      expect(section).toBeTruthy();
      expect(section?.classList.contains('admin-only')).toBe(true);
    });

    test('has create user button', () => {
      const btn = document.getElementById('create-user-btn');
      expect(btn).toBeTruthy();
    });

    test('has create group button', () => {
      const btn = document.getElementById('create-group-btn');
      expect(btn).toBeTruthy();
    });

    test('has create API key button', () => {
      const btn = document.getElementById('create-apikey-btn');
      expect(btn).toBeTruthy();
    });
  });

  describe('User Management Modal', () => {
    test('has user modal', () => {
      const modal = document.getElementById('user-modal');
      expect(modal).toBeTruthy();
      expect(modal?.classList.contains('hidden')).toBe(true);
    });

    test('has user form with email field', () => {
      const email = document.getElementById('user-email') as HTMLInputElement | null;
      expect(email).toBeTruthy();
      expect(email?.getAttribute('type')).toBe('email');
      expect(email?.hasAttribute('required')).toBe(true);
    });

    test('has user role select', () => {
      const role = document.getElementById('user-role') as HTMLSelectElement | null;
      expect(role).toBeTruthy();
      const options = Array.from(role?.querySelectorAll('option') ?? []).map(o => o.value);
      expect(options).toContain('viewer');
      expect(options).toContain('editor');
      expect(options).toContain('admin');
    });

    test('has user groups multi-select', () => {
      const groups = document.getElementById('user-groups') as HTMLSelectElement | null;
      expect(groups).toBeTruthy();
      expect(groups?.hasAttribute('multiple')).toBe(true);
    });
  });

  describe('Group Management Modal', () => {
    test('has group modal', () => {
      const modal = document.getElementById('group-modal');
      expect(modal).toBeTruthy();
    });

    test('has group form with name field', () => {
      const name = document.getElementById('group-name') as HTMLInputElement | null;
      expect(name).toBeTruthy();
      expect(name?.hasAttribute('required')).toBe(true);
    });

    test('has group description textarea', () => {
      const desc = document.getElementById('group-description');
      expect(desc).toBeTruthy();
      expect(desc?.tagName.toLowerCase()).toBe('textarea');
    });

    test('has permissions list container', () => {
      const list = document.getElementById('permissions-list');
      expect(list).toBeTruthy();
    });
  });

  describe('API Key Modal', () => {
    test('has create API key modal', () => {
      const modal = document.getElementById('create-apikey-modal');
      expect(modal).toBeTruthy();
    });

    test('has API key name input', () => {
      const input = document.getElementById('apikey-name') as HTMLInputElement | null;
      expect(input).toBeTruthy();
      expect(input?.hasAttribute('required')).toBe(true);
    });

    test('has expiration checkbox', () => {
      const checkbox = document.getElementById('apikey-expires');
      expect(checkbox).toBeTruthy();
    });

    test('has expiration date field (hidden by default)', () => {
      const field = document.getElementById('apikey-expires-at-field');
      expect(field).toBeTruthy();
      expect(field?.classList.contains('hidden')).toBe(true);
    });
  });

  describe('Savings History Section', () => {
    test('has savings history section', () => {
      const section = document.getElementById('savings-history-section');
      expect(section).toBeTruthy();
    });

    test('has savings period selector', () => {
      const select = document.getElementById('savings-period') as HTMLSelectElement | null;
      expect(select).toBeTruthy();
      const options = Array.from(select?.querySelectorAll('option') ?? []).map(o => o.value);
      expect(options).toContain('24h');
      expect(options).toContain('7d');
      expect(options).toContain('30d');
      expect(options).toContain('90d');
    });

    test('has savings stats cards', () => {
      const statsGrid = document.getElementById('savings-stats');
      expect(statsGrid).toBeTruthy();
      const statCards = statsGrid?.querySelectorAll('.stat-card');
      expect(statCards?.length).toBeGreaterThanOrEqual(3);
    });

    test('has savings history chart canvas', () => {
      const canvas = document.getElementById('savings-history-chart');
      expect(canvas).toBeTruthy();
      expect(canvas?.tagName.toLowerCase()).toBe('canvas');
    });

    test('has empty state for savings history', () => {
      const empty = document.getElementById('savings-history-empty');
      expect(empty).toBeTruthy();
      expect(empty?.classList.contains('hidden')).toBe(true);
    });
  });

  describe('Planned Purchases Section', () => {
    test('has planned purchases header', () => {
      const header = document.getElementById('planned-purchases-header');
      expect(header).toBeTruthy();
    });

    test('has planned purchases list container', () => {
      const list = document.getElementById('planned-purchases-list');
      expect(list).toBeTruthy();
    });
  });

  describe('Header Elements', () => {
    test('has user info container', () => {
      const userInfo = document.getElementById('user-info');
      expect(userInfo).toBeTruthy();
    });

    test('has user email span', () => {
      const email = document.getElementById('user-email');
      expect(email).toBeTruthy();
    });

    test('has logout button', () => {
      const btn = document.getElementById('logout-btn');
      expect(btn).toBeTruthy();
    });

    test('has API docs link', () => {
      const link = document.querySelector('a[href="/docs/"]');
      expect(link).toBeTruthy();
      expect(link?.getAttribute('target')).toBe('_blank');
    });

    test('has feedback link', () => {
      const link = document.getElementById('feedback-link');
      expect(link).toBeTruthy();
    });
  });

  describe('Form Validation Attributes', () => {
    test('number inputs have min/max constraints', () => {
      const coverage = document.getElementById('setting-default-coverage') as HTMLInputElement | null;
      expect(coverage?.getAttribute('min')).toBe('0');
      expect(coverage?.getAttribute('max')).toBe('100');

      const notifyDays = document.getElementById('setting-notification-days') as HTMLInputElement | null;
      expect(notifyDays?.getAttribute('min')).toBe('1');
      expect(notifyDays?.getAttribute('max')).toBe('30');
    });

    test('password inputs have minlength', () => {
      const password = document.getElementById('user-password') as HTMLInputElement | null;
      expect(password?.getAttribute('minlength')).toBe('12');
    });

    test('azure client-secret input carries a guidance hint (issue #15)', () => {
      const secretFields = document.getElementById('account-azure-secret-fields');
      expect(secretFields).not.toBeNull();
      const small = secretFields?.querySelector('small');
      expect(small).not.toBeNull();
      // Confirm the copy steers operators away from storing a long-lived
      // credential without spelling out a specific expiry policy the
      // hint might drift from.
      expect(small?.textContent).toMatch(/Workload Identity Federation/i);
      expect(small?.textContent).toMatch(/rotate/i);
    });

    test('azure cognitive search has service-default term + payment selectors (issue #16)', () => {
      // The recommendation filter offered "search" as an Azure service,
      // but the purchasing settings panel lacked a matching card.
      // providers/azure/services/search supports Cognitive Search
      // reservations, so the right fix is to expose the defaults.
      const termSel = document.getElementById('azure-search-term') as HTMLSelectElement | null;
      const paySel = document.getElementById('azure-search-payment') as HTMLSelectElement | null;
      expect(termSel).not.toBeNull();
      expect(paySel).not.toBeNull();
      expect(termSel?.querySelector('option[value="1"]')).not.toBeNull();
      expect(termSel?.querySelector('option[value="3"]')).not.toBeNull();
      // Azure payment values round-trip through the backend's AWS-style
      // vocabulary — "all-upfront" for Upfront, "no-upfront" for Monthly.
      expect(paySel?.querySelector('option[value="all-upfront"]')).not.toBeNull();
      expect(paySel?.querySelector('option[value="no-upfront"]')).not.toBeNull();
    });

    test('aws external-id input is readonly with a copy button + trust-policy hint (issue #18)', () => {
      const input = document.getElementById('account-aws-external-id') as HTMLInputElement | null;
      expect(input).not.toBeNull();
      expect(input?.hasAttribute('readonly')).toBe(true);
      const copyBtn = document.getElementById('account-aws-external-id-copy');
      expect(copyBtn).not.toBeNull();
      expect(copyBtn?.classList.contains('copy-btn')).toBe(true);
      // The hint must point operators at sts:ExternalId so they don't
      // paste the wrong value into the trust policy.
      const roleFields = document.getElementById('account-aws-role-fields');
      const small = roleFields?.querySelector('small');
      expect(small?.textContent).toMatch(/sts:ExternalId/);
    });

    test('aws trust-policy snippet block + copy button exist in role-fields (issue #19)', () => {
      const roleFields = document.getElementById('account-aws-role-fields');
      expect(roleFields).not.toBeNull();
      const pre = document.getElementById('account-aws-trust-policy');
      expect(pre?.tagName.toLowerCase()).toBe('pre');
      const copyBtn = document.getElementById('account-aws-trust-policy-copy');
      expect(copyBtn).not.toBeNull();
      expect(copyBtn?.classList.contains('copy-btn')).toBe(true);
      // Hint is rendered dynamically by settings.ts; confirm the
      // placeholder element exists so the render target is stable.
      const hint = document.getElementById('account-aws-trust-policy-hint');
      expect(hint).not.toBeNull();
    });

    test('aws IAM console deep link opens the role-creation wizard in a new tab (issue #21)', () => {
      const link = document.getElementById('account-aws-iam-console-link') as HTMLAnchorElement | null;
      expect(link).not.toBeNull();
      expect(link?.tagName.toLowerCase()).toBe('a');
      // Must open in a new tab so operators don't lose the modal
      // state they just configured. rel="noopener" is mandatory for
      // target="_blank" to avoid reverse-tabnabbing.
      expect(link?.getAttribute('target')).toBe('_blank');
      expect(link?.getAttribute('rel') ?? '').toMatch(/noopener/);
      // Link target must be the AWS console role-creation wizard.
      expect(link?.getAttribute('href')).toBe('https://console.aws.amazon.com/iam/home#/roles$new');
    });
  });
});
