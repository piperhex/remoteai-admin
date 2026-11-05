package devices

import (
	"testing"

	"github.com/piperhex/remoteai-admin/internal/platform"
)

func TestBulkResetFencesLateRecordsUntilSessionRecovery(t *testing.T) {
	sessions, session := fileRelayFixture(t)
	deliveries := 0
	sessions.hot.deliver = func(relayDelivery, platform.JSON) { deliveries++ }
	frame := platform.JSON{"type": "bulk", "sessionId": session.id, "payload": bulkRecord(1)}
	if err := sessions.route(session.desktop.socket, frame); err != nil {
		t.Fatal(err)
	}
	sessions.stopBulkSession(relayDelivery{sessionID: session.id, sessionDone: session.bulkDone,
		source: session.desktop.socket})
	for range 3 {
		if err := sessions.route(session.desktop.socket, frame); err != nil {
			t.Fatal(err)
		}
	}
	if deliveries != 1 {
		t.Fatal("late records reopened a cancelled file stream", deliveries)
	}
	if err := sessions.route(session.desktop.socket, platform.JSON{"type": "relay",
		"sessionId": session.id, "payload": "ab"}); err != nil {
		t.Fatal(err)
	}
	if deliveries != 2 || session.desktop.socket.closed.Load() || session.mobile.socket.closed.Load() {
		t.Fatal("file reset interrupted chat")
	}
	// The authenticated session resume path fences the old endpoints before accepting new records.
	session.cancelBulk()
	if err := sessions.route(session.desktop.socket, frame); err != nil {
		t.Fatal(err)
	}
	if deliveries != 3 {
		t.Fatal("session recovery did not restore legacy file delivery")
	}
}

func TestFileRelayKeepsBillingDeviceAndEndpointScope(t *testing.T) {
	sessions, session := fileRelayFixture(t)
	host, _ := bindTestFile(t, sessions, session, 0)
	viewer, _ := bindTestFile(t, sessions, session, 1)
	for _, client := range []*peer{host, viewer} {
		delivery, err := sessions.fileRelayDelivery(session.id, client)
		if err != nil {
			t.Fatal(err)
		}
		if delivery.owner != session.owner || delivery.device != session.device ||
			delivery.sessionID != session.id || delivery.source != client {
			t.Fatal("dedicated file delivery lost its authenticated billing scope")
		}
	}
}
