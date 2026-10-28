package lounge

import "encoding/json"

const maxWatchedContacts = 200

type contactPresence struct {
	ID     string `json:"id"`
	Online *bool  `json:"online"`
}

// Presence subscriptions are connection-only. Blockers hide their status from blocked viewers.
func (h *hub) watch(client *peer, ids []string) bool {
	if len(ids) > maxWatchedContacts {
		return false
	}
	watching := make(map[string]bool, len(ids))
	for _, id := range ids {
		if id == "" || len(id) > 80 {
			return false
		}
		if id != client.user.ID {
			watching[id] = true
		}
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	client.watching, client.presence = watching, nil
	h.publishContactPresenceLocked()
	return true
}

func samePresence(a, b *bool) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func (h *hub) publishContactPresenceLocked() {
	online := make(map[string]bool)
	for client := range h.clients {
		if h.activeLocked(client) {
			online[client.user.ID] = true
		}
	}
	for client := range h.clients {
		if !h.activeLocked(client) || len(client.watching) == 0 {
			continue
		}
		h.sendContactPresenceLocked(client, online)
	}
}

func (h *hub) sendContactPresenceLocked(client *peer, online map[string]bool) {
	changes := []contactPresence{}
	if client.presence == nil {
		client.presence = make(map[string]*bool)
	}
	for id := range client.watching {
		var current *bool
		if client.canContact(id) {
			value := online[id]
			current = &value
		}
		previous, known := client.presence[id]
		if !known || !samePresence(previous, current) {
			changes = append(changes, contactPresence{id, current})
			client.presence[id] = current
		}
	}
	if len(changes) == 0 {
		return
	}
	data, err := json.Marshal(struct {
		Type     string            `json:"type"`
		Contacts []contactPresence `json:"contacts"`
	}{"contacts", changes})
	if err == nil {
		client.enqueue(data)
	}
}
