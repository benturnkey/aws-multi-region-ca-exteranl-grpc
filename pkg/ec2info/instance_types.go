package ec2info

import (
	"context"
	"sync"

	"aws-multi-region-ca-exteranl-grpc/pkg/awsclient"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

// InstanceType holds the resource capacity of an EC2 instance type.
type InstanceType struct {
	InstanceType string
	VCPU         int64
	MemoryMb     int64
	GPU          int64
	Architecture string // "amd64" or "arm64"
}

// Resolver resolves instance type names to InstanceType data via DescribeInstanceTypes.
type Resolver struct {
	mu    sync.RWMutex
	cache map[string]*InstanceType
}

// NewResolver creates an empty Resolver.
func NewResolver() *Resolver {
	return &Resolver{
		cache: make(map[string]*InstanceType),
	}
}

// Get returns cached InstanceType data for a given instance type name.
func (r *Resolver) Get(name string) (*InstanceType, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	it, ok := r.cache[name]
	return it, ok
}

// Set adds or updates a single entry in the cache. Intended for tests.
func (r *Resolver) Set(name string, it *InstanceType) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cache[name] = it
}

// Refresh calls DescribeInstanceTypes (paginated) and populates the cache.
func (r *Resolver) Refresh(ctx context.Context, ec2Client awsclient.EC2API) error {
	next := make(map[string]*InstanceType)

	var nextToken *string
	for {
		resp, err := ec2Client.DescribeInstanceTypes(ctx, &ec2.DescribeInstanceTypesInput{
			NextToken: nextToken,
		})
		if err != nil {
			return err
		}

		for i := range resp.InstanceTypes {
			it := transformInstanceType(&resp.InstanceTypes[i])
			if it != nil {
				next[it.InstanceType] = it
			}
		}

		if resp.NextToken == nil {
			break
		}
		nextToken = resp.NextToken
	}

	r.mu.Lock()
	r.cache = next
	r.mu.Unlock()
	return nil
}

// transformInstanceType converts an AWS InstanceTypeInfo to our InstanceType.
// Mirrors upstream aws_util.go transformInstanceType logic.
func transformInstanceType(info *ec2types.InstanceTypeInfo) *InstanceType {
	if info == nil {
		return nil
	}
	it := &InstanceType{
		InstanceType: string(info.InstanceType),
	}

	if info.VCpuInfo != nil && info.VCpuInfo.DefaultVCpus != nil {
		it.VCPU = int64(*info.VCpuInfo.DefaultVCpus)
	}
	if info.MemoryInfo != nil && info.MemoryInfo.SizeInMiB != nil {
		it.MemoryMb = *info.MemoryInfo.SizeInMiB
	}

	// Sum GPU devices across all GPU manufacturers.
	if info.GpuInfo != nil {
		for _, gpu := range info.GpuInfo.Gpus {
			if gpu.Count != nil {
				it.GPU += int64(*gpu.Count)
			}
		}
	}

	// Determine architecture.
	it.Architecture = "amd64"
	if info.ProcessorInfo != nil {
		for _, arch := range info.ProcessorInfo.SupportedArchitectures {
			if arch == ec2types.ArchitectureTypeArm64 {
				it.Architecture = "arm64"
				break
			}
		}
	}

	return it
}
