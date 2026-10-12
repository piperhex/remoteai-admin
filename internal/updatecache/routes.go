package updatecache

import (
	"context"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/codex-switch/admin-go/internal/platform"
	"github.com/gin-gonic/gin"
)

// Register serves only already cached official assets. Public requests cannot trigger fetches or choose upstream URLs.
func Register(router *gin.Engine, config platform.Config) func() {
	ctx, cancel := context.WithCancel(context.Background())
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if proxy, err := url.Parse(config.Get("UPDATE_CACHE_PROXY", "")); err == nil && proxy.Host != "" {
		transport.Proxy = http.ProxyURL(proxy)
	}
	s := &cache{root: config.Get("UPDATE_CACHE_DIR", ""), catalogURL: releaseAPI,
		client:  &http.Client{Transport: transport, Timeout: 20 * time.Minute},
		records: map[string]record{}, done: make(chan struct{})}
	if s.root != "" {
		go s.run(ctx)
	} else {
		close(s.done)
	}
	slots := make(chan struct{}, 16)
	handler := func(c *gin.Context) {
		select {
		case slots <- struct{}{}:
			defer func() { <-slots }()
		default:
			c.Status(http.StatusServiceUnavailable)
			return
		}
		s.serve(c)
	}
	router.GET("/update-packages/:version/:name", handler)
	router.HEAD("/update-packages/:version/:name", handler)
	router.GET("/update-packages", s.status)
	return func() { cancel(); <-s.done; transport.CloseIdleConnections() }
}

func (s *cache) serve(c *gin.Context) {
	version, name := c.Param("version"), c.Param("name")
	if versionParts(version) == nil || !namePattern.MatchString(name) {
		c.Status(http.StatusNotFound)
		return
	}
	if len(c.GetHeader("Range")) > 128 || strings.Contains(c.GetHeader("Range"), ",") {
		c.Status(http.StatusRequestedRangeNotSatisfiable)
		return
	}
	s.mu.RLock()
	entry, ok := s.records[key(version, name)]
	var file *os.File
	var err error
	if ok {
		file, err = os.Open(filepath.Join(s.root, version, name))
	}
	s.mu.RUnlock()
	if !ok || err != nil {
		c.Status(http.StatusNotFound)
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() != entry.Asset.Size {
		c.Status(http.StatusNotFound)
		return
	}
	c.Header("ETag", `"`+entry.Asset.Digest+`"`)
	c.Header("Cache-Control", "public, max-age=3600")
	c.Header("Content-Type", "application/octet-stream")
	c.Header("X-Content-Type-Options", "nosniff")
	http.ServeContent(c.Writer, c.Request, name, info.ModTime(), file)
}

func (s *cache) status(c *gin.Context) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	versions := append([]string{}, s.versions...)
	sort.Strings(versions)
	var bytes int64
	for _, entry := range s.records {
		bytes += entry.Asset.Size
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, gin.H{"versions": versions, "files": len(s.records), "bytes": bytes})
}
