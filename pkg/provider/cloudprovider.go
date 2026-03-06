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

// NodeGroupManager handles scaling and template options for nodegroups.
type NodeGroupManager interface {
	TargetSize(ctx context.Context, id string) (int, error)
	IncreaseSize(ctx context.Context, id string, delta int) error
	DeleteNodes(ctx context.Context, id string, nodes []*protos.ExternalGrpcNode) error
	DecreaseTargetSize(ctx context.Context, id string, delta int) error
	TemplateNodeInfo(ctx context.Context, id string) ([]byte, error)
	GetOptions(ctx context.Context, id string) (*protos.NodeGroupAutoscalingOptions, error)
}

// NodeGroupMetadataProvider optionally enriches NodeGroups RPC with min/max/debug fields.
type NodeGroupMetadataProvider interface {
	NodeGroupMetadata(ctx context.Context, id string) (minSize int, maxSize int, debug string, err error)
}

type options struct {
	nodeGroupForNodeResolver NodeGroupForNodeResolver
	nodeGroupInstancesLister NodeGroupInstancesLister
	refresher                Refresher
	manager                  NodeGroupManager
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

// WithNodeGroupManager customizes mutative and template RPCs.
func WithNodeGroupManager(m NodeGroupManager) Option {
	return func(o *options) {
		o.manager = m
	}
}

// CloudProviderServer implements the externalgrpc CloudProvider service.
type CloudProviderServer struct {
	protos.UnimplementedCloudProviderServer

	lister                   NodeGroupIDLister
	nodeGroupForNodeResolver NodeGroupForNodeResolver
	nodeGroupInstancesLister NodeGroupInstancesLister
	refresher                Refresher
	manager                  NodeGroupManager
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
		manager:                  o.manager,
	}
}

// NodeGroups returns discovered node groups.
func (s *CloudProviderServer) NodeGroups(ctx context.Context, _ *protos.NodeGroupsRequest) (*protos.NodeGroupsResponse, error) {
	ids, err := s.lister.NodeGroupIDs(ctx)
	if err != nil {
		return nil, status.Errorf(codes.Unavailable, "discover nodegroups: %v", err)
	}

	nodeGroups := make([]*protos.NodeGroup, 0, len(ids))
	metadataProvider, hasMetadata := s.manager.(NodeGroupMetadataProvider)
	for _, id := range ids {
		ng := &protos.NodeGroup{Id: id}
		if hasMetadata {
			minSize, maxSize, debug, mdErr := metadataProvider.NodeGroupMetadata(ctx, id)
			if mdErr == nil {
				ng.MinSize = int32(minSize)
				ng.MaxSize = int32(maxSize)
				ng.Debug = debug
			}
		}
		nodeGroups = append(nodeGroups, ng)
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

func (s *CloudProviderServer) NodeGroupTargetSize(ctx context.Context, req *protos.NodeGroupTargetSizeRequest) (*protos.NodeGroupTargetSizeResponse, error) {
	if s.manager == nil {
		return nil, status.Errorf(codes.Unimplemented, "node group manager not configured")
	}
	size, err := s.manager.TargetSize(ctx, req.GetId())
	if err != nil {
		return nil, status.Errorf(codes.Unavailable, "target size: %v", err)
	}
	return &protos.NodeGroupTargetSizeResponse{TargetSize: int32(size)}, nil
}

func (s *CloudProviderServer) NodeGroupIncreaseSize(ctx context.Context, req *protos.NodeGroupIncreaseSizeRequest) (*protos.NodeGroupIncreaseSizeResponse, error) {
	if s.manager == nil {
		return nil, status.Errorf(codes.Unimplemented, "node group manager not configured")
	}
	delta := int(req.GetDelta())
	if delta <= 0 {
		return nil, status.Errorf(codes.InvalidArgument, "delta must be positive")
	}
	if err := s.manager.IncreaseSize(ctx, req.GetId(), delta); err != nil {
		return nil, status.Errorf(codes.Unavailable, "increase size: %v", err)
	}
	return &protos.NodeGroupIncreaseSizeResponse{}, nil
}

func (s *CloudProviderServer) NodeGroupDecreaseTargetSize(ctx context.Context, req *protos.NodeGroupDecreaseTargetSizeRequest) (*protos.NodeGroupDecreaseTargetSizeResponse, error) {
	if s.manager == nil {
		return nil, status.Errorf(codes.Unimplemented, "node group manager not configured")
	}
	delta := int(req.GetDelta())
	if delta >= 0 {
		return nil, status.Errorf(codes.InvalidArgument, "delta must be negative")
	}
	if err := s.manager.DecreaseTargetSize(ctx, req.GetId(), delta); err != nil {
		return nil, status.Errorf(codes.Unavailable, "decrease target size: %v", err)
	}
	return &protos.NodeGroupDecreaseTargetSizeResponse{}, nil
}

func (s *CloudProviderServer) NodeGroupDeleteNodes(ctx context.Context, req *protos.NodeGroupDeleteNodesRequest) (*protos.NodeGroupDeleteNodesResponse, error) {
	if s.manager == nil {
		return nil, status.Errorf(codes.Unimplemented, "node group manager not configured")
	}
	if err := s.manager.DeleteNodes(ctx, req.GetId(), req.GetNodes()); err != nil {
		return nil, status.Errorf(codes.Unavailable, "delete nodes: %v", err)
	}
	return &protos.NodeGroupDeleteNodesResponse{}, nil
}

func (s *CloudProviderServer) NodeGroupTemplateNodeInfo(ctx context.Context, req *protos.NodeGroupTemplateNodeInfoRequest) (*protos.NodeGroupTemplateNodeInfoResponse, error) {
	if s.manager == nil {
		return nil, status.Errorf(codes.Unimplemented, "node group manager not configured")
	}
	info, err := s.manager.TemplateNodeInfo(ctx, req.GetId())
	if err != nil {
		return nil, status.Errorf(codes.Unavailable, "template node info: %v", err)
	}
	return &protos.NodeGroupTemplateNodeInfoResponse{NodeBytes: info}, nil
}

func (s *CloudProviderServer) NodeGroupGetOptions(ctx context.Context, req *protos.NodeGroupAutoscalingOptionsRequest) (*protos.NodeGroupAutoscalingOptionsResponse, error) {
	if s.manager == nil {
		return nil, status.Errorf(codes.Unimplemented, "node group manager not configured")
	}
	opts, err := s.manager.GetOptions(ctx, req.GetId())
	if err != nil {
		return nil, status.Errorf(codes.Unavailable, "get options: %v", err)
	}
	return &protos.NodeGroupAutoscalingOptionsResponse{NodeGroupAutoscalingOptions: opts}, nil
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
