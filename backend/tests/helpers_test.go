// Package backend_test holds all backend test cases as an external,
// black-box package — it only touches backend's and drivepool's exported
// API. The one exception is the test-only seams in drivepool/export_test.go
// and backend/export_test.go, which exist precisely so this package can
// build fixtures without a real OAuth flow or real Drive/network calls.
package backend_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Souvik-223/rhino-framework/backend"
	"github.com/Souvik-223/rhino-framework/backend/authdb"
	"github.com/Souvik-223/rhino-framework/backend/tests/testutil"
	"github.com/Souvik-223/rhino-framework/db/dbtest"
	"github.com/Souvik-223/rhino-framework/drivepool"
	"github.com/Souvik-223/rhino-framework/drivepool/manifest"
)

const (
	fixtureUsername = "fixture-user"
	fixturePassword = "fixture-password-123"
)

// newTestServer builds a Server around one fixture user (registered
// directly through authdb, not via HTTP, so its generated user ID is known
// up front) whose Pool is seeded with a single fake Drive account. Tests
// that need to act as this user should log in via the returned
// credentials with the HTTP API, exactly as a real client would.
func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()

	tx := dbtest.OpenTx(t)
	usersDB := authdb.New(tx)

	user, err := usersDB.CreateUser(context.Background(), fixtureUsername, fixturePassword)
	if err != nil {
		t.Fatalf("create fixture user: %v", err)
	}

	m := manifest.New(tx, dbtest.TokenKey())

	// ListAccountStatus (used by GET /api/accounts) reads the manifest's
	// accounts table, not the in-memory accounts map below — both need a
	// matching row for the fake account to show up.
	if err := m.AddAccount(context.Background(), user.ID, manifest.Account{
		ID: "acct-1", Label: "test-account", AddedAt: time.Now(),
	}); err != nil {
		t.Fatalf("register fake account in manifest: %v", err)
	}

	fake := testutil.NewFakeStore(1<<30, 0)
	accounts := map[string]*drivepool.Account{
		"acct-1": {ID: "acct-1", Label: "test-account", Store: fake},
	}
	pool := drivepool.NewPoolForTesting(m, user.ID, accounts)

	srv := backend.NewTestServer(usersDB, user.ID, pool, []byte("test-session-secret-32-bytes!!!"))
	ts := httptest.NewServer(srv.Router())
	t.Cleanup(ts.Close)
	return ts
}

func newCookieJar(t *testing.T) http.CookieJar {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar: %v", err)
	}
	return jar
}

// login authenticates client (which must have a cookie jar set) as the
// fixture user so subsequent requests carry a valid session.
func login(t *testing.T, client *http.Client, baseURL string) {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"username": fixtureUsername, "password": fixturePassword})
	resp, err := client.Post(baseURL+"/api/auth/login", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("login: want 200, got %d", resp.StatusCode)
	}
}

// authedClient returns an http.Client with a cookie jar, logged in as the
// fixture user.
func authedClient(t *testing.T, ts *httptest.Server) *http.Client {
	t.Helper()
	client := ts.Client()
	client.Jar = newCookieJar(t)
	login(t, client, ts.URL)
	return client
}
