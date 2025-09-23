package mocks

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/service/costexplorer"
	cetypes "github.com/aws/aws-sdk-go-v2/service/costexplorer/types"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/elasticache"
	ectypes "github.com/aws/aws-sdk-go-v2/service/elasticache/types"
	"github.com/aws/aws-sdk-go-v2/service/memorydb"
	mdbtypes "github.com/aws/aws-sdk-go-v2/service/memorydb/types"
	"github.com/aws/aws-sdk-go-v2/service/rds"
	rdstypes "github.com/aws/aws-sdk-go-v2/service/rds/types"
	"github.com/aws/aws-sdk-go-v2/service/redshift"
	rstypes "github.com/aws/aws-sdk-go-v2/service/redshift/types"
	"github.com/stretchr/testify/mock"
)

// MockCostExplorerClient mocks the Cost Explorer client
type MockCostExplorerClient struct {
	mock.Mock
}

func (m *MockCostExplorerClient) GetReservationPurchaseRecommendation(ctx context.Context, params *costexplorer.GetReservationPurchaseRecommendationInput, optFns ...func(*costexplorer.Options)) (*costexplorer.GetReservationPurchaseRecommendationOutput, error) {
	args := m.Called(ctx, params)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*costexplorer.GetReservationPurchaseRecommendationOutput), args.Error(1)
}

// MockRDSClient mocks the RDS client
type MockRDSClient struct {
	mock.Mock
}

func (m *MockRDSClient) DescribeReservedDBInstancesOfferings(ctx context.Context, params *rds.DescribeReservedDBInstancesOfferingsInput, optFns ...func(*rds.Options)) (*rds.DescribeReservedDBInstancesOfferingsOutput, error) {
	args := m.Called(ctx, params)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*rds.DescribeReservedDBInstancesOfferingsOutput), args.Error(1)
}

func (m *MockRDSClient) PurchaseReservedDBInstancesOffering(ctx context.Context, params *rds.PurchaseReservedDBInstancesOfferingInput, optFns ...func(*rds.Options)) (*rds.PurchaseReservedDBInstancesOfferingOutput, error) {
	args := m.Called(ctx, params)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*rds.PurchaseReservedDBInstancesOfferingOutput), args.Error(1)
}

func (m *MockRDSClient) DescribeReservedDBInstances(ctx context.Context, params *rds.DescribeReservedDBInstancesInput, optFns ...func(*rds.Options)) (*rds.DescribeReservedDBInstancesOutput, error) {
	args := m.Called(ctx, params)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*rds.DescribeReservedDBInstancesOutput), args.Error(1)
}

// MockElastiCacheClient mocks the ElastiCache client
type MockElastiCacheClient struct {
	mock.Mock
}

func (m *MockElastiCacheClient) DescribeReservedCacheNodesOfferings(ctx context.Context, params *elasticache.DescribeReservedCacheNodesOfferingsInput, optFns ...func(*elasticache.Options)) (*elasticache.DescribeReservedCacheNodesOfferingsOutput, error) {
	args := m.Called(ctx, params)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*elasticache.DescribeReservedCacheNodesOfferingsOutput), args.Error(1)
}

func (m *MockElastiCacheClient) PurchaseReservedCacheNodesOffering(ctx context.Context, params *elasticache.PurchaseReservedCacheNodesOfferingInput, optFns ...func(*elasticache.Options)) (*elasticache.PurchaseReservedCacheNodesOfferingOutput, error) {
	args := m.Called(ctx, params)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*elasticache.PurchaseReservedCacheNodesOfferingOutput), args.Error(1)
}

func (m *MockElastiCacheClient) DescribeReservedCacheNodes(ctx context.Context, params *elasticache.DescribeReservedCacheNodesInput, optFns ...func(*elasticache.Options)) (*elasticache.DescribeReservedCacheNodesOutput, error) {
	args := m.Called(ctx, params)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*elasticache.DescribeReservedCacheNodesOutput), args.Error(1)
}

// MockEC2Client mocks the EC2 client
type MockEC2Client struct {
	mock.Mock
}

