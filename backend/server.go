// Package backend is the web portal's Gin-based HTTP API: registration/
// login/session handling, per-user Drive-pool access, and the embedded
// frontend build. See plans/web_portal.md for the full design.
package backend

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/Souvik-223/rhino-framework/backend/authdb"
	rhinodb "github.com/Souvik-223/rhino-framework/db"
	"github.com/Souvik-223/rhino-framework/drivepool/manifest"
)

const (
	sessionName       = "rhino_session"
	idleEvictInterval = 5 * time.Minute
	idlePoolTimeout   = 30 * time.Minute
)

// Server holds the portal's shared state: one Postgres connection pool
// (gdb), the login-account database, and the per-user drivepool.Pool cache
// (§1 of the plan). One Server per running `rhino serve` process.
type Server struct {
	cfg    Config
	gdb    *gorm.DB
	users  *authdb.DB
	cache  *poolCache
	engine *gin.Engine

	oauthMu      sync.Mutex
	oauthPending map[string]pendingOAuth

	stopEvict chan struct{}
}

// New connects to cfg.DatabaseURL, applies any pending migrations, and
// builds the router. Per-user Pools are opened lazily on first request, not
// here — they all share this same connection pool underneath.
func New(ctx context.Context, cfg Config) (*Server, error) {
	if err := rhinodb.Migrate(cfg.DatabaseURL); err != nil {
		return nil, fmt.Errorf("backend: migrate database: %w", err)
	}
	gdb, err := rhinodb.Open(cfg.DatabaseURL)
	if err != nil {
		return nil, fmt.Errorf("backend: open database: %w", err)
	}

	s := &Server{
		cfg:          cfg,
		gdb:          gdb,
		users:        authdb.New(gdb),
		cache:        newPoolCache(manifest.New(gdb, cfg.TokenEncryptionKey), cfg.ClientSecretPath),
		oauthPending: make(map[string]pendingOAuth),
		stopEvict:    make(chan struct{}),
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

// Close stops the idle-pool eviction loop, drops every cached per-user
// Pool, and closes the shared database connection pool. Call after the
// HTTP server has stopped accepting new requests.
func (s *Server) Close() error {
	close(s.stopEvict)
	s.cache.closeAll()
	sqlDB, err := s.gdb.DB()
	if err != nil {
		return err
	}
	return sqlDB.Close()
}

func (s *Server) evictLoop() {
	ticker := time.NewTicker(idleEvictInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			s.cache.evictIdle(idlePoolTimeout)
			s.sweepExpiredOAuthState()
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
	// Not behind requireSession: the OAuth "state" value is what authorizes
	// this request, since it's Google's redirect landing here, not our own
	// frontend calling in — see handlers_oauth.go.
	r.GET("/api/accounts/oauth/callback", s.handleOAuthCallback)

	api := r.Group("/api")
	api.POST("/auth/register", s.handleRegister)
	api.POST("/auth/login", s.handleLogin)
	api.POST("/auth/logout", s.handleLogout)

	authed := api.Group("/")
	authed.Use(s.requireSession)
	authed.GET("/me", s.handleMe)
	authed.GET("/accounts", s.handleListAccounts)
	authed.GET("/accounts/connect", s.handleConnectAccount)
	authed.DELETE("/accounts/:label", s.handleRemoveAccount)
	authed.GET("/files", s.handleListFiles)
	authed.POST("/files", s.handleUploadFile)
	authed.GET("/files/download", s.handleDownloadFile)
	authed.DELETE("/files", s.handleDeleteFile)

	s.registerSPA(r)

	return r
}
