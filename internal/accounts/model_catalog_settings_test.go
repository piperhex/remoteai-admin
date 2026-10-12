package accounts

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/codex-switch/admin-go/internal/platform"
	"github.com/gin-gonic/gin"
)

func TestModelRefreshIntervalValidation(t *testing.T) {
	for _, minutes := range []int{-1, 0, maxModelRefreshMinutes + 1} {
		if validateModelRefreshSettings(&modelRefreshSettings{IntervalMinutes: minutes}) == nil {
			t.Fatalf("invalid interval accepted: %d", minutes)
		}
	}
	for _, minutes := range []int{1, defaultModelRefreshMinutes, maxModelRefreshMinutes} {
		if err := validateModelRefreshSettings(&modelRefreshSettings{IntervalMinutes: minutes}); err != nil {
			t.Fatal(err)
		}
	}
}

func assertModelRefreshTTL(t *testing.T, s *service, minutes int) {
	t.Helper()
	ttl := s.deps.Redis.PTTL(t.Context(), modelCatalogAttemptKey).Val()
	expected := time.Duration(minutes) * time.Minute
	if ttl > expected || ttl < expected-time.Second {
		t.Fatalf("expected interval %v, got %v", expected, ttl)
	}
}

func TestModelRefreshSettingsDefaultPersistenceAndConcurrentEdits(t *testing.T) {
	s := modelCacheTestService(t)
	settings, err := s.readModelRefreshSettings(t.Context())
	if err != nil || !settings.Enabled || settings.IntervalMinutes != 30 || settings.Revision != "" {
		t.Fatal("unexpected defaults", settings, err)
	}
	settings.IntervalMinutes = 90
	saved, err := s.saveModelRefreshSettings(t.Context(), *settings)
	if err != nil {
		t.Fatal(err)
	}
	assertModelRefreshTTL(t, s, 90)
	if s.deps.Redis.TTL(t.Context(), modelCatalogSettingsKey).Val() != -1 {
		t.Fatal("settings must survive without expiry")
	}
	restarted := &service{deps: s.deps}
	actual, err := restarted.readModelRefreshSettings(t.Context())
	if err != nil || *actual != *saved {
		t.Fatal("settings lost on restart", actual, err)
	}
	settings.IntervalMinutes = 5
	if _, err := s.saveModelRefreshSettings(t.Context(), *settings); err == nil {
		t.Fatal("stale editor overwrote settings")
	}
	assertModelRefreshTTL(t, s, 90)
}

func TestModelRefreshDisabledSkipsStartupButAllowsManualRefreshAndEdits(t *testing.T) {
	s := modelCacheTestService(t)
	settings, err := s.saveModelRefreshSettings(t.Context(), modelRefreshSettings{IntervalMinutes: 60})
	if err != nil {
		t.Fatal(err)
	}
	// No database or cached catalog exists: a disabled startup must not attempt upstream work.
	if _, err := s.refreshModelCatalog(t.Context(), false); err != nil {
		t.Fatal(err)
	}
	calls := 0
	loader := func(context.Context, string) (*modelCatalogSnapshot, error) {
		calls++
		return &modelCatalogSnapshot{Catalog: json.RawMessage(`{"models":[{"slug":"manual-refresh"}]}`)}, nil
	}
	manual, err := s.refreshModelCatalogWith(t.Context(), true, loader)
	if err != nil || calls != 1 {
		t.Fatal("manual refresh was disabled", err)
	}
	if err := s.saveModelCatalog(t.Context(), manual, manual.Revision); err != nil {
		t.Fatal(err)
	}
	if s.deps.Redis.Exists(t.Context(), modelCatalogAttemptKey).Val() != 0 {
		t.Fatal("disabled refresh created a schedule")
	}
	settings.Enabled = true
	if _, err = s.saveModelRefreshSettings(t.Context(), *settings); err != nil {
		t.Fatal(err)
	}
	assertModelRefreshTTL(t, s, 60)
	if _, err = s.refreshModelCatalogWith(t.Context(), false, loader); err != nil || calls != 1 {
		t.Fatal("re-enabled refresh ignored interval", err)
	}
	if err = s.deps.Redis.Del(t.Context(), modelCatalogAttemptKey).Err(); err != nil {
		t.Fatal(err)
	}
	if _, err = s.refreshModelCatalogWith(t.Context(), false, loader); err != nil || calls != 2 {
		t.Fatal("due refresh did not run", err)
	}
	assertModelRefreshTTL(t, s, 60)
}

