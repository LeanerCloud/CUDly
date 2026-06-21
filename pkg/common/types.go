// Package common provides cloud-agnostic types and interfaces for multi-cloud cost optimization.
package common

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// ErrCommitmentPurchaseNotSupported is returned by ServiceClient.PurchaseCommitment
// implementations for services that have no programmatic committed-use / commitment
// purchase API. Their recommendations are advisory only: callers must surface them
// for human action rather than auto-purchasing. Returning this sentinel (instead of
// silently provisioning a billable resource) guarantees a "purchase" never creates
// infrastructure. Callers can detect it with errors.Is(err, ErrCommitmentPurchaseNotSupported).
var ErrCommitmentPurchaseNotSupported = errors.New("commitment purchase not supported for this service")

// ProviderType identifies the cloud provider.
type ProviderType string

const (
	ProviderAWS   ProviderType = "aws"
	ProviderAzure ProviderType = "azure"
	ProviderGCP   ProviderType = "gcp"
)

// String returns the string representation of the provider type.
func (p ProviderType) String() string {
	return string(p)
}

// ServiceType identifies the service type across clouds.
type ServiceType string

const (
	// Compute.
	ServiceCompute ServiceType = "compute" // EC2, VM, Compute Engine

	// Database.
	ServiceRelationalDB ServiceType = "relational-db" // RDS, Azure SQL, Cloud SQL
	ServiceNoSQL        ServiceType = "nosql"         // DynamoDB, CosmosDB, Firestore

	// Cache.
	ServiceCache ServiceType = "cache" // ElastiCache, Azure Cache, Memorystore

	// Search.
	ServiceSearch ServiceType = "search" // OpenSearch, Azure Search

	// Data Warehouse.
	ServiceDataWarehouse ServiceType = "data-warehouse" // Redshift, Synapse, BigQuery

	// Storage.
	ServiceStorage ServiceType = "storage" // S3, Blob Storage, Cloud Storage

	// Savings/Commitments.
	//
	// ServiceSavingsPlans is the canonical umbrella identifier for AWS Savings
	// Plans. The string value "savingsplans" (no hyphen) matches the frontend's
	// identifier and the value persisted in service_configs.service /
	// purchase_history.service so that direct comparisons
	// (rec.Service == ServiceSavingsPlans) work without a normalizer. See
	// issue #85 for the rationale (frontend chosen as canonical to avoid a
	// SQL data migration). Code that needs to recognize pre-#85 persisted
	// "savings-plans" rows (e.g. purchase_executions JSONB blobs) goes
	// through the mapper in internal/purchase/execution.go.
	ServiceSavingsPlans ServiceType = "savingsplans" // AWS Savings Plans (umbrella)

	// Per-plan-type Savings Plans slugs. Each maps 1:1 to an AWS
	// types.SupportedSavingsPlansType so users can configure term/payment
	// defaults independently per plan type. These were introduced after the
	// umbrella was normalized; the dash-form slugs intentionally differ from
	// the umbrella's "savingsplans" so a generic-vs-specific comparison is
	// unambiguous (use IsSavingsPlan to recognize the family).
	ServiceSavingsPlansCompute     ServiceType = "savings-plans-compute"     // ComputeSp: EC2, Fargate, Lambda
	ServiceSavingsPlansEC2Instance ServiceType = "savings-plans-ec2instance" // Ec2InstanceSp: specific EC2 families
	ServiceSavingsPlansSageMaker   ServiceType = "savings-plans-sagemaker"   // SagemakerSp
	ServiceSavingsPlansDatabase    ServiceType = "savings-plans-database"    // DatabaseSp: RDS
	ServiceCommitments             ServiceType = "commitments"               // Generic commitments

	// Other.
	ServiceOther ServiceType = "other" // Catch-all for unclassified services

	// Legacy AWS service types (for backward compatibility).
	ServiceEC2         ServiceType = "ec2"
	ServiceRDS         ServiceType = "rds"
	ServiceElastiCache ServiceType = "elasticache"
	ServiceOpenSearch  ServiceType = "opensearch"
	// ServiceElasticsearch is a typed alias of ServiceOpenSearch -- a future
	// const declared with the same string value but different intent will
	// now produce a compile error rather than silently equal.
	ServiceElasticsearch             = ServiceOpenSearch
	ServiceRedshift      ServiceType = "redshift"
	ServiceMemoryDB      ServiceType = "memorydb"
)

