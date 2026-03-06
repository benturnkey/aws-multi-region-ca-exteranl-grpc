package server_test

import (
	"context"
	"errors"
	"io"
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
	"aws-multi-region-ca-exteranl-grpc/pkg/ec2info"
	"aws-multi-region-ca-exteranl-grpc/pkg/observability"
	"aws-multi-region-ca-exteranl-grpc/pkg/server"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/autoscaling"
	autoscalingtypes "github.com/aws/aws-sdk-go-v2/service/autoscaling/types"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
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

func (f *fakeRegionProvider) ForNodeGroupID(id string) (*awsclient.Clients, error) {
	region, _, err := awsclient.ParseNodeGroupID(id)
	if err != nil {
		return nil, err
	}
	return f.ForRegion(region)
}

type fakeASGPagesClient struct {
	pages []*autoscaling.DescribeAutoScalingGroupsOutput
	err   error
	calls int

	setDesiredCapacityCalls []*autoscaling.SetDesiredCapacityInput
	terminateInstanceCalls  []*autoscaling.TerminateInstanceInAutoScalingGroupInput
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

func (f *fakeASGPagesClient) SetDesiredCapacity(_ context.Context, in *autoscaling.SetDesiredCapacityInput, _ ...func(*autoscaling.Options)) (*autoscaling.SetDesiredCapacityOutput, error) {
	if f.err != nil {
		return nil, f.err
	}
	f.setDesiredCapacityCalls = append(f.setDesiredCapacityCalls, in)
	return &autoscaling.SetDesiredCapacityOutput{}, nil
}

func (f *fakeASGPagesClient) TerminateInstanceInAutoScalingGroup(_ context.Context, in *autoscaling.TerminateInstanceInAutoScalingGroupInput, _ ...func(*autoscaling.Options)) (*autoscaling.TerminateInstanceInAutoScalingGroupOutput, error) {
	if f.err != nil {
		return nil, f.err
	}
	f.terminateInstanceCalls = append(f.terminateInstanceCalls, in)
	return &autoscaling.TerminateInstanceInAutoScalingGroupOutput{}, nil
}

type fakeEC2PagesClient struct {
	describeLTVersionsOutput *ec2.DescribeLaunchTemplateVersionsOutput
	describeLTVersionsErr    error

	describeInstanceTypesOutput []*ec2.DescribeInstanceTypesOutput
	describeInstanceTypesErr    error
	describeInstanceTypesCalls  int
}

func (f *fakeEC2PagesClient) DescribeLaunchTemplateVersions(_ context.Context, _ *ec2.DescribeLaunchTemplateVersionsInput, _ ...func(*ec2.Options)) (*ec2.DescribeLaunchTemplateVersionsOutput, error) {
	if f.describeLTVersionsErr != nil {
		return nil, f.describeLTVersionsErr
	}
	return f.describeLTVersionsOutput, nil
}

func (f *fakeEC2PagesClient) DescribeInstanceTypes(_ context.Context, _ *ec2.DescribeInstanceTypesInput, _ ...func(*ec2.Options)) (*ec2.DescribeInstanceTypesOutput, error) {
	if f.describeInstanceTypesErr != nil {
		return nil, f.describeInstanceTypesErr
	}
	if f.describeInstanceTypesCalls >= len(f.describeInstanceTypesOutput) {
		return &ec2.DescribeInstanceTypesOutput{}, nil
	}
	out := f.describeInstanceTypesOutput[f.describeInstanceTypesCalls]
	f.describeInstanceTypesCalls++
	return out, nil
}

// newTestInstanceTypeResolver creates a pre-populated resolver for tests.
func newTestInstanceTypeResolver(instanceTypes ...ec2info.InstanceType) *ec2info.Resolver {
	r := ec2info.NewResolver()
	for _, it := range instanceTypes {
		r.Set(it.InstanceType, &it)
	}
	return r
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

	fakeProvider := &fakeRegionProvider{
		regions: []string{"us-east-1"},
		clients: map[string]*awsclient.Clients{
			"us-east-1": {
				AutoScaling: &fakeASGPagesClient{pages: []*autoscaling.DescribeAutoScalingGroupsOutput{{}}},
			},
		},
		errByRegion: map[string]error{},
	}

	svc, err := server.Start(cfg, server.WithSnapshotBuilder(discovery.NewASGSnapshotBuilder(fakeProvider, nil, []string{"asg-a"})), server.WithAWSClientProvider(fakeProvider))
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

	resp, reqErr := client.Get("http://" + svc.HealthAddr() + "/healthz")
	if reqErr != nil {
		t.Fatalf("GET /healthz: %v", reqErr)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /healthz: status=%d want=%d", resp.StatusCode, http.StatusOK)
	}

	resp, reqErr = client.Get("http://" + svc.HealthAddr() + "/readyz")
	if reqErr != nil {
		t.Fatalf("GET /readyz: %v", reqErr)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("GET /readyz: status=%d want=%d", resp.StatusCode, http.StatusServiceUnavailable)
	}

	err = svc.Refresh(context.Background())
	if err != nil {
		t.Fatalf("initial Refresh: %v", err)
	}

	resp, reqErr = client.Get("http://" + svc.HealthAddr() + "/readyz")
	if reqErr != nil {
		t.Fatalf("GET /readyz after refresh: %v", reqErr)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /readyz after refresh: status=%d want=%d", resp.StatusCode, http.StatusOK)
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

	resp, reqErr := client.Get("http://" + svc.HealthAddr() + "/healthz")
	if reqErr != nil {
		t.Fatalf("GET /healthz: %v", reqErr)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /healthz: status=%d want=%d", resp.StatusCode, http.StatusOK)
	}

	resp, reqErr = client.Get("http://" + svc.HealthAddr() + "/readyz")
	if reqErr != nil {
		t.Fatalf("GET /readyz: %v", reqErr)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("GET /readyz: status=%d want=%d", resp.StatusCode, http.StatusServiceUnavailable)
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

	svc, err := server.Start(cfg,
		server.WithSnapshotBuilder(discovery.NewASGSnapshotBuilder(fakeProvider, nil, []string{"asg-a"})),
		server.WithAWSClientProvider(fakeProvider),
		server.WithInstanceTypeResolver(ec2info.NewResolver()),
	)
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

func TestMetricsServerStartsOnSeparatePort(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	cfgYAML := `regions:
  - us-east-1
grpc:
  address: 127.0.0.1:0
health:
  address: 127.0.0.1:0
observability:
  metrics:
    address: 127.0.0.1:0
`
	if err := os.WriteFile(cfgPath, []byte(cfgYAML), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	otelRes, err := observability.SetupForTest()
	if err != nil {
		t.Fatalf("setup observability: %v", err)
	}

	metrics, err := observability.NewMetrics(otelRes.MeterProvider)
	if err != nil {
		t.Fatalf("create metrics: %v", err)
	}

	svc, err := server.Start(cfg,
		server.WithMetricsHandler(otelRes.MetricsHandler),
		server.WithMetrics(metrics),
	)
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

	if svc.MetricsAddr() == "" {
		t.Fatal("MetricsAddr() should not be empty when handler is provided")
	}
	if svc.MetricsAddr() == svc.HealthAddr() {
		t.Fatalf("metrics and health should be on different addresses: metrics=%s health=%s", svc.MetricsAddr(), svc.HealthAddr())
	}

	client := &http.Client{Timeout: 2 * time.Second}

	resp, err := client.Get("http://" + svc.MetricsAddr() + "/metrics")
	if err != nil {
		t.Fatalf("GET /metrics: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /metrics: status=%d want=%d", resp.StatusCode, http.StatusOK)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if !strings.Contains(string(body), "# HELP") {
		t.Fatalf("expected Prometheus output, got: %s", string(body))
	}
}

func TestMetricsServerNotStartedWithoutHandler(t *testing.T) {
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

	if svc.MetricsAddr() != "" {
		t.Fatalf("MetricsAddr() should be empty without handler, got %q", svc.MetricsAddr())
	}
}

func TestHealthEndpointDoesNotServeMetrics(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	cfgYAML := `regions:
  - us-east-1
grpc:
  address: 127.0.0.1:0
health:
  address: 127.0.0.1:0
observability:
  metrics:
    address: 127.0.0.1:0
`
	if err := os.WriteFile(cfgPath, []byte(cfgYAML), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	otelRes, err := observability.SetupForTest()
	if err != nil {
		t.Fatalf("setup observability: %v", err)
	}

	svc, err := server.Start(cfg, server.WithMetricsHandler(otelRes.MetricsHandler))
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

	resp, err := client.Get("http://" + svc.HealthAddr() + "/metrics")
	if err != nil {
		t.Fatalf("GET /metrics on health: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("GET /metrics on health: status=%d want=%d", resp.StatusCode, http.StatusNotFound)
	}
}

func TestMetricsEndpointContainsExpectedMetrics(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	cfgYAML := `regions:
  - us-east-1
grpc:
  address: 127.0.0.1:0
health:
  address: 127.0.0.1:0
observability:
  metrics:
    address: 127.0.0.1:0
`
	if err := os.WriteFile(cfgPath, []byte(cfgYAML), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	otelRes, err := observability.SetupForTest()
	if err != nil {
		t.Fatalf("setup observability: %v", err)
	}

	metrics, err := observability.NewMetrics(otelRes.MeterProvider)
	if err != nil {
		t.Fatalf("create metrics: %v", err)
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
								MinSize:              aws.Int32(1),
								MaxSize:              aws.Int32(10),
								DesiredCapacity:      aws.Int32(3),
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

	svc, err := server.Start(cfg,
		server.WithMetricsHandler(otelRes.MetricsHandler),
		server.WithMetrics(metrics),
		server.WithSnapshotBuilder(discovery.NewASGSnapshotBuilder(fakeProvider, nil, []string{"asg-a"})),
		server.WithAWSClientProvider(fakeProvider),
		server.WithNodeGroupIDLister(func(context.Context) ([]string, error) {
			return []string{"us-east-1/asg-a"}, nil
		}),
		server.WithGRPCServerOptions(
			grpc.ChainUnaryInterceptor(observability.GRPCUnaryServerInterceptor(metrics)),
		),
	)
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

	// Trigger a cache refresh to populate gauges
	ctx := context.Background()
	if err := svc.Refresh(ctx); err != nil {
		t.Fatalf("Refresh: %v", err)
	}

	// Make a gRPC call to generate grpc_request_duration_seconds
	conn, err := grpc.NewClient(svc.GRPCAddr(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("grpc dial: %v", err)
	}
	defer conn.Close()

	grpcClient := protos.NewCloudProviderClient(conn)
	if _, err := grpcClient.NodeGroups(ctx, &protos.NodeGroupsRequest{}); err != nil {
		t.Fatalf("NodeGroups RPC: %v", err)
	}

	// Fetch metrics
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get("http://" + svc.MetricsAddr() + "/metrics")
	if err != nil {
		t.Fatalf("GET /metrics: %v", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	metricsOutput := string(body)

	expectedMetrics := []string{
		"grpc_request_duration_seconds",
		"cache_refresh_duration_seconds",
		"nodegroups_total",
		"instances_total",
		"asg_min_size",
		"asg_max_size",
		"asg_current_size",
	}
	for _, name := range expectedMetrics {
		if !strings.Contains(metricsOutput, name) {
			t.Errorf("metrics output missing %q", name)
		}
	}
}

func TestMetricsServerShutdownCleanly(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	cfgYAML := `regions:
  - us-east-1
grpc:
  address: 127.0.0.1:0
health:
  address: 127.0.0.1:0
observability:
  metrics:
    address: 127.0.0.1:0
`
	if err := os.WriteFile(cfgPath, []byte(cfgYAML), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	otelRes, err := observability.SetupForTest()
	if err != nil {
		t.Fatalf("setup observability: %v", err)
	}

	svc, err := server.Start(cfg, server.WithMetricsHandler(otelRes.MetricsHandler))
	if err != nil {
		t.Fatalf("start server: %v", err)
	}

	metricsAddr := svc.MetricsAddr()
	if metricsAddr == "" {
		t.Fatal("expected metrics server to be running")
	}

	// Stop should shut down metrics server without error
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := svc.Stop(ctx); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	// Verify metrics endpoint is no longer reachable
	client := &http.Client{Timeout: 1 * time.Second}
	_, err = client.Get("http://" + metricsAddr + "/metrics")
	if err == nil {
		t.Fatal("expected connection error after shutdown, but request succeeded")
	}
}

func TestGRPCMutativeNodeGroupOperations(t *testing.T) {
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

	asgClient := &fakeASGPagesClient{pages: []*autoscaling.DescribeAutoScalingGroupsOutput{
		{
			AutoScalingGroups: []autoscalingtypes.AutoScalingGroup{
				{
					AutoScalingGroupName: aws.String("asg-a"),
					DesiredCapacity:      aws.Int32(2),
					AvailabilityZones:    []string{"us-east-1a"},
					LaunchTemplate: &autoscalingtypes.LaunchTemplateSpecification{
						LaunchTemplateName: aws.String("my-lt"),
						Version:            aws.String("1"),
					},
					Instances: []autoscalingtypes.Instance{
						{InstanceId: aws.String("i-123"), AvailabilityZone: aws.String("us-east-1a"), LifecycleState: autoscalingtypes.LifecycleStateInService},
						{InstanceId: aws.String("i-456"), AvailabilityZone: aws.String("us-east-1a"), LifecycleState: autoscalingtypes.LifecycleStateInService},
					},
				},
			},
		},
	}}

	ec2Client := &fakeEC2PagesClient{
		describeLTVersionsOutput: &ec2.DescribeLaunchTemplateVersionsOutput{
			LaunchTemplateVersions: []ec2types.LaunchTemplateVersion{
				{
					LaunchTemplateData: &ec2types.ResponseLaunchTemplateData{
						InstanceType: ec2types.InstanceTypeM5Xlarge,
					},
				},
			},
		},
	}

	fakeProvider := &fakeRegionProvider{
		regions: []string{"us-east-1"},
		clients: map[string]*awsclient.Clients{
			"us-east-1": {
				AutoScaling: asgClient,
				EC2:         ec2Client,
			},
		},
		errByRegion: map[string]error{},
	}

	resolver := newTestInstanceTypeResolver(ec2info.InstanceType{
		InstanceType: "m5.xlarge",
		VCPU:         4,
		MemoryMb:     16384,
		Architecture: "amd64",
	})

	svc, err := server.Start(cfg,
		server.WithSnapshotBuilder(discovery.NewASGSnapshotBuilder(fakeProvider, nil, []string{"asg-a"})),
		server.WithAWSClientProvider(fakeProvider),
		server.WithInstanceTypeResolver(resolver),
	)
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

	conn, err := grpc.NewClient(svc.GRPCAddr(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("grpc dial: %v", err)
	}
	defer conn.Close()

	client := protos.NewCloudProviderClient(conn)
	if _, err := client.Refresh(ctx, &protos.RefreshRequest{}); err != nil {
		t.Fatalf("Refresh RPC error: %v", err)
	}

	targetSizeResp, err := client.NodeGroupTargetSize(ctx, &protos.NodeGroupTargetSizeRequest{Id: "us-east-1/asg-a"})
	if err != nil {
		t.Fatalf("TargetSize RPC error: %v", err)
	}
	if targetSizeResp.GetTargetSize() != 2 {
		t.Fatalf("TargetSize=%d want=2", targetSizeResp.GetTargetSize())
	}

	_, err = client.NodeGroupIncreaseSize(ctx, &protos.NodeGroupIncreaseSizeRequest{Id: "us-east-1/asg-a", Delta: 2})
	if err != nil {
		t.Fatalf("IncreaseSize RPC error: %v", err)
	}
	if len(asgClient.setDesiredCapacityCalls) != 1 {
		t.Fatalf("SetDesiredCapacity calls=%d want=1", len(asgClient.setDesiredCapacityCalls))
	}
	if *asgClient.setDesiredCapacityCalls[0].DesiredCapacity != 4 {
		t.Fatalf("SetDesiredCapacity DesiredCapacity=%d want=4", *asgClient.setDesiredCapacityCalls[0].DesiredCapacity)
	}

	_, err = client.NodeGroupDecreaseTargetSize(ctx, &protos.NodeGroupDecreaseTargetSizeRequest{Id: "us-east-1/asg-a", Delta: -1})
	if err != nil {
		t.Fatalf("DecreaseTargetSize RPC error: %v", err)
	}
	if len(asgClient.setDesiredCapacityCalls) != 2 {
		t.Fatalf("SetDesiredCapacity calls=%d want=2", len(asgClient.setDesiredCapacityCalls))
	}
	if *asgClient.setDesiredCapacityCalls[1].DesiredCapacity != 1 {
		t.Fatalf("SetDesiredCapacity DesiredCapacity=%d want=1", *asgClient.setDesiredCapacityCalls[1].DesiredCapacity)
	}

	_, err = client.NodeGroupDeleteNodes(ctx, &protos.NodeGroupDeleteNodesRequest{
		Id: "us-east-1/asg-a",
		Nodes: []*protos.ExternalGrpcNode{
			{ProviderID: "aws:///us-east-1a/i-456", Name: "node-2"},
		},
	})
	if err != nil {
		t.Fatalf("DeleteNodes RPC error: %v", err)
	}
	if len(asgClient.terminateInstanceCalls) != 1 {
		t.Fatalf("TerminateInstance calls=%d want=1", len(asgClient.terminateInstanceCalls))
	}
	if *asgClient.terminateInstanceCalls[0].InstanceId != "i-456" {
		t.Fatalf("TerminateInstance InstanceId=%s want=i-456", *asgClient.terminateInstanceCalls[0].InstanceId)
	}
	if !*asgClient.terminateInstanceCalls[0].ShouldDecrementDesiredCapacity {
		t.Fatalf("TerminateInstance ShouldDecrementDesiredCapacity=false want=true")
	}

	_, err = client.NodeGroupTemplateNodeInfo(ctx, &protos.NodeGroupTemplateNodeInfoRequest{Id: "us-east-1/asg-a"})
	if err != nil {
		t.Fatalf("TemplateNodeInfo RPC error: %v", err)
	}

	_, err = client.NodeGroupGetOptions(ctx, &protos.NodeGroupAutoscalingOptionsRequest{Id: "us-east-1/asg-a"})
	if err != nil {
		t.Fatalf("NodeGroupGetOptions RPC error: %v", err)
	}
}
