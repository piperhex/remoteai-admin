package updatecache

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
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

type transport func(*http.Request) (*http.Response, error)

func (f transport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func fixture(t *testing.T) (*cache, *[]release, []byte) {
	t.Helper()
	body := bytes.Repeat([]byte("official fixture"), 100)
	versions := []release{}
	s := &cache{root: t.TempDir(), records: map[string]record{}, catalogURL: releaseAPI}
	s.client = &http.Client{Transport: transport(func(r *http.Request) (*http.Response, error) {
		data := body
		if r.URL.String() == releaseAPI {
			data, _ = json.Marshal(versions)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(data)), Header: make(http.Header)}, nil
	})}
	return s, &versions, body
}

func released(tag string, body []byte) release {
	names := []string{"android.apk", "desktop.exe"}
	value := release{Tag: tag}
	for _, name := range names {
		value.Assets = append(value.Assets, asset{Name: name, URL: releasePrefix + tag + "/" + name,
			Digest: fmt.Sprintf("sha256:%x", sha256.Sum256(body)), Size: int64(len(body))})
	}
	return value
}

func TestRetainsThreeVersionsWithAllPlatformsAndNeverResurrectsOldFiles(t *testing.T) {
	s, versions, body := fixture(t)
	for _, tag := range []string{"v1.9.0", "v1.8.0", "v1.7.0"} {
		*versions = append(*versions, released(tag, body))
	}
	if err := s.refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	*versions = append(*versions, released("v1.10.0", body))
	if err := s.refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(s.records) != 6 || len(s.versions) != 3 {
		t.Fatalf("incorrect retained catalog: %v", s.versions)
	}
	if _, err := os.Stat(filepath.Join(s.root, "v1.7.0")); !os.IsNotExist(err) {
		t.Fatal("old version remains on disk")
	}
	router := gin.New()
	router.GET("/update-packages/:version/:name", s.serve)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest("GET", "/update-packages/v1.7.0/android.apk", nil))
	if response.Code != 404 {
		t.Fatal("an old URL resurrected an evicted package")
	}
}

func TestRangesHeadAndIntegrityAcrossRestart(t *testing.T) {
	s, versions, body := fixture(t)
	*versions = []release{released("v2.0.0", body)}
	if err := s.refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	restarted := &cache{root: s.root, records: map[string]record{}}
	if err := restarted.restore(); err != nil {
		t.Fatal(err)
	}
	router := gin.New()
	router.GET("/update-packages/:version/:name", restarted.serve)
	router.HEAD("/update-packages/:version/:name", restarted.serve)
	for _, method := range []string{"GET", "HEAD"} {
		request := httptest.NewRequest(method, "/update-packages/v2.0.0/android.apk", nil)
		request.Header.Set("Range", "bytes=13-120")
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		if response.Code != 206 || response.Header().Get("Content-Range") != fmt.Sprintf("bytes 13-120/%d", len(body)) {
			t.Fatalf("invalid %s range response: %d", method, response.Code)
		}
		if method == "GET" && !bytes.Equal(response.Body.Bytes(), body[13:121]) {
			t.Fatal("incorrect range payload")
		}
		if method == "HEAD" && response.Body.Len() != 0 {
			t.Fatal("HEAD sent a body")
		}
	}
	if err := os.WriteFile(filepath.Join(s.root, "v2.0.0", "android.apk"), bytes.Repeat([]byte("x"), len(body)), 0600); err != nil {
		t.Fatal(err)
	}
	restarted.records = map[string]record{}
	if err := restarted.restore(); err != nil {
		t.Fatal(err)
	}
	if _, exists := restarted.records["v2.0.0/android.apk"]; exists {
		t.Fatal("corrupt payload advertised after restart")
	}
}

