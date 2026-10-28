package lounge

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
)

func (h *hub) direct(sender *peer, input incoming) {
	targetID, err := uuid.Parse(input.To)
	if err != nil || targetID.String() == sender.user.ID || h.relationships == nil {
		h.reject(sender, input.ID, "client.lounge.directUnavailable")
		return
	}
	input.To = targetID.String()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	target, err := h.relationships.Account(ctx, input.To)
	if err != nil {
		h.reject(sender, input.ID, "client.lounge.directUnavailable")
		return
	}
	h.mu.Lock()
	available := h.directAvailableLocked(sender, input)
	h.mu.Unlock()
	if !available {
		h.reject(sender, input.ID, "client.lounge.directUnavailable")
		return
	}
	if h.directPolicy != nil {
		permission, err := h.directPermission(sender.user.ID, input)
		if err == nil {
			err = h.directPolicy.Admit(ctx, permission)
		}
		if err != nil {
			code := "client.lounge.directUnavailable"
			if errors.Is(err, errAwaitingReply) {
				code = awaitingReply
			}
			h.reject(sender, input.ID, code)
			return
		}
	}
	h.relayDirect(sender, input, target)
	h.sendDirectPermissions(sender.user.ID, []string{target.ID})
	h.sendDirectPermissions(target.ID, []string{sender.user.ID})
}

func (h *hub) directAvailableLocked(sender *peer, input incoming) bool {
	if !h.activeLocked(sender) || !sender.direct || !sender.canContact(input.To) || input.Receipts && !sender.receipts {
		return false
	}
	for client := range h.clients {
		if h.activeLocked(client) && client.direct && (!input.Receipts || client.receipts) &&
			client.user.ID == input.To && !client.blocks[sender.user.ID] {
			return true
		}
	}
	return false
}

func (h *hub) relayDirect(sender *peer, input incoming, target account) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.directAvailableLocked(sender, input) {
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
