package accounts

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/codex-switch/admin-go/internal/platform"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
)

func TestModelCatalogValidation(t *testing.T) {
	for _, raw := range []string{`[]`, `{}`, `{"models":[]}`, `{"models":null}`, `{"models":[{}]}`,
		`{"models":[{"slug":" "}]}`, `{"models":[{"slug":"x"},{"id":"x"}]}`, `not json`} {
		if validateModelCatalog([]byte(raw)) == nil {
			t.Fatalf("accepted invalid catalog: %s", raw)
		}
	}
	if validateModelCatalog([]byte(`{"models":[{"slug":"gpt-test","custom":{"enabled":true}}]}`)) != nil {
		t.Fatal("valid catalog rejected")
	}
	if validateModelCatalog([]byte(strings.Repeat(" ", maxModelCatalogBytes+1))) == nil {
		t.Fatal("oversized catalog accepted")
	}
}

func modelCacheTestService(t *testing.T) *service {
	t.Helper()
	address := os.Getenv("MODEL_CATALOG_TEST_REDIS")
	if address == "" {
		t.Skip("set MODEL_CATALOG_TEST_REDIS to an isolated Redis for cache integration tests")
	}
	client := redis.NewClient(&redis.Options{Addr: address})
	keys := []string{modelCatalogKey, modelCatalogLockKey, modelCatalogAttemptKey, modelCatalogSettingsKey}
	if err := client.Del(t.Context(), keys...).Err(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { client.Del(t.Context(), keys...); client.Close() })
	// No database is configured: successful reads and skipped refreshes must stay Redis-only.
	return &service{deps: &platform.Dependencies{Redis: client}}
}

func TestModelCatalogRedisRetentionAndConcurrentEdits(t *testing.T) {
	s := modelCacheTestService(t)
	ctx := t.Context()
	first := &modelCatalogSnapshot{Catalog: json.RawMessage(`{"models":[{"slug":"first"}]}`)}
	if err := s.saveModelCatalog(ctx, first, ""); err != nil {
		t.Fatal(err)
	}
	if ttl := s.deps.Redis.TTL(ctx, modelCatalogKey).Val(); ttl != -1 {
		t.Fatalf("last good catalog must not expire: %v", ttl)
	}
	if ttl := s.deps.Redis.TTL(ctx, modelCatalogAttemptKey).Val(); ttl < 29*time.Minute || ttl > 30*time.Minute {
		t.Fatalf("expected 30 minute refresh schedule: %v", ttl)
	}
	if _, err := s.refreshModelCatalog(ctx, false); err != nil {
		t.Fatal("fresh cache attempted SQL/upstream work", err)
	}
	manual := &modelCatalogSnapshot{Catalog: json.RawMessage(`{"models":[{"slug":"manual"}]}`), Source: "manual"}
	if err := s.saveModelCatalog(ctx, manual, first.Revision); err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{`{"models":[]}`, `{"models":[{}]}`, `bad json`} {
		if s.saveModelCatalog(ctx, &modelCatalogSnapshot{Catalog: json.RawMessage(raw)}, manual.Revision) == nil {
			t.Fatal("invalid refresh replaced manual cache")
		}
	}
	if s.saveModelCatalog(ctx, first, first.Revision) == nil {
		t.Fatal("in-flight stale refresh replaced manual cache")
	}
	actual, err := s.readModelCatalog(ctx)
	if err != nil || actual.Revision != manual.Revision || string(actual.Catalog) != string(manual.Catalog) {
		t.Fatal("last good snapshot changed", actual, err)
	}
}

