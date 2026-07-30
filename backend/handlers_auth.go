package backend

import (
	"net/http"

	"github.com/gin-contrib/sessions"
	"github.com/gin-gonic/gin"

	"github.com/Souvik-223/rhino-framework/backend/authdb"
)

type credentialsRequest struct {
	Username string `json:"username" binding:"required"`
	Password string `json:"password" binding:"required,min=8"`
}

func (s *Server) handleRegister(c *gin.Context) {
	var req credentialsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	user, err := s.users.CreateUser(c.Request.Context(), req.Username, req.Password)
	if err != nil {
		if err == authdb.ErrUsernameTaken {
			c.JSON(http.StatusConflict, gin.H{"error": "username already taken"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not create account"})
		return
	}

	s.startSession(c, user.ID)
	c.JSON(http.StatusCreated, gin.H{"username": user.Username})
}

func (s *Server) handleLogin(c *gin.Context) {
	var req credentialsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	user, err := s.users.Authenticate(c.Request.Context(), req.Username, req.Password)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid username or password"})
		return
	}

	s.startSession(c, user.ID)
	c.JSON(http.StatusOK, gin.H{"username": user.Username})
}

func (s *Server) handleLogout(c *gin.Context) {
	session := sessions.Default(c)
	session.Clear()
	session.Save()
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func (s *Server) handleMe(c *gin.Context) {
	userID := c.MustGet("userID").(string)
	user, err := s.users.GetByID(c.Request.Context(), userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not load user"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"username": user.Username})
}

func (s *Server) startSession(c *gin.Context, userID string) {
	session := sessions.Default(c)
	session.Set(sessionUserIDKey, userID)
	session.Save()
}
