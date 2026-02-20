package recommendations

import (
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/costexplorer/types"

	"github.com/LeanerCloud/CUDly/pkg/common"
)

// parseRDSDetails extracts RDS-specific details
func (c *Client) parseRDSDetails(rec *common.Recommendation, details *types.ReservationPurchaseRecommendationDetail) error {
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
func (c *Client) parseElastiCacheDetails(rec *common.Recommendation, details *types.ReservationPurchaseRecommendationDetail) error {
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

// parseEC2Details extracts EC2-specific details
func (c *Client) parseEC2Details(rec *common.Recommendation, details *types.ReservationPurchaseRecommendationDetail) error {
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
	if ec2Details.Tenancy != nil {
		ec2Info.Tenancy = *ec2Details.Tenancy
	} else {
		ec2Info.Tenancy = "shared"
	}

	if ec2Details.AvailabilityZone != nil && *ec2Details.AvailabilityZone != "" {
		ec2Info.Scope = "availability-zone"
	} else {
		ec2Info.Scope = "region"
	}

	rec.Details = ec2Info
	return nil
}

// parseOpenSearchDetails extracts OpenSearch-specific details
func (c *Client) parseOpenSearchDetails(rec *common.Recommendation, details *types.ReservationPurchaseRecommendationDetail) error {
	if details.InstanceDetails == nil || details.InstanceDetails.ESInstanceDetails == nil {
		return fmt.Errorf("OpenSearch/Elasticsearch instance details not found")
	}

	esDetails := details.InstanceDetails.ESInstanceDetails
	osInfo := &common.SearchDetails{}

	if esDetails.InstanceClass != nil && esDetails.InstanceSize != nil {
		rec.ResourceType = fmt.Sprintf("%s.%s", *esDetails.InstanceClass, *esDetails.InstanceSize)
		osInfo.InstanceType = rec.ResourceType
	}
	if esDetails.Region != nil {
		rec.Region = normalizeRegionName(*esDetails.Region)
	}

	rec.Details = osInfo
	return nil
}

// parseRedshiftDetails extracts Redshift-specific details
func (c *Client) parseRedshiftDetails(rec *common.Recommendation, details *types.ReservationPurchaseRecommendationDetail) error {
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

// parseMemoryDBDetails extracts MemoryDB-specific details
func (c *Client) parseMemoryDBDetails(rec *common.Recommendation, details *types.ReservationPurchaseRecommendationDetail) error {
	// MemoryDB might not have specific details in Cost Explorer yet
	rec.ResourceType = "db.r6gd.xlarge" // Default
	rec.Details = &common.CacheDetails{
		Engine:   "redis",
		NodeType: rec.ResourceType,
	}
	return nil
}
