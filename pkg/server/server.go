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
	"aws-multi-region-ca-exteranl-grpc/pkg/observability"
	"aws-multi-region-ca-exteranl-grpc/pkg/provider"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/autoscaling"
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

// StartOption customizes server startup dependencies.
type StartOption func(*startOptions)

// AWSClientProvider Abstracts the client factory for testing.
type AWSClientProvider interface {
	ForRegion(region string) (*awsclient.Clients, error)
	ForNodeGroupID(id string) (*awsclient.Clients, error)
}

type startOptions struct {
	nodeGroupIDLister        NodeGroupIDListerFunc
	nodeGroupForNodeResolver NodeGroupForNodeResolverFunc
	nodeGroupInstancesLister NodeGroupInstancesListerFunc
	cacheRefresher           CacheRefresherFunc
	snapshotBuilder          discovery.SnapshotBuilder
	awsClientProvider        AWSClientProvider
	metricsHandler           http.Handler
	grpcServerOptions        []grpc.ServerOption
	metrics                  *observability.Metrics
}

// WithAWSClientProvider overrides aws factory logic.
func WithAWSClientProvider(rp AWSClientProvider) StartOption {
	return func(o *startOptions) {
		o.awsClientProvider = rp
	}
}

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

// WithMetricsHandler registers an HTTP handler on the health mux at /metrics.
func WithMetricsHandler(h http.Handler) StartOption {
	return func(o *startOptions) {
		o.metricsHandler = h
	}
}

// WithGRPCServerOptions appends gRPC server options (e.g. interceptors).
func WithGRPCServerOptions(sopts ...grpc.ServerOption) StartOption {
	return func(o *startOptions) {
		o.grpcServerOptions = append(o.grpcServerOptions, sopts...)
	}
}

// WithMetrics enables observability instrumentation of AWS clients and cache refresh.
func WithMetrics(m *observability.Metrics) StartOption {
	return func(o *startOptions) {
		o.metrics = m
	}
}

// Service owns runtime listeners and HTTP health endpoints.
type Service struct {
	awsFactory               AWSClientProvider
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

	metricsServer *http.Server
	metricsAddr   string

	shutdownOnce sync.Once
}

