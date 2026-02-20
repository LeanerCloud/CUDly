/**
 * Tests for commitmentOptions module
 */
import {
  getCommitmentConfig,
  isValidCombination,
  getValidPaymentOptions,
  getValidTermOptions,
  populateTermSelect,
  populatePaymentSelect,
  getPaymentLabel,
  normalizePaymentValue,
  CommitmentConfig,
  PaymentOption,
  TermOption
} from '../commitmentOptions';

describe('commitmentOptions', () => {
  describe('getCommitmentConfig', () => {
    describe('AWS provider', () => {
      it('should return EC2 config with all payment options', () => {
        const config = getCommitmentConfig('aws', 'ec2');

        expect(config.terms).toHaveLength(2);
        expect(config.terms[0]).toEqual({ value: 1, label: '1 Year' });
        expect(config.terms[1]).toEqual({ value: 3, label: '3 Years' });

        expect(config.payments).toHaveLength(3);
        expect(config.payments.map(p => p.value)).toEqual([
          'no-upfront',
          'partial-upfront',
          'all-upfront'
        ]);

        expect(config.invalidCombinations).toBeUndefined();
      });

      it('should return savings-plans config with all options', () => {
        const config = getCommitmentConfig('aws', 'savings-plans');

        expect(config.terms).toHaveLength(2);
        expect(config.payments).toHaveLength(3);
        expect(config.invalidCombinations).toBeUndefined();
      });

      it('should return RDS config with invalid 3yr no-upfront combination', () => {
        const config = getCommitmentConfig('aws', 'rds');

        expect(config.terms).toHaveLength(2);
        expect(config.payments).toHaveLength(3);
        expect(config.invalidCombinations).toBeDefined();
        expect(config.invalidCombinations).toHaveLength(1);
        expect(config.invalidCombinations![0]).toEqual({ term: 3, payment: 'no-upfront' });
      });

      it('should return ElastiCache config with invalid 3yr no-upfront combination', () => {
        const config = getCommitmentConfig('aws', 'elasticache');

        expect(config.invalidCombinations).toBeDefined();
        expect(config.invalidCombinations![0]).toEqual({ term: 3, payment: 'no-upfront' });
      });

      it('should return OpenSearch config with invalid 3yr no-upfront combination', () => {
        const config = getCommitmentConfig('aws', 'opensearch');

        expect(config.invalidCombinations).toBeDefined();
        expect(config.invalidCombinations![0]).toEqual({ term: 3, payment: 'no-upfront' });
      });

      it('should return Redshift config with invalid 3yr no-upfront combination', () => {
        const config = getCommitmentConfig('aws', 'redshift');

        expect(config.invalidCombinations).toBeDefined();
        expect(config.invalidCombinations![0]).toEqual({ term: 3, payment: 'no-upfront' });
      });

      it('should return MemoryDB config with invalid 3yr no-upfront combination', () => {
        const config = getCommitmentConfig('aws', 'memorydb');

        expect(config.invalidCombinations).toBeDefined();
        expect(config.invalidCombinations![0]).toEqual({ term: 3, payment: 'no-upfront' });
      });

      it('should return default AWS config for unknown service', () => {
        const config = getCommitmentConfig('aws', 'unknown-service');

        expect(config.terms).toHaveLength(2);
        expect(config.payments).toHaveLength(3);
        expect(config.invalidCombinations).toBeUndefined();
      });

      it('should return default AWS config when no service specified', () => {
        const config = getCommitmentConfig('aws');

        expect(config.terms).toHaveLength(2);
        expect(config.payments).toHaveLength(3);
        expect(config.invalidCombinations).toBeUndefined();
      });

      it('should handle case-insensitive provider names', () => {
        const config1 = getCommitmentConfig('AWS', 'ec2');
        const config2 = getCommitmentConfig('aws', 'ec2');
        const config3 = getCommitmentConfig('Aws', 'EC2');

        expect(config1).toEqual(config2);
        // Note: service is also lowercased
        expect(config2).toEqual(config3);
      });

      it('should handle case-insensitive service names', () => {
        const config1 = getCommitmentConfig('aws', 'RDS');
        const config2 = getCommitmentConfig('aws', 'rds');

        expect(config1).toEqual(config2);
      });
    });

    describe('Azure provider', () => {
      it('should return Azure default config with upfront and monthly payments', () => {
        const config = getCommitmentConfig('azure');

        expect(config.terms).toHaveLength(2);
        expect(config.terms[0]).toEqual({ value: 1, label: '1 Year' });
        expect(config.terms[1]).toEqual({ value: 3, label: '3 Years' });

        expect(config.payments).toHaveLength(2);
        expect(config.payments[0]).toEqual({ value: 'upfront', label: 'Pay Upfront' });
        expect(config.payments[1]).toEqual({ value: 'monthly', label: 'Pay Monthly' });

        expect(config.invalidCombinations).toBeUndefined();
      });

      it('should return same config for any Azure service', () => {
        const config1 = getCommitmentConfig('azure', 'vm');
        const config2 = getCommitmentConfig('azure', 'sql');
        const config3 = getCommitmentConfig('azure');

        expect(config1).toEqual(config2);
        expect(config2).toEqual(config3);
      });
    });

    describe('GCP provider', () => {
      it('should return GCP config with only monthly payment option', () => {
        const config = getCommitmentConfig('gcp');

        expect(config.terms).toHaveLength(2);
        expect(config.terms[0]).toEqual({ value: 1, label: '1 Year' });
        expect(config.terms[1]).toEqual({ value: 3, label: '3 Years' });

        expect(config.payments).toHaveLength(1);
        expect(config.payments[0]).toEqual({ value: 'monthly', label: 'Monthly' });

        expect(config.invalidCombinations).toBeUndefined();
      });

      it('should return same config for any GCP service', () => {
        const config1 = getCommitmentConfig('gcp', 'compute');
        const config2 = getCommitmentConfig('gcp', 'cloudsql');
        const config3 = getCommitmentConfig('gcp');

        expect(config1).toEqual(config2);
        expect(config2).toEqual(config3);
      });
    });

    describe('Unknown provider', () => {
      it('should return default config for unknown provider', () => {
        const config = getCommitmentConfig('unknown-provider');

        // Default config uses AWS payments
        expect(config.terms).toHaveLength(2);
        expect(config.payments).toHaveLength(3);
        expect(config.payments.map(p => p.value)).toEqual([
          'no-upfront',
          'partial-upfront',
          'all-upfront'
        ]);
      });

      it('should return default config for empty provider string', () => {
        const config = getCommitmentConfig('');

        expect(config.terms).toHaveLength(2);
        expect(config.payments).toHaveLength(3);
      });
    });
  });

  describe('isValidCombination', () => {
    describe('AWS services without restrictions', () => {
      it('should return true for all EC2 combinations', () => {
        expect(isValidCombination('aws', 'ec2', 1, 'no-upfront')).toBe(true);
        expect(isValidCombination('aws', 'ec2', 1, 'partial-upfront')).toBe(true);
        expect(isValidCombination('aws', 'ec2', 1, 'all-upfront')).toBe(true);
        expect(isValidCombination('aws', 'ec2', 3, 'no-upfront')).toBe(true);
        expect(isValidCombination('aws', 'ec2', 3, 'partial-upfront')).toBe(true);
        expect(isValidCombination('aws', 'ec2', 3, 'all-upfront')).toBe(true);
      });

      it('should return true for all savings-plans combinations', () => {
        expect(isValidCombination('aws', 'savings-plans', 1, 'no-upfront')).toBe(true);
        expect(isValidCombination('aws', 'savings-plans', 3, 'no-upfront')).toBe(true);
      });
    });

    describe('AWS services with 3yr no-upfront restriction', () => {
      const restrictedServices = ['rds', 'elasticache', 'opensearch', 'redshift', 'memorydb'];

      it.each(restrictedServices)('should return false for %s 3yr no-upfront', (service) => {
        expect(isValidCombination('aws', service, 3, 'no-upfront')).toBe(false);
      });

      it.each(restrictedServices)('should return true for %s 1yr no-upfront', (service) => {
        expect(isValidCombination('aws', service, 1, 'no-upfront')).toBe(true);
      });

      it.each(restrictedServices)('should return true for %s 3yr partial-upfront', (service) => {
        expect(isValidCombination('aws', service, 3, 'partial-upfront')).toBe(true);
      });

      it.each(restrictedServices)('should return true for %s 3yr all-upfront', (service) => {
        expect(isValidCombination('aws', service, 3, 'all-upfront')).toBe(true);
      });
    });

    describe('Azure and GCP', () => {
      it('should return true for all Azure combinations', () => {
        expect(isValidCombination('azure', 'vm', 1, 'upfront')).toBe(true);
        expect(isValidCombination('azure', 'vm', 3, 'upfront')).toBe(true);
        expect(isValidCombination('azure', 'vm', 1, 'monthly')).toBe(true);
        expect(isValidCombination('azure', 'vm', 3, 'monthly')).toBe(true);
      });

      it('should return true for all GCP combinations', () => {
        expect(isValidCombination('gcp', 'compute', 1, 'monthly')).toBe(true);
        expect(isValidCombination('gcp', 'compute', 3, 'monthly')).toBe(true);
      });
    });

    describe('Edge cases', () => {
      it('should handle undefined service', () => {
        expect(isValidCombination('aws', undefined, 1, 'no-upfront')).toBe(true);
        expect(isValidCombination('aws', undefined, 3, 'no-upfront')).toBe(true);
      });

      it('should handle unknown provider', () => {
        expect(isValidCombination('unknown', 'service', 1, 'no-upfront')).toBe(true);
      });
    });
  });

  describe('getValidPaymentOptions', () => {
    describe('AWS services without restrictions', () => {
      it('should return all payment options for EC2 1-year term', () => {
        const options = getValidPaymentOptions('aws', 'ec2', 1);

        expect(options).toHaveLength(3);
        expect(options.map(o => o.value)).toEqual([
          'no-upfront',
          'partial-upfront',
          'all-upfront'
        ]);
      });

      it('should return all payment options for EC2 3-year term', () => {
        const options = getValidPaymentOptions('aws', 'ec2', 3);

        expect(options).toHaveLength(3);
      });
    });

    describe('AWS services with 3yr no-upfront restriction', () => {
      it('should exclude no-upfront for RDS 3-year term', () => {
        const options = getValidPaymentOptions('aws', 'rds', 3);

        expect(options).toHaveLength(2);
        expect(options.map(o => o.value)).toEqual([
          'partial-upfront',
          'all-upfront'
        ]);
      });

      it('should return all options for RDS 1-year term', () => {
        const options = getValidPaymentOptions('aws', 'rds', 1);

        expect(options).toHaveLength(3);
        expect(options.map(o => o.value)).toContain('no-upfront');
      });

      it('should exclude no-upfront for ElastiCache 3-year term', () => {
        const options = getValidPaymentOptions('aws', 'elasticache', 3);

        expect(options).toHaveLength(2);
        expect(options.map(o => o.value)).not.toContain('no-upfront');
      });

      it('should exclude no-upfront for OpenSearch 3-year term', () => {
        const options = getValidPaymentOptions('aws', 'opensearch', 3);

        expect(options).toHaveLength(2);
        expect(options.map(o => o.value)).not.toContain('no-upfront');
      });
    });

    describe('Azure', () => {
      it('should return Azure payment options', () => {
        const options = getValidPaymentOptions('azure', 'vm', 1);

        expect(options).toHaveLength(2);
        expect(options.map(o => o.value)).toEqual(['upfront', 'monthly']);
      });
    });

    describe('GCP', () => {
      it('should return only monthly option', () => {
        const options = getValidPaymentOptions('gcp', 'compute', 1);

        expect(options).toHaveLength(1);
        expect(options[0]!.value).toBe('monthly');
      });
    });

    describe('Edge cases', () => {
      it('should handle undefined service', () => {
        const options = getValidPaymentOptions('aws', undefined, 1);

        expect(options).toHaveLength(3);
      });
    });
  });

  describe('getValidTermOptions', () => {
    describe('AWS services without restrictions', () => {
      it('should return all term options for EC2 with no-upfront', () => {
        const options = getValidTermOptions('aws', 'ec2', 'no-upfront');

        expect(options).toHaveLength(2);
        expect(options.map(o => o.value)).toEqual([1, 3]);
      });

      it('should return all term options for EC2 with all-upfront', () => {
        const options = getValidTermOptions('aws', 'ec2', 'all-upfront');

        expect(options).toHaveLength(2);
      });
    });

    describe('AWS services with 3yr no-upfront restriction', () => {
      it('should exclude 3-year for RDS with no-upfront', () => {
        const options = getValidTermOptions('aws', 'rds', 'no-upfront');

        expect(options).toHaveLength(1);
        expect(options[0]).toEqual({ value: 1, label: '1 Year' });
      });

      it('should return all terms for RDS with partial-upfront', () => {
        const options = getValidTermOptions('aws', 'rds', 'partial-upfront');

        expect(options).toHaveLength(2);
        expect(options.map(o => o.value)).toEqual([1, 3]);
      });

      it('should return all terms for RDS with all-upfront', () => {
        const options = getValidTermOptions('aws', 'rds', 'all-upfront');

        expect(options).toHaveLength(2);
      });

      it('should exclude 3-year for ElastiCache with no-upfront', () => {
        const options = getValidTermOptions('aws', 'elasticache', 'no-upfront');

        expect(options).toHaveLength(1);
        expect(options[0]!.value).toBe(1);
      });

      it('should exclude 3-year for OpenSearch with no-upfront', () => {
        const options = getValidTermOptions('aws', 'opensearch', 'no-upfront');

        expect(options).toHaveLength(1);
        expect(options[0]!.value).toBe(1);
      });
    });

    describe('Azure', () => {
      it('should return all term options for any payment', () => {
        const upfrontOptions = getValidTermOptions('azure', 'vm', 'upfront');
        const monthlyOptions = getValidTermOptions('azure', 'vm', 'monthly');

        expect(upfrontOptions).toHaveLength(2);
        expect(monthlyOptions).toHaveLength(2);
      });
    });

    describe('GCP', () => {
      it('should return all term options for monthly payment', () => {
        const options = getValidTermOptions('gcp', 'compute', 'monthly');

        expect(options).toHaveLength(2);
        expect(options.map(o => o.value)).toEqual([1, 3]);
      });
    });

    describe('Edge cases', () => {
      it('should handle undefined service', () => {
        const options = getValidTermOptions('aws', undefined, 'no-upfront');

        expect(options).toHaveLength(2);
      });
    });
  });

  describe('populateTermSelect', () => {
    let selectElement: HTMLSelectElement;

    beforeEach(() => {
      selectElement = document.createElement('select');
      document.body.appendChild(selectElement);
    });

    it('should populate select with all term options when no payment specified', () => {
      populateTermSelect(selectElement, 'aws', 'ec2');

      expect(selectElement.options).toHaveLength(2);
      expect(selectElement.options[0]!.value).toBe('1');
      expect(selectElement.options[0]!.text).toBe('1 Year');
      expect(selectElement.options[1]!.value).toBe('3');
      expect(selectElement.options[1]!.text).toBe('3 Years');
    });

    it('should populate select with filtered terms based on payment', () => {
      populateTermSelect(selectElement, 'aws', 'rds', 'no-upfront');

      expect(selectElement.options).toHaveLength(1);
      expect(selectElement.options[0]!.value).toBe('1');
      expect(selectElement.options[0]!.text).toBe('1 Year');
    });

    it('should preserve current selection if still valid', () => {
      // First populate with all options
      populateTermSelect(selectElement, 'aws', 'ec2');
      selectElement.value = '3';

      // Repopulate - should preserve selection
      populateTermSelect(selectElement, 'aws', 'ec2', 'all-upfront');

      expect(selectElement.value).toBe('3');
    });

    it('should not preserve selection if no longer valid', () => {
      // First populate with all options and select 3 years
      populateTermSelect(selectElement, 'aws', 'rds');
      selectElement.value = '3';

      // Repopulate with restricted options
      populateTermSelect(selectElement, 'aws', 'rds', 'no-upfront');

      // Value should be first available option
      expect(selectElement.value).toBe('1');
    });

    it('should handle Azure provider', () => {
      populateTermSelect(selectElement, 'azure', 'vm');

      expect(selectElement.options).toHaveLength(2);
    });

    it('should handle GCP provider', () => {
      populateTermSelect(selectElement, 'gcp', 'compute');

      expect(selectElement.options).toHaveLength(2);
    });

    it('should clear existing options before populating', () => {
      // Add some initial options
      selectElement.innerHTML = '<option value="old">Old Option</option>';

      populateTermSelect(selectElement, 'aws', 'ec2');

      expect(selectElement.options).toHaveLength(2);
      expect(selectElement.querySelector('option[value="old"]')).toBeNull();
    });

    it('should handle undefined service', () => {
      populateTermSelect(selectElement, 'aws');

      expect(selectElement.options).toHaveLength(2);
    });
  });

  describe('populatePaymentSelect', () => {
    let selectElement: HTMLSelectElement;

    beforeEach(() => {
      selectElement = document.createElement('select');
      document.body.appendChild(selectElement);
    });

    it('should populate select with all payment options when no term specified', () => {
      populatePaymentSelect(selectElement, 'aws', 'ec2');

      expect(selectElement.options).toHaveLength(3);
      expect(selectElement.options[0]!.value).toBe('no-upfront');
      expect(selectElement.options[0]!.text).toBe('No Upfront');
      expect(selectElement.options[1]!.value).toBe('partial-upfront');
      expect(selectElement.options[1]!.text).toBe('Partial Upfront');
      expect(selectElement.options[2]!.value).toBe('all-upfront');
      expect(selectElement.options[2]!.text).toBe('All Upfront');
    });

    it('should populate select with filtered payments based on term', () => {
      populatePaymentSelect(selectElement, 'aws', 'rds', 3);

      expect(selectElement.options).toHaveLength(2);
      expect(selectElement.options[0]!.value).toBe('partial-upfront');
      expect(selectElement.options[1]!.value).toBe('all-upfront');
    });

    it('should preserve current selection if still valid', () => {
      // First populate with all options
      populatePaymentSelect(selectElement, 'aws', 'ec2');
      selectElement.value = 'all-upfront';

      // Repopulate - should preserve selection
      populatePaymentSelect(selectElement, 'aws', 'ec2', 1);

      expect(selectElement.value).toBe('all-upfront');
    });

    it('should not preserve selection if no longer valid', () => {
      // First populate with all options and select no-upfront
      populatePaymentSelect(selectElement, 'aws', 'rds');
      selectElement.value = 'no-upfront';

      // Repopulate with 3-year restriction
      populatePaymentSelect(selectElement, 'aws', 'rds', 3);

      // Value should be first available option
      expect(selectElement.value).toBe('partial-upfront');
    });

    it('should handle Azure provider', () => {
      populatePaymentSelect(selectElement, 'azure', 'vm');

      expect(selectElement.options).toHaveLength(2);
      expect(selectElement.options[0]!.value).toBe('upfront');
      expect(selectElement.options[1]!.value).toBe('monthly');
    });

    it('should handle GCP provider', () => {
      populatePaymentSelect(selectElement, 'gcp', 'compute');

      expect(selectElement.options).toHaveLength(1);
      expect(selectElement.options[0]!.value).toBe('monthly');
    });

    it('should clear existing options before populating', () => {
      // Add some initial options
      selectElement.innerHTML = '<option value="old">Old Option</option>';

      populatePaymentSelect(selectElement, 'aws', 'ec2');

      expect(selectElement.options).toHaveLength(3);
      expect(selectElement.querySelector('option[value="old"]')).toBeNull();
    });

    it('should handle undefined service', () => {
      populatePaymentSelect(selectElement, 'aws');

      expect(selectElement.options).toHaveLength(3);
    });

    it('should handle 1-year term with all options available', () => {
      populatePaymentSelect(selectElement, 'aws', 'rds', 1);

      // 1-year has no restrictions
      expect(selectElement.options).toHaveLength(3);
    });
  });

  describe('getPaymentLabel', () => {
    describe('AWS payment values', () => {
      it('should return correct label for no-upfront', () => {
        expect(getPaymentLabel('no-upfront')).toBe('No Upfront');
      });

      it('should return correct label for partial-upfront', () => {
        expect(getPaymentLabel('partial-upfront')).toBe('Partial Upfront');
      });

      it('should return correct label for all-upfront', () => {
        expect(getPaymentLabel('all-upfront')).toBe('All Upfront');
      });
    });

    describe('Azure payment values', () => {
      it('should return correct label for upfront', () => {
        expect(getPaymentLabel('upfront')).toBe('Pay Upfront');
      });

      it('should return correct label for monthly', () => {
        // Azure 'monthly' label is 'Pay Monthly' (comes before GCP in the array)
        expect(getPaymentLabel('monthly')).toBe('Pay Monthly');
      });
    });

    describe('GCP payment values', () => {
      it('should note that monthly matches Azure first due to array order', () => {
        // Note: GCP monthly has same value as Azure but Azure comes first in the array
        // So 'monthly' returns Azure's 'Pay Monthly' label, not GCP's 'Monthly'
        // This is a quirk of the implementation - testing actual behavior
        expect(getPaymentLabel('monthly')).toBe('Pay Monthly');
      });
    });

    describe('Unknown values', () => {
      it('should return the value itself for unknown payment type', () => {
        expect(getPaymentLabel('unknown-payment')).toBe('unknown-payment');
      });

      it('should return empty string for empty input', () => {
        expect(getPaymentLabel('')).toBe('');
      });
    });
  });

  describe('normalizePaymentValue', () => {
    describe('Azure normalization', () => {
      it('should convert all-upfront to upfront for Azure', () => {
        expect(normalizePaymentValue('all-upfront', 'azure')).toBe('upfront');
      });

      it('should convert partial-upfront to upfront for Azure', () => {
        expect(normalizePaymentValue('partial-upfront', 'azure')).toBe('upfront');
      });

      it('should convert no-upfront to monthly for Azure', () => {
        expect(normalizePaymentValue('no-upfront', 'azure')).toBe('monthly');
      });

      it('should preserve native Azure values', () => {
        expect(normalizePaymentValue('upfront', 'azure')).toBe('upfront');
        expect(normalizePaymentValue('monthly', 'azure')).toBe('monthly');
      });
    });

    describe('GCP normalization', () => {
      it('should convert all values to monthly for GCP', () => {
        expect(normalizePaymentValue('no-upfront', 'gcp')).toBe('monthly');
        expect(normalizePaymentValue('partial-upfront', 'gcp')).toBe('monthly');
        expect(normalizePaymentValue('all-upfront', 'gcp')).toBe('monthly');
        expect(normalizePaymentValue('monthly', 'gcp')).toBe('monthly');
        expect(normalizePaymentValue('any-value', 'gcp')).toBe('monthly');
      });
    });

    describe('AWS normalization', () => {
      it('should preserve AWS values unchanged', () => {
        expect(normalizePaymentValue('no-upfront', 'aws')).toBe('no-upfront');
        expect(normalizePaymentValue('partial-upfront', 'aws')).toBe('partial-upfront');
        expect(normalizePaymentValue('all-upfront', 'aws')).toBe('all-upfront');
      });
    });

    describe('Unknown provider', () => {
      it('should preserve value for unknown provider', () => {
        expect(normalizePaymentValue('some-value', 'unknown')).toBe('some-value');
      });
    });
  });

  describe('Type exports', () => {
    it('should export CommitmentConfig interface', () => {
      const config: CommitmentConfig = {
        terms: [{ value: 1, label: '1 Year' }],
        payments: [{ value: 'monthly', label: 'Monthly' }]
      };
      expect(config.terms).toHaveLength(1);
      expect(config.payments).toHaveLength(1);
    });

    it('should export PaymentOption interface', () => {
      const option: PaymentOption = { value: 'test', label: 'Test' };
      expect(option.value).toBe('test');
      expect(option.label).toBe('Test');
    });

    it('should export TermOption interface', () => {
      const option: TermOption = { value: 1, label: '1 Year' };
      expect(option.value).toBe(1);
      expect(option.label).toBe('1 Year');
    });

    it('should support optional invalidCombinations in CommitmentConfig', () => {
      const configWithoutInvalid: CommitmentConfig = {
        terms: [{ value: 1, label: '1 Year' }],
        payments: [{ value: 'monthly', label: 'Monthly' }]
      };

      const configWithInvalid: CommitmentConfig = {
        terms: [{ value: 1, label: '1 Year' }],
        payments: [{ value: 'monthly', label: 'Monthly' }],
        invalidCombinations: [{ term: 3, payment: 'no-upfront' }]
      };

      expect(configWithoutInvalid.invalidCombinations).toBeUndefined();
      expect(configWithInvalid.invalidCombinations).toHaveLength(1);
    });
  });
});
