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

	snap, err := BuildSnapshot(context.Background(), provider)
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

	if _, err := BuildSnapshot(context.Background(), provider); err == nil {
		t.Fatalf("expected error")
	}
}
