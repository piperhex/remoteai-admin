package updatecache

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestNativeRepositoryCacheSurvivesRestartAndRetainsItsNamespace(t *testing.T) {
	s, versions, body := fixture(t)
	value := released("v2.2.1", body)
	value.Assets = value.Assets[:1]
	value.Assets[0].URL = nativeReleasePrefix + value.Tag + "/" + value.Assets[0].Name
	*versions = []release{value}
	if err := s.refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	restarted := &cache{root: s.root, records: map[string]record{}}
	if err := restarted.restore(); err != nil {
		t.Fatal(err)
	}
	router := gin.New()
	restarted.registerRoutesAt(router, "/native-update-packages")
	for _, method := range []string{http.MethodGet, http.MethodHead} {
		request := httptest.NewRequest(method, "/native-update-packages/v2.2.1/android.apk", nil)
		request.Header.Set("Range", "bytes=13-120")
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		if response.Code != http.StatusPartialContent || response.Header().Get("ETag") != `"`+value.Assets[0].Digest+`"` {
			t.Fatalf("native mirror lost verified range support: %d", response.Code)
		}
		if method == http.MethodGet && !bytes.Equal(response.Body.Bytes(), body[13:121]) {
			t.Fatal("native mirror returned different bytes")
		}
	}
}

func TestNativeCatalogRejectsOtherRepositoriesAndDesktopAssets(t *testing.T) {
	value := released("v2.2.1", []byte("package")).Assets[0]
	value.URL = strings.Replace(value.URL, releasePrefix, nativeReleasePrefix, 1)
	if !supportedAsset("v2.2.1", value) {
		t.Fatal("official native APK rejected")
	}
	for _, repository := range []string{"untrusted", "remoteai-native-evil"} {
		invalid := value
		invalid.URL = strings.Replace(value.URL, "remoteai-native", repository, 1)
		if supportedAsset("v2.2.1", invalid) {
			t.Fatal("untrusted repository accepted")
		}
	}
	value.Name = "desktop.exe"
	value.URL = nativeReleasePrefix + "v2.2.1/" + value.Name
	if supportedAsset("v2.2.1", value) {
		t.Fatal("native cache accepted desktop release metadata")
	}
}
