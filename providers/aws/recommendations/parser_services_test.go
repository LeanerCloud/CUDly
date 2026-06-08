package recommendations

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/costexplorer/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/LeanerCloud/CUDly/pkg/common"
)

func TestParseRDSDetails(t *testing.T) {
	client := &Client{}

	tests := []struct {
		name        string
		details     *types.ReservationPurchaseRecommendationDetail
		expectError bool
		validate    func(t *testing.T, rec *common.Recommendation)
	}{
		{
			name: "Complete RDS details with Multi-AZ",
			details: &types.ReservationPurchaseRecommendationDetail{
				InstanceDetails: &types.InstanceDetails{
					RDSInstanceDetails: &types.RDSInstanceDetails{
						InstanceType:     aws.String("db.r5.large"),
						DatabaseEngine:   aws.String("mysql"),
						Region:           aws.String("US East (N. Virginia)"),
						DeploymentOption: aws.String("Multi-AZ"),
					},
				},
			},
			expectError: false,
			validate: func(t *testing.T, rec *common.Recommendation) {
				assert.Equal(t, "db.r5.large", rec.ResourceType)
				assert.Equal(t, "us-east-1", rec.Region)

				dbDetails, ok := rec.Details.(*common.DatabaseDetails)
				require.True(t, ok, "Details should be DatabaseDetails type")
				assert.Equal(t, "mysql", dbDetails.Engine)
				assert.Equal(t, "multi-az", dbDetails.AZConfig)
			},
		},
		{
			name: "RDS details with Single-AZ",
			details: &types.ReservationPurchaseRecommendationDetail{
				InstanceDetails: &types.InstanceDetails{
					RDSInstanceDetails: &types.RDSInstanceDetails{
						InstanceType:     aws.String("db.t3.medium"),
						DatabaseEngine:   aws.String("postgres"),
						Region:           aws.String("us-west-2"),
						DeploymentOption: aws.String("Single-AZ"),
					},
				},
			},
			expectError: false,
			validate: func(t *testing.T, rec *common.Recommendation) {
				dbDetails, ok := rec.Details.(*common.DatabaseDetails)
				require.True(t, ok)
				assert.Equal(t, "postgres", dbDetails.Engine)
				assert.Equal(t, "single-az", dbDetails.AZConfig)
			},
		},
		{
			// M4 regression test: when CE omits DeploymentOption, AZConfig must be
			// left empty rather than silently defaulted to "single-az".
			// single-AZ and multi-AZ RDS RIs have different prices; guessing wrong
			// would cause findOfferingID to buy the wrong offering class.
			name: "RDS details without deployment option leaves AZConfig empty (M4)",
			details: &types.ReservationPurchaseRecommendationDetail{
				InstanceDetails: &types.InstanceDetails{
					RDSInstanceDetails: &types.RDSInstanceDetails{
						InstanceType:     aws.String("db.m5.xlarge"),
						DatabaseEngine:   aws.String("aurora-postgresql"),
						Region:           aws.String("eu-west-1"),
						DeploymentOption: nil,
					},
				},
			},
			expectError: false,
			validate: func(t *testing.T, rec *common.Recommendation) {
				dbDetails, ok := rec.Details.(*common.DatabaseDetails)
				require.True(t, ok)
				assert.Equal(t, "aurora-postgresql", dbDetails.Engine)
				// AZConfig must be empty, not "single-az" (M4 fix).
				assert.Empty(t, dbDetails.AZConfig, "AZConfig must not be silently defaulted to single-az")
			},
		},
		{
			// CR #1085 regression test: an unrecognized DeploymentOption must error
			// rather than being silently folded into "single-az". The pre-fix code
			// used an else branch that mapped anything non-"Multi-AZ" to "single-az",
			// which could cause findOfferingID to query and buy the wrong RI class.
			name:        "Unknown DeploymentOption errors (CR #1085)",
			expectError: true,
			details: &types.ReservationPurchaseRecommendationDetail{
				InstanceDetails: &types.InstanceDetails{
					RDSInstanceDetails: &types.RDSInstanceDetails{
						InstanceType:     aws.String("db.r5.large"),
						DatabaseEngine:   aws.String("mysql"),
						Region:           aws.String("us-east-1"),
						DeploymentOption: aws.String("Multi-AZ-Readable-Standbys"),
					},
				},
			},
		},
		{
			name: "Missing RDS instance details",
			details: &types.ReservationPurchaseRecommendationDetail{
				InstanceDetails: &types.InstanceDetails{
					RDSInstanceDetails: nil,
				},
			},
			expectError: true,
		},
		{
			name: "Missing instance details completely",
			details: &types.ReservationPurchaseRecommendationDetail{
				InstanceDetails: nil,
			},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := &common.Recommendation{}
			err := client.parseRDSDetails(context.Background(), rec, tt.details)

			if tt.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				if tt.validate != nil {
					tt.validate(t, rec)
				}
			}
		})
	}
}

