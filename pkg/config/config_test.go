package config_test

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"aws-multi-region-ca-exteranl-grpc/pkg/config"
)

func TestLoadYAMLConfigContract(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	cfgYAML := `grpc:
  address: 127.0.0.1:18086
health:
  address: 127.0.0.1:18087
discovery:
  tags:
    "custom/tag": "value"
nodeGroups:
  - name: "explicit-asg-1"
  - name: "explicit-asg-2"
`
	if err := os.WriteFile(cfgPath, []byte(cfgYAML), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	if cfg.GRPC.Address != "127.0.0.1:18086" {
		t.Fatalf("grpc.address=%q", cfg.GRPC.Address)
	}
	if cfg.Health.Address != "127.0.0.1:18087" {
		t.Fatalf("health.address=%q", cfg.Health.Address)
	}
	if got := len(cfg.Discovery.Tags); got != 1 || cfg.Discovery.Tags["custom/tag"] != "value" {
		t.Fatalf("discovery.tags=%v", cfg.Discovery.Tags)
	}
	if got := len(cfg.NodeGroups); got != 2 || cfg.NodeGroups[0].Name != "explicit-asg-1" || cfg.NodeGroups[1].Name != "explicit-asg-2" {
		t.Fatalf("nodeGroups=%v", cfg.NodeGroups)
	}
}

func TestLoadYAMLConfigValidationErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		content string
		path    string
		wantErr string
	}{
		{
			name:    "empty path",
			path:    "__EMPTY__",
			wantErr: "config path is required",
		},
		{
			name: "invalid yaml",
			content: `grpc:
  address: 127.0.0.1:18086
health: [`,
			wantErr: "parse config yaml",
		},
		{
			name: "missing grpc address uses default",
			content: `grpc: {}
health:
  address: 127.0.0.1:18087
`,
		},
		{
			name: "missing health address uses default",
			content: `grpc:
  address: 127.0.0.1:18086
health: {}
`,
		},
		{
			name:    "missing file",
			path:    "does-not-exist.yaml",
			wantErr: "read config",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var cfgPath string
			if tt.path != "" {
				switch tt.path {
				case "__EMPTY__":
					cfgPath = ""
				case "does-not-exist.yaml":
					cfgPath = filepath.Join(t.TempDir(), tt.path)
				default:
					cfgPath = tt.path
				}
			} else {
				dir := t.TempDir()
				cfgPath = filepath.Join(dir, "config.yaml")
				if err := os.WriteFile(cfgPath, []byte(tt.content), 0o600); err != nil {
					t.Fatalf("write config: %v", err)
				}
			}

			_, err := config.Load(cfgPath)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error=%q want substring %q", err.Error(), tt.wantErr)
			}
		})
	}
}

func TestLoadYAMLConfigDefaults(t *testing.T) {
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

	if cfg.GRPC.Address != config.DefaultGRPCAddress {
		t.Fatalf("grpc.address=%q want=%q", cfg.GRPC.Address, config.DefaultGRPCAddress)
	}
	if cfg.Health.Address != config.DefaultHealthAddress {
		t.Fatalf("health.address=%q want=%q", cfg.Health.Address, config.DefaultHealthAddress)
	}
	if cfg.Observability.Traces.Endpoint != config.DefaultTracesEndpoint {
		t.Fatalf("observability.traces.endpoint=%q want=%q", cfg.Observability.Traces.Endpoint, config.DefaultTracesEndpoint)
	}
	if cfg.Observability.Metrics.Address != config.DefaultMetricsAddress {
		t.Fatalf("observability.metrics.address=%q want=%q", cfg.Observability.Metrics.Address, config.DefaultMetricsAddress)
	}
}

func TestLoadObservabilityConfigExplicit(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	cfgYAML := `observability:
  traces:
    endpoint: "otel-collector:4317"
  metrics:
    address: ":9191"
`
	if err := os.WriteFile(cfgPath, []byte(cfgYAML), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	if cfg.Observability.Traces.Endpoint != "otel-collector:4317" {
		t.Fatalf("observability.traces.endpoint=%q want=%q", cfg.Observability.Traces.Endpoint, "otel-collector:4317")
	}
	if cfg.Observability.Metrics.Address != ":9191" {
		t.Fatalf("observability.metrics.address=%q want=%q", cfg.Observability.Metrics.Address, ":9191")
	}
}

func TestLoadNormalizesRegions(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	cfgYAML := `regions:
  - " us-east-1 "
  - "us-west-2"
  - ""
  - "us-east-1"
`
	if err := os.WriteFile(cfgPath, []byte(cfgYAML), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	want := []string{"us-east-1", "us-west-2"}
	if got := cfg.NormalizedRegions(); !reflect.DeepEqual(got, want) {
		t.Fatalf("NormalizedRegions=%v want=%v", got, want)
	}
}
