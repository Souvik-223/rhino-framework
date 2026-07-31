package manifest

import (
	"context"
	"errors"
	"testing"

	"golang.org/x/oauth2"

	"github.com/Souvik-223/rhino-framework/db/dbtest"
	"github.com/Souvik-223/rhino-framework/storage"
)

// seedUser inserts a users row directly (manifest never touches the users
// table itself — see authdb) so tests can satisfy the user_id foreign key
// on accounts/virtual_files/chunks.
func seedUser(t *testing.T, m *Manifest) string {
	t.Helper()
	id := storage.GenerateID()
	if err := m.gdb.Exec(`INSERT INTO users (id, username, password_hash, created_at) VALUES (?, ?, ?, now())`,
		id, id, "unused").Error; err != nil {
		t.Fatalf("seed user: %v", err)
	}
	return id
}

func newTestManifest(t *testing.T) (*Manifest, string) {
	t.Helper()
	tx := dbtest.OpenTx(t)
	m := New(tx, dbtest.TokenKey())
	return m, seedUser(t, m)
}

func TestAccountTokenEncryptDecryptRoundTrip(t *testing.T) {
	ctx := context.Background()
	m, userID := newTestManifest(t)

	tok := &oauth2.Token{AccessToken: "access-123", RefreshToken: "refresh-456"}
	if err := m.AddAccount(ctx, userID, Account{ID: "acct-1", Label: "drive-a", Token: tok}); err != nil {
		t.Fatalf("AddAccount: %v", err)
	}

	got, err := m.GetAccount(ctx, userID, "drive-a")
	if err != nil {
		t.Fatalf("GetAccount: %v", err)
	}
	if got.Token.AccessToken != tok.AccessToken || got.Token.RefreshToken != tok.RefreshToken {
		t.Errorf("token round trip mismatch: got %+v want %+v", got.Token, tok)
	}
}

func TestAccountsAreTenantIsolated(t *testing.T) {
	ctx := context.Background()
	m, userA := newTestManifest(t)
	userB := seedUser(t, m)

	if err := m.AddAccount(ctx, userA, Account{ID: "acct-1", Label: "drive-a", Token: &oauth2.Token{}}); err != nil {
		t.Fatalf("AddAccount: %v", err)
	}

	if _, err := m.GetAccount(ctx, userB, "drive-a"); !errors.Is(err, ErrNotFound) {
		t.Errorf("cross-tenant GetAccount: want ErrNotFound, got %v", err)
	}
	list, err := m.ListAccounts(ctx, userB)
	if err != nil {
		t.Fatalf("ListAccounts: %v", err)
	}
	if len(list) != 0 {
		t.Errorf("cross-tenant ListAccounts: want 0, got %d", len(list))
	}
}

func TestVirtualFilesAreTenantIsolatedAndPerUserUnique(t *testing.T) {
	ctx := context.Background()
	m, userA := newTestManifest(t)
	userB := seedUser(t, m)

	vfA := &VirtualFile{Name: "doc.pdf", Size: 1, ContentHash: "h", ChunkSize: 1, FileKey: []byte("k"), Status: StatusComplete}
	if err := m.CreateVirtualFile(ctx, userA, vfA); err != nil {
		t.Fatalf("CreateVirtualFile userA: %v", err)
	}

	// Same name for a different user is fine — uniqueness is per-user.
	vfB := &VirtualFile{Name: "doc.pdf", Size: 1, ContentHash: "h2", ChunkSize: 1, FileKey: []byte("k"), Status: StatusComplete}
	if err := m.CreateVirtualFile(ctx, userB, vfB); err != nil {
		t.Fatalf("CreateVirtualFile userB with the same name should succeed: %v", err)
	}

	if _, err := m.GetVirtualFile(ctx, userB, "doc.pdf"); err != nil {
		t.Fatalf("userB GetVirtualFile: %v", err)
	}
	files, err := m.ListVirtualFiles(ctx, userA, "")
	if err != nil {
		t.Fatalf("ListVirtualFiles userA: %v", err)
	}
	if len(files) != 1 || files[0].ID != vfA.ID {
		t.Errorf("cross-tenant ListVirtualFiles: want only userA's file, got %+v", files)
	}
}

// TestCreateVirtualFileRejectsDuplicateNamePerUser is deliberately its own
// test rather than folded into the one above: Postgres aborts the whole
// transaction on the first failed statement and refuses every later
// command until it's rolled back (unlike SQLite, which doesn't) — so
// triggering the expected unique-constraint error here would poison any
// assertion that ran afterward in the same test's transaction. Each test
// function gets its own fresh transaction (db/dbtest.OpenTx), so this one
// deliberately failing doesn't affect any other test.
func TestCreateVirtualFileRejectsDuplicateNamePerUser(t *testing.T) {
	ctx := context.Background()
	m, userID := newTestManifest(t)

	vf := &VirtualFile{Name: "doc.pdf", Size: 1, ContentHash: "h", ChunkSize: 1, FileKey: []byte("k"), Status: StatusComplete}
	if err := m.CreateVirtualFile(ctx, userID, vf); err != nil {
		t.Fatalf("CreateVirtualFile: %v", err)
	}

	dup := &VirtualFile{Name: "doc.pdf", Size: 1, ContentHash: "h2", ChunkSize: 1, FileKey: []byte("k"), Status: StatusComplete}
	if err := m.CreateVirtualFile(ctx, userID, dup); !errors.Is(err, ErrNameExists) {
		t.Errorf("want ErrNameExists for a same-user duplicate, got %v", err)
	}
}

