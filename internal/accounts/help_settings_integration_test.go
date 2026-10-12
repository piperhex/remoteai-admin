package accounts

import (
	"os"
	"testing"

	"github.com/codex-switch/admin-go/internal/migrations"
	"github.com/codex-switch/admin-go/internal/platform"
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
	if err != nil || initial.Model != "" || initial.SystemPrompt == "" {
		t.Fatal(initial, err)
	}
	actor := &platform.Principal{ID: owner, Email: owner + "@example.test"}
	input := helpSettings{Model: "official-one", SystemPrompt: "Help politely"}
	saved, err := s.saveHelpSettings(t.Context(), input, actor)
	if err != nil || saved.Revision == "" {
		t.Fatal(saved, err)
	}
	read, err := readHelpSettings(t.Context(), tx)
	if err != nil || read != saved {
		t.Fatal(read, saved, err)
	}
	if _, err = s.saveHelpSettings(t.Context(), helpSettings{Model: "stale", SystemPrompt: "Wrong"}, actor); err == nil {
		t.Fatal("stale update accepted")
	}
	saved.SystemPrompt = "Use short answers"
	updated, err := s.saveHelpSettings(t.Context(), saved, actor)
	if err != nil || updated.Revision == saved.Revision {
		t.Fatal(updated, err)
	}
	checkHelpSettingsIsolation(t, tx, owner)
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
