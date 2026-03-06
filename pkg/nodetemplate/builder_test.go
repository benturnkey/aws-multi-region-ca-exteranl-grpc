package nodetemplate

import (
	"strings"
	"testing"

	"aws-multi-region-ca-exteranl-grpc/pkg/cache"
	"aws-multi-region-ca-exteranl-grpc/pkg/ec2info"
	"github.com/aws/aws-sdk-go-v2/aws"
	autoscalingtypes "github.com/aws/aws-sdk-go-v2/service/autoscaling/types"
	corev1 "k8s.io/api/core/v1"
)

func TestBuildTemplateNode(t *testing.T) {
	t.Parallel()

	ng := cache.NodeGroup{
		ID:                "us-east-1/my-asg",
		MinSize:           1,
		MaxSize:           10,
		TargetSize:        3,
		AvailabilityZones: []string{"us-east-1a", "us-east-1b"},
		Tags: []autoscalingtypes.TagDescription{
			{Key: aws.String("k8s.io/cluster-autoscaler/node-template/label/team"), Value: aws.String("platform")},
			{Key: aws.String("k8s.io/cluster-autoscaler/node-template/taint/dedicated"), Value: aws.String("gpu:NoSchedule")},
		},
	}

	it := &ec2info.InstanceType{
		InstanceType: "m5.xlarge",
		VCPU:         4,
		MemoryMb:     16384,
		GPU:          0,
		Architecture: "amd64",
	}

	node := BuildTemplateNode(ng, it)

	// Name should contain the ASG name.
	if !strings.HasPrefix(node.Name, "my-asg-template-") {
		t.Fatalf("Name=%q, want prefix my-asg-template-", node.Name)
	}

	// Check generic labels.
	if node.Labels["kubernetes.io/os"] != "linux" {
		t.Fatalf("os label=%q", node.Labels["kubernetes.io/os"])
	}
	if node.Labels["kubernetes.io/arch"] != "amd64" {
		t.Fatalf("arch label=%q", node.Labels["kubernetes.io/arch"])
	}
	if node.Labels["node.kubernetes.io/instance-type"] != "m5.xlarge" {
		t.Fatalf("instance-type label=%q", node.Labels["node.kubernetes.io/instance-type"])
	}
	if node.Labels["topology.kubernetes.io/zone"] != "us-east-1a" {
		t.Fatalf("zone label=%q", node.Labels["topology.kubernetes.io/zone"])
	}
	if node.Labels["topology.kubernetes.io/region"] != "us-east-1" {
		t.Fatalf("region label=%q", node.Labels["topology.kubernetes.io/region"])
	}
	if node.Labels["topology.ebs.csi.aws.com/zone"] != "us-east-1a" {
		t.Fatalf("ebs zone label=%q", node.Labels["topology.ebs.csi.aws.com/zone"])
	}

	// Check tag-extracted label.
	if node.Labels["team"] != "platform" {
		t.Fatalf("team label=%q", node.Labels["team"])
	}

	// Check taints.
	if len(node.Spec.Taints) != 1 {
		t.Fatalf("taints count=%d want=1", len(node.Spec.Taints))
	}
	if node.Spec.Taints[0].Key != "dedicated" || node.Spec.Taints[0].Effect != corev1.TaintEffectNoSchedule {
		t.Fatalf("taint=%+v", node.Spec.Taints[0])
	}

	// Check provider ID.
	if node.Spec.ProviderID != "aws:///us-east-1a/template-my-asg" {
		t.Fatalf("ProviderID=%q", node.Spec.ProviderID)
	}

	// Check capacity.
	cpu := node.Status.Capacity[corev1.ResourceCPU]
	if cpu.Value() != 4 {
		t.Fatalf("CPU=%d want=4", cpu.Value())
	}
	mem := node.Status.Capacity[corev1.ResourceMemory]
	expectedMemBytes := int64(16384 * 1024 * 1024)
	if mem.Value() != expectedMemBytes {
		t.Fatalf("Memory=%d want=%d", mem.Value(), expectedMemBytes)
	}
	pods := node.Status.Capacity[corev1.ResourcePods]
	if pods.Value() != 110 {
		t.Fatalf("Pods=%d want=110", pods.Value())
	}

	// No GPU expected.
	if _, ok := node.Status.Capacity[corev1.ResourceName("nvidia.com/gpu")]; ok {
		t.Fatal("expected no GPU resource")
	}

	// Allocatable should equal Capacity.
	allocCPU := node.Status.Allocatable[corev1.ResourceCPU]
	if allocCPU.Value() != 4 {
		t.Fatalf("Allocatable CPU=%d want=4", allocCPU.Value())
	}

	// Check conditions.
	if len(node.Status.Conditions) != 4 {
		t.Fatalf("conditions count=%d want=4", len(node.Status.Conditions))
	}
	foundReady := false
	for _, c := range node.Status.Conditions {
		if c.Type == corev1.NodeReady && c.Status == corev1.ConditionTrue {
			foundReady = true
		}
	}
	if !foundReady {
		t.Fatal("expected NodeReady=True condition")
	}
}

func TestBuildTemplateNodeWithGPU(t *testing.T) {
	t.Parallel()

	ng := cache.NodeGroup{
		ID:                "us-west-2/gpu-asg",
		AvailabilityZones: []string{"us-west-2a"},
	}

	it := &ec2info.InstanceType{
		InstanceType: "p3.2xlarge",
		VCPU:         8,
		MemoryMb:     61440,
		GPU:          1,
		Architecture: "amd64",
	}

	node := BuildTemplateNode(ng, it)

	gpu, ok := node.Status.Capacity[corev1.ResourceName("nvidia.com/gpu")]
	if !ok {
		t.Fatal("expected nvidia.com/gpu resource")
	}
	if gpu.Value() != 1 {
		t.Fatalf("GPU=%d want=1", gpu.Value())
	}
}

func TestBuildTemplateNodeArm64(t *testing.T) {
	t.Parallel()

	ng := cache.NodeGroup{
		ID:                "us-east-1/arm-asg",
		AvailabilityZones: []string{"us-east-1a"},
	}

	it := &ec2info.InstanceType{
		InstanceType: "m6g.large",
		VCPU:         2,
		MemoryMb:     8192,
		Architecture: "arm64",
	}

	node := BuildTemplateNode(ng, it)

	if node.Labels["kubernetes.io/arch"] != "arm64" {
		t.Fatalf("arch=%q want=arm64", node.Labels["kubernetes.io/arch"])
	}
}