func TestModelCatalogRefreshLockAndCadence(t *testing.T) {
	s := modelCacheTestService(t)
	ctx := t.Context()
	keys := []string{modelCatalogLockKey, modelCatalogAttemptKey, modelCatalogSettingsKey}
	claim := func(force bool, token string) int {
		result, err := claimModelRefresh.Run(ctx, s.deps.Redis, keys, force, token,
			modelCatalogRefreshTimeout.Milliseconds(), defaultModelRefreshMinutes, "").Int()
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	if claim(false, "first") != 1 || claim(true, "second") != 0 {
		t.Fatal("refreshes overlapped")
	}
	s.releaseModelCatalogLock("second")
	if claim(true, "third") != 0 {
		t.Fatal("non-owner released the lock")
	}
	s.releaseModelCatalogLock("first")
	if claim(false, "early") != 0 || claim(true, "manual") != 1 {
		t.Fatal("refresh interval or manual refresh ignored")
	}
	s.releaseModelCatalogLock("manual")
	if err := s.deps.Redis.Del(ctx, modelCatalogAttemptKey).Err(); err != nil {
		t.Fatal(err)
	}
	if claim(false, "due") != 1 {
		t.Fatal("due refresh not scheduled")
	}
}

func TestModelCatalogPublicReadAndAdminPermissions(t *testing.T) {
	s := modelCacheTestService(t)
	router := gin.New()
	s.deps.Authenticate = func(c *gin.Context) (*platform.Principal, error) {
		return &platform.Principal{ID: "reader", Permissions: []string{c.GetHeader("test-permission")}}, nil
	}
	s.registerModelCatalog(router)
	get := func(path, permission string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		request.Header.Set("test-permission", permission)
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, request)
		return recorder
	}
	if get("/official-models", "").Code != 503 {
		t.Fatal("empty cache must signal fallback")
	}
	catalog := json.RawMessage(`{"models":[{"slug":"test","context_window":128000}]}`)
	if err := s.saveModelCatalog(t.Context(), &modelCatalogSnapshot{Catalog: catalog}, ""); err != nil {
		t.Fatal(err)
	}
	public := get("/official-models", "")
	if public.Code != 200 || public.Body.String() != string(catalog) || public.Header().Get("ETag") == "" {
		t.Fatal(public.Code, public.Body.String())
	}
	if get("/admin/api/official-models", "admin.official-accounts.read-own").Code != 403 {
		t.Fatal("own-account reader accessed global cache management")
	}
	if get("/admin/api/official-models", "admin.official-accounts.read").Code != 200 {
		t.Fatal("reader rejected")
	}
	for _, method := range []string{http.MethodPatch, http.MethodPost} {
		path := "/admin/api/official-models"
		if method == http.MethodPost {
			path += "/refresh"
		}
		request := httptest.NewRequest(method, path, strings.NewReader(`{}`))
		request.Header.Set("test-permission", "admin.official-accounts.read")
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, request)
		if recorder.Code != 403 {
			t.Fatal("reader may edit/refresh global models")
		}
	}
}

func TestModelCatalogRequestUsesUsageOutboundProxy(t *testing.T) {
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Host != "official.invalid" || r.Header.Get("Authorization") != "Bearer fixture" ||
			r.Header.Get("ChatGPT-Account-Id") != "account" || r.Header.Get("User-Agent") != "codex_cli_rs/0.170.0" ||
			r.Header.Get("originator") != "codex_cli_rs" || r.Method != http.MethodGet ||
			r.URL.Query().Get("client_version") != "0.170.0" {
			t.Error("model request lost upstream routing or headers")
		}
		w.Write([]byte(`{"models":[{"slug":"test"}]}`))
	}))
	defer proxy.Close()
	s := &service{deps: &platform.Dependencies{Config: platform.Config{"CODEX_OUTBOUND_PROXY": proxy.URL}}}
	id := "account"
	row := &storedAccount{System: &systemAccount{CodexAccountID: &id,
		Auth: object{"tokens": object{"access_token": "fixture"}}}}
	options := modelCatalogRequest(t.Context(), "0.170.0")
	options.URL = strings.Replace(options.URL, "https://chatgpt.com/backend-api/codex", "http://official.invalid", 1)
	response, err := s.codexRequest(row, options)
	if err != nil || validateModelCatalog(response.Raw) != nil {
		t.Fatal("proxied model fetch failed", err)
	}
	s.outbound.proxy.CloseIdleConnections()
	options.MaxBytes = 1
	if _, err := s.sendCodex(row, options); err == nil {
		t.Fatal("oversized upstream response accepted")
	}
}
