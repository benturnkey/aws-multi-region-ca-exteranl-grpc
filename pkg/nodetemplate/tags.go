package nodetemplate

import (
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	autoscalingtypes "github.com/aws/aws-sdk-go-v2/service/autoscaling/types"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

const (
	labelTagPrefix    = "k8s.io/cluster-autoscaler/node-template/label/"
	taintTagPrefix    = "k8s.io/cluster-autoscaler/node-template/taint/"
	resourceTagPrefix = "k8s.io/cluster-autoscaler/node-template/resources/"
)

// ExtractLabelsFromTags extracts Kubernetes labels from ASG tags with the prefix
// "k8s.io/cluster-autoscaler/node-template/label/".
// Mirrors upstream aws_manager.go extractLabelsFromAsg.
func ExtractLabelsFromTags(tags []autoscalingtypes.TagDescription) map[string]string {
	labels := make(map[string]string)
	for _, tag := range tags {
		key := aws.ToString(tag.Key)
		if strings.HasPrefix(key, labelTagPrefix) {
			labelKey := key[len(labelTagPrefix):]
			if labelKey != "" {
				labels[labelKey] = aws.ToString(tag.Value)
			}
		}
	}
	return labels
}

// ExtractTaintsFromTags extracts Kubernetes taints from ASG tags with the prefix
// "k8s.io/cluster-autoscaler/node-template/taint/".
// Tag value format: "value:Effect" (e.g. "true:NoSchedule").
// Mirrors upstream aws_manager.go extractTaintsFromAsg.
func ExtractTaintsFromTags(tags []autoscalingtypes.TagDescription) []corev1.Taint {
	var taints []corev1.Taint
	for _, tag := range tags {
		key := aws.ToString(tag.Key)
		if !strings.HasPrefix(key, taintTagPrefix) {
			continue
		}
		taintKey := key[len(taintTagPrefix):]
		if taintKey == "" {
			continue
		}
		value := aws.ToString(tag.Value)
		parts := strings.SplitN(value, ":", 2)
		if len(parts) != 2 {
			continue
		}
		taints = append(taints, corev1.Taint{
			Key:    taintKey,
			Value:  parts[0],
			Effect: corev1.TaintEffect(parts[1]),
		})
	}
	return taints
}

// ExtractResourcesFromTags extracts custom resource quantities from ASG tags with
// the prefix "k8s.io/cluster-autoscaler/node-template/resources/".
// Mirrors upstream aws_manager.go extractAllocatableResourcesFromAsg.
func ExtractResourcesFromTags(tags []autoscalingtypes.TagDescription) map[corev1.ResourceName]*resource.Quantity {
	resources := make(map[corev1.ResourceName]*resource.Quantity)
	for _, tag := range tags {
		key := aws.ToString(tag.Key)
		if !strings.HasPrefix(key, resourceTagPrefix) {
			continue
		}
		resourceName := key[len(resourceTagPrefix):]
		if resourceName == "" {
			continue
		}
		value := aws.ToString(tag.Value)
		qty, err := resource.ParseQuantity(value)
		if err != nil {
			continue
		}
		resources[corev1.ResourceName(resourceName)] = &qty
	}
	return resources
}
