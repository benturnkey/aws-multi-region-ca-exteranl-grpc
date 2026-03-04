package provider

import (
	"context"

	"aws-multi-region-ca-exteranl-grpc/pkg/awsclient"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/anypb"
	"k8s.io/autoscaler/cluster-autoscaler/cloudprovider/externalgrpc/protos"
)

// NodeGroupIDLister is the read-only contract needed for NodeGroups RPC.
type NodeGroupIDLister interface {
	NodeGroupIDs(ctx context.Context) ([]string, error)
}

// NodeGroupForNodeResolver maps a node to its nodegroup ID.
type NodeGroupForNodeResolver func(ctx context.Context, node *protos.ExternalGrpcNode) (string, error)

// NodeGroupInstancesLister returns instances for a nodegroup.
type NodeGroupInstancesLister func(ctx context.Context, nodeGroupID string) ([]*protos.Instance, error)
type Refresher func(ctx context.Context) error

type options struct {
	nodeGroupForNodeResolver NodeGroupForNodeResolver
	nodeGroupInstancesLister NodeGroupInstancesLister
	refresher                Refresher
}

// Option customizes server behavior.
type Option func(*options)

// WithNodeGroupForNodeResolver customizes NodeGroupForNode lookups.
func WithNodeGroupForNodeResolver(fn NodeGroupForNodeResolver) Option {
	return func(o *options) {
		o.nodeGroupForNodeResolver = fn
	}
}

// WithNodeGroupInstancesLister customizes NodeGroupNodes lookups.
func WithNodeGroupInstancesLister(fn NodeGroupInstancesLister) Option {
	return func(o *options) {
		o.nodeGroupInstancesLister = fn
	}
}

// WithRefresher customizes Refresh behavior.
func WithRefresher(fn Refresher) Option {
	return func(o *options) {
		o.refresher = fn
	}
}

// CloudProviderServer implements the externalgrpc CloudProvider service.
type CloudProviderServer struct {
	protos.UnimplementedCloudProviderServer

	lister                   NodeGroupIDLister
	nodeGroupForNodeResolver NodeGroupForNodeResolver
	nodeGroupInstancesLister NodeGroupInstancesLister
	refresher                Refresher
}

// NewCloudProviderServer creates a CloudProvider RPC server.
func NewCloudProviderServer(lister NodeGroupIDLister, opts ...Option) *CloudProviderServer {
	o := &options{}
	for _, opt := range opts {
		opt(o)
	}
	return &CloudProviderServer{
		lister:                   lister,
		nodeGroupForNodeResolver: o.nodeGroupForNodeResolver,
		nodeGroupInstancesLister: o.nodeGroupInstancesLister,
		refresher:                o.refresher,
	}
}

// NodeGroups returns discovered node groups.
func (s *CloudProviderServer) NodeGroups(ctx context.Context, _ *protos.NodeGroupsRequest) (*protos.NodeGroupsResponse, error) {
	ids, err := s.lister.NodeGroupIDs(ctx)
	if err != nil {
		return nil, status.Errorf(codes.Unavailable, "discover nodegroups: %v", err)
	}

	nodeGroups := make([]*protos.NodeGroup, 0, len(ids))
	for _, id := range ids {
		nodeGroups = append(nodeGroups, &protos.NodeGroup{Id: id})
	}

	return &protos.NodeGroupsResponse{NodeGroups: nodeGroups}, nil
}

// NodeGroupForNode returns the nodegroup ID for a given node.
func (s *CloudProviderServer) NodeGroupForNode(ctx context.Context, req *protos.NodeGroupForNodeRequest) (*protos.NodeGroupForNodeResponse, error) {
	node := req.GetNode()
	if node == nil {
		return nil, status.Error(codes.InvalidArgument, "node is required")
	}
	if s.nodeGroupForNodeResolver == nil {
		return &protos.NodeGroupForNodeResponse{NodeGroup: &protos.NodeGroup{}}, nil
	}

	id, err := s.nodeGroupForNodeResolver(ctx, node)
	if err != nil {
		return nil, status.Errorf(codes.Unavailable, "resolve nodegroup for node: %v", err)
	}
	return &protos.NodeGroupForNodeResponse{NodeGroup: &protos.NodeGroup{Id: id}}, nil
}

// NodeGroupNodes returns cloud instances for the requested nodegroup.
func (s *CloudProviderServer) NodeGroupNodes(ctx context.Context, req *protos.NodeGroupNodesRequest) (*protos.NodeGroupNodesResponse, error) {
	id := req.GetId()
	if _, _, err := awsclient.ParseNodeGroupID(id); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid nodegroup id: %v", err)
	}
	if s.nodeGroupInstancesLister == nil {
		return &protos.NodeGroupNodesResponse{Instances: nil}, nil
	}

	instances, err := s.nodeGroupInstancesLister(ctx, id)
	if err != nil {
		return nil, status.Errorf(codes.Unavailable, "list nodegroup instances: %v", err)
	}
	return &protos.NodeGroupNodesResponse{Instances: instances}, nil
}

// Refresh rebuilds provider caches/state.
func (s *CloudProviderServer) Refresh(ctx context.Context, _ *protos.RefreshRequest) (*protos.RefreshResponse, error) {
	if s.refresher == nil {
		return &protos.RefreshResponse{}, nil
	}
	if err := s.refresher(ctx); err != nil {
		return nil, status.Errorf(codes.Unavailable, "refresh: %v", err)
	}
	return &protos.RefreshResponse{}, nil
}

// GPULabel is a no-op: GPUs are unsupported in this provider.
func (s *CloudProviderServer) GPULabel(context.Context, *protos.GPULabelRequest) (*protos.GPULabelResponse, error) {
	return &protos.GPULabelResponse{Label: ""}, nil
}

// GetAvailableGPUTypes is a no-op: GPUs are unsupported in this provider.
func (s *CloudProviderServer) GetAvailableGPUTypes(context.Context, *protos.GetAvailableGPUTypesRequest) (*protos.GetAvailableGPUTypesResponse, error) {
	return &protos.GetAvailableGPUTypesResponse{GpuTypes: map[string]*anypb.Any{}}, nil
}

// Ensure we fail compilation if interface wiring drifts.
var _ protos.CloudProviderServer = (*CloudProviderServer)(nil)
