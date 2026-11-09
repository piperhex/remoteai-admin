package devices

import (
	"testing"

	"github.com/piperhex/remoteai-admin/internal/platform"
)

func TestAccountAuthNotificationsOnlyReachCurrentCompatiblePCsOfTheOwner(t *testing.T) {
	g := newControlGateway(nil)
	first, second := queuedPeer(), queuedPeer()
	other, legacy, subscriber, replaced, closed := queuedPeer(), queuedPeer(), queuedPeer(), queuedPeer(), queuedPeer()
	g.sessions[first] = controlSession{owner: "alice", device: "pc-a", kind: "device", accountAuthSync: true}
	g.sessions[second] = controlSession{owner: "alice", device: "pc-b", kind: "device", accountAuthSync: true}
	g.sessions[other] = controlSession{owner: "bob", device: "pc-c", kind: "device", accountAuthSync: true}
	g.sessions[legacy] = controlSession{owner: "alice", device: "old", kind: "device"}
	g.sessions[subscriber] = controlSession{owner: "alice", kind: "subscriber", accountAuthSync: true}
	g.sessions[replaced] = g.sessions[first]
	g.sessions[closed] = controlSession{owner: "alice", device: "closed", kind: "device", accountAuthSync: true}
	g.sockets["alice:pc-a"], g.sockets["alice:pc-b"] = first, second
	g.sockets["bob:pc-c"], g.sockets["alice:old"], g.sockets["alice:closed"] = other, legacy, closed
	closed.closed.Store(true)
	g.deliverAccountAuthUpdate(accountAuthUpdate{"alice", []string{"account-1"}})
	for _, client := range []*peer{first, second} {
		frame := updateFrame(t, client)
		if frame["type"] != "account-auth-updated" || len(frame) != 2 ||
			frame["accountIds"].([]interface{})[0] != "account-1" {
			t.Fatal("unexpected credential notification", frame)
		}
	}
	for _, client := range []*peer{other, legacy, subscriber, replaced, closed} {
		if len(client.queue) != 0 {
			t.Fatal("notification leaked to an unrelated or unsupported session")
		}
	}
}

func TestAccountAuthSyncCapabilityIsExplicitAndBackwardCompatible(t *testing.T) {
	if !hasAccountAuthSync(platform.JSON{"capabilities": []interface{}{"account-auth-sync"}}) {
		t.Fatal("updated client did not negotiate credential notifications")
	}
	for _, value := range []interface{}{nil, "account-auth-sync", []interface{}{"account-usage"}} {
		if hasAccountAuthSync(platform.JSON{"capabilities": value}) {
			t.Fatal("legacy client accepted an unknown message")
		}
	}
}

func TestAccountAuthUploadTriggersTheLocalDeliveryHookWithoutRedis(t *testing.T) {
	deps := &platform.Dependencies{}
	g := newControlGateway(&Service{deps})
	stop := g.startAccountAuthSync()
	defer stop()
	client := queuedPeer()
	g.sessions[client] = controlSession{owner: "alice", device: "pc", kind: "device", accountAuthSync: true}
	g.sockets["alice:pc"] = client
	deps.AccountAuthChanged("alice", []string{"updated"})
	if updateFrame(t, client)["type"] != "account-auth-updated" {
		t.Fatal("upload hook did not deliver the committed credential notification")
	}
}
