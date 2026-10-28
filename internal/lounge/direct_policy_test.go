package lounge

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/gorilla/websocket"
	"gorm.io/gorm"
)

func directPolicyDB(t *testing.T) *gorm.DB {
	t.Helper()
	db := policyDB(t)
	upgrade, err := os.ReadFile("../../sql/20261028-lounge-direct-permissions.sql")
	if err != nil {
		t.Fatal(err)
	}
	embedded, err := os.ReadFile("../migrations/021_lounge_direct_permissions.sql")
	if err != nil {
		t.Fatal(err)
	}
	schema := strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(string(upgrade), "BEGIN;", ""), "COMMIT;", ""))
	if schema != strings.TrimSpace(string(embedded)) {
		t.Fatal("upgrade and bootstrap differ")
	}
	for range 2 {
		if err := db.Exec(schema).Error; err != nil {
			t.Fatal(err)
		}
	}
	return db
}

func TestDirectPermissionPersistsOnlyCurrentState(t *testing.T) {
	db := directPolicyDB(t)
	a, b := policyUser(t, db), policyUser(t, db)
	h := newHub(nil)
	h.directKey = []byte("private-test-key")
	input := incoming{ID: "private-id", To: b, Text: "private-message-marker", Images: []string{picture(t)}}
	permission, err := h.directPermission(a, input)
	if err != nil {
		t.Fatal(err)
	}
	repository := postgresDirectPolicy{db: db}
	ctx := context.Background()
	if err := repository.Admit(ctx, permission); err != nil {
		t.Fatal(err)
	}
	fresh := postgresDirectPolicy{db: db}
	if err := fresh.Admit(ctx, permission); err != nil {
		t.Fatal("identical retry was denied", err)
	}
	assertChangedOpeningDenied(t, h, fresh, openingCase{a, input})
	assertPermissionColumns(t, db)
	reply, _ := h.directPermission(b, incoming{ID: "reply", To: a, Text: "reply"})
	if err := fresh.Admit(ctx, reply); err != nil {
		t.Fatal(err)
	}
	var cleared bool
	if err := db.Raw(`SELECT initiator IS NULL AND fingerprint IS NULL FROM lounge_direct_permissions`).
		Scan(&cleared).Error; err != nil || !cleared {
		t.Fatal("reply must erase the pending retry verifier", err)
	}
	input.ID = "another"
	next, _ := h.directPermission(a, input)
	if err := fresh.Admit(ctx, next); err != nil {
		t.Fatal("reply did not open conversation", err)
	}
	if err := db.Exec("DELETE FROM users WHERE id = ?", b).Error; err != nil {
		t.Fatal(err)
	}
	var count int64
	db.Table("lounge_direct_permissions").Count(&count)
	if count != 0 {
		t.Fatal("deleted user left a permission")
	}
}

type openingCase struct {
	user  string
	input incoming
}

func assertChangedOpeningDenied(t *testing.T, h *hub, repository postgresDirectPolicy, opening openingCase) {
	t.Helper()
	input := opening.input
	changes := []incoming{input, input, input}
	changes[0].ID = "second-id"
	changes[1].Text = "changed-content"
	changes[2].Images = nil
	for _, changed := range changes {
		request, err := h.directPermission(opening.user, changed)
		if err != nil || !errors.Is(repository.Admit(context.Background(), request), errAwaitingReply) {
			t.Fatal("new content or ID bypassed opening limit", err)
		}
	}
}

func assertPermissionColumns(t *testing.T, db *gorm.DB) {
	t.Helper()
	var columns []string
	if err := db.Raw(`SELECT column_name FROM information_schema.columns
		WHERE table_schema = current_schema() AND table_name = 'lounge_direct_permissions' ORDER BY ordinal_position`).
		Scan(&columns).Error; err != nil {
		t.Fatal(err)
	}
	if strings.Join(columns, ",") != "first_user,second_user,initiator,fingerprint" {
		t.Fatal("unexpected content or event storage", columns)
	}
}

func permissionRead(t *testing.T, client *websocket.Conn) map[string]interface{} {
	t.Helper()
	for {
		frame := read(t, client)
		if frame["type"] != "direct-permissions" {
			return frame
		}
	}
}

func TestDirectServerRejectsSecondDeviceUntilReply(t *testing.T) {
	db := concurrentPolicyDB(t)
	h, url := fixture(t)
	h.directPolicy = postgresDirectPolicy{db: db}
	h.directKey = []byte("test-key")
	h.relationships = &memoryRelationships{blocks: make(map[string]map[string]bool)}
	a, b := policyUser(t, db), policyUser(t, db)
	alice := receiptConnection(t, h, url, a)
	bob := receiptConnection(t, h, url, b)
	first := incoming{Type: "message", ID: "first", To: b, Text: "hello"}
	write(t, alice, first)
	for _, client := range []*websocket.Conn{alice, bob} {
		if permissionRead(t, client)["id"] != first.ID {
			t.Fatal("first message was not forwarded")
		}
	}
	write(t, bob, incoming{Type: "receipt", ID: "first", To: a, Status: "read"})
	if permissionRead(t, alice)["type"] != "receipt" {
		t.Fatal("receipt not forwarded")
	}
	other := directConnection(t, h, url, a)
	write(t, other, incoming{Type: "message", ID: "second", To: b, Text: "another"})
	if frame := permissionRead(t, other); frame["code"] != awaitingReply {
		t.Fatal("new device bypassed server policy", frame)
	}
	write(t, bob, incoming{Type: "message", ID: "answer", To: a, Text: "hello back"})
	for _, client := range []*websocket.Conn{alice, other, bob} {
		if permissionRead(t, client)["id"] != "answer" {
			t.Fatal("reply did not reach both devices")
		}
	}
	for _, id := range []string{"more-one", "more-two"} {
		write(t, other, incoming{Type: "message", ID: id, To: b, Text: id})
		for _, client := range []*websocket.Conn{alice, other, bob} {
			if permissionRead(t, client)["id"] != id {
				t.Fatal("conversation did not remain open")
			}
		}
	}
}
