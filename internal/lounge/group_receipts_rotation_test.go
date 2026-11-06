package lounge

import (
	"testing"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

func receiptGrant(t *testing.T, h *hub, author, id string) string {
	t.Helper()
	grant, err := h.issueGroupReceipt(&peer{user: account{ID: author}, moderator: true, groupReads: true}, id)
	if err != nil {
		t.Fatal(err)
	}
	return grant
}

func receiptPong(t *testing.T, client *websocket.Conn) {
	t.Helper()
	write(t, client, incoming{Type: "ping"})
	groupReadFrame(t, client, "pong")
}

func TestRotatedGroupReceiptKeyRejectsOneReceiptAndKeepsConnectionsAlive(t *testing.T) {
	for _, kind := range []string{"group-read", "group-read-confirm"} {
		t.Run(kind, func(t *testing.T) { rotatedGroupReceipt(t, kind) })
	}
}

func rotatedGroupReceipt(t *testing.T, kind string) {
	t.Helper()
	author, viewer := uuid.NewString(), uuid.NewString()
	old := newHub([]byte("old-ticket-key"))
	old.directKey = []byte("old-persistent-kong-key")
	grant := receiptGrant(t, old, author, "historical")
	h, url := fixture(t)
	h.directKey = []byte("rotated-persistent-kong-key")
	h.policies = &memoryPolicies{values: map[string]policy{author: {Moderator: true}}}
	source, target := viewer, author
	if kind == "group-read-confirm" {
		source, target = author, viewer
	}
	receiver := groupReadConnection(t, h, url, target)
	// Legacy clients can retry on each reconnect without taking down the authenticated session.
	for attempt := 0; attempt < 3; attempt++ {
		sender := groupReadConnection(t, h, url, source)
		for retry := 0; retry < 2; retry++ {
			write(t, sender, incoming{Type: kind, ID: "historical", To: target, GroupReceipt: grant})
			rejected := groupReadFrame(t, sender, "group-receipt-rejected")
			if rejected["code"] != invalidGroupReceipt || rejected["receiptType"] != kind ||
				rejected["id"] != "historical" || rejected["groupReceipt"] != grant ||
				rejected["from"] != target || rejected["to"] != source {
				t.Fatal("rejection did not identify the failed capability", rejected)
			}
			receiptPong(t, sender)
			receiptPong(t, receiver) // No invalid receipt may precede this pong.
		}
		sender.Close()
	}
	assertCurrentGroupReceipt(t, receiptPeers{h, url, author, viewer})
}

type receiptPeers struct {
	hub                 *hub
	url, author, viewer string
}

func assertCurrentGroupReceipt(t *testing.T, peers receiptPeers) {
	t.Helper()
	h, url, author, viewer := peers.hub, peers.url, peers.author, peers.viewer
	admin := groupReadConnection(t, h, url, author)
	reader := groupReadConnection(t, h, url, viewer)
	grant := receiptGrant(t, h, author, "current")
	write(t, reader, incoming{Type: "group-read", ID: "current", To: author, GroupReceipt: grant})
	if groupReadFrame(t, admin, "group-read")["from"] != viewer {
		t.Fatal("valid read did not reach its author")
	}
	write(t, admin, incoming{Type: "group-read-confirm", ID: "current", To: viewer, GroupReceipt: grant})
	if groupReadFrame(t, reader, "group-read-confirm")["from"] != author {
		t.Fatal("valid confirmation did not reach its reader")
	}
	receiptPong(t, admin)
	receiptPong(t, reader)
}

func TestGroupReceiptSurvivesRestartWithUnchangedPersistentKey(t *testing.T) {
	author, viewer := uuid.NewString(), uuid.NewString()
	old := newHub([]byte("old-ephemeral-ticket-key"))
	old.directKey = []byte("unchanged-persistent-kong-key")
	grant := receiptGrant(t, old, author, "historical")
	h, url := fixture(t) // Different ephemeral ticket key after restart.
	h.directKey = old.directKey
	h.policies = &memoryPolicies{values: map[string]policy{author: {Moderator: true}}}
	admin := groupReadConnection(t, h, url, author)
	reader := groupReadConnection(t, h, url, viewer)
	write(t, reader, incoming{Type: "group-read", ID: "historical", To: author, GroupReceipt: grant})
	groupReadFrame(t, admin, "group-read")
	write(t, admin, incoming{Type: "group-read-confirm", ID: "historical", To: viewer, GroupReceipt: grant})
	groupReadFrame(t, reader, "group-read-confirm")
	receiptPong(t, admin)
	receiptPong(t, reader)
}
