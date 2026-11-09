package devices

import (
	"context"
	"encoding/json"
	"log/slog"
	"slices"
	"time"

	"github.com/piperhex/remoteai-admin/internal/platform"
	"github.com/redis/go-redis/v9"
)

const accountAuthSyncTopic = "remoteai:account-auth-updated"

type accountAuthUpdate struct {
	Owner string   `json:"owner"`
	IDs   []string `json:"ids"`
}

func hasAccountAuthSync(message platform.JSON) bool {
	return slices.Contains(normalizeCapabilities(message["capabilities"]), "account-auth-sync")
}

// Send identifiers only; each PC must download credentials through its authenticated sync API.
// Older clients do not understand this frame and must never receive it.
func (g *ControlGateway) deliverAccountAuthUpdate(update accountAuthUpdate) {
	g.mu.Lock()
	clients := []*peer{}
	for client, session := range g.sessions {
		if session.owner == update.Owner && session.kind == "device" && session.accountAuthSync &&
			g.sockets[session.owner+":"+session.device] == client && !client.closed.Load() {
			clients = append(clients, client)
		}
	}
	g.mu.Unlock()
	for _, client := range clients {
		client.send(platform.JSON{"type": "account-auth-updated", "accountIds": update.IDs}, nil)
	}
}

func (g *ControlGateway) startAccountAuthSync() func() {
	deps := g.service.deps
	ctx, cancel := context.WithCancel(context.Background())
	if deps.Redis != nil {
		sub := deps.Redis.Subscribe(ctx, accountAuthSyncTopic)
		ready, stop := context.WithTimeout(ctx, 3*time.Second)
		if _, err := sub.Receive(ready); err != nil {
			slog.Warn("account credential subscription is reconnecting", "error", err)
		}
		stop()
		go g.listenAccountAuthUpdates(ctx, sub)
	}
	deps.AccountAuthChanged = func(owner string, ids []string) {
		g.publishAccountAuthUpdate(ctx, accountAuthUpdate{owner, ids})
	}
	return cancel
}

func (g *ControlGateway) publishAccountAuthUpdate(ctx context.Context, update accountAuthUpdate) {
	broker := g.service.deps.Redis
	if broker == nil {
		g.deliverAccountAuthUpdate(update)
		return
	}
	payload, err := json.Marshal(update)
	if err == nil {
		publishCtx, stop := context.WithTimeout(ctx, 3*time.Second)
		defer stop()
		err = broker.Publish(publishCtx, accountAuthSyncTopic, payload).Err()
	}
	if err != nil {
		slog.Warn("account credential notification failed", "error", err)
		g.deliverAccountAuthUpdate(update)
	}
}

func (g *ControlGateway) listenAccountAuthUpdates(ctx context.Context, sub *redis.PubSub) {
	defer sub.Close()
	channel := sub.Channel()
	for {
		select {
		case <-ctx.Done():
			return
		case message, ok := <-channel:
			if !ok {
				return
			}
			var update accountAuthUpdate
			if len(message.Payload) <= 256*1024 && json.Unmarshal([]byte(message.Payload), &update) == nil &&
				update.Owner != "" {
				g.deliverAccountAuthUpdate(update)
			}
		}
	}
}
