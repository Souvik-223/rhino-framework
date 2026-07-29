// Package manifest persists the drivepool namespace: registered Drive
// accounts, virtual files, and the chunks that make them up, in a local
// SQLite database.
package manifest

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

var ErrNotFound = errors.New("manifest: not found")

type Manifest struct {
	db *sql.DB
}

// Open creates (or opens) the SQLite database at path, applying the schema.
func Open(path string) (*Manifest, error) {
	if dir := filepath.Dir(path); dir != "." {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, fmt.Errorf("manifest: create dir: %w", err)
		}
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("manifest: open: %w", err)
	}
	db.SetMaxOpenConns(1) // modernc.org/sqlite is not safe for concurrent writers on one *DB

	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("manifest: apply schema: %w", err)
	}

	return &Manifest{db: db}, nil
}

func (m *Manifest) Close() error {
	return m.db.Close()
}

// Path returns the SQLite file's location, following the OS config-dir
// convention (%APPDATA%\rhino\manifest.db on Windows).
func Path() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "rhino", "manifest.db"), nil
}

// --- accounts ---

type Account struct {
	ID             string
	Label          string
	TokenPath      string
	AddedAt        time.Time
	QuotaLimit     int64 // 0 means unlimited or not-yet-queried; see QuotaKnown
	QuotaUsage     int64
	QuotaKnown     bool
	QuotaCheckedAt time.Time
}

func (m *Manifest) AddAccount(ctx context.Context, a Account) error {
	_, err := m.db.ExecContext(ctx, `
		INSERT INTO accounts (id, label, token_path, added_at)
		VALUES (?, ?, ?, ?)`,
		a.ID, a.Label, a.TokenPath, a.AddedAt.UTC())
	return err
}

