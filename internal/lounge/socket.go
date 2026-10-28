package lounge

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	HandshakeTimeout: 10 * time.Second,
	// Browser and Tauri origins vary; a ticket in the first frame is required before joining.
	CheckOrigin: func(*http.Request) bool { return true },
}

func (h *hub) serve(c *gin.Context) {
	conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		return
	}
	client := &peer{conn: conn, queue: make(chan []byte, 32), done: make(chan struct{})}
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		client.close()
		return
	}
	h.clients[client] = false
	h.mu.Unlock()
	defer h.remove(client)
	conn.SetReadLimit(16 * 1024)
	if conn.SetReadDeadline(time.Now().Add(10*time.Second)) != nil {
		return
	}
	conn.SetPongHandler(func(string) error { return conn.SetReadDeadline(time.Now().Add(60 * time.Second)) })
	go client.write()
	h.read(client)
}

func (h *hub) read(client *peer) {
	window, count, bytes := time.Now(), 0, 0
	for {
		kind, data, err := client.conn.ReadMessage()
		if err != nil || kind != websocket.TextMessage {
			return
		}
		if time.Since(window) >= 10*time.Second {
			window, count, bytes = time.Now(), 0, 0
		}
		count++
		bytes += len(data)
		if count > 30 || bytes > 2*maxFrameBytes {
			return
		}
		var input incoming
		if json.Unmarshal(data, &input) != nil {
			return
		}
		if !h.receive(client, input) {
			return
		}
		if client.conn.SetReadDeadline(time.Now().Add(60*time.Second)) != nil {
			return
		}
	}
}

func (h *hub) receive(client *peer, input incoming) bool {
	if input.Type == "authenticate" {
		return h.authorize(client, input)
	}
	if client.user.ID == "" || !client.expires.After(time.Now()) {
		return false
	}
	if input.Type == "ping" {
		client.enqueue([]byte(`{"type":"pong"}`))
		return true
	}
	if input.Type == "moderate" {
		if len(input.ID) == 0 || len(input.ID) > 80 {
			return false
		}
		h.moderate(client, input)
		return true
	}
	if input.Type == "watch" {
		return h.watch(client, input.Watch)
	}
	if input.Type == "block" {
		if len(input.ID) == 0 || len(input.ID) > 80 {
			return false
		}
		h.block(client, input)
		return true
	}
	if input.Type != "message" || validate(input) != nil {
		return false
	}
	if h.maySend(client, input.ID) {
		if input.To != "" {
			h.direct(client, input)
		} else {
			h.broadcast(client, input)
		}
	}
	return true
}

func (h *hub) authorize(client *peer, input incoming) bool {
	user, expires, err := h.authenticate(input.Ticket)
	if err != nil || (client.user.ID != "" && user.ID != client.user.ID) {
		return false
	}
	value, err := h.userPolicy(user.ID)
	if err != nil {
		return false
	}
	h.settingsMu.Lock()
	defer h.settingsMu.Unlock()
	privacy, err := h.readRelationships(user.ID)
	if err != nil {
		return false
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	client.user, client.expires = user, expires
	client.moderator = value.Moderator
	client.direct = input.Direct
	client.applyRelationships(privacy)
	h.clients[client] = true
	data, err := json.Marshal(struct {
		Type        string    `json:"type"`
		User        account   `json:"user"`
		Policy      policy    `json:"policy"`
		OnlineCount *int      `json:"onlineCount,omitempty"`
		Direct      bool      `json:"direct"`
		Blocked     []account `json:"blocked"`
	}{"ready", user, value, h.onlineForLocked(value.Moderator), true, privacy.Blocked})
	if err != nil {
		return false
	}
	client.enqueue(data)
	h.publishPresenceLocked(client)
	h.publishContactPresenceLocked()
	client.conn.SetReadLimit(maxFrameBytes)
	return true
}
