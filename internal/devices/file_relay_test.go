package devices

import (
	"bytes"
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/piperhex/remoteai-admin/internal/platform"
)

func fileRelayFixture(t *testing.T) (*chatSessions, *hotSession) {
	t.Helper()
	host, _ := bulkBackpressurePeer(t)
	viewer, _ := bulkBackpressurePeer(t)
	sessions, _ := bulkTestDelivery(t, host, viewer)
	session := sessions.hot.sessions["download"]
	session.fileRelayMessage(platform.JSON{}, 0)
	session.fileRelayMessage(platform.JSON{}, 1)
	return sessions, session
}

func bindTestFile(t *testing.T, sessions *chatSessions, session *hotSession, side int) (*peer, *websocket.Conn) {
	t.Helper()
	socket, remote := bulkBackpressurePeer(t)
	socket.diagnostics = newChatDiagnostics()
	_, err := sessions.attachFileRelay(socket, platform.JSON{"type": "file-relay-authenticate",
		"sessionId": session.id, "ticket": session.files.tickets[side]})
	if err != nil {
		t.Fatal(err)
	}
	<-socket.queue // The authenticated ready message precedes all bulk output.
	return socket, remote
}

func TestFileRelayCapabilitiesAndLifecycle(t *testing.T) {
	sessions, session := fileRelayFixture(t)
	if session.files.tickets[0] == session.files.tickets[1] {
		t.Fatal("shared endpoint capability")
	}
	host, _ := bindTestFile(t, sessions, session, 0)
	viewer, _ := bindTestFile(t, sessions, session, 1)
	invalid, _ := bulkBackpressurePeer(t)
	for _, auth := range []platform.JSON{
		{"type": "file-relay-authenticate", "sessionId": "other", "ticket": session.files.tickets[0]},
		{"type": "file-relay-authenticate", "sessionId": session.id, "ticket": "invalid"},
		{"type": "authenticate", "sessionId": session.id, "ticket": session.files.tickets[0]},
	} {
		if _, err := sessions.attachFileRelay(invalid, auth); err == nil {
			t.Fatal("invalid capability accepted")
		}
	}
	replacement, _ := bindTestFile(t, sessions, session, 0)
	if !host.closed.Load() || viewer.closed.Load() || session.desktop.socket.closed.Load() {
		t.Fatal("file replacement closed unrelated transport")
	}
	sessions.detachFileRelay(session.id, host)
	delivery, err := sessions.fileRelayDelivery(session.id, replacement)
	if err != nil || delivery.target != viewer {
		t.Fatal("stale detach removed replacement", err)
	}
	ticket := session.files.tickets[0]
	session.cancelBulk()
	if !replacement.closed.Load() || !viewer.closed.Load() || sessions.hot.sessions[session.id] == nil {
		t.Fatal("session recovery did not isolate files")
	}
	if _, err := sessions.attachFileRelay(invalid, platform.JSON{"type": "file-relay-authenticate",
		"sessionId": session.id, "ticket": ticket}); err == nil {
		t.Fatal("revoked capability reused")
	}
}

func TestFileRelayNeverFallsBackIntoNegotiatedChatSocket(t *testing.T) {
	sessions, session := fileRelayFixture(t)
	host, _ := bindTestFile(t, sessions, session, 0)
	legacy, err := sessions.fileRelayDelivery(session.id, host)
	if err != nil || legacy.target != session.mobile.socket {
		t.Fatal("legacy viewer lost compatibility")
	}
	session.mobile.socket.fileConnection.Store(true)
	if _, err = sessions.fileRelayDelivery(session.id, host); err == nil {
		t.Fatal("new viewer used chat for files")
	}
	viewer, _ := bindTestFile(t, sessions, session, 1)
	delivery, err := sessions.fileRelayDelivery(session.id, host)
	if err != nil || delivery.target != viewer {
		t.Fatal("file endpoint not selected")
	}
	viewer.terminate()
	sessions.detachFileRelay(session.id, viewer)
	if _, err = sessions.fileRelayDelivery(session.id, host); err == nil {
		t.Fatal("file outage fell back to chat")
	}
	session.expires = time.Now().Add(-time.Second)
	if _, err = sessions.fileRelayDelivery(session.id, host); err == nil {
		t.Fatal("expired lease accepted")
	}
}

