package auth

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"os"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

// LoadClientConfig reads a downloaded Google OAuth "Desktop app" client
// secret file and returns an oauth2.Config for the given scopes. The
// RedirectURL is left empty here; RunConsentFlow fills it in per-run with
// the loopback address it actually bound.
func LoadClientConfig(clientSecretPath string, scopes ...string) (*oauth2.Config, error) {
	b, err := os.ReadFile(clientSecretPath)
	if err != nil {
		return nil, fmt.Errorf("auth: read client secret: %w", err)
	}

	cfg, err := google.ConfigFromJSON(b, scopes...)
	if err != nil {
		return nil, fmt.Errorf("auth: parse client secret: %w", err)
	}
	return cfg, nil
}

// RunConsentFlow drives the OAuth2 "installed app" loopback-redirect flow:
// it binds an ephemeral local port, prints the consent URL for the user to
// open in a browser, waits for Google's redirect back to that local port,
// exchanges the returned code for a token, and shuts the local server down.
//
// The out-of-band ("copy this code") flow is no longer supported by Google
// for any OAuth client type — the loopback redirect used here is the
// current, supported mechanism for Desktop app clients specifically.
func RunConsentFlow(ctx context.Context, cfg *oauth2.Config) (*oauth2.Token, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("auth: bind local callback listener: %w", err)
	}
	defer listener.Close()

	port := listener.Addr().(*net.TCPAddr).Port
	redirectURL := fmt.Sprintf("http://127.0.0.1:%d/callback", port)

	cfgCopy := *cfg
	cfgCopy.RedirectURL = redirectURL

	state := randomState()

	type result struct {
		code string
		err  error
	}
	resultCh := make(chan result, 1)

	mux := http.NewServeMux()
	mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("state") != state {
			http.Error(w, "state mismatch", http.StatusBadRequest)
			resultCh <- result{err: fmt.Errorf("auth: state mismatch in callback")}
			return
		}
		if errStr := q.Get("error"); errStr != "" {
			http.Error(w, "authorization denied", http.StatusBadRequest)
			resultCh <- result{err: fmt.Errorf("auth: authorization denied: %s", errStr)}
			return
		}

		code := q.Get("code")
		fmt.Fprintln(w, "Authorization complete — you can close this tab and return to the terminal.")
		resultCh <- result{code: code}
	})

	srv := &http.Server{Handler: mux}
	go srv.Serve(listener)
	defer srv.Shutdown(context.Background())

	// consent alongside select_account: Google only issues a refresh_token
	// on an account's first grant, so a bare select_account re-auth for an
	// already-authorized account can silently come back with no
	// refresh_token — see pool.go's AuthCodeURL for the failure mode.
	authURL := cfgCopy.AuthCodeURL(state, oauth2.AccessTypeOffline, oauth2.SetAuthURLParam("prompt", "select_account consent"))
	fmt.Printf("Open this URL to authorize access:\n\n%s\n\nWaiting on %s ...\n", authURL, redirectURL)

	select {
	case res := <-resultCh:
		if res.err != nil {
			return nil, res.err
		}
		tok, err := cfgCopy.Exchange(ctx, res.code)
		if err != nil {
			return nil, fmt.Errorf("auth: exchange code for token: %w", err)
		}
		return tok, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(5 * time.Minute):
		return nil, fmt.Errorf("auth: timed out waiting for authorization")
	}
}

func randomState() string {
	buf := make([]byte, 16)
	rand.Read(buf)
	return hex.EncodeToString(buf)
}
