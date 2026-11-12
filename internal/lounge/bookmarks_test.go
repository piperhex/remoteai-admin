package lounge

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/piperhex/remoteai-admin/internal/browserbookmarks"
)

type memoryBookmarks struct {
	values map[string]browserbookmarks.Document
	fail   bool
}

func (m *memoryBookmarks) Exchange(_ context.Context, owner string, edit *browserbookmarks.Document) (browserbookmarks.Document, bool, error) {
	doc, ok := m.values[owner]
	if !ok {
		doc.Entries = []browserbookmarks.Entry{}
	}
	if m.fail {
		return doc, false, errors.New("private storage error")
	}
	if edit == nil {
		return doc, false, nil
	}
	if err := browserbookmarks.Validate(*edit); err != nil {
		return doc, false, err
	}
	if edit.Revision != doc.Revision {
		return doc, false, nil
	}
	doc = browserbookmarks.Document{Revision: doc.Revision + 1, Entries: edit.Entries}
	m.values[owner] = doc
	return doc, true, nil
}

func bookmarkDocument() *browserbookmarks.Document {
	return &browserbookmarks.Document{Entries: []browserbookmarks.Entry{
		{ID: "guide", Kind: "link", Title: "Guide", URL: "https://example.test/guide"},
	}}
}

func TestBookmarksPushConflictIsolationAndReconnect(t *testing.T) {
	h, url := fixture(t)
	h.bookmarks = &memoryBookmarks{values: map[string]browserbookmarks.Document{}}
	a, b := uuid.NewString(), uuid.NewString()
	first, second, other := connect(t, h, url, a), connect(t, h, url, a), connect(t, h, url, b)
	write(t, first, incoming{Type: "bookmarks-save", ID: "write", UserID: b, Bookmarks: bookmarkDocument()})
	for _, client := range []*websocket.Conn{first, second} {
		frame := read(t, client)
		if frame["type"] != "bookmarks" || frame["accepted"] != true {
			t.Fatal("push missing", frame)
		}
	}
	write(t, second, incoming{Type: "bookmarks-save", ID: "stale", Bookmarks: bookmarkDocument()})
	if frame := read(t, second); frame["accepted"] != false {
		t.Fatal("stale edit replaced document", frame)
	}
	write(t, other, incoming{Type: "bookmarks-read", ID: "read"})
	doc := read(t, other)["document"].(map[string]interface{})
	if len(doc["entries"].([]interface{})) != 0 {
		t.Fatal("bookmarks leaked to another account")
	}
	fresh := connect(t, h, url, a)
	write(t, fresh, incoming{Type: "bookmarks-read", ID: "restore"})
	doc = read(t, fresh)["document"].(map[string]interface{})
	if doc["revision"] != float64(1) {
		t.Fatal("reconnect lost bookmarks", doc)
	}
	write(t, first, incoming{Type: "bookmarks-save", ID: "delete", Bookmarks: &browserbookmarks.Document{
		Revision: 1, Entries: []browserbookmarks.Entry{},
	}})
	if read(t, first)["accepted"] != true || read(t, second)["type"] != "bookmarks" {
		t.Fatal("delete not pushed")
	}
}

func TestBookmarksRejectInvalidAndStorageErrors(t *testing.T) {
	h, url := fixture(t)
	store := &memoryBookmarks{values: map[string]browserbookmarks.Document{}}
	h.bookmarks = store
	client := connect(t, h, url, uuid.NewString())
	doc := bookmarkDocument()
	doc.Entries[0].URL = "javascript:alert(1)"
	write(t, client, incoming{Type: "bookmarks-save", ID: "invalid", Bookmarks: doc})
	if frame := read(t, client); frame["type"] != "rejected" {
		t.Fatal(frame)
	}
	store.fail = true
	write(t, client, incoming{Type: "bookmarks-read", ID: "failed"})
	if frame := read(t, client); frame["code"] != "market.bookmarkSyncFailed" {
		t.Fatal("unsafe error", frame)
	}
}

func TestBookmarksPostgresPersistence(t *testing.T) {
	db := policyDB(t)
	schema, err := os.ReadFile("../migrations/030_browser_bookmarks.sql")
	if err != nil {
		t.Fatal(err)
	}
	upgrade, err := os.ReadFile("../../sql/20261112-browser-bookmarks.sql")
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(string(upgrade), "BEGIN;", ""), "COMMIT;", "")) != strings.TrimSpace(string(schema)) {
		t.Fatal("bootstrap and upgrade differ")
	}
	for range 2 {
		if err := db.Exec(string(schema)).Error; err != nil {
			t.Fatal(err)
		}
	}
	owner := policyUser(t, db)
	store := browserbookmarks.Postgres{DB: db}
	doc, accepted, err := store.Exchange(context.Background(), owner, bookmarkDocument())
	if err != nil || !accepted || doc.Revision != 1 {
		t.Fatal(doc, accepted, err)
	}
	fresh := browserbookmarks.Postgres{DB: db}
	doc, accepted, err = fresh.Exchange(context.Background(), owner, bookmarkDocument())
	if err != nil || accepted || len(doc.Entries) != 1 {
		t.Fatal(doc, accepted, err)
	}
}
