//go:build integration

package server_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"aws-multi-region-ca-exteranl-grpc/pkg/awsclient"
	"aws-multi-region-ca-exteranl-grpc/pkg/config"
	"aws-multi-region-ca-exteranl-grpc/pkg/discovery"
	"aws-multi-region-ca-exteranl-grpc/pkg/ec2info"
	"aws-multi-region-ca-exteranl-grpc/pkg/server"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/autoscaling"
	autoscalingtypes "github.com/aws/aws-sdk-go-v2/service/autoscaling/types"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"google.golang.org/grpc"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
)

const (
	caIntegrationEnvVar    = "RUN_CA_EXTERNALGRPC_INTEGRATION_TEST"
	caBinaryPathEnvVar     = "CA_EXTERNALGRPC_BINARY_PATH"
	caBinaryDownloadURLVar = "CA_EXTERNALGRPC_BINARY_URL"
	defaultCABinaryVersion = "1.35.0"
)

type methodCallTracker struct {
	mu     sync.Mutex
	counts map[string]int
}

func newMethodCallTracker() *methodCallTracker {
	return &methodCallTracker{
		counts: map[string]int{},
	}
}

func (m *methodCallTracker) Interceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		m.mu.Lock()
		m.counts[info.FullMethod]++
		m.mu.Unlock()
		return handler(ctx, req)
	}
}

func (m *methodCallTracker) Count(method string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.counts[method]
}

