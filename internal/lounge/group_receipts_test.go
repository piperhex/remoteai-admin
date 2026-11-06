package lounge

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

func groupReadConnection(t *testing.T, h *hub, url, user string) *websocket.Conn {
	t.Helper()
	client := connect(t, h, url, "")
	write(t, client, incoming{Type: "authenticate", Ticket: credential(t, h, user, time.Now().Add(time.Minute)),
		GroupReads: true, GroupAcks: true})
	if frame := read(t, client); frame["type"] != "ready" || frame["groupReads"] != true {
		t.Fatal("missing group receipt capability", frame)
	}
	return client
}

func groupReadFrame(t *testing.T, client *websocket.Conn, kind string) map[string]interface{} {
	t.Helper()
	for {
		frame := read(t, client)
		switch frame["type"] {
		case "presence", "policy", "client-versions":
			continue
		}
		if frame["type"] != kind {
			t.Fatalf("expected %s, got %v", kind, frame)
		}
		return frame
	}
}

func TestGroupReadsUseAuthenticatedPeopleAndOnlyReachTheCurrentAdministrator(t *testing.T) {
	h, url := fixture(t)
	manager, viewer := uuid.NewString(), uuid.NewString()
	h.policies = &memoryPolicies{values: map[string]policy{manager: {Moderator: true}}}
	admin := groupReadConnection(t, h, url, manager)
	otherAdmin := groupReadConnection(t, h, url, manager)
	reader := groupReadConnection(t, h, url, viewer)
	readerOther := groupReadConnection(t, h, url, viewer)
	write(t, admin, incoming{Type: "message", ID: "announcement", Text: "hello"})
	grant, ok := groupReadFrame(t, admin, "accepted")["groupReceipt"].(string)
	if !ok || grant == "" {
		t.Fatal("administrator did not receive a signed read capability")
	}
	for _, client := range []*websocket.Conn{otherAdmin, reader, readerOther} {
		if groupReadFrame(t, client, "message")["groupReceipt"] != grant {
			t.Fatal("group viewers did not receive the same capability")
		}
	}
	for _, client := range []*websocket.Conn{reader, readerOther} {
		if err := client.WriteJSON(map[string]interface{}{"type": "group-read", "id": "announcement", "to": manager,
			"groupReceipt": grant, "reader": map[string]string{"id": manager, "account": "forged"}}); err != nil {
			t.Fatal(err)
		}
		for _, target := range []*websocket.Conn{admin, otherAdmin} {
			frame := groupReadFrame(t, target, "group-read")
			if frame["from"] != viewer || frame["reader"].(map[string]interface{})["id"] != viewer {
				t.Fatal("reader identity was not authoritative", frame)
			}
		}
	}
	write(t, admin, incoming{Type: "group-read-confirm", ID: "announcement", To: viewer, GroupReceipt: grant})
	for _, client := range []*websocket.Conn{reader, readerOther} {
		if groupReadFrame(t, client, "group-read-confirm")["from"] != manager {
			t.Fatal("read confirmation was not returned to the reader's devices")
		}
	}
	h.notifyPolicy(manager, policy{})
	write(t, reader, incoming{Type: "group-read", ID: "announcement", To: manager, GroupReceipt: grant})
	for _, target := range []*websocket.Conn{admin, otherAdmin} {
		write(t, target, incoming{Type: "ping"})
		groupReadFrame(t, target, "pong")
	}
}

func TestOrdinaryGroupMessagesCannotRequestReadPeople(t *testing.T) {
	h, url := fixture(t)
	user := uuid.NewString()
	client := groupReadConnection(t, h, url, user)
	write(t, client, incoming{Type: "message", ID: "ordinary", Text: "hello", GroupReceipt: "forged"})
	if frame := groupReadFrame(t, client, "accepted"); frame["groupReceipt"] != nil {
		t.Fatal("ordinary message acquired group read tracking", frame)
	}
	grant, err := h.issueGroupReceipt(&peer{user: account{ID: uuid.NewString()}, moderator: true, groupReads: true}, "other")
	if err != nil {
		t.Fatal(err)
	}
	write(t, client, incoming{Type: "group-read-confirm", ID: "other", To: uuid.NewString(), GroupReceipt: grant})
	if _, _, err := client.ReadMessage(); err == nil {
		t.Fatal("ordinary account confirmed another administrator's read receipts")
	}
}

func TestGroupReceiptCapabilityRejectsTamperingAndDoesNotNeedServerHistory(t *testing.T) {
	h := newHub([]byte("process-key"))
	h.directKey = []byte("stable-receipt-key")
	author := uuid.NewString()
	grant, err := h.issueGroupReceipt(&peer{user: account{ID: author}, moderator: true, groupReads: true}, "message")
	if err != nil {
		t.Fatal(err)
	}
	fresh := newHub([]byte("new-process-key"))
	fresh.directKey = h.directKey
	input := incoming{ID: "message", GroupReceipt: grant}
	if actual, err := fresh.groupReceiptAuthor(input); err != nil || actual != author {
		t.Fatal("receipt could not be verified after a server restart", actual, err)
	}
	for _, invalid := range []incoming{{ID: "other", GroupReceipt: grant},
		{ID: "message", GroupReceipt: grant + "tampered"},
		{ID: "message", GroupReceipt: strings.Repeat("x", maxGroupReceiptBytes+1)}} {
		if _, err := fresh.groupReceiptAuthor(invalid); err == nil {
			t.Fatal("invalid group receipt capability was accepted")
		}
	}
}