func TestDeletingVirtualFileCascadesChunks(t *testing.T) {
	ctx := context.Background()
	m, userID := newTestManifest(t)

	if err := m.AddAccount(ctx, userID, Account{ID: "acct-1", Label: "a", Token: &oauth2.Token{}}); err != nil {
		t.Fatalf("AddAccount: %v", err)
	}
	vf := &VirtualFile{Name: "f", Size: 1, ContentHash: "h", ChunkSize: 1, FileKey: []byte("k"), Status: StatusComplete}
	if err := m.CreateVirtualFile(ctx, userID, vf); err != nil {
		t.Fatalf("CreateVirtualFile: %v", err)
	}
	accountID := "acct-1"
	if err := m.AddChunk(ctx, userID, &Chunk{
		VirtualFileID: vf.ID, Index: 0, AccountID: &accountID,
		RemoteFileID: "rf", RemoteFolderID: "rfo", PlaintextSize: 1, PlaintextSHA256: "sha", CiphertextMD5: "md5",
	}); err != nil {
		t.Fatalf("AddChunk: %v", err)
	}

	if err := m.DeleteVirtualFile(ctx, userID, "f", true); err != nil {
		t.Fatalf("DeleteVirtualFile purge: %v", err)
	}

	chunks, err := m.ListChunks(ctx, userID, vf.ID)
	if err != nil {
		t.Fatalf("ListChunks: %v", err)
	}
	if len(chunks) != 0 {
		t.Errorf("want chunks cascade-deleted with their virtual file, got %d left", len(chunks))
	}
}

func TestRemovingAccountNullsChunkAccountID(t *testing.T) {
	ctx := context.Background()
	m, userID := newTestManifest(t)

	if err := m.AddAccount(ctx, userID, Account{ID: "acct-1", Label: "a", Token: &oauth2.Token{}}); err != nil {
		t.Fatalf("AddAccount: %v", err)
	}
	vf := &VirtualFile{Name: "f", Size: 1, ContentHash: "h", ChunkSize: 1, FileKey: []byte("k"), Status: StatusComplete}
	if err := m.CreateVirtualFile(ctx, userID, vf); err != nil {
		t.Fatalf("CreateVirtualFile: %v", err)
	}
	accountID := "acct-1"
	if err := m.AddChunk(ctx, userID, &Chunk{
		VirtualFileID: vf.ID, Index: 0, AccountID: &accountID,
		RemoteFileID: "rf", RemoteFolderID: "rfo", PlaintextSize: 1, PlaintextSHA256: "sha", CiphertextMD5: "md5",
	}); err != nil {
		t.Fatalf("AddChunk: %v", err)
	}

	if err := m.RemoveAccount(ctx, userID, "a"); err != nil {
		t.Fatalf("RemoveAccount: %v", err)
	}

	chunks, err := m.ListChunks(ctx, userID, vf.ID)
	if err != nil {
		t.Fatalf("ListChunks: %v", err)
	}
	if len(chunks) != 1 {
		t.Fatalf("want the chunk row to survive account removal, got %d", len(chunks))
	}
	if chunks[0].AccountID != nil {
		t.Errorf("want AccountID cleared to nil after ON DELETE SET NULL, got %q", *chunks[0].AccountID)
	}
}

func TestCountActiveChunksExcludesDeletedFiles(t *testing.T) {
	ctx := context.Background()
	m, userID := newTestManifest(t)

	if err := m.AddAccount(ctx, userID, Account{ID: "acct-1", Label: "a", Token: &oauth2.Token{}}); err != nil {
		t.Fatalf("AddAccount: %v", err)
	}
	vf := &VirtualFile{Name: "f", Size: 1, ContentHash: "h", ChunkSize: 1, FileKey: []byte("k"), Status: StatusComplete}
	if err := m.CreateVirtualFile(ctx, userID, vf); err != nil {
		t.Fatalf("CreateVirtualFile: %v", err)
	}
	accountID := "acct-1"
	if err := m.AddChunk(ctx, userID, &Chunk{
		VirtualFileID: vf.ID, Index: 0, AccountID: &accountID,
		RemoteFileID: "rf", RemoteFolderID: "rfo", PlaintextSize: 1, PlaintextSHA256: "sha", CiphertextMD5: "md5",
	}); err != nil {
		t.Fatalf("AddChunk: %v", err)
	}

	n, err := m.CountActiveChunks(ctx, userID, "acct-1")
	if err != nil {
		t.Fatalf("CountActiveChunks: %v", err)
	}
	if n != 1 {
		t.Fatalf("want 1 active chunk, got %d", n)
	}

	if err := m.DeleteVirtualFile(ctx, userID, "f", false); err != nil {
		t.Fatalf("soft delete: %v", err)
	}
	n, err = m.CountActiveChunks(ctx, userID, "acct-1")
	if err != nil {
		t.Fatalf("CountActiveChunks after soft delete: %v", err)
	}
	if n != 0 {
		t.Errorf("want 0 active chunks once the file is soft-deleted, got %d", n)
	}
}