func TestParseElastiCacheDetails(t *testing.T) {
	client := &Client{}

	tests := []struct {
		name        string
		details     *types.ReservationPurchaseRecommendationDetail
		expectError bool
		validate    func(t *testing.T, rec *common.Recommendation)
	}{
		{
			name: "Complete ElastiCache details",
			details: &types.ReservationPurchaseRecommendationDetail{
				InstanceDetails: &types.InstanceDetails{
					ElastiCacheInstanceDetails: &types.ElastiCacheInstanceDetails{
						NodeType:           aws.String("cache.r5.large"),
						ProductDescription: aws.String("redis"),
						Region:             aws.String("US East (N. Virginia)"),
					},
				},
			},
			expectError: false,
			validate: func(t *testing.T, rec *common.Recommendation) {
				assert.Equal(t, "cache.r5.large", rec.ResourceType)
				assert.Equal(t, "us-east-1", rec.Region)

				cacheDetails, ok := rec.Details.(*common.CacheDetails)
				require.True(t, ok, "Details should be CacheDetails type")
				assert.Equal(t, "cache.r5.large", cacheDetails.NodeType)
				assert.Equal(t, "redis", cacheDetails.Engine)
			},
		},
		{
			name: "ElastiCache Memcached",
			details: &types.ReservationPurchaseRecommendationDetail{
				InstanceDetails: &types.InstanceDetails{
					ElastiCacheInstanceDetails: &types.ElastiCacheInstanceDetails{
						NodeType:           aws.String("cache.t3.medium"),
						ProductDescription: aws.String("memcached"),
						Region:             aws.String("eu-west-1"),
					},
				},
			},
			expectError: false,
			validate: func(t *testing.T, rec *common.Recommendation) {
				cacheDetails, ok := rec.Details.(*common.CacheDetails)
				require.True(t, ok)
				assert.Equal(t, "memcached", cacheDetails.Engine)
			},
		},
		{
			name: "Missing ElastiCache instance details",
			details: &types.ReservationPurchaseRecommendationDetail{
				InstanceDetails: &types.InstanceDetails{
					ElastiCacheInstanceDetails: nil,
				},
			},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := &common.Recommendation{}
			err := client.parseElastiCacheDetails(context.Background(), rec, tt.details)

			if tt.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				if tt.validate != nil {
					tt.validate(t, rec)
				}
			}
		})
	}
}

