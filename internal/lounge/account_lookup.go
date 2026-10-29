package lounge

import (
	"context"
	"encoding/json"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const maxAccountQueryLength = 320

type accountLookupRepository interface {
	FindAccounts(context.Context, string) ([]account, error)
}

// Match the whole local part or address; never use wildcard or substring matching.
// Reuse the silent database session so account queries are not written to logs.
func (p postgresRelationships) FindAccounts(ctx context.Context, query string) ([]account, error) {
	column := "lower(email)"
	if !strings.Contains(query, "@") {
		column = "lower(split_part(email, '@', 1))"
	}
	var users []account
	err := p.session(ctx).Raw(`SELECT id, email AS account FROM users
		WHERE disabled = false AND `+column+` = ? ORDER BY id LIMIT 2`, strings.ToLower(query)).Scan(&users).Error
	return users, err
}

func validAccountQuery(query string) bool {
	if query == "" || !utf8.ValidString(query) || utf8.RuneCountInString(query) > maxAccountQueryLength ||
		strings.IndexFunc(query, func(char rune) bool { return unicode.IsSpace(char) || unicode.IsControl(char) }) >= 0 {
		return false
	}
	local, domain, full := strings.Cut(query, "@")
	return !full || local != "" && domain != "" && !strings.Contains(domain, "@")
}

func (h *hub) lookupAccount(client *peer, input incoming) {
	query := strings.TrimSpace(input.Query)
	if !validAccountQuery(query) {
		h.reject(client, input.ID, "client.lounge.accountLookupInvalid")
		return
	}
	if h.accountLookup == nil || !client.direct {
		h.reject(client, input.ID, "client.lounge.accountLookupFailed")
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	users, err := h.accountLookup.FindAccounts(ctx, query)
	code := accountLookupError(users, client.user.ID)
	if err != nil {
		code = "client.lounge.accountLookupFailed"
	}
	if code != "" {
		h.reject(client, input.ID, code)
		return
	}
	if h.readNickname(&users[0]) != nil {
		h.reject(client, input.ID, "client.lounge.accountLookupFailed")
		return
	}
	data, err := json.Marshal(struct {
		Type string  `json:"type"`
		ID   string  `json:"id"`
		User account `json:"user"`
	}{"account-found", input.ID, users[0]})
	if err == nil {
		client.enqueue(data)
	}
}

func accountLookupError(users []account, self string) string {
	if len(users) == 0 {
		return "client.lounge.accountLookupNotFound"
	}
	if len(users) > 1 {
		return "client.lounge.accountLookupAmbiguous"
	}
	if users[0].ID == self {
		return "client.lounge.accountLookupSelf"
	}
	return ""
}
