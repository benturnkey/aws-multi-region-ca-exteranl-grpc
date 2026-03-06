package main

import (
	"context"
	"flag"
	"log"
	"os/signal"
	"syscall"
	"time"

	"aws-multi-region-ca-exteranl-grpc/pkg/config"
	"aws-multi-region-ca-exteranl-grpc/pkg/observability"
	"aws-multi-region-ca-exteranl-grpc/pkg/server"

	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"google.golang.org/grpc"
)

func main() {
	configPath := flag.String("config", "config.yaml", "path to service configuration")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	otelRes, err := observability.Setup(context.Background(), cfg.Observability.Traces.Endpoint)
	if err != nil {
		log.Fatalf("setup observability: %v", err)
	}

	metrics, err := observability.NewMetrics(otelRes.MeterProvider)
	if err != nil {
		log.Fatalf("create metrics: %v", err)
	}

	svc, err := server.Start(cfg,
		server.WithMetricsHandler(otelRes.MetricsHandler),
		server.WithGRPCServerOptions(
			grpc.StatsHandler(otelgrpc.NewServerHandler()),
			grpc.ChainUnaryInterceptor(observability.GRPCUnaryServerInterceptor(metrics)),
		),
		server.WithMetrics(metrics),
	)
	if err != nil {
		log.Fatalf("start service: %v", err)
	}
	log.Printf("service started; health=%s metrics=%s", svc.HealthAddr(), svc.MetricsAddr())

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	<-ctx.Done()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := svc.Stop(shutdownCtx); err != nil {
		log.Printf("stop service: %v", err)
	}
	if err := otelRes.Shutdown(shutdownCtx); err != nil {
		log.Printf("shutdown observability: %v", err)
	}
}
