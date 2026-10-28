package lounge

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/codex-switch/admin-go/internal/platform"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func policyDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := os.Getenv("LOUNGE_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("LOUNGE_TEST_DATABASE_URL is required for isolated PostgreSQL policy tests")
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
	tx := db.Begin()
	if tx.Error != nil {
		t.Fatal(tx.Error)
	}
	t.Cleanup(func() { tx.Rollback() })
	schema := "lounge_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if err := tx.Exec("CREATE SCHEMA " + schema).Error; err != nil {
		t.Fatal(err)
	}
	if err := tx.Exec("SET LOCAL search_path TO " + schema).Error; err != nil {
		t.Fatal(err)
	}
	if err := tx.Exec(`CREATE TABLE users (id uuid PRIMARY KEY, disabled boolean NOT NULL DEFAULT false)`).Error; err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile("../../sql/20261028-lounge-moderation.sql")
	if err != nil {
		t.Fatal(err)
	}
	sql := strings.ReplaceAll(strings.ReplaceAll(string(data), "BEGIN;", ""), "COMMIT;", "")
	if err := tx.Exec(sql).Error; err != nil {
		t.Fatal(err)
	}
	return tx
}

func policyUser(t *testing.T, db *gorm.DB) string {
	t.Helper()
	id := uuid.NewString()
	if err := db.Exec("INSERT INTO users (id) VALUES (?)", id).Error; err != nil {
		t.Fatal(err)
	}
	return id
}

func TestLoungePersistentModerationAndExpiry(t *testing.T) {
	db := policyDB(t)
	admin, moderator, target := policyUser(t, db), policyUser(t, db), policyUser(t, db)
	repository := postgresPolicies{db: db}
	ctx := context.Background()
	minutes, enabled := 10, true
	request := policyUpdate{Actor: moderator, Target: target, Change: policyChange{Minutes: &minutes}}
	if _, err := repository.Update(ctx, request); err == nil {
		t.Fatal("ordinary user could mute")
	}
	if _, err := repository.Update(ctx, policyUpdate{Actor: admin, Target: moderator, Admin: true,
		Change: policyChange{Moderator: &enabled}}); err != nil {
		t.Fatal(err)
	}
	value, err := repository.Update(ctx, request)
	if err != nil || value.MutedUntil == nil || time.Until(*value.MutedUntil) < 9*time.Minute {
		t.Fatal(value, err)
	}
	// A fresh repository still sees the mute; a reconnect must not clear moderation state.
	fresh, err := (postgresPolicies{db: db}).Read(ctx, target)
	if err != nil || fresh.MutedUntil == nil {
		t.Fatal("mute was not persisted", err)
	}
	minutes = 0
	if value, err = repository.Update(ctx, request); err != nil || value.MutedUntil != nil {
		t.Fatal("unmute", value, err)
	}
	minutes = maxMuteMinutes + 1
	if _, err = repository.Update(ctx, request); err == nil {
		t.Fatal("unbounded duration accepted")
	}
	enabled = false
	if _, err = repository.Update(ctx, policyUpdate{Actor: admin, Target: moderator, Admin: true,
		Change: policyChange{Moderator: &enabled}}); err != nil {
		t.Fatal(err)
	}
	minutes = 1
	if _, err = repository.Update(ctx, request); err == nil {
		t.Fatal("revoked moderator retained access")
	}
}

func TestLoungeMuteEnforcedOnActiveAndReconnectedSockets(t *testing.T) {
	db := policyDB(t)
	moderator, target := policyUser(t, db), policyUser(t, db)
	h, url := fixture(t)
	h.policies = postgresPolicies{db: db}
	enabled, minutes := true, 1
	if _, err := h.policies.Update(context.Background(), policyUpdate{Actor: uuid.NewString(), Target: moderator, Admin: true,
		Change: policyChange{Moderator: &enabled}}); err != nil {
		t.Fatal(err)
	}
	manager := connect(t, h, url, moderator)
	user := connect(t, h, url, target)
	write(t, manager, incoming{Type: "moderate", ID: "ban", UserID: target, Minutes: &minutes})
	if read(t, manager)["type"] != "moderated" {
		t.Fatal("moderation did not finish")
	}
	if read(t, user)["type"] != "policy" {
		t.Fatal("mute was not pushed")
	}
	write(t, user, incoming{Type: "message", ID: "blocked", Text: "must not pass"})
	read(t, user) // Refreshed own policy, followed by a rejection.
	if read(t, user)["type"] != "rejected" {
		t.Fatal("muted message accepted")
	}
	reconnected := connect(t, h, url, target)
	write(t, reconnected, incoming{Type: "message", ID: "still-blocked", Text: "must not pass"})
	read(t, reconnected)
	if read(t, reconnected)["type"] != "rejected" {
		t.Fatal("reconnect bypassed mute")
	}
	if read(t, user)["type"] != "policy" {
		t.Fatal("the other connection did not receive the same account's refreshed mute")
	}
	if err := db.Exec("UPDATE lounge_user_policies SET muted_until = ? WHERE user_id = ?",
		time.Now().Add(-time.Second), target).Error; err != nil {
		t.Fatal(err)
	}
	write(t, user, incoming{Type: "message", ID: "expired", Text: "allowed"})
	if read(t, user)["id"] != "expired" || read(t, manager)["id"] != "expired" {
		t.Fatal("expiry did not restore delivery")
	}
}

func TestLoungeAdminPermissionsAndLiveModeratorUpdates(t *testing.T) {
	db := policyDB(t)
	admin, target := policyUser(t, db), policyUser(t, db)
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(platform.RequestBodyParser())
	deps := &platform.Dependencies{DB: db, Authenticate: func(c *gin.Context) (*platform.Principal, error) {
		if c.GetHeader("Authorization") == "admin" {
			return &platform.Principal{ID: admin,
				Permissions: []string{"admin.users.read", "admin.users.manage"}}, nil
		}
		return &platform.Principal{ID: target}, nil
	}}
	h, socketURL := fixture(t)
	h.policies = postgresPolicies{db: db}
	h.registerModeration(router, deps)
	connected := connect(t, h, socketURL, target)
	path := "/admin/api/lounge/users/" + target
	for _, token := range []string{"ordinary", "admin"} {
		request := httptest.NewRequest("PATCH", path, strings.NewReader(`{"moderator":true}`))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Authorization", token)
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		if token == "ordinary" && response.Code != 403 {
			t.Fatal("unauthorized grant accepted", response.Code)
		}
		if token == "admin" && response.Code != 200 {
			t.Fatal(response.Code, response.Body.String())
		}
	}
	frame := read(t, connected)
	updated, ok := frame["policy"].(map[string]interface{})
	if frame["type"] != "policy" || !ok || updated["moderator"] != true {
		t.Fatal("moderator grant did not reach the active connection")
	}
	request := httptest.NewRequest("GET", path, nil)
	request.Header.Set("Authorization", "admin")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	var value policy
	if json.Unmarshal(response.Body.Bytes(), &value) != nil || !value.Moderator {
		t.Fatal("grant was not readable")
	}
}
