// Package manifest persists the drivepool namespace — registered Drive
// accounts, virtual files, and the chunks that make them up — in Postgres,
// shared by every portal user and the CLI alike. Every per-user table
// carries a user_id column; see scoped below for how that boundary is
// enforced on every query.
package manifest

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"golang.org/x/oauth2"
	"gorm.io/gorm"

	rhinodb "github.com/Souvik-223/rhino-framework/db"
	"github.com/Souvik-223/rhino-framework/storage"
)

var (
	ErrNotFound   = errors.New("manifest: not found")
	ErrNameExists = errors.New("manifest: name already exists")
)

type Manifest struct {
	gdb      *gorm.DB
	tokenKey []byte
}

// New wraps an already-open, already-migrated *gorm.DB (see db.Open /
// db.Migrate) and a 32-byte key used to encrypt OAuth tokens at rest
// (storage.CopyEncrypt/CopyDecrypt, AES-256-CTR) — see RHINO_TOKEN_ENCRYPTION_KEY.
func New(gdb *gorm.DB, tokenEncryptionKey []byte) *Manifest {
	return &Manifest{gdb: gdb, tokenKey: tokenEncryptionKey}
}

// scoped returns gdb pre-filtered to userID's rows, so every query built
// from it is tenant-isolated by construction. Every method that reads,
// updates, or deletes rows in a per-user table must start from this —
// never m.gdb directly — so a missing filter can't slip in silently.
// Panics on an empty userID rather than silently matching zero rows: an
// empty string here means a caller forgot to resolve identity, and that
// should fail loudly in dev/CI, not look like "no data yet" in production.
func (m *Manifest) scoped(ctx context.Context, userID string) *gorm.DB {
	if userID == "" {
		panic("manifest: scoped called with empty userID")
	}
	return m.gdb.WithContext(ctx).Where("user_id = ?", userID)
}

func (m *Manifest) encryptToken(tok *oauth2.Token) ([]byte, error) {
	plaintext, err := json.Marshal(tok)
	if err != nil {
		return nil, fmt.Errorf("manifest: marshal token: %w", err)
	}
	var ciphertext bytes.Buffer
	if _, err := storage.CopyEncrypt(m.tokenKey, bytes.NewReader(plaintext), &ciphertext); err != nil {
		return nil, fmt.Errorf("manifest: encrypt token: %w", err)
	}
	return ciphertext.Bytes(), nil
}

func (m *Manifest) decryptToken(ciphertext []byte) (*oauth2.Token, error) {
	var plaintext bytes.Buffer
	if _, err := storage.CopyDecrypt(m.tokenKey, bytes.NewReader(ciphertext), &plaintext); err != nil {
		return nil, fmt.Errorf("manifest: decrypt token: %w", err)
	}
	var tok oauth2.Token
	if err := json.Unmarshal(plaintext.Bytes(), &tok); err != nil {
		return nil, fmt.Errorf("manifest: unmarshal token: %w", err)
	}
	return &tok, nil
}

// --- accounts ---

func (m *Manifest) AddAccount(ctx context.Context, userID string, a Account) error {
	ciphertext, err := m.encryptToken(a.Token)
	if err != nil {
		return err
	}
	a.UserID = userID
	a.TokenCiphertext = ciphertext
	return m.gdb.WithContext(ctx).Create(&a).Error
}

func (m *Manifest) RemoveAccount(ctx context.Context, userID, label string) error {
	res := m.scoped(ctx, userID).Where("label = ?", label).Delete(&Account{})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

func (m *Manifest) GetAccount(ctx context.Context, userID, label string) (Account, error) {
	var a Account
	err := m.scoped(ctx, userID).Where("label = ?", label).First(&a).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return Account{}, ErrNotFound
	}
	if err != nil {
		return Account{}, err
	}
	tok, err := m.decryptToken(a.TokenCiphertext)
	if err != nil {
		return Account{}, err
	}
	a.Token = tok
	return a, nil
}

// ListAccounts decrypts every row's token, but a single account's
// undecryptable token (e.g. after a key rotation with no re-encryption
// migration) doesn't fail the whole call — its Token is left nil and the
// caller (Pool.buildAccount) degrades just that one account, matching the
// same graceful-degradation contract initErr provides for other
// account-initialization failures.
func (m *Manifest) ListAccounts(ctx context.Context, userID string) ([]Account, error) {
	var accounts []Account
	if err := m.scoped(ctx, userID).Order("label").Find(&accounts).Error; err != nil {
		return nil, err
	}
	for i := range accounts {
		if tok, err := m.decryptToken(accounts[i].TokenCiphertext); err == nil {
			accounts[i].Token = tok
		}
	}
	return accounts, nil
}

