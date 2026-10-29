package lounge

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type notificationRepository interface {
	Muted(context.Context, string) ([]string, error)
	Save(context.Context, string, string, bool) error
}

type postgresNotifications struct{ db *gorm.DB }

func (p postgresNotifications) session(ctx context.Context) *gorm.DB {
	return p.db.Session(&gorm.Session{Logger: logger.Default.LogMode(logger.Silent)}).WithContext(ctx)
}

func (p postgresNotifications) Muted(ctx context.Context, owner string) ([]string, error) {
	conversations := []string{}
	err := p.session(ctx).Table("lounge_notification_settings").Where("owner_id = ?", owner).
		Order("conversation_id").Pluck("conversation_id", &conversations).Error
	return conversations, err
}

func (p postgresNotifications) Save(ctx context.Context, owner, conversation string, muted bool) error {
	if !muted {
		return p.session(ctx).Exec(`DELETE FROM lounge_notification_settings
			WHERE owner_id = ? AND conversation_id = ?`, owner, conversation).Error
	}
	return p.session(ctx).Exec(`INSERT INTO lounge_notification_settings (owner_id, conversation_id)
		VALUES (?, ?) ON CONFLICT (owner_id, conversation_id) DO NOTHING`, owner, conversation).Error
}

// settingsMu serializes the initial snapshot with writes and their broadcasts.
func (h *hub) mutedConversations(owner string) ([]string, error) {
	if h.notifications == nil {
		return []string{}, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return h.notifications.Muted(ctx, owner)
}

func validNotificationConversation(owner string, conversation *string) bool {
	if conversation == nil || *conversation == owner {
		return false
	}
	if *conversation == "" {
		return true
	}
	id, err := uuid.Parse(*conversation)
	return err == nil && id.String() == *conversation
}

func (h *hub) saveNotificationSetting(client *peer, input incoming) {
	if h.notifications == nil || input.Muted == nil ||
		!validNotificationConversation(client.user.ID, input.Conversation) {
		h.reject(client, input.ID, "client.lounge.notificationSaveFailed")
		return
	}
	h.settingsMu.Lock()
	defer h.settingsMu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if h.notifications.Save(ctx, client.user.ID, *input.Conversation, *input.Muted) != nil {
		h.reject(client, input.ID, "client.lounge.notificationSaveFailed")
		return
	}
	data, err := json.Marshal(struct {
		Type         string `json:"type"`
		Conversation string `json:"conversation"`
		Muted        bool   `json:"muted"`
	}{"notification-settings", *input.Conversation, *input.Muted})
	if err != nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	// Notification preferences belong only to the authenticated owner, never to other participants.
	for subscriber := range h.clients {
		if h.activeLocked(subscriber) && subscriber.user.ID == client.user.ID {
			subscriber.enqueue(data)
		}
	}
	acknowledgeName(client, input.ID)
}
