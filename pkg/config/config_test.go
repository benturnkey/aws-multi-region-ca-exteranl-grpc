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
				if tt.path == "__EMPTY__" {
					cfgPath = ""
				} else if tt.path == "does-not-exist.yaml" {
					cfgPath = filepath.Join(t.TempDir(), tt.path)
				} else {
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
