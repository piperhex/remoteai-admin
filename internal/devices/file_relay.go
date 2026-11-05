package devices

import (
	"crypto/subtle"
	"errors"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/piperhex/remoteai-admin/internal/platform"
)

const fileRelayWait = 30 * time.Second

type fileRelayEndpoints struct {
	tickets [2]string
	sockets [2]*peer
}

// Tickets are endpoint-specific capabilities, issued only inside the authenticated chat socket.
// They authorize file records for this session, never chat RPC, another session or cloud APIs.
func (session *hotSession) fileRelayMessage(message platform.JSON, side int) platform.JSON {
	if session.files.tickets[side] == "" {
		ticket, err := uuid.NewRandom()
		if err != nil {
			return message
		}
		session.files.tickets[side] = ticket.String()
	}
	copy := platform.JSON{}
	for key, value := range message {
		copy[key] = value
	}
	copy["fileRelay"] = platform.JSON{"version": 1, "ticket": session.files.tickets[side]}
	return copy
}

func (session *hotSession) closeFileRelays() {
	for _, socket := range session.files.sockets {
		if socket != nil {
			socket.terminate()
		}
	}
	session.files = fileRelayEndpoints{}
}

func (g *ChatGateway) serveFileRelay(c *gin.Context) {
	conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		return
	}
	defer conn.Close()
	conn.SetReadLimit(int64(bulkRecordLimit + relayHeaderBytes + 128))
	if err = conn.SetReadDeadline(time.Now().Add(authTimeout)); err != nil {
		return
	}
	var auth platform.JSON
	if err = conn.ReadJSON(&auth); err != nil {
		return
	}
	client := newPeerWithDiagnostics(conn, newChatDiagnostics())
	defer client.terminate()
	id, err := g.sessions.attachFileRelay(client, auth)
	if err != nil {
		client.close(4001, "Invalid file connection")
		return
	}
	defer g.sessions.detachFileRelay(id, client)
	if err = conn.SetReadDeadline(time.Time{}); err != nil {
		return
	}
	state := &chatConnection{windowStart: time.Now()}
	for {
		if err = g.readFileRelay(client, id, state, g.meter); err != nil {
			client.diagnostics.log("file relay stopped", "reason", chatRejectionReason(err))
			return
		}
	}
}

func (s *chatSessions) attachFileRelay(client *peer, auth platform.JSON) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id, _ := auth["sessionId"].(string)
	ticket, _ := auth["ticket"].(string)
	session := s.hot.sessions[id]
	if auth["type"] != "file-relay-authenticate" || session == nil || !session.expires.After(time.Now()) ||
		session.mobile == nil || session.desktop.socket.closed.Load() || session.mobile.socket.closed.Load() {
		return "", errors.New("invalid file connection")
	}
	for side, expected := range session.files.tickets {
		if expected == "" || subtle.ConstantTimeCompare([]byte(ticket), []byte(expected)) != 1 {
			continue
		}
		if previous := session.files.sockets[side]; previous != nil {
			previous.terminate()
		}
		client.binaryRelay.Store(true)
		client.binaryBulk.Store(true)
		session.files.sockets[side] = client
		client.send(platform.JSON{"type": "file-relay-ready", "sessionId": id, "fileBulkV1": true}, nil)
		notifyBulkQueues()
		role := "file-viewer"
		if side == 0 {
			role = "file-host"
		}
		client.diagnostics.authenticated(chatIdentity{session.owner, session.device, role, session.expires}, nil)
		return id, nil
	}
	return "", errors.New("invalid file ticket")
}

func (s *chatSessions) detachFileRelay(id string, client *peer) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if session := s.hot.sessions[id]; session != nil {
		for side, socket := range session.files.sockets {
			if socket == client {
				session.files.sockets[side] = nil
			}
		}
	}
}

func (g *ChatGateway) readFileRelay(client *peer, id string, state *chatConnection, meter relayTrafficMeter) error {
	kind, reader, err := client.conn.NextReader()
	if err != nil {
		return err
	}
	if kind != websocket.BinaryMessage {
		return errors.New("file socket accepts binary records only")
	}
	bytes, err := readChatPayload(client, kind, reader)
	if err != nil {
		return err
	}
	frame, err := decodeBulkRelay(bytes)
	if err != nil || frame["sessionId"] != id {
		return errors.New("invalid file record")
	}
	if err = g.sessions.waitFilePeer(id, client); err != nil {
		return err
	}
	delivery, err := g.sessions.fileRelayDelivery(id, client)
	if err != nil {
		return err
	}
	client.diagnostics.received(len(bytes))
	if err = g.checkRate(state, len(bytes)); err != nil {
		return err
	}
	output := g.bulkOutput(delivery, bytes, meter)
	return delivery.waitFileRelay(output)
}

func (s *chatSessions) fileRelayDelivery(id string, client *peer) (relayDelivery, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	session := s.hot.sessions[id]
	if session == nil || session.mobile == nil || !session.expires.After(time.Now()) {
		return relayDelivery{}, errors.New("expired file connection")
	}
	side := 0
	if session.files.sockets[0] != client {
		side = 1
		if session.files.sockets[1] != client {
			return relayDelivery{}, errors.New("unbound file connection")
		}
	}
	target := session.files.sockets[1-side]
	if target == nil || target.closed.Load() {
		target = session.desktop.socket
		if side == 0 {
			target = session.mobile.socket
		}
		if target.fileConnection.Load() {
			return relayDelivery{}, errors.New("file peer unavailable")
		}
	}
	if target.closed.Load() || !target.binaryBulk.Load() {
		return relayDelivery{}, errors.New("file peer unavailable")
	}
	if session.bulkDone == nil {
		session.bulkDone = make(chan struct{})
	}
	return relayDelivery{owner: session.owner, device: session.device, sessionID: id,
		source: client, target: target, fromDesktop: side == 0, traffic: &session.traffic,
		sessionDone: session.bulkDone}, nil
}

func (s *chatSessions) waitFilePeer(id string, client *peer) error {
	timer := time.NewTimer(fileRelayWait)
	defer timer.Stop()
	for {
		bulkQueues.Lock()
		changed := bulkQueues.changed
		bulkQueues.Unlock()
		if _, err := s.fileRelayDelivery(id, client); err == nil {
			return nil
		}
		select {
		case <-changed:
		case <-client.done:
			return errors.New("file socket closed")
		case <-timer.C:
			return errors.New("file peer unavailable")
		}
	}
}

// Only this dedicated reader waits. TCP backpressure bounds the sender without blocking chat readers.
func (delivery relayDelivery) waitFileRelay(frame outputFrame) error {
	timer := time.NewTimer(fileRelayWait)
	defer timer.Stop()
	for {
		bulkQueues.Lock()
		changed := bulkQueues.changed
		bulkQueues.Unlock()
		if frame.cancelled() || delivery.target.closed.Load() {
			return errors.New("file connection closed")
		}
		if delivery.target.trySendBulk(frame, delivery.owner) {
			return nil
		}
		select {
		case <-changed:
		case <-delivery.source.done:
			return errors.New("file source closed")
		case <-delivery.target.done:
			return errors.New("file target closed")
		case <-delivery.sessionDone:
			return errors.New("file session changed")
		case <-timer.C:
			return errors.New("file connection stalled")
		}
	}
}
