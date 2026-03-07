package exchange

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestAnalyzeReshaping(t *testing.T) {
	tests := []struct {
		name        string
		ris         []RIInfo
		utilization []UtilizationInfo
		threshold   float64
		wantCount   int
		checkFirst  func(t *testing.T, rec ReshapeRecommendation)
	}{
		{
			name: "standard RI skipped (not convertible)",
			ris: []RIInfo{
				{ID: "ri-1", InstanceType: "m5.xlarge", InstanceCount: 1, OfferingClass: "standard"},
			},
			utilization: []UtilizationInfo{
				{RIID: "ri-1", UtilizationPercent: 50},
			},
			threshold: 95,
			wantCount: 0,
		},
		{
			name: "RI at 50% utilization suggests halved size",
			ris: []RIInfo{
				{ID: "ri-2", InstanceType: "m5.xlarge", InstanceCount: 1, OfferingClass: "convertible"},
			},
			utilization: []UtilizationInfo{
				{RIID: "ri-2", UtilizationPercent: 50},
			},
			threshold: 95,
			wantCount: 1,
			checkFirst: func(t *testing.T, rec ReshapeRecommendation) {
				// xlarge = 8 normalized units, 50% used = 4 units
				// large = 4 normalized units, so 1x large
				assert.Equal(t, "m5.large", rec.TargetInstanceType)
				assert.Equal(t, int32(1), rec.TargetCount)
				assert.InDelta(t, 4.0, rec.NormalizedUsed, 0.01)
			},
		},
		{
			name: "RI at 96% is above 95% threshold — no recommendation",
			ris: []RIInfo{
				{ID: "ri-3", InstanceType: "m5.xlarge", InstanceCount: 1, OfferingClass: "convertible"},
			},
			utilization: []UtilizationInfo{
				{RIID: "ri-3", UtilizationPercent: 96},
			},
			threshold: 95,
			wantCount: 0,
		},
		{
			name: "RI at 90% suggests slight downsize",
			ris: []RIInfo{
				{ID: "ri-4", InstanceType: "m5.2xlarge", InstanceCount: 1, OfferingClass: "convertible"},
			},
			utilization: []UtilizationInfo{
				{RIID: "ri-4", UtilizationPercent: 90},
			},
			threshold: 95,
			wantCount: 1,
			checkFirst: func(t *testing.T, rec ReshapeRecommendation) {
				// 2xlarge = 16, 90% = 14.4 normalized units
				// xlarge = 8, ceil(14.4/8) = 2 → 2x xlarge = 16 units
				// That's same total as current (1x 2xlarge = 16), but different shape
				assert.Equal(t, "m5.xlarge", rec.TargetInstanceType)
				assert.Equal(t, int32(2), rec.TargetCount)
			},
		},
		{
			name: "RI at 0% utilization — recommend expiry or reallocation",
			ris: []RIInfo{
				{ID: "ri-5", InstanceType: "m5.xlarge", InstanceCount: 1, OfferingClass: "convertible"},
			},
			utilization: []UtilizationInfo{
				{RIID: "ri-5", UtilizationPercent: 0},
			},
			threshold: 95,
			wantCount: 1,
			checkFirst: func(t *testing.T, rec ReshapeRecommendation) {
				assert.Equal(t, "", rec.TargetInstanceType)
				assert.Equal(t, int32(0), rec.TargetCount)
				assert.Contains(t, rec.Reason, "completely unused")
				assert.InDelta(t, 8.0, rec.NormalizedPurchased, 0.01) // xlarge = 8 units
			},
		},
		{
			name: "multiple counts: 4x m5.xlarge at 25%",
			ris: []RIInfo{
				{ID: "ri-6", InstanceType: "m5.xlarge", InstanceCount: 4, OfferingClass: "convertible"},
			},
			utilization: []UtilizationInfo{
				{RIID: "ri-6", UtilizationPercent: 25},
			},
			threshold: 95,
			wantCount: 1,
			checkFirst: func(t *testing.T, rec ReshapeRecommendation) {
				// 4x xlarge = 32 normalized, 25% = 8 units
				// xlarge = 8, so 1x xlarge
				assert.Equal(t, "m5.xlarge", rec.TargetInstanceType)
				assert.Equal(t, int32(1), rec.TargetCount)
			},
		},
		{
			name: "unknown instance family — skip gracefully",
			ris: []RIInfo{
				{ID: "ri-7", InstanceType: "unknown", InstanceCount: 1, OfferingClass: "convertible"},
			},
			utilization: []UtilizationInfo{
				{RIID: "ri-7", UtilizationPercent: 50},
			},
			threshold: 95,
			wantCount: 0,
		},
		{
			name: "no utilization data — skip",
			ris: []RIInfo{
				{ID: "ri-8", InstanceType: "m5.xlarge", InstanceCount: 1, OfferingClass: "convertible"},
			},
			utilization: []UtilizationInfo{},
			threshold:   95,
			wantCount:   0,
		},
		{
			name: "already smallest size with low utilization",
			ris: []RIInfo{
				{ID: "ri-9", InstanceType: "m5.nano", InstanceCount: 1, OfferingClass: "convertible"},
			},
			utilization: []UtilizationInfo{
				{RIID: "ri-9", UtilizationPercent: 50},
			},
			threshold: 95,
			wantCount: 0, // nano at 50% = 0.125 units → ceil(0.125/0.25)=1 nano → same config
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recs := AnalyzeReshaping(tt.ris, tt.utilization, tt.threshold)
			assert.Equal(t, tt.wantCount, len(recs), "unexpected recommendation count")
			if tt.wantCount > 0 && len(recs) > 0 && tt.checkFirst != nil {
				tt.checkFirst(t, recs[0])
			}
		})
	}
}

func TestNormalizationFactorForSize(t *testing.T) {
	// Verify key normalization factors via the public accessor
	assert.Equal(t, 0.25, NormalizationFactorForSize("nano"))
	assert.Equal(t, 0.5, NormalizationFactorForSize("micro"))
	assert.Equal(t, 1.0, NormalizationFactorForSize("small"))
	assert.Equal(t, 4.0, NormalizationFactorForSize("large"))
	assert.Equal(t, 8.0, NormalizationFactorForSize("xlarge"))
	assert.Equal(t, 192.0, NormalizationFactorForSize("24xlarge"))
	assert.Equal(t, 192.0, NormalizationFactorForSize("metal"))
	assert.Equal(t, 0.0, NormalizationFactorForSize("unknown"))
}

func TestParseInstanceType(t *testing.T) {
	tests := []struct {
		input      string
		wantFamily string
		wantSize   string
	}{
		{"m5.xlarge", "m5", "xlarge"},
		{"r6g.2xlarge", "r6g", "2xlarge"},
		{"c5.metal", "c5", "metal"},
		{"invalid", "", ""},
	}

	for _, tt := range tests {
		family, size := parseInstanceType(tt.input)
		assert.Equal(t, tt.wantFamily, family, "family for %s", tt.input)
		assert.Equal(t, tt.wantSize, size, "size for %s", tt.input)
	}
}
