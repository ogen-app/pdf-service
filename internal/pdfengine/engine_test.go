package pdfengine_test

import (
	"bytes"
	"errors"
	"fmt"
	"image/png"
	"os"
	"testing"

	"github.com/ogen-app/pdf-service/internal/pdfengine"
)

// eng is a shared engine — webassembly.Init compiles the embedded pdfium.wasm,
// which is too slow to do per test.
var eng *pdfengine.Engine

func TestMain(m *testing.M) {
	e, err := pdfengine.New(pdfengine.Config{Workers: 1})
	if err != nil {
		fmt.Fprintln(os.Stderr, "init engine:", err)
		os.Exit(1)
	}
	eng = e
	code := m.Run()
	_ = e.Close()
	os.Exit(code)
}

// buildPDF synthesises a structurally-valid n-page PDF with correct xref offsets
// (same fixture shape the Ogen pdfprobe tests use).
func buildPDF(n int) []byte {
	if n < 1 {
		n = 1
	}
	var buf bytes.Buffer
	buf.WriteString("%PDF-1.4\n%\xe2\xe3\xcf\xd3\n")

	offsets := make([]int, 0, 2+n)
	offsets = append(offsets, buf.Len())
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
	buf.WriteString(fmt.Sprintf("xref\n0 %d\n", totalObjs+1))
	buf.WriteString("0000000000 65535 f \n")
	for _, off := range offsets {
		buf.WriteString(fmt.Sprintf("%010d 00000 n \n", off))
	}
	buf.WriteString(fmt.Sprintf("trailer\n<< /Size %d /Root 1 0 R >>\n", totalObjs+1))
	buf.WriteString(fmt.Sprintf("startxref\n%d\n", xrefStart))
	buf.WriteString("%%EOF\n")
	return buf.Bytes()
}

func TestExtractReturnsPagesAndThumbnail(t *testing.T) {
	res, err := eng.Extract(buildPDF(2), true, 96)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if res.PageCount != 2 {
		t.Fatalf("page count = %d, want 2", res.PageCount)
	}
	if len(res.Pages) != 2 {
		t.Fatalf("len(pages) = %d, want 2", len(res.Pages))
	}
	if len(res.ThumbnailPNG) == 0 {
		t.Fatalf("expected a first-page thumbnail")
	}
	if _, err := png.Decode(bytes.NewReader(res.ThumbnailPNG)); err != nil {
		t.Fatalf("thumbnail is not a valid PNG: %v", err)
	}
}

func TestExtractWithoutThumbnail(t *testing.T) {
	res, err := eng.Extract(buildPDF(1), false, 0)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if res.PageCount != 1 {
		t.Fatalf("page count = %d, want 1", res.PageCount)
	}
	if len(res.ThumbnailPNG) != 0 {
		t.Fatalf("expected no thumbnail when not requested")
	}
}

func TestRenderReturnsPageCountAndThumbnail(t *testing.T) {
	res, err := eng.Render(buildPDF(3), true, 96)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if res.PageCount != 3 {
		t.Fatalf("page count = %d, want 3", res.PageCount)
	}
	if len(res.ThumbnailPNG) == 0 {
		t.Fatalf("expected a first-page thumbnail")
	}
}

func TestInvalidPDFIsTerminal(t *testing.T) {
	_, err := eng.Extract([]byte("this is definitely not a pdf"), false, 0)
	if !errors.Is(err, pdfengine.ErrInvalidPDF) {
		t.Fatalf("want ErrInvalidPDF, got %v", err)
	}
}
