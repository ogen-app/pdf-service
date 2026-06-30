// Package server implements the pdf.v1.PdfService gRPC service on top of the
// pdfium engine.
package server

import (
	"errors"
	"io"
	"log"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pdfv1 "github.com/ogen-app/pdf-service/gen/pdf/v1"
	"github.com/ogen-app/pdf-service/internal/chunk"
	"github.com/ogen-app/pdf-service/internal/pdfengine"
)

// maxPDFBytes bounds the reassembled PDF held in memory (the Ogen API caps
// uploads at 100 MB; allow headroom).
const maxPDFBytes = 110 << 20

// Server implements pdfv1.PdfServiceServer.
type Server struct {
	pdfv1.UnimplementedPdfServiceServer
	engine *pdfengine.Engine
}

func New(engine *pdfengine.Engine) *Server { return &Server{engine: engine} }

// Parse reassembles the streamed PDF, extracts per-page text into page-aware
// chunks, counts pages, and renders a first-page thumbnail.
func (s *Server) Parse(stream pdfv1.PdfService_ParseServer) error {
	var opts *pdfv1.ParseOptions
	var data []byte
	for {
		req, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		switch p := req.Payload.(type) {
		case *pdfv1.ParseRequest_Options:
			opts = p.Options
		case *pdfv1.ParseRequest_Chunk:
			if len(data)+len(p.Chunk) > maxPDFBytes {
				return status.Errorf(codes.InvalidArgument, "pdf exceeds %d bytes", maxPDFBytes)
			}
			data = append(data, p.Chunk...)
		}
	}
	if opts == nil {
		opts = &pdfv1.ParseOptions{}
	}
	if len(data) == 0 {
		return status.Error(codes.InvalidArgument, "empty pdf")
	}

	res, err := s.engine.Extract(data, opts.GetRenderThumbnail(), int(opts.GetThumbnailDpi()))
	if err != nil {
		return mapEngineErr(err)
	}

	paged := chunk.Pages(res.Pages, chunk.Config{
		Target:  int(opts.GetChunkTargetChars()),
		Overlap: int(opts.GetChunkOverlapChars()),
		Max:     int(opts.GetChunkMaxChars()),
	})
	chunks := make([]*pdfv1.Chunk, 0, len(paged))
	for i, c := range paged {
		chunks = append(chunks, &pdfv1.Chunk{
			Index:     int32(i),
			Text:      c.Text,
			PageStart: int32(c.PageStart),
			PageEnd:   int32(c.PageEnd),
		})
	}
	log.Printf("parse: pages=%d chunks=%d thumb=%dB", res.PageCount, len(chunks), len(res.ThumbnailPNG))
	return stream.SendAndClose(&pdfv1.ParseResponse{
		PageCount:    int32(res.PageCount),
		Chunks:       chunks,
		ThumbnailPng: res.ThumbnailPNG,
	})
}

// Render reassembles the streamed PDF and returns its page count + optional
// first-page thumbnail, with no text extraction.
func (s *Server) Render(stream pdfv1.PdfService_RenderServer) error {
	var opts *pdfv1.RenderOptions
	var data []byte
	for {
		req, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		switch p := req.Payload.(type) {
		case *pdfv1.RenderRequest_Options:
			opts = p.Options
		case *pdfv1.RenderRequest_Chunk:
			if len(data)+len(p.Chunk) > maxPDFBytes {
				return status.Errorf(codes.InvalidArgument, "pdf exceeds %d bytes", maxPDFBytes)
			}
			data = append(data, p.Chunk...)
		}
	}
	if opts == nil {
		opts = &pdfv1.RenderOptions{}
	}
	if len(data) == 0 {
		return status.Error(codes.InvalidArgument, "empty pdf")
	}

	res, err := s.engine.Render(data, opts.GetRenderThumbnail(), int(opts.GetThumbnailDpi()))
	if err != nil {
		return mapEngineErr(err)
	}
	log.Printf("render: pages=%d thumb=%dB", res.PageCount, len(res.ThumbnailPNG))
	return stream.SendAndClose(&pdfv1.RenderResponse{
		PageCount:    int32(res.PageCount),
		ThumbnailPng: res.ThumbnailPNG,
	})
}

// mapEngineErr turns an unparseable PDF into a terminal InvalidArgument (so the
// client doesn't retry); everything else is Internal (transient).
func mapEngineErr(err error) error {
	if errors.Is(err, pdfengine.ErrInvalidPDF) {
		return status.Error(codes.InvalidArgument, err.Error())
	}
	return status.Error(codes.Internal, err.Error())
}