func (m *MockEC2Client) DescribeReservedInstancesOfferings(ctx context.Context, params *ec2.DescribeReservedInstancesOfferingsInput, optFns ...func(*ec2.Options)) (*ec2.DescribeReservedInstancesOfferingsOutput, error) {
	args := m.Called(ctx, params)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*ec2.DescribeReservedInstancesOfferingsOutput), args.Error(1)
}

func (m *MockEC2Client) PurchaseReservedInstancesOffering(ctx context.Context, params *ec2.PurchaseReservedInstancesOfferingInput, optFns ...func(*ec2.Options)) (*ec2.PurchaseReservedInstancesOfferingOutput, error) {
	args := m.Called(ctx, params)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*ec2.PurchaseReservedInstancesOfferingOutput), args.Error(1)
}

func (m *MockEC2Client) DescribeReservedInstances(ctx context.Context, params *ec2.DescribeReservedInstancesInput, optFns ...func(*ec2.Options)) (*ec2.DescribeReservedInstancesOutput, error) {
	args := m.Called(ctx, params)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*ec2.DescribeReservedInstancesOutput), args.Error(1)
}

func (m *MockEC2Client) DescribeRegions(ctx context.Context, params *ec2.DescribeRegionsInput, optFns ...func(*ec2.Options)) (*ec2.DescribeRegionsOutput, error) {
	args := m.Called(ctx, params)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*ec2.DescribeRegionsOutput), args.Error(1)
}


// MockRedshiftClient mocks the Redshift client
type MockRedshiftClient struct {
	mock.Mock
}

func (m *MockRedshiftClient) DescribeReservedNodeOfferings(ctx context.Context, params *redshift.DescribeReservedNodeOfferingsInput, optFns ...func(*redshift.Options)) (*redshift.DescribeReservedNodeOfferingsOutput, error) {
	args := m.Called(ctx, params)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*redshift.DescribeReservedNodeOfferingsOutput), args.Error(1)
}

func (m *MockRedshiftClient) PurchaseReservedNodeOffering(ctx context.Context, params *redshift.PurchaseReservedNodeOfferingInput, optFns ...func(*redshift.Options)) (*redshift.PurchaseReservedNodeOfferingOutput, error) {
	args := m.Called(ctx, params)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*redshift.PurchaseReservedNodeOfferingOutput), args.Error(1)
}

// MockMemoryDBClient mocks the MemoryDB client
type MockMemoryDBClient struct {
	mock.Mock
}

func (m *MockMemoryDBClient) DescribeReservedNodesOfferings(ctx context.Context, params *memorydb.DescribeReservedNodesOfferingsInput, optFns ...func(*memorydb.Options)) (*memorydb.DescribeReservedNodesOfferingsOutput, error) {
	args := m.Called(ctx, params)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*memorydb.DescribeReservedNodesOfferingsOutput), args.Error(1)
}

func (m *MockMemoryDBClient) PurchaseReservedNodesOffering(ctx context.Context, params *memorydb.PurchaseReservedNodesOfferingInput, optFns ...func(*memorydb.Options)) (*memorydb.PurchaseReservedNodesOfferingOutput, error) {
	args := m.Called(ctx, params)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*memorydb.PurchaseReservedNodesOfferingOutput), args.Error(1)
}

// Helper functions to create sample outputs for testing

func CreateSampleRDSOfferings() *rds.DescribeReservedDBInstancesOfferingsOutput {
	return &rds.DescribeReservedDBInstancesOfferingsOutput{
		ReservedDBInstancesOfferings: []rdstypes.ReservedDBInstancesOffering{
			{
				ReservedDBInstancesOfferingId: stringPtr("offering-1"),
				DBInstanceClass:               stringPtr("db.t3.medium"),
				Duration:                      int32Ptr(31536000),
				OfferingType:                  stringPtr("No Upfront"),
				MultiAZ:                       boolPtr(false),
				ProductDescription:            stringPtr("mysql"),
				FixedPrice:                    float64Ptr(0),
				UsagePrice:                    float64Ptr(0.05),
				CurrencyCode:                  stringPtr("USD"),
			},
		},
	}
}

