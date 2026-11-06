package lounge

import (
	"encoding/json"
	"regexp"
	"sort"
)

const (
	maxClientVersions      = 16
	maxClientVersionLength = 64
)

var clientVersionPattern = regexp.MustCompile(`^\d+\.\d+\.\d+(?:-[\w.-]+)?(?:\+[\w.-]+)?$`)

func validClientVersion(value string) string {
	if len(value) > maxClientVersionLength || !clientVersionPattern.MatchString(value) {
		return ""
	}
	return value
}

type clientVersions struct {
	ID       string   `json:"id"`
	Versions []string `json:"clientVersions"`
}

// Connection-only metadata is never stored as a profile or used for authorization.
// Callers own h.mu, just as for profile and presence subscriptions.
func (h *hub) clientVersionsLocked(user string) []string {
	unique := make(map[string]bool)
	for client := range h.clientsByUser[user] {
		if !h.activeLocked(client) {
			continue
		}
		for _, version := range client.user.ClientVersions {
			if validClientVersion(version) != "" {
				unique[version] = true
			}
		}
	}
	versions := make([]string, 0, len(unique))
	for version := range unique {
		versions = append(versions, version)
	}
	sort.Strings(versions)
	if len(versions) > maxClientVersions {
		versions = versions[:maxClientVersions]
	}
	return versions
}

func (h *hub) sendClientVersionsLocked(client *peer, ids []string) {
	if !client.versionWatch || !h.activeLocked(client) {
		return
	}
	users := make([]clientVersions, 0, len(ids))
	for _, id := range ids {
		versions := []string{}
		if client.canContact(id) {
			versions = h.clientVersionsLocked(id)
		}
		users = append(users, clientVersions{ID: id, Versions: versions})
	}
	data, err := json.Marshal(struct {
		Type  string           `json:"type"`
		Users []clientVersions `json:"users"`
	}{"client-versions", users})
	if err == nil {
		client.enqueue(data)
	}
}

func (h *hub) publishClientVersionsLocked(user string) {
	if user == "" {
		return
	}
	for subscriber := range h.clients {
		if subscriber.profileWatch[user] {
			h.sendClientVersionsLocked(subscriber, []string{user})
		}
	}
}
