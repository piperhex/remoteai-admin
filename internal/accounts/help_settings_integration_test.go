package accounts

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"testing"

	"github.com/codex-switch/admin-go/internal/migrations"
	"github.com/codex-switch/admin-go/internal/platform"
	"github.com/gin-gonic/gin"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func helpSettingsTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := os.Getenv("HELP_SETTINGS_TEST_DSN")
	if dsn == "" {
		t.Skip("set HELP_SETTINGS_TEST_DSN to an isolated disposable PostgreSQL database")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	pool, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pool.Close() })
	if err = migrations.InitializeEmpty(db); err != nil {
		t.Fatal(err)
	}
	tx := db.Begin()
	t.Cleanup(func() { tx.Rollback() })
	return tx
}

func TestHelpSettingsPersistenceRevisionAndChatIsolation(t *testing.T) {
	tx := helpSettingsTestDB(t)
	owner := newID()
	insertUser := `INSERT INTO users(id,email,"passwordHash") VALUES(?,?,?)`
	if err := tx.Exec(insertUser, owner, owner+"@example.test", "fixture-only").Error; err != nil {
		t.Fatal(err)
	}
	insertChat := `INSERT INTO chat_settings(id,policy) VALUES('current','{"threadPageSize":50}')
		ON CONFLICT(id) DO NOTHING`
	if err := tx.Exec(insertChat).Error; err != nil {
		t.Fatal(err)
	}
	s := &service{deps: &platform.Dependencies{DB: tx}}
	initial, err := readHelpSettings(t.Context(), tx)
	if err != nil || initial.Model != "" || initial.SystemPrompt == "" || initial.Effort != defaultHelpEffort {
		t.Fatal(initial, err)
	}
	actor := &platform.Principal{ID: owner, Email: owner + "@example.test"}
	input := helpSettings{Model: "official-one", Effort: "high", SystemPrompt: "Help politely"}
	saved, err := s.saveHelpSettings(t.Context(), input, actor)
	if err != nil || saved.Revision == "" {
		t.Fatal(saved, err)
	}
	read, err := readHelpSettings(t.Context(), tx)
	if err != nil || !reflect.DeepEqual(read, saved) {
		t.Fatal(read, saved, err)
	}
	if _, err = s.saveHelpSettings(t.Context(), helpSettings{Model: "stale", SystemPrompt: "Wrong"}, actor); err == nil {
		t.Fatal("stale update accepted")
	}
	saved.SystemPrompt = "Use short answers"
	saved.Effort = "xhigh"
	saved.Questions = []helpQuestion{{Zh: "帮我检查设置", En: "Check my settings"}, {Zh: "如何开启电脑助手？"}}
	updated, err := s.saveHelpSettings(t.Context(), saved, actor)
	if err != nil || updated.Revision == saved.Revision || updated.Effort != "xhigh" {
		t.Fatal(updated, err)
	}
	read, err = readHelpSettings(t.Context(), tx)
	if err != nil || !reflect.DeepEqual(read, updated) {
		t.Fatal(read, updated, err)
	}
	checkPublicHelpEffort(t, s, "xhigh")
	checkHelpSettingsIsolation(t, tx, owner)
}

func TestLegacyHelpSettingsDefaultToLow(t *testing.T) {
	tx := helpSettingsTestDB(t)
	legacy := `{"model":"official-one","systemPrompt":"Help politely","revision":"legacy"}`
	row := helpSettingsRow{ID: helpSettingsID}
	if err := tx.Where("id = ?", helpSettingsID).Delete(&row).Error; err != nil {
		t.Fatal(err)
	}
	insert := "INSERT INTO chat_settings(id,policy) VALUES(?,?::jsonb)"
	if err := tx.Exec(insert, helpSettingsID, legacy).Error; err != nil {
		t.Fatal(err)
	}
	settings, err := readHelpSettings(t.Context(), tx)
	if err != nil || settings.Effort != defaultHelpEffort || settings.Revision != "legacy" {
		t.Fatal(settings, err)
	}
	if !reflect.DeepEqual(settings.Questions, defaultHelpQuestions()) {
		t.Fatalf("legacy suggestions were not preserved: %#v", settings.Questions)
	}
	checkPublicHelpEffort(t, &service{deps: &platform.Dependencies{DB: tx}}, defaultHelpEffort)
}

func checkPublicHelpEffort(t *testing.T, s *service, expected string) {
	t.Helper()
	router := gin.New()
	s.registerHelpSettings(router)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/help-assistant/settings", nil))
	var settings helpSettings
	if recorder.Code != http.StatusOK || json.Unmarshal(recorder.Body.Bytes(), &settings) != nil {
		t.Fatalf("public settings: %d %s", recorder.Code, recorder.Body.String())
	}
	if settings.Effort != expected {
		t.Fatalf("public effort = %q, want %q", settings.Effort, expected)
	}
}

func checkHelpSettingsIsolation(t *testing.T, tx *gorm.DB, owner string) {
	t.Helper()
	var chatPolicy string
	err := tx.Raw("SELECT policy::text FROM chat_settings WHERE id = 'current'").Scan(&chatPolicy).Error
	if err != nil || chatPolicy != `{"threadPageSize": 50}` {
		t.Fatal(chatPolicy, err)
	}
	var audits int64
	err = tx.Model(&auditEntry{}).Where(`action = ? AND "actorId" = ?`, "help-assistant.update", owner).
		Count(&audits).Error
	if err != nil || audits != 2 {
		t.Fatal(audits, err)
	}
}
