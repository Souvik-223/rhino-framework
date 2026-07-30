package backend

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// handleHealthz reports only that the process is up — no dependency
// checks, so a slow/unavailable authdb doesn't flip liveness and cause an
// orchestrator to kill-and-restart a server that just needs readyz to
// report not-ready for a bit.
func (s *Server) handleHealthz(c *gin.Context) {
	c.Status(http.StatusOK)
}

func (s *Server) handleReadyz(c *gin.Context) {
	if err := s.users.Ping(c.Request.Context()); err != nil {
		c.String(http.StatusServiceUnavailable, "not ready")
		return
	}
	c.Status(http.StatusOK)
}