func TestParseEC2Details(t *testing.T) {
	client := &Client{}

	tests := []struct {
		name        string
		details     *types.ReservationPurchaseRecommendationDetail
		expectError bool
		validate    func(t *testing.T, rec *common.Recommendation)
	}{
		{
			// CE returns Tenancy="shared" for default; parser must normalise to
			// the EC2 API canonical value "default" (types.TenancyDefault).
			// Scope with an AZ must be "Availability Zone" (types.ScopeAvailabilityZone).
			name: "Complete EC2 details with AZ scope, CE tenancy=shared",
			details: &types.ReservationPurchaseRecommendationDetail{
				InstanceDetails: &types.InstanceDetails{
					EC2InstanceDetails: &types.EC2InstanceDetails{
						InstanceType:     aws.String("m5.large"),
						Platform:         aws.String("Linux/UNIX"),
						Region:           aws.String("US East (N. Virginia)"),
						Tenancy:          aws.String("shared"),
						AvailabilityZone: aws.String("us-east-1a"),
					},
				},
			},
			expectError: false,
			validate: func(t *testing.T, rec *common.Recommendation) {
				assert.Equal(t, "m5.large", rec.ResourceType)
				assert.Equal(t, "us-east-1", rec.Region)

				ec2Details, ok := rec.Details.(*common.ComputeDetails)
				require.True(t, ok, "Details should be ComputeDetails type")
				assert.Equal(t, "m5.large", ec2Details.InstanceType)
				assert.Equal(t, "Linux/UNIX", ec2Details.Platform)
				// CE "shared" must be written as EC2 API canonical "default"
				assert.Equal(t, "default", ec2Details.Tenancy)
				// AZ present -> "Availability Zone" (not "availability-zone")
				assert.Equal(t, "Availability Zone", ec2Details.Scope)
			},
		},
		{
			name: "EC2 with regional scope (no AZ)",
			details: &types.ReservationPurchaseRecommendationDetail{
				InstanceDetails: &types.InstanceDetails{
					EC2InstanceDetails: &types.EC2InstanceDetails{
						InstanceType:     aws.String("t3.medium"),
						Platform:         aws.String("Linux/UNIX"),
						Region:           aws.String("us-west-2"),
						Tenancy:          aws.String("shared"),
						AvailabilityZone: nil,
					},
				},
			},
			expectError: false,
			validate: func(t *testing.T, rec *common.Recommendation) {
				ec2Details, ok := rec.Details.(*common.ComputeDetails)
				require.True(t, ok)
				// No AZ -> "Region" (not "region")
				assert.Equal(t, "Region", ec2Details.Scope)
			},
		},
		{
			name: "EC2 with empty AZ string",
			details: &types.ReservationPurchaseRecommendationDetail{
				InstanceDetails: &types.InstanceDetails{
					EC2InstanceDetails: &types.EC2InstanceDetails{
						InstanceType:     aws.String("t3.medium"),
						Platform:         aws.String("Linux/UNIX"),
						Region:           aws.String("us-west-2"),
						Tenancy:          aws.String("shared"),
						AvailabilityZone: aws.String(""),
					},
				},
			},
			expectError: false,
			validate: func(t *testing.T, rec *common.Recommendation) {
				ec2Details, ok := rec.Details.(*common.ComputeDetails)
				require.True(t, ok)
				// Empty AZ string treated same as nil -> "Region"
				assert.Equal(t, "Region", ec2Details.Scope)
			},
		},
		{
			name: "EC2 Windows with dedicated tenancy",
			details: &types.ReservationPurchaseRecommendationDetail{
				InstanceDetails: &types.InstanceDetails{
					EC2InstanceDetails: &types.EC2InstanceDetails{
						InstanceType:     aws.String("r5.xlarge"),
						Platform:         aws.String("Windows"),
						Region:           aws.String("eu-central-1"),
						Tenancy:          aws.String("dedicated"),
						AvailabilityZone: nil,
					},
				},
			},
			expectError: false,
			validate: func(t *testing.T, rec *common.Recommendation) {
				ec2Details, ok := rec.Details.(*common.ComputeDetails)
				require.True(t, ok)
				assert.Equal(t, "Windows", ec2Details.Platform)
				// "dedicated" is already the canonical EC2 API value
				assert.Equal(t, "dedicated", ec2Details.Tenancy)
			},
		},
		{
			// nil Tenancy in CE response must yield EC2 API canonical "default"
			// (not "shared" as the old parser wrote).
			name: "EC2 without tenancy defaults to default",
			details: &types.ReservationPurchaseRecommendationDetail{
				InstanceDetails: &types.InstanceDetails{
					EC2InstanceDetails: &types.EC2InstanceDetails{
						InstanceType: aws.String("m5.large"),
						Platform:     aws.String("Linux/UNIX"),
						Region:       aws.String("us-east-1"),
						Tenancy:      nil,
					},
				},
			},
			expectError: false,
			validate: func(t *testing.T, rec *common.Recommendation) {
				ec2Details, ok := rec.Details.(*common.ComputeDetails)
				require.True(t, ok)
				assert.Equal(t, "default", ec2Details.Tenancy)
			},
		},
		{
			name: "Missing EC2 instance details",
			details: &types.ReservationPurchaseRecommendationDetail{
				InstanceDetails: &types.InstanceDetails{
					EC2InstanceDetails: nil,
				},
			},
			expectError: true,
		},
		{
			// M5 regression test: "host" tenancy (Dedicated Hosts) has no
			// corresponding EC2 RI product. Previously this silently collapsed
			// to "default", which would cause findOfferingID to look up (and
			// potentially buy) a default-tenancy RI for a Dedicated Host workload.
			// The parser must error so the caller can decide, rather than
			// silently buying the wrong product.
			name: "EC2 host tenancy errors (M5)",
			details: &types.ReservationPurchaseRecommendationDetail{
				InstanceDetails: &types.InstanceDetails{
					EC2InstanceDetails: &types.EC2InstanceDetails{
						InstanceType: aws.String("m5.large"),
						Platform:     aws.String("Linux/UNIX"),
						Region:       aws.String("us-east-1"),
						Tenancy:      aws.String("host"),
					},
				},
			},
			expectError: true,
		},
		{
			// M5: an unrecognised tenancy value should also error.
			name: "EC2 unknown tenancy errors (M5)",
			details: &types.ReservationPurchaseRecommendationDetail{
				InstanceDetails: &types.InstanceDetails{
					EC2InstanceDetails: &types.EC2InstanceDetails{
						InstanceType: aws.String("m5.large"),
						Platform:     aws.String("Linux/UNIX"),
						Region:       aws.String("us-east-1"),
						Tenancy:      aws.String("future-unknown-tenancy"),
					},
				},
			},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := &common.Recommendation{}
			err := client.parseEC2Details(context.Background(), rec, tt.details)

			if tt.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				if tt.validate != nil {
					tt.validate(t, rec)
				}
			}
		})
	}
}

