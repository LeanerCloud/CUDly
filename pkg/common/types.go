// Package common provides cloud-agnostic types and interfaces for multi-cloud cost optimization
package common

import (
	"encoding/json"
	"fmt"
	"strings"
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

	// Cache
	ServiceCache ServiceType = "cache" // ElastiCache, Azure Cache, Memorystore

	// Search
	ServiceSearch ServiceType = "search" // OpenSearch, Azure Search

	// Data Warehouse
	ServiceDataWarehouse ServiceType = "data-warehouse" // Redshift, Synapse, BigQuery

	// Storage
	ServiceStorage ServiceType = "storage" // S3, Blob Storage, Cloud Storage

	// Savings/Commitments
	//
	// ServiceSavingsPlans is the canonical umbrella identifier for AWS Savings
	// Plans. The string value "savingsplans" (no hyphen) matches the frontend's
	// identifier and the value persisted in service_configs.service /
	// purchase_history.service so that direct comparisons
	// (rec.Service == ServiceSavingsPlans) work without a normaliser. See
	// issue #85 for the rationale (frontend chosen as canonical to avoid a
	// SQL data migration). Code that needs to recognise pre-#85 persisted
	// "savings-plans" rows (e.g. purchase_executions JSONB blobs) goes
	// through the mapper in internal/purchase/execution.go.
	ServiceSavingsPlans ServiceType = "savingsplans" // AWS Savings Plans (umbrella)

	// Per-plan-type Savings Plans slugs. Each maps 1:1 to an AWS
	// types.SupportedSavingsPlansType so users can configure term/payment
	// defaults independently per plan type. These were introduced after the
	// umbrella was normalised; the dash-form slugs intentionally differ from
	// the umbrella's "savingsplans" so a generic-vs-specific comparison is
	// unambiguous (use IsSavingsPlan to recognise the family).
	ServiceSavingsPlansCompute     ServiceType = "savings-plans-compute"     // ComputeSp: EC2, Fargate, Lambda
	ServiceSavingsPlansEC2Instance ServiceType = "savings-plans-ec2instance" // Ec2InstanceSp: specific EC2 families
	ServiceSavingsPlansSageMaker   ServiceType = "savings-plans-sagemaker"   // SagemakerSp
	ServiceSavingsPlansDatabase    ServiceType = "savings-plans-database"    // DatabaseSp: RDS
	ServiceCommitments             ServiceType = "commitments"               // Generic commitments

	// Other
	ServiceOther ServiceType = "other" // Catch-all for unclassified services

	// Legacy AWS service types (for backward compatibility)
	ServiceEC2         ServiceType = "ec2"
	ServiceRDS         ServiceType = "rds"
	ServiceElastiCache ServiceType = "elasticache"
	ServiceOpenSearch  ServiceType = "opensearch"
	// ServiceElasticsearch is a typed alias of ServiceOpenSearch — a future
	// const declared with the same string value but different intent will
	// now produce a compile error rather than silently equal.
	ServiceElasticsearch             = ServiceOpenSearch
	ServiceRedshift      ServiceType = "redshift"
	ServiceMemoryDB      ServiceType = "memorydb"
)

// String returns the string representation of the service type
func (s ServiceType) String() string {
	return string(s)
}

