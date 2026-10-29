package lounge

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

type lookupFixture struct{}

func (lookupFixture) FindAccounts(_ context.Context, query string) ([]account, error) {
	switch query {
	case "bob", "bob@example.test":
		return []account{{ID: "bob", Account: "bob@example.test"}}, nil
	case "shared":
		return []account{{ID: "one"}, {ID: "two"}}, nil
	case "alice":
		return []account{{ID: "alice", Account: "alice@example.test"}}, nil
	case "failure":
		return nil, errors.New("private database details")
	}
	return nil, nil
}

func TestAccountLookupRepliesOnlyToAuthenticatedRequester(t *testing.T) {
	h, url := fixture(t)
	h.accountLookup = lookupFixture{}
	alice := directConnection(t, h, url, "alice")
	other := directConnection(t, h, url, "observer")
	for _, query := range []string{"bob", " bob@example.test "} {
		write(t, alice, incoming{Type: "lookup-account", ID: "lookup", Query: query})
		frame := read(t, alice)
		if frame["type"] != "account-found" || frame["id"] != "lookup" ||
			frame["user"].(map[string]interface{})["account"] != "bob@example.test" {
			t.Fatal(frame)
		}
	}
	expectPong(t, other)
	guest := connect(t, h, url, "")
	write(t, guest, incoming{Type: "lookup-account", ID: "guest", Query: "bob"})
	guest.SetReadDeadline(time.Now().Add(time.Second))
	if _, _, err := guest.ReadMessage(); err == nil {
		t.Fatal("unauthenticated lookup returned data")
	}
}

func TestAccountLookupRejectsInvalidAmbiguousSelfAndUnavailable(t *testing.T) {
	h, url := fixture(t)
	h.accountLookup = lookupFixture{}
	alice := directConnection(t, h, url, "alice")
	for query, suffix := range map[string]string{
		"": "Invalid", "bob@": "Invalid", "@example.test": "Invalid", "bob@a@b": "Invalid",
		"bo b": "Invalid", "bob\x00": "Invalid", strings.Repeat("a", 321): "Invalid",
		"bo": "NotFound", "missing@example.test": "NotFound", "shared": "Ambiguous",
		"alice": "Self", "failure": "Failed",
	} {
		write(t, alice, incoming{Type: "lookup-account", ID: "check", Query: query})
		frame := read(t, alice)
		if frame["type"] != "rejected" || frame["code"] != "client.lounge.accountLookup"+suffix {
			t.Fatalf("query %q: %v", query, frame)
		}
	}
	expectPong(t, alice)
}

func TestAccountLookupMatchesExactEmailOrLocalPartInPostgres(t *testing.T) {
	db := policyDB(t)
	if err := db.Exec("ALTER TABLE users ADD COLUMN email text NOT NULL DEFAULT ''").Error; err != nil {
		t.Fatal(err)
	}
	for _, email := range []string{"Bob@example.test", "bob@another.test", "bobby@example.test",
		"unique@example.test", "a_b@example.test", "disabled@example.test"} {
		if err := db.Exec("INSERT INTO users (id, email, disabled) VALUES (?, ?, ?)",
			uuid.NewString(), email, strings.HasPrefix(email, "disabled")).Error; err != nil {
			t.Fatal(err)
		}
	}
	repository := postgresRelationships{db: db}
	for query, count := range map[string]int{"BOB": 2, "bob@EXAMPLE.test": 1, "bo": 0,
		"unique": 1, "bobby": 1, "b%": 0, "%": 0, "a_b": 1, "a_b@example.test": 1,
		"disabled": 0, "disabled@example.test": 0, "' OR true --": 0} {
		users, err := repository.FindAccounts(context.Background(), query)
		if err != nil || len(users) != count {
			t.Fatalf("query %q: %v, %v", query, users, err)
		}
	}
}

func TestAccountLookupFindsOfflineUserAndStartsPrivateDelivery(t *testing.T) {
	db := policyDB(t)
	if err := db.Exec("ALTER TABLE users ADD COLUMN email text NOT NULL DEFAULT ''").Error; err != nil {
		t.Fatal(err)
	}
	self, target := uuid.NewString(), uuid.NewString()
	if err := db.Exec("INSERT INTO users (id, email) VALUES (?, ?)", target, "unseen@example.test").Error; err != nil {
		t.Fatal(err)
	}
	h, url := fixture(t)
	h.accountLookup = postgresRelationships{db: db}
	h.relationships = &memoryRelationships{blocks: make(map[string]map[string]bool)}
	alice := directConnection(t, h, url, self)
	observer := directConnection(t, h, url, uuid.NewString())
	write(t, alice, incoming{Type: "lookup-account", ID: "offline", Query: "unseen"})
	frame := read(t, alice)
	if frame["type"] != "account-found" || frame["user"].(map[string]interface{})["id"] != target {
		t.Fatal(frame)
	}
	bob := directConnection(t, h, url, target)
	write(t, alice, incoming{Type: "message", ID: "new-private", Text: "hello", To: target})
	for _, frame := range []map[string]interface{}{read(t, alice), read(t, bob)} {
		if frame["id"] != "new-private" || frame["recipient"].(map[string]interface{})["id"] != target {
			t.Fatal(frame)
		}
	}
	expectPong(t, observer)
}
