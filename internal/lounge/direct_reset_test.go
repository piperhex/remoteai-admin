package lounge

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"gorm.io/gorm"
)

func applyDirectResetSchema(t *testing.T, db *gorm.DB) {
	t.Helper()
	upgrade, err := os.ReadFile("../../sql/20261029-lounge-direct-reset.sql")
	if err != nil {
		t.Fatal(err)
	}
	embedded, err := os.ReadFile("../migrations/023_lounge_direct_reset.sql")
	if err != nil {
		t.Fatal(err)
	}
	schema := strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(string(upgrade), "BEGIN;", ""), "COMMIT;", ""))
	if schema != strings.TrimSpace(string(embedded)) {
		t.Fatal("reset upgrade and bootstrap differ")
	}
	for range 2 {
		if err := db.Exec(schema).Error; err != nil {
			t.Fatal(err)
		}
	}
}

func TestDirectDeleteRequiresFreshExchangeAndRejectsQueuedMessages(t *testing.T) {
	db := directPolicyDB(t)
	a, b := policyUser(t, db), policyUser(t, db)
	h := newHub(nil)
	h.directKey = []byte("test-key")
	repository := postgresDirectPolicy{db: db}
	ctx := context.Background()
	opening, _ := h.directPermission(a, incoming{ID: "old-first", To: b, Text: "hello"})
	reply, _ := h.directPermission(b, incoming{ID: "old-reply", To: a, Text: "hello back"})
	for _, request := range []directPermission{opening, reply} {
		if err := repository.Admit(ctx, request); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := repository.Reset(ctx, opening); err != nil {
		t.Fatal(err)
	}
	for _, request := range []directPermission{opening, reply} {
		if err := repository.Admit(ctx, request); !errors.Is(err, errDirectReset) {
			t.Fatal("an old queued message reopened the deleted conversation", err)
		}
	}
	states, err := repository.States(ctx, a, []string{b})
	if err != nil || states.States[b] != "new" || states.Generations[b] != 1 {
		t.Fatal(states, err)
	}
	opening.Generation = 1
	if err := repository.Admit(ctx, opening); err != nil {
		t.Fatal(err)
	}
	second, _ := h.directPermission(a, incoming{ID: "second", To: b, Text: "hello", Generation: 1})
	if err := repository.Admit(ctx, second); !errors.Is(err, errAwaitingReply) {
		t.Fatal("second opening bypassed fresh exchange", err)
	}
	reply.Generation = 1
	if err := repository.Admit(ctx, reply); err != nil {
		t.Fatal(err)
	}
	states, err = repository.States(ctx, a, []string{b})
	if err != nil || states.States[b] != "open" {
		t.Fatal("fresh exchange did not open the chat", states, err)
	}
}

func TestDeleteDirectRejectsInvalidTargetsAndAcknowledgesReset(t *testing.T) {
	db := concurrentPolicyDB(t)
	h, url := fixture(t)
	h.directPolicy = postgresDirectPolicy{db: db}
	h.relationships = &memoryRelationships{blocks: make(map[string]map[string]bool)}
	a, b := policyUser(t, db), policyUser(t, db)
	client := directConnection(t, h, url, a)
	for _, target := range []string{a, "invalid"} {
		write(t, client, incoming{Type: "delete-direct", ID: "invalid-delete", UserID: target})
		if frame := permissionRead(t, client); frame["type"] != "rejected" {
			t.Fatal(frame)
		}
	}
	write(t, client, incoming{Type: "delete-direct", ID: "delete", UserID: b})
	if frame := permissionRead(t, client); frame["type"] != "updated" || frame["id"] != "delete" {
		t.Fatal(frame)
	}
	states, err := h.directPolicy.States(context.Background(), a, []string{b})
	if err != nil || states.States[b] != "new" || states.Generations[b] != 1 {
		t.Fatal(states, err)
	}
}
