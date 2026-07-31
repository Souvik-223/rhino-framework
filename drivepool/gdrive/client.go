// Package gdrive is a thin wrapper around the Google Drive API v3 client,
// exposing only the small surface drivepool needs (RemoteStore).
package gdrive

import (
	"context"
	"fmt"
	"io"

	"golang.org/x/oauth2"
	"google.golang.org/api/drive/v3"
	"google.golang.org/api/option"
)

// DriveFileScope grants access only to files/folders this application
// itself creates — it never sees the user's pre-existing Drive content.
const DriveFileScope = drive.DriveFileScope

// QuotaInfo reports an account's Drive-wide storage quota. Limit/Usage are
// bytes; Unlimited is true for Workspace accounts where Drive reports no
// fixed limit.
type QuotaInfo struct {
	Limit     int64
	Usage     int64
	Unlimited bool
	PhotoURL  string
}

func (q QuotaInfo) Available() int64 {
	if q.Unlimited {
		return 1<<63 - 1
	}
	if q.Limit <= q.Usage {
		return 0
	}
	return q.Limit - q.Usage
}

// RemoteStore is the small surface drivepool's core logic depends on —
// deliberately independent of *drive.Service so pool logic can be tested
// against a fake implementation with no network calls.
type RemoteStore interface {
	EnsureFolder(ctx context.Context, name string) (folderID string, err error)
	Upload(ctx context.Context, name, folderID string, r io.ReaderAt, size int64) (remoteFileID, md5Checksum string, err error)
	Download(ctx context.Context, remoteFileID string) (io.ReadCloser, error)
	Delete(ctx context.Context, remoteFileID string) error
	Quota(ctx context.Context) (QuotaInfo, error)
}

type Client struct {
	srv *drive.Service
}

func NewClient(ctx context.Context, ts oauth2.TokenSource) (*Client, error) {
	srv, err := drive.NewService(ctx, option.WithTokenSource(ts))
	if err != nil {
		return nil, fmt.Errorf("gdrive: create service: %w", err)
	}
	return &Client{srv: srv}, nil
}

var _ RemoteStore = (*Client)(nil)

// Quota queries the account's storageQuota and profile photo via About.get
// in a single call. The Fields call is required — Drive API v3's
// partial-response mechanism returns nothing useful without it. Usage (not
// UsageInDrive) is what's actually counted against Limit, since Gmail/Photos
// share the same quota pool. user.photoLink needs no scope beyond whatever
// Drive scope is already granted — it's the same account info Drive's own
// UI shows, not a separate profile/userinfo API.
func (c *Client) Quota(ctx context.Context) (QuotaInfo, error) {
	about, err := c.srv.About.Get().Fields("storageQuota", "user").Context(ctx).Do()
	if err != nil {
		return QuotaInfo{}, fmt.Errorf("gdrive: quota: %w", err)
	}
	var photoURL string
	if about.User != nil {
		photoURL = about.User.PhotoLink
	}
	if about.StorageQuota == nil || about.StorageQuota.Limit == 0 {
		return QuotaInfo{Unlimited: true, PhotoURL: photoURL}, nil
	}
	return QuotaInfo{
		Limit:    about.StorageQuota.Limit,
		Usage:    about.StorageQuota.Usage,
		PhotoURL: photoURL,
	}, nil
}

// EnsureFolder finds (or creates) a top-level, non-trashed folder with the
// given name, used to group one virtual file's chunks together on Drive.
func (c *Client) EnsureFolder(ctx context.Context, name string) (string, error) {
	q := fmt.Sprintf("name = %q and mimeType = 'application/vnd.google-apps.folder' and trashed = false", name)
	list, err := c.srv.Files.List().Q(q).Fields("files(id)").Context(ctx).Do()
	if err != nil {
		return "", fmt.Errorf("gdrive: list folder %q: %w", name, err)
	}
	if len(list.Files) > 0 {
		return list.Files[0].Id, nil
	}

	folder, err := c.srv.Files.Create(&drive.File{
		Name:     name,
		MimeType: "application/vnd.google-apps.folder",
	}).Fields("id").Context(ctx).Do()
	if err != nil {
		return "", fmt.Errorf("gdrive: create folder %q: %w", name, err)
	}
	return folder.Id, nil
}

// Upload creates a new Drive file via resumable upload — the right choice
// for the hundred-MB-to-GB chunk sizes drivepool uses. r must support
// io.ReaderAt (a staged local file does).
func (c *Client) Upload(ctx context.Context, name, folderID string, r io.ReaderAt, size int64) (string, string, error) {
	f := &drive.File{Name: name, Parents: []string{folderID}}
	created, err := c.srv.Files.Create(f).
		Fields("id", "md5Checksum").
		ResumableMedia(ctx, r, size, "application/octet-stream").
		Do()
	if err != nil {
		return "", "", fmt.Errorf("gdrive: upload %q: %w", name, err)
	}
	return created.Id, created.Md5Checksum, nil
}

func (c *Client) Download(ctx context.Context, remoteFileID string) (io.ReadCloser, error) {
	resp, err := c.srv.Files.Get(remoteFileID).Context(ctx).Download()
	if err != nil {
		return nil, fmt.Errorf("gdrive: download %q: %w", remoteFileID, err)
	}
	return resp.Body, nil
}

func (c *Client) Delete(ctx context.Context, remoteFileID string) error {
	if err := c.srv.Files.Delete(remoteFileID).Context(ctx).Do(); err != nil {
		return fmt.Errorf("gdrive: delete %q: %w", remoteFileID, err)
	}
	return nil
}