func CreateSampleElastiCacheOfferings() *elasticache.DescribeReservedCacheNodesOfferingsOutput {
	return &elasticache.DescribeReservedCacheNodesOfferingsOutput{
		ReservedCacheNodesOfferings: []ectypes.ReservedCacheNodesOffering{
			{
				ReservedCacheNodesOfferingId: stringPtr("offering-1"),
				CacheNodeType:                stringPtr("cache.r6g.large"),
				Duration:                     int32Ptr(31536000),
				OfferingType:                 stringPtr("No Upfront"),
				ProductDescription:           stringPtr("redis"),
				FixedPrice:                   float64Ptr(0),
				UsagePrice:                   float64Ptr(0.08),
			},
		},
	}
}

func CreateSampleEC2Offerings() *ec2.DescribeReservedInstancesOfferingsOutput {
	return &ec2.DescribeReservedInstancesOfferingsOutput{
		ReservedInstancesOfferings: []ec2types.ReservedInstancesOffering{
			{
				ReservedInstancesOfferingId: stringPtr("offering-1"),
				InstanceType:                ec2types.InstanceTypeM5Large,
				Duration:                    int64Ptr(31536000),
				OfferingType:                ec2types.OfferingTypeValuesNoUpfront,
				ProductDescription:          ec2types.RIProductDescriptionLinuxUnix,
				InstanceTenancy:             ec2types.TenancyDefault,
				FixedPrice:                  float32Ptr(0),
				UsagePrice:                  float32Ptr(0.096),
				CurrencyCode:                ec2types.CurrencyCodeValuesUsd,
			},
		},
	}
}


func CreateSampleRedshiftOfferings() *redshift.DescribeReservedNodeOfferingsOutput {
	return &redshift.DescribeReservedNodeOfferingsOutput{
		ReservedNodeOfferings: []rstypes.ReservedNodeOffering{
			{
				ReservedNodeOfferingId:   stringPtr("offering-1"),
				NodeType:                 stringPtr("dc2.large"),
				Duration:                 int32Ptr(31536000),
				ReservedNodeOfferingType: rstypes.ReservedNodeOfferingTypeRegular,
				FixedPrice:               float64Ptr(0),
				UsagePrice:               float64Ptr(0.25),
				CurrencyCode:             stringPtr("USD"),
			},
		},
	}
}

func CreateSampleMemoryDBOfferings() *memorydb.DescribeReservedNodesOfferingsOutput {
	return &memorydb.DescribeReservedNodesOfferingsOutput{
		ReservedNodesOfferings: []mdbtypes.ReservedNodesOffering{
			{
				ReservedNodesOfferingId: stringPtr("offering-1"),
				NodeType:                stringPtr("db.r6g.large"),
				Duration:                31536000,
				OfferingType:            stringPtr("No Upfront"),
				FixedPrice:              0,
				RecurringCharges: []mdbtypes.RecurringCharge{
					{
						RecurringChargeAmount:    0.15,
						RecurringChargeFrequency: stringPtr("Hourly"),
					},
				},
			},
		},
	}
}

func CreateSampleCostExplorerRecommendations(service string) *costexplorer.GetReservationPurchaseRecommendationOutput {
	return &costexplorer.GetReservationPurchaseRecommendationOutput{
		Recommendations: []cetypes.ReservationPurchaseRecommendation{
			{
				AccountScope: cetypes.AccountScopePayer,
				ServiceSpecification: &cetypes.ServiceSpecification{
					EC2Specification: &cetypes.EC2Specification{
						OfferingClass: cetypes.OfferingClassStandard,
					},
				},
				RecommendationDetails: []cetypes.ReservationPurchaseRecommendationDetail{
					{
						AccountId:                      stringPtr("123456789012"),
						InstanceDetails:                createInstanceDetails(service),
						RecommendedNumberOfInstancesToPurchase: stringPtr("3"),
						RecommendedNormalizedUnitsToPurchase:   stringPtr("3"),
						MinimumNumberOfInstancesUsedPerHour:    stringPtr("2"),
						MaximumNumberOfInstancesUsedPerHour:    stringPtr("5"),
						AverageNumberOfInstancesUsedPerHour:    stringPtr("3"),
						AverageUtilization:                     stringPtr("85"),
						EstimatedMonthlySavingsAmount:          stringPtr("500"),
						EstimatedMonthlySavingsPercentage:     stringPtr("30"),
						EstimatedMonthlyOnDemandCost:           stringPtr("1500"),
						UpfrontCost:                           stringPtr("0"),
						RecurringStandardMonthlyCost:          stringPtr("1000"),
					},
				},
				RecommendationSummary: &cetypes.ReservationPurchaseRecommendationSummary{
					TotalEstimatedMonthlySavingsAmount:     stringPtr("500"),
					TotalEstimatedMonthlySavingsPercentage: stringPtr("30"),
					CurrencyCode:                           stringPtr("USD"),
				},
			},
		},
	}
}

