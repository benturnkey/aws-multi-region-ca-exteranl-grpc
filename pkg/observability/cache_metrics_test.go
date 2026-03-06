package observability

import (
	"context"
	"errors"
	"testing"

	"aws-multi-region-ca-exteranl-grpc/pkg/cache"

	"go.opentelemetry.io/otel"
)

func TestRecordCacheRefreshSuccess(t *testing.T) {
	t.Parallel()
	m, err := NewMetrics(otel.GetMeterProvider())
	if err != nil {
		t.Fatal(err)
	}

	store := cache.NewStore()
	store.Replace(cache.Snapshot{
		NodeGroups: map[string]cache.NodeGroup{
			"us-east-1/asg-a": {ID: "us-east-1/asg-a", MinSize: 1, MaxSize: 10, TargetSize: 3},
		},
		InstancesByNodeGroup: map[string][]cache.Instance{
			"us-east-1/asg-a": {{ID: "i-1", State: cache.InstanceStateRunning}},
		},
		NodeGroupByProviderID: map[string]string{},
		NodeGroupByNodeName:   map[string]string{},
	})

	refreshCalled := false
	refreshFn := func(ctx context.Context) error {
		refreshCalled = true
		return nil
	}

	err = RecordCacheRefresh(context.Background(), m, refreshFn, store)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !refreshCalled {
		t.Fatal("refresh function not called")
	}
}

func TestRecordCacheRefreshError(t *testing.T) {
	t.Parallel()
	m, err := NewMetrics(otel.GetMeterProvider())
	if err != nil {
		t.Fatal(err)
	}

	store := cache.NewStore()
	wantErr := errors.New("refresh failed")
	refreshFn := func(ctx context.Context) error {
		return wantErr
	}

	err = RecordCacheRefresh(context.Background(), m, refreshFn, store)
	if !errors.Is(err, wantErr) {
		t.Fatalf("expected %v, got %v", wantErr, err)
	}
}

func TestUpdateSnapshotGauges(t *testing.T) {
	t.Parallel()
	m, err := NewMetrics(otel.GetMeterProvider())
	if err != nil {
		t.Fatal(err)
	}

	store := cache.NewStore()
	store.Replace(cache.Snapshot{
		NodeGroups: map[string]cache.NodeGroup{
			"us-east-1/asg-a": {ID: "us-east-1/asg-a", MinSize: 1, MaxSize: 10, TargetSize: 5},
			"us-west-2/asg-b": {ID: "us-west-2/asg-b", MinSize: 2, MaxSize: 20, TargetSize: 8},
		},
		InstancesByNodeGroup: map[string][]cache.Instance{
			"us-east-1/asg-a": {
				{ID: "i-1", State: cache.InstanceStateRunning},
				{ID: "i-2", State: cache.InstanceStateRunning},
			},
			"us-west-2/asg-b": {
				{ID: "i-3", State: cache.InstanceStateRunning},
			},
		},
		NodeGroupByProviderID: map[string]string{},
		NodeGroupByNodeName:   map[string]string{},
	})

	// Should not panic
	UpdateSnapshotGauges(context.Background(), m, store)
}
