package lounge

import (
	"context"
	"os"
	"strings"
	"testing"
)

func TestLoungeBlockStorageSurvivesRestartAndRepeatedUpgrade(t *testing.T) {
	db := policyDB(t)
	if err := db.Exec("ALTER TABLE users ADD COLUMN email text NOT NULL DEFAULT 'fixture@example.test'").Error; err != nil {
		t.Fatal(err)
	}
	migration, err := os.ReadFile("../../sql/20261028-lounge-blocks.sql")
	if err != nil {
		t.Fatal(err)
	}
	embedded, err := os.ReadFile("../migrations/020_lounge_blocks.sql")
	if err != nil {
		t.Fatal(err)
	}
	schema := strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(string(migration), "BEGIN;", ""), "COMMIT;", ""))
	if schema != strings.TrimSpace(string(embedded)) {
		t.Fatal("bootstrap and upgrade schemas differ")
	}
	if err := db.Exec(schema).Error; err != nil {
		t.Fatal(err)
	}
	a, b := policyUser(t, db), policyUser(t, db)
	repository := postgresRelationships{db: db}
	ctx := context.Background()
	value, err := repository.SetBlock(ctx, blockChange{a, b, true})
	if err != nil || len(value.Blocked) != 1 || value.Blocked[0].ID != b {
		t.Fatal(value, err)
	}
	if err := db.Exec(schema).Error; err != nil {
		t.Fatal(err)
	}
	fresh := postgresRelationships{db: db}
	value, err = fresh.Read(ctx, b)
	if err != nil || len(value.BlockedBy) != 1 || value.BlockedBy[0] != a || len(value.Blocked) != 0 {
		t.Fatal(value, err)
	}
	if _, err := fresh.SetBlock(ctx, blockChange{a, b, false}); err != nil {
		t.Fatal(err)
	}
	value, err = fresh.Read(ctx, b)
	if err != nil || len(value.BlockedBy) != 0 {
		t.Fatal(value, err)
	}
	if _, err := fresh.SetBlock(ctx, blockChange{a, b, true}); err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("DELETE FROM users WHERE id = ?", b).Error; err != nil {
		t.Fatal(err)
	}
	value, err = fresh.Read(ctx, a)
	if err != nil || len(value.Blocked) != 0 {
		t.Fatal("deleted account left a preference", err)
	}
	var columns []string
	if err := db.Raw(`SELECT column_name FROM information_schema.columns
		WHERE table_schema = current_schema() AND table_name = 'lounge_user_blocks' ORDER BY ordinal_position`).Scan(&columns).Error; err != nil {
		t.Fatal(err)
	}
	if strings.Join(columns, ",") != "user_id,blocked_user_id" {
		t.Fatal("unexpected server content storage", columns)
	}
}
