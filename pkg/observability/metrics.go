package observability

import (
	"go.opentelemetry.io/otel/metric"
)

const meterName = "aws-multi-region-ca-exteranl-grpc"

// Metrics holds all metric instruments for the service.
type Metrics struct {
	AWSRequestDuration   metric.Float64Histogram
	GRPCRequestDuration  metric.Float64Histogram
	CacheRefreshDuration metric.Float64Histogram
	NodeGroupsTotal      metric.Int64Gauge
	InstancesTotal       metric.Int64Gauge
	ASGMinSize           metric.Int64Gauge
	ASGMaxSize           metric.Int64Gauge
	ASGCurrentSize       metric.Int64Gauge
}

// NewMetrics registers all metric instruments with the given meter provider.
func NewMetrics(mp metric.MeterProvider) (*Metrics, error) {
	meter := mp.Meter(meterName)

	awsDuration, err := meter.Float64Histogram("aws_request_duration_seconds",
		metric.WithDescription("Duration of AWS API requests"),
		metric.WithUnit("s"),
	)
	if err != nil {
		return nil, err
	}

	grpcDuration, err := meter.Float64Histogram("grpc_request_duration_seconds",
		metric.WithDescription("Duration of gRPC requests"),
		metric.WithUnit("s"),
	)
	if err != nil {
		return nil, err
	}

	cacheRefreshDuration, err := meter.Float64Histogram("cache_refresh_duration_seconds",
		metric.WithDescription("Duration of cache refresh operations"),
		metric.WithUnit("s"),
	)
	if err != nil {
		return nil, err
	}

	nodeGroupsTotal, err := meter.Int64Gauge("nodegroups_total",
		metric.WithDescription("Total number of discovered node groups"),
	)
	if err != nil {
		return nil, err
	}

	instancesTotal, err := meter.Int64Gauge("instances_total",
		metric.WithDescription("Total number of cached instances"),
	)
	if err != nil {
		return nil, err
	}

	asgMinSize, err := meter.Int64Gauge("asg_min_size",
		metric.WithDescription("Minimum size of an ASG"),
	)
	if err != nil {
		return nil, err
	}

	asgMaxSize, err := meter.Int64Gauge("asg_max_size",
		metric.WithDescription("Maximum size of an ASG"),
	)
	if err != nil {
		return nil, err
	}

	asgCurrentSize, err := meter.Int64Gauge("asg_current_size",
		metric.WithDescription("Current desired size of an ASG"),
	)
	if err != nil {
		return nil, err
	}

	return &Metrics{
		AWSRequestDuration:   awsDuration,
		GRPCRequestDuration:  grpcDuration,
		CacheRefreshDuration: cacheRefreshDuration,
		NodeGroupsTotal:      nodeGroupsTotal,
		InstancesTotal:       instancesTotal,
		ASGMinSize:           asgMinSize,
		ASGMaxSize:           asgMaxSize,
		ASGCurrentSize:       asgCurrentSize,
	}, nil
}
