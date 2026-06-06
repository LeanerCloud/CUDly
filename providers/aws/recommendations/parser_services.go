package recommendations

import (
	"context"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/service/costexplorer/types"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"

	"github.com/LeanerCloud/CUDly/pkg/common"
)

// parseRDSDetails extracts RDS-specific details
func (c *Client) parseRDSDetails(_ context.Context, rec *common.Recommendation, details *types.ReservationPurchaseRecommendationDetail) error {
	if details.InstanceDetails == nil || details.InstanceDetails.RDSInstanceDetails == nil {
		return fmt.Errorf("RDS instance details not found")
	}

	rdsDetails := details.InstanceDetails.RDSInstanceDetails
	rdsInfo := &common.DatabaseDetails{}

	if rdsDetails.InstanceType != nil {
		rec.ResourceType = *rdsDetails.InstanceType
	}
	if rdsDetails.DatabaseEngine != nil {
		rdsInfo.Engine = *rdsDetails.DatabaseEngine
	}
	if rdsDetails.Region != nil {
		rec.Region = normalizeRegionName(*rdsDetails.Region)
	}
	if rdsDetails.DeploymentOption != nil {
		if *rdsDetails.DeploymentOption == "Multi-AZ" {
			rdsInfo.AZConfig = "multi-az"
		} else {
			rdsInfo.AZConfig = "single-az"
		}
	} else {
		rdsInfo.AZConfig = "single-az"
	}

	rec.Details = rdsInfo
	return nil
}

// parseElastiCacheDetails extracts ElastiCache-specific details
func (c *Client) parseElastiCacheDetails(_ context.Context, rec *common.Recommendation, details *types.ReservationPurchaseRecommendationDetail) error {
	if details.InstanceDetails == nil || details.InstanceDetails.ElastiCacheInstanceDetails == nil {
		return fmt.Errorf("ElastiCache instance details not found")
	}

	cacheDetails := details.InstanceDetails.ElastiCacheInstanceDetails
	cacheInfo := &common.CacheDetails{}

	if cacheDetails.NodeType != nil {
		rec.ResourceType = *cacheDetails.NodeType
		cacheInfo.NodeType = *cacheDetails.NodeType
	}
	if cacheDetails.ProductDescription != nil {
		cacheInfo.Engine = *cacheDetails.ProductDescription
	}
	if cacheDetails.Region != nil {
		rec.Region = normalizeRegionName(*cacheDetails.Region)
	}

	rec.Details = cacheInfo
	return nil
}

// resolveEC2Tenancy maps a Cost Explorer tenancy value to the EC2 RI API
// tenancy string. CE uses "shared" for the default tenancy; "dedicated" maps
// directly. Any nil or unrecognised value is treated as default.
func resolveEC2Tenancy(tenancy *string) string {
	if tenancy != nil && *tenancy == "dedicated" {
		return string(ec2types.TenancyDedicated)
	}
	return string(ec2types.TenancyDefault)
}

// resolveEC2Scope maps a Cost Explorer availability zone value to the EC2 RI
// API scope string. A non-empty AZ means AZ scope; otherwise region scope.
func resolveEC2Scope(az *string) string {
	if az != nil && *az != "" {
		return string(ec2types.ScopeAvailabilityZone)
	}
	return string(ec2types.ScopeRegional)
}

// enrichFromCatalogue populates VCPU and MemoryGB on ec2Info from the
// lazily-cached DescribeInstanceTypes catalogue. Non-fatal on cache miss.
func (c *Client) enrichFromCatalogue(ctx context.Context, ec2Info *common.ComputeDetails) {
	if ec2Info.InstanceType == "" {
		return
	}
	entry, ok := c.instanceTypeLookup(ctx, ec2Info.InstanceType)
	if !ok {
		return
	}
	if entry.vCPUs > 0 {
		ec2Info.VCPU = entry.vCPUs
	}
	if entry.memoryGB > 0 {
		ec2Info.MemoryGB = entry.memoryGB
	}
}

