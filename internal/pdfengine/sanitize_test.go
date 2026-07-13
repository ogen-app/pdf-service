package pdfengine

import (
	"testing"
	"unicode/utf8"
)

func TestSanitizeUTF8(t *testing.T) {
	// "\xed\xa0\x80" is U+D800, a lone UTF-16 surrogate — exactly what pdfium
	// can emit for a glyph with no ToUnicode mapping. As UTF-8 bytes it's
	// invalid and makes gRPC marshaling fail with "string field contains
	// invalid UTF-8" (CON-110). "\xff" is a stray invalid byte.
	const invalid = "ok\xed\xa0\x80text\xff"
	if utf8.ValidString(invalid) {
		t.Fatal("fixture should be invalid UTF-8")
	}
	got := sanitizeUTF8(invalid)
	if !utf8.ValidString(got) {
		t.Fatalf("sanitizeUTF8 returned invalid UTF-8: %q", got)
	}

	// Valid input must pass through byte-for-byte unchanged.
	const valid = "hello 世界 🌍"
	if out := sanitizeUTF8(valid); out != valid {
		t.Fatalf("valid input altered: got %q, want %q", out, valid)
	}
}
