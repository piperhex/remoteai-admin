package lounge

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

type savedContactsFixture struct {
	owner, cursor string
	users         []account
	err           error
}

func (s *savedContactsFixture) Page(_ context.Context, owner, cursor string) ([]account, error) {
	s.owner, s.cursor = owner, cursor
	return s.users, s.err
}

func TestSavedContactsAuthenticatedPaginationAndErrors(t *testing.T) {
	h, url := fixture(t)
	store := &savedContactsFixture{}
	h.savedContacts = store
	owner := uuid.NewString()
	client := directConnection(t, h, url, owner)
	for index := range savedContactsPageSize + 1 {
		store.users = append(store.users, account{ID: uuid.NewString(), Account: fmt.Sprintf("user%d@test", index)})
	}
	write(t, client, incoming{Type: "sync-contacts", ID: "page-one", UserID: uuid.NewString()})
	page := read(t, client)
	if store.owner != owner || page["id"] != "page-one" || len(page["users"].([]interface{})) != savedContactsPageSize {
		t.Fatal("contact page was not scoped to the authenticated account", page)
	}
	cursor := store.users[savedContactsPageSize-1].ID
	if page["cursor"] != cursor {
		t.Fatal("missing page cursor", page)
	}
	store.users = []account{store.users[savedContactsPageSize]}
	write(t, client, incoming{Type: "sync-contacts", ID: "page-two", Cursor: cursor})
	page = read(t, client)
	if store.cursor != cursor || page["cursor"] != nil || len(page["users"].([]interface{})) != 1 {
		t.Fatal("incorrect final page", page)
	}
	store.err = errors.New("private database connection details")
	write(t, client, incoming{Type: "sync-contacts", ID: "failed"})
	page = read(t, client)
	if page["error"] != true || page["users"] != nil || len(page) != 4 {
		t.Fatal("storage failure leaked details or contacts", page)
	}
}

func TestSavedContactsChangeOnlyNotifiesTheOwnersDevices(t *testing.T) {
	h, url := fixture(t)
	h.savedContacts = &savedContactsFixture{}
	a, b, c := uuid.NewString(), uuid.NewString(), uuid.NewString()
	first := directConnection(t, h, url, a)
	second := directConnection(t, h, url, a)
	other := directConnection(t, h, url, b)
	stranger := directConnection(t, h, url, c)
	h.savedContactsChanged(a, b)
	for _, client := range []*websocket.Conn{first, second, other} {
		if frame := read(t, client); frame["type"] != "saved-contacts-changed" || len(frame) != 1 {
			t.Fatal("unexpected contact change notification", frame)
		}
	}
	expectPong(t, stranger)
}
