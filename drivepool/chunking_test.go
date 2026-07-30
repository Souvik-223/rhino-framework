package drivepool

import (
	"bytes"
	"context"
	"testing"

	"github.com/Souvik-223/rhino-framework/drivepool/manifest"
)

func TestPutStreamSplitsAcrossAccountsAndRoundTrips(t *testing.T) {
	ctx := context.Background()
	p, _ := newTestPool(t)
	p.ChunkSize = 10 // force many small chunks instead of the 128MiB default

	acctA := addFakeAccount(t, p, "acct-a", "a", 25, 0) // 25 free
	acctB := addFakeAccount(t, p, "acct-b", "b", 20, 0) // 20 free

	want := bytes.Repeat([]byte("0123456789"), 4) // 40 bytes -> 4 chunks of 10

	if err := p.PutStream(ctx, bytes.NewReader(want), int64(len(want)), "big.bin"); err != nil {
		t.Fatalf("PutStream: %v", err)
	}

	vf, err := p.manifest.GetVirtualFile(ctx, "big.bin")
	if err != nil {
		t.Fatalf("GetVirtualFile: %v", err)
	}
	chunks, err := p.manifest.ListChunks(ctx, vf.ID)
	if err != nil {
		t.Fatalf("ListChunks: %v", err)
	}
	if len(chunks) != 4 {
		t.Fatalf("want 4 chunks, got %d", len(chunks))
	}

	usedAccounts := make(map[string]bool)
	for _, c := range chunks {
		usedAccounts[c.AccountID] = true
	}
	if !usedAccounts["acct-a"] || !usedAccounts["acct-b"] {
		t.Errorf("want chunks spread across both accounts, got accounts: %+v", usedAccounts)
	}
	_ = acctA
	_ = acctB

	var out bytes.Buffer
	if err := p.GetStream(ctx, "big.bin", &out); err != nil {
		t.Fatalf("GetStream: %v", err)
	}
	if !bytes.Equal(out.Bytes(), want) {
		t.Errorf("round trip mismatch: got %q want %q", out.Bytes(), want)
	}
}

func TestPutStreamFailureCleansUpUploadedChunks(t *testing.T) {
	ctx := context.Background()
	p, _ := newTestPool(t)
	p.ChunkSize = 10

	okStore := addFakeAccount(t, p, "acct-ok", "ok", 15, 0)
	badStore := addFakeAccount(t, p, "acct-bad", "bad", 12, 0)
	badStore.failUpload = true

	data := bytes.Repeat([]byte("x"), 30) // 3 chunks of 10: ok, bad (fails), ok

	err := p.PutStream(ctx, bytes.NewReader(data), int64(len(data)), "will-fail.bin")
	if err == nil {
		t.Fatal("want PutStream to fail when one chunk's upload fails")
	}

	if len(okStore.deleted) != 2 {
		t.Errorf("want the 2 chunks already uploaded to acct-ok cleaned up, got %d deletions: %+v", len(okStore.deleted), okStore.deleted)
	}
	if len(okStore.files) != 0 {
		t.Errorf("want no orphaned chunks left on acct-ok after cleanup, got %d", len(okStore.files))
	}

	if _, err := p.manifest.GetVirtualFile(ctx, "will-fail.bin"); err != manifest.ErrNotFound {
		t.Errorf("want no virtual file recorded after a failed Put, got err=%v", err)
	}
}

func TestGetStreamDetectsTamperedChunk(t *testing.T) {
	ctx := context.Background()
	p, _ := newTestPool(t)
	p.ChunkSize = 10
	store := addFakeAccount(t, p, "acct-a", "a", 1<<20, 0)

	data := bytes.Repeat([]byte("y"), 20) // 2 chunks
	if err := p.PutStream(ctx, bytes.NewReader(data), int64(len(data)), "tampered.bin"); err != nil {
		t.Fatalf("PutStream: %v", err)
	}

	// Corrupt one stored ciphertext chunk directly in the fake, simulating
	// bit-rot or an out-of-band modification on the remote side.
	for id, ciphertext := range store.files {
		corrupted := append([]byte(nil), ciphertext...)
		corrupted[len(corrupted)-1] ^= 0xFF
		store.files[id] = corrupted
		break
	}

	var out bytes.Buffer
	if err := p.GetStream(ctx, "tampered.bin", &out); err == nil {
		t.Fatal("want GetStream to fail on a tampered chunk, got nil error")
	}
}
