// Package authdb persists the web portal's own login accounts — separate
// from drivepool's per-user manifests, which hold each user's pooled Drive
// accounts and files. One users.db for the whole deployment, opened once
// at server startup.
package authdb

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
	_ "modernc.org/sqlite"

	"github.com/Souvik-223/rhino-framework/storage"
)

var (
	ErrNotFound      = errors.New("authdb: not found")
	ErrUsernameTaken = errors.New("authdb: username already taken")
)

const schema = `
CREATE TABLE IF NOT EXISTS users (
	id            TEXT PRIMARY KEY,
	username      TEXT UNIQUE NOT NULL,
	password_hash TEXT NOT NULL,
	created_at    DATETIME NOT NULL
);
`

type User struct {
	ID           string
	Username     string
	PasswordHash string
	CreatedAt    time.Time
}

type DB struct {
	db *sql.DB
}

// Open creates (or opens) the SQLite database at path, applying the schema.
func Open(path string) (*DB, error) {
	if dir := filepath.Dir(path); dir != "." {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, fmt.Errorf("authdb: create dir: %w", err)
		}
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("authdb: open: %w", err)
	}
	db.SetMaxOpenConns(1) // modernc.org/sqlite is not safe for concurrent writers on one *DB

	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("authdb: apply schema: %w", err)
	}
	return &DB{db: db}, nil
}

func (d *DB) Close() error { return d.db.Close() }

// Ping is used by the /readyz health check.
func (d *DB) Ping(ctx context.Context) error { return d.db.PingContext(ctx) }

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
	_, err = d.db.ExecContext(ctx, `
		INSERT INTO users (id, username, password_hash, created_at) VALUES (?, ?, ?, ?)`,
		u.ID, u.Username, u.PasswordHash, u.CreatedAt)
	if err != nil {
		if isUniqueConstraintErr(err) {
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
	row := d.db.QueryRowContext(ctx, `
		SELECT id, username, password_hash, created_at FROM users WHERE username = ?`, username)
	return scanUser(row)
}

func (d *DB) GetByID(ctx context.Context, id string) (*User, error) {
	row := d.db.QueryRowContext(ctx, `
		SELECT id, username, password_hash, created_at FROM users WHERE id = ?`, id)
	return scanUser(row)
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanUser(row rowScanner) (*User, error) {
	var u User
	if err := row.Scan(&u.ID, &u.Username, &u.PasswordHash, &u.CreatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &u, nil
}

func isUniqueConstraintErr(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed")
}
