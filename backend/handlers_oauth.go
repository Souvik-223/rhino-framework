package backend

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// pendingOAuth is what a "connect account" request has to remember across
// the redirect round-trip to Google and back: which user asked, and what
// label they want the new account registered under. The OAuth "state"
// value is the key into this map, so it does double duty as both the
// standard CSRF anti-forgery token and the lookup key for this context —
// knowledge of the correct state (which only ever came from a redirect we
// ourselves issued) is what authorizes completing the flow, not whatever
// session cookie happens to be attached to the callback request.
type pendingOAuth struct {
	userID    string
	label     string
	createdAt time.Time
}

const oauthStateTTL = 10 * time.Minute

// handleConnectAccount redirects the browser straight to Google's consent
// screen — this must be reached via a real top-level browser navigation
// (window.location.href on the frontend), not a fetch/XHR call, since the
// whole point is letting the browser itself follow the redirect chain to
// Google and back.
func (s *Server) handleConnectAccount(c *gin.Context) {
	pool, ok := s.pool(c)
	if !ok {
		return
	}
	label := c.Query("label")
	if label == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": `missing "label" query param`})
		return
	}

	state := randomState()
	s.oauthMu.Lock()
	s.oauthPending[state] = pendingOAuth{
		userID:    c.MustGet("userID").(string),
		label:     label,
		createdAt: time.Now(),
	}
	s.oauthMu.Unlock()

	authURL := pool.AuthCodeURL(s.oauthRedirectURL(), state)
	c.Redirect(http.StatusFound, authURL)
}

// handleOAuthCallback is what Google redirects the browser back to after
// the user approves (or denies) access. Not behind requireSession — the
// state lookup below is what authorizes this request, so it works even if
// the session cookie didn't survive the round trip through Google's site.
func (s *Server) handleOAuthCallback(c *gin.Context) {
	state := c.Query("state")

	s.oauthMu.Lock()
	pending, ok := s.oauthPending[state]
	if ok {
		delete(s.oauthPending, state) // one-shot: a state value is only ever good for one callback
	}
	s.oauthMu.Unlock()

	if !ok || time.Since(pending.createdAt) > oauthStateTTL {
		c.String(http.StatusBadRequest, "invalid or expired authorization request — go back and try connecting the account again")
		return
	}

	if errStr := c.Query("error"); errStr != "" {
		c.Redirect(http.StatusFound, "/?connect_error="+url.QueryEscape(errStr))
		return
	}

	pool, err := s.cache.get(c.Request.Context(), pending.userID)
	if err != nil {
		c.Redirect(http.StatusFound, "/?connect_error="+url.QueryEscape("could not open your pool: "+err.Error()))
		return
	}

	code := c.Query("code")
	if _, err := pool.CompleteConsent(c.Request.Context(), s.oauthRedirectURL(), code, pending.label); err != nil {
		c.Redirect(http.StatusFound, "/?connect_error="+url.QueryEscape(err.Error()))
		return
	}

	c.Redirect(http.StatusFound, "/") // back to the dashboard; it re-fetches the account list on load
}

func (s *Server) oauthRedirectURL() string {
	return strings.TrimRight(s.cfg.PublicURL, "/") + "/api/accounts/oauth/callback"
}

// sweepExpiredOAuthState drops pending flows nobody ever completed (the
// user closed the tab, denied access without an error redirect firing,
// etc.) — called from the same ticker as poolCache's idle eviction rather
// than starting a second one for what's normally a handful of tiny entries.
func (s *Server) sweepExpiredOAuthState() {
	s.oauthMu.Lock()
	defer s.oauthMu.Unlock()
	now := time.Now()
	for state, p := range s.oauthPending {
		if now.Sub(p.createdAt) > oauthStateTTL {
			delete(s.oauthPending, state)
		}
	}
}

func randomState() string {
	buf := make([]byte, 16)
	rand.Read(buf)
	return hex.EncodeToString(buf)
}
