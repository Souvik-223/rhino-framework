package backend_test

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"
)

func uploadFile(t *testing.T, client *http.Client, baseURL, name string, content []byte) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	part, err := mw.CreateFormFile("file", name)
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	if _, err := part.Write(content); err != nil {
		t.Fatalf("write form file: %v", err)
	}
	mw.Close()

	req, err := http.NewRequest(http.MethodPost, baseURL+"/api/files", &buf)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("upload: want 201, got %d", resp.StatusCode)
	}
}

func TestFileUploadListDownloadDelete(t *testing.T) {
	ts := newTestServer(t)
	client := authedClient(t, ts)

	content := []byte("hello from a backend test")
	uploadFile(t, client, ts.URL, "hello.txt", content)

	resp, err := client.Get(ts.URL + "/api/files")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	var files []map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&files); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	resp.Body.Close()
	if accounts, ok := files[0]["accounts"].([]any); !ok || len(accounts) != 1 || accounts[0] != "test-account" {
		t.Errorf("want file to report [\"test-account\"] as its drive, got: %+v", files[0]["accounts"])
	}
	if len(files) != 1 || files[0]["name"] != "hello.txt" {
		t.Fatalf("unexpected file list: %+v", files)
	}

	resp, err = client.Get(ts.URL + "/api/files/download?name=hello.txt")
	if err != nil {
		t.Fatalf("download: %v", err)
	}
	got, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		t.Fatalf("read download body: %v", err)
	}
	if !bytes.Equal(got, content) {
		t.Fatalf("download mismatch: got %q want %q", got, content)
	}

	req, err := http.NewRequest(http.MethodDelete, ts.URL+"/api/files?name=hello.txt&purge=true", nil)
	if err != nil {
		t.Fatalf("new delete request: %v", err)
	}
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete: want 204, got %d", resp.StatusCode)
	}

	resp, err = client.Get(ts.URL + "/api/files")
	if err != nil {
		t.Fatalf("list after delete: %v", err)
	}
	files = nil
	if err := json.NewDecoder(resp.Body).Decode(&files); err != nil {
		t.Fatalf("decode list after delete: %v", err)
	}
	resp.Body.Close()
	if len(files) != 0 {
		t.Fatalf("want no files after purge delete, got %+v", files)
	}
}

func TestFilesRequireAuthentication(t *testing.T) {
	ts := newTestServer(t)

	resp, err := ts.Client().Get(ts.URL + "/api/files")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("want 401 without a session, got %d", resp.StatusCode)
	}
}

func TestDownloadMissingFile(t *testing.T) {
	ts := newTestServer(t)
	client := authedClient(t, ts)

	resp, err := client.Get(ts.URL + "/api/files/download?name=does-not-exist.txt")
	if err != nil {
		t.Fatalf("download: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("want 404 for a missing file, got %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("want a JSON error body, got Content-Type %q", ct)
	}
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	if body["error"] != "file not found" {
		t.Errorf("want error message %q, got %+v", "file not found", body)
	}
}
