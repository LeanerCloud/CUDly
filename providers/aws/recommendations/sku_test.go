package recommendations

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"

	awsec2 "github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	cetypes "github.com/aws/aws-sdk-go-v2/service/costexplorer/types"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/LeanerCloud/CUDly/pkg/common"
)

// stubInstanceTypePager is an in-memory pager that returns a fixed list of
// InstanceTypeInfo pages. It counts how many times NextPage is called so
// tests can assert the one-call-per-cache-lifetime invariant.
type stubInstanceTypePager struct {
	pages     [][]ec2types.InstanceTypeInfo
	pageIndex int
	callCount int32 // atomic
}

func (p *stubInstanceTypePager) HasMorePages() bool {
	return p.pageIndex < len(p.pages)
}

func (p *stubInstanceTypePager) NextPage(_ context.Context, _ ...func(*awsec2.Options)) (*awsec2.DescribeInstanceTypesOutput, error) {
	atomic.AddInt32(&p.callCount, 1)
	if p.pageIndex >= len(p.pages) {
		return nil, fmt.Errorf("no more pages")
	}
	page := p.pages[p.pageIndex]
	p.pageIndex++
	return &awsec2.DescribeInstanceTypesOutput{InstanceTypes: page}, nil
}

// newStubPager builds a single-page stub from the given entries.
func newStubPager(entries ...ec2types.InstanceTypeInfo) *stubInstanceTypePager {
	return &stubInstanceTypePager{pages: [][]ec2types.InstanceTypeInfo{entries}}
}

// knownInstanceTypes returns a slice with two well-known instance types for
// use in tests that need a populated catalogue.
func knownInstanceTypes() []ec2types.InstanceTypeInfo {
	return []ec2types.InstanceTypeInfo{
		{
			InstanceType: ec2types.InstanceTypeM5Large,
			VCpuInfo:     &ec2types.VCpuInfo{DefaultVCpus: aws.Int32(2)},
			MemoryInfo:   &ec2types.MemoryInfo{SizeInMiB: aws.Int64(8192)},
		},
		{
			InstanceType: ec2types.InstanceTypeR5Xlarge,
			VCpuInfo:     &ec2types.VCpuInfo{DefaultVCpus: aws.Int32(4)},
			MemoryInfo:   &ec2types.MemoryInfo{SizeInMiB: aws.Int64(32768)},
		},
	}
}

// TestExtractInstanceTypeSKUEntry verifies field extraction from InstanceTypeInfo.
func TestExtractInstanceTypeSKUEntry(t *testing.T) {
	tests := []struct {
		name      string
		info      ec2types.InstanceTypeInfo
		wantVCPU  int
		wantMemGB float64
	}{
		{
			name: "m5.large -- 2 vCPU / 8 GB",
			info: ec2types.InstanceTypeInfo{
				VCpuInfo:   &ec2types.VCpuInfo{DefaultVCpus: aws.Int32(2)},
				MemoryInfo: &ec2types.MemoryInfo{SizeInMiB: aws.Int64(8192)},
			},
			wantVCPU:  2,
			wantMemGB: 8.0,
		},
		{
			name: "r5.xlarge -- 4 vCPU / 32 GB",
			info: ec2types.InstanceTypeInfo{
				VCpuInfo:   &ec2types.VCpuInfo{DefaultVCpus: aws.Int32(4)},
				MemoryInfo: &ec2types.MemoryInfo{SizeInMiB: aws.Int64(32768)},
			},
			wantVCPU:  4,
			wantMemGB: 32.0,
		},
		{
			name:      "nil VCpuInfo and MemoryInfo -- both zero",
			info:      ec2types.InstanceTypeInfo{},
			wantVCPU:  0,
			wantMemGB: 0.0,
		},
		{
			name: "VCpuInfo present but DefaultVCpus nil -- VCPU zero",
			info: ec2types.InstanceTypeInfo{
				VCpuInfo:   &ec2types.VCpuInfo{},
				MemoryInfo: &ec2types.MemoryInfo{SizeInMiB: aws.Int64(4096)},
			},
			wantVCPU:  0,
			wantMemGB: 4.0,
		},
		{
			name: "odd MiB value -- fractional GB",
			info: ec2types.InstanceTypeInfo{
				VCpuInfo:   &ec2types.VCpuInfo{DefaultVCpus: aws.Int32(1)},
				MemoryInfo: &ec2types.MemoryInfo{SizeInMiB: aws.Int64(512)},
			},
			wantVCPU:  1,
			wantMemGB: 0.5,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			entry := extractInstanceTypeSKUEntry(tt.info)
			assert.Equal(t, tt.wantVCPU, entry.vCPUs)
			assert.InDelta(t, tt.wantMemGB, entry.memoryGB, 0.001)
		})
	}
}