// parseEC2Details extracts EC2-specific details and enriches the rec with
// vCPU and memory from the lazily-cached DescribeInstanceTypes catalogue.
// If the catalogue fetch failed or the instance type is not found, VCPU
// and MemoryGB remain 0 (the omitempty JSON tags hide them from payloads).
func (c *Client) parseEC2Details(ctx context.Context, rec *common.Recommendation, details *types.ReservationPurchaseRecommendationDetail) error {
	if details.InstanceDetails == nil || details.InstanceDetails.EC2InstanceDetails == nil {
		return fmt.Errorf("EC2 instance details not found")
	}

	ec2Details := details.InstanceDetails.EC2InstanceDetails
	ec2Info := &common.ComputeDetails{}

	if ec2Details.InstanceType != nil {
		rec.ResourceType = *ec2Details.InstanceType
		ec2Info.InstanceType = *ec2Details.InstanceType
	}
	if ec2Details.Platform != nil {
		ec2Info.Platform = *ec2Details.Platform
	}
	if ec2Details.Region != nil {
		rec.Region = normalizeRegionName(*ec2Details.Region)
	}
	ec2Info.Tenancy = resolveEC2Tenancy(ec2Details.Tenancy)
	ec2Info.Scope = resolveEC2Scope(ec2Details.AvailabilityZone)
	c.enrichFromCatalogue(ctx, ec2Info)

	rec.Details = ec2Info
	return nil
}

// parseOpenSearchDetails extracts OpenSearch-specific details
func (c *Client) parseOpenSearchDetails(_ context.Context, rec *common.Recommendation, details *types.ReservationPurchaseRecommendationDetail) error {
	if details.InstanceDetails == nil || details.InstanceDetails.ESInstanceDetails == nil {
		return fmt.Errorf("OpenSearch/Elasticsearch instance details not found")
	}

	esDetails := details.InstanceDetails.ESInstanceDetails
	osInfo := &common.SearchDetails{}

	if esDetails.InstanceClass != nil && esDetails.InstanceSize != nil {
		instanceClass := *esDetails.InstanceClass
		instanceSize := *esDetails.InstanceSize
		if strings.HasPrefix(instanceSize, instanceClass+".") {
			rec.ResourceType = instanceSize
		} else {
			rec.ResourceType = fmt.Sprintf("%s.%s", instanceClass, instanceSize)
		}
		osInfo.InstanceType = rec.ResourceType
	}
	if esDetails.Region != nil {
		rec.Region = normalizeRegionName(*esDetails.Region)
	}

	rec.Details = osInfo
	return nil
}

// parseRedshiftDetails extracts Redshift-specific details
func (c *Client) parseRedshiftDetails(_ context.Context, rec *common.Recommendation, details *types.ReservationPurchaseRecommendationDetail) error {
	if details.InstanceDetails == nil || details.InstanceDetails.RedshiftInstanceDetails == nil {
		return fmt.Errorf("Redshift instance details not found")
	}

	rsDetails := details.InstanceDetails.RedshiftInstanceDetails
	rsInfo := &common.DataWarehouseDetails{}

	if rsDetails.NodeType != nil {
		rec.ResourceType = *rsDetails.NodeType
		rsInfo.NodeType = *rsDetails.NodeType
	}
	if rsDetails.Region != nil {
		rec.Region = normalizeRegionName(*rsDetails.Region)
	}

	rsInfo.NumberOfNodes = rec.Count
	if rsInfo.NumberOfNodes == 1 {
		rsInfo.ClusterType = "single-node"
	} else {
		rsInfo.ClusterType = "multi-node"
	}

	rec.Details = rsInfo
	return nil
}

// parseMemoryDBDetails extracts MemoryDB-specific details.
//
// Cost Explorer exposes MemoryDB reserved-node offerings via
// details.InstanceDetails.MemoryDBInstanceDetails (NodeType, Region), the same
// shape ElastiCache and Redshift use. The node type comes from that struct, not
// from a hardcoded default, so we never substitute a wrong instance type.
//
// If the MemoryDB sub-struct or its NodeType is absent, the function returns an
// error so the recommendation is skipped loudly (logged by parseRecommendations)
// rather than silently guessing an offering.
func (c *Client) parseMemoryDBDetails(_ context.Context, rec *common.Recommendation, details *types.ReservationPurchaseRecommendationDetail) error {
	if details == nil || details.InstanceDetails == nil || details.InstanceDetails.MemoryDBInstanceDetails == nil {
		return fmt.Errorf("MemoryDB instance details not found; cannot determine offering - Cost Explorer did not populate instance details")
	}

	mdbDetails := details.InstanceDetails.MemoryDBInstanceDetails
	if mdbDetails.NodeType == nil || *mdbDetails.NodeType == "" {
		return fmt.Errorf("MemoryDB recommendation has no NodeType; cannot determine offering")
	}

	rec.ResourceType = *mdbDetails.NodeType
	if mdbDetails.Region != nil {
		rec.Region = normalizeRegionName(*mdbDetails.Region)
	}
	rec.Details = &common.CacheDetails{
		Engine:   "redis",
		NodeType: *mdbDetails.NodeType,
	}
	return nil
}
