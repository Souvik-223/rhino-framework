package backend

import (
	"net/http"

	"github.com/gin-contrib/sessions"
	"github.com/gin-gonic/gin"
)

const sessionUserIDKey = "user_id"

// requireSession aborts with 401 unless the request carries a valid
// session; on success it sets "userID" in the Gin context so handlers can
// read it via c.MustGet("userID"). userID is derived only from the
// verified, signed session — never from a client-supplied field or URL
// param — since that's the entire isolation boundary between tenants.
func (s *Server) requireSession(c *gin.Context) {
	session := sessions.Default(c)
	userID, ok := session.Get(sessionUserIDKey).(string)
	if !ok || userID == "" {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
		return
	}
	c.Set("userID", userID)
	c.Next()
}
