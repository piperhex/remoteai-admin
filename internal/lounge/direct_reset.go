package lounge

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Generation changes invalidate queued messages from an earlier conversation, including offline devices.
func (p postgresDirectPolicy) Reset(ctx context.Context, pair directPermission) (int64, error) {
	var generation int64
	err := p.db.Session(&gorm.Session{Logger: logger.Default.LogMode(logger.Silent)}).WithContext(ctx).
		Raw(`INSERT INTO lounge_direct_permissions (first_user, second_user, generation, fresh)
			VALUES (?, ?, 1, true) ON CONFLICT (first_user, second_user) DO UPDATE
			SET generation = lounge_direct_permissions.generation + 1, fresh = true,
			initiator = NULL, fingerprint = NULL RETURNING generation`, pair.FirstUser, pair.SecondUser).Scan(&generation).Error
	return generation, err
}

func (h *hub) deleteDirect(client *peer, input incoming) {
	target, err := uuid.Parse(input.UserID)
	if err != nil || target.String() == client.user.ID || h.directPolicy == nil || h.relationships == nil {
		h.reject(client, input.ID, "client.lounge.deleteFailed")
		return
	}
	h.mu.Lock()
	active := h.activeLocked(client) && client.direct
	h.mu.Unlock()
	if !active {
		h.reject(client, input.ID, "client.lounge.deleteFailed")
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := h.relationships.Account(ctx, target.String()); err != nil {
		h.reject(client, input.ID, "client.lounge.deleteFailed")
		return
	}
	pair := directPermission{FirstUser: client.user.ID, SecondUser: target.String()}
	if pair.FirstUser > pair.SecondUser {
		pair.FirstUser, pair.SecondUser = pair.SecondUser, pair.FirstUser
	}
	generation, err := h.directPolicy.Reset(ctx, pair)
	if err != nil {
		h.reject(client, input.ID, "client.lounge.deleteFailed")
		return
	}
	h.sendDirectPermissions(client.user.ID, []string{target.String()})
	h.sendDirectPermissions(target.String(), []string{client.user.ID})
	data, err := json.Marshal(map[string]interface{}{"type": "updated", "id": input.ID,
		"userId": target.String(), "generation": generation})
	if err == nil {
		client.enqueue(data)
	}
}
