package storage

import (
	"bytes"
	"crypto/rand"
	"testing"
)

func TestCompressBytesDecompressBytesRoundTrip(t *testing.T) {
	payload := bytes.Repeat([]byte("hello rhino "), 100)

	compressed, err := CompressBytes(payload)
	if err != nil {
		t.Fatalf("CompressBytes: %v", err)
	}
	if len(compressed) >= len(payload) {
		t.Errorf("want compressible payload to shrink, got %d compressed vs %d original", len(compressed), len(payload))
	}

	out, err := DecompressBytes(compressed)
	if err != nil {
		t.Fatalf("DecompressBytes: %v", err)
	}
	if !bytes.Equal(out, payload) {
		t.Errorf("round trip mismatch: got %q want %q", out, payload)
	}
}

func TestCompressBytesRandomDataRoundTripsEvenWithoutShrinking(t *testing.T) {
	payload := make([]byte, 4096)
	if _, err := rand.Read(payload); err != nil {
		t.Fatalf("rand.Read: %v", err)
	}

	compressed, err := CompressBytes(payload)
	if err != nil {
		t.Fatalf("CompressBytes: %v", err)
	}

	out, err := DecompressBytes(compressed)
	if err != nil {
		t.Fatalf("DecompressBytes: %v", err)
	}
	if !bytes.Equal(out, payload) {
		t.Errorf("round trip mismatch for random data")
	}
}

func TestCompressBytesEmptyInput(t *testing.T) {
	compressed, err := CompressBytes(nil)
	if err != nil {
		t.Fatalf("CompressBytes: %v", err)
	}
	out, err := DecompressBytes(compressed)
	if err != nil {
		t.Fatalf("DecompressBytes: %v", err)
	}
	if len(out) != 0 {
		t.Errorf("want empty output for empty input, got %q", out)
	}
}
