package server

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync"

	"aws-multi-region-ca-exteranl-grpc/pkg/awsclient"
	"aws-multi-region-ca-exteranl-grpc/pkg/cache"
	"aws-multi-region-ca-exteranl-grpc/pkg/config"
	"aws-multi-region-ca-exteranl-grpc/pkg/discovery"
	"aws-multi-region-ca-exteranl-grpc/pkg/provider"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"k8s.io/autoscaler/cluster-autoscaler/cloudprovider/externalgrpc/protos"
)

// NodeGroupIDListerFunc lists nodegroup IDs.
type NodeGroupIDListerFunc func(ctx context.Context) ([]string, error)

// NodeGroupForNodeResolverFunc resolves nodegroup ID for a node.
type NodeGroupForNodeResolverFunc func(ctx context.Context, node *protos.ExternalGrpcNode) (string, error)

// NodeGroupInstancesListerFunc lists instances for a nodegroup.
type NodeGroupInstancesListerFunc func(ctx context.Context, nodeGroupID string) ([]*protos.Instance, error)
type CacheRefresherFunc func(ctx context.Context) error

type startOptions struct {
	nodeGroupIDLister        NodeGroupIDListerFunc
	nodeGroupForNodeResolver NodeGroupForNodeResolverFunc
	nodeGroupInstancesLister NodeGroupInstancesListerFunc
	cacheRefresher           CacheRefresherFunc
	snapshotBuilder          discovery.SnapshotBuilder
}

// StartOption customizes server startup dependencies.
type StartOption func(*startOptions)

// WithNodeGroupIDLister overrides nodegroup discovery logic (primarily for tests).
func WithNodeGroupIDLister(lister NodeGroupIDListerFunc) StartOption {
	return func(o *startOptions) {
		o.nodeGroupIDLister = lister
	}
}

// WithNodeGroupForNodeResolver overrides node->nodegroup lookup (primarily for tests).
func WithNodeGroupForNodeResolver(resolver NodeGroupForNodeResolverFunc) StartOption {
	return func(o *startOptions) {
		o.nodeGroupForNodeResolver = resolver
	}
}

// WithNodeGroupInstancesLister overrides nodegroup->instances lookup (primarily for tests).
func WithNodeGroupInstancesLister(lister NodeGroupInstancesListerFunc) StartOption {
	return func(o *startOptions) {
		o.nodeGroupInstancesLister = lister
	}
}

// WithCacheRefresher overrides cache refresh behavior (primarily for tests).
func WithCacheRefresher(refresher CacheRefresherFunc) StartOption {
	return func(o *startOptions) {
		o.cacheRefresher = refresher
	}
}

// WithSnapshotBuilder overrides snapshot construction used by Refresh (primarily for tests).
func WithSnapshotBuilder(builder discovery.SnapshotBuilder) StartOption {
	return func(o *startOptions) {
		o.snapshotBuilder = builder
	}
}

// Service owns runtime listeners and HTTP health endpoints.
type Service struct {
	awsFactory               *awsclient.Factory
	nodeCache                *cache.Store
	nodeGroupIDLister        NodeGroupIDListerFunc
	nodeGroupForNodeResolver NodeGroupForNodeResolverFunc
	nodeGroupInstancesLister NodeGroupInstancesListerFunc
	cacheRefresher           CacheRefresherFunc
	snapshotBuilder          discovery.SnapshotBuilder
	grpcListener             net.Listener
	grpcServer               *grpc.Server
	grpcAddr                 string

	healthServer *http.Server
	healthAddr   string

	shutdownOnce sync.Once
}

