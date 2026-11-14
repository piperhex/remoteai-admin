package browserhome

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/piperhex/remoteai-admin/internal/platform"
	"gorm.io/gorm"
)

type memoryStore struct {
	settings Settings
	assets   []Asset
	pack     Package
}

func (s *memoryStore) Read(context.Context) (Settings, error) { return s.settings, nil }
func (s *memoryStore) Upload(_ context.Context, assets []Asset, name string, _ *platform.Principal) (Settings, error) {
	bundle, pack, err := prepareBundle(assets)
	if err != nil {
		return Settings{}, err
	}
	s.assets, s.pack = assets, pack
	s.settings = Settings{Enabled: s.settings.Enabled, Revision: bundle.Revision, MD5: pack.MD5,
		FileName: name, FileCount: len(assets)}
	return s.settings, nil
}
func (s *memoryStore) Enable(_ context.Context, enabled bool, _ *platform.Principal) (Settings, error) {
	if enabled && s.settings.Revision == "" {
		return Settings{}, errNoUpload
	}
	s.settings.Enabled = enabled
	return s.settings, nil
}
func (s *memoryStore) Package(_ context.Context, digest string) (Package, error) {
	if s.pack.MD5 == digest {
		return s.pack, nil
	}
	return Package{}, gorm.ErrRecordNotFound
}
func (s *memoryStore) Asset(_ context.Context, revision, name string) (Asset, error) {
	for _, asset := range s.assets {
		if asset.Revision == revision && asset.Path == name {
			return asset, nil
		}
	}
	return Asset{}, gorm.ErrRecordNotFound
}

func homeRouter(store Store, permissions []string) *gin.Engine {
	router := gin.New()
	deps := &platform.Dependencies{}
	if permissions != nil {
		deps.Authenticate = func(*gin.Context) (*platform.Principal, error) {
			return &platform.Principal{ID: "admin", Permissions: permissions}, nil
		}
	}
	register(router, deps, store)
	return router
}

func request(router http.Handler, method, path string, body []byte) *httptest.ResponseRecorder {
	response := httptest.NewRecorder()
	req := httptest.NewRequest(method, path, bytes.NewReader(body))
	if method == "PATCH" {
		req.Header.Set("Content-Type", "application/json")
	}
	router.ServeHTTP(response, req)
	return response
}

func TestUploadPreviewEnableAndRestoreDefault(t *testing.T) {
	store := &memoryStore{}
	router := homeRouter(store, []string{"admin.browser-home.read", "admin.browser-home.manage"})
	if got := request(router, "PATCH", "/admin/api/browser-home", []byte(`{"enabled":true}`)); got.Code != 400 {
		t.Fatal(got.Code, got.Body)
	}
	data := archiveFixture(t, map[string]string{"index.html": `<script src="/app.js"></script>`, "app.js": "window.ready=true"})
	if got := request(router, "POST", "/admin/api/browser-home/archive?name=dist.zip", data); got.Code != 201 {
		t.Fatal(got.Code, got.Body)
	}
	if store.settings.Enabled {
		t.Fatal("upload implicitly enabled home")
	}
	if got := request(router, "PATCH", "/admin/api/browser-home", []byte(`{"enabled":true}`)); got.Code != 200 {
		t.Fatal(got)
	}
	current := request(router, "GET", "/browser-home/current", nil)
	var config map[string]interface{}
	if err := json.Unmarshal(current.Body.Bytes(), &config); err != nil {
		t.Fatal(err)
	}
	if config["md5"] != store.pack.MD5 || config["enabled"] != true || current.Header().Get("Cache-Control") != "no-store" {
		t.Fatal(config, current.Header())
	}
	home := request(router, "GET", config["entryPath"].(string), nil)
	if home.Code != 200 || !strings.Contains(home.Header().Get("Content-Security-Policy"), "sandbox allow-scripts") ||
		strings.Contains(home.Header().Get("Content-Security-Policy"), "allow-same-origin") {
		t.Fatal(home.Code, home.Header())
	}
	if home.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Fatal("module CORS missing")
	}
	pack := request(router, "GET", "/browser-home/packages/"+store.pack.MD5, nil)
	if !bytes.Equal(pack.Body.Bytes(), store.pack.Data) {
		t.Fatal("download changed package bytes")
	}
	if got := request(router, "PATCH", "/admin/api/browser-home", []byte(`{"enabled":false}`)); got.Code != 200 {
		t.Fatal(got)
	}
	if got := request(router, "GET", "/browser-home/current", nil); !strings.Contains(got.Body.String(), `"enabled":false`) {
		t.Fatal(got.Body)
	}
}

func TestHomeAdministrationRequiresDedicatedPermissions(t *testing.T) {
	for _, method := range []string{"GET", "POST", "PATCH"} {
		path := "/admin/api/browser-home"
		if method == "POST" {
			path += "/archive"
		}
		if response := request(homeRouter(&memoryStore{}, nil), method, path, nil); response.Code != 401 {
			t.Fatal(response.Code)
		}
		if response := request(homeRouter(&memoryStore{}, []string{}), method, path, nil); response.Code != 403 {
			t.Fatal(response.Code)
		}
	}
	reader := homeRouter(&memoryStore{}, []string{"admin.browser-home.read"})
	if request(reader, "GET", "/admin/api/browser-home", nil).Code != 200 {
		t.Fatal("reader cannot read")
	}
	if request(reader, "PATCH", "/admin/api/browser-home", []byte(`{"enabled":true}`)).Code != 403 {
		t.Fatal("reader can write")
	}
}
