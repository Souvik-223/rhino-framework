package drivepool

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Souvik-223/rhino-framework/drivepool/auth"
	"github.com/Souvik-223/rhino-framework/drivepool/manifest"
)

// DefaultClientSecretPath returns where the CLI's OAuth app credential
// (client_secret.json) is read from when neither --credentials nor
// RHINO_CLI_CLIENT_SECRET is set. This is the one piece of local
// filesystem config drivepool still resolves a default for — it's a
// shared, app-level secret, not per-user data (that all lives in Postgres
// now — see manifest.New/Open).
func DefaultClientSecretPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("drivepool: resolve config dir: %w", err)
	}
	return filepath.Join(dir, "rhino", "client_secret.json"), nil
}

// OpenWithUser loads the OAuth client config from credentialsPath (falling
// back to DefaultClientSecretPath if empty) and opens a Pool scoped to
// userID against the already-open, already-migrated manifest m — the
// Postgres-backed equivalent of the old OpenFromDataDir, now that there's
// no per-installation local directory to resolve: m is shared across every
// user, and only the OAuth app credential still comes from a local file.
func OpenWithUser(ctx context.Context, m *manifest.Manifest, userID, credentialsPath string, scopes ...string) (*Pool, error) {
	if credentialsPath == "" {
		var err error
		credentialsPath, err = DefaultClientSecretPath()
		if err != nil {
			return nil, err
		}
	}
	clientCfg, err := auth.LoadClientConfig(credentialsPath, scopes...)
	if err != nil {
		return nil, fmt.Errorf("drivepool: load OAuth client config: %w", err)
	}

	return Open(ctx, m, userID, clientCfg)
}
