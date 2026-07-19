// Package common — JSON codec helpers for the polymorphic ServiceDetails
// interface so per-rec details survive the round-trip through
// purchase_executions.recommendations (JSONB) without dropping the
// service-specific fields that drive offering lookup.
//
// Background: before issue #453 the only Details data preserved across the
// dashboard → DB → executePurchase boundary was a single "engine" string on
// RecommendationRecord. EC2 platform / tenancy / scope, RDS AZ-config, SP
// plan-type / hourly-commitment all vanished, so the cloud service client's
// findOfferingID either fell back to "Linux/UNIX"-style defaults (silently
// mis-purchasing Windows recs as Linux) or returned "invalid service
// details for <Service>" outright. We now persist the full ServiceDetails
// payload as a raw JSON blob on the record, and reconstruct the matching
// typed pointer here at execute time using the rec's Service string as the
// discriminator.
//
// All helpers live in pkg/common so the typed dispatch sits next to the
// type definitions (ComputeDetails / DatabaseDetails / …). internal/config
// (where RecommendationRecord lives) is deliberately kept free of
// pkg/common imports so the dependency graph stays a strict DAG.
package common

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// jsonNullBytes is the JSON encoding of nil. Cached so the null-payload
// check below doesn't allocate on every call (this is the read-back step
// of every purchase, so it's worth avoiding the string conversion that
// would otherwise happen on the same path).
var jsonNullBytes = []byte("null")

// MarshalServiceDetails encodes a ServiceDetails value into a raw JSON
// payload suitable for the RecommendationRecord.Details JSONB column.
// Returns (nil, nil) when details is nil so the column stays NULL / absent
// rather than persisting a literal JSON "null".
func MarshalServiceDetails(details ServiceDetails) (json.RawMessage, error) {
	if details == nil {
		return nil, nil
	}
	b, err := json.Marshal(details)
	if err != nil {
		return nil, fmt.Errorf("marshal service details: %w", err)
	}
	// json.Marshal(nilPointer) returns "null" — fold that to a nil
	// RawMessage so downstream callers can treat absent and explicitly-
	// null identically (DecodeServiceDetailsFor below ignores both).
	if bytes.Equal(b, jsonNullBytes) {
		return nil, nil
	}
	return b, nil
}

// DecodeServiceDetailsFor reconstructs a typed *Details pointer from a raw
// JSON payload, dispatched by the service string from RecommendationRecord.
// Service accepts the same strings persisted by the scheduler's converter
// (e.g. "ec2", "rds", "elasticache", "opensearch", "redshift", "memorydb",
// "savingsplans", "savings-plans-compute" and the dash-free spellings —
// see internal/purchase/execution.go: mapServiceType / mapSavingsPlansSlug).
//
// Behavior:
//   - raw is empty AND service maps to a known *Details type → returns a
//     zero-valued typed pointer (the legacy / pre-#453 fallback). The
//     downstream service client's buildOfferingFilters tolerates zero-
//     valued Platform / Tenancy / Scope etc. by substituting defaults.
//   - raw is empty AND service maps to no *Details type (services we
//     don't know about, plus AWS services whose clients don't currently
//     type-assert Details and accept nil) → returns (nil, nil).
//   - raw is non-empty → unmarshals into the typed pointer for the
//     service. Unmarshal errors surface as errors (don't fall back
//     silently — that's how #453's mis-purchases hid for a release).
//   - service is unknown → returns (nil, nil) so callers fall through to
//     their existing nil-details paths rather than refusing a purchase
//     on a service we never had a *Details type for in the first place.
func DecodeServiceDetailsFor(service string, raw json.RawMessage) (ServiceDetails, error) {
	target, ok := newDetailsForService(service)
	if !ok {
		// Service has no *Details type — nothing to decode. Empty raw
		// payload is fine; a non-empty payload on a service we don't
		// recognize is suspicious but tolerated (writers and readers
		// might be on different versions during a rolling deploy).
		return nil, nil
	}
	if len(raw) == 0 || bytes.Equal(raw, jsonNullBytes) {
		// Legacy row or genuinely absent payload — hand back a zero-
		// valued typed pointer so the service client's type-assertion
		// succeeds. buildOfferingFilters tolerates zero fields and
		// substitutes Platform=Linux/UNIX, Tenancy=default, Scope=Region
		// etc. This is documented as a known-degraded path; new rows
		// always carry full details. See issue #453 § "graceful
		// degradation for legacy rows".
		return target, nil
	}
	if err := json.Unmarshal(raw, target); err != nil {
		return nil, fmt.Errorf("unmarshal %s service details: %w", service, err)
	}
	return target, nil
}

// newDetailsForService returns a fresh zero-valued *Details pointer for a
// given service slug, or (nil, false) when no *Details type applies. The
// boolean lets callers distinguish "service we don't have a typed Details
// shape for" (OpenSearch / Redshift / MemoryDB use case today) from
// "unknown service" — both currently get nil but the semantic intent
// differs and a future refactor that adds a Details type for one of them
// will only need to flip the map here.
func newDetailsForService(service string) (ServiceDetails, bool) {
	switch service {
	// AWS — EC2 / RDS / ElastiCache: the three services whose
	// findOfferingID type-asserts a typed *Details pointer today. These
	// are the must-have entries — without them, every purchase fails
	// with "invalid service details" (issue #453).
	case string(ServiceEC2), string(ServiceCompute):
		return &ComputeDetails{}, true
	case string(ServiceRDS), string(ServiceRelationalDB):
		return &DatabaseDetails{}, true
	case string(ServiceElastiCache), string(ServiceCache):
		return &CacheDetails{}, true

	// AWS Savings Plans -- all umbrella + per-plan-type slugs use
	// SavingsPlanDetails. The dash-free spellings are the canonical
	// values of ServiceSavingsPlansAll / ServiceSavingsPlansCompute etc.;
	// "savings-plans" (with a dash) is the legacy umbrella alias that
	// internal/purchase/execution.go: mapSavingsPlansSlug still
	// recognizes so purchase_executions JSONB rows persisted before
	// the rename in PR #94 (merged 2026-04-30) still resolve. Recognizing it
	// here means a legacy direct-execute approval still decodes against the
	// right type.
	// TODO(#95): drop "savings-plans" here once the ~6-month retention
	// window has passed (earliest 2026-10-30). See execution.go
	// mapSavingsPlansSlug for the full removal checklist.
	case string(ServiceSavingsPlansAll),
		string(ServiceSavingsPlansCompute),
		string(ServiceSavingsPlansEC2Instance),
		string(ServiceSavingsPlansSageMaker),
		string(ServiceSavingsPlansDatabase),
		"savings-plans":
		return &SavingsPlanDetails{}, true

	// Services that don't currently type-assert Details in
	// findOfferingID (OpenSearch / Redshift / MemoryDB read rec.ResourceType
	// directly). We still recognize OpenSearch and Redshift here so a
	// forthcoming refactor that starts asserting can flip the table
	// value without changing call sites. MemoryDB has no dedicated
	// *Details type yet, so it's intentionally absent — callers see
	// (nil, false) and pass a nil Details, which the MemoryDB client
	// tolerates.
	case string(ServiceOpenSearch), string(ServiceSearch):
		return &SearchDetails{}, true
	case string(ServiceRedshift), string(ServiceDataWarehouse):
		return &DataWarehouseDetails{}, true
	}
	return nil, false
}
