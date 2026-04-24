/**
 * Tests for purchase-compatibility.isPaymentSupported — the UI-side
 * guardrail that hides payment options producing zero recommendations.
 */
import { isPaymentSupported, paymentOptionsFor } from '../lib/purchase-compatibility';

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
