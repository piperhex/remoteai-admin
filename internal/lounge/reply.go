package lounge

import (
	"strings"
	"unicode/utf8"
)

const maxReplyText = 280

// A client-provided excerpt travels with the reply; the server never keeps or retrieves an original message.
type replyQuote struct {
	ID         string `json:"id"`
	SenderID   string `json:"senderId"`
	Account    string `json:"account"`
	Text       string `json:"text"`
	ImageCount int    `json:"imageCount"`
}

func validateReply(reply *replyQuote) error {
	if reply == nil {
		return nil
	}
	if len(reply.ID) == 0 || len(reply.ID) > 80 || len(reply.SenderID) == 0 || len(reply.SenderID) > 80 ||
		strings.TrimSpace(reply.Account) == "" || utf8.RuneCountInString(reply.Account) > 320 ||
		utf8.RuneCountInString(reply.Text) > maxReplyText || reply.ImageCount < 0 || reply.ImageCount > maxImages ||
		(strings.TrimSpace(reply.Text) == "" && reply.ImageCount == 0) {
		return errInvalid
	}
	return nil
}
