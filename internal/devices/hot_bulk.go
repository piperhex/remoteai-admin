package devices

import "github.com/piperhex/remoteai-admin/internal/platform"

// Call under chatSessions.mu. A reconnect starts a new endpoint generation, so
// frames authorized for an old pair can never reach the replacement connection.
func (session *hotSession) cancelBulk() {
	session.closeFileRelays()
	if session.bulkDone != nil {
		close(session.bulkDone)
		session.bulkDone = nil
		notifyBulkQueues()
	}
}

func (s *chatSessions) stopBulkSession(delivery relayDelivery) {
	s.mu.Lock()
	defer s.mu.Unlock()
	session := s.hot.sessions[delivery.sessionID]
	if session == nil || session.bulkDone != delivery.sessionDone {
		return
	}
	// Fence file records without removing the chat session or its authenticated keys.
	session.cancelBulk()
	message := platform.JSON{"type": "file-relay-reset", "sessionId": session.id}
	session.desktop.socket.send(session.fileRelayMessage(message, 0), nil)
	if session.mobile != nil {
		session.mobile.socket.send(session.fileRelayMessage(message, 1), nil)
	}
	delivery.source.diagnostics.log("file relay reset", "reason", "queue pressure")
}
