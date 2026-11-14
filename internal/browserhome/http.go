package browserhome

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"path"
	"regexp"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/piperhex/remoteai-admin/internal/platform"
	"gorm.io/gorm"
)

type service struct{ store Store }

func Register(router *gin.Engine, deps *platform.Dependencies) error {
	register(router, deps, databaseStore{db: deps.DB})
	return nil
}

func register(router *gin.Engine, deps *platform.Dependencies, store Store) {
	s := service{store}
	router.GET("/browser-home/current", s.current)
	router.GET("/browser-home/packages/:md5", s.download)
	router.GET(sitePrefix+":revision/*path", s.asset)
	router.HEAD(sitePrefix+":revision/*path", s.asset)
	read := deps.RequirePermissions("admin.browser-home.read")
	manage := deps.RequirePermissions("admin.browser-home.manage")
	router.GET("/admin/api/browser-home", read, s.read)
	router.POST("/admin/api/browser-home/archive", manage, s.upload)
	router.PATCH("/admin/api/browser-home", manage, s.enable)
}

func (s service) current(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	settings, err := s.store.Read(c.Request.Context())
	entry := ""
	if settings.Enabled && settings.Revision != "" {
		entry = sitePrefix + settings.Revision + "/index.html"
	}
	platform.Respond(c, gin.H{"enabled": entry != "", "entryPath": entry,
		"revision": settings.Revision, "md5": settings.MD5}, err)
}

var digestPattern = regexp.MustCompile(`^[a-f0-9]{32}$`)

func (s service) download(c *gin.Context) {
	digest := c.Param("md5")
	if !digestPattern.MatchString(digest) {
		c.Status(404)
		return
	}
	value, err := s.store.Package(c.Request.Context(), digest)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		c.Status(404)
		return
	}
	if err != nil {
		platform.Respond(c, nil, err)
		return
	}
	c.Header("Cache-Control", "public, max-age=31536000, immutable")
	c.Header("ETag", `"`+digest+`"`)
	c.Set("skipETag", true)
	c.Data(200, "application/json", value.Data)
}

func (s service) read(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	settings, err := s.store.Read(c.Request.Context())
	platform.Respond(c, settings, err)
}

func fail(c *gin.Context, status int, code, message string) {
	c.AbortWithStatusJSON(status, gin.H{"statusCode": status, "code": code, "message": message})
}

func (s service) upload(c *gin.Context) {
	// A raw ZIP body avoids multipart temporary files and is bounded before parsing.
	data, err := io.ReadAll(http.MaxBytesReader(c.Writer, c.Request.Body, MaxArchiveBytes))
	if err != nil {
		fail(c, 413, "browserHome.tooLarge", MessageTooLarge)
		return
	}
	assets, err := ReadArchive(data)
	if errors.Is(err, errMissingIndex) {
		fail(c, 400, "browserHome.missingIndex", MessageMissingIndex)
		return
	}
	if err != nil {
		fail(c, 400, "browserHome.invalidArchive", MessageInvalidArchive)
		return
	}
	name := strings.TrimSpace(c.Query("name"))
	if name == "" || len(name) > 200 || strings.ContainsAny(name, "\x00\r\n/\\") {
		name = "dist.zip"
	}
	settings, err := s.store.Upload(c.Request.Context(), assets, name, platform.User(c))
	if errors.Is(err, errInvalidArchive) {
		fail(c, 400, "browserHome.invalidArchive", MessageInvalidArchive)
		return
	}
	platform.Respond(c, settings, err)
}

func (s service) enable(c *gin.Context) {
	var input struct {
		Enabled *bool `json:"enabled"`
	}
	if err := c.ShouldBindJSON(&input); err != nil || input.Enabled == nil {
		platform.Fail(c, 400, "Bad Request")
		return
	}
	settings, err := s.store.Enable(c.Request.Context(), *input.Enabled, platform.User(c))
	if errors.Is(err, errNoUpload) {
		fail(c, 400, "browserHome.noUpload", MessageNoUpload)
		return
	}
	platform.Respond(c, settings, err)
}

func (s service) asset(c *gin.Context) {
	revision, name := c.Param("revision"), strings.TrimPrefix(c.Param("path"), "/")
	if _, err := uuid.Parse(revision); err != nil {
		c.Status(404)
		return
	}
	if name == "" {
		name = "index.html"
	}
	if !safePath(name) {
		c.Status(404)
		return
	}
	asset, err := s.store.Asset(c.Request.Context(), revision, name)
	if errors.Is(err, gorm.ErrRecordNotFound) && path.Ext(name) == "" {
		asset, err = s.store.Asset(c.Request.Context(), revision, "index.html")
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		c.Status(404)
		return
	}
	if err != nil {
		platform.Respond(c, nil, err)
		return
	}
	serveAsset(c, asset)
}

func serveAsset(c *gin.Context, asset Asset) {
	// Enforce isolation even when an administrator opens a resource URL directly.
	c.Header("Content-Security-Policy", "sandbox allow-scripts; object-src 'none'; frame-src 'none'")
	c.Header("X-Content-Type-Options", "nosniff")
	c.Header("Referrer-Policy", "no-referrer")
	c.Header("Access-Control-Allow-Origin", "*")
	c.Writer.Header().Del("Access-Control-Allow-Credentials")
	c.Header("Cross-Origin-Resource-Policy", "cross-origin")
	c.Header("Cache-Control", "public, max-age=31536000, immutable")
	c.Header("Content-Type", asset.ContentType)
	c.Header("ETag", `"`+asset.Revision+":"+asset.Path+`"`)
	c.Set("skipETag", true)
	http.ServeContent(c.Writer, c.Request, asset.Path, time.Time{}, bytes.NewReader(asset.Data))
}
