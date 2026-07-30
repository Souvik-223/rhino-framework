package backend

import (
	"crypto/rand"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
)

// Config configures a Server. BaseDataDir holds users.db and a users/<id>/
// subtree per portal user (see plans/web_portal.md §1) — distinct from the
// CLI's single-directory drivepool.ResolveDataDir layout, even though both
// are resolved from the same RHINO_DATA_DIR env var in their own contexts.
type Config struct {
	BaseDataDir      string
	ClientSecretPath string // shared OAuth app credential, one per deployment
	SessionSecret    []byte
	SessionSecure    bool // set the cookie's Secure flag; requires this process (or its reverse proxy) to terminate TLS
}

// ConfigFromEnv builds a Config from RHINO_* environment variables,
// falling back to defaultBaseDataDir when RHINO_DATA_DIR isn't set.
func ConfigFromEnv(defaultBaseDataDir string) Config {
	baseDataDir := defaultBaseDataDir
	if v := os.Getenv("RHINO_DATA_DIR"); v != "" {
		baseDataDir = v
	}

	cfg := Config{
		BaseDataDir:      baseDataDir,
		ClientSecretPath: filepath.Join(baseDataDir, "client_secret.json"),
		SessionSecure:    os.Getenv("RHINO_SESSION_SECURE") == "1",
	}
	if v := os.Getenv("RHINO_CLIENT_SECRET"); v != "" {
		cfg.ClientSecretPath = v
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
			panic(fmt.Sprintf("backend: read RHINO_SESSION_SECRET_FILE %q: %v", path, err))
		}
		cfg.SessionSecret = []byte(strings.TrimSpace(string(b)))
	case os.Getenv("RHINO_SESSION_SECRET") != "":
		cfg.SessionSecret = []byte(os.Getenv("RHINO_SESSION_SECRET"))
	default:
		log.Println("backend: RHINO_SESSION_SECRET(_FILE) not set — using an ephemeral key; " +
			"every session will be invalidated on restart. Set one in production.")
		cfg.SessionSecret = randomBytes(32)
	}
	return cfg
}

func randomBytes(n int) []byte {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Sprintf("backend: crypto/rand failed: %v", err))
	}
	return b
}
