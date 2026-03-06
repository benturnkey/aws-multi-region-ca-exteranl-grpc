package config

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	DefaultGRPCAddress    = ":8086"
	DefaultHealthAddress  = ":8081"
	DefaultMetricsAddress = ":9090"
	DefaultTracesEndpoint = "localhost:4317"
)

// Config stores runtime service settings.
type Config struct {
	Regions       []string            `yaml:"regions"`
	Discovery     DiscoveryConfig     `yaml:"discovery"`
	NodeGroups    []NodeGroupConfig   `yaml:"nodeGroups"`
	GRPC          GRPCConfig          `yaml:"grpc"`
	Health        HealthConfig        `yaml:"health"`
	Observability ObservabilityConfig `yaml:"observability"`
}

type ObservabilityConfig struct {
	Traces  TracesConfig  `yaml:"traces"`
	Metrics MetricsConfig `yaml:"metrics"`
}

type TracesConfig struct {
	Endpoint string `yaml:"endpoint"`
}

type MetricsConfig struct {
	Address string `yaml:"address"`
}

type DiscoveryConfig struct {
	Tags map[string]string `yaml:"tags"`
}

type NodeGroupConfig struct {
	Name string `yaml:"name"`
}

type GRPCConfig struct {
	Address string `yaml:"address"`
}

type HealthConfig struct {
	Address string `yaml:"address"`
}

// Load reads and validates the service configuration from a YAML file.
func Load(path string) (Config, error) {
	if path == "" {
		return Config{}, errors.New("config path is required")
	}

	b, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("read config: %w", err)
	}

	var cfg Config
	if err := yaml.Unmarshal(b, &cfg); err != nil {
		return Config{}, fmt.Errorf("parse config yaml: %w", err)
	}

	if cfg.GRPC.Address == "" {
		cfg.GRPC.Address = DefaultGRPCAddress
	}
	if cfg.Health.Address == "" {
		cfg.Health.Address = DefaultHealthAddress
	}
	if cfg.Observability.Traces.Endpoint == "" {
		cfg.Observability.Traces.Endpoint = DefaultTracesEndpoint
	}
	if cfg.Observability.Metrics.Address == "" {
		cfg.Observability.Metrics.Address = DefaultMetricsAddress
	}

	// Default to standard cluster-autoscaler tag if no explicit nodegroups or tags specified
	if len(cfg.Discovery.Tags) == 0 && len(cfg.NodeGroups) == 0 {
		cfg.Discovery.Tags = map[string]string{
			"k8s.io/cluster-autoscaler/enabled": "true",
		}
	}

	cfg.Regions = normalizeRegions(cfg.Regions)

	return cfg, nil
}

// NormalizedRegions returns trimmed, deduped regions preserving order.
func (c Config) NormalizedRegions() []string {
	return normalizeRegions(c.Regions)
}

func normalizeRegions(in []string) []string {
	out := make([]string, 0, len(in))
	seen := make(map[string]struct{}, len(in))
	for _, region := range in {
		r := strings.TrimSpace(region)
		if r == "" {
			continue
		}
		if _, ok := seen[r]; ok {
			continue
		}
		seen[r] = struct{}{}
		out = append(out, r)
	}
	return out
}