// Start initializes listeners and starts serving health/readiness endpoints.
func Start(cfg config.Config, opts ...StartOption) (*Service, error) {
	factory, err := awsclient.NewFactory(context.Background(), cfg)
	if err != nil {
		return nil, fmt.Errorf("initialize aws clients: %w", err)
	}

	startCfg := &startOptions{}
	for _, opt := range opts {
		opt(startCfg)
	}

	svc := &Service{
		awsFactory: factory,
		nodeCache:  cache.NewStore(),
	}
	if startCfg.nodeGroupIDLister != nil {
		svc.nodeGroupIDLister = startCfg.nodeGroupIDLister
	} else {
		svc.nodeGroupIDLister = func(ctx context.Context) ([]string, error) {
			return discovery.ListNodeGroupIDs(ctx, factory)
		}
	}
	if startCfg.nodeGroupForNodeResolver != nil {
		svc.nodeGroupForNodeResolver = startCfg.nodeGroupForNodeResolver
	} else {
		svc.nodeGroupForNodeResolver = func(_ context.Context, node *protos.ExternalGrpcNode) (string, error) {
			id, _ := svc.nodeCache.NodeGroupIDForNode(node.GetProviderID(), node.GetName())
			return id, nil
		}
	}
	if startCfg.nodeGroupInstancesLister != nil {
		svc.nodeGroupInstancesLister = startCfg.nodeGroupInstancesLister
	} else {
		svc.nodeGroupInstancesLister = func(_ context.Context, nodeGroupID string) ([]*protos.Instance, error) {
			cached := svc.nodeCache.InstancesForNodeGroup(nodeGroupID)
			out := make([]*protos.Instance, 0, len(cached))
			for _, inst := range cached {
				out = append(out, &protos.Instance{
					Id: inst.ID,
					Status: &protos.InstanceStatus{
						InstanceState: mapInstanceState(inst.State),
					},
				})
			}
			return out, nil
		}
	}
	if startCfg.cacheRefresher != nil {
		svc.cacheRefresher = startCfg.cacheRefresher
	} else {
		if startCfg.snapshotBuilder != nil {
			svc.snapshotBuilder = startCfg.snapshotBuilder
		} else {
			svc.snapshotBuilder = discovery.NewASGSnapshotBuilder(factory)
		}
		svc.cacheRefresher = func(ctx context.Context) error {
			snap, err := svc.snapshotBuilder.Build(ctx)
			if err != nil {
				return err
			}
			svc.nodeCache.Replace(snap)
			return nil
		}
	}

	grpcLis, err := net.Listen("tcp", cfg.GRPC.Address)
	if err != nil {
		return nil, fmt.Errorf("listen grpc: %w", err)
	}

	healthLis, err := net.Listen("tcp", cfg.Health.Address)
	if err != nil {
		_ = grpcLis.Close()
		return nil, fmt.Errorf("listen health: %w", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	grpcServer := grpc.NewServer()
	grpcHealth := health.NewServer()
	grpcHealth.SetServingStatus("", healthpb.HealthCheckResponse_SERVING)
	healthpb.RegisterHealthServer(grpcServer, grpcHealth)
	protos.RegisterCloudProviderServer(grpcServer, provider.NewCloudProviderServer(
		svc,
		provider.WithNodeGroupForNodeResolver(provider.NodeGroupForNodeResolver(svc.nodeGroupForNodeResolver)),
		provider.WithNodeGroupInstancesLister(provider.NodeGroupInstancesLister(svc.nodeGroupInstancesLister)),
		provider.WithRefresher(svc.Refresh),
	))
	go func() {
		_ = grpcServer.Serve(grpcLis)
	}()

	healthSrv := &http.Server{Handler: mux}
	go func() {
		_ = healthSrv.Serve(healthLis)
	}()

	svc.grpcListener = grpcLis
	svc.grpcServer = grpcServer
	svc.grpcAddr = grpcLis.Addr().String()
	svc.healthServer = healthSrv
	svc.healthAddr = healthLis.Addr().String()
	return svc, nil
}

// GRPCAddr returns the bound gRPC listener address.
func (s *Service) GRPCAddr() string {
	return s.grpcAddr
}

// ClientsForRegion returns AWS clients for a configured region.
func (s *Service) ClientsForRegion(region string) (*awsclient.Clients, error) {
	return s.awsFactory.ForRegion(region)
}

// ClientsForNodeGroupID returns AWS clients for a nodegroup ID in region/asg-name format.
func (s *Service) ClientsForNodeGroupID(id string) (*awsclient.Clients, error) {
	return s.awsFactory.ForNodeGroupID(id)
}

// NodeGroupIDs returns discovered nodegroup IDs in region/asg-name format.
func (s *Service) NodeGroupIDs(ctx context.Context) ([]string, error) {
	return s.nodeGroupIDLister(ctx)
}

// Refresh rebuilds cache snapshot from AWS.
func (s *Service) Refresh(ctx context.Context) error {
	return s.cacheRefresher(ctx)
}

func mapInstanceState(in cache.InstanceState) protos.InstanceStatus_InstanceState {
	switch in {
	case cache.InstanceStateRunning:
		return protos.InstanceStatus_instanceRunning
	case cache.InstanceStateCreating:
		return protos.InstanceStatus_instanceCreating
	case cache.InstanceStateDeleting:
		return protos.InstanceStatus_instanceDeleting
	default:
		return protos.InstanceStatus_unspecified
	}
}

// HealthAddr returns the bound health listener address.
func (s *Service) HealthAddr() string {
	return s.healthAddr
}

// Stop gracefully shuts down server resources.
func (s *Service) Stop(ctx context.Context) error {
	var err error
	s.shutdownOnce.Do(func() {
		stopped := make(chan struct{})
		go func() {
			s.grpcServer.GracefulStop()
			close(stopped)
		}()
		select {
		case <-ctx.Done():
			s.grpcServer.Stop()
		case <-stopped:
		}

		if closeErr := s.healthServer.Shutdown(ctx); closeErr != nil && !errors.Is(closeErr, http.ErrServerClosed) {
			err = closeErr
		}
	})
	return err
}
