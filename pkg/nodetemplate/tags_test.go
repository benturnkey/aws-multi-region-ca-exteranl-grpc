package nodetemplate

import (
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	autoscalingtypes "github.com/aws/aws-sdk-go-v2/service/autoscaling/types"
	corev1 "k8s.io/api/core/v1"
)

func TestExtractLabelsFromTags(t *testing.T) {
	t.Parallel()

	tags := []autoscalingtypes.TagDescription{
		{Key: aws.String("k8s.io/cluster-autoscaler/node-template/label/team"), Value: aws.String("platform")},
		{Key: aws.String("k8s.io/cluster-autoscaler/node-template/label/env"), Value: aws.String("prod")},
		{Key: aws.String("other-tag"), Value: aws.String("ignored")},
		{Key: aws.String("k8s.io/cluster-autoscaler/node-template/label/"), Value: aws.String("empty-key")},
	}

	labels := ExtractLabelsFromTags(tags)
	if len(labels) != 2 {
		t.Fatalf("labels count=%d want=2", len(labels))
	}
	if labels["team"] != "platform" {
		t.Fatalf("team=%q want=platform", labels["team"])
	}
	if labels["env"] != "prod" {
		t.Fatalf("env=%q want=prod", labels["env"])
	}
}

func TestExtractLabelsFromTagsEmpty(t *testing.T) {
	t.Parallel()

	labels := ExtractLabelsFromTags(nil)
	if len(labels) != 0 {
		t.Fatalf("expected empty map, got %v", labels)
	}
}

func TestExtractTaintsFromTags(t *testing.T) {
	t.Parallel()

	tags := []autoscalingtypes.TagDescription{
		{Key: aws.String("k8s.io/cluster-autoscaler/node-template/taint/dedicated"), Value: aws.String("gpu:NoSchedule")},
		{Key: aws.String("k8s.io/cluster-autoscaler/node-template/taint/special"), Value: aws.String("true:PreferNoSchedule")},
		{Key: aws.String("other-tag"), Value: aws.String("ignored")},
		{Key: aws.String("k8s.io/cluster-autoscaler/node-template/taint/bad"), Value: aws.String("no-colon")},
		{Key: aws.String("k8s.io/cluster-autoscaler/node-template/taint/"), Value: aws.String("true:NoSchedule")},
	}

	taints := ExtractTaintsFromTags(tags)
	if len(taints) != 2 {
		t.Fatalf("taints count=%d want=2", len(taints))
	}

	if taints[0].Key != "dedicated" || taints[0].Value != "gpu" || taints[0].Effect != corev1.TaintEffectNoSchedule {
		t.Fatalf("taint[0]=%+v", taints[0])
	}
	if taints[1].Key != "special" || taints[1].Value != "true" || taints[1].Effect != corev1.TaintEffectPreferNoSchedule {
		t.Fatalf("taint[1]=%+v", taints[1])
	}
}

func TestExtractTaintsFromTagsEmpty(t *testing.T) {
	t.Parallel()

	taints := ExtractTaintsFromTags(nil)
	if len(taints) != 0 {
		t.Fatalf("expected empty, got %v", taints)
	}
}

func TestExtractResourcesFromTags(t *testing.T) {
	t.Parallel()

	tags := []autoscalingtypes.TagDescription{
		{Key: aws.String("k8s.io/cluster-autoscaler/node-template/resources/ephemeral-storage"), Value: aws.String("100Gi")},
		{Key: aws.String("k8s.io/cluster-autoscaler/node-template/resources/hugepages-2Mi"), Value: aws.String("512Mi")},
		{Key: aws.String("other-tag"), Value: aws.String("ignored")},
		{Key: aws.String("k8s.io/cluster-autoscaler/node-template/resources/bad"), Value: aws.String("not-a-quantity-!@#")},
	}

	resources := ExtractResourcesFromTags(tags)
	if len(resources) != 2 {
		t.Fatalf("resources count=%d want=2", len(resources))
	}

	ephemeral, ok := resources["ephemeral-storage"]
	if !ok {
		t.Fatal("ephemeral-storage not found")
	}
	if ephemeral.String() != "100Gi" {
		t.Fatalf("ephemeral-storage=%s want=100Gi", ephemeral.String())
	}

	hugepages, ok := resources["hugepages-2Mi"]
	if !ok {
		t.Fatal("hugepages-2Mi not found")
	}
	if hugepages.String() != "512Mi" {
		t.Fatalf("hugepages-2Mi=%s want=512Mi", hugepages.String())
	}
}
