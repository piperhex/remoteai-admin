// Package browserbookmarks owns account-scoped bookmark documents and optimistic concurrency.
package browserbookmarks

import (
	"errors"
	"net/url"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

const MaxEntries = 500
const maxRevision = 9007199254740990

var identifier = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,80}$`)
var ErrInvalid = errors.New("invalid bookmark document")

type Entry struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	URL      string `json:"url"`
	ParentID string `json:"parentId"`
	Kind     string `json:"kind"`
	Position int64  `json:"position"`
}

type Document struct {
	Revision int64   `json:"revision"`
	Entries  []Entry `json:"entries"`
}

func validEntry(entry Entry) bool {
	if !identifier.MatchString(entry.ID) || !utf8.ValidString(entry.Title) ||
		strings.TrimSpace(entry.Title) == "" || utf8.RuneCountInString(entry.Title) > 200 ||
		strings.IndexFunc(entry.Title, unicode.IsControl) >= 0 || entry.Position < 0 || entry.Position > maxRevision {
		return false
	}
	if entry.Kind == "folder" {
		return entry.URL == ""
	}
	if entry.Kind != "link" || len(entry.URL) > 4096 {
		return false
	}
	parsed, err := url.Parse(entry.URL)
	return err == nil && (parsed.Scheme == "http" || parsed.Scheme == "https") &&
		parsed.Hostname() != "" && parsed.User == nil
}

func Validate(doc Document) error {
	if doc.Revision < 0 || doc.Revision > maxRevision || doc.Entries == nil || len(doc.Entries) > MaxEntries {
		return ErrInvalid
	}
	entries := make(map[string]Entry, len(doc.Entries))
	for _, entry := range doc.Entries {
		if _, exists := entries[entry.ID]; exists || !validEntry(entry) {
			return ErrInvalid
		}
		entries[entry.ID] = entry
	}
	for _, entry := range doc.Entries {
		if !validParents(entry, entries) {
			return ErrInvalid
		}
	}
	return nil
}

func validParents(entry Entry, entries map[string]Entry) bool {
	seen := map[string]bool{entry.ID: true}
	for parent := entry.ParentID; parent != ""; {
		next, exists := entries[parent]
		if !exists || next.Kind != "folder" || seen[parent] || len(seen) > 8 {
			return false
		}
		seen[parent] = true
		parent = next.ParentID
	}
	return true
}
