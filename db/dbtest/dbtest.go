// Package dbtest provides one shared transaction-per-test helper so every
// package's tests (manifest, backend) don't each reinvent Postgres test
// setup. There's no SQLite fallback — DATABASE_URL must point at a real
// Postgres instance (CI runs one as a service container; locally, point it
// at a throwaway Postgres of your own, e.g. via Podman/Docker).
package dbtest

import (
	"os"
	"sync"
	"testing"

	"gorm.io/gorm"

	"github.com/Souvik-223/rhino-framework/db"
)

var (
	connOnce sync.Once
	conn     *gorm.DB
	connErr  error
)

func sharedConn(t *testing.T, dsn string) *gorm.DB {
	t.Helper()
	connOnce.Do(func() {
		if err := db.Migrate(dsn); err != nil {
			connErr = err
			return
		}
		conn, connErr = db.Open(dsn)
	})
	if connErr != nil {
		t.Fatalf("dbtest: connect/migrate: %v", connErr)
	}
	return conn
}

// OpenTx returns a *gorm.DB scoped to a fresh transaction that's rolled
// back when the test finishes, so tests never see each other's data and
// never need to clean up after themselves. Skips the test (not a failure)
// if DATABASE_URL isn't set — there's no local zero-config fallback left
// once SQLite is gone.
func OpenTx(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set — skipping Postgres-backed test")
	}

	tx := sharedConn(t, dsn).Begin()
	t.Cleanup(func() { tx.Rollback() })
	return tx
}

// TokenKey returns a fixed 32-byte key for tests that need one — never use
// this for anything but tests.
func TokenKey() []byte {
	return make([]byte, 32)
}
