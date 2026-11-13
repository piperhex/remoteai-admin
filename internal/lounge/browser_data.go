package lounge

import (
	"context"
	"encoding/json"
	"time"

	"github.com/piperhex/remoteai-admin/internal/browserdata"
)

// Browser data reuses the authenticated account connection and never enters chat history.
func (h *hub) exchangeBrowserData(client *peer, input incoming) {
	if h.browserData == nil || input.Type == "browser-data-save" && input.BrowserData == nil {
		h.reject(client, input.ID, "market.browserDataOffline")
		return
	}
	var edit *browserdata.Document
	if input.Type == "browser-data-save" {
		edit = input.BrowserData
	}
	h.settingsMu.Lock()
	defer h.settingsMu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	doc, accepted, err := h.browserData.Exchange(ctx, client.user.ID, edit)
	if err != nil {
		h.reject(client, input.ID, "market.browserDataOffline")
		return
	}
	frame, err := json.Marshal(struct {
		Type     string               `json:"type"`
		ID       string               `json:"id"`
		Document browserdata.Document `json:"document"`
		Accepted bool                 `json:"accepted"`
	}{"browser-data", input.ID, doc, accepted})
	if err != nil {
		h.reject(client, input.ID, "market.browserDataOffline")
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	client.enqueue(frame)
	if !accepted {
		return
	}
	for subscriber := range h.clientsByUser[client.user.ID] {
		if subscriber != client && h.activeLocked(subscriber) {
			subscriber.enqueue(frame)
		}
	}
}
