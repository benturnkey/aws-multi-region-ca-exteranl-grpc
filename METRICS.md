# Metrics

All metrics are exposed via a dedicated HTTP server at the configured `observability.metrics.address` (default `:9090`) on the `/metrics` endpoint in Prometheus exposition format.

## Configuration

```yaml
observability:
  traces:
    endpoint: "localhost:4317"  # OTLP gRPC collector endpoint
  metrics:
    address: ":9090"           # dedicated metrics HTTP server
```

## Exposed Metrics

### Histograms

| Metric | Labels | Description |
|--------|--------|-------------|
| `aws_request_duration_seconds` | `endpoint`, `region`, `status` | Duration of AWS API requests |
| `grpc_request_duration_seconds` | `method`, `status` | Duration of gRPC requests |
| `cache_refresh_duration_seconds` | `result` | Duration of cache refresh operations |

### Gauges

| Metric | Labels | Description |
|--------|--------|-------------|
| `nodegroups_total` | — | Total number of discovered node groups |
| `instances_total` | — | Total number of cached instances |
| `asg_min_size` | `nodegroup` | Minimum size of an ASG |
| `asg_max_size` | `nodegroup` | Maximum size of an ASG |
| `asg_current_size` | `nodegroup` | Current desired size of an ASG |
