package drivepool

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Souvik-223/rhino-framework/drivepool/manifest"
	"golang.org/x/oauth2"
)

func TestAuthCodeURLUsesGivenRedirectAndState(t *testing.T) {
	p, _ := newTestPool(t)
	p.clientCfg = &oauth2.Config{
		ClientID: "test-client-id",
		Endpoint: oauth2.Endpoint{AuthURL: "https://accounts.google.com/o/oauth2/auth"},
	}

	url := p.AuthCodeURL("http://localhost:8080/api/accounts/oauth/callback", "the-state-value")

	if !strings.Contains(url, "redirect_uri=http%3A%2F%2Flocalhost%3A8080%2Fapi%2Faccounts%2Foauth%2Fcallback") {
		t.Errorf("AuthCodeURL missing expected redirect_uri, got: %s", url)
	}
	if !strings.Contains(url, "state=the-state-value") {
		t.Errorf("AuthCodeURL missing expected state, got: %s", url)
	}
}

func TestCheckLabelAvailableRejectsDuplicates(t *testing.T) {
	ctx := context.Background()
	p, _ := newTestPool(t)

	if err := p.checkLabelAvailable(ctx, "home-gmail"); err != nil {
		t.Fatalf("want a fresh label to be available, got: %v", err)
	}

	if err := p.manifest.AddAccount(ctx, p.userID, manifest.Account{
		ID: "acct-1", Label: "home-gmail", AddedAt: time.Now(),
	}); err != nil {
		t.Fatalf("register account in manifest: %v", err)
	}

	if err := p.checkLabelAvailable(ctx, "home-gmail"); err == nil {
		t.Fatal("want an already-registered label to be rejected")
	}
}

func TestCompleteConsentRejectsDuplicateLabelBeforeAnyNetworkCall(t *testing.T) {
	ctx := context.Background()
	p, _ := newTestPool(t)
	// No clientCfg set — if CompleteConsent tried to exchange a code before
	// checking the label, this would nil-pointer-dereference instead of
	// returning the "already registered" error, so this test also proves
	// checkLabelAvailable runs first.
	if err := p.manifest.AddAccount(ctx, p.userID, manifest.Account{
		ID: "acct-1", Label: "home-gmail", AddedAt: time.Now(),
	}); err != nil {
		t.Fatalf("register account in manifest: %v", err)
	}

	_, err := p.CompleteConsent(ctx, "http://localhost:8080/api/accounts/oauth/callback", "some-code", "home-gmail")
	if err == nil {
		t.Fatal("want CompleteConsent to reject a duplicate label")
	}
}