func TestFileRelaySlowMultiMegabyteTransferKeepsChatAliveAndMetersBytes(t *testing.T) {
	sessions, session := fileRelayFixture(t)
	host, sender := bindTestFile(t, sessions, session, 0)
	viewer, receiver := bindTestFile(t, sessions, session, 1)
	gateway := &ChatGateway{sessions: sessions, traffic: newTrafficCounter(nil)}
	var charged atomic.Int64
	meter := relayMeterFunc(func(_ context.Context, owner string, size int, write func() error) error {
		if owner != session.owner {
			t.Error("wrong account")
		}
		if err := write(); err != nil {
			return err
		}
		charged.Add(int64(size))
		return nil
	})
	const count = 512
	wire := fullBulkWire(t)
	written, forwarded := make(chan error, 1), make(chan error, 1)
	go func() {
		for range count {
			if err := sender.WriteMessage(websocket.BinaryMessage, wire); err != nil {
				written <- err
				return
			}
		}
		written <- nil
	}()
	go func() {
		state := &chatConnection{windowStart: time.Now()}
		for range count {
			if err := gateway.readFileRelay(host, session.id, state, meter); err != nil {
				forwarded <- err
				return
			}
		}
		forwarded <- nil
	}()
	waitBulkCondition(t, func() bool { return viewer.bulkBuffered.Load() > bulkConnectionQueue/2 })
	if viewer.bulkBuffered.Load() > bulkConnectionQueue {
		t.Fatal("unbounded file queue")
	}
	// Leave the file writer blocked while a real chat frame is routed successfully.
	if err := sessions.route(session.desktop.socket, platform.JSON{"type": "relay", "sessionId": session.id,
		"payload": "ab"}); err != nil {
		t.Fatal(err)
	}
	if len(session.mobile.socket.queue) != 1 || session.desktop.socket.closed.Load() || session.mobile.socket.closed.Load() {
		t.Fatal("file backpressure blocked chat")
	}
	stopped := make(chan struct{})
	go func() { defer close(stopped); viewer.writeLoop() }()
	defer func() { viewer.terminate(); <-stopped }()
	for range count {
		kind, got, err := receiver.ReadMessage()
		if err != nil || kind != websocket.BinaryMessage || !bytes.Equal(got, wire) {
			t.Fatal("file changed", err)
		}
		time.Sleep(time.Millisecond)
	}
	if err := <-written; err != nil {
		t.Fatal(err)
	}
	if err := <-forwarded; err != nil {
		t.Fatal(err)
	}
	waitBulkCondition(t, func() bool { return viewer.bulkBuffered.Load() == 0 })
	if charged.Load() != int64(count*len(wire)) || session.traffic.upload.Load() != charged.Load() {
		t.Fatal("incorrect successful-byte accounting")
	}
	if sessions.hot.sessions[session.id] != session {
		t.Fatal("download removed chat")
	}
}

func TestFileRelayRejectsRPCAndWrongSessionWithoutClosingChat(t *testing.T) {
	for _, wrongSession := range []bool{false, true} {
		sessions, session := fileRelayFixture(t)
		host, sender := bindTestFile(t, sessions, session, 0)
		gateway := &ChatGateway{sessions: sessions}
		kind, payload := websocket.TextMessage, []byte(`{"type":"relay","sessionId":"download","payload":"ab"}`)
		if wrongSession {
			kind = websocket.BinaryMessage
			payload, _ = encodeBulkRelay(platform.JSON{"sessionId": "other", "payload": bulkRecord(1)})
		}
		if err := sender.WriteMessage(kind, payload); err != nil {
			t.Fatal(err)
		}
		if err := gateway.readFileRelay(host, session.id, &chatConnection{}, nil); err == nil {
			t.Fatal("invalid file frame accepted")
		}
		if session.desktop.socket.closed.Load() || session.mobile.socket.closed.Load() {
			t.Fatal("chat was closed")
		}
	}
}
