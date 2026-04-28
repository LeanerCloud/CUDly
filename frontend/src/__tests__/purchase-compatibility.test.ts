/**
 * Tests for purchase-compatibility.isPaymentSupported — the UI-side
 * guardrail that hides payment options producing zero recommendations.
 */
import {
  isPaymentSupported,
  paymentOptionsFor,
  isSavingsPlanService,
  savingsPlansBucketLabel,
  SAVINGS_PLANS_BUCKET_KEY,
} from '../lib/purchase-compatibility';

describe('isPaymentSupported', () => {
  test('AWS EC2 accepts all payment options for both terms', () => {
    expect(isPaymentSupported('aws', 'ec2', 1, 'all-upfront')).toBe(true);
    expect(isPaymentSupported('aws', 'ec2', 1, 'partial-upfront')).toBe(true);
    expect(isPaymentSupported('aws', 'ec2', 1, 'no-upfront')).toBe(true);
    expect(isPaymentSupported('aws', 'ec2', 3, 'all-upfront')).toBe(true);
    expect(isPaymentSupported('aws', 'ec2', 3, 'partial-upfront')).toBe(true);
    expect(isPaymentSupported('aws', 'ec2', 3, 'no-upfront')).toBe(true);
  });

  test('AWS RDS 3yr + no-upfront is rejected (the one hard rule)', () => {
    expect(isPaymentSupported('aws', 'rds', 1, 'no-upfront')).toBe(true);
    expect(isPaymentSupported('aws', 'rds', 3, 'no-upfront')).toBe(false);
    expect(isPaymentSupported('aws', 'rds', 3, 'all-upfront')).toBe(true);
    expect(isPaymentSupported('aws', 'rds', 3, 'partial-upfront')).toBe(true);
  });

  test('Azure only accepts all-upfront / upfront / monthly', () => {
    expect(isPaymentSupported('azure', 'compute', 1, 'all-upfront')).toBe(true);
    expect(isPaymentSupported('azure', 'compute', 1, 'upfront')).toBe(true);
    expect(isPaymentSupported('azure', 'compute', 1, 'monthly')).toBe(true);
    expect(isPaymentSupported('azure', 'compute', 1, 'no-upfront')).toBe(false);
    expect(isPaymentSupported('azure', 'compute', 1, 'partial-upfront')).toBe(false);
  });

  test('GCP only accepts all-upfront / upfront / monthly', () => {
    expect(isPaymentSupported('gcp', 'computeengine', 1, 'all-upfront')).toBe(true);
    expect(isPaymentSupported('gcp', 'computeengine', 3, 'monthly')).toBe(true);
    expect(isPaymentSupported('gcp', 'computeengine', 1, 'no-upfront')).toBe(false);
    expect(isPaymentSupported('gcp', 'computeengine', 1, 'partial-upfront')).toBe(false);
  });
});

describe('paymentOptionsFor', () => {
  test('AWS EC2 yields all 4 options', () => {
    expect(paymentOptionsFor('aws', 'ec2', 1)).toEqual(
      ['all-upfront', 'partial-upfront', 'no-upfront', 'monthly'],
    );
  });

  test('AWS RDS 3yr omits no-upfront', () => {
    expect(paymentOptionsFor('aws', 'rds', 3)).toEqual(
      ['all-upfront', 'partial-upfront', 'monthly'],
    );
  });

  test('Azure omits no-upfront + partial-upfront', () => {
    expect(paymentOptionsFor('azure', 'compute', 1)).toEqual(['all-upfront', 'monthly']);
  });

  test('GCP omits no-upfront + partial-upfront', () => {
    expect(paymentOptionsFor('gcp', 'computeengine', 3)).toEqual(['all-upfront', 'monthly']);
  });
});

// Issue #132: bulk-buy bucketing collapses all SP plan-type slugs into
// one bucket. These tests pin the small predicate + label utility.
describe('isSavingsPlanService (issue #132)', () => {
  test('matches every savings-plans-* slug', () => {
    expect(isSavingsPlanService('savings-plans-compute')).toBe(true);
    expect(isSavingsPlanService('savings-plans-ec2instance')).toBe(true);
    expect(isSavingsPlanService('savings-plans-sagemaker')).toBe(true);
    expect(isSavingsPlanService('savings-plans-database')).toBe(true);
  });

  test('matches the canonical bucket key itself', () => {
    expect(isSavingsPlanService(SAVINGS_PLANS_BUCKET_KEY)).toBe(true);
  });

  test('rejects non-SP slugs', () => {
    expect(isSavingsPlanService('ec2')).toBe(false);
    expect(isSavingsPlanService('rds')).toBe(false);
    expect(isSavingsPlanService('elasticache')).toBe(false);
    expect(isSavingsPlanService('')).toBe(false);
  });
});

describe('savingsPlansBucketLabel (issue #132)', () => {
  test('renders single plan type without parens being empty', () => {
    expect(savingsPlansBucketLabel(['savings-plans-compute'])).toBe(
      'Savings Plans (Compute)',
    );
  });

  test('renders mixed plan types in input order', () => {
    expect(
      savingsPlansBucketLabel(['savings-plans-compute', 'savings-plans-sagemaker']),
    ).toBe('Savings Plans (Compute + SageMaker)');
  });

  test('deduplicates repeated plan types', () => {
    expect(
      savingsPlansBucketLabel([
        'savings-plans-compute',
        'savings-plans-compute',
        'savings-plans-ec2instance',
      ]),
    ).toBe('Savings Plans (Compute + EC2 Instance)');
  });

  test('renders all four plan types', () => {
    expect(
      savingsPlansBucketLabel([
        'savings-plans-compute',
        'savings-plans-ec2instance',
        'savings-plans-sagemaker',
        'savings-plans-database',
      ]),
    ).toBe('Savings Plans (Compute + EC2 Instance + SageMaker + Database)');
  });

  test('skips non-SP slugs and falls back when none resolve', () => {
    expect(savingsPlansBucketLabel(['ec2', 'rds'])).toBe('Savings Plans');
    expect(savingsPlansBucketLabel([])).toBe('Savings Plans');
  });

  test('mixed SP and non-SP keeps only SP entries', () => {
    expect(
      savingsPlansBucketLabel(['ec2', 'savings-plans-compute', 'rds']),
    ).toBe('Savings Plans (Compute)');
  });
});