func TestChangingModelRefreshSettingsDiscardsInFlightAutomaticResult(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(map[bool]string{false: "disable", true: "reschedule"}[enabled], func(t *testing.T) {
			s := modelCacheTestService(t)
			original := &modelCatalogSnapshot{Catalog: json.RawMessage(`{"models":[{"slug":"original"}]}`)}
			if err := s.saveModelCatalog(t.Context(), original, ""); err != nil {
				t.Fatal(err)
			}
			if err := s.deps.Redis.Del(t.Context(), modelCatalogAttemptKey).Err(); err != nil {
				t.Fatal(err)
			}
			loader := func(context.Context, string) (*modelCatalogSnapshot, error) {
				_, err := s.saveModelRefreshSettings(t.Context(), modelRefreshSettings{
					Enabled: enabled, IntervalMinutes: 90,
				})
				return &modelCatalogSnapshot{Catalog: json.RawMessage(`{"models":[{"slug":"stale"}]}`)}, err
			}
			if _, err := s.refreshModelCatalogWith(t.Context(), false, loader); err == nil {
				t.Fatal("in-flight automatic update ignored changed settings")
			}
			actual, err := s.readModelCatalog(t.Context())
			if err != nil || actual.Revision != original.Revision {
				t.Fatal("cancelled result replaced catalog", actual, err)
			}
		})
	}
}

func TestManualCatalogSaveUsesCurrentConfiguredInterval(t *testing.T) {
	s := modelCacheTestService(t)
	if _, err := s.saveModelRefreshSettings(t.Context(), modelRefreshSettings{Enabled: true, IntervalMinutes: 15}); err != nil {
		t.Fatal(err)
	}
	snapshot := &modelCatalogSnapshot{Catalog: json.RawMessage(`{"models":[{"slug":"edited"}]}`)}
	if err := s.saveModelCatalog(t.Context(), snapshot, ""); err != nil {
		t.Fatal(err)
	}
	assertModelRefreshTTL(t, s, 15)
}

func TestStaleModelRefreshClaimCannotStartAfterSettingsChange(t *testing.T) {
	s := modelCacheTestService(t)
	if _, err := s.saveModelRefreshSettings(t.Context(), modelRefreshSettings{Enabled: true, IntervalMinutes: 15}); err != nil {
		t.Fatal(err)
	}
	if err := s.deps.Redis.Del(t.Context(), modelCatalogAttemptKey).Err(); err != nil {
		t.Fatal(err)
	}
	claimed, err := claimModelRefresh.Run(t.Context(), s.deps.Redis,
		[]string{modelCatalogLockKey, modelCatalogAttemptKey, modelCatalogSettingsKey}, false, "stale-worker",
		modelCatalogRefreshTimeout.Milliseconds(), defaultModelRefreshMinutes, "").Int()
	if err != nil || claimed != 0 || s.deps.Redis.Exists(t.Context(), modelCatalogLockKey).Val() != 0 {
		t.Fatal("stale worker acquired the refresh lock", claimed, err)
	}
}

func TestModelRefreshSettingsRoutePermissionsAndInput(t *testing.T) {
	s := modelCacheTestService(t)
	router := gin.New()
	s.deps.Authenticate = func(c *gin.Context) (*platform.Principal, error) {
		return &platform.Principal{ID: "reader", Permissions: []string{c.GetHeader("test-permission")}}, nil
	}
	s.registerModelCatalog(router)
	request := func(method, permission, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, "/admin/api/official-models/settings", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("test-permission", permission)
		response := httptest.NewRecorder()
		router.ServeHTTP(response, req)
		return response
	}
	if request(http.MethodGet, "admin.official-accounts.read", "").Code != 200 ||
		request(http.MethodGet, "admin.official-accounts.read-own", "").Code != 403 ||
		request(http.MethodPatch, "admin.official-accounts.read", "{}").Code != 403 {
		t.Fatal("incorrect settings permissions")
	}
	for _, body := range []string{`{}`, `{"enabled":false}`, `{"enabled":false,"intervalMinutes":30}`,
		`{"enabled":false,"intervalMinutes":null,"revision":""}`,
		`{"enabled":true,"intervalMinutes":1.5,"revision":""}`,
		`{"enabled":true,"intervalMinutes":0,"revision":""}`,
		`{"enabled":true,"intervalMinutes":10081,"revision":""}`} {
		if response := request(http.MethodPatch, "admin.official-accounts.manage", body); response.Code != 400 {
			t.Fatal("invalid settings accepted", response.Code, body)
		}
	}
}
