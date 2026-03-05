package observability

import (
	"context"
	"net/http"

	promclient "github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/prometheus"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
)

const serviceName = "aws-multi-region-ca-exteranl-grpc"

// Resources holds initialized OTEL providers and the Prometheus HTTP handler.
type Resources struct {
	TracerProvider *sdktrace.TracerProvider
	MeterProvider  *sdkmetric.MeterProvider
	MetricsHandler http.Handler
}

// Setup initializes OTLP trace exporter and Prometheus metric exporter,
// registers them as global OTEL providers, and returns Resources for wiring.
func Setup(ctx context.Context, traceEndpoint string) (*Resources, error) {
	res, err := resource.New(ctx,
		resource.WithAttributes(semconv.ServiceName(serviceName)),
	)
	if err != nil {
		return nil, err
	}

	traceExporter, err := otlptracegrpc.New(ctx,
		otlptracegrpc.WithEndpoint(traceEndpoint),
		otlptracegrpc.WithInsecure(),
	)
	if err != nil {
		return nil, err
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(traceExporter),
		sdktrace.WithResource(res),
	)
	otel.SetTracerProvider(tp)

	promExporter, err := prometheus.New()
	if err != nil {
		return nil, err
	}

	mp := sdkmetric.NewMeterProvider(
		sdkmetric.WithReader(promExporter),
		sdkmetric.WithResource(res),
	)
	otel.SetMeterProvider(mp)

	return &Resources{
		TracerProvider: tp,
		MeterProvider:  mp,
		MetricsHandler: promhttp.Handler(),
	}, nil
}

// SetupForTest initializes only the Prometheus metric exporter (no trace exporter)
// with an isolated registry, suitable for tests that don't have a running OTLP collector.
func SetupForTest() (*Resources, error) {
	registry := promclient.NewRegistry()

	promExporter, err := prometheus.New(prometheus.WithRegisterer(registry))
	if err != nil {
		return nil, err
	}

	res, err := resource.New(context.Background(),
		resource.WithAttributes(semconv.ServiceName(serviceName)),
	)
	if err != nil {
		return nil, err
	}

	mp := sdkmetric.NewMeterProvider(
		sdkmetric.WithReader(promExporter),
		sdkmetric.WithResource(res),
	)

	tp := sdktrace.NewTracerProvider(sdktrace.WithResource(res))

	return &Resources{
		TracerProvider: tp,
		MeterProvider:  mp,
		MetricsHandler: promhttp.HandlerFor(registry, promhttp.HandlerOpts{}),
	}, nil
}

// Shutdown flushes and releases providers.
func (r *Resources) Shutdown(ctx context.Context) error {
	if err := r.TracerProvider.Shutdown(ctx); err != nil {
		return err
	}
	return r.MeterProvider.Shutdown(ctx)
}
