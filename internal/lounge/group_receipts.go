package lounge

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/json"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

const groupReceiptAudience = "lounge-group-read"
const maxGroupReceiptBytes = 2048
const invalidGroupReceipt = "invalid-group-receipt"

type groupReceiptRejection struct {
	Type         string `json:"type"`
	Code         string `json:"code"`
	ReceiptType  string `json:"receiptType"`
	ID           string `json:"id"`
	From         string `json:"from"`
	To           string `json:"to"`
	GroupReceipt string `json:"groupReceipt"`
}

func (h *hub) groupReceiptKey() []byte {
	key := h.directKey
	if len(key) == 0 {
		key = h.key // Protocol fixtures have no production configuration.
	}
	digest := hmac.New(sha256.New, key)
	digest.Write([]byte(groupReceiptAudience))
	return digest.Sum(nil)
}

// A signed capability proves which administrator message was sent without retaining server history.
// Receipts and unique-reader lists remain in the endpoints' local archives, including across restarts.
func (h *hub) issueGroupReceipt(sender *peer, id string) (string, error) {
	if !sender.groupReads || !sender.moderator {
		return "", nil
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.RegisteredClaims{
		Subject: sender.user.ID, ID: id, Audience: []string{groupReceiptAudience},
	}).SignedString(h.groupReceiptKey())
}

func (h *hub) groupReceiptAuthor(input incoming) (string, error) {
	if input.ID == "" || len(input.ID) > 80 || input.GroupReceipt == "" ||
		len(input.GroupReceipt) > maxGroupReceiptBytes {
		return "", errInvalid
	}
	var claims jwt.RegisteredClaims
	_, err := jwt.ParseWithClaims(input.GroupReceipt, &claims,
		func(*jwt.Token) (interface{}, error) { return h.groupReceiptKey(), nil },
		jwt.WithValidMethods([]string{"HS256"}), jwt.WithAudience(groupReceiptAudience))
	if err != nil || claims.ID != input.ID || claims.Subject == "" {
		return "", errInvalid
	}
	return claims.Subject, nil
}

func (h *hub) forwardGroupReceipt(sender *peer, input incoming) bool {
	if input.ID == "" || len(input.ID) > 80 || len(input.GroupReceipt) > maxGroupReceiptBytes {
		return false
	}
	if _, err := uuid.Parse(input.To); err != nil || input.To == sender.user.ID {
		return false
	}
	author, err := h.groupReceiptAuthor(input)
	if err != nil {
		return h.rejectGroupReceipt(sender, input)
	}
	confirmation := input.Type == "group-read-confirm"
	if !confirmation && input.To != author || confirmation && sender.user.ID != author {
		return false
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if !sender.groupReads || !h.activeLocked(sender) || confirmation && !sender.moderator {
		return false
	}
	if !sender.canContact(input.To) || sender.blocks[input.To] {
		return true
	}
	return h.relayGroupReceiptLocked(sender, input, confirmation)
}

// Old clients ignore this frame and may retry, but a stale capability must not end their session.
func (h *hub) rejectGroupReceipt(sender *peer, input incoming) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if !sender.groupReads || !h.activeLocked(sender) {
		return false
	}
	data, err := json.Marshal(groupReceiptRejection{
		Type: "group-receipt-rejected", Code: invalidGroupReceipt, ReceiptType: input.Type,
		ID: input.ID, From: input.To, To: sender.user.ID, GroupReceipt: input.GroupReceipt,
	})
	if err != nil {
		return false
	}
	sender.enqueue(data)
	return true
}

func (h *hub) relayGroupReceiptLocked(sender *peer, input incoming, confirmation bool) bool {
	var reader *account
	if !confirmation {
		profile := withoutAvatar(sender.user)
		profile.Clients, profile.ClientVersions = nil, nil
		reader = &profile
	}
	data, err := json.Marshal(struct {
		Type         string   `json:"type"`
		ID           string   `json:"id"`
		From         string   `json:"from"`
		To           string   `json:"to"`
		GroupReceipt string   `json:"groupReceipt"`
		Reader       *account `json:"reader,omitempty"`
	}{input.Type, input.ID, sender.user.ID, input.To, input.GroupReceipt, reader})
	if err != nil {
		return false
	}
	for client := range h.clientsByUser[input.To] {
		if h.activeLocked(client) && client.groupReads && !client.blocks[sender.user.ID] &&
			!client.blockedBy[sender.user.ID] && (confirmation || client.moderator) {
			client.enqueue(data)
		}
	}
	return true
}
