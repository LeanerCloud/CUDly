package common

// Matches returns true if a Commitment covers the same resource type as a Recommendation.
// Match criteria: Provider, Region, Service, ResourceType, and normalized engine.
// OfferingClass and Term are not compared because Commitment has no such fields.
// State filtering (e.g. excluding "retired") is the caller's responsibility.
func Matches(rec Recommendation, c Commitment) bool {
	if rec.Provider != c.Provider {
		return false
	}
	if rec.Region != c.Region {
		return false
	}
	if rec.Service != c.Service {
		return false
	}
	if rec.ResourceType != c.ResourceType {
		return false
	}
	return NormalizeEngineName(EngineFromDetails(rec.Details)) == NormalizeEngineName(c.Engine)
}
