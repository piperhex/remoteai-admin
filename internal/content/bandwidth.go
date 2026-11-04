package content

import (
	"github.com/gin-gonic/gin"
	"github.com/piperhex/remoteai-admin/internal/platform"
)

func (s *service) bandwidth(c *gin.Context) {
	if s.deps.ReadBandwidth == nil {
		platform.Fail(c, 503, "Bandwidth monitoring is unavailable")
		return
	}
	platform.Respond(c, s.deps.ReadBandwidth(), nil)
}
