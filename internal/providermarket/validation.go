package providermarket

import (
	"encoding/json"
	"io"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/codex-switch/admin-go/internal/platform"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

const maxBodyBytes = 256 * 1024

var safeID = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,128}$`)
var modelID = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._:/-]{0,127}$`)
var publicPath = regexp.MustCompile(`^(/[a-zA-Z0-9._-]{1,32})*/?$`)

func validItemID(id string) bool {
	parsed, err := uuid.Parse(id)
	return err == nil && parsed.String() == id
}

func invalid() error  { return platform.NewError(400, "market.invalid") }
func conflict() error { return platform.NewError(409, "market.conflict") }
func denied() error   { return platform.NewError(403, "market.forbidden") }
func missing() error  { return platform.NewError(404, "market.missing") }

func decode(c *gin.Context, value interface{}) error {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxBodyBytes)
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	if decoder.Decode(value) != nil || decoder.Decode(new(interface{})) != io.EOF {
		return invalid()
	}
	return nil
}
func validText(value string, limit int) bool {
	return utf8.ValidString(value) && utf8.RuneCountInString(value) <= limit &&
		strings.IndexFunc(value, func(r rune) bool { return unicode.IsControl(r) && r != '\n' }) < 0
}
func validURL(value string) bool {
	u, err := url.Parse(value)
	return err == nil && len(value) <= 512 && (u.Scheme == "https" || u.Scheme == "http") &&
		u.Hostname() != "" && u.User == nil && u.RawQuery == "" && !u.ForceQuery && u.Fragment == "" &&
		u.RawPath == "" && publicPath.MatchString(u.Path) && !strings.Contains(u.Path, "..") &&
		!strings.ContainsAny(value, "\\\r\n\t ")
}
func validPrice(p price) bool {
	for _, value := range []*float64{p.Input, p.Output, p.CachedInput} {
		if value != nil && (math.IsNaN(*value) || math.IsInf(*value, 0) || *value < 0 || *value > 1e12) {
			return false
		}
	}
	return true
}
func validTier(t tier) bool {
	if math.IsNaN(t.Multiplier) || math.IsInf(t.Multiplier, 0) || t.Multiplier <= 0 || t.Multiplier > 1000 ||
		!validPrice(t.price) || !validPrice(t.LongContext) {
		return false
	}
	if t.LongContextThreshold == nil {
		return t.LongContext.Input == nil && t.LongContext.Output == nil && t.LongContext.CachedInput == nil
	}
	return *t.LongContextThreshold > 0 && *t.LongContextThreshold <= 1_000_000_000
}
func validModel(m model) bool {
	if !modelID.MatchString(m.ID) || len(m.Tiers) < 1 || len(m.Tiers) > 16 {
		return false
	}
	switch m.Protocol {
	case "openaiResponses", "openaiChat", "anthropicMessages", "geminiGenerateContent":
	default:
		return false
	}
	seen := map[float64]bool{}
	for _, t := range m.Tiers {
		if !validTier(t) || seen[t.Multiplier] {
			return false
		}
		seen[t.Multiplier] = true
	}
	return true
}
func validate(d draft) error {
	if !validBalanceQuery(d.BalanceQuery, d.BaseURL) || !validPageLink(d.PageLink) {
		return invalid()
	}
	if strings.TrimSpace(d.Name) == "" || !validText(d.Name, 80) || !validText(d.Description, 4000) ||
		!validURL(d.BaseURL) || d.Unit != "millionTokens" || len(d.Models) < 1 || len(d.Models) > 64 {
		return invalid()
	}
	switch d.Currency {
	case "USD", "CNY", "EUR", "JPY":
	default:
		return invalid()
	}
	seen := map[string]bool{}
	for _, m := range d.Models {
		if seen[m.ID] || !validModel(m) {
			return invalid()
		}
		seen[m.ID] = true
	}
	return nil
}

func validBalanceQuery(query *balanceQuery, base string) bool {
	if query == nil {
		return true
	}
	if !validURL(query.URL) || len(query.Script) == 0 || len(query.Script) > 16*1024 ||
		!utf8.ValidString(query.Script) {
		return false
	}
	baseURL, baseErr := url.Parse(base)
	target, targetErr := url.Parse(query.URL)
	if baseErr != nil || targetErr != nil || baseURL.Scheme != "https" ||
		target.Scheme != baseURL.Scheme || !strings.EqualFold(target.Host, baseURL.Host) {
		return false
	}
	return query.Auth == "bearer" || query.Auth == "x-api-key" || query.Auth == "none"
}
