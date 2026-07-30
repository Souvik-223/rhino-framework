package backend

import (
	"embed"
	"fmt"
	"io/fs"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// distFS embeds the frontend's built assets. go:embed can only reach files
// inside this package's own directory, not a sibling like ../frontend/dist
// — so the Makefile's build-frontend target copies frontend/dist here
// before `go build` runs (see plans/web_portal.md §3). The placeholder
// index.html checked in at backend/dist/index.html keeps this buildable
// before the frontend exists.
//
//go:embed all:dist
var distFS embed.FS

// registerSPA serves the embedded frontend build and falls back to
// index.html for any unmatched GET route, so vue-router's client-side
// routes all resolve to the same shell, which then handles routing in the
// browser. /api/* misses still 404 instead of falling back.
func (s *Server) registerSPA(r *gin.Engine) {
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		panic(fmt.Sprintf("backend: dist embed: %v", err)) // build-time invariant; dist/ always exists in this package
	}
	httpFS := http.FS(sub)
	fileServer := http.FileServer(httpFS)

	r.NoRoute(func(c *gin.Context) {
		if c.Request.Method != http.MethodGet || strings.HasPrefix(c.Request.URL.Path, "/api/") {
			c.Status(http.StatusNotFound)
			return
		}

		reqPath := strings.TrimPrefix(c.Request.URL.Path, "/")
		if reqPath != "" {
			if f, err := sub.Open(reqPath); err == nil {
				f.Close()
				fileServer.ServeHTTP(c.Writer, c.Request)
				return
			}
		}

		// SPA fallback for "/" and any unmatched client-side route. Read
		// and write index.html's bytes directly rather than going through
		// http.FileServer/ServeContent for it: net/http's static file
		// serving special-cases a resolved path literally named
		// "index.html" and redirects it to "/", which — since this
		// handler's whole job for that root path *is* serving
		// index.html — becomes a redirect loop.
		data, err := fs.ReadFile(sub, "index.html")
		if err != nil {
			c.Status(http.StatusInternalServerError)
			return
		}
		c.Data(http.StatusOK, "text/html; charset=utf-8", data)
	})
}
