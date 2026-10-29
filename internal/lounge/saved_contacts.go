package lounge

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

const savedContactsPageSize = 200

type savedContactRepository interface {
	Page(context.Context, string, string) ([]account, error)
}

type postgresSavedContacts struct{ db *gorm.DB }

// Mutual conversations already have durable permission rows. Restore only those current relationships,
// never message content, one-way introductions, deleted conversations or other users' contact lists.
func (p postgresSavedContacts) Page(ctx context.Context, owner, after string) ([]account, error) {
	if after == "" {
		after = uuid.Nil.String()
	}
	users := []account{}
	err := p.db.Session(&gorm.Session{Logger: logger.Default.LogMode(logger.Silent)}).WithContext(ctx).
		Raw(`SELECT u.id, u.email AS account FROM lounge_direct_permissions p
			JOIN users u ON u.id = CASE WHEN p.first_user = ? THEN p.second_user ELSE p.first_user END
			WHERE (p.first_user = ? OR p.second_user = ?) AND NOT p.fresh AND p.initiator IS NULL
			AND NOT u.disabled AND u.id > ? ORDER BY u.id LIMIT ?`,
			owner, owner, owner, after, savedContactsPageSize+1).Scan(&users).Error
	return users, err
}

func (h *hub) syncSavedContacts(client *peer, input incoming) bool {
	if h.savedContacts == nil || !client.direct || len(input.ID) == 0 || len(input.ID) > 80 {
		return false
	}
	if input.Cursor != "" {
		if id, err := uuid.Parse(input.Cursor); err != nil || id.String() != input.Cursor {
			return false
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	users, err := h.savedContacts.Page(ctx, client.user.ID, input.Cursor)
	frame := struct {
		Type   string    `json:"type"`
		ID     string    `json:"id"`
		Users  []account `json:"users"`
		Cursor string    `json:"cursor,omitempty"`
		Error  bool      `json:"error,omitempty"`
	}{Type: "saved-contacts", ID: input.ID, Users: users, Error: err != nil}
	if err != nil {
		frame.Users = nil
	} else if len(users) > savedContactsPageSize {
		frame.Users = users[:savedContactsPageSize]
		frame.Cursor = frame.Users[len(frame.Users)-1].ID
	}
	data, encodeErr := json.Marshal(frame)
	return encodeErr == nil && client.enqueue(data)
}

func (h *hub) savedContactsChanged(first, second string) {
	if h.savedContacts == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for client := range h.clients {
		if h.activeLocked(client) && client.direct && (client.user.ID == first || client.user.ID == second) {
			client.enqueue([]byte(`{"type":"saved-contacts-changed"}`))
		}
	}
}
