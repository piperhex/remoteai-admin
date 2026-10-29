package lounge

import (
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func enableGroupAcks(t *testing.T, h *hub, client *websocket.Conn, user string) {
	t.Helper()
	write(t, client, incoming{Type: "authenticate", GroupAcks: true,
		Ticket: credential(t, h, user, time.Now().Add(time.Minute))})
	if read(t, client)["type"] != "ready" {
		t.Fatal("group acknowledgment handshake failed")
	}
}

func holdRecipientQueue(t *testing.T, h *hub, user string) func() {
	t.Helper()
	h.mu.Lock()
	var target *peer
	for client := range h.clients {
		if client.user.ID == user {
			target = client
			break
		}
	}
	h.mu.Unlock()
	if target == nil {
		t.Fatal("recipient missing")
	}
	target.mu.Lock()
	var once sync.Once
	release := func() { once.Do(target.mu.Unlock) }
	t.Cleanup(release)
	return release
}

func TestLoungeGroupAcknowledgesBeforeBlockedFanoutWithoutImageEcho(t *testing.T) {
	h, url := fixture(t)
	alice := connect(t, h, url, "alice")
	enableGroupAcks(t, h, alice, "alice")
	bob := connect(t, h, url, "bob")
	otherSession := connect(t, h, url, "alice")
	release := holdRecipientQueue(t, h, "bob")
	input := incoming{Type: "message", ID: "mixed", Text: "hello", Images: []string{picture(t)}}
	write(t, alice, input)
	ack := read(t, alice) // Must arrive even while broadcast is unable to enqueue Bob's message.
	if ack["type"] != "accepted" || ack["id"] != input.ID || len(ack) != 3 {
		t.Fatalf("expected compact confirmation without message content: %v", ack)
	}
	if _, err := time.Parse(time.RFC3339Nano, ack["sentAt"].(string)); err != nil {
		t.Fatal("invalid server timestamp", err)
	}
	release()
	for _, recipient := range []*websocket.Conn{bob, otherSession} {
		frame := read(t, recipient)
		if frame["type"] != "message" || frame["id"] != input.ID || frame["sentAt"] != ack["sentAt"] ||
			frame["text"] != input.Text || frame["images"].([]interface{})[0] != input.Images[0] {
			t.Fatal("recipient did not receive the complete accepted message")
		}
	}
	write(t, alice, incoming{Type: "ping"})
	if read(t, alice)["type"] != "pong" {
		t.Fatal("sender received a redundant full message echo")
	}
}

func TestLoungeGroupNeverAcknowledgesRejectedOrInvalidMessages(t *testing.T) {
	for _, mode := range []string{"muted", "invalid"} {
		t.Run(mode, func(t *testing.T) {
			h, url := fixture(t)
			until := time.Now().Add(time.Hour)
			if mode == "muted" {
				h.policies = &memoryPolicies{values: map[string]policy{"alice": {MutedUntil: &until}}}
			}
			alice := connect(t, h, url, "alice")
			enableGroupAcks(t, h, alice, "alice")
			input := incoming{Type: "message", ID: "rejected"}
			if mode == "muted" {
				input.Text = "must be rejected"
			}
			write(t, alice, input)
			if mode == "invalid" {
				alice.SetReadDeadline(time.Now().Add(time.Second))
				if _, _, err := alice.ReadMessage(); err == nil {
					t.Fatal("invalid content was acknowledged")
				}
				return
			}
			if read(t, alice)["type"] != "policy" || read(t, alice)["type"] != "rejected" {
				t.Fatal("muted content was acknowledged")
			}
			write(t, alice, incoming{Type: "ping"})
			if read(t, alice)["type"] != "pong" {
				t.Fatal("rejected content was followed by an acknowledgment")
			}
		})
	}
}