// Start initializes listeners and starts serving health/readiness endpoints.
func Start(cfg config.Config, opts ...StartOption) (*Service, error) {
	if len(cfg.Regions) == 0 {
		return nil, errors.New("start: at least one region is required")
	}

	factory, err := awsclient.NewFactory(context.Background(), cfg)
	if err != nil {
		return nil, fmt.Errorf("initialize aws clients: %w", err)
	}

	startCfg := &startOptions{}
	for _, opt := range opts {
		opt(startCfg)
	}

	rp := startCfg.awsClientProvider
	if rp == nil {
		rp = AWSClientProvider(factory)
	}
	if startCfg.metrics != nil {
		rp = observability.NewInstrumentedClientProvider(rp, startCfg.metrics)
	}

	svc := &Service{
		awsFactory: rp,
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
			var nodeGroupNames []string
			for _, ng := range cfg.NodeGroups {
				nodeGroupNames = append(nodeGroupNames, ng.Name)
			}
			svc.snapshotBuilder = discovery.NewASGSnapshotBuilder(factory, cfg.Discovery.Tags, nodeGroupNames)
		}
		baseRefresh := func(ctx context.Context) error {
			snap, err := svc.snapshotBuilder.Build(ctx)
			if err != nil {
				return err
			}
			svc.nodeCache.Replace(snap)
			return nil
		}
		if startCfg.metrics != nil {
			svc.cacheRefresher = func(ctx context.Context) error {
				return observability.RecordCacheRefresh(ctx, startCfg.metrics, baseRefresh, svc.nodeCache)
			}
		} else {
			svc.cacheRefresher = baseRefresh
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
		if !svc.nodeCache.IsInitialized() {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte("cache not initialized"))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	grpcServer := grpc.NewServer(startCfg.grpcServerOptions...)
	grpcHealth := health.NewServer()
	grpcHealth.SetServingStatus("", healthpb.HealthCheckResponse_SERVING)
	healthpb.RegisterHealthServer(grpcServer, grpcHealth)
	protos.RegisterCloudProviderServer(grpcServer, provider.NewCloudProviderServer(
		svc,
		provider.WithNodeGroupForNodeResolver(provider.NodeGroupForNodeResolver(svc.nodeGroupForNodeResolver)),
		provider.WithNodeGroupInstancesLister(provider.NodeGroupInstancesLister(svc.nodeGroupInstancesLister)),
		provider.WithRefresher(svc.Refresh),
		provider.WithNodeGroupManager(svc),
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

	if startCfg.metricsHandler != nil && cfg.Observability.Metrics.Address != "" {
		metricsLis, err := net.Listen("tcp", cfg.Observability.Metrics.Address)
		if err != nil {
			_ = grpcLis.Close()
			_ = healthSrv.Close()
			return nil, fmt.Errorf("listen metrics: %w", err)
		}
		metricsMux := http.NewServeMux()
		metricsMux.Handle("/metrics", startCfg.metricsHandler)
		metricsSrv := &http.Server{Handler: metricsMux}
		go func() {
			_ = metricsSrv.Serve(metricsLis)
		}()
		svc.metricsServer = metricsSrv
		svc.metricsAddr = metricsLis.Addr().String()
	}

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

// TargetSize returns the target size from cache.
func (s *Service) TargetSize(ctx context.Context, id string) (int, error) {
	ng, ok := s.nodeCache.NodeGroup(id)
	if !ok {
		return 0, fmt.Errorf("nodegroup %q not found", id)
	}
	return ng.TargetSize, nil
}

// IncreaseSize calls AWS API to increase capacity.
func (s *Service) IncreaseSize(ctx context.Context, id string, delta int) error {
	return s.modifySize(ctx, id, delta)
}

// DecreaseTargetSize calls AWS API to decrease capacity.
func (s *Service) DecreaseTargetSize(ctx context.Context, id string, delta int) error {
	return s.modifySize(ctx, id, delta) // delta is already negative
}

func (s *Service) modifySize(ctx context.Context, id string, delta int) error {
	ng, ok := s.nodeCache.NodeGroup(id)
	if !ok {
		return fmt.Errorf("nodegroup %q not found", id)
	}
	clients, err := s.ClientsForNodeGroupID(id)
	if err != nil {
		return err
	}
	_, name, err := awsclient.ParseNodeGroupID(id)
	if err != nil {
		return err
	}
	newSize := int32(ng.TargetSize + delta)
	_, err = clients.AutoScaling.SetDesiredCapacity(ctx, &autoscaling.SetDesiredCapacityInput{
		AutoScalingGroupName: aws.String(name),
		DesiredCapacity:      aws.Int32(newSize),
	})
	return err
}

// DeleteNodes calls AWS API to terminate specific instances.
func (s *Service) DeleteNodes(ctx context.Context, id string, nodes []*protos.ExternalGrpcNode) error {
	clients, err := s.ClientsForNodeGroupID(id)
	if err != nil {
		return err
	}
	for _, node := range nodes {
		// Try to extract providerID
		providerID := node.GetProviderID()
		if providerID == "" {
			continue
		}
		// aws:///us-east-1a/i-123... -> i-123
		instanceID := s.extractLocalInstanceID(providerID)
		if instanceID == "" {
			continue
		}
		_, err := clients.AutoScaling.TerminateInstanceInAutoScalingGroup(ctx, &autoscaling.TerminateInstanceInAutoScalingGroupInput{
			InstanceId:                     aws.String(instanceID),
			ShouldDecrementDesiredCapacity: aws.Bool(true),
		})
		if err != nil {
			return fmt.Errorf("terminate %q: %w", instanceID, err)
		}
	}
	return nil
}

func (s *Service) extractLocalInstanceID(providerID string) string {
	for i := len(providerID) - 1; i >= 0; i-- {
		if providerID[i] == '/' {
			if i == len(providerID)-1 {
				return ""
			}
			return providerID[i+1:]
		}
	}
	return providerID
}

// TemplateNodeInfo returns the template node info.
func (s *Service) TemplateNodeInfo(ctx context.Context, id string) ([]byte, error) {
	// Not fully implemented: stub returning empty JSON to satisfy interface
	return []byte("{}"), nil
}

// GetOptions returns the autoscaling options.
func (s *Service) GetOptions(ctx context.Context, id string) (*protos.NodeGroupAutoscalingOptions, error) {
	// Not fully implemented: stub
	return nil, nil
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

// MetricsAddr returns the bound metrics listener address.
func (s *Service) MetricsAddr() string {
	return s.metricsAddr
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
		if s.metricsServer != nil {
			if closeErr := s.metricsServer.Shutdown(ctx); closeErr != nil && !errors.Is(closeErr, http.ErrServerClosed) {
				err = closeErr
			}
		}
	})
	return err
}
