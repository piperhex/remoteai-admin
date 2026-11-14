package browserextensions

import (
	"errors"
	"io"
	"net/http"
	"regexp"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/piperhex/remoteai-admin/internal/platform"
	"gorm.io/gorm"
)

type service struct{ store Store }

func Register(router *gin.Engine, deps *platform.Dependencies) error {
	register(router, deps, databaseStore{deps.DB})
	return nil
}

func register(router *gin.Engine, deps *platform.Dependencies, store Store) {
	s := service{store}
	router.GET("/browser-extensions", s.list(true))
	router.GET("/browser-extensions/:id/archive", validID, s.download)
	admin := router.Group("/admin/api/browser-extensions")
	read := deps.RequirePermissions("admin.browser-extensions.read")
	manage := deps.RequirePermissions("admin.browser-extensions.manage")
	admin.GET("", read, s.list(false))
	admin.POST("", manage, s.upload)
	admin.POST("/:id/archive", manage, validID, s.upload)
	admin.PATCH("/:id", manage, validID, s.publish)
	admin.DELETE("/:id", manage, validID, s.remove)
}

func validID(c *gin.Context) {
	if _, err := uuid.Parse(c.Param("id")); err != nil {
		c.AbortWithStatus(404)
	}
}

func (s service) list(public bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		items, err := s.store.List(c.Request.Context(), public)
		platform.Respond(c, gin.H{"items": items}, err)
	}
}

func fail(c *gin.Context, status int, code, message string) {
	c.AbortWithStatusJSON(status, gin.H{"statusCode": status, "code": code, "message": message})
}

func respond(c *gin.Context, value interface{}, err error) {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		fail(c, 404, "browserExtensions.missing", MessageMissing)
		return
	}
	platform.Respond(c, value, err)
}

func (s service) upload(c *gin.Context) {
	data, err := io.ReadAll(http.MaxBytesReader(c.Writer, c.Request.Body, MaxArchiveBytes))
	if err != nil {
		fail(c, 413, "browserExtensions.tooLarge", MessageTooLarge)
		return
	}
	upload, err := ReadArchive(data)
	if err != nil {
		fail(c, 400, "browserExtensions.invalidArchive", MessageInvalidArchive)
		return
	}
	upload.Item.ID = c.Param("id")
	item, err := s.store.Upload(c.Request.Context(), upload, platform.User(c))
	respond(c, item, err)
}

func (s service) publish(c *gin.Context) {
	var input struct {
		Published *bool `json:"published"`
	}
	if c.ShouldBindJSON(&input) != nil || input.Published == nil {
		platform.Fail(c, 400, "Bad Request")
		return
	}
	item, err := s.store.Publish(c.Request.Context(), c.Param("id"), *input.Published, platform.User(c))
	respond(c, item, err)
}

func (s service) remove(c *gin.Context) {
	err := s.store.Delete(c.Request.Context(), c.Param("id"), platform.User(c))
	respond(c, gin.H{"ok": true}, err)
}

var digestPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

func (s service) download(c *gin.Context) {
	digest := c.Query("sha256")
	if !digestPattern.MatchString(digest) {
		c.Status(404)
		return
	}
	data, err := s.store.Download(c.Request.Context(), c.Param("id"), digest)
	if err != nil {
		respond(c, nil, err)
		return
	}
	// Never cache packages beyond publication checks: unpublishing must stop new downloads.
	c.Header("Cache-Control", "no-store")
	c.Header("X-Content-Type-Options", "nosniff")
	c.Header("Content-Disposition", `attachment; filename="extension.zip"`)
	c.Data(200, "application/zip", data)
}
