package lounge

import (
	"context"
	"encoding/json"
	"time"

	"github.com/piperhex/remoteai-admin/internal/browserbookmarks"
)

// Bookmark traffic reuses the authenticated account connection and never enters chat history.
func (h *hub) exchangeBookmarks(client *peer, input incoming) {
	if h.bookmarks == nil || input.Type == "bookmarks-save" && input.Bookmarks == nil {
		h.reject(client, input.ID, "market.bookmarkSyncFailed")
		return
	}
	var edit *browserbookmarks.Document
	if input.Type == "bookmarks-save" {
		edit = input.Bookmarks
	}
	h.settingsMu.Lock()
	defer h.settingsMu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	doc, accepted, err := h.bookmarks.Exchange(ctx, client.user.ID, edit)
	if err != nil {
		h.reject(client, input.ID, "market.bookmarkSyncFailed")
		return
	}
	frame, err := json.Marshal(struct {
		Type     string                    `json:"type"`
		ID       string                    `json:"id"`
		Document browserbookmarks.Document `json:"document"`
		Accepted bool                      `json:"accepted"`
	}{"bookmarks", input.ID, doc, accepted})
	if err != nil {
		h.reject(client, input.ID, "market.bookmarkSyncFailed")
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
