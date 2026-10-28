package lounge

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

func receiptConnection(t *testing.T, h *hub, url, user string) *websocket.Conn {
	t.Helper()
	client := connect(t, h, url, "")
	write(t, client, incoming{Type: "authenticate", Ticket: credential(t, h, user, time.Now().Add(time.Minute)),
		Direct: true, Receipts: true})
	if frame := read(t, client); frame["receipts"] != true {
		t.Fatal("missing endpoint receipt support")
	}
	return client
}

func TestReceiptsUseAuthenticatedIdentityAndOnlyReachParticipants(t *testing.T) {
	h, url := fixture(t)
	h.relationships = &memoryRelationships{blocks: make(map[string]map[string]bool)}
	a, b := uuid.NewString(), uuid.NewString()
	alice := receiptConnection(t, h, url, a)
	otherAlice := receiptConnection(t, h, url, a)
	bob := receiptConnection(t, h, url, b)
	legacy := directConnection(t, h, url, a)
	outsider := receiptConnection(t, h, url, uuid.NewString())
	write(t, alice, incoming{Type: "message", ID: "one", Text: "private", To: b, Receipts: true})
	for _, client := range []*websocket.Conn{alice, otherAlice, bob} {
		if frame := read(t, client); frame["receipts"] != true {
			t.Fatal(frame)
		}
	}
	write(t, bob, map[string]interface{}{"type": "receipt", "id": "one", "to": a,
		"from": "forged", "status": "read"})
	for _, client := range []*websocket.Conn{alice, otherAlice} {
		frame := read(t, client)
		if frame["type"] != "receipt" || frame["from"] != b || frame["to"] != a || frame["status"] != "read" {
			t.Fatal(frame)
		}
	}
	write(t, alice, incoming{Type: "receipt-confirm", ID: "one", To: b, Status: "read"})
	if frame := read(t, bob); frame["type"] != "receipt-confirm" || frame["from"] != a {
		t.Fatal(frame)
	}
	expectPong(t, legacy)
	expectPong(t, outsider)
	expectPong(t, otherAlice)
}

func TestBlockerCanReceiveReceiptsForItsOwnMessages(t *testing.T) {
	h, url := fixture(t)
	h.relationships = &memoryRelationships{blocks: make(map[string]map[string]bool)}
	a, b := uuid.NewString(), uuid.NewString()
	alice := receiptConnection(t, h, url, a)
	bob := receiptConnection(t, h, url, b)
	blocked := true
	write(t, alice, incoming{Type: "block", ID: "block", UserID: b, Blocked: &blocked})
	read(t, alice)
	read(t, alice)
	write(t, alice, incoming{Type: "message", ID: "outgoing", Text: "still allowed", To: b, Receipts: true})
	read(t, alice)
	read(t, bob)
	write(t, bob, incoming{Type: "receipt", ID: "outgoing", To: a, Status: "delivered"})
	if frame := read(t, alice); frame["type"] != "receipt" {
		t.Fatal(frame)
	}
	write(t, alice, incoming{Type: "receipt-confirm", ID: "outgoing", To: b, Status: "delivered"})
	if frame := read(t, bob); frame["type"] != "receipt-confirm" {
		t.Fatal(frame)
	}
	write(t, alice, incoming{Type: "receipt", ID: "blocked-incoming", To: b, Status: "read"})
	expectPong(t, bob)
}

func TestReceiptRelayHasNoOfflineQueueAndRequiresCapableRecipient(t *testing.T) {
	h, url := fixture(t)
	h.relationships = &memoryRelationships{blocks: make(map[string]map[string]bool)}
	a, b := uuid.NewString(), uuid.NewString()
	bob := receiptConnection(t, h, url, b)
	write(t, bob, incoming{Type: "receipt", ID: "offline", To: a, Status: "read"})
	expectPong(t, bob)
	alice := receiptConnection(t, h, url, a)
	expectPong(t, alice)
	legacyID := uuid.NewString()
	legacy := directConnection(t, h, url, legacyID)
	write(t, alice, incoming{Type: "message", ID: "no-ack-client", To: legacyID, Text: "pending", Receipts: true})
	if frame := read(t, alice); frame["type"] != "rejected" {
		t.Fatal(frame)
	}
	expectPong(t, legacy)
}

func TestInvalidReceiptCannotBypassAuthenticationOrValidation(t *testing.T) {
	for _, kind := range []string{"receipt", "receipt-confirm"} {
		t.Run(kind, func(t *testing.T) {
			h, url := fixture(t)
			client := receiptConnection(t, h, url, uuid.NewString())
			write(t, client, incoming{Type: kind, ID: "one", To: uuid.NewString(), Status: "forged-state"})
			client.SetReadDeadline(time.Now().Add(time.Second))
			if _, _, err := client.ReadMessage(); err == nil {
				t.Fatal("invalid receipt was accepted")
			}
		})
	}
}
