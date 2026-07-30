// Package testutil provides a small in-memory gdrive.RemoteStore fake for
// backend/tests, mirroring drivepool/pool_test.go's fakeStore — duplicated
// here rather than exported from drivepool, since that one's unexported
// and this is a different package with no need to reach into drivepool's
// internals beyond the drivepool.Account/drivepool.NewPoolForTesting seam.
package testutil

import (
	"bytes"
	"context"
	"io"

	"github.com/Souvik-223/rhino-framework/drivepool/gdrive"
)

type FakeStore struct {
	Limit int64
	Usage int64
	Files map[string][]byte // remoteFileID -> ciphertext
}

func NewFakeStore(limit, usage int64) *FakeStore {
	return &FakeStore{Limit: limit, Usage: usage, Files: make(map[string][]byte)}
}

func (f *FakeStore) EnsureFolder(ctx context.Context, name string) (string, error) {
	return "folder-" + name, nil
}

func (f *FakeStore) Upload(ctx context.Context, name, folderID string, r io.ReaderAt, size int64) (string, string, error) {
	buf := make([]byte, size)
	if _, err := r.ReadAt(buf, 0); err != nil && err != io.EOF {
		return "", "", err
	}
	f.Files[name] = buf
	f.Usage += size
	return name, "", nil // empty md5 skips the upload-side integrity check in this fake
}

func (f *FakeStore) Download(ctx context.Context, remoteFileID string) (io.ReadCloser, error) {
	b, ok := f.Files[remoteFileID]
	if !ok {
		return nil, io.ErrUnexpectedEOF
	}
	return io.NopCloser(bytes.NewReader(b)), nil
}

func (f *FakeStore) Delete(ctx context.Context, remoteFileID string) error {
	delete(f.Files, remoteFileID)
	return nil
}

func (f *FakeStore) Quota(ctx context.Context) (gdrive.QuotaInfo, error) {
	return gdrive.QuotaInfo{Limit: f.Limit, Usage: f.Usage}, nil
}

var _ gdrive.RemoteStore = (*FakeStore)(nil)
