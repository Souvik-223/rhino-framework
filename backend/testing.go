package backend

import (
	"time"

	"github.com/Souvik-223/rhino-framework/backend/authdb"
	"github.com/Souvik-223/rhino-framework/drivepool"
)

// NewTestServer wires a Server around a single pre-built Pool registered
// under userID, skipping the real per-user directory/OAuth bootstrap that
// New/poolCache.get do.
//
// Like drivepool.NewPoolForTesting, this has to be a regular exported
// function rather than an export_test.go seam: backend/tests is a
// separate package in a separate directory, and Go only compiles a
// package's "_test.go" files into that same package's own test binary —
// never into another package that merely imports it. Not used by any
// production code path.
func NewTestServer(users *authdb.DB, userID string, pool *drivepool.Pool, sessionSecret []byte) *Server {
	s := &Server{
		cfg:   Config{SessionSecret: sessionSecret},
		users: users,
		cache: &poolCache{
			pools: map[string]*poolCacheEntry{
				userID: {pool: pool, lastUsed: time.Now()},
			},
		},
		stopEvict: make(chan struct{}),
	}
	s.engine = s.newRouter()
	return s
}
