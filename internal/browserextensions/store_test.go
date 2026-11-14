package browserextensions

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/piperhex/remoteai-admin/internal/platform"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestDatabaseLifecycleAndAuditRollback(t *testing.T) {
	dsn := os.Getenv("BROWSER_EXTENSIONS_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set BROWSER_EXTENSIONS_TEST_DATABASE_URL to an isolated PostgreSQL database")
	}
	db, err := gorm.Open(postgres.Open(dsn))
	if err != nil {
		t.Fatal(err)
	}
	pool, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	tx := db.Begin()
	defer tx.Rollback()
	schema := "extensions_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	for _, query := range []string{`CREATE SCHEMA "` + schema + `"`, `SET LOCAL search_path TO "` + schema + `"`} {
		if err := tx.Exec(query).Error; err != nil {
			t.Fatal(err)
		}
	}
	sql, err := os.ReadFile("../../sql/20261115-browser-extensions.sql")
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Exec(strings.ReplaceAll(string(sql), "public.", "")).Error; err != nil {
		t.Fatal(err)
	}
	if err := tx.Exec(`CREATE TABLE admin_audit_logs (id text, "actorId" text, "actorEmail" text,
		action text, "targetType" text, "targetId" text, "createdAt" timestamptz)`).Error; err != nil {
		t.Fatal(err)
	}
	store := databaseStore{tx}
	ctx := context.Background()
	actor := &platform.Principal{ID: uuid.NewString()}
	pack, err := ReadArchive(archiveFixture(t, map[string]string{"manifest.json": fixtureManifest}))
	if err != nil {
		t.Fatal(err)
	}
	item, err := store.Upload(ctx, pack, actor)
	if err != nil {
		t.Fatal(err)
	}
	if items, err := store.List(ctx, true); err != nil || len(items) != 0 {
		t.Fatal(items, err)
	}
	if _, err := store.Publish(ctx, item.ID, true, actor); err != nil {
		t.Fatal(err)
	}
	if data, err := store.Download(ctx, item.ID, item.SHA256); err != nil || len(data) == 0 {
		t.Fatal(err)
	}
	if _, err := store.Download(ctx, item.ID, strings.Repeat("0", 64)); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatal(err)
	}
	pack.Item.ID = item.ID
	if _, err := store.Upload(ctx, pack, actor); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Download(ctx, item.ID, item.SHA256); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatal(err)
	}
	if _, err := store.Publish(ctx, item.ID, true, actor); err != nil {
		t.Fatal(err)
	}
	if err := tx.Exec("DROP TABLE admin_audit_logs").Error; err != nil {
		t.Fatal(err)
	}
	if _, err := store.Publish(ctx, item.ID, false, actor); err == nil {
		t.Fatal("audit failure committed")
	}
	if _, err := store.Upload(ctx, pack, actor); err == nil {
		t.Fatal("replacement survived audit failure")
	}
	if err := store.Delete(ctx, item.ID, actor); err == nil {
		t.Fatal("deletion survived audit failure")
	}
	if data, err := store.Download(ctx, item.ID, item.SHA256); err != nil || len(data) == 0 {
		t.Fatal("rollback lost package", err)
	}
	if err := tx.Exec(`CREATE TABLE admin_audit_logs (id text, "actorId" text, "actorEmail" text,
		action text, "targetType" text, "targetId" text, "createdAt" timestamptz)`).Error; err != nil {
		t.Fatal(err)
	}
	if err := store.Delete(ctx, item.ID, actor); err != nil {
		t.Fatal(err)
	}
	var remaining int64
	if err := tx.Model(&Package{}).Count(&remaining).Error; err != nil || remaining != 0 {
		t.Fatal("orphaned package", remaining, err)
	}
}
