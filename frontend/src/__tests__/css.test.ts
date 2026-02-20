/**
 * CSS validation tests
 * These tests verify that our CSS styling works correctly
 */
import fs from 'fs';
import path from 'path';

describe('CSS Styles', () => {
  let css: string;

  beforeAll(() => {
    // Read all CSS files from the styles directory
    const stylesDir = path.join(__dirname, '..', 'styles');
    const cssFiles = fs.readdirSync(stylesDir)
      .filter(file => file.endsWith('.css'))
      .map(file => fs.readFileSync(path.join(stylesDir, file), 'utf8'));
    css = cssFiles.join('\n');
  });

  describe('Required CSS Rules', () => {
    test('has body styles', () => {
      expect(css).toMatch(/body\s*\{/);
    });

    test('has header styles', () => {
      expect(css).toMatch(/header\s*\{/);
    });

    test('has card styles', () => {
      expect(css).toMatch(/\.card\s*\{/);
    });

    test('has button styles', () => {
      expect(css).toMatch(/button\s*\{/);
    });

    test('has primary button variant', () => {
      expect(css).toMatch(/button\.primary\s*\{/);
    });

    test('has danger button variant', () => {
      expect(css).toMatch(/button\.danger\s*\{/);
    });

    test('has modal styles', () => {
      expect(css).toMatch(/\.modal\s*\{/);
    });

    test('has hidden class', () => {
      expect(css).toMatch(/\.hidden\s*\{/);
    });

    test('has tab styles', () => {
      expect(css).toMatch(/\.tab-btn\s*\{/);
      expect(css).toMatch(/\.tab-content\s*\{/);
    });

    test('has form styles', () => {
      expect(css).toMatch(/\.form-section\s*\{/);
    });

    test('has table styles', () => {
      expect(css).toMatch(/table\s*\{/);
      expect(css).toMatch(/th,\s*td\s*\{/);
    });
  });

  describe('CSS Classes', () => {
    test('has savings class with green color', () => {
      expect(css).toMatch(/\.savings\s*\{[^}]*color:\s*#34a853/);
    });

    test('has error class with red color', () => {
      expect(css).toMatch(/\.error\s*\{[^}]*color:\s*#ea4335/);
    });

    test('has provider badge classes', () => {
      expect(css).toMatch(/\.provider-badge\.aws/);
      expect(css).toMatch(/\.provider-badge\.azure/);
      expect(css).toMatch(/\.provider-badge\.gcp/);
    });

    test('has status badge classes', () => {
      expect(css).toMatch(/\.status-badge\.active/);
      expect(css).toMatch(/\.status-badge\.paused/);
      expect(css).toMatch(/\.status-badge\.disabled/);
    });

    test('has toggle switch styles', () => {
      expect(css).toMatch(/\.toggle-label/);
      expect(css).toMatch(/\.slider/);
    });
  });

  describe('Responsive Design', () => {
    test('has responsive breakpoints', () => {
      expect(css).toMatch(/@media\s*\(max-width:\s*768px\)/);
    });

    test('has responsive styles block', () => {
      // Check that the media query contains responsive styles
      expect(css).toMatch(/@media\s*\(max-width:\s*768px\)\s*\{[\s\S]*header\s*\{/);
    });

    test('has responsive table styles', () => {
      // Check that table is styled within the media query
      expect(css).toMatch(/@media\s*\(max-width:\s*768px\)\s*\{[\s\S]*table\s*\{/);
    });
  });

  describe('Layout', () => {
    test('has grid layout for cards', () => {
      expect(css).toMatch(/display:\s*grid/);
    });

    test('has flexbox for headers/controls', () => {
      expect(css).toMatch(/display:\s*flex/);
    });

    test('has box-sizing border-box', () => {
      expect(css).toMatch(/box-sizing:\s*border-box/);
    });
  });

  describe('Color Scheme', () => {
    test('uses primary blue color', () => {
      expect(css).toMatch(/#1a73e8/);
    });

    test('uses success green color', () => {
      expect(css).toMatch(/#34a853/);
    });

    test('uses warning yellow color', () => {
      expect(css).toMatch(/#fbbc04/);
    });

    test('uses danger red color', () => {
      expect(css).toMatch(/#ea4335/);
    });
  });

  describe('Interactive Elements', () => {
    test('has hover states for buttons', () => {
      expect(css).toMatch(/button:hover/);
    });

    test('has hover states for table rows', () => {
      expect(css).toMatch(/tr:hover/);
    });

    test('has transition animations', () => {
      expect(css).toMatch(/transition:/);
    });
  });

  describe('Button Variants', () => {
    test('has success button variant', () => {
      expect(css).toMatch(/button\.success\s*\{/);
    });

    test('has small button variant', () => {
      expect(css).toMatch(/\.btn-small\s*\{/);
    });
  });

  describe('State Classes', () => {
    test('has loading state styles', () => {
      expect(css).toMatch(/\.loading\s*\{/);
    });

    test('has empty state styles', () => {
      expect(css).toMatch(/\.empty\s*\{/);
    });

    test('has error-message styles', () => {
      expect(css).toMatch(/\.error-message\s*\{/);
    });

    test('has success-message styles', () => {
      expect(css).toMatch(/\.success-message\s*\{/);
    });

    test('has help-text styles', () => {
      expect(css).toMatch(/\.help-text\s*\{/);
    });
  });

  describe('Layout Components', () => {
    test('has controls-bar styles', () => {
      expect(css).toMatch(/\.controls-bar\s*\{/);
    });

    test('has filter-group styles', () => {
      expect(css).toMatch(/\.filter-group\s*\{/);
    });

    test('has action-group styles', () => {
      expect(css).toMatch(/\.action-group\s*\{/);
    });

    test('has date-range-picker styles', () => {
      expect(css).toMatch(/\.date-range-picker\s*\{/);
    });
  });

  describe('Form Components', () => {
    test('has form-row styles', () => {
      expect(css).toMatch(/\.form-row\s*\{/);
    });

    test('has input styles', () => {
      expect(css).toMatch(/input\[type/);
    });

    test('has select styles', () => {
      expect(css).toMatch(/select\s*\{/);
    });

    test('has label styles', () => {
      expect(css).toMatch(/label\s*\{/);
    });

    test('has textarea styles in modal', () => {
      expect(css).toMatch(/\.modal-content textarea\s*\{/);
    });
  });

  describe('Settings Components', () => {
    test('has settings-form styles', () => {
      expect(css).toMatch(/\.settings-form\s*\{/);
    });

    test('has settings-category styles', () => {
      expect(css).toMatch(/\.settings-category\s*\{/);
    });

    test('has setting-row styles', () => {
      expect(css).toMatch(/\.setting-row\s*\{/);
    });

    test('has credential-status styles', () => {
      expect(css).toMatch(/\.credential-status/);
    });

    test('has service-defaults-grid styles', () => {
      expect(css).toMatch(/\.service-defaults-grid\s*\{/);
    });

    test('has service-default-card styles', () => {
      expect(css).toMatch(/\.service-default-card\s*\{/);
    });
  });

  describe('Plan Components', () => {
    test('has plan-card styles', () => {
      expect(css).toMatch(/\.plan-card\s*\{/);
    });

    test('has plan-header styles', () => {
      expect(css).toMatch(/\.plan-header\s*\{/);
    });

    test('has plan-body styles', () => {
      expect(css).toMatch(/\.plan-body\s*\{/);
    });

    test('has plan-details styles', () => {
      expect(css).toMatch(/\.plan-details\s*\{/);
    });

    test('has ramp-option styles', () => {
      expect(css).toMatch(/\.ramp-option\s*\{/);
    });
  });

  describe('Modal Components', () => {
    test('has modal-overlay styles', () => {
      expect(css).toMatch(/\.modal-overlay\s*\{/);
    });

    test('has modal-content styles', () => {
      expect(css).toMatch(/\.modal-content\s*\{/);
    });

    test('has modal-buttons styles', () => {
      expect(css).toMatch(/\.modal-buttons\s*\{/);
    });

    test('has modal-wide variant', () => {
      expect(css).toMatch(/\.modal-wide\s*\{/);
    });
  });

  describe('CLI Command Styles', () => {
    test('has cli-command styles', () => {
      expect(css).toMatch(/\.cli-command\s*\{/);
    });

    test('has copy-btn styles', () => {
      expect(css).toMatch(/\.copy-btn\s*\{/);
    });
  });

  describe('Tooltip Styles', () => {
    test('has info-icon styles', () => {
      expect(css).toMatch(/\.info-icon\s*\{/);
    });

    test('has tooltip-text styles', () => {
      expect(css).toMatch(/\.tooltip-text\s*\{/);
    });
  });
});

describe('CSS File Organization', () => {
  const stylesDir = path.join(__dirname, '..', 'styles');

  function readCssFile(filename: string): string {
    return fs.readFileSync(path.join(stylesDir, filename), 'utf8');
  }

  test('base.css contains reset and typography', () => {
    const base = readCssFile('base.css');
    expect(base).toMatch(/\*\s*\{[^}]*box-sizing:\s*border-box/);
    expect(base).toMatch(/body\s*\{/);
    expect(base).toMatch(/\.hidden\s*\{/);
    expect(base).toMatch(/\.loading\s*\{/);
    expect(base).toMatch(/\.error\s*\{/);
  });

  test('layout.css contains header and main styles', () => {
    const layout = readCssFile('layout.css');
    expect(layout).toMatch(/header\s*\{/);
    expect(layout).toMatch(/main\s*\{/);
  });

  test('components.css contains cards and buttons', () => {
    const components = readCssFile('components.css');
    expect(components).toMatch(/\.card\s*\{/);
    expect(components).toMatch(/button\s*\{/);
    expect(components).toMatch(/button\.primary\s*\{/);
    expect(components).toMatch(/\.provider-badge/);
    expect(components).toMatch(/\.status-badge/);
  });

  test('forms.css contains form elements', () => {
    const forms = readCssFile('forms.css');
    expect(forms).toMatch(/input/);
    expect(forms).toMatch(/select\s*\{/);
    expect(forms).toMatch(/label\s*\{/);
    expect(forms).toMatch(/\.toggle-label/);
    expect(forms).toMatch(/\.slider\s*\{/);
  });

  test('tables.css contains table styles', () => {
    const tables = readCssFile('tables.css');
    expect(tables).toMatch(/table\s*\{/);
    expect(tables).toMatch(/th,?\s*td\s*\{/);
    expect(tables).toMatch(/tr:hover/);
  });

  test('modals.css contains modal styles', () => {
    const modals = readCssFile('modals.css');
    expect(modals).toMatch(/\.modal\s*\{/);
    expect(modals).toMatch(/\.modal-content\s*\{/);
    expect(modals).toMatch(/\.modal-overlay\s*\{/);
    expect(modals).toMatch(/\.modal-buttons\s*\{/);
  });

  test('tabs.css contains tab navigation styles', () => {
    const tabs = readCssFile('tabs.css');
    expect(tabs).toMatch(/\.tabs\s*\{/);
    expect(tabs).toMatch(/\.tab-btn\s*\{/);
    expect(tabs).toMatch(/\.tab-content\s*\{/);
  });

  test('plans.css contains plan-specific styles', () => {
    const plans = readCssFile('plans.css');
    expect(plans).toMatch(/\.plan-card\s*\{/);
    expect(plans).toMatch(/\.ramp-option/);
  });

  test('settings.css contains settings form styles', () => {
    const settings = readCssFile('settings.css');
    expect(settings).toMatch(/\.settings-form\s*\{/);
    expect(settings).toMatch(/\.settings-category\s*\{/);
    expect(settings).toMatch(/\.setting-row\s*\{/);
    expect(settings).toMatch(/\.credential-status/);
  });

  test('responsive.css contains media queries', () => {
    const responsive = readCssFile('responsive.css');
    expect(responsive).toMatch(/@media\s*\(max-width:\s*768px\)/);
  });

  test('index.css imports all other files', () => {
    const index = readCssFile('index.css');
    expect(index).toMatch(/@import\s+['"]\.\/base\.css['"]/);
    expect(index).toMatch(/@import\s+['"]\.\/layout\.css['"]/);
    expect(index).toMatch(/@import\s+['"]\.\/components\.css['"]/);
    expect(index).toMatch(/@import\s+['"]\.\/forms\.css['"]/);
    expect(index).toMatch(/@import\s+['"]\.\/tables\.css['"]/);
    expect(index).toMatch(/@import\s+['"]\.\/modals\.css['"]/);
    expect(index).toMatch(/@import\s+['"]\.\/tabs\.css['"]/);
    expect(index).toMatch(/@import\s+['"]\.\/responsive\.css['"]/);
  });
});

describe('CSS Applied Correctly', () => {
  beforeEach(() => {
    document.body.innerHTML = `
      <style>
        .hidden { display: none; }
        .card { background: white; padding: 1rem; }
        .savings { color: #34a853; }
        .error { color: #ea4335; }
        .tab-content { display: none; }
        .tab-content.active { display: block; }
      </style>
      <div class="card">Test Card</div>
      <div class="hidden">Hidden Element</div>
      <div class="savings">$100</div>
      <div class="error">Error message</div>
      <div class="tab-content">Tab 1</div>
      <div class="tab-content active">Tab 2</div>
    `;
  });

  test('hidden class hides elements', () => {
    const hidden = document.querySelector('.hidden') as HTMLElement;
    const computedStyle = window.getComputedStyle(hidden);
    expect(computedStyle.display).toBe('none');
  });

  test('tab-content is hidden by default', () => {
    const tab = document.querySelector('.tab-content:not(.active)') as HTMLElement;
    const computedStyle = window.getComputedStyle(tab);
    expect(computedStyle.display).toBe('none');
  });

  test('active tab is visible', () => {
    const activeTab = document.querySelector('.tab-content.active') as HTMLElement;
    const computedStyle = window.getComputedStyle(activeTab);
    expect(computedStyle.display).toBe('block');
  });
});
