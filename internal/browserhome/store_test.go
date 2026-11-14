package browserhome

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/piperhex/remoteai-admin/internal/platform"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestDatabaseUploadIsAtomicAndPersistsCompletePackages(t *testing.T) {
	dsn := os.Getenv("BROWSER_HOME_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set BROWSER_HOME_TEST_DATABASE_URL to an isolated PostgreSQL database")
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
	schema := "home_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	for _, query := range []string{`CREATE SCHEMA "` + schema + `"`, `SET LOCAL search_path TO "` + schema + `"`} {
		if err := tx.Exec(query).Error; err != nil {
			t.Fatal(err)
		}
	}
	sql, err := os.ReadFile("../../sql/20261114-browser-home.sql")
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
	actor := &platform.Principal{ID: uuid.NewString(), Email: "admin@fixture.test"}
	assets := func(text string) []Asset {
		value, err := ReadArchive(archiveFixture(t, map[string]string{"index.html": text, "app.js": "window.ready = true"}))
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	first, err := store.Upload(ctx, assets("<h1>First</h1>"), "dist.zip", actor)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Enable(ctx, true, actor); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.Read(ctx)
	if err != nil || loaded.MD5 != first.MD5 || !loaded.Enabled {
		t.Fatal(loaded, err)
	}
	pack, err := store.Package(ctx, first.MD5)
	if err != nil || len(pack.Data) == 0 {
		t.Fatal(err)
	}
	second, err := store.Upload(ctx, assets("<h1>Second</h1>"), "next.zip", actor)
	if err != nil || !second.Enabled || second.MD5 == first.MD5 {
		t.Fatal(second, err)
	}
	if _, err := store.Asset(ctx, first.Revision, "app.js"); err != nil {
		t.Fatal("in-flight old assets lost", err)
	}
	if err := tx.Exec("DROP TABLE admin_audit_logs").Error; err != nil {
		t.Fatal(err)
	}
	if _, err := store.Upload(ctx, assets("<h1>Failed</h1>"), "failed.zip", actor); err == nil {
		t.Fatal("audit failure committed")
	}
	loaded, err = store.Read(ctx)
	if err != nil || loaded.MD5 != second.MD5 {
		t.Fatal("failed upload changed active version", loaded, err)
	}
}
