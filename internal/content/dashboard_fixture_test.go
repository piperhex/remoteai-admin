package content

import (
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func dashboardDatabase(t *testing.T) *gorm.DB {
	t.Helper()
	if os.Getenv("ADMIN_GO_DASHBOARD_DB_TEST") != "1" {
		t.Skip("local PostgreSQL dashboard fixture is opt-in")
	}
	db, err := gorm.Open(postgres.Open(
		"host=127.0.0.1 port=15432 user=parity password=local-parity-only dbname=admin_go sslmode=disable"))
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
	createDashboardTables(t, tx)
	return tx
}

func createDashboardTables(t *testing.T, db *gorm.DB) {
	t.Helper()
	schema := "dashboard_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	for _, statement := range []string{
		`CREATE SCHEMA "` + schema + `"`, `SET LOCAL search_path TO "` + schema + `"`,
		// A non-UTC database session must not change either grouping or day boundaries.
		`SET LOCAL TIME ZONE 'America/Los_Angeles'`,
		`CREATE TABLE users(id uuid PRIMARY KEY, disabled boolean DEFAULT false, "createdAt" timestamptz)`,
		`CREATE TABLE device_installations("deviceId" uuid PRIMARY KEY, platform text, "firstSeenAt" timestamptz)`,
		`CREATE TABLE device_telemetry_events(id uuid PRIMARY KEY, "deviceId" uuid, platform text,
		 "eventType" text, "createdAt" timestamptz)`,
		`CREATE TABLE system_accounts(id uuid, plan text)`,
		`CREATE TABLE system_account_bindings("systemAccountId" uuid)`,
		`CREATE TABLE user_feedback("lastRepliedAt" timestamptz)`,
		`CREATE TABLE admin_approval_requests(status text)`,
		`CREATE TABLE chat_relay_traffic(hour_start timestamptz, bytes bigint)`,
	} {
		if err := db.Exec(statement).Error; err != nil {
			t.Fatal(err)
		}
	}
}