// String returns the string representation of the service type.
func (s ServiceType) String() string {
	return string(s)
}

// IsSavingsPlan reports whether s is any Savings Plans service slug --
// the legacy umbrella (ServiceSavingsPlans), any of the four per-plan-type
// constants, or the dash-free frontend spelling "savingsplans" that the API
// handler stores verbatim without normalization. Use it when code needs to
// recognize the Savings Plans family irrespective of plan type (e.g., stats
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

// CommitmentType represents different commitment types across clouds.
type CommitmentType string

const (
	CommitmentReservedInstance CommitmentType = "reserved-instance" // AWS RI, Azure RI
	CommitmentSavingsPlan      CommitmentType = "savings-plan"      // AWS Savings Plans
	CommitmentCUD              CommitmentType = "committed-use"     // GCP CUD
	CommitmentReservedCapacity CommitmentType = "reserved-capacity" // Azure/GCP storage
)

// String returns the string representation of the commitment type.
func (c CommitmentType) String() string {
	return string(c)
}

// Recommendation represents a commitment purchase recommendation across any cloud provider.
type Recommendation struct {
	Timestamp                   time.Time       `json:"timestamp,omitempty" csv:"Timestamp"`
	Details                     ServiceDetails  `json:"details,omitempty" csv:"-"`
	RecurringMonthlyCost        *float64        `json:"recurring_monthly_cost,omitempty" csv:"RecurringMonthlyCost"`
	CommitmentType              CommitmentType  `json:"commitment_type" csv:"CommitmentType"`
	AccountName                 string          `json:"account_name" csv:"AccountName"`
	ResourceType                string          `json:"resource_type" csv:"ResourceType"`
	Provider                    ProviderType    `json:"provider" csv:"Provider"`
	Term                        string          `json:"term" csv:"Term"`
	PaymentOption               string          `json:"payment_option" csv:"PaymentOption"`
	Region                      string          `json:"region" csv:"Region"`
	Account                     string          `json:"account" csv:"Account"`
	SourceRecommendation        string          `json:"source_recommendation,omitempty" csv:"SourceRecommendation"`
	Service                     ServiceType     `json:"service" csv:"Service"`
	UsageHistory                []float64       `json:"usage_history,omitempty" csv:"-"`
	RawRecommendation           json.RawMessage `json:"raw_recommendation,omitempty" csv:"-"`
	OnDemandCost                float64         `json:"on_demand_cost" csv:"OnDemandCost"`
	SavingsPercentage           float64         `json:"savings_percentage" csv:"SavingsPercentage"`
	EstimatedSavings            float64         `json:"estimated_savings" csv:"EstimatedSavings"`
	BreakEvenMonths             float64         `json:"break_even_months,omitempty" csv:"BreakEvenMonths"`
	AverageInstancesUsedPerHour float64         `json:"average_instances_used_per_hour,omitempty" csv:"AverageInstancesUsedPerHour"`
	RecommendedUtilization      float64         `json:"recommended_utilization,omitempty" csv:"RecommendedUtilization"`
	RecommendedCount            int             `json:"recommended_count,omitempty" csv:"RecommendedCount"`
	ExistingCoveragePct         float64         `json:"existing_coverage_pct,omitempty" csv:"ExistingCoveragePct"`
	ProjectedUtilization        float64         `json:"projected_utilization,omitempty" csv:"ProjectedUtilization"`
	ProjectedCoverage           float64         `json:"projected_coverage,omitempty" csv:"ProjectedCoverage"`
	CommitmentCost              float64         `json:"commitment_cost" csv:"CommitmentCost"`
	Count                       int             `json:"count" csv:"Count"`
	ExistingCoverageKnown       bool            `json:"existing_coverage_known,omitempty" csv:"-"`
}

// ServiceDetails is an interface for service-specific details.
type ServiceDetails interface {
	GetServiceType() ServiceType
	GetDetailDescription() string
}

