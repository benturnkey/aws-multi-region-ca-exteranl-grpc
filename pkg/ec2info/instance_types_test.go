package ec2info

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

type fakeEC2Client struct {
	describeInstanceTypesOutput []*ec2.DescribeInstanceTypesOutput
	describeInstanceTypesErr    error
	describeInstanceTypesCalls  int

	describeLTVersionsOutput *ec2.DescribeLaunchTemplateVersionsOutput
	describeLTVersionsErr    error
}

func (f *fakeEC2Client) DescribeInstanceTypes(_ context.Context, _ *ec2.DescribeInstanceTypesInput, _ ...func(*ec2.Options)) (*ec2.DescribeInstanceTypesOutput, error) {
	if f.describeInstanceTypesErr != nil {
		return nil, f.describeInstanceTypesErr
	}
	if f.describeInstanceTypesCalls >= len(f.describeInstanceTypesOutput) {
		return &ec2.DescribeInstanceTypesOutput{}, nil
	}
	out := f.describeInstanceTypesOutput[f.describeInstanceTypesCalls]
	f.describeInstanceTypesCalls++
	return out, nil
}

func (f *fakeEC2Client) DescribeLaunchTemplateVersions(_ context.Context, _ *ec2.DescribeLaunchTemplateVersionsInput, _ ...func(*ec2.Options)) (*ec2.DescribeLaunchTemplateVersionsOutput, error) {
	if f.describeLTVersionsErr != nil {
		return nil, f.describeLTVersionsErr
	}
	return f.describeLTVersionsOutput, nil
}

func TestResolverRefreshAndGet(t *testing.T) {
	t.Parallel()

	client := &fakeEC2Client{
		describeInstanceTypesOutput: []*ec2.DescribeInstanceTypesOutput{
			{
				InstanceTypes: []ec2types.InstanceTypeInfo{
					{
						InstanceType: ec2types.InstanceTypeM5Xlarge,
						VCpuInfo:     &ec2types.VCpuInfo{DefaultVCpus: aws.Int32(4)},
						MemoryInfo:   &ec2types.MemoryInfo{SizeInMiB: aws.Int64(16384)},
						ProcessorInfo: &ec2types.ProcessorInfo{
							SupportedArchitectures: []ec2types.ArchitectureType{ec2types.ArchitectureTypeX8664},
						},
					},
					{
						InstanceType: ec2types.InstanceTypeM6gLarge,
						VCpuInfo:     &ec2types.VCpuInfo{DefaultVCpus: aws.Int32(2)},
						MemoryInfo:   &ec2types.MemoryInfo{SizeInMiB: aws.Int64(8192)},
						ProcessorInfo: &ec2types.ProcessorInfo{
							SupportedArchitectures: []ec2types.ArchitectureType{ec2types.ArchitectureTypeArm64},
						},
					},
				},
			},
		},
	}

	resolver := NewResolver()
	if err := resolver.Refresh(context.Background(), client); err != nil {
		t.Fatalf("Refresh: %v", err)
	}

	it, ok := resolver.Get("m5.xlarge")
	if !ok {
		t.Fatal("m5.xlarge not found")
	}
	if it.VCPU != 4 {
		t.Fatalf("VCPU=%d want=4", it.VCPU)
	}
	if it.MemoryMb != 16384 {
		t.Fatalf("MemoryMb=%d want=16384", it.MemoryMb)
	}
	if it.Architecture != "amd64" {
		t.Fatalf("Architecture=%q want=amd64", it.Architecture)
	}

	it2, ok := resolver.Get("m6g.large")
	if !ok {
		t.Fatal("m6g.large not found")
	}
	if it2.VCPU != 2 {
		t.Fatalf("VCPU=%d want=2", it2.VCPU)
	}
	if it2.Architecture != "arm64" {
		t.Fatalf("Architecture=%q want=arm64", it2.Architecture)
	}

	_, ok = resolver.Get("nonexistent")
	if ok {
		t.Fatal("expected nonexistent to not be found")
	}
}

func TestResolverRefreshWithGPU(t *testing.T) {
	t.Parallel()

	client := &fakeEC2Client{
		describeInstanceTypesOutput: []*ec2.DescribeInstanceTypesOutput{
			{
				InstanceTypes: []ec2types.InstanceTypeInfo{
					{
						InstanceType: ec2types.InstanceType("p3.xlarge"),
						VCpuInfo:     &ec2types.VCpuInfo{DefaultVCpus: aws.Int32(4)},
						MemoryInfo:   &ec2types.MemoryInfo{SizeInMiB: aws.Int64(61440)},
						GpuInfo: &ec2types.GpuInfo{
							Gpus: []ec2types.GpuDeviceInfo{
								{Count: aws.Int32(1)},
							},
						},
						ProcessorInfo: &ec2types.ProcessorInfo{
							SupportedArchitectures: []ec2types.ArchitectureType{ec2types.ArchitectureTypeX8664},
						},
					},
				},
			},
		},
	}

	resolver := NewResolver()
	if err := resolver.Refresh(context.Background(), client); err != nil {
		t.Fatalf("Refresh: %v", err)
	}

	it, ok := resolver.Get("p3.xlarge")
	if !ok {
		t.Fatal("p3.xlarge not found")
	}
	if it.GPU != 1 {
		t.Fatalf("GPU=%d want=1", it.GPU)
	}
}

func TestResolverRefreshError(t *testing.T) {
	t.Parallel()

	client := &fakeEC2Client{
		describeInstanceTypesErr: errors.New("boom"),
	}

	resolver := NewResolver()
	if err := resolver.Refresh(context.Background(), client); err == nil {
		t.Fatal("expected error")
	}
}

func TestResolverRefreshPagination(t *testing.T) {
	t.Parallel()

	nextToken := "page2"
	client := &fakeEC2Client{
		describeInstanceTypesOutput: []*ec2.DescribeInstanceTypesOutput{
			{
				InstanceTypes: []ec2types.InstanceTypeInfo{
					{
						InstanceType: ec2types.InstanceTypeT3Micro,
						VCpuInfo:     &ec2types.VCpuInfo{DefaultVCpus: aws.Int32(2)},
						MemoryInfo:   &ec2types.MemoryInfo{SizeInMiB: aws.Int64(1024)},
						ProcessorInfo: &ec2types.ProcessorInfo{
							SupportedArchitectures: []ec2types.ArchitectureType{ec2types.ArchitectureTypeX8664},
						},
					},
				},
				NextToken: &nextToken,
			},
			{
				InstanceTypes: []ec2types.InstanceTypeInfo{
					{
						InstanceType: ec2types.InstanceTypeT3Small,
						VCpuInfo:     &ec2types.VCpuInfo{DefaultVCpus: aws.Int32(2)},
						MemoryInfo:   &ec2types.MemoryInfo{SizeInMiB: aws.Int64(2048)},
						ProcessorInfo: &ec2types.ProcessorInfo{
							SupportedArchitectures: []ec2types.ArchitectureType{ec2types.ArchitectureTypeX8664},
						},
					},
				},
			},
		},
	}

	resolver := NewResolver()
	if err := resolver.Refresh(context.Background(), client); err != nil {
		t.Fatalf("Refresh: %v", err)
	}

	if _, ok := resolver.Get("t3.micro"); !ok {
		t.Fatal("t3.micro not found (page 1)")
	}
	if _, ok := resolver.Get("t3.small"); !ok {
		t.Fatal("t3.small not found (page 2)")
	}
}
