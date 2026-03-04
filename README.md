# AWS Multi-Region CA External gRPC Service

Standalone AWS cloud provider service for Kubernetes Cluster Autoscaler using the `externalgrpc` cloud provider interface.

## Purpose

This project moves AWS multi-region node group logic out of the in-tree autoscaler AWS provider and into an external gRPC service.

Current scope:

- AWS ASG multi-region support
- Node group identity format: `region/asg-name`
- ASG + Launch Template direction (no EKS-specific APIs)
- No GPU support (GPU RPCs are explicit no-ops)

## Current Implementation

Implemented today:

- Nix flake-based dev/test setup
- YAML config loading with defaults
- gRPC + HTTP health/readiness server startup
- Region-aware AWS client factory (ASG/EC2)
- Read-only discovery + snapshot cache rebuild
- gRPC RPCs:
  - `NodeGroups`
  - `NodeGroupForNode`
  - `NodeGroupNodes`
  - `Refresh`
  - `GPULabel` (no-op)
  - `GetAvailableGPUTypes` (no-op)
  - `NodeGroupTargetSize`
  - `NodeGroupIncreaseSize`
  - `NodeGroupDecreaseTargetSize`
  - `NodeGroupDeleteNodes`
  - `NodeGroupTemplateNodeInfo`
  - `NodeGroupGetOptions`

## TODO Features

- Config contract expansion:
  - ASG tag auto-discovery config
  - explicit node group config
  - TLS/mTLS settings
  - structured logging settings
- Refresh policy:
  - configurable `refresh_interval` enforcement
  - optional background periodic refresh loop
- AWS behavior parity:
  - Launch Template/mixed instance policy template modeling
  - autoscaling-options tags handling
- Operational hardening:
  - Prometheus metrics
  - retry/backoff/timeouts
  - IAM docs and deployment manifests
  - race/concurrency hardening beyond current test baseline

## Local Development

Enter the development shell:

```bash
nix develop
```

Run tests:

```bash
go test ./...
```

## Notes

- GPUs are intentionally unsupported in this service version.
- EKS managed nodegroup metadata enrichment is intentionally out of scope.
