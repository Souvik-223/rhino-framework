// Package authdb persists the web portal's own login accounts — separate
// from drivepool's per-user domain data (accounts/files/chunks), which
// lives in its own tables in the same Postgres database (see
// drivepool/manifest). Both packages share one *gorm.DB, opened once by
// db.Open/db.Migrate and handed in here — authdb never opens a connection
// of its own.
package authdb

import (
	"context"
	"errors"
	"fmt"
	"time"

	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"

	rhinodb "github.com/Souvik-223/rhino-framework/db"
	"github.com/Souvik-223/rhino-framework/storage"
)

var (
	ErrNotFound      = errors.New("authdb: not found")
	ErrUsernameTaken = errors.New("authdb: username already taken")
)

type User struct {
	ID           string    `gorm:"primaryKey"`
	Username     string    `gorm:"not null;uniqueIndex;column:username"`
	PasswordHash string    `gorm:"not null;column:password_hash"`
	CreatedAt    time.Time `gorm:"not null;column:created_at"`
}

func (User) TableName() string { return "users" }

type DB struct {
	gdb *gorm.DB
}

// New wraps an already-open, already-migrated *gorm.DB (see db.Open /
// db.Migrate, called once at process startup).
func New(gdb *gorm.DB) *DB {
	return &DB{gdb: gdb}
}

// Ping is used by the /readyz health check.
func (d *DB) Ping(ctx context.Context) error {
	sqlDB, err := d.gdb.DB()
	if err != nil {
		return fmt.Errorf("authdb: underlying sql.DB: %w", err)
	}
	return sqlDB.PingContext(ctx)
}

// CreateUser hashes password with bcrypt and registers a new login
// account. The plaintext password is never stored or logged.
func (d *DB) CreateUser(ctx context.Context, username, password string) (*User, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return nil, fmt.Errorf("authdb: hash password: %w", err)
	}

	u := &User{
		ID:           storage.GenerateID(),
		Username:     username,
		PasswordHash: string(hash),
		CreatedAt:    time.Now().UTC(),
	}
	if err := d.gdb.WithContext(ctx).Create(u).Error; err != nil {
		if rhinodb.IsUniqueViolation(err) {
			return nil, ErrUsernameTaken
		}
		return nil, err
	}
	return u, nil
}

// Authenticate looks up username and verifies password against its stored
// hash. It deliberately returns the same ErrNotFound for "no such user" and
// "wrong password," so callers can't use response differences to enumerate
// valid usernames.
func (d *DB) Authenticate(ctx context.Context, username, password string) (*User, error) {
	u, err := d.GetByUsername(ctx, username)
	if err != nil {
		return nil, err
	}
	if err := bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(password)); err != nil {
		return nil, ErrNotFound
	}
	return u, nil
}

func (d *DB) GetByUsername(ctx context.Context, username string) (*User, error) {
	var u User
	err := d.gdb.WithContext(ctx).Where("username = ?", username).First(&u).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &u, nil
}

func (d *DB) GetByID(ctx context.Context, id string) (*User, error) {
	var u User
	err := d.gdb.WithContext(ctx).Where("id = ?", id).First(&u).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &u, nil
}

// GetOrCreateCLIUser resolves username against the shared users table,
// auto-provisioning a row if none exists yet. The created row's
// password_hash is a bcrypt hash of a random, immediately-discarded value —
// structurally valid but never matchable — so a CLI-provisioned identity
// can never log into the web portal by password. The CLI operates with
// direct DB trust (no HTTP/session layer), so it needs no password of its
// own; pointing --user at an existing portal username instead makes the
// CLI operate on that person's real data, since it's the same users row.
func (d *DB) GetOrCreateCLIUser(ctx context.Context, username string) (*User, error) {
	u, err := d.GetByUsername(ctx, username)
	if err == nil {
		return u, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return nil, err
	}
	return d.CreateUser(ctx, username, storage.GenerateID())
}
