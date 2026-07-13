// Package chunk turns per-page PDF text into embedding-sized, page-attributed
// chunks. Ported from the Ogen API's old src/pdf/chunker.go (CON-103) so
// chunking lives next to extraction in pdf-service.
package chunk

import (
	"strings"
	"unicode/utf8"
)

// Default chunk sizing — mirrors the Ogen embedder's input limits. Overridable
// per request via Config (the proto carries chunk_target/overlap/max options).
const (
	DefaultMax     = 6000
	DefaultTarget  = 5500
	DefaultOverlap = 500
)

// Page is the extracted plain text for a single PDF page (Num is 1-indexed).
type Page struct {
	Num  int
	Text string
}

// PagedChunk is a chunk of text with the page range it spans.
type PagedChunk struct {
	Text      string
	PageStart int
	PageEnd   int
}

// Config sets chunk sizing; non-positive fields fall back to the Default*
// constants.
type Config struct {
	Target  int
	Overlap int
	Max     int
}

func (c Config) resolve() (target, overlap, max int) {
	target, overlap, max = c.Target, c.Overlap, c.Max
	if target <= 0 {
		target = DefaultTarget
	}
	if overlap <= 0 {
		overlap = DefaultOverlap
	}
	if max <= 0 {
		max = DefaultMax
	}
	return target, overlap, max
}

type tagged struct {
	text string
	page int
}

// Pages splits the per-page plain text into embedding-sized chunks while
// preserving page boundaries:
//   - Each page is split into paragraphs (on "\n\n"), each tagged with its page.
//   - Paragraphs accumulate into chunks up to target chars; on overflow the
//     chunk is flushed and the next starts with the last `overlap` chars of the
//     previous (inheriting its ending page).
//   - A single paragraph longer than target is pre-split at word boundaries.
//   - PageStart/PageEnd track the first and last source page in each chunk.
func Pages(pages []Page, cfg Config) []PagedChunk {
	chunkTarget, chunkOverlap, maxEmbedChars := cfg.resolve()

	var paras []tagged
	for _, p := range pages {
		for _, pr := range strings.Split(strings.TrimSpace(p.Text), "\n\n") {
			pr = strings.TrimSpace(pr)
			if pr == "" {
				continue
			}
			for _, sub := range splitLargeAtWord(pr, chunkTarget) {
				paras = append(paras, tagged{text: sub, page: p.Num})
			}
		}
	}
	if len(paras) == 0 {
		return nil
	}

	// Short document: a single chunk spanning all pages that contributed text.
	totalLen := 0
	for _, pt := range paras {
		totalLen += len(pt.text) + 2
	}
	if totalLen <= maxEmbedChars {
		return []PagedChunk{joinAll(paras)}
	}

	var (
		out        []PagedChunk
		curBuf     strings.Builder
		curStart   int
		curEnd     int
		curStarted bool
	)

	flush := func() {
		t := strings.TrimSpace(curBuf.String())
		if t == "" {
			return
		}
		out = append(out, PagedChunk{Text: t, PageStart: curStart, PageEnd: curEnd})
	}

	for _, pt := range paras {
		if curStarted && curBuf.Len()+len(pt.text)+2 > chunkTarget {
			prev := curBuf.String()
			flush()

			curBuf.Reset()
			if len(prev) > chunkOverlap {
				// Snap the overlap start forward to a rune boundary: starting
				// mid-rune would leave orphaned continuation bytes and make the
				// chunk invalid UTF-8, which proto3 rejects (CON-110).
				start := len(prev) - chunkOverlap
				for start < len(prev) && !utf8.RuneStart(prev[start]) {
					start++
				}
				curBuf.WriteString(prev[start:])
				curBuf.WriteString("\n\n")
			}
			curStart = curEnd
		}

		if !curStarted || curBuf.Len() == 0 {
			curStart = pt.page
			curStarted = true
		}
		if curBuf.Len() > 0 {
			curBuf.WriteString("\n\n")
		}
		curBuf.WriteString(pt.text)
		curEnd = pt.page
	}
	flush()

	if len(out) == 0 {
		return []PagedChunk{joinAll(paras)}
	}
	return out
}

// joinAll concatenates every paragraph into one chunk spanning the full page
// range — used for short documents and as a fallback.
func joinAll(paras []tagged) PagedChunk {
	var b strings.Builder
	for i, pt := range paras {
		if i > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString(pt.text)
	}
	return PagedChunk{
		Text:      b.String(),
		PageStart: paras[0].page,
		PageEnd:   paras[len(paras)-1].page,
	}
}

func splitLargeAtWord(para string, target int) []string {
	if len(para) <= target {
		return []string{para}
	}
	var parts []string
	remaining := para
	for len(remaining) > target {
		boundary := target
		for boundary > 0 && remaining[boundary] != ' ' {
			boundary--
		}
		if boundary == 0 {
			// No space within the first target bytes: hard-split at target, but
			// back up to a rune boundary so we don't cut a multi-byte rune in
			// half and emit invalid UTF-8 (CON-110). A rune is at most 4 bytes,
			// so this steps back at most 3.
			boundary = target
			for boundary > 0 && !utf8.RuneStart(remaining[boundary]) {
				boundary--
			}
			if boundary == 0 {
				boundary = target
			}
		}
		parts = append(parts, strings.TrimSpace(remaining[:boundary]))
		remaining = strings.TrimSpace(remaining[boundary:])
	}
	if len(remaining) > 0 {
		parts = append(parts, remaining)
	}
	return parts
}
