package drivepool

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Souvik-223/rhino-framework/db/dbtest"
	"github.com/Souvik-223/rhino-framework/drivepool/gdrive"
	"github.com/Souvik-223/rhino-framework/drivepool/manifest"
	"github.com/Souvik-223/rhino-framework/storage"
)

// fakeStore is an in-memory RemoteStore so pool/placement logic can be
// tested without any real Drive account or network access. Guarded by a
// mutex because PutStream/GetStream upload and download chunks
// concurrently, and two chunks of the same file can legitimately land on
// the same account — a real Drive account has no shared local mutable
// state to race on (it's just independent HTTP calls), but this fake's map
// does, so it needs its own synchronization to correctly simulate that.
type fakeStore struct {
	mu         sync.Mutex
	limit      int64
	usage      int64
	files      map[string][]byte // remoteFileID -> ciphertext
	folders    map[string]bool   // folderID -> exists — Drive has no separate folder type, just another file ID
	next       int
	failUpload bool // if true, Upload always errors — used to test partial-failure cleanup
	deleted    []string
}

func newFakeStore(limit, usage int64) *fakeStore {
	return &fakeStore{limit: limit, usage: usage, files: make(map[string][]byte), folders: make(map[string]bool)}
}

func (f *fakeStore) EnsureFolder(ctx context.Context, name string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	id := "folder-" + name
	f.folders[id] = true
	return id, nil
}

func (f *fakeStore) Upload(ctx context.Context, name, folderID string, r io.ReaderAt, size int64) (string, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.failUpload {
		return "", "", fmt.Errorf("fakeStore: simulated upload failure")
	}
	buf := make([]byte, size)
	if _, err := r.ReadAt(buf, 0); err != nil && err != io.EOF {
		return "", "", err
	}
	f.next++
	id := name
	f.files[id] = buf
	f.usage += size
	return id, "", nil // empty md5 skips the upload-side integrity check in this fake
}

func (f *fakeStore) Download(ctx context.Context, remoteFileID string) (io.ReadCloser, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	b, ok := f.files[remoteFileID]
	if !ok {
		return nil, os.ErrNotExist
	}
	return io.NopCloser(bytes.NewReader(b)), nil
}

func (f *fakeStore) Delete(ctx context.Context, remoteFileID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	if _, ok := f.files[remoteFileID]; ok {
		delete(f.files, remoteFileID)
		f.deleted = append(f.deleted, remoteFileID)
		return nil
	}
	if _, ok := f.folders[remoteFileID]; ok {
		delete(f.folders, remoteFileID)
		f.deleted = append(f.deleted, remoteFileID)
		return nil
	}
	return fmt.Errorf("fakeStore: delete %q: %w", remoteFileID, gdrive.ErrNotFound)
}

func (f *fakeStore) Quota(ctx context.Context) (gdrive.QuotaInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	return gdrive.QuotaInfo{Limit: f.limit, Usage: f.usage}, nil
}

var _ gdrive.RemoteStore = (*fakeStore)(nil)

// newTestPool opens a Postgres transaction (rolled back automatically when
// the test finishes — see db/dbtest) scoped to a freshly seeded test user,
// and builds a Pool directly against it. Skips (not fails) if DATABASE_URL
// isn't set, since there's no local zero-config fallback anymore.
func newTestPool(t *testing.T) (*Pool, map[string]*fakeStore) {
	t.Helper()
	tx := dbtest.OpenTx(t)

	userID := storage.GenerateID()
	if err := tx.Exec(`INSERT INTO users (id, username, password_hash, created_at) VALUES (?, ?, ?, now())`,
		userID, userID, "unused").Error; err != nil {
		t.Fatalf("seed test user: %v", err)
	}

	m := manifest.New(tx, dbtest.TokenKey())
	p := &Pool{manifest: m, userID: userID, accounts: make(map[string]*Account)}
	return p, make(map[string]*fakeStore)
}

