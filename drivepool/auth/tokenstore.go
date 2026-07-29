// Package auth handles the Google OAuth2 consent flow and per-account
// token persistence for the drivepool CLI.
package auth

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/oauth2"
)

// TokenStore persists one oauth2.Token per account label, each in its own
// file under dir/<label>/token.json.
type TokenStore struct {
	dir string
}

func NewTokenStore(dir string) *TokenStore {
	return &TokenStore{dir: dir}
}

func (s *TokenStore) Path(label string) string {
	return filepath.Join(s.dir, label, "token.json")
}

func (s *TokenStore) Save(label string, tok *oauth2.Token) error {
	path := s.Path(label)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("auth: create token dir: %w", err)
	}

	b, err := json.MarshalIndent(tok, "", "  ")
	if err != nil {
		return fmt.Errorf("auth: encode token: %w", err)
	}

	if err := os.WriteFile(path, b, 0o600); err != nil {
		return fmt.Errorf("auth: write token: %w", err)
	}
	return nil
}

func (s *TokenStore) Load(label string) (*oauth2.Token, error) {
	b, err := os.ReadFile(s.Path(label))
	if err != nil {
		return nil, fmt.Errorf("auth: read token: %w", err)
	}

	var tok oauth2.Token
	if err := json.Unmarshal(b, &tok); err != nil {
		return nil, fmt.Errorf("auth: decode token: %w", err)
	}
	return &tok, nil
}
