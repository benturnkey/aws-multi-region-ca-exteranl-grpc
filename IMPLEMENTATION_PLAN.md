# AWS Multi-Region Cluster Autoscaler External gRPC Service

## Goal

Move AWS multi-region ASG logic out of in-tree Cluster Autoscaler AWS provider and into a standalone external gRPC cloud provider service compatible with `--cloud-provider=externalgrpc`.

## Scope

- Build a new service repo implementing the External gRPC `CloudProvider` API.
- Support AWS ASG multi-region discovery and scale operations.
- Support node and node group modeling from ASG + Launch Templates only (no EKS integration).
- Keep in-tree AWS provider single-region and unchanged in behavior after migration.

## Non-Goals

- Implementing new autoscaling algorithms.
- Replacing Cluster Autoscaler core loops.
- Adding cloud providers beyond AWS in this repo.
- Supporting GPU-aware behavior.
- Supporting EKS managed nodegroups.

## Current Status (2026-03-04)

Implemented:

- Nix-based dev/test workflow (`flake.nix`) and Go module bootstrap.
- YAML config loading with defaults for gRPC/health addresses and region normalization.
- Service startup with:
  - gRPC server
  - gRPC health service
  - HTTP `/healthz` and `/readyz` endpoints
- Per-region AWS client factory and routing:
  - region lookups
  - `region/asg-name` nodegroup ID parsing/routing
- Configurable ASG discovery via tags (defaults to `k8s.io/cluster-autoscaler/enabled: "true"`)
- Configurable ASG discovery via explicit node group IDs
- Read-only discovery and cache foundations:
  - multi-region ASG nodegroup discovery
  - snapshot builder
  - atomic cache replacement
- External gRPC provider (partial):
  - `NodeGroups`
  - `NodeGroupForNode`
  - `NodeGroupNodes`
  - `Refresh`
  - `GPULabel` no-op
  - `GetAvailableGPUTypes` no-op
  - `NodeGroupTargetSize`
  - `NodeGroupIncreaseSize`
  - `NodeGroupDecreaseTargetSize`
  - `NodeGroupDeleteNodes`
  - `NodeGroupTemplateNodeInfo`
  - `NodeGroupGetOptions`
- Refresh path updates cache and powers cache-backed nodegroup/node RPC behavior.
- Unit and integration tests for all implemented surfaces above.

Not implemented yet:

- Background/ticker refresh policy and configurable refresh interval enforcement.
- Launch Template and mixed instances policy modeling for template node info.
- Metrics, structured logging, retries/backoff, auth hardening, and rollout/deployment artifacts.

## Required gRPC API Surface

Based on `cluster-autoscaler/cloudprovider/externalgrpc/protos/externalgrpc.proto`.

### CloudProvider RPCs

- `NodeGroups`
- `NodeGroupForNode`
- `Refresh`
- `Cleanup`
- `GPULabel` -> no-op empty response (GPU unavailable)
- `GetAvailableGPUTypes` -> no-op empty list (GPU unavailable)

### NodeGroup RPCs

- `NodeGroupTargetSize`
- `NodeGroupIncreaseSize`
- `NodeGroupDeleteNodes`
- `NodeGroupDecreaseTargetSize`
- `NodeGroupNodes`
- `NodeGroupTemplateNodeInfo`
- `NodeGroupGetOptions`

### Optional RPCs (initially unimplemented)

- `PricingNodePrice` -> return gRPC `Unimplemented`
- `PricingPodPrice` -> return gRPC `Unimplemented`

## External ID and Identity Model

- NodeGroup ID format: `region/asg-name` (always region-qualified).
- Instance identity key: providerID if present, fallback to instance ID.
- Internal cache keys:
  - ASG key: `region/asg-name`
  - Instance key: `region/instance-id` (plus providerID index for lookup)

Rationale: avoids ASG name collision across regions and keeps routing deterministic.

## Service Architecture

Use a layered design:

1. `cmd/server`

- gRPC server bootstrap, TLS/mTLS, health endpoints, graceful shutdown.

2. `pkg/config`

- Parse YAML/env config.
- Region normalization (trim, dedupe, preserve order).

3. `pkg/awsclient`

- Per-region AWS SDK client factory (ASG, EC2).
- Endpoint overrides and credentials handling.

4. `pkg/discovery`

- Explicit ASG and tag-based auto-discovery per region.
- Fan-out describe calls and merge results.

5. `pkg/cache`

- In-memory snapshot cache:
  - ASG metadata
  - ASG -> instances
  - instance -> ASG
  - autoscaling options from ASG tags
- Atomic snapshot replacement on refresh.

6. `pkg/template`

- Build `v1.Node` template from ASG launch config/template/mixed instances policy.
- Do not perform EKS managed nodegroup label/taint enrichment.

7. `pkg/provider`

- Implements External gRPC server methods against cache + AWS APIs.
- Routes mutating ops by region from nodegroup ID.

## Config Contract (Service)

