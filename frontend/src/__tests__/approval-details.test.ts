/**
 * Tests for the approval-details modal body builder (issue #374).
 *
 * The pure builder `renderApprovalDetailsBody` is tested directly with
 * a stubbed accounts map and a hand-rolled PurchaseDetails fixture,
 * since the wrapper `buildApprovalDetailsBody` is a thin shim over the
 * network helpers (those are covered by api-* tests).
 */

import {
  computeEffectiveSavingsPct,
  formatAccountLabel,
  renderApprovalDetailsBody,
} from '../approval-details';
import type { CloudAccount } from '../api/accounts';
import type { PurchaseDetails, Recommendation } from '../api/types';

function makeAccount(partial: Partial<CloudAccount> & Pick<CloudAccount, 'id' | 'name'>): CloudAccount {
  return {
    description: '',
    provider: 'aws',
    external_id: '',
    contact_email: undefined,
    enabled: true,
    credentials_configured: true,
    created_at: '',
    updated_at: '',
    ...partial,
  } as CloudAccount;
}

function makeRec(partial: Partial<Recommendation>): Recommendation {
  return {
    id: 'rec-1',
    provider: 'aws',
    service: 'ec2',
    region: 'us-east-1',
    resource_type: 'm5.xlarge',
    count: 1,
    term: 1,
    payment: 'all-upfront',
    upfront_cost: 1000,
    monthly_cost: 50,
    savings: 20,
    selected: true,
    purchased: false,
    ...partial,
  };
}

function makeDetails(recs: Recommendation[], overrides: Partial<PurchaseDetails> = {}): PurchaseDetails {
  return {
    execution_id: 'exec-abc',
    status: 'pending',
    total_upfront_cost: recs.reduce((a, r) => a + (r.upfront_cost ?? 0), 0),
    estimated_savings: recs.reduce((a, r) => a + r.savings, 0),
    recommendations: recs,
    ...overrides,
  };
}

describe('renderApprovalDetailsBody', () => {
  it('renders the aggregate header with total upfront, monthly + annual savings, and counts', () => {
    const rec = makeRec({ provider: 'aws', cloud_account_id: 'acct-uuid-1', upfront_cost: 1200, savings: 30 });
    const details = makeDetails([rec]);
    const accountsById = new Map<string, CloudAccount>([
      ['acct-uuid-1', makeAccount({ id: 'acct-uuid-1', name: 'Prod', external_id: '123456789012' })],
    ]);
    const body = renderApprovalDetailsBody(details, accountsById);

    const stats = body.querySelectorAll('.approval-details-stat');
    expect(stats.length).toBe(6);
    const text = body.textContent || '';
    expect(text).toContain('Upfront');
    expect(text).toContain('$1,200');
    expect(text).toContain('Monthly savings');
    expect(text).toContain('$30');
    expect(text).toContain('Annual savings');
    expect(text).toContain('$360');
    expect(text).toContain('Commitments');
    expect(text).toContain('Providers');
    expect(text).toContain('Accounts');
  });

  it('renders the per-rec table with all 12 columns', () => {
    const rec = makeRec({});
    const body = renderApprovalDetailsBody(makeDetails([rec]), new Map());
    const headers = Array.from(body.querySelectorAll('.approval-details-table thead th')).map(th => th.textContent);
    expect(headers).toEqual([
      'Account', 'Provider', 'Service', 'Resource', 'Engine', 'Region',
      'Count', 'Term', 'Payment', 'Upfront', 'Monthly savings', 'Eff. savings %',
    ]);
  });

  it('resolves cloud_account_id to "Name (external_id)" when accounts are known', () => {
    const rec = makeRec({ cloud_account_id: 'acct-uuid-1' });
    const accountsById = new Map<string, CloudAccount>([
      ['acct-uuid-1', makeAccount({ id: 'acct-uuid-1', name: 'Prod AWS', external_id: '999988887777' })],
    ]);
    const body = renderApprovalDetailsBody(makeDetails([rec]), accountsById);
    const cells = body.querySelectorAll('.approval-details-table tbody td');
    expect(cells[0]?.textContent).toBe('Prod AWS (999988887777)');
  });

  it('falls back to "acct <prefix>" when the rec carries a UUID we can\'t resolve', () => {
    const rec = makeRec({ cloud_account_id: '11111111-2222-3333-4444-555555555555' });
    const body = renderApprovalDetailsBody(makeDetails([rec]), new Map());
    const cells = body.querySelectorAll('.approval-details-table tbody td');
    expect(cells[0]?.textContent).toBe('acct 11111111…');
  });

  it('shows "(ambient)" for recs without a cloud_account_id', () => {
    const rec = makeRec({});
    delete rec.cloud_account_id;
    const body = renderApprovalDetailsBody(makeDetails([rec]), new Map());
    const cells = body.querySelectorAll('.approval-details-table tbody td');
    expect(cells[0]?.textContent).toBe('(ambient)');
  });

  it('renders the engine column for RDS-shaped recs and "—" otherwise', () => {
    const rdsRec = makeRec({ service: 'rds', engine: 'postgres' });
    const ec2Rec = makeRec({ id: 'rec-2', service: 'ec2', engine: '' });
    const body = renderApprovalDetailsBody(makeDetails([rdsRec, ec2Rec]), new Map());
    const rows = body.querySelectorAll('.approval-details-table tbody tr');
    expect(rows.length).toBe(2);
    // Column index 4 is Engine (0-based).
    expect(rows[0]?.querySelectorAll('td')[4]?.textContent).toBe('postgres');
    expect(rows[1]?.querySelectorAll('td')[4]?.textContent).toBe('—');
  });

  it('renders Term via formatTerm ("1 Year" / "3 Years") for consistency with the rest of the dashboard', () => {
    const rec1 = makeRec({ term: 1 });
    const rec3 = makeRec({ id: 'rec-2', term: 3 });
    const body = renderApprovalDetailsBody(makeDetails([rec1, rec3]), new Map());
    const rows = body.querySelectorAll('.approval-details-table tbody tr');
    expect(rows[0]?.querySelectorAll('td')[7]?.textContent).toBe('1 Year');
    expect(rows[1]?.querySelectorAll('td')[7]?.textContent).toBe('3 Years');
  });

  it('formats upfront and monthly savings via formatCurrency (matches recs page rounding)', () => {
    const rec = makeRec({ upfront_cost: 4567.89, savings: 12.5 });
    const body = renderApprovalDetailsBody(makeDetails([rec]), new Map());
    const cells = body.querySelectorAll('.approval-details-table tbody td');
    expect(cells[9]?.textContent).toBe('$4,568');
    expect(cells[10]?.textContent).toBe('$13');
  });

  it('computes effective savings % when on_demand_cost is set, "—" otherwise', () => {
    const withBaseline = makeRec({ savings: 30, on_demand_cost: 100 });
    const withoutBaseline = makeRec({ id: 'rec-2', savings: 30, on_demand_cost: null, monthly_cost: null });
    const body = renderApprovalDetailsBody(makeDetails([withBaseline, withoutBaseline]), new Map());
    const rows = body.querySelectorAll('.approval-details-table tbody tr');
    expect(rows[0]?.querySelectorAll('td')[11]?.textContent).toBe('30.0%');
    expect(rows[1]?.querySelectorAll('td')[11]?.textContent).toBe('—');
  });

  it('counts distinct providers and accounts in the header', () => {
    const recs = [
      makeRec({ id: 'r1', provider: 'aws', cloud_account_id: 'acct-1' }),
      makeRec({ id: 'r2', provider: 'azure', cloud_account_id: 'acct-2' }),
      makeRec({ id: 'r3', provider: 'gcp', cloud_account_id: 'acct-2' }),
    ];
    const body = renderApprovalDetailsBody(makeDetails(recs), new Map());
    const stats = body.querySelectorAll('.approval-details-stat');
    // Stat index 4 = Providers, index 5 = Accounts (post-Commitments).
    expect(stats[3]?.querySelector('.approval-details-stat-value')?.textContent).toBe('3'); // Commitments
    expect(stats[4]?.querySelector('.approval-details-stat-value')?.textContent).toBe('3'); // Providers
    expect(stats[5]?.querySelector('.approval-details-stat-value')?.textContent).toBe('2'); // Accounts
  });

  it('falls back to a legacy text sentence when recommendations are empty', () => {
    const details = makeDetails([]);
    const body = renderApprovalDetailsBody(details, new Map());
    expect(body.querySelector('.approval-details-fallback')).not.toBeNull();
    expect(body.querySelector('.approval-details-table')).toBeNull();
    expect(body.textContent).toContain('Cloud commitments will be charged');
  });

  it('escapes user-controlled string values in the table', () => {
    const rec = makeRec({ resource_type: '<script>alert(1)</script>' });
    const body = renderApprovalDetailsBody(makeDetails([rec]), new Map());
    // After escapeHtml, the raw "<script>" must not appear as a script
    // element inside the table; the literal angle brackets are
    // escaped into entities and re-rendered as text content.
    expect(body.querySelector('.approval-details-table script')).toBeNull();
    const cells = body.querySelectorAll('.approval-details-table tbody td');
    expect(cells[3]?.textContent).toBe('<script>alert(1)</script>');
  });
});

