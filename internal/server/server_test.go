package server_test

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"os"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	pdfv1 "github.com/ogen-app/pdf-service/gen/pdf/v1"
	"github.com/ogen-app/pdf-service/internal/pdfengine"
	"github.com/ogen-app/pdf-service/internal/server"
)

var client pdfv1.PdfServiceClient

func TestMain(m *testing.M) {
	eng, err := pdfengine.New(pdfengine.Config{Workers: 1})
	if err != nil {
		fmt.Fprintln(os.Stderr, "init engine:", err)
		os.Exit(1)
	}
	defer eng.Close()

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		fmt.Fprintln(os.Stderr, "listen:", err)
		os.Exit(1)
	}
	srv := grpc.NewServer()
	pdfv1.RegisterPdfServiceServer(srv, server.New(eng))
	go func() { _ = srv.Serve(lis) }()
	defer srv.Stop()

	conn, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		fmt.Fprintln(os.Stderr, "dial:", err)
		os.Exit(1)
	}
	defer conn.Close()
	client = pdfv1.NewPdfServiceClient(conn)

	os.Exit(m.Run())
}

func buildPDF(n int) []byte {
	var buf bytes.Buffer
	buf.WriteString("%PDF-1.4\n%\xe2\xe3\xcf\xd3\n")
	offsets := []int{buf.Len()}
	buf.WriteString("1 0 obj\n<< /Type /Catalog /Pages 2 0 R >>\nendobj\n")
	offsets = append(offsets, buf.Len())
	buf.WriteString("2 0 obj\n<< /Type /Pages /Kids [")
	for i := 0; i < n; i++ {
		if i > 0 {
			buf.WriteString(" ")
		}
		buf.WriteString(fmt.Sprintf("%d 0 R", 3+i))
	}
	buf.WriteString(fmt.Sprintf("] /Count %d >>\nendobj\n", n))
	for i := 0; i < n; i++ {
		offsets = append(offsets, buf.Len())
		buf.WriteString(fmt.Sprintf("%d 0 obj\n<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] >>\nendobj\n", 3+i))
	}
	xrefStart := buf.Len()
	totalObjs := 2 + n
	buf.WriteString(fmt.Sprintf("xref\n0 %d\n0000000000 65535 f \n", totalObjs+1))
	for _, off := range offsets {
		buf.WriteString(fmt.Sprintf("%010d 00000 n \n", off))
	}
	buf.WriteString(fmt.Sprintf("trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", totalObjs+1, xrefStart))
	return buf.Bytes()
}

func TestParseEndToEnd(t *testing.T) {
	stream, err := client.Parse(context.Background())
	if err != nil {
		t.Fatalf("open stream: %v", err)
	}
	if err := stream.Send(&pdfv1.ParseRequest{Payload: &pdfv1.ParseRequest_Options{
		Options: &pdfv1.ParseOptions{RenderThumbnail: true, ThumbnailDpi: 96},
	}}); err != nil {
		t.Fatalf("send options: %v", err)
	}
	data := buildPDF(2)
	mid := len(data) / 2 // two frames, to exercise reassembly
	for _, part := range [][]byte{data[:mid], data[mid:]} {
		if err := stream.Send(&pdfv1.ParseRequest{Payload: &pdfv1.ParseRequest_Chunk{Chunk: part}}); err != nil {
			t.Fatalf("send chunk: %v", err)
		}
	}
	resp, err := stream.CloseAndRecv()
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if resp.GetPageCount() != 2 {
		t.Fatalf("page count = %d, want 2", resp.GetPageCount())
	}
	if len(resp.GetThumbnailPng()) == 0 {
		t.Fatalf("expected a thumbnail")
	}
}

func TestRenderEndToEnd(t *testing.T) {
	stream, err := client.Render(context.Background())
	if err != nil {
		t.Fatalf("open stream: %v", err)
	}
	_ = stream.Send(&pdfv1.RenderRequest{Payload: &pdfv1.RenderRequest_Options{
		Options: &pdfv1.RenderOptions{RenderThumbnail: true, ThumbnailDpi: 96},
	}})
	_ = stream.Send(&pdfv1.RenderRequest{Payload: &pdfv1.RenderRequest_Chunk{Chunk: buildPDF(3)}})
	resp, err := stream.CloseAndRecv()
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if resp.GetPageCount() != 3 {
		t.Fatalf("page count = %d, want 3", resp.GetPageCount())
	}
	if len(resp.GetThumbnailPng()) == 0 {
		t.Fatalf("expected a thumbnail")
	}
}

func TestParseInvalidPDFIsInvalidArgument(t *testing.T) {
	stream, err := client.Parse(context.Background())
	if err != nil {
		t.Fatalf("open stream: %v", err)
	}
	_ = stream.Send(&pdfv1.ParseRequest{Payload: &pdfv1.ParseRequest_Options{Options: &pdfv1.ParseOptions{}}})
	_ = stream.Send(&pdfv1.ParseRequest{Payload: &pdfv1.ParseRequest_Chunk{Chunk: []byte("not a pdf at all")}})
	_, err = stream.CloseAndRecv()
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("want InvalidArgument for a corrupt PDF, got %v", err)
	}
}
