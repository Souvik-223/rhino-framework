package backend

import (
	"context"
	"path/filepath"
	"sync"
	"time"

	"github.com/Souvik-223/rhino-framework/drivepool"
	"github.com/Souvik-223/rhino-framework/drivepool/gdrive"
)

// poolCache lazily opens one *drivepool.Pool per portal user and reuses it
// across requests, evicting entries idle for longer than idlePoolTimeout.
// drivepool.Pool/Manifest are untouched by any of this — every user just
// gets their own manifest.db + accounts/ under baseDataDir/users/<id>/,
// opened through the same drivepool.OpenFromDataDir the CLI uses.
type poolCache struct {
	mu               sync.Mutex
	baseDataDir      string
	clientSecretPath string
	pools            map[string]*poolCacheEntry
}

type poolCacheEntry struct {
	pool     *drivepool.Pool
	lastUsed time.Time
}

func newPoolCache(baseDataDir, clientSecretPath string) *poolCache {
	return &poolCache{
		baseDataDir:      baseDataDir,
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

	dataDir := filepath.Join(c.baseDataDir, "users", userID)
	p, err := drivepool.OpenFromDataDir(ctx, dataDir, c.clientSecretPath, gdrive.DriveFileScope)
	if err != nil {
		return nil, err
	}
	c.pools[userID] = &poolCacheEntry{pool: p, lastUsed: time.Now()}
	return p, nil
}

// evictIdle closes and removes cache entries untouched for longer than
// maxIdle. manifest.Manifest restricts itself to one open connection, so
// holding many users' DBs open indefinitely would just accumulate file
// handles for no benefit.
func (c *poolCache) evictIdle(maxIdle time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now()
	for id, e := range c.pools {
		if now.Sub(e.lastUsed) > maxIdle {
			e.pool.Close()
			delete(c.pools, id)
		}
	}
}

func (c *poolCache) closeAll() {
	c.mu.Lock()
	defer c.mu.Unlock()
	for id, e := range c.pools {
		e.pool.Close()
		delete(c.pools, id)
	}
}