func TestCorruptionAndInterruptedBodiesAreNeverPublished(t *testing.T) {
	s, _, body := fixture(t)
	entry := record{Version: "v1.0.0", Asset: released("v1.0.0", body).Assets[0]}
	for _, invalid := range [][]byte{body[:7], append(append([]byte{}, body...), 1), bytes.Repeat([]byte("x"), len(body))} {
		if s.store(entry, bytes.NewReader(invalid)) == nil {
			t.Fatal("accepted invalid payload")
		}
		if len(s.records) != 0 {
			t.Fatal("published incomplete package")
		}
		files, err := os.ReadDir(s.root)
		if err != nil || len(files) != 0 {
			t.Fatal("left an incomplete payload on disk")
		}
	}
}

func TestCatalogAuthorityAndTemporaryCleanup(t *testing.T) {
	s, _, body := fixture(t)
	values := []release{released("v2.0.0", body), released("v10.0.0", body), released("v3.0.0", body), released("v1.0.0", body)}
	values = append(values, release{Tag: "v99.0.0", Prerelease: true}, release{Tag: "v98.0.0", Draft: true})
	selected, err := selectReleases(values)
	if err != nil || selected[0].Tag != "v10.0.0" || selected[2].Tag != "v2.0.0" {
		t.Fatal("incorrect version ordering")
	}
	bad := released("v2.0.0", body).Assets[0]
	for _, invalid := range []asset{
		{Name: "../outside.apk", Size: bad.Size, Digest: bad.Digest, URL: bad.URL},
		{Name: bad.Name, Size: bad.Size, Digest: "", URL: bad.URL},
		{Name: bad.Name, Size: bad.Size, Digest: bad.Digest, URL: strings.Replace(bad.URL, "github.com", "evil.test", 1)},
	} {
		if supportedAsset("v2.0.0", invalid) {
			t.Fatal("accepted untrusted asset")
		}
	}
	for _, name := range []string{".download-crash", ".index-crash", "unrelated.txt"} {
		if err := os.WriteFile(filepath.Join(s.root, name), []byte("x"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.cleanTemporary(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(s.root, "unrelated.txt")); err != nil {
		t.Fatal("removed an unrelated file")
	}
}

func TestPrefetchUsesBoundedRangesAndRejectsIncorrectRangeHeaders(t *testing.T) {
	s, _, _ := fixture(t)
	body := bytes.Repeat([]byte("a"), 16*1024*1024+17)
	entry := record{Version: "v1.0.0", Asset: released("v1.0.0", body).Assets[0]}
	requests := 0
	invalid := false
	s.client.Transport = transport(func(r *http.Request) (*http.Response, error) {
		requests++
		var start, end int
		if _, err := fmt.Sscanf(r.Header.Get("Range"), "bytes=%d-%d", &start, &end); err != nil {
			t.Fatal(err)
		}
		if end-start+1 > 8*1024*1024 {
			t.Fatal("unbounded upstream response")
		}
		header := make(http.Header)
		header.Set("Content-Length", strconv.Itoa(end-start+1))
		header.Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, len(body)))
		if invalid {
			header.Set("Content-Range", "bytes 1-2/3")
		}
		return &http.Response{StatusCode: 206, Header: header,
			Body: io.NopCloser(bytes.NewReader(body[start : end+1]))}, nil
	})
	if err := s.fetch(context.Background(), entry); err != nil {
		t.Fatal(err)
	}
	if requests != 3 || s.verifyFile(entry) != nil {
		t.Fatal("range prefetch did not produce the complete verified payload")
	}
	invalid = true
	entry.Version = "v1.0.1"
	entry.Asset.URL = strings.Replace(entry.Asset.URL, "v1.0.0", "v1.0.1", 1)
	if err := s.fetch(context.Background(), entry); err == nil {
		t.Fatal("accepted incorrect upstream Content-Range")
	}
	if _, ok := s.records[key(entry.Version, entry.Asset.Name)]; ok {
		t.Fatal("published invalid range output")
	}
}
