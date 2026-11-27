// Package zipbest writes zip archives (cbz, epub) with Deflate at its maximum compression level, so every writer of
// one packs alike.
package zipbest

import (
	"archive/zip"
	"compress/flate"
	"io"
)

// NewWriter is a zip writer whose Deflate entries use the best compression.
func NewWriter(out io.Writer) *zip.Writer {
	writer := zip.NewWriter(out)
	writer.RegisterCompressor(zip.Deflate, func(w io.Writer) (io.WriteCloser, error) { return flate.NewWriter(w, flate.BestCompression) })
	return writer
}
