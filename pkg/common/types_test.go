package common

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestProviderType_String(t *testing.T) {
	t.Parallel()
	tests := []struct {
		provider ProviderType
		expected string
	}{
		{ProviderAWS, "aws"},
		{ProviderAzure, "azure"},
		{ProviderGCP, "gcp"},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(string(tt.provider), func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.expected, tt.provider.String())
		})
	}
}

func TestServiceType_String(t *testing.T) {
	t.Parallel()
	tests := []struct {
		service  ServiceType
		expected string
	}{
		{ServiceCompute, "compute"},
		{ServiceRelationalDB, "relational-db"},
		{ServiceNoSQL, "nosql"},
		{ServiceCache, "cache"},
		{ServiceSearch, "search"},
		{ServiceDataWarehouse, "data-warehouse"},
		{ServiceStorage, "storage"},
		{ServiceSavingsPlans, "savings-plans"},
		{ServiceCommitments, "commitments"},
		{ServiceOther, "other"},
		{ServiceEC2, "ec2"},
		{ServiceRDS, "rds"},
		{ServiceElastiCache, "elasticache"},
		{ServiceOpenSearch, "opensearch"},
		{ServiceRedshift, "redshift"},
		{ServiceMemoryDB, "memorydb"},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(string(tt.service), func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.expected, tt.service.String())
		})
	}
}

func TestCommitmentType_String(t *testing.T) {
	t.Parallel()
	tests := []struct {
		commitment CommitmentType
		expected   string
	}{
		{CommitmentReservedInstance, "reserved-instance"},
		{CommitmentSavingsPlan, "savings-plan"},
		{CommitmentCUD, "committed-use"},
		{CommitmentReservedCapacity, "reserved-capacity"},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(string(tt.commitment), func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.expected, tt.commitment.String())
		})
	}
}

func TestComputeDetails_GetServiceType(t *testing.T) {
	t.Parallel()
	details := ComputeDetails{
		InstanceType: "m5.large",
		Platform:     "linux",
		Tenancy:      "default",
		Scope:        "regional",
	}

	assert.Equal(t, ServiceCompute, details.GetServiceType())
}

func TestComputeDetails_GetDetailDescription(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		details  ComputeDetails
		expected string
	}{
		{
			name: "Linux default",
			details: ComputeDetails{
				Platform: "linux",
				Tenancy:  "default",
			},
			expected: "linux/default",
		},
		{
			name: "Windows dedicated",
			details: ComputeDetails{
				Platform: "windows",
				Tenancy:  "dedicated",
			},
			expected: "windows/dedicated",
		},
		{
			// vCPU alone is insufficient — both fields must be populated for
			// the size suffix to appear, otherwise we'd surface "16 vCPU /
			// 0 GB" which is misleading.
			name: "VCPU populated but MemoryGB zero — base description only",
			details: ComputeDetails{
				Platform: "linux",
				Tenancy:  "default",
				VCPU:     16,
			},
			expected: "linux/default",
		},
		{
			// Symmetric guard for the MemoryGB-only case.
			name: "MemoryGB populated but VCPU zero — base description only",
			details: ComputeDetails{
				Platform: "linux",
				Tenancy:  "default",
				MemoryGB: 32,
			},
			expected: "linux/default",
		},
		{
			// Whole-number GB renders without trailing zeros (16 GB,
			// not 16.000000 GB).
			name: "Both fields populated — integer memory",
			details: ComputeDetails{
				Platform: "linux",
				Tenancy:  "default",
				VCPU:     4,
				MemoryGB: 16,
			},
			expected: "linux/default (4 vCPU / 16 GB)",
		},
		{
			// Fractional GB (Azure has 0.5 GB SKUs) renders verbatim.
			name: "Both fields populated — fractional memory",
			details: ComputeDetails{
				Platform: "linux",
				Tenancy:  "default",
				VCPU:     1,
				MemoryGB: 0.5,
			},
			expected: "linux/default (1 vCPU / 0.5 GB)",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.expected, tt.details.GetDetailDescription())
		})
	}
}

