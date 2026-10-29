package lounge

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

type memoryNames struct {
	nicknames map[string]string
	remarks   map[string]map[string]string
	fail      bool
}

func (m *memoryNames) Nicknames(_ context.Context, ids []string) ([]nicknameProfile, error) {
	result := []nicknameProfile{}
	for _, id := range ids {
		result = append(result, nicknameProfile{ID: id, Nickname: m.nicknames[id]})
	}
	return result, nil
}
func (m *memoryNames) Remarks(_ context.Context, owner string, ids []string) ([]contactRemark, error) {
	result := []contactRemark{}
	for _, id := range ids {
		result = append(result, contactRemark{ID: id, Remark: m.remarks[owner][id]})
	}
	return result, nil
}
func (m *memoryNames) SaveNickname(_ context.Context, value nicknameProfile) error {
	if m.fail {
		return errors.New("private database error")
	}
	m.nicknames[value.ID] = value.Nickname
	return nil
}
func (m *memoryNames) SaveRemark(_ context.Context, owner string, value contactRemark) error {
	if m.fail {
		return errors.New("private database error")
	}
	if m.remarks[owner] == nil {
		m.remarks[owner] = map[string]string{}
	}
	m.remarks[owner][value.ID] = value.Remark
	return nil
}

func assertName(t *testing.T, conn *websocket.Conn, field, expected string) {
	t.Helper()
	frame := read(t, conn)
	values, ok := frame[field].([]interface{})
	if frame["type"] != "names" || !ok || len(values) != 1 {
		t.Fatal("missing name update", frame)
	}
	key := "nickname"
	if field == "remarks" {
		key = "remark"
	}
	if values[0].(map[string]interface{})[key] != expected {
		t.Fatal("wrong name update", frame)
	}
}

func TestLoungeNamesOwnershipAndLiveSync(t *testing.T) {
	h, url := fixture(t)
	h.names = &memoryNames{nicknames: map[string]string{}, remarks: map[string]map[string]string{}}
	h.profiles = &memoryProfiles{avatars: map[string]string{}}
	a, b := uuid.NewString(), uuid.NewString()
	alice, second, bob := connect(t, h, url, a), connect(t, h, url, a), connect(t, h, url, b)
	write(t, bob, incoming{Type: "profile-watch", Watch: []string{a}})
	read(t, bob)
	read(t, bob)
	write(t, alice, incoming{Type: "nickname", ID: "nickname", UserID: b, Nickname: " 小明 "})
	for _, conn := range []*websocket.Conn{alice, second, bob} {
		assertName(t, conn, "profiles", "小明")
	}
	if read(t, alice)["type"] != "updated" {
		t.Fatal("save not acknowledged")
	}
	write(t, alice, incoming{Type: "remark", ID: "remark", UserID: b, Remark: "同事小李"})
	for _, conn := range []*websocket.Conn{alice, second} {
		assertName(t, conn, "remarks", "同事小李")
	}
	read(t, alice)
	write(t, bob, incoming{Type: "ping"})
	if read(t, bob)["type"] != "pong" {
		t.Fatal("private remark leaked to contact")
	}
	write(t, second, incoming{Type: "message", ID: "group", Text: "hello", Nickname: "spoofed", Remark: "secret"})
	for _, conn := range []*websocket.Conn{alice, second, bob} {
		frame := read(t, conn)
		sender := frame["sender"].(map[string]interface{})
		if sender["nickname"] != "小明" || sender["remark"] != nil {
			t.Fatal("message identity was not server-owned")
		}
	}
	write(t, alice, incoming{Type: "remark", ID: "clear", UserID: b})
	for _, conn := range []*websocket.Conn{alice, second} {
		assertName(t, conn, "remarks", "")
	}
	read(t, alice)
	write(t, second, incoming{Type: "nickname", ID: "clear-nickname"})
	for _, conn := range []*websocket.Conn{alice, second, bob} {
		assertName(t, conn, "profiles", "")
	}
	read(t, second)
}

func TestLoungeNamesValidationAndFailedWrites(t *testing.T) {
	for _, value := range []string{"小明", "", strings.Repeat("😀", maxNicknameLength)} {
		if !validName(value, maxNicknameLength) {
			t.Fatal("valid name rejected")
		}
	}
	for _, value := range []string{"line\nbreak", "line\u2028break", "\x00", strings.Repeat("字", maxNicknameLength+1)} {
		if validName(value, maxNicknameLength) {
			t.Fatal("invalid name accepted")
		}
	}
	h, url := fixture(t)
	h.names = &memoryNames{fail: true}
	id := uuid.NewString()
	client := connect(t, h, url, id)
	for _, input := range []incoming{
		{Type: "nickname", ID: "save", Nickname: "小明"},
		{Type: "remark", ID: "save", UserID: uuid.NewString(), Remark: "colleague"},
		{Type: "remark", ID: "save", UserID: id, Remark: "self"},
		{Type: "remark", ID: "save", UserID: "invalid", Remark: "bad target"},
	} {
		write(t, client, input)
		frame := read(t, client)
		if frame["type"] != "rejected" || frame["code"] != "client.lounge."+input.Type+"SaveFailed" {
			t.Fatal("write failure did not use safe error", frame)
		}
	}
	unauthorized := connect(t, h, url, "")
	write(t, unauthorized, incoming{Type: "nickname", ID: "unauthorized", Nickname: "x"})
	unauthorized.SetReadDeadline(time.Now().Add(time.Second))
	if _, _, err := unauthorized.ReadMessage(); err == nil {
		t.Fatal("unauthorized name write accepted")
	}
}
