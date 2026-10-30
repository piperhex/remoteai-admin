package server

import (
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func compressionHandler(contentType string, extra http.Header) http.Handler {
	router := transportRouter()
	router.GET("/resource", func(c *gin.Context) {
		for key, values := range extra {
			c.Writer.Header()[key] = values
		}
		c.Data(http.StatusOK, contentType, []byte(strings.Repeat("resource content ", 200)))
	})
	return Handler(router)
}

func decompressedBody(t *testing.T, response *httptest.ResponseRecorder) string {
	t.Helper()
	reader, err := gzip.NewReader(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	data, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestCompressionNegotiation(t *testing.T) {
	handler := compressionHandler("application/json; charset=utf-8", nil)
	cases := []struct {
		encoding string
		gzip     bool
	}{
		{"", false}, {"identity", false}, {"br", false}, {"gzip", true},
		{"br, gzip;q=0.5", true}, {"GZip;Q=1", true}, {"*;q=0.5", true},
		{"gzip;q=0, *;q=1", false}, {"gzip;q=bogus", false}, {"gzip;q=NaN", false},
		{"gzip;q=-1", false}, {"gzip;q=2", false}, {"gzip;q=0.5, identity;q=1", false},
	}
	for _, item := range cases {
		t.Run(item.encoding, func(t *testing.T) {
			response := responseFor(handler, "GET", "/resource", http.Header{"Accept-Encoding": {item.encoding}})
			if (response.Header().Get("Content-Encoding") == "gzip") != item.gzip {
				t.Fatalf("headers = %v", response.Header())
			}
			if response.Header().Get("Vary") != "Origin, Accept-Encoding" {
				t.Fatalf("Vary = %q", response.Header().Get("Vary"))
			}
			if item.gzip && decompressedBody(t, response) != strings.Repeat("resource content ", 200) {
				t.Fatal("decoded resource changed")
			}
		})
	}
}

func TestCompressionPreservesConditionalRequestsAndHead(t *testing.T) {
	handler := compressionHandler("text/css", nil)
	plain := responseFor(handler, "GET", "/resource", http.Header{})
	headers := http.Header{"Accept-Encoding": {"gzip"}}
	compressed := responseFor(handler, "GET", "/resource", headers)
	compressedLength := strconv.Itoa(compressed.Body.Len())
	if compressed.Header().Get("Content-Length") != compressedLength ||
		compressed.Header().Get("ETag") != plain.Header().Get("ETag") {
		t.Fatalf("headers = %v", compressed.Header())
	}
	head := responseFor(handler, "HEAD", "/resource", headers)
	if head.Body.Len() != 0 || head.Header().Get("Content-Length") != compressedLength ||
		head.Header().Get("Content-Encoding") != "gzip" {
		t.Fatalf("HEAD headers = %v", head.Header())
	}
	headers.Set("If-None-Match", plain.Header().Get("ETag"))
	unchanged := responseFor(handler, "GET", "/resource", headers)
	if unchanged.Code != 304 || unchanged.Body.Len() != 0 ||
		unchanged.Header().Get("Content-Encoding") != "" || unchanged.Header().Get("Content-Length") != "" ||
		!strings.Contains(unchanged.Header().Get("Vary"), "Accept-Encoding") {
		t.Fatalf("304 response = %d %v", unchanged.Code, unchanged.Header())
	}
}

func TestCompressionContentTypes(t *testing.T) {
	for _, contentType := range []string{"text/html", "text/css", "application/javascript", "image/svg+xml"} {
		response := responseFor(compressionHandler(contentType, nil), "GET", "/resource",
			http.Header{"Accept-Encoding": {"gzip"}})
		if response.Header().Get("Content-Encoding") != "gzip" {
			t.Errorf("%s was not compressed", contentType)
		}
	}
	for _, contentType := range []string{"image/png", "application/zip", "text/event-stream", "application/octet-stream"} {
		response := responseFor(compressionHandler(contentType, nil), "GET", "/resource",
			http.Header{"Accept-Encoding": {"gzip"}})
		if response.Header().Get("Content-Encoding") != "" || response.Body.Len() != 3400 {
			t.Errorf("%s was modified", contentType)
		}
	}
}

func TestCompressionExclusions(t *testing.T) {
	cases := []http.Header{
		{"Cache-Control": {"public, no-transform"}}, {"Content-Encoding": {"br"}},
		{"Content-Range": {"bytes 0-3399/5000"}},
	}
	for _, headers := range cases {
		response := responseFor(compressionHandler("text/plain", headers), "GET", "/resource",
			http.Header{"Accept-Encoding": {"gzip"}})
		if response.Body.String() != strings.Repeat("resource content ", 200) {
			t.Fatalf("excluded response changed: %v", headers)
		}
	}
	handler := Handler(transportRouter())
	small := responseFor(handler, "GET", "/items/small", http.Header{"Accept-Encoding": {"gzip"}})
	if small.Header().Get("Content-Encoding") != "" || small.Body.String() != "small" {
		t.Fatal("small response was compressed")
	}
	upgrade := responseFor(compressionHandler("text/plain", nil), "GET", "/resource",
		http.Header{"Upgrade": {"websocket"}, "Accept-Encoding": {"gzip"}})
	if upgrade.Header().Get("Content-Encoding") != "" {
		t.Fatal("WebSocket transport was compressed")
	}
}

func TestCompressedStaticResourcesAndRanges(t *testing.T) {
	root := t.TempDir()
	data := strings.Repeat("body{color:green}\n", 200)
	if err := os.WriteFile(filepath.Join(root, "app.css"), []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	router := transportRouter()
	staticRoutes(router, root)
	handler := Handler(router)
	headers := http.Header{"Accept-Encoding": {"gzip"}}
	compressed := responseFor(handler, "GET", "/app.css", headers)
	if decompressedBody(t, compressed) != data {
		t.Fatal("static content changed")
	}
	headers.Set("If-None-Match", compressed.Header().Get("ETag"))
	unchanged := responseFor(handler, "GET", "/app.css", headers)
	if unchanged.Code != 304 || !strings.Contains(unchanged.Header().Get("Vary"), "Accept-Encoding") {
		t.Fatalf("static 304 = %d %v", unchanged.Code, unchanged.Header())
	}
	headers.Del("If-None-Match")
	headers.Set("Range", "bytes=0-1500")
	partial := responseFor(handler, "GET", "/app.css", headers)
	if partial.Code != 206 || partial.Body.String() != data[:1501] || partial.Header().Get("Content-Encoding") != "" {
		t.Fatalf("range response = %d %v", partial.Code, partial.Header())
	}
}
