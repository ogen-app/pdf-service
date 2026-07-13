package chunk_test

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/ogen-app/pdf-service/internal/chunk"
)

func TestPages_ShortDocIsOneChunkSpanningAllPages(t *testing.T) {
	got := chunk.Pages([]chunk.Page{
		{Num: 1, Text: "hello world"},
		{Num: 2, Text: "second page text"},
	}, chunk.Config{})
	if len(got) != 1 {
		t.Fatalf("want 1 chunk, got %d", len(got))
	}
	if got[0].PageStart != 1 || got[0].PageEnd != 2 {
		t.Fatalf("page range = %d-%d, want 1-2", got[0].PageStart, got[0].PageEnd)
	}
	if !strings.Contains(got[0].Text, "hello world") || !strings.Contains(got[0].Text, "second page text") {
		t.Fatalf("chunk text missing content: %q", got[0].Text)
	}
}

func TestPages_BlankPagesReturnNil(t *testing.T) {
	if got := chunk.Pages([]chunk.Page{{Num: 1, Text: "   \n\n  "}}, chunk.Config{}); got != nil {
		t.Fatalf("want nil for whitespace-only input, got %d chunks", len(got))
	}
}

func TestPages_MultibyteTextStaysValidUTF8(t *testing.T) {
	// A long run of 3-byte runes with no ASCII spaces forces both the
	// word-boundary hard split and the overlap slice to cut inside the text at
	// offsets that don't fall on rune boundaries. Naive byte slicing there
	// splits a rune and yields invalid UTF-8, which proto3 string fields
	// (Chunk.Text) reject at gRPC marshal time (CON-110).
	cfg := chunk.Config{Target: 100, Overlap: 20, Max: 150}
	body := strings.Repeat("世", 200) // 600 bytes, no spaces, none aligned to 100/20
	got := chunk.Pages([]chunk.Page{
		{Num: 1, Text: body},
		{Num: 2, Text: body},
	}, cfg)
	if len(got) < 2 {
		t.Fatalf("expected multiple chunks for a long doc, got %d", len(got))
	}
	for i, c := range got {
		if !utf8.ValidString(c.Text) {
			t.Fatalf("chunk %d contains invalid UTF-8: %q", i, c.Text)
		}
	}
}

func TestPages_LongDocSplitsAcrossChunksWithValidPageRanges(t *testing.T) {
	cfg := chunk.Config{Target: 100, Overlap: 20, Max: 150}
	para := strings.TrimSpace(strings.Repeat("word ", 30)) // ~149 chars
	pages := []chunk.Page{
		{Num: 1, Text: para},
		{Num: 2, Text: para},
		{Num: 3, Text: para},
	}
	got := chunk.Pages(pages, cfg)
	if len(got) < 2 {
		t.Fatalf("expected multiple chunks for a long doc, got %d", len(got))
	}
	for i, c := range got {
		if strings.TrimSpace(c.Text) == "" {
			t.Fatalf("chunk %d is empty", i)
		}
		if c.PageStart < 1 || c.PageEnd > 3 || c.PageStart > c.PageEnd {
			t.Fatalf("chunk %d has invalid page range %d-%d", i, c.PageStart, c.PageEnd)
		}
	}
}