// addFakeAccount registers a fake account both in the in-memory pool (what
// placement/upload/download actually read) and in the manifest's accounts
// table (what RemoveAccount/GetAccount query) — a real Pool always has both
// in sync, and RemoveAccount's guard specifically depends on the manifest
// row existing.
func addFakeAccount(t *testing.T, p *Pool, id, label string, limit, usage int64) *fakeStore {
	t.Helper()
	fs := newFakeStore(limit, usage)
	if err := p.manifest.AddAccount(context.Background(), p.userID, manifest.Account{
		ID:      id,
		Label:   label,
		AddedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("add account %q to manifest: %v", label, err)
	}
	p.accounts[id] = &Account{ID: id, Label: label, Store: fs}
	return fs
}

func TestPickAccountPrefersMostFreeSpace(t *testing.T) {
	ctx := context.Background()
	p, _ := newTestPool(t)

	addFakeAccount(t, p, "acct-full", "full", 100, 95)            // 5 free
	roomy := addFakeAccount(t, p, "acct-roomy", "roomy", 100, 10) // 90 free
	addFakeAccount(t, p, "acct-mid", "mid", 100, 50)              // 50 free

	candidates := make([]*Account, 0, len(p.accounts))
	for _, a := range p.accounts {
		candidates = append(candidates, a)
	}

	chosen, err := pickAccount(ctx, candidates)
	if err != nil {
		t.Fatalf("pickAccount: %v", err)
	}
	if chosen.ID != "acct-roomy" {
		t.Errorf("want acct-roomy chosen, got %s", chosen.ID)
	}
	_ = roomy
}

func TestPickAccountSkipsUnhealthy(t *testing.T) {
	ctx := context.Background()
	p, _ := newTestPool(t)

	broken := &Account{ID: "broken", Label: "broken", initErr: os.ErrPermission}
	p.accounts["broken"] = broken
	addFakeAccount(t, p, "acct-ok", "ok", 100, 0)

	candidates := make([]*Account, 0, len(p.accounts))
	for _, a := range p.accounts {
		candidates = append(candidates, a)
	}

	chosen, err := pickAccount(ctx, candidates)
	if err != nil {
		t.Fatalf("pickAccount: %v", err)
	}
	if chosen.ID != "acct-ok" {
		t.Errorf("want acct-ok chosen, got %s", chosen.ID)
	}
}

func TestPickAccountNoneHealthy(t *testing.T) {
	ctx := context.Background()
	p, _ := newTestPool(t)
	p.accounts["broken"] = &Account{ID: "broken", initErr: os.ErrPermission}

	candidates := make([]*Account, 0, len(p.accounts))
	for _, a := range p.accounts {
		candidates = append(candidates, a)
	}

	if _, err := pickAccount(ctx, candidates); err != ErrNoHealthyAccounts {
		t.Errorf("want ErrNoHealthyAccounts, got %v", err)
	}
}

func TestPutGetRoundTrip(t *testing.T) {
	ctx := context.Background()
	p, _ := newTestPool(t)
	addFakeAccount(t, p, "acct-a", "a", 1<<30, 0)
	addFakeAccount(t, p, "acct-b", "b", 1<<30, 1<<20)

	srcPath := filepath.Join(t.TempDir(), "src.txt")
	want := []byte("hello from the drive pool test")
	if err := os.WriteFile(srcPath, want, 0o600); err != nil {
		t.Fatalf("write src: %v", err)
	}

	if err := p.Put(ctx, srcPath, "docs/hello.txt"); err != nil {
		t.Fatalf("Put: %v", err)
	}

	destPath := filepath.Join(t.TempDir(), "dest.txt")
	if err := p.Get(ctx, "docs/hello.txt", destPath); err != nil {
		t.Fatalf("Get: %v", err)
	}

	got, err := os.ReadFile(destPath)
	if err != nil {
		t.Fatalf("read dest: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("round trip mismatch: got %q want %q", got, want)
	}

	files, err := p.List(ctx, "")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(files) != 1 || files[0].Name != "docs/hello.txt" {
		t.Errorf("unexpected List result: %+v", files)
	}

	if err := p.Remove(ctx, "docs/hello.txt", false); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	files, err = p.List(ctx, "")
	if err != nil {
		t.Fatalf("List after remove: %v", err)
	}
	if len(files) != 0 {
		t.Errorf("expected no files after tombstone remove, got %+v", files)
	}
}

// TestRemovePurgeToleratesAlreadyDeletedRemoteChunk reproduces the bug
// report: a chunk deleted directly in Drive's own UI (outside this app)
// before the user also clicks delete here used to make the whole purge
// fail with "not found" and leave the virtual file (and every other
// chunk) stuck undeleted forever, even on retry. The remote delete call
// failing with "already gone" should be treated the same as success.
func TestRemovePurgeToleratesAlreadyDeletedRemoteChunk(t *testing.T) {
	ctx := context.Background()
	p, _ := newTestPool(t)
	store := addFakeAccount(t, p, "acct-a", "a", 1<<30, 0)

	srcPath := filepath.Join(t.TempDir(), "src.txt")
	if err := os.WriteFile(srcPath, []byte("data manually removed from drive first"), 0o600); err != nil {
		t.Fatalf("write src: %v", err)
	}
	if err := p.Put(ctx, srcPath, "docs/hello.txt"); err != nil {
		t.Fatalf("Put: %v", err)
	}

	vf, err := p.manifest.GetVirtualFile(ctx, p.userID, "docs/hello.txt")
	if err != nil {
		t.Fatalf("GetVirtualFile: %v", err)
	}
	chunks, err := p.manifest.ListChunks(ctx, p.userID, vf.ID)
	if err != nil {
		t.Fatalf("ListChunks: %v", err)
	}
	if len(chunks) != 1 {
		t.Fatalf("want 1 chunk, got %d", len(chunks))
	}

	// Simulate the user deleting the chunk's file directly in Drive's own
	// UI before ever clicking delete in this app.
	if err := store.Delete(ctx, chunks[0].RemoteFileID); err != nil {
		t.Fatalf("simulate manual drive deletion: %v", err)
	}

	if err := p.Remove(ctx, "docs/hello.txt", true); err != nil {
		t.Fatalf("Remove(purge=true) should tolerate an already-deleted remote chunk, got: %v", err)
	}

	if _, err := p.manifest.GetVirtualFile(ctx, p.userID, "docs/hello.txt"); !errors.Is(err, manifest.ErrNotFound) {
		t.Errorf("want the virtual file purged from the manifest, got err=%v", err)
	}
}

// TestRemovePurgeDeletesTheRemoteFolderToo reproduces the bug report: purge
// deleted each chunk's file but left the per-account folder it lived in
// behind, empty — orphaned hashed-name folders accumulating on Drive with
// every deleted file. Purge should remove the now-empty folder too.
func TestRemovePurgeDeletesTheRemoteFolderToo(t *testing.T) {
	ctx := context.Background()
	p, _ := newTestPool(t)
	store := addFakeAccount(t, p, "acct-a", "a", 1<<30, 0)

	srcPath := filepath.Join(t.TempDir(), "src.txt")
	if err := os.WriteFile(srcPath, []byte("data whose folder should be cleaned up too"), 0o600); err != nil {
		t.Fatalf("write src: %v", err)
	}
	if err := p.Put(ctx, srcPath, "docs/hello.txt"); err != nil {
		t.Fatalf("Put: %v", err)
	}

	vf, err := p.manifest.GetVirtualFile(ctx, p.userID, "docs/hello.txt")
	if err != nil {
		t.Fatalf("GetVirtualFile: %v", err)
	}
	chunks, err := p.manifest.ListChunks(ctx, p.userID, vf.ID)
	if err != nil {
		t.Fatalf("ListChunks: %v", err)
	}
	if len(chunks) != 1 {
		t.Fatalf("want 1 chunk, got %d", len(chunks))
	}
	folderID := chunks[0].RemoteFolderID

	store.mu.Lock()
	_, existsBefore := store.folders[folderID]
	store.mu.Unlock()
	if !existsBefore {
		t.Fatalf("test setup: want folder %q to exist on the fake store before Remove", folderID)
	}

	if err := p.Remove(ctx, "docs/hello.txt", true); err != nil {
		t.Fatalf("Remove(purge=true): %v", err)
	}

	store.mu.Lock()
	_, existsAfter := store.folders[folderID]
	store.mu.Unlock()
	if existsAfter {
		t.Errorf("want folder %q deleted along with its chunk, but it's still there", folderID)
	}
}

// TestRemoveAccountBlocksWhenChunksActive reproduces the bug report: upload
// a file, disconnect the account holding its only chunk, then try to
// download it. Without the guard, RemoveAccount would silently succeed and
// the later Get would fail with "account unavailable" — a file that looked
// healthy right up until someone disconnected the wrong drive. The guard
// should refuse removal up front instead.
func TestRemoveAccountBlocksWhenChunksActive(t *testing.T) {
	ctx := context.Background()
	p, _ := newTestPool(t)
	addFakeAccount(t, p, "acct-a", "a", 1<<30, 0)

	srcPath := filepath.Join(t.TempDir(), "src.txt")
	if err := os.WriteFile(srcPath, []byte("data that would be lost"), 0o600); err != nil {
		t.Fatalf("write src: %v", err)
	}
	if err := p.Put(ctx, srcPath, "docs/hello.txt"); err != nil {
		t.Fatalf("Put: %v", err)
	}

	err := p.RemoveAccount(ctx, "a", false)
	if !errors.Is(err, ErrAccountInUse) {
		t.Fatalf("want ErrAccountInUse, got %v", err)
	}

	if _, ok := p.accounts["acct-a"]; !ok {
		t.Error("account should still be registered after a blocked removal")
	}
	if _, err := p.manifest.GetAccount(ctx, p.userID, "a"); err != nil {
		t.Errorf("account should still be in the manifest after a blocked removal: %v", err)
	}

	destPath := filepath.Join(t.TempDir(), "dest.txt")
	if err := p.Get(ctx, "docs/hello.txt", destPath); err != nil {
		t.Errorf("file should still be downloadable after a blocked removal: %v", err)
	}
}

// TestRemoveAccountForceOrphansAndMarksDegraded exercises the explicit
// escape hatch: force=true still removes the account (matching the CLI's
// documented "does not delete its remote files" behavior), and the file's
// chunk is left pointing at a since-removed account. ListWithAccounts must
// surface that as Degraded rather than silently showing a bare account ID.
func TestRemoveAccountForceOrphansAndMarksDegraded(t *testing.T) {
	ctx := context.Background()
	p, _ := newTestPool(t)
	addFakeAccount(t, p, "acct-a", "a", 1<<30, 0)

	srcPath := filepath.Join(t.TempDir(), "src.txt")
	if err := os.WriteFile(srcPath, []byte("data that becomes orphaned"), 0o600); err != nil {
		t.Fatalf("write src: %v", err)
	}
	if err := p.Put(ctx, srcPath, "docs/hello.txt"); err != nil {
		t.Fatalf("Put: %v", err)
	}

	if err := p.RemoveAccount(ctx, "a", true); err != nil {
		t.Fatalf("force RemoveAccount: %v", err)
	}
	if _, ok := p.accounts["acct-a"]; ok {
		t.Error("account should be gone from the in-memory pool after a forced removal")
	}

	files, err := p.ListWithAccounts(ctx, "")
	if err != nil {
		t.Fatalf("ListWithAccounts: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("want 1 file, got %d", len(files))
	}
	if !files[0].Degraded {
		t.Error("file should be marked Degraded once its only account is gone")
	}
	if got := files[0].Accounts; len(got) != 1 || got[0] != "disconnected" {
		t.Errorf("want [\"disconnected\"] as the account label, got %v", got)
	}
}

// TestRemoveAccountAllowedWithoutActiveChunks confirms the guard doesn't
// block the common, safe case: an account nothing currently depends on.
func TestRemoveAccountAllowedWithoutActiveChunks(t *testing.T) {
	ctx := context.Background()
	p, _ := newTestPool(t)
	addFakeAccount(t, p, "acct-a", "a", 1<<30, 0)

	if err := p.RemoveAccount(ctx, "a", false); err != nil {
		t.Fatalf("RemoveAccount on an unused account should succeed, got: %v", err)
	}
	if _, err := p.manifest.GetAccount(ctx, p.userID, "a"); !errors.Is(err, manifest.ErrNotFound) {
		t.Errorf("want manifest.ErrNotFound after removal, got %v", err)
	}
}

// TestPoolsAreTenantIsolated confirms two Pools scoped to different users
// never see each other's accounts or files, even when both exist in the
// same (shared, transaction-scoped) database — the multi-tenant safety
// property every manifest query depends on scoped() to provide.
func TestPoolsAreTenantIsolated(t *testing.T) {
	ctx := context.Background()
	pa, _ := newTestPool(t)
	addFakeAccount(t, pa, "acct-a", "alices-drive", 1<<30, 0)
	if err := pa.PutStream(ctx, bytes.NewReader([]byte("alice's data")), 12, "secret.txt"); err != nil {
		t.Fatalf("PutStream as user A: %v", err)
	}

	// A second Pool, same underlying tx/db, but scoped to a different user
	// (never seeded into users — reads don't need the FK to be satisfied,
	// only writes do, and this Pool only reads).
	pb := &Pool{manifest: pa.manifest, userID: storage.GenerateID(), accounts: make(map[string]*Account)}

	files, err := pb.List(ctx, "")
	if err != nil {
		t.Fatalf("List as user B: %v", err)
	}
	if len(files) != 0 {
		t.Errorf("user B should see 0 files, got %d: %+v", len(files), files)
	}

	if _, err := pb.manifest.GetVirtualFile(ctx, pb.userID, "secret.txt"); !errors.Is(err, manifest.ErrNotFound) {
		t.Errorf("user B GetVirtualFile: want ErrNotFound, got %v", err)
	}
	statuses, err := pb.ListAccountStatus(ctx)
	if err != nil {
		t.Fatalf("ListAccountStatus as user B: %v", err)
	}
	if len(statuses) != 0 {
		t.Errorf("user B should see 0 accounts, got %d: %+v", len(statuses), statuses)
	}
}
