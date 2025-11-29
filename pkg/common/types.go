// Package common provides cloud-agnostic types and interfaces for multi-cloud cost optimization
package common

import (
	"time"
)

// ProviderType identifies the cloud provider
type ProviderType string

const (
	ProviderAWS   ProviderType = "aws"
	ProviderAzure ProviderType = "azure"
	ProviderGCP   ProviderType = "gcp"
)

// String returns the string representation of the provider type
func (p ProviderType) String() string {
	return string(p)
}

// ServiceType identifies the service type across clouds
type ServiceType string

const (
	// Compute
	ServiceCompute ServiceType = "compute" // EC2, VM, Compute Engine

	// Database
	ServiceRelationalDB ServiceType = "relational-db" // RDS, Azure SQL, Cloud SQL
	ServiceNoSQL        ServiceType = "nosql"         // DynamoDB, CosmosDB, Firestore
	ServiceNoSQLDB      ServiceType = "nosql"         // Alias for ServiceNoSQL

	// Cache
	ServiceCache ServiceType = "cache" // ElastiCache, Azure Cache, Memorystore

	// Search
	ServiceSearch ServiceType = "search" // OpenSearch, Azure Search

	// Data Warehouse
	ServiceDataWarehouse ServiceType = "data-warehouse" // Redshift, Synapse, BigQuery

	// Storage
	ServiceStorage ServiceType = "storage" // S3, Blob Storage, Cloud Storage

	// Savings/Commitments
	ServiceSavingsPlans ServiceType = "savings-plans" // AWS Savings Plans
	ServiceCommitments  ServiceType = "commitments"   // Generic commitments

	// Other
	ServiceOther ServiceType = "other" // Catch-all for unclassified services

	// Legacy AWS service types (for backward compatibility)
	ServiceEC2           ServiceType = "ec2"
	ServiceRDS           ServiceType = "rds"
	ServiceElastiCache   ServiceType = "elasticache"
	ServiceOpenSearch    ServiceType = "opensearch"
	ServiceElasticsearch ServiceType = "opensearch" // Alias for ServiceOpenSearch (AWS rebranded)
	ServiceRedshift      ServiceType = "redshift"
	ServiceMemoryDB      ServiceType = "memorydb"
)

// String returns the string representation of the service type
func (s ServiceType) String() string {
	return string(s)
}

// CommitmentType represents different commitment types across clouds
type CommitmentType string

const (
	CommitmentReservedInstance CommitmentType = "reserved-instance" // AWS RI, Azure RI
	CommitmentSavingsPlan      CommitmentType = "savings-plan"      // AWS Savings Plans
	CommitmentCUD              CommitmentType = "committed-use"     // GCP CUD
	CommitmentReservedCapacity CommitmentType = "reserved-capacity" // Azure/GCP storage
)

// String returns the string representation of the commitment type
func (c CommitmentType) String() string {
	return string(c)
}

// Recommendation represents a commitment purchase recommendation across any cloud provider
type Recommendation struct {
	// Provider identification
	Provider    ProviderType `json:"provider" csv:"Provider"`
	Account     string       `json:"account" csv:"Account"`
	AccountName string       `json:"account_name" csv:"AccountName"`

	// Service identification
	Service ServiceType `json:"service" csv:"Service"`
	Region  string      `json:"region" csv:"Region"`

	// Resource details
	ResourceType string `json:"resource_type" csv:"ResourceType"` // Instance type, node type, VM size, etc.
	Count        int    `json:"count" csv:"Count"`

	// Commitment details
	CommitmentType CommitmentType `json:"commitment_type" csv:"CommitmentType"` // RI, SP, CUD, etc.
	Term           string         `json:"term" csv:"Term"`                      // 1yr, 3yr
	PaymentOption  string         `json:"payment_option" csv:"PaymentOption"`   // all-upfront, partial, no-upfront, monthly

	// Cost information
	OnDemandCost      float64 `json:"on_demand_cost" csv:"OnDemandCost"`
	CommitmentCost    float64 `json:"commitment_cost" csv:"CommitmentCost"`
	EstimatedSavings  float64 `json:"estimated_savings" csv:"EstimatedSavings"`
	SavingsPercentage float64 `json:"savings_percentage" csv:"SavingsPercentage"`

	// Service-specific details (polymorphic)
	Details ServiceDetails `json:"details,omitempty" csv:"-"`

	// Metadata
	SourceRecommendation string    `json:"source_recommendation,omitempty" csv:"SourceRecommendation"`
	Timestamp            time.Time `json:"timestamp,omitempty" csv:"Timestamp"`
}

// ServiceDetails is an interface for service-specific details
type ServiceDetails interface {
	GetServiceType() ServiceType
	GetDetailDescription() string
}

// PurchaseResult represents the outcome of a commitment purchase
type PurchaseResult struct {
	Recommendation Recommendation `json:"recommendation"`
	Success        bool           `json:"success"`
	CommitmentID   string         `json:"commitment_id,omitempty"`
	Error          error          `json:"error,omitempty"`
	Cost           float64        `json:"cost"`
	DryRun         bool           `json:"dry_run"`
	Timestamp      time.Time      `json:"timestamp"`
}

