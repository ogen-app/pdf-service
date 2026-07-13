package logging

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"testing"

	"github.com/ogen-app/pdf-service/internal/config"
)

func TestParseLevel(t *testing.T) {
	cases := map[string]slog.Level{
		"debug":   slog.LevelDebug,
		"DEBUG":   slog.LevelDebug,
		"info":    slog.LevelInfo,
		"":        slog.LevelInfo,
		"bogus":   slog.LevelInfo,
		"warn":    slog.LevelWarn,
		"warning": slog.LevelWarn,
		"error":   slog.LevelError,
	}
	for in, want := range cases {
		if got := parseLevel(in); got != want {
			t.Errorf("parseLevel(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestNewBaseHandlerFormat(t *testing.T) {
	var jsonBuf bytes.Buffer
	slog.New(newBaseHandler(&jsonBuf, &config.Config{LogFormat: "json"})).Info("hi")
	if !json.Valid(bytes.TrimSpace(jsonBuf.Bytes())) {
		t.Fatalf("json format did not produce JSON: %q", jsonBuf.String())
	}

	var textBuf bytes.Buffer
	slog.New(newBaseHandler(&textBuf, &config.Config{LogFormat: "text"})).Info("hi")
	if json.Valid(bytes.TrimSpace(textBuf.Bytes())) {
		t.Fatalf("text format unexpectedly produced JSON: %q", textBuf.String())
	}
}

func TestNewBaseHandlerLevelFilters(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(newBaseHandler(&buf, &config.Config{LogLevel: "warn", LogFormat: "json"}))
	logger.Info("suppressed")
	logger.Warn("kept")

	if bytes.Contains(buf.Bytes(), []byte("suppressed")) {
		t.Fatalf("info line should be filtered at warn: %q", buf.String())
	}
	if !bytes.Contains(buf.Bytes(), []byte("kept")) {
		t.Fatalf("warn line should pass at warn: %q", buf.String())
	}
}

func TestNewInstallsDefaultAndDoesNotPanic(t *testing.T) {
	// New mutates global state (slog default, stdlib log, grpclog); just assert
	// it wires up without panicking and enriches via the default logger.
	logger := New(&config.Config{LogLevel: "debug", LogFormat: "text"})
	if logger == nil {
		t.Fatal("New returned nil")
	}
	slog.Default().InfoContext(WithRequestID(context.Background(), "x"), "boot")
}