func TestDatabaseDetails_GetServiceType(t *testing.T) {
	t.Parallel()
	details := DatabaseDetails{
		Engine:   "mysql",
		AZConfig: "multi-az",
	}

	assert.Equal(t, ServiceRelationalDB, details.GetServiceType())
}

func TestDatabaseDetails_GetDetailDescription(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		details  DatabaseDetails
		expected string
	}{
		{
			name: "MySQL multi-az",
			details: DatabaseDetails{
				Engine:   "mysql",
				AZConfig: "multi-az",
			},
			expected: "mysql/multi-az",
		},
		{
			name: "PostgreSQL single-az",
			details: DatabaseDetails{
				Engine:   "postgres",
				AZConfig: "single-az",
			},
			expected: "postgres/single-az",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.expected, tt.details.GetDetailDescription())
		})
	}
}

func TestCacheDetails_GetServiceType(t *testing.T) {
	t.Parallel()
	details := CacheDetails{
		Engine:   "redis",
		NodeType: "cache.r6g.large",
	}

	assert.Equal(t, ServiceCache, details.GetServiceType())
}

func TestCacheDetails_GetDetailDescription(t *testing.T) {
	t.Parallel()
	details := CacheDetails{
		Engine:   "redis",
		NodeType: "cache.r6g.large",
	}

	assert.Equal(t, "redis/cache.r6g.large", details.GetDetailDescription())
}

func TestSearchDetails_GetServiceType(t *testing.T) {
	t.Parallel()
	details := SearchDetails{
		InstanceType: "r5.large.search",
	}

	assert.Equal(t, ServiceSearch, details.GetServiceType())
}

func TestSearchDetails_GetDetailDescription(t *testing.T) {
	t.Parallel()
	details := SearchDetails{
		InstanceType: "r5.large.search",
	}

	assert.Equal(t, "r5.large.search", details.GetDetailDescription())
}

func TestDataWarehouseDetails_GetServiceType(t *testing.T) {
	t.Parallel()
	details := DataWarehouseDetails{
		NodeType:      "dc2.large",
		NumberOfNodes: 3,
	}

	assert.Equal(t, ServiceDataWarehouse, details.GetServiceType())
}

func TestDataWarehouseDetails_GetDetailDescription(t *testing.T) {
	t.Parallel()
	details := DataWarehouseDetails{
		NodeType:      "dc2.large",
		NumberOfNodes: 3,
	}

	assert.Equal(t, "dc2.large", details.GetDetailDescription())
}

func TestSavingsPlanDetails_GetServiceType(t *testing.T) {
	t.Parallel()
	details := SavingsPlanDetails{
		PlanType:         "Compute",
		HourlyCommitment: 10.50,
	}

	assert.Equal(t, ServiceSavingsPlans, details.GetServiceType())
}

func TestSavingsPlanDetails_GetDetailDescription(t *testing.T) {
	t.Parallel()
	details := SavingsPlanDetails{
		PlanType: "Compute",
	}

	assert.Equal(t, "Compute", details.GetDetailDescription())
}

func TestRecommendation_Struct(t *testing.T) {
	t.Parallel()
	rec := Recommendation{
		Provider:          ProviderAWS,
		Account:           "123456789012",
		AccountName:       "prod",
		Service:           ServiceRDS,
		Region:            "us-east-1",
		ResourceType:      "db.t3.medium",
		Count:             2,
		CommitmentType:    CommitmentReservedInstance,
		Term:              "1yr",
		PaymentOption:     "all-upfront",
		OnDemandCost:      1000.0,
		CommitmentCost:    600.0,
		EstimatedSavings:  400.0,
		SavingsPercentage: 40.0,
	}

	assert.Equal(t, ProviderAWS, rec.Provider)
	assert.Equal(t, "123456789012", rec.Account)
	assert.Equal(t, ServiceRDS, rec.Service)
	assert.Equal(t, 2, rec.Count)
	assert.Equal(t, 40.0, rec.SavingsPercentage)
}

