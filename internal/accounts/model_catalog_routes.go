package accounts

import (
	"encoding/json"
	"net/http"

	"github.com/codex-switch/admin-go/internal/platform"
	"github.com/gin-gonic/gin"
)

func (s *service) registerModelCatalog(r *gin.Engine) {
	r.GET("/official-models", noStore, s.publicModelCatalog)
	admin := r.Group("/admin/api/official-models", s.deps.RequireAuth())
	admin.GET("", s.deps.RequirePermissions("admin.official-accounts.read"), noStore,
		endpoint(func(c *gin.Context) (interface{}, error) { return s.readModelCatalog(c.Request.Context()) }))
	manage := s.deps.RequirePermissions("admin.official-accounts.manage")
	admin.GET("/settings", s.deps.RequirePermissions("admin.official-accounts.read"), noStore,
		endpoint(func(c *gin.Context) (interface{}, error) { return s.readModelRefreshSettings(c.Request.Context()) }))
	admin.PATCH("/settings", manage, endpoint(s.editModelRefreshSettings))
	admin.PATCH("", manage, endpoint(s.editModelCatalog))
	admin.POST("/refresh", manage, endpoint(func(c *gin.Context) (interface{}, error) {
		return s.refreshModelCatalog(c.Request.Context(), true)
	}))
}

func (s *service) publicModelCatalog(c *gin.Context) {
	snapshot, err := s.readModelCatalog(c.Request.Context())
	if err != nil || snapshot.Catalog == nil {
		platform.Fail(c, http.StatusServiceUnavailable, "模型列表暂时不可用，请稍后重试。")
		return
	}
	c.Header("ETag", modelCatalogETag(snapshot.Catalog))
	c.Header("X-Models-Client-Version", snapshot.ClientVersion)
	c.Data(http.StatusOK, "application/json", snapshot.Catalog)
}

func (s *service) editModelCatalog(c *gin.Context) (interface{}, error) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxModelCatalogBytes)
	var input struct {
		Catalog  json.RawMessage `json:"catalog"`
		Revision string          `json:"revision"`
	}
	if c.ShouldBindJSON(&input) != nil {
		return nil, platform.NewError(400, "模型列表格式无效，请检查后重试。")
	}
	previous, err := s.readModelCatalog(c.Request.Context())
	if err != nil {
		return nil, err
	}
	snapshot := &modelCatalogSnapshot{Catalog: input.Catalog, ClientVersion: previous.ClientVersion, Source: "manual"}
	if err = s.saveModelCatalog(c.Request.Context(), snapshot, input.Revision); err != nil {
		return nil, err
	}
	return snapshot, s.record(platform.User(c), auditOptions{
		Action: "official-models.update", TargetID: snapshot.Revision,
	})
}
