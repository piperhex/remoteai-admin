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
	ID       string       `json:"id"`
	Versions []string     `json:"clientVersions"`
	Clients  []clientInfo `json:"clients"`
}

type clientPlatform string

const (
	desktopClient clientPlatform = "desktop"
	mobileClient  clientPlatform = "mobile"
)

type clientInfo struct {
	Version  string         `json:"version"`
	Platform clientPlatform `json:"platform,omitempty"`
}

func validClientPlatform(value string) clientPlatform {
	if value == string(desktopClient) || value == string(mobileClient) {
		return clientPlatform(value)
	}
	return ""
}

func peerClients(client *peer) []clientInfo {
	if len(client.user.Clients) > 0 {
		return client.user.Clients
	}
	clients := make([]clientInfo, 0, len(client.user.ClientVersions))
	for _, version := range client.user.ClientVersions {
		clients = append(clients, clientInfo{Version: version})
	}
	return clients
}

// Connection-only metadata is never stored as a profile or used for authorization.
// Callers own h.mu, just as for profile and presence subscriptions.
func (h *hub) clientDetailsLocked(user string) []clientInfo {
	unique := make(map[string]clientInfo)
	for client := range h.clientsByUser[user] {
		if !h.activeLocked(client) {
			continue
		}
		for _, info := range peerClients(client) {
			if validClientVersion(info.Version) != "" {
				info.Platform = validClientPlatform(string(info.Platform))
				unique[string(info.Platform)+":"+info.Version] = info
			}
		}
	}
	clients := make([]clientInfo, 0, len(unique))
	for _, info := range unique {
		clients = append(clients, info)
	}
	sort.Slice(clients, func(a, b int) bool {
		if clients[a].Platform != clients[b].Platform {
			return platformOrder(clients[a].Platform) < platformOrder(clients[b].Platform)
		}
		return clients[a].Version < clients[b].Version
	})
	if len(clients) > maxClientVersions {
		clients = clients[:maxClientVersions]
	}
	return clients
}

func platformOrder(value clientPlatform) int {
	switch value {
	case desktopClient:
		return 0
	case mobileClient:
		return 1
	default:
		return 2
	}
}

func versionsForClients(clients []clientInfo) []string {
	unique := make(map[string]bool)
	for _, client := range clients {
		unique[client.Version] = true
	}
	versions := make([]string, 0, len(unique))
	for version := range unique {
		versions = append(versions, version)
	}
	sort.Strings(versions)
	return versions
}

func (h *hub) sendClientVersionsLocked(client *peer, ids []string) {
	if !client.versionWatch || !h.activeLocked(client) {
		return
	}
	users := make([]clientVersions, 0, len(ids))
	for _, id := range ids {
		clients := []clientInfo{}
		if client.canContact(id) {
			clients = h.clientDetailsLocked(id)
		}
		users = append(users, clientVersions{ID: id, Versions: versionsForClients(clients), Clients: clients})
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
