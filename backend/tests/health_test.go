package backend_test

import (
	"net/http"
	"testing"
)

func TestHealthzAndReadyz(t *testing.T) {
	ts := newTestServer(t)

	resp, err := http.Get(ts.URL + "/healthz")
	if err != nil {
		t.Fatalf("healthz: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("healthz: want 200, got %d", resp.StatusCode)
	}

	resp, err = http.Get(ts.URL + "/readyz")
	if err != nil {
		t.Fatalf("readyz: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("readyz: want 200, got %d", resp.StatusCode)
	}
}
