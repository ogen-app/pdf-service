package logging

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
)

// fakeStream is a minimal grpc.ServerStream: the interceptor only calls
// Context() and SetHeader(). Other methods are never invoked in these tests.
type fakeStream struct {
	grpc.ServerStream
	ctx    context.Context
	header metadata.MD
}

func (f *fakeStream) Context() context.Context { return f.ctx }
func (f *fakeStream) SetHeader(md metadata.MD) error {
	f.header = metadata.Join(f.header, md)
	return nil
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func runStream(t *testing.T, incoming context.Context) (handlerCtx context.Context, header metadata.MD) {
	t.Helper()
	fs := &fakeStream{ctx: incoming}
	interceptor := StreamServerInterceptor(discardLogger())
	err := interceptor(nil, fs, &grpc.StreamServerInfo{FullMethod: "/pdf.v1.PdfService/Parse"},
		func(_ any, ss grpc.ServerStream) error {
			handlerCtx = ss.Context()
			return nil
		})
	if err != nil {
		t.Fatalf("interceptor returned error: %v", err)
	}
	return handlerCtx, fs.header
}

func TestStreamInterceptorHonorsInboundRequestID(t *testing.T) {
	md := metadata.Pairs(RequestIDHeader, "abc-123", TenantIDHeader, "tenant-9")
	ctx := metadata.NewIncomingContext(context.Background(), md)

	handlerCtx, header := runStream(t, ctx)

	if id, ok := RequestID(handlerCtx); !ok || id != "abc-123" {
		t.Fatalf("request id in handler ctx = %q (%v), want abc-123", id, ok)
	}
	if id, ok := TenantID(handlerCtx); !ok || id != "tenant-9" {
		t.Fatalf("tenant id in handler ctx = %q (%v), want tenant-9", id, ok)
	}
	if got := header.Get(RequestIDHeader); len(got) == 0 || got[0] != "abc-123" {
		t.Fatalf("request id not echoed on response header: %v", header)
	}
}

func TestStreamInterceptorGeneratesRequestID(t *testing.T) {
	handlerCtx, header := runStream(t, context.Background())

	id, ok := RequestID(handlerCtx)
	if !ok || id == "" {
		t.Fatalf("expected a generated request id, got %q (%v)", id, ok)
	}
	if _, ok := TenantID(handlerCtx); ok {
		t.Fatal("tenant id should be absent when no metadata supplies it")
	}
	if got := header.Get(RequestIDHeader); len(got) == 0 || got[0] != id {
		t.Fatalf("echoed header %v does not match generated id %q", header, id)
	}
}

func TestLevelForCode(t *testing.T) {
	const rpc = "/pdf.v1.PdfService/Parse"
	cases := []struct {
		method string
		code   codes.Code
		want   slog.Level
	}{
		{rpc, codes.OK, slog.LevelInfo},
		{rpc, codes.Internal, slog.LevelError},
		{rpc, codes.Unavailable, slog.LevelError},
		{rpc, codes.InvalidArgument, slog.LevelWarn},
		{rpc, codes.NotFound, slog.LevelWarn},
		{"/grpc.health.v1.Health/Check", codes.OK, slog.LevelDebug},
		{"/grpc.health.v1.Health/Check", codes.Internal, slog.LevelDebug},
	}
	for _, c := range cases {
		if got := levelForCode(c.method, c.code); got != c.want {
			t.Errorf("levelForCode(%q, %v) = %v, want %v", c.method, c.code, got, c.want)
		}
	}
}
