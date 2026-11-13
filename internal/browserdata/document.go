// Package browserdata stores account history and client-encrypted password records.
package browserdata

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"
	"regexp"
	"unicode/utf8"
)

const MaxHistory = 3000
const MaxPasswords = 500
const maxRevision = 9007199254740990

var identifier = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,80}$`)
var ErrInvalid = errors.New("invalid browser data")

type Sealed struct {
	Nonce      string `json:"nonce"`
	Ciphertext string `json:"ciphertext"`
}
type Entry struct {
	ID     string  `json:"id"`
	Kind   string  `json:"kind"`
	URL    string  `json:"url,omitempty"`
	Title  string  `json:"title"`
	At     int64   `json:"at,omitempty"`
	Salt   string  `json:"salt,omitempty"`
	Check  *Sealed `json:"check,omitempty"`
	Secret *Sealed `json:"secret,omitempty"`
}
type Document struct {
	Revision int64   `json:"revision"`
	Entries  []Entry `json:"entries"`
}

func validHex(value string, length int) bool {
	if len(value) != length {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}
func validSealed(value *Sealed) bool {
	return value != nil && validHex(value.Nonce, 24) && len(value.Ciphertext) >= 32 &&
		len(value.Ciphertext) <= 40000 && validHex(value.Ciphertext, len(value.Ciphertext))
}
func validEntry(entry Entry) bool {
	if !identifier.MatchString(entry.ID) {
		return false
	}
	if entry.Kind == "vault" {
		return entry.ID == "vault" && validHex(entry.Salt, 32) && validSealed(entry.Check) &&
			entry.Secret == nil && entry.URL == "" && entry.Title == ""
	}
	if entry.ID == "vault" || entry.At < 0 || entry.At > maxRevision || entry.Check != nil {
		return false
	}
	if entry.Kind == "password" {
		return validHex(entry.Salt, 32) && validSealed(entry.Secret) && entry.URL == "" && entry.Title == ""
	}
	if entry.Kind != "visit" || len(entry.URL) > 8192 || utf8.RuneCountInString(entry.Title) > 512 ||
		!utf8.ValidString(entry.Title) || entry.Secret != nil || entry.Salt != "" {
		return false
	}
	parsed, err := url.Parse(entry.URL)
	return err == nil && (parsed.Scheme == "http" || parsed.Scheme == "https") &&
		parsed.Hostname() != "" && parsed.User == nil
}
func Validate(doc Document) error {
	if doc.Revision < 0 || doc.Revision > maxRevision || doc.Entries == nil ||
		len(doc.Entries) > MaxHistory+MaxPasswords+1 {
		return ErrInvalid
	}
	ids := map[string]bool{}
	salt := ""
	history, passwords := 0, 0
	for _, entry := range doc.Entries {
		if ids[entry.ID] || !validEntry(entry) {
			return ErrInvalid
		}
		ids[entry.ID] = true
		switch entry.Kind {
		case "vault":
			salt = entry.Salt
		case "visit":
			history++
		case "password":
			passwords++
		}
	}
	for _, entry := range doc.Entries {
		if entry.Kind == "password" && entry.Salt != salt {
			return ErrInvalid
		}
	}
	raw, err := json.Marshal(doc)
	if err != nil || len(raw) > 4*1024*1024 || history > MaxHistory || passwords > MaxPasswords {
		return ErrInvalid
	}
	return nil
}
