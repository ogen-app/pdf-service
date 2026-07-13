// Package pdfengine wraps klippa-app/go-pdfium: it opens a PDF and extracts
// per-page text, page count, and a first-page thumbnail.
//
// Backend: the native CGO build of pdfium (CON-103). Builds require CGO plus
// libpdfium discoverable through pkg-config (a pdfium.pc); see the Dockerfile
// and README. The single-threaded pool serialises every pdfium call (pdfium is
// not thread-safe), so concurrent requests queue at the engine.
package pdfengine

import (
	"bytes"
	"errors"
	"fmt"
	"image/png"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/klippa-app/go-pdfium"
	"github.com/klippa-app/go-pdfium/references"
	"github.com/klippa-app/go-pdfium/requests"
	"github.com/klippa-app/go-pdfium/single_threaded"

	"github.com/ogen-app/pdf-service/internal/chunk"
)

// ErrInvalidPDF marks a PDF that pdfium could not open (corrupt, encrypted, or
// not a PDF). The gRPC layer maps it to InvalidArgument so the client treats it
// as terminal (no retry).
var ErrInvalidPDF = errors.New("pdfengine: invalid or unreadable pdf")

const instanceTimeout = 60 * time.Second

// Engine owns a pdfium worker pool. pdfium is not thread-safe, so concurrent
// Extract/Render calls are bounded by the pool size and each runs on its own
// instance.
type Engine struct {
	pool pdfium.Pool
}

// Config tunes the engine.
type Config struct {
	// Workers is advisory: the single-threaded CGO backend serialises all
	// pdfium work through one native instance regardless. Kept for API
	// stability and a future multi_threaded backend.
	Workers int
}

// New initialises the pdfium pool (native, single-threaded).
func New(_ Config) (*Engine, error) {
	pool := single_threaded.Init(single_threaded.Config{})
	return &Engine{pool: pool}, nil
}

// Close tears down the pool.
func (e *Engine) Close() error {
	if e == nil || e.pool == nil {
		return nil
	}
	return e.pool.Close()
}

// ExtractResult is the output of a full parse.
type ExtractResult struct {
	Pages        []chunk.Page
	PageCount    int
	ThumbnailPNG []byte // empty if not requested / render failed
}

// Extract opens the PDF, returns per-page text + page count, and (when
// renderThumbnail) a first-page PNG at dpi (<=0 -> 96). Thumbnail failures are
// non-fatal — the rest of the result is still returned.
func (e *Engine) Extract(data []byte, renderThumbnail bool, dpi int) (*ExtractResult, error) {
	inst, err := e.pool.GetInstance(instanceTimeout)
	if err != nil {
		return nil, fmt.Errorf("pdfengine: get instance: %w", err)
	}
	defer inst.Close()

	doc, closeDoc, err := openDocument(inst, data)
	if err != nil {
		return nil, err
	}
	defer closeDoc()

	count, err := pageCount(inst, doc)
	if err != nil {
		return nil, err
	}

	pages := make([]chunk.Page, 0, count)
	for i := 0; i < count; i++ {
		txt, terr := inst.GetPageText(&requests.GetPageText{
			Page: requests.Page{ByIndex: &requests.PageByIndex{Document: doc, Index: i}},
		})
		if terr != nil {
			// Per-page text failures are non-fatal: record an empty page.
			pages = append(pages, chunk.Page{Num: i + 1})
			continue
		}
		pages = append(pages, chunk.Page{Num: i + 1, Text: sanitizeUTF8(strings.TrimSpace(txt.Text))})
	}

	res := &ExtractResult{Pages: pages, PageCount: count}
	if renderThumbnail && count > 0 {
		res.ThumbnailPNG = renderFirstPage(inst, doc, dpi)
	}
	return res, nil
}

// sanitizeUTF8 makes extracted text safe to place in a proto3 string field.
// pdfium hands back text as UTF-16 and, for glyphs that lack a proper ToUnicode
// mapping, can emit unpaired surrogates; the UTF-16->UTF-8 conversion then
// yields byte sequences that are not valid UTF-8. proto3 string fields must be
// valid UTF-8, so gRPC marshaling of such text fails with "string field
// contains invalid UTF-8" (CON-110). Replace each invalid run with U+FFFD.
func sanitizeUTF8(s string) string {
	if utf8.ValidString(s) {
		return s
	}
	return strings.ToValidUTF8(s, "�")
}

// RenderResult is page count + optional thumbnail (no text).
type RenderResult struct {
	PageCount    int
	ThumbnailPNG []byte
}

// Render opens the PDF and returns its page count and (when renderThumbnail) a
// first-page PNG — no text extraction.
func (e *Engine) Render(data []byte, renderThumbnail bool, dpi int) (*RenderResult, error) {
	inst, err := e.pool.GetInstance(instanceTimeout)
	if err != nil {
		return nil, fmt.Errorf("pdfengine: get instance: %w", err)
	}
	defer inst.Close()

	doc, closeDoc, err := openDocument(inst, data)
	if err != nil {
		return nil, err
	}
	defer closeDoc()

	count, err := pageCount(inst, doc)
	if err != nil {
		return nil, err
	}
	res := &RenderResult{PageCount: count}
	if renderThumbnail && count > 0 {
		res.ThumbnailPNG = renderFirstPage(inst, doc, dpi)
	}
	return res, nil
}

func openDocument(inst pdfium.Pdfium, data []byte) (doc references.FPDF_DOCUMENT, closeDoc func(), err error) {
	od, err := inst.OpenDocument(&requests.OpenDocument{File: &data})
	if err != nil {
		return doc, func() {}, fmt.Errorf("%w: %v", ErrInvalidPDF, err)
	}
	return od.Document, func() {
		_, _ = inst.FPDF_CloseDocument(&requests.FPDF_CloseDocument{Document: od.Document})
	}, nil
}

func pageCount(inst pdfium.Pdfium, doc references.FPDF_DOCUMENT) (int, error) {
	pc, err := inst.FPDF_GetPageCount(&requests.FPDF_GetPageCount{Document: doc})
	if err != nil {
		return 0, fmt.Errorf("pdfengine: page count: %w", err)
	}
	return pc.PageCount, nil
}

// renderFirstPage renders page 0 to a PNG; returns nil on any failure (thumbnail
// is best-effort).
func renderFirstPage(inst pdfium.Pdfium, doc references.FPDF_DOCUMENT, dpi int) []byte {
	if dpi <= 0 {
		dpi = 96
	}
	render, err := inst.RenderPageInDPI(&requests.RenderPageInDPI{
		DPI:  dpi,
		Page: requests.Page{ByIndex: &requests.PageByIndex{Document: doc, Index: 0}},
	})
	if err != nil {
		return nil
	}
	defer render.Cleanup()
	if render.Result.Image == nil {
		return nil
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, render.Result.Image); err != nil {
		return nil
	}
	return buf.Bytes()
}