func TestClusterAutoscalerExternalGRPCScaleUp(t *testing.T) {
	if os.Getenv(caIntegrationEnvVar) != "1" {
		t.Skipf("set %s=1 to run integration test", caIntegrationEnvVar)
	}
	if os.Getenv("KUBEBUILDER_ASSETS") == "" {
		t.Skip("KUBEBUILDER_ASSETS must be set for envtest")
	}

	testEnv := &envtest.Environment{}
	restCfg, err := testEnv.Start()
	if err != nil {
		t.Fatalf("start envtest: %v", err)
	}
	t.Cleanup(func() {
		if stopErr := testEnv.Stop(); stopErr != nil {
			t.Fatalf("stop envtest: %v", stopErr)
		}
	})

	kubeClient, err := kubernetes.NewForConfig(restCfg)
	if err != nil {
		t.Fatalf("create kube client: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	t.Cleanup(cancel)

	if err := seedClusterState(ctx, kubeClient); err != nil {
		t.Fatalf("seed cluster state: %v", err)
	}

	asgOutput := &autoscaling.DescribeAutoScalingGroupsOutput{
		AutoScalingGroups: []autoscalingtypes.AutoScalingGroup{
			{
				AutoScalingGroupName: aws.String("asg-a"),
				MinSize:              aws.Int32(1),
				MaxSize:              aws.Int32(5),
				DesiredCapacity:      aws.Int32(1),
				AvailabilityZones:    []string{"us-east-1a"},
				LaunchTemplate: &autoscalingtypes.LaunchTemplateSpecification{
					LaunchTemplateName: aws.String("my-lt"),
					Version:            aws.String("1"),
				},
				Instances: []autoscalingtypes.Instance{
					{
						InstanceId:       aws.String("i-123"),
						AvailabilityZone: aws.String("us-east-1a"),
						LifecycleState:   autoscalingtypes.LifecycleStateInService,
					},
				},
			},
		},
	}

	fakeASG := &fakeASGPagesClient{pages: repeatASGOutput(asgOutput, 500)}
	fakeEC2 := &fakeEC2PagesClient{
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
				AutoScaling: fakeASG,
				EC2:         fakeEC2,
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

	cfg := mustLoadTestServerConfig(t)
	methods := newMethodCallTracker()

	svc, err := server.Start(cfg,
		server.WithSnapshotBuilder(discovery.NewASGSnapshotBuilder(fakeProvider, nil, []string{"asg-a"})),
		server.WithAWSClientProvider(fakeProvider),
		server.WithInstanceTypeResolver(resolver),
		server.WithNodeGroupIDLister(func(context.Context) ([]string, error) {
			return []string{"us-east-1/asg-a"}, nil
		}),
		server.WithGRPCServerOptions(grpc.UnaryInterceptor(methods.Interceptor())),
	)
	if err != nil {
		t.Fatalf("start externalgrpc service: %v", err)
	}
	t.Cleanup(func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer stopCancel()
		if stopErr := svc.Stop(stopCtx); stopErr != nil {
			t.Fatalf("stop service: %v", stopErr)
		}
	})

	if err := svc.Refresh(ctx); err != nil {
		t.Fatalf("pre-refresh service cache: %v", err)
	}

	kubeconfigPath, err := writeKubeconfigFile(t.TempDir(), restCfg)
	if err != nil {
		t.Fatalf("write kubeconfig: %v", err)
	}

	cloudConfigPath := filepath.Join(t.TempDir(), "ca-cloud-config.yaml")
	cloudConfig := fmt.Sprintf("address: %q\ngrpc_timeout: 3s\n", svc.GRPCAddr())
	if err := os.WriteFile(cloudConfigPath, []byte(cloudConfig), 0o600); err != nil {
		t.Fatalf("write cloud config: %v", err)
	}

	caBinaryPath := resolveClusterAutoscalerBinary(t)

	cmdCtx, cmdCancel := context.WithCancel(context.Background())
	t.Cleanup(cmdCancel)

	cmd := exec.CommandContext(cmdCtx, caBinaryPath,
		"--cloud-provider=externalgrpc",
		"--cloud-config="+cloudConfigPath,
		"--kubeconfig="+kubeconfigPath,
		"--scan-interval=1s",
		"--scale-down-enabled=false",
		"--skip-nodes-with-system-pods=false",
		"--v=5",
	)
	var caLogs bytes.Buffer
	cmd.Stdout = &caLogs
	cmd.Stderr = &caLogs

	if err := cmd.Start(); err != nil {
		t.Fatalf("start cluster-autoscaler: %v", err)
	}
	t.Cleanup(func() {
		cmdCancel()
		_ = cmd.Wait()
	})

	err = waitForCondition(90*time.Second, func() (bool, error) {
		if methods.Count("/protos.CloudProvider/NodeGroups") == 0 {
			return false, nil
		}
		if methods.Count("/protos.CloudProvider/Refresh") == 0 {
			return false, nil
		}
		if methods.Count("/protos.CloudProvider/NodeGroupIncreaseSize") == 0 {
			return false, nil
		}
		if len(fakeASG.setDesiredCapacityCalls) == 0 {
			return false, nil
		}
		return true, nil
	})
	if err != nil {
		t.Fatalf("waiting for scale-up RPCs: %v\ncluster-autoscaler logs:\n%s", err, caLogs.String())
	}

	if len(fakeASG.setDesiredCapacityCalls) == 0 {
		t.Fatalf("expected at least one SetDesiredCapacity call\ncluster-autoscaler logs:\n%s", caLogs.String())
	}
	lastCall := fakeASG.setDesiredCapacityCalls[len(fakeASG.setDesiredCapacityCalls)-1]
	if aws.ToInt32(lastCall.DesiredCapacity) < 2 {
		t.Fatalf("unexpected DesiredCapacity=%d, expected >=2\ncluster-autoscaler logs:\n%s", aws.ToInt32(lastCall.DesiredCapacity), caLogs.String())
	}
}

func seedClusterState(ctx context.Context, kubeClient kubernetes.Interface) error {
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "node-a",
			Labels: map[string]string{
				"kubernetes.io/os":   "linux",
				"kubernetes.io/arch": "amd64",
			},
		},
		Spec: corev1.NodeSpec{
			ProviderID: "aws:///us-east-1a/i-123",
		},
	}
	createdNode, err := kubeClient.CoreV1().Nodes().Create(ctx, node, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("create node: %w", err)
	}
	createdNode.Status.Capacity = corev1.ResourceList{
		corev1.ResourceCPU:    resource.MustParse("1000m"),
		corev1.ResourceMemory: resource.MustParse("1024Mi"),
		corev1.ResourcePods:   resource.MustParse("10"),
	}
	createdNode.Status.Allocatable = corev1.ResourceList{
		corev1.ResourceCPU:    resource.MustParse("1000m"),
		corev1.ResourceMemory: resource.MustParse("1024Mi"),
		corev1.ResourcePods:   resource.MustParse("10"),
	}
	createdNode.Status.Conditions = []corev1.NodeCondition{
		{
			Type:               corev1.NodeReady,
			Status:             corev1.ConditionTrue,
			LastTransitionTime: metav1.Now(),
		},
	}
	finalNode, err := kubeClient.CoreV1().Nodes().UpdateStatus(ctx, createdNode, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("update node status: %w", err)
	}
	fmt.Printf("Node status after update: CPU=%v, Memory=%v\n", finalNode.Status.Capacity.Cpu(), finalNode.Status.Capacity.Memory())
	for _, c := range finalNode.Status.Conditions {
		fmt.Printf("Node condition: %v=%v\n", c.Type, c.Status)
	}

	workloadPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "workload-on-node-a",
			Namespace: "default",
		},
		Spec: corev1.PodSpec{
			NodeName: "node-a",
			Containers: []corev1.Container{
				{
					Name:  "busy",
					Image: "busybox",
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("1000m"),
							corev1.ResourceMemory: resource.MustParse("32Mi"),
						},
					},
				},
			},
		},
	}
	createdWorkloadPod, err := kubeClient.CoreV1().Pods("default").Create(ctx, workloadPod, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("create workload pod: %w", err)
	}
	createdWorkloadPod.Status.Phase = corev1.PodRunning
	if _, err := kubeClient.CoreV1().Pods("default").UpdateStatus(ctx, createdWorkloadPod, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("update workload pod status: %w", err)
	}

	pendingPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pending-unschedulable-pod",
			Namespace: "default",
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name:  "pending",
					Image: "busybox",
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("80m"),
							corev1.ResourceMemory: resource.MustParse("32Mi"),
						},
					},
				},
			},
		},
	}
	createdPending, err := kubeClient.CoreV1().Pods("default").Create(ctx, pendingPod, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("create pending pod: %w", err)
	}
	createdPending.Status.Phase = corev1.PodPending
	createdPending.Status.Conditions = []corev1.PodCondition{
		{
			Type:               corev1.PodScheduled,
			Status:             corev1.ConditionFalse,
			Reason:             corev1.PodReasonUnschedulable,
			Message:            "0/1 nodes are available: 1 Insufficient cpu.",
			LastTransitionTime: metav1.Now(),
		},
	}
	if _, err := kubeClient.CoreV1().Pods("default").UpdateStatus(ctx, createdPending, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("update pending pod status: %w", err)
	}

	return nil
}