// IsSavingsPlan reports whether s is any Savings Plans service slug —
// the legacy umbrella (ServiceSavingsPlans), any of the four per-plan-type
// constants, or the dash-free frontend spelling "savingsplans" that the API
// handler stores verbatim without normalisation. Use it when code needs to
// recognise the Savings Plans family irrespective of plan type (e.g., stats
// aggregation, region-ignoring filters, display-name branching).
func IsSavingsPlan(s ServiceType) bool {
	switch s {
	case ServiceSavingsPlans,
		ServiceSavingsPlansCompute,
		ServiceSavingsPlansEC2Instance,
		ServiceSavingsPlansSageMaker,
		ServiceSavingsPlansDatabase:
		return true
	}
	return string(s) == "savingsplans"
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
	// RecurringMonthlyCost is the recurring monthly charge for this commitment
	// (i.e. the part the user pays every month after any upfront payment).
	// nil means the provider API did not return a monthly breakdown — the
	// frontend renders nil as "—" rather than "$0" to avoid misleading users.
	// Populated by cloud parsers when the API exposes it; left nil otherwise.
	RecurringMonthlyCost *float64 `json:"recurring_monthly_cost,omitempty" csv:"RecurringMonthlyCost"`

	// Service-specific details (polymorphic)
	Details ServiceDetails `json:"details,omitempty" csv:"-"`

	// Metadata
	SourceRecommendation string    `json:"source_recommendation,omitempty" csv:"SourceRecommendation"`
	Timestamp            time.Time `json:"timestamp,omitempty" csv:"Timestamp"`

	// Break-even in months (populated by cloud parsers where available; used by scorer filter)
	BreakEvenMonths float64 `json:"break_even_months,omitempty" csv:"BreakEvenMonths"`

	// Utilization signals — populated by cloud parsers when the API exposes them.
	// Used by --target-coverage sizing (see cmd/helpers.go: ApplyTargetCoverage).
	// AverageInstancesUsedPerHour is RI-only (zero for SPs and other commitment types).
	// RecommendedUtilization is "what AWS projects for the full recommendation"
	// (%). ProjectedUtilization / ProjectedCoverage are populated by the sizing
	// step after we pick our own quantity.
	// RecommendedCount is AWS's pre-sizing count (mirrors Count before
	// ApplyCoverage / ApplyTargetCoverage mutates Count); zero for SPs since
	// the SP commitment is dollar-denominated rather than count-denominated.
	// ExistingCoveragePct is the share of demand already covered by existing
	// commitments in the same pool (from CE GetReservationCoverage /
	// GetSavingsPlansCoverage). Zero = "no signal" (CE returned nothing for
	// this pool, or the fetch step wasn't run); sizing then degenerates to
	// the no-existing-commitments path. See cmd/helpers.go.
	AverageInstancesUsedPerHour float64 `json:"average_instances_used_per_hour,omitempty" csv:"AverageInstancesUsedPerHour"`
	RecommendedUtilization      float64 `json:"recommended_utilization,omitempty" csv:"RecommendedUtilization"`
	RecommendedCount            int     `json:"recommended_count,omitempty" csv:"RecommendedCount"`
	ExistingCoveragePct         float64 `json:"existing_coverage_pct,omitempty" csv:"ExistingCoveragePct"`
	// ExistingCoverageKnown distinguishes "CE returned a value for this
	// pool" (Known=true, Pct possibly 0.0 meaning the pool has running
	// instances but no RI coverage yet) from "CE has no data for this
	// pool" (Known=false, Pct=0.0 by default). Set by
	// ApplyCoverageMapToRecommendations whenever a pool lookup hits, and
	// by family-NU sizing when a family-level existing% lands on the rec.
	// CSV writers use this to render "n/a" for unknown vs "0.0" for
	// genuine zero-coverage pools.
	ExistingCoverageKnown bool    `json:"existing_coverage_known,omitempty" csv:"-"`
	ProjectedUtilization  float64 `json:"projected_utilization,omitempty" csv:"ProjectedUtilization"`
	ProjectedCoverage     float64 `json:"projected_coverage,omitempty" csv:"ProjectedCoverage"`

	// RawRecommendation holds the original cloud API response bytes for audit/debugging.
	// omitempty ensures nil is absent from JSON (not written as null).
	RawRecommendation json.RawMessage `json:"raw_recommendation,omitempty" csv:"-"`
}

// ServiceDetails is an interface for service-specific details
type ServiceDetails interface {
	GetServiceType() ServiceType
	GetDetailDescription() string
}

