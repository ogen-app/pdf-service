//go:build linux && cgo

package pdfengine

/*
#include <malloc.h>
*/
import "C"

// mallocTrim asks glibc to return free pages from every malloc arena to the OS.
// pdfium runs via CGO on whichever OS thread the Go scheduler picks, so its
// allocations spread across many per-thread arenas; after a document closes,
// glibc keeps the freed pages inside them as resident memory. malloc_trim(0)
// releases them (including free pages in the middle of an arena, not just the
// top).
func mallocTrim() { C.malloc_trim(0) }
