// Command rhino is the CLI for the Google-Drive-pooled storage feature:
// it registers multiple Drive accounts, pools their free space, and
// stores/retrieves files through that pool. See README.md and
// tasks/modification_plan.md for the one-time Google Cloud setup this
// requires before "account add" will work.
package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Souvik-223/rhino-framework/drivepool"
	"github.com/Souvik-223/rhino-framework/drivepool/auth"
	"github.com/Souvik-223/rhino-framework/drivepool/gdrive"
	"github.com/Souvik-223/rhino-framework/drivepool/manifest"
	"github.com/spf13/cobra"
)

var credentialsPath string

func main() {
	root := &cobra.Command{
		Use:           "rhino",
		Short:         "Pool multiple Google Drive accounts into one virtual storage volume",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.PersistentFlags().StringVar(&credentialsPath, "credentials", "",
		"path to the OAuth client_secret.json (default: <config dir>/rhino/client_secret.json)")

	root.AddCommand(newAccountCmd(), newPutCmd(), newGetCmd(), newLsCmd(), newRmCmd(), newStatusCmd())

	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func configDir() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("resolve config dir: %w", err)
	}
	return filepath.Join(dir, "rhino"), nil
}

// openPool wires up the manifest, token store, and OAuth client config,
// then loads every already-registered account. Callers must Close() it.
func openPool(ctx context.Context) (*drivepool.Pool, error) {
	cfgDir, err := configDir()
	if err != nil {
		return nil, err
	}

	manifestPath, err := manifest.Path()
	if err != nil {
		return nil, err
	}
	m, err := manifest.Open(manifestPath)
	if err != nil {
		return nil, fmt.Errorf("open manifest: %w", err)
	}

	tokens := auth.NewTokenStore(filepath.Join(cfgDir, "accounts"))

	secretPath := credentialsPath
	if secretPath == "" {
		secretPath = filepath.Join(cfgDir, "client_secret.json")
	}
	clientCfg, err := auth.LoadClientConfig(secretPath, gdrive.DriveFileScope)
	if err != nil {
		m.Close()
		return nil, fmt.Errorf("load OAuth client config from %s (see README's one-time setup steps): %w", secretPath, err)
	}

	pool, err := drivepool.Open(ctx, m, tokens, clientCfg)
	if err != nil {
		m.Close()
		return nil, err
	}
	return pool, nil
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
			pool, err := openPool(cmd.Context())
			if err != nil {
				return err
			}
			defer pool.Close()

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
			pool, err := openPool(cmd.Context())
			if err != nil {
				return err
			}
			defer pool.Close()

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
	return &cobra.Command{
		Use:   "remove <label>",
		Short: "Deregister an account (does not delete its remote files)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			pool, err := openPool(cmd.Context())
			if err != nil {
				return err
			}
			defer pool.Close()

			if err := pool.RemoveAccount(cmd.Context(), args[0]); err != nil {
				return err
			}
			fmt.Printf("account %q removed\n", args[0])
			return nil
		},
	}
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

			pool, err := openPool(cmd.Context())
			if err != nil {
				return err
			}
			defer pool.Close()

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
			pool, err := openPool(cmd.Context())
			if err != nil {
				return err
			}
			defer pool.Close()

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

			pool, err := openPool(cmd.Context())
			if err != nil {
				return err
			}
			defer pool.Close()

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
			pool, err := openPool(cmd.Context())
			if err != nil {
				return err
			}
			defer pool.Close()

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
			pool, err := openPool(cmd.Context())
			if err != nil {
				return err
			}
			defer pool.Close()

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
