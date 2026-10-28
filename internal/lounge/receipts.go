package lounge

import (
	"encoding/json"

	"github.com/google/uuid"
)

// Receipt state and retry queues belong to the endpoints. Only the authenticated source is forwarded.
func (h *hub) forwardReceipt(sender *peer, input incoming) bool {
	if len(input.ID) == 0 || len(input.ID) > 80 || (input.Status != "delivered" && input.Status != "read") {
		return false
	}
	if _, err := uuid.Parse(input.To); err != nil || input.To == sender.user.ID {
		return false
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if !sender.direct || !sender.receipts || !h.activeLocked(sender) {
		return false
	}
	confirmation := input.Type == "receipt-confirm"
	if confirmation && !sender.canContact(input.To) || !confirmation && sender.blocks[input.To] {
		return true
	}
	data, err := json.Marshal(struct {
		Type   string `json:"type"`
		ID     string `json:"id"`
		From   string `json:"from"`
		To     string `json:"to"`
		Status string `json:"status"`
	}{input.Type, input.ID, sender.user.ID, input.To, input.Status})
	if err != nil {
		return false
	}
	for client := range h.clients {
		if !h.activeLocked(client) || !client.receipts || client.user.ID != input.To {
			continue
		}
		// Receipts travel against the original message direction; a blocker can still send and receive its receipts.
		if confirmation && client.blocks[sender.user.ID] || !confirmation && client.blockedBy[sender.user.ID] {
			continue
		}
		client.enqueue(data)
	}
	return true
}
