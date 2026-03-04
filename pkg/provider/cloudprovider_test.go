package provider_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"aws-multi-region-ca-exteranl-grpc/pkg/provider"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"k8s.io/autoscaler/cluster-autoscaler/cloudprovider/externalgrpc/protos"
)

type fakeLister struct {
	ids []string
	err error
}

func (f *fakeLister) NodeGroupIDs(context.Context) ([]string, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.ids, nil
}

func TestNodeGroupsReturnsIDs(t *testing.T) {
	t.Parallel()

	srv := provider.NewCloudProviderServer(&fakeLister{ids: []string{"us-east-1/asg-a", "us-west-2/asg-b"}})
	resp, err := srv.NodeGroups(context.Background(), &protos.NodeGroupsRequest{})
	if err != nil {
		t.Fatalf("NodeGroups error: %v", err)
	}

	got := make([]string, 0, len(resp.GetNodeGroups()))
	for _, ng := range resp.GetNodeGroups() {
		got = append(got, ng.GetId())
	}
	want := []string{"us-east-1/asg-a", "us-west-2/asg-b"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("NodeGroups IDs=%v want=%v", got, want)
	}
}

func TestNodeGroupsMapsErrorsToUnavailable(t *testing.T) {
	t.Parallel()

	srv := provider.NewCloudProviderServer(&fakeLister{err: errors.New("boom")})
	_, err := srv.NodeGroups(context.Background(), &protos.NodeGroupsRequest{})
	if err == nil {
		t.Fatalf("expected error")
	}
	if status.Code(err) != codes.Unavailable {
		t.Fatalf("status code=%v want=%v", status.Code(err), codes.Unavailable)
	}
}

func TestNodeGroupForNodeRequiresNode(t *testing.T) {
	t.Parallel()

	srv := provider.NewCloudProviderServer(&fakeLister{})
	_, err := srv.NodeGroupForNode(context.Background(), &protos.NodeGroupForNodeRequest{})
	if err == nil {
		t.Fatalf("expected error")
	}
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("status code=%v want=%v", status.Code(err), codes.InvalidArgument)
	}
}

func TestNodeGroupForNodeReturnsResolvedID(t *testing.T) {
	t.Parallel()

	srv := provider.NewCloudProviderServer(
		&fakeLister{},
		provider.WithNodeGroupForNodeResolver(func(context.Context, *protos.ExternalGrpcNode) (string, error) {
			return "us-east-1/asg-a", nil
		}),
	)
	resp, err := srv.NodeGroupForNode(context.Background(), &protos.NodeGroupForNodeRequest{
		Node: &protos.ExternalGrpcNode{Name: "node-a"},
	})
	if err != nil {
		t.Fatalf("NodeGroupForNode error: %v", err)
	}
	if got := resp.GetNodeGroup().GetId(); got != "us-east-1/asg-a" {
		t.Fatalf("id=%q", got)
	}
}

func TestNodeGroupNodesValidatesID(t *testing.T) {
	t.Parallel()

	srv := provider.NewCloudProviderServer(&fakeLister{})
	_, err := srv.NodeGroupNodes(context.Background(), &protos.NodeGroupNodesRequest{Id: "bad"})
	if err == nil {
		t.Fatalf("expected error")
	}
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("status code=%v want=%v", status.Code(err), codes.InvalidArgument)
	}
}

func TestNodeGroupNodesReturnsInstances(t *testing.T) {
	t.Parallel()

	srv := provider.NewCloudProviderServer(
		&fakeLister{},
		provider.WithNodeGroupInstancesLister(func(context.Context, string) ([]*protos.Instance, error) {
			return []*protos.Instance{
				{Id: "aws:///us-east-1a/i-123"},
				{Id: "aws:///us-east-1a/i-456"},
			}, nil
		}),
	)
	resp, err := srv.NodeGroupNodes(context.Background(), &protos.NodeGroupNodesRequest{Id: "us-east-1/asg-a"})
	if err != nil {
		t.Fatalf("NodeGroupNodes error: %v", err)
	}
	if len(resp.GetInstances()) != 2 {
		t.Fatalf("instances=%d want=2", len(resp.GetInstances()))
	}
}

func TestRefreshCallsRefresher(t *testing.T) {
	t.Parallel()

	called := false
	srv := provider.NewCloudProviderServer(
		&fakeLister{},
		provider.WithRefresher(func(context.Context) error {
			called = true
			return nil
		}),
	)
	if _, err := srv.Refresh(context.Background(), &protos.RefreshRequest{}); err != nil {
		t.Fatalf("Refresh error: %v", err)
	}
	if !called {
		t.Fatalf("expected refresher to be called")
	}
}

func TestRefreshMapsErrorsToUnavailable(t *testing.T) {
	t.Parallel()

	srv := provider.NewCloudProviderServer(
		&fakeLister{},
		provider.WithRefresher(func(context.Context) error {
			return errors.New("boom")
		}),
	)
	_, err := srv.Refresh(context.Background(), &protos.RefreshRequest{})
	if err == nil {
		t.Fatalf("expected error")
	}
	if status.Code(err) != codes.Unavailable {
		t.Fatalf("status code=%v want=%v", status.Code(err), codes.Unavailable)
	}
}
