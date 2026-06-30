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
	"github.com/ogen-app/pdf-service/internal/pdfengine"
	"github.com/ogen-app/pdf-service/internal/server"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("pdf-service: config: %v", err)
	}

	engine, err := pdfengine.New(pdfengine.Config{Workers: cfg.Workers})
	if err != nil {
		log.Fatalf("pdf-service: init engine: %v", err)
	}
	defer engine.Close()

	lis, err := net.Listen("tcp", cfg.Listen)
	if err != nil {
		log.Fatalf("pdf-service: listen %s: %v", cfg.Listen, err)
	}

	srv := grpc.NewServer()
	pdfv1.RegisterPdfServiceServer(srv, server.New(engine))

	hs := health.NewServer()
	healthpb.RegisterHealthServer(srv, hs)
	hs.SetServingStatus("pdf.v1.PdfService", healthpb.HealthCheckResponse_SERVING)
	hs.SetServingStatus("", healthpb.HealthCheckResponse_SERVING)

	go func() {
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
		<-sig
		log.Println("pdf-service: shutting down")
		srv.GracefulStop()
	}()

	log.Printf("pdf-service: listening on %s (workers=%d)", cfg.Listen, cfg.Workers)
	if err := srv.Serve(lis); err != nil {
		log.Fatalf("pdf-service: serve: %v", err)
	}
}
