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
	defer func() { client.close(); h.mu.Lock(); delete(h.clients, client); h.mu.Unlock() }()
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
		return h.authorize(client, input.Ticket)
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
	if input.Type != "message" || validate(input) != nil {
		return false
	}
	if h.maySend(client, input.ID) {
		h.broadcast(client, input)
	}
	return true
}

func (h *hub) authorize(client *peer, ticket string) bool {
	user, expires, err := h.authenticate(ticket)
	if err != nil || (client.user.ID != "" && user.ID != client.user.ID) {
		return false
	}
	value, err := h.userPolicy(user.ID)
	if err != nil {
		return false
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	client.user, client.expires = user, expires
	data, err := json.Marshal(struct {
		Type   string  `json:"type"`
		User   account `json:"user"`
		Policy policy  `json:"policy"`
	}{"ready", user, value})
	if err != nil {
		return false
	}
	client.enqueue(data)
	h.clients[client] = true
	client.conn.SetReadLimit(maxFrameBytes)
	return true
}
