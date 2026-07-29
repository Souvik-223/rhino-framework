package drivepool

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/Souvik-223/rhino-framework/drivepool/gdrive"
	"github.com/Souvik-223/rhino-framework/drivepool/manifest"
)

// fakeStore is an in-memory RemoteStore so pool/placement logic can be
// tested without any real Drive account or network access.
type fakeStore struct {
	limit int64
	usage int64
	files map[string][]byte // remoteFileID -> ciphertext
	next  int
}

func newFakeStore(limit, usage int64) *fakeStore {
	return &fakeStore{limit: limit, usage: usage, files: make(map[string][]byte)}
}

func (f *fakeStore) EnsureFolder(ctx context.Context, name string) (string, error) {
	return "folder-" + name, nil
}

func (f *fakeStore) Upload(ctx context.Context, name, folderID string, r io.ReaderAt, size int64) (string, string, error) {
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
	b, ok := f.files[remoteFileID]
	if !ok {
		return nil, os.ErrNotExist
	}
	return io.NopCloser(bytes.NewReader(b)), nil
}

func (f *fakeStore) Delete(ctx context.Context, remoteFileID string) error {
	delete(f.files, remoteFileID)
	return nil
}

func (f *fakeStore) Quota(ctx context.Context) (gdrive.QuotaInfo, error) {
	return gdrive.QuotaInfo{Limit: f.limit, Usage: f.usage}, nil
}

var _ gdrive.RemoteStore = (*fakeStore)(nil)

func newTestPool(t *testing.T) (*Pool, map[string]*fakeStore) {
	t.Helper()
	m, err := manifest.Open(filepath.Join(t.TempDir(), "manifest.db"))
	if err != nil {
		t.Fatalf("open manifest: %v", err)
	}
	t.Cleanup(func() { m.Close() })

	p := &Pool{manifest: m, accounts: make(map[string]*Account)}
	return p, make(map[string]*fakeStore)
}

func addFakeAccount(t *testing.T, p *Pool, id, label string, limit, usage int64) *fakeStore {
	t.Helper()
	fs := newFakeStore(limit, usage)
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
