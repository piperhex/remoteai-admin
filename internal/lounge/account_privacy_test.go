package lounge

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

func TestLoungeTicketContainsOnlyAccountPrefix(t *testing.T) {
	h, socketURL := fixture(t)
	url := strings.Replace(strings.TrimSuffix(socketURL, "/socket"), "ws", "http", 1) + "/ticket"
	request, err := http.NewRequest(http.MethodPost, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer account-test-token")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var body struct{ Ticket string }
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	var value claims
	_, err = jwt.ParseWithClaims(body.Ticket, &value, func(*jwt.Token) (interface{}, error) { return h.key, nil })
	if err != nil || value.Account != "alice" || value.Subject != "one" {
		t.Fatalf("ticket exposed address or changed identity: %+v, %v", value, err)
	}
}

func TestLoungeAccountLabelsArePrivateAcrossSocketFrames(t *testing.T) {
	h, url := fixture(t)
	a, b := uuid.NewString(), uuid.NewString()
	h.relationships = &memoryRelationships{blocks: map[string]map[string]bool{a: {b: true}}}
	h.savedContacts = &savedContactsFixture{users: []account{{ID: b, Account: b + "@example.test"}}}
	h.accountLookup = lookupFixture{}
	alice := connect(t, h, url, "")
	write(t, alice, incoming{Type: "authenticate", Direct: true,
		Ticket: credential(t, h, a, time.Now().Add(time.Minute))})
	ready := read(t, alice)
	assertPublicAccount(t, ready["user"], a)
	assertPublicAccount(t, ready["blocked"].([]interface{})[0], b)
	blocked := false
	write(t, alice, incoming{Type: "block", ID: "unblock", UserID: b, Blocked: &blocked})
	if read(t, alice)["type"] != "privacy" || read(t, alice)["type"] != "updated" {
		t.Fatal("missing unblock acknowledgement")
	}
	bob := directConnection(t, h, url, b)
	for _, target := range []string{"", b} {
		// Send raw legacy JSON: a reply's custom marshaler must not sanitize the test input.
		write(t, alice, map[string]interface{}{"type": "message", "id": uuid.NewString(), "to": target,
			"text": "user-written@example.test", "reply": map[string]interface{}{
				"id": "old", "senderId": b, "account": b + "@example.test", "text": "quoted@example.test"}})
		for _, client := range []*websocket.Conn{alice, bob} {
			frame := read(t, client)
			assertPublicAccount(t, frame["sender"], a)
			if target != "" {
				assertPublicAccount(t, frame["recipient"], b)
			}
			quote := frame["reply"].(map[string]interface{})
			if quote["account"] != b || quote["text"] != "quoted@example.test" || frame["text"] != "user-written@example.test" {
				t.Fatal("reply leaked account or modified user content", frame)
			}
		}
	}
	assertLookupAndSavedLabels(t, alice, b)
}

func assertLookupAndSavedLabels(t *testing.T, client *websocket.Conn, saved string) {
	t.Helper()
	write(t, client, incoming{Type: "lookup-account", ID: "lookup", Query: "bob@example.test"})
	assertPublicAccount(t, read(t, client)["user"], "bob")
	write(t, client, incoming{Type: "sync-contacts", ID: "contacts"})
	assertPublicAccount(t, read(t, client)["users"].([]interface{})[0], saved)
	blocked := true
	write(t, client, incoming{Type: "block", ID: "block", UserID: saved, Blocked: &blocked})
	assertPublicAccount(t, read(t, client)["blocked"].([]interface{})[0], saved)
}

func assertPublicAccount(t *testing.T, value interface{}, expected string) {
	t.Helper()
	user, ok := value.(map[string]interface{})
	if !ok || user["account"] != expected || user["id"] != expected {
		t.Fatalf("unexpected public account: %#v", value)
	}
}

func TestLoungePublicAccountPreservesProfileAndDistinctIdentity(t *testing.T) {
	users := []account{{ID: "one", Account: "same@first.test", Nickname: "nickname@chosen.test", Moderator: true},
		{ID: "two", Account: "same@second.test", Avatar: "avatar"}}
	encoded, err := json.Marshal(users)
	if err != nil {
		t.Fatal(err)
	}
	var decoded []account
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded[0].Account != "same" || decoded[1].Account != "same" || decoded[0].ID == decoded[1].ID ||
		decoded[0].Nickname != users[0].Nickname || !decoded[0].Moderator || decoded[1].Avatar != "avatar" ||
		users[0].Account != "same@first.test" {
		t.Fatal("account privacy changed identity, profile or original value", string(encoded))
	}
}
