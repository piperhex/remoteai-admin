package lounge

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/piperhex/remoteai-admin/internal/browserdata"
)

type memoryBrowserData struct {
	values map[string]browserdata.Document
	fail   bool
}

func (m *memoryBrowserData) Exchange(_ context.Context, owner string, edit *browserdata.Document) (browserdata.Document, bool, error) {
	doc, ok := m.values[owner]
	if !ok {
		doc.Entries = []browserdata.Entry{}
	}
	if m.fail {
		return doc, false, errors.New("private storage error")
	}
	if edit == nil {
		return doc, false, nil
	}
	if err := browserdata.Validate(*edit); err != nil {
		return doc, false, err
	}
	if edit.Revision != doc.Revision {
		return doc, false, nil
	}
	doc = browserdata.Document{Revision: doc.Revision + 1, Entries: edit.Entries}
	m.values[owner] = doc
	return doc, true, nil
}

func browserDataDocument() *browserdata.Document {
	return &browserdata.Document{Entries: []browserdata.Entry{
		{ID: "guide", Kind: "visit", At: 1234, Title: "Guide", URL: "https://example.test/guide"},
	}}
}

func TestBrowserDataPushConflictIsolationAndReconnect(t *testing.T) {
	h, url := fixture(t)
	h.browserData = &memoryBrowserData{values: map[string]browserdata.Document{}}
	a, b := uuid.NewString(), uuid.NewString()
	first, second, other := connect(t, h, url, a), connect(t, h, url, a), connect(t, h, url, b)
	write(t, first, incoming{Type: "browser-data-save", ID: "write", UserID: b, BrowserData: browserDataDocument()})
	for _, client := range []*websocket.Conn{first, second} {
		frame := read(t, client)
		if frame["type"] != "browser-data" || frame["accepted"] != true {
			t.Fatal("push missing", frame)
		}
	}
	write(t, second, incoming{Type: "browser-data-save", ID: "stale", BrowserData: browserDataDocument()})
	if frame := read(t, second); frame["accepted"] != false {
		t.Fatal("stale edit replaced document", frame)
	}
	write(t, other, incoming{Type: "browser-data-read", ID: "read"})
	doc := read(t, other)["document"].(map[string]interface{})
	if len(doc["entries"].([]interface{})) != 0 {
		t.Fatal("bookmarks leaked to another account")
	}
	fresh := connect(t, h, url, a)
	write(t, fresh, incoming{Type: "browser-data-read", ID: "restore"})
	doc = read(t, fresh)["document"].(map[string]interface{})
	if doc["revision"] != float64(1) {
		t.Fatal("reconnect lost bookmarks", doc)
	}
	write(t, first, incoming{Type: "browser-data-save", ID: "delete", BrowserData: &browserdata.Document{
		Revision: 1, Entries: []browserdata.Entry{},
	}})
	if read(t, first)["accepted"] != true || read(t, second)["type"] != "browser-data" {
		t.Fatal("delete not pushed")
	}
}

func TestBrowserDataRejectInvalidAndStorageErrors(t *testing.T) {
	h, url := fixture(t)
	store := &memoryBrowserData{values: map[string]browserdata.Document{}}
	h.browserData = store
	client := connect(t, h, url, uuid.NewString())
	doc := browserDataDocument()
	doc.Entries[0].URL = "javascript:alert(1)"
	write(t, client, incoming{Type: "browser-data-save", ID: "invalid", BrowserData: doc})
	if frame := read(t, client); frame["type"] != "rejected" {
		t.Fatal(frame)
	}
	store.fail = true
	write(t, client, incoming{Type: "browser-data-read", ID: "failed"})
	if frame := read(t, client); frame["code"] != "market.browserDataOffline" {
		t.Fatal("unsafe error", frame)
	}
}

func TestBrowserDataPostgresPersistence(t *testing.T) {
	db := policyDB(t)
	schema, err := os.ReadFile("../migrations/031_browser_data.sql")
	if err != nil {
		t.Fatal(err)
	}
	upgrade, err := os.ReadFile("../../sql/20261113-browser-data.sql")
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
	store := browserdata.Postgres{DB: db}
	doc, accepted, err := store.Exchange(context.Background(), owner, browserDataDocument())
	if err != nil || !accepted || doc.Revision != 1 {
		t.Fatal(doc, accepted, err)
	}
	fresh := browserdata.Postgres{DB: db}
	doc, accepted, err = fresh.Exchange(context.Background(), owner, browserDataDocument())
	if err != nil || accepted || len(doc.Entries) != 1 {
		t.Fatal(doc, accepted, err)
	}
}
