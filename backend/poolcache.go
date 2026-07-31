package backend

import (
	"context"
	"sync"
	"time"

	"github.com/Souvik-223/rhino-framework/drivepool"
	"github.com/Souvik-223/rhino-framework/drivepool/gdrive"
	"github.com/Souvik-223/rhino-framework/drivepool/manifest"
)

// poolCache lazily builds one *drivepool.Pool per portal user and reuses it
// across requests, evicting entries idle for longer than idlePoolTimeout.
// Every Pool shares the same underlying manifest/*gorm.DB connection pool
// — unlike the old per-user SQLite file, there's nothing per-user to open
// or close here beyond the in-memory Drive API clients Pool itself builds.
type poolCache struct {
	mu               sync.Mutex
	manifest         *manifest.Manifest
	clientSecretPath string
	pools            map[string]*poolCacheEntry
}

type poolCacheEntry struct {
	pool     *drivepool.Pool
	lastUsed time.Time
}

func newPoolCache(m *manifest.Manifest, clientSecretPath string) *poolCache {
	return &poolCache{
		manifest:         m,
		clientSecretPath: clientSecretPath,
		pools:            make(map[string]*poolCacheEntry),
	}
}

func (c *poolCache) get(ctx context.Context, userID string) (*drivepool.Pool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if e, ok := c.pools[userID]; ok {
		e.lastUsed = time.Now()
		return e.pool, nil
	}

	p, err := drivepool.OpenWithUser(ctx, c.manifest, userID, c.clientSecretPath, gdrive.DriveFileScope)
	if err != nil {
		return nil, err
	}
	c.pools[userID] = &poolCacheEntry{pool: p, lastUsed: time.Now()}
	return p, nil
}

// evictIdle drops cache entries untouched for longer than maxIdle. There's
// no per-entry connection to close anymore (Pool doesn't own one) — this
// just bounds how many users' in-memory Drive clients/OAuth token sources
// stay warm at once.
func (c *poolCache) evictIdle(maxIdle time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now()
	for id, e := range c.pools {
		if now.Sub(e.lastUsed) > maxIdle {
			delete(c.pools, id)
		}
	}
}

func (c *poolCache) closeAll() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.pools = make(map[string]*poolCacheEntry)
}
