package lounge

import (
	"encoding/json"
	"strings"
)

func accountPrefix(value string) string {
	prefix, _, _ := strings.Cut(value, "@")
	return prefix
}

// MarshalJSON is the common boundary for every public lounge account, including
// database-backed contacts and recipients that have not connected to the lounge.
func (value account) MarshalJSON() ([]byte, error) {
	type publicAccount account
	value.Account = accountPrefix(value.Account)
	return json.Marshal(publicAccount(value))
}

// Old clients may quote previously received full addresses. Only the account
// label is shortened; user-authored message text and nicknames remain intact.
func (value replyQuote) MarshalJSON() ([]byte, error) {
	type publicReply replyQuote
	value.Account = accountPrefix(value.Account)
	return json.Marshal(publicReply(value))
}
