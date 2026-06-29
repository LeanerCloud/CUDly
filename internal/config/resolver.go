package config

// ResolveServiceConfig merges a sparse per-account override on top of a global
// ServiceConfig. Any non-nil pointer field in override replaces the global value;
// nil fields inherit from global. Slice fields (include/exclude lists) are
// replaced wholesale when non-empty in the override.
//
// If override is nil the global is returned unchanged (no copy is made).
// If global is nil but override is non-nil, a baseline ServiceConfig is synthesized
// with safe defaults (Enabled: true, Provider/Service from the override context via
// the provider and service parameters) and the override is merged into it. This
// lets a per-account override take effect even when no global ServiceConfig row
// exists for that (provider, service) pair.
// If both are nil, nil is returned.
// When merging, slice fields from the global are copied to avoid callers mutating
// the global's underlying arrays through the resolved config.
func ResolveServiceConfig(provider, service string, global *ServiceConfig, override *AccountServiceOverride) *ServiceConfig {
	if override == nil {
		return global // may be nil; callers that check for nil entry handle this correctly
	}

	baseline := global
	if baseline == nil {
		// No global row — synthesize a safe default so the override can be applied.
		// Enabled defaults to true (consistent with "service is on unless told otherwise").
		baseline = &ServiceConfig{
			Provider: provider,
			Service:  service,
			Enabled:  true,
		}
	}

	resolved := &ServiceConfig{
		Provider:       baseline.Provider,
		Service:        baseline.Service,
		Enabled:        baseline.Enabled,
		Term:           baseline.Term,
		Payment:        baseline.Payment,
		Coverage:       baseline.Coverage,
		RampSchedule:   baseline.RampSchedule,
		IncludeEngines: copyStrSlice(baseline.IncludeEngines),
		ExcludeEngines: copyStrSlice(baseline.ExcludeEngines),
		IncludeRegions: copyStrSlice(baseline.IncludeRegions),
		ExcludeRegions: copyStrSlice(baseline.ExcludeRegions),
		IncludeTypes:   copyStrSlice(baseline.IncludeTypes),
		ExcludeTypes:   copyStrSlice(baseline.ExcludeTypes),
		MinCount:       baseline.MinCount,
	}

	applyScalarOverrides(resolved, override)
	applySliceOverrides(resolved, override)
	return resolved
}

// copyStrSlice returns a shallow copy of s, or nil if s is nil.
func copyStrSlice(s []string) []string {
	if s == nil {
		return nil
	}
	cp := make([]string, len(s))
	copy(cp, s)
	return cp
}

func applyScalarOverrides(r *ServiceConfig, o *AccountServiceOverride) {
	if o.Enabled != nil {
		r.Enabled = *o.Enabled
	}
	if o.Term != nil {
		r.Term = *o.Term
	}
	if o.Payment != nil {
		r.Payment = *o.Payment
	}
	if o.Coverage != nil {
		r.Coverage = *o.Coverage
	}
	if o.RampSchedule != nil {
		r.RampSchedule = *o.RampSchedule
	}
}

func applySliceOverrides(r *ServiceConfig, o *AccountServiceOverride) {
	if len(o.IncludeEngines) > 0 {
		r.IncludeEngines = o.IncludeEngines
	}
	if len(o.ExcludeEngines) > 0 {
		r.ExcludeEngines = o.ExcludeEngines
	}
	if len(o.IncludeRegions) > 0 {
		r.IncludeRegions = o.IncludeRegions
	}
	if len(o.ExcludeRegions) > 0 {
		r.ExcludeRegions = o.ExcludeRegions
	}
	if len(o.IncludeTypes) > 0 {
		r.IncludeTypes = o.IncludeTypes
	}
	if len(o.ExcludeTypes) > 0 {
		r.ExcludeTypes = o.ExcludeTypes
	}
}