// ScaleRecommendationCosts multiplies all cost-bearing fields of rec by
// ratio and returns the result. RecurringMonthlyCost is allocated as a
// new pointer when present so callers don't mutate the upstream rec's
// pointer target. Used by sizing paths (ApplyCoverage, ApplyTargetCoverage,
// family-NU) to keep Count and cost in sync when a recommendation is
// sized down (or up) from AWS's proposal -- without this helper the same
// four-field scaling pattern was duplicated at every sizing site.
func ScaleRecommendationCosts(rec *Recommendation, ratio float64) Recommendation {
	scaled := *rec
	scaled.CommitmentCost *= ratio
	scaled.OnDemandCost *= ratio
	scaled.EstimatedSavings *= ratio
	if scaled.RecurringMonthlyCost != nil {
		v := *scaled.RecurringMonthlyCost * ratio
		scaled.RecurringMonthlyCost = &v
	}
	return scaled
}

// PurchaseResult represents the outcome of a commitment purchase.
type PurchaseResult struct {
	Timestamp      time.Time      `json:"timestamp"`
	Error          error          `json:"error,omitempty"`
	CommitmentID   string         `json:"commitment_id,omitempty"`
	Recommendation Recommendation `json:"recommendation"`
	Cost           float64        `json:"cost"`
	Success        bool           `json:"success"`
	DryRun         bool           `json:"dry_run"`
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

// IdempotencyTagKey is the tag key under which the deterministic per-rec
// idempotency token (see DeriveIdempotencyToken) is stamped on commitments that
// the cloud API cannot dedupe natively. EC2 Reserved Instances have no
// ClientToken on PurchaseReservedInstancesOffering, so the EC2 client checks for
// an already-tagged RI before purchasing and tags the freshly-bought RI with
// this key afterwards, making a re-driven purchase idempotent (issue #636).
const IdempotencyTagKey = "cudly-idempotency-token"

// PurchaseOptions carries per-execution metadata threaded through
// ServiceClient.PurchaseCommitment. Source is the CUDly surface that triggered
// the purchase (CLI vs web); every provider stamps it onto the commitment it
// creates (as a tag, label, or -- where the cloud API permits nothing else --
// encoded in the commitment description).
type PurchaseOptions struct {
	Source string
	// ReservationID, when set, is used as the provider-side commitment
	// identifier (e.g. RDS ReservedDBInstanceId) so purchased commitments
	// carry a descriptive, account/engine/region-aware name instead of a
	// generic auto-generated one. Providers sanitize it to their ID rules.
	ReservationID string
	// IdempotencyToken, when non-empty, is a deterministic token (see
	// DeriveIdempotencyToken) that makes commitment creation idempotent across
	// re-drives of the same execution (issue #636). Savings Plans pass it as
	// the native CreateSavingsPlan ClientToken; EC2 RIs (which have no native
	// token) check for an existing RI tagged with it before purchasing and tag
	// the new RI with it afterwards. Empty means no idempotency guard (the CLI
	// purchase path, which has no owning execution, leaves it empty and keeps
	// its prior non-idempotent behavior).
	IdempotencyToken string
	// ExecutionID, when non-empty, is the purchase_executions row UUID that
	// owns this purchase attempt. Carried so the purchase-execution flow can
	// emit log lines tagged with the execution ID for correlation with the
	// CloudWatch / DB execution row (issue #667). The CLI purchase path has
	// no owning execution and leaves it empty.
	ExecutionID string
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
// so pool-key matching can collapse both sides via normalizeDeployment
// in the recommendations package. Without this field, RDS expiry
// adjustments would silently miss because Recommendation lookup keys
// are deployment-aware while commitment keys defaulted to empty.
type Commitment struct {
	StartDate      time.Time      `json:"start_date"`
	EndDate        time.Time      `json:"end_date"`
	ResourceType   string         `json:"resource_type"`
	CommitmentType CommitmentType `json:"commitment_type"`
	Service        ServiceType    `json:"service"`
	Region         string         `json:"region"`
	Provider       ProviderType   `json:"provider"`
	Engine         string         `json:"engine,omitempty"`
	Deployment     string         `json:"deployment,omitempty"`
	CommitmentID   string         `json:"commitment_id"`
	Account        string         `json:"account"`
	State          string         `json:"state"`
	Count          int            `json:"count"`
	Cost           float64        `json:"cost"`
}

// OfferingDetails represents cloud provider offering details.
type OfferingDetails struct {
	OfferingID          string  `json:"offering_id"`
	ResourceType        string  `json:"resource_type"`
	Term                string  `json:"term"`
	PaymentOption       string  `json:"payment_option"`
	Currency            string  `json:"currency"`
	UpfrontCost         float64 `json:"upfront_cost"`
	RecurringCost       float64 `json:"recurring_cost"`
	TotalCost           float64 `json:"total_cost"`
	EffectiveHourlyRate float64 `json:"effective_hourly_rate"`
}

// RecommendationParams represents parameters for fetching recommendations.
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

// Account represents a cloud account/subscription/project.
type Account struct {
	Provider    ProviderType `json:"provider"`
	ID          string       `json:"id"`
	Name        string       `json:"name"`
	DisplayName string       `json:"display_name"`
	IsDefault   bool         `json:"is_default"`
}

// Region represents a cloud region/location.
type Region struct {
	Provider    ProviderType `json:"provider"`
	ID          string       `json:"id"`
	Name        string       `json:"name"`
	DisplayName string       `json:"display_name"`
}

// ComputeDetails represents compute-specific details (EC2, VM, Compute Engine).
//
// VCPU + MemoryGB are populated by per-provider catalog lookups when
// available (Azure: armcompute.ResourceSKU.Capabilities; AWS:
// ec2:DescribeInstanceTypes; GCP: machine-type catalog). They are
// optional -- converters that don't yet wire a catalog leave them at the
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

func (d ComputeDetails) GetServiceType() ServiceType { //nolint:gocritic // hugeParam: value receiver required by ServiceDetails interface; providers/azure assigns ComputeDetails by value
	return ServiceCompute
}

// GetDetailDescription returns a short human description of the compute
// recommendation. The base form is "<platform>/<tenancy>"; when both VCPU
// and MemoryGB are populated (>0) the size is appended as
// " (<vcpu> vCPU / <memory> GB)" to give the UI a one-line summary
// without forcing the caller to inspect the struct.
func (d ComputeDetails) GetDetailDescription() string { //nolint:gocritic // hugeParam: value receiver required by ServiceDetails interface; providers/azure assigns ComputeDetails by value
	base := d.Platform + "/" + d.Tenancy
	if d.VCPU > 0 && d.MemoryGB > 0 {
		// %g trims trailing zeros (16 GB, not 16.000000 GB) but keeps
		// fractional sizes (e.g. 0.5 GB for the smallest Azure SKUs).
		return fmt.Sprintf("%s (%d vCPU / %g GB)", base, d.VCPU, d.MemoryGB)
	}
	return base
}

// DatabaseDetails represents database-specific details (RDS, Azure SQL, Cloud SQL).
type DatabaseDetails struct {
	Engine        string `json:"engine"` // mysql, postgres, sqlserver, etc.
	EngineVersion string `json:"engine_version,omitempty"`
	AZConfig      string `json:"az_config"` // single-az, multi-az
	InstanceClass string `json:"instance_class"`
	Deployment    string `json:"deployment,omitempty"` // Azure: single, pool
}

func (d DatabaseDetails) GetServiceType() ServiceType { //nolint:gocritic // hugeParam: value receiver required by ServiceDetails interface; providers/azure assigns DatabaseDetails by value
	return ServiceRelationalDB
}

func (d DatabaseDetails) GetDetailDescription() string { //nolint:gocritic // hugeParam: value receiver required by ServiceDetails interface; providers/azure assigns DatabaseDetails by value
	return d.Engine + "/" + d.AZConfig
}

// CacheDetails represents cache-specific details (ElastiCache, Azure Cache, Memorystore).
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

// SearchDetails represents search-specific details (OpenSearch, Azure Search).
type SearchDetails struct {
	InstanceType    string `json:"instance_type"`
	MasterNodeType  string `json:"master_node_type,omitempty"`
	MasterNodeCount int    `json:"master_node_count,omitempty"`
}

func (d SearchDetails) GetServiceType() ServiceType {
	return ServiceSearch
}

func (d SearchDetails) GetDetailDescription() string {
	return d.InstanceType
}

// DataWarehouseDetails represents data warehouse-specific details (Redshift, Synapse, BigQuery).
type DataWarehouseDetails struct {
	NodeType      string `json:"node_type"`
	ClusterType   string `json:"cluster_type,omitempty"`
	NumberOfNodes int    `json:"number_of_nodes"`
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
// (e.g. SKU string lacks a throughput tier or API-type hint) -- do NOT
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

// SavingsPlanDetails represents AWS Savings Plans specific details.
type SavingsPlanDetails struct {
	PlanType         string  `json:"plan_type"`
	Coverage         string  `json:"coverage,omitempty"`
	HourlyCommitment float64 `json:"hourly_commitment"`
}

func (d SavingsPlanDetails) GetServiceType() ServiceType {
	return ServiceSavingsPlans
}

func (d SavingsPlanDetails) GetDetailDescription() string {
	return d.PlanType
}

// RIUtilization holds utilization data for a single Reserved Instance over a
// lookback period. The struct is defined here (rather than in a provider
// package) so both AWS and Azure can return the same type and callers can
// treat either provider's results uniformly without a cross-provider import.
//
// AWS field mapping (Cost Explorer GetReservationUtilization):
//
//	ReservedInstanceID  <- dimension key (SUBSCRIPTION_ID group value)
//	PurchasedHours      <- UtilizationsByTime[].Groups[].Utilization.PurchasedHours
//	TotalActualHours    <- UtilizationsByTime[].Groups[].Utilization.TotalActualHours
//	UnusedHours         <- UtilizationsByTime[].Groups[].Utilization.UnusedHours
//	UtilizationPercent  <- derived: (TotalActualHours / PurchasedHours) * 100
//
// Azure field mapping (Consumption ReservationsSummaries, monthly grain):
//
//	ReservedInstanceID  <- Properties.ReservationID
//	PurchasedHours      <- Properties.ReservedHours  (sum across periods)
//	TotalActualHours    <- Properties.UsedHours       (sum across periods)
//	UnusedHours         <- PurchasedHours - TotalActualHours
//	UtilizationPercent  <- derived: (TotalActualHours / PurchasedHours) * 100
//	SKUName             <- Properties.SKUName (Azure-only; empty string for AWS)
type RIUtilization struct {
	ReservedInstanceID string  `json:"reserved_instance_id"`
	SKUName            string  `json:"sku_name,omitempty"`
	UtilizationPercent float64 `json:"utilization_percent"`
	PurchasedHours     float64 `json:"purchased_hours"`
	TotalActualHours   float64 `json:"total_actual_hours"`
	UnusedHours        float64 `json:"unused_hours"`
}

// AuditRecord is one line in the JSON-lines audit log.
// Status values: "success", "error", "skipped" (dry-run), "skipped_covered" (idempotency).
type AuditRecord struct {
	Timestamp         time.Time       `json:"timestamp"`
	CommitmentID      string          `json:"commitment_id"`
	ErrorMessage      string          `json:"error_message"`
	AccountName       string          `json:"account_name"`
	Region            string          `json:"region"`
	Service           string          `json:"service"`
	ResourceType      string          `json:"resource_type"`
	CommitmentType    CommitmentType  `json:"commitment_type"`
	Source            string          `json:"source,omitempty"`
	AccountID         string          `json:"account_id"`
	Provider          ProviderType    `json:"provider"`
	Status            string          `json:"status"`
	RunID             string          `json:"run_id"`
	RawRecommendation json.RawMessage `json:"raw_recommendation,omitempty"`
	EstimatedCost     float64         `json:"estimated_cost"`
	Count             int             `json:"count"`
	EstimatedSavings  float64         `json:"estimated_savings"`
	Term              int             `json:"term_months"`
	DryRun            bool            `json:"dry_run"`
}