describe('formatAccountLabel', () => {
  it('returns "Name (external_id)" when both are present', () => {
    const acct = makeAccount({ id: 'a', name: 'Prod', external_id: '999988887777' });
    expect(formatAccountLabel(acct, 'a')).toBe('Prod (999988887777)');
  });

  it('returns just Name when external_id is empty', () => {
    const acct = makeAccount({ id: 'a', name: 'Bastion', external_id: '' });
    expect(formatAccountLabel(acct, 'a')).toBe('Bastion');
  });

  it('returns "acct <prefix>" when the UUID is unknown', () => {
    expect(formatAccountLabel(undefined, 'aaaaaaaa-bbbb-cccc')).toBe('acct aaaaaaaa…');
  });

  it('returns "(ambient)" when no account id was carried', () => {
    expect(formatAccountLabel(undefined, undefined)).toBe('(ambient)');
  });
});

describe('computeEffectiveSavingsPct', () => {
  it('uses on_demand_cost as the denominator when provided', () => {
    const rec = makeRec({ savings: 30, on_demand_cost: 100 });
    expect(computeEffectiveSavingsPct(rec)).toBeCloseTo(30, 5);
  });

  it('reconstructs from monthly_cost + savings when on_demand_cost is null', () => {
    const rec = makeRec({ savings: 20, monthly_cost: 80, on_demand_cost: null });
    expect(computeEffectiveSavingsPct(rec)).toBeCloseTo(20, 5);
  });

  it('returns null when neither denominator is available', () => {
    const rec = makeRec({ savings: 10, monthly_cost: null, on_demand_cost: null });
    expect(computeEffectiveSavingsPct(rec)).toBeNull();
  });

  it('returns null when on_demand_cost is zero (avoid divide by zero)', () => {
    const rec = makeRec({ savings: 10, monthly_cost: 0, on_demand_cost: 0 });
    expect(computeEffectiveSavingsPct(rec)).toBeNull();
  });
});
