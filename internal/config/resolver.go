package config

// ResolveServiceConfig merges a sparse per-account override on top of a global
// ServiceConfig. Any non-nil pointer field in override replaces the global value;
// nil fields inherit from global. Slice fields (include/exclude lists) are
// replaced wholesale when non-empty in the override.
//
// If override is nil the global is returned unchanged (no copy is made).
func ResolveServiceConfig(global *ServiceConfig, override *AccountServiceOverride) *ServiceConfig {
	if override == nil {
		return global
	}

	resolved := &ServiceConfig{
		Provider:       global.Provider,
		Service:        global.Service,
		Enabled:        global.Enabled,
		Term:           global.Term,
		Payment:        global.Payment,
		Coverage:       global.Coverage,
		RampSchedule:   global.RampSchedule,
		IncludeEngines: global.IncludeEngines,
		ExcludeEngines: global.ExcludeEngines,
		IncludeRegions: global.IncludeRegions,
		ExcludeRegions: global.ExcludeRegions,
		IncludeTypes:   global.IncludeTypes,
		ExcludeTypes:   global.ExcludeTypes,
	}

	applyScalarOverrides(resolved, override)
	applySliceOverrides(resolved, override)
	return resolved
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
