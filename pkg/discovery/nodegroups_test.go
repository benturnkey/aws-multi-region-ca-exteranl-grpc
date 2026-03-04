package discovery

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"aws-multi-region-ca-exteranl-grpc/pkg/awsclient"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/autoscaling"
	autoscalingtypes "github.com/aws/aws-sdk-go-v2/service/autoscaling/types"
)

type fakeProvider struct {
	regions     []string
	clients     map[string]*awsclient.Clients
	errByRegion map[string]error
}

func (f *fakeProvider) Regions() []string {
	return f.regions
}

func (f *fakeProvider) ForRegion(region string) (*awsclient.Clients, error) {
	if err, ok := f.errByRegion[region]; ok {
		return nil, err
	}
	c, ok := f.clients[region]
	if !ok {
		return nil, errors.New("region not found")
	}
	return c, nil
}

type fakeASGClient struct {
	pages         []*autoscaling.DescribeAutoScalingGroupsOutput
	err           error
	calls         int
	describeCalls []*autoscaling.DescribeAutoScalingGroupsInput
}

func (f *fakeASGClient) DescribeAutoScalingGroups(_ context.Context, in *autoscaling.DescribeAutoScalingGroupsInput, _ ...func(*autoscaling.Options)) (*autoscaling.DescribeAutoScalingGroupsOutput, error) {
	f.describeCalls = append(f.describeCalls, in)
	if f.err != nil {
		return nil, f.err
	}
	if f.calls >= len(f.pages) {
		return &autoscaling.DescribeAutoScalingGroupsOutput{}, nil
	}
	out := f.pages[f.calls]
	f.calls++
	return out, nil
}

func (f *fakeASGClient) SetDesiredCapacity(context.Context, *autoscaling.SetDesiredCapacityInput, ...func(*autoscaling.Options)) (*autoscaling.SetDesiredCapacityOutput, error) {
	return nil, nil
}

func (f *fakeASGClient) TerminateInstanceInAutoScalingGroup(context.Context, *autoscaling.TerminateInstanceInAutoScalingGroupInput, ...func(*autoscaling.Options)) (*autoscaling.TerminateInstanceInAutoScalingGroupOutput, error) {
	return nil, nil
}

func TestListNodeGroupIDs(t *testing.T) {
	t.Parallel()

	provider := &fakeProvider{
		regions: []string{"us-east-1", "us-west-2"},
		clients: map[string]*awsclient.Clients{
			"us-east-1": {
				AutoScaling: &fakeASGClient{pages: []*autoscaling.DescribeAutoScalingGroupsOutput{
					{
						AutoScalingGroups: []autoscalingtypes.AutoScalingGroup{
							{AutoScalingGroupName: aws.String("asg-east-a")},
							{AutoScalingGroupName: aws.String("asg-east-b")},
						},
						NextToken: aws.String("page2"),
					},
					{
						AutoScalingGroups: []autoscalingtypes.AutoScalingGroup{
							{AutoScalingGroupName: aws.String("asg-east-c")},
						},
					},
				}},
			},
			"us-west-2": {
				AutoScaling: &fakeASGClient{pages: []*autoscaling.DescribeAutoScalingGroupsOutput{
					{
						AutoScalingGroups: []autoscalingtypes.AutoScalingGroup{
							{AutoScalingGroupName: aws.String("asg-west-a")},
						},
					},
				}},
			},
		},
		errByRegion: map[string]error{},
	}

	got, err := ListNodeGroupIDs(context.Background(), provider)
	if err != nil {
		t.Fatalf("ListNodeGroupIDs error: %v", err)
	}

	want := []string{
		"us-east-1/asg-east-a",
		"us-east-1/asg-east-b",
		"us-east-1/asg-east-c",
		"us-west-2/asg-west-a",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ListNodeGroupIDs=%v want=%v", got, want)
	}
}

func TestListNodeGroupIDsReturnsErrorOnProviderLookupFailure(t *testing.T) {
	t.Parallel()

	provider := &fakeProvider{
		regions:     []string{"us-east-1"},
		errByRegion: map[string]error{"us-east-1": errors.New("boom")},
		clients:     map[string]*awsclient.Clients{},
	}

	if _, err := ListNodeGroupIDs(context.Background(), provider); err == nil {
		t.Fatalf("expected error")
	}
}

func TestListNodeGroupIDsReturnsErrorOnDescribeFailure(t *testing.T) {
	t.Parallel()

	provider := &fakeProvider{
		regions: []string{"us-east-1"},
		clients: map[string]*awsclient.Clients{
			"us-east-1": {
				AutoScaling: &fakeASGClient{err: errors.New("describe failed")},
			},
		},
		errByRegion: map[string]error{},
	}

	if _, err := ListNodeGroupIDs(context.Background(), provider); err == nil {
		t.Fatalf("expected error")
	}
}
