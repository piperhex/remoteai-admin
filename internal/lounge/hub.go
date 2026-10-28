package lounge

import (
	"encoding/json"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const maxQueuedBytes = 12 * 1024 * 1024

type peer struct {
	conn      *websocket.Conn
	queue     chan []byte
	done      chan struct{}
	once      sync.Once
	mu        sync.Mutex
	queued    int
	user      account
	expires   time.Time
	moderator bool // Guarded by hub.mu, including live permission revocation.
	direct    bool
	receipts  bool
	blocks    map[string]bool
	blockedBy map[string]bool
	watching  map[string]bool
	presence  map[string]*bool
}

type hub struct {
	policies      policyRepository
	key           []byte
	mu            sync.Mutex
	clients       map[*peer]bool
	closed        bool
	presenceCount int
	relationships relationshipRepository
	settingsMu    sync.Mutex
}

func newHub(key []byte) *hub { return &hub{key: key, clients: make(map[*peer]bool)} }

func (p *peer) close() {
	p.once.Do(func() {
		close(p.done)
		// Closing an already broken socket is best effort; no connection or content is logged.
		_ = p.conn.Close()
	})
}

func (p *peer) enqueue(data []byte) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.queued+len(data) > maxQueuedBytes {
		p.close()
		return false
	}
	select {
	case <-p.done:
		return false
	case p.queue <- data:
		p.queued += len(data)
		return true
	default:
		p.close()
		return false
	}
}

func (p *peer) write() {
	defer p.close()
	ticker := time.NewTicker(20 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-p.done:
			return
		case data := <-p.queue:
			p.mu.Lock()
			p.queued -= len(data)
			p.mu.Unlock()
			if p.conn.SetWriteDeadline(time.Now().Add(10*time.Second)) != nil {
				return
			}
			if p.conn.WriteMessage(websocket.TextMessage, data) != nil {
				return
			}
		case <-ticker.C:
			if p.conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(10*time.Second)) != nil {
				return
			}
		}
	}
}

func (h *hub) broadcast(sender *peer, input incoming) {
	profile := sender.user
	profile.Avatar = input.Avatar
	data, err := json.Marshal(message{Type: "message", ID: input.ID, Sender: profile,
		Text: input.Text, Images: input.Images, Reply: input.Reply, SentAt: time.Now().UTC().Format(time.RFC3339Nano)})
	if err != nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	// Only current authenticated connections receive this frame. There is no history, log, or replay store.
	for client, authenticated := range h.clients {
		if authenticated && client.expires.After(time.Now()) && !client.blocks[sender.user.ID] {
			client.enqueue(data)
		}
	}
}

func (h *hub) close() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.closed = true
	for client := range h.clients {
		client.close()
	}
}