func TestParseOpenSearchDetails(t *testing.T) {
	client := &Client{}

	tests := []struct {
		name        string
		details     *types.ReservationPurchaseRecommendationDetail
		expectError bool
		validate    func(t *testing.T, rec *common.Recommendation)
	}{
		{
			name: "Complete OpenSearch details",
			details: &types.ReservationPurchaseRecommendationDetail{
				InstanceDetails: &types.InstanceDetails{
					ESInstanceDetails: &types.ESInstanceDetails{
						InstanceClass: aws.String("r5"),
						InstanceSize:  aws.String("large.search"),
						Region:        aws.String("US East (N. Virginia)"),
					},
				},
			},
			expectError: false,
			validate: func(t *testing.T, rec *common.Recommendation) {
				assert.Equal(t, "r5.large.search", rec.ResourceType)
				assert.Equal(t, "us-east-1", rec.Region)

				osDetails, ok := rec.Details.(*common.SearchDetails)
				require.True(t, ok, "Details should be SearchDetails type")
				assert.Equal(t, "r5.large.search", osDetails.InstanceType)
			},
		},
		{
			name: "OpenSearch m5 instance",
			details: &types.ReservationPurchaseRecommendationDetail{
				InstanceDetails: &types.InstanceDetails{
					ESInstanceDetails: &types.ESInstanceDetails{
						InstanceClass: aws.String("m5"),
						InstanceSize:  aws.String("xlarge.search"),
						Region:        aws.String("eu-west-1"),
					},
				},
			},
			expectError: false,
			validate: func(t *testing.T, rec *common.Recommendation) {
				assert.Equal(t, "m5.xlarge.search", rec.ResourceType)
			},
		},
		{
			name: "Missing OpenSearch instance details",
			details: &types.ReservationPurchaseRecommendationDetail{
				InstanceDetails: &types.InstanceDetails{
					ESInstanceDetails: nil,
				},
			},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := &common.Recommendation{}
			err := client.parseOpenSearchDetails(context.Background(), rec, tt.details)

			if tt.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				if tt.validate != nil {
					tt.validate(t, rec)
				}
			}
		})
	}
}

