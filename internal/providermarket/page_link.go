package providermarket

import (
	"bytes"
	"encoding/json"
	"net/url"
	"strings"

	"golang.org/x/net/http/httpguts"
)

const (
	maxPageBytes       = 16 * 1024
	maxPageURLBytes    = 4096
	maxPageHeaders     = 32
	maxPageHeaderBytes = 4096
)

// PageLink configures website visits only; it never changes upstream API requests.
type PageLink struct {
	URL     string            `json:"url"`
	Query   string            `json:"query"`
	Headers map[string]string `json:"headers"`
}

// MarketSource preserves provenance and the configuration reviewed when installing.
type MarketSource struct {
	ID   string   `json:"id"`
	Page PageLink `json:"page"`
}

func validPageLink(page *PageLink) bool {
	if page == nil {
		return true // Legacy entries remain readable; publication requires a page separately.
	}
	u, err := url.Parse(page.URL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil ||
		len(page.URL) > maxPageURLBytes || strings.ContainsAny(page.URL, "\\ \t\r\n") ||
		strings.ContainsAny(page.Query, "\r\n#") || page.Headers == nil || len(page.Headers) > maxPageHeaders {
		return false
	}
	encoded, err := json.Marshal(page)
	if err != nil || len(encoded) > maxPageBytes {
		return false
	}
	seen := map[string]bool{}
	for name, value := range page.Headers {
		lower := strings.ToLower(name)
		if seen[lower] || !httpguts.ValidHeaderFieldName(name) || reservedPageHeader(lower) || len(value) > maxPageHeaderBytes {
			return false
		}
		for _, c := range value {
			if c != '\t' && (c < 32 || c > 126) {
				return false
			}
		}
		seen[lower] = true
	}
	return true
}

func reservedPageHeader(name string) bool {
	if strings.HasPrefix(name, "sec-") {
		return true
	}
	switch name {
	case "host", "connection", "content-length", "transfer-encoding", "upgrade", "te", "trailer",
		"cookie", "set-cookie", "proxy-authorization", "proxy-connection", "keep-alive":
		return true
	}
	return false
}

// ValidateMarketSource checks the private sync DTO independently of public publication requests.
func ValidateMarketSource(value map[string]interface{}) error {
	if value == nil {
		return nil
	}
	data, err := json.Marshal(value)
	if err != nil {
		return invalid()
	}
	var source MarketSource
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&source) != nil || !validItemID(source.ID) || !validPageLink(&source.Page) {
		return invalid()
	}
	return nil
}
