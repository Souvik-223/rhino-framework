package backend_test

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestListAccounts(t *testing.T) {
	ts := newTestServer(t)
	client := authedClient(t, ts)

	resp, err := client.Get(ts.URL + "/api/accounts")
	if err != nil {
		t.Fatalf("list accounts: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}

	var accounts []map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&accounts); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(accounts) != 1 || accounts[0]["label"] != "test-account" {
		t.Fatalf("unexpected accounts: %+v", accounts)
	}
}

func TestAccountsRequireAuthentication(t *testing.T) {
	ts := newTestServer(t)

	resp, err := ts.Client().Get(ts.URL + "/api/accounts")
	if err != nil {
		t.Fatalf("list accounts: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("want 401 without a session, got %d", resp.StatusCode)
	}
}
