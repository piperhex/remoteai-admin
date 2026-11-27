package filesharing

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/piperhex/remoteai-admin/internal/platform"
	"gorm.io/gorm"
)

const maxBatch = 500

var fingerprintPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

type service struct{ store store }

func Register(router *gin.Engine, deps *platform.Dependencies) error {
	s := service{store{deps.DB}}
	admin := router.Group("/admin/api/file-sharing", func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		c.Next()
	})
	read := deps.RequirePermissions("admin.file-sharing.read")
	manage := deps.RequirePermissions("admin.file-sharing.manage")
	admin.GET("", read, s.list)
	admin.GET("/:id", read, validFileID, s.status)
	admin.POST("", manage, s.create)
	admin.PUT("/:id/chunks/:index", manage, validFileID, s.upload)
	admin.POST("/:id/complete", manage, validFileID, s.complete)
	admin.PATCH("/:id", manage, validFileID, s.rename)
	admin.POST("/batch/delete", manage, s.remove)
	router.GET("/shared-files/:id", validFileID, s.download)
	router.HEAD("/shared-files/:id", validFileID, s.download)
	return nil
}

func isFileID(id string) bool {
	parsed, err := uuid.Parse(id)
	return err == nil && parsed.Version() == 4 && parsed.String() == id
}

func validFileID(c *gin.Context) {
	if !isFileID(c.Param("id")) {
		c.AbortWithStatus(http.StatusNotFound)
	}
}

func (s service) list(c *gin.Context) {
	items, err := s.store.list(c.Request.Context())
	respond(c, gin.H{"items": items, "chunkBytes": chunkBytes, "maxBytes": maxSize,
		"maxBatch": maxBatch, "uploadWindow": uploadWindow}, err)
}

func (s service) status(c *gin.Context) {
	item, err := s.store.status(c.Request.Context(), c.Param("id"))
	respond(c, item, err)
}

func (s service) create(c *gin.Context) {
	var input struct {
		Name        string `json:"name"`
		Size        *int64 `json:"size"`
		Fingerprint string `json:"fingerprint"`
	}
	if c.ShouldBindJSON(&input) != nil || input.Size == nil || *input.Size < 0 ||
		!fingerprintPattern.MatchString(input.Fingerprint) {
		fail(c, http.StatusBadRequest, "fileSharing.failed")
		return
	}
	if *input.Size > maxSize {
		fail(c, http.StatusRequestEntityTooLarge, "fileSharing.tooLarge")
		return
	}
	if *input.Size == 0 {
		empty := sha256.Sum256(nil)
		if input.Fingerprint != hex.EncodeToString(empty[:]) {
			fail(c, http.StatusBadRequest, "fileSharing.fileMismatch")
			return
		}
	}
	name := strings.TrimSpace(input.Name)
	if !validName(name) {
		fail(c, http.StatusBadRequest, "fileSharing.invalidName")
		return
	}
	item := file{Name: name, Size: *input.Size, Fingerprint: input.Fingerprint}
	err := s.store.create(c.Request.Context(), &item, platform.User(c))
	respond(c, item, err)
}

func (s service) upload(c *gin.Context) {
	index, err := strconv.Atoi(c.Param("index"))
	if err != nil || index < 0 || index >= maxSize/chunkBytes || c.ContentType() != "application/octet-stream" {
		fail(c, http.StatusBadRequest, "fileSharing.interrupted")
		return
	}
	data, err := io.ReadAll(http.MaxBytesReader(c.Writer, c.Request.Body, chunkBytes))
	if err != nil {
		var limit *http.MaxBytesError
		if errors.As(err, &limit) {
			fail(c, http.StatusRequestEntityTooLarge, "fileSharing.interrupted")
			return
		}
		fail(c, http.StatusBadRequest, "fileSharing.interrupted")
		return
	}
	part := chunk{FileID: c.Param("id"), ChunkIndex: index, Data: data}
	if expected := c.GetHeader("X-Upload-SHA256"); expected != "" {
		s.uploadWindowChunk(c, part, expected)
		return
	}
	item, err := s.store.append(c.Request.Context(), part)
	respond(c, item, err)
}

func (s service) uploadWindowChunk(c *gin.Context, part chunk, expected string) {
	digest := sha256.Sum256(part.Data)
	if expected != hex.EncodeToString(digest[:]) {
		respond(c, nil, errFingerprint)
		return
	}
	part.Digest = digest[:]
	if err := s.store.saveWindowChunk(c.Request.Context(), part); err != nil {
		respond(c, nil, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (s service) complete(c *gin.Context) {
	item, err := s.store.complete(c.Request.Context(), c.Param("id"))
	respond(c, item, err)
}

func (s service) rename(c *gin.Context) {
	var input struct {
		Name string `json:"name"`
	}
	if c.ShouldBindJSON(&input) != nil {
		fail(c, http.StatusBadRequest, "fileSharing.invalidName")
		return
	}
	name := strings.TrimSpace(input.Name)
	if !validName(name) {
		fail(c, http.StatusBadRequest, "fileSharing.invalidName")
		return
	}
	err := s.store.rename(c.Request.Context(), file{ID: c.Param("id"), Name: name}, platform.User(c))
	respond(c, gin.H{"ok": true}, err)
}

func (s service) remove(c *gin.Context) {
	var input struct {
		IDs []string `json:"ids"`
	}
	if c.ShouldBindJSON(&input) != nil || len(input.IDs) == 0 || len(input.IDs) > maxBatch {
		fail(c, http.StatusBadRequest, "fileSharing.failed")
		return
	}
	for _, id := range input.IDs {
		if !isFileID(id) {
			fail(c, http.StatusBadRequest, "fileSharing.failed")
			return
		}
	}
	count, err := s.store.remove(c.Request.Context(), input.IDs, platform.User(c))
	respond(c, gin.H{"deleted": count}, err)
}

func validName(name string) bool {
	if name == "" || len(name) > 200 || strings.HasSuffix(name, ".") || strings.ContainsAny(name, `<>:"/\|?*`) {
		return false
	}
	return strings.IndexFunc(name, unicode.IsControl) == -1
}

func fail(c *gin.Context, status int, code string) {
	c.AbortWithStatusJSON(status, gin.H{"statusCode": status, "code": code})
}

func respond(c *gin.Context, value interface{}, err error) {
	switch {
	case errors.Is(err, gorm.ErrRecordNotFound):
		fail(c, http.StatusNotFound, "fileSharing.missing")
	case errors.Is(err, errOffset), errors.Is(err, errIncomplete):
		fail(c, http.StatusConflict, "fileSharing.interrupted")
	case errors.Is(err, errChunkSize):
		fail(c, http.StatusBadRequest, "fileSharing.interrupted")
	case errors.Is(err, errFingerprint):
		fail(c, http.StatusConflict, "fileSharing.fileMismatch")
	default:
		platform.Respond(c, value, err)
	}
}
