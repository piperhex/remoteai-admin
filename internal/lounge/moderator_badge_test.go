package lounge

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

func TestLoungeModeratorIdentityComesFromPolicy(t *testing.T) {
	h, url := fixture(t)
	managerID, ordinaryID := uuid.NewString(), uuid.NewString()
	h.policies = &memoryPolicies{values: map[string]policy{managerID: {Moderator: true}}}
	h.relationships = &memoryRelationships{blocks: map[string]map[string]bool{}}
	manager := connect(t, h, url, "")
	write(t, manager, incoming{Type: "authenticate", Direct: true,
		Ticket: credential(t, h, managerID, time.Now().Add(time.Minute))})
	if user := read(t, manager)["user"].(map[string]interface{}); user["moderator"] != true {
		t.Fatal("ready omitted the authenticated lounge role", user)
	}
	ordinary := directConnection(t, h, url, ordinaryID)
	for _, enabled := range []bool{true, false, true} {
		if _, err := h.changePolicy(context.Background(), policyUpdate{Target: managerID, Admin: true,
			Change: policyChange{Moderator: &enabled}}); err != nil {
			t.Fatal(err)
		}
		if read(t, manager)["type"] != "policy" {
			t.Fatal("role change was not acknowledged")
		}
		for _, target := range []string{"", ordinaryID} {
			write(t, manager, incoming{Type: "message", ID: uuid.NewString(), Text: "role", To: target})
			for _, client := range []*websocket.Conn{manager, ordinary} {
				assertMessageModerator(t, read(t, client), enabled)
			}
		}
	}
	for _, target := range []string{"", managerID} {
		write(t, ordinary, map[string]interface{}{"type": "message", "id": uuid.NewString(), "text": "forged",
			"to": target, "moderator": true, "sender": map[string]interface{}{"id": managerID, "moderator": true}})
		for _, client := range []*websocket.Conn{ordinary, manager} {
			assertMessageModerator(t, read(t, client), false)
		}
	}
}

func assertMessageModerator(t *testing.T, frame map[string]interface{}, expected bool) {
	t.Helper()
	sender, ok := frame["sender"].(map[string]interface{})
	if frame["type"] != "message" || !ok || sender["moderator"] != expected {
		t.Fatalf("unexpected message role: %v", frame)
	}
}
