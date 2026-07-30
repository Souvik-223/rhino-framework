package drivepool

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Souvik-223/rhino-framework/drivepool/auth"
	"github.com/Souvik-223/rhino-framework/drivepool/manifest"
)

// ResolveDataDir returns the directory rhino should read/write its
// manifest, tokens, and OAuth client config from. override (typically
// os.Getenv("RHINO_DATA_DIR")) wins if set; otherwise falls back to
// os.UserConfigDir()/rhino, matching the CLI's original behavior exactly.
func ResolveDataDir(override string) (string, error) {
	if override != "" {
		return override, nil
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("drivepool: resolve config dir: %w", err)
	}
	return filepath.Join(dir, "rhino"), nil
}

// OpenFromDataDir wires up the manifest, token store, and OAuth client
// config found under dataDir and opens a Pool. credentialsPath overrides
// where client_secret.json is read from — pass "" to default to
// dataDir/client_secret.json (the CLI's layout), or an explicit shared path
// (used when dataDir is a per-user directory but the OAuth app credential
// is shared across every user).
func OpenFromDataDir(ctx context.Context, dataDir, credentialsPath string, scopes ...string) (*Pool, error) {
	m, err := manifest.Open(filepath.Join(dataDir, "manifest.db"))
	if err != nil {
		return nil, fmt.Errorf("drivepool: open manifest: %w", err)
	}

	tokens := auth.NewTokenStore(filepath.Join(dataDir, "accounts"))

	if credentialsPath == "" {
		credentialsPath = filepath.Join(dataDir, "client_secret.json")
	}
	clientCfg, err := auth.LoadClientConfig(credentialsPath, scopes...)
	if err != nil {
		m.Close()
		return nil, fmt.Errorf("drivepool: load OAuth client config: %w", err)
	}

	return Open(ctx, m, tokens, clientCfg)
}
