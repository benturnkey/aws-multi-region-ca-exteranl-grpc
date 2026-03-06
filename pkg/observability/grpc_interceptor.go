package observability

import (
	"context"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"google.golang.org/grpc"
	"google.golang.org/grpc/status"
)

// GRPCUnaryServerInterceptor returns a gRPC unary server interceptor that
// records grpc_request_duration_seconds with method and gRPC status code.
func GRPCUnaryServerInterceptor(m *Metrics) grpc.UnaryServerInterceptor {
	return func(
		ctx context.Context,
		req any,
		info *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler,
	) (any, error) {
		start := time.Now()
		resp, err := handler(ctx, req)
		elapsed := time.Since(start).Seconds()

		st, _ := status.FromError(err)
		m.GRPCRequestDuration.Record(ctx, elapsed,
			metric.WithAttributes(
				attribute.String("method", info.FullMethod),
				attribute.String("status", st.Code().String()),
			),
		)
		return resp, err
	}
}