// TestFetchInstanceTypeCatalogue_PopulatesMap verifies the paginator walk
// produces a correctly-keyed map.
func TestFetchInstanceTypeCatalogue_PopulatesMap(t *testing.T) {
	pager := newStubPager(knownInstanceTypes()...)
	m := fetchInstanceTypeCatalogue(context.Background(), pager)

	require.NotNil(t, m)
	assert.Len(t, m, 2)

	m5, ok := m["m5.large"]
	require.True(t, ok, "m5.large must be in catalogue")
	assert.Equal(t, 2, m5.vCPUs)
	assert.InDelta(t, 8.0, m5.memoryGB, 0.001)

	r5, ok := m["r5.xlarge"]
	require.True(t, ok, "r5.xlarge must be in catalogue")
	assert.Equal(t, 4, r5.vCPUs)
	assert.InDelta(t, 32.0, r5.memoryGB, 0.001)
}

// TestFetchInstanceTypeCatalogue_PageError returns nil on page fetch failure.
func TestFetchInstanceTypeCatalogue_PageError(t *testing.T) {
	errPager := &errorOnFirstPagePager{}
	m := fetchInstanceTypeCatalogue(context.Background(), errPager)
	assert.Nil(t, m, "catalogue must be nil on page fetch error")
}

// TestFetchInstanceTypeCatalogue_ContextCanceled returns nil when ctx is canceled.
func TestFetchInstanceTypeCatalogue_ContextCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already canceled

	pager := newStubPager(knownInstanceTypes()...)
	m := fetchInstanceTypeCatalogue(ctx, pager)
	assert.Nil(t, m, "catalogue must be nil when ctx is already canceled")
}

// TestInstanceTypeLookup_CachedOnce asserts that a single GetRecommendations
// run issues at most one DescribeInstanceTypes fan-out regardless of how many
// EC2 recs are returned (the N+1 invariant, issue #218 acceptance criterion).
func TestInstanceTypeLookup_CachedOnce(t *testing.T) {
	pager := newStubPager(knownInstanceTypes()...)
	var pagerCreations int32

	client := NewClientWithAPI(&mockCostExplorerAPI{}, "us-east-1")
	client.SetInstanceTypePagerFactory(func() InstanceTypePager {
		atomic.AddInt32(&pagerCreations, 1)
		return pager
	})

	// Call instanceTypeLookup twice for the same and different instance types.
	ctx := context.Background()
	_, _ = client.instanceTypeLookup(ctx, "m5.large")
	_, _ = client.instanceTypeLookup(ctx, "r5.xlarge")
	_, _ = client.instanceTypeLookup(ctx, "m5.large")

	assert.Equal(t, int32(1), atomic.LoadInt32(&pagerCreations),
		"pager factory must be called exactly once per client lifetime")
}

