package common

// Duration constants for AWS Reserved Instances and similar commitments
// These durations are specified in seconds as per AWS API specifications
const (
	// OneYearDurationSeconds represents 1 year in seconds (365 days)
	OneYearDurationSeconds = 31536000

	// ThreeYearsDurationSeconds represents 3 years in seconds (1095 days)
	ThreeYearsDurationSeconds = 94608000

	// OneYearMonths represents 1 year commitment term in months
	OneYearMonths = 12

	// ThreeYearsMonths represents 3 year commitment term in months
	ThreeYearsMonths = 36
)

// GetTermMonthsFromDuration converts duration in seconds to term in months
func GetTermMonthsFromDuration(durationSeconds int32) int {
	switch durationSeconds {
	case ThreeYearsDurationSeconds:
		return ThreeYearsMonths
	case OneYearDurationSeconds:
		return OneYearMonths
	default:
		return OneYearMonths // Default to 1 year
	}
}

// GetDurationSecondsFromTermMonths converts term in months to duration in seconds
func GetDurationSecondsFromTermMonths(termMonths int) int32 {
	if termMonths >= ThreeYearsMonths {
		return ThreeYearsDurationSeconds
	}
	return OneYearDurationSeconds
}
