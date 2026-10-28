package lounge

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/png"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/codex-switch/admin-go/internal/platform"
	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/gorilla/websocket"
)

func fixture(t *testing.T) (*hub, string) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	router := gin.New()
	h := newHub([]byte("test-only-ephemeral-signing-key"))
	router.GET("/lounge/socket", h.serve)
	router.POST("/lounge/ticket", func(c *gin.Context) {
		if c.GetHeader("Authorization") != "Bearer account-test-token" {
			c.Status(401)
			return
		}
		c.Set("principal", &platform.Principal{ID: "one", Email: "alice@example.test"})
		h.ticket(c)
	})
	server := httptest.NewServer(router)
	t.Cleanup(func() { h.close(); server.Close() })
	return h, "ws" + strings.TrimPrefix(server.URL, "http") + "/lounge/socket"
}

func credential(t *testing.T, h *hub, user string, expires time.Time) string {
	t.Helper()
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims{Account: user + "@example.test",
		RegisteredClaims: jwt.RegisteredClaims{Subject: user, Audience: []string{"lounge"},
			ExpiresAt: jwt.NewNumericDate(expires)},
	}).SignedString(h.key)
	if err != nil {
		t.Fatal(err)
	}
	return token
}

func connect(t *testing.T, h *hub, url, user string) *websocket.Conn {
	t.Helper()
	client, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { client.Close() })
	if user != "" {
		write(t, client, incoming{Type: "authenticate", Ticket: credential(t, h, user, time.Now().Add(time.Minute))})
		if frame := read(t, client); frame["type"] != "ready" {
			t.Fatalf("handshake: %v", frame)
		}
	}
	return client
}

func write(t *testing.T, client *websocket.Conn, frame interface{}) {
	t.Helper()
	if err := client.WriteJSON(frame); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, client *websocket.Conn) map[string]interface{} {
	t.Helper()
	client.SetReadDeadline(time.Now().Add(2 * time.Second))
	for {
		var frame map[string]interface{}
		if err := client.ReadJSON(&frame); err != nil {
			t.Fatal(err)
		}
		if frame["type"] != "presence" {
			return frame
		}
	}
}

func picture(t *testing.T) string {
	t.Helper()
	var buffer bytes.Buffer
	if err := png.Encode(&buffer, image.NewNRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(buffer.Bytes())
}

func TestLoungeBroadcastMixedMessageAndNoReplay(t *testing.T) {
	h, url := fixture(t)
	alice := connect(t, h, url, "alice")
	bob := connect(t, h, url, "bob")
	write(t, alice, map[string]interface{}{"type": "message", "id": "mixed", "text": "hello 😀", "images": []string{picture(t)},
		"sender": account{ID: "forged", Account: "forged"}})
	for _, client := range []*websocket.Conn{alice, bob} {
		frame := read(t, client)
		if frame["text"] != "hello 😀" || frame["id"] != "mixed" {
			t.Fatalf("message: %v", frame)
		}
		if frame["sender"].(map[string]interface{})["id"] != "alice" {
			t.Fatal("sender was not authenticated")
		}
		if len(frame["images"].([]interface{})) != 1 {
			t.Fatal("mixed image missing")
		}
	}
	late := connect(t, h, url, "late")
	write(t, late, incoming{Type: "ping"})
	if read(t, late)["type"] != "pong" {
		t.Fatal("history was replayed")
	}
	write(t, alice, incoming{Type: "message", ID: "new", Text: "new"})
	if read(t, late)["id"] != "new" {
		t.Fatal("live message missing")
	}
}

func TestLoungeUnauthenticatedExpiredAndForeignTicketsCannotJoin(t *testing.T) {
	for _, mode := range []string{"none", "expired", "foreign"} {
		t.Run(mode, func(t *testing.T) {
			h, url := fixture(t)
			client := connect(t, h, url, "")
			frame := incoming{Type: "message", ID: "rejected", Text: "must not broadcast"}
			if mode == "expired" {
				frame = incoming{Type: "authenticate", Ticket: credential(t, h, "x", time.Now().Add(-time.Minute))}
			}
			if mode == "foreign" {
				frame = incoming{Type: "authenticate", Ticket: credential(t, newHub([]byte("other-key")), "x", time.Now().Add(time.Minute))}
			}
			write(t, client, frame)
			client.SetReadDeadline(time.Now().Add(time.Second))
			if _, _, err := client.ReadMessage(); err == nil {
				t.Fatal("unauthorized socket received a frame")
			}
		})
	}
}

func TestLoungeRenewalKeepsConnectionAndRejectsAccountSwitch(t *testing.T) {
	h, url := fixture(t)
	client := connect(t, h, url, "alice")
	write(t, client, incoming{Type: "authenticate", Ticket: credential(t, h, "alice", time.Now().Add(2*time.Minute))})
	if read(t, client)["type"] != "ready" {
		t.Fatal("renewal failed")
	}
	write(t, client, incoming{Type: "ping"})
	if read(t, client)["type"] != "pong" {
		t.Fatal("connection lost on renewal")
	}
	write(t, client, incoming{Type: "authenticate", Ticket: credential(t, h, "bob", time.Now().Add(time.Minute))})
	client.SetReadDeadline(time.Now().Add(time.Second))
	if _, _, err := client.ReadMessage(); err == nil {
		t.Fatal("identity switch accepted")
	}
}

func TestLoungeRejectsOversizedAndDisguisedImages(t *testing.T) {
	good := incoming{Type: "message", ID: "image", Images: []string{picture(t)}}
	if err := validate(good); err != nil {
		t.Fatal(err)
	}
	for _, data := range []string{
		"data:image/svg+xml;base64," + base64.StdEncoding.EncodeToString([]byte("<svg/>")),
		"data:image/png;base64," + base64.StdEncoding.EncodeToString([]byte("not an image")),
		"data:image/png;base64," + base64.StdEncoding.EncodeToString(make([]byte, maxImageBytes+1)),
		strings.Replace(picture(t), "image/png", "image/jpeg", 1),
	} {
		if validate(incoming{ID: "x", Images: []string{data}}) == nil {
			t.Fatal("invalid image accepted")
		}
	}
	if validate(incoming{ID: "x", Text: strings.Repeat("字", maxTextLength+1)}) == nil {
		t.Fatal("text limit bypassed")
	}
}

func TestLoungeTicketRequiresLoginAndIsNotCacheable(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	stop, err := Register(router, &platform.Dependencies{Authenticate: func(c *gin.Context) (*platform.Principal, error) {
		if c.GetHeader("Authorization") == "Bearer valid" {
			return &platform.Principal{ID: "id", Email: "mail"}, nil
		}
		return nil, platform.NewError(401, "Unauthorized")
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	for _, loggedIn := range []bool{false, true} {
		request := httptest.NewRequest("POST", "/lounge/ticket", nil)
		if loggedIn {
			request.Header.Set("Authorization", "Bearer valid")
		}
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		if !loggedIn && response.Code != 401 {
			t.Fatal("ticket issued without login")
		}
		if loggedIn {
			var body map[string]string
			if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &body) != nil || body["ticket"] == "" ||
				response.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("invalid ticket response")
			}
		}
	}
}