// ScaleRecommendationCosts multiplies all cost-bearing fields of rec by
// ratio and returns the result. RecurringMonthlyCost is allocated as a
// new pointer when present so callers don't mutate the upstream rec's
// pointer target. Used by sizing paths (ApplyCoverage, ApplyTargetCoverage,
// family-NU) to keep Count and cost in sync when a recommendation is
// sized down (or up) from AWS's proposal — without this helper the same
// four-field scaling pattern was duplicated at every sizing site.
func ScaleRecommendationCosts(rec Recommendation, ratio float64) Recommendation {
	rec.CommitmentCost *= ratio
	rec.OnDemandCost *= ratio
	rec.EstimatedSavings *= ratio
	if rec.RecurringMonthlyCost != nil {
		scaled := *rec.RecurringMonthlyCost * ratio
		rec.RecurringMonthlyCost = &scaled
	}
	return rec
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

// Source values for PurchaseOptions.Source. Kept lowercase so they can be used
// directly as GCP label values (GCP labels must be lowercase) and match AWS
// tag / Azure reservation tag conventions.
const (
	PurchaseSourceCLI = "cudly-cli"
	PurchaseSourceWeb = "cudly-web"
)

// PurchaseTagKey is the tag/label key every CUDly-purchased commitment carries
// so customers and CUDly itself can attribute commitments back to the tool.
const PurchaseTagKey = "purchase-automation"

// PurchaseOptions carries per-execution metadata threaded through
// ServiceClient.PurchaseCommitment. Source is the CUDly surface that triggered
// the purchase (CLI vs web); every provider stamps it onto the commitment it
// creates (as a tag, label, or — where the cloud API permits nothing else —
// encoded in the commitment description).
type PurchaseOptions struct {
	Source string
}

// NormalizeSource lowercases s and returns it when it matches an allowed
// source. Returns an error on anything else so cloud tags cannot be polluted
// with arbitrary caller-supplied strings (which would be impossible to
// retroactively remove from committed resources).
func NormalizeSource(s string) (string, error) {
	lower := strings.ToLower(strings.TrimSpace(s))
	switch lower {
	case PurchaseSourceCLI, PurchaseSourceWeb:
		return lower, nil
	case "":
		return "", fmt.Errorf("purchase source is required")
	default:
		return "", fmt.Errorf("invalid purchase source %q (allowed: %s, %s)", s, PurchaseSourceCLI, PurchaseSourceWeb)
	}
}

// Commitment represents an existing commitment (RI/SP/CUD/etc).
//
// Deployment is RDS-specific (Multi-AZ vs Single-AZ); it stays empty for
// services that don't have a deployment dimension (EC2, ElastiCache,
// etc). When populated, it carries the same vocabulary as
// DatabaseDetails.AZConfig on Recommendation ("single-az" / "multi-az")
// so pool-key matching can collapse both sides via normaliseDeployment
// in the recommendations package. Without this field, RDS expiry
// adjustments would silently miss because Recommendation lookup keys
// are deployment-aware while commitment keys defaulted to empty.
type Commitment struct {
	Provider       ProviderType   `json:"provider"`
	Account        string         `json:"account"`
	CommitmentID   string         `json:"commitment_id"`
	CommitmentType CommitmentType `json:"commitment_type"`
	Service        ServiceType    `json:"service"`
	Region         string         `json:"region"`
	ResourceType   string         `json:"resource_type"`
	Engine         string         `json:"engine,omitempty"`     // Database engine for RDS/ElastiCache (e.g., "mysql", "aurora-postgresql")
	Deployment     string         `json:"deployment,omitempty"` // RDS Multi-AZ vs Single-AZ; empty for non-RDS
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
	// Savings Plans specific filters
	IncludeSPTypes []string // Compute, EC2Instance, SageMaker, Database
	ExcludeSPTypes []string
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

// ComputeDetails represents compute-specific details (EC2, VM, Compute Engine).
//
// VCPU + MemoryGB are populated by per-provider catalogue lookups when
// available (Azure: armcompute.ResourceSKU.Capabilities; AWS:
// ec2:DescribeInstanceTypes; GCP: machine-type catalogue). They are
// optional — converters that don't yet wire a catalogue leave them at the
// zero value, and the JSON tag uses omitempty so unknown values don't
// pollute the API payload.
type ComputeDetails struct {
	InstanceType string  `json:"instance_type"`
	Platform     string  `json:"platform"`            // linux, windows
	Tenancy      string  `json:"tenancy"`             // default, dedicated, host
	Scope        string  `json:"scope"`               // regional, zonal
	VCPU         int     `json:"vcpu,omitempty"`      // 0 = unknown
	MemoryGB     float64 `json:"memory_gb,omitempty"` // 0 = unknown
}

func (d ComputeDetails) GetServiceType() ServiceType {
	return ServiceCompute
}

// GetDetailDescription returns a short human description of the compute
// recommendation. The base form is "<platform>/<tenancy>"; when both VCPU
// and MemoryGB are populated (>0) the size is appended as
// " (<vcpu> vCPU / <memory> GB)" to give the UI a one-line summary
// without forcing the caller to inspect the struct.
func (d ComputeDetails) GetDetailDescription() string {
	base := d.Platform + "/" + d.Tenancy
	if d.VCPU > 0 && d.MemoryGB > 0 {
		// %g trims trailing zeros (16 GB, not 16.000000 GB) but keeps
		// fractional sizes (e.g. 0.5 GB for the smallest Azure SKUs).
		return fmt.Sprintf("%s (%d vCPU / %g GB)", base, d.VCPU, d.MemoryGB)
	}
	return base
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
	Engine   string `json:"engine"` // redis, memcached
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

// NoSQLDetails represents NoSQL-specific details (Cosmos DB, DynamoDB,
// Firestore). Engine is the provider-level family ("cosmos", "dynamodb",
// "firestore"); APIType is the cosmos-specific sub-API (sql, mongodb,
// cassandra, gremlin, table) and stays empty for non-cosmos engines.
// ThroughputUnits is the reserved-throughput unit (RU/s for cosmos).
// Zero-value fields indicate the source payload didn't supply the data
// (e.g. SKU string lacks a throughput tier or API-type hint) — do NOT
// treat zero as "definitely zero", only as "unknown".
type NoSQLDetails struct {
	Engine          string `json:"engine"`
	APIType         string `json:"api_type,omitempty"`
	ThroughputUnits int    `json:"throughput_units,omitempty"`
}

func (d NoSQLDetails) GetServiceType() ServiceType {
	return ServiceNoSQL
}

func (d NoSQLDetails) GetDetailDescription() string {
	if d.APIType == "" {
		return d.Engine
	}
	return d.Engine + "/" + d.APIType
}

// SavingsPlanDetails represents AWS Savings Plans specific details
type SavingsPlanDetails struct {
	PlanType         string  `json:"plan_type"` // Compute, EC2Instance, SageMaker
	HourlyCommitment float64 `json:"hourly_commitment"`
	Coverage         string  `json:"coverage,omitempty"`
}

func (d SavingsPlanDetails) GetServiceType() ServiceType {
	return ServiceSavingsPlans
}

func (d SavingsPlanDetails) GetDetailDescription() string {
	return d.PlanType
}

// AuditRecord is one line in the JSON-lines audit log.
// Status values: "success", "error", "skipped" (dry-run), "skipped_covered" (idempotency).
type AuditRecord struct {
	RunID             string          `json:"run_id"`
	Provider          ProviderType    `json:"provider"`
	AccountID         string          `json:"account_id"`
	AccountName       string          `json:"account_name"`
	Region            string          `json:"region"`
	Service           string          `json:"service"`
	ResourceType      string          `json:"resource_type"`
	CommitmentType    CommitmentType  `json:"commitment_type"`
	Term              int             `json:"term_months"`
	Count             int             `json:"count"`
	EstimatedCost     float64         `json:"estimated_cost"`
	EstimatedSavings  float64         `json:"estimated_savings"`
	CommitmentID      string          `json:"commitment_id"`
	Status            string          `json:"status"`
	ErrorMessage      string          `json:"error_message"`
	Timestamp         time.Time       `json:"timestamp"`
	DryRun            bool            `json:"dry_run"`
	RawRecommendation json.RawMessage `json:"raw_recommendation,omitempty"`
	// Source identifies the CUDly surface (cudly-cli / cudly-web) that
	// triggered the purchase. Mirrors the value stamped on the commitment
	// itself via purchase-automation tag/label.
	Source string `json:"source,omitempty"`
}
