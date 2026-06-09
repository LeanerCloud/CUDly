package config

import (
	"sort"
	"strings"
)

// DerivePlanProviders extracts the distinct set of providers a plan targets
// by parsing the keys of plan.Services. Keys are expected to use the
// "provider/service" format produced by buildServiceConfig.
func DerivePlanProviders(plan *PurchasePlan) []string {
	if plan == nil {
		return nil
	}
	seen := make(map[string]struct{}, len(plan.Services))
	for k := range plan.Services {
		provider, _, ok := strings.Cut(k, "/")
		if !ok || provider == "" {
			continue
		}
		seen[provider] = struct{}{}
	}
	providers := make([]string, 0, len(seen))
	for provider := range seen {
		providers = append(providers, provider)
	}
	sort.Strings(providers)
	return providers
}