// TestParseEC2Details_VCPUAndMemoryPopulated asserts that parseEC2Details
// enriches ComputeDetails.VCPU and MemoryGB from the catalogue.
func TestParseEC2Details_VCPUAndMemoryPopulated(t *testing.T) {
	pager := newStubPager(knownInstanceTypes()...)
	client := NewClientWithAPI(&mockCostExplorerAPI{}, "us-east-1")
	client.SetInstanceTypePagerFactory(func() InstanceTypePager { return pager })

	details := &cetypes.ReservationPurchaseRecommendationDetail{
		InstanceDetails: &cetypes.InstanceDetails{
			EC2InstanceDetails: &cetypes.EC2InstanceDetails{
				InstanceType: aws.String("m5.large"),
				Platform:     aws.String("Linux/UNIX"),
				Region:       aws.String("us-east-1"),
				Tenancy:      aws.String("shared"),
			},
		},
	}

	rec := &common.Recommendation{}
	err := client.parseEC2Details(context.Background(), rec, details)
	require.NoError(t, err)

	cd, ok := rec.Details.(*common.ComputeDetails)
	require.True(t, ok)
	assert.Equal(t, 2, cd.VCPU)
	assert.InDelta(t, 8.0, cd.MemoryGB, 0.001)
}

// TestParseEC2Details_CatalogueMiss leaves VCPU/MemoryGB at zero gracefully.
func TestParseEC2Details_CatalogueMiss(t *testing.T) {
	pager := newStubPager(knownInstanceTypes()...) // does not contain c5.large
	client := NewClientWithAPI(&mockCostExplorerAPI{}, "us-east-1")
	client.SetInstanceTypePagerFactory(func() InstanceTypePager { return pager })

	details := &cetypes.ReservationPurchaseRecommendationDetail{
		InstanceDetails: &cetypes.InstanceDetails{
			EC2InstanceDetails: &cetypes.EC2InstanceDetails{
				InstanceType: aws.String("c5.large"),
				Platform:     aws.String("Linux/UNIX"),
				Region:       aws.String("us-east-1"),
			},
		},
	}

	rec := &common.Recommendation{}
	err := client.parseEC2Details(context.Background(), rec, details)
	require.NoError(t, err, "catalogue miss must not fail the conversion")

	cd, ok := rec.Details.(*common.ComputeDetails)
	require.True(t, ok)
	assert.Equal(t, 0, cd.VCPU, "VCPU must be 0 on catalogue miss")
	assert.InDelta(t, 0.0, cd.MemoryGB, 0.001, "MemoryGB must be 0 on catalogue miss")
}

// TestParseEC2Details_NoCatalogueConfigured leaves VCPU/MemoryGB at zero
// gracefully when no pager factory is set (legacy test path).
func TestParseEC2Details_NoCatalogueConfigured(t *testing.T) {
	client := &Client{} // no factory

	details := &cetypes.ReservationPurchaseRecommendationDetail{
		InstanceDetails: &cetypes.InstanceDetails{
			EC2InstanceDetails: &cetypes.EC2InstanceDetails{
				InstanceType: aws.String("m5.large"),
				Platform:     aws.String("Linux/UNIX"),
				Region:       aws.String("us-east-1"),
			},
		},
	}

	rec := &common.Recommendation{}
	err := client.parseEC2Details(context.Background(), rec, details)
	require.NoError(t, err)

	cd, ok := rec.Details.(*common.ComputeDetails)
	require.True(t, ok)
	assert.Equal(t, 0, cd.VCPU)
	assert.InDelta(t, 0.0, cd.MemoryGB, 0.001)
}

// errorOnFirstPagePager is a pager stub that returns an error on the first NextPage call.
type errorOnFirstPagePager struct {
	called bool
}

func (p *errorOnFirstPagePager) HasMorePages() bool {
	return !p.called
}

func (p *errorOnFirstPagePager) NextPage(_ context.Context, _ ...func(*awsec2.Options)) (*awsec2.DescribeInstanceTypesOutput, error) {
	p.called = true
	return nil, fmt.Errorf("simulated AWS API error")
}
