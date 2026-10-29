package lounge

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestLoungeNamesPostgresIsolationAndRestart(t *testing.T) {
	db := concurrentPolicyDB(t)
	embedded, err := os.ReadFile("../migrations/024_lounge_names.sql")
	if err != nil {
		t.Fatal(err)
	}
	upgrade, err := os.ReadFile("../../sql/20261029-lounge-names.sql")
	if err != nil {
		t.Fatal(err)
	}
	schema := strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(string(upgrade), "BEGIN;", ""), "COMMIT;", ""))
	if schema != strings.TrimSpace(string(embedded)) {
		t.Fatal("upgrade differs from bootstrap")
	}
	for range 2 {
		if err := db.Exec(schema).Error; err != nil {
			t.Fatal(err)
		}
	}
	store := postgresNames{db: db}
	a, b, c := policyUser(t, db), policyUser(t, db), policyUser(t, db)
	ctx := context.Background()
	if err := store.SaveNickname(ctx, nicknameProfile{ID: b, Nickname: "小李"}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveRemark(ctx, a, contactRemark{ID: b, Remark: "我的同事"}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveRemark(ctx, c, contactRemark{ID: b, Remark: "我的朋友"}); err != nil {
		t.Fatal(err)
	}
	for owner, expected := range map[string]string{a: "我的同事", b: "", c: "我的朋友"} {
		values, err := store.Remarks(ctx, owner, []string{b})
		if err != nil || len(values) != 1 || values[0].Remark != expected {
			t.Fatal("remark owner isolation failed", err)
		}
	}
	if store.SaveRemark(ctx, a, contactRemark{ID: uuid.NewString(), Remark: "missing"}) == nil {
		t.Fatal("nonexistent contact accepted")
	}
	h, url := fixture(t)
	h.names = store
	conn := connect(t, h, url, "")
	write(t, conn, incoming{Type: "authenticate", Ticket: credential(t, h, b, time.Now().Add(time.Minute))})
	frame := read(t, conn)
	if frame["names"] != true || frame["user"].(map[string]interface{})["nickname"] != "小李" {
		t.Fatal("fresh connection did not load persisted nickname")
	}
	h.close()
	fresh, freshURL := fixture(t)
	fresh.names = postgresNames{db: db}
	fresh.profiles = &memoryProfiles{avatars: map[string]string{}}
	client := connect(t, fresh, freshURL, a)
	write(t, client, incoming{Type: "profile-watch", Watch: []string{b}})
	read(t, client)
	assertName(t, client, "remarks", "我的同事")
	if err := store.SaveRemark(ctx, a, contactRemark{ID: b}); err != nil {
		t.Fatal(err)
	}
	values, err := store.Remarks(ctx, a, []string{b})
	if err != nil || values[0].Remark != "" {
		t.Fatal("clear was not persisted")
	}
	var columns []string
	db.Raw(`SELECT column_name FROM information_schema.columns WHERE table_schema = current_schema()
		AND table_name = 'lounge_contact_remarks' ORDER BY column_name`).Scan(&columns)
	if strings.Join(columns, ",") != "owner_id,remark,user_id" {
		t.Fatal("unexpected retained remark data")
	}
}
