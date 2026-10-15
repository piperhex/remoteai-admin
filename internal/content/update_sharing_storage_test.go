package content

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/codex-switch/admin-go/internal/platform"
	"github.com/codex-switch/admin-go/internal/updatesharing"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func sharingDatabase(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := os.Getenv("UPDATE_SHARING_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set UPDATE_SHARING_TEST_DATABASE_URL to an isolated PostgreSQL database")
	}
	db, err := gorm.Open(postgres.Open(dsn))
	if err != nil {
		t.Fatal(err)
	}
	pool, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := pool.Close(); err != nil {
			t.Error(err)
		}
	})
	tx := db.Begin()
	if tx.Error != nil {
		t.Fatal(tx.Error)
	}
	t.Cleanup(func() {
		if err := tx.Rollback().Error; err != nil {
			t.Error(err)
		}
	})
	schema := "sharing_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	for _, sql := range []string{`CREATE SCHEMA "` + schema + `"`, `SET LOCAL search_path TO "` + schema + `"`} {
		if err := tx.Exec(sql).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.AutoMigrate(&updatesharing.Record{}, &auditEntry{}); err != nil {
		t.Fatal(err)
	}
	return tx
}

func TestUpdateSharingPersistsPublicPolicyAndAuditAtomically(t *testing.T) {
	db := sharingDatabase(t)
	router := gin.New()
	s := &service{deps: &platform.Dependencies{DB: db, Authenticate: func(*gin.Context) (*platform.Principal, error) {
		return &platform.Principal{ID: uuid.NewString(), Email: "admin@fixture.test", Role: "admin",
			Permissions: []string{"admin.update-sharing.manage"}}, nil
	}}}
	s.updateSharingRoutes(router)
	get := func() updatesharing.Settings {
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/update-sharing/settings", nil))
		var policy updatesharing.Settings
		if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &policy) != nil ||
			response.Header().Get("Cache-Control") != "no-store" {
			t.Fatal(response.Code, response.Body)
		}
		return policy
	}
	if got := get(); got != updatesharing.Defaults() {
		t.Fatal(got)
	}
	want := updatesharing.Settings{Scope: updatesharing.Own, UploadKibPerSecond: 64, UnmeteredOnly: true}
	body, _ := json.Marshal(want)
	save := func(payload string) *httptest.ResponseRecorder {
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest(http.MethodPatch, "/admin/api/update-sharing", strings.NewReader(payload)))
		return response
	}
	if response := save(string(body)); response.Code != 200 {
		t.Fatal(response.Code, response.Body)
	}
	if got := get(); got != want {
		t.Fatal(got, want)
	}
	var count int64
	if err := db.Model(&auditEntry{}).Where("action = ?", "update-sharing.update").Count(&count).Error; err != nil || count != 1 {
		t.Fatal(count, err)
	}
	if response := save(`{}`); response.Code != 400 {
		t.Fatal(response.Code)
	}
	if got := get(); got != want {
		t.Fatal("invalid request changed policy", got)
	}
	if err := db.Migrator().DropTable(&auditEntry{}); err != nil {
		t.Fatal(err)
	}
	defaults, _ := json.Marshal(updatesharing.Defaults())
	if response := save(string(defaults)); response.Code != 500 {
		t.Fatal(response.Code, response.Body)
	}
	if got := get(); got != want {
		t.Fatal("audit failure did not roll back policy", got)
	}
}
