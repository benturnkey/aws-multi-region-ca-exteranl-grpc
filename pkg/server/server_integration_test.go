package server_test

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"aws-multi-region-ca-exteranl-grpc/pkg/awsclient"
	"aws-multi-region-ca-exteranl-grpc/pkg/config"
	"aws-multi-region-ca-exteranl-grpc/pkg/discovery"
	"aws-multi-region-ca-exteranl-grpc/pkg/server"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/autoscaling"
	autoscalingtypes "github.com/aws/aws-sdk-go-v2/service/autoscaling/types"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"k8s.io/autoscaler/cluster-autoscaler/cloudprovider/externalgrpc/protos"
)

type fakeRegionProvider struct {
	regions     []string
	clients     map[string]*awsclient.Clients
	errByRegion map[string]error
}

func (f *fakeRegionProvider) Regions() []string { return f.regions }

func (f *fakeRegionProvider) ForRegion(region string) (*awsclient.Clients, error) {
	if err, ok := f.errByRegion[region]; ok {
		return nil, err
	}
	c, ok := f.clients[region]
	if !ok {
		return nil, errors.New("region not found")
	}
	return c, nil
}

type fakeASGPagesClient struct {
	pages []*autoscaling.DescribeAutoScalingGroupsOutput
	err   error
	calls int
}

func (f *fakeASGPagesClient) DescribeAutoScalingGroups(_ context.Context, _ *autoscaling.DescribeAutoScalingGroupsInput, _ ...func(*autoscaling.Options)) (*autoscaling.DescribeAutoScalingGroupsOutput, error) {
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

func TestLoadConfigAndServeHealthReadiness(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	cfgYAML := `regions:
  - us-east-1
grpc:
  address: 127.0.0.1:0
health:
  address: 127.0.0.1:0
`
	if err := os.WriteFile(cfgPath, []byte(cfgYAML), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	svc, err := server.Start(cfg)
	if err != nil {
		t.Fatalf("start server: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if stopErr := svc.Stop(ctx); stopErr != nil {
			t.Fatalf("stop server: %v", stopErr)
		}
	})

	client := &http.Client{Timeout: 2 * time.Second}
	for _, path := range []string{"/healthz", "/readyz"} {
		resp, reqErr := client.Get("http://" + svc.HealthAddr() + path)
		if reqErr != nil {
			t.Fatalf("GET %s: %v", path, reqErr)
		}
		resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET %s: status=%d want=%d", path, resp.StatusCode, http.StatusOK)
		}
	}
}

