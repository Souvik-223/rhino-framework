// Package backend is the web portal's Gin-based HTTP API: registration/
// login/session handling, per-user Drive-pool access, and the embedded
// frontend build. See plans/web_portal.md for the full design.
package backend

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"

	"github.com/Souvik-223/rhino-framework/backend/authdb"
)

const (
	sessionName       = "rhino_session"
	idleEvictInterval = 5 * time.Minute
	idlePoolTimeout   = 30 * time.Minute
)

// Server holds the portal's shared state: the login-account database and
// the per-user drivepool.Pool cache (§1 of the plan). One Server per
// running `rhino serve` process.
type Server struct {
	cfg    Config
	users  *authdb.DB
	cache  *poolCache
	engine *gin.Engine

	stopEvict chan struct{}
}

// New opens (or creates) users.db under cfg.BaseDataDir and builds the
// router. Per-user Pools are opened lazily on first request, not here.
func New(ctx context.Context, cfg Config) (*Server, error) {
	users, err := authdb.Open(filepath.Join(cfg.BaseDataDir, "users.db"))
	if err != nil {
		return nil, fmt.Errorf("backend: open authdb: %w", err)
	}

	s := &Server{
		cfg:       cfg,
		users:     users,
		cache:     newPoolCache(cfg.BaseDataDir, cfg.ClientSecretPath),
		stopEvict: make(chan struct{}),
	}
	s.engine = s.newRouter()

	go s.evictLoop()
	return s, nil
}

// Router returns the server's http.Handler, meant to be wrapped in a
// *http.Server by the caller so graceful shutdown (signal.NotifyContext +
// Shutdown(ctx)) works the same way it would for any Go HTTP server — see
// cmd/rhino/serve.go.
func (s *Server) Router() http.Handler {
	return s.engine
}

// Close stops the idle-pool eviction loop, closes every cached per-user
// Pool, and closes the authdb handle. Call after the HTTP server has
// stopped accepting new requests.
func (s *Server) Close() error {
	close(s.stopEvict)
	s.cache.closeAll()
	return s.users.Close()
}

func (s *Server) evictLoop() {
	ticker := time.NewTicker(idleEvictInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			s.cache.evictIdle(idlePoolTimeout)
		case <-s.stopEvict:
			return
		}
	}
}

func (s *Server) newRouter() *gin.Engine {
	if os.Getenv("RHINO_GIN_DEBUG") != "1" {
		gin.SetMode(gin.ReleaseMode)
	}
	r := gin.New()
	r.Use(gin.Logger(), gin.Recovery())

	store := cookie.NewStore(s.cfg.SessionSecret)
	store.Options(sessions.Options{
		Path:     "/",
		MaxAge:   7 * 24 * 3600,
		HttpOnly: true,
		Secure:   s.cfg.SessionSecure,
		SameSite: http.SameSiteLaxMode,
	})
	r.Use(sessions.Sessions(sessionName, store))

	r.GET("/healthz", s.handleHealthz)
	r.GET("/readyz", s.handleReadyz)

	api := r.Group("/api")
	api.POST("/auth/register", s.handleRegister)
	api.POST("/auth/login", s.handleLogin)
	api.POST("/auth/logout", s.handleLogout)

	authed := api.Group("/")
	authed.Use(s.requireSession)
	authed.GET("/me", s.handleMe)
	authed.GET("/accounts", s.handleListAccounts)
	authed.POST("/accounts", s.handleAddAccount)
	authed.DELETE("/accounts/:label", s.handleRemoveAccount)
	authed.GET("/files", s.handleListFiles)
	authed.POST("/files", s.handleUploadFile)
	authed.GET("/files/download", s.handleDownloadFile)
	authed.DELETE("/files", s.handleDeleteFile)

	s.registerSPA(r)

	return r
}
