package platform

import (
	"strings"

	"github.com/gin-gonic/gin"
)

// IsLoungeRequest excludes lounge transport and moderation from shared diagnostic logging.
func IsLoungeRequest(c *gin.Context) bool {
	path := c.Request.URL.Path
	return path == "/lounge" || strings.HasPrefix(path, "/lounge/") ||
		path == "/admin/api/lounge" || strings.HasPrefix(path, "/admin/api/lounge/")
}