func TestParseRedshiftDetails(t *testing.T) {
	client := &Client{}

	tests := []struct {
		name        string
		details     *types.ReservationPurchaseRecommendationDetail
		count       int
		expectError bool
		validate    func(t *testing.T, rec *common.Recommendation)
	}{
		{
			name: "Single node Redshift cluster",
			details: &types.ReservationPurchaseRecommendationDetail{
				InstanceDetails: &types.InstanceDetails{
					RedshiftInstanceDetails: &types.RedshiftInstanceDetails{
						NodeType: aws.String("dc2.large"),
						Region:   aws.String("US East (N. Virginia)"),
					},
				},
			},
			count:       1,
			expectError: false,
			validate: func(t *testing.T, rec *common.Recommendation) {
				assert.Equal(t, "dc2.large", rec.ResourceType)
				assert.Equal(t, "us-east-1", rec.Region)

				rsDetails, ok := rec.Details.(*common.DataWarehouseDetails)
				require.True(t, ok, "Details should be DataWarehouseDetails type")
				assert.Equal(t, "dc2.large", rsDetails.NodeType)
				assert.Equal(t, 1, rsDetails.NumberOfNodes)
				assert.Equal(t, "single-node", rsDetails.ClusterType)
			},
		},
		{
			name: "Multi-node Redshift cluster",
			details: &types.ReservationPurchaseRecommendationDetail{
				InstanceDetails: &types.InstanceDetails{
					RedshiftInstanceDetails: &types.RedshiftInstanceDetails{
						NodeType: aws.String("ra3.4xlarge"),
						Region:   aws.String("us-west-2"),
					},
				},
			},
			count:       5,
			expectError: false,
			validate: func(t *testing.T, rec *common.Recommendation) {
				rsDetails, ok := rec.Details.(*common.DataWarehouseDetails)
				require.True(t, ok)
				assert.Equal(t, 5, rsDetails.NumberOfNodes)
				assert.Equal(t, "multi-node", rsDetails.ClusterType)
			},
		},
		{
			name: "Missing Redshift instance details",
			details: &types.ReservationPurchaseRecommendationDetail{
				InstanceDetails: &types.InstanceDetails{
					RedshiftInstanceDetails: nil,
				},
			},
			count:       1,
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := &common.Recommendation{
				Count: tt.count,
			}
			err := client.parseRedshiftDetails(context.Background(), rec, tt.details)

			if tt.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				if tt.validate != nil {
					tt.validate(t, rec)
				}
			}
		})
	}
}

func TestParseMemoryDBDetails(t *testing.T) {
	client := &Client{}

	t.Run("missing MemoryDB instance details returns error", func(t *testing.T) {
		rec := &common.Recommendation{}
		err := client.parseMemoryDBDetails(context.Background(), rec, &types.ReservationPurchaseRecommendationDetail{})
		require.Error(t, err, "should fail loudly when MemoryDB instance details are absent")
		assert.Contains(t, err.Error(), "instance details not found")
		assert.Nil(t, rec.Details, "Details should remain nil on error")
		assert.Empty(t, rec.ResourceType, "ResourceType should remain empty on error")
	})

	t.Run("nil NodeType returns error", func(t *testing.T) {
		rec := &common.Recommendation{}
		details := &types.ReservationPurchaseRecommendationDetail{
			InstanceDetails: &types.InstanceDetails{
				MemoryDBInstanceDetails: &types.MemoryDBInstanceDetails{
					Region: aws.String("US East (N. Virginia)"),
				},
			},
		}
		err := client.parseMemoryDBDetails(context.Background(), rec, details)
		require.Error(t, err, "should fail loudly when NodeType is absent")
		assert.Contains(t, err.Error(), "NodeType")
		assert.Nil(t, rec.Details, "Details should remain nil on error")
	})

	t.Run("populates Details, ResourceType and Region from CE node type", func(t *testing.T) {
		rec := &common.Recommendation{}
		details := &types.ReservationPurchaseRecommendationDetail{
			InstanceDetails: &types.InstanceDetails{
				MemoryDBInstanceDetails: &types.MemoryDBInstanceDetails{
					NodeType: aws.String("db.r6g.large"),
					Region:   aws.String("US East (N. Virginia)"),
				},
			},
		}
		err := client.parseMemoryDBDetails(context.Background(), rec, details)
		require.NoError(t, err)
		assert.Equal(t, "db.r6g.large", rec.ResourceType)
		assert.Equal(t, "us-east-1", rec.Region)
		cacheDetails, ok := rec.Details.(*common.CacheDetails)
		require.True(t, ok, "Details should be *common.CacheDetails")
		assert.Equal(t, "db.r6g.large", cacheDetails.NodeType)
		assert.Equal(t, "redis", cacheDetails.Engine)
	})

	t.Run("xlarge node type populates Details", func(t *testing.T) {
		rec := &common.Recommendation{}
		details := &types.ReservationPurchaseRecommendationDetail{
			InstanceDetails: &types.InstanceDetails{
				MemoryDBInstanceDetails: &types.MemoryDBInstanceDetails{
					NodeType: aws.String("db.r6gd.xlarge"),
				},
			},
		}
		err := client.parseMemoryDBDetails(context.Background(), rec, details)
		require.NoError(t, err)
		assert.Equal(t, "db.r6gd.xlarge", rec.ResourceType)
		cacheDetails, ok := rec.Details.(*common.CacheDetails)
		require.True(t, ok, "Details should be *common.CacheDetails")
		assert.Equal(t, "db.r6gd.xlarge", cacheDetails.NodeType)
		assert.Equal(t, "redis", cacheDetails.Engine)
	})
}
