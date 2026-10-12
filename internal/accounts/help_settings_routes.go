package accounts

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/codex-switch/admin-go/internal/platform"
	"github.com/gin-gonic/gin"
)

const maxHelpSettingsBytes = 128 * 1024

func (s *service) registerHelpSettings(router *gin.Engine) {
	router.GET("/help-assistant/settings", noStore, endpoint(func(c *gin.Context) (interface{}, error) {
		settings, err := readHelpSettings(c.Request.Context(), s.deps.DB)
		if err != nil {
			return nil, platform.NewError(503, "助手设置暂时不可用，请稍后重试。")
		}
		if _, err = normalizeHelpSettings(settings); err != nil {
			return nil, platform.NewError(503, "助手尚未配置，请联系管理员。")
		}
		return settings, nil
	}))
	admin := router.Group("/admin/api/help-assistant", s.deps.RequireAuth())
	admin.GET("", s.deps.RequirePermissions("admin.official-accounts.read"), noStore,
		endpoint(func(c *gin.Context) (interface{}, error) {
			return readHelpSettings(c.Request.Context(), s.deps.DB)
		}))
	admin.PATCH("", s.deps.RequirePermissions("admin.official-accounts.manage"), endpoint(s.editHelpSettings))
}

func (s *service) editHelpSettings(c *gin.Context) (interface{}, error) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxHelpSettingsBytes)
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	var input helpSettings
	if decoder.Decode(&input) != nil || decoder.Decode(new(interface{})) != io.EOF {
		return nil, platform.NewError(400, "助手设置格式无效，请检查后重试。")
	}
	input, err := normalizeHelpSettings(input)
	if err != nil {
		return nil, err
	}
	catalog, err := s.readModelCatalog(c.Request.Context())
	if err != nil {
		return nil, platform.NewError(503, "模型列表暂时不可用，请稍后重试。")
	}
	if err = validateHelpModel(input.Model, catalog.Catalog); err != nil {
		return nil, err
	}
	return s.saveHelpSettings(c.Request.Context(), input, platform.User(c))
}
