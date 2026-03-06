package nodetemplate

import (
	"fmt"
	"math/rand"
	"strings"

	"aws-multi-region-ca-exteranl-grpc/pkg/cache"
	"aws-multi-region-ca-exteranl-grpc/pkg/ec2info"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// BuildTemplateNode constructs a realistic v1.Node from a cached NodeGroup and
// resolved InstanceType. Mirrors upstream buildNodeFromTemplate in aws_manager.go.
func BuildTemplateNode(ng cache.NodeGroup, instanceType *ec2info.InstanceType) *corev1.Node {
	// Derive ASG name from nodegroup ID (region/asg-name).
	asgName := ng.ID
	if idx := strings.Index(ng.ID, "/"); idx >= 0 {
		asgName = ng.ID[idx+1:]
	}

	nodeName := fmt.Sprintf("%s-template-%d", asgName, rand.Int63())

	// Determine zone and region from ASG availability zones.
	var zone, region string
	if len(ng.AvailabilityZones) > 0 {
		zone = ng.AvailabilityZones[0]
		// Region is the AZ minus the trailing letter(s).
		if len(zone) > 1 {
			region = zone[:len(zone)-1]
		}
	}

	// Build generic labels (mirrors upstream buildGenericLabels).
	labels := map[string]string{
		"kubernetes.io/os":              "linux",
		"kubernetes.io/arch":            instanceType.Architecture,
		"node.kubernetes.io/instance-type": instanceType.InstanceType,
		"kubernetes.io/hostname":        nodeName,
	}
	if zone != "" {
		labels["topology.kubernetes.io/zone"] = zone
		labels["topology.ebs.csi.aws.com/zone"] = zone
	}
	if region != "" {
		labels["topology.kubernetes.io/region"] = region
	}

	// Merge tag-extracted labels (override generic ones if present).
	for k, v := range ExtractLabelsFromTags(ng.Tags) {
		labels[k] = v
	}

	// Build taints from tags.
	taints := ExtractTaintsFromTags(ng.Tags)

	// Build capacity.
	capacity := corev1.ResourceList{
		corev1.ResourceCPU:    *resource.NewQuantity(instanceType.VCPU, resource.DecimalSI),
		corev1.ResourceMemory: *resource.NewQuantity(instanceType.MemoryMb*1024*1024, resource.BinarySI),
		corev1.ResourcePods:   resource.MustParse("110"),
	}
	if instanceType.GPU > 0 {
		capacity[corev1.ResourceName("nvidia.com/gpu")] = *resource.NewQuantity(instanceType.GPU, resource.DecimalSI)
	}

	// Merge tag-extracted resources into capacity.
	for name, qty := range ExtractResourcesFromTags(ng.Tags) {
		capacity[name] = *qty
	}

	// Allocatable = Capacity (matches upstream: "TODO: use proper allocatable!!").
	allocatable := capacity.DeepCopy()

	providerID := fmt.Sprintf("aws:///%s/template-%s", zone, asgName)

	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:   nodeName,
			Labels: labels,
		},
		Spec: corev1.NodeSpec{
			ProviderID: providerID,
			Taints:     taints,
		},
		Status: corev1.NodeStatus{
			Capacity:    capacity,
			Allocatable: allocatable,
			Conditions:  buildReadyConditions(),
		},
	}
}

// buildReadyConditions returns the standard set of node conditions indicating a
// healthy, ready node. Mirrors upstream BuildReadyConditions.
func buildReadyConditions() []corev1.NodeCondition {
	return []corev1.NodeCondition{
		{
			Type:   corev1.NodeReady,
			Status: corev1.ConditionTrue,
		},
		{
			Type:   corev1.NodeNetworkUnavailable,
			Status: corev1.ConditionFalse,
		},
		{
			Type:   corev1.NodeDiskPressure,
			Status: corev1.ConditionFalse,
		},
		{
			Type:   corev1.NodeMemoryPressure,
			Status: corev1.ConditionFalse,
		},
	}
}
