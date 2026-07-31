// Command rhino is the CLI for the Google-Drive-pooled storage feature:
// it registers multiple Drive accounts, pools their free space, and
// stores/retrieves files through that pool. See README.md for the
// one-time Google Cloud setup this requires before "account add" will
// work.
package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/joho/godotenv"
	"github.com/spf13/cobra"

	"github.com/Souvik-223/rhino-framework/backend/authdb"
	rhinodb "github.com/Souvik-223/rhino-framework/db"
	"github.com/Souvik-223/rhino-framework/drivepool"
	"github.com/Souvik-223/rhino-framework/drivepool/gdrive"
	"github.com/Souvik-223/rhino-framework/drivepool/manifest"
)

var (
	credentialsPath string
	username        string
)

func main() {
	// Loads .env from the current directory into the process environment
	// if one exists (DATABASE_URL, RHINO_SESSION_SECRET, etc. — see
	// .env.example) — every RHINO_*/DATABASE_URL var is read via os.Getenv, which only
	// sees real process env vars, so without this a .env file sitting next
	// to the binary would silently do nothing. Real environment variables
	// (already set by the shell, Docker, etc.) always win: godotenv only
	// fills in vars that aren't already set.
	if err := godotenv.Load(); err != nil && !os.IsNotExist(err) {
		fmt.Fprintln(os.Stderr, "warning: could not load .env:", err)
	}

	root := &cobra.Command{
		Use:           "rhino",
		Short:         "Pool multiple Google Drive accounts into one virtual storage volume",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	// RHINO_CLI_CLIENT_SECRET is deliberately a different env var from the
	// backend's RHINO_CLIENT_SECRET (backend/config.go) — the CLI's OAuth
	// client is normally a "Desktop app" credential (loopback redirect,
	// auth.RunConsentFlow), while a deployed web portal typically needs its
	// own separate "Web application" credential with a fixed registered
	// redirect URI (see plans/web_portal.md and the README's setup notes).
	// Keeping the env vars distinct lets both point at different
	// client_secret.json files from the same .env without either
	// overriding the other.
	root.PersistentFlags().StringVar(&credentialsPath, "credentials", envOr("RHINO_CLI_CLIENT_SECRET", ""),
		"path to the OAuth client_secret.json (default: <config dir>/rhino/client_secret.json, or $RHINO_CLI_CLIENT_SECRET if set)")
	// Every account/file this CLI touches belongs to this identity — it's
	// the same "users" table the web portal logs into, so pointing --user
	// at an existing portal username operates on that person's real data
	// (visible in their dashboard too); a fresh name silently provisions a
	// CLI-only identity with no usable password (see authdb.GetOrCreateCLIUser).
	// No default: an unset identity is a hard error, not a silent guess,
	// since a shared machine with an implicit default user is exactly the
	// kind of mistake that leaks one person's files into another's view.
	root.PersistentFlags().StringVar(&username, "user", envOr("RHINO_USER", ""),
		"portal username identifying which account's data this command reads/writes")

	root.AddCommand(newAccountCmd(), newPutCmd(), newGetCmd(), newLsCmd(), newRmCmd(), newStatusCmd(), newServeCmd())

	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

// openPool connects to DATABASE_URL, resolves --user against the shared
// users table (auto-provisioning it if this is a fresh CLI-only identity),
// and opens a Pool scoped to that user. The returned close func releases
// the database connection this call opened — always call it, even on
// error paths that got far enough to open one.
func openPool(ctx context.Context) (*drivepool.Pool, func(), error) {
	if username == "" {
		return nil, nil, fmt.Errorf("rhino: no user configured — set RHINO_USER or pass --user <name>")
	}

	databaseURL, err := rhinodb.DatabaseURLFromEnv()
	if err != nil {
		return nil, nil, err
	}
	if err := rhinodb.Migrate(databaseURL); err != nil {
		return nil, nil, err
	}
	gdb, err := rhinodb.Open(databaseURL)
	if err != nil {
		return nil, nil, err
	}
	closeDB := func() {
		if sqlDB, err := gdb.DB(); err == nil {
			sqlDB.Close()
		}
	}

	key, err := rhinodb.TokenEncryptionKeyFromEnv()
	if err != nil {
		closeDB()
		return nil, nil, err
	}

	user, err := authdb.New(gdb).GetOrCreateCLIUser(ctx, username)
	if err != nil {
		closeDB()
		return nil, nil, fmt.Errorf("rhino: resolve --user %q: %w", username, err)
	}

	pool, err := drivepool.OpenWithUser(ctx, manifest.New(gdb, key), user.ID, credentialsPath, gdrive.DriveFileScope)
	if err != nil {
		closeDB()
		return nil, nil, fmt.Errorf("%w (see README's one-time setup steps)", err)
	}
	return pool, closeDB, nil
}

func newAccountCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "account",
		Short: "Manage pooled Google Drive accounts",
	}
	cmd.AddCommand(newAccountAddCmd(), newAccountListCmd(), newAccountRemoveCmd())
	return cmd
}

