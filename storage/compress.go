package storage

import (
	"bytes"
	"compress/flate"
	"fmt"
	"io"
)

const (
	CompressionNone  = "none"
	CompressionFlate = "flate"
)

// CompressBytes compresses data with raw DEFLATE. The caller decides
// whether the result is worth keeping (see drivepool's uploadChunk) — this
// always returns the compressed form, even if it turns out larger.
func CompressBytes(data []byte) ([]byte, error) {
	var buf bytes.Buffer
	w, err := flate.NewWriter(&buf, flate.DefaultCompression)
	if err != nil {
		return nil, err
	}
	if _, err := w.Write(data); err != nil {
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// DecompressBytes reverses CompressBytes.
func DecompressBytes(data []byte) ([]byte, error) {
	r := flate.NewReader(bytes.NewReader(data))
	defer r.Close()
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, r); err != nil {
		return nil, fmt.Errorf("storage: decompress: %w", err)
	}
	return buf.Bytes(), nil
}
