package lounge

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

func (h *hub) direct(sender *peer, input incoming) {
	if _, err := uuid.Parse(input.To); err != nil || input.To == sender.user.ID || h.relationships == nil {
		h.reject(sender, input.ID, "client.lounge.directUnavailable")
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	target, err := h.relationships.Account(ctx, input.To)
	if err != nil {
		h.reject(sender, input.ID, "client.lounge.directUnavailable")
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if !sender.direct || !sender.canContact(target.ID) || input.Receipts && !sender.receipts {
		h.reject(sender, input.ID, "client.lounge.directUnavailable")
		return
	}
	profile := sender.user
	profile.Avatar = input.Avatar
	data, err := json.Marshal(message{Type: "message", ID: input.ID, Sender: profile, Text: input.Text,
		Images: input.Images, Reply: input.Reply, Recipient: &target, Receipts: input.Receipts,
		SentAt: time.Now().UTC().Format(time.RFC3339Nano)})
	if err != nil {
		return
	}
	delivered := false
	for client := range h.clients {
		if h.activeLocked(client) && client.direct && (!input.Receipts || client.receipts) &&
			client.user.ID == target.ID && !client.blocks[sender.user.ID] {
			delivered = client.enqueue(data) || delivered
		}
	}
	if !delivered {
		h.reject(sender, input.ID, "client.lounge.directUnavailable")
		return
	}
	for client := range h.clients {
		if h.activeLocked(client) && client.direct && (!input.Receipts || client.receipts) && client.user.ID == sender.user.ID {
			client.enqueue(data)
		}
	}
}

func (h *hub) activeLocked(client *peer) bool {
	if !h.clients[client] || !client.expires.After(time.Now()) {
		return false
	}
	select {
	case <-client.done:
		return false
	default:
		return true
	}
}