func TestStartServerServesGRPCHealth(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	cfgYAML := `regions:
  - us-east-1
grpc:
  address: 127.0.0.1:0
health:
  address: 127.0.0.1:0
`
	if err := os.WriteFile(cfgPath, []byte(cfgYAML), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	svc, err := server.Start(cfg)
	if err != nil {
		t.Fatalf("start server: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if stopErr := svc.Stop(ctx); stopErr != nil {
			t.Fatalf("stop server: %v", stopErr)
		}
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	conn, err := grpc.NewClient(
		svc.GRPCAddr(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("grpc dial: %v", err)
	}
	defer conn.Close()

	healthClient := healthpb.NewHealthClient(conn)
	resp, err := healthClient.Check(ctx, &healthpb.HealthCheckRequest{})
	if err != nil {
		t.Fatalf("grpc health check: %v", err)
	}
	if resp.GetStatus() != healthpb.HealthCheckResponse_SERVING {
		t.Fatalf("health status=%s want=%s", resp.GetStatus(), healthpb.HealthCheckResponse_SERVING)
	}
}

func TestStartServerWithDefaultConfigValues(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	cfgYAML := `regions:
  - us-east-1
`
	if err := os.WriteFile(cfgPath, []byte(cfgYAML), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	svc, err := server.Start(cfg)
	if err != nil {
		t.Fatalf("start server with defaults: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if stopErr := svc.Stop(ctx); stopErr != nil {
			t.Fatalf("stop server: %v", stopErr)
		}
	})

	client := &http.Client{Timeout: 2 * time.Second}
	for _, path := range []string{"/healthz", "/readyz"} {
		resp, reqErr := client.Get("http://" + svc.HealthAddr() + path)
		if reqErr != nil {
			t.Fatalf("GET %s: %v", path, reqErr)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET %s: status=%d want=%d", path, resp.StatusCode, http.StatusOK)
		}
	}
}

func TestStartServerFailsFastWithoutRegions(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	cfgYAML := `{}`
	if err := os.WriteFile(cfgPath, []byte(cfgYAML), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	_, err = server.Start(cfg)
	if err == nil {
		t.Fatalf("expected startup error when regions is empty")
	}
	if !strings.Contains(err.Error(), "at least one region is required") {
		t.Fatalf("error=%q want substring %q", err.Error(), "at least one region is required")
	}
}

func TestServiceRoutesAWSClientsByNodeGroupID(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	cfgYAML := `regions:
  - us-east-1
  - us-west-2
grpc:
  address: 127.0.0.1:0
health:
  address: 127.0.0.1:0
`
	if err := os.WriteFile(cfgPath, []byte(cfgYAML), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	svc, err := server.Start(cfg)
	if err != nil {
		t.Fatalf("start server: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if stopErr := svc.Stop(ctx); stopErr != nil {
			t.Fatalf("stop server: %v", stopErr)
		}
	})

	if _, err := svc.ClientsForRegion("us-east-1"); err != nil {
		t.Fatalf("ClientsForRegion(us-east-1): %v", err)
	}
	if _, err := svc.ClientsForNodeGroupID("us-west-2/my-asg"); err != nil {
		t.Fatalf("ClientsForNodeGroupID(us-west-2/my-asg): %v", err)
	}
	if _, err := svc.ClientsForNodeGroupID("eu-central-1/my-asg"); err == nil {
		t.Fatalf("expected error for unknown region")
	}
	if _, err := svc.ClientsForNodeGroupID("bad-format-id"); err == nil {
		t.Fatalf("expected error for bad nodegroup id format")
	}
}

func TestGRPCNodeGroupsReturnsDiscoveredIDs(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	cfgYAML := `regions:
  - us-east-1
grpc:
  address: 127.0.0.1:0
health:
  address: 127.0.0.1:0
`
	if err := os.WriteFile(cfgPath, []byte(cfgYAML), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	wantIDs := []string{"us-east-1/asg-a", "us-west-2/asg-b"}
	svc, err := server.Start(cfg, server.WithNodeGroupIDLister(func(context.Context) ([]string, error) {
		return wantIDs, nil
	}))
	if err != nil {
		t.Fatalf("start server: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if stopErr := svc.Stop(ctx); stopErr != nil {
			t.Fatalf("stop server: %v", stopErr)
		}
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	conn, err := grpc.NewClient(
		svc.GRPCAddr(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("grpc dial: %v", err)
	}
	defer conn.Close()

	client := protos.NewCloudProviderClient(conn)
	resp, err := client.NodeGroups(ctx, &protos.NodeGroupsRequest{})
	if err != nil {
		t.Fatalf("NodeGroups RPC error: %v", err)
	}

	gotIDs := make([]string, 0, len(resp.GetNodeGroups()))
	for _, ng := range resp.GetNodeGroups() {
		gotIDs = append(gotIDs, ng.GetId())
	}
	if !reflect.DeepEqual(gotIDs, wantIDs) {
		t.Fatalf("nodegroup IDs=%v want=%v", gotIDs, wantIDs)
	}
}

func TestGRPCNodeGroupForNodeReturnsResolvedGroup(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	cfgYAML := `regions:
  - us-east-1
grpc:
  address: 127.0.0.1:0
health:
  address: 127.0.0.1:0
`
	if err := os.WriteFile(cfgPath, []byte(cfgYAML), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	svc, err := server.Start(cfg, server.WithNodeGroupForNodeResolver(func(context.Context, *protos.ExternalGrpcNode) (string, error) {
		return "us-east-1/asg-a", nil
	}))
	if err != nil {
		t.Fatalf("start server: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if stopErr := svc.Stop(ctx); stopErr != nil {
			t.Fatalf("stop server: %v", stopErr)
		}
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	conn, err := grpc.NewClient(
		svc.GRPCAddr(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("grpc dial: %v", err)
	}
	defer conn.Close()

	client := protos.NewCloudProviderClient(conn)
	resp, err := client.NodeGroupForNode(ctx, &protos.NodeGroupForNodeRequest{
		Node: &protos.ExternalGrpcNode{ProviderID: "aws:///i-123", Name: "node-a"},
	})
	if err != nil {
		t.Fatalf("NodeGroupForNode RPC error: %v", err)
	}
	if got := resp.GetNodeGroup().GetId(); got != "us-east-1/asg-a" {
		t.Fatalf("nodegroup id=%q", got)
	}
}

func TestGRPCNodeGroupNodesReturnsInstances(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	cfgYAML := `regions:
  - us-east-1
grpc:
  address: 127.0.0.1:0
health:
  address: 127.0.0.1:0
`
	if err := os.WriteFile(cfgPath, []byte(cfgYAML), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	svc, err := server.Start(cfg, server.WithNodeGroupInstancesLister(func(context.Context, string) ([]*protos.Instance, error) {
		return []*protos.Instance{
			{Id: "aws:///us-east-1a/i-123"},
			{Id: "aws:///us-east-1a/i-456"},
		}, nil
	}))
	if err != nil {
		t.Fatalf("start server: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if stopErr := svc.Stop(ctx); stopErr != nil {
			t.Fatalf("stop server: %v", stopErr)
		}
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	conn, err := grpc.NewClient(
		svc.GRPCAddr(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("grpc dial: %v", err)
	}
	defer conn.Close()

	client := protos.NewCloudProviderClient(conn)
	resp, err := client.NodeGroupNodes(ctx, &protos.NodeGroupNodesRequest{Id: "us-east-1/asg-a"})
	if err != nil {
		t.Fatalf("NodeGroupNodes RPC error: %v", err)
	}
	if len(resp.GetInstances()) != 2 {
		t.Fatalf("instances=%d want=2", len(resp.GetInstances()))
	}
}

func TestGRPCRefreshInvokesCacheRefresher(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	cfgYAML := `regions:
  - us-east-1
grpc:
  address: 127.0.0.1:0
health:
  address: 127.0.0.1:0
`
	if err := os.WriteFile(cfgPath, []byte(cfgYAML), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	refreshed := false
	svc, err := server.Start(cfg, server.WithCacheRefresher(func(context.Context) error {
		refreshed = true
		return nil
	}))
	if err != nil {
		t.Fatalf("start server: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if stopErr := svc.Stop(ctx); stopErr != nil {
			t.Fatalf("stop server: %v", stopErr)
		}
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	conn, err := grpc.NewClient(
		svc.GRPCAddr(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("grpc dial: %v", err)
	}
	defer conn.Close()

	client := protos.NewCloudProviderClient(conn)
	if _, err := client.Refresh(ctx, &protos.RefreshRequest{}); err != nil {
		t.Fatalf("Refresh RPC error: %v", err)
	}
	if !refreshed {
		t.Fatalf("expected refresher to be called")
	}
}

func TestGRPCRefreshPopulatesCacheBackedNodeRPCs(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	cfgYAML := `regions:
  - us-east-1
grpc:
  address: 127.0.0.1:0
health:
  address: 127.0.0.1:0
`
	if err := os.WriteFile(cfgPath, []byte(cfgYAML), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	fakeProvider := &fakeRegionProvider{
		regions: []string{"us-east-1"},
		clients: map[string]*awsclient.Clients{
			"us-east-1": {
				AutoScaling: &fakeASGPagesClient{pages: []*autoscaling.DescribeAutoScalingGroupsOutput{
					{
						AutoScalingGroups: []autoscalingtypes.AutoScalingGroup{
							{
								AutoScalingGroupName: aws.String("asg-a"),
								Instances: []autoscalingtypes.Instance{
									{
										InstanceId:       aws.String("i-123"),
										AvailabilityZone: aws.String("us-east-1a"),
										LifecycleState:   autoscalingtypes.LifecycleStateInService,
									},
								},
							},
						},
					},
				}},
			},
		},
		errByRegion: map[string]error{},
	}

	svc, err := server.Start(cfg, server.WithSnapshotBuilder(discovery.NewASGSnapshotBuilder(fakeProvider)))
	if err != nil {
		t.Fatalf("start server: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if stopErr := svc.Stop(ctx); stopErr != nil {
			t.Fatalf("stop server: %v", stopErr)
		}
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	conn, err := grpc.NewClient(
		svc.GRPCAddr(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("grpc dial: %v", err)
	}
	defer conn.Close()

	client := protos.NewCloudProviderClient(conn)
	if _, err := client.Refresh(ctx, &protos.RefreshRequest{}); err != nil {
		t.Fatalf("Refresh RPC error: %v", err)
	}

	ngResp, err := client.NodeGroupForNode(ctx, &protos.NodeGroupForNodeRequest{
		Node: &protos.ExternalGrpcNode{ProviderID: "aws:///us-east-1a/i-123", Name: "ip-10-0-0-1"},
	})
	if err != nil {
		t.Fatalf("NodeGroupForNode RPC error: %v", err)
	}
	if got := ngResp.GetNodeGroup().GetId(); got != "us-east-1/asg-a" {
		t.Fatalf("nodegroup id=%q want=%q", got, "us-east-1/asg-a")
	}

	nodesResp, err := client.NodeGroupNodes(ctx, &protos.NodeGroupNodesRequest{Id: "us-east-1/asg-a"})
	if err != nil {
		t.Fatalf("NodeGroupNodes RPC error: %v", err)
	}
	if len(nodesResp.GetInstances()) != 1 {
		t.Fatalf("instances=%d want=1", len(nodesResp.GetInstances()))
	}
	if nodesResp.GetInstances()[0].GetId() != "aws:///us-east-1a/i-123" {
		t.Fatalf("instance id=%q", nodesResp.GetInstances()[0].GetId())
	}
}
