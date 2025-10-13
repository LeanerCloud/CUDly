package common

import (
	"fmt"
	"strings"
)

// ValidInstanceTypes contains valid instance type patterns for each service
var ValidInstanceTypes = map[ServiceType][]string{
	ServiceRDS: {
		// T-series (Burstable)
		"db.t2.micro", "db.t2.small", "db.t2.medium", "db.t2.large", "db.t2.xlarge", "db.t2.2xlarge",
		"db.t3.micro", "db.t3.small", "db.t3.medium", "db.t3.large", "db.t3.xlarge", "db.t3.2xlarge",
		"db.t4g.micro", "db.t4g.small", "db.t4g.medium", "db.t4g.large", "db.t4g.xlarge", "db.t4g.2xlarge",
		// M-series (General Purpose)
		"db.m4.large", "db.m4.xlarge", "db.m4.2xlarge", "db.m4.4xlarge", "db.m4.10xlarge", "db.m4.16xlarge",
		"db.m5.large", "db.m5.xlarge", "db.m5.2xlarge", "db.m5.4xlarge", "db.m5.8xlarge", "db.m5.12xlarge", "db.m5.16xlarge", "db.m5.24xlarge",
		"db.m5d.large", "db.m5d.xlarge", "db.m5d.2xlarge", "db.m5d.4xlarge", "db.m5d.8xlarge", "db.m5d.12xlarge", "db.m5d.16xlarge", "db.m5d.24xlarge",
		"db.m6i.large", "db.m6i.xlarge", "db.m6i.2xlarge", "db.m6i.4xlarge", "db.m6i.8xlarge", "db.m6i.12xlarge", "db.m6i.16xlarge", "db.m6i.24xlarge", "db.m6i.32xlarge",
		"db.m6g.large", "db.m6g.xlarge", "db.m6g.2xlarge", "db.m6g.4xlarge", "db.m6g.8xlarge", "db.m6g.12xlarge", "db.m6g.16xlarge",
		"db.m6gd.large", "db.m6gd.xlarge", "db.m6gd.2xlarge", "db.m6gd.4xlarge", "db.m6gd.8xlarge", "db.m6gd.12xlarge", "db.m6gd.16xlarge",
		"db.m7g.large", "db.m7g.xlarge", "db.m7g.2xlarge", "db.m7g.4xlarge", "db.m7g.8xlarge", "db.m7g.12xlarge", "db.m7g.16xlarge",
		// R-series (Memory Optimized)
		"db.r4.large", "db.r4.xlarge", "db.r4.2xlarge", "db.r4.4xlarge", "db.r4.8xlarge", "db.r4.16xlarge",
		"db.r5.large", "db.r5.xlarge", "db.r5.2xlarge", "db.r5.4xlarge", "db.r5.8xlarge", "db.r5.12xlarge", "db.r5.16xlarge", "db.r5.24xlarge",
		"db.r5b.large", "db.r5b.xlarge", "db.r5b.2xlarge", "db.r5b.4xlarge", "db.r5b.8xlarge", "db.r5b.12xlarge", "db.r5b.16xlarge", "db.r5b.24xlarge",
		"db.r5d.large", "db.r5d.xlarge", "db.r5d.2xlarge", "db.r5d.4xlarge", "db.r5d.8xlarge", "db.r5d.12xlarge", "db.r5d.16xlarge", "db.r5d.24xlarge",
		"db.r6i.large", "db.r6i.xlarge", "db.r6i.2xlarge", "db.r6i.4xlarge", "db.r6i.8xlarge", "db.r6i.12xlarge", "db.r6i.16xlarge", "db.r6i.24xlarge", "db.r6i.32xlarge",
		"db.r6g.large", "db.r6g.xlarge", "db.r6g.2xlarge", "db.r6g.4xlarge", "db.r6g.8xlarge", "db.r6g.12xlarge", "db.r6g.16xlarge",
		"db.r6gd.large", "db.r6gd.xlarge", "db.r6gd.2xlarge", "db.r6gd.4xlarge", "db.r6gd.8xlarge", "db.r6gd.12xlarge", "db.r6gd.16xlarge",
		"db.r7g.large", "db.r7g.xlarge", "db.r7g.2xlarge", "db.r7g.4xlarge", "db.r7g.8xlarge", "db.r7g.12xlarge", "db.r7g.16xlarge",
		// X-series (Memory Optimized - Extra Large)
		"db.x1.16xlarge", "db.x1.32xlarge",
		"db.x1e.xlarge", "db.x1e.2xlarge", "db.x1e.4xlarge", "db.x1e.8xlarge", "db.x1e.16xlarge", "db.x1e.32xlarge",
		"db.x2g.large", "db.x2g.xlarge", "db.x2g.2xlarge", "db.x2g.4xlarge", "db.x2g.8xlarge", "db.x2g.12xlarge", "db.x2g.16xlarge",
		"db.x2iedn.xlarge", "db.x2iedn.2xlarge", "db.x2iedn.4xlarge", "db.x2iedn.8xlarge", "db.x2iedn.16xlarge", "db.x2iedn.24xlarge", "db.x2iedn.32xlarge",
		// Z-series (High Frequency)
		"db.z1d.large", "db.z1d.xlarge", "db.z1d.2xlarge", "db.z1d.3xlarge", "db.z1d.6xlarge", "db.z1d.12xlarge",
	},
	ServiceElastiCache: {
		// T-series (Burstable)
		"cache.t2.micro", "cache.t2.small", "cache.t2.medium",
		"cache.t3.micro", "cache.t3.small", "cache.t3.medium",
		"cache.t4g.micro", "cache.t4g.small", "cache.t4g.medium",
		// M-series (General Purpose)
		"cache.m4.large", "cache.m4.xlarge", "cache.m4.2xlarge", "cache.m4.4xlarge", "cache.m4.10xlarge",
		"cache.m5.large", "cache.m5.xlarge", "cache.m5.2xlarge", "cache.m5.4xlarge", "cache.m5.12xlarge", "cache.m5.24xlarge",
		"cache.m6g.large", "cache.m6g.xlarge", "cache.m6g.2xlarge", "cache.m6g.4xlarge", "cache.m6g.8xlarge", "cache.m6g.12xlarge", "cache.m6g.16xlarge",
		"cache.m7g.large", "cache.m7g.xlarge", "cache.m7g.2xlarge", "cache.m7g.4xlarge", "cache.m7g.8xlarge", "cache.m7g.12xlarge", "cache.m7g.16xlarge",
		// R-series (Memory Optimized)
		"cache.r4.large", "cache.r4.xlarge", "cache.r4.2xlarge", "cache.r4.4xlarge", "cache.r4.8xlarge", "cache.r4.16xlarge",
		"cache.r5.large", "cache.r5.xlarge", "cache.r5.2xlarge", "cache.r5.4xlarge", "cache.r5.12xlarge", "cache.r5.24xlarge",
		"cache.r6g.large", "cache.r6g.xlarge", "cache.r6g.2xlarge", "cache.r6g.4xlarge", "cache.r6g.8xlarge", "cache.r6g.12xlarge", "cache.r6g.16xlarge",
		"cache.r6gd.xlarge", "cache.r6gd.2xlarge", "cache.r6gd.4xlarge", "cache.r6gd.8xlarge", "cache.r6gd.12xlarge", "cache.r6gd.16xlarge",
		"cache.r7g.large", "cache.r7g.xlarge", "cache.r7g.2xlarge", "cache.r7g.4xlarge", "cache.r7g.8xlarge", "cache.r7g.12xlarge", "cache.r7g.16xlarge",
	},
	ServiceEC2: {
		// T-series (Burstable)
		"t2.nano", "t2.micro", "t2.small", "t2.medium", "t2.large", "t2.xlarge", "t2.2xlarge",
		"t3.nano", "t3.micro", "t3.small", "t3.medium", "t3.large", "t3.xlarge", "t3.2xlarge",
		"t3a.nano", "t3a.micro", "t3a.small", "t3a.medium", "t3a.large", "t3a.xlarge", "t3a.2xlarge",
		"t4g.nano", "t4g.micro", "t4g.small", "t4g.medium", "t4g.large", "t4g.xlarge", "t4g.2xlarge",
		// M-series (General Purpose)
		"m4.large", "m4.xlarge", "m4.2xlarge", "m4.4xlarge", "m4.10xlarge", "m4.16xlarge",
		"m5.large", "m5.xlarge", "m5.2xlarge", "m5.4xlarge", "m5.8xlarge", "m5.12xlarge", "m5.16xlarge", "m5.24xlarge", "m5.metal",
		"m5a.large", "m5a.xlarge", "m5a.2xlarge", "m5a.4xlarge", "m5a.8xlarge", "m5a.12xlarge", "m5a.16xlarge", "m5a.24xlarge",
		"m5d.large", "m5d.xlarge", "m5d.2xlarge", "m5d.4xlarge", "m5d.8xlarge", "m5d.12xlarge", "m5d.16xlarge", "m5d.24xlarge", "m5d.metal",
		"m5n.large", "m5n.xlarge", "m5n.2xlarge", "m5n.4xlarge", "m5n.8xlarge", "m5n.12xlarge", "m5n.16xlarge", "m5n.24xlarge", "m5n.metal",
		"m6i.large", "m6i.xlarge", "m6i.2xlarge", "m6i.4xlarge", "m6i.8xlarge", "m6i.12xlarge", "m6i.16xlarge", "m6i.24xlarge", "m6i.32xlarge", "m6i.metal",
		"m6g.medium", "m6g.large", "m6g.xlarge", "m6g.2xlarge", "m6g.4xlarge", "m6g.8xlarge", "m6g.12xlarge", "m6g.16xlarge", "m6g.metal",
		"m7g.medium", "m7g.large", "m7g.xlarge", "m7g.2xlarge", "m7g.4xlarge", "m7g.8xlarge", "m7g.12xlarge", "m7g.16xlarge", "m7g.metal",
		// C-series (Compute Optimized)
		"c4.large", "c4.xlarge", "c4.2xlarge", "c4.4xlarge", "c4.8xlarge",
		"c5.large", "c5.xlarge", "c5.2xlarge", "c5.4xlarge", "c5.9xlarge", "c5.12xlarge", "c5.18xlarge", "c5.24xlarge", "c5.metal",
		"c5a.large", "c5a.xlarge", "c5a.2xlarge", "c5a.4xlarge", "c5a.8xlarge", "c5a.12xlarge", "c5a.16xlarge", "c5a.24xlarge",
		"c5d.large", "c5d.xlarge", "c5d.2xlarge", "c5d.4xlarge", "c5d.9xlarge", "c5d.12xlarge", "c5d.18xlarge", "c5d.24xlarge", "c5d.metal",
		"c5n.large", "c5n.xlarge", "c5n.2xlarge", "c5n.4xlarge", "c5n.9xlarge", "c5n.18xlarge", "c5n.metal",
		"c6i.large", "c6i.xlarge", "c6i.2xlarge", "c6i.4xlarge", "c6i.8xlarge", "c6i.12xlarge", "c6i.16xlarge", "c6i.24xlarge", "c6i.32xlarge", "c6i.metal",
		"c6g.medium", "c6g.large", "c6g.xlarge", "c6g.2xlarge", "c6g.4xlarge", "c6g.8xlarge", "c6g.12xlarge", "c6g.16xlarge", "c6g.metal",
		"c7g.medium", "c7g.large", "c7g.xlarge", "c7g.2xlarge", "c7g.4xlarge", "c7g.8xlarge", "c7g.12xlarge", "c7g.16xlarge", "c7g.metal",
		// R-series (Memory Optimized)
		"r4.large", "r4.xlarge", "r4.2xlarge", "r4.4xlarge", "r4.8xlarge", "r4.16xlarge",
		"r5.large", "r5.xlarge", "r5.2xlarge", "r5.4xlarge", "r5.8xlarge", "r5.12xlarge", "r5.16xlarge", "r5.24xlarge", "r5.metal",
		"r5a.large", "r5a.xlarge", "r5a.2xlarge", "r5a.4xlarge", "r5a.8xlarge", "r5a.12xlarge", "r5a.16xlarge", "r5a.24xlarge",
		"r5b.large", "r5b.xlarge", "r5b.2xlarge", "r5b.4xlarge", "r5b.8xlarge", "r5b.12xlarge", "r5b.16xlarge", "r5b.24xlarge", "r5b.metal",
		"r5d.large", "r5d.xlarge", "r5d.2xlarge", "r5d.4xlarge", "r5d.8xlarge", "r5d.12xlarge", "r5d.16xlarge", "r5d.24xlarge", "r5d.metal",
		"r5n.large", "r5n.xlarge", "r5n.2xlarge", "r5n.4xlarge", "r5n.8xlarge", "r5n.12xlarge", "r5n.16xlarge", "r5n.24xlarge", "r5n.metal",
		"r6i.large", "r6i.xlarge", "r6i.2xlarge", "r6i.4xlarge", "r6i.8xlarge", "r6i.12xlarge", "r6i.16xlarge", "r6i.24xlarge", "r6i.32xlarge", "r6i.metal",
		"r6g.medium", "r6g.large", "r6g.xlarge", "r6g.2xlarge", "r6g.4xlarge", "r6g.8xlarge", "r6g.12xlarge", "r6g.16xlarge", "r6g.metal",
		"r7g.medium", "r7g.large", "r7g.xlarge", "r7g.2xlarge", "r7g.4xlarge", "r7g.8xlarge", "r7g.12xlarge", "r7g.16xlarge", "r7g.metal",
		// X-series (Memory Optimized - Extra Large)
		"x1.16xlarge", "x1.32xlarge",
		"x1e.xlarge", "x1e.2xlarge", "x1e.4xlarge", "x1e.8xlarge", "x1e.16xlarge", "x1e.32xlarge",
		"x2iedn.xlarge", "x2iedn.2xlarge", "x2iedn.4xlarge", "x2iedn.8xlarge", "x2iedn.16xlarge", "x2iedn.24xlarge", "x2iedn.32xlarge", "x2iedn.metal",
		"x2gd.medium", "x2gd.large", "x2gd.xlarge", "x2gd.2xlarge", "x2gd.4xlarge", "x2gd.8xlarge", "x2gd.12xlarge", "x2gd.16xlarge", "x2gd.metal",
		// I-series (Storage Optimized)
		"i3.large", "i3.xlarge", "i3.2xlarge", "i3.4xlarge", "i3.8xlarge", "i3.16xlarge", "i3.metal",
		"i3en.large", "i3en.xlarge", "i3en.2xlarge", "i3en.3xlarge", "i3en.6xlarge", "i3en.12xlarge", "i3en.24xlarge", "i3en.metal",
		"i4i.large", "i4i.xlarge", "i4i.2xlarge", "i4i.4xlarge", "i4i.8xlarge", "i4i.16xlarge", "i4i.32xlarge", "i4i.metal",
		// D-series (Dense Storage)
		"d2.xlarge", "d2.2xlarge", "d2.4xlarge", "d2.8xlarge",
		"d3.xlarge", "d3.2xlarge", "d3.4xlarge", "d3.8xlarge",
		"d3en.xlarge", "d3en.2xlarge", "d3en.4xlarge", "d3en.6xlarge", "d3en.8xlarge", "d3en.12xlarge",
		// H-series (HDD Storage Optimized)
		"h1.2xlarge", "h1.4xlarge", "h1.8xlarge", "h1.16xlarge",
		// Z-series (High Frequency)
		"z1d.large", "z1d.xlarge", "z1d.2xlarge", "z1d.3xlarge", "z1d.6xlarge", "z1d.12xlarge", "z1d.metal",
		// P-series (GPU)
		"p2.xlarge", "p2.8xlarge", "p2.16xlarge",
		"p3.2xlarge", "p3.8xlarge", "p3.16xlarge",
		"p3dn.24xlarge",
		"p4d.24xlarge",
		// G-series (GPU)
		"g3.4xlarge", "g3.8xlarge", "g3.16xlarge",
		"g4dn.xlarge", "g4dn.2xlarge", "g4dn.4xlarge", "g4dn.8xlarge", "g4dn.12xlarge", "g4dn.16xlarge", "g4dn.metal",
		"g5.xlarge", "g5.2xlarge", "g5.4xlarge", "g5.8xlarge", "g5.12xlarge", "g5.16xlarge", "g5.24xlarge", "g5.48xlarge",
		// F-series (FPGA)
		"f1.2xlarge", "f1.4xlarge", "f1.16xlarge",
	},
	ServiceOpenSearch: {
		// T-series
		"t3.small.search", "t3.medium.search",
		// M-series
		"m5.large.search", "m5.xlarge.search", "m5.2xlarge.search", "m5.4xlarge.search", "m5.12xlarge.search",
		"m6g.large.search", "m6g.xlarge.search", "m6g.2xlarge.search", "m6g.4xlarge.search", "m6g.8xlarge.search", "m6g.12xlarge.search",
		// C-series
		"c5.large.search", "c5.xlarge.search", "c5.2xlarge.search", "c5.4xlarge.search", "c5.9xlarge.search", "c5.18xlarge.search",
		"c6g.large.search", "c6g.xlarge.search", "c6g.2xlarge.search", "c6g.4xlarge.search", "c6g.8xlarge.search", "c6g.12xlarge.search",
		// R-series
		"r5.large.search", "r5.xlarge.search", "r5.2xlarge.search", "r5.4xlarge.search", "r5.12xlarge.search",
		"r6g.large.search", "r6g.xlarge.search", "r6g.2xlarge.search", "r6g.4xlarge.search", "r6g.8xlarge.search", "r6g.12xlarge.search",
		"r6gd.large.search", "r6gd.xlarge.search", "r6gd.2xlarge.search", "r6gd.4xlarge.search", "r6gd.8xlarge.search", "r6gd.12xlarge.search", "r6gd.16xlarge.search",
		// I-series
		"i3.large.search", "i3.xlarge.search", "i3.2xlarge.search", "i3.4xlarge.search", "i3.8xlarge.search", "i3.16xlarge.search",
	},
	ServiceRedshift: {
		// DC2 (Dense Compute)
		"dc2.large", "dc2.8xlarge",
		// RA3 (Managed Storage)
		"ra3.xlplus", "ra3.4xlarge", "ra3.16xlarge",
		// DS2 (Dense Storage - older generation)
		"ds2.xlarge", "ds2.8xlarge",
	},
	ServiceMemoryDB: {
		// T-series
		"db.t4g.small", "db.t4g.medium",
		// M-series
		"db.m6g.large", "db.m6g.xlarge", "db.m6g.2xlarge", "db.m6g.4xlarge", "db.m6g.8xlarge", "db.m6g.12xlarge", "db.m6g.16xlarge",
		// R-series
		"db.r6g.large", "db.r6g.xlarge", "db.r6g.2xlarge", "db.r6g.4xlarge", "db.r6g.8xlarge", "db.r6g.12xlarge", "db.r6g.16xlarge",
		"db.r6gd.xlarge", "db.r6gd.2xlarge", "db.r6gd.4xlarge", "db.r6gd.8xlarge", "db.r6gd.12xlarge", "db.r6gd.16xlarge",
		"db.r7g.large", "db.r7g.xlarge", "db.r7g.2xlarge", "db.r7g.4xlarge", "db.r7g.8xlarge", "db.r7g.12xlarge", "db.r7g.16xlarge",
	},
}