`config.yaml`:

- `regions: [us-east-1, us-west-2]`
- `grpc`:
  - `address` (implemented; default `:8086`)
- `health`:
  - `address` (implemented; default `:8081`)
- `refresh_interval` (planned; not enforced yet)
- Additional `discovery`, `aws`, TLS, and logging fields are planned but not implemented yet.

## Behavior Mapping from Existing In-Tree Logic

Implement externally:

- Multi-region ASG discovery fan-out.
- Region-aware service routing for:
  - set desired capacity
  - terminate instance
  - scaling activity checks
  - mixed instances policy lookups
- Placeholder instances for desired > active, including unfulfillable signaling.
- Autoscaling options from ASG tags.
- GPU methods return explicit no-op values (no GPU support).

Leave to CA core:

- Main autoscaling decision loop.
- gRPC client caching semantics in `externalgrpc` provider.

## API Semantics and Error Policy

- Not found nodegroup -> gRPC `NotFound`.
- Validation errors (bad ID format, bad delta) -> `InvalidArgument`.
- AWS auth/permission failures -> `PermissionDenied` or `FailedPrecondition` as appropriate.
- Upstream API/transient failures -> `Unavailable`/`DeadlineExceeded`.
- Optional methods not provided -> `Unimplemented`.

## Compatibility Notes

- GPU compatibility:
  - `GPULabel` returns an empty value to indicate no GPU label is available.
  - `GetAvailableGPUTypes` returns an empty list.
  - These responses are intentional and represent unsupported GPU capacity in this provider.
- EKS compatibility:
  - EKS managed nodegroup metadata enrichment is not implemented.
  - Node and node group behavior is derived from ASG and Launch Template data only.
  - No EKS API calls are made by this service.

## Metrics and Observability

- Prometheus metrics in service:
  - `aws_request_duration_seconds{endpoint,region,status}`
  - `grpc_request_duration_seconds{method,status}`
  - `cache_refresh_duration_seconds{result}`
  - `nodegroups_total`
  - `instances_total`
- Structured logs include `region`, `asg`, `instance_id`, `nodegroup_id`, `request_id`.

## Security

- mTLS support for service-to-CA traffic.
- IAM via IRSA/instance profile/env/shared config (no static secrets in code).
- Least privilege IAM policy for ASG/EC2 operations used.

## Delivery Phases

### Phase 0: Repo Bootstrap

- [x] Initialize Go module.
- [ ] Add proto generation wiring and CI checks.
- [x] Add base gRPC server and config loading.

### Phase 1: Read-Only Provider Surface

- [x] Implement `NodeGroups`, `NodeGroupForNode`, `NodeGroupNodes`, `Refresh`.
- [x] Build multi-region discovery + cache snapshots.
- [ ] Add unit tests for region collision (`same asg name` in 2 regions).

### Phase 2: Mutating NodeGroup Operations

- [x] Implement `NodeGroupIncreaseSize`, `NodeGroupDecreaseTargetSize`, `NodeGroupDeleteNodes`, `NodeGroupTargetSize`.
- [x] Add region-aware routing tests and id parsing tests.

### Phase 3: Template and Options

- [x] Implement `NodeGroupTemplateNodeInfo` and `NodeGroupGetOptions`.
- [ ] Support mixed instances policy from ASG + Launch Templates only.
- [x] Implement/verify GPU RPCs return no-op responses.

### Phase 4: Hardening

- Metrics/logging, retry/backoff, timeout tuning, race checks.
- Integration tests against mocked AWS clients and gRPC client.

### Phase 5: Migration and Rollout

- Deploy service in-cluster.
- Configure CA with `--cloud-provider=externalgrpc`.
- Validate canary (2+ regions) then production rollout.
- Revert/remove in-tree AWS multi-region patch from CA repo.

## Testing Strategy

- Unit tests:
  - region normalization
  - ID parsing/formatting
  - cache rebuild correctness
  - placeholder behavior
  - routing by region
- Contract tests:
  - gRPC server adheres to proto semantics and status codes
- Integration tests:
  - externalgrpc client <-> service interactions
  - refresh and scale workflows with fake AWS clients
- Concurrency:
  - `go test -race ./...`

## Risks and Mitigations

- Cache staleness across regions:
  - Use periodic refresh + explicit refresh RPC + atomic snapshots.
- Regional API throttling:
  - Add per-region rate limiting/backoff and monitor request metrics.
- ID incompatibility:
  - Standardize on `region/asg-name` and document as contract.
- Behavior drift from in-tree AWS:
  - Port logic in small, test-backed slices and compare outputs in fixtures.

## Rollout Checklist

- Service deployed with mTLS.
- CA switched to `externalgrpc` with valid cloud-config address/certs.
- Multi-region discovery verified (`NodeGroups` shows `region/name` IDs).
- Scale up/down ops succeed in each configured region.
- Metrics and logs validated.
- In-tree AWS multi-region changes removed.