func mustLoadTestServerConfig(t *testing.T) config.Config {
	t.Helper()

	cfgPath := filepath.Join(t.TempDir(), "service-config.yaml")
	cfgYAML := `regions:
  - us-east-1
grpc:
  address: 127.0.0.1:0
health:
  address: 127.0.0.1:0
`
	if err := os.WriteFile(cfgPath, []byte(cfgYAML), 0o600); err != nil {
		t.Fatalf("write service config: %v", err)
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("load service config: %v", err)
	}
	return cfg
}

func writeKubeconfigFile(dir string, restCfg *rest.Config) (string, error) {
	kubeCfg := clientcmdapi.NewConfig()
	kubeCfg.Clusters["envtest"] = &clientcmdapi.Cluster{
		Server:                   restCfg.Host,
		CertificateAuthorityData: restCfg.CAData,
	}
	kubeCfg.AuthInfos["envtest-admin"] = &clientcmdapi.AuthInfo{
		ClientCertificateData: restCfg.CertData,
		ClientKeyData:         restCfg.KeyData,
		Token:                 restCfg.BearerToken,
	}
	kubeCfg.Contexts["envtest-context"] = &clientcmdapi.Context{
		Cluster:  "envtest",
		AuthInfo: "envtest-admin",
	}
	kubeCfg.CurrentContext = "envtest-context"

	outPath := filepath.Join(dir, "kubeconfig")
	if err := clientcmd.WriteToFile(*kubeCfg, outPath); err != nil {
		return "", err
	}
	return outPath, nil
}