// ValidateInstanceType checks if an instance type is valid for a given service
func ValidateInstanceType(instanceType string, service ServiceType) error {
	validTypes, ok := ValidInstanceTypes[service]
	if !ok {
		// If service not found, allow any instance type (forward compatibility)
		return nil
	}

	instanceType = strings.TrimSpace(instanceType)

	// Check for exact match
	for _, validType := range validTypes {
		if instanceType == validType {
			return nil
		}
	}

	return fmt.Errorf("invalid instance type '%s' for service %s", instanceType, service)
}

// ValidateInstanceTypes validates a list of instance types against all services
func ValidateInstanceTypes(instanceTypes []string) error {
	if len(instanceTypes) == 0 {
		return nil
	}

	// Collect all valid instance types across all services
	allValidTypes := make(map[string]bool)
	for _, types := range ValidInstanceTypes {
		for _, t := range types {
			allValidTypes[t] = true
		}
	}

	invalidTypes := make([]string, 0)
	for _, instanceType := range instanceTypes {
		instanceType = strings.TrimSpace(instanceType)
		if !allValidTypes[instanceType] {
			invalidTypes = append(invalidTypes, instanceType)
		}
	}

	if len(invalidTypes) > 0 {
		return fmt.Errorf("invalid instance type(s): %s. Use full instance type names like 'db.t3.small', 'cache.r5.large', 'm5.xlarge'", strings.Join(invalidTypes, ", "))
	}

	return nil
}

