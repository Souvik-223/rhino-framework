package drivepool

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"github.com/Souvik-223/rhino-framework/drivepool/manifest"
	"github.com/Souvik-223/rhino-framework/storage"
)

func TestPutStreamCompressesCompressibleData(t *testing.T) {
	ctx := context.Background()
	p, _ := newTestPool(t)
	p.ChunkSize = 50
	addFakeAccount(t, p, "acct-a", "a", 1<<30, 0)

	want := bytes.Repeat([]byte("rhino rhino rhino rhino "), 10) // 240 bytes, highly repetitive

	if err := p.PutStream(ctx, bytes.NewReader(want), int64(len(want)), "compressible.txt"); err != nil {
		t.Fatalf("PutStream: %v", err)
	}

	vf, err := p.manifest.GetVirtualFile(ctx, p.userID, "compressible.txt")
	if err != nil {
		t.Fatalf("GetVirtualFile: %v", err)
	}
	chunks, err := p.manifest.ListChunks(ctx, p.userID, vf.ID)
	if err != nil {
		t.Fatalf("ListChunks: %v", err)
	}
	if len(chunks) == 0 {
		t.Fatal("want at least one chunk")
	}

	sawCompressed := false
	for _, c := range chunks {
		if c.CompressionAlgo == storage.CompressionFlate {
			sawCompressed = true
			if c.CompressedSize >= c.PlaintextSize {
				t.Errorf("chunk %d: want compressed size < plaintext size, got %d >= %d", c.Index, c.CompressedSize, c.PlaintextSize)
			}
		}
	}
	if !sawCompressed {
		t.Error("want at least one chunk compressed for highly repetitive data")
	}

	var out bytes.Buffer
	if err := p.GetStream(ctx, "compressible.txt", &out); err != nil {
		t.Fatalf("GetStream: %v", err)
	}
	if !bytes.Equal(out.Bytes(), want) {
		t.Errorf("round trip mismatch: got %q want %q", out.Bytes(), want)
	}
}

func TestPutStreamSkipsCompressionForRandomData(t *testing.T) {
	ctx := context.Background()
	p, _ := newTestPool(t)
	p.ChunkSize = 50
	addFakeAccount(t, p, "acct-a", "a", 1<<30, 0)

	want := make([]byte, 200)
	if _, err := rand.Read(want); err != nil {
		t.Fatalf("rand.Read: %v", err)
	}

	if err := p.PutStream(ctx, bytes.NewReader(want), int64(len(want)), "random.bin"); err != nil {
		t.Fatalf("PutStream: %v", err)
	}

	vf, err := p.manifest.GetVirtualFile(ctx, p.userID, "random.bin")
	if err != nil {
		t.Fatalf("GetVirtualFile: %v", err)
	}
	chunks, err := p.manifest.ListChunks(ctx, p.userID, vf.ID)
	if err != nil {
		t.Fatalf("ListChunks: %v", err)
	}
	if len(chunks) == 0 {
		t.Fatal("want at least one chunk")
	}

	for _, c := range chunks {
		if c.CompressionAlgo != storage.CompressionNone {
			t.Errorf("chunk %d: want CompressionNone for incompressible data, got %q", c.Index, c.CompressionAlgo)
		}
		if c.CompressedSize != c.PlaintextSize {
			t.Errorf("chunk %d: want compressed size == plaintext size when uncompressed, got %d != %d", c.Index, c.CompressedSize, c.PlaintextSize)
		}
	}

	var out bytes.Buffer
	if err := p.GetStream(ctx, "random.bin", &out); err != nil {
		t.Fatalf("GetStream: %v", err)
	}
	if !bytes.Equal(out.Bytes(), want) {
		t.Errorf("round trip mismatch for random data")
	}
}

// TestGetStreamReadsPreCompressionChunkUnmodified simulates a chunk written
// by code that predates this feature: CompressionAlgo left at its Go zero
// value ("") and ciphertext produced by encrypting raw plaintext directly,
// with no compression step at all. downloadChunk must treat that the same
// as CompressionNone and return the plaintext unmodified.
func TestGetStreamReadsPreCompressionChunkUnmodified(t *testing.T) {
	ctx := context.Background()
	p, _ := newTestPool(t)
	store := addFakeAccount(t, p, "acct-a", "a", 1<<30, 0)

	fileKey := storage.NewEncryptionKey()
	plaintext := []byte("data written before compression existed")

	var ciphertext bytes.Buffer
	if _, err := storage.CopyEncrypt(fileKey, bytes.NewReader(plaintext), &ciphertext); err != nil {
		t.Fatalf("CopyEncrypt: %v", err)
	}
	store.files["legacy-chunk"] = ciphertext.Bytes()

	sum := sha256.Sum256(plaintext)
	plaintextHash := hex.EncodeToString(sum[:])
	accountID := "acct-a"

	vf := &manifest.VirtualFile{
		Name:        "legacy.bin",
		Size:        int64(len(plaintext)),
		ContentHash: plaintextHash,
		ChunkSize:   int64(len(plaintext)),
		FileKey:     fileKey,
		Status:      manifest.StatusComplete,
	}
	if err := p.manifest.CreateVirtualFile(ctx, p.userID, vf); err != nil {
		t.Fatalf("CreateVirtualFile: %v", err)
	}

	// CompressionAlgo/CompressedSize deliberately left unset (Go zero
	// values), simulating a row written by code that predates this column.
	if err := p.manifest.AddChunk(ctx, p.userID, &manifest.Chunk{
		VirtualFileID:   vf.ID,
		Index:           0,
		AccountID:       &accountID,
		RemoteFileID:    "legacy-chunk",
		RemoteFolderID:  "folder-legacy",
		PlaintextSize:   int64(len(plaintext)),
		PlaintextSHA256: plaintextHash,
		CiphertextMD5:   "unused-in-this-test",
	}); err != nil {
		t.Fatalf("AddChunk: %v", err)
	}

	var out bytes.Buffer
	if err := p.GetStream(ctx, "legacy.bin", &out); err != nil {
		t.Fatalf("GetStream: %v", err)
	}
	if !bytes.Equal(out.Bytes(), plaintext) {
		t.Errorf("want legacy chunk to round-trip unmodified, got %q want %q", out.Bytes(), plaintext)
	}
}
