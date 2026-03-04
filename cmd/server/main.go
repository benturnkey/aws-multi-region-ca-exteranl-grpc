package main

import (
	"context"
	"flag"
	"log"
	"os/signal"
	"syscall"
	"time"

	"aws-multi-region-ca-exteranl-grpc/pkg/config"
	"aws-multi-region-ca-exteranl-grpc/pkg/server"
)

func main() {
	configPath := flag.String("config", "config.yaml", "path to service configuration")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	svc, err := server.Start(cfg)
	if err != nil {
		log.Fatalf("start service: %v", err)
	}
	log.Printf("service started; health endpoints on %s", svc.HealthAddr())

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	<-ctx.Done()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := svc.Stop(shutdownCtx); err != nil {
		log.Fatalf("stop service: %v", err)
	}
}