// Commitment represents an existing commitment (RI/SP/CUD/etc)
type Commitment struct {
	Provider       ProviderType   `json:"provider"`
	Account        string         `json:"account"`
	CommitmentID   string         `json:"commitment_id"`
	CommitmentType CommitmentType `json:"commitment_type"`
	Service        ServiceType    `json:"service"`
	Region         string         `json:"region"`
	ResourceType   string         `json:"resource_type"`
	Engine         string         `json:"engine,omitempty"` // Database engine for RDS/ElastiCache (e.g., "mysql", "aurora-postgresql")
	Count          int            `json:"count"`
	StartDate      time.Time      `json:"start_date"`
	EndDate        time.Time      `json:"end_date"`
	State          string         `json:"state"`
	Cost           float64        `json:"cost"`
}

// OfferingDetails represents cloud provider offering details
type OfferingDetails struct {
	OfferingID          string  `json:"offering_id"`
	ResourceType        string  `json:"resource_type"`
	Term                string  `json:"term"`
	PaymentOption       string  `json:"payment_option"`
	UpfrontCost         float64 `json:"upfront_cost"`
	RecurringCost       float64 `json:"recurring_cost"`
	TotalCost           float64 `json:"total_cost"`
	EffectiveHourlyRate float64 `json:"effective_hourly_rate"`
	Currency            string  `json:"currency"`
}

// RecommendationParams represents parameters for fetching recommendations
type RecommendationParams struct {
	Service        ServiceType
	Region         string
	LookbackPeriod string // 7d, 30d, 60d
	Term           string // 1yr, 3yr
	PaymentOption  string
	AccountFilter  []string
	IncludeRegions []string
	ExcludeRegions []string
}

// Account represents a cloud account/subscription/project
type Account struct {
	Provider    ProviderType `json:"provider"`
	ID          string       `json:"id"`
	Name        string       `json:"name"`
	DisplayName string       `json:"display_name"`
	IsDefault   bool         `json:"is_default"`
}

// Region represents a cloud region/location
type Region struct {
	Provider    ProviderType `json:"provider"`
	ID          string       `json:"id"`
	Name        string       `json:"name"`
	DisplayName string       `json:"display_name"`
}

// ComputeDetails represents compute-specific details (EC2, VM, Compute Engine)
type ComputeDetails struct {
	InstanceType string `json:"instance_type"`
	Platform     string `json:"platform"` // linux, windows
	Tenancy      string `json:"tenancy"`  // default, dedicated, host
	Scope        string `json:"scope"`    // regional, zonal
}

func (d ComputeDetails) GetServiceType() ServiceType {
	return ServiceCompute
}

func (d ComputeDetails) GetDetailDescription() string {
	return d.Platform + "/" + d.Tenancy
}

// DatabaseDetails represents database-specific details (RDS, Azure SQL, Cloud SQL)
type DatabaseDetails struct {
	Engine        string `json:"engine"` // mysql, postgres, sqlserver, etc.
	EngineVersion string `json:"engine_version,omitempty"`
	AZConfig      string `json:"az_config"` // single-az, multi-az
	InstanceClass string `json:"instance_class"`
	Deployment    string `json:"deployment,omitempty"` // Azure: single, pool
}

func (d DatabaseDetails) GetServiceType() ServiceType {
	return ServiceRelationalDB
}

func (d DatabaseDetails) GetDetailDescription() string {
	return d.Engine + "/" + d.AZConfig
}

// CacheDetails represents cache-specific details (ElastiCache, Azure Cache, Memorystore)
type CacheDetails struct {
	Engine   string `json:"engine"`    // redis, memcached
	NodeType string `json:"node_type"`
	Shards   int    `json:"shards,omitempty"`
}

func (d CacheDetails) GetServiceType() ServiceType {
	return ServiceCache
}

func (d CacheDetails) GetDetailDescription() string {
	return d.Engine + "/" + d.NodeType
}

// SearchDetails represents search-specific details (OpenSearch, Azure Search)
type SearchDetails struct {
	InstanceType    string `json:"instance_type"`
	MasterNodeCount int    `json:"master_node_count,omitempty"`
	MasterNodeType  string `json:"master_node_type,omitempty"`
}

func (d SearchDetails) GetServiceType() ServiceType {
	return ServiceSearch
}

func (d SearchDetails) GetDetailDescription() string {
	return d.InstanceType
}

// DataWarehouseDetails represents data warehouse-specific details (Redshift, Synapse, BigQuery)
type DataWarehouseDetails struct {
	NodeType      string `json:"node_type"`
	NumberOfNodes int    `json:"number_of_nodes"`
	ClusterType   string `json:"cluster_type,omitempty"` // single-node, multi-node
}

func (d DataWarehouseDetails) GetServiceType() ServiceType {
	return ServiceDataWarehouse
}

func (d DataWarehouseDetails) GetDetailDescription() string {
	return d.NodeType
}

// SavingsPlanDetails represents AWS Savings Plans specific details
type SavingsPlanDetails struct {
	PlanType         string  `json:"plan_type"`        // Compute, EC2Instance, SageMaker
	HourlyCommitment float64 `json:"hourly_commitment"`
	Coverage         string  `json:"coverage,omitempty"`
}

func (d SavingsPlanDetails) GetServiceType() ServiceType {
	return ServiceSavingsPlans
}

func (d SavingsPlanDetails) GetDetailDescription() string {
	return d.PlanType
}