func createInstanceDetails(service string) *cetypes.InstanceDetails {
	switch service {
	case "rds":
		return &cetypes.InstanceDetails{
			RDSInstanceDetails: &cetypes.RDSInstanceDetails{
				DatabaseEngine:       stringPtr("mysql"),
				DatabaseEdition:      stringPtr("Standard"),
				InstanceType:         stringPtr("db.t3.medium"),
				DeploymentOption:     stringPtr("Single-AZ"),
				LicenseModel:         stringPtr("general-public-license"),
				Region:              stringPtr("us-east-1"),
				SizeFlexEligible:    true,
			},
		}
	case "elasticache":
		return &cetypes.InstanceDetails{
			ElastiCacheInstanceDetails: &cetypes.ElastiCacheInstanceDetails{
				NodeType:         stringPtr("cache.r6g.large"),
				ProductDescription: stringPtr("redis"),
				Region:           stringPtr("us-east-1"),
				SizeFlexEligible: true,
			},
		}
	case "ec2":
		return &cetypes.InstanceDetails{
			EC2InstanceDetails: &cetypes.EC2InstanceDetails{
				InstanceType:     stringPtr("m5.large"),
				Region:          stringPtr("us-east-1"),
				Platform:        stringPtr("Linux/UNIX"),
				Tenancy:         stringPtr("Shared"),
				AvailabilityZone: stringPtr("us-east-1a"),
				SizeFlexEligible: true,
			},
		}
	case "opensearch":
		return &cetypes.InstanceDetails{
			ESInstanceDetails: &cetypes.ESInstanceDetails{
				InstanceClass:    stringPtr("r5.large.search"),
				Region:          stringPtr("us-east-1"),
				SizeFlexEligible: true,
			},
		}
	case "redshift":
		return &cetypes.InstanceDetails{
			RedshiftInstanceDetails: &cetypes.RedshiftInstanceDetails{
				NodeType:         stringPtr("dc2.large"),
				Region:          stringPtr("us-east-1"),
				SizeFlexEligible: true,
			},
		}
	default:
		return &cetypes.InstanceDetails{}
	}
}

func CreateSampleEC2Regions() *ec2.DescribeRegionsOutput {
	return &ec2.DescribeRegionsOutput{
		Regions: []ec2types.Region{
			{
				RegionName: stringPtr("us-east-1"),
				Endpoint:   stringPtr("ec2.us-east-1.amazonaws.com"),
			},
			{
				RegionName: stringPtr("us-west-2"),
				Endpoint:   stringPtr("ec2.us-west-2.amazonaws.com"),
			},
			{
				RegionName: stringPtr("eu-west-1"),
				Endpoint:   stringPtr("ec2.eu-west-1.amazonaws.com"),
			},
		},
	}
}

// Helper functions
func stringPtr(s string) *string {
	return &s
}

func int32Ptr(i int32) *int32 {
	return &i
}

func int64Ptr(i int64) *int64 {
	return &i
}

func float32Ptr(f float32) *float32 {
	return &f
}

func float64Ptr(f float64) *float64 {
	return &f
}

func boolPtr(b bool) *bool {
	return &b
}