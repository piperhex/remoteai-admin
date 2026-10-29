package lounge

import (
	"context"
	"errors"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

type memoryNotifications struct {
	values map[string]map[string]bool
	fail   bool
}

func (m *memoryNotifications) Muted(_ context.Context, owner string) ([]string, error) {
	values := []string{}
	for conversation, muted := range m.values[owner] {
		if muted {
			values = append(values, conversation)
		}
	}
	sort.Strings(values)
	return values, nil
}

func (m *memoryNotifications) Save(_ context.Context, owner, conversation string, muted bool) error {
	if m.fail {
		return errors.New("private storage failure")
	}
	if m.values[owner] == nil {
		m.values[owner] = map[string]bool{}
	}
	m.values[owner][conversation] = muted
	return nil
}

func TestNotificationSettingsSyncAndIsolation(t *testing.T) {
	h, url := fixture(t)
	h.notifications = &memoryNotifications{values: map[string]map[string]bool{}}
	a, b := uuid.NewString(), uuid.NewString()
	first, second, other := connect(t, h, url, a), connect(t, h, url, a), connect(t, h, url, b)
	for _, conversation := range []string{"", b} {
		for _, muted := range []bool{true, false, true} {
			write(t, first, incoming{Type: "notification-setting", ID: "save", Conversation: &conversation, Muted: &muted})
			for _, client := range []*websocket.Conn{first, second} {
				frame := read(t, client)
				if frame["type"] != "notification-settings" || frame["conversation"] != conversation || frame["muted"] != muted {
					t.Fatal("same-account update missing", frame)
				}
			}
			if read(t, first)["type"] != "updated" {
				t.Fatal("save was not acknowledged")
			}
		}
	}
	write(t, other, incoming{Type: "ping"})
	if read(t, other)["type"] != "pong" {
		t.Fatal("private notification settings leaked to another account")
	}
	assertNotificationReady(t, h, url, a, []string{"", b})
	assertNotificationReady(t, h, url, b, []string{})
	// Muting never prevents delivery of the messages needed by list badges and history.
	write(t, other, incoming{Type: "message", ID: "message", Text: "still delivered"})
	if read(t, first)["type"] != "message" || read(t, second)["type"] != "message" {
		t.Fatal("muting blocked group delivery")
	}
}

func assertNotificationReady(t *testing.T, h *hub, url, owner string, expected []string) {
	t.Helper()
	client := connect(t, h, url, "")
	write(t, client, incoming{Type: "authenticate", Ticket: credential(t, h, owner, time.Now().Add(time.Minute))})
	frame := read(t, client)
	values := []string{}
	for _, value := range frame["mutedConversations"].([]interface{}) {
		values = append(values, value.(string))
	}
	if frame["notificationSettings"] != true || !reflect.DeepEqual(values, expected) {
		t.Fatal("new connection did not restore owner settings", frame)
	}
}

func TestNotificationSettingsValidationAndFailures(t *testing.T) {
	h, url := fixture(t)
	h.notifications = &memoryNotifications{fail: true}
	owner, invalid, group, muted := uuid.NewString(), "invalid", "", true
	client := connect(t, h, url, owner)
	for _, conversation := range []*string{nil, &invalid, &owner, &group} {
		write(t, client, incoming{Type: "notification-setting", ID: "save", Conversation: conversation, Muted: &muted})
		frame := read(t, client)
		if frame["type"] != "rejected" || frame["code"] != "client.lounge.notificationSaveFailed" {
			t.Fatal("invalid/failed save was not rejected safely", frame)
		}
	}
	write(t, client, incoming{Type: "notification-setting", ID: "missing-bool", Conversation: &group})
	if read(t, client)["type"] != "rejected" {
		t.Fatal("missing muted value accepted")
	}
	unauthorized := connect(t, h, url, "")
	write(t, unauthorized, incoming{Type: "notification-setting", ID: "unauthorized", Conversation: &group, Muted: &muted})
	unauthorized.SetReadDeadline(time.Now().Add(time.Second))
	if _, _, err := unauthorized.ReadMessage(); err == nil {
		t.Fatal("unauthenticated save accepted")
	}
}

func TestNotificationSettingsPostgresRestart(t *testing.T) {
	db := concurrentPolicyDB(t)
	embedded, err := os.ReadFile("../migrations/025_lounge_notifications.sql")
	if err != nil {
		t.Fatal(err)
	}
	upgrade, err := os.ReadFile("../../sql/20261029-lounge-notifications.sql")
	if err != nil {
		t.Fatal(err)
	}
	schema := strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(string(upgrade), "BEGIN;", ""), "COMMIT;", ""))
	if schema != strings.TrimSpace(string(embedded)) {
		t.Fatal("upgrade differs from bootstrap")
	}
	for range 2 {
		if err := db.Exec(schema).Error; err != nil {
			t.Fatal(err)
		}
	}
	a, b := policyUser(t, db), policyUser(t, db)
	store, ctx := postgresNotifications{db: db}, context.Background()
	for _, conversation := range []string{"", b} {
		if err := store.Save(ctx, a, conversation, true); err != nil {
			t.Fatal(err)
		}
	}
	if store.Save(ctx, a, uuid.NewString(), true) == nil || store.Save(ctx, a, a, true) == nil {
		t.Fatal("invalid contact accepted")
	}
	h, url := fixture(t)
	h.notifications = postgresNotifications{db: db}
	assertNotificationReady(t, h, url, a, []string{"", b})
	assertNotificationReady(t, h, url, b, []string{})
	if err := store.Save(ctx, a, "", false); err != nil {
		t.Fatal(err)
	}
	fresh, freshURL := fixture(t)
	fresh.notifications = postgresNotifications{db: db}
	assertNotificationReady(t, fresh, freshURL, a, []string{b})
}
