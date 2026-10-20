//go:build !linux || !cgo

package pdfengine

// mallocTrim is a no-op off glibc/Linux (e.g. local macOS builds), where
// malloc_trim does not exist.
func mallocTrim() {}
