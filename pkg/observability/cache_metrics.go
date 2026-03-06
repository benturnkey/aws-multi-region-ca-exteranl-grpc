package observability

import (
	"context"
	"time"

	"aws-multi-region-ca-exteranl-grpc/pkg/cache"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
)

// RecordCacheRefresh wraps a refresh function with tracing and cache_refresh_duration_seconds.
// On success it also updates snapshot gauges.
func RecordCacheRefresh(ctx context.Context, m *Metrics, refreshFn func(context.Context) error, store *cache.Store) error {
	ctx, span := tracer.Start(ctx, "cache.refresh")
	defer span.End()

	start := time.Now()
	err := refreshFn(ctx)
	elapsed := time.Since(start).Seconds()

	result := "success"
	if err != nil {
		result = "error"
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	}

	m.CacheRefreshDuration.Record(ctx, elapsed,
		metric.WithAttributes(attribute.String("result", result)),
	)

	if err == nil {
		UpdateSnapshotGauges(ctx, m, store)
	}
	return err
}

// UpdateSnapshotGauges records nodegroups_total, instances_total, and per-ASG size gauges.
func UpdateSnapshotGauges(ctx context.Context, m *Metrics, store *cache.Store) {
	nodeGroups := store.AllNodeGroups()

	m.NodeGroupsTotal.Record(ctx, int64(len(nodeGroups)))

	var totalInstances int64
	for _, ng := range nodeGroups {
		instances := store.InstancesForNodeGroup(ng.ID)
		totalInstances += int64(len(instances))

		attrs := metric.WithAttributes(attribute.String("nodegroup", ng.ID))
		m.ASGMinSize.Record(ctx, int64(ng.MinSize), attrs)
		m.ASGMaxSize.Record(ctx, int64(ng.MaxSize), attrs)
		m.ASGCurrentSize.Record(ctx, int64(ng.TargetSize), attrs)
	}
	m.InstancesTotal.Record(ctx, totalInstances)
}
