package discovery

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"aws-multi-region-ca-exteranl-grpc/pkg/awsclient"
	"aws-multi-region-ca-exteranl-grpc/pkg/cache"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/autoscaling"
	autoscalingtypes "github.com/aws/aws-sdk-go-v2/service/autoscaling/types"
)

func TestBuildSnapshot(t *testing.T) {
	t.Parallel()

	provider := &fakeProvider{
		regions: []string{"us-east-1"},
		clients: map[string]*awsclient.Clients{
			"us-east-1": {
				AutoScaling: &fakeASGClient{pages: []*autoscaling.DescribeAutoScalingGroupsOutput{{
					AutoScalingGroups: []autoscalingtypes.AutoScalingGroup{{
						AutoScalingGroupName: aws.String("asg-a"),
						Instances: []autoscalingtypes.Instance{
							{InstanceId: aws.String("i-1"), AvailabilityZone: aws.String("us-east-1a"), LifecycleState: autoscalingtypes.LifecycleStateInService},
							{InstanceId: aws.String("i-2"), AvailabilityZone: aws.String("us-east-1a"), LifecycleState: autoscalingtypes.LifecycleStatePending},
						},
					}},
				}}},
			},
		},
		errByRegion: map[string]error{},
	}

	snap, err := NewASGSnapshotBuilder(provider, nil, []string{"asg-a"}).Build(context.Background())
	if err != nil {
		t.Fatalf("BuildSnapshot error: %v", err)
	}

	if got, ok := snap.NodeGroupByProviderID["i-1"]; !ok || got != "us-east-1/asg-a" {
		t.Fatalf("instance lookup missing")
	}
	if got, ok := snap.NodeGroupByProviderID["aws:///us-east-1a/i-2"]; !ok || got != "us-east-1/asg-a" {
		t.Fatalf("providerID lookup missing")
	}

	wantInstances := []cache.Instance{
		{ID: "aws:///us-east-1a/i-1", State: cache.InstanceStateRunning},
		{ID: "aws:///us-east-1a/i-2", State: cache.InstanceStateCreating},
	}
	if got := snap.InstancesByNodeGroup["us-east-1/asg-a"]; !reflect.DeepEqual(got, wantInstances) {
		t.Fatalf("instances=%v want=%v", got, wantInstances)
	}
}

func TestBuildSnapshotErrorsOnDescribeFailure(t *testing.T) {
	t.Parallel()

	provider := &fakeProvider{
		regions: []string{"us-east-1"},
		clients: map[string]*awsclient.Clients{
			"us-east-1": {AutoScaling: &fakeASGClient{err: errors.New("boom")}},
		},
		errByRegion: map[string]error{},
	}

	if _, err := NewASGSnapshotBuilder(provider, nil, []string{"boom"}).Build(context.Background()); err == nil {
		t.Fatalf("expected error")
	}
}

func TestBuildSnapshotFilterAndNameBatching(t *testing.T) {
	t.Parallel()

	fakeASG := &fakeASGClient{pages: []*autoscaling.DescribeAutoScalingGroupsOutput{{
		AutoScalingGroups: []autoscalingtypes.AutoScalingGroup{{
			AutoScalingGroupName: aws.String("asg-filtered"),
		}},
	}, {
		AutoScalingGroups: []autoscalingtypes.AutoScalingGroup{{
			AutoScalingGroupName: aws.String("asg-named"),
		}},
	}}}

	provider := &fakeProvider{
		regions: []string{"us-east-1"},
		clients: map[string]*awsclient.Clients{
			"us-east-1": {
				AutoScaling: fakeASG,
			},
		},
		errByRegion: map[string]error{},
	}

	tags := map[string]string{"env": "prod"}
	names := []string{"asg-named"}

	builder := NewASGSnapshotBuilder(provider, tags, names)
	snap, err := builder.Build(context.Background())
	if err != nil {
		t.Fatalf("Build error: %v", err)
	}

	// Expect two calls, one for tags, one for names
	if len(fakeASG.describeCalls) != 2 {
		t.Fatalf("expected 2 Describe calls, got %d", len(fakeASG.describeCalls))
	}

	firstCall := fakeASG.describeCalls[0]
	if len(firstCall.Filters) != 1 || *firstCall.Filters[0].Name != "tag:env" || firstCall.Filters[0].Values[0] != "prod" {
		t.Fatalf("first call filters missing/incorrect")
	}

	secondCall := fakeASG.describeCalls[1]
	if len(secondCall.AutoScalingGroupNames) != 1 || secondCall.AutoScalingGroupNames[0] != "asg-named" {
		t.Fatalf("second call names missing/incorrect")
	}

	if _, ok := snap.NodeGroups["us-east-1/asg-filtered"]; !ok {
		t.Fatalf("missing filtered asg")
	}
	if _, ok := snap.NodeGroups["us-east-1/asg-named"]; !ok {
		t.Fatalf("missing named asg")
	}
}