func newAccountAddCmd() *cobra.Command {
	var label string
	cmd := &cobra.Command{
		Use:   "add",
		Short: "Register a new Google Drive account via OAuth consent",
		RunE: func(cmd *cobra.Command, args []string) error {
			if label == "" {
				return fmt.Errorf("--label is required")
			}
			pool, closeDB, err := openPool(cmd.Context())
			if err != nil {
				return err
			}
			defer closeDB()

			acc, err := pool.AddAccount(cmd.Context(), label)
			if err != nil {
				return err
			}
			fmt.Printf("account %q registered\n", acc.Label)
			return nil
		},
	}
	cmd.Flags().StringVar(&label, "label", "", "short name for this account, e.g. \"home-gmail\"")
	return cmd
}

func newAccountListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List registered accounts and their live quota",
		RunE: func(cmd *cobra.Command, args []string) error {
			pool, closeDB, err := openPool(cmd.Context())
			if err != nil {
				return err
			}
			defer closeDB()

			statuses, err := pool.ListAccountStatus(cmd.Context())
			if err != nil {
				return err
			}
			if len(statuses) == 0 {
				fmt.Println("no accounts registered yet — try: rhino account add --label <name>")
				return nil
			}
			for _, s := range statuses {
				if s.Err != nil {
					fmt.Printf("%-20s unavailable: %v\n", s.Label, s.Err)
					continue
				}
				if s.Unlimited {
					fmt.Printf("%-20s unlimited\n", s.Label)
					continue
				}
				fmt.Printf("%-20s %s free of %s\n", s.Label, humanBytes(s.Available), humanBytes(s.Limit))
			}
			return nil
		},
	}
}

func newAccountRemoveCmd() *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "remove <label>",
		Short: "Deregister an account (does not delete its remote files)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			pool, closeDB, err := openPool(cmd.Context())
			if err != nil {
				return err
			}
			defer closeDB()

			if err := pool.RemoveAccount(cmd.Context(), args[0], force); err != nil {
				return err
			}
			fmt.Printf("account %q removed\n", args[0])
			return nil
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "remove even if it still holds chunks for a file (those chunks become unrecoverable)")
	return cmd
}

func newPutCmd() *cobra.Command {
	var as string
	cmd := &cobra.Command{
		Use:   "put <local-path>",
		Short: "Encrypt and upload a file to whichever account has the most free space",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			localPath := args[0]
			virtualName := as
			if virtualName == "" {
				virtualName = filepath.Base(localPath)
			}

			pool, closeDB, err := openPool(cmd.Context())
			if err != nil {
				return err
			}
			defer closeDB()

			if err := pool.Put(cmd.Context(), localPath, virtualName); err != nil {
				return err
			}
			fmt.Printf("stored %q\n", virtualName)
			return nil
		},
	}
	cmd.Flags().StringVar(&as, "as", "", "virtual name to store under (default: the local file's base name)")
	return cmd
}

func newGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <virtual-name> <dest-path>",
		Short: "Download and decrypt a file from the pool",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			pool, closeDB, err := openPool(cmd.Context())
			if err != nil {
				return err
			}
			defer closeDB()

			if err := pool.Get(cmd.Context(), args[0], args[1]); err != nil {
				return err
			}
			fmt.Printf("wrote %s\n", args[1])
			return nil
		},
	}
}

func newLsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "ls [prefix]",
		Short: "List files stored in the pool",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var prefix string
			if len(args) == 1 {
				prefix = args[0]
			}

			pool, closeDB, err := openPool(cmd.Context())
			if err != nil {
				return err
			}
			defer closeDB()

			files, err := pool.List(cmd.Context(), prefix)
			if err != nil {
				return err
			}
			if len(files) == 0 {
				fmt.Println("no files stored yet")
				return nil
			}
			for _, f := range files {
				fmt.Printf("%-40s %10s  %s\n", f.Name, humanBytes(f.Size), f.Status)
			}
			return nil
		},
	}
}

func newRmCmd() *cobra.Command {
	var purge bool
	cmd := &cobra.Command{
		Use:   "rm <virtual-name>",
		Short: "Remove a file from the pool",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			pool, closeDB, err := openPool(cmd.Context())
			if err != nil {
				return err
			}
			defer closeDB()

			if err := pool.Remove(cmd.Context(), args[0], purge); err != nil {
				return err
			}
			if purge {
				fmt.Printf("purged %q (remote chunks deleted)\n", args[0])
			} else {
				fmt.Printf("removed %q (remote chunks kept; use --purge to delete them)\n", args[0])
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&purge, "purge", false, "also delete the file's remote chunks")
	return cmd
}

func newStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show pool-wide totals across all registered accounts",
		RunE: func(cmd *cobra.Command, args []string) error {
			pool, closeDB, err := openPool(cmd.Context())
			if err != nil {
				return err
			}
			defer closeDB()

			statuses, err := pool.ListAccountStatus(cmd.Context())
			if err != nil {
				return err
			}
			files, err := pool.List(cmd.Context(), "")
			if err != nil {
				return err
			}

			var total, available int64
			healthy := 0
			for _, s := range statuses {
				if s.Err != nil {
					continue
				}
				healthy++
				total += s.Limit
				available += s.Available
			}

			fmt.Printf("%d/%d accounts healthy | %s total | %s free | %d files\n",
				healthy, len(statuses), humanBytes(total), humanBytes(available), len(files))
			return nil
		},
	}
}

func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for x := n / unit; x >= unit; x /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}
