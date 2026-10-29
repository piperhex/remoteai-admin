package devices

import (
	"strings"
	"testing"

	"github.com/codex-switch/admin-go/internal/platform"
)

func joinAttachment(t *testing.T, sessions *chatSessions, owner string) *peer {
	t.Helper()
	client := queuedPeer()
	err := sessions.join(client, loungeIdentity(owner), platform.JSON{
		"publicKey": strings.Repeat("ab", 32), "purpose": "attachments"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	updateFrame(t, client)
	return client
}

func TestAttachmentPairsUseImmediateMeteredRelayAndSeparateHistory(t *testing.T) {
	sessions := newChatSessions(nil)
	history := joinHistory(t, sessions, "alice")
	source := joinAttachment(t, sessions, "alice")
	target := joinAttachment(t, sessions, "bob")
	outsider := joinAttachment(t, sessions, "charlie")
	if len(sessions.sessions) != 0 || len(history.queue) != 0 {
		t.Fatal("attachment registered for history")
	}
	if err := sessions.pairAttachment(target, platform.JSON{"id": "request", "owner": "alice",
		"device": "runtime-device", "fileId": "authorized-file"}, nil); err != nil {
		t.Fatal(err)
	}
	left, right := updateFrame(t, source), updateFrame(t, target)
	if left["type"] != "attachment-peer" || left["sessionId"] != right["sessionId"] ||
		left["fileId"] != "authorized-file" || left["viewer"] != "bob" {
		t.Fatal(left, right)
	}
	var delivered relayDelivery
	sessions.deliver = func(value relayDelivery, _ platform.JSON) { delivered = value }
	frame := platform.JSON{"type": "relay", "sessionId": left["sessionId"], "payload": "aabbccdd"}
	if err := sessions.route(source, frame); err != nil {
		t.Fatal("initial relay unavailable", err)
	}
	if delivered.source != source || delivered.target != target || delivered.owner != "bob" {
		t.Fatal("attachment bypassed existing relay accounting")
	}
	if sessions.route(outsider, frame) == nil {
		t.Fatal("unrelated account entered attachment channel")
	}
	if err := sessions.pairAttachment(history, platform.JSON{}, nil); err == nil {
		t.Fatal("history socket accepted")
	}
}
