package lounge

import (
	"encoding/hex"
	"encoding/json"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

// Old process keys cannot be recovered. Only the authenticated author's device can
// authorize a fresh image grant from its own archived message and original audience.
func (h *hub) renewImageGrant(client *peer, input incoming) {
	if _, err := uuid.Parse(input.To); err != nil || input.MessageID == "" || len(input.MessageID) > 80 {
		return
	}
	if _, err := uuid.Parse(input.FileID); err != nil {
		return
	}
	frame := map[string]interface{}{"id": input.ID, "from": client.user.ID, "messageId": input.MessageID,
		"fileId": input.FileID, "type": input.Type}
	if input.Type == "image-grant-request" {
		if proof, err := hex.DecodeString(input.Proof); err != nil || len(proof) != 32 {
			return
		}
		frame["proof"] = input.Proof
	} else {
		grant, err := h.renewApprovedImage(client.user.ID, input)
		if err != nil {
			return
		}
		frame["type"], frame["grant"] = "image-grant", grant
	}
	data, err := json.Marshal(frame)
	if err != nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.activeLocked(client) || client.blocks[input.To] || client.blockedBy[input.To] {
		return
	}
	for target := range h.clientsByUser[input.To] {
		if h.activeLocked(target) && !target.blocks[client.user.ID] && !target.blockedBy[client.user.ID] {
			target.enqueue(data)
		}
	}
}

func (h *hub) renewApprovedImage(author string, input incoming) (string, error) {
	if input.Conversation == nil || len(input.Attachments) != 1 || input.Generation < 0 {
		return "", errInvalid
	}
	recipient := *input.Conversation
	if recipient != "" && recipient != input.To && author != input.To {
		return "", errInvalid
	}
	file := input.Attachments[0]
	if file.Kind != "image" || file.ID != input.FileID || !validAttachment(file, recipient) {
		return "", errInvalid
	}
	file.Grant = ""
	claims := attachmentClaims{File: file, Device: file.Device, Recipient: recipient, Generation: input.Generation,
		RegisteredClaims: jwt.RegisteredClaims{Subject: author, Audience: []string{"lounge-attachment"}}}
	if err := h.checkAttachmentGeneration(claims); err != nil {
		return "", err
	}
	return h.signAttachment(claims)
}
