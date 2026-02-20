/**
 * Commitment options configuration for different providers and services
 *
 * This module defines which payment and term options are available
 * for each cloud provider and service combination.
 */

export interface PaymentOption {
  value: string;
  label: string;
}

export interface TermOption {
  value: number;
  label: string;
}

export interface CommitmentConfig {
  terms: TermOption[];
  payments: PaymentOption[];
  // Some combinations are invalid (e.g., RDS 3yr no-upfront)
  invalidCombinations?: Array<{ term: number; payment: string }>;
}

// AWS Payment Options
const AWS_PAYMENTS: PaymentOption[] = [
  { value: 'no-upfront', label: 'No Upfront' },
  { value: 'partial-upfront', label: 'Partial Upfront' },
  { value: 'all-upfront', label: 'All Upfront' }
];

// Azure Payment Options
const AZURE_PAYMENTS: PaymentOption[] = [
  { value: 'upfront', label: 'Pay Upfront' },
  { value: 'monthly', label: 'Pay Monthly' }
];

// GCP Payment Options (no upfront concept - just monthly billing)
const GCP_PAYMENTS: PaymentOption[] = [
  { value: 'monthly', label: 'Monthly' }
];

// Standard term options
const STANDARD_TERMS: TermOption[] = [
  { value: 1, label: '1 Year' },
  { value: 3, label: '3 Years' }
];

// Provider/Service specific configurations
const commitmentConfigs: Record<string, Record<string, CommitmentConfig>> = {
  aws: {
    // EC2 - all options available
    ec2: {
      terms: STANDARD_TERMS,
      payments: AWS_PAYMENTS
    },
    // Savings Plans - all options available
    'savings-plans': {
      terms: STANDARD_TERMS,
      payments: AWS_PAYMENTS
    },
    // RDS - no 3-year no-upfront option
    rds: {
      terms: STANDARD_TERMS,
      payments: AWS_PAYMENTS,
      invalidCombinations: [
        { term: 3, payment: 'no-upfront' }
      ]
    },
    // ElastiCache - no 3-year no-upfront option
    elasticache: {
      terms: STANDARD_TERMS,
      payments: AWS_PAYMENTS,
      invalidCombinations: [
        { term: 3, payment: 'no-upfront' }
      ]
    },
    // OpenSearch - no 3-year no-upfront option
    opensearch: {
      terms: STANDARD_TERMS,
      payments: AWS_PAYMENTS,
      invalidCombinations: [
        { term: 3, payment: 'no-upfront' }
      ]
    },
    // Redshift - no 3-year no-upfront option
    redshift: {
      terms: STANDARD_TERMS,
      payments: AWS_PAYMENTS,
      invalidCombinations: [
        { term: 3, payment: 'no-upfront' }
      ]
    },
    // MemoryDB - no 3-year no-upfront option
    memorydb: {
      terms: STANDARD_TERMS,
      payments: AWS_PAYMENTS,
      invalidCombinations: [
        { term: 3, payment: 'no-upfront' }
      ]
    },
    // Default for AWS services not specifically configured
    _default: {
      terms: STANDARD_TERMS,
      payments: AWS_PAYMENTS
    }
  },
  azure: {
    // All Azure services use the same options
    _default: {
      terms: STANDARD_TERMS,
      payments: AZURE_PAYMENTS
    }
  },
  gcp: {
    // All GCP services use the same options (monthly only)
    _default: {
      terms: STANDARD_TERMS,
      payments: GCP_PAYMENTS
    }
  }
};

// Default fallback configuration
const DEFAULT_CONFIG: CommitmentConfig = {
  terms: STANDARD_TERMS,
  payments: AWS_PAYMENTS
};

/**
 * Get commitment configuration for a provider/service combination
 */
export function getCommitmentConfig(provider: string, service?: string): CommitmentConfig {
  const providerConfigs = commitmentConfigs[provider.toLowerCase()];

  if (!providerConfigs) {
    // Unknown provider, return default
    return DEFAULT_CONFIG;
  }

  if (service) {
    const serviceConfig = providerConfigs[service.toLowerCase()];
    if (serviceConfig) {
      return serviceConfig;
    }
  }

  return providerConfigs._default ?? DEFAULT_CONFIG;
}

/**
 * Check if a term/payment combination is valid for a provider/service
 */
export function isValidCombination(provider: string, service: string | undefined, term: number, payment: string): boolean {
  const config = getCommitmentConfig(provider, service);

  if (!config.invalidCombinations) {
    return true;
  }

  return !config.invalidCombinations.some(
    combo => combo.term === term && combo.payment === payment
  );
}

/**
 * Get valid payment options for a given provider/service/term combination
 */
export function getValidPaymentOptions(provider: string, service: string | undefined, term: number): PaymentOption[] {
  const config = getCommitmentConfig(provider, service);

  if (!config.invalidCombinations) {
    return config.payments;
  }

  return config.payments.filter(
    payment => !config.invalidCombinations?.some(
      combo => combo.term === term && combo.payment === payment.value
    )
  );
}

/**
 * Get valid term options for a given provider/service/payment combination
 */
export function getValidTermOptions(provider: string, service: string | undefined, payment: string): TermOption[] {
  const config = getCommitmentConfig(provider, service);

  if (!config.invalidCombinations) {
    return config.terms;
  }

  return config.terms.filter(
    term => !config.invalidCombinations?.some(
      combo => combo.term === term.value && combo.payment === payment
    )
  );
}

/**
 * Populate a select element with term options
 */
export function populateTermSelect(
  selectElement: HTMLSelectElement,
  provider: string,
  service?: string,
  selectedPayment?: string
): void {
  const options = selectedPayment
    ? getValidTermOptions(provider, service, selectedPayment)
    : getCommitmentConfig(provider, service).terms;

  const currentValue = selectElement.value;
  selectElement.innerHTML = options
    .map(opt => `<option value="${opt.value}">${opt.label}</option>`)
    .join('');

  // Try to preserve current selection
  if (options.some(opt => String(opt.value) === currentValue)) {
    selectElement.value = currentValue;
  }
}

/**
 * Populate a select element with payment options
 */
export function populatePaymentSelect(
  selectElement: HTMLSelectElement,
  provider: string,
  service?: string,
  selectedTerm?: number
): void {
  const options = selectedTerm !== undefined
    ? getValidPaymentOptions(provider, service, selectedTerm)
    : getCommitmentConfig(provider, service).payments;

  const currentValue = selectElement.value;
  selectElement.innerHTML = options
    .map(opt => `<option value="${opt.value}">${opt.label}</option>`)
    .join('');

  // Try to preserve current selection
  if (options.some(opt => opt.value === currentValue)) {
    selectElement.value = currentValue;
  }
}

/**
 * Get display name for a payment option value
 */
export function getPaymentLabel(value: string): string {
  const allPayments = [...AWS_PAYMENTS, ...AZURE_PAYMENTS, ...GCP_PAYMENTS];
  const payment = allPayments.find(p => p.value === value);
  return payment?.label ?? value;
}

/**
 * Map legacy AWS payment values to display labels
 */
export function normalizePaymentValue(value: string, provider: string): string {
  // Handle legacy values or cross-provider values
  if (provider === 'azure') {
    if (value === 'all-upfront' || value === 'partial-upfront') {
      return 'upfront';
    }
    if (value === 'no-upfront') {
      return 'monthly';
    }
  } else if (provider === 'gcp') {
    // GCP only has monthly
    return 'monthly';
  }
  return value;
}
