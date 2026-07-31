// Package db owns the process-wide Postgres connection — both the CLI and
// the web portal backend share this same package rather than each opening
// their own *gorm.DB, since (unlike the old per-user SQLite files) there's
// now exactly one physical database for everyone to connect to.
package db

import (
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

// uniqueViolationCode is Postgres's SQLSTATE for a unique constraint
// violation (https://www.postgresql.org/docs/current/errcodes-appendix.html).
const uniqueViolationCode = "23505"

// Open connects to databaseURL and configures the pool for Neon's
// characteristics: a low connection cap (Neon enforces one per plan tier)
// and short idle/lifetime limits, since Neon can suspend an idle compute
// after a few minutes on lower tiers and a long-lived idle connection just
// delays that suspension for no benefit.
func Open(databaseURL string) (*gorm.DB, error) {
	if databaseURL == "" {
		return nil, fmt.Errorf("db: DATABASE_URL is not set")
	}

	logLevel := gormlogger.Warn // errors and slow queries only, by default
	if os.Getenv("RHINO_DB_LOG") == "1" {
		logLevel = gormlogger.Info
	}
	gormLogger := gormlogger.New(log.New(os.Stdout, "", log.LstdFlags), gormlogger.Config{
		SlowThreshold: time.Second,
		LogLevel:      logLevel,
		// "record not found" is a routine, already-handled outcome (every
		// Get-style method here checks for it and returns a sentinel error)
		// — without this it logs at Error level on every miss, which is
		// noise, not a real problem worth flagging.
		IgnoreRecordNotFoundError: true,
	})

	gdb, err := gorm.Open(postgres.New(postgres.Config{
		DSN: databaseURL,
		// Neon's pooled endpoint (a hostname containing "-pooler") is
		// PgBouncer in transaction-pooling mode, which doesn't support the
		// server-side prepared statements pgx/GORM use by default — a
		// statement prepared on one pooled backend connection can get
		// executed against a different one on the next call. Simple
		// protocol re-sends the full query text every time instead of
		// preparing it once, which is slightly less efficient but is the
		// documented, supported way to run an ORM through PgBouncer. Safe
		// to leave on even against a direct (non-pooled) connection.
		PreferSimpleProtocol: true,
	}), &gorm.Config{
		Logger: gormLogger,
	})
	if err != nil {
		return nil, fmt.Errorf("db: open: %w", err)
	}

	sqlDB, err := gdb.DB()
	if err != nil {
		return nil, fmt.Errorf("db: underlying sql.DB: %w", err)
	}
	sqlDB.SetMaxOpenConns(10)
	sqlDB.SetMaxIdleConns(5)
	sqlDB.SetConnMaxLifetime(30 * time.Minute)
	sqlDB.SetConnMaxIdleTime(5 * time.Minute)

	return gdb, nil
}

// stringFromEnvOrFile prefers fileVar (the Docker/k8s secrets convention: a
// mounted file, e.g. a Docker secret, rather than an env var's value —
// easier to leak via `docker inspect` or a process listing) and falls back
// to the plain envVar if fileVar isn't set.
func stringFromEnvOrFile(envVar, fileVar string) (string, error) {
	if path := os.Getenv(fileVar); path != "" {
		b, err := os.ReadFile(path)
		if err != nil {
			return "", fmt.Errorf("db: read %s %q: %w", fileVar, path, err)
		}
		return strings.TrimSpace(string(b)), nil
	}
	return os.Getenv(envVar), nil
}

// DatabaseURLFromEnv reads the Postgres connection string from DATABASE_URL
// or DATABASE_URL_FILE — shared by the backend and the CLI so both resolve
// it the same way.
func DatabaseURLFromEnv() (string, error) {
	url, err := stringFromEnvOrFile("DATABASE_URL", "DATABASE_URL_FILE")
	if err != nil {
		return "", err
	}
	if url == "" {
		return "", fmt.Errorf("db: DATABASE_URL (or DATABASE_URL_FILE) is not set")
	}
	return url, nil
}

// TokenEncryptionKeyFromEnv reads and validates RHINO_TOKEN_ENCRYPTION_KEY
// or RHINO_TOKEN_ENCRYPTION_KEY_FILE (32 bytes, hex-encoded) — shared by
// the backend and the CLI, both of which need this same key to open a
// manifest.Manifest.
func TokenEncryptionKeyFromEnv() ([]byte, error) {
	keyHex, err := stringFromEnvOrFile("RHINO_TOKEN_ENCRYPTION_KEY", "RHINO_TOKEN_ENCRYPTION_KEY_FILE")
	if err != nil {
		return nil, err
	}
	key, err := hex.DecodeString(keyHex)
	if keyHex == "" || err != nil || len(key) != 32 {
		return nil, fmt.Errorf("db: RHINO_TOKEN_ENCRYPTION_KEY(_FILE) must be set to 32 bytes, hex-encoded (64 hex characters) — generate one with `openssl rand -hex 32`")
	}
	return key, nil
}

// IsUniqueViolation reports whether err is a Postgres unique-constraint
// violation — replaces the old SQLite-era string-matching on
// "UNIQUE constraint failed", which doesn't apply to Postgres's error text.
func IsUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == uniqueViolationCode
}
