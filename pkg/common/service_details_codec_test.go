package common

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMarshalServiceDetails_NilAndPointers confirms the contract that:
//   - nil interface  → (nil, nil)
//   - non-nil value  → JSON bytes containing the fields
//   - typed nil ptr  → (nil, nil) (json.Marshal of a nil pointer emits
//     "null"; the helper folds that to a nil RawMessage so callers can
//     treat absent and null identically).
func TestMarshalServiceDetails_NilAndPointers(t *testing.T) {
	b, err := MarshalServiceDetails(nil)
	require.NoError(t, err)
	assert.Nil(t, b, "nil interface must produce nil RawMessage")

	var nilCompute *ComputeDetails
	b, err = MarshalServiceDetails(nilCompute)
	require.NoError(t, err)
	assert.Nil(t, b, "typed nil pointer must produce nil RawMessage")

	b, err = MarshalServiceDetails(&ComputeDetails{InstanceType: "m5.large", Platform: "Windows"})
	require.NoError(t, err)
	assert.NotEmpty(t, b)
	assert.Contains(t, string(b), `"platform":"Windows"`)
}

// TestDecodeServiceDetailsFor_RoundTrip is the core round-trip regression
// guard for issue #453: a Windows EC2 rec must come back out as Windows,
// not silently default to Linux/UNIX.
func TestDecodeServiceDetailsFor_RoundTrip(t *testing.T) {
	cases := []struct {
		name    string
		service string
		in      ServiceDetails
		// assert inspects the typed pointer returned by decode and verifies
		// the per-service fields round-tripped.
		assert func(t *testing.T, d ServiceDetails)
	}{
		{
			name:    "ec2_windows_dedicated",
			service: string(ServiceEC2),
			in:      &ComputeDetails{InstanceType: "m5.large", Platform: "Windows", Tenancy: "dedicated", Scope: "Region", VCPU: 2, MemoryGB: 8},
			assert: func(t *testing.T, d ServiceDetails) {
				cd, ok := d.(*ComputeDetails)
				if assert.True(t, ok, "want *ComputeDetails, got %T", d) {
					assert.Equal(t, "Windows", cd.Platform)
					assert.Equal(t, "dedicated", cd.Tenancy)
					assert.Equal(t, "Region", cd.Scope)
					assert.Equal(t, 2, cd.VCPU)
					assert.InDelta(t, 8.0, cd.MemoryGB, 0.001)
				}
			},
		},
		{
			name:    "rds_postgres_multiaz",
			service: string(ServiceRDS),
			in:      &DatabaseDetails{Engine: "postgres", EngineVersion: "15", AZConfig: "multi-az", InstanceClass: "db.r5.large"},
			assert: func(t *testing.T, d ServiceDetails) {
				dd, ok := d.(*DatabaseDetails)
				if assert.True(t, ok, "want *DatabaseDetails, got %T", d) {
					assert.Equal(t, "postgres", dd.Engine)
					assert.Equal(t, "15", dd.EngineVersion)
					assert.Equal(t, "multi-az", dd.AZConfig)
				}
			},
		},
		{
			name:    "elasticache_redis",
			service: string(ServiceElastiCache),
			in:      &CacheDetails{Engine: "redis", NodeType: "cache.r5.large", Shards: 3},
			assert: func(t *testing.T, d ServiceDetails) {
				cd, ok := d.(*CacheDetails)
				if assert.True(t, ok, "want *CacheDetails, got %T", d) {
					assert.Equal(t, "redis", cd.Engine)
					assert.Equal(t, 3, cd.Shards)
				}
			},
		},
		{
			name:    "savings_plans_compute",
			service: string(ServiceSavingsPlansCompute),
			in:      &SavingsPlanDetails{PlanType: "Compute", HourlyCommitment: 1.5, Coverage: "ec2"},
			assert: func(t *testing.T, d ServiceDetails) {
				sp, ok := d.(*SavingsPlanDetails)
				if assert.True(t, ok, "want *SavingsPlanDetails, got %T", d) {
					assert.Equal(t, "Compute", sp.PlanType)
					assert.InDelta(t, 1.5, sp.HourlyCommitment, 0.001)
				}
			},
		},
		{
			name:    "opensearch",
			service: string(ServiceOpenSearch),
			in:      &SearchDetails{InstanceType: "r5.large.search", MasterNodeCount: 3, MasterNodeType: "m6g.large.search"},
			assert: func(t *testing.T, d ServiceDetails) {
				sd, ok := d.(*SearchDetails)
				if assert.True(t, ok, "want *SearchDetails, got %T", d) {
					assert.Equal(t, "r5.large.search", sd.InstanceType)
					assert.Equal(t, 3, sd.MasterNodeCount)
				}
			},
		},
		{
			name:    "redshift",
			service: string(ServiceRedshift),
			in:      &DataWarehouseDetails{NodeType: "ra3.xlplus", NumberOfNodes: 4, ClusterType: "multi-node"},
			assert: func(t *testing.T, d ServiceDetails) {
				dw, ok := d.(*DataWarehouseDetails)
				if assert.True(t, ok, "want *DataWarehouseDetails, got %T", d) {
					assert.Equal(t, "ra3.xlplus", dw.NodeType)
					assert.Equal(t, 4, dw.NumberOfNodes)
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			blob, err := MarshalServiceDetails(tc.in)
			require.NoError(t, err)
			require.NotEmpty(t, blob)

			out, err := DecodeServiceDetailsFor(tc.service, blob)
			require.NoError(t, err)
			require.NotNil(t, out)
			tc.assert(t, out)
		})
	}
}

// TestDecodeServiceDetailsFor_LegacyEmpty pins the documented fallback
// behaviour: an empty payload on a service that needs typed Details
// yields a zero-valued typed pointer (so the cloud client's type-
// assertion succeeds and buildOfferingFilters can substitute defaults).
func TestDecodeServiceDetailsFor_LegacyEmpty(t *testing.T) {
	for _, svc := range []string{
		string(ServiceEC2),
		string(ServiceRDS),
		string(ServiceElastiCache),
		string(ServiceSavingsPlansAll),
		string(ServiceSavingsPlansCompute),
		string(ServiceOpenSearch),
		string(ServiceRedshift),
	} {
		t.Run(svc+"_nil_raw", func(t *testing.T) {
			out, err := DecodeServiceDetailsFor(svc, nil)
			require.NoError(t, err)
			assert.NotNil(t, out, "empty raw on a known service must yield a typed zero pointer")
		})
		t.Run(svc+"_explicit_null", func(t *testing.T) {
			out, err := DecodeServiceDetailsFor(svc, json.RawMessage(`null`))
			require.NoError(t, err)
			assert.NotNil(t, out, "explicit JSON null must be treated like an empty payload")
		})
	}
}

// TestDecodeServiceDetailsFor_UnknownService confirms that an unknown
// service slug returns (nil, nil) rather than erroring — callers can
// then fall through to their existing nil-Details paths.
func TestDecodeServiceDetailsFor_UnknownService(t *testing.T) {
	out, err := DecodeServiceDetailsFor("not-a-real-service", nil)
	require.NoError(t, err)
	assert.Nil(t, out)

	out, err = DecodeServiceDetailsFor("not-a-real-service", json.RawMessage(`{"foo":"bar"}`))
	require.NoError(t, err)
	assert.Nil(t, out, "non-empty payload on unknown service must NOT raise; rolling deploys can produce this shape")
}

// TestDecodeServiceDetailsFor_MalformedJSON ensures a malformed payload
// surfaces as an error rather than silently producing a zero-valued
// Details (which is how #453 hid for a release).
func TestDecodeServiceDetailsFor_MalformedJSON(t *testing.T) {
	_, err := DecodeServiceDetailsFor(string(ServiceEC2), json.RawMessage(`{not json}`))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unmarshal")
}

// TestDecodeServiceDetailsFor_LegacyDashFormSP keeps the legacy
// "savings-plans" umbrella alias decoding to *SavingsPlanDetails so a
// purchase_execution row persisted under the dash form before PR #94
// still resolves correctly when re-executed.
func TestDecodeServiceDetailsFor_LegacyDashFormSP(t *testing.T) {
	in := &SavingsPlanDetails{PlanType: "Compute", HourlyCommitment: 1.0}
	blob, err := MarshalServiceDetails(in)
	require.NoError(t, err)

	out, err := DecodeServiceDetailsFor("savings-plans", blob)
	require.NoError(t, err)
	sp, ok := out.(*SavingsPlanDetails)
	require.True(t, ok, "got %T", out)
	assert.Equal(t, "Compute", sp.PlanType)
}