// GetInstanceTypesByService returns all valid instance types for a service
func GetInstanceTypesByService(service ServiceType) []string {
	if types, ok := ValidInstanceTypes[service]; ok {
		return types
	}
	return []string{}
}

// IsValidInstanceType checks if an instance type is valid for any service
func IsValidInstanceType(instanceType string) bool {
	instanceType = strings.TrimSpace(instanceType)
	for _, types := range ValidInstanceTypes {
		for _, validType := range types {
			if instanceType == validType {
				return true
			}
		}
	}
	return false
}

// GetInstanceTypePrefix returns the prefix of an instance type (e.g., "db.t3" from "db.t3.small")
func GetInstanceTypePrefix(instanceType string) string {
	parts := strings.Split(instanceType, ".")
	if len(parts) >= 2 {
		return strings.Join(parts[:2], ".")
	}
	return instanceType
}

// GetInstanceTypeFamily returns the family of an instance type (e.g., "t3" from "db.t3.small")
func GetInstanceTypeFamily(instanceType string) string {
	parts := strings.Split(instanceType, ".")
	if len(parts) >= 2 {
		// For RDS/ElastiCache: db.t3.small -> t3
		// For EC2: t3.small -> t3
		if parts[0] == "db" || parts[0] == "cache" {
			if len(parts) >= 3 {
				return parts[1]
			}
		} else {
			return parts[0]
		}
	}
	return ""
}
