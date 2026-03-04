package cache_test

import (
	"reflect"
	"testing"

	"aws-multi-region-ca-exteranl-grpc/pkg/cache"
)

func TestStoreResolvesNodeGroupIDWithFallback(t *testing.T) {
	t.Parallel()

	s := cache.NewStore()
	s.Replace(cache.Snapshot{
		NodeGroupByProviderID: map[string]string{
			"aws:///i-123": "us-east-1/asg-a",
			"i-123":        "us-east-1/asg-a",
		},
		NodeGroupByNodeName: map[string]string{"ip-1-2-3-4": "us-west-2/asg-b"},
	})

	if id, ok := s.NodeGroupIDForNode("aws:///i-123", ""); !ok || id != "us-east-1/asg-a" {
		t.Fatalf("providerID lookup = (%q,%v)", id, ok)
	}
	if id, ok := s.NodeGroupIDForNode("", "ip-1-2-3-4"); !ok || id != "us-west-2/asg-b" {
		t.Fatalf("nodeName lookup = (%q,%v)", id, ok)
	}
	if id, ok := s.NodeGroupIDForNode("aws:///us-east-1a/i-123", ""); !ok || id != "us-east-1/asg-a" {
		t.Fatalf("providerID suffix fallback = (%q,%v)", id, ok)
	}
}

func TestStoreInstancesReturnsCopy(t *testing.T) {
	t.Parallel()

	s := cache.NewStore()
	s.Replace(cache.Snapshot{
		InstancesByNodeGroup: map[string][]cache.Instance{
			"us-east-1/asg-a": {{ID: "i-1", State: cache.InstanceStateRunning}},
		},
	})

	got := s.InstancesForNodeGroup("us-east-1/asg-a")
	want := []cache.Instance{{ID: "i-1", State: cache.InstanceStateRunning}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("InstancesForNodeGroup=%v want=%v", got, want)
	}
	got[0].ID = "mutated"

	again := s.InstancesForNodeGroup("us-east-1/asg-a")
	if again[0].ID != "i-1" {
		t.Fatalf("cache was mutated through returned slice")
	}
}
