package backend

import (
	"crypto/rand"
	"fmt"
	"log"
	"os"
	"strings"

	rhinodb "github.com/Souvik-223/rhino-framework/db"
	"github.com/Souvik-223/rhino-framework/drivepool"
)

// Config configures a Server. Every portal user's data lives in one shared
// Postgres database (DatabaseURL) — see drivepool/manifest and
// backend/poolcache.go — rather than a per-user local directory.
type Config struct {
	DatabaseURL        string
	TokenEncryptionKey []byte // 32 bytes; encrypts/decrypts stored OAuth tokens, see manifest.Manifest
	ClientSecretPath   string // shared OAuth app credential, one per deployment
	SessionSecret      []byte
	SessionSecure      bool   // set the cookie's Secure flag; requires this process (or its reverse proxy) to terminate TLS
	PublicURL          string // this server's externally-reachable base URL; see handlers_oauth.go
}

// ConfigFromEnv builds a Config from RHINO_*/DATABASE_URL environment
// variables. DATABASE_URL and RHINO_TOKEN_ENCRYPTION_KEY have no fallback
// — unlike the old local-SQLite-file default, there's no reasonable
// "just works with zero config" default for a shared cloud database or an
// encryption key, so both are hard requirements here.
func ConfigFromEnv() (Config, error) {
	cfg := Config{
		SessionSecure: os.Getenv("RHINO_SESSION_SECURE") == "1",
		PublicURL:     "http://localhost:8080", // matches serve.go's default --addr; override for anything else
	}

	databaseURL, err := rhinodb.DatabaseURLFromEnv()
	if err != nil {
		return Config{}, fmt.Errorf("backend: %w", err)
	}
	cfg.DatabaseURL = databaseURL

	key, err := rhinodb.TokenEncryptionKeyFromEnv()
	if err != nil {
		return Config{}, fmt.Errorf("backend: %w", err)
	}
	cfg.TokenEncryptionKey = key

	if v := os.Getenv("RHINO_CLIENT_SECRET"); v != "" {
		cfg.ClientSecretPath = v
	} else {
		path, err := drivepool.DefaultClientSecretPath()
		if err != nil {
			return Config{}, err
		}
		cfg.ClientSecretPath = path
	}

	if v := os.Getenv("RHINO_PUBLIC_URL"); v != "" {
		cfg.PublicURL = strings.TrimRight(v, "/")
	}

	switch {
	case os.Getenv("RHINO_SESSION_SECRET_FILE") != "":
		// The Docker/k8s secrets convention: the secret's bytes live in a
		// mounted file (e.g. /run/secrets/session_secret), not in an env
		// var's value, which is easier to leak via `docker inspect` or a
		// process listing.
		path := os.Getenv("RHINO_SESSION_SECRET_FILE")
		b, err := os.ReadFile(path)
		if err != nil {
			return Config{}, fmt.Errorf("backend: read RHINO_SESSION_SECRET_FILE %q: %w", path, err)
		}
		cfg.SessionSecret = []byte(strings.TrimSpace(string(b)))
	case os.Getenv("RHINO_SESSION_SECRET") != "":
		cfg.SessionSecret = []byte(os.Getenv("RHINO_SESSION_SECRET"))
	default:
		log.Println("backend: RHINO_SESSION_SECRET(_FILE) not set — using an ephemeral key; " +
			"every session will be invalidated on restart. Set one in production.")
		cfg.SessionSecret = randomBytes(32)
	}
	return cfg, nil
}

func randomBytes(n int) []byte {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Sprintf("backend: crypto/rand failed: %v", err))
	}
	return b
}
