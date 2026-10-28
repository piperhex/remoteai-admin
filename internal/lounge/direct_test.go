package lounge

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

type memoryRelationships struct {
	mu     sync.Mutex
	blocks map[string]map[string]bool
}

func (m *memoryRelationships) Read(_ context.Context, user string) (relationships, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := relationships{Blocked: []account{}, BlockedBy: []string{}}
	for actor, targets := range m.blocks {
		for target, blocked := range targets {
			if !blocked {
				continue
			}
			if actor == user {
				result.Blocked = append(result.Blocked, account{ID: target, Account: target + "@example.test"})
			}
			if target == user {
				result.BlockedBy = append(result.BlockedBy, actor)
			}
		}
	}
	return result, nil
}
func (m *memoryRelationships) SetBlock(ctx context.Context, change blockChange) (relationships, error) {
	m.mu.Lock()
	if m.blocks[change.Actor] == nil {
		m.blocks[change.Actor] = make(map[string]bool)
	}
	m.blocks[change.Actor][change.Target] = change.Blocked
	m.mu.Unlock()
	return m.Read(ctx, change.Actor)
}
func (m *memoryRelationships) Account(_ context.Context, id string) (account, error) {
	return account{ID: id, Account: id + "@example.test"}, nil
}
func directConnection(t *testing.T, h *hub, url, user string) *websocket.Conn {
	t.Helper()
	client := connect(t, h, url, "")
	write(t, client, incoming{Type: "authenticate", Ticket: credential(t, h, user, time.Now().Add(time.Minute)), Direct: true})
	if frame := read(t, client); frame["direct"] != true {
		t.Fatal("missing direct support")
	}
	return client
}
func expectPong(t *testing.T, client *websocket.Conn) {
	t.Helper()
	write(t, client, incoming{Type: "ping"})
	if frame := read(t, client); frame["type"] != "pong" {
		t.Fatalf("unexpected delivery: %v", frame)
	}
}
func expectContact(t *testing.T, client *websocket.Conn, online interface{}) {
	t.Helper()
	frame := read(t, client)
	if frame["type"] != "contacts" {
		t.Fatalf("expected contact status: %v", frame)
	}
	contacts := frame["contacts"].([]interface{})
	if len(contacts) != 1 || contacts[0].(map[string]interface{})["online"] != online {
		t.Fatal(frame)
	}
}

func TestDirectNeverBroadcastsToOthersOrLegacyClients(t *testing.T) {
	h, url := fixture(t)
	h.relationships = &memoryRelationships{blocks: make(map[string]map[string]bool)}
	a, b := uuid.NewString(), uuid.NewString()
	alice := directConnection(t, h, url, a)
	bob := directConnection(t, h, url, b)
	bobOther := directConnection(t, h, url, b)
	legacy := connect(t, h, url, b)
	outsider := directConnection(t, h, url, uuid.NewString())
	write(t, alice, incoming{Type: "message", ID: "private", Text: "private image", Images: []string{picture(t)}, To: b})
	for _, client := range []*websocket.Conn{alice, bob, bobOther} {
		frame := read(t, client)
		if frame["id"] != "private" || frame["recipient"].(map[string]interface{})["id"] != b {
			t.Fatal(frame)
		}
	}
	expectPong(t, legacy)
	expectPong(t, outsider)
	offline := uuid.NewString()
	write(t, alice, incoming{Type: "message", ID: "missed", Text: "never replay", To: offline})
	if frame := read(t, alice); frame["type"] != "rejected" {
		t.Fatal(frame)
	}
	later := directConnection(t, h, url, offline)
	expectPong(t, later)
	legacyOnly := uuid.NewString()
	connect(t, h, url, legacyOnly)
	write(t, alice, incoming{Type: "message", ID: "old-client", Text: "private", To: legacyOnly})
	if frame := read(t, alice); frame["type"] != "rejected" {
		t.Fatal("legacy client received a private message", frame)
	}
}

func TestSingleDirectionBlockHidesBlockerAndFiltersGroupAndPrivate(t *testing.T) {
	h, url := fixture(t)
	h.relationships = &memoryRelationships{blocks: make(map[string]map[string]bool)}
	a, b, c := uuid.NewString(), uuid.NewString(), uuid.NewString()
	alice := directConnection(t, h, url, a)
	aliceOther := directConnection(t, h, url, a)
	bob := directConnection(t, h, url, b)
	carol := directConnection(t, h, url, c)
	write(t, alice, incoming{Type: "watch", Watch: []string{b}})
	expectContact(t, alice, true)
	write(t, bob, incoming{Type: "watch", Watch: []string{a}})
	expectContact(t, bob, true)
	enabled := true
	write(t, alice, incoming{Type: "block", ID: "block", UserID: b, Blocked: &enabled})
	if read(t, alice)["type"] != "privacy" || read(t, aliceOther)["type"] != "privacy" {
		t.Fatal("privacy not synced")
	}
	if read(t, alice)["type"] != "updated" {
		t.Fatal("block failed")
	}
	expectContact(t, bob, nil)
	// Blocking is deliberately one-way: Alice still sees Bob and can send to him.
	expectPong(t, alice)
	write(t, alice, incoming{Type: "message", ID: "outbound", Text: "allowed", To: b})
	for _, client := range []*websocket.Conn{alice, aliceOther, bob} {
		if read(t, client)["id"] != "outbound" {
			t.Fatal("blocker cannot initiate a private message")
		}
	}
	write(t, bob, incoming{Type: "message", ID: "inbound", Text: "blocked", To: a})
	if read(t, bob)["type"] != "rejected" {
		t.Fatal("blocked private message delivered")
	}
	write(t, bob, incoming{Type: "message", ID: "group", Text: "group"})
	if read(t, bob)["id"] != "group" || read(t, carol)["id"] != "group" {
		t.Fatal("other group recipients lost message")
	}
	expectPong(t, alice)
	expectPong(t, aliceOther)
	// A new connection must enforce stored preferences without waiting for any client setup.
	aliceFresh := directConnection(t, h, url, a)
	bobFresh := directConnection(t, h, url, b)
	write(t, bobFresh, incoming{Type: "watch", Watch: []string{a}})
	expectContact(t, bobFresh, nil)
	write(t, bobFresh, incoming{Type: "message", ID: "after-reconnect", Text: "blocked", To: a})
	if read(t, bobFresh)["type"] != "rejected" {
		t.Fatal("reconnect bypassed block")
	}
	expectPong(t, aliceFresh)
	enabled = false
	write(t, alice, incoming{Type: "block", ID: "unblock", UserID: b, Blocked: &enabled})
	if read(t, alice)["type"] != "privacy" || read(t, alice)["type"] != "updated" {
		t.Fatal("unblock failed")
	}
	expectContact(t, bob, true)
	expectContact(t, bobFresh, true)
}

func TestContactPresenceTracksLastDeviceAndHidesBlockedStatusChanges(t *testing.T) {
	h, url := fixture(t)
	a, b := uuid.NewString(), uuid.NewString()
	h.relationships = &memoryRelationships{blocks: make(map[string]map[string]bool)}
	alice := directConnection(t, h, url, a)
	write(t, alice, incoming{Type: "watch", Watch: []string{b}})
	expectContact(t, alice, false)
	bob := directConnection(t, h, url, b)
	expectContact(t, alice, true)
	bobOther := directConnection(t, h, url, b)
	bob.Close()
	expectPong(t, alice)
	bobOther.Close()
	expectContact(t, alice, false)
}