func resolveClusterAutoscalerBinary(t *testing.T) string {
	t.Helper()

	if explicitPath := os.Getenv(caBinaryPathEnvVar); explicitPath != "" {
		info, err := os.Stat(explicitPath)
		if err != nil {
			t.Fatalf("%s is set but path is invalid: %v", caBinaryPathEnvVar, err)
		}
		if info.IsDir() {
			t.Fatalf("%s must point to a binary file, got directory: %s", caBinaryPathEnvVar, explicitPath)
		}
		return explicitPath
	}

	cacheDir, err := os.UserCacheDir()
	if err != nil {
		t.Fatalf("determine user cache dir: %v", err)
	}
	binDir := filepath.Join(cacheDir, "aws-multi-region-ca-externalgrpc", "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("create binary cache dir: %v", err)
	}
	binFilename := fmt.Sprintf("cluster-autoscaler-%s-%s-%s", defaultCABinaryVersion, runtime.GOOS, runtime.GOARCH)
	binPath := filepath.Join(binDir, binFilename)

	info, err := os.Stat(binPath)
	if err == nil && !info.IsDir() && info.Size() > 0 {
		return binPath
	}
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("stat cached binary: %v", err)
	}

	downloadURL := os.Getenv(caBinaryDownloadURLVar)
	if downloadURL != "" {
		downloadBinary(t, downloadURL, binPath)
		return binPath
	}

	imageName := fmt.Sprintf("registry.k8s.io/autoscaling/cluster-autoscaler-%s:v%s", runtime.GOARCH, defaultCABinaryVersion)
	t.Logf("Extracting cluster-autoscaler binary from Docker image %s", imageName)

	pullCmd := exec.Command("docker", "pull", imageName)
	if out, err := pullCmd.CombinedOutput(); err != nil {
		t.Fatalf("docker pull %s failed: %v\n%s", imageName, err, string(out))
	}

	createCmd := exec.Command("docker", "create", imageName)
	out, err := createCmd.Output()
	if err != nil {
		t.Fatalf("docker create %s failed: %v", imageName, err)
	}
	containerID := strings.TrimSpace(string(out))
	defer func() {
		_ = exec.Command("docker", "rm", containerID).Run()
	}()

	tmpPath := binPath + ".tmp"
	cpCmd := exec.Command("docker", "cp", containerID+":/cluster-autoscaler", tmpPath)
	if out, err := cpCmd.CombinedOutput(); err != nil {
		t.Fatalf("docker cp failed: %v\n%s", err, string(out))
	}

	if err := os.Chmod(tmpPath, 0755); err != nil {
		t.Fatalf("chmod binary file: %v", err)
	}
	if err := os.Rename(tmpPath, binPath); err != nil {
		t.Fatalf("install binary file: %v", err)
	}

	return binPath
}

func downloadBinary(t *testing.T, downloadURL, binPath string) {
	resp, err := http.Get(downloadURL)
	if err != nil {
		t.Fatalf("download cluster-autoscaler binary: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		t.Fatalf("download cluster-autoscaler binary: status=%d body=%q", resp.StatusCode, string(body))
	}

	tmpPath := binPath + ".tmp"
	out, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
	if err != nil {
		t.Fatalf("create temp binary file: %v", err)
	}
	if _, err := io.Copy(out, resp.Body); err != nil {
		_ = out.Close()
		t.Fatalf("write binary file: %v", err)
	}
	if err := out.Close(); err != nil {
		t.Fatalf("close binary file: %v", err)
	}
	if err := os.Rename(tmpPath, binPath); err != nil {
		t.Fatalf("install binary file: %v", err)
	}
}

func repeatASGOutput(out *autoscaling.DescribeAutoScalingGroupsOutput, count int) []*autoscaling.DescribeAutoScalingGroupsOutput {
	if count <= 0 {
		return nil
	}
	pages := make([]*autoscaling.DescribeAutoScalingGroupsOutput, 0, count)
	for i := 0; i < count; i++ {
		pages = append(pages, out)
	}
	return pages
}

func waitForCondition(timeout time.Duration, cond func() (bool, error)) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		ok, err := cond()
		if err != nil {
			return err
		}
		if ok {
			return nil
		}
		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("timeout after %s", timeout)
}
