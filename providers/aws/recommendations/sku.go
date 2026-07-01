package recommendations

import (
	"context"
	"sync"

	awsec2 "github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"

	"github.com/LeanerCloud/CUDly/pkg/logging"
)

// DescribeInstanceTypesAPI is the subset of the EC2 client interface
// needed to build a DescribeInstanceTypes paginator. The production
// implementation is *ec2.Client; tests inject a stub.
type DescribeInstanceTypesAPI interface {
	DescribeInstanceTypes(ctx context.Context, params *awsec2.DescribeInstanceTypesInput, optFns ...func(*awsec2.Options)) (*awsec2.DescribeInstanceTypesOutput, error)
}

// InstanceTypePager defines the iteration contract for DescribeInstanceTypes pages.
// Production code uses ec2.NewDescribeInstanceTypesPaginator; tests inject a stub.
type InstanceTypePager interface {
	HasMorePages() bool
	NextPage(ctx context.Context, optFns ...func(*awsec2.Options)) (*awsec2.DescribeInstanceTypesOutput, error)
}

// instanceTypeSKUEntry caches the vCPU/memory shape for one instance type.
// Either field is 0 when the API returned no data for it;
// common.ComputeDetails treats 0 as "unknown" (omitempty JSON tags).
type instanceTypeSKUEntry struct {
	vCPUs    int
	memoryGB float64
}

// skuCatalog holds a lazily-built per-Client instance-type catalog.
// The catalog is fetched ONCE per client lifetime via sync.Once
// so a single recommendations refresh issues at most one
// DescribeInstanceTypes fan-out regardless of how many EC2 recs are returned.
type skuCatalog struct {
	m    map[string]instanceTypeSKUEntry
	once sync.Once
}

// lookup returns the catalog entry for instanceType, building the
// catalog on the first call. ok=false on cache miss or fetch failure;
// the caller falls back to VCPU=0 / MemoryGB=0 and does NOT fail the
// conversion (graceful-degradation contract from Azure PR #810).
func (s *skuCatalog) lookup(ctx context.Context, instanceType string, newPager func() InstanceTypePager) (instanceTypeSKUEntry, bool) {
	s.once.Do(func() {
		s.m = fetchInstanceTypeCatalogue(ctx, newPager())
	})
	if s.m == nil {
		return instanceTypeSKUEntry{}, false
	}
	entry, ok := s.m[instanceType]
	return entry, ok
}

// fetchInstanceTypeCatalogue walks the DescribeInstanceTypes paginator and
// reduces each page into an instanceType->instanceTypeSKUEntry map.
//
// Context cancellation / deadline exceeded is treated as a hard stop
// (per feedback_ctx_cancel_terminal.md): the first ctx.Err() returns nil so
// instanceTypeLookup falls back to the empty-field path; the error is logged at
// WARN so operators can detect it.
//
// Any page-fetch error also returns nil (partial results are discarded so
// callers never see a half-populated catalog).
func fetchInstanceTypeCatalogue(ctx context.Context, pager InstanceTypePager) map[string]instanceTypeSKUEntry {
	out := make(map[string]instanceTypeSKUEntry)
	for pager.HasMorePages() {
		if err := ctx.Err(); err != nil {
			logging.Warnf("aws ec2: instance type catalog fetch interrupted: %v -- Details.VCPU/MemoryGB left at 0", err)
			return nil
		}
		page, err := pager.NextPage(ctx)
		if err != nil {
			logging.Warnf("aws ec2: instance type catalog page fetch failed: %v -- Details.VCPU/MemoryGB left at 0", err)
			return nil
		}
		populateInstanceTypeSKUMap(out, page.InstanceTypes)
	}
	return out
}

// populateInstanceTypeSKUMap writes one instanceTypeSKUEntry per item in
// instanceTypes into out. First-write-wins on duplicate names.
func populateInstanceTypeSKUMap(out map[string]instanceTypeSKUEntry, instanceTypes []ec2types.InstanceTypeInfo) {
	for i := range instanceTypes {
		info := &instanceTypes[i]
		name := string(info.InstanceType)
		if name == "" {
			continue
		}
		if _, exists := out[name]; exists {
			continue
		}
		out[name] = extractInstanceTypeSKUEntry(info)
	}
}

// extractInstanceTypeSKUEntry reads the vCPU count and memory size from
// InstanceTypeInfo. Returns (0, 0) when either field is absent or nil;
// callers treat 0 as "unknown".
func extractInstanceTypeSKUEntry(info *ec2types.InstanceTypeInfo) instanceTypeSKUEntry {
	var vCPUs int
	var memoryGB float64

	if info.VCpuInfo != nil && info.VCpuInfo.DefaultVCpus != nil {
		vCPUs = int(*info.VCpuInfo.DefaultVCpus)
	}
	if info.MemoryInfo != nil && info.MemoryInfo.SizeInMiB != nil {
		memoryGB = float64(*info.MemoryInfo.SizeInMiB) / 1024.0
	}

	return instanceTypeSKUEntry{vCPUs: vCPUs, memoryGB: memoryGB}
}
