package lounge

import (
	"strings"
	"testing"

	"github.com/gorilla/websocket"
)

func TestQuoteTravelsWithLiveReplyWithoutOriginalOrReplay(t *testing.T) {
	h, url := fixture(t)
	alice := connect(t, h, url, "alice")
	bob := connect(t, h, url, "bob")
	quote := &replyQuote{ID: "earlier", SenderID: "other", Account: "other@example.test",
		Text: "an earlier message 😀", ImageCount: 1}
	write(t, alice, incoming{Type: "message", ID: "reply", Text: "my response", Reply: quote})
	for _, client := range []*websocket.Conn{alice, bob} {
		frame := read(t, client)
		value, ok := frame["reply"].(map[string]interface{})
		if !ok || value["id"] != quote.ID || value["text"] != quote.Text || value["imageCount"] != float64(1) {
			t.Fatal("quote not forwarded intact")
		}
	}
	late := connect(t, h, url, "late")
	write(t, late, incoming{Type: "ping"})
	if read(t, late)["type"] != "pong" {
		t.Fatal("quoted content was replayed")
	}
}

func TestQuoteLimitsAndImageOnlyQuotes(t *testing.T) {
	valid := replyQuote{ID: "old", SenderID: "bob", Account: "bob@example.test", ImageCount: 1}
	if validateReply(&valid) != nil || validateReply(nil) != nil {
		t.Fatal("valid quote rejected")
	}
	cases := []replyQuote{valid, valid, valid, valid, valid, valid, valid}
	cases[0].ID = ""
	cases[1].SenderID = strings.Repeat("x", 81)
	cases[2].Text = strings.Repeat("字", maxReplyText+1)
	cases[3].Account = strings.Repeat("x", 321)
	cases[4].ImageCount = 4
	cases[5].ImageCount = -1
	cases[6].ImageCount = 0
	for index, value := range cases {
		if validate(incoming{ID: "new", Text: "response", Reply: &value}) == nil {
			t.Fatalf("invalid quote %d accepted", index)
		}
	}
}
