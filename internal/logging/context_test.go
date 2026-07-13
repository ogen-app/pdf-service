package logging

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"testing"
)

func newJSONLogger(buf *bytes.Buffer) *slog.Logger {
	return slog.New(ContextHandler{slog.NewJSONHandler(buf, nil)})
}

func lastLine(t *testing.T, buf *bytes.Buffer) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &m); err != nil {
		t.Fatalf("log line is not JSON: %v (%q)", err, buf.String())
	}
	return m
}

func TestContextHandlerEnrichesWithIDs(t *testing.T) {
	var buf bytes.Buffer
	ctx := WithTenantID(WithRequestID(context.Background(), "rid-1"), "ten-1")
	newJSONLogger(&buf).InfoContext(ctx, "hi")

	m := lastLine(t, &buf)
	if m["request_id"] != "rid-1" {
		t.Fatalf("request_id = %v, want rid-1", m["request_id"])
	}
	if m["tenant_id"] != "ten-1" {
		t.Fatalf("tenant_id = %v, want ten-1", m["tenant_id"])
	}
}

func TestContextHandlerOmitsAbsentIDs(t *testing.T) {
	var buf bytes.Buffer
	newJSONLogger(&buf).InfoContext(context.Background(), "hi")

	m := lastLine(t, &buf)
	if _, ok := m["request_id"]; ok {
		t.Fatalf("request_id should be absent, got %v", m["request_id"])
	}
	if _, ok := m["tenant_id"]; ok {
		t.Fatalf("tenant_id should be absent, got %v", m["tenant_id"])
	}
}

func TestContextHandlerSurvivesWithAttrs(t *testing.T) {
	var buf bytes.Buffer
	logger := newJSONLogger(&buf).With("component", "test")
	logger.InfoContext(WithRequestID(context.Background(), "rid-2"), "hi")

	m := lastLine(t, &buf)
	if m["component"] != "test" {
		t.Fatalf("component = %v, want test", m["component"])
	}
	if m["request_id"] != "rid-2" {
		t.Fatalf("request_id lost through With(): %v", m["request_id"])
	}
}
