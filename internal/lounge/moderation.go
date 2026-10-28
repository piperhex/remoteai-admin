package lounge

import (
	"context"
	"encoding/json"
	"time"

	"github.com/codex-switch/admin-go/internal/platform"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

func (h *hub) registerModeration(router *gin.Engine, deps *platform.Dependencies) {
	group := router.Group("/admin/api/lounge", deps.RequireAuth())
	group.GET("/users/:id", deps.RequirePermissions("admin.users.read"), func(c *gin.Context) {
		if _, err := uuid.Parse(c.Param("id")); err != nil {
			platform.Fail(c, 400, "client.lounge.userUnavailable")
			return
		}
		value, err := h.userPolicy(c.Param("id"))
		platform.Respond(c, value, err)
	})
	group.PATCH("/users/:id", deps.RequirePermissions("admin.users.manage"), func(c *gin.Context) {
		var change policyChange
		if c.ShouldBindJSON(&change) != nil {
			platform.Fail(c, 400, "client.lounge.invalidDuration")
			return
		}
		request := policyUpdate{Actor: platform.User(c).ID, Target: c.Param("id"), Admin: true, Change: change}
		value, err := h.changePolicy(c.Request.Context(), request)
		platform.Respond(c, value, err)
	})
}

func (h *hub) changePolicy(ctx context.Context, request policyUpdate) (policy, error) {
	if _, err := uuid.Parse(request.Target); err != nil {
		return policy{}, platform.NewError(400, "client.lounge.userUnavailable")
	}
	if h.policies == nil {
		return policy{}, platform.NewError(503, "client.lounge.unavailable")
	}
	value, err := h.policies.Update(ctx, request)
	if err != nil {
		if _, known := err.(*platform.HTTPError); known {
			return policy{}, err
		}
		return policy{}, platform.NewError(503, "client.lounge.unavailable")
	}
	h.notifyPolicy(request.Target, value)
	return value, nil
}

func (h *hub) notifyPolicy(user string, value policy) {
	data, err := json.Marshal(struct {
		Type   string `json:"type"`
		Policy policy `json:"policy"`
	}{"policy", value})
	if err != nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for client := range h.clients {
		if client.user.ID == user {
			client.enqueue(data)
		}
	}
}

func (h *hub) moderate(client *peer, input incoming) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := h.changePolicy(ctx, policyUpdate{Actor: client.user.ID, Target: input.UserID,
		Change: policyChange{Minutes: input.Minutes}})
	if err != nil {
		h.reject(client, input.ID, "client.lounge.moderationFailed")
		return
	}
	data, err := json.Marshal(map[string]string{"type": "moderated", "id": input.ID})
	if err == nil {
		client.enqueue(data)
	}
}

func (h *hub) maySend(client *peer, id string) bool {
	value, err := h.userPolicy(client.user.ID)
	if err != nil {
		h.reject(client, id, "client.lounge.unavailable")
		return false
	}
	if value.MutedUntil != nil && value.MutedUntil.After(time.Now()) {
		h.notifyPolicy(client.user.ID, value)
		h.reject(client, id, "client.lounge.muted")
		return false
	}
	return true
}

func (h *hub) reject(client *peer, id, code string) {
	data, err := json.Marshal(map[string]string{"type": "rejected", "id": id, "code": code})
	if err == nil {
		client.enqueue(data)
	}
}
