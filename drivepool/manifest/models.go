package manifest

import (
	"time"

	"golang.org/x/oauth2"
)

// Account is a registered Drive account. UserID is the tenant boundary —
// every query in this package that touches accounts filters on it (see
// scoped in manifest.go). TokenCiphertext is the AES-256-CTR-encrypted,
// JSON-marshaled OAuth token (storage.CopyEncrypt, keyed by the Manifest's
// tokenKey); Token is the transient, decrypted form populated by
// GetAccount/ListAccounts/AddAccount — it's never written to the database
// directly, only TokenCiphertext is a real column.
type Account struct {
	ID              string     `gorm:"primaryKey"`
	UserID          string     `gorm:"not null;column:user_id;uniqueIndex:idx_accounts_user_label"`
	Label           string     `gorm:"not null;uniqueIndex:idx_accounts_user_label"`
	TokenCiphertext []byte     `gorm:"not null;column:token_ciphertext"`
	AddedAt         time.Time  `gorm:"not null;column:added_at"`
	QuotaLimit      *int64     `gorm:"column:quota_limit"` // nil = not yet checked
	QuotaUsage      *int64     `gorm:"column:quota_usage"`
	QuotaCheckedAt  *time.Time `gorm:"column:quota_checked_at"`

	Token *oauth2.Token `gorm:"-"`
}

func (Account) TableName() string { return "accounts" }

// VirtualFile is one logical uploaded file, split into one or more Chunks.
type VirtualFile struct {
	ID          int64     `gorm:"primaryKey"`
	UserID      string    `gorm:"not null;column:user_id;uniqueIndex:idx_vf_user_name"`
	Name        string    `gorm:"not null;uniqueIndex:idx_vf_user_name"`
	Size        int64     `gorm:"not null"`
	ContentHash string    `gorm:"not null;column:content_hash"`
	ChunkSize   int64     `gorm:"not null;column:chunk_size"`
	FileKey     []byte    `gorm:"not null;column:file_key"`
	Replicas    int       `gorm:"not null;default:1"`
	Version     int       `gorm:"not null;default:1"`
	Status      string    `gorm:"not null"` // complete | incomplete | deleted
	CreatedAt   time.Time `gorm:"not null;column:created_at"`
	ModifiedAt  time.Time `gorm:"not null;column:modified_at"`
}

func (VirtualFile) TableName() string { return "virtual_files" }

const (
	StatusComplete   = "complete"
	StatusIncomplete = "incomplete"
	StatusDeleted    = "deleted"
)

// Chunk is one encrypted piece of a VirtualFile's bytes, stored on one
// Drive account. AccountID is nullable (ON DELETE SET NULL): once an
// account is force-removed, its chunks survive with AccountID nil instead
// of being deleted or left pointing at a nonexistent row — see
// Pool.ListWithAccounts for how that's surfaced as "disconnected".
type Chunk struct {
	ID              int64     `gorm:"primaryKey"`
	UserID          string    `gorm:"not null;column:user_id"`
	VirtualFileID   int64     `gorm:"not null;column:virtual_file_id;uniqueIndex:idx_chunks_vf_idx"`
	Index           int       `gorm:"not null;column:idx;uniqueIndex:idx_chunks_vf_idx"`
	AccountID       *string   `gorm:"column:account_id"`
	RemoteFileID    string    `gorm:"not null;column:remote_file_id"`
	RemoteFolderID  string    `gorm:"not null;column:remote_folder_id"`
	PlaintextSize   int64     `gorm:"not null;column:plaintext_size"`
	PlaintextSHA256 string    `gorm:"not null;column:plaintext_sha256"`
	CiphertextMD5   string    `gorm:"not null;column:ciphertext_md5"`
	UploadedAt      time.Time `gorm:"not null;column:uploaded_at"`
}

func (Chunk) TableName() string { return "chunks" }

// ChunkReplica is schema-only today — nothing reads or writes it yet.
// Tracked here for parity with the migration; multi-account chunk
// replication is future work (see CLAUDE.md's Phase 3 note).
type ChunkReplica struct {
	ChunkID      int64  `gorm:"primaryKey;column:chunk_id"`
	AccountID    string `gorm:"primaryKey;column:account_id"`
	UserID       string `gorm:"not null;column:user_id"`
	RemoteFileID string `gorm:"not null;column:remote_file_id"`
}

func (ChunkReplica) TableName() string { return "chunk_replicas" }
