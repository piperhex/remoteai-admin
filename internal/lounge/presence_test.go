package lounge

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/codex-switch/admin-go/internal/platform"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

type memoryPolicies struct {
	mu     sync.Mutex
	values map[string]policy
}

func (p *memoryPolicies) Read(_ context.Context, id string) (policy, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.values[id], nil
}
func (p *memoryPolicies) Update(_ context.Context, request policyUpdate) (policy, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	value := p.values[request.Target]
	if request.Change.Moderator != nil {
		value.Moderator = *request.Change.Moderator
	}
	p.values[request.Target] = value
	return value, nil
}
func rawFrame(t *testing.T, client *websocket.Conn) map[string]interface{} {
	t.Helper()
	client.SetReadDeadline(time.Now().Add(2 * time.Second))
	var result map[string]interface{}
	if err := client.ReadJSON(&result); err != nil {
		t.Fatal(err)
	}
	return result
}
func expectPresence(t *testing.T, client *websocket.Conn, count int) {
	t.Helper()
	frame := rawFrame(t, client)
	if frame["type"] != "presence" || frame["onlineCount"] != float64(count) {
		t.Fatalf("unexpected presence: %v", frame)
	}
}

func TestPresenceOnlyReachesModeratorsAndCountsAccounts(t *testing.T) {
	h, url := fixture(t)
	h.policies = &memoryPolicies{values: map[string]policy{"manager": {Moderator: true}}}
	manager := connect(t, h, url, "manager")
	bob := connect(t, h, url, "bob")
	expectPresence(t, manager, 2)
	secondDevice := connect(t, h, url, "bob")
	write(t, bob, incoming{Type: "ping"})
	if rawFrame(t, bob)["type"] != "pong" {
		t.Fatal("ordinary user received a presence frame")
	}
	write(t, manager, incoming{Type: "ping"})
	if rawFrame(t, manager)["type"] != "pong" {
		t.Fatal("second device changed the account count")
	}
	secondDevice.Close()
	bob.Close()
	expectPresence(t, manager, 1)
	if _, err := h.policies.Update(context.Background(), policyUpdate{Target: "manager",
		Change: policyChange{Moderator: new(bool)}}); err != nil {
		t.Fatal(err)
	}
	h.notifyPolicy("manager", policy{})
	if frame := rawFrame(t, manager); frame["onlineCount"] != nil {
		t.Fatal("revocation exposed count")
	}
	connect(t, h, url, "third")
	write(t, manager, incoming{Type: "ping"})
	if rawFrame(t, manager)["type"] != "pong" {
		t.Fatal("revoked moderator received count")
	}
}

func TestPresenceReadyGrantAndAdminEndpointPermissions(t *testing.T) {
	h, url := fixture(t)
	h.policies = &memoryPolicies{values: map[string]policy{"manager": {Moderator: true}}}
	client := connect(t, h, url, "")
	write(t, client, incoming{Type: "authenticate", Ticket: credential(t, h, "manager", time.Now().Add(time.Minute))})
	if frame := rawFrame(t, client); frame["onlineCount"] != float64(1) {
		t.Fatal("ready count missing")
	}
	ordinary := connect(t, h, url, "ordinary")
	expectPresence(t, client, 2)
	h.notifyPolicy("ordinary", policy{Moderator: true})
	if frame := rawFrame(t, ordinary); frame["onlineCount"] != float64(2) {
		t.Fatal("grant count missing")
	}
	router := gin.New()
	deps := &platform.Dependencies{Authenticate: func(c *gin.Context) (*platform.Principal, error) {
		if c.GetHeader("Authorization") == "admin" {
			return &platform.Principal{ID: "admin", Permissions: []string{"admin.users.read"}}, nil
		}
		return &platform.Principal{ID: "ordinary"}, nil
	}}
	h.registerModeration(router, deps)
	for _, token := range []string{"ordinary", "admin"} {
		request := httptest.NewRequest("GET", "/admin/api/lounge/presence", nil)
		request.Header.Set("Authorization", token)
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		if token == "ordinary" {
			if response.Code != 403 {
				t.Fatal("count permission bypassed")
			}
			continue
		}
		var result map[string]int
		if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &result) != nil ||
			result["onlineCount"] != 2 || response.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("invalid admin presence response")
		}
	}
}

func TestPresenceExcludesExpiredAndUnauthenticatedSockets(t *testing.T) {
	h := newHub(nil)
	h.clients[&peer{user: account{ID: "expired"}, expires: time.Now().Add(-time.Minute), done: make(chan struct{})}] = true
	h.clients[&peer{user: account{ID: "unauthorized"}, expires: time.Now().Add(time.Minute), done: make(chan struct{})}] = false
	if h.onlineLocked() != 0 {
		t.Fatal("inactive clients counted")
	}
}