// CountActiveChunks reports how many chunks belonging to non-deleted
// virtual files currently reference accountID. Used to block removing an
// account that's the only known copy of some file's data.
//
// This is the one query in this package that doesn't route through
// scoped(): the JOIN against virtual_files means an unqualified "user_id"
// filter would be ambiguous (both tables have the column), so it's
// qualified explicitly as chunks.user_id instead.
func (m *Manifest) CountActiveChunks(ctx context.Context, userID, accountID string) (int, error) {
	var n int64
	err := m.gdb.WithContext(ctx).
		Table("chunks").
		Joins("JOIN virtual_files ON virtual_files.id = chunks.virtual_file_id").
		Where("chunks.user_id = ? AND chunks.account_id = ? AND virtual_files.status != ?", userID, accountID, StatusDeleted).
		Count(&n).Error
	return int(n), err
}

func (m *Manifest) UpdateAccountQuota(ctx context.Context, userID, id string, limit, usage int64, checkedAt time.Time) error {
	return m.scoped(ctx, userID).Model(&Account{}).Where("id = ?", id).
		Updates(map[string]any{
			"quota_limit":      limit,
			"quota_usage":      usage,
			"quota_checked_at": checkedAt.UTC(),
		}).Error
}

// --- virtual files ---

func (m *Manifest) CreateVirtualFile(ctx context.Context, userID string, vf *VirtualFile) error {
	vf.UserID = userID
	now := time.Now().UTC()
	vf.CreatedAt, vf.ModifiedAt = now, now
	if vf.Replicas == 0 {
		vf.Replicas = 1
	}
	if vf.Version == 0 {
		vf.Version = 1
	}

	err := m.gdb.WithContext(ctx).Create(vf).Error
	if rhinodb.IsUniqueViolation(err) {
		return ErrNameExists
	}
	return err
}

func (m *Manifest) SetVirtualFileStatus(ctx context.Context, userID string, id int64, status string) error {
	return m.scoped(ctx, userID).Model(&VirtualFile{}).Where("id = ?", id).
		Updates(map[string]any{"status": status, "modified_at": time.Now().UTC()}).Error
}

func (m *Manifest) GetVirtualFile(ctx context.Context, userID, name string) (VirtualFile, error) {
	var vf VirtualFile
	err := m.scoped(ctx, userID).Where("name = ?", name).First(&vf).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return VirtualFile{}, ErrNotFound
	}
	return vf, err
}

func (m *Manifest) ListVirtualFiles(ctx context.Context, userID, prefix string) ([]VirtualFile, error) {
	var out []VirtualFile
	err := m.scoped(ctx, userID).
		Where("name LIKE ? AND status != ?", prefix+"%", StatusDeleted).
		Order("name").
		Find(&out).Error
	return out, err
}

// DeleteVirtualFile soft-deletes (status=deleted) unless purge is set, in
// which case the row is removed outright — chunks cascade-delete with it
// (ON DELETE CASCADE). purge intentionally doesn't check RowsAffected: the
// caller (Pool.Remove) already confirmed the file exists via GetVirtualFile
// before calling this.
func (m *Manifest) DeleteVirtualFile(ctx context.Context, userID, name string, purge bool) error {
	if purge {
		return m.scoped(ctx, userID).Where("name = ?", name).Delete(&VirtualFile{}).Error
	}
	res := m.scoped(ctx, userID).Model(&VirtualFile{}).Where("name = ?", name).
		Updates(map[string]any{"status": StatusDeleted, "modified_at": time.Now().UTC()})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

// --- chunks ---

func (m *Manifest) AddChunk(ctx context.Context, userID string, c *Chunk) error {
	c.UserID = userID
	c.UploadedAt = time.Now().UTC()
	return m.gdb.WithContext(ctx).Create(c).Error
}

func (m *Manifest) ListChunks(ctx context.Context, userID string, virtualFileID int64) ([]Chunk, error) {
	var out []Chunk
	err := m.scoped(ctx, userID).Where("virtual_file_id = ?", virtualFileID).Order("idx").Find(&out).Error
	return out, err
}
