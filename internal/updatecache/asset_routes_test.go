package updatecache

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestAssetIDServesVerifiedRangesAcrossRestartAndEviction(t *testing.T) {
	s, versions, body := fixture(t)
	version := released("v2.0.0", body)
	version.Assets[0].ID = 652496018
	*versions = []release{version}
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
		request := httptest.NewRequest(method, "/update-packages/assets/652496018", nil)
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
		if response.Header().Get("ETag") != `"`+version.Assets[0].Digest+`"` {
			t.Fatal("asset ID lost verified digest")
		}
	}
	if err := restarted.retain(map[string]bool{}); err != nil {
		t.Fatal(err)
	}
	assertMissingAsset(t, router, "652496018")
}

func TestUnknownAndInvalidAssetIDsNeverFetchOrExposeFiles(t *testing.T) {
	s, _, _ := fixture(t)
	s.client.Transport = transport(func(*http.Request) (*http.Response, error) {
		t.Fatal("public request attempted an upstream download")
		return nil, nil
	})
	router := gin.New()
	router.GET("/update-packages/:version/:name", s.serve)
	for _, id := range []string{"42", "0", "-1", "+1", "01", "9223372036854775808", "index.json"} {
		assertMissingAsset(t, router, id)
	}
}

func assertMissingAsset(t *testing.T, router *gin.Engine, id string) {
	t.Helper()
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest("GET", "/update-packages/assets/"+id, nil))
	if response.Code != http.StatusNotFound {
		t.Fatalf("asset %s returned %d", id, response.Code)
	}
}

func TestCatalogRefreshAddsIDsToExistingCacheWithoutDownloadingAgain(t *testing.T) {
	s, versions, body := fixture(t)
	*versions = []release{released("v2.0.0", body)}
	if err := s.refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	(*versions)[0].Assets[0].ID = 42
	original := s.client.Transport
	s.client.Transport = transport(func(request *http.Request) (*http.Response, error) {
		if request.URL.String() != releaseAPI {
			t.Fatal("downloaded an already verified file again")
		}
		return original.RoundTrip(request)
	})
	if err := s.refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if entry, ok := s.lookup("assets", "42"); !ok || entry.Asset.Name != "android.apk" {
		t.Fatal("existing verified cache did not gain an asset ID")
	}
}

func TestDebianUpdaterAssetsCanBeCached(t *testing.T) {
	value := released("v2.0.0", []byte("deb fixture")).Assets[0]
	value.Name = "Remote.AI_2.0.0_linux-amd64.deb"
	value.URL = releasePrefix + "v2.0.0/" + value.Name
	if !supportedAsset("v2.0.0", value) {
		t.Fatal("Debian updater package excluded from cache")
	}
	value.URL = strings.Replace(value.URL, "piperhex", "untrusted", 1)
	if supportedAsset("v2.0.0", value) {
		t.Fatal("accepted a non-official Debian package")
	}
}
