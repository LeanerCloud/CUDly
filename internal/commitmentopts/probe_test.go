package commitmentopts

import (
	"context"
	"errors"
	"sort"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/elasticache"
	elasticachetypes "github.com/aws/aws-sdk-go-v2/service/elasticache/types"
	"github.com/aws/aws-sdk-go-v2/service/memorydb"
	memorydbtypes "github.com/aws/aws-sdk-go-v2/service/memorydb/types"
	"github.com/aws/aws-sdk-go-v2/service/opensearch"
	opensearchtypes "github.com/aws/aws-sdk-go-v2/service/opensearch/types"
	"github.com/aws/aws-sdk-go-v2/service/rds"
	rdstypes "github.com/aws/aws-sdk-go-v2/service/rds/types"
	"github.com/aws/aws-sdk-go-v2/service/redshift"
	redshifttypes "github.com/aws/aws-sdk-go-v2/service/redshift/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// sortCombos gives tests a stable comparison order — probers dedupe by
// map keys so iteration order is otherwise nondeterministic.
func sortCombos(c []Combo) []Combo {
	sort.Slice(c, func(i, j int) bool {
		if c[i].TermYears != c[j].TermYears {
			return c[i].TermYears < c[j].TermYears
		}
		return c[i].Payment < c[j].Payment
	})
	return c
}

// ---------------------------------------------------------------------------
// RDS
// ---------------------------------------------------------------------------

type fakeRDS struct {
	fn func(*rds.DescribeReservedDBInstancesOfferingsInput) (*rds.DescribeReservedDBInstancesOfferingsOutput, error)
}

func (f *fakeRDS) DescribeReservedDBInstancesOfferings(ctx context.Context, in *rds.DescribeReservedDBInstancesOfferingsInput, _ ...func(*rds.Options)) (*rds.DescribeReservedDBInstancesOfferingsOutput, error) {
	return f.fn(in)
}

func TestRDSProber_Probe(t *testing.T) {
	fake := &fakeRDS{
		fn: func(in *rds.DescribeReservedDBInstancesOfferingsInput) (*rds.DescribeReservedDBInstancesOfferingsOutput, error) {
			assert.Equal(t, probeTargetRDS, aws.ToString(in.DBInstanceClass))
			return &rds.DescribeReservedDBInstancesOfferingsOutput{
				ReservedDBInstancesOfferings: []rdstypes.ReservedDBInstancesOffering{
					{Duration: aws.Int32(31536000), OfferingType: aws.String("All Upfront")},
					{Duration: aws.Int32(31536000), OfferingType: aws.String("Partial Upfront")},
					{Duration: aws.Int32(31536000), OfferingType: aws.String("No Upfront")},
					{Duration: aws.Int32(94608000), OfferingType: aws.String("All Upfront")},
					// dup of a 1yr All Upfront — must collapse
					{Duration: aws.Int32(31536000), OfferingType: aws.String("All Upfront")},
					// anomalies
					{Duration: aws.Int32(94608000), OfferingType: aws.String("Light Utilization")},
					{Duration: aws.Int32(5 * 31536000), OfferingType: aws.String("All Upfront")},
					{Duration: aws.Int32(31536000), OfferingType: aws.String("Mystery Option")},
				},
			}, nil
		},
	}
	p := &RDSProber{NewClient: func(cfg aws.Config) RDSDescribeOfferings { return fake }}

	assert.Equal(t, "rds", p.Service())

	got, err := p.Probe(context.Background(), aws.Config{})
	require.NoError(t, err)
	got = sortCombos(got)
	want := []Combo{
		{Provider: "aws", Service: "rds", TermYears: 1, Payment: "all-upfront"},
		{Provider: "aws", Service: "rds", TermYears: 1, Payment: "no-upfront"},
		{Provider: "aws", Service: "rds", TermYears: 1, Payment: "partial-upfront"},
		{Provider: "aws", Service: "rds", TermYears: 3, Payment: "all-upfront"},
	}
	assert.Equal(t, want, got)
}

func TestRDSProber_ErrorPropagates(t *testing.T) {
	boom := errors.New("boom")
	fake := &fakeRDS{fn: func(*rds.DescribeReservedDBInstancesOfferingsInput) (*rds.DescribeReservedDBInstancesOfferingsOutput, error) {
		return nil, boom
	}}
	p := &RDSProber{NewClient: func(cfg aws.Config) RDSDescribeOfferings { return fake }}
	_, err := p.Probe(context.Background(), aws.Config{})
	require.Error(t, err)
	assert.ErrorIs(t, err, boom)
}

func TestRDSProber_PageCap(t *testing.T) {
	// Integration-level check that the RDS prober honours the page cap
	// when wired through walkPaginated. The cap itself is exercised in
	// detail by TestWalkPaginated_StopsAtPageCap; this test guards the
	// wiring (RDS uses Marker rather than NextToken) so a refactor that
	// stops threading the marker can't silently break only RDS.
	calls := 0
	fake := &fakeRDS{fn: func(*rds.DescribeReservedDBInstancesOfferingsInput) (*rds.DescribeReservedDBInstancesOfferingsOutput, error) {
		calls++
		return &rds.DescribeReservedDBInstancesOfferingsOutput{
			Marker: aws.String("more"),
		}, nil
	}}
	p := &RDSProber{NewClient: func(cfg aws.Config) RDSDescribeOfferings { return fake }}
	_, err := p.Probe(context.Background(), aws.Config{})
	require.NoError(t, err)
	assert.Equal(t, maxPages, calls)
}

// ---------------------------------------------------------------------------
// ElastiCache
// ---------------------------------------------------------------------------

type fakeElastiCache struct {
	fn func(*elasticache.DescribeReservedCacheNodesOfferingsInput) (*elasticache.DescribeReservedCacheNodesOfferingsOutput, error)
}

func (f *fakeElastiCache) DescribeReservedCacheNodesOfferings(ctx context.Context, in *elasticache.DescribeReservedCacheNodesOfferingsInput, _ ...func(*elasticache.Options)) (*elasticache.DescribeReservedCacheNodesOfferingsOutput, error) {
	return f.fn(in)
}

func TestElastiCacheProber_Probe(t *testing.T) {
	fake := &fakeElastiCache{
		fn: func(in *elasticache.DescribeReservedCacheNodesOfferingsInput) (*elasticache.DescribeReservedCacheNodesOfferingsOutput, error) {
			assert.Equal(t, probeTargetElastiCache, aws.ToString(in.CacheNodeType))
			return &elasticache.DescribeReservedCacheNodesOfferingsOutput{
				ReservedCacheNodesOfferings: []elasticachetypes.ReservedCacheNodesOffering{
					{Duration: aws.Int32(31536000), OfferingType: aws.String("All Upfront")},
					{Duration: aws.Int32(94608000), OfferingType: aws.String("No Upfront")},
					// legacy utilization-style — must be dropped
					{Duration: aws.Int32(94608000), OfferingType: aws.String("Heavy Utilization")},
				},
			}, nil
		},
	}
	p := &ElastiCacheProber{NewClient: func(cfg aws.Config) ElastiCacheDescribeOfferings { return fake }}
	assert.Equal(t, "elasticache", p.Service())

	got, err := p.Probe(context.Background(), aws.Config{})
	require.NoError(t, err)
	got = sortCombos(got)
	want := []Combo{
		{Provider: "aws", Service: "elasticache", TermYears: 1, Payment: "all-upfront"},
		{Provider: "aws", Service: "elasticache", TermYears: 3, Payment: "no-upfront"},
	}
	assert.Equal(t, want, got)
}

// ---------------------------------------------------------------------------
// OpenSearch
// ---------------------------------------------------------------------------

type fakeOpenSearch struct {
	fn func(*opensearch.DescribeReservedInstanceOfferingsInput) (*opensearch.DescribeReservedInstanceOfferingsOutput, error)
}

func (f *fakeOpenSearch) DescribeReservedInstanceOfferings(ctx context.Context, in *opensearch.DescribeReservedInstanceOfferingsInput, _ ...func(*opensearch.Options)) (*opensearch.DescribeReservedInstanceOfferingsOutput, error) {
	return f.fn(in)
}

func TestOpenSearchProber_Probe(t *testing.T) {
	fake := &fakeOpenSearch{
		fn: func(in *opensearch.DescribeReservedInstanceOfferingsInput) (*opensearch.DescribeReservedInstanceOfferingsOutput, error) {
			return &opensearch.DescribeReservedInstanceOfferingsOutput{
				ReservedInstanceOfferings: []opensearchtypes.ReservedInstanceOffering{
					{
						InstanceType:  opensearchtypes.OpenSearchPartitionInstanceType(probeTargetOpenSearch),
						Duration:      31536000,
						PaymentOption: opensearchtypes.ReservedInstancePaymentOptionAllUpfront,
					},
					{
						InstanceType:  opensearchtypes.OpenSearchPartitionInstanceType(probeTargetOpenSearch),
						Duration:      94608000,
						PaymentOption: opensearchtypes.ReservedInstancePaymentOptionPartialUpfront,
					},
					// off-instance-type — must be filtered out client-side
					{
						InstanceType:  opensearchtypes.OpenSearchPartitionInstanceType("r6g.large.search"),
						Duration:      31536000,
						PaymentOption: opensearchtypes.ReservedInstancePaymentOptionNoUpfront,
					},
				},
			}, nil
		},
	}
	p := &OpenSearchProber{NewClient: func(cfg aws.Config) OpenSearchDescribeOfferings { return fake }}
	assert.Equal(t, "opensearch", p.Service())

	got, err := p.Probe(context.Background(), aws.Config{})
	require.NoError(t, err)
	got = sortCombos(got)
	want := []Combo{
		{Provider: "aws", Service: "opensearch", TermYears: 1, Payment: "all-upfront"},
		{Provider: "aws", Service: "opensearch", TermYears: 3, Payment: "partial-upfront"},
	}
	assert.Equal(t, want, got)
}

// ---------------------------------------------------------------------------
// Redshift
// ---------------------------------------------------------------------------

type fakeRedshift struct {
	fn func(*redshift.DescribeReservedNodeOfferingsInput) (*redshift.DescribeReservedNodeOfferingsOutput, error)
}

func (f *fakeRedshift) DescribeReservedNodeOfferings(ctx context.Context, in *redshift.DescribeReservedNodeOfferingsInput, _ ...func(*redshift.Options)) (*redshift.DescribeReservedNodeOfferingsOutput, error) {
	return f.fn(in)
}

func TestRedshiftProber_Probe(t *testing.T) {
	fake := &fakeRedshift{
		fn: func(in *redshift.DescribeReservedNodeOfferingsInput) (*redshift.DescribeReservedNodeOfferingsOutput, error) {
			return &redshift.DescribeReservedNodeOfferingsOutput{
				ReservedNodeOfferings: []redshifttypes.ReservedNodeOffering{
					{
						NodeType:     aws.String(probeTargetRedshift),
						Duration:     aws.Int32(31536000),
						OfferingType: aws.String("All Upfront"),
					},
					{
						NodeType:     aws.String(probeTargetRedshift),
						Duration:     aws.Int32(94608000),
						OfferingType: aws.String("No Upfront"),
					},
					// off-node-type — filtered
					{
						NodeType:     aws.String("ra3.xlplus"),
						Duration:     aws.Int32(31536000),
						OfferingType: aws.String("All Upfront"),
					},
				},
			}, nil
		},
	}
	p := &RedshiftProber{NewClient: func(cfg aws.Config) RedshiftDescribeOfferings { return fake }}
	assert.Equal(t, "redshift", p.Service())

	got, err := p.Probe(context.Background(), aws.Config{})
	require.NoError(t, err)
	got = sortCombos(got)
	want := []Combo{
		{Provider: "aws", Service: "redshift", TermYears: 1, Payment: "all-upfront"},
		{Provider: "aws", Service: "redshift", TermYears: 3, Payment: "no-upfront"},
	}
	assert.Equal(t, want, got)
}

// ---------------------------------------------------------------------------
// MemoryDB
// ---------------------------------------------------------------------------

type fakeMemoryDB struct {
	fn func(*memorydb.DescribeReservedNodesOfferingsInput) (*memorydb.DescribeReservedNodesOfferingsOutput, error)
}

func (f *fakeMemoryDB) DescribeReservedNodesOfferings(ctx context.Context, in *memorydb.DescribeReservedNodesOfferingsInput, _ ...func(*memorydb.Options)) (*memorydb.DescribeReservedNodesOfferingsOutput, error) {
	return f.fn(in)
}

func TestMemoryDBProber_Probe(t *testing.T) {
	fake := &fakeMemoryDB{
		fn: func(in *memorydb.DescribeReservedNodesOfferingsInput) (*memorydb.DescribeReservedNodesOfferingsOutput, error) {
			assert.Equal(t, probeTargetMemoryDB, aws.ToString(in.NodeType))
			return &memorydb.DescribeReservedNodesOfferingsOutput{
				ReservedNodesOfferings: []memorydbtypes.ReservedNodesOffering{
					{Duration: 31536000, OfferingType: aws.String("All Upfront")},
					{Duration: 94608000, OfferingType: aws.String("Partial Upfront")},
					// anomaly: 18-month duration
					{Duration: int32(18 * 30 * 86400), OfferingType: aws.String("All Upfront")},
				},
			}, nil
		},
	}
	p := &MemoryDBProber{NewClient: func(cfg aws.Config) MemoryDBDescribeOfferings { return fake }}
	assert.Equal(t, "memorydb", p.Service())

	got, err := p.Probe(context.Background(), aws.Config{})
	require.NoError(t, err)
	got = sortCombos(got)
	want := []Combo{
		{Provider: "aws", Service: "memorydb", TermYears: 1, Payment: "all-upfront"},
		{Provider: "aws", Service: "memorydb", TermYears: 3, Payment: "partial-upfront"},
	}
	assert.Equal(t, want, got)
}

// ---------------------------------------------------------------------------
// EC2
// ---------------------------------------------------------------------------

type fakeEC2 struct {
	fn func(*ec2.DescribeReservedInstancesOfferingsInput) (*ec2.DescribeReservedInstancesOfferingsOutput, error)
}

func (f *fakeEC2) DescribeReservedInstancesOfferings(ctx context.Context, in *ec2.DescribeReservedInstancesOfferingsInput, _ ...func(*ec2.Options)) (*ec2.DescribeReservedInstancesOfferingsOutput, error) {
	return f.fn(in)
}

func TestEC2Prober_Probe(t *testing.T) {
	fake := &fakeEC2{
		fn: func(in *ec2.DescribeReservedInstancesOfferingsInput) (*ec2.DescribeReservedInstancesOfferingsOutput, error) {
			assert.Equal(t, ec2types.InstanceType(probeTargetEC2), in.InstanceType)
			require.NotNil(t, in.IncludeMarketplace)
			assert.False(t, aws.ToBool(in.IncludeMarketplace))
			return &ec2.DescribeReservedInstancesOfferingsOutput{
				ReservedInstancesOfferings: []ec2types.ReservedInstancesOffering{
					{Duration: aws.Int64(31536000), OfferingType: ec2types.OfferingTypeValuesAllUpfront},
					{Duration: aws.Int64(31536000), OfferingType: ec2types.OfferingTypeValuesPartialUpfront},
					{Duration: aws.Int64(31536000), OfferingType: ec2types.OfferingTypeValuesNoUpfront},
					{Duration: aws.Int64(94608000), OfferingType: ec2types.OfferingTypeValuesAllUpfront},
					// legacy pre-2011 utilization — must be dropped
					{Duration: aws.Int64(31536000), OfferingType: ec2types.OfferingTypeValuesHeavyUtilization},
					{Duration: aws.Int64(31536000), OfferingType: ec2types.OfferingTypeValuesMediumUtilization},
					{Duration: aws.Int64(31536000), OfferingType: ec2types.OfferingTypeValuesLightUtilization},
				},
			}, nil
		},
	}
	p := &EC2Prober{NewClient: func(cfg aws.Config) EC2DescribeOfferings { return fake }}
	assert.Equal(t, "ec2", p.Service())

	got, err := p.Probe(context.Background(), aws.Config{})
	require.NoError(t, err)
	got = sortCombos(got)
	want := []Combo{
		{Provider: "aws", Service: "ec2", TermYears: 1, Payment: "all-upfront"},
		{Provider: "aws", Service: "ec2", TermYears: 1, Payment: "no-upfront"},
		{Provider: "aws", Service: "ec2", TermYears: 1, Payment: "partial-upfront"},
		{Provider: "aws", Service: "ec2", TermYears: 3, Payment: "all-upfront"},
	}
	assert.Equal(t, want, got)
}

func TestDefaultProbers(t *testing.T) {
	probers := DefaultProbers()
	services := make([]string, 0, len(probers))
	for _, p := range probers {
		services = append(services, p.Service())
	}
	sort.Strings(services)
	assert.Equal(t, []string{"ec2", "elasticache", "memorydb", "opensearch", "rds", "redshift"}, services)
}

// ---------------------------------------------------------------------------
// walkPaginated — the shared pagination helper every prober runs through.
// Testing the helper once covers the page-cap behaviour for all six
// services in lieu of six near-identical Test{Service}Prober_PageCap tests.
// The per-prober Probe tests above still exercise the wiring (which token
// field each AWS API uses, per-item conversion, optional client-side
// filtering) end-to-end.
// ---------------------------------------------------------------------------

func TestWalkPaginated_StopsAtPageCap(t *testing.T) {
	// Every page returns a non-empty token; walkPaginated must stop after
	// maxPages calls to bound worst-case API spend if pagination
	// detection is ever broken (SDK regression, malformed AWS response).
	calls := 0
	_, err := walkPaginated(context.Background(), "test", func(ctx context.Context, token *string) ([]rawOffer, *string, error) {
		calls++
		return nil, aws.String("more"), nil
	})
	require.NoError(t, err)
	assert.Equal(t, maxPages, calls)
}

func TestWalkPaginated_StopsOnNilToken(t *testing.T) {
	// AWS signals "no more pages" with a nil token — the helper must
	// stop on the first page that returns one rather than keep looping.
	calls := 0
	got, err := walkPaginated(context.Background(), "test", func(ctx context.Context, token *string) ([]rawOffer, *string, error) {
		calls++
		return []rawOffer{{durationSeconds: 31536000, payment: "All Upfront"}}, nil, nil
	})
	require.NoError(t, err)
	assert.Equal(t, 1, calls)
	assert.Len(t, got, 1)
}

func TestWalkPaginated_StopsOnEmptyToken(t *testing.T) {
	// Some AWS APIs return an empty (non-nil) string instead of nil to
	// signal "done" — the helper must treat both as equivalent.
	calls := 0
	_, err := walkPaginated(context.Background(), "test", func(ctx context.Context, token *string) ([]rawOffer, *string, error) {
		calls++
		return nil, aws.String(""), nil
	})
	require.NoError(t, err)
	assert.Equal(t, 1, calls)
}

func TestWalkPaginated_ThreadsTokenAcrossPages(t *testing.T) {
	// The helper must pass each page's returned token back as the next
	// page's input token so the AWS SDK can resume from the right cursor.
	tokens := []string{"", "page1", "page2"}
	got := []string{}
	calls := 0
	_, err := walkPaginated(context.Background(), "test", func(ctx context.Context, token *string) ([]rawOffer, *string, error) {
		got = append(got, aws.ToString(token))
		calls++
		if calls >= len(tokens) {
			return nil, nil, nil
		}
		return nil, aws.String(tokens[calls]), nil
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"", "page1", "page2"}, got)
}

func TestWalkPaginated_WrapsErrorWithService(t *testing.T) {
	// Errors from fetchPage must be wrapped as "<service>: <err>" so
	// callers see which prober failed without each one repeating the
	// wrap. errors.Is must still find the underlying error.
	boom := errors.New("boom")
	_, err := walkPaginated(context.Background(), "memorydb", func(ctx context.Context, token *string) ([]rawOffer, *string, error) {
		return nil, nil, boom
	})
	require.Error(t, err)
	assert.ErrorIs(t, err, boom)
	assert.Contains(t, err.Error(), "memorydb:")
}

func TestWalkPaginated_AccumulatesAcrossPages(t *testing.T) {
	// Items from each page should accumulate into a single slice in
	// page order — collect() does the dedupe, walkPaginated does not.
	calls := 0
	got, err := walkPaginated(context.Background(), "test", func(ctx context.Context, token *string) ([]rawOffer, *string, error) {
		calls++
		offers := []rawOffer{{durationSeconds: int64(calls), payment: "All Upfront"}}
		if calls >= 3 {
			return offers, nil, nil
		}
		return offers, aws.String("more"), nil
	})
	require.NoError(t, err)
	assert.Equal(t, 3, calls)
	require.Len(t, got, 3)
	assert.Equal(t, int64(1), got[0].durationSeconds)
	assert.Equal(t, int64(2), got[1].durationSeconds)
	assert.Equal(t, int64(3), got[2].durationSeconds)
}