func TestPurchaseResult_Struct(t *testing.T) {
	t.Parallel()
	result := PurchaseResult{
		Success:      true,
		CommitmentID: "ri-12345",
		Cost:         600.0,
		DryRun:       false,
	}

	assert.True(t, result.Success)
	assert.Equal(t, "ri-12345", result.CommitmentID)
	assert.Equal(t, 600.0, result.Cost)
	assert.False(t, result.DryRun)
}

func TestCommitment_Struct(t *testing.T) {
	t.Parallel()
	commitment := Commitment{
		Provider:       ProviderAWS,
		Account:        "123456789012",
		CommitmentID:   "ri-12345",
		CommitmentType: CommitmentReservedInstance,
		Service:        ServiceRDS,
		Region:         "us-east-1",
		ResourceType:   "db.t3.medium",
		Count:          2,
		State:          "active",
	}

	assert.Equal(t, ProviderAWS, commitment.Provider)
	assert.Equal(t, "ri-12345", commitment.CommitmentID)
	assert.Equal(t, "active", commitment.State)
}

func TestOfferingDetails_Struct(t *testing.T) {
	t.Parallel()
	offering := OfferingDetails{
		OfferingID:          "offering-123",
		ResourceType:        "db.t3.medium",
		Term:                "1yr",
		PaymentOption:       "all-upfront",
		UpfrontCost:         500.0,
		RecurringCost:       0.0,
		TotalCost:           500.0,
		EffectiveHourlyRate: 0.057,
		Currency:            "USD",
	}

	assert.Equal(t, "offering-123", offering.OfferingID)
	assert.Equal(t, 500.0, offering.TotalCost)
	assert.Equal(t, "USD", offering.Currency)
}

func TestRecommendationParams_Struct(t *testing.T) {
	t.Parallel()
	params := RecommendationParams{
		Service:        ServiceRDS,
		Region:         "us-east-1",
		LookbackPeriod: "30d",
		Term:           "1yr",
		PaymentOption:  "all-upfront",
		AccountFilter:  []string{"123456789012"},
		IncludeRegions: []string{"us-east-1", "us-west-2"},
		ExcludeRegions: []string{"eu-west-1"},
	}

	assert.Equal(t, ServiceRDS, params.Service)
	assert.Equal(t, "30d", params.LookbackPeriod)
	assert.Len(t, params.IncludeRegions, 2)
}

func TestAccount_Struct(t *testing.T) {
	t.Parallel()
	account := Account{
		Provider:    ProviderAWS,
		ID:          "123456789012",
		Name:        "prod-account",
		DisplayName: "Production Account",
		IsDefault:   true,
	}

	assert.Equal(t, ProviderAWS, account.Provider)
	assert.Equal(t, "123456789012", account.ID)
	assert.True(t, account.IsDefault)
}

func TestRegion_Struct(t *testing.T) {
	t.Parallel()
	region := Region{
		Provider:    ProviderAWS,
		ID:          "us-east-1",
		Name:        "us-east-1",
		DisplayName: "US East (N. Virginia)",
	}

	assert.Equal(t, ProviderAWS, region.Provider)
	assert.Equal(t, "us-east-1", region.ID)
	assert.Equal(t, "US East (N. Virginia)", region.DisplayName)
}

func TestNormalizeSource(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		in      string
		want    string
		wantErr bool
	}{
		{"cli lowercase", "cudly-cli", "cudly-cli", false},
		{"web lowercase", "cudly-web", "cudly-web", false},
		{"cli mixed case", "CUDly-CLI", "cudly-cli", false},
		{"web mixed case", "CUDly-Web", "cudly-web", false},
		{"cli with whitespace", "  cudly-cli\n", "cudly-cli", false},
		{"empty string", "", "", true},
		{"whitespace only", "   ", "", true},
		{"unknown source", "cudly-api", "", true},
		{"injection attempt", "cudly-cli; DROP TABLE", "", true},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := NormalizeSource(tc.in)
			if tc.wantErr {
				assert.Error(t, err)
				return
			}
			assert.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}