func (m *Manifest) RemoveAccount(ctx context.Context, label string) error {
	res, err := m.db.ExecContext(ctx, `DELETE FROM accounts WHERE label = ?`, label)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (m *Manifest) GetAccount(ctx context.Context, label string) (Account, error) {
	row := m.db.QueryRowContext(ctx, `
		SELECT id, label, token_path, added_at, quota_limit, quota_usage, quota_checked_at
		FROM accounts WHERE label = ?`, label)
	return scanAccount(row)
}

func (m *Manifest) ListAccounts(ctx context.Context) ([]Account, error) {
	rows, err := m.db.QueryContext(ctx, `
		SELECT id, label, token_path, added_at, quota_limit, quota_usage, quota_checked_at
		FROM accounts ORDER BY label`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Account
	for rows.Next() {
		a, err := scanAccount(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (m *Manifest) UpdateAccountQuota(ctx context.Context, id string, limit, usage int64, checkedAt time.Time) error {
	_, err := m.db.ExecContext(ctx, `
		UPDATE accounts SET quota_limit = ?, quota_usage = ?, quota_checked_at = ? WHERE id = ?`,
		limit, usage, checkedAt.UTC(), id)
	return err
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanAccount(row rowScanner) (Account, error) {
	var (
		a          Account
		quotaLimit sql.NullInt64
		quotaUsage sql.NullInt64
		checkedAt  sql.NullTime
	)
	if err := row.Scan(&a.ID, &a.Label, &a.TokenPath, &a.AddedAt, &quotaLimit, &quotaUsage, &checkedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Account{}, ErrNotFound
		}
		return Account{}, err
	}
	a.QuotaKnown = quotaLimit.Valid
	a.QuotaLimit = quotaLimit.Int64
	a.QuotaUsage = quotaUsage.Int64
	a.QuotaCheckedAt = checkedAt.Time
	return a, nil
}

// --- virtual files ---

type VirtualFile struct {
	ID          int64
	Name        string
	Size        int64
	ContentHash string
	ChunkSize   int64
	FileKey     []byte
	Replicas    int
	Version     int
	Status      string // "complete" | "incomplete" | "deleted"
	CreatedAt   time.Time
	ModifiedAt  time.Time
}

const (
	StatusComplete   = "complete"
	StatusIncomplete = "incomplete"
	StatusDeleted    = "deleted"
)

func (m *Manifest) CreateVirtualFile(ctx context.Context, vf *VirtualFile) error {
	now := time.Now().UTC()
	vf.CreatedAt, vf.ModifiedAt = now, now
	if vf.Replicas == 0 {
		vf.Replicas = 1
	}
	if vf.Version == 0 {
		vf.Version = 1
	}

	res, err := m.db.ExecContext(ctx, `
		INSERT INTO virtual_files (name, size, content_hash, chunk_size, file_key, replicas, version, status, created_at, modified_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		vf.Name, vf.Size, vf.ContentHash, vf.ChunkSize, vf.FileKey, vf.Replicas, vf.Version, vf.Status, vf.CreatedAt, vf.ModifiedAt)
	if err != nil {
		return err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return err
	}
	vf.ID = id
	return nil
}

func (m *Manifest) SetVirtualFileStatus(ctx context.Context, id int64, status string) error {
	_, err := m.db.ExecContext(ctx, `
		UPDATE virtual_files SET status = ?, modified_at = ? WHERE id = ?`,
		status, time.Now().UTC(), id)
	return err
}

func (m *Manifest) GetVirtualFile(ctx context.Context, name string) (VirtualFile, error) {
	row := m.db.QueryRowContext(ctx, `
		SELECT id, name, size, content_hash, chunk_size, file_key, replicas, version, status, created_at, modified_at
		FROM virtual_files WHERE name = ?`, name)
	return scanVirtualFile(row)
}

func (m *Manifest) ListVirtualFiles(ctx context.Context, prefix string) ([]VirtualFile, error) {
	rows, err := m.db.QueryContext(ctx, `
		SELECT id, name, size, content_hash, chunk_size, file_key, replicas, version, status, created_at, modified_at
		FROM virtual_files WHERE name LIKE ? || '%' AND status != ? ORDER BY name`,
		prefix, StatusDeleted)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []VirtualFile
	for rows.Next() {
		vf, err := scanVirtualFile(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, vf)
	}
	return out, rows.Err()
}

func (m *Manifest) DeleteVirtualFile(ctx context.Context, name string, purge bool) error {
	if purge {
		_, err := m.db.ExecContext(ctx, `DELETE FROM virtual_files WHERE name = ?`, name)
		return err
	}
	res, err := m.db.ExecContext(ctx, `
		UPDATE virtual_files SET status = ?, modified_at = ? WHERE name = ?`,
		StatusDeleted, time.Now().UTC(), name)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func scanVirtualFile(row rowScanner) (VirtualFile, error) {
	var vf VirtualFile
	if err := row.Scan(&vf.ID, &vf.Name, &vf.Size, &vf.ContentHash, &vf.ChunkSize, &vf.FileKey,
		&vf.Replicas, &vf.Version, &vf.Status, &vf.CreatedAt, &vf.ModifiedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return VirtualFile{}, ErrNotFound
		}
		return VirtualFile{}, err
	}
	return vf, nil
}

// --- chunks ---

type Chunk struct {
	ID              int64
	VirtualFileID   int64
	Index           int
	AccountID       string
	RemoteFileID    string
	RemoteFolderID  string
	PlaintextSize   int64
	PlaintextSHA256 string
	CiphertextMD5   string
	UploadedAt      time.Time
}

func (m *Manifest) AddChunk(ctx context.Context, c *Chunk) error {
	c.UploadedAt = time.Now().UTC()
	res, err := m.db.ExecContext(ctx, `
		INSERT INTO chunks (virtual_file_id, idx, account_id, remote_file_id, remote_folder_id, plaintext_size, plaintext_sha256, ciphertext_md5, uploaded_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		c.VirtualFileID, c.Index, c.AccountID, c.RemoteFileID, c.RemoteFolderID, c.PlaintextSize, c.PlaintextSHA256, c.CiphertextMD5, c.UploadedAt)
	if err != nil {
		return err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return err
	}
	c.ID = id
	return nil
}

func (m *Manifest) ListChunks(ctx context.Context, virtualFileID int64) ([]Chunk, error) {
	rows, err := m.db.QueryContext(ctx, `
		SELECT id, virtual_file_id, idx, account_id, remote_file_id, remote_folder_id, plaintext_size, plaintext_sha256, ciphertext_md5, uploaded_at
		FROM chunks WHERE virtual_file_id = ? ORDER BY idx`, virtualFileID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Chunk
	for rows.Next() {
		var c Chunk
		if err := rows.Scan(&c.ID, &c.VirtualFileID, &c.Index, &c.AccountID, &c.RemoteFileID, &c.RemoteFolderID,
			&c.PlaintextSize, &c.PlaintextSHA256, &c.CiphertextMD5, &c.UploadedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
