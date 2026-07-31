package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/Souvik-223/rhino-framework/backend"
)

// newServeCmd runs the multi-user web portal against the same shared
// Postgres database (DATABASE_URL) every other command uses — every portal
// user's data lives in that one database now, not a per-installation local
// directory, so there's nothing left for this command to resolve locally
// beyond the OAuth client_secret.json (see backend.ConfigFromEnv).
func newServeCmd() *cobra.Command {
	var addr, certFile, keyFile string
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Run the web portal (multi-user, drag-and-drop file browser)",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := backend.ConfigFromEnv()
			if err != nil {
				return err
			}

			srv, err := backend.New(cmd.Context(), cfg)
			if err != nil {
				return err
			}

			httpSrv := &http.Server{Addr: addr, Handler: srv.Router()}

			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()

			serveErr := make(chan error, 1)
			go func() {
				var err error
				if certFile != "" {
					err = httpSrv.ListenAndServeTLS(certFile, keyFile)
				} else {
					err = httpSrv.ListenAndServe()
				}
				if err != nil && err != http.ErrServerClosed {
					serveErr <- err
					return
				}
				close(serveErr)
			}()

			fmt.Printf("rhino serve: listening on %s\n", addr)

			select {
			case <-ctx.Done():
			case err := <-serveErr:
				if err != nil {
					return err
				}
			}

			shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			if err := httpSrv.Shutdown(shutdownCtx); err != nil {
				return fmt.Errorf("shutdown: %w", err)
			}
			return srv.Close()
		},
	}
	cmd.Flags().StringVar(&addr, "addr", envOr("RHINO_ADDR", ":8080"), "address to listen on")
	cmd.Flags().StringVar(&certFile, "tls-cert", envOr("RHINO_TLS_CERT", ""), "TLS certificate file (omit to serve plain HTTP, e.g. behind a reverse proxy)")
	cmd.Flags().StringVar(&keyFile, "tls-key", envOr("RHINO_TLS_KEY", ""), "TLS private key file")
	return cmd
}

// envOr lets a flag's default come from an env var (so a container can set
// RHINO_ADDR etc. instead of overriding the entrypoint's args) while still
// letting an explicit --flag override either.
func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
