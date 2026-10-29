package devices

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/codex-switch/admin-go/internal/platform"
)

func loungeIdentity(owner string) chatIdentity {
	return chatIdentity{owner: owner, device: "runtime-device", role: "lounge", expires: time.Now().Add(time.Minute)}
}

func joinHistory(t *testing.T, sessions *chatSessions, owner string) *peer {
	t.Helper()
	client := queuedPeer()
	err := sessions.join(client, loungeIdentity(owner), platform.JSON{"publicKey": strings.Repeat("ab", 32)}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if updateFrame(t, client)["type"] != "sync-registered" {
		t.Fatal("missing registration")
	}
	return client
}

func TestLoungeSyncPairsOnlySameAccountAndUsesExistingRelay(t *testing.T) {
	sessions := newChatSessions(nil)
	first := joinHistory(t, sessions, "alice")
	outsider := joinHistory(t, sessions, "bob")
	second := joinHistory(t, sessions, "alice")
	left, right := updateFrame(t, first), updateFrame(t, second)
	if left["sessionId"] != right["sessionId"] || left["desktop"] != true || right["desktop"] != false {
		t.Fatal("pair roles or session disagree", left, right)
	}
	if len(outsider.queue) != 0 || len(sessions.sessions) != 1 || len(sessions.desktops) != 0 {
		t.Fatal("history peer leaked to another account or registered as a remote-control host")
	}
	var delivered relayDelivery
	sessions.deliver = func(value relayDelivery, _ platform.JSON) { delivered = value }
	frame := platform.JSON{"type": "relay", "sessionId": left["sessionId"], "payload": "aabbccdd"}
	if err := sessions.route(first, frame); err != nil {
		t.Fatal(err)
	}
	if delivered.owner != "alice" || delivered.source != first || delivered.target != second {
		t.Fatal("encrypted history bypassed the existing metered relay")
	}
	if sessions.route(outsider, frame) == nil {
		t.Fatal("cross-account relay accepted")
	}
	if sessions.route(first, platform.JSON{"type": "request", "sessionId": left["sessionId"]}) == nil {
		t.Fatal("arbitrary control operation accepted")
	}
	sessions.disconnect(second, true)
	if err := sessions.route(first, frame); err != nil {
		t.Fatal("late frame disconnected remaining device", err)
	}
	if len(sessions.sessions) != 0 || len(sessions.lounge) != 2 || updateFrame(t, first)["type"] != "peer-close" {
		t.Fatal("disconnect did not clean up history pairs")
	}
}

func TestLoungeSyncBoundsDevicesAndRenewsBothLeaseEndpoints(t *testing.T) {
	sessions := newChatSessions(nil)
	first := joinHistory(t, sessions, "alice")
	second := joinHistory(t, sessions, "alice")
	updateFrame(t, first)
	updateFrame(t, second)
	identity := loungeIdentity("alice")
	identity.expires = time.Now().Add(5 * time.Minute)
	sessions.renew(first, identity)
	lease := updateFrame(t, first)
	updateFrame(t, second)
	if lease["type"] != "sync-lease" || lease["expiresAt"] != float64(sessions.lounge[second].identity.expires.UnixMilli()) {
		t.Fatal("lease outlived the unrenewed endpoint")
	}
	sessions.renew(second, identity)
	if updateFrame(t, first)["expiresAt"] != float64(identity.expires.UnixMilli()) {
		t.Fatal("lease not extended")
	}
	updateFrame(t, second)
	for i := 2; i < maxLoungeDevices; i++ {
		joinHistory(t, sessions, "alice")
	}
	err := sessions.join(queuedPeer(), identity, platform.JSON{"publicKey": strings.Repeat("ab", 32)}, nil)
	if err == nil || len(sessions.lounge) != maxLoungeDevices {
		t.Fatal("device limit not enforced")
	}
}

func TestLoungeSyncAuthenticationRequiresDedicatedTicketAndStableIdentity(t *testing.T) {
	deps := &platform.Dependencies{LoungeIdentity: func(ticket string) (string, time.Time, error) {
		if ticket != "lounge-ticket" && ticket != "other-account" {
			return "", time.Time{}, errors.New("invalid")
		}
		return ticket, time.Now().Add(time.Minute), nil
	}}
	gateway := &ChatGateway{service: &Service{deps: deps}}
	frame := platform.JSON{"type": "authenticate", "role": "lounge", "deviceId": "runtime",
		"transportVersion": float64(2), "accessToken": "lounge-ticket"}
	identity, err := gateway.authenticate(frame)
	if err != nil || identity.role != "lounge" || identity.owner != "lounge-ticket" {
		t.Fatal(identity, err)
	}
	client := queuedPeer()
	state := &chatConnection{identity: &identity}
	if gateway.renewAuthentication(client, state, platform.JSON{"accessToken": "other-account"}) == nil {
		t.Fatal("renewal switched accounts")
	}
	frame["accessToken"] = "regular-account-token"
	if _, err := gateway.authenticate(frame); err == nil {
		t.Fatal("regular token accepted as lounge ticket")
	}
	frame["accessToken"] = "lounge-ticket"
	frame["transportVersion"] = float64(1)
	if _, err := gateway.authenticate(frame); err == nil {
		t.Fatal("unsupported transport accepted")
	}
}

func TestLoungeSyncGenerationsUseAuthenticatedOwnerAndSanitizeFailures(t *testing.T) {
	const contact = "a6d4b17e-0e4a-4dce-9a94-4b6b8f89bf3b"
	failed := false
	deps := &platform.Dependencies{LoungeGenerations: func(ctx context.Context, owner string, peers []string) (map[string]int64, error) {
		if owner != "alice" || len(peers) != 1 || peers[0] != contact {
			t.Fatal("untrusted history lookup", owner, peers)
		}
		if _, ok := ctx.Deadline(); !ok {
			t.Fatal("unbounded database lookup")
		}
		if failed {
			return nil, errors.New("private database details")
		}
		return map[string]int64{contact: 3}, nil
	}}
	gateway := &ChatGateway{service: &Service{deps: deps}}
	client := queuedPeer()
	state := &chatConnection{identity: new(chatIdentity)}
	*state.identity = loungeIdentity("alice")
	request := platform.JSON{"type": "history-generations", "id": "query", "owner": "mallory",
		"peers": []interface{}{contact}}
	if err := gateway.receive(client, state, request, 100); err != nil {
		t.Fatal(err)
	}
	frame := updateFrame(t, client)
	if frame["generations"].(map[string]interface{})[contact] != float64(3) {
		t.Fatal(frame)
	}
	failed = true
	if err := gateway.receive(client, state, request, 100); err != nil {
		t.Fatal(err)
	}
	frame = updateFrame(t, client)
	if frame["error"] != true || frame["generations"] != nil {
		t.Fatal("database error exposed", frame)
	}
	request["peers"] = []interface{}{"not-a-user-id"}
	if gateway.receive(client, state, request, 100) == nil {
		t.Fatal("invalid peer accepted")
	}
}
