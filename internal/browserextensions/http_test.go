package browserextensions

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/piperhex/remoteai-admin/internal/platform"
	"gorm.io/gorm"
)

type memoryStore struct{ items map[string]Upload }

func (s *memoryStore) List(_ context.Context, public bool) ([]Item, error) {
	items := []Item{}
	for _, upload := range s.items {
		if !public || upload.Item.Published {
			items = append(items, upload.Item)
		}
	}
	return items, nil
}
func (s *memoryStore) Upload(_ context.Context, upload Upload, _ *platform.Principal) (Item, error) {
	if upload.Item.ID == "" {
		upload.Item.ID = uuid.NewString()
	} else if _, ok := s.items[upload.Item.ID]; !ok {
		return Item{}, gorm.ErrRecordNotFound
	}
	s.items[upload.Item.ID] = upload
	return upload.Item, nil
}
func (s *memoryStore) Publish(_ context.Context, id string, published bool, _ *platform.Principal) (Item, error) {
	upload, ok := s.items[id]
	if !ok {
		return Item{}, gorm.ErrRecordNotFound
	}
	upload.Item.Published = published
	s.items[id] = upload
	return upload.Item, nil
}
func (s *memoryStore) Delete(_ context.Context, id string, _ *platform.Principal) error {
	if _, ok := s.items[id]; !ok {
		return gorm.ErrRecordNotFound
	}
	delete(s.items, id)
	return nil
}
func (s *memoryStore) Download(_ context.Context, id, digest string) ([]byte, error) {
	upload, ok := s.items[id]
	if !ok || !upload.Item.Published || upload.Item.SHA256 != digest {
		return nil, gorm.ErrRecordNotFound
	}
	return upload.Data, nil
}
func marketRouter(store Store, permissions []string) *gin.Engine {
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
func TestPublishDownloadReplaceUnpublishAndDelete(t *testing.T) {
	store := &memoryStore{items: map[string]Upload{}}
	router := marketRouter(store, []string{"admin.browser-extensions.read", "admin.browser-extensions.manage"})
	data := archiveFixture(t, map[string]string{"manifest.json": fixtureManifest})
	if got := request(router, "POST", "/admin/api/browser-extensions", data); got.Code != 201 {
		t.Fatal(got.Code, got.Body)
	}
	var item Item
	for _, upload := range store.items {
		item = upload.Item
	}
	admin := "/admin/api/browser-extensions/" + item.ID
	public := "/browser-extensions/" + item.ID + "/archive?sha256=" + item.SHA256
	if got := request(router, "GET", public, nil); got.Code != 404 {
		t.Fatal("draft downloadable", got.Code)
	}
	if got := request(router, "GET", "/browser-extensions", nil); got.Body.String() != `{"items":[]}` {
		t.Fatal(got.Body)
	}
	for _, published := range []bool{true, false, true} {
		body := []byte(`{"published":false}`)
		if published {
			body = []byte(`{"published":true}`)
		}
		if got := request(router, "PATCH", admin, body); got.Code != 200 {
			t.Fatal(got.Code, got.Body)
		}
		got := request(router, "GET", public, nil)
		if published && (got.Code != 200 || !bytes.Equal(got.Body.Bytes(), data) || got.Header().Get("Cache-Control") != "no-store") {
			t.Fatal(got.Code, got.Header())
		}
		if !published && got.Code != 404 {
			t.Fatal("unpublished package downloadable")
		}
	}
	if got := request(router, "POST", admin+"/archive", data); got.Code != 201 {
		t.Fatal(got.Code, got.Body)
	}
	if got := request(router, "GET", public, nil); got.Code != 404 {
		t.Fatal("replacement implicitly published")
	}
	if got := request(router, "DELETE", admin, nil); got.Code != 200 {
		t.Fatal(got.Code, got.Body)
	}
	if got := request(router, "GET", public, nil); got.Code != 404 {
		t.Fatal("deleted package downloadable")
	}
}
func TestAdminPermissionsAndPublicRead(t *testing.T) {
	for _, permissions := range [][]string{nil, {}, {"admin.browser-extensions.read"}} {
		router := marketRouter(&memoryStore{items: map[string]Upload{}}, permissions)
		for _, route := range [][2]string{{"POST", ""}, {"POST", "/00000000-0000-4000-8000-000000000000/archive"},
			{"PATCH", "/00000000-0000-4000-8000-000000000000"}, {"DELETE", "/00000000-0000-4000-8000-000000000000"}} {
			got := request(router, route[0], "/admin/api/browser-extensions"+route[1], nil)
			if got.Code != 401 && got.Code != 403 {
				t.Fatal("unauthorized mutation", got.Code)
			}
		}
		if got := request(router, "GET", "/browser-extensions", nil); got.Code != 200 {
			t.Fatal(got.Code)
		}
	}
}
