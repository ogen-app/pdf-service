// Command pdf-service is the gRPC PDF parsing microservice (CON-103). It serves
// pdf.v1.PdfService (Parse + Render) backed by pdfium, plus a standard
// grpc.health.v1 endpoint. It is internal-only — the Ogen API reaches it over
// the private network.
package main

import (
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"

	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"

	pdfv1 "github.com/ogen-app/pdf-service/gen/pdf/v1"
	"github.com/ogen-app/pdf-service/internal/config"
	"github.com/ogen-app/pdf-service/internal/logging"
	"github.com/ogen-app/pdf-service/internal/pdfengine"
	"github.com/ogen-app/pdf-service/internal/server"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		// Pre-logger: config drives the logger's level/format, so a load
		// failure can only report through the stdlib default (CON-107 keeps
		// boot log.Fatal*).
		log.Fatalf("pdf-service: config: %v", err)
	}

	logger := logging.New(cfg)

	engine, err := pdfengine.New(pdfengine.Config{Workers: cfg.Workers})
	if err != nil {
		logger.Error("init engine", "component", "boot", "err", err)
		os.Exit(1)
	}
	defer engine.Close()

	lis, err := net.Listen("tcp", cfg.Listen)
	if err != nil {
		logger.Error("listen", "component", "boot", "addr", cfg.Listen, "err", err)
		os.Exit(1)
	}

	srv := grpc.NewServer(
		grpc.ChainUnaryInterceptor(logging.UnaryServerInterceptor(logger)),
		grpc.ChainStreamInterceptor(logging.StreamServerInterceptor(logger)),
	)
	pdfv1.RegisterPdfServiceServer(srv, server.New(engine))

	hs := health.NewServer()
	healthpb.RegisterHealthServer(srv, hs)
	hs.SetServingStatus("pdf.v1.PdfService", healthpb.HealthCheckResponse_SERVING)
	hs.SetServingStatus("", healthpb.HealthCheckResponse_SERVING)

	go func() {
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
		<-sig
		logger.Info("shutting down", "component", "boot")
		srv.GracefulStop()
	}()

	logger.Info("listening", "component", "boot", "addr", cfg.Listen, "workers", cfg.Workers)
	if err := srv.Serve(lis); err != nil {
		logger.Error("serve", "component", "boot", "err", err)
		os.Exit(1)
	}
}
