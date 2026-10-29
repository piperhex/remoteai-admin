package lounge

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/google/uuid"
)

type nameUpdate struct {
	Type     string            `json:"type"`
	Profiles []nicknameProfile `json:"profiles,omitempty"`
	Remarks  []contactRemark   `json:"remarks,omitempty"`
}

func sendNames(client *peer, value nameUpdate) {
	value.Type = "names"
	data, err := json.Marshal(value)
	if err == nil {
		client.enqueue(data)
	}
}

func (h *hub) readNickname(user *account) error {
	if h.names == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	profiles, err := h.names.Nicknames(ctx, []string{user.ID})
	if len(profiles) > 0 {
		user.Nickname = profiles[0].Nickname
	}
	return err
}

// Called while settingsMu is held, so a watch snapshot cannot overtake a newer saved value.
func (h *hub) watchedNames(client *peer, ids []string) (nameUpdate, error) {
	if h.names == nil {
		return nameUpdate{}, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	profiles, err := h.names.Nicknames(ctx, ids)
	if err != nil {
		return nameUpdate{}, err
	}
	remarks, err := h.names.Remarks(ctx, client.user.ID, ids)
	return nameUpdate{Profiles: profiles, Remarks: remarks}, err
}

func (h *hub) saveNickname(client *peer, input incoming) {
	value := strings.TrimSpace(input.Nickname)
	if h.names == nil || !validName(input.Nickname, maxNicknameLength) {
		h.reject(client, input.ID, "client.lounge.nicknameSaveFailed")
		return
	}
	h.settingsMu.Lock()
	defer h.settingsMu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	profile := nicknameProfile{ID: client.user.ID, Nickname: value}
	if h.names.SaveNickname(ctx, profile) != nil {
		h.reject(client, input.ID, "client.lounge.nicknameSaveFailed")
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for subscriber := range h.clients {
		if !h.activeLocked(subscriber) {
			continue
		}
		own := subscriber.user.ID == profile.ID
		if own {
			subscriber.user.Nickname = value
		}
		if own || subscriber.profileWatch[profile.ID] {
			sendNames(subscriber, nameUpdate{Profiles: []nicknameProfile{profile}})
		}
	}
	acknowledgeName(client, input.ID)
}

func (h *hub) saveRemark(client *peer, input incoming) {
	id, err := uuid.Parse(input.UserID)
	if h.names == nil || err != nil || id.String() != input.UserID || input.UserID == client.user.ID ||
		!validName(input.Remark, maxRemarkLength) {
		h.reject(client, input.ID, "client.lounge.remarkSaveFailed")
		return
	}
	h.settingsMu.Lock()
	defer h.settingsMu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	remark := contactRemark{ID: input.UserID, Remark: strings.TrimSpace(input.Remark)}
	if h.names.SaveRemark(ctx, client.user.ID, remark) != nil {
		h.reject(client, input.ID, "client.lounge.remarkSaveFailed")
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	// Never publish a private label to the contact or their profile subscribers.
	for subscriber := range h.clients {
		if h.activeLocked(subscriber) && subscriber.user.ID == client.user.ID {
			sendNames(subscriber, nameUpdate{Remarks: []contactRemark{remark}})
		}
	}
	acknowledgeName(client, input.ID)
}

func acknowledgeName(client *peer, id string) {
	data, err := json.Marshal(map[string]string{"type": "updated", "id": id})
	if err == nil {
		client.enqueue(data)
	}
}
