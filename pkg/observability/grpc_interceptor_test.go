package observability

import (
	"context"
	"errors"
	"testing"

	"go.opentelemetry.io/otel"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestGRPCInterceptorCallsHandler(t *testing.T) {
	t.Parallel()
	m, err := NewMetrics(otel.GetMeterProvider())
	if err != nil {
		t.Fatal(err)
	}

	interceptor := GRPCUnaryServerInterceptor(m)

	called := false
	handler := func(ctx context.Context, req any) (any, error) {
		called = true
		return "response", nil
	}

	info := &grpc.UnaryServerInfo{FullMethod: "/test.Service/TestMethod"}
	resp, err := interceptor(context.Background(), "request", info, handler)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !called {
		t.Fatal("handler not called")
	}
	if resp != "response" {
		t.Fatalf("expected 'response', got %v", resp)
	}
}

func TestGRPCInterceptorForwardsErrors(t *testing.T) {
	t.Parallel()
	m, err := NewMetrics(otel.GetMeterProvider())
	if err != nil {
		t.Fatal(err)
	}

	interceptor := GRPCUnaryServerInterceptor(m)

	wantErr := status.Error(codes.NotFound, "not found")
	handler := func(ctx context.Context, req any) (any, error) {
		return nil, wantErr
	}

	info := &grpc.UnaryServerInfo{FullMethod: "/test.Service/TestMethod"}
	_, err = interceptor(context.Background(), "request", info, handler)
	if err == nil {
		t.Fatal("expected error")
	}

	st, ok := status.FromError(err)
	if !ok {
		t.Fatal("expected gRPC status error")
	}
	if st.Code() != codes.NotFound {
		t.Fatalf("expected NotFound, got %v", st.Code())
	}
}

func TestGRPCInterceptorHandlesNonGRPCErrors(t *testing.T) {
	t.Parallel()
	m, err := NewMetrics(otel.GetMeterProvider())
	if err != nil {
		t.Fatal(err)
	}

	interceptor := GRPCUnaryServerInterceptor(m)

	wantErr := errors.New("plain error")
	handler := func(ctx context.Context, req any) (any, error) {
		return nil, wantErr
	}

	info := &grpc.UnaryServerInfo{FullMethod: "/test.Service/ErrorMethod"}
	_, err = interceptor(context.Background(), "request", info, handler)
	if !errors.Is(err, wantErr) {
		t.Fatalf("expected %v, got %v", wantErr, err)
	}
}
