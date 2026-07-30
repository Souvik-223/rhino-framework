package backend_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"
)

func TestRegisterThenMeThenLogout(t *testing.T) {
	ts := newTestServer(t)
	client := ts.Client()
	client.Jar = newCookieJar(t)

	body, _ := json.Marshal(map[string]string{"username": "alice", "password": "hunter2-password"})
	resp, err := client.Post(ts.URL+"/api/auth/register", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("register: want 201, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// Registering already starts a session — /me should work immediately.
	resp, err = client.Get(ts.URL + "/api/me")
	if err != nil {
		t.Fatalf("me: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("me after register: want 200, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	resp, err = client.Post(ts.URL+"/api/auth/logout", "application/json", nil)
	if err != nil {
		t.Fatalf("logout: %v", err)
	}
	resp.Body.Close()

	resp, err = client.Get(ts.URL + "/api/me")
	if err != nil {
		t.Fatalf("me after logout: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("me after logout: want 401, got %d", resp.StatusCode)
	}
}

func TestRegisterDuplicateUsernameRejected(t *testing.T) {
	ts := newTestServer(t)
	client := ts.Client()
	client.Jar = newCookieJar(t)

	body, _ := json.Marshal(map[string]string{"username": "carol", "password": "a-fine-password"})
	resp, _ := client.Post(ts.URL+"/api/auth/register", "application/json", bytes.NewReader(body))
	resp.Body.Close()

	resp, err := client.Post(ts.URL+"/api/auth/register", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("duplicate register: want 409, got %d", resp.StatusCode)
	}
}

func TestLoginWrongPasswordRejected(t *testing.T) {
	ts := newTestServer(t)
	client := ts.Client()
	client.Jar = newCookieJar(t)

	body, _ := json.Marshal(map[string]string{"username": fixtureUsername, "password": "not-the-right-password"})
	resp, err := client.Post(ts.URL+"/api/auth/login", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("want 401 for wrong password, got %d", resp.StatusCode)
	}
}
