package backend

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/Souvik-223/rhino-framework/drivepool"
	"github.com/Souvik-223/rhino-framework/drivepool/manifest"
)

// pool resolves the calling user's Pool via the cache, writing a JSON
// error response and returning ok=false on failure so handlers can just
// `if !ok { return }`.
func (s *Server) pool(c *gin.Context) (*drivepool.Pool, bool) {
	userID := c.MustGet("userID").(string)
	p, err := s.cache.get(c.Request.Context(), userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("open pool: %v", err)})
		return nil, false
	}
	return p, true
}

func (s *Server) handleListAccounts(c *gin.Context) {
	pool, ok := s.pool(c)
	if !ok {
		return
	}
	statuses, err := pool.ListAccountStatus(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	out := make([]gin.H, 0, len(statuses))
	for _, st := range statuses {
		entry := gin.H{"label": st.Label}
		if st.Err != nil {
			entry["error"] = st.Err.Error()
		} else {
			entry["limit"] = st.Limit
			entry["usage"] = st.Usage
			entry["available"] = st.Available
			entry["unlimited"] = st.Unlimited
			if st.PhotoURL != "" {
				entry["photoUrl"] = st.PhotoURL
			}
		}
		out = append(out, entry)
	}
	c.JSON(http.StatusOK, out)
}

func (s *Server) handleRemoveAccount(c *gin.Context) {
	pool, ok := s.pool(c)
	if !ok {
		return
	}
	label := c.Param("label")
	force, _ := strconv.ParseBool(c.Query("force"))
	if err := pool.RemoveAccount(c.Request.Context(), label, force); err != nil {
		if errors.Is(err, drivepool.ErrAccountInUse) {
			c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	c.Status(http.StatusNoContent)
}

func (s *Server) handleListFiles(c *gin.Context) {
	pool, ok := s.pool(c)
	if !ok {
		return
	}
	prefix := c.Query("prefix")
	files, err := pool.ListWithAccounts(c.Request.Context(), prefix)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	out := make([]gin.H, 0, len(files))
	for _, f := range files {
		out = append(out, gin.H{
			"name":       f.Name,
			"size":       f.Size,
			"status":     f.Status,
			"modifiedAt": f.ModifiedAt,
			"accounts":   f.Accounts,
			"degraded":   f.Degraded,
		})
	}
	c.JSON(http.StatusOK, out)
}

// handleUploadFile streams the multipart file part straight into
// pool.PutStream — no temp file on the server at all (see
// plans/web_portal.md §1.2). Virtual file names may contain "/" (matching
// the CLI's ls/rm prefix semantics), so file identity travels as a query
// param on the sibling routes below, not as a URL path segment.
func (s *Server) handleUploadFile(c *gin.Context) {
	pool, ok := s.pool(c)
	if !ok {
		return
	}

	fileHeader, err := c.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": `missing "file" form field`})
		return
	}
	virtualName := c.PostForm("name")
	if virtualName == "" {
		virtualName = fileHeader.Filename
	}

	f, err := fileHeader.Open()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	defer f.Close()

	if err := pool.PutStream(c.Request.Context(), f, fileHeader.Size, virtualName); err != nil {
		if errors.Is(err, manifest.ErrNameExists) {
			c.JSON(http.StatusConflict, gin.H{"error": fmt.Sprintf("a file named %q already exists — delete it permanently first if you want to reuse the name", virtualName)})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, gin.H{"name": virtualName})
}

func (s *Server) handleDownloadFile(c *gin.Context) {
	pool, ok := s.pool(c)
	if !ok {
		return
	}
	name := c.Query("name")
	if name == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing \"name\" query param"})
		return
	}

	c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=%q", name))
	c.Header("Content-Type", "application/octet-stream")
	if err := pool.GetStream(c.Request.Context(), name, c.Writer); err != nil {
		// The response may already be partially written by the time a
		// chunk fails partway through a stream — there's no clean way to
		// retract bytes already sent to the client at this point.
		c.AbortWithError(http.StatusInternalServerError, err) //nolint:errcheck
	}
}

func (s *Server) handleDeleteFile(c *gin.Context) {
	pool, ok := s.pool(c)
	if !ok {
		return
	}
	name := c.Query("name")
	if name == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing \"name\" query param"})
		return
	}
	purge, _ := strconv.ParseBool(c.Query("purge"))

	if err := pool.Remove(c.Request.Context(), name, purge); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	c.Status(http.StatusNoContent)
}
